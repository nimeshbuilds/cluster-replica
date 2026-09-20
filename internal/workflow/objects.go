package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
	"k8s.io/client-go/dynamic"
)

func resource(conn *target.Connection, version, plural, namespace string) dynamic.ResourceInterface {
	r := conn.Dynamic.Resource(planner.GVR(version, plural))
	if namespace != "" {
		return r.Namespace(namespace)
	}
	return r
}
func entry(st *state.State, id string) *state.Entry {
	for i := range st.Entries {
		if st.Entries[i].ID == id && !st.Entries[i].Deleted {
			return &st.Entries[i]
		}
	}
	return nil
}
func controlled(obj *unstructured.Unstructured, e *state.Entry, owner string) bool {
	return (e.UID == "" || e.UID == string(obj.GetUID())) && obj.GetAnnotations()[planner.OwnerAnnotation] == owner && obj.GetAnnotations()[planner.OperationAnnotation] == e.OperationID
}
func failure(reason, detail string) error { return &planner.Problem{Reason: reason, Detail: detail} }

func (w *Engine) apply(ctx context.Context, conn *target.Connection, st *state.State, desired state.Object, refresh bool) (bool, error) {
	e := entry(st, desired.ID)
	client := resource(conn, desired.APIVersion, desired.Resource, desired.Namespace)
	live, err := client.Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, failure("GuestReadFailed", "Cannot inspect a planned guest resource; verify API availability and target permissions.")
	}
	if e == nil {
		if err == nil {
			return false, failure("OwnershipConflict", "A planned resource already exists in the guest; Replicove will not adopt it: "+desired.ID)
		}
		op, err := state.OperationID()
		if err != nil {
			return false, err
		}
		st.Entries = append(st.Entries, state.Entry{ID: desired.ID, Type: "object", APIVersion: desired.APIVersion, Kind: desired.Kind, Resource: desired.Resource, Namespace: desired.Namespace, Name: desired.Name, OperationID: op})
		if err := w.Store.Save(ctx, st); err != nil {
			return false, err
		}
		e = &st.Entries[len(st.Entries)-1]
	}
	wanted := (&unstructured.Unstructured{Object: desired.Desired}).DeepCopy()
	annotations := wanted.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[planner.OwnerAnnotation] = st.OwnerUID
	annotations[planner.OperationAnnotation] = e.OperationID
	wanted.SetAnnotations(annotations)
	if apierrors.IsNotFound(err) {
		// A UID that disappeared is drift, never an invitation to recreate experiments.
		if e.UID != "" && refresh {
			e.Deleted = true
			if err := w.Store.Save(ctx, st); err != nil {
				return false, err
			}
			return w.apply(ctx, conn, st, desired, true)
		}
		if e.UID != "" {
			return false, failure("TargetDrift", "An owned resource is missing; explicit refresh is required before recreation.")
		}
		live, err = client.Create(ctx, wanted, metav1.CreateOptions{FieldManager: "replicove"})
		if err != nil {
			return false, failure("GuestApplyFailed", "Cannot create planned resource "+desired.ID+"; inspect target admission, quotas, and API compatibility.")
		}
		e.UID = string(live.GetUID())
		e.Applied = desired.Desired
		if err := w.Store.Save(ctx, st); err != nil {
			return false, err
		}
		return true, nil
	}
	if !controlled(live, e, st.OwnerUID) {
		return false, failure("OwnershipConflict", "An inventoried resource has a different UID or operation marker; no mutation was attempted.")
	}
	if e.UID == "" {
		e.UID = string(live.GetUID())
		e.Applied = desired.Desired
		return true, w.Store.Save(ctx, st)
	}
	if refresh && !reflect.DeepEqual(e.Applied, desired.Desired) {
		old, _ := json.Marshal(e.Applied)
		next, _ := json.Marshal(desired.Desired)
		current, _ := json.Marshal(live.Object)
		patch, err := jsonmergepatch.CreateThreeWayJSONMergePatch(old, next, current)
		if err != nil {
			return false, failure("RefreshConflict", "Cannot construct a refresh patch.")
		}
		var object map[string]any
		if err := json.Unmarshal(patch, &object); err != nil {
			return false, err
		}
		meta, ok := object["metadata"].(map[string]any)
		if !ok {
			meta = map[string]any{}
		}
		meta["uid"] = e.UID
		meta["resourceVersion"] = live.GetResourceVersion()
		object["metadata"] = meta
		patch, _ = json.Marshal(object)
		if _, err := client.Patch(ctx, e.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "replicove"}); err != nil {
			return false, failure("RefreshConflict", "The owned resource rejected a refresh; immutable fields and concurrent changes are preserved.")
		}
		e.Applied = desired.Desired
		return true, w.Store.Save(ctx, st)
	}
	return false, nil
}

func (w *Engine) namespaces(ctx context.Context, conn *target.Connection, st *state.State) error {
	seen := map[string]bool{}
	for _, obj := range st.Plan.Objects {
		if obj.Namespace != "" {
			seen[obj.Namespace] = true
		}
	}
	names := []string{}
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		id := planner.ID("", "Namespace", "", name)
		if entry(st, id) == nil {
			_, err := conn.Kubernetes.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				if st.MirrorRunUID != "" {
					return failure("MirrorNamespaceConflict", "A mirror requires an exclusively owned guest namespace; an existing namespace was preserved.")
				}
				continue
			}
			if !apierrors.IsNotFound(err) {
				return failure("GuestReadFailed", "Cannot read guest namespaces.")
			}
			if st.Provider == "existing" && st.MirrorRunUID == "" {
				return failure("ExistingNamespaceRequired", "Create the mapped namespace in the existing guest before applying this request.")
			}
		}
		_, err := w.apply(ctx, conn, st, state.Object{ID: id, APIVersion: "v1", Kind: "Namespace", Resource: "namespaces", Name: name, Desired: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": name}}}, false)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Engine) deleteEntry(ctx context.Context, conn *target.Connection, st *state.State, e *state.Entry) (bool, error) {
	if e.Deleted {
		return true, nil
	}
	client := resource(conn, e.APIVersion, e.Resource, e.Namespace)
	obj, err := client.Get(ctx, e.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
		e.Deleted = true
		return true, w.Store.Save(ctx, st)
	}
	if err != nil {
		return false, failure("CleanupReadFailed", "Cannot inspect an inventoried resource during cleanup.")
	}
	if !controlled(obj, e, st.OwnerUID) {
		return false, failure("OwnershipConflict", "Cleanup found a changed UID or ownership marker; the resource was preserved.")
	}
	if obj.GetDeletionTimestamp() != nil {
		return false, nil
	}
	uid, rv := obj.GetUID(), obj.GetResourceVersion()
	propagation := metav1.DeletePropagationForeground
	err = client.Delete(ctx, e.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &propagation})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, failure("CleanupDeleteFailed", "Cannot delete an inventoried resource; finalizers and admission policy remain in effect.")
	}
	// Most leaf resources disappear immediately. Verify that before yielding,
	// while still waiting for dependants/finalizers when deletion is asynchronous.
	if _, err := client.Get(ctx, e.Name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		e.Deleted = true
		return true, w.Store.Save(ctx, st)
	}
	return false, nil
}

func Ready(obj *unstructured.Unstructured) bool {
	if obj.GetDeletionTimestamp() != nil {
		return false
	}
	integer := func(fields ...string) int64 { v, _, _ := unstructured.NestedInt64(obj.Object, fields...); return v }
	condition := func(kind string) bool {
		list, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && m["type"] == kind && m["status"] == "True" {
				return true
			}
		}
		return false
	}
	switch obj.GetKind() {
	case "Deployment", "StatefulSet":
		want := int64(1)
		if n, ok, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas"); ok {
			want = n
		}
		return integer("status", "observedGeneration") >= obj.GetGeneration() && integer("status", "readyReplicas") == want && integer("status", "updatedReplicas") == want
	case "DaemonSet":
		return integer("status", "observedGeneration") >= obj.GetGeneration() && integer("status", "numberReady") == integer("status", "desiredNumberScheduled")
	case "Job":
		return condition("Complete")
	case "CustomResourceDefinition":
		return condition("Established")
	case "PersistentVolumeClaim":
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		return phase == "Bound"
	}
	return true
}

// ContainsDesired ignores server defaults and status but not desired values.
func ContainsDesired(want, live any) bool {
	switch a := want.(type) {
	case map[string]any:
		b, ok := live.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range a {
			if !ContainsDesired(value, b[key]) {
				return false
			}
		}
		return true
	case []any:
		b, ok := live.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !ContainsDesired(a[i], b[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, live)
	}
}
func Check(obj *unstructured.Unstructured, condition, field, expected string) bool {
	if condition != "" {
		list, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && m["type"] == condition && m["status"] == "True" {
				return true
			}
		}
		return false
	}
	if !strings.HasPrefix(field, "status.") {
		return false
	}
	value, ok, _ := unstructured.NestedFieldNoCopy(obj.Object, strings.Split(field, ".")...)
	return ok && fmt.Sprint(value) == expected
}
