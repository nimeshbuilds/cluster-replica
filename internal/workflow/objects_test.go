package workflow

import (
	"context"
	"testing"

	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testEngine(t *testing.T) (*Engine, *state.State, *target.Connection) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	host := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "private"}, Data: map[string][]byte{"key": make([]byte, 32)}}).Build()
	store := &state.Store{Client: host, Namespace: "private"}
	st := &state.State{OwnerUID: "owner", OwnerName: "test", OwnerNamespace: "lab"}
	if err := store.Save(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	dyn.PrependReactor("create", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		obj.SetUID(types.UID("created-" + obj.GetName()))
		return false, nil, nil
	})
	return &Engine{Store: store, Client: host}, st, &target.Connection{Dynamic: dyn}
}
func TestIntentRecoveryForeignProtectionRefreshAndDelete(t *testing.T) {
	ctx := context.Background()
	engine, st, conn := testEngine(t)
	desired := state.Object{ID: "settings", APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: "guest", Name: "settings", Desired: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "settings", "namespace": "guest"}, "data": map[string]any{"mode": "initial"}}}
	if _, err := engine.apply(ctx, conn, st, desired, false); err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 1 || st.Entries[0].UID == "" {
		t.Fatal("intent or UID not persisted")
	}
	loaded, err := engine.Store.Load(ctx, st.OwnerUID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.apply(ctx, conn, loaded, desired, false); err != nil {
		t.Fatal("restart failed", err)
	}
	api := conn.Dynamic.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace("guest")
	live, _ := api.Get(ctx, "settings", metav1.GetOptions{})
	_ = unstructured.SetNestedField(live.Object, "experiment", "data", "extra")
	_, _ = api.Update(ctx, live, metav1.UpdateOptions{})
	desired.Desired = runtime.DeepCopyJSON(desired.Desired)
	desired.Desired["data"].(map[string]any)["mode"] = "updated"
	if _, err := engine.apply(ctx, conn, loaded, desired, true); err != nil {
		t.Fatal(err)
	}
	live, _ = api.Get(ctx, "settings", metav1.GetOptions{})
	extra, _, _ := unstructured.NestedString(live.Object, "data", "extra")
	if extra != "experiment" {
		t.Fatal("refresh removed experiment field")
	}
	live.SetUID("foreign")
	_, _ = api.Update(ctx, live, metav1.UpdateOptions{})
	if _, err := engine.deleteEntry(ctx, conn, loaded, &loaded.Entries[0]); err == nil {
		t.Fatal("foreign UID deleted")
	}
	live.SetUID(types.UID(loaded.Entries[0].UID))
	_, _ = api.Update(ctx, live, metav1.UpdateOptions{})
	if _, err := engine.deleteEntry(ctx, conn, loaded, &loaded.Entries[0]); err != nil {
		t.Fatal(err)
	}
	if done, err := engine.deleteEntry(ctx, conn, loaded, &loaded.Entries[0]); err != nil || !done {
		t.Fatal("cleanup not verified")
	}
	foreign := &unstructured.Unstructured{Object: desired.Desired}
	foreign.SetUID("unrelated")
	_, _ = api.Create(ctx, foreign, metav1.CreateOptions{})
	if _, err := engine.apply(ctx, conn, loaded, desired, true); err == nil {
		t.Fatal("preexisting resource adopted")
	}
}
func TestReadinessAndDesiredSubset(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"generation": int64(2)}, "spec": map[string]any{"replicas": int64(1)}, "status": map[string]any{"observedGeneration": int64(1), "readyReplicas": int64(1), "updatedReplicas": int64(1)}}}
	if Ready(obj) {
		t.Fatal("stale generation ready")
	}
	_ = unstructured.SetNestedField(obj.Object, int64(2), "status", "observedGeneration")
	if !Ready(obj) {
		t.Fatal("ready deployment rejected")
	}
	if ContainsDesired(map[string]any{"data": map[string]any{"secret": "new"}}, map[string]any{"data": map[string]any{"secret": "old"}}) {
		t.Fatal("drift not detected")
	}
	if Check(obj, "", "spec.replicas", "1") {
		t.Fatal("readiness check read non-status field")
	}
	if planner.OperationAnnotation == "" {
		t.Fatal("missing operation marker")
	}
}
