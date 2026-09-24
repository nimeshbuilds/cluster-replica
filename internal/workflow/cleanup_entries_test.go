package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func addCleanupEntry(t *testing.T, engine *Engine, conn *target.Connection, st *state.State, version, kind, plural, namespace, name string) {
	t.Helper()
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": version, "kind": kind, "metadata": map[string]any{"name": name}}}
	obj.SetNamespace(namespace)
	desired := state.Object{ID: planner.ObjectID(obj), APIVersion: version, Kind: kind, Resource: plural, Namespace: namespace, Name: name, Desired: obj.Object}
	if _, err := engine.apply(context.Background(), conn, st, desired, false); err != nil {
		t.Fatal(err)
	}
}

// Hold deletion the way a real finalizer does. Kubernetes' namespace and PVC
// controllers are not part of a dynamic fake, so tests explicitly complete
// their garbage collection after observing the correct deletion requests.
func holdCleanupDeletion(t *testing.T, conn *target.Connection, plural string) {
	t.Helper()
	dyn := conn.Dynamic.(*dynamicfake.FakeDynamicClient)
	dyn.PrependReactor("delete", plural, func(action ktesting.Action) (bool, runtime.Object, error) {
		a := action.(ktesting.DeleteAction)
		stored, err := dyn.Tracker().Get(a.GetResource(), a.GetNamespace(), a.GetName())
		if err != nil {
			return true, nil, err
		}
		body, err := runtime.DefaultUnstructuredConverter.ToUnstructured(stored)
		if err != nil {
			return true, nil, err
		}
		obj := &unstructured.Unstructured{Object: body}
		if a.GetDeleteOptions().Preconditions == nil || a.GetDeleteOptions().Preconditions.UID == nil || *a.GetDeleteOptions().Preconditions.UID != obj.GetUID() {
			t.Error("cleanup deletion did not pin the live object UID")
		}
		now := metav1.NewTime(time.Now())
		obj.SetDeletionTimestamp(&now)
		if plural == "persistentvolumeclaims" && len(obj.GetFinalizers()) == 0 {
			obj.SetFinalizers([]string{pvcProtectionFinalizer, metav1.FinalizerDeleteDependents})
		}
		err = dyn.Tracker().Update(a.GetResource(), obj, a.GetNamespace())
		return true, nil, err
	})
}

func cleanupDeletes(conn *target.Connection, plural string) []string {
	var names []string
	for _, action := range conn.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == plural {
			names = append(names, action.(ktesting.DeleteAction).GetName())
		}
	}
	return names
}

func completeCleanupGC(t *testing.T, conn *target.Connection, version, plural, namespace, name string) {
	t.Helper()
	gv, err := schema.ParseGroupVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Dynamic.(*dynamicfake.FakeDynamicClient).Tracker().Delete(gv.WithResource(plural), namespace, name); err != nil {
		t.Fatal(err)
	}
}

func addGuestVolumeConsumer(t *testing.T, conn *target.Connection, namespace string) {
	t.Helper()
	_, err := resource(conn, "v1", "pods", namespace).Create(context.Background(), &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "test-probe", "namespace": namespace},
		"spec":   map[string]any{"volumes": []any{map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "data"}}}},
		"status": map[string]any{"phase": "Succeeded"},
	}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCleanupOwnedNamespaceUnblocksGuestAddedPVCConsumer(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", "guest")
	addCleanupEntry(t, engine, conn, st, "v1", "PersistentVolumeClaim", "persistentvolumeclaims", "guest", "data")
	addGuestVolumeConsumer(t, conn, "guest")
	holdCleanupDeletion(t, conn, "persistentvolumeclaims")
	holdCleanupDeletion(t, conn, "namespaces")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("unverified asynchronous cleanup must remain pending: done=%v err=%v", done, err)
	}
	if got := cleanupDeletes(conn, "namespaces"); len(got) != 1 || got[0] != "guest" {
		t.Fatal("PVC protection prevented requesting owned namespace deletion", got)
	}
	if got := cleanupDeletes(conn, "pods"); len(got) != 0 {
		t.Fatal("cleanup directly deleted an uninventoried guest Pod", got)
	}
	// A restart must still wait: asking Kubernetes to delete the namespace is
	// not evidence that its PVC and consumer have actually disappeared.
	loaded, err := engine.Store.Load(ctx, st.OwnerUID)
	if err != nil {
		t.Fatal(err)
	}
	if done, err := engine.cleanupGuestEntries(ctx, conn, loaded); err != nil || done {
		t.Fatalf("restart lost the outstanding cleanup: done=%v err=%v", done, err)
	}
	if loaded.GuestCleaned {
		t.Fatal("guest was marked cleaned before garbage collection")
	}
	completeCleanupGC(t, conn, "v1", "pods", "guest", "test-probe")
	completeCleanupGC(t, conn, "v1", "persistentvolumeclaims", "guest", "data")
	completeCleanupGC(t, conn, "v1", "namespaces", "", "guest")
	if done, err := engine.cleanupGuestEntries(ctx, conn, loaded); err != nil || !done {
		t.Fatalf("observed finalizer completion did not finish cleanup: done=%v err=%v", done, err)
	}
	for _, e := range loaded.Entries {
		if !e.Deleted {
			t.Fatal("an inventoried object was not observed absent", e.ID)
		}
	}
}

func TestCleanupBorrowedNamespacePreservesForeignPVCConsumer(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	st.Provider = "existing"
	_, err := resource(conn, "v1", "namespaces", "").Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "borrowed"}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	addCleanupEntry(t, engine, conn, st, "v1", "ConfigMap", "configmaps", "borrowed", "dependency")
	addCleanupEntry(t, engine, conn, st, "v1", "PersistentVolumeClaim", "persistentvolumeclaims", "borrowed", "data")
	addGuestVolumeConsumer(t, conn, "borrowed")
	holdCleanupDeletion(t, conn, "persistentvolumeclaims")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("borrowed namespace PVC must keep cleanup pending: done=%v err=%v", done, err)
	}
	for _, plural := range []string{"namespaces", "pods", "configmaps"} {
		if got := cleanupDeletes(conn, plural); len(got) != 0 {
			t.Fatal("borrowed namespace relaxed cleanup ordering", plural, got)
		}
	}
	if _, err := resource(conn, "v1", "pods", "borrowed").Get(ctx, "test-probe", metav1.GetOptions{}); err != nil {
		t.Fatal("foreign consumer disappeared", err)
	}
}

func TestCleanupNamespaceReplacementOrMarkerChangeCannotBypassPVC(t *testing.T) {
	for _, change := range []string{"uid", "owner", "operation", "deleted-inventory"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			engine, st, conn := testEngine(t)
			addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", "guest")
			addCleanupEntry(t, engine, conn, st, "v1", "ConfigMap", "configmaps", "guest", "dependency")
			addCleanupEntry(t, engine, conn, st, "v1", "PersistentVolumeClaim", "persistentvolumeclaims", "guest", "data")
			addGuestVolumeConsumer(t, conn, "guest")
			client := resource(conn, "v1", "namespaces", "")
			ns, err := client.Get(ctx, "guest", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "uid":
				ns.SetUID("replacement-uid")
			case "owner":
				annotations := ns.GetAnnotations()
				annotations[planner.OwnerAnnotation] = "different-owner"
				ns.SetAnnotations(annotations)
			case "operation":
				annotations := ns.GetAnnotations()
				annotations[planner.OperationAnnotation] = "different-operation"
				ns.SetAnnotations(annotations)
			case "deleted-inventory":
				st.Entries[0].Deleted = true
			}
			if _, err := client.Update(ctx, ns, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			holdCleanupDeletion(t, conn, "persistentvolumeclaims")
			done, err := engine.cleanupGuestEntries(ctx, conn, st)
			if done {
				t.Fatal("replacement namespace permitted verified cleanup")
			}
			if change != "deleted-inventory" {
				problem, ok := err.(*planner.Problem)
				if !ok || problem.Reason != "OwnershipConflict" {
					t.Fatalf("namespace change did not surface ownership conflict: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			for _, plural := range []string{"namespaces", "pods", "configmaps"} {
				if got := cleanupDeletes(conn, plural); len(got) != 0 {
					t.Fatal("unsafe namespace permitted later deletion", plural, got)
				}
			}
		})
	}
}

func TestCleanupDrainsMultipleOwnedNamespaces(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	for _, ns := range []string{"first", "second"} {
		addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", ns)
	}
	for _, ns := range []string{"first", "second"} {
		addCleanupEntry(t, engine, conn, st, "v1", "PersistentVolumeClaim", "persistentvolumeclaims", ns, "data")
		addGuestVolumeConsumer(t, conn, ns)
	}
	holdCleanupDeletion(t, conn, "persistentvolumeclaims")
	holdCleanupDeletion(t, conn, "namespaces")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("asynchronous namespaces must remain pending: done=%v err=%v", done, err)
	}
	if got := cleanupDeletes(conn, "namespaces"); len(got) != 1 || got[0] != "second" {
		t.Fatal("namespace cleanup did not preserve its sequential barrier", got)
	}
	completeCleanupGC(t, conn, "v1", "pods", "second", "test-probe")
	completeCleanupGC(t, conn, "v1", "persistentvolumeclaims", "second", "data")
	completeCleanupGC(t, conn, "v1", "namespaces", "", "second")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("remaining namespace must still be pending: done=%v err=%v", done, err)
	}
	if got := cleanupDeletes(conn, "namespaces"); len(got) != 2 || got[1] != "first" {
		t.Fatal("earlier namespace did not progress after the later one drained", got)
	}
	completeCleanupGC(t, conn, "v1", "pods", "first", "test-probe")
	completeCleanupGC(t, conn, "v1", "persistentvolumeclaims", "first", "data")
	completeCleanupGC(t, conn, "v1", "namespaces", "", "first")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || !done {
		t.Fatalf("verified cleanup did not finish both namespaces: done=%v err=%v", done, err)
	}
}

func TestCleanupRefreshedNamespaceKeepsEarlierControllerUntilDrained(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", "first")
	addCleanupEntry(t, engine, conn, st, "apps/v1", "Deployment", "deployments", "first", "controller")
	// Refresh can append another namespace after an earlier controller. That
	// controller may still be needed by finalizers in the new namespace.
	addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", "second")
	addCleanupEntry(t, engine, conn, st, "v1", "PersistentVolumeClaim", "persistentvolumeclaims", "second", "data")
	holdCleanupDeletion(t, conn, "persistentvolumeclaims")
	holdCleanupDeletion(t, conn, "namespaces")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("new namespace must remain pending: done=%v err=%v", done, err)
	}
	if got := cleanupDeletes(conn, "namespaces"); len(got) != 1 || got[0] != "second" {
		t.Fatal("new namespace deletion was not requested", got)
	}
	if got := cleanupDeletes(conn, "deployments"); len(got) != 0 {
		t.Fatal("pending refreshed namespace lost its earlier controller", got)
	}
}

func TestCleanupReplacedPVCStopsBeforeNamespaceDeletion(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", "guest")
	addCleanupEntry(t, engine, conn, st, "v1", "PersistentVolumeClaim", "persistentvolumeclaims", "guest", "data")
	client := resource(conn, "v1", "persistentvolumeclaims", "guest")
	pvc, err := client.Get(ctx, "data", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pvc.SetUID("replacement-pvc")
	if _, err := client.Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	done, err := engine.cleanupGuestEntries(ctx, conn, st)
	problem, ok := err.(*planner.Problem)
	if done || !ok || problem.Reason != "OwnershipConflict" {
		t.Fatalf("replacement PVC did not stop namespace cleanup: done=%v err=%v", done, err)
	}
	for _, plural := range []string{"persistentvolumeclaims", "namespaces"} {
		if got := cleanupDeletes(conn, plural); len(got) != 0 {
			t.Fatal("replacement PVC or its namespace was deleted", plural, got)
		}
	}
}

func TestCleanupPendingCustomResourceKeepsControllerAndDefinition(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", "guest")
	addCleanupEntry(t, engine, conn, st, "apiextensions.k8s.io/v1", "CustomResourceDefinition", "customresourcedefinitions", "", "widgets.fixture.test")
	addCleanupEntry(t, engine, conn, st, "apps/v1", "Deployment", "deployments", "guest", "controller")
	addCleanupEntry(t, engine, conn, st, "fixture.test/v1", "Widget", "widgets", "guest", "custom-resource")
	holdCleanupDeletion(t, conn, "widgets")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("custom-resource finalizer must keep cleanup pending: done=%v err=%v", done, err)
	}
	for _, plural := range []string{"deployments", "customresourcedefinitions", "namespaces"} {
		if got := cleanupDeletes(conn, plural); len(got) != 0 {
			t.Fatal("pending custom resource lost a cleanup dependency", plural, got)
		}
	}
}

func TestCleanupCustomPVCFinalizerKeepsItsControllerAndNamespace(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	addCleanupEntry(t, engine, conn, st, "v1", "Namespace", "namespaces", "", "guest")
	addCleanupEntry(t, engine, conn, st, "apps/v1", "Deployment", "deployments", "guest", "controller")
	addCleanupEntry(t, engine, conn, st, "v1", "PersistentVolumeClaim", "persistentvolumeclaims", "guest", "data")
	client := resource(conn, "v1", "persistentvolumeclaims", "guest")
	pvc, err := client.Get(ctx, "data", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pvc.SetFinalizers([]string{pvcProtectionFinalizer, "fixture.test/storage-cleanup"})
	if _, err := client.Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	holdCleanupDeletion(t, conn, "persistentvolumeclaims")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("custom PVC finalizer must retain its dependency barrier: done=%v err=%v", done, err)
	}
	for _, plural := range []string{"deployments", "namespaces"} {
		if got := cleanupDeletes(conn, plural); len(got) != 0 {
			t.Fatal("custom PVC finalizer lost a cleanup dependency", plural, got)
		}
	}
	// Once the responsible operator finishes its own finalizer, ordinary PVC
	// protection can be released through verified owned-namespace deletion.
	pvc, err = client.Get(ctx, "data", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pvc.SetFinalizers([]string{pvcProtectionFinalizer})
	if _, err := client.Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	holdCleanupDeletion(t, conn, "namespaces")
	if done, err := engine.cleanupGuestEntries(ctx, conn, st); err != nil || done {
		t.Fatalf("namespace cleanup must remain pending until observed complete: done=%v err=%v", done, err)
	}
	if got := cleanupDeletes(conn, "namespaces"); len(got) != 1 || got[0] != "guest" {
		t.Fatal("finished custom finalizer prevented ordinary PVC cleanup", got)
	}
}
