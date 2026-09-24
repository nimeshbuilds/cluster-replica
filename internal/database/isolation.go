package database

import (
	"context"
	"reflect"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func hostLabels(st *state.State, d *state.Database, stage bool) map[string]string {
	l := labelsForRole(d, stage)
	l["vcluster.loft.sh/managed-by"] = catalog.Resolve(st.OwnerUID).ReleaseName
	l["vcluster.loft.sh/namespace"] = d.Namespace
	return l
}
func labelsForRole(d *state.Database, stage bool) map[string]string { return labels(d, stage) }
func (c *Controller) hostPolicies(ctx context.Context, st *state.State, d *state.Database, open bool) error {
	all := &networkingv1.NetworkPolicyList{}
	if c.Client.List(ctx, all, client.InNamespace(st.OwnerNamespace)) != nil {
		return ErrFailed
	}
	for _, stage := range []bool{true, false} {
		name := base(d) + "-" + state.Name(st.OwnerUID)[16:24]
		if stage {
			name += "-stage"
		}
		selector := hostLabels(st, d, stage)
		for _, p := range all.Items {
			if p.Name == name {
				continue
			}
			sel, err := metav1.LabelSelectorAsSelector(&p.Spec.PodSelector)
			if err != nil {
				return ErrDenied
			}
			if sel.Matches(klabels.Set(selector)) && (len(p.Spec.Ingress) > 0 || len(p.Spec.Egress) > 0) {
				return ErrDenied
			}
		}
		i := -1
		for j := range d.HostEntries {
			if d.HostEntries[j].Name == name {
				i = j
				break
			}
		}
		if i < 0 {
			op, err := state.OperationID()
			if err != nil {
				return err
			}
			d.HostEntries = append(d.HostEntries, state.Entry{Name: name, Namespace: st.OwnerNamespace, Kind: "NetworkPolicy", APIVersion: "networking.k8s.io/v1", Resource: "networkpolicies", OperationID: op})
			i = len(d.HostEntries) - 1
			if err := c.Store.Save(ctx, st); err != nil {
				return err
			}
		}
		e := &d.HostEntries[i]
		desired := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: st.OwnerNamespace, Annotations: map[string]string{planner.OwnerAnnotation: st.OwnerUID, planner.OperationAnnotation: e.OperationID}}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: selector}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}}}
		if open && !stage {
			peer := metav1.LabelSelector{MatchLabels: map[string]string{"vcluster.loft.sh/managed-by": catalog.Resolve(st.OwnerUID).ReleaseName, "vcluster.loft.sh/namespace": d.Namespace}}
			desired.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &peer}}, Ports: []networkingv1.NetworkPolicyPort{{Port: ptr(intstr.FromInt32(5432)), Protocol: ptr(corev1.ProtocolTCP)}}}}
		}
		live := &networkingv1.NetworkPolicy{}
		err := c.Client.Get(ctx, client.ObjectKeyFromObject(desired), live)
		if apierrors.IsNotFound(err) {
			if e.UID != "" {
				return ErrOwnership
			}
			if c.Client.Create(ctx, desired) != nil {
				return ErrFailed
			}
			live = desired
		} else if err != nil {
			return ErrFailed
		}
		if (e.UID != "" && e.UID != string(live.UID)) || live.Annotations[planner.OwnerAnnotation] != st.OwnerUID || live.Annotations[planner.OperationAnnotation] != e.OperationID {
			return ErrOwnership
		}
		if !reflect.DeepEqual(live.Spec, desired.Spec) {
			// Only the verified sanitized phase may open an existing deny policy.
			closed := desired.DeepCopy()
			closed.Spec.Ingress = nil
			if !open || !reflect.DeepEqual(live.Spec, closed.Spec) {
				return ErrOwnership
			}
			live.Spec = desired.Spec
			if c.Client.Update(ctx, live) != nil {
				return ErrFailed
			}
		}
		if e.UID == "" {
			e.UID = string(live.UID)
			if err := c.Store.Save(ctx, st); err != nil {
				return err
			}
		}
	}
	return nil
}

// Verify translated host labels before any source bytes enter the staging Pod.
func (c *Controller) verifyHostPods(ctx context.Context, st *state.State, d *state.Database) error {
	list := &corev1.PodList{}
	if c.Client.List(ctx, list, client.InNamespace(st.OwnerNamespace)) != nil {
		return ErrFailed
	}
	policies := &networkingv1.NetworkPolicyList{}
	if c.Client.List(ctx, policies, client.InNamespace(st.OwnerNamespace)) != nil {
		return ErrFailed
	}
	for _, stage := range []bool{true, false} {
		name := base(d)
		if stage {
			name += "-stage"
		}
		count := 0
		for _, p := range list.Items {
			if p.Annotations["vcluster.loft.sh/object-namespace"] != d.Namespace || p.Annotations["vcluster.loft.sh/object-name"] != name {
				continue
			}
			count++
			for k, v := range hostLabels(st, d, stage) {
				if p.Labels[k] != v {
					return ErrDenied
				}
			}
			for _, policy := range policies.Items {
				selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
				if err != nil {
					return ErrDenied
				}
				if selector.Matches(klabels.Set(p.Labels)) && (len(policy.Spec.Ingress) > 0 || len(policy.Spec.Egress) > 0) {
					return ErrDenied
				}
			}
		}
		if count != 1 {
			return ErrFailed
		}
	}
	return nil
}
func (c *Controller) cleanupHostPolicies(ctx context.Context, st *state.State, d *state.Database) (bool, error) {
	for i := range d.HostEntries {
		e := &d.HostEntries[i]
		if e.Deleted {
			continue
		}
		live := &networkingv1.NetworkPolicy{}
		err := c.Client.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.Name}, live)
		if apierrors.IsNotFound(err) {
			e.Deleted = true
			if err := c.Store.Save(ctx, st); err != nil {
				return false, err
			}
			continue
		}
		if err != nil {
			return false, ErrFailed
		}
		if (e.UID != "" && e.UID != string(live.UID)) || live.Annotations[planner.OwnerAnnotation] != st.OwnerUID || live.Annotations[planner.OperationAnnotation] != e.OperationID {
			return false, ErrOwnership
		}
		if c.Client.Delete(ctx, live, client.Preconditions{UID: &live.UID, ResourceVersion: &live.ResourceVersion}) != nil {
			return false, ErrFailed
		}
		return false, nil
	}
	return true, nil
}

// vCluster guest deletion can precede host deletion. Raw data and its host
// policy remain private until the translated Pod has actually disappeared.
func (c *Controller) hostPodsGone(ctx context.Context, st *state.State, d *state.Database, stageOnly bool) (bool, error) {
	list := &corev1.PodList{}
	if c.Client.List(ctx, list, client.InNamespace(st.OwnerNamespace)) != nil {
		return false, ErrFailed
	}
	for _, p := range list.Items {
		if p.Annotations["vcluster.loft.sh/object-namespace"] != d.Namespace {
			continue
		}
		name := p.Annotations["vcluster.loft.sh/object-name"]
		if name != base(d)+"-stage" && (stageOnly || name != base(d)) {
			continue
		}
		if p.Labels["vcluster.loft.sh/managed-by"] != catalog.Resolve(st.OwnerUID).ReleaseName {
			return false, ErrOwnership
		}
		return false, nil
	}
	return true, nil
}
