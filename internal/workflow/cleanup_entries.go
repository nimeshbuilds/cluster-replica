package workflow

import (
	"context"

	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Kubernetes' PVC-protection finalizer is not exported by k8s.io/api.
const pvcProtectionFinalizer = "kubernetes.io/pvc-protection"

// cleanupGuestEntries normally waits for each reverse dependency to disappear
// before deleting the next one: a custom resource can need its controller to
// finish a finalizer. A PVC in a namespace we own is the exception. Guest test
// Pods may still reference that PVC, so waiting for it before even requesting
// namespace deletion deadlocks PVC protection against namespace cleanup.
func (w *Engine) cleanupGuestEntries(ctx context.Context, conn *target.Connection, st *state.State) (bool, error) {
	ownedNamespaces := map[string]*state.Entry{}
	for i := range st.Entries {
		e := &st.Entries[i]
		if e.APIVersion == "v1" && e.Kind == "Namespace" && e.Resource == "namespaces" && e.Namespace == "" && !e.Deleted {
			ownedNamespaces[e.Name] = e
		}
	}
	pending := false
	for i := len(st.Entries) - 1; i >= 0; i-- {
		e := &st.Entries[i]
		done, err := w.deleteEntry(ctx, conn, st, e)
		if err != nil {
			return false, err
		}
		if done {
			continue
		}
		pending = true
		if namespace := ownedNamespaces[e.Namespace]; e.APIVersion == "v1" && e.Kind == "PersistentVolumeClaim" && e.Resource == "persistentvolumeclaims" && namespace != nil {
			// An operator's custom PVC finalizer can still need an earlier
			// controller. Relax ordering only for ordinary Kubernetes PVC and
			// foreground-deletion protection, never arbitrary finalizers.
			pvc, err := resource(conn, e.APIVersion, e.Resource, e.Namespace).Get(ctx, e.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
				return false, nil
			}
			if err != nil {
				return false, failure("CleanupReadFailed", "Cannot verify a pending volume's ownership and finalizers.")
			}
			if !controlled(pvc, e, st.OwnerUID) {
				return false, failure("OwnershipConflict", "A pending volume changed UID or ownership markers; namespace cleanup was preserved.")
			}
			for _, finalizer := range pvc.GetFinalizers() {
				if finalizer != pvcProtectionFinalizer && finalizer != metav1.FinalizerDeleteDependents {
					return false, nil
				}
			}
			// Revalidate before relaxing dependency ordering, not just before
			// deletion. deleteEntry checks the same identity again and deletes
			// with UID/resourceVersion preconditions. A borrowed namespace has
			// no entry, so none of its unrelated consumers can be removed.
			live, err := resource(conn, "v1", "namespaces", "").Get(ctx, namespace.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
				return false, nil
			}
			if err != nil {
				return false, failure("CleanupReadFailed", "Cannot verify the owned namespace while its volume cleanup is pending.")
			}
			if !controlled(live, namespace, st.OwnerUID) {
				return false, failure("OwnershipConflict", "A volume's namespace changed UID or ownership markers; no namespace deletion was attempted.")
			}
			continue
		}
		return false, nil
	}
	return !pending, nil
}
