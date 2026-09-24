package chaos

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var imageDigest = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[a-f0-9]{64}$`)
var kinds = []string{"PodDelete", "ScaleZero", "NetworkIsolation", "CPUStress", "MemoryStress", "CustomJob"}

func duration(x *api.ReplicaExperiment) int64 {
	if x.Spec.DurationSeconds == 0 {
		return 30
	}
	return x.Spec.DurationSeconds
}
func jobFault(kind string) bool {
	return kind == "CPUStress" || kind == "MemoryStress" || kind == "CustomJob"
}
func resources(f api.ChaosFault) (int64, int64) {
	cpu, mem := f.CPUMilli, f.MemoryMiB
	if cpu == 0 {
		cpu = 100
	}
	if mem == 0 {
		mem = 32
	}
	return cpu, mem
}
func validate(x *api.ReplicaExperiment, g *api.ChaosGrant, enforced bool) error {
	if g == nil {
		return errors.New("The administrator grant does not enable chaos experiments.")
	}
	if len(x.Spec.Faults) < 1 || len(x.Spec.Faults) > 8 {
		return errors.New("Select between one and eight faults.")
	}
	max := g.MaxDurationSeconds
	if max == 0 {
		max = 60
	}
	if duration(x) < 1 || duration(x) > 900 || max < 1 || max > 900 || duration(x) > max {
		return errors.New("Experiment duration exceeds the bounded administrator grant.")
	}
	maxCPU, maxMem := g.MaxCPUMilli, g.MaxMemoryMiB
	if maxCPU == 0 {
		maxCPU = 500
	}
	if maxMem == 0 {
		maxMem = 128
	}
	if maxCPU < 1 || maxCPU > 2000 || maxMem < 16 || maxMem > 1024 {
		return errors.New("The administrator resource budget is invalid.")
	}
	var cpu, mem int64
	seen := map[string]bool{}
	for _, f := range x.Spec.Faults {
		if !slices.Contains(kinds, f.Kind) || !slices.Contains(g.Kinds, f.Kind) || !slices.Contains(g.Namespaces, f.Namespace) || f.Namespace == "default" || f.Namespace == "kube-system" || f.Namespace == "kube-public" || f.Namespace == "kube-node-lease" {
			return errors.New("Fault kind or guest namespace is not explicitly granted.")
		}
		key := f.Kind + "/" + f.Namespace
		if f.Target != nil {
			key += "/" + f.Target.Kind + "/" + f.Target.Name
		}
		if seen[key] {
			return errors.New("Duplicate faults against the same target are not supported.")
		}
		seen[key] = true
		if f.Kind == "PodDelete" || f.Kind == "ScaleZero" {
			if f.Target == nil || f.Target.Name == "" || f.Target.UID == "" {
				return errors.New("Destructive faults require the exact current guest object name and UID.")
			}
			if f.Kind == "PodDelete" && f.Target.Kind != "Pod" || f.Kind == "ScaleZero" && f.Target.Kind != "Deployment" && f.Target.Kind != "StatefulSet" {
				return errors.New("The fault does not support the selected target kind.")
			}
		} else if f.Target != nil {
			return errors.New("This fault does not accept a target object.")
		}
		if jobFault(f.Kind) {
			if !enforced {
				return errors.New("Job faults require administrator-qualified NetworkPolicy enforcement.")
			}
			if !imageDigest.MatchString(f.Image) || !slices.Contains(g.Images, f.Image) {
				return errors.New("Job images must exactly match an administrator-approved immutable image digest.")
			}
			c, m := resources(f)
			if c < 1 || c > 2000 || m < 16 || m > 1024 {
				return errors.New("Invalid Job resource limits.")
			}
			cpu += c
			mem += m
			if f.Kind == "CustomJob" {
				if len(f.Command) == 0 || len(f.Command) > 32 {
					return errors.New("CustomJob requires an explicit command of at most 32 arguments.")
				}
				for _, s := range f.Command {
					if len(s) > 4096 {
						return errors.New("A custom command argument exceeds 4096 bytes.")
					}
				}
			} else if len(f.Command) != 0 {
				return errors.New("Built-in stress commands cannot be overridden; use CustomJob.")
			}
		} else if f.Image != "" || len(f.Command) > 0 || f.CPUMilli != 0 || f.MemoryMiB != 0 {
			return errors.New("Image, command, and resource fields are only valid for Job faults.")
		}
		if f.Kind == "NetworkIsolation" && !enforced {
			return errors.New("NetworkIsolation requires administrator-qualified NetworkPolicy enforcement.")
		}
	}
	if cpu > maxCPU || mem > maxMem {
		return errors.New("Combined Job resources exceed the administrator experiment budget.")
	}
	return nil
}

func apiResource(conn *target.Connection, kind, ns string) dynamic.ResourceInterface {
	gvr := schema.GroupVersionResource{Version: "v1"}
	switch kind {
	case "Pod":
		gvr.Resource = "pods"
	case "ServiceAccount":
		gvr.Resource = "serviceaccounts"
	case "Deployment":
		gvr.Group = "apps"
		gvr.Resource = "deployments"
	case "StatefulSet":
		gvr.Group = "apps"
		gvr.Resource = "statefulsets"
	case "ReplicaSet":
		gvr.Group = "apps"
		gvr.Resource = "replicasets"
	case "NetworkPolicy":
		gvr.Group = "networking.k8s.io"
		gvr.Resource = "networkpolicies"
	case "Job":
		gvr.Group = "batch"
		gvr.Resource = "jobs"
	}
	return conn.Dynamic.Resource(gvr).Namespace(ns)
}

func (r *Reconciler) prepare(ctx context.Context, x *api.ReplicaExperiment, parent, st *state.State, conn *target.Connection) error {
	namespaces := map[string]string{}
	for _, f := range x.Spec.Faults {
		if _, ok := namespaces[f.Namespace]; ok {
			continue
		}
		var entry *state.Entry
		for i := range parent.Entries {
			e := &parent.Entries[i]
			if !e.Deleted && e.Kind == "Namespace" && e.Name == f.Namespace && e.UID != "" {
				entry = e
				break
			}
		}
		if entry == nil {
			return errors.New("Experiments require namespaces exclusively created and owned by this replica.")
		}
		ns, err := conn.Kubernetes.CoreV1().Namespaces().Get(ctx, f.Namespace, metav1.GetOptions{})
		if err != nil || string(ns.UID) != entry.UID || ns.Annotations[planner.OwnerAnnotation] != parent.OwnerUID || ns.Annotations[planner.OperationAnnotation] != entry.OperationID {
			return errors.New("Guest namespace UID or replica ownership does not match protected inventory.")
		}
		namespaces[f.Namespace] = entry.UID
	}
	isolation := map[string]bool{}
	for i, f := range x.Spec.Faults {
		if f.Kind == "NetworkIsolation" || jobFault(f.Kind) {
			if parent.Provider != "helm" || st.RuntimeName == "" {
				return errors.New("Network and Job faults require the qualified managed Helm vCluster runtime.")
			}
			if err := r.verifyHostPolicies(ctx, st); err != nil {
				return err
			}
			if !isolation[f.Namespace] {
				name := fmt.Sprintf("chaos-%s-%d-net", state.Name(st.OwnerUID)[16:28], i)
				selector := map[string]any{"matchLabels": map[string]any{"vcluster.loft.sh/managed-by": st.RuntimeName, "vcluster.loft.sh/namespace": f.Namespace}}
				o := map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]any{"name": name, "namespace": st.OwnerNamespace}, "spec": map[string]any{"podSelector": selector, "policyTypes": []any{"Ingress", "Egress"}, "ingress": []any{}, "egress": []any{}}}
				if err := appendAction(st, "HostNetworkIsolation", f.Namespace, namespaces[f.Namespace], "NetworkPolicy", name, "", nil, o); err != nil {
					return err
				}
				isolation[f.Namespace] = true
			}
		}
		if jobFault(f.Kind) {
			name := fmt.Sprintf("chaos-%s-%d", state.Name(st.OwnerUID)[16:28], i)
			sa := map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": name, "namespace": f.Namespace}, "automountServiceAccountToken": false}
			if err := appendAction(st, "Create", f.Namespace, namespaces[f.Namespace], "ServiceAccount", name, "", nil, sa); err != nil {
				return err
			}
			if err := appendAction(st, "Create", f.Namespace, namespaces[f.Namespace], "Job", name, "", nil, job(x, f, name)); err != nil {
				return err
			}
			continue
		}
		if f.Kind == "NetworkIsolation" {
			continue
		}
		live, err := apiResource(conn, f.Target.Kind, f.Namespace).Get(ctx, f.Target.Name, metav1.GetOptions{})
		if err != nil || string(live.GetUID()) != f.Target.UID || live.GetDeletionTimestamp() != nil {
			return errors.New("The exact guest fault target is unavailable or changed UID.")
		}
		if f.Kind == "PodDelete" {
			if err := ownedPod(ctx, conn, parent, live); err != nil {
				return err
			}
			if err := appendAction(st, f.Kind, f.Namespace, namespaces[f.Namespace], f.Target.Kind, f.Target.Name, f.Target.UID, nil, nil); err != nil {
				return err
			}
			continue
		}
		if !ownedTarget(parent, live) {
			return errors.New("Scale targets must match protected replica object ownership and UID.")
		}
		if live.GetAnnotations()[marker] != "" {
			return errors.New("Another fault already controls the target workload.")
		}
		original, found, err := unstructured.NestedInt64(live.Object, "spec", "replicas")
		if err != nil {
			return errors.New("Cannot read workload replicas.")
		}
		if !found {
			original = 1
		}
		identity := map[string]any{"replicaOperation": live.GetAnnotations()[planner.OperationAnnotation]}
		if err := appendAction(st, f.Kind, f.Namespace, namespaces[f.Namespace], f.Target.Kind, f.Target.Name, f.Target.UID, &original, identity); err != nil {
			return err
		}
	}
	return nil
}

func appendAction(st *state.State, kind, ns, nsUID, targetKind, name, uid string, original *int64, obj map[string]any) error {
	op, err := state.OperationID()
	if err != nil {
		return err
	}
	st.Experiment.Actions = append(st.Experiment.Actions, state.ChaosAction{Kind: kind, Namespace: ns, NamespaceUID: nsUID, TargetKind: targetKind, Name: name, UID: uid, OperationID: op, OriginalReplicas: original, Object: obj})
	return nil
}
func ownedTarget(parent *state.State, live *unstructured.Unstructured) bool {
	for _, e := range parent.Entries {
		if !e.Deleted && e.UID != "" && e.UID == string(live.GetUID()) && e.Kind == live.GetKind() && e.Namespace == live.GetNamespace() && e.Name == live.GetName() && live.GetAnnotations()[planner.OwnerAnnotation] == parent.OwnerUID && live.GetAnnotations()[planner.OperationAnnotation] == e.OperationID {
			return true
		}
	}
	return false
}
func ownedPod(ctx context.Context, conn *target.Connection, parent *state.State, pod *unstructured.Unstructured) error {
	// Only controller-managed Pods are eligible; deleting a naked Pod has no recovery controller.
	for _, ref := range pod.GetOwnerReferences() {
		if ref.Controller == nil || !*ref.Controller || ref.APIVersion != "apps/v1" || (ref.Kind != "ReplicaSet" && ref.Kind != "StatefulSet") {
			continue
		}
		work, err := apiResource(conn, ref.Kind, pod.GetNamespace()).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil || work.GetUID() != ref.UID {
			continue
		}
		if ref.Kind == "StatefulSet" && ownedTarget(parent, work) {
			return nil
		}
		for _, up := range work.GetOwnerReferences() {
			if up.Controller != nil && *up.Controller && up.Kind == "Deployment" && up.APIVersion == "apps/v1" {
				d, err := apiResource(conn, "Deployment", pod.GetNamespace()).Get(ctx, up.Name, metav1.GetOptions{})
				if err == nil && d.GetUID() == up.UID && ownedTarget(parent, d) {
					return nil
				}
			}
		}
	}
	return errors.New("PodDelete requires a Pod controlled by an inventoried Deployment or StatefulSet.")
}
func (r *Reconciler) verifyNamespace(ctx context.Context, conn *target.Connection, a *state.ChaosAction) error {
	current, err := namespaceCurrent(ctx, conn, a)
	if err != nil {
		return err
	}
	if !current {
		return errors.New("Guest namespace was deleted or replaced; no foreign namespace will be mutated.")
	}
	return nil
}
func namespaceCurrent(ctx context.Context, conn *target.Connection, a *state.ChaosAction) (bool, error) {
	ns, err := conn.Kubernetes.CoreV1().Namespaces().Get(ctx, a.Namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("Cannot verify guest namespace identity during cleanup.")
	}
	return string(ns.UID) == a.NamespaceUID, nil
}

func job(x *api.ReplicaExperiment, f api.ChaosFault, name string) map[string]any {
	cpu, mem := resources(f)
	cmd := append([]string(nil), f.Command...)
	switch f.Kind {
	case "CPUStress":
		cmd = []string{"python3", "-c", "while True: pass"}
	case "MemoryStress":
		cmd = []string{"python3", "-c", fmt.Sprintf("import time\ndata=bytearray(%d)\nfor i in range(0,len(data),4096): data[i]=1\ntime.sleep(%d)", mem*1024*1024/2, duration(x)+1)}
	}
	command := make([]any, len(cmd))
	for i, s := range cmd {
		command[i] = s
	}
	return map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": name, "namespace": f.Namespace}, "spec": map[string]any{
		"backoffLimit": int64(0), "activeDeadlineSeconds": duration(x), "template": map[string]any{"metadata": map[string]any{"labels": map[string]any{owner: string(x.UID)}}, "spec": map[string]any{
			"serviceAccountName": name, "automountServiceAccountToken": false, "restartPolicy": "Never", "terminationGracePeriodSeconds": int64(1),
			"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": int64(65532), "runAsGroup": int64(65532), "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
			"containers":      []any{map[string]any{"name": "fault", "image": f.Image, "imagePullPolicy": "IfNotPresent", "command": command, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []any{"ALL"}}}, "resources": map[string]any{"requests": map[string]any{"cpu": fmt.Sprintf("%dm", cpu), "memory": fmt.Sprintf("%dMi", mem)}, "limits": map[string]any{"cpu": fmt.Sprintf("%dm", cpu), "memory": fmt.Sprintf("%dMi", mem)}}}},
		}},
	}}
}
