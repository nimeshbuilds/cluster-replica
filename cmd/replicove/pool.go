package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"

	operatorchart "github.com/nimeshbuilds/cluster-replica/charts/replicove"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"github.com/nimeshbuilds/cluster-replica/internal/testrun"
	"github.com/spf13/cobra"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/release"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// Pool files are administrator-owned local configuration, never a privileged
// in-cluster controller that can allocate arbitrary namespaces or grants.
type poolFile struct {
	APIVersion        string       `json:"apiVersion"`
	Kind              string       `json:"kind"`
	KubernetesVersion string       `json:"kubernetesVersion,omitempty"`
	Members           []poolMember `json:"members"`
}
type poolMember struct {
	Namespace       string         `json:"namespace"`
	SystemNamespace string         `json:"systemNamespace"`
	ReleaseName     string         `json:"releaseName,omitempty"`
	Values          map[string]any `json:"values"`
}

func (c *cli) poolCommand() *cobra.Command {
	root := &cobra.Command{Use: "pool", Short: "Render administrator-managed destinations for concurrent test runs"}
	var file string
	cmd := &cobra.Command{Use: "render -f POOL.yaml", Short: "Render native installation YAML offline; no cluster access or grants are created", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(file)
		if err != nil {
			return errors.New("cannot read pool file")
		}
		pool, err := parsePool(data)
		if err != nil {
			return err
		}
		output, err := renderPool(cmd.Context(), pool)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(output)
		return err
	}}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Local administrator ReplicaPool file")
	_ = cmd.MarkFlagRequired("file")
	root.AddCommand(cmd)
	return root
}

func parsePool(data []byte) (poolFile, error) {
	var out poolFile
	if len(data) > 1024*1024 {
		return out, errors.New("pool file exceeds 1 MiB")
	}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	doc, err := reader.Read()
	if err != nil {
		return out, errors.New("invalid pool YAML")
	}
	if _, err := reader.Read(); err != io.EOF {
		return out, errors.New("pool file requires exactly one YAML document")
	}
	if err := yaml.UnmarshalStrict(doc, &out); err != nil {
		return out, errors.New("invalid pool YAML: check field names and value types")
	}
	return out, validatePool(&out)
}

func validatePool(pool *poolFile) error {
	if pool.APIVersion != testrun.RecipeVersion || pool.Kind != "ReplicaPool" {
		return errors.New("use a replicove.nimeshbuilds.dev/v1alpha1 ReplicaPool local file")
	}
	if len(pool.Members) == 0 || len(pool.Members) > 32 {
		return errors.New("pool requires 1..32 members")
	}
	if pool.KubernetesVersion == "" {
		pool.KubernetesVersion = "v1.36.0"
	}
	if _, err := common.ParseKubeVersion(pool.KubernetesVersion); err != nil {
		return errors.New("invalid pool Kubernetes version")
	}
	names := map[string]bool{}
	systems := map[string]bool{}
	managed := 0
	for i := range pool.Members {
		m := &pool.Members[i]
		for _, name := range []string{m.Namespace, m.SystemNamespace} {
			if len(validation.IsDNS1123Label(name)) != 0 || names[name] || strings.HasPrefix(name, "kube-") || name == "default" {
				return errors.New("pool destinations and protected system namespaces must be distinct non-system DNS labels")
			}
			names[name] = true
		}
		systems[m.SystemNamespace] = true
		if m.ReleaseName == "" {
			m.ReleaseName = "replicove"
		}
		if len(m.ReleaseName) > 53 || len(validation.IsDNS1123Label(m.ReleaseName)) != 0 {
			return errors.New("pool releaseName must be a DNS label of at most 53 characters")
		}
		if m.Values == nil {
			return errors.New("each pool member requires explicit chart values and resource quotas")
		}
		capacity, ok := m.Values["capacity"].(map[string]any)
		if !ok || capacity["enabled"] != true {
			return errors.New("each pool member requires capacity.enabled true")
		}
		hard, ok := capacity["hard"].(map[string]any)
		if !ok {
			return errors.New("each pool member requires explicit capacity.hard quotas")
		}
		for _, key := range []string{"requests.cpu", "requests.memory", "requests.storage", "pods", "persistentvolumeclaims"} {
			value, ok := hard[key]
			if !ok {
				return errors.New("pool quotas must bound requested CPU, memory, storage, pods and PVCs including runtime overhead")
			}
			q, err := resource.ParseQuantity(fmt.Sprint(value))
			if err != nil || q.Sign() <= 0 {
				return errors.New("pool quota quantities must be positive")
			}
		}
		if mirrors, ok := m.Values["mirrors"].(map[string]any); ok && mirrors["enabled"] == true {
			controller, ok := mirrors["snapshotController"].(map[string]any)
			if !ok {
				return errors.New("offline pool mirrors require explicit snapshotController.mode existing or managed")
			}
			switch controller["mode"] {
			case "existing":
			case "managed":
				managed++
			default:
				return errors.New("offline pool mirrors require explicit snapshotController.mode existing or managed")
			}
		}
	}
	if managed > 1 {
		return errors.New("only one pool member may manage the shared snapshot controller")
	}
	for _, m := range pool.Members {
		if sources, ok := m.Values["sources"].([]any); ok {
			for _, source := range sources {
				if item, ok := source.(map[string]any); ok {
					ns, _ := item["namespace"].(string)
					if systems[ns] {
						return errors.New("pool source reads cannot include protected pool system namespaces")
					}
				}
			}
		}
	}
	return nil
}

func renderPool(ctx context.Context, pool poolFile) ([]byte, error) {
	if err := validatePool(&pool); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.WriteString("# Generated offline by replicove pool render. Review as an administrator before applying.\n")
	// CRDs are shared and emitted exactly once. No encryption key is rendered.
	paths, err := fs.Glob(operatorchart.Files, "crds/*.yaml")
	if err != nil {
		return nil, errors.New("cannot read embedded APIs")
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, err := operatorchart.Files.ReadFile(path)
		if err != nil {
			return nil, errors.New("cannot read embedded API")
		}
		output.WriteString("---\n")
		output.Write(data)
		output.WriteByte('\n')
	}
	for _, m := range pool.Members {
		files := []*archive.BufferedFile{}
		if err := fs.WalkDir(operatorchart.Files, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := operatorchart.Files.ReadFile(path)
			if err != nil {
				return err
			}
			files = append(files, &archive.BufferedFile{Name: path, Data: data})
			return nil
		}); err != nil {
			return nil, errors.New("cannot load embedded installation chart")
		}
		chart, err := loader.LoadFiles(files)
		if err != nil {
			return nil, errors.New("embedded installation chart is invalid")
		}
		// Copy the map before enforcing native ownership and isolated state.
		raw, err := yaml.Marshal(m.Values)
		if err != nil {
			return nil, errors.New("invalid pool chart values")
		}
		values := map[string]any{}
		if err := yaml.Unmarshal(raw, &values); err != nil {
			return nil, errors.New("invalid pool chart values")
		}
		values["destinationNamespace"] = m.Namespace
		values["createDestinationNamespace"] = false
		values["stateKey"] = map[string]any{"bootstrap": true}
		cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
		install := helmprovider.NewInstall(cfg)
		install.DryRunStrategy = action.DryRunClient
		install.ReleaseName = m.ReleaseName
		install.Namespace = m.SystemNamespace
		install.KubeVersion, _ = common.ParseKubeVersion(pool.KubernetesVersion)
		rel, err := install.RunWithContext(ctx, chart, values)
		if err != nil {
			return nil, errors.New("cannot render pool chart; check member values and module prerequisites")
		}
		accessor, err := release.NewAccessor(rel)
		if err != nil {
			return nil, errors.New("cannot read rendered installation")
		}
		for _, name := range []string{m.SystemNamespace, m.Namespace} {
			fmt.Fprintf(&output, "---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n  labels:\n    replicove.nimeshbuilds.dev/infrastructure: \"true\"\n", name)
		}
		output.WriteString("---\n")
		output.WriteString(accessor.Manifest())
		output.WriteByte('\n')
	}
	return output.Bytes(), nil
}
