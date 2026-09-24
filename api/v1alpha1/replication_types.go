package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NamespacedName struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type ResourceRule struct {
	// Empty group means the core API. Wildcards must be explicitly granted.
	Group string `json:"group"`
	Kind  string `json:"kind"`
}

type ObjectReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

type ResourceSelector struct {
	Namespaces    []string              `json:"namespaces,omitempty"`
	Groups        []string              `json:"groups,omitempty"`
	Kinds         []string              `json:"kinds,omitempty"`
	Names         []string              `json:"names,omitempty"`
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}

// ObjectPatch selects the original source identity and applies a JSON merge patch.
type ObjectPatch struct {
	ObjectReference `json:",inline"`
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Patch apiextensionsv1.JSON `json:"patch"`
}

type HelmOverride struct {
	NamespacedName `json:",inline"`
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Values apiextensionsv1.JSON `json:"values"`
}

type ReplicationSpec struct {
	// +kubebuilder:validation:MaxItems=4
	Databases []DatabaseCopy `json:"databases,omitempty"`
	// Empty means all source namespaces explicitly delegated by the grant.
	Namespaces      []string           `json:"namespaces,omitempty"`
	Include         []ResourceSelector `json:"include,omitempty"`
	Exclude         []ResourceSelector `json:"exclude,omitempty"`
	HelmReleases    []NamespacedName   `json:"helmReleases,omitempty"`
	NamespaceMap    map[string]string  `json:"namespaceMap,omitempty"`
	StorageClassMap map[string]string  `json:"storageClassMap,omitempty"`
	Patches         []ObjectPatch      `json:"patches,omitempty"`
	HelmOverrides   []HelmOverride     `json:"helmOverrides,omitempty"`
	// +kubebuilder:validation:Enum=None;Snapshot;Follow
	// +kubebuilder:default=None
	Secrets string `json:"secrets,omitempty"`
	// EmptyVolumes creates fresh storage; it does not copy source data.
	// +kubebuilder:validation:Enum=None;EmptyVolumes
	// +kubebuilder:default=None
	Data string `json:"data,omitempty"`
	// Additional readiness checks for custom resources.
	Checks []ReadinessCheck `json:"checks,omitempty"`
}

// ReadinessCheck selects the original source identity and evaluates its live guest counterpart.
type ReadinessCheck struct {
	ObjectReference `json:",inline"`
	// Use either a condition or a dot-separated status field and expected value.
	Condition string `json:"condition,omitempty"`
	Field     string `json:"field,omitempty"`
	Equals    string `json:"equals,omitempty"`
}

type TargetSpec struct {
	// auto honors the grant's configured provider; it never bypasses Platform.
	// +kubebuilder:validation:Enum=auto;helm;existing;platform
	// +kubebuilder:default=auto
	Provider string `json:"provider,omitempty"`
	// Name of an administrator-defined existing target in the grant.
	ExistingRef string `json:"existingRef,omitempty"`
}

type ExistingTarget struct {
	Name             string         `json:"name"`
	KubeconfigSecret NamespacedName `json:"kubeconfigSecret"`
	// The administrator pins the expected kube-system namespace UID.
	ClusterUID string `json:"clusterUID"`
	// Optional administrator-qualified vCluster 0.37.1 single-namespace runtime
	// in the destination namespace. Required for CSI mirrors into an existing target.
	MirrorReleaseName string `json:"mirrorReleaseName,omitempty"`
}

type PlatformTarget struct {
	Namespace       string `json:"namespace"`
	Project         string `json:"project"`
	Template        string `json:"template"`
	TemplateVersion string `json:"templateVersion"`
}

// AccessSubject is an administrator-approved reader of session credentials.
// It does not infer who originally created a request in the shared namespace.
type AccessSubject struct {
	// +kubebuilder:validation:Enum=User;Group;ServiceAccount
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type ReplicaGrantSpec struct {
	// Maximum admitted replicas using this grant. Zero leaves only runtime capacity limits.
	// Queued requests retain their original TTL. Host resource limits are enforced by ResourceQuota.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	MaxConcurrentReplicas int32 `json:"maxConcurrentReplicas,omitempty"`
	// +kubebuilder:validation:MaxItems=32
	Databases []DatabaseGrant `json:"databases,omitempty"`
	Chaos     *ChaosGrant     `json:"chaos,omitempty"`
	// Explicit permission to read volume data. Empty-volume permission alone is insufficient.
	Mirror          *MirrorGrant `json:"mirror,omitempty"`
	TargetNamespace string       `json:"targetNamespace"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	SourceNamespaces []string `json:"sourceNamespaces"`
	// Rules allow namespaced desired-state reads; credentials have separate grants.
	Resources        []ResourceRule `json:"resources"`
	ClusterResources []ResourceRule `json:"clusterResources,omitempty"`
	// Explicitly selected credential names. '*' is an administrator opt-in.
	Secrets []NamespacedName `json:"secrets,omitempty"`
	// Helm release storage contains sensitive values and embedded templates.
	HelmReleases    []NamespacedName `json:"helmReleases,omitempty"`
	ExistingTargets []ExistingTarget `json:"existingTargets,omitempty"`
	Platform        *PlatformTarget  `json:"platform,omitempty"`
	// +kubebuilder:validation:Enum=helm;platform
	// +kubebuilder:default=helm
	DefaultProvider string `json:"defaultProvider,omitempty"`
	// +kubebuilder:default="24h"
	MaxTTL string `json:"maxTTL,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2000
	// +kubebuilder:default=500
	MaxObjects int32 `json:"maxObjects,omitempty"`
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=716800
	// +kubebuilder:default=524288
	MaxCaptureBytes   int32    `json:"maxCaptureBytes,omitempty"`
	AllowEmptyVolumes bool     `json:"allowEmptyVolumes,omitempty"`
	AccessRoles       []string `json:"accessRoles,omitempty"`
	// Optional exact-Secret RBAC recipients for every access session under this grant.
	// +kubebuilder:validation:MaxItems=32
	AccessSubjects []AccessSubject `json:"accessSubjects,omitempty"`
	// +kubebuilder:validation:Minimum=600
	// +kubebuilder:validation:Maximum=3600
	// +kubebuilder:default=900
	MaxAccessSeconds int64 `json:"maxAccessSeconds,omitempty"`
}

// ReplicaGrant is administered at cluster scope. Destination users must not
// receive write access to grants or to the operator's protected state namespace.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type ReplicaGrant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ReplicaGrantSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type ReplicaGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicaGrant `json:"items"`
}

type PlannedResource struct {
	ObjectReference `json:",inline"`
	SourceNamespace string `json:"sourceNamespace,omitempty"`
	SourceName      string `json:"sourceName,omitempty"`
	SourceUID       string `json:"sourceUID,omitempty"`
	SourceVersion   string `json:"sourceVersion,omitempty"`
	SelectionReason string `json:"selectionReason,omitempty"`
	// Transformation categories contain no source or destination field values.
	Transformations []string `json:"transformations,omitempty"`
	Dependencies    []string `json:"dependencies,omitempty"`
}

type PlanSummary struct {
	// Counts describe objects observed within granted reads, not a full cluster census.
	Omitted      map[string]int32  `json:"omitted,omitempty"`
	Resources    []PlannedResource `json:"resources,omitempty"`
	Revision     string            `json:"revision"`
	CapturedAt   metav1.Time       `json:"capturedAt"`
	ObjectCount  int32             `json:"objectCount"`
	PackageCount int32             `json:"packageCount"`
	AppliedCount int32             `json:"appliedCount,omitempty"`
	// Only object identity and reasons are exposed; capture payloads stay encrypted.
	Message string `json:"message,omitempty"`
}

type ReplicaAccessSpec struct {
	ReplicaName string `json:"replicaName"`
	ReplicaUID  string `json:"replicaUID"`
	// +kubebuilder:validation:Enum=viewer;deployer;admin
	Role string `json:"role"`
	// +kubebuilder:validation:Minimum=600
	// +kubebuilder:validation:Maximum=3600
	// +kubebuilder:default=900
	DurationSeconds int64 `json:"durationSeconds,omitempty"`
}

type ReplicaAccessStatus struct {
	Phase            string       `json:"phase,omitempty"`
	CredentialSecret string       `json:"credentialSecret,omitempty"`
	ExpiresAt        *metav1.Time `json:"expiresAt,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ReplicaAccess struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="access spec is immutable"
	Spec   ReplicaAccessSpec   `json:"spec"`
	Status ReplicaAccessStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ReplicaAccessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicaAccess `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReplicaGrant{}, &ReplicaGrantList{}, &ReplicaAccess{}, &ReplicaAccessList{})
}
