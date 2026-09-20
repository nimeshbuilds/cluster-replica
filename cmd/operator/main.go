package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/capture"
	"github.com/nimeshbuilds/cluster-replica/internal/controller"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/workflow"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var namespace, chartPath, probes, stateNamespace string
	var bootstrapStateKey bool
	flag.BoolVar(&bootstrapStateKey, "bootstrap-state-key", false, "Initialize the immutable state key once, then exit (installation Job only)")
	flag.StringVar(&stateNamespace, "state-namespace", "", "Administrator-only namespace for encrypted captures and access state")
	flag.StringVar(&namespace, "watch-namespace", "", "Required: one administrator-granted lab namespace")
	flag.StringVar(&chartPath, "chart-path", "", "Optional administrator-supplied archive; SHA-256 must match the pinned profile")
	flag.StringVar(&probes, "health-probe-bind-address", ":8081", "Health probe address")
	logOptions := zap.Options{}
	logOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOptions)))
	if namespace == "" && !bootstrapStateKey {
		fmt.Fprintln(os.Stderr, "--watch-namespace is required")
		os.Exit(2)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	config := ctrl.GetConfigOrDie()
	config.Timeout = 30 * time.Second
	if bootstrapStateKey {
		c, err := client.New(config, client.Options{Scheme: scheme})
		check(err)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		check(state.BootstrapKey(ctx, c, stateNamespace))
		fmt.Println("Protected state key initialized or validated")
		return
	}
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}},
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: probes,
		LeaderElection:         true, LeaderElectionNamespace: namespace,
		LeaderElectionID: "cluster-replica.nimeshbuilds.dev",
	})
	check(err)
	uncached, err := client.New(config, client.Options{Scheme: scheme})
	check(err)
	provider := &helmprovider.Provider{Config: config, Client: uncached, Namespace: namespace, ChartPath: chartPath}
	reconciler := &controller.Reconciler{Client: mgr.GetClient(), Provider: provider, Namespace: namespace}
	if stateNamespace != "" {
		if stateNamespace == namespace {
			fmt.Fprintln(os.Stderr, "state namespace must be separate from the destination namespace")
			os.Exit(2)
		}
		dyn, err := dynamic.NewForConfig(config)
		check(err)
		disc, err := discovery.NewDiscoveryClientForConfig(config)
		check(err)
		engine := &workflow.Engine{Client: uncached, Store: &state.Store{Client: uncached, Namespace: stateNamespace, MaxBytes: 716800}, Reader: &capture.Reader{Config: config, Dynamic: dyn, Discovery: disc}, Runtime: provider}
		reconciler.Workflow = engine
		check((&workflow.AccessReconciler{Engine: engine, Namespace: namespace}).SetupWithManager(mgr))
	}
	check(reconciler.SetupWithManager(mgr))
	check(mgr.AddHealthzCheck("healthz", healthz.Ping))
	check(mgr.AddReadyzCheck("readyz", healthz.Ping))
	check(mgr.Start(ctrl.SetupSignalHandler()))
}

func check(err error) {
	if err != nil {
		ctrl.Log.Error(err, "operator stopped")
		os.Exit(1)
	}
}
