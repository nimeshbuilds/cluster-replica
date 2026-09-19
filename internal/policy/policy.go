// Package policy implements explicit namespace delegation. Kubernetes RBAC
// authenticates users; only cluster administrators may modify ReplicaGrants.
package policy

import (
	"fmt"
	"slices"
	"strings"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"k8s.io/apimachinery/pkg/util/validation"
)

type Denied struct{ Reason, Detail string }

func (e *Denied) Error() string        { return e.Detail }
func deny(reason, detail string) error { return &Denied{reason, detail} }

type Resolution struct {
	Provider   string
	Namespaces []string
	MaxObjects int
	MaxBytes   int
	Existing   *api.ExistingTarget
}

func Resolve(grant *api.ReplicaGrant, obj *api.ClusterReplica, privateNamespace string) (Resolution, error) {
	out := Resolution{}
	if grant == nil || obj.Spec.GrantRef == "" || grant.Name != obj.Spec.GrantRef || grant.Spec.TargetNamespace != obj.Namespace {
		return out, deny("GrantDenied", "An administrator grant for this destination namespace is required.")
	}
	if obj.Namespace == privateNamespace {
		return out, deny("ProtectedNamespace", "Replicas cannot run in the protected state namespace.")
	}
	ttl, err := catalog.Validate(api.ClusterReplicaSpec{Profile: obj.Spec.Profile, TTL: obj.Spec.TTL, CleanupPolicy: "HelmReleaseOnly"})
	if err != nil {
		return out, deny("InvalidSpec", err.Error())
	}
	maxTTL := grant.Spec.MaxTTL
	if maxTTL == "" {
		maxTTL = "24h"
	}
	cap, err := time.ParseDuration(maxTTL)
	if err != nil || ttl > cap {
		return out, deny("TTLNotGranted", "The requested TTL exceeds the administrator grant.")
	}
	if obj.Spec.CleanupPolicy != "DeleteOwned" {
		return out, deny("CleanupPolicyRequired", "Replication and existing targets require DeleteOwned cleanup.")
	}
	out.Provider = grant.Spec.DefaultProvider
	if out.Provider == "" {
		out.Provider = "helm"
	}
	if obj.Spec.Target != nil && obj.Spec.Target.Provider != "" && obj.Spec.Target.Provider != "auto" {
		out.Provider = obj.Spec.Target.Provider
	}
	if grant.Spec.DefaultProvider == "platform" && out.Provider == "helm" {
		return out, deny("PlatformRequired", "This grant requires the configured Platform provider.")
	}
	switch out.Provider {
	case "helm":
	case "platform":
		if grant.Spec.Platform == nil {
			return out, deny("PlatformNotConfigured", "The grant has no Platform configuration; Helm fallback is disabled.")
		}
	case "existing":
		if obj.Spec.Target == nil {
			return out, deny("TargetNotGranted", "Select an existing target from the administrator grant.")
		}
		for _, target := range grant.Spec.ExistingTargets {
			if target.Name == obj.Spec.Target.ExistingRef {
				copy := target
				out.Existing = &copy
			}
		}
		if out.Existing == nil || out.Existing.ClusterUID == "" || out.Existing.KubeconfigSecret.Namespace != privateNamespace {
			return out, deny("TargetNotGranted", "The existing target must be pinned by cluster UID and use an administrator-only credential Secret.")
		}
	default:
		return out, deny("UnknownProvider", "The requested runtime provider is unsupported.")
	}
	out.MaxObjects = int(grant.Spec.MaxObjects)
	if out.MaxObjects == 0 {
		out.MaxObjects = 500
	}
	out.MaxBytes = int(grant.Spec.MaxCaptureBytes)
	if out.MaxBytes == 0 {
		out.MaxBytes = 524288
	}
	if out.MaxObjects < 1 || out.MaxObjects > 2000 || out.MaxBytes < 1024 || out.MaxBytes > 716800 {
		return out, deny("InvalidGrant", "The administrator grant has invalid capture limits.")
	}
	out.Namespaces = append([]string{}, grant.Spec.SourceNamespaces...)
	if obj.Spec.Replication != nil && len(obj.Spec.Replication.Namespaces) > 0 {
		out.Namespaces = append([]string{}, obj.Spec.Replication.Namespaces...)
	}
	slices.Sort(out.Namespaces)
	out.Namespaces = slices.Compact(out.Namespaces)
	for _, ns := range out.Namespaces {
		if ns == obj.Namespace || ns == privateNamespace || !slices.Contains(grant.Spec.SourceNamespaces, ns) || len(validation.IsDNS1123Label(ns)) > 0 {
			return out, deny("SourceNotGranted", fmt.Sprintf("Source namespace %q is not an allowed independent source.", ns))
		}
	}
	if r := obj.Spec.Replication; r != nil {
		if r.Secrets != "" && r.Secrets != "None" && r.Secrets != "Snapshot" && r.Secrets != "Follow" {
			return out, deny("InvalidSpec", "Unknown secret update mode.")
		}
		if r.Data != "" && r.Data != "None" && r.Data != "EmptyVolumes" {
			return out, deny("InvalidSpec", "Unknown data mode.")
		}
		if r.Data == "EmptyVolumes" && !grant.Spec.AllowEmptyVolumes {
			return out, deny("DataNotGranted", "Fresh PVC provisioning is not authorized by this grant.")
		}
		for src, dst := range r.NamespaceMap {
			if !slices.Contains(out.Namespaces, src) || len(validation.IsDNS1123Label(dst)) > 0 || strings.HasPrefix(dst, "kube-") {
				return out, deny("InvalidMapping", "Namespace mappings must originate in the selected source scope and avoid guest system namespaces.")
			}
		}
		for _, release := range r.HelmReleases {
			if !AllowsName(grant.Spec.HelmReleases, release.Namespace, release.Name) || !slices.Contains(out.Namespaces, release.Namespace) {
				return out, deny("HelmNotGranted", fmt.Sprintf("Helm release %s/%s is not granted.", release.Namespace, release.Name))
			}
		}
	}
	return out, nil
}

func AllowsName(refs []api.NamespacedName, namespace, name string) bool {
	for _, ref := range refs {
		if ref.Namespace == namespace && (ref.Name == name || ref.Name == "*") {
			return true
		}
	}
	return false
}
func AllowsKind(rules []api.ResourceRule, group, kind string) bool {
	for _, rule := range rules {
		if (rule.Group == group || rule.Group == "*") && (rule.Kind == kind || rule.Kind == "*") {
			return true
		}
	}
	return false
}
func Namespace(r *api.ReplicationSpec, source string) string {
	if r != nil && r.NamespaceMap[source] != "" {
		return r.NamespaceMap[source]
	}
	return source
}
func ReservedKind(group, kind string) bool {
	if group == api.GroupVersion.Group || strings.HasSuffix(group, "loft.sh") {
		return true
	}
	switch kind {
	case "Node", "PersistentVolume", "VolumeAttachment", "CertificateSigningRequest", "TokenReview", "SubjectAccessReview", "LocalSubjectAccessReview", "SelfSubjectAccessReview", "SelfSubjectRulesReview", "Namespace", "Pod", "ReplicaSet", "Endpoints", "EndpointSlice", "Event", "Lease":
		return true
	}
	return false
}
