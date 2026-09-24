package chaos

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Guest NetworkPolicies are deliberately not synced by the pinned runtime.
// Isolation must therefore be applied on the host with an exact runtime and
// guest-namespace selector, never to host/source workloads or the control plane.
func (r *Reconciler) verifyHostPolicies(ctx context.Context, st *state.State) error {
	if st.OwnerNamespace != r.Namespace || st.RuntimeName == "" || st.Provider != "helm" {
		return errors.New("The protected managed runtime scope is not qualified for host isolation.")
	}
	policies := &networkingv1.NetworkPolicyList{}
	if err := r.Client.List(ctx, policies, client.InNamespace(st.OwnerNamespace)); err != nil {
		return errors.New("Cannot inspect host isolation policies.")
	}
	for _, p := range policies.Items {
		if len(p.Spec.Ingress) > 0 || len(p.Spec.Egress) > 0 {
			return errors.New("Host NetworkPolicies are additive; an allow policy conflicts with this fault. Mirror baseline policies are preserved.")
		}
	}
	return nil
}
func policyObject(st *state.State, a *state.ChaosAction) (*networkingv1.NetworkPolicy, error) {
	desired := &networkingv1.NetworkPolicy{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(a.Object, desired); err != nil {
		return nil, errors.New("Invalid protected network fault intent.")
	}
	labels := desired.Spec.PodSelector.MatchLabels
	if desired.Namespace != st.OwnerNamespace || desired.Name != a.Name || len(labels) != 2 || labels["vcluster.loft.sh/managed-by"] != st.RuntimeName || labels["vcluster.loft.sh/namespace"] != a.Namespace || len(desired.Spec.PodSelector.MatchExpressions) != 0 || len(desired.Spec.Ingress) != 0 || len(desired.Spec.Egress) != 0 {
		return nil, errors.New("Protected network selector exceeds its runtime scope.")
	}
	desired.Annotations = map[string]string{owner: st.OwnerUID, marker: a.OperationID}
	return desired, nil
}
func hostOwned(p *networkingv1.NetworkPolicy, st *state.State, a *state.ChaosAction) bool {
	return (a.UID == "" || string(p.UID) == a.UID) && p.Annotations[owner] == st.OwnerUID && p.Annotations[marker] == a.OperationID
}
func (r *Reconciler) applyHostPolicy(ctx context.Context, st *state.State, a *state.ChaosAction) error {
	if err := r.verifyHostPolicies(ctx, st); err != nil {
		return err
	}
	desired, err := policyObject(st, a)
	if err != nil {
		return err
	}
	live := &networkingv1.NetworkPolicy{}
	err = r.Client.Get(ctx, client.ObjectKeyFromObject(desired), live)
	if apierrors.IsNotFound(err) {
		if a.UID != "" || a.Applied {
			return errors.New("The owned host isolation policy disappeared; the fault cannot continue.")
		}
		if err := r.Client.Create(ctx, desired); err != nil {
			return errors.New("Cannot create scoped host isolation policy.")
		}
		live = desired
	} else if err != nil {
		return errors.New("Cannot inspect scoped host isolation policy.")
	}
	// Empty slices are normalized by API serialization; compare semantically.
	expected := desired.Spec.DeepCopy()
	actual := live.Spec.DeepCopy()
	if len(expected.Ingress) == 0 {
		expected.Ingress = nil
	}
	if len(expected.Egress) == 0 {
		expected.Egress = nil
	}
	if len(actual.Ingress) == 0 {
		actual.Ingress = nil
	}
	if len(actual.Egress) == 0 {
		actual.Egress = nil
	}
	if !hostOwned(live, st, a) || !reflect.DeepEqual(actual, expected) {
		return errors.New("Host isolation policy UID, ownership, or rules changed.")
	}
	a.UID = string(live.UID)
	a.Applied = true
	return r.Store.Save(ctx, st)
}
func (r *Reconciler) rollbackHostPolicy(ctx context.Context, st *state.State, a *state.ChaosAction) (bool, error) {
	// The virtual API can report Job/Pod deletion before the syncer has
	// removed its physical Pod. Keep host isolation until those Pods are gone.
	pods := &corev1.PodList{}
	if err := r.Client.List(ctx, pods, client.InNamespace(st.OwnerNamespace), client.MatchingLabels{"vcluster.loft.sh/managed-by": st.RuntimeName, "vcluster.loft.sh/namespace": a.Namespace}); err != nil {
		return false, errors.New("Cannot verify physical fault Pod cleanup.")
	}
	for _, action := range st.Experiment.Actions {
		if action.TargetKind != "Job" || action.Namespace != a.Namespace {
			continue
		}
		for _, p := range pods.Items {
			name := p.Annotations["vcluster.loft.sh/object-name"]
			if name == "" {
				return false, errors.New("An unmapped physical guest Pod prevents verified fault cleanup.")
			}
			if strings.HasPrefix(name, action.Name+"-") {
				return false, nil
			}
		}
	}
	desired, err := policyObject(st, a)
	if err != nil {
		return false, err
	}
	live := &networkingv1.NetworkPolicy{}
	err = r.Client.Get(ctx, client.ObjectKeyFromObject(desired), live)
	if apierrors.IsNotFound(err) {
		a.Cleaned = true
		return true, r.Store.Save(ctx, st)
	}
	if err != nil {
		return false, errors.New("Cannot inspect host fault cleanup.")
	}
	if !hostOwned(live, st, a) {
		return false, errors.New("Host fault cleanup preserved a policy with conflicting ownership.")
	}
	if err := r.Client.Delete(ctx, live, client.Preconditions{UID: &live.UID, ResourceVersion: &live.ResourceVersion}); err != nil && !apierrors.IsNotFound(err) {
		return false, errors.New("Cannot remove the owned host fault policy.")
	}
	return false, nil
}
