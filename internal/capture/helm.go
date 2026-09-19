package capture

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// chart reconstructs the exact stored chart and revision's user values. This
// proves source provenance, not an upstream registry URL or signature. Helm is
// used only as a renderer: objects enter the same UID-tracked plan as raw YAML.
func (r *Reader) chart(ctx context.Context, ref api.NamespacedName, spec *api.ReplicationSpec, _ string) (*state.Package, []*unstructured.Unstructured, error) {
	cfg, err := helmprovider.Configuration(r.Config, ref.Namespace)
	if err != nil {
		return nil, nil, failed("HelmReadFailed", "Cannot initialize granted Helm storage.")
	}
	stored, err := cfg.Releases.Last(ref.Name)
	if err != nil {
		return nil, nil, failed("HelmReadFailed", "Cannot read granted Helm release %s/%s.", ref.Namespace, ref.Name)
	}
	source, err := release.NewAccessor(stored)
	if err != nil || source.Status() != "deployed" {
		return nil, nil, failed("HelmSourceNotReady", "Source release %s/%s must be deployed.", ref.Namespace, ref.Name)
	}
	ch, ok := source.Chart().(*chartv2.Chart)
	if !ok {
		return nil, nil, failed("ChartFormatUnsupported", "The source chart format is unsupported.")
	}
	valuesReader := action.NewGetValues(cfg)
	valuesReader.Version = source.Version()
	values, err := valuesReader.Run(ref.Name)
	if err != nil {
		return nil, nil, failed("HelmReadFailed", "Cannot read the captured Helm revision values.")
	}
	for _, override := range spec.HelmOverrides {
		if override.Namespace == ref.Namespace && override.Name == ref.Name {
			before, _ := json.Marshal(values)
			data, e := jsonpatch.MergePatch(before, override.Values.Raw)
			if e != nil {
				return nil, nil, failed("InvalidHelmOverride", "Helm values override is invalid.")
			}
			if e = json.Unmarshal(data, &values); e != nil {
				return nil, nil, failed("InvalidHelmOverride", "Helm values must be an object.")
			}
		}
	}
	tmp, err := os.MkdirTemp("", "replicove-chart-")
	if err != nil {
		return nil, nil, failed("CaptureStorageFailed", "Cannot allocate temporary chart storage.")
	}
	defer os.RemoveAll(tmp)
	file, err := chartutil.Save(ch, tmp)
	if err != nil {
		return nil, nil, failed("ChartArchiveFailed", "Cannot reconstruct the stored chart archive.")
	}
	archive, err := os.ReadFile(file)
	if err != nil || len(archive) > state.MaxPlainBytes {
		return nil, nil, failed("CaptureLimit", "The embedded chart exceeds capture limits.")
	}
	render := action.NewInstall(cfg)
	render.ReleaseName = ref.Name
	render.Namespace = ref.Namespace
	render.DryRunStrategy = action.DryRunClient
	render.IncludeCRDs = true
	render.KubeVersion, _ = common.ParseKubeVersion(catalog.GuestVersion)
	rendered, err := render.RunWithContext(ctx, ch, values)
	if err != nil {
		return nil, nil, failed("ChartRenderFailed", "Chart rendering failed; verify pinned guest version compatibility and explicit values.")
	}
	result, err := release.NewAccessor(rendered)
	if err != nil {
		return nil, nil, failed("ChartRenderFailed", "Cannot inspect the rendered release.")
	}
	for _, hook := range result.Hooks() {
		h, e := release.NewHookAccessor(hook)
		if e != nil {
			return nil, nil, failed("ChartHooksUnsupported", "Unsupported chart hook format.")
		}
		obj := &unstructured.Unstructured{}
		decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(h.Manifest()), 4096)
		if e := decoder.Decode(obj); e != nil {
			return nil, nil, failed("ChartHooksUnsupported", "Invalid chart hook manifest.")
		}
		for _, event := range strings.Split(obj.GetAnnotations()["helm.sh/hook"], ",") {
			if !strings.HasPrefix(strings.TrimSpace(event), "test") {
				return nil, nil, failed("ChartHooksUnsupported", "Release %s/%s requires lifecycle hooks; disable them through a documented chart value before capture.", ref.Namespace, ref.Name)
			}
		}
	}

	objects := []*unstructured.Unstructured{}
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(result.Manifest()), 4096)
	for {
		obj := &unstructured.Unstructured{}
		err := decoder.Decode(obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, failed("ChartRenderFailed", "Chart output is invalid.")
		}
		if len(obj.Object) > 0 {
			objects = append(objects, obj)
		}
	}
	return &state.Package{ID: planner.PackageID(ref.Namespace, ref.Name), SourceNamespace: ref.Namespace, SourceName: ref.Name, SourceRevision: source.Version(), Namespace: ref.Namespace, Name: ref.Name, ChartName: ch.Metadata.Name, ChartVersion: ch.Metadata.Version, ChartArchive: archive, Values: values}, objects, nil
}
