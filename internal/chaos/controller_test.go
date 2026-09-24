package chaos

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const approvedImage = "docker.io/library/python@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fixture struct {
	r      *Reconciler
	x      *api.ReplicaExperiment
	p      *api.ClusterReplica
	g      *api.ReplicaGrant
	parent *state.State
	conn   *target.Connection
	dyn    *dynamicfake.FakeDynamicClient
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{api.AddToScheme, corev1.AddToScheme, appsv1.AddToScheme, batchv1.AddToScheme, networkingv1.AddToScheme} {
		if err := add(s); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{now: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	f.g = &api.ReplicaGrant{ObjectMeta: metav1.ObjectMeta{Name: "grant", UID: "grant-uid", ResourceVersion: "1"}, Spec: api.ReplicaGrantSpec{TargetNamespace: "lab", Chaos: &api.ChaosGrant{Namespaces: []string{"app-copy"}, Kinds: kinds, MaxDurationSeconds: 300, Images: []string{approvedImage}, MaxCPUMilli: 500, MaxMemoryMiB: 256}}}
	f.p = &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "lab", UID: "replica-uid", CreationTimestamp: metav1.NewTime(f.now)}, Spec: api.ClusterReplicaSpec{GrantRef: "grant", TTL: "1h", Profile: "vcluster-0.37.1-lab"}, Status: api.ClusterReplicaStatus{Phase: "Ready"}}
	f.x = &api.ReplicaExperiment{ObjectMeta: metav1.ObjectMeta{Name: "fault", Namespace: "lab", UID: "experiment-uid"}, Spec: api.ReplicaExperimentSpec{ReplicaRef: api.MirrorObjectRef{Name: "demo", UID: "replica-uid"}, DurationSeconds: 30, Faults: []api.ChaosFault{{Kind: "ScaleZero", Namespace: "app-copy", Target: &api.ChaosTarget{Kind: "Deployment", Name: "web", UID: "web-uid"}}}}}
	k := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&api.ReplicaExperiment{}, &api.ClusterReplica{}).WithObjects(f.g, f.p, f.x, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "system"}, Data: map[string][]byte{"key": make([]byte, 32)}}).Build()
	store := &state.Store{Client: k, Namespace: "system"}
	f.parent = &state.State{OwnerUID: "replica-uid", OwnerNamespace: "lab", OwnerName: "demo", GrantUID: "grant-uid", GrantVersion: "1", Provider: "helm", TargetClusterUID: "guest-uid", Entries: []state.Entry{{Kind: "Namespace", Name: "app-copy", UID: "namespace-uid", OperationID: "ns-op"}, {Kind: "Deployment", Namespace: "app-copy", Name: "web", UID: "web-uid", OperationID: "web-op"}}}
	if err := store.Save(context.Background(), f.parent); err != nil {
		t.Fatal(err)
	}
	replicas := int32(3)
	d := &appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app-copy", UID: "web-uid", Annotations: map[string]string{planner.OwnerAnnotation: "replica-uid", planner.OperationAnnotation: "web-op"}}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}}
	f.dyn = dynamicfake.NewSimpleDynamicClient(s, d)
	f.dyn.PrependReactor("create", "*", func(a ktesting.Action) (bool, runtime.Object, error) {
		o := a.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		if o.GetUID() == "" {
			o.SetUID(types.UID("created-" + o.GetName()))
		}
		return false, nil, nil
	})
	guest := kubefake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-copy", UID: "namespace-uid", Annotations: map[string]string{planner.OwnerAnnotation: "replica-uid", planner.OperationAnnotation: "ns-op"}}})
	f.conn = &target.Connection{UID: "guest-uid", Dynamic: f.dyn, Kubernetes: guest}
	f.r = &Reconciler{Client: k, Store: store, Namespace: "lab", NetworkPolicyEnforced: true, Now: func() time.Time { return f.now }, Connect: func(context.Context, client.Client, *state.State, string) (*target.Connection, error) {
		return f.conn, nil
	}}
	return f
}
func (f *fixture) step(t *testing.T) {
	t.Helper()
	_, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(f.x)})
	if err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) phase(t *testing.T) string {
	t.Helper()
	x := &api.ReplicaExperiment{}
	if err := f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x); err != nil {
		t.Fatal(err)
	}
	return x.Status.Phase
}
func (f *fixture) active(t *testing.T) {
	t.Helper()
	f.step(t)
	f.step(t)
	if p := f.phase(t); p != "Active" {
		x := &api.ReplicaExperiment{}
		_ = f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x)
		t.Fatalf("phase %s: %s", p, x.Status.Message)
	}
}
func (f *fixture) workload(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	d, err := apiResource(f.conn, "Deployment", "app-copy").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func (f *fixture) updateFaults(t *testing.T, faults []api.ChaosFault) {
	t.Helper()
	x := &api.ReplicaExperiment{}
	_ = f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x)
	x.Spec.Faults = faults
	if err := f.r.Client.Update(context.Background(), x); err != nil {
		t.Fatal(err)
	}
}

func TestScaleRollbackAfterRestartAndExpiry(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	d := f.workload(t)
	n, _, _ := unstructured.NestedInt64(d.Object, "spec", "replicas")
	if n != 0 {
		t.Fatal("fault not applied")
	}
	// Reconstruct reconciler to prove no in-memory original count is required.
	r := *f.r
	f.r = &r
	f.now = f.now.Add(31 * time.Second)
	f.step(t)
	if f.phase(t) != "Completed" {
		t.Fatal("expiry did not complete rollback")
	}
	d = f.workload(t)
	n, _, _ = unstructured.NestedInt64(d.Object, "spec", "replicas")
	if n != 3 || d.GetAnnotations()[marker] != "" {
		t.Fatal("original scale or marker not restored")
	}
}
func TestDeleteWaitsForRollback(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	x := &api.ReplicaExperiment{}
	_ = f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x)
	if err := f.r.Client.Delete(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	if err := f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x); !apierrors.IsNotFound(err) {
		t.Fatalf("finalizer not cleared after rollback: %v", err)
	}
	n, _, _ := unstructured.NestedInt64(f.workload(t).Object, "spec", "replicas")
	if n != 3 {
		t.Fatal("deletion skipped restore")
	}
	st, err := f.r.Store.Load(context.Background(), string(f.x.UID))
	if err != nil || st != nil {
		t.Fatal("experiment state not cleaned")
	}
}
func TestLostScaleResponseRecoversIntent(t *testing.T) {
	f := newFixture(t)
	f.step(t)
	st, err := f.r.Store.Load(context.Background(), string(f.x.UID))
	if err != nil {
		t.Fatal(err)
	}
	a := &st.Experiment.Actions[0]
	d := f.workload(t)
	_ = unstructured.SetNestedField(d.Object, int64(0), "spec", "replicas")
	ann := d.GetAnnotations()
	ann[marker] = a.OperationID
	d.SetAnnotations(ann)
	if _, err := apiResource(f.conn, "Deployment", "app-copy").Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	if f.phase(t) != "Active" {
		t.Fatal("did not recover response-loss intent")
	}
	f.now = f.now.Add(time.Minute)
	f.step(t)
	n, _, _ := unstructured.NestedInt64(f.workload(t).Object, "spec", "replicas")
	if n != 3 {
		t.Fatal("lost-response rollback failed")
	}
}
func TestRollbackPreservesForeignScaleAndReplacement(t *testing.T) {
	for _, change := range []string{"scale", "uid"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			f.active(t)
			d := f.workload(t)
			if change == "scale" {
				_ = unstructured.SetNestedField(d.Object, int64(7), "spec", "replicas")
			} else {
				d.SetUID("replacement")
			}
			if _, err := apiResource(f.conn, "Deployment", "app-copy").Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			f.now = f.now.Add(time.Minute)
			f.step(t)
			if f.phase(t) != "Blocked" {
				t.Fatal("foreign change should block rollback")
			}
			live := f.workload(t)
			if change == "scale" {
				n, _, _ := unstructured.NestedInt64(live.Object, "spec", "replicas")
				if n != 7 {
					t.Fatal("foreign scale overwritten")
				}
			} else if live.GetUID() != "replacement" {
				t.Fatal("replacement mutated")
			}
		})
	}
}
func TestGrantAndIdentityRejections(t *testing.T) {
	for _, scenario := range []string{"no grant", "foreign namespace", "wrong guest", "wrong object UID", "disabled network", "unapproved image", "tag image", "resource budget"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			switch scenario {
			case "no grant":
				f.g.Spec.Chaos = nil
				_ = f.r.Client.Update(context.Background(), f.g)
			case "foreign namespace":
				f.parent.Entries = f.parent.Entries[1:]
				_ = f.r.Store.Save(context.Background(), f.parent)
			case "wrong guest":
				f.conn.UID = "host-uid"
			case "wrong object UID":
				f.updateFaults(t, []api.ChaosFault{{Kind: "ScaleZero", Namespace: "app-copy", Target: &api.ChaosTarget{Kind: "Deployment", Name: "web", UID: "host-workload-uid"}}})
			case "disabled network":
				f.r.NetworkPolicyEnforced = false
				f.updateFaults(t, []api.ChaosFault{{Kind: "NetworkIsolation", Namespace: "app-copy"}})
			case "unapproved image":
				f.updateFaults(t, []api.ChaosFault{{Kind: "CustomJob", Namespace: "app-copy", Image: strings.Replace(approvedImage, "aaaa", "bbbb", 1), Command: []string{"true"}}})
			case "tag image":
				f.updateFaults(t, []api.ChaosFault{{Kind: "CPUStress", Namespace: "app-copy", Image: "python:latest"}})
			case "resource budget":
				f.updateFaults(t, []api.ChaosFault{{Kind: "CPUStress", Namespace: "app-copy", Image: approvedImage, CPUMilli: 600}})
			}
			f.step(t)
			phase := f.phase(t)
			if phase != "Rejected" && phase != "Blocked" {
				t.Fatalf("unsafe request reached %s", phase)
			}
			n, _, _ := unstructured.NestedInt64(f.workload(t).Object, "spec", "replicas")
			if n != 3 {
				t.Fatal("rejection mutated guest")
			}
		})
	}
}
func TestGrantRevocationRollsBack(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	g := &api.ReplicaGrant{}
	_ = f.r.Client.Get(context.Background(), client.ObjectKey{Name: f.g.Name}, g)
	g.Spec.Chaos = nil
	if err := f.r.Client.Update(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	if f.phase(t) != "Failed" {
		t.Fatal("revocation did not finish rollback")
	}
	n, _, _ := unstructured.NestedInt64(f.workload(t).Object, "spec", "replicas")
	if n != 3 {
		t.Fatal("revocation skipped restore")
	}
}
func TestParentCleanupStopsExperiment(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	done, err := f.r.CleanupReplica(context.Background(), f.parent)
	if err != nil || done {
		t.Fatal("parent cleanup must wait")
	}
	f.step(t)
	done, err = f.r.CleanupReplica(context.Background(), f.parent)
	if err != nil || !done {
		t.Fatal("parent cleanup should proceed after rollback")
	}
}
func TestMissingStateNeverClearsFinalizer(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	if err := f.r.Store.Delete(context.Background(), string(f.x.UID)); err != nil {
		t.Fatal(err)
	}
	x := &api.ReplicaExperiment{}
	_ = f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x)
	_ = f.r.Client.Delete(context.Background(), x)
	f.step(t)
	if f.phase(t) != "Blocked" {
		t.Fatal("missing state removed protection")
	}
}
func TestNetworkIsolationLivesOnlyOnHost(t *testing.T) {
	f := newFixture(t)
	f.updateFaults(t, []api.ChaosFault{{Kind: "NetworkIsolation", Namespace: "app-copy"}})
	// Managed runtime names are normally supplied by the parent's Ready status.
	p := &api.ClusterReplica{}
	_ = f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.p), p)
	f.r.Connect = func(_ context.Context, _ client.Client, st *state.State, _ string) (*target.Connection, error) {
		st.RuntimeName = "managed"
		return f.conn, nil
	}
	f.active(t)
	policies := &networkingv1.NetworkPolicyList{}
	if err := f.r.Client.List(context.Background(), policies); err != nil {
		t.Fatal(err)
	}
	if len(policies.Items) != 1 {
		t.Fatal("host policy absent")
	}
	policy := policies.Items[0]
	if policy.Namespace != "lab" || policy.Spec.PodSelector.MatchLabels["vcluster.loft.sh/managed-by"] != "managed" || policy.Spec.PodSelector.MatchLabels["vcluster.loft.sh/namespace"] != "app-copy" {
		t.Fatal("host/source isolation scope wrong")
	}
	for _, a := range f.dyn.Actions() {
		if a.GetResource().Resource == "networkpolicies" {
			t.Fatal("created ineffective guest policy")
		}
	}
	f.now = f.now.Add(time.Minute)
	f.step(t)
	f.step(t)
	if f.phase(t) != "Completed" {
		t.Fatal("host policy cleanup did not finish")
	}
	_ = f.r.Client.List(context.Background(), policies)
	if len(policies.Items) != 0 {
		t.Fatal("host fault policy leaked")
	}
}
func TestHostAllowPoliciesRejectWithoutMutation(t *testing.T) {
	f := newFixture(t)
	f.updateFaults(t, []api.ChaosFault{{Kind: "NetworkIsolation", Namespace: "app-copy"}})
	f.r.Connect = func(_ context.Context, _ client.Client, st *state.State, _ string) (*target.Connection, error) {
		st.RuntimeName = "managed"
		return f.conn, nil
	}
	allow := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "mirror-baseline", Namespace: "lab"}, Spec: networkingv1.NetworkPolicySpec{Egress: []networkingv1.NetworkPolicyEgressRule{{}}}}
	if err := f.r.Client.Create(context.Background(), allow); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	if f.phase(t) != "Rejected" {
		t.Fatal("additive allow policy falsely isolated")
	}
	policies := &networkingv1.NetworkPolicyList{}
	_ = f.r.Client.List(context.Background(), policies)
	if len(policies.Items) != 1 || policies.Items[0].Name != "mirror-baseline" {
		t.Fatal("baseline policy modified")
	}
}
func TestPreparationFailureHasNoGuestEffects(t *testing.T) {
	f := newFixture(t)
	f.dyn.PrependReactor("get", "deployments", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("unavailable") })
	f.step(t)
	if f.phase(t) != "Rejected" {
		t.Fatal("failed preflight accepted")
	}
	for _, a := range f.dyn.Actions() {
		if a.GetVerb() != "get" {
			t.Fatal("mutation during failed preflight")
		}
	}
}

func TestCombinedFaultJobsAreRestrictedAndRollbackCompletely(t *testing.T) {
	f := newFixture(t)
	f.updateFaults(t, []api.ChaosFault{
		{Kind: "ScaleZero", Namespace: "app-copy", Target: &api.ChaosTarget{Kind: "Deployment", Name: "web", UID: "web-uid"}},
		{Kind: "CPUStress", Namespace: "app-copy", Image: approvedImage, CPUMilli: 100, MemoryMiB: 32},
		{Kind: "MemoryStress", Namespace: "app-copy", Image: approvedImage, CPUMilli: 100, MemoryMiB: 64},
		{Kind: "CustomJob", Namespace: "app-copy", Image: approvedImage, Command: []string{"python3", "-c", "print('fault')"}, CPUMilli: 50, MemoryMiB: 32},
	})
	f.step(t)
	f.step(t)
	if f.phase(t) != "Preparing" {
		t.Fatal("jobs became Active before containers started")
	}
	st, err := f.r.Store.Load(context.Background(), string(f.x.UID))
	if err != nil {
		t.Fatal(err)
	}
	jobs := 0
	for _, a := range st.Experiment.Actions {
		if a.TargetKind != "Job" {
			continue
		}
		jobs++
		o, err := apiResource(f.conn, "Job", a.Namespace).Get(context.Background(), a.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		automount, _, _ := unstructured.NestedBool(o.Object, "spec", "template", "spec", "automountServiceAccountToken")
		if automount {
			t.Fatal("job mounts API token")
		}
		podspec, _, _ := unstructured.NestedMap(o.Object, "spec", "template", "spec")
		for _, key := range []string{"hostNetwork", "hostPID", "hostIPC", "volumes", "nodeName", "tolerations"} {
			if _, ok := podspec[key]; ok {
				t.Fatalf("job exposed %s", key)
			}
		}
		containers, _, _ := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "containers")
		container := containers[0].(map[string]any)
		sc := container["securityContext"].(map[string]any)
		if sc["allowPrivilegeEscalation"] != false || sc["readOnlyRootFilesystem"] != true {
			t.Fatal("job security context not restricted")
		}
		deadline, _, _ := unstructured.NestedInt64(o.Object, "spec", "activeDeadlineSeconds")
		if deadline != 30 {
			t.Fatal("job deadline is unbounded")
		}
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: a.Name + "-pod", Namespace: a.Namespace, UID: types.UID(a.Name + "-pod"), Labels: map[string]string{owner: st.OwnerUID}, Annotations: map[string]string{owner: st.OwnerUID, marker: a.OperationID}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: a.Name, UID: types.UID(a.UID)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "fault", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
		if _, err := f.conn.Kubernetes.CoreV1().Pods(a.Namespace).Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if jobs != 3 {
		t.Fatal("not all job types prepared")
	}
	f.step(t)
	if f.phase(t) != "Active" {
		t.Fatal("running combined faults not active")
	}
	f.now = f.now.Add(time.Minute)
	for i := 0; i < 15 && f.phase(t) != "Completed"; i++ {
		f.step(t)
	}
	if f.phase(t) != "Completed" {
		t.Fatal("combined rollback incomplete")
	}
	n, _, _ := unstructured.NestedInt64(f.workload(t).Object, "spec", "replicas")
	if n != 3 {
		t.Fatal("scale not restored")
	}
	for _, kind := range []string{"Job", "ServiceAccount"} {
		list, err := apiResource(f.conn, kind, "app-copy").List(context.Background(), metav1.ListOptions{})
		if err != nil || len(list.Items) != 0 {
			t.Fatalf("%s leaked: %v", kind, err)
		}
	}
	policies := &networkingv1.NetworkPolicyList{}
	_ = f.r.Client.List(context.Background(), policies)
	if len(policies.Items) != 0 {
		t.Fatal("host policy leaked")
	}
}
func TestPartialFaultFailureRollsBackEarlierEffects(t *testing.T) {
	f := newFixture(t)
	f.updateFaults(t, []api.ChaosFault{{Kind: "ScaleZero", Namespace: "app-copy", Target: &api.ChaosTarget{Kind: "Deployment", Name: "web", UID: "web-uid"}}, {Kind: "CPUStress", Namespace: "app-copy", Image: approvedImage}})
	f.dyn.PrependReactor("create", "jobs", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("admission rejection")
	})
	f.step(t)
	f.step(t)
	for i := 0; i < 10 && f.phase(t) != "Failed"; i++ {
		f.step(t)
	}
	if f.phase(t) != "Failed" {
		t.Fatal("failed composite did not finish rollback")
	}
	n, _, _ := unstructured.NestedInt64(f.workload(t).Object, "spec", "replicas")
	if n != 3 {
		t.Fatal("earlier scale fault leaked")
	}
}
func TestPodDeleteRequiresInventoriedControllerAndPreservesReplacement(t *testing.T) {
	f := newFixture(t)
	yes := true
	rs := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]any{"namespace": "app-copy", "name": "web-rs", "uid": "rs-uid"}}}
	rs.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "web-uid", Controller: &yes}})
	pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"namespace": "app-copy", "name": "web-pod", "uid": "pod-uid"}}}
	pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "rs-uid", Controller: &yes}})
	for _, o := range []*unstructured.Unstructured{rs, pod} {
		if _, err := apiResource(f.conn, o.GetKind(), o.GetNamespace()).Create(context.Background(), o, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	f.updateFaults(t, []api.ChaosFault{{Kind: "PodDelete", Namespace: "app-copy", Target: &api.ChaosTarget{Kind: "Pod", Name: "web-pod", UID: "pod-uid"}}})
	f.active(t)
	if _, err := apiResource(f.conn, "Pod", "app-copy").Get(context.Background(), "web-pod", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("original pod not deleted")
	}
	pod.SetUID("replacement-pod")
	if _, err := apiResource(f.conn, "Pod", "app-copy").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	live, err := apiResource(f.conn, "Pod", "app-copy").Get(context.Background(), "web-pod", metav1.GetOptions{})
	if err != nil || live.GetUID() != "replacement-pod" {
		t.Fatal("replacement pod deleted")
	}
}
func TestConcurrentExperimentWaitsForRollback(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	x := f.x.DeepCopy()
	x.Name = "other"
	x.UID = "other-uid"
	x.ResourceVersion = ""
	if err := f.r.Client.Create(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(x)}); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(x), x); err != nil {
		t.Fatal(err)
	}
	if x.Status.Phase != "Pending" {
		t.Fatal("concurrent fault acquired same replica")
	}
}

func TestNetworkCleanupWaitsForPhysicalFaultPods(t *testing.T) {
	f := newFixture(t)
	f.updateFaults(t, []api.ChaosFault{{Kind: "CustomJob", Namespace: "app-copy", Image: approvedImage, Command: []string{"true"}}})
	f.step(t)
	f.step(t)
	st, err := f.r.Store.Load(context.Background(), string(f.x.UID))
	if err != nil {
		t.Fatal(err)
	}
	var jobAction state.ChaosAction
	for _, a := range st.Experiment.Actions {
		if a.TargetKind == "Job" {
			jobAction = a
		}
	}
	hostPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "physical-fault-pod", Namespace: "lab", UID: "physical-uid", Labels: map[string]string{"vcluster.loft.sh/managed-by": st.RuntimeName, "vcluster.loft.sh/namespace": "app-copy"}, Annotations: map[string]string{"vcluster.loft.sh/object-name": jobAction.Name + "-abcde", "vcluster.loft.sh/object-namespace": "app-copy"}}}
	if err := f.r.Client.Create(context.Background(), hostPod); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	for i := 0; i < 8; i++ {
		f.step(t)
	}
	if f.phase(t) != "Cleaning" {
		t.Fatal("physical fault pod must delay cleanup")
	}
	policies := &networkingv1.NetworkPolicyList{}
	_ = f.r.Client.List(context.Background(), policies)
	if len(policies.Items) != 1 {
		t.Fatal("removed isolation while physical job pod remained")
	}
	if err := f.r.Client.Delete(context.Background(), hostPod); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	f.step(t)
	if f.phase(t) != "Failed" {
		t.Fatal("cleanup did not resume or unstarted fault was falsely reported completed")
	}
}
func TestNamespaceReplacementIsNotMutatedDuringRollback(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	ns, err := f.conn.Kubernetes.CoreV1().Namespaces().Get(context.Background(), "app-copy", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ns.UID = "replacement-namespace"
	if _, err := f.conn.Kubernetes.CoreV1().Namespaces().Update(context.Background(), ns, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	// The fake tracker keeps the old Deployment; a real namespace replacement
	// cannot retain it. Verify the controller makes no guest mutation at all.
	before := len(f.dyn.Actions())
	f.now = f.now.Add(time.Minute)
	f.step(t)
	if f.phase(t) != "Completed" {
		t.Fatal("vanished original namespace did not complete cleanup")
	}
	for _, a := range f.dyn.Actions()[before:] {
		if a.GetVerb() == "patch" || a.GetVerb() == "delete" || a.GetVerb() == "update" {
			t.Fatal("mutated a replacement guest namespace")
		}
	}
}

type failOneStateDeletion struct {
	client.Client
	name   string
	failed bool
}

func (f *failOneStateDeletion) Delete(ctx context.Context, o client.Object, options ...client.DeleteOption) error {
	if _, ok := o.(*corev1.Secret); ok && o.GetName() == f.name && !f.failed {
		f.failed = true
		return errors.New("simulated response loss")
	}
	return f.Client.Delete(ctx, o, options...)
}
func TestFinishedStateReapedAfterFinalizerDeletionResponseLoss(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	f.now = f.now.Add(time.Minute)
	f.step(t)
	if f.phase(t) != "Completed" {
		t.Fatal("not safely rolled back")
	}
	x := &api.ReplicaExperiment{}
	_ = f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x)
	if err := f.r.Client.Delete(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	wrapped := &failOneStateDeletion{Client: f.r.Client, name: state.Name(string(f.x.UID))}
	f.r.Client = wrapped
	f.r.Store.Client = wrapped
	if _, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(f.x)}); err == nil {
		t.Fatal("expected lost state-delete response")
	}
	if err := f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(f.x), x); !apierrors.IsNotFound(err) {
		t.Fatal("finalizer removal did not precede protected-state deletion")
	}
	st, err := f.r.Store.Load(context.Background(), string(f.x.UID))
	if err != nil || st == nil || st.Experiment.Phase != "Finished" {
		t.Fatal("verified completion record was not retained")
	}
	if done, err := f.r.CleanupReplica(context.Background(), f.parent); err != nil || !done {
		t.Fatalf("orphan reaping failed: %v", err)
	}
	st, err = f.r.Store.Load(context.Background(), string(f.x.UID))
	if err != nil || st != nil {
		t.Fatal("finished orphan record leaked after parent cleanup")
	}
}
func TestMissingStateBlocksConcurrentAdmission(t *testing.T) {
	f := newFixture(t)
	f.active(t)
	if err := f.r.Store.Delete(context.Background(), string(f.x.UID)); err != nil {
		t.Fatal(err)
	}
	x := f.x.DeepCopy()
	x.Name = "other"
	x.UID = "other-uid"
	x.ResourceVersion = ""
	if err := f.r.Client.Create(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(x)}); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Client.Get(context.Background(), client.ObjectKeyFromObject(x), x); err != nil {
		t.Fatal(err)
	}
	if x.Status.Phase != "Pending" {
		t.Fatal("missing authority allowed a competing fault")
	}
}
