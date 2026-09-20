package helm

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chartloader "helm.sh/helm/v4/pkg/chart/loader"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestMirrorDependencyContract(t *testing.T) {
	root := filepath.Join("..", "..", "..", "charts", "replicove")
	for file, expected := range map[string]string{
		"volumesnapshots.yaml":        "b032116e987fb1d7d93cec7d942833adeb9d95c9bdc7e00c10b280c2fe4a6c33",
		"volumesnapshotcontents.yaml": "895a3c1e73b60f06a0deb566dd123d01bdf1b2efc5d5ff5231ff8bbcf42dafc7",
		"volumesnapshotclasses.yaml":  "75e6565aac2c0f2949ed13ea884bbaa388cb7be576b558b709cf1168e011828d",
	} {
		data, err := os.ReadFile(filepath.Join(root, "files", "snapshot-crds", file))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != expected {
			t.Fatalf("unreviewed upstream snapshot schema change: %s", file)
		}
	}
	for _, mode := range []string{"disabled", "auto", "existing", "managed"} {
		t.Run(mode, func(t *testing.T) {
			ch, err := chartloader.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			selected := mode
			if mode == "disabled" {
				selected = "auto"
			}
			values := map[string]any{"mirrors": map[string]any{"enabled": mode != "disabled", "networkPolicyEnforced": true, "snapshotController": map[string]any{"mode": selected}, "sources": []any{"source-dev"}}}
			objs := renderRBACContract(t, ch, "v1.36.4", "replicove-system", values)
			controller, crds, sourceRoles := 0, 0, 0
			for _, obj := range objs {
				if strings.Contains(obj.GetName(), "snapshot-controller") || strings.HasSuffix(obj.GetName(), "snapshot.storage.k8s.io") || obj.GetNamespace() == "source-dev" {
					if obj.GetLabels()["replicove.nimeshbuilds.dev/infrastructure"] != "true" {
						t.Fatalf("mirror infrastructure could be captured as application configuration: %s/%s", obj.GetKind(), obj.GetName())
					}
				}
				if obj.GetKind() == "CustomResourceDefinition" && strings.HasSuffix(obj.GetName(), "snapshot.storage.k8s.io") {
					crds++
					if obj.GetAnnotations()["helm.sh/resource-policy"] != "keep" {
						t.Fatal("shared snapshot API removed on uninstall")
					}
				}
				if obj.GetKind() == "Deployment" && obj.GetName() == "contract-snapshot-controller" {
					controller++
					containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
					if containers[0].(map[string]any)["image"] != "registry.k8s.io/sig-storage/snapshot-controller:v8.6.0" {
						t.Fatal("unqualified controller image")
					}
				}
				if obj.GetKind() == "Role" && obj.GetNamespace() == "source-dev" {
					sourceRoles++
					rules := roleRules(t, obj)
					if !permits(rules, "snapshot.storage.k8s.io", "volumesnapshots", "create") || !permits(rules, "", "persistentvolumeclaims", "get") {
						t.Fatal("missing bounded mirror source permissions")
					}
					for _, resource := range []string{"persistentvolumeclaims", "secrets", "deployments"} {
						for _, verb := range []string{"create", "update", "patch", "delete"} {
							if permits(rules, "", resource, verb) || permits(rules, "apps", resource, verb) {
								t.Fatalf("source mutation permitted: %s %s", verb, resource)
							}
						}
					}
				}
			}
			managed := mode == "auto" || mode == "managed"
			if managed && (controller != 1 || crds != 3) || !managed && (controller != 0 || crds != 0) {
				t.Fatalf("unexpected module resources: controller=%d crds=%d", controller, crds)
			}
			if mode == "disabled" && sourceRoles != 0 || mode != "disabled" && sourceRoles != 1 {
				t.Fatalf("unexpected source delegation count %d", sourceRoles)
			}
		})
	}
}
