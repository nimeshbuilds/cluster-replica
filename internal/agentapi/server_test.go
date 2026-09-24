package agentapi

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
	"time"
)

func session(t *testing.T, b Backend) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	a, z := mcp.NewInMemoryTransports()
	server, err := New(b, "test")
	if err != nil {
		t.Fatal(err)
	}
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, z, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}
func backend(t *testing.T, write bool) Backend {
	t.Helper()
	s := runtime.NewScheme()
	_ = api.AddToScheme(s)
	o := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "lab", UID: "correct"}, Status: api.ClusterReplicaStatus{Phase: "Ready"}}
	other := o.DeepCopy()
	other.Namespace = "foreign"
	other.Name = "invisible"
	return Backend{Client: fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&api.ClusterReplica{}).WithObjects(o, other).Build(), Namespace: "lab", AllowWrite: write}
}

func TestMCPHandshakeReadOnlyAndNamespace(t *testing.T) {
	cs := session(t, backend(t, false))
	ctx := context.Background()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if strings.Contains(tool.Name, "delete") || strings.Contains(tool.Name, "create") {
			t.Fatal("default interface advertises mutation")
		}
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "replica_list", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("list failed: %v", err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "invisible") {
		t.Fatal("cross-namespace read")
	}
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "replica_plan", Arguments: map[string]any{"name": "invisible"}})
	if err != nil || !result.IsError {
		t.Fatalf("foreign object did not fail safely: %v", err)
	}
}

func TestMCPDeletionRequiresExactIdentity(t *testing.T) {
	b := backend(t, true)
	cs := session(t, b)
	ctx := context.Background()
	for _, uid := range []string{"", "reused"} {
		r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "replica_delete", Arguments: map[string]any{"name": "demo", "uid": uid}})
		if e != nil || !r.IsError {
			t.Fatalf("unsafe delete accepted: %v", e)
		}
	}
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: "lab", Name: "demo"}, &api.ClusterReplica{}); err != nil {
		t.Fatal("denied call deleted resource")
	}
	r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "replica_delete", Arguments: map[string]any{"name": "demo", "uid": "correct"}})
	if e != nil || r.IsError {
		t.Fatalf("authorized delete failed: %v", e)
	}
}

func TestStrictSpecNoAdditionalJSON(t *testing.T) {
	for _, input := range []string{`{"ttl":"2h","unknown":true}`, `{"ttl":"2h"} {"ttl":"3h"}`} {
		if decodeSpec(input, &api.ClusterReplicaSpec{}) == nil {
			t.Fatal("unrecognized input accepted")
		}
	}
}

func TestInvalidNamespace(t *testing.T) {
	for _, ns := range []string{"", "*", "other/ns"} {
		b := backend(t, false)
		b.Namespace = ns
		if _, err := New(b, "test"); err == nil {
			t.Fatalf("unsafe namespace %q accepted", ns)
		}
	}
}
