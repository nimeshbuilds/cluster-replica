package main

import (
	"errors"
	"fmt"
	"os"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/database"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func (c *cli) databaseCommand() *cobra.Command {
	root := &cobra.Command{Use: "database", Short: "Validate explicitly granted PostgreSQL copy policies"}
	var grantFile, replicationFile, protectedNamespace string
	validate := &cobra.Command{Use: "validate", Short: "Check grant and masking rules locally without connecting to a database", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		gb, err := os.ReadFile(grantFile)
		if err != nil {
			return errors.New("cannot read database grant manifest")
		}
		rb, err := os.ReadFile(replicationFile)
		if err != nil {
			return errors.New("cannot read replication spec")
		}
		g := &api.ReplicaGrant{}
		r := &api.ReplicationSpec{}
		if yaml.UnmarshalStrict(gb, g) != nil || yaml.UnmarshalStrict(rb, r) != nil {
			return errors.New("invalid grant or replication YAML")
		}
		if len(r.Databases) == 0 {
			return errors.New("replication spec has no database copies")
		}
		for _, copy := range r.Databases {
			if _, err := database.Resolve(copy, g.Spec.Databases, protectedNamespace); err != nil {
				return err
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Validated %d database policy selections. Runtime, storage, source permissions, and relationships are checked during preparation.\n", len(r.Databases))
		return nil
	}}
	validate.Flags().StringVar(&grantFile, "grant-file", "", "Administrator ReplicaGrant YAML")
	validate.Flags().StringVar(&replicationFile, "replication-file", "", "ReplicationSpec YAML")
	validate.Flags().StringVar(&protectedNamespace, "state-namespace", "replicove-system", "Protected operator namespace holding database credential Secrets")
	_ = validate.MarkFlagRequired("grant-file")
	_ = validate.MarkFlagRequired("replication-file")
	root.AddCommand(validate)
	return root
}
