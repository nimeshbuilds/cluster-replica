package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ChaosGrant is an explicit administrator delegation for guest-only faults.
// It grants no host, node, privileged-container, or cloud API access.
type ChaosGrant struct {
	// Exact guest namespace names; each must also be exclusively replica-owned.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Namespaces []string `json:"namespaces"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=6
	Kinds []string `json:"kinds"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	// +kubebuilder:default=60
	MaxDurationSeconds int64 `json:"maxDurationSeconds,omitempty"`
	// Exact immutable image references (name@sha256:digest), including stress images.
	// +kubebuilder:validation:MaxItems=16
	Images []string `json:"images,omitempty"`
	// Total across all stress and custom Jobs in an experiment.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2000
	// +kubebuilder:default=500
	MaxCPUMilli int64 `json:"maxCPUMilli,omitempty"`
	// +kubebuilder:validation:Minimum=16
	// +kubebuilder:validation:Maximum=1024
	// +kubebuilder:default=128
	MaxMemoryMiB int64 `json:"maxMemoryMiB,omitempty"`
}

type ChaosTarget struct {
	// +kubebuilder:validation:Enum=Pod;Deployment;StatefulSet
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Exact current guest UID, never a host/source UID.
	// +kubebuilder:validation:MinLength=1
	UID string `json:"uid"`
}

// ChaosFault is a single bounded fault. Faults in one experiment run together.
// Only PodDelete/ScaleZero take a target. NetworkIsolation covers one whole namespace.
// Job faults accept an image and resource limits, never a raw Pod specification.
type ChaosFault struct {
	// +kubebuilder:validation:Enum=PodDelete;ScaleZero;NetworkIsolation;CPUStress;MemoryStress;CustomJob
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Namespace string       `json:"namespace"`
	Target    *ChaosTarget `json:"target,omitempty"`
	Image     string       `json:"image,omitempty"`
	// CustomJob only. No service account, Secret, volume, or host access is exposed.
	// +kubebuilder:validation:MaxItems=32
	Command []string `json:"command,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2000
	CPUMilli int64 `json:"cpuMilli,omitempty"`
	// +kubebuilder:validation:Minimum=16
	// +kubebuilder:validation:Maximum=1024
	MemoryMiB int64 `json:"memoryMiB,omitempty"`
}

type ReplicaExperimentSpec struct {
	ReplicaRef MirrorObjectRef `json:"replicaRef"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	// +kubebuilder:default=30
	DurationSeconds int64 `json:"durationSeconds,omitempty"`
	// Simultaneous faults. One active experiment per replica avoids competing rollback.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Faults []ChaosFault `json:"faults"`
}

type ReplicaExperimentStatus struct {
	Phase     string       `json:"phase,omitempty"`
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	Message   string       `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rexperiment
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Replica",type=string,JSONPath=`.spec.replicaRef.name`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expiresAt`
type ReplicaExperiment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="experiment is immutable"
	Spec   ReplicaExperimentSpec   `json:"spec"`
	Status ReplicaExperimentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ReplicaExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicaExperiment `json:"items"`
}

func init() { SchemeBuilder.Register(&ReplicaExperiment{}, &ReplicaExperimentList{}) }
