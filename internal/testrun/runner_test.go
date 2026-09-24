package testrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const credentialFixture = `apiVersion: v1
kind: Config
current-context: guest
clusters:
- name: guest
  cluster:
    server: https://guest.example
    certificate-authority-data: Y2E=
contexts:
- name: guest
  context:
    cluster: guest
    user: guest
users:
- name: guest
  user:
    token: secret-fixture-never-in-report
`

func recipeFixture() Recipe {
	return Recipe{APIVersion: RecipeVersion, Kind: "TestRecipe", Replica: &api.ClusterReplicaSpec{Profile: catalog.PersistentProfile, TTL: "2h", CleanupPolicy: "DeleteOwned", GrantRef: "fixture", Approval: "Automatic"}, Execution: Execution{Command: []string{"fixture-command", "sensitive-argument"}, Timeout: "10m"}}
}

type fixtureClient struct {
	client.Client
	mu                sync.Mutex
	events            []string
	now               time.Time
	phase             string
	accessPhase       string
	credential        string
	foreignCredential bool
	accessSeconds     int64
	finalizer         bool
	chaosPhase        string
	deleteHook        func(context.Context, client.Object) error
}

func (f *fixtureClient) record(value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, value)
}
func (f *fixtureClient) observed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}
func (f *fixtureClient) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	o.SetUID(types.UID("uid-" + o.GetName()))
	o.SetCreationTimestamp(metav1.NewTime(f.now))
	o.SetGeneration(1)
	switch v := o.(type) {
	case *api.ClusterReplica:
		f.record("create-replica")
		phase := f.phase
		if phase == "" {
			phase = "Ready"
		}
		v.Status = api.ClusterReplicaStatus{Phase: phase, Runtime: &api.RuntimeReference{Profile: catalog.PersistentProfile, ChartVersion: catalog.ChartVersion, ChartSHA256: catalog.ChartSHA256, GuestKubernetesVersion: catalog.GuestVersion, ReleaseName: "runtime"}, Plan: &api.PlanSummary{Revision: "capture-revision", CapturedAt: metav1.NewTime(f.now)}, SourceVersion: "v1.36.0", TargetVersion: "v1.36.0"}
		if f.finalizer {
			v.Finalizers = []string{"fixture/cleanup"}
		}
	case *api.ReplicaMirror:
		f.record("create-mirror")
		child := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: v.Name + "-generation", Namespace: v.Namespace}, Spec: v.Spec.Template}
		if err := f.Create(ctx, child); err != nil {
			return err
		}
		v.Status = api.ReplicaMirrorStatus{Phase: "Ready", ObservedGeneration: 1, ActiveReplica: &api.MirrorObjectRef{Name: child.Name, UID: string(child.UID)}, ActiveRun: &api.MirrorObjectRef{Name: "capture", UID: "capture-uid"}, CapturedAt: &metav1.Time{Time: f.now}}
	case *api.ReplicaAccess:
		f.record("create-access")
		phase := f.accessPhase
		if phase == "" {
			phase = "Ready"
		}
		seconds := f.accessSeconds
		if seconds == 0 {
			seconds = 900
		}
		v.Status = api.ReplicaAccessStatus{Phase: phase, CredentialSecret: "credential", ExpiresAt: &metav1.Time{Time: f.now.Add(time.Duration(seconds) * time.Second)}}
		data := f.credential
		if data == "" {
			data = credentialFixture
		}
		owner := v.UID
		if f.foreignCredential {
			owner = "foreign-uid"
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credential", Namespace: v.Namespace, OwnerReferences: []metav1.OwnerReference{{UID: owner}}}, Data: map[string][]byte{"config": []byte(data)}}
		if err := f.Client.Create(ctx, secret); err != nil {
			return err
		}
	case *api.ReplicaExperiment:
		f.record("create-chaos")
		v.Status.Phase = f.chaosPhase
		if v.Status.Phase == "" {
			v.Status.Phase = "Active"
		}
	}
	return f.Client.Create(ctx, o, opts...)
}
func (f *fixtureClient) Delete(ctx context.Context, o client.Object, opts ...client.DeleteOption) error {
	if ctx.Err() != nil {
		return errors.New("cleanup inherited cancelled context")
	}
	var options client.DeleteOptions
	for _, opt := range opts {
		opt.ApplyToDelete(&options)
	}
	if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != o.GetUID() {
		return errors.New("delete missing UID precondition")
	}
	switch o.(type) {
	case *api.ClusterReplica:
		f.record("delete-replica")
	case *api.ReplicaMirror:
		f.record("delete-mirror")
	case *api.ReplicaAccess:
		f.record("delete-access")
	case *api.ReplicaExperiment:
		f.record("delete-chaos")
	}
	if f.deleteHook != nil {
		if err := f.deleteHook(ctx, o); err != nil {
			return err
		}
	}
	return f.Client.Delete(ctx, o, opts...)
}
func (f *fixtureClient) Patch(ctx context.Context, o client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if m, ok := o.(*api.ReplicaMirror); ok {
		if m.Spec.HoldUntil == nil {
			f.record("release-lease")
		} else {
			f.record("hold-lease")
		}
	}
	return f.Client.Patch(ctx, o, patch, opts...)
}

func runnerFixture(t *testing.T) (*Runner, *fixtureClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	f := &fixtureClient{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), now: time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)}
	r := &Runner{Client: f, Namespace: "test-space", Now: func() time.Time { return f.now }, NewID: func() (string, error) { return "unique", nil },
		OpenSession: func(_ context.Context, _ *api.ClusterReplica, data []byte) (Session, error) {
			if string(data) != credentialFixture {
				t.Error("unexpected credential")
			}
			f.record("open-session")
			return Session{Kubeconfig: "/private/guest-config", Close: func() { f.record("close-session") }}, nil
		},
		Execute: func(_ context.Context, args []string, path string) (int, error) {
			if path != "/private/guest-config" || len(args) != 2 {
				t.Error("wrong invocation")
			}
			f.record("test")
			return 0, nil
		},
	}
	return r, f
}

func TestSuccessfulRunPinsCaptureAndVerifiesCleanup(t *testing.T) {
	r, f := runnerFixture(t)
	report := r.Run(context.Background(), recipeFixture())
	if !report.Successful() {
		t.Fatalf("%+v", report)
	}
	if report.Request.UID != "uid-test-unique" || report.Replica.UID != report.Request.UID || report.PlanRevision != "capture-revision" || report.Runtime.ChartSHA256 != catalog.ChartSHA256 || report.CapturedAt == nil || report.RecipeSHA256 == "" {
		t.Fatalf("missing provenance: %+v", report)
	}
	if want := []string{"create-replica", "create-access", "open-session", "test", "close-session", "delete-access", "delete-replica"}; !reflect.DeepEqual(f.observed(), want) {
		t.Fatalf("events: %v", f.observed())
	}
	if err := f.Get(context.Background(), client.ObjectKey{Namespace: r.Namespace, Name: report.Request.Name}, &api.ClusterReplica{}); !apierrors.IsNotFound(err) {
		t.Fatal("cleanup did not remove request")
	}
}

func TestTestFailureAndCleanupFailureAreIndependent(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleaned", true: "cleanup-blocked"}[cleanupFails], func(t *testing.T) {
			r, f := runnerFixture(t)
			f.finalizer = cleanupFails
			r.Execute = func(context.Context, []string, string) (int, error) { return 17, errors.New("secret command error") }
			r.Wait = func(context.Context) error { return context.DeadlineExceeded }
			report := r.Run(context.Background(), recipeFixture())
			if report.Test.Status != "Failed" || *report.Test.ExitCode != 17 || report.Successful() {
				t.Fatalf("%+v", report)
			}
			want := "Verified"
			if cleanupFails {
				want = "Unverified"
			}
			if report.Cleanup.Status != want {
				t.Fatalf("%+v", report.Cleanup)
			}
			data, _ := json.Marshal(report)
			if strings.Contains(string(data), "secret command error") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestCancelledProcessStillUsesIndependentCleanupContext(t *testing.T) {
	r, f := runnerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Execute = func(ctx context.Context, _ []string, _ string) (int, error) {
		cancel()
		<-ctx.Done()
		return -1, ctx.Err()
	}
	recipe := recipeFixture()
	recipe.Lifecycle.KeepOnFailure = true
	report := r.Run(ctx, recipe)
	if report.Test.Status != "Interrupted" || report.Cleanup.Status != "Verified" || report.RetainedUntil != nil {
		t.Fatalf("%+v", report)
	}
	if !strings.Contains(strings.Join(f.observed(), ","), "delete-replica") {
		t.Fatal("cancellation skipped cleanup")
	}
}

func TestKeepFailureRetainsOnlyUntilOriginalTTLAndRevokesAccess(t *testing.T) {
	r, f := runnerFixture(t)
	r.Execute = func(context.Context, []string, string) (int, error) { return 1, nil }
	recipe := recipeFixture()
	recipe.Lifecycle.KeepOnFailure = true
	report := r.Run(context.Background(), recipe)
	if report.Cleanup.Status != "Retained" || report.RetainedUntil == nil || !report.RetainedUntil.Equal(f.now.Add(2*time.Hour)) {
		t.Fatalf("%+v", report)
	}
	if strings.Contains(strings.Join(f.observed(), ","), "delete-replica") || !strings.Contains(strings.Join(f.observed(), ","), "delete-access") {
		t.Fatal(f.observed())
	}
	if err := f.Get(context.Background(), client.ObjectKey{Namespace: r.Namespace, Name: report.Request.Name}, &api.ClusterReplica{}); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupPreservesReplacementUID(t *testing.T) {
	r, f := runnerFixture(t)
	r.Execute = func(ctx context.Context, _ []string, _ string) (int, error) {
		obj := &api.ClusterReplica{}
		key := client.ObjectKey{Namespace: r.Namespace, Name: "test-unique"}
		if err := f.Client.Get(ctx, key, obj); err != nil {
			t.Fatal(err)
		}
		if err := f.Client.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		obj.UID = "replacement"
		obj.ResourceVersion = ""
		if err := f.Client.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	}
	report := r.Run(context.Background(), recipeFixture())
	if report.Cleanup.Status != "Unverified" {
		t.Fatalf("%+v", report)
	}
	obj := &api.ClusterReplica{}
	if err := f.Get(context.Background(), client.ObjectKey{Namespace: r.Namespace, Name: "test-unique"}, obj); err != nil || obj.UID != "replacement" {
		t.Fatal("replacement deleted")
	}
}

func TestCollidingRequestIsNeverAdopted(t *testing.T) {
	r, f := runnerFixture(t)
	foreign := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "test-unique", Namespace: r.Namespace, UID: "foreign"}}
	if err := f.Client.Create(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	report := r.Run(context.Background(), recipeFixture())
	if report.Setup.Status != "Failed" || report.Cleanup.Status != "NotNeeded" || report.Request != nil {
		t.Fatalf("%+v", report)
	}
	current := &api.ClusterReplica{}
	if err := f.Get(context.Background(), client.ObjectKeyFromObject(foreign), current); err != nil || current.UID != "foreign" {
		t.Fatal("foreign request changed")
	}
}

func TestSetupFailuresNeverExecuteAndStillCleanOwnedRequest(t *testing.T) {
	for _, scenario := range []string{"rejected", "revision", "foreign-credential", "unsafe-credential", "expired-credential", "denied-access", "session"} {
		t.Run(scenario, func(t *testing.T) {
			r, f := runnerFixture(t)
			recipe := recipeFixture()
			switch scenario {
			case "rejected":
				f.phase = "Rejected"
			case "revision":
				recipe.ExpectedPlanRevision = "old-revision"
			case "foreign-credential":
				f.foreignCredential = true
			case "unsafe-credential":
				f.credential = strings.Replace(credentialFixture, "token: secret-fixture-never-in-report", "tokenFile: /private/host-token", 1)
			case "expired-credential":
				f.accessSeconds = 601
			case "denied-access":
				f.accessPhase = "Rejected"
			case "session":
				r.OpenSession = func(context.Context, *api.ClusterReplica, []byte) (Session, error) {
					return Session{}, errors.New("secret connect details")
				}
			}
			report := r.Run(context.Background(), recipe)
			if report.Setup.Status != "Failed" || report.Test.Status != "NotRun" || report.Cleanup.Status != "Verified" {
				t.Fatalf("%+v", report)
			}
			for _, event := range f.observed() {
				if event == "test" {
					t.Fatal("executed after setup failure")
				}
			}
		})
	}
}

func TestMirrorLeaseAndChaosRollbackOrder(t *testing.T) {
	r, f := runnerFixture(t)
	recipe := recipeFixture()
	recipe.Mirror = &api.ReplicaMirrorSpec{Template: *recipe.Replica, Volumes: []api.NamespacedName{{Namespace: "source", Name: "data"}}}
	recipe.Replica = nil
	recipe.Chaos = &Chaos{DurationSeconds: 60, Faults: []api.ChaosFault{{Kind: "NetworkIsolation", Namespace: "guest"}}}
	report := r.Run(context.Background(), recipe)
	if !report.Successful() || report.MirrorCapture.UID != "capture-uid" || report.Experiment == nil {
		t.Fatalf("%+v", report)
	}
	if want := []string{"create-mirror", "create-replica", "hold-lease", "create-access", "open-session", "create-chaos", "test", "close-session", "delete-chaos", "delete-access", "release-lease", "delete-mirror"}; !reflect.DeepEqual(f.observed(), want) {
		t.Fatalf("%v", f.observed())
	}
}

func TestFailedChaosRollbackPreventsDeletingRuntime(t *testing.T) {
	r, f := runnerFixture(t)
	recipe := recipeFixture()
	recipe.Chaos = &Chaos{DurationSeconds: 60, Faults: []api.ChaosFault{{Kind: "NetworkIsolation", Namespace: "guest"}}}
	f.deleteHook = func(_ context.Context, o client.Object) error {
		if _, ok := o.(*api.ReplicaExperiment); ok {
			return errors.New("rollback blocked")
		}
		return nil
	}
	report := r.Run(context.Background(), recipe)
	if report.Test.Status != "Passed" || report.Cleanup.Status != "Unverified" {
		t.Fatalf("%+v", report)
	}
	events := strings.Join(f.observed(), ",")
	if strings.Contains(events, "delete-access") || strings.Contains(events, "delete-replica") {
		t.Fatal(events)
	}
}

func TestReportContainsNoSecretCommandOrRawConfigAndDoesNotOverwrite(t *testing.T) {
	r, _ := runnerFixture(t)
	report := r.Run(context.Background(), recipeFixture())
	dir := t.TempDir()
	if err := WriteReports(dir, report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.json", "junit.xml"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"secret-fixture-never-in-report", "sensitive-argument", "fixture-command", "certificate-authority-data", "/private/guest-config"} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("report leaked %s", secret)
			}
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("report not private")
		}
	}
	if err := WriteReports(dir, report); err == nil {
		t.Fatal("overwrote reports")
	}
}
