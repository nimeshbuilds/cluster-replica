package state

import "time"

type MirrorRef struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

// Mirror state is protected alongside ordinary replica inventory. Public status
// is never the authority for source handles, child ownership, or active revision.
type Mirror struct {
	ExpiresAt  time.Time   `json:"expiresAt"`
	Runs       []MirrorRef `json:"runs,omitempty"`
	ActiveUID  string      `json:"activeUID,omitempty"`
	PendingUID string      `json:"pendingUID,omitempty"`
	LastSync   time.Time   `json:"lastSync,omitempty"`
	HoldUntil  time.Time   `json:"holdUntil,omitempty"`
}

type MirrorRun struct {
	MirrorUID      string           `json:"mirrorUID"`
	RevisionUID    string           `json:"revisionUID"`
	ChildOperation string           `json:"childOperation,omitempty"`
	ChildTTL       string           `json:"childTTL,omitempty"`
	Child          MirrorRef        `json:"child,omitempty"`
	Phase          string           `json:"phase"`
	Snapshots      []MirrorSnapshot `json:"snapshots,omitempty"`
	CapturedAt     time.Time        `json:"capturedAt,omitempty"`
	ActivatedAt    time.Time        `json:"activatedAt,omitempty"`
	RuntimeRelease string           `json:"runtimeRelease,omitempty"`
	Imports        []MirrorImport   `json:"imports,omitempty"`
	Policies       []Entry          `json:"policies,omitempty"`
	Volumes        []Volume         `json:"volumes,omitempty"`
}

type MirrorSnapshot struct {
	Namespace     string    `json:"namespace"`
	PVCName       string    `json:"pvcName"`
	PVCUID        string    `json:"pvcUID"`
	PVName        string    `json:"pvName"`
	PVUID         string    `json:"pvUID"`
	SourceHandle  string    `json:"sourceHandle"`
	Driver        string    `json:"driver"`
	StorageClass  string    `json:"storageClass"`
	SnapshotClass string    `json:"snapshotClass"`
	Name          string    `json:"name"`
	UID           string    `json:"uid,omitempty"`
	OperationID   string    `json:"operationID"`
	ContentName   string    `json:"contentName,omitempty"`
	ContentUID    string    `json:"contentUID,omitempty"`
	Handle        string    `json:"handle,omitempty"`
	CapturedAt    time.Time `json:"capturedAt,omitempty"`
}

type MirrorImport struct {
	Name        string `json:"name"`
	UID         string `json:"uid,omitempty"`
	ContentUID  string `json:"contentUID,omitempty"`
	OperationID string `json:"operationID"`
	SourceIndex int    `json:"sourceIndex"`
}
