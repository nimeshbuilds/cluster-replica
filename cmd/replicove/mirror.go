package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func (c *cli) mirrorCommand() *cobra.Command {
	root := &cobra.Command{Use: "mirror", Short: "Create writable workload copies and reset them from host snapshots"}
	var file string
	create := &cobra.Command{Use: "create NAME", Short: "Create a mirror from a ReplicaMirror YAML manifest", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(file)
		if err != nil {
			return errors.New("cannot read mirror manifest")
		}
		m := &api.ReplicaMirror{}
		if err := yaml.UnmarshalStrict(data, m); err != nil {
			return errors.New("invalid ReplicaMirror manifest")
		}
		if m.APIVersion != api.GroupVersion.String() || m.Kind != "ReplicaMirror" {
			return errors.New("use a replica.nimeshbuilds.dev/v1alpha1 ReplicaMirror manifest")
		}
		if m.Name != "" && m.Name != args[0] || m.Namespace != "" && m.Namespace != c.Namespace {
			return errors.New("manifest name/namespace must match the selected CLI destination")
		}
		m.ObjectMeta = metav1.ObjectMeta{Name: args[0], Namespace: c.Namespace}
		m.Status = api.ReplicaMirrorStatus{}
		k, _, err := c.clients()
		if err != nil {
			return err
		}
		if err := k.Create(cmd.Context(), m); err != nil {
			return errors.New("cannot create mirror; inspect the API schema, grant, and namespace permissions")
		}
		fmt.Fprintln(cmd.OutOrStdout(), m.Name)
		return nil
	}}
	create.Flags().StringVarP(&file, "file", "f", "", "ReplicaMirror manifest")
	_ = create.MarkFlagRequired("file")
	root.AddCommand(create)
	for _, action := range []string{"sync", "reset"} {
		var revision, runName string
		var force bool
		cmd := &cobra.Command{Use: action + " NAME", Short: map[string]string{"sync": "Capture latest host data and prepare a replacement", "reset": "Rebuild from a retained capture revision"}[action], Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			k, _, err := c.clients()
			if err != nil {
				return err
			}
			m := &api.ReplicaMirror{}
			if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: args[0]}, m); err != nil {
				return errors.New("cannot read mirror")
			}
			run := &api.ReplicaMirrorRun{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, GenerateName: m.Name + "-"}, Spec: api.ReplicaMirrorRunSpec{MirrorRef: api.MirrorObjectRef{Name: m.Name, UID: string(m.UID)}, Action: "Sync", Force: force}}
			if action == "reset" {
				prior := &api.ReplicaMirrorRun{}
				if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: revision}, prior); err != nil {
					return errors.New("cannot read retained revision")
				}
				if prior.Spec.MirrorRef.UID != string(m.UID) {
					return errors.New("revision belongs to another mirror")
				}
				run.Spec.Action = "Reset"
				run.Spec.RevisionRef = &api.MirrorObjectRef{Name: prior.Name, UID: string(prior.UID)}
			}
			if runName != "" {
				run.Name = runName
				run.GenerateName = ""
			}
			if err := k.Create(cmd.Context(), run); err != nil {
				if !apierrors.IsAlreadyExists(err) || runName == "" {
					return errors.New("cannot create mirror run")
				}
				existing := &api.ReplicaMirrorRun{}
				if err := k.Get(cmd.Context(), client.ObjectKeyFromObject(run), existing); err != nil || !reflect.DeepEqual(existing.Spec, run.Spec) {
					return errors.New("run name already exists with a different request")
				}
				run = existing
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Mirror run: %s/%s\n", run.Namespace, run.Name)
			return nil
		}}
		cmd.Flags().StringVar(&runName, "run-name", "", "Explicit unique request name; retries with the same request are idempotent")
		cmd.Flags().BoolVar(&force, "force", false, "Bypass the test lease only if the administrator grant permits it")
		if action == "reset" {
			cmd.Flags().StringVar(&revision, "revision", "", "Retained Sync run name")
			_ = cmd.MarkFlagRequired("revision")
		}
		root.AddCommand(cmd)
	}
	var hold time.Duration
	for _, action := range []string{"status", "revisions", "suspend", "resume", "hold", "release", "delete"} {
		cmd := &cobra.Command{Use: action + " NAME", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			k, _, err := c.clients()
			if err != nil {
				return err
			}
			m := &api.ReplicaMirror{}
			if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: args[0]}, m); err != nil {
				return errors.New("cannot read mirror")
			}
			before := m.DeepCopy()
			switch action {
			case "status":
				return json.NewEncoder(cmd.OutOrStdout()).Encode(m.Status)
			case "revisions":
				list := &api.ReplicaMirrorRunList{}
				if err := k.List(cmd.Context(), list, client.InNamespace(c.Namespace)); err != nil {
					return errors.New("cannot list mirror runs")
				}
				out := []map[string]any{}
				for _, run := range list.Items {
					if run.Spec.MirrorRef.UID == string(m.UID) {
						out = append(out, map[string]any{"name": run.Name, "uid": run.UID, "action": run.Spec.Action, "status": run.Status})
					}
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			case "suspend":
				m.Spec.Suspend = true
			case "resume":
				m.Spec.Suspend = false
			case "hold":
				if hold <= 0 || hold > 30*time.Minute {
					return errors.New("test lease duration must be greater than zero and at most 30m")
				}
				m.Spec.HoldUntil = &metav1.Time{Time: time.Now().UTC().Add(hold)}
			case "release":
				m.Spec.HoldUntil = nil
			case "delete":
				return k.Delete(cmd.Context(), m, client.Preconditions{UID: &m.UID})
			}
			if err := k.Patch(cmd.Context(), m, client.MergeFrom(before)); err != nil {
				return errors.New("cannot update mirror controls")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Mirror controls updated")
			return nil
		}}
		if action == "hold" {
			cmd.Flags().DurationVar(&hold, "duration", 15*time.Minute, "Pin the active test generation for at most 30m")
		}
		root.AddCommand(cmd)
	}
	root.AddCommand(&cobra.Command{Use: "cancel RUN", Short: "Cancel a candidate or remove an unused revision; active revisions remain protected", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		k, _, err := c.clients()
		if err != nil {
			return err
		}
		run := &api.ReplicaMirrorRun{}
		if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: args[0]}, run); err != nil {
			return errors.New("cannot read mirror run")
		}
		return k.Delete(cmd.Context(), run, client.Preconditions{UID: &run.UID})
	}})
	for _, tunnel := range []bool{false, true} {
		cmd := c.accessCommand(tunnel)
		original := cmd.RunE
		cmd.Short = "Access the current mirror generation with expiring credentials"
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			k, _, err := c.clients()
			if err != nil {
				return err
			}
			m := &api.ReplicaMirror{}
			if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: args[0]}, m); err != nil || m.Status.ActiveReplica == nil {
				return errors.New("mirror has no active generation")
			}
			child := &api.ClusterReplica{}
			if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: m.Status.ActiveReplica.Name}, child); err != nil || string(child.UID) != m.Status.ActiveReplica.UID {
				return errors.New("active generation identity is unavailable")
			}
			return original(cmd, []string{child.Name})
		}
		root.AddCommand(cmd)
	}
	return root
}
