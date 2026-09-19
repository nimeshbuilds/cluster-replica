// Seed a real Helm release in the disposable test source namespace.
package main

import (
	"context"
	"fmt"
	"github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader"
	"k8s.io/client-go/tools/clientcmd"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		panic("usage: seed CHART NAMESPACE NAME")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		panic("test kubeconfig unavailable")
	}
	configuration, err := helm.Configuration(cfg, os.Args[2])
	if err != nil {
		panic("test Helm client unavailable")
	}
	chart, err := loader.Load(os.Args[1])
	if err != nil {
		panic("test chart unavailable")
	}
	install := action.NewInstall(configuration)
	install.ReleaseName = os.Args[3]
	install.Namespace = os.Args[2]
	install.DisableHooks = true
	_, err = install.RunWithContext(context.Background(), chart, map[string]any{})
	if err != nil {
		panic("test source release failed")
	}
	fmt.Println("Source Helm release installed")
}
