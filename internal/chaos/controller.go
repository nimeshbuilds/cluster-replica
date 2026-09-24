// Package chaos runs bounded, administrator-delegated faults in verified guests.
// Durable encrypted intent is written before effects and retained until rollback.
package chaos

import (
	"context"
	"errors"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const Finalizer = "replicove.nimeshbuilds.dev/experiment-cleanup"
const marker = "replicove.nimeshbuilds.dev/chaos-operation"
const owner = "replicove.nimeshbuilds.dev/experiment"
const poll = 2 * time.Second

type Reconciler struct {
	Client                client.Client
	Store                 *state.Store
	Namespace             string
	NetworkPolicyEnforced bool
	Now                   func() time.Time
	// Connect is injectable for tests. Production uses target.Connect, which rejects host credentials.
	Connect func(context.Context, client.Client, *state.State, string) (*target.Connection, error)
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A single worker serializes admission to the per-replica experiment slot.
	return ctrl.NewControllerManagedBy(mgr).For(&api.ReplicaExperiment{}).Complete(r)
}
func (r *Reconciler) connect(ctx context.Context, st *state.State) (*target.Connection, error) {
	f := r.Connect
	if f == nil {
		f = target.Connect
	}
	conn, err := f(ctx, r.Client, st, st.RuntimeName)
	if err != nil {
		return nil, errors.New("verified guest API unavailable; cleanup remains pending")
	}
	if conn.UID == "" || conn.UID != st.TargetClusterUID {
		return nil, errors.New("guest identity differs from protected replica state")
	}
	return conn, nil
}

func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	if r.Namespace == "" || request.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}
	x := &api.ReplicaExperiment{}
	if err := r.Client.Get(ctx, request.NamespacedName, x); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	st, err := r.Store.Load(ctx, string(x.UID))
	if err != nil {
		return r.report(ctx, x, nil, "Blocked", "Protected experiment state is unavailable.")
	}
	if st != nil && st.Experiment == nil {
		return r.report(ctx, x, nil, "Blocked", "Protected experiment state has the wrong type.")
	}
	if st != nil && (st.CleanupStarted || !x.DeletionTimestamp.IsZero() || !r.now().Before(st.Experiment.ExpiresAt)) {
		outcome := st.Experiment.Outcome
		if outcome == "" {
			outcome = "Completed"
			if !x.DeletionTimestamp.IsZero() {
				outcome = "Cancelled"
			} else if st.Experiment.Phase != "Active" && st.Experiment.Phase != "Finished" {
				outcome = "Failed"
				st.Experiment.Message = "Duration elapsed before all requested faults became active."
			}
		}
		return r.cleanup(ctx, x, st, outcome, st.Experiment.Message)
	}
	if st != nil && st.Experiment.Phase == "Finished" {
		return r.report(ctx, x, st, st.Experiment.Outcome, st.Experiment.Message)
	}
	if st == nil && !x.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(x, Finalizer) {
			return r.report(ctx, x, nil, "Blocked", "Restore missing protected state before removing the cleanup finalizer.")
		}
		return r.finish(ctx, x)
	}
	if st == nil && (x.Status.StartedAt != nil || controllerutil.ContainsFinalizer(x, Finalizer)) {
		return r.report(ctx, x, nil, "Blocked", "Protected experiment state is missing; effects cannot safely be recreated.")
	}
	p := &api.ClusterReplica{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: x.Namespace, Name: x.Spec.ReplicaRef.Name}, p); err != nil || string(p.UID) != x.Spec.ReplicaRef.UID {
		if st != nil {
			return r.cleanup(ctx, x, st, "Cancelled", "The pinned replica no longer exists.")
		}
		return r.report(ctx, x, nil, "Rejected", "Select the exact UID of an existing replica.")
	}
	parent, err := r.Store.Load(ctx, string(p.UID))
	if err != nil || parent == nil {
		return r.report(ctx, x, st, "Blocked", "Protected replica state is unavailable.")
	}
	ttl, err := catalog.Validate(api.ClusterReplicaSpec{Profile: p.Spec.Profile, TTL: p.Spec.TTL, CleanupPolicy: "HelmReleaseOnly"})
	if err != nil {
		return r.report(ctx, x, st, "Rejected", "Replica TTL is invalid.")
	}
	deadline := p.CreationTimestamp.Add(ttl)
	if parent.CleanupStarted || !p.DeletionTimestamp.IsZero() || !r.now().Before(deadline) {
		return r.cleanup(ctx, x, st, "Cancelled", "The parent replica is ending.")
	}
	g := &api.ReplicaGrant{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: p.Spec.GrantRef}, g); err != nil || g.Spec.TargetNamespace != x.Namespace || string(g.UID) != parent.GrantUID || g.ResourceVersion != parent.GrantVersion {
		if st != nil {
			return r.cleanup(ctx, x, st, "Failed", "The administrator grant changed or was revoked.")
		}
		return r.report(ctx, x, nil, "Rejected", "The current grant must match the replica's captured delegation.")
	}
	if err := validate(x, g.Spec.Chaos, r.NetworkPolicyEnforced); err != nil {
		if st != nil {
			return r.cleanup(ctx, x, st, "Failed", err.Error())
		}
		return r.report(ctx, x, nil, "Rejected", err.Error())
	}
	if st == nil {
		if p.Status.Phase != "Ready" || parent.TargetClusterUID == "" {
			return r.report(ctx, x, nil, "Pending", "Wait for a ready replica with a pinned guest identity.")
		}
		if parent.MirrorRunUID != "" {
			run, e := r.Store.Load(ctx, parent.MirrorRunUID)
			if e != nil || run == nil || run.MirrorRun == nil {
				return r.report(ctx, x, nil, "Blocked", "Mirror generation state is unavailable.")
			}
			m, e := r.Store.Load(ctx, run.MirrorRun.MirrorUID)
			if e != nil || m == nil || m.Mirror == nil || m.Mirror.ActiveUID != run.OwnerUID {
				return r.report(ctx, x, nil, "Rejected", "Experiments require the active mirror generation.")
			}
			if m.Mirror.ExpiresAt.Before(deadline) {
				deadline = m.Mirror.ExpiresAt
			}
		}
		if err := r.slot(ctx, x); err != nil {
			return r.report(ctx, x, nil, "Pending", err.Error())
		}
		started := r.now()
		end := started.Add(time.Duration(duration(x)) * time.Second)
		if end.After(deadline) {
			return r.report(ctx, x, nil, "Rejected", "The experiment must finish before its parent replica expires.")
		}
		st = &state.State{OwnerUID: string(x.UID), OwnerNamespace: x.Namespace, OwnerName: x.Name, Provider: parent.Provider, GrantUID: parent.GrantUID, GrantVersion: parent.GrantVersion, TargetClusterUID: parent.TargetClusterUID, TargetSecretNamespace: parent.TargetSecretNamespace, TargetSecretName: parent.TargetSecretName, RuntimeRootUID: parent.RuntimeRootUID, ReplicaUID: parent.OwnerUID, Experiment: &state.Experiment{ReplicaUID: parent.OwnerUID, StartedAt: started, ExpiresAt: end, Phase: "Preparing"}}
		if parent.Provider == "helm" {
			st.RuntimeName = catalog.Resolve(parent.OwnerUID).ReleaseName
		}
		conn, e := r.connect(ctx, st)
		if e != nil {
			return r.report(ctx, x, nil, "Blocked", e.Error())
		}
		if err := r.prepare(ctx, x, parent, st, conn); err != nil {
			return r.report(ctx, x, nil, "Rejected", err.Error())
		}
		if err := r.Store.Save(ctx, st); err != nil {
			return r.report(ctx, x, nil, "Blocked", "Cannot persist experiment intent.")
		}
	}
	if !controllerutil.ContainsFinalizer(x, Finalizer) {
		// No guest effect is possible until BOTH the encrypted intent and this
		// finalizer exist. A crash between these writes resumes from the intent.
		before := x.DeepCopy()
		controllerutil.AddFinalizer(x, Finalizer)
		return ctrl.Result{RequeueAfter: time.Millisecond}, r.Client.Patch(ctx, x, client.MergeFrom(before))
	}
	conn, err := r.connect(ctx, st)
	if err != nil {
		return r.report(ctx, x, st, "Blocked", err.Error())
	}
	for i := range st.Experiment.Actions {
		a := &st.Experiment.Actions[i]
		if err := r.verifyNamespace(ctx, conn, a); err != nil {
			return r.cleanup(ctx, x, st, "Failed", err.Error())
		}
		if err := r.apply(ctx, conn, st, a); err != nil {
			return r.cleanup(ctx, x, st, "Failed", err.Error())
		}
	}
	started, err := r.jobsStarted(ctx, conn, st)
	if err != nil {
		return r.cleanup(ctx, x, st, "Failed", err.Error())
	}
	if !started {
		return r.report(ctx, x, st, "Preparing", "Waiting for restricted fault Jobs to start in the guest.")
	}
	st.Experiment.Phase = "Active"
	if err := r.Store.Save(ctx, st); err != nil {
		return r.report(ctx, x, st, "Blocked", "Cannot persist active experiment state.")
	}
	return r.report(ctx, x, st, "Active", "All fault intents were applied. Application recovery is evaluated by your tests.")
}

func (r *Reconciler) slot(ctx context.Context, x *api.ReplicaExperiment) error {
	items := &api.ReplicaExperimentList{}
	if err := r.Client.List(ctx, items, client.InNamespace(x.Namespace)); err != nil {
		return errors.New("Cannot inspect concurrent experiments.")
	}
	for _, other := range items.Items {
		if other.UID == x.UID || other.Spec.ReplicaRef.UID != x.Spec.ReplicaRef.UID {
			continue
		}
		st, err := r.Store.Load(ctx, string(other.UID))
		if err != nil {
			return errors.New("Another experiment's protected state is unavailable.")
		}
		if st == nil && (controllerutil.ContainsFinalizer(&other, Finalizer) || other.Status.StartedAt != nil) {
			return errors.New("Another experiment lost its protected state; restore it before admitting new faults.")
		}
		if st != nil && st.Experiment != nil && st.Experiment.Phase != "Finished" {
			return errors.New("Another experiment owns this replica's fault slot; wait for rollback.")
		}
	}
	return nil
}

// CleanupReplica is called before access and runtime teardown. Deletion only
// finishes once all reversible effects have been verified removed/restored.
func (r *Reconciler) CleanupReplica(ctx context.Context, parent *state.State) (bool, error) {
	items := &api.ReplicaExperimentList{}
	if err := r.Client.List(ctx, items, client.InNamespace(parent.OwnerNamespace)); err != nil {
		return false, errors.New("cannot enumerate replica experiments")
	}
	pending := false
	for i := range items.Items {
		x := &items.Items[i]
		if x.Spec.ReplicaRef.UID != parent.OwnerUID {
			continue
		}
		pending = true
		if x.DeletionTimestamp.IsZero() {
			if err := r.Client.Delete(ctx, x, client.Preconditions{UID: &x.UID}); err != nil && !apierrors.IsNotFound(err) {
				return false, errors.New("cannot stop replica experiment")
			}
		}
	}
	// Recover a response-loss/crash after finalizer removal but before deleting
	// the Finished protected record. Never discard unresolved orphan intent.
	secrets := &corev1.SecretList{}
	if err := r.Client.List(ctx, secrets, client.InNamespace(r.Store.Namespace), client.MatchingLabels{"app.kubernetes.io/managed-by": "replicove"}); err != nil {
		return false, errors.New("cannot inspect protected experiment cleanup records")
	}
	for _, secret := range secrets.Items {
		uid := secret.Labels["replicove.nimeshbuilds.dev/owner"]
		if uid == "" {
			continue
		}
		st, err := r.Store.Load(ctx, uid)
		if err != nil {
			return false, err
		}
		if st == nil || st.Experiment == nil || st.Experiment.ReplicaUID != parent.OwnerUID {
			continue
		}
		x := &api.ReplicaExperiment{}
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: st.OwnerNamespace, Name: st.OwnerName}, x)
		if err == nil && string(x.UID) == st.OwnerUID {
			continue
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return false, errors.New("cannot verify orphan experiment identity")
		}
		if st.Experiment.Phase != "Finished" {
			return false, errors.New("an orphaned experiment still requires rollback; restore its request and protected state")
		}
		if err := r.Store.Delete(ctx, st.OwnerUID); err != nil {
			return false, err
		}
	}
	return !pending, nil
}

func (r *Reconciler) report(ctx context.Context, x *api.ReplicaExperiment, st *state.State, phase, message string) (ctrl.Result, error) {
	before := x.DeepCopy()
	x.Status.Phase = phase
	x.Status.Message = message
	if st != nil && st.Experiment != nil {
		x.Status.StartedAt = &metav1.Time{Time: st.Experiment.StartedAt}
		x.Status.ExpiresAt = &metav1.Time{Time: st.Experiment.ExpiresAt}
	}
	if err := r.Client.Status().Patch(ctx, x, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}
	if phase == "Completed" || phase == "Cancelled" || phase == "Failed" || phase == "Rejected" {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: poll}, nil
}
func (r *Reconciler) finish(ctx context.Context, x *api.ReplicaExperiment) (ctrl.Result, error) {
	before := x.DeepCopy()
	controllerutil.RemoveFinalizer(x, Finalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, x, client.MergeFrom(before))
}
