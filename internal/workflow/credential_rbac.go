package workflow

import (
	"context"
	"reflect"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// credentialReaders gives only administrator-selected subjects access to the
// exact credential Secret. No wildcard Secret permissions or caller annotations.
func (r *AccessReconciler) credentialReaders(ctx context.Context, a *api.ReplicaAccess, grant *api.ReplicaGrant, st *state.State) error {
	if len(grant.Spec.AccessSubjects) == 0 {
		return nil
	}
	subjects := []any{}
	for _, subject := range grant.Spec.AccessSubjects {
		item := map[string]any{"kind": subject.Kind, "name": subject.Name}
		if subject.Name == "" {
			return failure("InvalidAccessSubject", "Credential reader names must be nonempty.")
		}
		switch subject.Kind {
		case "ServiceAccount":
			if len(validation.IsDNS1123Label(subject.Namespace)) > 0 || len(validation.IsDNS1123Subdomain(subject.Name)) > 0 {
				return failure("InvalidAccessSubject", "A credential ServiceAccount reader requires a valid name and namespace.")
			}
			item["namespace"] = subject.Namespace
		case "User", "Group":
			if subject.Namespace != "" {
				return failure("InvalidAccessSubject", "User and Group credential readers cannot have a namespace.")
			}
			item["apiGroup"] = "rbac.authorization.k8s.io"
		default:
			return failure("InvalidAccessSubject", "Unknown credential reader kind.")
		}
		subjects = append(subjects, item)
	}
	name := AccessName(string(a.UID))
	for _, kind := range []string{"Role", "RoleBinding"} {
		id := "credential:" + kind + ":" + name
		var e *state.Entry
		for i := range st.HostEntries {
			if st.HostEntries[i].ID == id {
				e = &st.HostEntries[i]
				break
			}
		}
		if e == nil {
			operation, err := state.OperationID()
			if err != nil {
				return err
			}
			st.HostEntries = append(st.HostEntries, state.Entry{ID: id, Type: "credential-rbac", APIVersion: "rbac.authorization.k8s.io/v1", Kind: kind, Namespace: a.Namespace, Name: name, OperationID: operation})
			if err := r.Engine.Store.Save(ctx, st); err != nil {
				return err
			}
			e = &st.HostEntries[len(st.HostEntries)-1]
		}
		wanted := &unstructured.Unstructured{Object: map[string]any{"apiVersion": e.APIVersion, "kind": kind, "metadata": map[string]any{"name": name, "namespace": a.Namespace}}}
		wanted.SetAnnotations(map[string]string{planner.OwnerAnnotation: st.OwnerUID, planner.OperationAnnotation: e.OperationID})
		wanted.SetLabels(map[string]string{planner.InfrastructureLabel: "true"})
		wanted.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "ReplicaAccess", Name: a.Name, UID: a.UID}})
		if kind == "Role" {
			wanted.Object["rules"] = []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"secrets"}, "resourceNames": []any{name}, "verbs": []any{"get"}}}
		} else {
			wanted.Object["roleRef"] = map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": name}
			wanted.Object["subjects"] = subjects
		}
		live := &unstructured.Unstructured{}
		live.SetAPIVersion(e.APIVersion)
		live.SetKind(kind)
		err := r.Engine.Client.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: name}, live)
		if apierrors.IsNotFound(err) {
			if e.UID != "" {
				return failure("CredentialRBACConflict", "An inventoried credential permission is missing; create a new access session.")
			}
			if err := r.Engine.Client.Create(ctx, wanted); err != nil {
				return failure("CredentialRBACDenied", "Cannot create the exact-Secret credential permission.")
			}
			live = wanted
		} else if err != nil {
			return failure("CredentialRBACDenied", "Cannot inspect the credential permission.")
		}
		if !controlled(live, e, st.OwnerUID) {
			return failure("CredentialRBACConflict", "A foreign object occupies the credential permission name.")
		}
		for _, field := range []string{"rules", "roleRef", "subjects"} {
			if !reflect.DeepEqual(wanted.Object[field], live.Object[field]) {
				return failure("CredentialRBACConflict", "The credential permission was changed; it was not adopted or overwritten.")
			}
		}
		if e.UID == "" {
			e.UID = string(live.GetUID())
			if err := r.Engine.Store.Save(ctx, st); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *AccessReconciler) revokeCredentialReaders(ctx context.Context, st *state.State) error {
	// Remove the binding first. These leaf RBAC objects need no foreground GC.
	for i := len(st.HostEntries) - 1; i >= 0; i-- {
		e := &st.HostEntries[i]
		if e.Deleted {
			continue
		}
		if e.Type != "credential-rbac" {
			return failure("CredentialRBACConflict", "Unexpected credential permission inventory.")
		}
		live := &unstructured.Unstructured{}
		live.SetAPIVersion(e.APIVersion)
		live.SetKind(e.Kind)
		err := r.Engine.Client.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.Name}, live)
		if apierrors.IsNotFound(err) {
			e.Deleted = true
			continue
		}
		if err != nil {
			return failure("CredentialRBACDenied", "Cannot inspect credential permission cleanup.")
		}
		if !controlled(live, e, st.OwnerUID) {
			return failure("CredentialRBACConflict", "A credential permission name was reused; it was preserved.")
		}
		uid, rv := live.GetUID(), live.GetResourceVersion()
		if err := r.Engine.Client.Delete(ctx, live, client.Preconditions{UID: &uid, ResourceVersion: &rv}); err != nil && !apierrors.IsNotFound(err) {
			return failure("CredentialRBACDenied", "Cannot revoke credential Secret read permission.")
		}
		if err := r.Engine.Client.Get(ctx, client.ObjectKeyFromObject(live), live); !apierrors.IsNotFound(err) {
			return failure("CredentialRBACPending", "Waiting for credential permissions to disappear.")
		}
		e.Deleted = true
	}
	return r.Engine.Store.Save(ctx, st)
}
