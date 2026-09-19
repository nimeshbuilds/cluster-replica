//go:build integration

package integration

import (
	"context"
	"errors"
	chartloader "helm.sh/helm/v4/pkg/chart/loader"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/controller"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	for _, name := range []string{"replicove-system", "replica-lab", "source-dev"} {
		if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	ch, err := chartloader.Load(filepath.Join("..", "..", "charts", "replicove"))
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
	key := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "replicove-system", Name: "replicove-state-key"}, key); err != nil {
		t.Fatal(err)
	}
	if len(key.Data["key"]) != 32 || key.Immutable == nil || !*key.Immutable {
		t.Fatal("state key is not immutable AES-256 material")
	}
}
