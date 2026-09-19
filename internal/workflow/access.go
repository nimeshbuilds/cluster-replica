package workflow

import (
	"context"
	"reflect"
	"slices"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const AccessFinalizer = "replicove.nimeshbuilds.dev/access-revoke"

type AccessReconciler struct {
	Engine    *Engine
	Namespace string
}

func AccessName(uid string) string { return "ra-" + state.Name(uid)[16:] }
func (r *AccessReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&api.ReplicaAccess{}).Complete(r)
}
func (r *AccessReconciler) Reconcile(ctx context.Context, key ctrl.Request) (ctrl.Result, error) {
	if key.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}
	a := &api.ReplicaAccess{}
	if err := r.Engine.Client.Get(ctx, key.NamespacedName, a); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	st, err := r.Engine.Store.Load(ctx, string(a.UID))
	if err != nil {
		return r.report(ctx, a, "Blocked", "AccessStateUnavailable", "Protected access state is unavailable.")
	}
	terminal := a.Status.Phase == "Expired" || a.Status.Phase == "Revoked"
	if !a.DeletionTimestamp.IsZero() || terminal || st != nil && st.CleanupStarted || st != nil && !st.AccessExpiresAt.IsZero() && !time.Now().Before(st.AccessExpiresAt) {
		return r.revoke(ctx, a, st)
	}
	obj := &api.ClusterReplica{}
	if err := r.Engine.Client.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: a.Spec.ReplicaName}, obj); err != nil || string(obj.UID) != a.Spec.ReplicaUID {
		return r.revoke(ctx, a, st)
	}
	parent, err := r.Engine.Store.Load(ctx, a.Spec.ReplicaUID)
	if err != nil {
		return r.report(ctx, a, "Blocked", "ReplicaStateUnavailable", "Replica ownership state is unavailable.")
	}
	if parent == nil || parent.CleanupStarted || obj.Status.ExpiresAt == nil || !obj.DeletionTimestamp.IsZero() || !time.Now().Before(obj.Status.ExpiresAt.Time) {
		return r.revoke(ctx, a, st)
	}
	grant := &api.ReplicaGrant{}
	if err := r.Engine.Client.Get(ctx, client.ObjectKey{Name: obj.Spec.GrantRef}, grant); err != nil || string(grant.UID) != parent.GrantUID || grant.ResourceVersion != parent.GrantVersion || grant.Spec.TargetNamespace != a.Namespace || !slices.Contains(grant.Spec.AccessRoles, a.Spec.Role) {
		return r.revoke(ctx, a, st)
	}
	roles := map[string]string{"viewer": "view", "deployer": "edit", "admin": "cluster-admin"}
	role, ok := roles[a.Spec.Role]
	if !ok {
		return r.report(ctx, a, "Rejected", "UnknownRole", "Choose viewer, deployer, or admin within the administrator grant.")
	}
	if !controllerutil.ContainsFinalizer(a, AccessFinalizer) {
		before := a.DeepCopy()
		controllerutil.AddFinalizer(a, AccessFinalizer)
		return ctrl.Result{RequeueAfter: time.Millisecond}, r.Engine.Client.Patch(ctx, a, client.MergeFrom(before))
	}
	if st == nil {
		if obj.Status.Phase != "Ready" {
			return r.report(ctx, a, "Pending", "ReplicaNotReady", "Wait for the replica's readiness checks.")
		}
		seconds := a.Spec.DurationSeconds
		if seconds == 0 {
			seconds = 900
		}
		cap := grant.Spec.MaxAccessSeconds
		if cap == 0 {
			cap = 900
		}
		if seconds > cap || seconds < 600 {
			return r.report(ctx, a, "Rejected", "DurationNotGranted", "The requested credential duration is outside the grant.")
		}
		end := a.CreationTimestamp.Add(time.Duration(seconds) * time.Second)
		if obj.Status.ExpiresAt.Time.Before(end) {
			end = obj.Status.ExpiresAt.Time
		}
		if time.Until(end) < 600*time.Second {
			return r.report(ctx, a, "Rejected", "ReplicaExpiresSoon", "At least ten minutes must remain to issue a bounded Kubernetes token.")
		}
		st = &state.State{OwnerUID: string(a.UID), OwnerNamespace: a.Namespace, OwnerName: a.Name, Provider: parent.Provider, GrantUID: parent.GrantUID, GrantVersion: parent.GrantVersion, TargetClusterUID: parent.TargetClusterUID, TargetSecretNamespace: parent.TargetSecretNamespace, TargetSecretName: parent.TargetSecretName, RuntimeRootUID: parent.RuntimeRootUID, AccessExpiresAt: end, ReplicaUID: a.Spec.ReplicaUID}
		if obj.Status.Runtime != nil {
			st.RuntimeName = obj.Status.Runtime.ReleaseName
		}
		if err := r.Engine.Store.Save(ctx, st); err != nil {
			return r.report(ctx, a, "Blocked", "AccessStateUnavailable", "Cannot persist access intent.")
		}
	}
	conn, err := target.Connect(ctx, r.Engine.Client, st, st.RuntimeName)
	if err != nil {
		return r.report(ctx, a, "Pending", "TargetUnavailable", "The pinned guest API is unavailable.")
	}
	name := AccessName(string(a.UID))
	objects := []state.Object{
		{ID: planner.ID("", "ServiceAccount", "default", name), APIVersion: "v1", Kind: "ServiceAccount", Resource: "serviceaccounts", Namespace: "default", Name: name, Desired: map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": name, "namespace": "default"}, "automountServiceAccountToken": false}},
		{ID: planner.ID("rbac.authorization.k8s.io", "ClusterRoleBinding", "", name), APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Resource: "clusterrolebindings", Name: name, Desired: map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": map[string]any{"name": name}, "roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": role}, "subjects": []any{map[string]any{"kind": "ServiceAccount", "namespace": "default", "name": name}}}},
	}
	for _, desired := range objects {
		if _, err := r.Engine.apply(ctx, conn, st, desired, false); err != nil {
			return r.report(ctx, a, "Blocked", "AccessOwnershipConflict", "Cannot create or verify the guest access identity.")
		}
	}
	secret := &corev1.Secret{}
	err = r.Engine.Client.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: name}, secret)
	if apierrors.IsNotFound(err) {
		seconds := int64(time.Until(st.AccessExpiresAt).Seconds())
		if seconds < 600 {
			return r.revoke(ctx, a, st)
		}
		token, err := conn.Kubernetes.CoreV1().ServiceAccounts("default").CreateToken(ctx, name, &authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &seconds}}, metav1.CreateOptions{})
		if err != nil {
			return r.report(ctx, a, "Blocked", "TokenRequestFailed", "The guest did not issue a service-account token.")
		}
		if token.Status.Token == "" || token.Status.ExpirationTimestamp.Time.After(st.AccessExpiresAt.Add(2*time.Second)) {
			return r.revoke(ctx, a, st)
		}
		if token.Status.ExpirationTimestamp.Time.Before(st.AccessExpiresAt) {
			st.AccessExpiresAt = token.Status.ExpirationTimestamp.Time
			if err := r.Engine.Store.Save(ctx, st); err != nil {
				return r.report(ctx, a, "Blocked", "AccessStateUnavailable", "Cannot persist the server's token expiration.")
			}
		}
		config := clientcmdapi.Config{APIVersion: "v1", Kind: "Config", CurrentContext: "replicove", Clusters: map[string]*clientcmdapi.Cluster{"replicove": {Server: conn.Config.Host, CertificateAuthorityData: conn.Config.CAData, TLSServerName: conn.Config.ServerName}}, Contexts: map[string]*clientcmdapi.Context{"replicove": {Cluster: "replicove", AuthInfo: "replicove"}}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"replicove": {Token: token.Status.Token}}}
		data, err := clientcmd.Write(config)
		if err != nil {
			return r.report(ctx, a, "Blocked", "CredentialEncodingFailed", "Cannot encode guest credentials.")
		}
		immutable := true
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: a.Namespace, Name: name, OwnerReferences: []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "ReplicaAccess", Name: a.Name, UID: a.UID}}}, Immutable: &immutable, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"config": data}}
		if err := r.Engine.Client.Create(ctx, secret); err != nil {
			return r.report(ctx, a, "Blocked", "CredentialWriteFailed", "Cannot save the short-lived credential Secret.")
		}
	} else if err != nil {
		return r.report(ctx, a, "Blocked", "CredentialReadFailed", "Cannot inspect the credential Secret.")
	}
	if !target.OwnedBy(secret.OwnerReferences, string(a.UID)) || (st.CredentialSecretUID != "" && st.CredentialSecretUID != string(secret.UID)) {
		return r.report(ctx, a, "Blocked", "CredentialOwnershipConflict", "A foreign Secret occupies the access credential name.")
	}
	if st.CredentialSecretUID == "" {
		st.CredentialSecretUID = string(secret.UID)
		if err := r.Engine.Store.Save(ctx, st); err != nil {
			return r.report(ctx, a, "Blocked", "AccessStateUnavailable", "Cannot persist credential ownership.")
		}
	}
	before := a.DeepCopy()
	a.Status.CredentialSecret = name
	a.Status.ExpiresAt = &metav1.Time{Time: st.AccessExpiresAt}
	if err := r.Engine.Client.Status().Patch(ctx, a, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}
	return r.report(ctx, a, "Ready", "CredentialIssued", "A bounded guest credential is available in the referenced Secret.")
}
func (r *AccessReconciler) revoke(ctx context.Context, a *api.ReplicaAccess, st *state.State) (ctrl.Result, error) {
	if st != nil {
		if !st.CleanupStarted {
			st.CleanupStarted = true
			if err := r.Engine.Store.Save(ctx, st); err != nil {
				return r.report(ctx, a, "Revoking", "AccessStateUnavailable", "Cannot persist access revocation intent.")
			}
		}
		pending := false
		for _, e := range st.Entries {
			if !e.Deleted {
				pending = true
			}
		}
		if pending {
			conn, err := target.Connect(ctx, r.Engine.Client, st, st.RuntimeName)
			if err != nil {
				return r.report(ctx, a, "Revoking", "TargetUnavailable", "Guest access identities must be removed before cleanup can finish.")
			}
			for i := len(st.Entries) - 1; i >= 0; i-- {
				done, err := r.Engine.deleteEntry(ctx, conn, st, &st.Entries[i])
				if err != nil || !done {
					return r.report(ctx, a, "Revoking", "IdentityRevocationPending", "Waiting for guest access identities to be removed.")
				}
			}
		}
		secret := &corev1.Secret{}
		err := r.Engine.Client.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: AccessName(string(a.UID))}, secret)
		if err == nil {
			if !target.OwnedBy(secret.OwnerReferences, string(a.UID)) || (st.CredentialSecretUID != "" && st.CredentialSecretUID != string(secret.UID)) {
				return r.report(ctx, a, "Blocked", "CredentialOwnershipConflict", "A credential name was reused; it was preserved.")
			}
			if err := r.Engine.Client.Delete(ctx, secret, client.Preconditions{UID: &secret.UID, ResourceVersion: &secret.ResourceVersion}); err != nil && !apierrors.IsNotFound(err) {
				return r.report(ctx, a, "Revoking", "CredentialCleanupPending", "Cannot remove the expired credential Secret.")
			}
		} else if !apierrors.IsNotFound(err) {
			return r.report(ctx, a, "Revoking", "CredentialCleanupPending", "Cannot inspect the expired credential Secret.")
		}
		if _, err := r.report(ctx, a, "Revoked", "AccessRemoved", "The guest access identity and credential Secret were removed."); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Engine.Store.Delete(ctx, string(a.UID)); err != nil {
			return r.report(ctx, a, "Revoking", "AccessStateUnavailable", "Cannot remove protected access state.")
		}
	}
	if controllerutil.ContainsFinalizer(a, AccessFinalizer) {
		before := a.DeepCopy()
		controllerutil.RemoveFinalizer(a, AccessFinalizer)
		if err := r.Engine.Client.Patch(ctx, a, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if !a.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	return r.report(ctx, a, "Revoked", "AccessRemoved", "The access request is no longer active; create a new request to obtain access.")
}
func (r *AccessReconciler) report(ctx context.Context, a *api.ReplicaAccess, phase, reason, message string) (ctrl.Result, error) {
	before := a.DeepCopy()
	a.Status.Phase = phase
	status := metav1.ConditionFalse
	if phase == "Ready" {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&a.Status.Conditions, metav1.Condition{Type: "Ready", Status: status, Reason: reason, Message: message, ObservedGeneration: a.Generation})
	if !reflect.DeepEqual(before.Status, a.Status) {
		if err := r.Engine.Client.Status().Patch(ctx, a, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	after := poll
	if phase == "Revoking" {
		after = time.Second
	}
	if phase == "Revoked" {
		after = 0
	}
	return ctrl.Result{RequeueAfter: after}, nil
}
