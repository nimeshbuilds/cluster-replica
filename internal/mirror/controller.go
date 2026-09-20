package mirror

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/workflow"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const Finalizer = "replicove.nimeshbuilds.dev/mirror-cleanup"
const RunAnnotation = "replicove.nimeshbuilds.dev/mirror-run"
const poll = 5 * time.Second

type Reconciler struct {
	Client                client.Client
	Store                 *state.Store
	Engine                *workflow.Engine
	Namespace             string
	NetworkPolicyEnforced bool
	Now                   func() time.Time
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&api.ReplicaMirror{}).
		Watches(&api.ReplicaMirrorRun{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []reconcile.Request {
			run := o.(*api.ReplicaMirrorRun)
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: run.Namespace, Name: run.Spec.MirrorRef.Name}}}
		})).Complete(r)
}

func mirrorTemplate(m *api.ReplicaMirror) *api.ClusterReplica {
	obj := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Namespace: m.Namespace}, Spec: *m.Spec.Template.DeepCopy()}
	if obj.Spec.Replication != nil {
		obj.Spec.Replication.Data = "EmptyVolumes"
	}
	return obj
}
func (r *Reconciler) authorize(m *api.ReplicaMirror, g *api.ReplicaGrant) (policy.Resolution, error) {
	scope, err := policy.Resolve(g, mirrorTemplate(m), r.Store.Namespace)
	if err != nil {
		return scope, err
	}
	if !r.NetworkPolicyEnforced {
		return scope, problem("NetworkIsolationRequired", "The mirror module requires an administrator-qualified host CNI that enforces its NetworkPolicies.")
	}
	if g.Spec.Mirror == nil {
		return scope, problem("VolumeDataNotGranted", "The administrator grant does not permit source volume-data capture.")
	}
	if scope.Provider == "platform" || (scope.Provider == "existing" && scope.Existing.MirrorReleaseName == "") {
		return scope, problem("MirrorRuntimeNotQualified", "Use a managed runtime or an administrator-qualified existing vCluster 0.37.1 single-namespace runtime.")
	}
	if m.Spec.Template.Approval == "Manual" {
		return scope, problem("MirrorApprovalRequired", "Mirrors require Automatic approval within an explicit administrator volume grant; manual per-plan approval is not supported.")
	}
	if m.Spec.Template.Replication == nil || m.Spec.Template.Replication.Secrets == "Follow" {
		return scope, problem("InvalidMirrorTemplate", "Use explicit replication and pinned, non-Follow Secrets. Each selected volume requires a separate data-capture grant.")
	}
	if m.Spec.Consistency != "" && m.Spec.Consistency != "CrashConsistent" {
		return scope, problem("ConsistencyUnsupported", "This CSI adapter provides per-volume crash consistency only.")
	}
	if len(m.Spec.Volumes) == 0 || len(m.Spec.Volumes) > 16 {
		return scope, problem("InvalidMirrorVolumes", "Select between one and sixteen explicitly granted PVCs.")
	}
	seen := map[api.NamespacedName]bool{}
	for _, v := range m.Spec.Volumes {
		if seen[v] || !slices.Contains(scope.Namespaces, v.Namespace) {
			return scope, problem("VolumeDataNotGranted", "Mirror PVCs must be unique and inside the selected source scope.")
		}
		seen[v] = true
		found := false
		for _, a := range g.Spec.Mirror.Volumes {
			if a.NamespacedName == v && a.SnapshotClass != "" && a.StorageClass != "" {
				found = true
			}
		}
		if !found {
			return scope, problem("VolumeDataNotGranted", "A selected PVC has no exact administrator snapshot and storage-class grant.")
		}
	}
	minInterval := g.Spec.Mirror.MinInterval
	if minInterval == "" {
		minInterval = "1h"
	}
	minimum, e := time.ParseDuration(minInterval)
	if e != nil || minimum < time.Minute {
		return scope, problem("InvalidMirrorGrant", "The administrator minimum interval must be at least one minute.")
	}
	if m.Spec.Interval != "" {
		d, e := time.ParseDuration(m.Spec.Interval)
		if e != nil || d < minimum || d > 168*time.Hour {
			return scope, problem("IntervalNotGranted", "The reset interval is outside the administrator's permitted range.")
		}
	}
	limit := g.Spec.Mirror.MaxRevisions
	if limit == 0 {
		limit = 2
	}
	if retained(m) > int(limit) || limit < 1 || limit > 10 {
		return scope, problem("RetentionNotGranted", "Capture retention exceeds the administrator grant.")
	}
	return scope, nil
}
func retained(m *api.ReplicaMirror) int {
	if m.Spec.RetainRevisions == 0 {
		return 2
	}
	return int(m.Spec.RetainRevisions)
}

func (r *Reconciler) Reconcile(ctx context.Context, key ctrl.Request) (ctrl.Result, error) {
	if r.Namespace == "" || key.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}
	m := &api.ReplicaMirror{}
	if err := r.Client.Get(ctx, key.NamespacedName, m); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	st, err := r.Store.Load(ctx, string(m.UID))
	if err != nil {
		return r.report(ctx, m, "Blocked", err)
	}
	ttl, err := catalog.Validate(api.ClusterReplicaSpec{Profile: m.Spec.Template.Profile, TTL: m.Spec.Template.TTL, CleanupPolicy: "HelmReleaseOnly"})
	if err != nil {
		return r.report(ctx, m, "Rejected", problem("InvalidTTL", "The mirror template requires a supported profile and bounded TTL."))
	}
	expires := m.CreationTimestamp.Add(ttl)
	if !m.DeletionTimestamp.IsZero() || !r.now().Before(expires) {
		return r.cleanup(ctx, m, st)
	}
	g := &api.ReplicaGrant{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: m.Spec.Template.GrantRef}, g); err != nil {
		return r.report(ctx, m, "Blocked", problem("GrantUnavailable", "The mirror source grant is unavailable."))
	}
	scope, err := r.authorize(m, g)
	if err != nil {
		return r.report(ctx, m, "Blocked", err)
	}
	if st != nil && (st.GrantUID != string(g.UID) || st.GrantVersion != g.ResourceVersion) {
		return r.report(ctx, m, "Blocked", problem("GrantChanged", "The source grant changed; delete and recreate the mirror to accept the new delegation."))
	}
	if !controllerutil.ContainsFinalizer(m, Finalizer) {
		before := m.DeepCopy()
		controllerutil.AddFinalizer(m, Finalizer)
		return ctrl.Result{RequeueAfter: time.Millisecond}, r.Client.Patch(ctx, m, client.MergeFrom(before))
	}
	if st == nil {
		if m.Status.ActiveRun != nil || m.Status.PendingRun != nil {
			return r.report(ctx, m, "Blocked", state.ErrUnavailable)
		}
		st = &state.State{OwnerUID: string(m.UID), OwnerName: m.Name, OwnerNamespace: m.Namespace, GrantUID: string(g.UID), GrantVersion: g.ResourceVersion, Mirror: &state.Mirror{ExpiresAt: expires}}
		if err := r.Store.Save(ctx, st); err != nil {
			return r.report(ctx, m, "Blocked", err)
		}
	}
	if st.Mirror == nil {
		return r.report(ctx, m, "Blocked", state.ErrIntegrity)
	}
	if m.Spec.HoldUntil != nil && !m.Spec.HoldUntil.Time.Equal(st.Mirror.HoldUntil) {
		if m.Spec.HoldUntil.After(r.now().Add(30 * time.Minute)) {
			return r.report(ctx, m, "Blocked", problem("LeaseTooLong", "A test lease may extend at most 30 minutes from acceptance."))
		}
		st.Mirror.HoldUntil = m.Spec.HoldUntil.Time
		if err := r.Store.Save(ctx, st); err != nil {
			return r.report(ctx, m, "Blocked", err)
		}
	} else if m.Spec.HoldUntil == nil && !st.Mirror.HoldUntil.IsZero() {
		st.Mirror.HoldUntil = time.Time{}
		if err := r.Store.Save(ctx, st); err != nil {
			return r.report(ctx, m, "Blocked", err)
		}
	}
	if err := r.enrollRuns(ctx, m, st); err != nil {
		return r.report(ctx, m, "Blocked", err)
	}
	// Cleanup retired runtime generations before admitting another candidate.
	if done, err := r.collect(ctx, m, st, false); err != nil || !done {
		return r.report(ctx, m, "Cleaning", err)
	}
	if st.Mirror.PendingUID == "" {
		for _, ref := range st.Mirror.Runs {
			run := &api.ReplicaMirrorRun{}
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: ref.Name}, run); err != nil {
				continue
			}
			if string(run.UID) != ref.UID {
				continue
			}
			rs, e := r.Store.Load(ctx, ref.UID)
			if e != nil {
				return r.report(ctx, m, "Blocked", e)
			}
			if run.DeletionTimestamp.IsZero() && rs != nil && rs.MirrorRun.Phase == "Queued" {
				st.Mirror.PendingUID = ref.UID
				break
			}
		}
		if err := r.Store.Save(ctx, st); err != nil {
			return r.report(ctx, m, "Blocked", err)
		}
	}
	if st.Mirror.PendingUID != "" {
		rs, err := r.Store.Load(ctx, st.Mirror.PendingUID)
		if err != nil || rs == nil {
			if err == nil {
				err = state.ErrUnavailable
			}
			return r.report(ctx, m, "Blocked", err)
		}
		run := &api.ReplicaMirrorRun{}
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: rs.OwnerName}, run); err != nil || string(run.UID) != rs.OwnerUID {
			return r.report(ctx, m, "Blocked", state.ErrIntegrity)
		}
		if run.Spec.Force && !g.Spec.Mirror.AllowForcedReset {
			return r.failRun(ctx, m, st, rs, run, problem("ForcedResetNotGranted", "The source grant does not permit bypassing a test lease."))
		}
		if err := r.advance(ctx, m, g, scope, st, rs, run, expires); err != nil {
			return r.failRun(ctx, m, st, rs, run, err)
		}
		if err := r.syncRunStatus(ctx, run, rs); err != nil {
			return ctrl.Result{}, err
		}
	} else if !m.Spec.Suspend && (st.Mirror.ActiveUID == "" && st.Mirror.LastSync.IsZero() || m.Spec.Interval != "" && !r.now().Before(st.Mirror.LastSync.Add(duration(m.Spec.Interval)))) && expires.Sub(r.now()) >= time.Minute {
		// A deterministic scheduled request name makes retries after a crash idempotent.
		name := shortName("sync", st.OwnerUID, int(st.Mirror.LastSync.Unix()))
		run := &api.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Namespace: m.Namespace, Name: name}, Spec: api.ReplicaMirrorRunSpec{MirrorRef: api.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}, Action: "Sync"}}
		if err := r.Client.Create(ctx, run); err != nil && !apierrors.IsAlreadyExists(err) {
			return r.report(ctx, m, "Blocked", problem("RunCreateFailed", "Cannot create the scheduled mirror run."))
		}
	}
	before := m.DeepCopy()
	m.Status.ExpiresAt = &metav1.Time{Time: expires}
	m.Status.ObservedGeneration = m.Generation
	m.Status.ActiveRun = nil
	m.Status.ActiveReplica = nil
	m.Status.PendingRun = nil
	m.Status.NextSyncAt = nil
	if st.Mirror.ActiveUID != "" {
		active, err := r.Store.Load(ctx, st.Mirror.ActiveUID)
		if err != nil || active == nil {
			return r.report(ctx, m, "Blocked", state.ErrUnavailable)
		}
		m.Status.ActiveRun = &api.MirrorObjectRef{Name: active.OwnerName, UID: active.OwnerUID}
		m.Status.ActiveReplica = &api.MirrorObjectRef{Name: active.MirrorRun.Child.Name, UID: active.MirrorRun.Child.UID}
		m.Status.CapturedAt = &metav1.Time{Time: active.MirrorRun.CapturedAt}
		m.Status.ActivatedAt = &metav1.Time{Time: active.MirrorRun.ActivatedAt}
	}
	if st.Mirror.PendingUID != "" {
		pending, _ := r.Store.Load(ctx, st.Mirror.PendingUID)
		if pending != nil {
			m.Status.PendingRun = &api.MirrorObjectRef{Name: pending.OwnerName, UID: pending.OwnerUID}
		}
	}
	if m.Spec.Interval != "" && !m.Spec.Suspend && !st.Mirror.LastSync.IsZero() {
		next := st.Mirror.LastSync.Add(duration(m.Spec.Interval))
		m.Status.NextSyncAt = &metav1.Time{Time: next}
	}
	if !reflect.DeepEqual(before.Status, m.Status) {
		if err := r.Client.Status().Patch(ctx, m, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}
	phase := "Preparing"
	if st.Mirror.ActiveUID != "" {
		phase = "Ready"
	}
	if st.Mirror.PendingUID != "" {
		phase = "Syncing"
	}
	if m.Spec.Suspend && st.Mirror.PendingUID == "" {
		phase = "Suspended"
	}
	return r.report(ctx, m, phase, nil)
}
func duration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }

func (r *Reconciler) enrollRuns(ctx context.Context, m *api.ReplicaMirror, st *state.State) error {
	list := &api.ReplicaMirrorRunList{}
	if err := r.Client.List(ctx, list, client.InNamespace(m.Namespace)); err != nil {
		return problem("RunListUnavailable", "Cannot enumerate mirror requests.")
	}
	slices.SortFunc(list.Items, func(a, b api.ReplicaMirrorRun) int {
		if c := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); c != 0 {
			return c
		}
		return stringsCompare(a.Name, b.Name)
	})
	for i := range list.Items {
		run := &list.Items[i]
		if run.Spec.MirrorRef.Name != m.Name || run.Spec.MirrorRef.UID != string(m.UID) {
			continue
		}
		found := false
		for _, ref := range st.Mirror.Runs {
			if ref.UID == string(run.UID) {
				found = true
			}
		}
		if found {
			continue
		}
		if !run.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(run, Finalizer) {
			continue
		}
		if len(st.Mirror.Runs) >= 32 {
			return problem("RunLimit", "At most 32 outstanding mirror runs are allowed; remove completed requests before adding more.")
		}
		before := run.DeepCopy()
		controllerutil.AddFinalizer(run, Finalizer)
		if err := controllerutil.SetControllerReference(m, run, r.Client.Scheme()); err != nil {
			return problem("RunOwnershipConflict", "A reset request already belongs to another controller.")
		}
		if err := r.Client.Patch(ctx, run, client.MergeFrom(before)); err != nil {
			return err
		}
		rs, err := r.Store.Load(ctx, string(run.UID))
		if err != nil {
			return err
		}
		if rs == nil {
			rs = &state.State{OwnerUID: string(run.UID), OwnerNamespace: m.Namespace, OwnerName: run.Name, GrantUID: st.GrantUID, GrantVersion: st.GrantVersion, MirrorRun: &state.MirrorRun{MirrorUID: st.OwnerUID, Phase: "Queued", RevisionUID: string(run.UID)}}
			if err := r.Store.Save(ctx, rs); err != nil {
				return err
			}
		}
		st.Mirror.Runs = append(st.Mirror.Runs, state.MirrorRef{Name: run.Name, UID: string(run.UID)})
		if err := r.Store.Save(ctx, st); err != nil {
			return err
		}
	}
	return nil
}
func stringsCompare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func (r *Reconciler) failRun(ctx context.Context, m *api.ReplicaMirror, parent, st *state.State, run *api.ReplicaMirrorRun, err error) (ctrl.Result, error) {
	// Transient errors remain retryable; protected intent and the active generation survive.
	before := run.DeepCopy()
	run.Status.Phase = "Blocked"
	run.Status.Message = safeError(err)
	if e := r.Client.Status().Patch(ctx, run, client.MergeFrom(before)); e != nil {
		return ctrl.Result{}, e
	}
	return r.report(ctx, m, "Blocked", err)
}
func safeError(err error) string {
	var p *planner.Problem
	var d *policy.Denied
	if errors.As(err, &p) {
		return p.Detail
	}
	if errors.As(err, &d) {
		return d.Detail
	}
	return "The operation could not complete; protected state and owned resources were retained for retry."
}
func (r *Reconciler) report(ctx context.Context, m *api.ReplicaMirror, phase string, err error) (ctrl.Result, error) {
	before := m.DeepCopy()
	m.Status.Phase = phase
	reason := "Mirror" + phase
	message := "Per-volume crash-consistent copies; inspect run status for capture and activation progress."
	condition := metav1.ConditionFalse
	if m.Status.ActiveReplica != nil && err == nil {
		condition = metav1.ConditionTrue
	}
	if err != nil {
		message = safeError(err)
		var p *planner.Problem
		var d *policy.Denied
		if errors.As(err, &p) {
			reason = p.Reason
		} else if errors.As(err, &d) {
			reason = d.Reason
		}
	}
	meta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{Type: "Ready", Status: condition, Reason: reason, Message: message, ObservedGeneration: m.Generation, LastTransitionTime: metav1.NewTime(r.now())})
	if reflect.DeepEqual(before.Status, m.Status) {
		return ctrl.Result{RequeueAfter: poll}, nil
	}
	return ctrl.Result{RequeueAfter: poll}, r.Client.Status().Patch(ctx, m, client.MergeFrom(before))
}

func (r *Reconciler) syncRunStatus(ctx context.Context, run *api.ReplicaMirrorRun, st *state.State) error {
	before := run.DeepCopy()
	run.Status.Phase = st.MirrorRun.Phase
	run.Status.Message = "Per-volume crash-consistent capture. Processes restart; external services are not cloned."
	if st.MirrorRun.Child.UID != "" {
		run.Status.ReplicaRef = &api.MirrorObjectRef{Name: st.MirrorRun.Child.Name, UID: st.MirrorRun.Child.UID}
	}
	rev, err := r.Store.Load(ctx, st.MirrorRun.RevisionUID)
	if err != nil {
		return err
	}
	if rev != nil {
		run.Status.RevisionRef = &api.MirrorObjectRef{Name: rev.OwnerName, UID: rev.OwnerUID}
	}
	if !st.MirrorRun.CapturedAt.IsZero() {
		run.Status.CapturedAt = &metav1.Time{Time: st.MirrorRun.CapturedAt}
	}
	if !st.MirrorRun.ActivatedAt.IsZero() {
		run.Status.ActivatedAt = &metav1.Time{Time: st.MirrorRun.ActivatedAt}
	}
	if reflect.DeepEqual(before.Status, run.Status) {
		return nil
	}
	return r.Client.Status().Patch(ctx, run, client.MergeFrom(before))
}
