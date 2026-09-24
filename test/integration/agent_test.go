//go:build integration

package integration

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/agentapi"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"testing"
	"time"
)

func TestAgentProtocolAuthorization(t *testing.T) {
	environment := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd")}, ErrorIfCRDPathMissing: true}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	}()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, o := range []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "agent-lab"}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "inspect", Namespace: "agent-lab"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{api.GroupVersion.Group}, Resources: []string{"clusterreplicas"}, Verbs: []string{"get", "list"}}}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "inspect", Namespace: "agent-lab"}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "inspect"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: "rbac.authorization.k8s.io", Name: "test-agent"}}},
		&api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "agent-lab"}, Spec: api.ClusterReplicaSpec{Profile: "vcluster-0.37.1-lab", TTL: "2h", CleanupPolicy: "DeleteOwned", GrantRef: "fixture"}},
	} {
		if err := admin.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	callerCfg := rest.CopyConfig(cfg)
	callerCfg.Impersonate = rest.ImpersonationConfig{UserName: "test-agent"}
	caller, err := client.New(callerCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	// The server exposes mutations, but the caller's real RBAC must still deny them.
	server, err := agentapi.New(agentapi.Backend{Client: caller, Namespace: "agent-lab", AllowWrite: true}, "integration")
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	request := &api.ClusterReplica{}
	if err := admin.Get(ctx, client.ObjectKey{Namespace: "agent-lab", Name: "fixture"}, request); err != nil {
		t.Fatal(err)
	}
	for attempts := 0; ; attempts++ {
		r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "replica_plan", Arguments: map[string]any{"name": "fixture"}})
		if e == nil && !r.IsError {
			break
		}
		if attempts > 20 {
			t.Fatalf("RBAC reader never ready: %v", e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, p := range []*mcp.CallToolParams{
		{Name: "replica_delete", Arguments: map[string]any{"name": "fixture", "uid": string(request.UID)}},
		{Name: "replica_access_request", Arguments: map[string]any{"name": "fixture", "uid": string(request.UID), "role": "admin", "durationSeconds": 900}},
		{Name: "replica_create", Arguments: map[string]any{"name": "escape", "spec": `{"profile":"vcluster-0.37.1-lab","ttl":"2h","cleanupPolicy":"DeleteOwned","grantRef":"fixture"}`}},
	} {
		r, e := cs.CallTool(ctx, p)
		if e != nil || !r.IsError {
			t.Fatalf("tool %s bypassed Kubernetes RBAC: %v", p.Name, e)
		}
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(request), &api.ClusterReplica{}); err != nil {
		t.Fatal("denied delete changed state")
	}
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "inspect", Namespace: "agent-lab"}}
	if err := admin.Delete(ctx, binding); err != nil {
		t.Fatal(err)
	}
	for attempts := 0; ; attempts++ {
		r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "replica_plan", Arguments: map[string]any{"name": "fixture"}})
		if e == nil && r.IsError {
			break
		}
		if attempts > 30 {
			t.Fatal("same MCP session retained revoked authorization")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
