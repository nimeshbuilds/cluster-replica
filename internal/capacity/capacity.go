// Package capacity serializes admissions through encrypted optimistic-concurrency
// reservations. Reservations are released only after verified resource cleanup.
package capacity

import (
	"context"
	"errors"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func identity(namespace string) string { return "capacity-" + state.Name(namespace) }

func Acquire(ctx context.Context, store *state.Store, obj *api.ClusterReplica, grant *api.ReplicaGrant, provider string) (bool, error) {
	for attempt := 0; attempt < 8; attempt++ {
		ledger, err := store.Load(ctx, identity(obj.Namespace))
		if err != nil {
			return false, err
		}
		if ledger == nil {
			ledger = &state.State{OwnerUID: identity(obj.Namespace), OwnerNamespace: obj.Namespace, OwnerName: "capacity", Capacity: &state.Capacity{}}
		}
		if ledger.Capacity == nil {
			return false, state.ErrIntegrity
		}
		count := int32(0)
		for _, r := range ledger.Capacity.Reservations {
			if r.UID == string(obj.UID) {
				return true, nil
			}
			if r.GrantUID == string(grant.UID) {
				count++
			}
			if provider == "helm" && r.Provider == "helm" {
				return false, nil
			}
		}
		if grant.Spec.MaxConcurrentReplicas > 0 && count >= grant.Spec.MaxConcurrentReplicas {
			return false, nil
		}
		// Preserve pre-upgrade replicas that did not yet have an admission ledger.
		// A live runtime Service is evidence of allocation; a planned status alone is not.
		if provider == "helm" {
			peers := &api.ClusterReplicaList{}
			if err := store.Client.List(ctx, peers, client.InNamespace(obj.Namespace)); err != nil {
				return false, err
			}
			for _, p := range peers.Items {
				if p.UID == obj.UID || p.Status.Runtime == nil || p.Status.Phase == "Expired" || p.Status.Phase == "Cleaned" {
					continue
				}
				svc := &corev1.Service{}
				err := store.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: p.Status.Runtime.ReleaseName}, svc)
				if err == nil {
					return false, nil
				}
				if !apierrors.IsNotFound(err) {
					return false, err
				}
			}
		}
		ledger.Capacity.Reservations = append(ledger.Capacity.Reservations, state.Reservation{UID: string(obj.UID), GrantUID: string(grant.UID), Provider: provider})
		if err := store.Save(ctx, ledger); err == nil {
			return true, nil
		} else if !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
			return false, err
		}
	}
	return false, errors.New("capacity reservation contention; retry")
}

func Release(ctx context.Context, store *state.Store, namespace, uid string) error {
	for attempt := 0; attempt < 8; attempt++ {
		ledger, err := store.Load(ctx, identity(namespace))
		if err != nil {
			return err
		}
		if ledger == nil {
			return nil
		}
		if ledger.Capacity == nil {
			return state.ErrIntegrity
		}
		items := ledger.Capacity.Reservations[:0]
		found := false
		for _, r := range ledger.Capacity.Reservations {
			if r.UID == uid {
				found = true
				continue
			}
			items = append(items, r)
		}
		if !found {
			return nil
		}
		ledger.Capacity.Reservations = items
		// Keep the empty ledger so a concurrent acquire cannot lose a reservation to deletion.
		if err := store.Save(ctx, ledger); err == nil {
			return nil
		} else if !apierrors.IsConflict(err) {
			return err
		}
	}
	return errors.New("capacity release contention; retry")
}
