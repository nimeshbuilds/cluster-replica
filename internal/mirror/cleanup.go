package mirror

import (
	"context"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *Reconciler) retire(ctx context.Context, st *state.State) (bool, error) {
	if st.MirrorRun.Child.UID != "" {
		child := &api.ClusterReplica{}
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: st.OwnerNamespace, Name: st.MirrorRun.Child.Name}, child)
		if !apierrors.IsNotFound(err) {
			if err != nil {
				return false, problem("GenerationCleanupUnavailable", "Cannot inspect the owned generation.")
			}
			if string(child.UID) != st.MirrorRun.Child.UID {
				return false, problem("GenerationOwnershipConflict", "The generation name was reused; its replacement was preserved.")
			}
			if child.DeletionTimestamp.IsZero() {
				if err := r.Client.Delete(ctx, child, client.Preconditions{UID: &child.UID, ResourceVersion: &child.ResourceVersion}); err != nil && !apierrors.IsNotFound(err) {
					return false, err
				}
			}
			return false, nil
		}
	}
	for _, v := range st.MirrorRun.Volumes {
		pv := &corev1.PersistentVolume{}
		err := r.Client.Get(ctx, client.ObjectKey{Name: v.Name}, pv)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, problem("VolumeCleanupUnavailable", "Cannot verify restored volume deletion.")
		}
		if string(pv.UID) != v.UID {
			return false, problem("VolumeOwnershipConflict", "A recorded destination PV name was reused; its replacement was preserved.")
		}
		return false, nil
	}
	if done, err := r.cleanupStorage(ctx, st, false); err != nil || !done {
		return false, err
	}
	return r.cleanupPolicies(ctx, st)
}

func (r *Reconciler) collect(ctx context.Context, m *api.ReplicaMirror, parent *state.State, all bool) (bool, error) {
	states := map[string]*state.State{}
	requests := map[string]*api.ReplicaMirrorRun{}
	for _, ref := range parent.Mirror.Runs {
		st, err := r.Store.Load(ctx, ref.UID)
		if err != nil {
			return false, err
		}
		if st != nil {
			if st.MirrorRun == nil || st.MirrorRun.MirrorUID != parent.OwnerUID {
				return false, state.ErrIntegrity
			}
			states[ref.UID] = st
		}
		run := &api.ReplicaMirrorRun{}
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: ref.Name}, run)
		if err == nil {
			if string(run.UID) != ref.UID {
				return false, problem("RunOwnershipConflict", "A mirror run name was reused; its replacement was preserved.")
			}
			requests[ref.UID] = run
		} else if !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	keep := map[string]bool{}
	if !all {
		for _, id := range []string{parent.Mirror.ActiveUID, parent.Mirror.PendingUID} {
			if st := states[id]; st != nil {
				if run := requests[id]; id == parent.Mirror.ActiveUID || run != nil && run.DeletionTimestamp.IsZero() {
					keep[st.MirrorRun.RevisionUID] = true
				}
			}
		}
		for i := len(parent.Mirror.Runs) - 1; i >= 0 && len(keep) < retained(m); i-- {
			id := parent.Mirror.Runs[i].UID
			st := states[id]
			run := requests[id]
			if st != nil && run != nil && run.DeletionTimestamp.IsZero() && st.MirrorRun.RevisionUID == id && !st.MirrorRun.CapturedAt.IsZero() {
				keep[id] = true
			}
		}
	}
	for i, ref := range parent.Mirror.Runs {
		st := states[ref.UID]
		run := requests[ref.UID]
		deleting := all || run == nil || !run.DeletionTimestamp.IsZero()
		if st != nil && !all && ref.UID == parent.Mirror.ActiveUID {
			continue
		}
		if st != nil && !deleting && (ref.UID == parent.Mirror.PendingUID || st.MirrorRun.Phase == "Queued") {
			continue
		}
		if st != nil {
			if done, err := r.retire(ctx, st); err != nil || !done {
				return false, err
			}
			if keep[ref.UID] {
				if st.MirrorRun.Phase != "Retained" {
					st.MirrorRun.Phase = "Retained"
					if err := r.Store.Save(ctx, st); err != nil {
						return false, err
					}
					if run != nil {
						if err := r.syncRunStatus(ctx, run, st); err != nil {
							return false, err
						}
					}
				}
				continue
			}
			if done, err := r.cleanupStorage(ctx, st, true); err != nil || !done {
				return false, err
			}
			if err := r.Store.Delete(ctx, st.OwnerUID); err != nil {
				return false, err
			}
		}
		if run != nil {
			before := run.DeepCopy()
			controllerutil.RemoveFinalizer(run, Finalizer)
			if err := r.Client.Patch(ctx, run, client.MergeFrom(before)); err != nil {
				return false, err
			}
			if run.DeletionTimestamp.IsZero() {
				if err := r.Client.Delete(ctx, run, client.Preconditions{UID: &run.UID}); err != nil && !apierrors.IsNotFound(err) {
					return false, err
				}
			}
		}
		parent.Mirror.Runs = append(parent.Mirror.Runs[:i], parent.Mirror.Runs[i+1:]...)
		if parent.Mirror.PendingUID == ref.UID {
			parent.Mirror.PendingUID = ""
		}
		if parent.Mirror.ActiveUID == ref.UID {
			parent.Mirror.ActiveUID = ""
		}
		if err := r.Store.Save(ctx, parent); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (r *Reconciler) cleanup(ctx context.Context, m *api.ReplicaMirror, st *state.State) (ctrl.Result, error) {
	if st != nil {
		if st.Mirror == nil {
			return r.report(ctx, m, "Blocked", state.ErrIntegrity)
		}
		if err := r.enrollRuns(ctx, m, st); err != nil {
			return r.report(ctx, m, "Deleting", err)
		}
		done, err := r.collect(ctx, m, st, true)
		if err != nil || !done {
			return r.report(ctx, m, "Deleting", err)
		}
		if err := r.Store.Delete(ctx, st.OwnerUID); err != nil {
			return r.report(ctx, m, "Deleting", err)
		}
	}
	before := m.DeepCopy()
	m.Status.ActiveRun = nil
	m.Status.ActiveReplica = nil
	m.Status.PendingRun = nil
	m.Status.NextSyncAt = nil
	if err := r.Client.Status().Patch(ctx, m, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}
	if !m.DeletionTimestamp.IsZero() {
		before = m.DeepCopy()
		controllerutil.RemoveFinalizer(m, Finalizer)
		return ctrl.Result{}, r.Client.Patch(ctx, m, client.MergeFrom(before))
	}
	result, err := r.report(ctx, m, "Expired", nil)
	result.RequeueAfter = 0
	return result, err
}

// CleanupGeneration runs before and after guest cleanup, including cancellation
// before Ready and TTL expiry. The existing runtime itself is never deleted.
func (r *Reconciler) CleanupGeneration(ctx context.Context, child *state.State, guestCleaned bool) (bool, error) {
	run, err := r.Store.Load(ctx, child.MirrorRunUID)
	if err != nil {
		return false, err
	}
	if run == nil || run.MirrorRun == nil || run.MirrorRun.Child.UID != child.OwnerUID {
		return false, state.ErrIntegrity
	}
	revision, err := r.Store.Load(ctx, run.MirrorRun.RevisionUID)
	if err != nil {
		return false, err
	}
	if revision == nil || revision.MirrorRun == nil {
		return false, state.ErrUnavailable
	}
	if err := r.inventoryVolumes(ctx, run, revision, child, false); err != nil {
		return false, err
	}
	if !guestCleaned {
		return true, nil
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := r.Client.List(ctx, claims, client.InNamespace(run.OwnerNamespace)); err != nil {
		return false, err
	}
	for _, pvc := range claims.Items {
		if pvc.Labels["vcluster.loft.sh/managed-by"] != run.MirrorRun.RuntimeRelease {
			continue
		}
		for _, o := range child.Plan.Objects {
			if o.Kind == "PersistentVolumeClaim" && pvc.Annotations["vcluster.loft.sh/object-namespace"] == o.Namespace && pvc.Annotations["vcluster.loft.sh/object-name"] == o.Name {
				return false, nil
			}
		}
	}
	for _, v := range run.MirrorRun.Volumes {
		pv := &corev1.PersistentVolume{}
		err := r.Client.Get(ctx, client.ObjectKey{Name: v.Name}, pv)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if string(pv.UID) != v.UID {
			return false, problem("VolumeOwnershipConflict", "A restored PV name was reused; the replacement was preserved.")
		}
		return false, nil
	}
	return true, nil
}
