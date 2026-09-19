package workloads

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kYaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func TestWorkloadChartContracts(t *testing.T) {
	root := os.Getenv("WORKLOAD_CHARTS")
	if root == "" {
		t.Skip("set WORKLOAD_CHARTS after hack/fetch-workload-charts.py")
	}
	for name, file := range map[string]string{"cert-manager": "cert-manager-v1.20.4.tgz", "spark": "spark-operator-2.5.2.tgz", "trino": "trino-1.42.2.tgz"} {
		t.Run(name, func(t *testing.T) {
			ch, err := loader.Load(filepath.Join(root, file))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(name, "values.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]any{}
			if err := yaml.UnmarshalStrict(data, &values); err != nil {
				t.Fatal(err)
			}
			cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
			install := action.NewInstall(cfg)
			install.ReleaseName = "fixture"
			install.Namespace = "source-dev"
			install.DryRunStrategy = action.DryRunClient
			install.IncludeCRDs = true
			install.KubeVersion, _ = common.ParseKubeVersion("v1.36.0")
			result, err := install.RunWithContext(context.Background(), ch, values)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := release.NewAccessor(result)
			for _, hook := range a.Hooks() {
				h, err := release.NewHookAccessor(hook)
				if err != nil {
					t.Fatal(err)
				}
				obj := &unstructured.Unstructured{}
				decoder := kYaml.NewYAMLOrJSONDecoder(strings.NewReader(h.Manifest()), 4096)
				if err := decoder.Decode(obj); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(obj.GetAnnotations()["helm.sh/hook"], "test") {
					t.Fatalf("lifecycle hook %s requires adaptation", obj.GetName())
				}
			}
			decoder := kYaml.NewYAMLOrJSONDecoder(strings.NewReader(a.Manifest()), 4096)
			count := 0
			for {
				obj := &unstructured.Unstructured{}
				err := decoder.Decode(obj)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(obj.Object) == 0 {
					continue
				}
				count++
				if obj.GetNamespace() == "kube-system" {
					t.Fatal("chart escaped selected source namespace")
				}
				if obj.GetNamespace() == "" && obj.GetKind() != "CustomResourceDefinition" && obj.GetKind() != "ClusterRole" && obj.GetKind() != "ClusterRoleBinding" && obj.GetKind() != "MutatingWebhookConfiguration" && obj.GetKind() != "ValidatingWebhookConfiguration" {
					obj.SetNamespace("source-dev")
				}
				if _, err := planner.Transform(obj, &api.ReplicationSpec{Secrets: "Snapshot"}); err != nil {
					t.Fatal(err)
				}
				if obj.GetKind() == "Deployment" {
					t.Log("rendered deployment", obj.GetName())
				}
			}
			if count == 0 {
				t.Fatal("empty chart")
			}
		})
	}
}
