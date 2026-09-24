package testrun

import (
	"strings"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func TestStrictRecipeParsing(t *testing.T) {
	fixture, _ := yaml.Marshal(recipeFixture())
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"unknown", append(append([]byte(nil), fixture...), []byte("secretTypo: do-not-echo-this-secret\n")...)},
		{"duplicate", append(append([]byte(nil), fixture...), []byte("kind: Other\n")...)},
		{"multiple", append(append([]byte(nil), fixture...), []byte("---\nkind: Other\n")...)},
		{"empty", nil},
		{"oversized", []byte(strings.Repeat("a", 1024*1024+1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.data)
			if err == nil {
				t.Fatal("accepted malformed recipe")
			}
			if strings.Contains(err.Error(), "do-not-echo") {
				t.Fatal("parser disclosed source")
			}
		})
	}
	parsed, err := Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.DefaultAndValidate(); err != nil {
		t.Fatal(err)
	}
	if parsed.Execution.Role != "viewer" || parsed.Execution.CredentialSeconds != 900 || parsed.Lifecycle.CleanupTimeout != "15m" {
		t.Fatalf("%+v", parsed)
	}
}

func TestRecipeRejectsUnsafeOrUnboundedLifecycle(t *testing.T) {
	tests := map[string]func(*Recipe){
		"no target":           func(r *Recipe) { r.Replica = nil },
		"both targets":        func(r *Recipe) { r.Mirror = &api.ReplicaMirrorSpec{} },
		"wrong version":       func(r *Recipe) { r.APIVersion = "v1" },
		"wrong kind":          func(r *Recipe) { r.Kind = "ClusterReplica" },
		"bad prefix":          func(r *Recipe) { r.NamePrefix = "../name" },
		"no command":          func(r *Recipe) { r.Execution.Command = nil },
		"nul command":         func(r *Recipe) { r.Execution.Command = []string{"exe\x00"} },
		"wrong role":          func(r *Recipe) { r.Execution.Role = "root" },
		"short token":         func(r *Recipe) { r.Execution.CredentialSeconds = 599 },
		"oversized token":     func(r *Recipe) { r.Execution.CredentialSeconds = 3601 },
		"token expires":       func(r *Recipe) { r.Execution.Timeout = "20m" },
		"excessive duration":  func(r *Recipe) { r.Execution.Timeout = "31m"; r.Execution.CredentialSeconds = 3600 },
		"invalid duration":    func(r *Recipe) { r.Execution.Timeout = "forever" },
		"excessive ready":     func(r *Recipe) { r.Lifecycle.ReadyTimeout = "3h" },
		"excessive cleanup":   func(r *Recipe) { r.Lifecycle.CleanupTimeout = "2h" },
		"legacy cleanup":      func(r *Recipe) { r.Replica.CleanupPolicy = "HelmReleaseOnly" },
		"manual approval":     func(r *Recipe) { r.Replica.Approval = "Manual" },
		"no grant":            func(r *Recipe) { r.Replica.GrantRef = "" },
		"existing runtime":    func(r *Recipe) { r.Replica.Target = &api.TargetSpec{Provider: "existing", ExistingRef: "shared"} },
		"short ttl":           func(r *Recipe) { r.Replica.TTL = "30m" },
		"unsupported profile": func(r *Recipe) { r.Replica.Profile = "latest" },
		"empty chaos":         func(r *Recipe) { r.Chaos = &Chaos{DurationSeconds: 60} },
		"long chaos": func(r *Recipe) {
			r.Chaos = &Chaos{DurationSeconds: 601, Faults: []api.ChaosFault{{Kind: "NetworkIsolation", Namespace: "guest"}}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r := recipeFixture()
			mutate(&r)
			if err := r.DefaultAndValidate(); err == nil {
				t.Fatal("accepted invalid recipe")
			}
		})
	}
}

func TestRecipeMirrorDoesNotScheduleResetDuringTest(t *testing.T) {
	for _, mode := range []string{"suspend", "interval", "hold"} {
		r := recipeFixture()
		r.Mirror = &api.ReplicaMirrorSpec{Template: *r.Replica}
		r.Replica = nil
		switch mode {
		case "suspend":
			r.Mirror.Suspend = true
		case "interval":
			r.Mirror.Interval = "1h"
		case "hold":
			r.Mirror.HoldUntil = &metav1.Time{}
		}
		if err := r.DefaultAndValidate(); err == nil {
			t.Fatalf("accepted %s", mode)
		}
	}
}
