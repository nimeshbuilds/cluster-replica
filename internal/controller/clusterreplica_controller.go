package controller

import (
	"context"
	"errors"
	"reflect"
	"time"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	runtimeprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const Finalizer = "replica.nimeshbuilds.dev/runtime-cleanup"
const pollInterval = 15 * time.Second

type Reconciler struct {
	client.Client
	Provider  runtimeprovider.Provider
	Namespace string
	Now       func() time.Time
}

func (r *Reconciler) Reconcile(ctx context.Context, key ctrl.Request) (ctrl.Result, error) {
	// Defense in depth: never let an accidentally broad cache provision elsewhere.
	if key.Namespace != r.Namespace || r.Namespace == "" {
		return ctrl.Result{}, nil
	}
	obj := &v1alpha1.ClusterReplica{}
	if err := r.Get(ctx, key.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	if !obj.DeletionTimestamp.IsZero() {
		return r.cleanup(ctx, obj, true)
	}
	if obj.Status.Phase == "Expired" {
		return ctrl.Result{}, nil
	}
	ttl, err := catalog.Validate(obj.Spec)
	if err != nil {
		return r.report(ctx, obj, "Rejected", "InvalidSpec", err.Error(), false, 0)
	}
	if obj.UID == "" || obj.CreationTimestamp.IsZero() {
		return r.report(ctx, obj, "Rejected", "MissingIdentity", "Kubernetes must assign a UID and creation timestamp.", false, 0)
	}
	// Recompute from immutable metadata/spec; a restart must never reset the TTL.
	expires := obj.CreationTimestamp.Add(ttl)
	if !now.Before(expires) {
		return r.cleanup(ctx, obj, false)
	}
	if !controllerutil.ContainsFinalizer(obj, Finalizer) {
		before := obj.DeepCopy()
		controllerutil.AddFinalizer(obj, Finalizer)
		return ctrl.Result{RequeueAfter: time.Millisecond}, r.Patch(ctx, obj, client.MergeFrom(before))
	}
	if obj.Status.Runtime == nil {
		before := obj.DeepCopy()
		resolved := catalog.Resolve(string(obj.UID))
		obj.Status.Runtime = &resolved
		obj.Status.ExpiresAt = &metav1.Time{Time: expires}
		obj.Status.Phase = "Provisioning"
		obj.Status.ObservedGeneration = obj.Generation
		return ctrl.Result{RequeueAfter: time.Millisecond}, r.Status().Patch(ctx, obj, client.MergeFrom(before))
	}
	// A persisted reference is never silently re-resolved to another version.
	if *obj.Status.Runtime != catalog.Resolve(string(obj.UID)) {
		return r.report(ctx, obj, "Blocked", "ProfileMismatch", "The recorded runtime requires its original catalog adapter.", false, retryBeforeExpiry(now, expires))
	}
	observation, err := r.Provider.Ensure(ctx, request(obj))
	if err != nil {
		return r.report(ctx, obj, "Blocked", failureReason(err), safeMessage(err), false, retryBeforeExpiry(now, expires))
	}
	if observation.Ready {
		return r.report(ctx, obj, "RuntimeReady", "ControlPlaneAvailable", "vCluster control plane is ready. Host toolset replication has not run.", true, retryBeforeExpiry(now, expires))
	}
	return r.report(ctx, obj, "Provisioning", "WaitingForControlPlane", "Waiting for the managed vCluster Deployment to become ready.", false, retryBeforeExpiry(now, expires))
}

func (r *Reconciler) cleanup(ctx context.Context, obj *v1alpha1.ClusterReplica, deleting bool) (ctrl.Result, error) {
	if obj.Status.Runtime != nil {
		// Validate immutable release identity even when its old chart is unavailable.
		if obj.Status.Runtime.ReleaseName != catalog.Resolve(string(obj.UID)).ReleaseName {
			return r.report(ctx, obj, "Blocked", "OwnershipConflict", "Recorded release identity does not match this request; cleanup is blocked.", false, pollInterval)
		}
		if err := r.Provider.Delete(ctx, request(obj)); err != nil {
			return r.report(ctx, obj, "Deleting", failureReason(err), safeMessage(err), false, pollInterval)
		}
	}
	if deleting {
		before := obj.DeepCopy()
		controllerutil.RemoveFinalizer(obj, Finalizer)
		return ctrl.Result{}, r.Patch(ctx, obj, client.MergeFrom(before))
	}
	return r.report(ctx, obj, "Expired", "RuntimeRemoved", "TTL elapsed. The Helm release is absent; synced workloads, credentials and external data are outside this cleanup policy.", false, 0)
}

func request(obj *v1alpha1.ClusterReplica) runtimeprovider.Request {
	return runtimeprovider.Request{Namespace: obj.Namespace, OwnerUID: string(obj.UID), Reference: *obj.Status.Runtime}
}

func retryBeforeExpiry(now, expires time.Time) time.Duration {
	return min(pollInterval, max(time.Millisecond, expires.Sub(now)))
}

func failureReason(err error) string {
	switch {
	case errors.Is(err, runtimeprovider.ErrOwnership):
		return "OwnershipConflict"
	case errors.Is(err, runtimeprovider.ErrReleaseFailed):
		return "ReleaseFailed"
	case errors.Is(err, runtimeprovider.ErrProfileMismatch):
		return "ProfileMismatch"
	case errors.Is(err, runtimeprovider.ErrCleanupIncomplete):
		return "RetainedResources"
	case errors.Is(err, runtimeprovider.ErrDeletionPending):
		return "DeletionInProgress"
	default:
		return "RuntimeOperationFailed"
	}
}

// Helm and Kubernetes errors can contain manifests or connection credentials.
// Keep raw provider errors out of status and automatic logs.
func safeMessage(err error) string {
	switch {
	case errors.Is(err, runtimeprovider.ErrOwnership):
		return "Release ownership differs from this request. No adoption or deletion was attempted."
	case errors.Is(err, runtimeprovider.ErrReleaseFailed):
		return "The Helm release failed. A namespace administrator must inspect it; delete and recreate the request to retry installation."
	case errors.Is(err, runtimeprovider.ErrCleanupIncomplete):
		return "Helm retained resources. Review the release before removing its finalizer."
	case errors.Is(err, runtimeprovider.ErrDeletionPending):
		return "Waiting for chart manifest objects to disappear. Helm history is retained for cleanup retries."
	case errors.Is(err, runtimeprovider.ErrProfileMismatch):
		return "The runtime reference does not match this adapter."
	default:
		return "Runtime operation failed. Check namespace permissions, connectivity and Helm release state as the namespace administrator."
	}
}

func (r *Reconciler) report(ctx context.Context, obj *v1alpha1.ClusterReplica, phase, reason, message string, ready bool, after time.Duration) (ctrl.Result, error) {
	before := obj.DeepCopy()
	obj.Status.Phase = phase
	obj.Status.ObservedGeneration = obj.Generation
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{Type: "RuntimeReady", Status: status, Reason: reason, Message: message, ObservedGeneration: obj.Generation})
	if reflect.DeepEqual(before.Status, obj.Status) {
		return ctrl.Result{RequeueAfter: after}, nil
	}
	return ctrl.Result{RequeueAfter: after}, r.Status().Patch(ctx, obj, client.MergeFrom(before))
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&v1alpha1.ClusterReplica{}).Complete(r)
}
