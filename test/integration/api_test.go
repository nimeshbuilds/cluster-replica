//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"helm.sh/helm/v4/pkg/action"
	chartloader "helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/kube"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kYaml "k8s.io/apimachinery/pkg/util/yaml"
	"os"
	"path/filepath"
	"reflect"
	"sigs.k8s.io/yaml"
	"strings"
	"testing"
	"time"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/capture"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/controller"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type stubRuntime struct {
	installs, deletes int
	deleteErr         error
}

func (p *stubRuntime) Ensure(context.Context, runtimeprovider.Request) (runtimeprovider.Observation, error) {
	p.installs++
	return runtimeprovider.Observation{Ready: true}, nil
}
func (p *stubRuntime) Delete(context.Context, runtimeprovider.Request) error {
	p.deletes++
	return p.deleteErr
}

func TestAPIServerContract(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Fatal("run make test-integration to fetch the pinned local test binaries")
	}
	environment := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd")}, ErrorIfCRDPathMissing: true}
	config, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "lab"}}); err != nil {
		t.Fatal(err)
	}
	newRequest := func(name string) *v1alpha1.ClusterReplica {
		return &v1alpha1.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lab"}, Spec: v1alpha1.ClusterReplicaSpec{Profile: catalog.Profile, TTL: "5m", CleanupPolicy: "HelmReleaseOnly"}}
	}

	t.Run("admission rejects unsupported and unbounded requests", func(t *testing.T) {
		for i, mutate := range []func(*v1alpha1.ClusterReplica){
			func(o *v1alpha1.ClusterReplica) { o.Spec.TTL = "4m" },
			func(o *v1alpha1.ClusterReplica) { o.Spec.TTL = "169h" },
			func(o *v1alpha1.ClusterReplica) { o.Spec.Profile = "latest" },
			func(o *v1alpha1.ClusterReplica) { o.Spec.CleanupPolicy = "DeleteEverything" },
		} {
			obj := newRequest("invalid")
			mutate(obj)
			if err := c.Create(ctx, obj); !apierrors.IsInvalid(err) {
				t.Fatalf("case %d: want validation rejection, got %v", i, err)
			}
		}
	})

	t.Run("mirror admission preserves selection and run identity", func(t *testing.T) {
		m := &v1alpha1.ReplicaMirror{ObjectMeta: metav1.ObjectMeta{Name: "mirror-admission", Namespace: "lab"}, Spec: v1alpha1.ReplicaMirrorSpec{Template: v1alpha1.ClusterReplicaSpec{Profile: catalog.PersistentProfile, TTL: "1h", CleanupPolicy: "DeleteOwned", GrantRef: "mirror-source", Replication: &v1alpha1.ReplicationSpec{Namespaces: []string{"source"}}}, Volumes: []v1alpha1.NamespacedName{{Namespace: "source", Name: "data"}}}}
		if err := c.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
		if m.Spec.Consistency != "CrashConsistent" || m.Spec.RetainRevisions != 2 {
			t.Fatal("mirror defaults missing")
		}
		original := m.DeepCopy()
		for _, mutate := range []func(*v1alpha1.ReplicaMirror){
			func(m *v1alpha1.ReplicaMirror) { m.Spec.Template.TTL = "2h" },
			func(m *v1alpha1.ReplicaMirror) { m.Spec.Volumes[0].Name = "another" },
			func(m *v1alpha1.ReplicaMirror) { m.Spec.RetainRevisions = 11 },
			func(m *v1alpha1.ReplicaMirror) { m.Spec.Consistency = "ApplicationConsistent" },
		} {
			bad := original.DeepCopy()
			mutate(bad)
			if err := c.Update(ctx, bad); !apierrors.IsInvalid(err) {
				t.Fatalf("unsafe mirror mutation admitted: %v", err)
			}
		}
		m.Spec.Suspend = true
		m.Spec.Interval = "1h"
		m.Spec.HoldUntil = &metav1.Time{Time: time.Now().Add(time.Minute)}
		if err := c.Update(ctx, m); err != nil {
			t.Fatal(err)
		}
		for i, spec := range []v1alpha1.ReplicaMirrorRunSpec{
			{Action: "Reset", MirrorRef: v1alpha1.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}},
			{Action: "Sync", MirrorRef: v1alpha1.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}, RevisionRef: &v1alpha1.MirrorObjectRef{Name: "prior", UID: "uid"}},
			{Action: "Sync", MirrorRef: v1alpha1.MirrorObjectRef{Name: m.Name}},
		} {
			run := &v1alpha1.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("bad-mirror-run-%d", i), Namespace: "lab"}, Spec: spec}
			if err := c.Create(ctx, run); !apierrors.IsInvalid(err) {
				t.Fatalf("invalid run admitted: %v", err)
			}
		}
		run := &v1alpha1.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Name: "valid-mirror-run", Namespace: "lab"}, Spec: v1alpha1.ReplicaMirrorRunSpec{Action: "Sync", MirrorRef: v1alpha1.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}}}
		if err := c.Create(ctx, run); err != nil {
			t.Fatal(err)
		}
		run.Spec.Force = true
		if err := c.Update(ctx, run); !apierrors.IsInvalid(err) {
			t.Fatalf("immutable run changed: %v", err)
		}
	})

	t.Run("published mirror examples satisfy current schemas", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "mirror", "grant.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		grant := &v1alpha1.ReplicaGrant{}
		if err := yaml.UnmarshalStrict(data, grant); err != nil {
			t.Fatal(err)
		}
		if err := c.Create(ctx, grant); err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(filepath.Join("..", "..", "examples", "mirror", "mirror.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		mirror := &v1alpha1.ReplicaMirror{}
		if err := yaml.UnmarshalStrict(data, mirror); err != nil {
			t.Fatal(err)
		}
		mirror.Namespace = "lab"
		if err := c.Create(ctx, mirror); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("immutable spec status expiry and finalizer", func(t *testing.T) {
		obj := newRequest("lifecycle")
		if err := c.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
		obj.Spec.TTL = "1h"
		if err := c.Update(ctx, obj); !apierrors.IsInvalid(err) {
			t.Fatalf("spec mutation was not rejected: %v", err)
		}
		key := client.ObjectKeyFromObject(obj)
		if err := c.Get(ctx, key, obj); err != nil {
			t.Fatal(err)
		}
		now := obj.CreationTimestamp.Add(time.Minute)
		provider := &stubRuntime{}
		r := &controller.Reconciler{Client: c, Provider: provider, Namespace: "lab", Now: func() time.Time { return now }}
		reconcile := func() {
			t.Helper()
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, key, obj); err != nil {
				t.Fatal(err)
			}
		}
		reconcile()
		reconcile()
		if provider.installs != 0 || obj.Status.Runtime == nil || !controllerutil.ContainsFinalizer(obj, controller.Finalizer) {
			t.Fatal("external effects preceded durable preparation")
		}
		reconcile()
		if obj.Status.Phase != "RuntimeReady" {
			t.Fatal("status subresource was not updated")
		}
		now = obj.CreationTimestamp.Add(5 * time.Minute)
		reconcile()
		reconcile()
		if obj.Status.Phase != "Expired" || provider.installs != 1 {
			t.Fatal("expiry did not prevent resurrection")
		}
		provider.deleteErr = errors.New("temporary cleanup failure")
		if err := c.Delete(ctx, obj); err != nil {
			t.Fatal(err)
		}
		reconcile()
		if !controllerutil.ContainsFinalizer(obj, controller.Finalizer) {
			t.Fatal("cleanup error dropped finalizer")
		}
		provider.deleteErr = nil
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, key, obj); !apierrors.IsNotFound(err) {
			t.Fatalf("finalized CR still exists: %v", err)
		}
	})

	t.Run("real pinned Helm chart installs idempotently through the API", func(t *testing.T) {
		path := os.Getenv("VCLUSTER_CHART")
		if path == "" {
			t.Fatal("VCLUSTER_CHART is required for this integration suite")
		}
		req := runtimeprovider.Request{Namespace: "lab", OwnerUID: "install-test-uid", Reference: catalog.Resolve("install-test-uid")}
		p := &helmprovider.Provider{Config: config, Client: c, Namespace: "lab", ChartPath: path}
		observation, err := p.Ensure(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Ready {
			t.Fatal("installation alone was reported ready")
		}
		deployments := &appsv1.DeploymentList{}
		if err := c.List(ctx, deployments, client.InNamespace("lab"), client.MatchingLabels{catalog.OwnerLabel: req.OwnerUID}); err != nil {
			t.Fatal(err)
		}
		if len(deployments.Items) != 1 {
			t.Fatal("chart did not create one owned control-plane Deployment")
		}
		// A resumed observation must not fetch/reinstall the chart. envtest has no
		// scheduler/controller-manager, so this Deployment correctly stays unready.
		p.ChartPath = "/does-not-exist.tgz"
		observation, err = p.Ensure(ctx, req)
		if err != nil || observation.Ready {
			t.Fatalf("idempotent observation failed: %v", err)
		}
	})

	t.Run("Helm cleanup keeps evidence and refuses foreign ownership", func(t *testing.T) {
		kube, err := kubernetes.NewForConfig(config)
		if err != nil {
			t.Fatal(err)
		}
		store := storage.Init(driver.NewSecrets(kube.CoreV1().Secrets("lab")))
		req := runtimeprovider.Request{Namespace: "lab", OwnerUID: "helm-test-uid", Reference: catalog.Resolve("helm-test-uid")}
		manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cleanup-fixture\n  namespace: lab\n"
		rel := &releasev1.Release{Name: req.Reference.ReleaseName, Namespace: "lab", Version: 1, Info: &releasev1.Info{Status: releasecommon.StatusDeployed}, Manifest: manifest, Labels: map[string]string{catalog.OwnerLabel: req.OwnerUID}}
		if err := store.Create(rel); err != nil {
			t.Fatal(err)
		}
		obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cleanup-fixture", Namespace: "lab", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, Annotations: map[string]string{"meta.helm.sh/release-name": "unrelated-release", "meta.helm.sh/release-namespace": "lab"}}}
		if err := c.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
		p := &helmprovider.Provider{Config: config, Client: c, Namespace: "lab", ChartPath: "/does-not-exist.tgz"}
		if err := p.Delete(ctx, req); !errors.Is(err, runtimeprovider.ErrOwnership) {
			t.Fatalf("did not block foreign object: %v", err)
		}
		if _, err := store.Last(req.Reference.ReleaseName); err != nil {
			t.Fatal("lost release history on ownership conflict")
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal("touched foreign object")
		}
		obj.Annotations["meta.helm.sh/release-name"] = req.Reference.ReleaseName
		// envtest has no garbage collector. A test finalizer simulates unfinished deletion.
		obj.Finalizers = []string{"test.nimeshbuilds.dev/hold"}
		if err := c.Update(ctx, obj); err != nil {
			t.Fatal(err)
		}
		if err := p.Delete(ctx, req); err == nil {
			t.Fatal("claimed deletion complete while a manifest object remained")
		}
		if _, err := store.Last(req.Reference.ReleaseName); err != nil {
			t.Fatal("lost retry evidence during partial deletion")
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal(err)
		}
		if obj.DeletionTimestamp.IsZero() {
			t.Fatal("Helm did not request deletion")
		}
		obj.Finalizers = nil // simulate completion by the absent test garbage collector
		if err := c.Update(ctx, obj); err != nil {
			t.Fatal(err)
		}
		if err := p.Delete(ctx, req); err != nil {
			t.Fatalf("cleanup could not resume without chart archive: %v", err)
		}
		if _, err := store.Last(req.Reference.ReleaseName); !errors.Is(err, driver.ErrReleaseNotFound) {
			t.Fatalf("history not purged after manifest absence: %v", err)
		}
	})
}

func TestOperatorInstallationChart(t *testing.T) {
	environment := &envtest.Environment{}
	config, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Stop()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"replicove-system", "source-dev"} {
		if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	chartSource := os.Getenv("REPLICOVE_OPERATOR_CHART")
	if chartSource == "" {
		chartSource = filepath.Join("..", "..", "charts", "replicove")
	}
	ch, err := chartloader.Load(chartSource)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := helmprovider.Configuration(config, "replicove-system")
	if err != nil {
		t.Fatal(err)
	}
	install := helmprovider.NewInstall(cfg)
	install.ReleaseName = "replicove"
	install.Namespace = "replicove-system"
	install.Timeout = 30 * time.Second
	values := map[string]any{"image": map[string]any{"repository": "test", "tag": "test"}, "sources": []any{map[string]any{"namespace": "source-dev", "rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps"}, "verbs": []any{"get", "list"}}}}}}
	if _, err := install.RunWithContext(ctx, ch, values); err != nil {
		t.Fatal(err)
	}
	destination := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: "replica-lab"}, destination); err != nil {
		t.Fatalf("one-shot chart did not create destination namespace: %v", err)
	}
	if destination.Annotations["helm.sh/resource-policy"] != "keep" {
		t.Fatal("uninstall would remove a namespace containing replica lifecycles")
	}
	destinationUID := destination.UID
	key := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "replicove-system", Name: "replicove-state-key"}, key); err != nil {
		t.Fatal(err)
	}
	if len(key.Data["key"]) != 32 || key.Immutable == nil || !*key.Immutable {
		t.Fatal("state key is not immutable AES-256 material")
	}
	originalKey := append([]byte{}, key.Data["key"]...)
	originalKeyUID := key.UID
	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = "replicove-system"
	upgrade.WaitStrategy = kube.HookOnlyStrategy
	upgrade.Timeout = 30 * time.Second
	if _, err := upgrade.RunWithContext(ctx, "replicove", ch, values); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil {
		t.Fatal(err)
	}
	if key.UID != originalKeyUID || !bytes.Equal(key.Data["key"], originalKey) {
		t.Fatal("operator upgrade replaced the encryption key")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(destination), destination); err != nil || destination.UID != destinationUID {
		t.Fatal("operator upgrade removed or replaced the destination namespace")
	}
	assertOperatorAuthorization(t, config)
	// Opt in after installation, preserving user overrides and the immutable key.
	// envtest checks real Helm/API behavior; the live suite additionally preserves
	// a running guest and exercises snapshot capture, restore and deletion.
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	deployments := kubeClient.AppsV1().Deployments("replicove-system")
	if _, err := deployments.Get(ctx, "replicove-snapshot-controller", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("snapshot controller must be absent before opt-in: %v", err)
	}
	beforeOperator, err := deployments.Get(ctx, "replicove", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	upgrade = action.NewUpgrade(cfg)
	upgrade.Namespace = "replicove-system"
	upgrade.WaitStrategy = kube.HookOnlyStrategy
	upgrade.Timeout = 30 * time.Second
	upgrade.ResetThenReuseValues = true
	overlay := map[string]any{"mirrors": map[string]any{"enabled": true, "networkPolicyEnforced": true, "sources": []any{"source-dev"}}}
	if _, err := upgrade.RunWithContext(ctx, "replicove", ch, overlay); err != nil {
		t.Fatalf("enabling mirrors on an existing release: %v", err)
	}
	saved, err := cfg.Releases.Last("replicove")
	if err != nil {
		t.Fatal(err)
	}
	savedV1, ok := saved.(*releasev1.Release)
	if !ok {
		t.Fatalf("unexpected Helm release format: %T", saved)
	}
	for name, value := range values {
		if !reflect.DeepEqual(savedV1.Config[name], value) {
			t.Fatalf("enabling mirrors replaced the saved %s override", name)
		}
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(key), key); err != nil || key.UID != originalKeyUID || !bytes.Equal(key.Data["key"], originalKey) {
		t.Fatal("enabling mirrors replaced the encryption key")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(destination), destination); err != nil || destination.UID != destinationUID {
		t.Fatal("enabling mirrors replaced the destination namespace")
	}
	afterOperator, err := deployments.Get(ctx, "replicove", metav1.GetOptions{})
	if err != nil || afterOperator.UID != beforeOperator.UID {
		t.Fatal("enabling mirrors replaced the operator Deployment")
	}
	if !strings.Contains(strings.Join(afterOperator.Spec.Template.Spec.Containers[0].Args, " "), "--mirrors=true") {
		t.Fatal("mirror controller was not enabled")
	}
	if _, err := deployments.Get(ctx, "replicove-snapshot-controller", metav1.GetOptions{}); err != nil {
		t.Fatalf("late opt-in did not install snapshot controller: %v", err)
	}
	if _, err := kubeClient.RbacV1().Roles("source-dev").Get(ctx, "replicove-system-replicove-mirror", metav1.GetOptions{}); err != nil {
		t.Fatalf("late opt-in did not grant scoped snapshot permissions: %v", err)
	}
	// Real Helm storage often has nil Config when users accepted chart defaults.
	// An override must still work and must never mutate the source release.
	sourceChart, err := chartloader.Load(filepath.Join("..", "e2e", "chart"))
	if err != nil {
		t.Fatal(err)
	}
	sourceCfg, err := helmprovider.Configuration(config, "source-dev")
	if err != nil {
		t.Fatal(err)
	}
	seed := helmprovider.NewInstall(sourceCfg)
	seed.ReleaseName = "fixture"
	seed.Namespace = "source-dev"
	if _, err := seed.RunWithContext(ctx, sourceChart, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	source := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "source-dev", Name: "chart-settings"}, source); err != nil {
		t.Fatal(err)
	}
	sourceRV := source.ResourceVersion
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	reader := &capture.Reader{Config: config, Dynamic: dyn, Discovery: disc}
	request := &v1alpha1.ClusterReplica{Spec: v1alpha1.ClusterReplicaSpec{Replication: &v1alpha1.ReplicationSpec{NamespaceMap: map[string]string{"source-dev": "integration"}, HelmReleases: []v1alpha1.NamespacedName{{Namespace: "source-dev", Name: "fixture"}}, HelmOverrides: []v1alpha1.HelmOverride{{NamespacedName: v1alpha1.NamespacedName{Namespace: "source-dev", Name: "fixture"}, Values: apiextensionsv1.JSON{Raw: []byte(`{"message":"guest-chart"}`)}}}}}}
	grant := &v1alpha1.ReplicaGrant{Spec: v1alpha1.ReplicaGrantSpec{Resources: []v1alpha1.ResourceRule{{Kind: "ConfigMap"}}, HelmReleases: []v1alpha1.NamespacedName{{Namespace: "source-dev", Name: "fixture"}}}}
	plan, err := reader.Capture(ctx, request, grant, policy.Resolution{Namespaces: []string{"source-dev"}, MaxObjects: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Packages) != 1 || len(plan.Objects) != 1 || plan.Objects[0].Namespace != "integration" || plan.Objects[0].Desired["data"].(map[string]any)["message"] != "guest-chart" {
		t.Fatal("captured chart defaults/override or namespace mapping is incorrect")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(source), source); err != nil {
		t.Fatal(err)
	}
	if source.ResourceVersion != sourceRV || source.Data["message"] != "source-chart" {
		t.Fatal("capture modified the source")
	}

	fixture, err := os.ReadFile(filepath.Join("..", "e2e", "source.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := kYaml.NewYAMLOrJSONDecoder(strings.NewReader(string(fixture)), 4096)
	for {
		obj := &unstructured.Unstructured{}
		err := decoder.Decode(obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	selection, err := os.ReadFile(filepath.Join("..", "e2e", "replication.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	request.Spec.Replication = &v1alpha1.ReplicationSpec{}
	if err := yaml.UnmarshalStrict(selection, request.Spec.Replication); err != nil {
		t.Fatal(err)
	}
	grantFixture, err := os.ReadFile(filepath.Join("..", "e2e", "grant.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.UnmarshalStrict(grantFixture, grant); err != nil {
		t.Fatal(err)
	}
	plan, err = reader.Capture(ctx, request, grant, policy.Resolution{Namespaces: []string{"source-dev"}, MaxObjects: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Objects) != 6 {
		t.Fatalf("full fixture has %d objects", len(plan.Objects))
	}

}
