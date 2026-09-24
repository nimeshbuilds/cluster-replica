package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/diagnostics"
	"github.com/spf13/cobra"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *cli) doctorCommand() *cobra.Command {
	var grant, replica, format string
	cmd := &cobra.Command{Use: "doctor", Short: "Check API availability and caller permissions without provisioning or injecting faults", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		k, cfg, err := c.clients()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()
		typed, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return errors.New("cannot initialize Kubernetes diagnostics")
		}
		report := diagnostics.Report{Namespace: c.Namespace, Phase: "Preflight"}
		blocked := false
		add := func(check, result, detail string) {
			report.Findings = append(report.Findings, diagnostics.Finding{Check: check, Result: result, Detail: detail})
			if result == "failed" {
				blocked = true
			}
		}
		if v, e := typed.Discovery().ServerVersion(); e == nil {
			report.SourceVersion = v.GitVersion
			add("host-api", "passed", "The selected host API is reachable.")
		} else {
			add("host-api", "failed", "Cannot read the selected host API version.")
		}
		if _, e := typed.Discovery().ServerResourcesForGroupVersion(api.GroupVersion.String()); e != nil {
			add("replicove-api", "failed", "Replicove CRDs are absent, unreadable, or discovery is unavailable.")
		} else {
			add("replicove-api", "passed", "Replicove API discovery succeeded; this does not prove an operator is running.")
		}
		for _, q := range []struct{ resource, verb string }{{"clusterreplicas", "create"}, {"clusterreplicas", "get"}, {"clusterreplicas", "delete"}, {"replicaaccesses", "create"}, {"replicaaccesses", "get"}, {"replicaaccesses", "delete"}} {
			review, e := typed.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{Group: api.GroupVersion.Group, Namespace: c.Namespace, Resource: q.resource, Verb: q.verb}}}, metav1.CreateOptions{})
			check := "caller:" + q.verb + ":" + q.resource
			if e != nil {
				add(check, "unknown", "The API could not evaluate this caller permission.")
			} else if !review.Status.Allowed {
				add(check, "failed", "The authenticated caller is not allowed this request operation.")
			} else {
				add(check, "passed", "The authenticated caller is allowed this request operation.")
			}
		}
		if grant != "" {
			g := &api.ReplicaGrant{}
			if e := k.Get(ctx, client.ObjectKey{Name: grant}, g); e != nil {
				add("grant", "unknown", "Cannot inspect the named grant with this caller identity; ask its administrator.")
			} else if g.Spec.TargetNamespace != c.Namespace {
				add("grant", "failed", "The grant targets another destination namespace.")
			} else {
				add("grant", "passed", "Grant destination matches. Actual selections, data permissions and operator source RBAC are checked during capture.")
			}
		}
		if replica != "" {
			o := &api.ClusterReplica{}
			if e := k.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: replica}, o); e != nil {
				add("replica", "failed", "Cannot inspect the requested replica.")
			} else {
				ex := diagnostics.Explain(o)
				report.Name = ex.Name
				report.UID = ex.UID
				report.Revision = ex.Revision
				report.Resources = ex.Resources
				report.Omitted = ex.Omitted
				report.TargetVersion = ex.TargetVersion
				report.Findings = append(report.Findings, ex.Findings...)
			}
		}
		if _, e := typed.Discovery().ServerResourcesForGroupVersion("snapshot.storage.k8s.io/v1"); e == nil {
			add("snapshot-api", "observed", "Snapshot APIs are served; a qualified driver and actual restore test are still required.")
		} else {
			add("snapshot-api", "unknown", "Snapshot APIs were not discoverable. Enable or qualify mirroring before requesting data snapshots.")
		}
		add("network-isolation", "unverified", "API discovery cannot prove CNI enforcement. Run the documented disposable connectivity qualification before enabling data or network chaos.")
		add("credentials", "unverified", "Exact session Secret read access is checked only after issuance; diagnostics never read credential Secrets.")
		if format == "json" {
			err = json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		} else if format == "text" {
			err = diagnostics.Write(cmd.OutOrStdout(), report)
		} else {
			return fmt.Errorf("--output must be text or json")
		}
		if err != nil {
			return err
		}
		if blocked {
			return errors.New("preflight found missing required API access; see the report")
		}
		return nil
	}}
	cmd.Flags().StringVar(&grant, "grant", "", "Inspect one administrator grant")
	cmd.Flags().StringVar(&replica, "replica", "", "Include an existing replica's metadata-only plan")
	cmd.Flags().StringVar(&format, "output", "text", "Report format: text or json")
	return cmd
}
