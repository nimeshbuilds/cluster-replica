// Package runtime separates reconciliation from the vCluster implementation.
package runtime

import (
	"context"
	"errors"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
)

var (
	ErrOwnership         = errors.New("runtime ownership does not match this request")
	ErrReleaseFailed     = errors.New("Helm release failed; inspect it as the namespace administrator")
	ErrProfileMismatch   = errors.New("persisted runtime does not match the selected profile")
	ErrCleanupIncomplete = errors.New("Helm reports retained resources; administrator review is required")
	ErrDeletionPending   = errors.New("chart manifest objects are still terminating")
)

type Request struct {
	Namespace string
	OwnerUID  string
	Reference v1alpha1.RuntimeReference
}

type Observation struct {
	Ready bool
}

// Provider methods must be idempotent. Delete must work without the chart registry.
// Delete only manages the Helm release in the first prototype.
type Provider interface {
	Ensure(context.Context, Request) (Observation, error)
	Delete(context.Context, Request) error
}
