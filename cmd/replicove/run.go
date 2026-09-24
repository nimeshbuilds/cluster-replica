package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	"github.com/nimeshbuilds/cluster-replica/internal/testrun"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func (c *cli) runCommand() *cobra.Command {
	var file, artifactDir string
	cmd := &cobra.Command{Use: "run -f RECIPE.yaml [-- COMMAND ARG...]", Short: "Create a bounded test replica, run a local command, and verify cleanup", Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 && cmd.ArgsLenAtDash() != 0 {
				return errors.New("put the test command after --")
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return errors.New("cannot read test recipe")
			}
			recipe, err := testrun.Parse(data)
			if err != nil {
				return err
			}
			if len(args) != 0 {
				recipe.Execution.Command = args
			}
			if err := recipe.DefaultAndValidate(); err != nil {
				return err
			}
			if len(recipe.Destinations) > 0 && (cmd.Flags().Changed("namespace") || cmd.InheritedFlags().Changed("namespace")) {
				return errors.New("choose either recipe destinations or an explicit --namespace, not both")
			}
			if artifactDir == "" {
				id, err := testrun.ID()
				if err != nil {
					return err
				}
				artifactDir = "replicove-run-" + time.Now().UTC().Format("20060102T150405Z") + "-" + id[:8]
			}
			if err := os.Mkdir(artifactDir, 0700); err != nil {
				return errors.New("cannot create artifacts directory; choose a new path")
			}
			absolute, err := filepath.Abs(artifactDir)
			if err != nil {
				return errors.New("cannot resolve artifacts directory")
			}
			k, cfg, err := c.clients()
			if err != nil {
				return err
			}
			namespace := c.Namespace
			if len(recipe.Destinations) > 0 {
				chosen, err := testrun.SelectDestination(cmd.Context(), k, recipe.Destinations)
				if err != nil {
					return err
				}
				namespace = chosen.Namespace
				if recipe.Replica != nil {
					recipe.Replica.GrantRef = chosen.Grant
				} else {
					recipe.Mirror.Template.GrantRef = chosen.Grant
				}
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Provisioning a fresh test environment in %s. Reports: %s\n", namespace, absolute)
			runner := &testrun.Runner{Client: k, Namespace: namespace, OpenSession: runSession(cfg), Execute: testrun.Process(cmd.OutOrStdout(), cmd.ErrOrStderr()), ResolveFaults: resolveRunFaults}
			report := runner.Run(cmd.Context(), recipe)
			if err := testrun.WriteReports(absolute, report); err != nil {
				return err
			}
			if report.Request != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Request: %s/%s (%s)\n", namespace, report.Request.Name, report.Request.UID)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Setup: %s. Test: %s. Cleanup: %s. Reports: %s\n", report.Setup.Status, report.Test.Status, report.Cleanup.Status, absolute)
			if report.RetainedUntil != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Retained until original TTL: %s\n", report.RetainedUntil.Format(time.RFC3339))
			}
			if !report.Successful() {
				return errors.New("run did not pass every stage; inspect report.json for separate setup, test and cleanup outcomes")
			}
			return nil
		}}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Local TestRecipe YAML file")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().StringVar(&artifactDir, "artifacts", "", "New private directory for JSON provenance and JUnit results")
	return cmd
}

func resolveRunFaults(ctx context.Context, path string, faults []api.ChaosFault) ([]api.ChaosFault, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read private guest credential")
	}
	cfg, err := target.Parse(data)
	if err != nil {
		return nil, errors.New("unsafe guest credential")
	}
	k, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, errors.New("cannot initialize scoped guest client")
	}
	return resolveRunFaultsWithClient(ctx, k, faults)
}

func resolveRunFaultsWithClient(ctx context.Context, k kubernetes.Interface, faults []api.ChaosFault) ([]api.ChaosFault, error) {
	for i := range faults {
		f := &faults[i]
		if f.Target == nil || f.Target.UID != "" {
			continue
		}
		if f.Namespace == "" || f.Target.Name == "" {
			return nil, errors.New("chaos target requires an explicit namespace and name")
		}
		var obj metav1.Object
		var err error
		switch f.Target.Kind {
		case "Pod":
			obj, err = k.CoreV1().Pods(f.Namespace).Get(ctx, f.Target.Name, metav1.GetOptions{})
		case "Deployment":
			obj, err = k.AppsV1().Deployments(f.Namespace).Get(ctx, f.Target.Name, metav1.GetOptions{})
		case "StatefulSet":
			obj, err = k.AppsV1().StatefulSets(f.Namespace).Get(ctx, f.Target.Name, metav1.GetOptions{})
		default:
			return nil, errors.New("unsupported chaos target kind")
		}
		if err != nil || obj == nil || obj.GetUID() == "" {
			return nil, errors.New("cannot resolve guest chaos target")
		}
		f.Target.UID = string(obj.GetUID())
	}
	return faults, nil
}

func runSession(cfg *rest.Config) func(context.Context, *api.ClusterReplica, []byte) (testrun.Session, error) {
	return func(ctx context.Context, replica *api.ClusterReplica, data []byte) (testrun.Session, error) {
		if replica.Status.Runtime == nil {
			return testrun.Session{}, errors.New("test session requires an owned runtime tunnel")
		}
		forwarded, stop, done, err := forward(ctx, cfg, replica, data)
		if err != nil {
			return testrun.Session{}, err
		}
		dir, err := os.MkdirTemp("", "replicove-test-credential-")
		if err != nil {
			stop()
			return testrun.Session{}, errors.New("cannot create private test credential directory")
		}
		path := filepath.Join(dir, "config")
		remove, err := writeCredential(path, forwarded)
		if err != nil {
			stop()
			_ = os.Remove(dir)
			return testrun.Session{}, err
		}
		return testrun.Session{Kubeconfig: path, Done: done, Close: func() { stop(); remove(); _ = os.Remove(dir) }}, nil
	}
}
