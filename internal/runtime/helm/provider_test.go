package helm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReleaseOwnershipRequiresUIDNamespaceAndName(t *testing.T) {
	req := runtimeprovider.Request{Namespace: "lab", OwnerUID: "uid", Reference: catalog.Resolve("uid")}
	for _, tc := range []struct {
		name, namespace, uid string
		want                 bool
	}{
		{req.Reference.ReleaseName, "lab", "uid", true},
		{req.Reference.ReleaseName, "lab", "other", false},
		{req.Reference.ReleaseName, "lab", "", false},
		{req.Reference.ReleaseName, "other", "uid", false},
		{"other", "lab", "uid", false},
	} {
		a, err := release.NewAccessor(&releasev1.Release{Name: tc.name, Namespace: tc.namespace, Labels: map[string]string{catalog.OwnerLabel: tc.uid}})
		if err != nil {
			t.Fatal(err)
		}
		if Owned(a, req) != tc.want {
			t.Fatalf("incorrect ownership decision: %+v", tc)
		}
	}
}

func TestManifestCleanupRejectsClusterAndForeignNamespaceResources(t *testing.T) {
	for _, manifest := range []string{
		"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: protected\n",
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: protected\n  namespace: production\n",
	} {
		if _, err := manifestObjects(manifest, "lab"); err == nil {
			t.Fatal("accepted cleanup outside namespace contract")
		}
	}
}

func TestCleanupDoesNotTreatRetainedOrForeignObjectsAsGone(t *testing.T) {
	manifest := "apiVersion: v1\nkind: Service\nmetadata:\n  name: example\n  namespace: lab\n"
	objects, err := manifestObjects(manifest, "lab")
	if err != nil {
		t.Fatal(err)
	}
	req := runtimeprovider.Request{Namespace: "lab", OwnerUID: "uid", Reference: catalog.Resolve("uid")}
	for _, tc := range []struct {
		name       string
		annotation map[string]string
		want       error
	}{
		{"foreign", map[string]string{}, runtimeprovider.ErrOwnership},
		{"retained", map[string]string{"meta.helm.sh/release-name": req.Reference.ReleaseName, "meta.helm.sh/release-namespace": "lab", "helm.sh/resource-policy": "keep"}, runtimeprovider.ErrCleanupIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := objects[0].DeepCopy()
			obj.SetAnnotations(tc.annotation)
			obj.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "Helm"})
			p := &Provider{Client: fake.NewClientBuilder().WithObjects(obj).Build()}
			_, err := p.remaining(context.Background(), objects, req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestObserveRequiresCurrentReadyOwnedDeployment(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	req := runtimeprovider.Request{Namespace: "lab", OwnerUID: "uid", Reference: catalog.Resolve("uid")}
	replicas := int32(1)
	set := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "control-plane", Namespace: "lab", Generation: 2, Labels: map[string]string{catalog.OwnerLabel: "uid"}, Annotations: map[string]string{"meta.helm.sh/release-name": req.Reference.ReleaseName, "meta.helm.sh/release-namespace": "lab"}}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set).WithObjects(set).Build()
	p := &Provider{Client: c}
	got, err := p.observe(context.Background(), req)
	if err != nil || got.Ready {
		t.Fatal("stale Deployment status reported ready")
	}
	set.Status.ObservedGeneration = 2
	if err := c.Status().Update(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	got, err = p.observe(context.Background(), req)
	if err != nil || !got.Ready {
		t.Fatalf("owned current ready set not recognized: %v", err)
	}
}

func TestArchiveDigestIsCheckedBeforeParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chart.tgz")
	if err := os.WriteFile(path, []byte("untrusted chart content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChart(context.Background(), path); err == nil {
		t.Fatal("accepted unpinned archive")
	}
}

// Compile-time assertion also catches accidental interface drift in dependencies.
var _ runtimeprovider.Provider = (*Provider)(nil)
