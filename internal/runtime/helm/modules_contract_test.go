package helm

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chartloader "helm.sh/helm/v4/pkg/chart/loader"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestOptionalModuleInstallationAndAuthorizationContract(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		chaos, databases, mirrors bool
	}{{name: "disabled"}, {name: "chaos", chaos: true}, {name: "databases", databases: true}, {name: "mirrors", mirrors: true}, {name: "combined", chaos: true, databases: true, mirrors: true}} {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := chartloader.Load(filepath.Join("..", "..", "..", "charts", "replicove"))
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]any{
				"chaos":     map[string]any{"enabled": tc.chaos, "networkPolicyEnforced": true},
				"databases": map[string]any{"enabled": tc.databases},
				"mirrors":   map[string]any{"enabled": tc.mirrors, "networkPolicyEnforced": true, "snapshotController": map[string]any{"mode": "existing"}},
				"sources":   []any{map[string]any{"namespace": "source", "rules": []any{map[string]any{"apiGroups": []any{"", "apps"}, "resources": []any{"configmaps", "secrets", "deployments"}, "verbs": []any{"get", "list"}}}}},
			}
			objects := renderRBACContract(t, ch, "v1.36.4", "replicove-system", values)
			deployment, runtimeRole, sourceRoles := 0, 0, 0
			for _, o := range objects {
				if o.GetKind() == "Deployment" && o.GetName() == "contract" {
					deployment++
					containers, _, _ := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "containers")
					args := containers[0].(map[string]any)["args"].([]any)
					for flag, wanted := range map[string]bool{"--chaos=true": tc.chaos, "--chaos-network-policy-enforced=true": tc.chaos, "--databases=true": tc.databases, "--mirrors=true": tc.mirrors} {
						found := false
						for _, a := range args {
							if a == flag {
								found = true
							}
						}
						if found != wanted {
							t.Fatalf("module flag %s presence=%v, want %v", flag, found, wanted)
						}
					}
				}
				if o.GetKind() == "Role" && o.GetNamespace() == "source" {
					sourceRoles++
					for _, rule := range roleRules(t, o) {
						if !reflect.DeepEqual(rule.Verbs, []string{"get", "list"}) {
							t.Fatal("optional module expanded source mutation privileges")
						}
						for _, r := range rule.Resources {
							if r != "configmaps" && r != "secrets" && r != "deployments" {
								t.Fatalf("unrequested source resource %s", r)
							}
						}
					}
				}
				if o.GetKind() == "Role" && o.GetName() == "contract-runtime" {
					runtimeRole++
					rules := roleRules(t, o)
					for _, verb := range []string{"get", "list", "watch", "delete"} {
						if !permits(rules, "replica.nimeshbuilds.dev", "replicaexperiments", verb) {
							t.Fatalf("cleanup cannot %s experiment when module disabled", verb)
						}
					}
					if permits(rules, "replica.nimeshbuilds.dev", "replicaexperiments/status", "patch") != tc.chaos {
						t.Fatal("experiment controller status permissions must follow opt-in")
					}
					for _, verb := range []string{"get", "create", "delete"} {
						if permits(rules, "networking.k8s.io", "networkpolicies", verb) != (tc.chaos || tc.databases || tc.mirrors) {
							t.Fatalf("host isolation %s scope differs from module opt-in", verb)
						}
					}
					if (tc.chaos || tc.databases) && !permits(rules, "networking.k8s.io", "networkpolicies", "list") {
						t.Fatal("cannot check additive host allow policies")
					}
				}
				if o.GetKind() == "ClusterRole" {
					for _, rule := range roleRules(t, o) {
						// The existing CSI mirror adapter explicitly owns imported
						// VolumeSnapshotContents; the new modules must not broaden it.
						if tc.mirrors && reflect.DeepEqual(rule.APIGroups, []string{"snapshot.storage.k8s.io"}) && reflect.DeepEqual(rule.Resources, []string{"volumesnapshotcontents"}) && reflect.DeepEqual(rule.Verbs, []string{"get", "create", "delete"}) {
							continue
						}
						for _, verb := range rule.Verbs {
							if slices.Contains([]string{"*", "create", "update", "patch", "delete", "deletecollection", "bind", "escalate", "impersonate"}, verb) {
								t.Fatalf("module expanded host-wide authority: %s", verb)
							}
						}
					}
				}
			}
			if deployment != 1 || runtimeRole != 1 || sourceRoles != 1 {
				t.Fatalf("incomplete install contract: %d %d %d", deployment, runtimeRole, sourceRoles)
			}
		})
	}
}

func TestCapacityChartRequiresExplicitLimitsAndPreservesScope(t *testing.T) {
	ch, err := chartloader.Load(filepath.Join("..", "..", "..", "charts", "replicove"))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range renderRBACContract(t, ch, "v1.36.4", "replicove-system", nil) {
		if o.GetKind() == "ResourceQuota" || o.GetKind() == "LimitRange" {
			t.Fatal("capacity changed without opt-in")
		}
	}
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	install := NewInstall(cfg)
	install.DryRunStrategy = action.DryRunClient
	install.ReleaseName = "contract"
	install.Namespace = "replicove-system"
	install.KubeVersion, _ = common.ParseKubeVersion("v1.36.4")
	if _, err := install.RunWithContext(context.Background(), ch, map[string]any{"capacity": map[string]any{"enabled": true}}); err == nil || !strings.Contains(err.Error(), "capacity.hard") {
		t.Fatal("capacity enabled without any explicit host budget")
	}
	hard := map[string]any{"requests.cpu": "2", "requests.memory": "2Gi", "requests.storage": "4Gi", "persistentvolumeclaims": "4", "pods": "12"}
	values := map[string]any{"destinationNamespace": "budget-lab", "capacity": map[string]any{"enabled": true, "hard": hard, "defaultContainer": map[string]any{"defaultRequest": map[string]any{"cpu": "50m", "memory": "32Mi"}, "default": map[string]any{"cpu": "500m", "memory": "128Mi"}}}}
	quota, limit := 0, 0
	for _, o := range renderRBACContract(t, ch, "v1.36.4", "replicove-system", values) {
		if o.GetKind() != "ResourceQuota" && o.GetKind() != "LimitRange" {
			continue
		}
		if o.GetNamespace() != "budget-lab" || o.GetAnnotations()["helm.sh/resource-policy"] != "keep" || o.GetLabels()["replicove.nimeshbuilds.dev/infrastructure"] != "true" {
			t.Fatal("capacity scope, upgrade retention or capture exclusion changed")
		}
		if o.GetKind() == "ResourceQuota" {
			quota++
			q := &corev1.ResourceQuota{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, q); err != nil {
				t.Fatal(err)
			}
			if len(q.Spec.Hard) != len(hard) {
				t.Fatal("quota omitted or invented budget constraints")
			}
			for key, value := range hard {
				got := q.Spec.Hard[corev1.ResourceName(key)]
				want := resource.MustParse(value.(string))
				if got.Cmp(want) != 0 {
					t.Fatalf("quota changed %s", key)
				}
			}
		} else {
			limit++
			l := &corev1.LimitRange{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, l); err != nil {
				t.Fatal(err)
			}
			if len(l.Spec.Limits) != 1 || l.Spec.Limits[0].Type != corev1.LimitTypeContainer {
				t.Fatal("container defaults not scoped to containers")
			}
			got := l.Spec.Limits[0].DefaultRequest[corev1.ResourceMemory]
			if got.Cmp(resource.MustParse("32Mi")) != 0 {
				t.Fatal("container default resource request changed")
			}
		}
	}
	if quota != 1 || limit != 1 {
		t.Fatal("capacity resource bundle incomplete")
	}
}
