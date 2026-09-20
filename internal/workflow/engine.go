// Package workflow reconciles the portable replica lifecycle using durable
// intent records. The host is read-only except for the granted lab and state.
package workflow

import (
	"context"
	"errors"
	"reflect"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const Finalizer = "replica.nimeshbuilds.dev/runtime-cleanup"
const RefreshAnnotation = "replicove.nimeshbuilds.dev/refresh"
const poll = 10 * time.Second

type Capturer interface {
	Capture(context.Context, *api.ClusterReplica, *api.ReplicaGrant, policy.Resolution) (*state.Plan, error)
}
type Engine struct {
	Client            client.Client
	Store             *state.Store
	Reader            Capturer
	Runtime           runtimeprovider.Provider
	Now               func() time.Time
	MirrorCleanup     func(context.Context, *state.State, bool) (bool, error)
	MirrorPreparation func(context.Context, *api.ClusterReplica, *state.State, *target.Connection) (bool, error)
}

func (w *Engine) Reconcile(ctx context.Context, obj *api.ClusterReplica) (ctrl.Result, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}

	st, err := w.Store.Load(ctx, string(obj.UID))
	if err != nil {
		return w.report(ctx, obj, "Blocked", err, false)
	}
	if obj.Status.Phase == "Expired" && obj.DeletionTimestamp.IsZero() && st == nil {
		return ctrl.Result{}, nil
	}
	ttl, err := catalog.Validate(api.ClusterReplicaSpec{Profile: obj.Spec.Profile, TTL: obj.Spec.TTL, CleanupPolicy: "HelmReleaseOnly"})
	if err != nil {
		return w.report(ctx, obj, "Rejected", failure("InvalidSpec", err.Error()), false)
	}
	expires := obj.CreationTimestamp.Add(ttl)
	if !obj.DeletionTimestamp.IsZero() || !now.Before(expires) {
		return w.cleanup(ctx, obj, st)
	}
	// Generation plans are prepared by the mirror controller, never captured by
	// racing child reconciliation. A user annotation cannot supply protected state.
	if obj.Annotations["replicove.nimeshbuilds.dev/mirror-run"] != "" && st == nil && obj.DeletionTimestamp.IsZero() {
		return w.report(ctx, obj, "Preparing", failure("MirrorPreparationPending", "Waiting for the mirror controller's protected generation plan."), false)
	}
	grant := &api.ReplicaGrant{}
	if err := w.Client.Get(ctx, client.ObjectKey{Name: obj.Spec.GrantRef}, grant); err != nil {
		return w.report(ctx, obj, "Blocked", failure("GrantUnavailable", "The administrator grant is absent or unreadable."), false)
	}
	scope, err := policy.Resolve(grant, obj, w.Store.Namespace)
	if err != nil {
		return w.report(ctx, obj, "Rejected", err, false)
	}
	if scope.Provider == "platform" {
		return w.report(ctx, obj, "Blocked", failure("PlatformQualificationRequired", "This grant selects Platform; a qualified Platform adapter is required. Helm fallback is disabled."), false)
	}
	if st != nil && (st.GrantUID != string(grant.UID) || st.GrantVersion != grant.ResourceVersion) {
		return w.report(ctx, obj, "Blocked", failure("GrantChanged", "The administrator grant changed after capture; delete and recreate this request under the new grant."), false)
	}
	if !controllerutil.ContainsFinalizer(obj, Finalizer) {
		before := obj.DeepCopy()
		controllerutil.AddFinalizer(obj, Finalizer)
		return ctrl.Result{RequeueAfter: time.Millisecond}, w.Client.Patch(ctx, obj, client.MergeFrom(before))
	}
	if st == nil {
		if obj.Status.Runtime != nil {
			return w.report(ctx, obj, "Blocked", state.ErrUnavailable, false)
		}
		st = &state.State{OwnerUID: string(obj.UID), OwnerNamespace: obj.Namespace, OwnerName: obj.Name, GrantUID: string(grant.UID), GrantVersion: grant.ResourceVersion, Provider: scope.Provider}
		if scope.Existing != nil {
			st.TargetSecretNamespace = scope.Existing.KubeconfigSecret.Namespace
			st.TargetSecretName = scope.Existing.KubeconfigSecret.Name
			st.TargetClusterUID = scope.Existing.ClusterUID
		} else {
			st.TargetSecretNamespace = obj.Namespace
			st.TargetSecretName = "vc-" + catalog.Resolve(st.OwnerUID).ReleaseName
		}
		if err := w.Store.Save(ctx, st); err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
	}
	if st.Plan == nil || obj.Annotations[RefreshAnnotation] != st.RefreshToken {
		if st.MirrorRunUID != "" {
			return w.report(ctx, obj, "Blocked", failure("MirrorPlanPinned", "Use a mirror sync or reset request to replace a generation."), false)
		}
		if st.Provider == "existing" {
			conn, err := target.Connect(ctx, w.Client, st, "")
			if err != nil {
				return w.report(ctx, obj, "Connecting", err, false)
			}
			scope.GuestVersion = conn.Version
		}
		plan, err := w.Reader.Capture(ctx, obj, grant, scope)
		if err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
		plan.Revision, err = w.Store.Revision(ctx, plan)
		if err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
		st.Plan = plan
		st.RefreshToken = obj.Annotations[RefreshAnnotation]
		bounded := *w.Store
		bounded.MaxBytes = scope.MaxBytes
		if err := bounded.Save(ctx, st); err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
	}
	before := obj.DeepCopy()
	obj.Status.ExpiresAt = &metav1.Time{Time: expires}
	obj.Status.SourceVersion = st.Plan.SourceVersion
	if st.Provider == "helm" {
		resolved := catalog.ResolveProfile(st.OwnerUID, obj.Spec.Profile)
		if obj.Status.Runtime != nil && *obj.Status.Runtime != resolved {
			return w.report(ctx, obj, "Blocked", failure("ProfileMismatch", "Runtime identity no longer matches the pinned profile."), false)
		}
		obj.Status.Runtime = &resolved
	}
	obj.Status.Plan = &api.PlanSummary{Revision: st.Plan.Revision, CapturedAt: metav1.NewTime(st.Plan.CapturedAt), ObjectCount: int32(len(st.Plan.Objects)), PackageCount: int32(len(st.Plan.Packages)), Message: "Encrypted source capture; Helm charts are reconstructed as inventoried resources."}
	if st.AppliedRevision == st.Plan.Revision {
		obj.Status.Plan.AppliedCount = int32(len(st.Plan.Objects))
	}
	for _, item := range st.Plan.Objects {
		obj.Status.Plan.Resources = append(obj.Status.Plan.Resources, api.PlannedResource{ObjectReference: api.ObjectReference{APIVersion: item.APIVersion, Kind: item.Kind, Namespace: item.Namespace, Name: item.Name}, SourceNamespace: item.SourceNamespace, Dependencies: item.Dependencies})
	}
	if !reflect.DeepEqual(before.Status, obj.Status) {
		if err := w.Client.Status().Patch(ctx, obj, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if obj.Spec.Approval == "Manual" && obj.Annotations["replicove.nimeshbuilds.dev/approved-plan"] != st.Plan.Revision {
		return w.report(ctx, obj, "AwaitingApproval", nil, false)
	}

	if st.Provider == "helm" {
		observed, err := w.Runtime.Ensure(ctx, runtimeprovider.Request{Namespace: obj.Namespace, OwnerUID: st.OwnerUID, Reference: *obj.Status.Runtime})
		if err != nil {
			return w.report(ctx, obj, "Blocked", failure("RuntimeOperationFailed", "The pinned vCluster runtime could not be provisioned; inspect its Helm release and namespace permissions."), false)
		}
		if !observed.Ready {
			return w.report(ctx, obj, "Provisioning", nil, false)
		}
		svc := &corev1.Service{}
		if err := w.Client.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: obj.Status.Runtime.ReleaseName}, svc); err != nil {
			return w.report(ctx, obj, "Provisioning", target.ErrUnavailable, false)
		}
		workload := &unstructured.Unstructured{}
		workload.SetAPIVersion("apps/v1")
		workload.SetKind("Deployment")
		if obj.Spec.Profile == catalog.PersistentProfile {
			workload.SetKind("StatefulSet")
		}
		if err := w.Client.Get(ctx, client.ObjectKey{Namespace: obj.Namespace, Name: obj.Status.Runtime.ReleaseName}, workload); err != nil {
			return w.report(ctx, obj, "Provisioning", target.ErrUnavailable, false)
		}
		if st.RuntimeWorkloadUID == "" {
			st.RuntimeWorkloadUID = string(workload.GetUID())
			if err := w.Store.Save(ctx, st); err != nil {
				return w.report(ctx, obj, "Blocked", err, false)
			}
		} else if st.RuntimeWorkloadUID != string(workload.GetUID()) {
			return w.report(ctx, obj, "Blocked", target.ErrUnsafe, false)
		}

		if st.RuntimeRootUID == "" {
			st.RuntimeRootUID = string(svc.UID)
			if err := w.Store.Save(ctx, st); err != nil {
				return w.report(ctx, obj, "Blocked", err, false)
			}
		} else if st.RuntimeRootUID != string(svc.UID) {
			return w.report(ctx, obj, "Blocked", target.ErrUnsafe, false)
		}
	}
	release := ""
	if obj.Status.Runtime != nil {
		release = obj.Status.Runtime.ReleaseName
	}
	conn, err := target.Connect(ctx, w.Client, st, release)
	if err != nil {
		return w.report(ctx, obj, "Connecting", err, false)
	}
	if st.TargetClusterUID == "" {
		st.TargetClusterUID = conn.UID
		if err := w.Store.Save(ctx, st); err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
	}
	if obj.Status.TargetVersion != conn.Version {
		before := obj.DeepCopy()
		obj.Status.TargetVersion = conn.Version
		if err := w.Client.Status().Patch(ctx, obj, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := w.namespaces(ctx, conn, st); err != nil {
		return w.report(ctx, obj, "Blocked", err, false)
	}
	if st.MirrorRunUID != "" {
		if w.MirrorPreparation == nil {
			return w.report(ctx, obj, "Blocked", failure("MirrorsDisabled", "Enable the mirror module to restore this generation."), false)
		}
		ready, err := w.MirrorPreparation(ctx, obj, st, conn)
		if err != nil || !ready {
			return w.report(ctx, obj, "Restoring", err, false)
		}
	}
	refreshing := st.AppliedRevision != st.Plan.Revision
	byID := map[string]state.Object{}
	for _, item := range st.Plan.Objects {
		byID[item.ID] = item
	}
	// Prune removed owned objects before installing the new snapshot, in reverse
	// application order. Newly mapped namespaces remain until final cleanup.
	if refreshing {
		for i := len(st.Entries) - 1; i >= 0; i-- {
			e := &st.Entries[i]
			if e.Deleted || e.Kind == "Namespace" {
				continue
			}
			if _, present := byID[e.ID]; !present {
				done, err := w.deleteEntry(ctx, conn, st, e)
				if err != nil {
					return w.report(ctx, obj, "Blocked", err, false)
				}
				if !done {
					return w.report(ctx, obj, "Refreshing", nil, false)
				}
			}
		}
	}
	for _, id := range st.Plan.Order {
		desired := byID[id]
		for _, dep := range desired.Dependencies {
			d, ok := byID[dep]
			if !ok {
				continue
			}
			if d.Kind == "CustomResourceDefinition" || d.Kind == "Deployment" || d.Kind == "StatefulSet" {
				live, err := resource(conn, d.APIVersion, d.Resource, d.Namespace).Get(ctx, d.Name, metav1.GetOptions{})
				if err != nil || !Ready(live) {
					return w.report(ctx, obj, "Replicating", nil, false)
				}
			}
		}
		if _, err := w.apply(ctx, conn, st, desired, refreshing); err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
	}
	if obj.Spec.Replication != nil && obj.Spec.Replication.Secrets == "Follow" {
		if err := w.follow(ctx, obj, grant, conn, st); err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
	}
	drift := int32(0)
	ready := true
	for _, desired := range st.Plan.Objects {
		live, err := resource(conn, desired.APIVersion, desired.Resource, desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
		if err != nil {
			return w.report(ctx, obj, "Blocked", failure("GuestReadFailed", "Cannot verify a replicated object."), false)
		}
		if !Ready(live) {
			ready = false
		}
		if !ContainsDesired(desired.Desired, live.Object) {
			drift++
		}
		if obj.Spec.Replication != nil {
			for _, check := range obj.Spec.Replication.Checks {
				if check.APIVersion == desired.APIVersion && check.Kind == desired.Kind && check.Namespace == desired.SourceNamespace && check.Name == desired.SourceName && !Check(live, check.Condition, check.Field, check.Equals) {
					ready = false
				}
			}
		}
	}
	if st.AppliedRevision != st.Plan.Revision {
		st.AppliedRevision = st.Plan.Revision
		if err := w.Store.Save(ctx, st); err != nil {
			return w.report(ctx, obj, "Blocked", err, false)
		}
	}
	before = obj.DeepCopy()
	obj.Status.DriftCount = drift
	obj.Status.Plan.AppliedCount = int32(len(st.Plan.Objects))
	if !reflect.DeepEqual(before.Status, obj.Status) {
		if err := w.Client.Status().Patch(ctx, obj, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if !ready {
		return w.report(ctx, obj, "Verifying", nil, false)
	}
	return w.report(ctx, obj, "Ready", nil, true)
}

func (w *Engine) report(ctx context.Context, obj *api.ClusterReplica, phase string, err error, ready bool) (ctrl.Result, error) {
	reason, message := phase, "Waiting for the next replica lifecycle step."
	if ready {
		message = "The captured desired state is installed and configured readiness checks passed."
	}
	if phase == "Expired" {
		message = "TTL elapsed. Inventoried guest resources, managed runtime, and encrypted capture were removed."
	}
	if err != nil {
		reason = "OperationFailed"
		message = "The operation could not complete; inspect permissions and connectivity without exposing credentials."
		var p *planner.Problem
		var denied *policy.Denied
		var connection *target.ConnectionError
		switch {
		case errors.As(err, &p):
			reason, message = p.Reason, p.Detail
		case errors.As(err, &denied):
			reason, message = denied.Reason, denied.Detail
		case errors.Is(err, state.ErrTooLarge):
			reason, message = "CaptureLimit", "The encrypted capture exceeds the configured storage limit; narrow the selection."
		case errors.Is(err, state.ErrIntegrity):
			reason, message = "StateIntegrity", "The protected state failed authentication; recovery requires its original key and data."
		case errors.Is(err, state.ErrUnavailable):
			reason, message = "StateUnavailable", "The administrator-only state store or its encryption key is unavailable."
		case errors.Is(err, target.ErrUnsafe):
			reason, message = "TargetIdentityMismatch", "Guest credentials, ownership, or the pinned cluster identity failed validation."
		case errors.As(err, &connection):
			reason, message = "Target"+connection.Reason, connection.Error()
		case errors.Is(err, target.ErrUnavailable):
			reason, message = "TargetUnavailable", "The guest API or its administrator-provided credentials are not yet available."
		}
	}
	before := obj.DeepCopy()
	obj.Status.Phase = phase
	obj.Status.ObservedGeneration = obj.Generation
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{Type: "Ready", Status: status, Reason: reason, Message: message, ObservedGeneration: obj.Generation})
	if !reflect.DeepEqual(before.Status, obj.Status) {
		if err := w.Client.Status().Patch(ctx, obj, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if !reflect.DeepEqual(before.Status, obj.Status) {
		ctrl.LoggerFrom(ctx).Info("Replica condition", "phase", phase, "reason", reason, "message", message)
	}

	after := poll
	if phase == "Deleting" || phase == "Refreshing" {
		after = time.Second
	}
	if phase == "Expired" {
		after = 0
	}
	return ctrl.Result{RequeueAfter: after}, nil
}

func (w *Engine) cleanup(ctx context.Context, obj *api.ClusterReplica, st *state.State) (ctrl.Result, error) {
	if st == nil {
		if obj.Status.Runtime != nil && obj.Status.Phase != "Expired" && obj.Status.Phase != "Cleaned" {
			return w.report(ctx, obj, "Blocked", state.ErrUnavailable, false)
		}
		return w.finish(ctx, obj)
	}
	if !st.CleanupStarted {
		st.CleanupStarted = true
		if err := w.Store.Save(ctx, st); err != nil {
			return w.report(ctx, obj, "Deleting", err, false)
		}
	}
	accesses := &api.ReplicaAccessList{}
	if err := w.Client.List(ctx, accesses, client.InNamespace(obj.Namespace)); err != nil {
		return w.report(ctx, obj, "Deleting", failure("AccessRevocationFailed", "Cannot enumerate replica access requests."), false)
	}
	pending := false
	for i := range accesses.Items {
		a := &accesses.Items[i]
		if a.Spec.ReplicaUID == st.OwnerUID {
			pending = true
			if a.DeletionTimestamp.IsZero() {
				if err := w.Client.Delete(ctx, a); err != nil && !apierrors.IsNotFound(err) {
					return w.report(ctx, obj, "Deleting", failure("AccessRevocationFailed", "Cannot revoke an access request."), false)
				}
			}
		}
	}
	if pending {
		return w.report(ctx, obj, "Deleting", nil, false)
	}
	if st.Provider == "helm" {
		if err := w.inventoryHost(ctx, st); err != nil {
			return w.report(ctx, obj, "Deleting", err, false)
		}
	}
	if st.MirrorRunUID != "" && !st.GuestCleaned {
		if w.MirrorCleanup == nil {
			return w.report(ctx, obj, "Deleting", failure("MirrorsDisabled", "Enable the mirror module to clean up owned snapshots and restored volumes."), false)
		}
		if done, err := w.MirrorCleanup(ctx, st, false); err != nil || !done {
			return w.report(ctx, obj, "Deleting", err, false)
		}
	}
	if !st.GuestCleaned {
		remaining := false
		for _, e := range st.Entries {
			if !e.Deleted {
				remaining = true
			}
		}
		if remaining {
			release := ""
			if obj.Status.Runtime != nil {
				release = obj.Status.Runtime.ReleaseName
			}
			conn, err := target.Connect(ctx, w.Client, st, release)
			if err != nil {
				return w.report(ctx, obj, "Deleting", err, false)
			}
			for i := len(st.Entries) - 1; i >= 0; i-- {
				done, err := w.deleteEntry(ctx, conn, st, &st.Entries[i])
				if err != nil {
					return w.report(ctx, obj, "Deleting", err, false)
				}
				if !done {
					return w.report(ctx, obj, "Deleting", nil, false)
				}
			}
		}
		st.GuestCleaned = true
		if err := w.Store.Save(ctx, st); err != nil {
			return w.report(ctx, obj, "Deleting", err, false)
		}
	}
	if st.Provider == "helm" && obj.Status.Runtime != nil {
		if obj.Status.Runtime.ReleaseName != catalog.Resolve(st.OwnerUID).ReleaseName {
			return w.report(ctx, obj, "Blocked", target.ErrUnsafe, false)
		}
		if err := w.inventoryHost(ctx, st); err != nil {
			return w.report(ctx, obj, "Deleting", err, false)
		}
		if err := w.Runtime.Delete(ctx, runtimeprovider.Request{Namespace: obj.Namespace, OwnerUID: st.OwnerUID, Reference: *obj.Status.Runtime}); err != nil {
			return w.report(ctx, obj, "Deleting", failure("RuntimeCleanupPending", "Waiting for managed vCluster Helm resources to disappear."), false)
		}
		done, err := w.hostCleanup(ctx, st)
		if err != nil {
			return w.report(ctx, obj, "Deleting", err, false)
		}
		if !done {
			return w.report(ctx, obj, "Deleting", nil, false)
		}
	}
	if st.MirrorRunUID != "" {
		if w.MirrorCleanup == nil {
			return w.report(ctx, obj, "Deleting", failure("MirrorsDisabled", "Enable the mirror module to finish restored volume cleanup."), false)
		}
		if done, err := w.MirrorCleanup(ctx, st, true); err != nil || !done {
			return w.report(ctx, obj, "Deleting", err, false)
		}
	}
	terminal := "Expired"
	if !obj.DeletionTimestamp.IsZero() {
		terminal = "Cleaned"
	}
	if _, err := w.report(ctx, obj, terminal, nil, false); err != nil {
		return ctrl.Result{}, err
	}
	if err := w.Store.Delete(ctx, st.OwnerUID); err != nil {
		return w.report(ctx, obj, "Deleting", err, false)
	}
	return w.finish(ctx, obj)
}
func (w *Engine) finish(ctx context.Context, obj *api.ClusterReplica) (ctrl.Result, error) {
	if !obj.DeletionTimestamp.IsZero() {
		before := obj.DeepCopy()
		controllerutil.RemoveFinalizer(obj, Finalizer)
		return ctrl.Result{}, w.Client.Patch(ctx, obj, client.MergeFrom(before))
	}
	// Persist Expired before removing the private state so retries cannot re-create.
	return w.report(ctx, obj, "Expired", nil, false)
}
