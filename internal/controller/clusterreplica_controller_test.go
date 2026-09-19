package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type fakeProvider struct {
	ensures, deletes     int
	ready                bool
	ensureErr, deleteErr error
	onEnsure             func(runtimeprovider.Request)
}

func (p *fakeProvider) Ensure(_ context.Context, req runtimeprovider.Request) (runtimeprovider.Observation, error) {
	p.ensures++
	if p.onEnsure != nil {
		p.onEnsure(req)
	}
	return runtimeprovider.Observation{Ready: p.ready}, p.ensureErr
}
func (p *fakeProvider) Delete(context.Context, runtimeprovider.Request) error {
	p.deletes++
	return p.deleteErr
}

type fixture struct {
	t   *testing.T
	r   *Reconciler
	p   *fakeProvider
	now time.Time
	key ctrl.Request
}

func setup(t *testing.T) *fixture {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	obj := &v1alpha1.ClusterReplica{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "lab", UID: types.UID("first-uid"), Generation: 1, CreationTimestamp: metav1.NewTime(created)},
		Spec:       v1alpha1.ClusterReplicaSpec{Profile: catalog.Profile, TTL: "1h", CleanupPolicy: "HelmReleaseOnly"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	f := &fixture{t: t, p: &fakeProvider{}, now: created.Add(time.Minute), key: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}}
	f.r = &Reconciler{Client: c, Provider: f.p, Namespace: "lab", Now: func() time.Time { return f.now }}
	return f
}
func (f *fixture) get() *v1alpha1.ClusterReplica {
	f.t.Helper()
	obj := &v1alpha1.ClusterReplica{}
	if err := f.r.Get(context.Background(), f.key.NamespacedName, obj); err != nil {
		f.t.Fatal(err)
	}
	return obj
}
func (f *fixture) reconcile() ctrl.Result {
	f.t.Helper()
	result, err := f.r.Reconcile(context.Background(), f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}
func (f *fixture) initialized() { f.reconcile(); f.reconcile() }

func TestPersistIdentityAndFinalizerBeforeAnyInstallation(t *testing.T) {
	f := setup(t)
	f.p.onEnsure = func(req runtimeprovider.Request) {
		stored := f.get()
		if !controllerutil.ContainsFinalizer(stored, Finalizer) || stored.Status.Runtime == nil || *stored.Status.Runtime != req.Reference {
			t.Fatal("side effect preceded durable identity/finalizer")
		}
	}
	f.initialized()
	if f.p.ensures != 0 {
		t.Fatal("installed before resolution was persisted")
	}
	f.reconcile()
	if f.p.ensures != 1 {
		t.Fatal("expected install")
	}
	if got := f.get().Status.ExpiresAt.Time; !got.Equal(f.get().CreationTimestamp.Add(time.Hour)) {
		t.Fatal("TTL moved away from creation time")
	}
}

func TestRestartDoesNotExtendLifetimeOrChangeRelease(t *testing.T) {
	f := setup(t)
	f.initialized()
	original := *f.get().Status.Runtime
	f.now = f.now.Add(58 * time.Minute)
	f.r = &Reconciler{Client: f.r.Client, Provider: f.p, Namespace: "lab", Now: func() time.Time { return f.now }}
	f.p.ready = true
	f.reconcile()
	if *f.get().Status.Runtime != original || f.get().Status.Phase != "RuntimeReady" {
		t.Fatal("restart lost resolved runtime")
	}
	f.now = f.get().CreationTimestamp.Add(time.Hour)
	f.reconcile()
	if f.p.deletes != 1 || f.get().Status.Phase != "Expired" {
		t.Fatal("request failed to expire at original deadline")
	}
	before := f.p.ensures
	f.reconcile()
	if f.p.ensures != before {
		t.Fatal("expired request was resurrected")
	}
}

func TestExpiredBeforeFirstReconcileNeverInstalls(t *testing.T) {
	f := setup(t)
	f.now = f.now.Add(2 * time.Hour)
	f.reconcile()
	if f.p.ensures != 0 || f.p.deletes != 0 || f.get().Status.Phase != "Expired" {
		t.Fatal("stale request provisioned a new runtime")
	}
}

func TestCleanupFailureKeepsFinalizerAndRetries(t *testing.T) {
	f := setup(t)
	f.initialized()
	if err := f.r.Delete(context.Background(), f.get()); err != nil {
		t.Fatal(err)
	}
	f.p.deleteErr = errors.New("API unavailable")
	result := f.reconcile()
	if !controllerutil.ContainsFinalizer(f.get(), Finalizer) || result.RequeueAfter == 0 {
		t.Fatal("failed cleanup dropped its finalizer or retry")
	}
	f.p.deleteErr = nil
	f.reconcile()
	err := f.r.Get(context.Background(), f.key.NamespacedName, &v1alpha1.ClusterReplica{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected finalized deletion, got %v", err)
	}
	if f.p.ensures != 0 {
		t.Fatal("deletion tried to provision")
	}
}

func TestExpiryCleanupFailureCannotReportExpired(t *testing.T) {
	f := setup(t)
	f.initialized()
	f.now = f.now.Add(2 * time.Hour)
	f.p.deleteErr = runtimeprovider.ErrOwnership
	f.reconcile()
	if f.get().Status.Phase == "Expired" {
		t.Fatal("claimed expiry completed despite ownership conflict")
	}
	f.p.deleteErr = nil
	f.reconcile()
	if f.get().Status.Phase != "Expired" {
		t.Fatal("cleanup did not recover")
	}
}

func TestRuntimeErrorsDoNotExposeSecretMaterial(t *testing.T) {
	f := setup(t)
	f.initialized()
	f.p.ensureErr = errors.New("password=SUPER_SECRET https://user:password@host")
	f.reconcile()
	obj := f.get()
	if obj.Status.Phase != "Blocked" {
		t.Fatal("failed runtime reported success")
	}
	for _, c := range obj.Status.Conditions {
		if strings.Contains(c.Message, "SUPER_SECRET") || strings.Contains(c.Message, "https://") {
			t.Fatal("raw provider error leaked")
		}
	}
}

func TestNamespaceBoundaryAndMissingResource(t *testing.T) {
	f := setup(t)
	f.r.Namespace = "different"
	f.reconcile()
	if len(f.get().Finalizers) != 0 || f.p.ensures != 0 {
		t.Fatal("touched request outside grant")
	}
	f.r.Namespace = "lab"
	f.key.Name = "missing"
	f.reconcile()
}

func TestTamperedReleaseIdentityBlocksProvisionAndDeletion(t *testing.T) {
	f := setup(t)
	f.initialized()
	obj := f.get()
	obj.Status.Runtime.ReleaseName = "someone-elses-cluster"
	if err := f.r.Status().Update(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
	f.reconcile()
	f.now = f.now.Add(2 * time.Hour)
	f.reconcile()
	if f.p.ensures != 0 || f.p.deletes != 0 {
		t.Fatal("acted on unrelated runtime identity")
	}
}

func TestCleanupDoesNotRequireCurrentChartProfile(t *testing.T) {
	f := setup(t)
	f.initialized()
	obj := f.get()
	obj.Status.Runtime.ChartVersion = "old-unavailable-chart"
	if err := f.r.Status().Update(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * time.Hour)
	f.reconcile()
	if f.p.deletes != 1 {
		t.Fatal("cleanup depended on current catalog availability")
	}
}
