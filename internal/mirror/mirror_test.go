package mirror

import (
	"context"
	"fmt"
	"testing"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func fixture(t *testing.T) (*Reconciler, *api.ReplicaMirror, *api.ReplicaGrant) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	for _, kind := range []string{"VolumeSnapshot", "VolumeSnapshotContent", "VolumeSnapshotClass"} {
		scheme.AddKnownTypeWithName(schema.FromAPIVersionAndKind(snapshotAPI, kind), &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(schema.FromAPIVersionAndKind(snapshotAPI, kind+"List"), &unstructured.UnstructuredList{})
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	seq := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&api.ReplicaMirror{}, &api.ReplicaMirrorRun{}, &api.ClusterReplica{}).WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "system", Name: state.KeySecret}, Data: map[string][]byte{"key": make([]byte, 32)}}).WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
		seq++
		if o.GetUID() == "" {
			o.SetUID(types.UID(fmt.Sprintf("generated-%d", seq)))
		}
		if o.GetCreationTimestamp().Time.IsZero() {
			o.SetCreationTimestamp(metav1.NewTime(now))
		}
		return c.Create(ctx, o, opts...)
	}}).Build()
	r := &Reconciler{Client: c, Store: &state.Store{Client: c, Namespace: "system"}, Namespace: "lab", NetworkPolicyEnforced: true, Now: func() time.Time { return now }}
	m := &api.ReplicaMirror{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "orders", UID: "mirror-uid", CreationTimestamp: metav1.NewTime(now)}, Spec: api.ReplicaMirrorSpec{Template: api.ClusterReplicaSpec{Profile: catalog.Profile, TTL: "2h", CleanupPolicy: "DeleteOwned", GrantRef: "source-grant", Replication: &api.ReplicationSpec{Data: "EmptyVolumes", Namespaces: []string{"source"}, NamespaceMap: map[string]string{"source": "test"}}}, Volumes: []api.NamespacedName{{Namespace: "source", Name: "data"}}, Consistency: "CrashConsistent", RetainRevisions: 2}}
	g := &api.ReplicaGrant{ObjectMeta: metav1.ObjectMeta{Name: "source-grant", UID: "grant-uid"}, Spec: api.ReplicaGrantSpec{TargetNamespace: "lab", SourceNamespaces: []string{"source"}, AllowEmptyVolumes: true, Resources: []api.ResourceRule{{Group: "", Kind: "PersistentVolumeClaim"}}, Mirror: &api.MirrorGrant{MinInterval: "1m", MaxRevisions: 2, Volumes: []api.MirrorVolumeGrant{{NamespacedName: api.NamespacedName{Namespace: "source", Name: "data"}, SnapshotClass: "snap", StorageClass: "storage"}}}}}
	return r, m, g
}

func TestMirrorAuthorization(t *testing.T) {
	for name, mutate := range map[string]func(*Reconciler, *api.ReplicaMirror, *api.ReplicaGrant){
		"volume data needs separate grant": func(_ *Reconciler, _ *api.ReplicaMirror, g *api.ReplicaGrant) { g.Spec.Mirror = nil },
		"no wildcard source volumes":       func(_ *Reconciler, _ *api.ReplicaMirror, g *api.ReplicaGrant) { g.Spec.Mirror.Volumes[0].Name = "*" },
		"unqualified networking":           func(r *Reconciler, _ *api.ReplicaMirror, _ *api.ReplicaGrant) { r.NetworkPolicyEnforced = false },
		"cross namespace PVC": func(_ *Reconciler, m *api.ReplicaMirror, _ *api.ReplicaGrant) {
			m.Spec.Volumes[0].Namespace = "private"
		},
		"duplicate volume": func(_ *Reconciler, m *api.ReplicaMirror, _ *api.ReplicaGrant) {
			m.Spec.Volumes = append(m.Spec.Volumes, m.Spec.Volumes[0])
		},
		"false application consistency": func(_ *Reconciler, m *api.ReplicaMirror, _ *api.ReplicaGrant) {
			m.Spec.Consistency = "ApplicationConsistent"
		},
		"follow secrets changes pinned state": func(_ *Reconciler, m *api.ReplicaMirror, _ *api.ReplicaGrant) {
			m.Spec.Template.Replication.Secrets = "Follow"
		},
		"schedule exceeds budget": func(_ *Reconciler, m *api.ReplicaMirror, g *api.ReplicaGrant) {
			g.Spec.Mirror.MinInterval = "2h"
			m.Spec.Interval = "1h"
		},
		"retention exceeds budget": func(_ *Reconciler, m *api.ReplicaMirror, _ *api.ReplicaGrant) { m.Spec.RetainRevisions = 3 },
	} {
		t.Run(name, func(t *testing.T) {
			r, m, g := fixture(t)
			mutate(r, m, g)
			if _, err := r.authorize(m, g); err == nil {
				t.Fatal("unsafe mirror authorized")
			}
		})
	}
	r, m, g := fixture(t)
	if _, err := r.authorize(m, g); err != nil {
		t.Fatal(err)
	}
}

func sourceStorage(t *testing.T, r *Reconciler) {
	t.Helper()
	ctx := context.Background()
	sc := "storage"
	deletePolicy := corev1.PersistentVolumeReclaimDelete
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "source", Name: "data", UID: "source-pvc"}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "source-pv", StorageClassName: &sc}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}}
	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "source-pv", UID: "source-pv-uid"}, Spec: corev1.PersistentVolumeSpec{ClaimRef: &corev1.ObjectReference{UID: pvc.UID}, PersistentVolumeSource: corev1.PersistentVolumeSource{CSI: &corev1.CSIPersistentVolumeSource{Driver: "fixture.csi", VolumeHandle: "production-disk"}}}}
	vsc := object("VolumeSnapshotClass", "", "snap")
	vsc.Object["driver"] = "fixture.csi"
	vsc.Object["deletionPolicy"] = "Delete"
	for _, o := range []client.Object{pvc, pv, vsc, &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: sc}, Provisioner: "fixture.csi", ReclaimPolicy: &deletePolicy}} {
		if err := r.Client.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStorageRejectsBackendAndOwnershipChanges(t *testing.T) {
	ctx := context.Background()
	r, m, g := fixture(t)
	sourceStorage(t, r)
	st := &state.State{OwnerUID: "run-uid", OwnerName: "sync", OwnerNamespace: "lab", MirrorRun: &state.MirrorRun{MirrorUID: string(m.UID)}}
	if err := r.Store.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	s, err := r.inspectVolume(ctx, m.Spec.Volumes[0], g.Spec.Mirror.Volumes[0], 0, st)
	if err != nil {
		t.Fatal(err)
	}
	st.MirrorRun.Snapshots = []state.MirrorSnapshot{s}
	if err := r.Store.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if done, err := r.captureVolumes(ctx, st); err != nil || done {
		t.Fatalf("capture without CSI completion: %v %v", done, err)
	}
	snap := object("VolumeSnapshot", "source", s.Name)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(snap), snap); err != nil {
		t.Fatal(err)
	}
	snap.Object["status"] = map[string]any{"readyToUse": true, "boundVolumeSnapshotContentName": "content", "creationTime": r.now().Format(time.RFC3339)}
	if err := r.Client.Update(ctx, snap); err != nil {
		t.Fatal(err)
	}
	content := object("VolumeSnapshotContent", "", "content")
	content.Object["spec"] = map[string]any{"volumeSnapshotRef": map[string]any{"uid": string(snap.GetUID())}, "source": map[string]any{"volumeHandle": "another-production-disk"}, "driver": "fixture.csi", "deletionPolicy": "Delete"}
	content.Object["status"] = map[string]any{"readyToUse": true, "snapshotHandle": "snapshot-a"}
	if err := r.Client.Create(ctx, content); err != nil {
		t.Fatal(err)
	}
	if _, err := r.captureVolumes(ctx, st); err == nil {
		t.Fatal("snapshot of a different source disk accepted")
	}
	_ = unstructured.SetNestedField(content.Object, "production-disk", "spec", "source", "volumeHandle")
	if err := r.Client.Update(ctx, content); err != nil {
		t.Fatal(err)
	}
	if done, err := r.captureVolumes(ctx, st); err != nil || !done {
		t.Fatalf("qualified source snapshot: %v %v", done, err)
	}
	// A replacement object cannot be deleted merely because it reuses a name.
	if err := r.Client.Delete(ctx, snap); err != nil {
		t.Fatal(err)
	}
	foreign := object("VolumeSnapshot", "source", s.Name)
	foreign.SetUID("foreign")
	mark(foreign, st.OwnerUID, s.OperationID)
	if err := r.Client.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cleanupStorage(ctx, st, true); err == nil {
		t.Fatal("foreign replacement deleted")
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(foreign), foreign); err != nil {
		t.Fatal("foreign replacement disappeared")
	}
}

func TestGenerationUsesIndependentSnapshotAndPreservesSavedPlan(t *testing.T) {
	base := &state.Plan{Objects: []state.Object{{ID: planner.ID("", "PersistentVolumeClaim", "original", "data"), APIVersion: "v1", Kind: "PersistentVolumeClaim", Resource: "persistentvolumeclaims", SourceNamespace: "source", SourceName: "data", Namespace: "original", Name: "data", Desired: map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": "data", "namespace": "original"}, "spec": map[string]any{"storageClassName": "old", "accessModes": []any{"ReadWriteOnce"}}}}}}
	child := &api.ClusterReplica{Spec: api.ClusterReplicaSpec{Replication: &api.ReplicationSpec{NamespaceMap: map[string]string{"source": "generation-b"}}}}
	snaps := []state.MirrorSnapshot{{Namespace: "source", PVCName: "data", StorageClass: "restore-sc"}}
	plan, err := generationPlan(base, snaps, []state.MirrorImport{{Name: "private-restore-snapshot"}}, child, "existing")
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{Object: plan.Objects[0].Desired}
	if u.GetNamespace() != "generation-b" || str(u, "spec", "dataSource", "name") != "private-restore-snapshot" || u.GetAnnotations()["vcluster.loft.sh/skip-translate"] != "true" || str(u, "spec", "storageClassName") != "restore-sc" {
		t.Fatalf("incorrect restore plan: %v", u.Object)
	}
	original := &unstructured.Unstructured{Object: base.Objects[0].Desired}
	if original.GetNamespace() != "original" || str(original, "spec", "dataSource", "name") != "" {
		t.Fatal("saved revision was modified")
	}
	if err := validatePlan(base, nil, "helm"); err == nil {
		t.Fatal("ungranted empty fallback accepted")
	}
}

func TestExpiredMirrorCleansWithoutCurrentGrant(t *testing.T) {
	ctx := context.Background()
	r, m, _ := fixture(t)
	m.CreationTimestamp = metav1.NewTime(r.now().Add(-3 * time.Hour))
	m.Finalizers = []string{Finalizer}
	if err := r.Client.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	st := &state.State{OwnerUID: string(m.UID), OwnerName: m.Name, OwnerNamespace: m.Namespace, Mirror: &state.Mirror{}}
	if err := r.Store.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := r.cleanup(ctx, m, st); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
		t.Fatal(err)
	}
	if m.Status.Phase != "Expired" {
		t.Fatalf("got phase %s", m.Status.Phase)
	}
	if got, err := r.Store.Load(ctx, string(m.UID)); err != nil || got != nil {
		t.Fatalf("cleanup state remains: %v %v", got, err)
	}
}

func TestRestoreImportsCannotDeleteOriginalCapture(t *testing.T) {
	ctx := context.Background()
	r, _, _ := fixture(t)
	content := object("VolumeSnapshotContent", "", "source-content")
	content.SetUID("source-content-uid")
	content.Object["status"] = map[string]any{"readyToUse": true, "snapshotHandle": "saved-handle"}
	if err := r.Client.Create(ctx, content); err != nil {
		t.Fatal(err)
	}
	revision := &state.State{MirrorRun: &state.MirrorRun{Snapshots: []state.MirrorSnapshot{{ContentName: content.GetName(), ContentUID: string(content.GetUID()), Handle: "saved-handle", Driver: "fixture.csi"}}}}
	st := &state.State{OwnerUID: "restore-run", OwnerName: "restore", OwnerNamespace: "lab", MirrorRun: &state.MirrorRun{}}
	if err := r.Store.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := r.importVolumes(ctx, st, revision); err != nil {
		t.Fatal(err)
	}
	imported := object("VolumeSnapshotContent", "", st.MirrorRun.Imports[0].Name)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(imported), imported); err != nil {
		t.Fatal(err)
	}
	if str(imported, "spec", "deletionPolicy") != "Retain" {
		t.Fatal("import could delete original backend data")
	}
	for i := 0; i < 4; i++ {
		if _, err := r.cleanupStorage(ctx, st, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(content), content); err != nil {
		t.Fatal("source capture was removed during import cleanup")
	}
}
