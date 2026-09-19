// Package planner selects desired-state roots and produces deterministic plans.
package planner

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const OperationAnnotation = "replicove.nimeshbuilds.dev/operation"
const OwnerAnnotation = "replicove.nimeshbuilds.dev/owner"

// InfrastructureLabel prevents the operator's own source readers and runtime
// management resources from becoming dependencies of a guest replica.
const InfrastructureLabel = "replicove.nimeshbuilds.dev/infrastructure"

func IsInfrastructure(obj *unstructured.Unstructured) bool {
	return obj.GetLabels()[InfrastructureLabel] == "true"
}

type Problem struct{ Reason, Detail string }

func (e *Problem) Error() string { return e.Detail }
func problem(reason, format string, args ...any) error {
	return &Problem{reason, fmt.Sprintf(format, args...)}
}

func ID(group, kind, namespace, name string) string {
	return strings.Join([]string{"object", group, kind, namespace, name}, ":")
}
func ObjectID(obj *unstructured.Unstructured) string {
	return ID(obj.GroupVersionKind().Group, obj.GetKind(), obj.GetNamespace(), obj.GetName())
}
func PackageID(namespace, name string) string { return "helm:" + namespace + "/" + name }

func Matches(selector api.ResourceSelector, obj *unstructured.Unstructured) (bool, error) {
	in := func(values []string, value string) bool {
		return len(values) == 0 || slices.Contains(values, "*") || slices.Contains(values, value)
	}
	if !in(selector.Namespaces, obj.GetNamespace()) || !in(selector.Groups, obj.GroupVersionKind().Group) || !in(selector.Kinds, obj.GetKind()) || !in(selector.Names, obj.GetName()) {
		return false, nil
	}
	if selector.LabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(selector.LabelSelector)
		if err != nil {
			return false, problem("InvalidSelector", "A label selector is invalid.")
		}
		return sel.Matches(labels.Set(obj.GetLabels())), nil
	}
	return true, nil
}

func Selected(spec *api.ReplicationSpec, obj *unstructured.Unstructured) (selected, excluded bool, err error) {
	if spec == nil {
		return false, false, nil
	}
	selected = len(spec.Include) == 0
	for _, sel := range spec.Include {
		ok, e := Matches(sel, obj)
		if e != nil {
			return false, false, e
		}
		selected = selected || ok
	}
	for _, sel := range spec.Exclude {
		ok, e := Matches(sel, obj)
		if e != nil {
			return false, false, e
		}
		excluded = excluded || ok
	}
	return selected && !excluded, excluded, nil
}

func IsGenerated(obj *unstructured.Unstructured) bool {
	if len(obj.GetOwnerReferences()) > 0 {
		return true
	}
	if obj.GetName() == "kube-root-ca.crt" && obj.GetKind() == "ConfigMap" {
		return true
	}
	if obj.GetKind() == "ServiceAccount" && obj.GetName() == "default" {
		return true
	}
	return false
}

func CredentialAllowed(obj *unstructured.Unstructured, grant *api.ReplicaGrant, spec *api.ReplicationSpec) bool {
	if obj.GetKind() != "Secret" || obj.GroupVersionKind().Group != "" {
		return true
	}
	kind, _, _ := unstructured.NestedString(obj.Object, "type")
	if kind == "kubernetes.io/service-account-token" || kind == "bootstrap.kubernetes.io/token" || kind == "helm.sh/release.v1" {
		return false
	}
	return spec != nil && spec.Secrets != "" && spec.Secrets != "None" && policy.AllowsName(grant.Spec.Secrets, obj.GetNamespace(), obj.GetName())
}

func Transform(source *unstructured.Unstructured, spec *api.ReplicationSpec) (*unstructured.Unstructured, error) {
	obj := source.DeepCopy()
	delete(obj.Object, "status")
	meta, ok := obj.Object["metadata"].(map[string]any)
	if !ok {
		return nil, problem("InvalidObject", "Resource metadata is missing.")
	}
	for _, key := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "deletionTimestamp", "deletionGracePeriodSeconds", "managedFields", "ownerReferences", "finalizers", "generateName", "selfLink"} {
		delete(meta, key)
	}
	annotations := obj.GetAnnotations()
	for key := range annotations {
		if strings.HasPrefix(key, "meta.helm.sh/") || strings.HasPrefix(key, "replicove.nimeshbuilds.dev/") || strings.HasPrefix(key, "replica.nimeshbuilds.dev/") || key == "kubectl.kubernetes.io/last-applied-configuration" || key == "deployment.kubernetes.io/revision" || strings.HasPrefix(key, "pv.kubernetes.io/") || strings.HasPrefix(key, "volume.kubernetes.io/") {
			delete(annotations, key)
		}
	}
	obj.SetAnnotations(annotations)
	labels := obj.GetLabels()
	delete(labels, "app.kubernetes.io/managed-by")
	delete(labels, "pod-template-hash")
	delete(labels, "controller-revision-hash")
	obj.SetLabels(labels)
	if obj.GetNamespace() != "" {
		obj.SetNamespace(policy.Namespace(spec, obj.GetNamespace()))
	}
	if strings.HasPrefix(obj.GetNamespace(), "kube-") {
		return nil, problem("SystemNamespaceMappingRequired", "Map source namespace %s to a non-system guest namespace.", source.GetNamespace())
	}
	if spec != nil {
		for _, override := range spec.Patches {
			if override.APIVersion != source.GetAPIVersion() || override.Kind != source.GetKind() || override.Namespace != source.GetNamespace() || override.Name != source.GetName() {
				continue
			}
			before, _ := json.Marshal(obj.Object)
			patched, err := jsonpatch.MergePatch(before, override.Patch.Raw)
			if err != nil {
				return nil, problem("InvalidPatch", "Patch for %s is invalid.", ObjectID(source))
			}
			next := &unstructured.Unstructured{}
			if err := next.UnmarshalJSON(patched); err != nil {
				return nil, problem("InvalidPatch", "Patch for %s is not a Kubernetes object.", ObjectID(source))
			}
			if next.GetAPIVersion() != obj.GetAPIVersion() || next.GetKind() != obj.GetKind() || next.GetName() != obj.GetName() || next.GetNamespace() != obj.GetNamespace() || next.GetUID() != "" || next.GetResourceVersion() != "" || len(next.GetOwnerReferences()) > 0 || len(next.GetFinalizers()) > 0 || next.Object["status"] != nil || next.GetAnnotations()[OperationAnnotation] != "" || next.GetAnnotations()[OwnerAnnotation] != "" {
				return nil, problem("InvalidPatch", "Patches cannot change resource identity, lifecycle metadata or status.")
			}
			obj = next
		}
	}
	switch obj.GetKind() {
	case "Service":
		headless, _, _ := unstructured.NestedString(obj.Object, "spec", "clusterIP")
		for _, key := range []string{"clusterIP", "clusterIPs", "ipFamilies", "ipFamilyPolicy", "healthCheckNodePort"} {
			unstructured.RemoveNestedField(obj.Object, "spec", key)
		}
		if headless == "None" {
			_ = unstructured.SetNestedField(obj.Object, "None", "spec", "clusterIP")
		}
		ports, ok, _ := unstructured.NestedSlice(obj.Object, "spec", "ports")
		if ok {
			for _, p := range ports {
				if m, ok := p.(map[string]any); ok {
					delete(m, "nodePort")
				}
			}
			_ = unstructured.SetNestedSlice(obj.Object, ports, "spec", "ports")
		}
		serviceType, _, _ := unstructured.NestedString(obj.Object, "spec", "type")
		if serviceType == "LoadBalancer" || serviceType == "NodePort" {
			return nil, problem("ExternalServiceMappingRequired", "Service %s/%s requires an explicit patch to a supported guest Service type.", source.GetNamespace(), source.GetName())
		}
	case "ServiceAccount":
		unstructured.RemoveNestedField(obj.Object, "secrets")
		for key := range obj.GetAnnotations() {
			if strings.Contains(key, "eks.amazonaws.com") || strings.Contains(key, "iam.gke.io") || strings.Contains(key, "azure.workload.identity") {
				return nil, problem("IdentityAdapterRequired", "ServiceAccount %s/%s requires an explicit verified cloud identity mapping.", source.GetNamespace(), source.GetName())
			}
		}
	case "PersistentVolumeClaim":
		if spec == nil || spec.Data != "EmptyVolumes" {
			return nil, problem("DataModeRequired", "PVC %s/%s requires data: EmptyVolumes; PVC metadata is not a data clone.", source.GetNamespace(), source.GetName())
		}
		for _, key := range []string{"volumeName", "dataSource", "dataSourceRef", "selector"} {
			unstructured.RemoveNestedField(obj.Object, "spec", key)
		}
		sc, ok, _ := unstructured.NestedString(obj.Object, "spec", "storageClassName")
		if ok && spec.StorageClassMap[sc] != "" {
			_ = unstructured.SetNestedField(obj.Object, spec.StorageClassMap[sc], "spec", "storageClassName")
		}
	case "StatefulSet":
		claims, ok, _ := unstructured.NestedSlice(obj.Object, "spec", "volumeClaimTemplates")
		if ok && len(claims) > 0 {
			if spec == nil || spec.Data != "EmptyVolumes" {
				return nil, problem("DataModeRequired", "StatefulSet %s/%s creates PVCs and requires data: EmptyVolumes.", source.GetNamespace(), source.GetName())
			}
			for _, claim := range claims {
				m, ok := claim.(map[string]any)
				if !ok {
					return nil, problem("InvalidObject", "StatefulSet claim template is invalid.")
				}
				for _, key := range []string{"volumeName", "dataSource", "dataSourceRef", "selector"} {
					unstructured.RemoveNestedField(m, "spec", key)
				}
				sc, ok, _ := unstructured.NestedString(m, "spec", "storageClassName")
				if ok && spec.StorageClassMap[sc] != "" {
					_ = unstructured.SetNestedField(m, spec.StorageClassMap[sc], "spec", "storageClassName")
				}
			}
			_ = unstructured.SetNestedSlice(obj.Object, claims, "spec", "volumeClaimTemplates")
		}

	case "Job":
		unstructured.RemoveNestedField(obj.Object, "spec", "selector")
		unstructured.RemoveNestedField(obj.Object, "spec", "manualSelector")
		for _, key := range []string{"batch.kubernetes.io/controller-uid", "controller-uid", "batch.kubernetes.io/job-name", "job-name"} {
			unstructured.RemoveNestedField(obj.Object, "spec", "template", "metadata", "labels", key)
		}
	}
	// References containing namespaces must track the namespace map.
	rewriteNamespaces(obj.Object, spec)
	for _, path := range podPaths(obj.GetKind()) {
		pod, ok, _ := unstructured.NestedMap(obj.Object, path...)
		if !ok {
			continue
		}
		delete(pod, "nodeName")
		for _, key := range []string{"hostNetwork", "hostPID", "hostIPC"} {
			if pod[key] == true {
				return nil, problem("HostCapabilityRequired", "%s %s requests host access, which this portable adapter does not grant.", obj.GetKind(), obj.GetName())
			}
		}
		if vols, ok := pod["volumes"].([]any); ok {
			for _, v := range vols {
				if m, ok := v.(map[string]any); ok && m["hostPath"] != nil {
					return nil, problem("HostCapabilityRequired", "%s %s references a hostPath volume.", obj.GetKind(), obj.GetName())
				}
			}
		}
		for _, key := range []string{"containers", "initContainers"} {
			if cs, ok := pod[key].([]any); ok {
				for _, c := range cs {
					if m, ok := c.(map[string]any); ok {
						if sc, ok := m["securityContext"].(map[string]any); ok && sc["privileged"] == true {
							return nil, problem("HostCapabilityRequired", "%s %s requests a privileged container.", obj.GetKind(), obj.GetName())
						}
					}
				}
			}
		}
		_ = unstructured.SetNestedMap(obj.Object, pod, path...)
	}

	return obj, nil
}

func rewriteNamespaces(value any, spec *api.ReplicationSpec) {
	switch x := value.(type) {
	case map[string]any:
		for k, v := range x {
			// Only known reference shapes; never rewrite arbitrary ConfigMap/Secret data.
			if k == "subjects" {
				if refs, ok := v.([]any); ok {
					for _, r := range refs {
						if m, ok := r.(map[string]any); ok && m["kind"] == "ServiceAccount" {
							if ns, ok := m["namespace"].(string); ok {
								m["namespace"] = policy.Namespace(spec, ns)
							}
						}
					}
				}
			}
			if k == "clientConfig" || k == "webhook" {
				if m, ok := v.(map[string]any); ok {
					if svc, ok := m["service"].(map[string]any); ok {
						if ns, ok := svc["namespace"].(string); ok {
							svc["namespace"] = policy.Namespace(spec, ns)
						}
					}
				}
			}
			if k != "data" && k != "stringData" {
				rewriteNamespaces(v, spec)
			}
		}
	case []any:
		for _, v := range x {
			rewriteNamespaces(v, spec)
		}
	}
}

func podPaths(kind string) [][]string {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "ReplicaSet":
		return [][]string{{"spec", "template", "spec"}}
	case "CronJob":
		return [][]string{{"spec", "jobTemplate", "spec", "template", "spec"}}
	}
	return nil
}

// Dependencies returns desired-state references, never opaque values or tokens.
func Dependencies(obj *unstructured.Unstructured) []string {
	deps := []string{}
	add := func(group, kind, ns, name string) {
		if name != "" {
			deps = append(deps, ID(group, kind, ns, name))
		}
	}
	ns := obj.GetNamespace()
	for _, path := range podPaths(obj.GetKind()) {
		pod, ok, _ := unstructured.NestedMap(obj.Object, path...)
		if !ok {
			continue
		}
		if sa, ok := pod["serviceAccountName"].(string); ok && sa != "default" {
			add("", "ServiceAccount", ns, sa)
		}
		if pulls, ok := pod["imagePullSecrets"].([]any); ok {
			for _, p := range pulls {
				if m, ok := p.(map[string]any); ok {
					n, _ := m["name"].(string)
					add("", "Secret", ns, n)
				}
			}
		}
		walkReferences(pod, ns, add)
	}
	if obj.GetKind() == "ServiceAccount" {
		refs, _, _ := unstructured.NestedSlice(obj.Object, "imagePullSecrets")
		for _, ref := range refs {
			if m, ok := ref.(map[string]any); ok {
				name, _ := m["name"].(string)
				add("", "Secret", ns, name)
			}
		}
	}
	if obj.GetKind() == "RoleBinding" || obj.GetKind() == "ClusterRoleBinding" {
		ref, _, _ := unstructured.NestedMap(obj.Object, "roleRef")
		kind, _ := ref["kind"].(string)
		name, _ := ref["name"].(string)
		targetNS := ns
		if kind == "ClusterRole" {
			targetNS = ""
		}
		add("rbac.authorization.k8s.io", kind, targetNS, name)
		subjects, _, _ := unstructured.NestedSlice(obj.Object, "subjects")
		for _, s := range subjects {
			if m, ok := s.(map[string]any); ok && m["kind"] == "ServiceAccount" {
				n, _ := m["name"].(string)
				nsp, _ := m["namespace"].(string)
				add("", "ServiceAccount", nsp, n)
			}
		}
	}
	if obj.GetKind() == "ValidatingAdmissionPolicyBinding" {
		name, _, _ := unstructured.NestedString(obj.Object, "spec", "policyName")
		add("admissionregistration.k8s.io", "ValidatingAdmissionPolicy", "", name)
	}
	if obj.GetKind() == "HorizontalPodAutoscaler" {
		ref, _, _ := unstructured.NestedMap(obj.Object, "spec", "scaleTargetRef")
		version, _ := ref["apiVersion"].(string)
		kind, _ := ref["kind"].(string)
		name, _ := ref["name"].(string)
		gv, err := schema.ParseGroupVersion(version)
		if err == nil {
			add(gv.Group, kind, ns, name)
		}
	}

	slices.Sort(deps)
	return slices.Compact(deps)
}
func walkReferences(v any, ns string, add func(string, string, string, string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if m, ok := v.(map[string]any); ok {
				kind, key := "", "name"
				switch k {
				case "secretKeyRef", "secretRef":
					kind = "Secret"
				case "secret":
					kind = "Secret"
					key = "secretName"
					if m[key] == nil {
						key = "name"
					}
				case "configMapKeyRef", "configMapRef", "configMap":
					kind = "ConfigMap"
				case "persistentVolumeClaim":
					kind = "PersistentVolumeClaim"
					key = "claimName"
				}
				if kind != "" {
					name, _ := m[key].(string)
					add("", kind, ns, name)
				}
			}
			walkReferences(v, ns, add)
		}
	case []any:
		for _, v := range x {
			walkReferences(v, ns, add)
		}
	}
}

func Order(plan *state.Plan) error {
	dependencies := map[string][]string{}
	for _, obj := range plan.Objects {
		dependencies[obj.ID] = obj.Dependencies
	}
	keys := make([]string, 0, len(dependencies))
	for key := range dependencies {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	visited := map[string]int{}
	order := []string{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] == 2 {
			return nil
		}
		if visited[id] == 1 {
			return problem("DependencyCycle", "Dependency cycle at %s.", id)
		}
		refs, ok := dependencies[id]
		if !ok {
			return problem("MissingDependency", "Required dependency %s was excluded, not granted, or not captured.", id)
		}
		visited[id] = 1
		slices.Sort(refs)
		for _, dep := range refs {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visited[id] = 2
		order = append(order, id)
		return nil
	}
	for _, id := range keys {
		if err := visit(id); err != nil {
			return err
		}
	}
	plan.Order = order
	return nil
}

func GVR(apiVersion, resource string) schema.GroupVersionResource {
	gv, _ := schema.ParseGroupVersion(apiVersion)
	return gv.WithResource(resource)
}
