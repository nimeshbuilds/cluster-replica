package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func (c *cli) chaosCommand() *cobra.Command {
	root := &cobra.Command{Use: "chaos", Short: "Run bounded guest faults and verify their rollback"}
	var file string
	create := &cobra.Command{Use: "create NAME", Short: "Create an immutable ReplicaExperiment from YAML", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(file)
		if err != nil {
			return errors.New("cannot read experiment manifest")
		}
		x, err := experimentManifest(data, args[0], c.Namespace)
		if err != nil {
			return err
		}
		k, _, err := c.clients()
		if err != nil {
			return err
		}
		if err := k.Create(cmd.Context(), x); err != nil {
			return errors.New("cannot create experiment; inspect namespace permissions, CRDs, and grant")
		}
		fmt.Fprintln(cmd.OutOrStdout(), x.Name)
		return nil
	}}
	create.Flags().StringVarP(&file, "file", "f", "", "ReplicaExperiment manifest")
	_ = create.MarkFlagRequired("file")
	root.AddCommand(create)
	for _, action := range []string{"status", "delete"} {
		root.AddCommand(&cobra.Command{Use: action + " NAME", Short: map[string]string{"status": "Show fault phase, deadline, and rollback status", "delete": "Stop the experiment; finalizer waits for verified rollback"}[action], Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			k, _, err := c.clients()
			if err != nil {
				return err
			}
			x := &api.ReplicaExperiment{}
			if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: args[0]}, x); err != nil {
				return errors.New("cannot read experiment")
			}
			if action == "status" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(x.Status)
			}
			if err := k.Delete(cmd.Context(), x, client.Preconditions{UID: &x.UID}); err != nil {
				return errors.New("cannot request experiment cleanup")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Cleanup requested; wait for the ReplicaExperiment to disappear before disabling chaos.")
			return nil
		}})
	}
	return root
}

func experimentManifest(data []byte, name, namespace string) (*api.ReplicaExperiment, error) {
	x := &api.ReplicaExperiment{}
	if err := yaml.UnmarshalStrict(data, x); err != nil {
		return nil, errors.New("invalid ReplicaExperiment manifest")
	}
	if x.APIVersion != api.GroupVersion.String() || x.Kind != "ReplicaExperiment" {
		return nil, errors.New("use a replica.nimeshbuilds.dev/v1alpha1 ReplicaExperiment manifest")
	}
	if x.Name != "" && x.Name != name || x.Namespace != "" && x.Namespace != namespace {
		return nil, errors.New("manifest name/namespace must match the selected CLI destination")
	}
	x.ObjectMeta = metav1.ObjectMeta{Name: name, Namespace: namespace}
	x.Status = api.ReplicaExperimentStatus{}
	return x, nil
}
