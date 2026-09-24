// Package agentapi exposes a small MCP surface. Each operation uses the
// configured caller's Kubernetes client, never the operator's privileged state.
package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/diagnostics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Backend struct {
	Client     client.Client
	Namespace  string
	AllowWrite bool
}
type Name struct {
	Name string `json:"name" jsonschema:"Exact replica name in the configured destination namespace"`
}
type Ref struct {
	Name string `json:"name"`
	UID  string `json:"uid" jsonschema:"Exact current Kubernetes UID; reusable names alone cannot authorize deletion"`
}
type Create struct {
	Name string `json:"name"`
	Spec string `json:"spec" jsonschema:"A JSON ClusterReplicaSpec, including grantRef, profile, ttl, replication and DeleteOwned cleanupPolicy"`
}
type Access struct {
	Name            string `json:"name"`
	UID             string `json:"uid"`
	Role            string `json:"role"`
	DurationSeconds int64  `json:"durationSeconds"`
}
type Mutation struct {
	Name        string `json:"name"`
	UID         string `json:"uid"`
	Operation   string `json:"operation"`
	Revision    string `json:"revision,omitempty"`
	RevisionUID string `json:"revisionUID,omitempty"`
}
type Reply struct {
	Name    string `json:"name,omitempty"`
	UID     string `json:"uid,omitempty"`
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message"`
}
type Empty struct{}
type Inventory struct {
	Replicas []diagnostics.Report `json:"replicas"`
}

func validName(name string) error {
	if name == "" || len(validation.IsDNS1123Subdomain(name)) > 0 {
		return errors.New("a valid exact Kubernetes name is required")
	}
	return nil
}
func (b Backend) replica(ctx context.Context, name string) (*api.ClusterReplica, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	o := &api.ClusterReplica{}
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: name}, o); err != nil {
		return nil, errors.New("replica unavailable or caller not authorized")
	}
	return o, nil
}
func decodeSpec(raw string, out any) error {
	if len(raw) > 1<<20 {
		return errors.New("request exceeds size limit")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid request spec")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON spec")
	}
	return nil
}

func New(b Backend, version string) (*mcp.Server, error) {
	if err := ValidateNamespace(b.Namespace); err != nil {
		return nil, err
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "replicove", Version: version}, nil)
	read := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "replica_list", Description: "List metadata-only replica status in the configured namespace; source data and credentials are never returned", Annotations: read}, func(ctx context.Context, r *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, Inventory, error) {
		list := &api.ClusterReplicaList{}
		if err := b.Client.List(ctx, list, client.InNamespace(b.Namespace)); err != nil {
			return nil, Inventory{}, errors.New("listing denied or unavailable")
		}
		out := Inventory{Replicas: []diagnostics.Report{}}
		for i := range list.Items {
			out.Replicas = append(out.Replicas, diagnostics.Explain(&list.Items[i]))
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "replica_plan", Description: "Explain captured selection, transformations, dependencies and unverified capabilities without exposing payloads", Annotations: read}, func(ctx context.Context, r *mcp.CallToolRequest, in Name) (*mcp.CallToolResult, diagnostics.Report, error) {
		o, err := b.replica(ctx, in.Name)
		if err != nil {
			return nil, diagnostics.Report{}, err
		}
		return nil, diagnostics.Explain(o), nil
	})
	// A bounded poll fits one MCP request; cancellation propagates to Kubernetes.
	mcp.AddTool(s, &mcp.Tool{Name: "replica_wait", Description: "Wait up to 30 seconds for readiness, returning the current metadata-only plan", Annotations: read}, func(ctx context.Context, r *mcp.CallToolRequest, in Name) (*mcp.CallToolResult, diagnostics.Report, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		for {
			o, err := b.replica(ctx, in.Name)
			if err != nil {
				return nil, diagnostics.Report{}, err
			}
			out := diagnostics.Explain(o)
			if o.Status.Phase == "Ready" || o.Status.Phase == "Rejected" || o.Status.Phase == "Expired" {
				return nil, out, nil
			}
			select {
			case <-ctx.Done():
				return nil, out, nil
			case <-time.After(time.Second):
			}
		}
	})
	if !b.AllowWrite {
		return s, nil
	}
	mcp.AddTool(s, &mcp.Tool{Name: "replica_create", Description: "Create a fresh immutable replica request. Kubernetes RBAC and the administrator ReplicaGrant both apply."}, func(ctx context.Context, r *mcp.CallToolRequest, in Create) (*mcp.CallToolResult, Reply, error) {
		if err := validName(in.Name); err != nil {
			return nil, Reply{}, err
		}
		var spec api.ClusterReplicaSpec
		if err := decodeSpec(in.Spec, &spec); err != nil {
			return nil, Reply{}, err
		}
		if spec.GrantRef == "" || spec.CleanupPolicy != "DeleteOwned" {
			return nil, Reply{}, errors.New("grantRef and DeleteOwned cleanup are required")
		}
		o := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: in.Name, Namespace: b.Namespace}, Spec: spec}
		if err := b.Client.Create(ctx, o); err != nil {
			return nil, Reply{}, errors.New("creation denied, invalid, or name already exists")
		}
		return nil, Reply{Name: o.Name, UID: string(o.UID), Message: "Request created; inspect the plan and readiness before testing."}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "replica_delete", Description: "Request deletion of an exact replica UID. Completion requires observing finalizer removal, not just this response."}, func(ctx context.Context, r *mcp.CallToolRequest, in Ref) (*mcp.CallToolResult, Reply, error) {
		o, err := b.replica(ctx, in.Name)
		if err != nil {
			return nil, Reply{}, err
		}
		if in.UID == "" || string(o.UID) != in.UID {
			return nil, Reply{}, errors.New("replica UID mismatch")
		}
		if err := b.Client.Delete(ctx, o, client.Preconditions{UID: &o.UID, ResourceVersion: &o.ResourceVersion}); err != nil {
			return nil, Reply{}, errors.New("deletion denied or concurrent modification")
		}
		return nil, Reply{Name: o.Name, UID: string(o.UID), Message: "Deletion requested; wait until the exact request is gone to confirm cleanup."}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "replica_access_request", Description: "Request bounded guest access for an exact replica UID. Returns only the request identity; credential retrieval requires separate exact-Secret Kubernetes RBAC."}, func(ctx context.Context, r *mcp.CallToolRequest, in Access) (*mcp.CallToolResult, Reply, error) {
		o, err := b.replica(ctx, in.Name)
		if err != nil {
			return nil, Reply{}, err
		}
		if in.UID == "" || string(o.UID) != in.UID {
			return nil, Reply{}, errors.New("replica UID mismatch")
		}
		if in.Role != "viewer" && in.Role != "deployer" && in.Role != "admin" {
			return nil, Reply{}, errors.New("invalid guest role")
		}
		if in.DurationSeconds < 600 || in.DurationSeconds > 3600 {
			return nil, Reply{}, errors.New("duration must be between 600 and 3600 seconds")
		}
		a := &api.ReplicaAccess{ObjectMeta: metav1.ObjectMeta{GenerateName: o.Name + "-agent-", Namespace: b.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "ClusterReplica", Name: o.Name, UID: o.UID}}}, Spec: api.ReplicaAccessSpec{ReplicaName: o.Name, ReplicaUID: in.UID, Role: in.Role, DurationSeconds: in.DurationSeconds}}
		if err := b.Client.Create(ctx, a); err != nil {
			return nil, Reply{}, errors.New("access request denied or unavailable")
		}
		return nil, Reply{Name: a.Name, UID: string(a.UID), Message: "Access requested; retrieve credentials through the documented Kubernetes or CLI workflow."}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "mirror_run", Description: "Request a latest-source Sync or retained-revision Reset for the exact mirror UID. May discard guest changes; existing grants and test leases apply."}, func(ctx context.Context, r *mcp.CallToolRequest, in Mutation) (*mcp.CallToolResult, Reply, error) {
		if err := validName(in.Name); err != nil {
			return nil, Reply{}, err
		}
		o := &api.ReplicaMirror{}
		if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: in.Name}, o); err != nil {
			return nil, Reply{}, errors.New("mirror denied or unavailable")
		}
		if in.UID == "" || string(o.UID) != in.UID {
			return nil, Reply{}, errors.New("mirror UID mismatch")
		}
		if in.Operation != "Sync" && in.Operation != "Reset" {
			return nil, Reply{}, errors.New("operation must be Sync or Reset")
		}
		if (in.Operation == "Reset") != (in.Revision != "") {
			return nil, Reply{}, errors.New("only Reset requires a retained revision reference")
		}
		x := &api.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{GenerateName: o.Name + "-agent-", Namespace: b.Namespace}, Spec: api.ReplicaMirrorRunSpec{MirrorRef: api.MirrorObjectRef{Name: o.Name, UID: in.UID}, Action: in.Operation}}
		if in.Operation == "Reset" {
			if in.RevisionUID == "" {
				return nil, Reply{}, errors.New("Reset requires the exact retained revision UID")
			}
			x.Spec.RevisionRef = &api.MirrorObjectRef{Name: in.Revision, UID: in.RevisionUID}
		}
		if err := b.Client.Create(ctx, x); err != nil {
			return nil, Reply{}, errors.New("mirror run denied or invalid")
		}
		return nil, Reply{Name: x.Name, UID: string(x.UID), Message: "Mirror run requested; poll its status before requesting access."}, nil
	})
	return s, nil
}

// ValidateNamespace prevents an empty namespace from expanding list operations cluster-wide.
func ValidateNamespace(namespace string) error {
	if namespace == "" || len(validation.IsDNS1123Label(namespace)) > 0 {
		return errors.New("a nonempty valid Kubernetes namespace is required")
	}
	return nil
}
