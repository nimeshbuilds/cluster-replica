// Package testrun runs local commands against freshly created, bounded replicas.
// It deliberately never adopts existing requests or changes the user's kubeconfig.
package testrun

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"regexp"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

const RecipeVersion = "replicove.nimeshbuilds.dev/v1alpha1"

// Recipe is a local CLI document, not a Kubernetes custom resource. Embedding
// only specs prevents status, finalizers, names or owner references being adopted.
type Recipe struct {
	APIVersion   string                  `json:"apiVersion"`
	Kind         string                  `json:"kind"`
	NamePrefix   string                  `json:"namePrefix,omitempty"`
	Destinations []Destination           `json:"destinations,omitempty"`
	Replica      *api.ClusterReplicaSpec `json:"replica,omitempty"`
	Mirror       *api.ReplicaMirrorSpec  `json:"mirror,omitempty"`
	Execution    Execution               `json:"execution"`
	Lifecycle    Lifecycle               `json:"lifecycle,omitempty"`
	// ExpectedPlanRevision is an optional assertion, not a replay mechanism.
	ExpectedPlanRevision string `json:"expectedPlanRevision,omitempty"`
	Chaos                *Chaos `json:"chaos,omitempty"`
}

type Execution struct {
	Command           []string `json:"command,omitempty"`
	Timeout           string   `json:"timeout,omitempty"`
	Role              string   `json:"role,omitempty"`
	CredentialSeconds int64    `json:"credentialSeconds,omitempty"`
}

type Destination struct {
	Namespace string `json:"namespace"`
	Grant     string `json:"grant"`
}

type Lifecycle struct {
	ReadyTimeout   string `json:"readyTimeout,omitempty"`
	CleanupTimeout string `json:"cleanupTimeout,omitempty"`
	// KeepOnFailure retains only this run's request until its original TTL.
	KeepOnFailure bool `json:"keepOnFailure,omitempty"`
}

type Chaos struct {
	DurationSeconds int64            `json:"durationSeconds"`
	Faults          []api.ChaosFault `json:"faults"`
}

func Parse(data []byte) (Recipe, error) {
	var out Recipe
	if len(data) > 1024*1024 {
		return out, errors.New("test recipe exceeds 1 MiB")
	}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	doc, err := reader.Read()
	if err != nil || len(bytes.TrimSpace(doc)) == 0 {
		return out, errors.New("test recipe must contain one YAML document")
	}
	if _, err = reader.Read(); err != io.EOF {
		return out, errors.New("test recipe must contain exactly one YAML document")
	}
	if err := yaml.UnmarshalStrict(doc, &out); err != nil {
		// Parser errors may quote embedded source values. Never include them.
		return out, errors.New("invalid test recipe YAML: check field names and value types")
	}
	return out, nil
}

func (r *Recipe) DefaultAndValidate() error {
	if r.APIVersion != RecipeVersion || r.Kind != "TestRecipe" {
		return errors.New("use a replicove.nimeshbuilds.dev/v1alpha1 TestRecipe")
	}
	if (r.Replica == nil) == (r.Mirror == nil) {
		return errors.New("recipe requires exactly one replica or mirror spec")
	}
	if len(r.Destinations) > 32 {
		return errors.New("a recipe supports at most 32 destination members")
	}
	seen := map[string]bool{}
	for _, d := range r.Destinations {
		if len(d.Namespace) > 63 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(d.Namespace) || len(d.Grant) > 253 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`).MatchString(d.Grant) || seen[d.Namespace] {
			return errors.New("destinations require unique DNS namespace names and explicit grant names")
		}
		seen[d.Namespace] = true
	}
	if r.NamePrefix == "" {
		r.NamePrefix = "test"
	}
	if len(r.NamePrefix) > 40 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(r.NamePrefix) {
		return errors.New("namePrefix must be a DNS label of at most 40 characters")
	}
	if r.Execution.Timeout == "" {
		r.Execution.Timeout = "10m"
	}
	if r.Execution.Role == "" {
		r.Execution.Role = "viewer"
	}
	if r.Execution.CredentialSeconds == 0 {
		r.Execution.CredentialSeconds = 900
	}
	if r.Lifecycle.ReadyTimeout == "" {
		r.Lifecycle.ReadyTimeout = "20m"
	}
	if r.Lifecycle.CleanupTimeout == "" {
		r.Lifecycle.CleanupTimeout = "15m"
	}
	if len(r.Execution.Command) == 0 || r.Execution.Command[0] == "" {
		return errors.New("provide execution.command or a command after --")
	}
	for _, arg := range r.Execution.Command {
		if bytes.IndexByte([]byte(arg), 0) >= 0 {
			return errors.New("command arguments cannot contain NUL bytes")
		}
	}
	if r.Execution.Role != "viewer" && r.Execution.Role != "deployer" && r.Execution.Role != "admin" {
		return errors.New("execution.role must be viewer, deployer or admin")
	}
	test, e1 := time.ParseDuration(r.Execution.Timeout)
	ready, e2 := time.ParseDuration(r.Lifecycle.ReadyTimeout)
	cleanup, e3 := time.ParseDuration(r.Lifecycle.CleanupTimeout)
	if e1 != nil || e2 != nil || e3 != nil || test <= 0 || test > 27*time.Minute || ready <= 0 || ready > 2*time.Hour || cleanup <= 0 || cleanup > time.Hour {
		return errors.New("timeouts must be positive: execution at most 27m, ready at most 2h, cleanup at most 1h")
	}
	if r.Execution.CredentialSeconds < 600 || r.Execution.CredentialSeconds > 3600 || time.Duration(r.Execution.CredentialSeconds)*time.Second < test+30*time.Second {
		return errors.New("credentialSeconds must be 600..3600 and cover execution.timeout plus 30s; the grant must permit this duration")
	}
	spec := r.Replica
	if r.Mirror != nil {
		spec = &r.Mirror.Template
		if r.Mirror.Suspend || r.Mirror.HoldUntil != nil || r.Mirror.Interval != "" {
			return errors.New("test mirrors must start unsuspended with no holdUntil or scheduled interval")
		}
	}
	if spec.CleanupPolicy != "DeleteOwned" || spec.Approval == "Manual" || spec.GrantRef == "" && len(r.Destinations) == 0 {
		return errors.New("test requests require cleanupPolicy DeleteOwned, Automatic approval and grantRef")
	}
	if spec.Target != nil && spec.Target.Provider == "existing" {
		return errors.New("test recipes require a fresh managed runtime; existing targets are not owned by the runner")
	}
	if spec.Replication != nil && spec.Replication.Secrets == "Follow" {
		return errors.New("test recipes require pinned secrets; use None or Snapshot instead of Follow")
	}
	// Reuse exact pinned profile and TTL validation without the legacy cleanup mode.
	ttl, err := catalog.Validate(api.ClusterReplicaSpec{Profile: spec.Profile, TTL: spec.TTL, CleanupPolicy: "HelmReleaseOnly"})
	if err != nil || ttl <= ready+test+cleanup || ttl < ready+10*time.Minute+cleanup {
		return errors.New("request TTL must exceed ready + execution + cleanup timeouts and leave ten minutes for credential issuance")
	}
	if r.Chaos != nil && (r.Chaos.DurationSeconds < 1 || r.Chaos.DurationSeconds > 900 || r.Chaos.DurationSeconds > int64(test.Seconds()) || len(r.Chaos.Faults) == 0 || len(r.Chaos.Faults) > 8) {
		return errors.New("chaos requires 1..8 faults and a duration of 1..900 seconds no greater than execution.timeout")
	}
	return nil
}

func duration(value string) time.Duration { d, _ := time.ParseDuration(value); return d }
