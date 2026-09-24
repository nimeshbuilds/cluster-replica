// Package diagnostics exposes metadata-only reproduction evidence. It never
// serializes source bodies, patches, Helm values or credential material.
package diagnostics

import (
	"fmt"
	"io"
	"reflect"
	"sort"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"time"
)

type Finding struct {
	Check  string `json:"check"`
	Result string `json:"result"`
	Detail string `json:"detail"`
}
type Report struct {
	ExpiresAt     string                `json:"expiresAt,omitempty"`
	CapturedAt    string                `json:"capturedAt,omitempty"`
	Runtime       *api.RuntimeReference `json:"runtime,omitempty"`
	Name          string                `json:"name"`
	Namespace     string                `json:"namespace"`
	UID           string                `json:"uid,omitempty"`
	Phase         string                `json:"phase"`
	SourceVersion string                `json:"sourceVersion,omitempty"`
	TargetVersion string                `json:"targetVersion,omitempty"`
	Revision      string                `json:"revision,omitempty"`
	Resources     []api.PlannedResource `json:"resources,omitempty"`
	Omitted       map[string]int32      `json:"omitted,omitempty"`
	Findings      []Finding             `json:"findings"`
}

// Transformations deliberately uses a closed vocabulary. Even custom field
// names or map keys can contain sensitive values and are not exposed.
func Transformations(source, desired *unstructured.Unstructured) []string {
	out := []string{"server-metadata-removed"}
	if source.GetNamespace() != desired.GetNamespace() {
		out = append(out, "namespace-mapped")
	}
	for _, part := range []struct{ key, label string }{{"spec", "spec-transformed"}, {"data", "data-transformed"}, {"stringData", "data-transformed"}, {"type", "type-transformed"}, {"rules", "authorization-transformed"}, {"roleRef", "authorization-transformed"}, {"subjects", "authorization-transformed"}, {"webhooks", "webhooks-transformed"}} {
		if !reflect.DeepEqual(source.Object[part.key], desired.Object[part.key]) {
			out = append(out, part.label)
		}
	}
	sort.Strings(out)
	return out
}

func Explain(obj *api.ClusterReplica) Report {
	r := Report{Runtime: obj.Status.Runtime, Name: obj.Name, Namespace: obj.Namespace, UID: string(obj.UID), Phase: obj.Status.Phase, SourceVersion: obj.Status.SourceVersion, TargetVersion: obj.Status.TargetVersion}
	if obj.Status.ExpiresAt != nil {
		r.ExpiresAt = obj.Status.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if obj.Status.Plan != nil {
		r.CapturedAt = obj.Status.Plan.CapturedAt.UTC().Format(time.RFC3339)
		r.Revision = obj.Status.Plan.Revision
		r.Resources = obj.Status.Plan.Resources
		r.Omitted = obj.Status.Plan.Omitted
		r.Findings = append(r.Findings, Finding{"capture", "recorded", "Selection and dependencies are bounded by the grant; source API reads are not an atomic cluster snapshot."})
	} else {
		r.Findings = append(r.Findings, Finding{"capture", "pending", "No captured plan is available yet."})
	}
	if obj.Status.Phase == "Ready" {
		r.Findings = append(r.Findings, Finding{"readiness", "passed", "Configured Kubernetes readiness checks passed; this is not proof of application correctness."})
	}
	if obj.Status.DriftCount > 0 {
		r.Findings = append(r.Findings, Finding{"drift", "observed", fmt.Sprintf("%d captured objects differ from their desired state.", obj.Status.DriftCount)})
	}
	for _, c := range obj.Status.Conditions {
		if c.Status == "False" {
			r.Findings = append(r.Findings, Finding{c.Type, c.Reason, c.Message})
		}
	}
	if obj.Spec.Replication != nil {
		s := obj.Spec.Replication
		if len(s.Patches) > 0 || len(s.HelmOverrides) > 0 {
			r.Findings = append(r.Findings, Finding{"overrides", "configured", "Explicit patches or Helm overrides apply; values are omitted from this report."})
		}
		if s.Data == "EmptyVolumes" {
			r.Findings = append(r.Findings, Finding{"volume-data", "empty", "Ordinary application PVCs start empty. This report does not imply a source data copy."})
		}
		if s.Secrets != "" && s.Secrets != "None" {
			r.Findings = append(r.Findings, Finding{"secrets", s.Secrets, "Only explicitly granted secrets are eligible; payloads are never included in this report."})
		}
	}
	r.Findings = append(r.Findings, Finding{"external-dependencies", "unverified", "Cloud IAM, external services, arbitrary embedded endpoints, kernel behavior and application consistency need explicit adapters or tests."})
	return r
}

func Write(w io.Writer, r Report) error {
	if _, err := fmt.Fprintf(w, "Replica %s/%s — %s\nPlan revision: %s\n", r.Namespace, r.Name, r.Phase, r.Revision); err != nil {
		return err
	}
	for _, f := range r.Findings {
		if _, err := fmt.Fprintf(w, "[%s] %s: %s\n", f.Result, f.Check, f.Detail); err != nil {
			return err
		}
	}
	for _, o := range r.Resources {
		if _, err := fmt.Fprintf(w, "%s %s/%s -> %s/%s (%s)\n  transformations: %v\n  dependencies: %v\n", o.Kind, o.SourceNamespace, o.SourceName, o.Namespace, o.Name, o.SelectionReason, o.Transformations, o.Dependencies); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(r.Omitted))
	for k := range r.Omitted {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := fmt.Fprintf(w, "Observed omissions: %s=%d\n", k, r.Omitted[k]); err != nil {
			return err
		}
	}
	return nil
}
