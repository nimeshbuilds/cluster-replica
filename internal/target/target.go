// Package target constructs data-only, TLS-verified guest clients. Credentials
// never pass through commands, files, status, or log messages.
package target

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrUnsafe = errors.New("target credentials or identity are unsafe")
var ErrUnavailable = errors.New("target connection unavailable")

// ConnectionError contains only classified diagnostics, never upstream error
// text, credential content, or an untrusted endpoint URL.
type ConnectionError struct{ Stage, Reason string }

func (e *ConnectionError) Error() string { return e.Stage + " failed (" + e.Reason + ")." }
func (e *ConnectionError) Unwrap() error { return ErrUnavailable }
func unavailable(stage string, err error) error {
	reason := "Unavailable"
	var hostname x509.HostnameError
	var authority x509.UnknownAuthorityError
	var certificate x509.CertificateInvalidError
	var dns *net.DNSError
	var network net.Error
	switch {
	case apierrors.IsNotFound(err):
		reason = "NotFound"
	case apierrors.IsUnauthorized(err):
		reason = "Unauthorized"
	case apierrors.IsForbidden(err):
		reason = "Forbidden"
	case errors.As(err, &hostname):
		reason = "TLSHostname"
	case errors.As(err, &authority):
		reason = "TLSAuthority"
	case errors.As(err, &certificate):
		reason = "TLSCertificate"
	case errors.As(err, &dns):
		reason = "DNS"
	case errors.As(err, &network) && network.Timeout():
		reason = "Timeout"
	}
	return &ConnectionError{Stage: stage, Reason: reason}
}

type Connection struct {
	Config       *rest.Config
	Dynamic      dynamic.Interface
	Kubernetes   kubernetes.Interface
	Discovery    discovery.DiscoveryInterface
	UID, Version string
}

func Parse(data []byte) (*rest.Config, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return nil, ErrUnsafe
	}
	raw, err := clientcmd.Load(data)
	if err != nil {
		return nil, ErrUnsafe
	}
	if len(raw.AuthInfos) != 1 || len(raw.Clusters) != 1 || len(raw.Contexts) != 1 {
		return nil, ErrUnsafe
	}
	for _, a := range raw.AuthInfos {
		if a.Exec != nil || a.AuthProvider != nil || a.TokenFile != "" || a.ClientCertificate != "" || a.ClientKey != "" || a.Impersonate != "" || a.ImpersonateUID != "" || len(a.ImpersonateGroups) > 0 || len(a.ImpersonateUserExtra) > 0 || a.Username != "" || a.Password != "" {
			return nil, ErrUnsafe
		}
	}
	for _, c := range raw.Clusters {
		u, e := url.Parse(c.Server)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || c.InsecureSkipTLSVerify || c.CertificateAuthority != "" || len(c.CertificateAuthorityData) == 0 || c.ProxyURL != "" {
			return nil, ErrUnsafe
		}
	}
	cfg, err := clientcmd.NewDefaultClientConfig(*raw, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, ErrUnsafe
	}
	cfg.Timeout = 20 * time.Second
	cfg.UserAgent = "replicove"
	return cfg, nil
}

// Connect pins both runtime root ownership and the guest's kube-system UID.
// Existing target endpoint authority is exclusively administrator-delegated.
func Connect(ctx context.Context, host client.Client, st *state.State, release string) (*Connection, error) {
	secret := &corev1.Secret{}
	if err := host.Get(ctx, client.ObjectKey{Namespace: st.TargetSecretNamespace, Name: st.TargetSecretName}, secret); err != nil {
		return nil, unavailable("Read target credential Secret", err)
	}
	owned := st.Provider == "helm"
	if owned {
		svc := &corev1.Service{}
		if err := host.Get(ctx, client.ObjectKey{Namespace: st.OwnerNamespace, Name: release}, svc); err != nil {
			return nil, unavailable("Read runtime Service", err)
		}
		if string(svc.UID) != st.RuntimeRootUID || !OwnedBy(secret.OwnerReferences, st.RuntimeRootUID) {
			return nil, ErrUnsafe
		}
	}
	cfg, err := Parse(secret.Data["config"])
	if err != nil {
		return nil, err
	}
	if owned {
		cfg.Host = "https://" + release + "." + st.OwnerNamespace + ".svc:443"
		// vCluster 0.37.1 signs release.namespace, but not its .svc alias.
		// Route through Kubernetes DNS while verifying the actual signed name.
		cfg.TLSClientConfig.ServerName = release + "." + st.OwnerNamespace
	}
	clients, err := Clients(cfg)
	if err != nil {
		return nil, err
	}
	ns, err := clients.Kubernetes.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return nil, unavailable("Read guest cluster identity", err)
	}
	clients.UID = string(ns.UID)
	if st.TargetClusterUID != "" && st.TargetClusterUID != clients.UID {
		return nil, ErrUnsafe
	}
	source := &corev1.Namespace{}
	if err := host.Get(ctx, client.ObjectKey{Name: "kube-system"}, source); err != nil {
		return nil, unavailable("Read host cluster identity", err)
	}
	if source.UID == ns.UID {
		return nil, ErrUnsafe
	}
	version, err := clients.Discovery.ServerVersion()
	if err != nil {
		return nil, unavailable("Discover guest Kubernetes version", err)
	}
	clients.Version = version.GitVersion
	return clients, nil
}
func Clients(cfg *rest.Config) (*Connection, error) {
	d, e := dynamic.NewForConfig(cfg)
	if e != nil {
		return nil, ErrUnsafe
	}
	k, e := kubernetes.NewForConfig(cfg)
	if e != nil {
		return nil, ErrUnsafe
	}
	disc, e := discovery.NewDiscoveryClientForConfig(cfg)
	if e != nil {
		return nil, ErrUnsafe
	}
	return &Connection{Config: cfg, Dynamic: d, Kubernetes: k, Discovery: disc}, nil
}
func OwnedBy(refs []metav1.OwnerReference, uid string) bool {
	if uid == "" {
		return false
	}
	for _, ref := range refs {
		if string(ref.UID) == uid {
			return true
		}
	}
	return false
}
func LocalServer(server string) bool {
	u, err := url.Parse(server)
	return err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || strings.HasSuffix(u.Hostname(), ".svc"))
}
