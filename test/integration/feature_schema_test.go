//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/capacity"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/chaos"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chartloader "helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/release"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kYaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

func TestExperimentAndDatabaseAPISchema(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Fatal("run make test-integration to fetch the pinned local API server")
	}
	environment := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd")}, ErrorIfCRDPathMissing: true}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	k, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, ns := range []string{"replica-lab", "replicove-system"} {
		if err := k.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
			t.Fatal(err)
		}
	}
	key := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "replicove-system"}, Data: map[string][]byte{"key": make([]byte, 32)}}
	if err := k.Create(ctx, key); err != nil {
		t.Fatal(err)
	}
	read := func(path string, out any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.UnmarshalStrict(data, out); err != nil {
			t.Fatal(err)
		}
	}
	grant := &api.ReplicaGrant{}
	read("examples/postgresql/grant.yaml", grant)
	t.Run("published PostgreSQL delegation survives API storage", func(t *testing.T) {
		if err := k.Create(ctx, grant); err != nil {
			t.Fatal(err)
		}
		stored := &api.ReplicaGrant{}
		if err := k.Get(ctx, client.ObjectKeyFromObject(grant), stored); err != nil {
			t.Fatal(err)
		}
		if len(stored.Spec.Databases) != 1 || !reflect.DeepEqual(stored.Spec.Databases, grant.Spec.Databases) {
			t.Fatal("database policy fields were pruned or changed")
		}
		if stored.Spec.Databases[0].Masking[2].Strategy != "Null" {
			t.Fatal("Null masking strategy did not remain a string")
		}
	})
	t.Run("published PostgreSQL request retains generated credential dependencies", func(t *testing.T) {
		spec := &api.ReplicationSpec{}
		read("examples/postgresql/replication.yaml", spec)
		o := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "database-example", Namespace: "replica-lab"}, Spec: api.ClusterReplicaSpec{Profile: catalog.PersistentProfile, TTL: "1h", CleanupPolicy: "DeleteOwned", GrantRef: grant.Name, Replication: spec}}
		if err := k.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
		stored := &api.ClusterReplica{}
		if err := k.Get(ctx, client.ObjectKeyFromObject(o), stored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored.Spec.Replication.Databases, spec.Databases) || len(stored.Spec.Replication.Patches) != 1 || !strings.Contains(string(stored.Spec.Replication.Patches[0].Patch.Raw), "test-database") {
			t.Fatal("database copy or application wiring was pruned")
		}
	})
	var admitted *api.ReplicaExperiment
	t.Run("published chaos manifests and status subresource", func(t *testing.T) {
		for _, file := range []string{"examples/chaos/experiment.yaml", "examples/chaos/custom-job.yaml"} {
			x := &api.ReplicaExperiment{}
			read(file, x)
			x.Spec.ReplicaRef.UID = "current-replica-uid"
			for i := range x.Spec.Faults {
				if x.Spec.Faults[i].Target != nil {
					x.Spec.Faults[i].Target.UID = "current-guest-workload-uid"
				}
			}
			x.Status.Phase = "Spoofed"
			if err := k.Create(ctx, x); err != nil {
				t.Fatalf("%s rejected: %v", file, err)
			}
			stored := &api.ReplicaExperiment{}
			if err := k.Get(ctx, client.ObjectKeyFromObject(x), stored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored.Spec, x.Spec) || stored.Status.Phase != "" {
				t.Fatal("schema changed spec or admitted status through create")
			}
			stored.Status.Phase = "Preparing"
			if err := k.Status().Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(file, "experiment.yaml") {
				admitted = stored
			}
		}
	})
	t.Run("entire experiment spec is immutable", func(t *testing.T) {
		if admitted == nil {
			t.Fatal("published example was not admitted")
		}
		for name, mutate := range map[string]func(*api.ReplicaExperiment){
			"replica name": func(x *api.ReplicaExperiment) { x.Spec.ReplicaRef.Name = "other" },
			"replica UID":  func(x *api.ReplicaExperiment) { x.Spec.ReplicaRef.UID = "replacement" },
			"duration":     func(x *api.ReplicaExperiment) { x.Spec.DurationSeconds = 120 },
			"namespace":    func(x *api.ReplicaExperiment) { x.Spec.Faults[0].Namespace = "different" },
			"workload UID": func(x *api.ReplicaExperiment) { x.Spec.Faults[0].Target.UID = "replacement" },
			"extra fault": func(x *api.ReplicaExperiment) {
				x.Spec.Faults = append(x.Spec.Faults, api.ChaosFault{Kind: "NetworkIsolation", Namespace: "integration"})
			},
		} {
			t.Run(name, func(t *testing.T) {
				bad := admitted.DeepCopy()
				mutate(bad)
				if err := k.Update(ctx, bad); !apierrors.IsInvalid(err) {
					t.Fatalf("spec mutation was admitted: %v", err)
				}
			})
		}
	})
	newExperiment := func() *api.ReplicaExperiment {
		return &api.ReplicaExperiment{ObjectMeta: metav1.ObjectMeta{GenerateName: "schema-fault-", Namespace: "replica-lab"}, Spec: api.ReplicaExperimentSpec{ReplicaRef: api.MirrorObjectRef{Name: "demo", UID: "pinned"}, Faults: []api.ChaosFault{{Kind: "NetworkIsolation", Namespace: "integration"}}}}
	}
	t.Run("schema applies bounded defaults", func(t *testing.T) {
		x := newExperiment()
		if err := k.Create(ctx, x); err != nil {
			t.Fatal(err)
		}
		if x.Spec.DurationSeconds != 30 {
			t.Fatal("bounded default duration missing")
		}
		g := grant.DeepCopy()
		g.ObjectMeta = metav1.ObjectMeta{Name: "default-feature-policy"}
		g.Spec.Chaos = &api.ChaosGrant{Namespaces: []string{"integration"}, Kinds: []string{"ScaleZero"}}
		g.Spec.Databases[0].Port = 0
		g.Spec.Databases[0].SSLMode = ""
		g.Spec.Databases[0].TimeoutSeconds = 0
		if err := k.Create(ctx, g); err != nil {
			t.Fatal(err)
		}
		if g.Spec.Chaos.MaxDurationSeconds != 60 || g.Spec.Chaos.MaxCPUMilli != 500 || g.Spec.Chaos.MaxMemoryMiB != 128 || g.Spec.Databases[0].Port != 5432 || g.Spec.Databases[0].SSLMode != "verify-full" || g.Spec.Databases[0].TimeoutSeconds != 300 {
			t.Fatal("feature schema defaults missing")
		}
	})
	t.Run("API rejects excessive or malformed fault requests", func(t *testing.T) {
		for name, mutate := range map[string]func(*api.ReplicaExperiment){
			"no faults": func(x *api.ReplicaExperiment) { x.Spec.Faults = []api.ChaosFault{} },
			"nine faults": func(x *api.ReplicaExperiment) {
				for len(x.Spec.Faults) < 9 {
					x.Spec.Faults = append(x.Spec.Faults, x.Spec.Faults[0])
				}
			},
			"duration":          func(x *api.ReplicaExperiment) { x.Spec.DurationSeconds = 901 },
			"negative duration": func(x *api.ReplicaExperiment) { x.Spec.DurationSeconds = -1 },
			"host fault":        func(x *api.ReplicaExperiment) { x.Spec.Faults[0].Kind = "NodeReboot" },
			"CPU":               func(x *api.ReplicaExperiment) { x.Spec.Faults[0].CPUMilli = 2001 },
			"memory":            func(x *api.ReplicaExperiment) { x.Spec.Faults[0].MemoryMiB = 1025 },
			"command":           func(x *api.ReplicaExperiment) { x.Spec.Faults[0].Command = make([]string, 33) },
			"replica UID":       func(x *api.ReplicaExperiment) { x.Spec.ReplicaRef.UID = "" },
			"workload UID": func(x *api.ReplicaExperiment) {
				x.Spec.Faults[0] = api.ChaosFault{Kind: "ScaleZero", Namespace: "integration", Target: &api.ChaosTarget{Kind: "Deployment", Name: "web"}}
			},
		} {
			t.Run(name, func(t *testing.T) {
				bad := newExperiment()
				mutate(bad)
				if err := k.Create(ctx, bad); !apierrors.IsInvalid(err) {
					t.Fatalf("malformed fault accepted: %v", err)
				}
			})
		}
	})
	t.Run("API rejects oversized or unsupported administrator policies", func(t *testing.T) {
		for name, mutate := range map[string]func(*api.ReplicaGrant){
			"plaintext source":  func(g *api.ReplicaGrant) { g.Spec.Databases[0].SSLMode = "disable" },
			"empty masking":     func(g *api.ReplicaGrant) { g.Spec.Databases[0].Masking = []api.DatabaseMask{} },
			"unknown masking":   func(g *api.ReplicaGrant) { g.Spec.Databases[0].Masking[0].Strategy = "Encrypt" },
			"archive too large": func(g *api.ReplicaGrant) { g.Spec.Databases[0].MaxBytes = 4294967297 },
			"archive too small": func(g *api.ReplicaGrant) { g.Spec.Databases[0].MaxBytes = 1024 },
			"database timeout":  func(g *api.ReplicaGrant) { g.Spec.Databases[0].TimeoutSeconds = 1801 },
			"database port":     func(g *api.ReplicaGrant) { g.Spec.Databases[0].Port = 65536 },
			"chaos duration": func(g *api.ReplicaGrant) {
				g.Spec.Chaos = &api.ChaosGrant{Namespaces: []string{"integration"}, Kinds: []string{"ScaleZero"}, MaxDurationSeconds: 901}
			},
			"chaos memory": func(g *api.ReplicaGrant) {
				g.Spec.Chaos = &api.ChaosGrant{Namespaces: []string{"integration"}, Kinds: []string{"ScaleZero"}, MaxMemoryMiB: 1025}
			},
		} {
			t.Run(name, func(t *testing.T) {
				bad := grant.DeepCopy()
				bad.ObjectMeta = metav1.ObjectMeta{GenerateName: "invalid-feature-policy-"}
				mutate(bad)
				if err := k.Create(ctx, bad); !apierrors.IsInvalid(err) {
					t.Fatalf("invalid delegation accepted: %v", err)
				}
			})
		}
	})
	t.Run("disabled chaos preserves finalizers until controller is reenabled", func(t *testing.T) {
		applyFeatureRoles(t, k, false)
		callerCfg := rest.CopyConfig(cfg)
		callerCfg.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:replicove-system:feature-contract"}
		caller, err := client.New(callerCfg, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		x := newExperiment()
		x.GenerateName = ""
		x.Name = "disabled-cleanup"
		x.Finalizers = []string{chaos.Finalizer}
		if err := k.Create(ctx, x); err != nil {
			t.Fatal(err)
		}
		adminStore := &state.Store{Client: k, Namespace: "replicove-system"}
		st := &state.State{OwnerUID: string(x.UID), OwnerName: x.Name, OwnerNamespace: x.Namespace, TargetClusterUID: "guest-uid", ReplicaUID: x.Spec.ReplicaRef.UID, Experiment: &state.Experiment{ReplicaUID: x.Spec.ReplicaRef.UID, Phase: "Active", StartedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}}
		if err := adminStore.Save(ctx, st); err != nil {
			t.Fatal(err)
		}
		r := &chaos.Reconciler{Client: caller, Store: &state.Store{Client: caller, Namespace: "replicove-system"}, Namespace: "replica-lab", Connect: func(context.Context, client.Client, *state.State, string) (*target.Connection, error) {
			return &target.Connection{UID: "guest-uid"}, nil
		}}
		parent := &state.State{OwnerNamespace: "replica-lab", OwnerUID: x.Spec.ReplicaRef.UID}
		// Only CleanupReplica runs with the module disabled. Real RBAC must
		// permit deletion requests, while leaving existing finalizers in place.
		for i := 0; ; i++ {
			done, err := r.CleanupReplica(ctx, parent)
			if err == nil {
				if done {
					t.Fatal("disabled module falsely completed cleanup")
				}
				break
			}
			if i == 30 {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := k.Get(ctx, client.ObjectKeyFromObject(x), x); err != nil {
			t.Fatal(err)
		}
		if x.DeletionTimestamp.IsZero() || len(x.Finalizers) != 1 {
			t.Fatal("disabled cleanup lost the fault finalizer")
		}
		if _, err := adminStore.Load(ctx, string(x.UID)); err != nil {
			t.Fatal(err)
		}
		applyFeatureRoles(t, k, true)
		for i := 0; ; i++ {
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(x)})
			if err == nil {
				break
			}
			if i == 30 {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := k.Get(ctx, client.ObjectKeyFromObject(x), &api.ReplicaExperiment{}); !apierrors.IsNotFound(err) {
			t.Fatalf("reenabled controller could not complete finalizer: %v", err)
		}
		if remaining, err := adminStore.Load(ctx, string(x.UID)); err != nil || remaining != nil {
			t.Fatal("completed experiment state remains")
		}
	})
	t.Run("capacity reservations use real API optimistic concurrency", func(t *testing.T) {
		store := &state.Store{Client: k, Namespace: "replicove-system"}
		for _, tc := range []struct {
			name      string
			providers []string
			perGrant  int32
		}{
			{name: "helm-across-grants", providers: []string{"helm", "helm", "helm"}},
			{name: "existing-grant-limit", providers: []string{"existing", "existing", "existing"}, perGrant: 1},
			{name: "mixed-provider-grant-limit", providers: []string{"existing", "helm", "existing"}, perGrant: 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				requests := make([]*api.ClusterReplica, len(tc.providers))
				grants := make([]*api.ReplicaGrant, len(tc.providers))
				for i := range requests {
					if i > 0 && tc.perGrant > 0 {
						grants[i] = grants[0]
					} else {
						g := grant.DeepCopy()
						g.ObjectMeta = metav1.ObjectMeta{Name: fmt.Sprintf("capacity-%s-%d", tc.name, i)}
						g.Spec.Databases = nil
						g.Spec.Chaos = nil
						g.Spec.MaxConcurrentReplicas = tc.perGrant
						if err := k.Create(ctx, g); err != nil {
							t.Fatal(err)
						}
						grants[i] = g
					}
					requests[i] = &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("capacity-%s-%d", tc.name, i), Namespace: "replica-lab"}, Spec: api.ClusterReplicaSpec{Profile: catalog.PersistentProfile, TTL: "1h", CleanupPolicy: "DeleteOwned", GrantRef: grants[i].Name}}
					if err := k.Create(ctx, requests[i]); err != nil {
						t.Fatal(err)
					}
				}
				type result struct {
					index int
					ok    bool
					err   error
				}
				start := make(chan struct{})
				results := make(chan result, len(requests))
				for i := range requests {
					go func(i int) {
						<-start
						ok, err := capacity.Acquire(ctx, store, requests[i], grants[i], tc.providers[i])
						results <- result{index: i, ok: ok, err: err}
					}(i)
				}
				close(start)
				winner, count := -1, 0
				for range requests {
					got := <-results
					if got.err != nil {
						t.Errorf("request %d: %v", got.index, got.err)
					}
					if got.ok {
						winner = got.index
						count++
					}
				}
				if count != 1 {
					t.Fatalf("admitted %d concurrent requests, want exactly one", count)
				}
				loser := (winner + 1) % len(requests)
				if ok, err := capacity.Acquire(ctx, store, requests[winner], grants[winner], tc.providers[winner]); !ok || err != nil {
					t.Fatalf("repeated acquire for same UID: %v", err)
				}
				if err := capacity.Release(ctx, store, "replica-lab", string(requests[loser].UID)); err != nil {
					t.Fatal(err)
				}
				if ok, err := capacity.Acquire(ctx, store, requests[loser], grants[loser], tc.providers[loser]); ok || err != nil {
					t.Fatalf("foreign UID release freed another request's slot: accepted=%v error=%v", ok, err)
				}
				if err := capacity.Release(ctx, store, "replica-lab", string(requests[winner].UID)); err != nil {
					t.Fatal(err)
				}
				if ok, err := capacity.Acquire(ctx, store, requests[loser], grants[loser], tc.providers[loser]); !ok || err != nil {
					t.Fatalf("verified release did not free capacity: %v", err)
				}
				if err := capacity.Release(ctx, store, "replica-lab", string(requests[loser].UID)); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

// Use the actual chart roles, not a hand-written substitute that could mask a
// missing permission during an off/on upgrade.
func applyFeatureRoles(t *testing.T, k client.Client, enabled bool) {
	t.Helper()
	ch, err := chartloader.Load(filepath.Join("..", "..", "charts", "replicove"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	install := helmprovider.NewInstall(cfg)
	install.DryRunStrategy = action.DryRunClient
	install.ReleaseName = "feature-contract"
	install.Namespace = "replicove-system"
	install.KubeVersion, _ = common.ParseKubeVersion("v1.37.0")
	rel, err := install.RunWithContext(context.Background(), ch, map[string]any{"chaos": map[string]any{"enabled": enabled}})
	if err != nil {
		t.Fatal(err)
	}
	accessor, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatal(err)
	}
	decoder := kYaml.NewYAMLOrJSONDecoder(strings.NewReader(accessor.Manifest()), 4096)
	for {
		o := &unstructured.Unstructured{}
		if err := decoder.Decode(o); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if o.GetKind() != "Role" && o.GetKind() != "RoleBinding" && o.GetKind() != "ServiceAccount" {
			continue
		}
		live := o.DeepCopy()
		err := k.Get(context.Background(), client.ObjectKeyFromObject(o), live)
		if apierrors.IsNotFound(err) {
			if err := k.Create(context.Background(), o); err != nil {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		} else {
			o.SetResourceVersion(live.GetResourceVersion())
			if err := k.Update(context.Background(), o); err != nil {
				t.Fatal(err)
			}
		}
	}
}
