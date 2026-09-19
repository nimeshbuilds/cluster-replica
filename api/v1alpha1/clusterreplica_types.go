package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ClusterReplicaSpec is intentionally narrower than the proposed product API.
// Create a new request to change its immutable runtime or lifetime.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new ClusterReplica"
type ClusterReplicaSpec struct {
	// Profile selects an exact, experimental runtime configuration. No profile is certified yet.
	// +kubebuilder:validation:Enum=vcluster-0.37.1-lab
	Profile string `json:"profile"`
	// TTL starts at the resource's creation time, including provisioning time.
	// +kubebuilder:validation:Pattern=`^([1-9][0-9]{0,3}m|[1-9][0-9]{0,2}h)$`
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('5m') && duration(self) <= duration('168h')",message="ttl must be between 5 minutes and 168 hours"
	TTL string `json:"ttl"`
	// CleanupPolicy explicitly acknowledges that synced workloads and external data are not inventoried yet.
	// +kubebuilder:validation:Enum=HelmReleaseOnly
	CleanupPolicy string `json:"cleanupPolicy"`
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
