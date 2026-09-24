package v1alpha1

// DatabaseColumn identifies one column; SQL expressions are never accepted.
type DatabaseColumn struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Column string `json:"column"`
}

type DatabaseMask struct {
	DatabaseColumn `json:",inline"`
	// Token is salted SHA-256 text pseudonymization, not general anonymization.
	// +kubebuilder:validation:Enum=Token;Null;Constant
	Strategy string `json:"strategy"`
	Domain   string `json:"domain,omitempty"`
	Value    string `json:"value,omitempty"`
}

// DatabaseRelationship declares an equality relationship that must survive masking.
type DatabaseRelationship struct {
	From DatabaseColumn `json:"from"`
	To   DatabaseColumn `json:"to"`
}

// DatabaseSubset retains rows whose selected column's text equals this value.
// It applies only to this declared table, never implicitly to related tables.
type DatabaseSubset struct {
	DatabaseColumn `json:",inline"`
	// +kubebuilder:validation:MaxLength=256
	Equals string `json:"equals"`
}

// DatabaseGrant is an administrator-only explicit database read delegation.
// PVC, Secret-copy and namespace grants do not imply database permission.
type DatabaseGrant struct {
	Name            string `json:"name"`
	SourceNamespace string `json:"sourceNamespace"`
	Host            string `json:"host"`
	// +kubebuilder:default=5432
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port     int32  `json:"port,omitempty"`
	Database string `json:"database"`
	// Secret keys are username/password. It must be in the protected operator namespace.
	CredentialsSecret NamespacedName `json:"credentialsSecret"`
	// +kubebuilder:validation:Enum=require;verify-full
	// +kubebuilder:default=verify-full
	SSLMode string `json:"sslMode,omitempty"`
	// Administrator-qualified docker-library PostgreSQL 17 image, pinned by sha256 digest.
	Image        string `json:"image"`
	StorageClass string `json:"storageClass"`
	// Bounds archive stream bytes, staging memory volume and final requested storage.
	// +kubebuilder:validation:Minimum=16777216
	// +kubebuilder:validation:Maximum=4294967296
	MaxBytes int64 `json:"maxBytes"`
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=1800
	// +kubebuilder:default=300
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
	// Explicit assertion that the host CNI enforces host NetworkPolicy.
	NetworkPolicyEnforced bool `json:"networkPolicyEnforced"`
	// Masking is admin controlled and required; every copy uses these rules.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=128
	Masking []DatabaseMask `json:"masking"`
	// +kubebuilder:validation:MaxItems=64
	Relationships []DatabaseRelationship `json:"relationships,omitempty"`
	// Filters run in the isolated copy before masking. Undeclared tables remain intact.
	// +kubebuilder:validation:MaxItems=64
	Subsets []DatabaseSubset `json:"subsets,omitempty"`
}

// DatabaseCopy requests one fresh logical snapshot of an explicitly granted database.
type DatabaseCopy struct {
	// Name becomes the application Service and credential Secret name.
	Name  string `json:"name"`
	Grant string `json:"grant"`
	// Namespace is the mapped guest application namespace, not a source namespace.
	Namespace string `json:"namespace"`
}
