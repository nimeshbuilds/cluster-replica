package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ClusterReplicaSpec describes one immutable, administrator-granted replica.
// Create a new request to change its immutable runtime or lifetime.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new ClusterReplica"
type ClusterReplicaSpec struct {
	// Profile selects an exact, experimental runtime configuration. No profile is certified yet.
	// +kubebuilder:validation:Enum=vcluster-0.37.1-lab;vcluster-0.37.1-persistent
	Profile string `json:"profile"`
	// TTL starts at the resource's creation time, including provisioning time.
	// +kubebuilder:validation:Pattern=`^([1-9][0-9]{0,3}m|[1-9][0-9]{0,2}h)$`
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('5m') && duration(self) <= duration('168h')",message="ttl must be between 5 minutes and 168 hours"
	TTL string `json:"ttl"`
	// CleanupPolicy selects the legacy Helm-only lifecycle or full owned-resource cleanup.
	// +kubebuilder:validation:Enum=HelmReleaseOnly;DeleteOwned
	CleanupPolicy string `json:"cleanupPolicy"`
	// Approval controls whether a captured plan needs an explicit revision approval.
	// +kubebuilder:validation:Enum=Automatic;Manual
	// +kubebuilder:default=Automatic
	Approval string `json:"approval,omitempty"`
	// GrantRef delegates source capabilities to this destination namespace.
	GrantRef    string           `json:"grantRef,omitempty"`
	Target      *TargetSpec      `json:"target,omitempty"`
	Replication *ReplicationSpec `json:"replication,omitempty"`
}

// RuntimeReference is persisted before installation. It contains no credentials.
type RuntimeReference struct {
	ReleaseName            string `json:"releaseName"`
	Profile                string `json:"profile"`
	ChartVersion           string `json:"chartVersion"`
	ChartSHA256            string `json:"chartSHA256"`
	GuestKubernetesVersion string `json:"guestKubernetesVersion"`
}

type ClusterReplicaStatus struct {
	ObservedGeneration int64             `json:"observedGeneration,omitempty"`
	Phase              string            `json:"phase,omitempty"`
	Runtime            *RuntimeReference `json:"runtime,omitempty"`
	ExpiresAt          *metav1.Time      `json:"expiresAt,omitempty"`
	Plan               *PlanSummary      `json:"plan,omitempty"`
	TargetVersion      string            `json:"targetVersion,omitempty"`
	SourceVersion      string            `json:"sourceVersion,omitempty"`
	DriftCount         int32             `json:"driftCount,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=crpl
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Runtime",type=string,JSONPath=`.status.runtime.releaseName`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ClusterReplica struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterReplicaSpec   `json:"spec"`
	Status            ClusterReplicaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterReplicaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterReplica `json:"items"`
}

func init() { SchemeBuilder.Register(&ClusterReplica{}, &ClusterReplicaList{}) }
