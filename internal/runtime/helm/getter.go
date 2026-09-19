package helm

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The manager and Helm share the exact same authenticated REST configuration.
type restGetter struct {
	config    *rest.Config
	namespace string
}

func (g *restGetter) ToRESTConfig() (*rest.Config, error) { return rest.CopyConfig(g.config), nil }
func (g *restGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	c, err := discovery.NewDiscoveryClientForConfig(g.config)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(c), nil
}
func (g *restGetter) ToRESTMapper() (meta.RESTMapper, error) {
	c, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(c), nil
}
func (g *restGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig { return g }
func (g *restGetter) ClientConfig() (*rest.Config, error)           { return g.ToRESTConfig() }
func (g *restGetter) Namespace() (string, bool, error)              { return g.namespace, true, nil }
func (g *restGetter) ConfigAccess() clientcmd.ConfigAccess          { return nil }
func (g *restGetter) RawConfig() (clientcmdapi.Config, error) {
	// No credential material is copied to a serializable kubeconfig.
	return clientcmdapi.Config{CurrentContext: "operator", Contexts: map[string]*clientcmdapi.Context{"operator": {Namespace: g.namespace}}}, nil
}
