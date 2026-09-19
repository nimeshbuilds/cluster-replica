package helm

import (
	"context"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/release"
)

// This is a real upstream chart schema/render check, not a live-cluster test.
func TestPinnedChartContract(t *testing.T) {
	path := os.Getenv("VCLUSTER_CHART")
	if path == "" {
		t.Skip("set VCLUSTER_CHART to run the upstream contract check (make test-contract)")
	}
	ch, err := LoadChart(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v1.35.0", "v1.36.0", "v1.37.0"} {
		t.Run(version, func(t *testing.T) {
			cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
			install := action.NewInstall(cfg)
			install.DryRunStrategy = action.DryRunClient
			install.ReleaseName = "contract-test"
			install.Namespace = "lab"
			install.WaitStrategy = kube.HookOnlyStrategy
			install.KubeVersion, err = common.ParseKubeVersion(version)
			if err != nil {
				t.Fatal(err)
			}
			rel, err := install.RunWithContext(context.Background(), ch, catalog.Values("contract-uid"))
			if err != nil {
				t.Fatal(err)
			}
			a, err := release.NewAccessor(rel)
			if err != nil {
				t.Fatal(err)
			}
			objects, err := manifestObjects(a.Manifest(), "lab")
			if err != nil {
				t.Fatalf("profile exceeds cleanup/RBAC scope: %v", err)
			}
			if len(objects) == 0 {
				t.Fatal("chart rendered no resources")
			}
			if len(a.Hooks()) != 0 {
				t.Fatal("profile requires hooks, which this adapter disables")
			}
			sets := 0
			for _, obj := range objects {
				if obj.GetAnnotations()["helm.sh/resource-policy"] == "keep" {
					t.Fatal("profile retains chart objects")
				}
				if obj.GetKind() == "Deployment" {
					sets++
					if obj.GetLabels()[catalog.OwnerLabel] != "contract-uid" {
						t.Fatal("ownership label not rendered")
					}
					if _, ok := obj.Object["spec"].(map[string]any)["volumeClaimTemplates"]; ok {
						t.Fatal("lab control plane unexpectedly requests persistent storage")
					}
				}
			}
			if sets != 1 {
				t.Fatalf("want one control-plane Deployment, got %d", sets)
			}
			if !strings.Contains(a.Manifest(), "ghcr.io/loft-sh/vcluster-oss:0.37.1") {
				t.Fatal("OSS runtime image not pinned")
			}
			if !strings.Contains(a.Manifest(), "ghcr.io/loft-sh/kubernetes:v1.36.0") {
				t.Fatal("guest version drifted")
			}
		})
	}
}

func TestPinnedChartContractPersistent(t *testing.T) {
	path := os.Getenv("VCLUSTER_CHART")
	if path == "" {
		t.Skip("run make test-contract")
	}
	ch, err := LoadChart(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v1.35.0", "v1.36.0", "v1.37.0"} {
		t.Run(version, func(t *testing.T) {
			cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
			install := action.NewInstall(cfg)
			install.DryRunStrategy = action.DryRunClient
			install.ReleaseName = "persistent-test"
			install.Namespace = "lab"
			install.KubeVersion, _ = common.ParseKubeVersion(version)
			rel, err := install.RunWithContext(context.Background(), ch, catalog.ValuesProfile("owner", catalog.PersistentProfile))
			if err != nil {
				t.Fatal(err)
			}
			a, _ := release.NewAccessor(rel)
			objects, err := manifestObjects(a.Manifest(), "lab")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, obj := range objects {
				if obj.GetKind() != "StatefulSet" {
					continue
				}
				found = true
				policy, _, _ := unstructured.NestedString(obj.Object, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted")
				if policy != "Delete" {
					t.Fatal("control-plane PVC is retained")
				}
				claims, _, _ := unstructured.NestedSlice(obj.Object, "spec", "volumeClaimTemplates")
				if len(claims) != 1 {
					t.Fatal("durable profile must have one control-plane PVC")
				}
			}
			if !found {
				t.Fatal("persistent profile did not render a StatefulSet")
			}
		})
	}
}
