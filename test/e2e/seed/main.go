// Seed a real Helm release in the disposable test source namespace.
package main

import (
	"context"
	"fmt"
	"github.com/nimeshbuilds/cluster-replica/internal/runtime/helm"
	"helm.sh/helm/v4/pkg/chart/loader"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"sigs.k8s.io/yaml"
)

func main() {
	if len(os.Args) != 4 && len(os.Args) != 5 {
		panic("usage: seed CHART NAMESPACE NAME [VALUES]")
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
	install := helm.NewInstall(configuration)
	install.ReleaseName = os.Args[3]
	install.Namespace = os.Args[2]
	install.DisableHooks = true
	values := map[string]any{}
	if len(os.Args) == 5 {
		data, e := os.ReadFile(os.Args[4])
		if e != nil {
			panic("test values unavailable")
		}
		if e := yaml.UnmarshalStrict(data, &values); e != nil {
			panic("test values invalid")
		}
	}
	_, err = install.RunWithContext(context.Background(), chart, values)
	if err != nil {
		panic("test source release failed")
	}
	fmt.Println("Source Helm release installed")
}
