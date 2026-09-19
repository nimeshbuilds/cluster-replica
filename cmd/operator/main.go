package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/controller"
	helmprovider "github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var namespace, chartPath, probes string
	flag.StringVar(&namespace, "watch-namespace", "", "Required: one administrator-granted lab namespace")
	flag.StringVar(&chartPath, "chart-path", "", "Optional administrator-supplied archive; SHA-256 must match the pinned profile")
	flag.StringVar(&probes, "health-probe-bind-address", ":8081", "Health probe address")
	logOptions := zap.Options{}
	logOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOptions)))
	if namespace == "" {
		fmt.Fprintln(os.Stderr, "--watch-namespace is required")
		os.Exit(2)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	config := ctrl.GetConfigOrDie()
	config.Timeout = 30 * time.Second
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
	check((&controller.Reconciler{Client: mgr.GetClient(), Provider: provider, Namespace: namespace}).SetupWithManager(mgr))
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
