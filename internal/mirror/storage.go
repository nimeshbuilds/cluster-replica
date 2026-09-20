// Package mirror coordinates disposable, independently restored workload generations.
package mirror

import (
	"context"
	"fmt"
	"reflect"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const snapshotAPI = "snapshot.storage.k8s.io/v1"
const ownerKey = "replicove.nimeshbuilds.dev/mirror-owner"
const operationKey = "replicove.nimeshbuilds.dev/mirror-operation"

func problem(reason, message string) error { return &planner.Problem{Reason: reason, Detail: message} }
func object(kind, ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(snapshotAPI)
	u.SetKind(kind)
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}
func str(u *unstructured.Unstructured, path ...string) string {
	s, _, _ := unstructured.NestedString(u.Object, path...)
	return s
}
func ready(u *unstructured.Unstructured) bool {
	b, _, _ := unstructured.NestedBool(u.Object, "status", "readyToUse")
	return b
}
func mark(u client.Object, owner, op string) {
	u.SetAnnotations(map[string]string{ownerKey: owner, operationKey: op})
}
func owned(u client.Object, owner, op, uid string) bool {
	return u.GetAnnotations()[ownerKey] == owner && u.GetAnnotations()[operationKey] == op && (uid == "" || string(u.GetUID()) == uid)
}

// ensureSnapshot records intent before creation. Existing foreign objects and
// changed source handles are never adopted, even when their names match.
func (r *Reconciler) ensureSnapshot(ctx context.Context, st *state.State, desired *unstructured.Unstructured, op string, uid *string) (*unstructured.Unstructured, error) {
	live := object(desired.GetKind(), desired.GetNamespace(), desired.GetName())
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(live), live)
	if apierrors.IsNotFound(err) {
		if *uid != "" {
			return nil, problem("SnapshotMissing", "An inventoried snapshot was removed; it will not be silently replaced.")
		}
		mark(desired, st.OwnerUID, op)
		if err = r.Client.Create(ctx, desired); err != nil {
			return nil, problem("SnapshotCreateFailed", "Snapshot creation failed; check delegated snapshot permissions and controllers.")
		}
		live = desired
	} else if err != nil {
		return nil, problem("SnapshotUnavailable", "Snapshot APIs or permissions are unavailable.")
	}
	if !owned(live, st.OwnerUID, op, *uid) || !containsSpec(desired.Object["spec"], live.Object["spec"]) {
		return nil, problem("SnapshotOwnershipConflict", "Snapshot identity or specification changed; no mutation was attempted.")
	}
	if *uid == "" {
		*uid = string(live.GetUID())
		if err = r.Store.Save(ctx, st); err != nil {
			return nil, err
		}
	}
	if !live.GetDeletionTimestamp().IsZero() {
		return nil, problem("SnapshotTerminating", "An owned snapshot is terminating.")
	}
	return live, nil
}
func containsSpec(want, got any) bool {
	w, ok := want.(map[string]any)
	if !ok {
		return reflect.DeepEqual(want, got)
	}
	g, ok := got.(map[string]any)
	if !ok {
		return false
	}
	for k, v := range w {
		if !containsSpec(v, g[k]) {
			return false
		}
	}
	return true
}

func (r *Reconciler) inspectVolume(ctx context.Context, ref api.NamespacedName, grant api.MirrorVolumeGrant, index int, st *state.State) (state.MirrorSnapshot, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, pvc); err != nil {
		return state.MirrorSnapshot{}, problem("SourceVolumeUnavailable", "A granted source PVC is unavailable.")
	}
	if pvc.Status.Phase != corev1.ClaimBound || pvc.Spec.VolumeName == "" || (pvc.Spec.VolumeMode != nil && *pvc.Spec.VolumeMode != corev1.PersistentVolumeFilesystem) {
		return state.MirrorSnapshot{}, problem("UnsupportedVolume", "CSI mirrors require a bound filesystem PVC.")
	}
	pv := &corev1.PersistentVolume{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: pvc.Spec.VolumeName}, pv); err != nil {
		return state.MirrorSnapshot{}, problem("SourceVolumeUnavailable", "Cannot verify the source PV identity.")
	}
	if pv.Spec.CSI == nil || pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID != pvc.UID {
		return state.MirrorSnapshot{}, problem("UnsupportedVolume", "The source must be a CSI volume bound to the exact granted PVC UID.")
	}
	vsc := object("VolumeSnapshotClass", "", grant.SnapshotClass)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(vsc), vsc); err != nil {
		return state.MirrorSnapshot{}, problem("SnapshotClassUnavailable", "Install the snapshot APIs and grant the selected snapshot class.")
	}
	sc := &storagev1.StorageClass{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: grant.StorageClass}, sc); err != nil {
		return state.MirrorSnapshot{}, problem("StorageClassUnavailable", "The destination storage class is unavailable.")
	}
	if str(vsc, "driver") != pv.Spec.CSI.Driver || sc.Provisioner != pv.Spec.CSI.Driver || str(vsc, "deletionPolicy") != "Delete" || sc.ReclaimPolicy == nil || *sc.ReclaimPolicy != corev1.PersistentVolumeReclaimDelete {
		return state.MirrorSnapshot{}, problem("StorageContractMismatch", "Source CSI, snapshot, and destination drivers must match; owned snapshots and destination volumes require Delete retention.")
	}
	op, err := state.OperationID()
	if err != nil {
		return state.MirrorSnapshot{}, err
	}
	return state.MirrorSnapshot{Namespace: ref.Namespace, PVCName: ref.Name, PVCUID: string(pvc.UID), PVName: pv.Name, PVUID: string(pv.UID), SourceHandle: pv.Spec.CSI.VolumeHandle, Driver: pv.Spec.CSI.Driver, StorageClass: grant.StorageClass, SnapshotClass: grant.SnapshotClass, Name: shortName("capture", st.OwnerUID, index), OperationID: op}, nil
}

func (r *Reconciler) captureVolumes(ctx context.Context, st *state.State) (bool, error) {
	for i := range st.MirrorRun.Snapshots {
		s := &st.MirrorRun.Snapshots[i]
		pvc := &corev1.PersistentVolumeClaim{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: s.PVCName}, pvc); err != nil || string(pvc.UID) != s.PVCUID || pvc.Spec.VolumeName != s.PVName {
			return false, problem("SourceVolumeChanged", "The source PVC identity changed during capture.")
		}
		desired := object("VolumeSnapshot", s.Namespace, s.Name)
		desired.Object["spec"] = map[string]any{"volumeSnapshotClassName": s.SnapshotClass, "source": map[string]any{"persistentVolumeClaimName": s.PVCName}}
		snap, err := r.ensureSnapshot(ctx, st, desired, s.OperationID, &s.UID)
		if err != nil {
			return false, err
		}
		if _, ok, _ := unstructured.NestedMap(snap.Object, "status", "error"); ok {
			return false, problem("CaptureFailed", "The CSI controller reported a snapshot failure; the previous test generation is preserved.")
		}
		if !ready(snap) {
			return false, nil
		}
		content := object("VolumeSnapshotContent", "", str(snap, "status", "boundVolumeSnapshotContentName"))
		if content.GetName() == "" {
			return false, nil
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(content), content); err != nil {
			return false, problem("SnapshotContentUnavailable", "Cannot verify snapshot content ownership.")
		}
		if str(content, "spec", "volumeSnapshotRef", "uid") != s.UID || str(content, "spec", "source", "volumeHandle") != s.SourceHandle || str(content, "spec", "driver") != s.Driver || str(content, "spec", "deletionPolicy") != "Delete" || !ready(content) {
			return false, problem("SnapshotContentMismatch", "The CSI snapshot does not match the recorded source volume and retention policy.")
		}
		handle := str(content, "status", "snapshotHandle")
		if handle == "" {
			return false, nil
		}
		if s.Handle != "" && (s.Handle != handle || s.ContentUID != string(content.GetUID())) {
			return false, problem("SnapshotContentChanged", "The captured backend snapshot identity changed.")
		}
		stamp, err := time.Parse(time.RFC3339, str(snap, "status", "creationTime"))
		if err != nil {
			return false, problem("SnapshotTimestampMissing", "The CSI driver did not report a usable recovery-point timestamp.")
		}
		s.Handle = handle
		s.ContentName = content.GetName()
		s.ContentUID = string(content.GetUID())
		s.CapturedAt = stamp
		if err := r.Store.Save(ctx, st); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Import the already authorized snapshot into the runtime namespace. Retain on
// the import prevents it from deleting the original backend capture.
func (r *Reconciler) importVolumes(ctx context.Context, st, revision *state.State) (bool, error) {
	if len(st.MirrorRun.Imports) == 0 {
		for i := range revision.MirrorRun.Snapshots {
			op, err := state.OperationID()
			if err != nil {
				return false, err
			}
			st.MirrorRun.Imports = append(st.MirrorRun.Imports, state.MirrorImport{Name: shortName("restore", st.OwnerUID, i), OperationID: op, SourceIndex: i})
		}
		if err := r.Store.Save(ctx, st); err != nil {
			return false, err
		}
	}
	for i := range st.MirrorRun.Imports {
		imp := &st.MirrorRun.Imports[i]
		s := revision.MirrorRun.Snapshots[imp.SourceIndex]
		original := object("VolumeSnapshotContent", "", s.ContentName)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(original), original); err != nil || string(original.GetUID()) != s.ContentUID || str(original, "status", "snapshotHandle") != s.Handle || !ready(original) {
			return false, problem("RevisionUnavailable", "The retained snapshot is no longer available with its recorded identity.")
		}
		snap := object("VolumeSnapshot", st.OwnerNamespace, imp.Name)
		snap.Object["spec"] = map[string]any{"source": map[string]any{"volumeSnapshotContentName": imp.Name}}
		live, err := r.ensureSnapshot(ctx, st, snap, imp.OperationID, &imp.UID)
		if err != nil {
			return false, err
		}
		content := object("VolumeSnapshotContent", "", imp.Name)
		content.Object["spec"] = map[string]any{"driver": s.Driver, "deletionPolicy": "Retain", "source": map[string]any{"snapshotHandle": s.Handle}, "sourceVolumeMode": "Filesystem", "volumeSnapshotRef": map[string]any{"name": imp.Name, "namespace": st.OwnerNamespace, "uid": imp.UID}}
		if _, err = r.ensureSnapshot(ctx, st, content, imp.OperationID, &imp.ContentUID); err != nil {
			return false, err
		}
		if !ready(live) {
			return false, nil
		}
	}
	return true, nil
}

func (r *Reconciler) deleteSnapshotObject(ctx context.Context, obj *unstructured.Unstructured, owner, op, uid string) (bool, error) {
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, problem("SnapshotCleanupUnavailable", "Cannot inspect snapshot cleanup inventory.")
	}
	if !owned(obj, owner, op, uid) {
		return false, problem("SnapshotOwnershipConflict", "A snapshot cleanup name was reused; the replacement was preserved.")
	}
	if obj.GetDeletionTimestamp().IsZero() {
		u, rv := obj.GetUID(), obj.GetResourceVersion()
		if err = r.Client.Delete(ctx, obj, client.Preconditions{UID: &u, ResourceVersion: &rv}); err != nil && !apierrors.IsNotFound(err) {
			return false, problem("SnapshotCleanupPending", "Snapshot deletion is pending; finalizers remain in effect.")
		}
	}
	return false, nil
}

func (r *Reconciler) cleanupStorage(ctx context.Context, st *state.State, captures bool) (bool, error) {
	for _, imp := range st.MirrorRun.Imports {
		done, err := r.deleteSnapshotObject(ctx, object("VolumeSnapshot", st.OwnerNamespace, imp.Name), st.OwnerUID, imp.OperationID, imp.UID)
		if err != nil || !done {
			return false, err
		}
		done, err = r.deleteSnapshotObject(ctx, object("VolumeSnapshotContent", "", imp.Name), st.OwnerUID, imp.OperationID, imp.ContentUID)
		if err != nil || !done {
			return false, err
		}
	}
	if !captures {
		return true, nil
	}
	for _, s := range st.MirrorRun.Snapshots {
		done, err := r.deleteSnapshotObject(ctx, object("VolumeSnapshot", s.Namespace, s.Name), st.OwnerUID, s.OperationID, s.UID)
		if err != nil || !done {
			return false, err
		}
		// Dynamic content is deleted by the snapshot controller. Never force-delete it.
		if s.ContentName != "" {
			c := object("VolumeSnapshotContent", "", s.ContentName)
			err = r.Client.Get(ctx, client.ObjectKeyFromObject(c), c)
			if !apierrors.IsNotFound(err) {
				if err != nil {
					return false, problem("SnapshotCleanupUnavailable", "Cannot verify backend capture cleanup.")
				}
				if string(c.GetUID()) != s.ContentUID {
					return false, problem("SnapshotOwnershipConflict", "Snapshot content identity changed during cleanup.")
				}
				return false, nil
			}
		}
	}
	return true, nil
}

func shortName(prefix, uid string, i int) string {
	return fmt.Sprintf("rm-%s-%s-%d", prefix, state.Name(uid)[len("replicove-state-"):], i)
}
