//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestExistingTargetPinsIdentityAndRejectsHost(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	type cluster struct {
		cfg    *rest.Config
		client client.Client
		uid    string
	}
	start := func() cluster {
		env := &envtest.Environment{}
		cfg, err := env.Start()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := env.Stop(); err != nil {
				t.Error(err)
			}
		})
		k, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}}
		if err := k.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
		if err := k.Get(ctx, client.ObjectKey{Name: "kube-system"}, ns); err != nil {
			t.Fatal(err)
		}
		return cluster{cfg, k, string(ns.UID)}
	}
	host, guest := start(), start()
	if err := host.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "private"}}); err != nil {
		t.Fatal(err)
	}
	configBytes := func(cfg *rest.Config) []byte {
		raw := clientcmdapi.Config{CurrentContext: "guest", Clusters: map[string]*clientcmdapi.Cluster{"guest": {Server: cfg.Host, CertificateAuthorityData: cfg.CAData, CertificateAuthority: cfg.CAFile}}, Contexts: map[string]*clientcmdapi.Context{"guest": {Cluster: "guest", AuthInfo: "guest"}}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"guest": {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData, ClientCertificate: cfg.CertFile, ClientKey: cfg.KeyFile}}}
		if err := clientcmdapi.FlattenConfig(&raw); err != nil {
			t.Fatal(err)
		}
		data, err := clientcmd.Write(raw)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "private", Name: "guest"}, Data: map[string][]byte{"config": configBytes(guest.cfg)}}
	if err := host.client.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	st := &state.State{Provider: "existing", TargetSecretNamespace: "private", TargetSecretName: "guest", TargetClusterUID: guest.uid}
	connection, err := target.Connect(ctx, host.client, st, "")
	if err != nil || connection.UID != guest.uid {
		t.Fatal("legitimate existing guest rejected", err)
	}
	st.TargetClusterUID = "different"
	if _, err := target.Connect(ctx, host.client, st, ""); !errors.Is(err, target.ErrUnsafe) {
		t.Fatal("changed target UID accepted", err)
	}
	secret.Data["config"] = configBytes(host.cfg)
	if err := host.client.Update(ctx, secret); err != nil {
		t.Fatal(err)
	}
	st.TargetClusterUID = host.uid
	if _, err := target.Connect(ctx, host.client, st, ""); !errors.Is(err, target.ErrUnsafe) {
		t.Fatal("host cluster accepted as guest", err)
	}
}
