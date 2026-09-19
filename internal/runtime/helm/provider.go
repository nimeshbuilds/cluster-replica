// Package helm implements the standalone vCluster runtime. Platform and
// externally owned targets will have separate providers.
package helm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/storage/driver"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Provider struct {
	Config    *rest.Config
	Client    client.Client // uncached: status and ownership checks must be fresh
	Namespace string
	ChartPath string
}

func (p *Provider) configuration(namespace string) (*action.Configuration, error) {
	if namespace == "" || namespace != p.Namespace {
		return nil, runtimeprovider.ErrOwnership
	}
	return Configuration(p.Config, namespace)
}

// Configuration uses the caller's existing identity and suppresses upstream
// manifest logging. Callers must authorize the namespace before using it.
func Configuration(source *rest.Config, namespace string) (*action.Configuration, error) {
	config := rest.CopyConfig(source)
	config.Timeout = 30 * time.Second
	// Upstream error logging may contain rendered Secret manifests. The controller
	// emits classified status messages; admins inspect Helm directly for details.
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	err := cfg.Init(&restGetter{config: config, namespace: namespace}, namespace, "secret")
	return cfg, err
}

func Owned(rel release.Accessor, req runtimeprovider.Request) bool {
	return req.OwnerUID != "" && rel.Name() == req.Reference.ReleaseName && rel.Namespace() == req.Namespace && rel.Labels()[catalog.OwnerLabel] == req.OwnerUID
}

func (p *Provider) Ensure(ctx context.Context, req runtimeprovider.Request) (runtimeprovider.Observation, error) {
	if req.Reference != catalog.Resolve(req.OwnerUID) {
		return runtimeprovider.Observation{}, runtimeprovider.ErrProfileMismatch
	}
	cfg, err := p.configuration(req.Namespace)
	if err != nil {
		return runtimeprovider.Observation{}, err
	}
	stored, err := cfg.Releases.Last(req.Reference.ReleaseName)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		ch, err := LoadChart(ctx, p.ChartPath)
		if err != nil {
			return runtimeprovider.Observation{}, err
		}
		install := action.NewInstall(cfg)
		install.ReleaseName, install.Namespace = req.Reference.ReleaseName, req.Namespace
		install.CreateNamespace, install.TakeOwnership = false, false
		install.DisableHooks, install.SkipCRDs = true, true
		install.Timeout = time.Minute
		install.WaitStrategy = kube.HookOnlyStrategy
		install.Labels = map[string]string{catalog.OwnerLabel: req.OwnerUID}
		_, err = install.RunWithContext(ctx, ch, catalog.Values(req.OwnerUID))
		return runtimeprovider.Observation{}, err
	}
	if err != nil {
		return runtimeprovider.Observation{}, err
	}
	accessor, err := release.NewAccessor(stored)
	if err != nil {
		return runtimeprovider.Observation{}, err
	}
	if !Owned(accessor, req) {
		return runtimeprovider.Observation{}, runtimeprovider.ErrOwnership
	}
	chartAccessor, err := chart.NewAccessor(accessor.Chart())
	// Helm's accessor uses Go field names (Version), not YAML keys (version).
	if err != nil || chartAccessor.MetadataAsMap()["Version"] != req.Reference.ChartVersion {
		return runtimeprovider.Observation{}, runtimeprovider.ErrProfileMismatch
	}
	switch accessor.Status() {
	case "deployed":
		return p.observe(ctx, req)
	case "pending-install", "pending-upgrade", "pending-rollback":
		// Never start a second operation over a pending Helm transaction.
		return runtimeprovider.Observation{}, nil
	default:
		return runtimeprovider.Observation{}, runtimeprovider.ErrReleaseFailed
	}
}

func (p *Provider) observe(ctx context.Context, req runtimeprovider.Request) (runtimeprovider.Observation, error) {
	sets := &appsv1.DeploymentList{}
	if err := p.Client.List(ctx, sets, client.InNamespace(req.Namespace), client.MatchingLabels{catalog.OwnerLabel: req.OwnerUID}); err != nil {
		return runtimeprovider.Observation{}, err
	}
	if len(sets.Items) != 1 {
		return runtimeprovider.Observation{}, nil
	}
	s := sets.Items[0]
	if s.Annotations["meta.helm.sh/release-name"] != req.Reference.ReleaseName || s.Annotations["meta.helm.sh/release-namespace"] != req.Namespace {
		return runtimeprovider.Observation{}, runtimeprovider.ErrOwnership
	}
	ready := s.DeletionTimestamp.IsZero() && s.Spec.Replicas != nil && *s.Spec.Replicas > 0 && s.Status.ObservedGeneration >= s.Generation && s.Status.ReadyReplicas == *s.Spec.Replicas && s.Status.AvailableReplicas == *s.Spec.Replicas && s.Status.UpdatedReplicas == *s.Spec.Replicas
	return runtimeprovider.Observation{Ready: ready}, nil
}

// Delete retains Helm history until every chart manifest object is absent. This
// preserves cleanup evidence across a process crash or partial uninstall. It
// does not claim to inventory vCluster-generated or guest-synced objects.
func (p *Provider) Delete(ctx context.Context, req runtimeprovider.Request) error {
	if req.OwnerUID == "" || req.Reference.ReleaseName != catalog.Resolve(req.OwnerUID).ReleaseName {
		return runtimeprovider.ErrOwnership
	}
	cfg, err := p.configuration(req.Namespace)
	if err != nil {
		return err
	}
	stored, err := cfg.Releases.Last(req.Reference.ReleaseName)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	accessor, err := release.NewAccessor(stored)
	if err != nil {
		return err
	}
	if !Owned(accessor, req) {
		return runtimeprovider.ErrOwnership
	}
	objects, err := manifestObjects(accessor.Manifest(), req.Namespace)
	if err != nil {
		return err
	}
	if _, err := p.remaining(ctx, objects, req); err != nil {
		return err
	}
	if accessor.Status() != "uninstalled" {
		uninstall := action.NewUninstall(cfg)
		uninstall.DisableHooks, uninstall.KeepHistory = true, true
		uninstall.Timeout = time.Minute
		uninstall.WaitStrategy = kube.HookOnlyStrategy
		uninstall.DeletionPropagation = "foreground"
		if _, err := uninstall.Run(req.Reference.ReleaseName); err != nil {
			return err
		}
	}
	remaining, err := p.remaining(ctx, objects, req)
	if err != nil {
		return err
	}
	if remaining {
		return runtimeprovider.ErrDeletionPending
	}
	history, err := cfg.Releases.History(req.Reference.ReleaseName)
	if err != nil {
		return err
	}
	// Verify all revisions before purging any of them.
	for _, item := range history {
		a, err := release.NewAccessor(item)
		if err != nil {
			return err
		}
		if !Owned(a, req) {
			return runtimeprovider.ErrOwnership
		}
	}
	for _, item := range history {
		a, _ := release.NewAccessor(item)
		if _, err := cfg.Releases.Delete(a.Name(), a.Version()); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) remaining(ctx context.Context, objects []*unstructured.Unstructured, req runtimeprovider.Request) (bool, error) {
	remaining := false
	for _, expected := range objects {
		actual := &unstructured.Unstructured{}
		actual.SetGroupVersionKind(expected.GroupVersionKind())
		err := p.Client.Get(ctx, client.ObjectKeyFromObject(expected), actual)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if actual.GetAnnotations()["meta.helm.sh/release-name"] != req.Reference.ReleaseName || actual.GetAnnotations()["meta.helm.sh/release-namespace"] != req.Namespace || actual.GetLabels()["app.kubernetes.io/managed-by"] != "Helm" {
			return false, runtimeprovider.ErrOwnership
		}
		if actual.GetAnnotations()["helm.sh/resource-policy"] == "keep" {
			return false, runtimeprovider.ErrCleanupIncomplete
		}
		remaining = true
	}
	return remaining, nil
}

func manifestObjects(manifest, namespace string) ([]*unstructured.Unstructured, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	var objects []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if len(obj.Object) == 0 {
			continue
		}
		// This fixed profile must never manage host cluster-scoped resources.
		switch obj.GetAPIVersion() + "/" + obj.GetKind() {
		case "v1/Service", "v1/Secret", "v1/ConfigMap", "v1/ServiceAccount", "v1/LimitRange", "v1/ResourceQuota", "apps/v1/Deployment", "rbac.authorization.k8s.io/v1/Role", "rbac.authorization.k8s.io/v1/RoleBinding", "policy/v1/PodDisruptionBudget", "networking.k8s.io/v1/NetworkPolicy":
		default:
			return nil, fmt.Errorf("%w: unexpected resource %s/%s", runtimeprovider.ErrProfileMismatch, obj.GetAPIVersion(), obj.GetKind())
		}
		if obj.GetNamespace() == "" {
			obj.SetNamespace(namespace)
		}
		if obj.GetNamespace() != namespace || obj.GetName() == "" {
			return nil, runtimeprovider.ErrOwnership
		}
		objects = append(objects, obj)
	}
	return objects, nil
}
