package database

import (
	"context"
	"reflect"
	"slices"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func applicationNamespaces(st *state.State) ([]string, error) {
	if st.Provider != "helm" || st.Plan == nil {
		return nil, ErrDenied
	}
	namespaces := []string{}
	for _, o := range st.Plan.Objects {
		if o.Namespace == "" {
			continue
		}
		if o.Namespace == "kube-system" || o.Namespace == "kube-public" || o.Namespace == "kube-node-lease" {
			return nil, ErrDenied
		}
		namespaces = append(namespaces, o.Namespace)
	}
	slices.Sort(namespaces)
	namespaces = slices.Compact(namespaces)
	if len(namespaces) == 0 {
		return nil, ErrDenied
	}
	return namespaces, nil
}

func applicationScope(st *state.State, namespaces []string) metav1.LabelSelector {
	return metav1.LabelSelector{MatchLabels: map[string]string{"vcluster.loft.sh/managed-by": catalog.Resolve(st.OwnerUID).ReleaseName}, MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "vcluster.loft.sh/namespace", Operator: metav1.LabelSelectorOpIn, Values: namespaces}}}
}

// Establish this boundary before source reads or application installation. A
// deny policy selects every planned guest namespace. A separate application-only
// allow policy deliberately does not select synthetic database Pods: policies
// are additive, so selecting them would break their zero-egress staging boundary.
func (c *Controller) ensureApplicationIsolation(ctx context.Context, st *state.State) error {
	namespaces, err := applicationNamespaces(st)
	if err != nil || len(st.Databases) == 0 {
		return ErrDenied
	}
	d := &st.Databases[0]
	if len(d.ApplicationPolicies) == 0 {
		for _, role := range []string{"deny", "apps"} {
			op, err := state.OperationID()
			if err != nil {
				return err
			}
			d.ApplicationPolicies = append(d.ApplicationPolicies, state.Entry{Name: "replicove-db-egress-" + state.Name(st.OwnerUID)[16:24] + "-" + role, Namespace: st.OwnerNamespace, APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy", Resource: "networkpolicies", OperationID: op})
		}
		if err := c.Store.Save(ctx, st); err != nil {
			return err
		}
	}
	if len(d.ApplicationPolicies) != 2 {
		return ErrOwnership
	}
	all := &networkingv1.NetworkPolicyList{}
	if err := c.Client.List(ctx, all, client.InNamespace(st.OwnerNamespace)); err != nil {
		return ErrFailed
	}
	for _, p := range all.Items {
		ours := false
		for _, e := range d.ApplicationPolicies {
			ours = ours || p.Name == e.Name
		}
		// A foreign selector can match labels introduced by a workload controller.
		// Reject all additional host egress allowances instead of guessing its
		// future intersection. Other namespaces and ingress-only rules are safe.
		if !ours && len(p.Spec.Egress) > 0 {
			return ErrDenied
		}
	}
	peer := applicationScope(st, namespaces)
	apps := *peer.DeepCopy()
	apps.MatchExpressions = append(apps.MatchExpressions, metav1.LabelSelectorRequirement{Key: "replicove.nimeshbuilds.dev/database", Operator: metav1.LabelSelectorOpDoesNotExist})
	release := catalog.Resolve(st.OwnerUID).ReleaseName
	dnsPeer := metav1.LabelSelector{MatchLabels: map[string]string{"vcluster.loft.sh/managed-by": release, "vcluster.loft.sh/namespace": "kube-system", "k8s-app": "vcluster-kube-dns"}}
	apiPeer := metav1.LabelSelector{MatchLabels: map[string]string{"app": "vcluster", "release": release}}
	dnsPorts := []networkingv1.NetworkPolicyPort{}
	for _, n := range []int32{53, 1053} {
		for _, protocol := range []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP} {
			dnsPorts = append(dnsPorts, networkingv1.NetworkPolicyPort{Port: ptr(intstr.FromInt32(n)), Protocol: ptr(protocol)})
		}
	}
	apiPorts := []networkingv1.NetworkPolicyPort{}
	for _, n := range []int32{443, 6443, 8443} {
		apiPorts = append(apiPorts, networkingv1.NetworkPolicyPort{Port: ptr(intstr.FromInt32(n)), Protocol: ptr(corev1.ProtocolTCP)})
	}
	for i := range d.ApplicationPolicies {
		e := &d.ApplicationPolicies[i]
		if e.Deleted {
			return ErrOwnership
		}
		desired := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: e.Name, Namespace: e.Namespace, Annotations: map[string]string{planner.OwnerAnnotation: st.OwnerUID, planner.OperationAnnotation: e.OperationID}}, Spec: networkingv1.NetworkPolicySpec{PodSelector: peer, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}}}
		if i == 1 {
			desired.Spec.PodSelector = apps
			desired.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
				{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &peer}}},
				{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &dnsPeer}}, Ports: dnsPorts},
				{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &apiPeer}}, Ports: apiPorts},
			}
		}
		live := &networkingv1.NetworkPolicy{}
		err := c.Client.Get(ctx, client.ObjectKeyFromObject(desired), live)
		if apierrors.IsNotFound(err) {
			if e.UID != "" {
				return ErrOwnership
			}
			if err := c.Client.Create(ctx, desired); err != nil {
				return ErrFailed
			}
			live = desired
		} else if err != nil {
			return ErrFailed
		}
		if !ownedApplicationPolicy(live, st.OwnerUID, e) || !reflect.DeepEqual(live.Spec, desired.Spec) {
			return ErrOwnership
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

func ownedApplicationPolicy(p *networkingv1.NetworkPolicy, owner string, e *state.Entry) bool {
	return (e.UID == "" || e.UID == string(p.UID)) && p.Annotations[planner.OwnerAnnotation] == owner && p.Annotations[planner.OperationAnnotation] == e.OperationID
}

// CleanupIsolation must run after guest workload and managed runtime teardown,
// not during database resource cleanup. A lagging translated application Pod must
// never regain production connectivity while it is still terminating.
func (c *Controller) CleanupIsolation(ctx context.Context, st *state.State) (bool, error) {
	if len(st.Databases) == 0 || len(st.Databases[0].ApplicationPolicies) == 0 {
		return true, nil
	}
	namespaces, err := applicationNamespaces(st)
	if err != nil {
		return false, err
	}
	pods := &corev1.PodList{}
	if c.Client.List(ctx, pods, client.InNamespace(st.OwnerNamespace), client.MatchingLabels{"vcluster.loft.sh/managed-by": catalog.Resolve(st.OwnerUID).ReleaseName}) != nil {
		return false, ErrFailed
	}
	for _, p := range pods.Items {
		if slices.Contains(namespaces, p.Labels["vcluster.loft.sh/namespace"]) {
			return false, nil
		}
	}
	for i := range st.Databases[0].ApplicationPolicies {
		e := &st.Databases[0].ApplicationPolicies[i]
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
		if !ownedApplicationPolicy(live, st.OwnerUID, e) {
			return false, ErrOwnership
		}
		if err := c.Client.Delete(ctx, live, client.Preconditions{UID: &live.UID, ResourceVersion: &live.ResourceVersion}); err != nil {
			return false, ErrFailed
		}
		return false, nil
	}
	return true, nil
}
