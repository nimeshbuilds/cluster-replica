package workflow

import (
	"context"
	"errors"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type databaseCleanupRuntime struct{ failure error }

func (*databaseCleanupRuntime) Ensure(context.Context, runtimeprovider.Request) (runtimeprovider.Observation, error) {
	return runtimeprovider.Observation{}, errors.New("unexpected install during cleanup")
}
func (r *databaseCleanupRuntime) Delete(context.Context, runtimeprovider.Request) error {
	return r.failure
}

func TestDatabaseIsolationCleanupWaitsForRuntimeAndHostCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	ref := catalog.Resolve("parent")
	obj := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "lab", Namespace: "lab", UID: "parent"}, Status: api.ClusterReplicaStatus{Runtime: &ref}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "translated-app", Namespace: "lab", UID: "app-uid"}}
	k := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj, pod, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "system"}, Data: map[string][]byte{"key": make([]byte, 32)}}).Build()
	s := &state.State{OwnerUID: "parent", OwnerName: "lab", OwnerNamespace: "lab", Provider: "helm", GuestCleaned: true, Databases: []state.Database{{Name: "db"}}, HostEntries: []state.Entry{{APIVersion: "v1", Kind: "Pod", Resource: "pods", Namespace: "lab", Name: pod.Name, UID: string(pod.UID)}}}
	store := &state.Store{Client: k, Namespace: "system"}
	ctx := context.Background()
	if err := store.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	provider := &databaseCleanupRuntime{failure: errors.New("runtime still terminating")}
	calls := 0
	e := &Engine{Client: k, Store: store, Runtime: provider, DatabaseIsolationCleanup: func(context.Context, *state.State) (bool, error) {
		calls++
		if err := k.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{}); !apierrors.IsNotFound(err) {
			t.Fatal("isolation removed before translated host application")
		}
		return false, nil
	}}
	if _, err := e.cleanup(ctx, obj, s); err != nil || calls != 0 {
		t.Fatal("runtime failure did not retain isolation")
	}
	provider.failure = nil
	if _, err := e.cleanup(ctx, obj, s); err != nil || calls != 0 {
		t.Fatal("pending host Pod removal did not retain isolation")
	}
	if _, err := e.cleanup(ctx, obj, s); err != nil || calls != 1 {
		t.Fatal("post-runtime isolation cleanup was not called")
	}
	if remaining, err := store.Load(ctx, s.OwnerUID); err != nil || remaining == nil {
		t.Fatal("pending isolation cleanup lost protected state")
	}
	e.DatabaseIsolationCleanup = nil
	if _, err := e.cleanup(ctx, obj, s); err != nil || obj.Status.Phase != "Deleting" || obj.Status.Conditions[0].Reason != "DatabasesDisabled" {
		t.Fatal("disabling database module bypassed retained network policy cleanup")
	}
	e.DatabaseIsolationCleanup = func(context.Context, *state.State) (bool, error) { return true, nil }
	if _, err := e.cleanup(ctx, obj, s); err != nil || obj.Status.Phase != "Expired" {
		t.Fatal("verified isolation cleanup did not finish replica teardown")
	}
	if remaining, err := store.Load(ctx, s.OwnerUID); err != nil || remaining != nil {
		t.Fatal("finished cleanup retained protected state")
	}
}
