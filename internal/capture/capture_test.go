package capture

import (
	"context"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

type discoveryStub struct {
	*discoveryfake.FakeDiscovery
	lists []*metav1.APIResourceList
}

func (d discoveryStub) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return d.lists, nil
}
func TestExactSecretReadsAndNamespaceSelection(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	objects := []runtime.Object{&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "source", UID: "cm"}, Data: map[string]string{"mode": "test"}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "allowed", Namespace: "source", UID: "secret"}, Data: map[string][]byte{"password": []byte("fixture-only")}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ungranted", Namespace: "source", UID: "no"}}}
	dyn := dynamicfake.NewSimpleDynamicClient(scheme, objects...)
	disc := discoveryStub{FakeDiscovery: &discoveryfake.FakeDiscovery{Fake: &ktesting.Fake{}, FakedServerVersion: &version.Info{GitVersion: "v1.36.4"}}, lists: []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"list", "get"}}, {Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: []string{"list", "get"}}}}}}
	reader := &Reader{Dynamic: dyn, Discovery: disc}
	grant := &api.ReplicaGrant{Spec: api.ReplicaGrantSpec{Resources: []api.ResourceRule{{Kind: "ConfigMap"}}, Secrets: []api.NamespacedName{{Namespace: "source", Name: "allowed"}}}}
	request := &api.ClusterReplica{Spec: api.ClusterReplicaSpec{Replication: &api.ReplicationSpec{Secrets: "Snapshot", NamespaceMap: map[string]string{"source": "guest"}}}}
	plan, err := reader.Capture(context.Background(), request, grant, policy.Resolution{Namespaces: []string{"source"}, MaxObjects: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Objects) != 2 {
		t.Fatalf("got %d objects", len(plan.Objects))
	}
	for _, obj := range plan.Objects {
		if obj.Namespace != "guest" {
			t.Fatal("namespace not mapped")
		}
	}
	for _, action := range dyn.Actions() {
		if action.GetResource().Resource == "secrets" && action.GetVerb() != "get" {
			t.Fatal("read all Secrets despite exact-name delegation")
		}
		if action.GetNamespace() != "source" {
			t.Fatal("read outside source scope")
		}
	}
	request.Spec.Replication.Secrets = "None"
	dyn.ClearActions()
	plan, err = reader.Capture(context.Background(), request, grant, policy.Resolution{Namespaces: []string{"source"}, MaxObjects: 10})
	if err != nil || len(plan.Objects) != 1 {
		t.Fatal("secret opt-out ignored")
	}
	for _, action := range dyn.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Fatal("read Secrets despite opt-out")
		}
	}
}
