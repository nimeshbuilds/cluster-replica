package chaos

import (
	"context"
	"encoding/json"
	"errors"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func owned(live *unstructured.Unstructured, st *state.State, a *state.ChaosAction) bool {
	return (a.UID == "" || string(live.GetUID()) == a.UID) && live.GetAnnotations()[owner] == st.OwnerUID && live.GetAnnotations()[marker] == a.OperationID
}
func (r *Reconciler) apply(ctx context.Context, conn *target.Connection, st *state.State, a *state.ChaosAction) error {
	if a.Kind == "HostNetworkIsolation" {
		return r.applyHostPolicy(ctx, st, a)
	}
	if a.TargetKind == "Job" {
		if err := r.verifyHostPolicies(ctx, st); err != nil {
			return err
		}
	}
	c := apiResource(conn, a.TargetKind, a.Namespace)
	live, err := c.Get(ctx, a.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return errors.New("Cannot inspect fault target.")
	}
	switch a.Kind {
	case "Create":
		if apierrors.IsNotFound(err) {
			if a.UID != "" || a.Applied {
				return errors.New("An owned fault resource disappeared; it will not be recreated.")
			}
			obj := (&unstructured.Unstructured{Object: a.Object}).DeepCopy()
			obj.SetAnnotations(map[string]string{owner: st.OwnerUID, marker: a.OperationID})
			if a.TargetKind == "Job" {
				_ = unstructured.SetNestedStringMap(obj.Object, map[string]string{owner: st.OwnerUID, marker: a.OperationID}, "spec", "template", "metadata", "annotations")
			}
			live, err = c.Create(ctx, obj, metav1.CreateOptions{FieldManager: "replicove-chaos"})
			if err != nil {
				return errors.New("Fault resource creation failed; rollback will remove only owned resources.")
			}
		}
		if !owned(live, st, a) {
			return errors.New("A fault resource has a conflicting UID or ownership marker.")
		}
		if a.TargetKind == "Job" {
			failed, _, _ := unstructured.NestedInt64(live.Object, "status", "failed")
			if failed > 0 {
				return errors.New("A restricted fault Job failed; inspect its guest logs.")
			}
		}
		a.UID = string(live.GetUID())
		a.Applied = true
	case "PodDelete":
		if apierrors.IsNotFound(err) {
			a.Applied = true
			return r.Store.Save(ctx, st)
		}
		if string(live.GetUID()) != a.UID { // A controller may have recreated the same Pod name; never delete the replacement.
			a.Applied = true
			return r.Store.Save(ctx, st)
		}
		if !a.Applied {
			uid, rv := live.GetUID(), live.GetResourceVersion()
			grace := int64(1)
			if err := c.Delete(ctx, a.Name, metav1.DeleteOptions{GracePeriodSeconds: &grace, Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
				return errors.New("Cannot delete the pinned guest Pod.")
			}
			a.Applied = true
		}
	case "ScaleZero":
		if err != nil || string(live.GetUID()) != a.UID {
			return errors.New("Scale target UID changed; replacement workloads are preserved.")
		}
		if !scaleOwnership(live, st, a) {
			return errors.New("Scale target replica ownership changed; the resource was preserved.")
		}
		replicas, _, _ := unstructured.NestedInt64(live.Object, "spec", "replicas")
		mark := live.GetAnnotations()[marker]
		if mark == a.OperationID && replicas == 0 {
			a.Applied = true
			return r.Store.Save(ctx, st)
		}
		if a.Applied || mark != "" || a.OriginalReplicas == nil || replicas != *a.OriginalReplicas {
			return errors.New("Scale target changed concurrently; rollback will preserve foreign changes.")
		}
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"uid": a.UID, "resourceVersion": live.GetResourceVersion(), "annotations": map[string]any{marker: a.OperationID}}, "spec": map[string]any{"replicas": int64(0)}})
		if _, err := c.Patch(ctx, a.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "replicove-chaos"}); err != nil {
			return errors.New("Cannot apply the UID-checked scale fault.")
		}
		a.Applied = true
	default:
		return errors.New("Unknown protected fault action.")
	}
	return r.Store.Save(ctx, st)
}

func (r *Reconciler) cleanup(ctx context.Context, x *api.ReplicaExperiment, st *state.State, outcome, message string) (ctrl.Result, error) {
	if st == nil {
		if !x.DeletionTimestamp.IsZero() {
			return r.finish(ctx, x)
		}
		return r.report(ctx, x, nil, outcome, message)
	}
	if st.Experiment.Phase == "Finished" {
		if !x.DeletionTimestamp.IsZero() {
			return r.finishAndForget(ctx, x, st)
		}
		return r.report(ctx, x, st, st.Experiment.Outcome, st.Experiment.Message)
	}
	if !st.CleanupStarted {
		st.CleanupStarted = true
		st.Experiment.Outcome = outcome
		st.Experiment.Message = message
		if err := r.Store.Save(ctx, st); err != nil {
			return r.report(ctx, x, st, "Blocked", "Cannot persist rollback intent.")
		}
	}
	conn, err := r.connect(ctx, st)
	if err != nil {
		return r.report(ctx, x, st, "Blocked", err.Error())
	}
	for i := len(st.Experiment.Actions) - 1; i >= 0; i-- {
		a := &st.Experiment.Actions[i]
		if a.Cleaned {
			continue
		}
		if a.Kind != "HostNetworkIsolation" {
			current, err := namespaceCurrent(ctx, conn, a)
			if err != nil {
				return r.report(ctx, x, st, "Blocked", err.Error())
			}
			if !current {
				// Never operate on a new namespace that reused the original name.
				a.Cleaned = true
				if err := r.Store.Save(ctx, st); err != nil {
					return r.report(ctx, x, st, "Blocked", "Cannot record vanished guest namespace cleanup.")
				}
				continue
			}
		}
		done, err := r.rollback(ctx, conn, st, a)
		if err != nil {
			return r.report(ctx, x, st, "Blocked", err.Error())
		}
		if !done {
			return r.report(ctx, x, st, "Cleaning", "Waiting for owned fault resources to disappear.")
		}
	}
	st.Experiment.Phase = "Finished"
	if err := r.Store.Save(ctx, st); err != nil {
		return r.report(ctx, x, st, "Blocked", "Cannot persist verified rollback completion.")
	}
	if !x.DeletionTimestamp.IsZero() {
		return r.finishAndForget(ctx, x, st)
	}
	return r.report(ctx, x, st, st.Experiment.Outcome, st.Experiment.Message)
}

func (r *Reconciler) finishAndForget(ctx context.Context, x *api.ReplicaExperiment, st *state.State) (ctrl.Result, error) {
	// Never remove the only rollback authority while a cleanup finalizer still
	// exists. A crash after finalizer removal leaves a verified Finished record;
	// parent cleanup reaps that record without trusting any public status.
	result, err := r.finish(ctx, x)
	if err != nil {
		return result, err
	}
	return result, r.Store.Delete(ctx, st.OwnerUID)
}

func (r *Reconciler) rollback(ctx context.Context, conn *target.Connection, st *state.State, a *state.ChaosAction) (bool, error) {
	if a.Kind == "HostNetworkIsolation" {
		return r.rollbackHostPolicy(ctx, st, a)
	}
	if a.Kind == "PodDelete" {
		a.Cleaned = true
		return true, r.Store.Save(ctx, st)
	}
	c := apiResource(conn, a.TargetKind, a.Namespace)
	live, err := c.Get(ctx, a.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, errors.New("Cannot inspect a fault during rollback.")
	}
	if a.Kind == "Create" {
		if apierrors.IsNotFound(err) {
			if a.TargetKind == "Job" {
				done, err := r.cleanPods(ctx, conn, st, a)
				if err != nil || !done {
					return done, err
				}
			}
			a.Cleaned = true
			return true, r.Store.Save(ctx, st)
		}
		if !owned(live, st, a) {
			return false, errors.New("Rollback preserved a resource with a different UID or ownership marker.")
		}
		if a.UID == "" {
			a.UID = string(live.GetUID())
			if err := r.Store.Save(ctx, st); err != nil {
				return false, err
			}
		}
		uid, rv := live.GetUID(), live.GetResourceVersion()
		propagation := metav1.DeletePropagationForeground
		if live.GetDeletionTimestamp() == nil {
			if err := c.Delete(ctx, a.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &propagation}); err != nil && !apierrors.IsNotFound(err) {
				return false, errors.New("Cannot remove an owned fault resource.")
			}
		}
		return false, nil
	}
	if a.Kind == "ScaleZero" {
		if apierrors.IsNotFound(err) {
			a.Cleaned = true
			return true, r.Store.Save(ctx, st)
		}
		if string(live.GetUID()) != a.UID {
			return false, errors.New("Rollback preserved a replacement workload with a different UID.")
		}
		if !scaleOwnership(live, st, a) {
			return false, errors.New("Rollback preserved a workload whose replica ownership changed.")
		}
		replicas, _, _ := unstructured.NestedInt64(live.Object, "spec", "replicas")
		if a.OriginalReplicas == nil {
			return false, errors.New("Original replica count is missing from protected state.")
		}
		mark := live.GetAnnotations()[marker]
		if mark == "" && replicas == *a.OriginalReplicas {
			a.Cleaned = true
			return true, r.Store.Save(ctx, st)
		}
		if mark != a.OperationID || replicas != 0 {
			return false, errors.New("Rollback is blocked by a concurrent scale or ownership change; restore the expected fault state before retrying.")
		}
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"uid": a.UID, "resourceVersion": live.GetResourceVersion(), "annotations": map[string]any{marker: nil}}, "spec": map[string]any{"replicas": *a.OriginalReplicas}})
		updated, err := c.Patch(ctx, a.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "replicove-chaos"})
		if err != nil {
			return false, errors.New("Cannot restore the original workload replica count.")
		}
		now, _, _ := unstructured.NestedInt64(updated.Object, "spec", "replicas")
		if now != *a.OriginalReplicas || updated.GetAnnotations()[marker] != "" {
			return false, errors.New("The restored workload did not retain the expected replica count.")
		}
		a.Cleaned = true
		return true, r.Store.Save(ctx, st)
	}
	return false, errors.New("Unknown protected rollback action.")
}

func scaleOwnership(live *unstructured.Unstructured, st *state.State, a *state.ChaosAction) bool {
	op, _ := a.Object["replicaOperation"].(string)
	return op != "" && live.GetAnnotations()[planner.OwnerAnnotation] == st.ReplicaUID && live.GetAnnotations()[planner.OperationAnnotation] == op
}

func (r *Reconciler) cleanPods(ctx context.Context, conn *target.Connection, st *state.State, a *state.ChaosAction) (bool, error) {
	pods, err := conn.Kubernetes.CoreV1().Pods(a.Namespace).List(ctx, metav1.ListOptions{LabelSelector: owner + "=" + st.OwnerUID})
	if err != nil {
		return false, errors.New("Cannot verify fault Job Pod cleanup.")
	}
	pending := false
	for _, p := range pods.Items {
		matches := false
		for _, ref := range p.OwnerReferences {
			if ref.Kind == "Job" && ref.Name == a.Name && (a.UID == "" || string(ref.UID) == a.UID) {
				matches = true
			}
		}
		if !matches {
			continue
		}
		if p.Annotations[owner] != st.OwnerUID || p.Annotations[marker] != a.OperationID {
			return false, errors.New("A remaining Job Pod has changed ownership; it was preserved.")
		}
		pending = true
		uid, rv := p.UID, p.ResourceVersion
		grace := int64(1)
		if p.DeletionTimestamp == nil {
			if err := conn.Kubernetes.CoreV1().Pods(a.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{GracePeriodSeconds: &grace, Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
				return false, errors.New("Cannot remove an owned fault Job Pod.")
			}
		}
	}
	return !pending, nil
}

func (r *Reconciler) jobsStarted(ctx context.Context, conn *target.Connection, st *state.State) (bool, error) {
	for _, a := range st.Experiment.Actions {
		if a.TargetKind != "Job" {
			continue
		}
		job, err := apiResource(conn, "Job", a.Namespace).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil || !owned(job, st, &a) {
			return false, errors.New("Cannot verify the owned fault Job.")
		}
		succeeded, _, _ := unstructured.NestedInt64(job.Object, "status", "succeeded")
		if succeeded > 0 {
			continue
		}
		pods, err := conn.Kubernetes.CoreV1().Pods(a.Namespace).List(ctx, metav1.ListOptions{LabelSelector: owner + "=" + st.OwnerUID})
		if err != nil {
			return false, errors.New("Cannot inspect fault Job startup.")
		}
		running := false
		for _, p := range pods.Items {
			if p.Status.Phase != corev1.PodRunning || p.Annotations[marker] != a.OperationID || p.Annotations[owner] != st.OwnerUID {
				continue
			}
			for _, ref := range p.OwnerReferences {
				if ref.Kind == "Job" && string(ref.UID) == a.UID {
					for _, c := range p.Status.ContainerStatuses {
						if c.Name == "fault" && c.State.Running != nil {
							running = true
						}
					}
				}
			}
		}
		if !running {
			return false, nil
		}
	}
	return true, nil
}
