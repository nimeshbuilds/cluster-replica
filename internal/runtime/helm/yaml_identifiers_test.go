package helm

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chartloader "helm.sh/helm/v4/pkg/chart/loader"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestSnapshotControllerNameFitsKubernetesLabels(t *testing.T) {
	chart, err := chartloader.Load(filepath.Join("..", "..", "..", "charts", "replicove"))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{43, 44} {
		cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
		install := NewInstall(cfg)
		install.DryRunStrategy = action.DryRunClient
		install.ReleaseName = strings.Repeat("a", size)
		install.Namespace = "operator-system"
		install.KubeVersion, _ = common.ParseKubeVersion("v1.36.4")
		_, err := install.RunWithContext(context.Background(), chart, map[string]any{"stateKey": map[string]any{"bootstrap": true}, "mirrors": map[string]any{"enabled": true, "networkPolicyEnforced": true, "snapshotController": map[string]any{"mode": "managed"}}})
		if size == 43 && err != nil {
			t.Fatalf("valid boundary release rejected: %v", err)
		}
		if size == 44 && (err == nil || !strings.Contains(err.Error(), "at most 43 characters")) {
			t.Fatalf("expected actionable label length error, got %v", err)
		}
	}
}

func TestOperatorNamespacePreservesYAMLScalarNames(t *testing.T) {
	chart, err := chartloader.Load(filepath.Join("..", "..", "..", "charts", "replicove"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"123", "true", "false", "null", "yes", "no", "on", "off"} {
		t.Run(name, func(t *testing.T) {
			found := false
			for _, obj := range renderRBACContract(t, chart, "v1.36.0", "operator-system", map[string]any{"destinationNamespace": name, "stateKey": map[string]any{"bootstrap": true}}) {
				if obj.GetKind() != "Namespace" {
					continue
				}
				var ns corev1.Namespace
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &ns); err != nil {
					t.Fatalf("namespace must retain a string name: %v", err)
				}
				if ns.Name != name {
					t.Fatalf("namespace changed from %q to %q", name, ns.Name)
				}
				found = true
			}
			if !found {
				t.Fatal("destination namespace was not rendered")
			}
		})
	}
}
