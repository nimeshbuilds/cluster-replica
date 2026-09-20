package mirror

import (
	"context"
	"fmt"
	"reflect"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/policy"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *Reconciler) advance(ctx context.Context, m *api.ReplicaMirror, g *api.ReplicaGrant, scope policy.Resolution, parent, st *state.State, run *api.ReplicaMirrorRun, expires time.Time) error {
	if !run.DeletionTimestamp.IsZero() {
		return nil
	}
	if st.MirrorRun.Phase == "Queued" {
		if expires.Sub(r.now()) < 5*time.Minute {
			return problem("MirrorExpiring", "Fewer than five minutes remain; no new generation can be provisioned.")
		}
		if run.Spec.Action == "Reset" {
			if run.Spec.RevisionRef == nil {
				return problem("RevisionRequired", "Reset requires a retained Sync run name and UID.")
			}
			found := false
			for _, ref := range parent.Mirror.Runs {
				if ref.UID == run.Spec.RevisionRef.UID && ref.Name == run.Spec.RevisionRef.Name {
					found = true
				}
			}
			if !found {
				return problem("RevisionNotGranted", "The requested revision is not retained by this mirror.")
			}
			rev, err := r.Store.Load(ctx, run.Spec.RevisionRef.UID)
			if err != nil {
				return err
			}
			if rev == nil || rev.MirrorRun == nil || rev.MirrorRun.MirrorUID != parent.OwnerUID || rev.MirrorRun.RevisionUID != rev.OwnerUID || rev.MirrorRun.CapturedAt.IsZero() {
				return problem("RevisionUnavailable", "Reset requires a completed, retained source capture from this mirror.")
			}
			st.MirrorRun.RevisionUID = rev.OwnerUID
			st.MirrorRun.CapturedAt = rev.MirrorRun.CapturedAt
			st.MirrorRun.Phase = "Restoring"
		} else if run.Spec.Action == "Sync" {
			request := mirrorTemplate(m)
			if scope.Existing != nil {
				probe := &state.State{Provider: "existing", TargetSecretNamespace: scope.Existing.KubeconfigSecret.Namespace, TargetSecretName: scope.Existing.KubeconfigSecret.Name, TargetClusterUID: scope.Existing.ClusterUID}
				conn, err := target.Connect(ctx, r.Client, probe, "")
				if err != nil {
					return err
				}
				scope.GuestVersion = conn.Version
			}
			plan, err := r.Engine.Reader.Capture(ctx, request, g, scope)
			if err != nil {
				return err
			}
			st.Plan = plan
			for i, v := range m.Spec.Volumes {
				var vg api.MirrorVolumeGrant
				for _, allowed := range g.Spec.Mirror.Volumes {
					if allowed.NamespacedName == v {
						vg = allowed
						break
					}
				}
				snap, err := r.inspectVolume(ctx, v, vg, i, st)
				if err != nil {
					return err
				}
				st.MirrorRun.Snapshots = append(st.MirrorRun.Snapshots, snap)
				// Explicitly selected StatefulSet claims can be controller-owned and
				// therefore excluded by ordinary desired-state capture. Capture them
				// through the exact, independent volume grant.
				found := false
				for _, o := range plan.Objects {
					if o.Kind == "PersistentVolumeClaim" && o.SourceNamespace == v.Namespace && o.SourceName == v.Name {
						found = true
					}
				}
				if !found {
					pvc := &unstructured.Unstructured{}
					pvc.SetAPIVersion("v1")
					pvc.SetKind("PersistentVolumeClaim")
					if err := r.Client.Get(ctx, client.ObjectKey{Namespace: v.Namespace, Name: v.Name}, pvc); err != nil {
						return problem("SourceVolumeUnavailable", "Cannot capture the explicitly granted PVC configuration.")
					}
					desired, err := planner.Transform(pvc, request.Spec.Replication)
					if err != nil {
						return err
					}
					plan.Objects = append(plan.Objects, state.Object{ID: planner.ObjectID(desired), APIVersion: "v1", Kind: "PersistentVolumeClaim", Resource: "persistentvolumeclaims", SourceNamespace: v.Namespace, SourceName: v.Name, SourceUID: string(pvc.GetUID()), SourceVersion: pvc.GetResourceVersion(), Namespace: desired.GetNamespace(), Name: desired.GetName(), Desired: desired.Object})
				}
			}
			if err := validatePlan(plan, st.MirrorRun.Snapshots, scope.Provider); err != nil {
				return err
			}
			if err := planner.Order(plan); err != nil {
				return err
			}
			plan.Revision, err = r.Store.Revision(ctx, plan)
			if err != nil {
				return err
			}
			if len(plan.Objects) > scope.MaxObjects {
				return problem("CaptureLimit", "The mirror's configuration plus explicit PVCs exceeds the source grant object limit.")
			}
			st.MirrorRun.Phase = "Capturing"
		} else {
			return problem("UnknownRunAction", "Only Sync and Reset operations are supported.")
		}
		parent.Mirror.LastSync = r.now()
		if err := r.Store.Save(ctx, parent); err != nil {
			return err
		}
		bounded := *r.Store
		bounded.MaxBytes = scope.MaxBytes
		return bounded.Save(ctx, st)
	}
	if st.MirrorRun.Phase == "Capturing" {
		done, err := r.captureVolumes(ctx, st)
		if err != nil || !done {
			return err
		}
		// Reject a mixed configuration capture instead of claiming an atomic
		// Kubernetes-plus-storage transaction. Status-only changes also retry.
		for _, o := range st.Plan.Objects {
			if o.SourceUID == "" {
				continue
			}
			live := &unstructured.Unstructured{}
			live.SetAPIVersion(o.APIVersion)
			live.SetKind(o.Kind)
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: o.SourceNamespace, Name: o.SourceName}, live); err != nil || string(live.GetUID()) != o.SourceUID {
				return problem("SourceConfigurationChanged", "Source identity changed during capture. Cancel this run and sync again; the active generation is preserved.")
			}
			desired, err := planner.Transform(live, m.Spec.Template.Replication)
			if err != nil || !reflect.DeepEqual(desired.Object, o.Desired) {
				return problem("SourceConfigurationChanged", "Source desired configuration changed during capture. Cancel this run and sync again; the active generation is preserved.")
			}
		}
		st.MirrorRun.CapturedAt = st.MirrorRun.Snapshots[0].CapturedAt
		for _, s := range st.MirrorRun.Snapshots {
			if s.CapturedAt.After(st.MirrorRun.CapturedAt) {
				st.MirrorRun.CapturedAt = s.CapturedAt
			}
		}
		st.MirrorRun.Phase = "Restoring"
		return r.Store.Save(ctx, st)
	}
	revision, err := r.Store.Load(ctx, st.MirrorRun.RevisionUID)
	if err != nil {
		return err
	}
	if revision == nil || revision.Plan == nil {
		return state.ErrUnavailable
	}
	if st.MirrorRun.Phase == "Restoring" {
		done, err := r.importVolumes(ctx, st, revision)
		if err != nil || !done {
			return err
		}
		child, err := r.child(ctx, m, scope, st, expires)
		if err != nil {
			return err
		}
		if scope.Provider == "helm" {
			st.MirrorRun.RuntimeRelease = catalog.Resolve(string(child.UID)).ReleaseName
		} else {
			st.MirrorRun.RuntimeRelease = scope.Existing.MirrorReleaseName
		}
		if err := r.Store.Save(ctx, st); err != nil {
			return err
		}
		cs, err := r.Store.Load(ctx, string(child.UID))
		if err != nil {
			return err
		}
		if cs == nil {
			plan, err := generationPlan(revision.Plan, revision.MirrorRun.Snapshots, st.MirrorRun.Imports, child, scope.Provider)
			if err != nil {
				return err
			}
			plan.Revision, err = r.Store.Revision(ctx, plan)
			if err != nil {
				return err
			}
			cs = &state.State{OwnerUID: string(child.UID), OwnerName: child.Name, OwnerNamespace: child.Namespace, GrantUID: st.GrantUID, GrantVersion: st.GrantVersion, Provider: scope.Provider, MirrorRunUID: st.OwnerUID, Plan: plan}
			if scope.Existing != nil {
				cs.TargetSecretNamespace = scope.Existing.KubeconfigSecret.Namespace
				cs.TargetSecretName = scope.Existing.KubeconfigSecret.Name
				cs.TargetClusterUID = scope.Existing.ClusterUID
			} else {
				cs.TargetSecretNamespace = child.Namespace
				cs.TargetSecretName = "vc-" + catalog.Resolve(cs.OwnerUID).ReleaseName
			}
			if err := r.ensurePolicies(ctx, st, cs); err != nil {
				return err
			}
			return r.Store.Save(ctx, cs)
		}
		if cs.MirrorRunUID != st.OwnerUID {
			return state.ErrIntegrity
		}
		if !meta.IsStatusConditionTrue(child.Status.Conditions, "Ready") {
			return nil
		}
		if err := r.verifyVolumes(ctx, st, revision, cs); err != nil {
			return err
		}
		st.MirrorRun.Phase = "AwaitingActivation"
		return r.Store.Save(ctx, st)
	}
	if st.MirrorRun.Phase == "AwaitingActivation" {
		if !run.Spec.Force && r.now().Before(parent.Mirror.HoldUntil) {
			return nil
		}
		if parent.Mirror.ActiveUID != "" && parent.Mirror.ActiveUID != st.OwnerUID {
			old, err := r.Store.Load(ctx, parent.Mirror.ActiveUID)
			if err != nil {
				return err
			}
			if old == nil {
				return state.ErrUnavailable
			}
			old.MirrorRun.Phase = "Retiring"
			if err := r.Store.Save(ctx, old); err != nil {
				return err
			}
		}
		// Persist activation before publishing status. Reconciliation recovers
		// either side of a crash without ever selecting an unvalidated candidate.
		st.MirrorRun.Phase = "Active"
		st.MirrorRun.ActivatedAt = r.now()
		if err := r.Store.Save(ctx, st); err != nil {
			return err
		}
		parent.Mirror.ActiveUID = st.OwnerUID
		parent.Mirror.PendingUID = ""
		return r.Store.Save(ctx, parent)
	}
	if st.MirrorRun.Phase == "Active" && parent.Mirror.PendingUID == st.OwnerUID {
		parent.Mirror.ActiveUID = st.OwnerUID
		parent.Mirror.PendingUID = ""
		return r.Store.Save(ctx, parent)
	}
	return nil
}

func validatePlan(plan *state.Plan, volumes []state.MirrorSnapshot, provider string) error {
	for _, o := range plan.Objects {
		if provider == "existing" && o.Namespace == "" {
			return problem("SharedComponentConflict", "Existing-runtime mirrors currently require namespaced resources; use a dedicated runtime for cluster-scoped operators and schemas.")
		}
		if o.Kind == "Job" {
			return problem("JobReplayRequiresAdapter", "One-shot Jobs cannot be replayed automatically by this mirror adapter.")
		}
		if o.Kind == "PersistentVolumeClaim" {
			found := false
			for _, s := range volumes {
				if o.SourceNamespace == s.Namespace && o.SourceName == s.PVCName {
					found = true
				}
			}
			if !found {
				return problem("VolumeDataNotGranted", "Every captured PVC must be listed in the mirror and granted for data capture; empty fallback is disabled.")
			}
		}
		if o.Kind == "StatefulSet" {
			claims, _, _ := unstructured.NestedSlice(o.Desired, "spec", "volumeClaimTemplates")
			replicas, found, _ := unstructured.NestedInt64(o.Desired, "spec", "replicas")
			if !found {
				replicas = 1
			}
			start, _, _ := unstructured.NestedInt64(o.Desired, "spec", "ordinals", "start")
			for _, claim := range claims {
				c := claim.(map[string]any)
				name, _, _ := unstructured.NestedString(c, "metadata", "name")
				for n := start; n < start+replicas; n++ {
					wanted := fmt.Sprintf("%s-%s-%d", name, o.SourceName, n)
					found := false
					for _, s := range volumes {
						if s.Namespace == o.SourceNamespace && s.PVCName == wanted {
							found = true
						}
					}
					if !found {
						return problem("StatefulSetVolumeMissing", "Every current StatefulSet ordinal must have an explicitly granted captured PVC; automatic empty claims are forbidden.")
					}
				}
			}
		}
	}
	return nil
}

func (r *Reconciler) child(ctx context.Context, m *api.ReplicaMirror, scope policy.Resolution, st *state.State, expires time.Time) (*api.ClusterReplica, error) {
	name := shortName("generation", st.OwnerUID, 0)
	child := &api.ClusterReplica{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: name}, child)
	if apierrors.IsNotFound(err) {
		if st.MirrorRun.Child.UID != "" {
			return nil, problem("GenerationMissing", "The recorded candidate generation was deleted; cancel this run and create another.")
		}
		remaining := int(expires.Sub(r.now()) / time.Minute)
		if remaining < 5 {
			return nil, problem("MirrorExpiring", "Insufficient lifetime remains for a generation.")
		}
		child = mirrorTemplate(m)
		child.Name = name
		child.Spec.TTL = fmt.Sprintf("%dm", remaining)
		child.Annotations = map[string]string{RunAnnotation: st.OwnerUID}
		if scope.Provider == "existing" {
			child.Spec.Replication.NamespaceMap = map[string]string{}
			for i, ns := range scope.Namespaces {
				child.Spec.Replication.NamespaceMap[ns] = shortName("guest", st.OwnerUID, i)
			}
		}
		if err := controllerutil.SetControllerReference(m, child, r.Client.Scheme()); err != nil {
			return nil, err
		}
		if err = r.Client.Create(ctx, child); err != nil {
			return nil, problem("GenerationCreateFailed", "Cannot create the independently owned generation request.")
		}
	} else if err != nil {
		return nil, problem("GenerationUnavailable", "Cannot inspect the generation request.")
	}
	if child.Annotations[RunAnnotation] != st.OwnerUID || !target.OwnedBy(child.OwnerReferences, string(m.UID)) || (st.MirrorRun.Child.UID != "" && st.MirrorRun.Child.UID != string(child.UID)) {
		return nil, problem("GenerationOwnershipConflict", "The generation name belongs to another identity.")
	}
	if st.MirrorRun.Child.UID == "" {
		st.MirrorRun.Child = state.MirrorRef{Name: name, UID: string(child.UID)}
		if err := r.Store.Save(ctx, st); err != nil {
			return nil, err
		}
	}
	return child, nil
}

func generationPlan(base *state.Plan, snapshots []state.MirrorSnapshot, imports []state.MirrorImport, child *api.ClusterReplica, provider string) (*state.Plan, error) {
	plan := &state.Plan{CapturedAt: base.CapturedAt, SourceVersion: base.SourceVersion, Packages: base.Packages, Notes: base.Notes}
	for _, original := range base.Objects {
		o := original
		u := (&unstructured.Unstructured{Object: original.Desired}).DeepCopy()
		if provider == "existing" && o.Namespace != "" {
			spec := &api.ReplicationSpec{Data: "EmptyVolumes", NamespaceMap: map[string]string{o.Namespace: policy.Namespace(child.Spec.Replication, o.SourceNamespace)}}
			var err error
			u, err = planner.Transform(u, spec)
			if err != nil {
				return nil, err
			}
		}
		o.Namespace = u.GetNamespace()
		o.ID = planner.ObjectID(u)
		o.Desired = u.Object
		if o.Kind == "PersistentVolumeClaim" {
			for i, s := range snapshots {
				if s.Namespace != o.SourceNamespace || s.PVCName != o.SourceName {
					continue
				}
				if i >= len(imports) {
					return nil, state.ErrIntegrity
				}
				_ = unstructured.SetNestedField(u.Object, map[string]any{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": imports[i].Name}, "spec", "dataSource")
				_ = unstructured.SetNestedField(u.Object, s.StorageClass, "spec", "storageClassName")
				annotations := u.GetAnnotations()
				if annotations == nil {
					annotations = map[string]string{}
				}
				annotations["vcluster.loft.sh/skip-translate"] = "true"
				u.SetAnnotations(annotations)
			}
		}
		if o.Kind == "CronJob" {
			_ = unstructured.SetNestedField(u.Object, true, "spec", "suspend")
		}
		if o.Kind == "StatefulSet" {
			claims, _, _ := unstructured.NestedSlice(u.Object, "spec", "volumeClaimTemplates")
			for _, claim := range claims {
				c := claim.(map[string]any)
				name, _, _ := unstructured.NestedString(c, "metadata", "name")
				for _, s := range snapshots {
					if s.Namespace == o.SourceNamespace && s.PVCName == fmt.Sprintf("%s-%s-0", name, o.SourceName) {
						_ = unstructured.SetNestedField(c, s.StorageClass, "spec", "storageClassName")
						break
					}
				}
			}
			_ = unstructured.SetNestedSlice(u.Object, claims, "spec", "volumeClaimTemplates")
		}
		o.Desired = u.Object
		o.Dependencies = planner.Dependencies(u)
		// Precreate every StatefulSet claim before its controller can create empty data.
		if o.Kind == "StatefulSet" {
			for _, s := range snapshots {
				if s.Namespace == o.SourceNamespace {
					o.Dependencies = append(o.Dependencies, planner.ID("", "PersistentVolumeClaim", o.Namespace, s.PVCName))
				}
			}
		}
		plan.Objects = append(plan.Objects, o)
	}
	if err := planner.Order(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// Prepare is called before the workflow applies application objects. It accepts
// only encrypted controller-created generations, with their still-owned imports.
func (r *Reconciler) Prepare(ctx context.Context, child *api.ClusterReplica, cs *state.State, _ *target.Connection) (bool, error) {
	st, err := r.Store.Load(ctx, cs.MirrorRunUID)
	if err != nil {
		return false, err
	}
	if st == nil || st.MirrorRun == nil || st.MirrorRun.Child.UID != string(child.UID) {
		return false, state.ErrIntegrity
	}
	parent, err := r.Store.Load(ctx, st.MirrorRun.MirrorUID)
	if err != nil {
		return false, err
	}
	if parent == nil || parent.Mirror == nil {
		return false, state.ErrUnavailable
	}
	if parent.Mirror.ActiveUID != st.OwnerUID && parent.Mirror.PendingUID != st.OwnerUID {
		return false, problem("GenerationRetired", "This generation is no longer active or being prepared.")
	}
	if err := r.ensurePolicies(ctx, st, cs); err != nil {
		return false, err
	}
	for _, imp := range st.MirrorRun.Imports {
		s := object("VolumeSnapshot", child.Namespace, imp.Name)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(s), s); err != nil || !owned(s, st.OwnerUID, imp.OperationID, imp.UID) || !ready(s) {
			return false, problem("RestoreSourceUnavailable", "A recorded restore snapshot is unavailable or has changed identity.")
		}
	}
	return true, nil
}
