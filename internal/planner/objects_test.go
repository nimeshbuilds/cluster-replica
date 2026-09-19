package planner

import (
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func fixture(t *testing.T, s string) *unstructured.Unstructured {
	t.Helper()
	o := &unstructured.Unstructured{}
	if err := o.UnmarshalJSON([]byte(s)); err != nil {
		t.Fatal(err)
	}
	return o
}
func TestDependenciesAndExcludedSecret(t *testing.T) {
	o := fixture(t, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"source"},"spec":{"template":{"spec":{"serviceAccountName":"app","containers":[{"name":"app","env":[{"name":"TOKEN","valueFrom":{"secretKeyRef":{"name":"password","key":"token"}}}]}]}}}}`)
	refs := Dependencies(o)
	if len(refs) != 2 {
		t.Fatalf("references: %v", refs)
	}
	p := &state.Plan{Objects: []state.Object{{ID: ObjectID(o), Dependencies: refs}}}
	if err := Order(p); err == nil || !strings.Contains(err.Error(), "Missing") && !strings.Contains(err.Error(), "dependency") {
		t.Fatal("missing secret accepted")
	}
}
func TestTransformRemovesServerIdentityAndPreservesSource(t *testing.T) {
	o := fixture(t, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"source","uid":"old","resourceVersion":"4","finalizers":["foreign"]},"data":{"endpoint":"https://source.example"},"status":{"secret":"hidden"}}`)
	got, err := Transform(o, &api.ReplicationSpec{NamespaceMap: map[string]string{"source": "guest"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetUID() != "" || got.GetNamespace() != "guest" || got.Object["status"] != nil || len(got.GetFinalizers()) != 0 {
		t.Fatal("server identity copied")
	}
	if o.GetUID() != "old" || o.GetNamespace() != "source" {
		t.Fatal("source mutated")
	}
}
func TestExclusionsWinAndOrderIsStable(t *testing.T) {
	o := fixture(t, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"source"}}`)
	yes, no, err := Selected(&api.ReplicationSpec{Include: []api.ResourceSelector{{Kinds: []string{"ConfigMap"}}}, Exclude: []api.ResourceSelector{{Names: []string{"config"}}}}, o)
	if err != nil || yes || !no {
		t.Fatal("exclusion did not win")
	}
	p := &state.Plan{Objects: []state.Object{{ID: "b", Dependencies: []string{"a"}}, {ID: "a"}}}
	if err := Order(p); err != nil || strings.Join(p.Order, ",") != "a,b" {
		t.Fatal("unstable order")
	}
	p.Objects[1].Dependencies = []string{"b"}
	if err := Order(p); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestOverridesCannotAddHostPrivilegesOrOperationIdentity(t *testing.T) {
	source := fixture(t, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"source"},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"test"}]}}}}`)
	for _, patch := range []string{`{"spec":{"template":{"spec":{"hostNetwork":true}}}}`, `{"spec":{"template":{"spec":{"containers":[{"name":"app","securityContext":{"privileged":true}}]}}}}`, `{"metadata":{"annotations":{"replicove.nimeshbuilds.dev/operation":"forged"}}}`, `{"metadata":{"name":"different"}}`} {
		spec := &api.ReplicationSpec{Patches: []api.ObjectPatch{{ObjectReference: api.ObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "source", Name: "app"}, Patch: apiextensionsv1.JSON{Raw: []byte(patch)}}}}
		if _, err := Transform(source, spec); err == nil {
			t.Fatal("unsafe override accepted")
		}
	}
	service := fixture(t, `{"apiVersion":"v1","kind":"Service","metadata":{"name":"app","namespace":"source"},"spec":{"type":"LoadBalancer","clusterIP":"10.0.0.1","ports":[{"port":80,"nodePort":30080}]}}`)
	spec := &api.ReplicationSpec{Patches: []api.ObjectPatch{{ObjectReference: api.ObjectReference{APIVersion: "v1", Kind: "Service", Namespace: "source", Name: "app"}, Patch: apiextensionsv1.JSON{Raw: []byte(`{"spec":{"type":"ClusterIP"}}`)}}}}
	result, err := Transform(service, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := unstructured.NestedString(result.Object, "spec", "clusterIP"); ok {
		t.Fatal("source IP retained")
	}
}

func TestStatefulStorageOptInAndHeadlessService(t *testing.T) {
	service := fixture(t, `{"apiVersion":"v1","kind":"Service","metadata":{"name":"headless","namespace":"source"},"spec":{"clusterIP":"None","clusterIPs":["None"]}}`)
	got, err := Transform(service, &api.ReplicationSpec{})
	if err != nil {
		t.Fatal(err)
	}
	ip, _, _ := unstructured.NestedString(got.Object, "spec", "clusterIP")
	if ip != "None" {
		t.Fatal("headless service semantics lost")
	}
	set := fixture(t, `{"apiVersion":"apps/v1","kind":"StatefulSet","metadata":{"name":"database","namespace":"source"},"spec":{"volumeClaimTemplates":[{"metadata":{"name":"data"},"spec":{"storageClassName":"production","dataSource":{"name":"source-snapshot"}}}]}}`)
	if _, err := Transform(set, &api.ReplicationSpec{}); err == nil {
		t.Fatal("StatefulSet provisioned ungranted storage")
	}
	got, err = Transform(set, &api.ReplicationSpec{Data: "EmptyVolumes", StorageClassMap: map[string]string{"production": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	claims, _, _ := unstructured.NestedSlice(got.Object, "spec", "volumeClaimTemplates")
	spec := claims[0].(map[string]any)["spec"].(map[string]any)
	if spec["storageClassName"] != "test" || spec["dataSource"] != nil {
		t.Fatal("claim mapping or fresh-volume contract failed")
	}
}
