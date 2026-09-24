package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type recordingRunner struct {
	calls    []string
	failMask bool
}

func (r *recordingRunner) Capture(context.Context, Source, Destination) (string, error) {
	r.calls = append(r.calls, "capture")
	return strings.Repeat("a", 64), nil
}
func (r *recordingRunner) SQL(_ context.Context, d Destination, sql string) error {
	if sql == OpenSQL {
		r.calls = append(r.calls, "publish")
	} else {
		r.calls = append(r.calls, "mask")
		if r.failMask {
			return errors.New("sensitive row")
		}
	}
	return nil
}
func (r *recordingRunner) Copy(context.Context, Destination, Destination, int64) (string, error) {
	r.calls = append(r.calls, "sanitized-copy")
	return strings.Repeat("b", 64), nil
}

func controllerFixture(t *testing.T) (*Controller, *api.ClusterReplica, *api.ReplicaGrant, *state.State, *target.Connection, *recordingRunner) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	_ = storagev1.AddToScheme(scheme)
	s := &state.State{OwnerUID: "replica-uid", OwnerName: "lab", OwnerNamespace: "lab", Provider: "helm", Plan: &state.Plan{Revision: "r1", Objects: []state.Object{{Namespace: "test", Kind: "Deployment", Name: "app"}}}}
	d := state.Database{Name: "test-db", Namespace: "test"}
	hostObjects := []runtime.Object{&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "standard"}, Provisioner: "test.example/csi", ReclaimPolicy: ptr(corev1.PersistentVolumeReclaimDelete)}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "system"}, Data: map[string][]byte{"key": []byte(strings.Repeat("k", 32))}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "source-reader", Namespace: "system"}, Data: map[string][]byte{"username": []byte("reader"), "password": []byte("private")}}}
	guestPods := []runtime.Object{}
	for _, stage := range []bool{true, false} {
		name := base(&d)
		if stage {
			name += "-stage"
		}
		hostObjects = append(hostObjects, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-host", Namespace: "lab", Labels: hostLabels(s, &d, stage), Annotations: map[string]string{"vcluster.loft.sh/object-name": name, "vcluster.loft.sh/object-namespace": "test"}}})
		guestPods = append(guestPods, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test"}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}})
	}
	host := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(hostObjects...).Build()
	store := &state.Store{Client: host, Namespace: "system"}
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	dyn.PrependReactor("create", "*", func(a ktesting.Action) (bool, runtime.Object, error) {
		o := a.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		o.SetUID(types.UID(o.GetKind() + "/" + o.GetName()))
		return false, nil, nil
	})
	conn := &target.Connection{Dynamic: dyn, Kubernetes: kubefake.NewSimpleClientset(guestPods...)}
	runner := &recordingRunner{}
	c := &Controller{Client: host, Store: store, Runner: runner}
	obj := &api.ClusterReplica{Spec: api.ClusterReplicaSpec{Replication: &api.ReplicationSpec{Databases: []api.DatabaseCopy{copyFixture()}}}}
	grant := &api.ReplicaGrant{Spec: api.ReplicaGrantSpec{SourceNamespaces: []string{"source"}, Databases: []api.DatabaseGrant{grantFixture()}}}
	return c, obj, grant, s, conn, runner
}

func TestPreparationPublishesOnlyAfterSanitizedCopyAndStageDeletion(t *testing.T) {
	c, o, g, s, conn, r := controllerFixture(t)
	ctx := context.Background()
	ready, err := c.Prepare(ctx, o, g, s, conn)
	if err != nil || ready {
		t.Fatalf("first prepare: %v %v", ready, err)
	}
	if strings.Join(r.calls, ",") != "capture,mask,sanitized-copy" {
		t.Fatal(r.calls)
	}
	if _, err := conn.Dynamic.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"}).Namespace("test").Get(ctx, "test-db", metav1.GetOptions{}); err == nil {
		t.Fatal("service published while cleanup pending")
	}
	if err := c.Client.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "replicove-db-test-db-stage-host", Namespace: "lab"}}); err != nil {
		t.Fatal(err)
	}
	ready, err = c.Prepare(ctx, o, g, s, conn)
	if err != nil || !ready {
		t.Fatalf("second prepare: %v %v", ready, err)
	}
	if strings.Join(r.calls, ",") != "capture,mask,sanitized-copy,publish" {
		t.Fatal(r.calls)
	}
	if s.Databases[0].Salt != "" {
		t.Fatal("salt should be discarded after sanitization")
	}
	_, err = c.Prepare(ctx, o, g, s, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 4 {
		t.Fatal("source recaptured after readiness")
	}
}

func TestMaskFailureDoesNotPublishOrRecapture(t *testing.T) {
	c, o, g, s, conn, r := controllerFixture(t)
	r.failMask = true
	for i := 0; i < 2; i++ {
		ready, err := c.Prepare(context.Background(), o, g, s, conn)
		if ready || !errors.Is(err, ErrFailed) {
			t.Fatalf("failure accepted: %v %v", ready, err)
		}
	}
	if strings.Join(r.calls, ",") != "capture,mask" {
		t.Fatal(r.calls)
	}
	if s.Databases[0].Phase != "Failed" {
		t.Fatal("missing durable failure")
	}
}

func TestInterruptedCaptureIsTerminal(t *testing.T) {
	c, o, g, s, conn, r := controllerFixture(t)
	s.Databases = []state.Database{{Name: "test-db", Namespace: "test", Grant: "accounts", PlanRevision: "r1", Phase: "Copying"}}
	ready, err := c.Prepare(context.Background(), o, g, s, conn)
	if ready || !errors.Is(err, ErrFailed) || len(r.calls) != 0 {
		t.Fatal("interrupted capture repeated")
	}
}

func TestHostAllowPolicyBlocksBeforeSourceRead(t *testing.T) {
	c, o, g, s, conn, r := controllerFixture(t)
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: "lab"}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}, Egress: []networkingv1.NetworkPolicyEgressRule{{}}}}
	if err := c.Client.Create(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	ready, err := c.Prepare(context.Background(), o, g, s, conn)
	if ready || !errors.Is(err, ErrDenied) || len(r.calls) != 0 {
		t.Fatal("overlapping allow policy accepted")
	}
}
