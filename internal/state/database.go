package state

import "time"

// Database contains protected ownership and progress, never source credentials/data.
// A transfer intent surviving restart is terminal: it must not silently re-capture.
type Database struct {
	Name            string    `json:"name"`
	Namespace       string    `json:"namespace"`
	Grant           string    `json:"grant"`
	Phase           string    `json:"phase"`
	Salt            string    `json:"salt,omitempty"`
	CapturedAt      time.Time `json:"capturedAt,omitempty"`
	VerifiedAt      time.Time `json:"verifiedAt,omitempty"`
	PlanRevision    string    `json:"planRevision"`
	ArchiveSHA256   string    `json:"archiveSHA256,omitempty"`
	SanitizedSHA256 string    `json:"sanitizedSHA256,omitempty"`
	Entries         []Entry   `json:"entries,omitempty"`
	HostEntries     []Entry   `json:"hostEntries,omitempty"`
}
