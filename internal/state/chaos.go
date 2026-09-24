package state

import "time"

// Experiment is the encrypted authority for fault intent and rollback. Status is
// observational only. Persist every action before touching the guest API.
type Experiment struct {
	ReplicaUID string        `json:"replicaUID"`
	StartedAt  time.Time     `json:"startedAt"`
	ExpiresAt  time.Time     `json:"expiresAt"`
	Phase      string        `json:"phase"`
	Outcome    string        `json:"outcome,omitempty"`
	Message    string        `json:"message,omitempty"`
	Actions    []ChaosAction `json:"actions"`
}

type ChaosAction struct {
	Kind             string `json:"kind"`
	Namespace        string `json:"namespace"`
	NamespaceUID     string `json:"namespaceUID"`
	TargetKind       string `json:"targetKind,omitempty"`
	Name             string `json:"name"`
	UID              string `json:"uid,omitempty"`
	OperationID      string `json:"operationID"`
	OriginalReplicas *int64 `json:"originalReplicas,omitempty"`
	// Applied is written after the response; intent permits recovery after a lost response.
	Applied bool           `json:"applied,omitempty"`
	Cleaned bool           `json:"cleaned,omitempty"`
	Object  map[string]any `json:"object,omitempty"`
}
