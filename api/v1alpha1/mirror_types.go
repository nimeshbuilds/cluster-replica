package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// MirrorVolumeGrant delegates capture of one PVC and the exact storage classes.
// It never delegates arbitrary backend handles or direct source volume mounts.
type MirrorVolumeGrant struct {
	NamespacedName `json:",inline"`
	SnapshotClass  string `json:"snapshotClass"`
	StorageClass   string `json:"storageClass"`
}

type MirrorGrant struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Volumes []MirrorVolumeGrant `json:"volumes"`
	// +kubebuilder:default="1h"
	MinInterval string `json:"minInterval,omitempty"`
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	MaxRevisions     int32 `json:"maxRevisions,omitempty"`
	AllowForcedReset bool  `json:"allowForcedReset,omitempty"`
}

type ReplicaMirrorSpec struct {
	// Template is immutable. TTL is the lifetime of the entire mirror, not each run.
	Template ClusterReplicaSpec `json:"template"`
	// Each PVC must also appear in the captured resource plan and source grant.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="mirror volumes are immutable"
	Volumes []NamespacedName `json:"volumes"`
	// Only CrashConsistent is implemented; no implicit database consistency claim.
	// +kubebuilder:validation:Enum=CrashConsistent
	// +kubebuilder:default=CrashConsistent
	Consistency string `json:"consistency,omitempty"`
	// Empty disables scheduling. Manual sync and reset remain available.
	// +kubebuilder:validation:Pattern=`^$|^([1-9][0-9]{0,3}m|[1-9][0-9]{0,2}h)$`
	Interval string `json:"interval,omitempty"`
	Suspend  bool   `json:"suspend,omitempty"`
	// Retained capture revisions, including the active revision. Runtime copies are not retained.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	RetainRevisions int32 `json:"retainRevisions,omitempty"`
	// A bounded shared-namespace test lease. New requests may extend it by at most 30 minutes.
	HoldUntil *metav1.Time `json:"holdUntil,omitempty"`
}

type MirrorObjectRef struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

type ReplicaMirrorStatus struct {
	ObservedGeneration int64            `json:"observedGeneration,omitempty"`
	Phase              string           `json:"phase,omitempty"`
	ActiveRun          *MirrorObjectRef `json:"activeRun,omitempty"`
	ActiveReplica      *MirrorObjectRef `json:"activeReplica,omitempty"`
	PendingRun         *MirrorObjectRef `json:"pendingRun,omitempty"`
	CapturedAt         *metav1.Time     `json:"capturedAt,omitempty"`
	ActivatedAt        *metav1.Time     `json:"activatedAt,omitempty"`
	NextSyncAt         *metav1.Time     `json:"nextSyncAt,omitempty"`
	ExpiresAt          *metav1.Time     `json:"expiresAt,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rmirror
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Replica",type=string,JSONPath=`.status.activeReplica.name`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expiresAt`
type ReplicaMirror struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ReplicaMirrorSpec   `json:"spec"`
	Status            ReplicaMirrorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ReplicaMirrorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicaMirror `json:"items"`
}

type ReplicaMirrorRunSpec struct {
	MirrorRef MirrorObjectRef `json:"mirrorRef"`
	// Sync captures current host state. Reset reuses a retained Sync run's revision.
	// +kubebuilder:validation:Enum=Sync;Reset
	Action      string           `json:"action"`
	RevisionRef *MirrorObjectRef `json:"revisionRef,omitempty"`
	// Force bypasses the test lease only when the administrator grant permits it.
	Force bool `json:"force,omitempty"`
}

type ReplicaMirrorRunStatus struct {
	Phase       string           `json:"phase,omitempty"`
	ReplicaRef  *MirrorObjectRef `json:"replicaRef,omitempty"`
	RevisionRef *MirrorObjectRef `json:"revisionRef,omitempty"`
	CapturedAt  *metav1.Time     `json:"capturedAt,omitempty"`
	ActivatedAt *metav1.Time     `json:"activatedAt,omitempty"`
	Message     string           `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rmrun
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.spec.action`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type ReplicaMirrorRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="mirror run is immutable"
	Spec   ReplicaMirrorRunSpec   `json:"spec"`
	Status ReplicaMirrorRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ReplicaMirrorRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReplicaMirrorRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReplicaMirror{}, &ReplicaMirrorList{}, &ReplicaMirrorRun{}, &ReplicaMirrorRunList{})
}
