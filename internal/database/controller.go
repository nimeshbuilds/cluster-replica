package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Controller struct {
	Client client.Client
	Store  *state.Store
	Runner Runner
	Now    func() time.Time
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Prepare must run before any application desired state and before issuing access.
// Failure is terminal for the capture; no source data is silently re-captured.
func (c *Controller) Prepare(ctx context.Context, obj *api.ClusterReplica, grant *api.ReplicaGrant, st *state.State, conn *target.Connection) (bool, error) {
	if obj.Spec.Replication == nil || len(obj.Spec.Replication.Databases) == 0 {
		return true, nil
	}
	copies := obj.Spec.Replication.Databases
	if err := ValidatePlan(copies, st); err != nil {
		return false, err
	}
	for _, copy := range copies {
		g, err := Resolve(copy, grant.Spec.Databases, c.Store.Namespace)
		if err != nil {
			return false, err
		}
		if !slices.Contains(grant.Spec.SourceNamespaces, g.SourceNamespace) {
			return false, ErrDenied
		}
		i := -1
		for j := range st.Databases {
			if st.Databases[j].Name == copy.Name && st.Databases[j].Namespace == copy.Namespace {
				i = j
				break
			}
		}
		if i < 0 {
			salt, err := random()
			if err != nil {
				return false, err
			}
			st.Databases = append(st.Databases, state.Database{Name: copy.Name, Namespace: copy.Namespace, Grant: copy.Grant, Phase: "Preparing", Salt: salt, PlanRevision: st.Plan.Revision})
			i = len(st.Databases) - 1
			if err := c.Store.Save(ctx, st); err != nil {
				return false, err
			}
		}
		d := &st.Databases[i]
		if d.Grant != copy.Grant || d.PlanRevision != st.Plan.Revision {
			return false, ErrDenied
		}
		if err := c.ensureApplicationIsolation(ctx, st); err != nil {
			return false, err
		}
		if d.Phase == "Copying" {
			d.Phase = "Failed"
			if err := c.Store.Save(ctx, st); err != nil {
				return false, err
			}
		}
		if d.Phase == "Failed" {
			if _, err := c.remove(ctx, st, d, conn, true); err != nil {
				return false, err
			}
			return false, ErrFailed
		}
		if d.Phase == "Ready" {
			if err := c.hostPolicies(ctx, st, d, true); err != nil {
				return false, err
			}
			if err := c.verify(ctx, d, conn); err != nil {
				return false, err
			}
			continue
		}
		if d.Phase == "Preparing" {
			storage := &storagev1.StorageClass{}
			if err := c.Client.Get(ctx, client.ObjectKey{Name: g.StorageClass}, storage); err != nil || storage.Provisioner == "kubernetes.io/no-provisioner" || storage.ReclaimPolicy == nil || *storage.ReclaimPolicy != corev1.PersistentVolumeReclaimDelete {
				return false, ErrDenied
			}
			if err := c.hostPolicies(ctx, st, d, false); err != nil {
				return false, err
			}
			if err := c.resources(ctx, st, d, g, conn); err != nil {
				return false, err
			}
			for _, name := range []string{base(d) + "-stage", base(d)} {
				p, err := conn.Kubernetes.CoreV1().Pods(d.Namespace).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return false, ErrFailed
				}
				if !podReady(p) {
					return false, nil
				}
			}
			if err := c.verifyHostPods(ctx, st, d); err != nil {
				return false, err
			}
			secret := &corev1.Secret{}
			if err := c.Client.Get(ctx, client.ObjectKey{Namespace: g.CredentialsSecret.Namespace, Name: g.CredentialsSecret.Name}, secret); err != nil {
				return false, ErrDenied
			}
			user, password := string(secret.Data["username"]), string(secret.Data["password"])
			if !identifier.MatchString(user) || len(password) == 0 || len(password) > 4096 || strings.ContainsRune(password, 0) {
				return false, ErrDenied
			}
			d.Phase = "Copying"
			d.CapturedAt = c.now()
			if err := c.Store.Save(ctx, st); err != nil {
				return false, err
			}
			runner := c.Runner
			if runner == nil {
				runner = PostgreSQL{}
			}
			deadline := c.now().Add(time.Duration(g.TimeoutSeconds) * time.Second)
			if obj.Status.ExpiresAt != nil && obj.Status.ExpiresAt.Time.Before(deadline) {
				deadline = obj.Status.ExpiresAt.Time
			}
			opctx, cancel := context.WithDeadline(ctx, deadline)
			stage := Destination{Connection: conn, Namespace: d.Namespace, Pod: base(d) + "-stage", Database: g.Database}
			final := stage
			final.Pod = base(d)
			d.ArchiveSHA256, err = runner.Capture(opctx, Source{Grant: g, Username: user, Password: password}, stage)
			if err == nil {
				var sql string
				sql, err = MaskSQL(g, d.Salt)
				if err == nil {
					err = runner.SQL(opctx, stage, sql)
				}
			}
			if err == nil {
				d.SanitizedSHA256, err = runner.Copy(opctx, stage, final, g.MaxBytes)
			}
			cancel()
			if err != nil {
				d.Phase = "Failed"
				_ = c.Store.Save(ctx, st)
				_, _ = c.remove(ctx, st, d, conn, true)
				return false, ErrFailed
			}
			d.Phase = "Sanitized"
			d.VerifiedAt = c.now()
			d.Salt = ""
			if err := c.Store.Save(ctx, st); err != nil {
				return false, err
			}
		}
		if d.Phase == "Sanitized" {
			done, err := c.remove(ctx, st, d, conn, true)
			if err != nil || !done {
				return false, err
			}
			if done, err := c.hostPodsGone(ctx, st, d, true); err != nil || !done {
				return false, err
			}
			runner := c.Runner
			if runner == nil {
				runner = PostgreSQL{}
			}
			opctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = runner.SQL(opctx, Destination{Connection: conn, Namespace: d.Namespace, Pod: base(d), Database: g.Database}, OpenSQL)
			cancel()
			if err != nil {
				return false, ErrFailed
			}
			if err := c.publish(ctx, st, d, conn); err != nil {
				return false, err
			}
			d.Phase = "Ready"
			if err := c.Store.Save(ctx, st); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// Cleanup is retried until every UID-pinned data resource is absent. Call before
// the workflow removes owned namespaces/runtime and before deleting protected state.
func (c *Controller) Cleanup(ctx context.Context, st *state.State, conn *target.Connection) (bool, error) {
	for i := range st.Databases {
		done, err := c.remove(ctx, st, &st.Databases[i], conn, false)
		if err != nil || !done {
			return false, err
		}
		if done, err := c.hostPodsGone(ctx, st, &st.Databases[i], false); err != nil || !done {
			return false, err
		}
		if done, err := c.cleanupHostPolicies(ctx, st, &st.Databases[i]); err != nil || !done {
			return false, err
		}
	}
	return true, nil
}

func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", ErrFailed
	}
	return hex.EncodeToString(b), nil
}
func base(d *state.Database) string { return "replicove-db-" + d.Name }
func labels(d *state.Database, stage bool) map[string]string {
	role := "final"
	if stage {
		role = "stage"
	}
	return map[string]string{"replicove.nimeshbuilds.dev/database": d.Name, "replicove.nimeshbuilds.dev/database-role": role}
}

func (c *Controller) resources(ctx context.Context, st *state.State, d *state.Database, g api.DatabaseGrant, conn *target.Connection) error {
	password, err := random()
	if err != nil {
		return err
	}
	secret := &corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}, Data: map[string][]byte{"host": []byte(d.Name + "." + d.Namespace + ".svc"), "port": []byte("5432"), "database": []byte(g.Database), "username": []byte("replicove"), "password": []byte(password)}}
	if err := c.ensure(ctx, st, d, conn, secret, "secrets", false); err != nil {
		return err
	}
	size := resource.NewQuantity(g.MaxBytes, resource.BinarySI)
	pvc := &corev1.PersistentVolumeClaim{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, ObjectMeta: metav1.ObjectMeta{Name: base(d), Namespace: d.Namespace}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, StorageClassName: &g.StorageClass, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: *size}}}}
	if err := c.ensure(ctx, st, d, conn, pvc, "persistentvolumeclaims", false); err != nil {
		return err
	}
	for _, stage := range []bool{true, false} {
		name := base(d)
		if stage {
			name += "-stage"
		}
		uid := int64(999)
		no := false
		yes := true
		mode := corev1.SeccompProfileTypeRuntimeDefault
		volume := corev1.Volume{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: base(d)}}}
		if stage {
			volume.VolumeSource = corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: size}}
		}
		pod := &corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: d.Namespace, Labels: labels(d, stage)}, Spec: corev1.PodSpec{AutomountServiceAccountToken: &no, EnableServiceLinks: &no, SecurityContext: &corev1.PodSecurityContext{RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, RunAsNonRoot: &yes, SeccompProfile: &corev1.SeccompProfile{Type: mode}}, Volumes: []corev1.Volume{volume, {Name: "socket", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}, Containers: []corev1.Container{{Name: "postgres", Image: g.Image, Args: []string{"postgres", "-c", "log_min_messages=panic", "-c", "log_min_error_statement=panic", "-c", "log_statement=none", "-c", "log_connections=off", "-c", "log_disconnections=off"}, Env: []corev1.EnvVar{{Name: "PGDATA", Value: "/var/lib/postgresql/data/pgdata"}, {Name: "POSTGRES_USER", Value: "replicove"}, {Name: "POSTGRES_DB", Value: g.Database}, {Name: "POSTGRES_HOST_AUTH_METHOD", Value: "reject"}, {Name: "POSTGRES_INITDB_ARGS", Value: "--auth-host=reject --auth-local=trust"}, {Name: "POSTGRES_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: d.Name}, Key: "password"}}}}, SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &no, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}, VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/var/lib/postgresql/data"}, {Name: "socket", MountPath: "/var/run/postgresql"}}, ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"pg_isready", "-h", "127.0.0.1", "-U", "replicove", "-d", g.Database}}}, PeriodSeconds: 2}, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: *resource.NewQuantity(g.MaxBytes+(256<<20), resource.BinarySI)}}}}}}
		if err := c.ensure(ctx, st, d, conn, pod, "pods", stage); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) publish(ctx context.Context, st *state.State, d *state.Database, conn *target.Connection) error {
	if err := c.hostPolicies(ctx, st, d, true); err != nil {
		return err
	}
	svc := &corev1.Service{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"}, ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}, Spec: corev1.ServiceSpec{Selector: labels(d, false), Ports: []corev1.ServicePort{{Name: "postgres", Port: 5432, TargetPort: intstr.FromInt32(5432)}}}}
	return c.ensure(ctx, st, d, conn, svc, "services", false)
}
func ptr[T any](v T) *T { return &v }

func owned(obj metav1.Object, d *state.Database, owner string) bool {
	for _, e := range d.Entries {
		if e.Name == obj.GetName() && e.Namespace == obj.GetNamespace() && !e.Deleted && e.UID == string(obj.GetUID()) && obj.GetAnnotations()[planner.OwnerAnnotation] == owner && obj.GetAnnotations()[planner.OperationAnnotation] == e.OperationID {
			return true
		}
	}
	return false
}

func (c *Controller) ensure(ctx context.Context, st *state.State, d *state.Database, conn *target.Connection, obj runtime.Object, plural string, stage bool) error {
	value, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return ErrFailed
	}
	wanted := &unstructured.Unstructured{Object: value}
	gvr := schema.FromAPIVersionAndKind(wanted.GetAPIVersion(), wanted.GetKind()).GroupVersion().WithResource(plural)
	r := conn.Dynamic.Resource(gvr).Namespace(d.Namespace)
	i := -1
	for j := range d.Entries {
		if d.Entries[j].Resource == plural && d.Entries[j].Name == wanted.GetName() && !d.Entries[j].Deleted {
			i = j
			break
		}
	}
	live, err := r.Get(ctx, wanted.GetName(), metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return ErrFailed
	}
	if i < 0 {
		if err == nil {
			return ErrOwnership
		}
		op, err := state.OperationID()
		if err != nil {
			return err
		}
		kind := "database"
		if stage {
			kind = "database-stage"
		}
		d.Entries = append(d.Entries, state.Entry{ID: "database/" + plural + "/" + wanted.GetName(), Type: kind, APIVersion: wanted.GetAPIVersion(), Kind: wanted.GetKind(), Resource: plural, Namespace: d.Namespace, Name: wanted.GetName(), OperationID: op})
		i = len(d.Entries) - 1
		if err := c.Store.Save(ctx, st); err != nil {
			return err
		}
	}
	e := &d.Entries[i]
	if apierrors.IsNotFound(err) {
		if e.UID != "" {
			return ErrOwnership
		}
		wanted.SetAnnotations(map[string]string{planner.OwnerAnnotation: st.OwnerUID, planner.OperationAnnotation: e.OperationID})
		live, err = r.Create(ctx, wanted, metav1.CreateOptions{})
		if err != nil {
			return ErrFailed
		}
	}
	if (e.UID != "" && e.UID != string(live.GetUID())) || live.GetAnnotations()[planner.OwnerAnnotation] != st.OwnerUID || live.GetAnnotations()[planner.OperationAnnotation] != e.OperationID {
		return ErrOwnership
	}
	if e.UID == "" {
		e.UID = string(live.GetUID())
		return c.Store.Save(ctx, st)
	}
	return nil
}

func (c *Controller) remove(ctx context.Context, st *state.State, d *state.Database, conn *target.Connection, stageOnly bool) (bool, error) {
	for i := len(d.Entries) - 1; i >= 0; i-- {
		e := &d.Entries[i]
		if e.Deleted || stageOnly && e.Type != "database-stage" {
			continue
		}
		gv, err := schema.ParseGroupVersion(e.APIVersion)
		if err != nil {
			return false, ErrOwnership
		}
		r := conn.Dynamic.Resource(gv.WithResource(e.Resource)).Namespace(e.Namespace)
		live, err := r.Get(ctx, e.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			e.Deleted = true
			if err := c.Store.Save(ctx, st); err != nil {
				return false, err
			}
			continue
		}
		if err != nil {
			return false, ErrFailed
		}
		if (e.UID != "" && e.UID != string(live.GetUID())) || live.GetAnnotations()[planner.OwnerAnnotation] != st.OwnerUID || live.GetAnnotations()[planner.OperationAnnotation] != e.OperationID {
			return false, ErrOwnership
		}
		uid := live.GetUID()
		rv := live.GetResourceVersion()
		if err := r.Delete(ctx, e.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}); err != nil && !apierrors.IsNotFound(err) {
			return false, ErrFailed
		}
		return false, nil
	}
	return true, nil
}
func (c *Controller) verify(ctx context.Context, d *state.Database, conn *target.Connection) error {
	for _, e := range d.Entries {
		if e.Deleted {
			continue
		}
		gv, err := schema.ParseGroupVersion(e.APIVersion)
		if err != nil {
			return ErrOwnership
		}
		o, err := conn.Dynamic.Resource(gv.WithResource(e.Resource)).Namespace(e.Namespace).Get(ctx, e.Name, metav1.GetOptions{})
		if err != nil || types.UID(e.UID) != o.GetUID() {
			return ErrOwnership
		}
	}
	return nil
}
