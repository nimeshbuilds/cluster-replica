package capacity

import (
	"context"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentAdmissionAndCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	host := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "private"}, Data: map[string][]byte{"key": make([]byte, 32)}}).Build()
	store := &state.Store{Client: host, Namespace: "private"}
	g := &api.ReplicaGrant{ObjectMeta: metav1.ObjectMeta{UID: "grant"}, Spec: api.ReplicaGrantSpec{MaxConcurrentReplicas: 1}}
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := []string{}
	for _, id := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			obj := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: id, UID: types.UID(id), Namespace: "lab"}}
			ok, err := Acquire(ctx, store, obj, g, "existing")
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				admitted = append(admitted, id)
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if len(admitted) != 1 {
		t.Fatalf("admitted %d, expected one", len(admitted))
	}
	if err := Release(ctx, store, "lab", "foreign"); err != nil {
		t.Fatal(err)
	}
	ledger, _ := store.Load(ctx, identity("lab"))
	if len(ledger.Capacity.Reservations) != 1 {
		t.Fatal("foreign release removed reservation")
	}
	if err := Release(ctx, store, "lab", admitted[0]); err != nil {
		t.Fatal(err)
	}
	obj := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "next", UID: "next", Namespace: "lab"}}
	if ok, err := Acquire(ctx, store, obj, g, "existing"); !ok || err != nil {
		t.Fatalf("capacity not reusable: %v", err)
	}
	if ok, err := Acquire(ctx, store, obj, g, "existing"); !ok || err != nil {
		t.Fatal("same UID acquire not idempotent")
	}
}

func TestManagedRuntimeConsumesNamespaceSlotAcrossGrants(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "p"}, Data: map[string][]byte{"key": make([]byte, 32)}}).Build()
	s := &state.Store{Client: c, Namespace: "p"}
	ctx := context.Background()
	for i, id := range []string{"a", "b"} {
		o := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: "lab", UID: types.UID(id)}}
		g := &api.ReplicaGrant{ObjectMeta: metav1.ObjectMeta{UID: types.UID(id)}}
		ok, e := Acquire(ctx, s, o, g, "helm")
		if e != nil || ok != (i == 0) {
			t.Fatalf("slot admission %s=%v %v", id, ok, e)
		}
	}
}

func TestReservationIdentityFitsKubernetesLabel(t *testing.T) {
	for _, ns := range []string{"lab", strings.Repeat("a", 63)} {
		if errs := validation.IsValidLabelValue(identity(ns)); len(errs) > 0 {
			t.Fatalf("invalid protected state identity: %v", errs)
		}
	}
}
