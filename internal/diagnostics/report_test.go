package diagnostics

import (
	"encoding/json"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
	"testing"
)

func TestReportNeverIncludesRequestPayloads(t *testing.T) {
	secret := "do-not-expose-this-value"
	o := &api.ClusterReplica{Spec: api.ClusterReplicaSpec{Replication: &api.ReplicationSpec{Patches: []api.ObjectPatch{{Patch: ext.JSON{Raw: []byte(`{"data":{"password":"` + secret + `"}}`)}}}}}}
	data, err := json.Marshal(Explain(o))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("report disclosed patch data")
	}
	source := &unstructured.Unstructured{Object: map[string]any{"data": map[string]any{secret: secret}, secret: secret, "metadata": map[string]any{"namespace": "source"}}}
	desired := &unstructured.Unstructured{Object: map[string]any{"data": map[string]any{}, "metadata": map[string]any{"namespace": "test"}}}
	data, _ = json.Marshal(Transformations(source, desired))
	if strings.Contains(string(data), secret) {
		t.Fatal("transformation report disclosed opaque field names")
	}
	if !strings.Contains(string(data), "namespace-mapped") || !strings.Contains(string(data), "data-transformed") {
		t.Fatal("missing transformation explanation")
	}
}

func TestReadyDoesNotClaimApplicationOrStorageProof(t *testing.T) {
	o := &api.ClusterReplica{Spec: api.ClusterReplicaSpec{Replication: &api.ReplicationSpec{Data: "EmptyVolumes"}}, Status: api.ClusterReplicaStatus{Phase: "Ready", DriftCount: 2, Plan: &api.PlanSummary{Revision: "pinned"}}}
	r := Explain(o)
	if r.Revision != "pinned" {
		t.Fatal("lost plan identity")
	}
	results := map[string]string{}
	for _, f := range r.Findings {
		results[f.Check] = f.Result
	}
	for k, want := range map[string]string{"readiness": "passed", "external-dependencies": "unverified", "volume-data": "empty", "drift": "observed"} {
		if results[k] != want {
			t.Fatalf("%s=%s", k, results[k])
		}
	}
}
