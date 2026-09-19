// Package capture reads only administrator-delegated source scope. It never
// writes to the source cluster or returns raw Kubernetes errors to CR status.
package capture

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type Reader struct {
	Config    *rest.Config
	Dynamic   dynamic.Interface
	Discovery discovery.DiscoveryInterface
}
type candidate struct {
	object             state.Object
	selected, excluded bool
	fromChart          bool
}
type resource struct {
	gvr        schema.GroupVersionResource
	kind       string
	namespaced bool
}

func failed(reason, format string, args ...any) error {
	return &planner.Problem{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

func (r *Reader) Capture(ctx context.Context, request *api.ClusterReplica, grant *api.ReplicaGrant, scope policy.Resolution) (*state.Plan, error) {
	version, err := r.Discovery.ServerVersion()
	if err != nil {
		return nil, failed("DiscoveryUnavailable", "Cannot determine the source Kubernetes version.")
	}
	plan := &state.Plan{CapturedAt: time.Now().UTC(), SourceVersion: version.GitVersion}
	if request.Spec.Replication == nil {
		return plan, nil
	}
	lists, err := r.Discovery.ServerPreferredResources()
	if err != nil {
		groups, partial := discovery.GroupDiscoveryFailedErrorGroups(err)
		if !partial {
			return nil, failed("DiscoveryIncomplete", "Source API discovery is unavailable.")
		}
		// Unrelated aggregated APIs (for example metrics) must not prevent an
		// explicitly scoped capture. A wildcard grant includes every group.
		for gv := range groups {
			for _, rules := range [][]api.ResourceRule{grant.Spec.Resources, grant.Spec.ClusterResources} {
				for _, rule := range rules {
					if rule.Group == "*" || rule.Group == gv.Group {
						return nil, failed("DiscoveryIncomplete", "Discovery of granted API group %s is unavailable.", gv.Group)
					}
				}
			}
		}
	}
	resources := map[string]resource{}
	ordered := []resource{}
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			return nil, err
		}
		for _, item := range list.APIResources {
			if strings.Contains(item.Name, "/") || !slices.Contains(item.Verbs, "list") {
				continue
			}
			res := resource{gv.WithResource(item.Name), item.Kind, item.Namespaced}
			resources[gv.Group+"/"+item.Kind] = res
			ordered = append(ordered, res)
		}
	}
	slices.SortFunc(ordered, func(a, b resource) int { return strings.Compare(a.gvr.String(), b.gvr.String()) })
	pool := map[string]candidate{}
	count := 0
	packages := map[string]bool{}
	for _, ref := range request.Spec.Replication.HelmReleases {
		packages[ref.Namespace+"/"+ref.Name] = true
	}
	add := func(source *unstructured.Unstructured, res resource, fromChart bool) error {
		if planner.IsGenerated(source) || planner.IsInfrastructure(source) || policy.ReservedKind(res.gvr.Group, res.kind) {
			return nil
		}
		if !fromChart && packages[source.GetAnnotations()["meta.helm.sh/release-namespace"]+"/"+source.GetAnnotations()["meta.helm.sh/release-name"]] {
			return nil
		}
		selected, excluded, err := planner.Selected(request.Spec.Replication, source)
		if err != nil {
			return err
		}
		if fromChart {
			selected = !excluded
		}
		if !planner.CredentialAllowed(source, grant, request.Spec.Replication) && !(fromChart && res.kind == "Secret" && request.Spec.Replication.Secrets != "" && request.Spec.Replication.Secrets != "None") {
			return nil
		}
		if excluded {
			return nil
		}
		desired, err := planner.Transform(source, request.Spec.Replication)
		if err != nil {
			if selected {
				return err
			}
			return nil
		}
		id := planner.ObjectID(desired)
		if previous, ok := pool[id]; ok {
			if !fromChart || previous.fromChart {
				return failed("DuplicateTarget", "More than one source maps to %s.", id)
			}
			// Embedded chart CRDs may lack Helm ownership annotations in the source.
			// An explicitly selected chart supplies their authoritative desired state.
		} else {
			count++
			if count > scope.MaxObjects {
				return failed("CaptureLimit", "Source capture exceeds the object limit in the grant; narrow its scope.")
			}
		}

		pool[id] = candidate{object: state.Object{ID: id, APIVersion: desired.GetAPIVersion(), Kind: res.kind, Resource: res.gvr.Resource, SourceNamespace: source.GetNamespace(), SourceName: source.GetName(), SourceUID: string(source.GetUID()), SourceVersion: source.GetResourceVersion(), Namespace: desired.GetNamespace(), Name: desired.GetName(), Desired: desired.Object, Dependencies: planner.Dependencies(desired)}, selected: selected, excluded: excluded, fromChart: fromChart}
		return nil
	}
	for _, res := range ordered {
		if policy.ReservedKind(res.gvr.Group, res.kind) || res.kind == "Secret" {
			continue
		}
		rules := grant.Spec.Resources
		namespaces := scope.Namespaces
		if !res.namespaced {
			rules = grant.Spec.ClusterResources
			namespaces = []string{""}
		}
		if !policy.AllowsKind(rules, res.gvr.Group, res.kind) {
			continue
		}
		for _, ns := range namespaces {
			next := ""
			for {
				var client dynamic.ResourceInterface = r.Dynamic.Resource(res.gvr)
				if res.namespaced {
					client = r.Dynamic.Resource(res.gvr).Namespace(ns)
				}
				list, err := client.List(ctx, metav1.ListOptions{Limit: 100, Continue: next})
				if err != nil {
					return nil, failed("SourceReadDenied", "Cannot list granted %s in namespace %q; verify source RBAC.", res.kind, ns)
				}
				for i := range list.Items {
					item := &list.Items[i]
					item.SetAPIVersion(res.gvr.GroupVersion().String())
					item.SetKind(res.kind)
					if err := add(item, res, false); err != nil {
						return nil, err
					}
				}
				next = list.GetContinue()
				if next == "" {
					break
				}
			}
		}
	}
	// Credentials are separate exact-name reads unless an administrator opted into '*'.
	if request.Spec.Replication.Secrets != "" && request.Spec.Replication.Secrets != "None" {
		res := resource{schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, "Secret", true}
		for _, ref := range grant.Spec.Secrets {
			if !slices.Contains(scope.Namespaces, ref.Namespace) {
				continue
			}
			client := r.Dynamic.Resource(res.gvr).Namespace(ref.Namespace)
			if ref.Name == "*" {
				next := ""
				for {
					list, err := client.List(ctx, metav1.ListOptions{Limit: 100, Continue: next})
					if err != nil {
						return nil, failed("SecretReadDenied", "Cannot read granted Secrets in %s.", ref.Namespace)
					}
					for i := range list.Items {
						list.Items[i].SetAPIVersion("v1")
						list.Items[i].SetKind("Secret")
						if err := add(&list.Items[i], res, false); err != nil {
							return nil, err
						}
					}
					next = list.GetContinue()
					if next == "" {
						break
					}
				}
			} else {
				item, err := client.Get(ctx, ref.Name, metav1.GetOptions{})
				if err != nil {
					return nil, failed("SecretReadDenied", "Cannot read granted Secret %s/%s.", ref.Namespace, ref.Name)
				}
				item.SetAPIVersion("v1")
				item.SetKind("Secret")
				if err := add(item, res, false); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, ref := range request.Spec.Replication.HelmReleases {
		pkg, objects, err := r.chart(ctx, ref, request.Spec.Replication, scope.GuestVersion)
		if err != nil {
			return nil, err
		}
		plan.Packages = append(plan.Packages, *pkg)
		for _, obj := range objects {
			if obj.GetKind() == "Namespace" {
				continue
			}
			key := obj.GroupVersionKind().Group + "/" + obj.GetKind()
			res, ok := resources[key]
			if !ok {
				return nil, failed("UnknownChartAPI", "Chart resource %s is not served by the source API.", key)
			}
			rules := grant.Spec.Resources
			if !res.namespaced {
				rules = grant.Spec.ClusterResources
			}
			if !policy.AllowsKind(rules, res.gvr.Group, res.kind) {
				return nil, failed("ChartResourceNotGranted", "Chart requires an explicit grant for %s.", key)
			}
			if res.namespaced {
				if obj.GetNamespace() == "" {
					obj.SetNamespace(ref.Namespace)
				}
				if !slices.Contains(scope.Namespaces, obj.GetNamespace()) {
					return nil, failed("ChartNamespaceNotGranted", "Chart targets an ungranted namespace.")
				}
			} else {
				obj.SetNamespace("")
			}
			if err := add(obj, res, true); err != nil {
				return nil, err
			}
		}
	}
	// Install matching RoleBindings before starting operator workloads. Otherwise
	// readiness can wait forever for permissions that appear later in the plan.
	bindings := map[string][]string{}
	for id, c := range pool {
		if c.object.Kind != "RoleBinding" && c.object.Kind != "ClusterRoleBinding" {
			continue
		}
		subjects, _, _ := unstructured.NestedSlice(c.object.Desired, "subjects")
		for _, s := range subjects {
			if m, ok := s.(map[string]any); ok && m["kind"] == "ServiceAccount" {
				name, _ := m["name"].(string)
				ns, _ := m["namespace"].(string)
				key := planner.ID("", "ServiceAccount", ns, name)
				bindings[key] = append(bindings[key], id)
			}
		}
	}
	for id, c := range pool {
		if c.object.Kind != "Deployment" && c.object.Kind != "StatefulSet" && c.object.Kind != "DaemonSet" && c.object.Kind != "Job" && c.object.Kind != "CronJob" {
			continue
		}
		original := append([]string{}, c.object.Dependencies...)
		for _, dep := range original {
			c.object.Dependencies = append(c.object.Dependencies, bindings[dep]...)
		}
		pool[id] = c
	}

	// CRs must be ordered after their definitions and all captured operator workloads.
	definitions := map[string]string{}
	operators := []string{}
	for id, c := range pool {
		if c.object.Kind == "CustomResourceDefinition" {
			u := &unstructured.Unstructured{Object: c.object.Desired}
			group, _, _ := unstructured.NestedString(u.Object, "spec", "group")
			kind, _, _ := unstructured.NestedString(u.Object, "spec", "names", "kind")
			definitions[group+"/"+kind] = id
		}
		if c.selected && (c.object.Kind == "Deployment" || c.object.Kind == "StatefulSet") {
			operators = append(operators, id)
		}
	}
	for id, c := range pool {
		group := schema.FromAPIVersionAndKind(c.object.APIVersion, c.object.Kind).Group
		if def := definitions[group+"/"+c.object.Kind]; def != "" {
			c.object.Dependencies = append(c.object.Dependencies, def)
			c.object.Dependencies = append(c.object.Dependencies, operators...)
			pool[id] = c
		}
	}
	included := map[string]bool{}
	var include func(string) error
	include = func(id string) error {
		if included[id] {
			return nil
		}
		c, ok := pool[id]
		if !ok || c.excluded {
			return failed("MissingDependency", "Required dependency %s was excluded, not granted, or unavailable.", id)
		}
		included[id] = true
		for _, dep := range c.object.Dependencies {
			if err := include(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for id, c := range pool {
		if c.selected {
			if err := include(id); err != nil {
				return nil, err
			}
		}
	}
	ids := make([]string, 0, len(included))
	for id := range included {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		plan.Objects = append(plan.Objects, pool[id].object)
	}
	for _, check := range request.Spec.Replication.Checks {
		found := false
		for _, obj := range plan.Objects {
			if check.APIVersion == obj.APIVersion && check.Kind == obj.Kind && check.Namespace == obj.SourceNamespace && check.Name == obj.SourceName {
				found = true
				break
			}
		}
		if !found {
			return nil, failed("ReadinessTargetMissing", "A readiness check references an object outside the captured plan.")
		}
		if (check.Condition == "" && check.Field == "") || (check.Condition != "" && check.Field != "") || (check.Field != "" && !strings.HasPrefix(check.Field, "status.")) {
			return nil, failed("InvalidReadinessCheck", "Use one condition or a status field for each readiness check.")
		}
	}

	if err := planner.Order(plan); err != nil {
		return nil, err
	}
	return plan, nil
}
