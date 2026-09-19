package helm

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	chartloader "helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/release"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// The installer must already hold every permission in the upstream Role.
// Otherwise Kubernetes rejects installation unless bind/escalate is granted.
// Keep this check tied to the actual pinned chart, not a copy of its rules.
func TestPinnedChartContractRBAC(t *testing.T) {
	path := os.Getenv("VCLUSTER_CHART")
	if path == "" {
		t.Skip("run make test-contract")
	}
	upstream, err := LoadChart(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := chartloader.Load(filepath.Join("..", "..", "..", "charts", "replicove"))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v1.35.0", "v1.36.0", "v1.37.0"} {
		t.Run(version, func(t *testing.T) {
			var granted []rbacv1.PolicyRule
			for _, obj := range renderRBACContract(t, operator, version, "replicove-system", nil) {
				if obj.GetKind() == "Role" && obj.GetName() == "contract-runtime" {
					granted = roleRules(t, obj)
				}
			}
			if len(granted) == 0 {
				t.Fatal("destination runtime Role is missing")
			}
			for _, rule := range granted {
				for _, parts := range [][]string{rule.APIGroups, rule.Resources, rule.Verbs} {
					if slices.Contains(parts, "*") {
						t.Fatal("runtime Role grants wildcard permissions")
					}
				}
				for _, verb := range []string{"bind", "escalate", "impersonate"} {
					if slices.Contains(rule.Verbs, verb) {
						t.Fatalf("runtime Role bypasses authorization through %s", verb)
					}
				}
			}
			for _, profile := range []string{catalog.Profile, catalog.PersistentProfile} {
				for _, obj := range renderRBACContract(t, upstream, version, "replica-lab", catalog.ValuesProfile("owner", profile)) {
					if obj.GetKind() != "Role" {
						continue
					}
					for _, rule := range roleRules(t, obj) {
						for _, group := range rule.APIGroups {
							for _, resource := range rule.Resources {
								for _, verb := range rule.Verbs {
									if !permits(granted, group, resource, verb) {
										t.Errorf("%s needs %s %s/%s to install upstream Role without escalation", profile, verb, group, resource)
									}
								}
							}
						}
					}
				}
			}
		})
	}
}

func renderRBACContract(t *testing.T, ch chart.Charter, version, namespace string, values map[string]any) []*unstructured.Unstructured {
	t.Helper()
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	install := NewInstall(cfg)
	install.DryRunStrategy = action.DryRunClient
	install.ReleaseName, install.Namespace = "contract", namespace
	var err error
	install.KubeVersion, err = common.ParseKubeVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := install.RunWithContext(context.Background(), ch, values)
	if err != nil {
		t.Fatal(err)
	}
	a, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(a.Manifest()), 4096)
	var objects []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err == io.EOF {
			return objects
		} else if err != nil {
			t.Fatal(err)
		}
		if len(obj.Object) != 0 {
			objects = append(objects, obj)
		}
	}
}

func roleRules(t *testing.T, obj *unstructured.Unstructured) []rbacv1.PolicyRule {
	t.Helper()
	role := &rbacv1.Role{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, role); err != nil {
		t.Fatal(err)
	}
	return role.Rules
}

func permits(rules []rbacv1.PolicyRule, group, resource, verb string) bool {
	for _, rule := range rules {
		if len(rule.ResourceNames) == 0 && slices.Contains(rule.APIGroups, group) && slices.Contains(rule.Resources, resource) && slices.Contains(rule.Verbs, verb) {
			return true
		}
	}
	return false
}
