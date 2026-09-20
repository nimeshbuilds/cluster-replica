package mirror

import (
	"context"
	"reflect"
	"slices"

	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func guestNamespaces(st *state.State) []string {
	out := []string{}
	for _, o := range st.Plan.Objects {
		if o.Namespace != "" {
			out = append(out, o.Namespace)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
func selector(release string, namespaces []string) metav1.LabelSelector {
	return metav1.LabelSelector{MatchLabels: map[string]string{"vcluster.loft.sh/managed-by": release}, MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "vcluster.loft.sh/namespace", Operator: metav1.LabelSelectorOpIn, Values: namespaces}}}
}

// Policies live on the host so guest users cannot delete them through their
// virtual API. Administrators must qualify enforcement by the host CNI.
func (r *Reconciler) ensurePolicies(ctx context.Context, run, child *state.State) error {
	if !r.NetworkPolicyEnforced {
		return problem("NetworkIsolationRequired", "Host network-policy enforcement has not been qualified.")
	}
	if len(run.MirrorRun.Policies) == 0 {
		op, err := state.OperationID()
		if err != nil {
			return err
		}
		run.MirrorRun.Policies = []state.Entry{{Name: shortName("isolation", run.OwnerUID, 0), Namespace: run.OwnerNamespace, OperationID: op}}
		if err := r.Store.Save(ctx, run); err != nil {
			return err
		}
	}
	e := &run.MirrorRun.Policies[0]
	release := run.MirrorRun.RuntimeRelease
	namespaces := guestNamespaces(child)
	if release == "" || len(namespaces) == 0 {
		return problem("IsolationScopeMissing", "A mirror must contain namespaced workloads in a qualified runtime.")
	}
	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	dns := intstr.FromInt32(53)
	peer := selector(release, namespaces)
	dnsPeer := metav1.LabelSelector{MatchLabels: map[string]string{"vcluster.loft.sh/managed-by": release, "vcluster.loft.sh/namespace": "kube-system", "k8s-app": "kube-dns"}}
	apiPeer := metav1.LabelSelector{MatchLabels: map[string]string{"app": "vcluster", "release": release}}
	ports := []networkingv1.NetworkPolicyPort{}
	for _, n := range []int32{443, 6443, 8443} {
		p := intstr.FromInt32(n)
		ports = append(ports, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &p})
	}
	desired := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: e.Namespace, Name: e.Name}, Spec: networkingv1.NetworkPolicySpec{PodSelector: peer, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}, Egress: []networkingv1.NetworkPolicyEgressRule{
		{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &peer}}},
		{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &dnsPeer}}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}}},
		{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &apiPeer}}, Ports: ports},
	}}}
	mark(desired, run.OwnerUID, e.OperationID)
	live := &networkingv1.NetworkPolicy{}
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(desired), live)
	if apierrors.IsNotFound(err) {
		if e.UID != "" {
			return problem("IsolationRemoved", "The host isolation policy was removed; the mirror cannot proceed.")
		}
		if err = r.Client.Create(ctx, desired); err != nil {
			return problem("IsolationCreateFailed", "The operator cannot establish host egress isolation.")
		}
		live = desired
	} else if err != nil {
		return problem("IsolationUnavailable", "Cannot inspect the host isolation policy.")
	}
	if !owned(live, run.OwnerUID, e.OperationID, e.UID) || !reflect.DeepEqual(live.Spec, desired.Spec) {
		return problem("IsolationChanged", "The recorded host isolation policy changed; activation is blocked.")
	}
	if e.UID == "" {
		e.UID = string(live.UID)
		return r.Store.Save(ctx, run)
	}
	return nil
}

func (r *Reconciler) verifyVolumes(ctx context.Context, run, revision, child *state.State) error {
	return r.inventoryVolumes(ctx, run, revision, child, true)
}
func (r *Reconciler) inventoryVolumes(ctx context.Context, run, revision, child *state.State, requireBound bool) error {
	claims := &corev1.PersistentVolumeClaimList{}
	if err := r.Client.List(ctx, claims, client.InNamespace(run.OwnerNamespace)); err != nil {
		return problem("VolumeVerificationUnavailable", "Cannot inspect restored host PVCs.")
	}
	for _, s := range revision.MirrorRun.Snapshots {
		var targetNS, targetName string
		for _, o := range child.Plan.Objects {
			if o.Kind == "PersistentVolumeClaim" && o.SourceNamespace == s.Namespace && o.SourceName == s.PVCName {
				targetNS = o.Namespace
				targetName = o.Name
			}
		}
		var found *corev1.PersistentVolumeClaim
		for i := range claims.Items {
			p := &claims.Items[i]
			if p.Labels["vcluster.loft.sh/managed-by"] == run.MirrorRun.RuntimeRelease && p.Annotations["vcluster.loft.sh/object-namespace"] == targetNS && p.Annotations["vcluster.loft.sh/object-name"] == targetName {
				if found != nil {
					return problem("VolumeMappingAmbiguous", "Multiple host PVCs claim the restored guest identity.")
				}
				found = p
			}
		}
		if found == nil || found.Spec.VolumeName == "" {
			if requireBound {
				return problem("RestoredVolumePending", "Waiting for each restored guest PVC to bind to independent host storage.")
			}
			continue
		}
		validSource := false
		for i, snap := range revision.MirrorRun.Snapshots {
			if snap.PVCUID == s.PVCUID && i < len(run.MirrorRun.Imports) && found.Spec.DataSource != nil && found.Spec.DataSource.APIGroup != nil && *found.Spec.DataSource.APIGroup == "snapshot.storage.k8s.io" && found.Spec.DataSource.Kind == "VolumeSnapshot" && found.Spec.DataSource.Name == run.MirrorRun.Imports[i].Name {
				validSource = true
			}
		}
		if !validSource {
			return problem("RestoreSourceChanged", "The restored host claim no longer references its recorded snapshot import.")
		}
		if string(found.UID) == s.PVCUID || found.Spec.VolumeName == s.PVName {
			return problem("SourceVolumeReuseDenied", "A restored claim resolves to source storage; activation is denied.")
		}
		pv := &corev1.PersistentVolume{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: found.Spec.VolumeName}, pv); err != nil {
			if !requireBound && apierrors.IsNotFound(err) {
				continue
			}
			return problem("VolumeVerificationUnavailable", "Cannot verify the new backing volume.")
		}
		if found.Spec.StorageClassName == nil || *found.Spec.StorageClassName != s.StorageClass {
			return problem("StorageContractMismatch", "The restored storage class changed.")
		}
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != s.Driver || pv.Spec.CSI.VolumeHandle == s.SourceHandle || pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID != found.UID || pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimDelete {
			return problem("SourceVolumeReuseDenied", "The restored volume does not satisfy independent ownership and deletion requirements.")
		}
		seen := false
		for _, v := range run.MirrorRun.Volumes {
			if v.UID == string(pv.UID) {
				seen = true
			}
		}
		if !seen {
			run.MirrorRun.Volumes = append(run.MirrorRun.Volumes, state.Volume{Name: pv.Name, UID: string(pv.UID), ClaimUID: string(found.UID)})
			if err := r.Store.Save(ctx, run); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Reconciler) cleanupPolicies(ctx context.Context, st *state.State) (bool, error) {
	for _, e := range st.MirrorRun.Policies {
		p := &networkingv1.NetworkPolicy{}
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.Name}, p)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, problem("IsolationCleanupUnavailable", "Cannot inspect owned isolation policy.")
		}
		if !owned(p, st.OwnerUID, e.OperationID, e.UID) {
			return false, problem("IsolationOwnershipConflict", "An isolation policy changed identity; it was preserved.")
		}
		if err := r.Client.Delete(ctx, p, client.Preconditions{UID: &p.UID, ResourceVersion: &p.ResourceVersion}); err != nil && !apierrors.IsNotFound(err) {
			return false, problem("IsolationCleanupPending", "Waiting to remove owned isolation policy.")
		}
		return false, nil
	}
	return true, nil
}
