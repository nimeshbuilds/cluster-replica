package mirror

import (
	"context"
	"fmt"
	"testing"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func save(t *testing.T, r *Reconciler, st *state.State) {
	t.Helper()
	if st.OwnerName == "" {
		st.OwnerName = st.OwnerUID
	}
	if st.OwnerNamespace == "" {
		st.OwnerNamespace = "lab"
	}
	if err := r.Store.Save(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}
func create(t *testing.T, r *Reconciler, obj client.Object) {
	t.Helper()
	if err := r.Client.Create(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
}

func TestChildCreationIntentRejectsForgedGeneration(t *testing.T) {
	ctx := context.Background()
	r, m, g := fixture(t)
	scope, err := r.authorize(m, g)
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{OwnerUID: "run-a", OwnerName: "run", OwnerNamespace: m.Namespace, MirrorRun: &state.MirrorRun{MirrorUID: string(m.UID)}}
	save(t, r, st)
	forged := mirrorTemplate(m)
	forged.Name = shortName("generation", st.OwnerUID, 0)
	forged.Annotations = map[string]string{RunAnnotation: st.OwnerUID}
	forged.Spec.GrantRef = "another-grant"
	if err := controllerutil.SetControllerReference(m, forged, r.Client.Scheme()); err != nil {
		t.Fatal(err)
	}
	create(t, r, forged)
	if _, err := r.child(ctx, m, scope, st, r.now().Add(168*time.Hour)); err == nil {
		t.Fatal("forged request adopted")
	}
	if st.MirrorRun.Child.UID != "" {
		t.Fatal("foreign child inventoried")
	}
	if err := r.Client.Delete(ctx, forged); err != nil {
		t.Fatal(err)
	}
	child, err := r.child(ctx, m, scope, st, r.now().Add(168*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if child.Spec.TTL != "168h" || child.Annotations[operationKey] == "" {
		t.Fatalf("missing bounded creation intent: %+v", child)
	}
	// Replay after a crash adopts only the exact operation and UID.
	again, err := r.child(ctx, m, scope, st, r.now().Add(168*time.Hour))
	if err != nil || again.UID != child.UID {
		t.Fatalf("idempotent creation: %v", err)
	}
}

func TestDeletedActiveResetStillPinsItsCapture(t *testing.T) {
	ctx := context.Background()
	r, m, _ := fixture(t)
	m.Spec.RetainRevisions = 1
	parent := &state.State{OwnerUID: string(m.UID), OwnerNamespace: m.Namespace, OwnerName: m.Name, Mirror: &state.Mirror{ActiveUID: "reset", Runs: []state.MirrorRef{{Name: "capture", UID: "capture"}, {Name: "newer", UID: "newer"}, {Name: "reset", UID: "reset"}}}}
	save(t, r, parent)
	for _, id := range []string{"capture", "newer", "reset"} {
		revision := id
		action := "Sync"
		if id == "reset" {
			revision = "capture"
			action = "Reset"
		}
		st := &state.State{OwnerUID: id, OwnerName: id, OwnerNamespace: m.Namespace, MirrorRun: &state.MirrorRun{MirrorUID: string(m.UID), RevisionUID: revision, Phase: "Retained", CapturedAt: r.now()}}
		save(t, r, st)
		run := &api.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: m.Namespace, UID: types.UID(id), Finalizers: []string{Finalizer}}, Spec: api.ReplicaMirrorRunSpec{MirrorRef: api.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}, Action: action}}
		create(t, r, run)
		if id == "reset" {
			if err := r.Client.Delete(ctx, run); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < 4; i++ {
		if _, err := r.collect(ctx, m, parent, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"capture", "reset"} {
		got, err := r.Store.Load(ctx, id)
		if err != nil || got == nil {
			t.Fatalf("deleted active reset lost protected %s: %v", id, err)
		}
	}
}

func TestEnrollmentRecoversDeletionBetweenJournalWrites(t *testing.T) {
	ctx := context.Background()
	r, m, _ := fixture(t)
	parent := &state.State{OwnerUID: string(m.UID), OwnerNamespace: m.Namespace, OwnerName: m.Name, Mirror: &state.Mirror{}}
	save(t, r, parent)
	run := &api.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Name: "cancelled", Namespace: m.Namespace, Finalizers: []string{Finalizer}}, Spec: api.ReplicaMirrorRunSpec{MirrorRef: api.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}, Action: "Sync"}}
	create(t, r, run)
	st := &state.State{OwnerUID: string(run.UID), OwnerName: run.Name, OwnerNamespace: run.Namespace, MirrorRun: &state.MirrorRun{MirrorUID: parent.OwnerUID, Phase: "Queued"}}
	save(t, r, st)
	if err := r.Client.Delete(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := r.enrollRuns(ctx, m, parent); err != nil {
		t.Fatal(err)
	}
	if len(parent.Mirror.Runs) != 1 {
		t.Fatal("cancelled journal was orphaned")
	}
	for i := 0; i < 3; i++ {
		if _, err := r.collect(ctx, m, parent, false); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := r.Store.Load(ctx, st.OwnerUID); err != nil || got != nil {
		t.Fatal("cancelled protected state leaked")
	}
}

func TestLeaseReleaseRevalidatesCandidate(t *testing.T) {
	ctx := context.Background()
	r, m, g := fixture(t)
	parent := &state.State{OwnerUID: string(m.UID), Mirror: &state.Mirror{ActiveUID: "working", PendingUID: "candidate"}}
	save(t, r, parent)
	rev := &state.State{OwnerUID: "capture", Plan: &state.Plan{}, MirrorRun: &state.MirrorRun{}}
	save(t, r, rev)
	st := &state.State{OwnerUID: "candidate", OwnerNamespace: m.Namespace, MirrorRun: &state.MirrorRun{MirrorUID: parent.OwnerUID, RevisionUID: rev.OwnerUID, Phase: "AwaitingActivation", Child: state.MirrorRef{Name: "gone", UID: "gone-uid"}}}
	save(t, r, st)
	run := &api.ReplicaMirrorRun{Spec: api.ReplicaMirrorRunSpec{Action: "Reset", RevisionRef: &api.MirrorObjectRef{Name: "capture", UID: "capture"}}}
	if err := r.advance(ctx, m, g, policy.Resolution{}, parent, st, run, r.now().Add(time.Hour)); err == nil {
		t.Fatal("unavailable held candidate activated")
	}
	if parent.Mirror.ActiveUID != "working" {
		t.Fatal("working generation was replaced")
	}
}

func TestPartialRestoreCleanupWaitsForPhysicalStorage(t *testing.T) {
	ctx := context.Background()
	r, m, _ := fixture(t)
	rev := &state.State{OwnerUID: "capture", MirrorRun: &state.MirrorRun{Snapshots: []state.MirrorSnapshot{{Namespace: "source", PVCName: "data", PVCUID: "source-uid", PVName: "source-pv", SourceHandle: "source-handle", Driver: "fixture.csi", StorageClass: "storage"}}}}
	save(t, r, rev)
	run := &state.State{OwnerUID: "run", OwnerNamespace: m.Namespace, MirrorRun: &state.MirrorRun{Child: state.MirrorRef{UID: "child"}, RevisionUID: rev.OwnerUID, RuntimeRelease: "shared", Imports: []state.MirrorImport{{Name: "restore"}}}}
	save(t, r, run)
	child := &state.State{OwnerUID: "child", MirrorRunUID: "run", Plan: &state.Plan{Objects: []state.Object{{Kind: "PersistentVolumeClaim", Namespace: "generation", Name: "data", SourceNamespace: "source", SourceName: "data"}}}}
	group := "snapshot.storage.k8s.io"
	sc := "storage"
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "host-copy", Namespace: m.Namespace, Labels: map[string]string{"vcluster.loft.sh/managed-by": "shared"}, Annotations: map[string]string{"vcluster.loft.sh/object-namespace": "generation", "vcluster.loft.sh/object-name": "data"}}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "copy-pv", StorageClassName: &sc, DataSource: &corev1.TypedLocalObjectReference{APIGroup: &group, Kind: "VolumeSnapshot", Name: "restore"}}}
	create(t, r, pvc)
	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "copy-pv"}, Spec: corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{CSI: &corev1.CSIPersistentVolumeSource{Driver: "fixture.csi", VolumeHandle: "new-copy-handle"}}, ClaimRef: &corev1.ObjectReference{UID: pvc.UID}, PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete}}
	create(t, r, pv)
	if done, err := r.CleanupGeneration(ctx, child, false); err != nil || !done {
		t.Fatalf("partial inventory: %v %v", done, err)
	}
	if done, err := r.CleanupGeneration(ctx, child, true); err != nil || done {
		t.Fatalf("host PVC must finish deleting: %v %v", done, err)
	}
	if err := r.Client.Delete(ctx, pvc); err != nil {
		t.Fatal(err)
	}
	if done, err := r.CleanupGeneration(ctx, child, true); err != nil || done {
		t.Fatalf("physical PV must finish deleting: %v %v", done, err)
	}
	if err := r.Client.Delete(ctx, pv); err != nil {
		t.Fatal(err)
	}
	if done, err := r.CleanupGeneration(ctx, child, true); err != nil || !done {
		t.Fatalf("finished cleanup: %v %v", done, err)
	}
}

func TestFiveMinuteMirrorCanCreateBoundedGeneration(t *testing.T) {
	r, m, g := fixture(t)
	m.Spec.Template.TTL = "5m"
	scope, err := r.authorize(m, g)
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{OwnerUID: "short-run", MirrorRun: &state.MirrorRun{MirrorUID: string(m.UID)}}
	save(t, r, st)
	child, err := r.child(context.Background(), m, scope, st, r.now().Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if child.Spec.TTL != "5m" {
		t.Fatalf("invalid minimum child TTL: %s", child.Spec.TTL)
	}
}

func TestQueueOverflowCannotBlockOwnedCleanup(t *testing.T) {
	ctx := context.Background()
	r, m, _ := fixture(t)
	parent := &state.State{OwnerUID: string(m.UID), Mirror: &state.Mirror{}}
	for i := 0; i < 32; i++ {
		parent.Mirror.Runs = append(parent.Mirror.Runs, state.MirrorRef{Name: fmt.Sprintf("enrolled-%d", i), UID: fmt.Sprintf("uid-%d", i)})
	}
	save(t, r, parent)
	run := &api.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Name: "overflow", Namespace: m.Namespace}, Spec: api.ReplicaMirrorRunSpec{MirrorRef: api.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}, Action: "Sync"}}
	create(t, r, run)
	if err := r.enrollRuns(ctx, m, parent); err != nil {
		t.Fatal("overflow blocked cleanup:", err)
	}
	if len(parent.Mirror.Runs) != 32 {
		t.Fatal("queue exceeded bound")
	}
	if _, err := r.collect(ctx, m, parent, true); err != nil {
		t.Fatal(err)
	}
	if len(parent.Mirror.Runs) != 31 {
		t.Fatal("cleanup could not progress with overflowing queue")
	}
}

func TestDeletionRetiresResetConsumersBeforeDeletingRevision(t *testing.T) {
	ctx := context.Background()
	r, m, _ := fixture(t)
	child := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: m.Namespace, Finalizers: []string{"fixture-cleanup"}}}
	create(t, r, child)
	parent := &state.State{OwnerUID: string(m.UID), Mirror: &state.Mirror{ActiveUID: "reset", Runs: []state.MirrorRef{{Name: "capture", UID: "capture"}, {Name: "reset", UID: "reset"}}}}
	save(t, r, parent)
	capture := &state.State{OwnerUID: "capture", MirrorRun: &state.MirrorRun{MirrorUID: parent.OwnerUID, RevisionUID: "capture", CapturedAt: r.now(), Phase: "Retained"}}
	save(t, r, capture)
	reset := &state.State{OwnerUID: "reset", MirrorRun: &state.MirrorRun{MirrorUID: parent.OwnerUID, RevisionUID: "capture", Child: state.MirrorRef{Name: child.Name, UID: string(child.UID)}, Phase: "Active"}}
	save(t, r, reset)
	if done, err := r.collect(ctx, m, parent, true); err != nil || done {
		t.Fatalf("active restore retirement: %v %v", done, err)
	}
	if got, err := r.Store.Load(ctx, "capture"); err != nil || got == nil {
		t.Fatal("recovery-point inventory removed before its reset consumer finished cleanup")
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(child), child); err != nil || child.DeletionTimestamp.IsZero() {
		t.Fatal("cleanup did not start retiring the consumer")
	}
}

func TestQueuedResetPinsSelectedRevision(t *testing.T) {
	ctx := context.Background()
	r, m, _ := fixture(t)
	m.Spec.RetainRevisions = 1
	parent := &state.State{OwnerUID: string(m.UID), Mirror: &state.Mirror{ActiveUID: "newer", Runs: []state.MirrorRef{{Name: "capture", UID: "capture"}, {Name: "newer", UID: "newer"}, {Name: "queued", UID: "queued"}}}}
	save(t, r, parent)
	for _, id := range []string{"capture", "newer", "queued"} {
		st := &state.State{OwnerUID: id, MirrorRun: &state.MirrorRun{MirrorUID: parent.OwnerUID, RevisionUID: id, CapturedAt: r.now(), Phase: "Retained"}}
		run := &api.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: m.Namespace, UID: types.UID(id)}, Spec: api.ReplicaMirrorRunSpec{Action: "Sync", MirrorRef: api.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}}}
		if id == "queued" {
			st.MirrorRun.Phase = "Queued"
			st.MirrorRun.CapturedAt = time.Time{}
			run.Spec.Action = "Reset"
			run.Spec.RevisionRef = &api.MirrorObjectRef{Name: "capture", UID: "capture"}
		}
		create(t, r, run)
		save(t, r, st)
	}
	if _, err := r.collect(ctx, m, parent, false); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Store.Load(ctx, "capture"); err != nil || got == nil {
		t.Fatal("queued reset lost its selected capture")
	}
}
