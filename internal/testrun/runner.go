package testrun

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const runLabel = "replicove.nimeshbuilds.dev/test-run"

type Session struct {
	Kubeconfig string
	Done       <-chan error
	Close      func()
}

// Runner injects only external effects: Kubernetes, credential/tunnel setup,
// process execution, and time. OpenSession must erase its temporary credential.
type Runner struct {
	Client        client.Client
	Namespace     string
	OpenSession   func(context.Context, *api.ClusterReplica, []byte) (Session, error)
	Execute       func(context.Context, []string, string) (int, error)
	ResolveFaults func(context.Context, string, []api.ChaosFault) ([]api.ChaosFault, error)
	Now           func() time.Time
	Wait          func(context.Context) error
	NewID         func() (string, error)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
func (r *Runner) wait(ctx context.Context) error {
	if r.Wait != nil {
		return r.Wait(ctx)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		return nil
	}
}
func ID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("cannot generate run identity")
	}
	return hex.EncodeToString(b[:]), nil
}

func (r *Runner) Run(ctx context.Context, recipe Recipe) (report Report) {
	report = Report{APIVersion: RecipeVersion, Kind: "TestRunReport", Namespace: r.Namespace, StartedAt: r.now(),
		Setup: Outcome{Status: "Failed"}, Test: Outcome{Status: "NotRun"}, Cleanup: Outcome{Status: "NotNeeded"}}
	defer func() { report.FinishedAt = r.now() }()
	if err := recipe.DefaultAndValidate(); err != nil {
		report.Setup.Reason = err.Error()
		return
	}
	if r.Client == nil || r.OpenSession == nil || r.Execute == nil || r.Namespace == "" {
		report.Setup.Reason = "runner dependencies are unavailable"
		return
	}
	canonical, _ := json.Marshal(recipe)
	report.RecipeSHA256 = fmt.Sprintf("%x", sha256.Sum256(canonical))
	newID := r.NewID
	if newID == nil {
		newID = ID
	}
	id, err := newID()
	if err != nil {
		report.Setup.Reason = "cannot generate run identity"
		return
	}
	report.RunID = id
	metadata := metav1.ObjectMeta{Name: recipe.NamePrefix + "-" + id, Namespace: r.Namespace, Labels: map[string]string{runLabel: id}}
	var request client.Object
	kind := "ClusterReplica"
	if recipe.Mirror != nil {
		kind = "ReplicaMirror"
		request = &api.ReplicaMirror{ObjectMeta: metadata, Spec: *recipe.Mirror.DeepCopy()}
	} else {
		request = &api.ClusterReplica{ObjectMeta: metadata, Spec: *recipe.Replica.DeepCopy()}
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, duration(recipe.Lifecycle.ReadyTimeout))
	defer readyCancel()
	if err := r.Client.Create(readyCtx, request); err != nil {
		report.Setup.Reason = "cannot create the unique test request"
		if !apierrors.IsAlreadyExists(err) && !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
			report.Request = &Reference{Kind: kind, Name: metadata.Name}
			report.Cleanup = Outcome{Status: "Unverified", Reason: "create result was uncertain; inspect the run name; any admitted request retains its bounded TTL"}
		}
		// A colliding or uncertain name is never adopted or deleted.
		return
	}
	report.Request = reference(kind, request)
	if request.GetUID() == "" {
		report.Setup.Reason = "API returned no request UID"
		report.Cleanup = Outcome{Status: "Unverified", Reason: "request ownership cannot be proven; inspect the run name"}
		return
	}
	var access *api.ReplicaAccess
	var experiment *api.ReplicaExperiment
	var session Session
	var lease *metav1.Time
	// Independent bounded cleanup runs even after interruption or test timeout.
	// Fault rollback precedes access revocation, which precedes replica teardown.
	defer func() {
		if session.Close != nil {
			session.Close()
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), duration(recipe.Lifecycle.CleanupTimeout))
		defer cancel()
		start := r.now()
		report.Cleanup = Outcome{Status: "Verified"}
		defer func() { report.Cleanup.DurationSeconds = r.now().Sub(start).Seconds() }()
		if experiment != nil {
			if err := r.deleteAndWait(cleanupCtx, experiment); err != nil {
				report.Cleanup = Outcome{Status: "Unverified", Reason: "chaos rollback could not be verified; request remains bounded by TTL"}
				return
			}
		}
		if access != nil {
			if err := r.deleteAndWait(cleanupCtx, access); err != nil {
				report.Cleanup = Outcome{Status: "Unverified", Reason: "credential revocation could not be verified; request remains bounded by TTL"}
				return
			}
		}
		if lease != nil {
			if err := r.releaseLease(cleanupCtx, request.(*api.ReplicaMirror), lease); err != nil {
				report.Cleanup = Outcome{Status: "Unverified", Reason: "mirror lease release could not be verified; request remains bounded by TTL"}
				return
			}
		}
		// Keep only an executed failing test, never failed provisioning or cancelled
		// runs. The immutable original TTL is the retention deadline, not extended.
		if recipe.Lifecycle.KeepOnFailure && report.Test.Status == "Failed" && ctx.Err() == nil {
			fresh := request.DeepCopyObject().(client.Object)
			if err := r.getOwned(cleanupCtx, request, fresh); err != nil {
				report.Cleanup = Outcome{Status: "Unverified", Reason: "retained request ownership could not be verified"}
				return
			}
			spec := recipe.Replica
			if spec == nil {
				spec = &recipe.Mirror.Template
			}
			expiry := fresh.GetCreationTimestamp().Add(duration(spec.TTL))
			report.RetainedUntil = &expiry
			report.Cleanup = Outcome{Status: "Retained", Reason: "failed test retained until the original TTL; access revoked and any chaos rolled back"}
			return
		}
		if err := r.deleteAndWait(cleanupCtx, request); err != nil {
			report.Cleanup = Outcome{Status: "Unverified", Reason: "owned-resource finalizer cleanup could not be verified before the cleanup deadline"}
		}
	}()

	replica, err := r.waitReplica(readyCtx, request, &report)
	if err != nil {
		report.Setup.Reason = err.Error()
		return
	}
	if replica.Status.Plan == nil || replica.Status.Plan.Revision == "" {
		report.Setup.Reason = "ready replica has no captured plan revision"
		return
	}
	report.Replica = reference("ClusterReplica", replica)
	report.PlanRevision = replica.Status.Plan.Revision
	report.Resources = replica.Status.Plan.DeepCopy().Resources
	captured := replica.Status.Plan.CapturedAt.Time
	report.CapturedAt = &captured
	report.Runtime = replica.Status.Runtime.DeepCopy()
	report.SourceVersion = replica.Status.SourceVersion
	report.TargetVersion = replica.Status.TargetVersion
	if recipe.ExpectedPlanRevision != "" && recipe.ExpectedPlanRevision != report.PlanRevision {
		report.Setup.Reason = "captured plan differs from expectedPlanRevision"
		return
	}
	if recipe.Mirror != nil {
		lease, err = r.holdLease(readyCtx, request.(*api.ReplicaMirror), replica, duration(recipe.Execution.Timeout)+3*time.Minute)
		if err != nil {
			report.Setup.Reason = err.Error()
			return
		}
	}
	access, err = r.createAccess(readyCtx, replica, id, recipe.Execution)
	if err != nil {
		report.Setup.Reason = err.Error()
		return
	}
	credential, expiry, err := r.waitAccess(readyCtx, access)
	if err != nil {
		report.Setup.Reason = err.Error()
		return
	}
	if expiry.Sub(r.now()) < duration(recipe.Execution.Timeout)+15*time.Second {
		report.Setup.Reason = "issued credential expires before the requested test window"
		return
	}
	sessionCtx, cancelSession := context.WithCancel(ctx)
	stopOpening := context.AfterFunc(readyCtx, cancelSession)
	session, err = r.OpenSession(sessionCtx, replica, credential)
	stopOpening()
	defer cancelSession()
	if err != nil {
		report.Setup.Reason = "cannot open the scoped guest session"
		return
	}
	if session.Close == nil || session.Kubeconfig == "" {
		report.Setup.Reason = "guest session has no private credential file or cleanup"
		return
	}
	if recipe.Chaos != nil {
		faults := make([]api.ChaosFault, len(recipe.Chaos.Faults))
		for i := range recipe.Chaos.Faults {
			recipe.Chaos.Faults[i].DeepCopyInto(&faults[i])
		}
		if r.ResolveFaults != nil {
			faults, err = r.ResolveFaults(readyCtx, session.Kubeconfig, faults)
			if err != nil {
				report.Setup.Reason = "cannot resolve exact guest chaos targets"
				return
			}
		}
		for _, fault := range faults {
			if fault.Target != nil && fault.Target.UID == "" {
				report.Setup.Reason = "chaos targets require a resolved guest UID"
				return
			}
			report.ChaosFaults = append(report.ChaosFaults, FaultEvidence{Kind: fault.Kind, Namespace: fault.Namespace, Target: fault.Target, Image: fault.Image})
		}
		experiment = &api.ReplicaExperiment{ObjectMeta: metav1.ObjectMeta{Name: metadata.Name + "-chaos", Namespace: r.Namespace, Labels: map[string]string{runLabel: id}}, Spec: api.ReplicaExperimentSpec{ReplicaRef: api.MirrorObjectRef{Name: replica.Name, UID: string(replica.UID)}, DurationSeconds: recipe.Chaos.DurationSeconds, Faults: faults}}
		if err := r.Client.Create(readyCtx, experiment); err != nil {
			if definitiveCreateFailure(err) {
				experiment = nil
			}
			report.Setup.Reason = "cannot create the owned chaos experiment"
			return
		}
		report.Experiment = reference("ReplicaExperiment", experiment)
		if err := r.waitExperiment(readyCtx, experiment); err != nil {
			report.Setup.Reason = err.Error()
			return
		}
	}
	if readyCtx.Err() != nil || expiry.Sub(r.now()) < duration(recipe.Execution.Timeout)+15*time.Second || lease != nil && lease.Time.Sub(r.now()) < duration(recipe.Execution.Timeout)+15*time.Second {
		report.Setup.Reason = "setup exhausted the readiness, credential or mirror lease window"
		return
	}
	if err := r.verifyStable(readyCtx, replica, request, lease, report.PlanRevision, experiment); err != nil {
		report.Setup.Reason = "replica identity, captured plan, lease or chaos changed before test execution"
		return
	}
	// The ready timeout covers setup only. Keep the session context alive through
	// execution; the caller's cancellation is still observed immediately.
	report.Setup = Outcome{Status: "Passed", DurationSeconds: r.now().Sub(report.StartedAt).Seconds()}
	testCtx, cancel := context.WithTimeout(ctx, duration(recipe.Execution.Timeout))
	defer cancel()
	start := r.now()
	result := make(chan struct {
		code int
		err  error
	}, 1)
	go func() {
		code, err := r.Execute(testCtx, recipe.Execution.Command, session.Kubeconfig)
		result <- struct {
			code int
			err  error
		}{code, err}
	}()
	var guard <-chan error
	guard = r.guard(testCtx, replica, request, lease, report.PlanRevision, experiment)
	select {
	case res := <-result:
		report.Test = Outcome{Status: "Passed", ExitCode: &res.code}
		if res.err != nil || res.code != 0 {
			report.Test.Status = "Failed"
			report.Test.Reason = "test command exited unsuccessfully"
		}
	case <-testCtx.Done():
		cancel()
		<-result
		report.Test = Outcome{Status: "Interrupted", Reason: "test command was cancelled or timed out"}
	case <-session.Done:
		cancel()
		<-result
		report.Test = Outcome{Status: "Interrupted", Reason: "guest tunnel ended during the test"}
	case <-guard:
		cancel()
		<-result
		report.Test = Outcome{Status: "Interrupted", Reason: "replica identity, capture revision or mirror generation changed during the test"}
	}
	if testCtx.Err() != nil && report.Test.Status != "Interrupted" {
		report.Test.Status = "Interrupted"
		report.Test.Reason = "test command was cancelled or timed out"
	}
	report.Test.DurationSeconds = r.now().Sub(start).Seconds()
	return
}

func reference(kind string, o client.Object) *Reference {
	return &Reference{Kind: kind, Name: o.GetName(), UID: string(o.GetUID())}
}

func (r *Runner) getOwned(ctx context.Context, expected, out client.Object) error {
	if expected.GetUID() == "" {
		return errors.New("missing ownership UID")
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(expected), out); err != nil {
		return err
	}
	if out.GetUID() != expected.GetUID() {
		return errors.New("object name was reused; preserving the replacement")
	}
	return nil
}

func (r *Runner) deleteAndWait(ctx context.Context, expected client.Object) error {
	current := expected.DeepCopyObject().(client.Object)
	if err := r.getOwned(ctx, expected, current); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	uid := expected.GetUID()
	if err := r.Client.Delete(ctx, current, client.Preconditions{UID: &uid}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	for {
		current = expected.DeepCopyObject().(client.Object)
		if err := r.getOwned(ctx, expected, current); apierrors.IsNotFound(err) {
			return nil
		} else if err != nil {
			return err
		}
		if err := r.wait(ctx); err != nil {
			return err
		}
	}
}

func (r *Runner) waitReplica(ctx context.Context, request client.Object, report *Report) (*api.ClusterReplica, error) {
	for {
		obj := &api.ClusterReplica{}
		var err error
		if mirror, ok := request.(*api.ReplicaMirror); ok {
			m := &api.ReplicaMirror{}
			if err := r.getOwned(ctx, mirror, m); err != nil {
				return nil, errors.New("cannot verify mirror request identity")
			}
			if m.Status.Phase == "Rejected" {
				return nil, errors.New("mirror request was rejected; inspect its status")
			}
			if m.Status.ActiveReplica != nil && m.Status.ActiveRun != nil && m.Status.Phase == "Ready" {
				expected := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: m.Status.ActiveReplica.Name, UID: types.UID(m.Status.ActiveReplica.UID)}}
				err = r.getOwned(ctx, expected, obj)
				report.MirrorCapture = &Reference{Kind: "ReplicaMirrorRun", Name: m.Status.ActiveRun.Name, UID: m.Status.ActiveRun.UID}
				if m.Status.CapturedAt != nil {
					t := m.Status.CapturedAt.Time
					report.MirrorCapturedAt = &t
				}
			} else {
				obj = nil
			}
		} else {
			err = r.getOwned(ctx, request, obj)
		}
		if err != nil {
			return nil, errors.New("cannot verify replica request identity")
		}
		if obj != nil {
			if !obj.DeletionTimestamp.IsZero() {
				return nil, errors.New("replica is already being deleted")
			}
			if obj.Status.Phase == "Rejected" {
				return nil, errors.New("replica request was rejected; inspect its status")
			}
			if obj.Status.Phase == "Ready" {
				return obj, nil
			}
		}
		if err := r.wait(ctx); err != nil {
			return nil, errors.New("replica readiness deadline reached or operation cancelled")
		}
	}
}

func (r *Runner) holdLease(ctx context.Context, m *api.ReplicaMirror, replica *api.ClusterReplica, lifetime time.Duration) (*metav1.Time, error) {
	current := &api.ReplicaMirror{}
	if err := r.getOwned(ctx, m, current); err != nil {
		return nil, errors.New("cannot verify mirror before leasing")
	}
	if current.Status.ActiveReplica == nil || current.Status.ActiveReplica.UID != string(replica.UID) || current.Spec.HoldUntil != nil {
		return nil, errors.New("mirror generation changed or another test holds its lease")
	}
	before := current.DeepCopy()
	until := metav1.NewTime(r.now().Add(lifetime).Truncate(time.Second))
	current.Spec.HoldUntil = &until
	if err := r.Client.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return nil, errors.New("cannot acquire mirror test lease")
	}
	for {
		if err := r.getOwned(ctx, m, current); err != nil {
			return &until, errors.New("cannot verify mirror lease acceptance")
		}
		if current.Spec.HoldUntil == nil || !current.Spec.HoldUntil.Equal(&until) || current.Status.ActiveReplica == nil || current.Status.ActiveReplica.UID != string(replica.UID) {
			return &until, errors.New("mirror lease or generation changed during acquisition")
		}
		if current.Status.ObservedGeneration >= current.Generation && current.Status.Phase == "Ready" {
			return &until, nil
		}
		if err := r.wait(ctx); err != nil {
			return &until, errors.New("mirror lease was not accepted before the setup deadline")
		}
	}
}

func (r *Runner) releaseLease(ctx context.Context, m *api.ReplicaMirror, until *metav1.Time) error {
	current := &api.ReplicaMirror{}
	if err := r.getOwned(ctx, m, current); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if current.Spec.HoldUntil == nil {
		return nil
	}
	if !current.Spec.HoldUntil.Equal(until) {
		return errors.New("lease now belongs to another operation")
	}
	before := current.DeepCopy()
	current.Spec.HoldUntil = nil
	if err := r.Client.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}
	for {
		if err := r.getOwned(ctx, m, current); apierrors.IsNotFound(err) {
			return nil
		} else if err != nil {
			return err
		}
		if current.Spec.HoldUntil != nil {
			return errors.New("another operation acquired the lease")
		}
		if current.Status.ObservedGeneration >= current.Generation {
			return nil
		}
		if err := r.wait(ctx); err != nil {
			return err
		}
	}
}

func (r *Runner) createAccess(ctx context.Context, replica *api.ClusterReplica, id string, execution Execution) (*api.ReplicaAccess, error) {
	a := &api.ReplicaAccess{ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: "test-access-" + id, Labels: map[string]string{runLabel: id}, OwnerReferences: []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "ClusterReplica", Name: replica.Name, UID: replica.UID}}}, Spec: api.ReplicaAccessSpec{ReplicaName: replica.Name, ReplicaUID: string(replica.UID), Role: execution.Role, DurationSeconds: execution.CredentialSeconds}}
	if err := r.Client.Create(ctx, a); err != nil {
		if definitiveCreateFailure(err) {
			return nil, errors.New("cannot create scoped access request")
		}
		return a, errors.New("scoped access create result is uncertain; inspect the run name")
	}
	return a, nil
}

func definitiveCreateFailure(err error) bool {
	return apierrors.IsAlreadyExists(err) || apierrors.IsForbidden(err) || apierrors.IsInvalid(err)
}

func (r *Runner) waitAccess(ctx context.Context, expected *api.ReplicaAccess) ([]byte, time.Time, error) {
	for {
		a := &api.ReplicaAccess{}
		if err := r.getOwned(ctx, expected, a); err != nil {
			return nil, time.Time{}, errors.New("cannot verify access request identity")
		}
		if a.Status.Phase == "Rejected" || a.Status.Phase == "Revoked" || a.Status.Phase == "Blocked" || a.Status.Phase == "Expired" {
			return nil, time.Time{}, errors.New("scoped access request did not pass its checks")
		}
		if a.Status.Phase == "Ready" {
			secret := &corev1.Secret{}
			if a.Status.CredentialSecret == "" || a.Status.ExpiresAt == nil {
				return nil, time.Time{}, errors.New("ready access request has no bounded credential")
			}
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: a.Status.CredentialSecret}, secret); err != nil || !target.OwnedBy(secret.OwnerReferences, string(a.UID)) {
				return nil, time.Time{}, errors.New("cannot read the exact owned credential Secret")
			}
			if _, err := target.Parse(secret.Data["config"]); err != nil {
				return nil, time.Time{}, errors.New("credential failed data-only TLS validation")
			}
			return secret.Data["config"], a.Status.ExpiresAt.Time, nil
		}
		if err := r.wait(ctx); err != nil {
			return nil, time.Time{}, errors.New("access issuance deadline reached or operation cancelled")
		}
	}
}

func (r *Runner) waitExperiment(ctx context.Context, expected *api.ReplicaExperiment) error {
	for {
		current := &api.ReplicaExperiment{}
		if err := r.getOwned(ctx, expected, current); err != nil {
			return errors.New("cannot verify chaos experiment identity")
		}
		if current.Status.Phase == "Active" {
			return nil
		}
		if current.Status.Phase == "Failed" || current.Status.Phase == "Blocked" || current.Status.Phase == "Rejected" || current.Status.Phase == "Completed" || current.Status.Phase == "Cancelled" {
			return errors.New("chaos experiment did not become active; inspect experiment status")
		}
		if err := r.wait(ctx); err != nil {
			return errors.New("chaos activation deadline reached or operation cancelled")
		}
	}
}

func (r *Runner) verifyStable(ctx context.Context, replica *api.ClusterReplica, request client.Object, lease *metav1.Time, revision string, experiment *api.ReplicaExperiment) error {
	fresh := &api.ClusterReplica{}
	if err := r.getOwned(ctx, replica, fresh); err != nil || !fresh.DeletionTimestamp.IsZero() || fresh.Status.Phase != "Ready" && experiment == nil || fresh.Status.Plan == nil || fresh.Status.Plan.Revision != revision {
		return errors.New("replica changed")
	}
	if fresh.Status.Phase == "Blocked" || fresh.Status.Phase == "Rejected" || fresh.Status.Phase == "Expired" || fresh.Status.Phase == "Failed" || fresh.Status.ExpiresAt != nil && !r.now().Before(fresh.Status.ExpiresAt.Time) {
		return errors.New("replica became unavailable")
	}
	if lease != nil {
		m := &api.ReplicaMirror{}
		if err := r.getOwned(ctx, request, m); err != nil || m.Spec.HoldUntil == nil || !m.Spec.HoldUntil.Equal(lease) || !r.now().Before(lease.Time) || m.Status.ActiveReplica == nil || m.Status.ActiveReplica.UID != string(replica.UID) {
			return errors.New("mirror changed")
		}
	}
	if experiment != nil {
		current := &api.ReplicaExperiment{}
		if err := r.getOwned(ctx, experiment, current); err != nil || !current.DeletionTimestamp.IsZero() || (current.Status.Phase != "Active" && current.Status.Phase != "Completed") {
			return errors.New("chaos failed")
		}
	}
	return nil
}

func (r *Runner) guard(ctx context.Context, replica *api.ClusterReplica, request client.Object, lease *metav1.Time, revision string, experiment *api.ReplicaExperiment) <-chan error {
	done := make(chan error, 1)
	go func() {
		for {
			if err := r.verifyStable(ctx, replica, request, lease, revision, experiment); err != nil {
				done <- err
				return
			}
			if err := r.wait(ctx); err != nil {
				return
			}
		}
	}()
	return done
}
