package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	operatorchart "github.com/nimeshbuilds/cluster-replica/charts/replicove"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	"github.com/nimeshbuilds/cluster-replica/internal/workflow"
	"github.com/spf13/cobra"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var version = "dev"

type cli struct{ Namespace, Kubeconfig, Context string }

func (c *cli) clients() (client.Client, *rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if c.Kubeconfig != "" {
		rules.ExplicitPath = c.Kubeconfig
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: c.Context}).ClientConfig()
	if err != nil {
		return nil, nil, errors.New("cannot load the selected host kubeconfig")
	}
	cfg.Timeout = 30 * time.Second
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	k, err := client.New(cfg, client.Options{Scheme: scheme})
	return k, cfg, err
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := command().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func command() *cobra.Command {
	c := &cli{}
	root := &cobra.Command{Use: "replicove", Version: version, Short: "Your cluster's tools. A fresh place to test.", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVarP(&c.Namespace, "namespace", "n", "replica-lab", "Administrator-granted destination namespace")
	root.PersistentFlags().StringVar(&c.Kubeconfig, "kubeconfig", "", "Host kubeconfig path")
	root.PersistentFlags().StringVar(&c.Context, "context", "", "Host kubeconfig context")
	var grant, ttl, from, replicationFile, profile string
	var manual bool
	create := &cobra.Command{Use: "create NAME", Short: "Capture the granted toolset and provision an ephemeral replica", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		k, _, err := c.clients()
		if err != nil {
			return err
		}
		spec := &api.ReplicationSpec{}
		if from != "" {
			spec.Namespaces = strings.Split(from, ",")
		}
		if replicationFile != "" {
			data, err := os.ReadFile(replicationFile)
			if err != nil {
				return errors.New("cannot read replication spec file")
			}
			if err := yaml.UnmarshalStrict(data, spec); err != nil {
				return errors.New("invalid replication spec YAML")
			}
		}
		approval := "Automatic"
		if manual {
			approval = "Manual"
		}
		obj := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: args[0], Namespace: c.Namespace}, Spec: api.ClusterReplicaSpec{Profile: profile, TTL: ttl, CleanupPolicy: "DeleteOwned", GrantRef: grant, Replication: spec, Approval: approval}}
		if err := k.Create(cmd.Context(), obj); err != nil {
			return errors.New("cannot create ClusterReplica; verify namespace access, grant, name, and spec")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s/%s. Use replicove plan %s -n %s to inspect progress.\n", c.Namespace, args[0], args[0], c.Namespace)
		return nil
	}}
	create.Flags().StringVar(&grant, "grant", "", "Administrator ReplicaGrant name")
	_ = create.MarkFlagRequired("grant")
	create.Flags().StringVar(&profile, "profile", catalog.PersistentProfile, "Pinned vCluster runtime profile")
	create.Flags().StringVar(&ttl, "ttl", "2h", "Lifetime including provisioning")
	create.Flags().StringVar(&from, "from", "", "Comma-separated granted source namespaces")
	create.Flags().StringVar(&replicationFile, "replication-file", "", "YAML ReplicationSpec with selectors, mappings, and overrides")
	create.Flags().BoolVar(&manual, "manual", false, "Require approval of the captured plan before provisioning")
	root.AddCommand(create)
	for _, name := range []string{"status", "plan", "approve", "refresh", "delete"} {
		op := name
		root.AddCommand(&cobra.Command{Use: op + " NAME", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			k, _, err := c.clients()
			if err != nil {
				return err
			}
			obj := &api.ClusterReplica{}
			if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: args[0]}, obj); err != nil {
				return errors.New("cannot read the ClusterReplica")
			}
			switch op {
			case "status", "plan":
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(obj.Status)
			case "delete":
				if err := k.Delete(cmd.Context(), obj); err != nil {
					return errors.New("cannot request replica deletion")
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Deletion requested; the finalizer verifies owned-resource cleanup.")
				return nil
			default:
				before := obj.DeepCopy()
				if obj.Annotations == nil {
					obj.Annotations = map[string]string{}
				}
				if op == "approve" {
					if obj.Status.Plan == nil {
						return errors.New("the source plan is not ready yet")
					}
					obj.Annotations["replicove.nimeshbuilds.dev/approved-plan"] = obj.Status.Plan.Revision
				} else {
					token, err := state.OperationID()
					if err != nil {
						return err
					}
					obj.Annotations[workflow.RefreshAnnotation] = token
				}
				if err := k.Patch(cmd.Context(), obj, client.MergeFrom(before)); err != nil {
					return errors.New("cannot update the replica request")
				}
				fmt.Fprintln(cmd.OutOrStdout(), op+" requested")
				return nil
			}
		}})
	}
	root.AddCommand(c.accessCommand(false), c.accessCommand(true), c.installCommand())
	return root
}
func (c *cli) installCommand() *cobra.Command {
	var system, image, valuesFile string
	cmd := &cobra.Command{Use: "install", Short: "Install the operator and CRDs as a host administrator", RunE: func(cmd *cobra.Command, args []string) error {
		k, cfg, err := c.clients()
		if err != nil {
			return err
		}
		if system == c.Namespace {
			return errors.New("system namespace must be separate from the destination")
		}
		for _, ns := range []string{system, c.Namespace} {
			obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
			if err := k.Create(cmd.Context(), obj); err != nil && !apierrors.IsAlreadyExists(err) {
				return errors.New("cannot create installation namespaces")
			}
		}
		files := []*archive.BufferedFile{}
		if err := fs.WalkDir(operatorchart.Files, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := operatorchart.Files.ReadFile(path)
			if err != nil {
				return err
			}
			files = append(files, &archive.BufferedFile{Name: path, Data: data})
			return nil
		}); err != nil {
			return err
		}
		chart, err := loader.LoadFiles(files)
		if err != nil {
			return errors.New("embedded operator chart is invalid")
		}
		if version != "dev" {
			chart.Metadata.Version = strings.TrimPrefix(version, "v")
			chart.Metadata.AppVersion = strings.TrimPrefix(version, "v")
		}
		values := map[string]any{}
		if valuesFile != "" {
			data, err := os.ReadFile(valuesFile)
			if err != nil {
				return errors.New("cannot read installation values")
			}
			if err := yaml.UnmarshalStrict(data, &values); err != nil {
				return errors.New("invalid installation values YAML")
			}
		}
		values["destinationNamespace"] = c.Namespace
		if image != "" {
			index := strings.LastIndex(image, ":")
			if index < 1 {
				return errors.New("--image requires an explicit repository:tag")
			}
			values["image"] = map[string]any{"repository": image[:index], "tag": image[index+1:]}
		}
		configuration, err := helmprovider.Configuration(cfg, system)
		if err != nil {
			return errors.New("cannot initialize installer")
		}
		install := helmprovider.NewInstall(configuration)
		install.ReleaseName = "replicove"
		install.Namespace = system
		install.CreateNamespace = false
		install.Timeout = 2 * time.Minute
		if _, err := install.RunWithContext(cmd.Context(), chart, values); err != nil {
			return errors.New("operator installation failed; inspect the replicove Helm release in the system namespace")
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Operator installed. Apply an administrator ReplicaGrant before creating replicas.")
		return nil
	}}
	cmd.Flags().StringVar(&system, "system-namespace", "replicove-system", "Protected operator and encrypted-state namespace")
	cmd.Flags().StringVar(&image, "image", "", "Explicit operator repository:tag override")
	cmd.Flags().StringVar(&valuesFile, "values", "", "Operator chart values YAML, including source read permissions")
	return cmd
}
func (c *cli) accessCommand(tunnel bool) *cobra.Command {
	var role, output string
	var seconds int64
	var wait time.Duration
	name := "access"
	if tunnel {
		name = "connect"
	}
	cmd := &cobra.Command{Use: name + " NAME", Short: "Issue bounded guest credentials; connect also opens a local tunnel", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		k, cfg, err := c.clients()
		if err != nil {
			return err
		}
		obj := &api.ClusterReplica{}
		if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: c.Namespace, Name: args[0]}, obj); err != nil {
			return errors.New("cannot read the ClusterReplica")
		}
		a := &api.ReplicaAccess{ObjectMeta: metav1.ObjectMeta{GenerateName: args[0] + "-", Namespace: c.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "ClusterReplica", Name: obj.Name, UID: obj.UID}}}, Spec: api.ReplicaAccessSpec{ReplicaName: obj.Name, ReplicaUID: string(obj.UID), Role: role, DurationSeconds: seconds}}
		if err := k.Create(cmd.Context(), a); err != nil {
			return errors.New("cannot create a ReplicaAccess request")
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Access request: %s/%s\n", a.Namespace, a.Name)
		deadline := time.Now().Add(wait)
		for a.Status.Phase != "Ready" {
			if time.Now().After(deadline) {
				return errors.New("access request is not ready; inspect ReplicaAccess status")
			}
			if a.Status.Phase == "Rejected" || a.Status.Phase == "Revoked" || a.Status.Phase == "Blocked" {
				return errors.New("access request did not pass its checks; inspect ReplicaAccess status")
			}
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-time.After(time.Second):
			}
			if err := k.Get(cmd.Context(), client.ObjectKeyFromObject(a), a); err != nil {
				return errors.New("cannot read access request status")
			}
		}
		secret := &corev1.Secret{}
		if err := k.Get(cmd.Context(), client.ObjectKey{Namespace: a.Namespace, Name: a.Status.CredentialSecret}, secret); err != nil {
			return errors.New("cannot read the access credential Secret; request exact-name Secret read permission from the namespace administrator")
		}
		if !target.OwnedBy(secret.OwnerReferences, string(a.UID)) {
			return errors.New("credential Secret ownership differs from the access request")
		}
		if _, err := target.Parse(secret.Data["config"]); err != nil {
			return errors.New("guest kubeconfig failed data-only TLS validation")
		}
		data := secret.Data["config"]
		var stop func()
		var tunnelDone <-chan error
		if tunnel {
			if obj.Status.Runtime == nil {
				return errors.New("existing targets require their configured network route; use access instead")
			}
			forwarded, close, done, err := forward(cmd.Context(), cfg, obj, data)
			if err != nil {
				return err
			}
			data = forwarded
			stop = close
			tunnelDone = done
			defer stop()
		}
		if output == "" {
			output = filepath.Join(".", args[0]+".kubeconfig")
		}
		cleanup, err := writeCredential(output, data)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Guest kubeconfig: %s (expires %s)\n", output, a.Status.ExpiresAt.UTC().Format(time.RFC3339))
		if tunnel {
			defer cleanup()
			fmt.Fprintln(cmd.OutOrStdout(), "Tunnel listening on 127.0.0.1. Keep this process running; Ctrl-C closes it.")
			timer := time.NewTimer(time.Until(a.Status.ExpiresAt.Time))
			defer timer.Stop()
			select {
			case <-cmd.Context().Done():
			case <-timer.C:
			case <-tunnelDone:
				return errors.New("guest tunnel closed; run connect again to establish a new session")
			}
			return nil
		}
		return nil
	}}
	cmd.Flags().StringVar(&role, "role", "viewer", "Granted role: viewer, deployer, admin")
	cmd.Flags().Int64Var(&seconds, "duration-seconds", 900, "Credential duration, capped by grant and replica TTL")
	cmd.Flags().StringVar(&output, "output", "", "New kubeconfig file; existing files are never overwritten")
	cmd.Flags().DurationVar(&wait, "wait", 3*time.Minute, "Maximum wait for credential issuance")
	return cmd
}
func writeCredential(path string, data []byte) (func(), error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("cannot create output file; choose a new path")
	}
	info, _ := f.Stat()
	cleanup := func() {
		current, err := os.Lstat(path)
		if err == nil && info != nil && os.SameFile(info, current) {
			_ = os.Remove(path)
		}
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return nil, errors.New("cannot write credential file")
	}
	if err := f.Close(); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}
func forward(ctx context.Context, cfg *rest.Config, obj *api.ClusterReplica, data []byte) ([]byte, func(), <-chan error, error) {
	k, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, nil, errors.New("cannot initialize host tunnel")
	}
	pods, err := k.CoreV1().Pods(obj.Namespace).List(ctx, metav1.ListOptions{LabelSelector: catalog.OwnerLabel + "=" + string(obj.UID)})
	if err != nil {
		return nil, nil, nil, errors.New("cannot list owned vCluster pods")
	}
	pod := ""
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
			if pod != "" {
				return nil, nil, nil, errors.New("more than one runtime pod is running; retry after rollout")
			}
			pod = p.Name
		}
	}
	if pod == "" {
		return nil, nil, nil, errors.New("no running vCluster pod is available")
	}
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, nil, nil, errors.New("cannot initialize authenticated port forwarding")
	}
	url := k.CoreV1().RESTClient().Post().Resource("pods").Namespace(obj.Namespace).Name(pod).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)
	stop, ready := make(chan struct{}), make(chan struct{})
	forwarder, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{"0:8443"}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return nil, nil, nil, errors.New("cannot initialize local tunnel")
	}
	result := make(chan error, 1)
	go func() { result <- forwarder.ForwardPorts() }()
	closeTunnel := func() { close(stop) }
	select {
	case <-ready:
	case <-ctx.Done():
		closeTunnel()
		return nil, nil, nil, ctx.Err()
	case <-result:
		closeTunnel()
		return nil, nil, nil, errors.New("host port-forward failed")
	}
	ports, err := forwarder.GetPorts()
	if err != nil || len(ports) != 1 {
		closeTunnel()
		return nil, nil, nil, errors.New("cannot determine tunnel port")
	}
	raw, err := clientcmd.Load(data)
	if err != nil {
		closeTunnel()
		return nil, nil, nil, errors.New("invalid guest kubeconfig")
	}
	for _, cluster := range raw.Clusters {
		cluster.Server = fmt.Sprintf("https://127.0.0.1:%d", ports[0].Local)
	}
	output, err := clientcmd.Write(*raw)
	if err != nil {
		closeTunnel()
		return nil, nil, nil, errors.New("cannot encode tunneled kubeconfig")
	}
	return output, closeTunnel, result, nil
}
