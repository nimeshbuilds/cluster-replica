package workflow

import (
	"context"

	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Host cleanup uses ownerReference UID reachability, never a name prefix or a
// mutable label alone. The UID inventory survives removal of its runtime root.
func (w *Engine) inventoryHost(ctx context.Context, st *state.State) error {
	if st.RuntimeRootUID == "" {
		return nil
	}
	kinds := [][3]string{{"v1", "Pod", "pods"}, {"v1", "Service", "services"}, {"v1", "Secret", "secrets"}, {"v1", "ConfigMap", "configmaps"}, {"v1", "PersistentVolumeClaim", "persistentvolumeclaims"}, {"apps/v1", "ReplicaSet", "replicasets"}, {"apps/v1", "Deployment", "deployments"}, {"apps/v1", "StatefulSet", "statefulsets"}, {"apps/v1", "DaemonSet", "daemonsets"}, {"batch/v1", "Job", "jobs"}, {"batch/v1", "CronJob", "cronjobs"}}
	objects := []unstructured.Unstructured{}
	plurals := map[string]string{}
	for _, kind := range kinds {
		list := &unstructured.UnstructuredList{}
		list.SetAPIVersion(kind[0])
		list.SetKind(kind[1] + "List")
		if err := w.Client.List(ctx, list, client.InNamespace(st.OwnerNamespace)); err != nil {
			return failure("HostInventoryUnavailable", "Cannot inspect all required host resource types during cleanup.")
		}
		for i := range list.Items {
			list.Items[i].SetAPIVersion(kind[0])
			list.Items[i].SetKind(kind[1])
			plurals[kind[0]+"/"+kind[1]] = kind[2]
		}
		objects = append(objects, list.Items...)
	}
	known := map[string]bool{st.RuntimeRootUID: true}
	if st.RuntimeWorkloadUID != "" {
		known[st.RuntimeWorkloadUID] = true
	}
	for _, e := range st.HostEntries {
		known[e.UID] = true
	}
	changed := false
	for again := true; again; {
		again = false
		for _, obj := range objects {
			if known[string(obj.GetUID())] {
				continue
			}
			owned := false
			for uid := range known {
				if target.OwnedBy(obj.GetOwnerReferences(), uid) {
					owned = true
					break
				}
			}
			if !owned {
				continue
			}
			known[string(obj.GetUID())] = true
			again = true
			changed = true
			st.HostEntries = append(st.HostEntries, state.Entry{Type: "host", APIVersion: obj.GetAPIVersion(), Kind: obj.GetKind(), Resource: plurals[obj.GetAPIVersion()+"/"+obj.GetKind()], Namespace: obj.GetNamespace(), Name: obj.GetName(), UID: string(obj.GetUID())})
		}
	}
	for _, obj := range objects {
		if obj.GetKind() != "PersistentVolumeClaim" || !known[string(obj.GetUID())] {
			continue
		}
		name, ok, _ := unstructured.NestedString(obj.Object, "spec", "volumeName")
		if !ok || name == "" {
			continue
		}
		pv := &unstructured.Unstructured{}
		pv.SetAPIVersion("v1")
		pv.SetKind("PersistentVolume")
		if err := w.Client.Get(ctx, client.ObjectKey{Name: name}, pv); err != nil {
			return failure("VolumeInventoryUnavailable", "Cannot inspect a bound replica PersistentVolume before cleanup.")
		}
		claimUID, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "uid")
		if claimUID != string(obj.GetUID()) {
			return failure("VolumeOwnershipConflict", "A bound volume points to a different claim UID.")
		}
		reclaim, _, _ := unstructured.NestedString(pv.Object, "spec", "persistentVolumeReclaimPolicy")
		if reclaim != "Delete" {
			return failure("VolumeRetained", "A replica volume has Retain reclaim policy; its administrator must resolve retention before cleanup can be verified.")
		}
		seen := false
		for _, volume := range st.Volumes {
			if volume.UID == string(pv.GetUID()) {
				seen = true
			}
		}
		if !seen {
			st.Volumes = append(st.Volumes, state.Volume{Name: name, UID: string(pv.GetUID()), ClaimUID: claimUID})
			changed = true
		}
	}

	if changed {
		return w.Store.Save(ctx, st)
	}
	return nil
}
func (w *Engine) hostCleanup(ctx context.Context, st *state.State) (bool, error) {
	if err := w.inventoryHost(ctx, st); err != nil {
		return false, err
	}
	done := true
	for i := range st.HostEntries {
		e := &st.HostEntries[i]
		if e.Deleted {
			continue
		}
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(e.APIVersion)
		obj.SetKind(e.Kind)
		err := w.Client.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.Name}, obj)
		if apierrors.IsNotFound(err) {
			e.Deleted = true
			continue
		}
		if err != nil {
			return false, failure("HostCleanupUnavailable", "Cannot inspect host cleanup inventory.")
		}
		if string(obj.GetUID()) != e.UID {
			return false, failure("OwnershipConflict", "A host inventory name was reused with a new UID; it was preserved.")
		}
		done = false
		if obj.GetDeletionTimestamp() != nil {
			continue
		}
		uid, rv := obj.GetUID(), obj.GetResourceVersion()
		propagation := metav1.DeletePropagationForeground
		if err := w.Client.Delete(ctx, obj, client.Preconditions{UID: &uid, ResourceVersion: &rv}, client.PropagationPolicy(propagation)); err != nil && !apierrors.IsNotFound(err) {
			return false, failure("HostCleanupPending", "A host object could not be removed; finalizers remain in effect.")
		}
	}
	for _, volume := range st.Volumes {
		pv := &unstructured.Unstructured{}
		pv.SetAPIVersion("v1")
		pv.SetKind("PersistentVolume")
		err := w.Client.Get(ctx, client.ObjectKey{Name: volume.Name}, pv)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, failure("VolumeCleanupUnavailable", "Cannot verify PersistentVolume deletion.")
		}
		if string(pv.GetUID()) != volume.UID {
			return false, failure("VolumeOwnershipConflict", "A volume name was reused; it was preserved.")
		}
		done = false
	}

	if err := w.Store.Save(ctx, st); err != nil {
		return false, err
	}
	return done, nil
}
