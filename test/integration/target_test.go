//go:build integration

package integration

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/capture"
	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	"github.com/nimeshbuilds/cluster-replica/internal/workflow"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestExistingTargetPinsIdentityAndRejectsHost(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = api.AddToScheme(scheme)
	type cluster struct {
		cfg    *rest.Config
		client client.Client
		uid    string
	}
	start := func() cluster {
		env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd")}, ErrorIfCRDPathMissing: true}
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

	t.Run("full workflow settles without status churn", func(t *testing.T) {
		secret.Data["config"] = configBytes(guest.cfg)
		if err := host.client.Update(ctx, secret); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"lab", "source"} {
			if err := host.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := guest.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "integration"}}); err != nil {
			t.Fatal(err)
		}
		key := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: state.KeySecret, Namespace: "private"}, Data: map[string][]byte{"key": make([]byte, 32)}}
		if err := host.client.Create(ctx, key); err != nil {
			t.Fatal(err)
		}
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "source"}, Data: map[string]string{"mode": "source"}}
		if err := host.client.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		grant := &api.ReplicaGrant{ObjectMeta: metav1.ObjectMeta{Name: "integration"}, Spec: api.ReplicaGrantSpec{TargetNamespace: "lab", SourceNamespaces: []string{"source"}, Resources: []api.ResourceRule{{Kind: "ConfigMap"}}, ExistingTargets: []api.ExistingTarget{{Name: "guest", KubeconfigSecret: api.NamespacedName{Namespace: "private", Name: "guest"}, ClusterUID: guest.uid}}}}
		grant.Spec.AccessRoles = []string{"viewer"}
		grant.Spec.AccessSubjects = []api.AccessSubject{{Kind: "User", Name: "fixture-reader"}}
		if err := host.client.Create(ctx, grant); err != nil {
			t.Fatal(err)
		}
		obj := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "integration", Namespace: "lab"}, Spec: api.ClusterReplicaSpec{Profile: catalog.Profile, GrantRef: grant.Name, TTL: "1h", CleanupPolicy: "DeleteOwned", Target: &api.TargetSpec{Provider: "existing", ExistingRef: "guest"}, Replication: &api.ReplicationSpec{NamespaceMap: map[string]string{"source": "integration"}}}}
		if err := host.client.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
		dyn, err := dynamic.NewForConfig(host.cfg)
		if err != nil {
			t.Fatal(err)
		}
		disc, err := discovery.NewDiscoveryClientForConfig(host.cfg)
		if err != nil {
			t.Fatal(err)
		}
		engine := &workflow.Engine{Client: host.client, Store: &state.Store{Client: host.client, Namespace: "private"}, Reader: &capture.Reader{Config: host.cfg, Dynamic: dyn, Discovery: disc}}
		step := func() {
			t.Helper()
			if err := host.client.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Reconcile(ctx, obj); err != nil {
				t.Fatal(err)
			}
			if err := host.client.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 5 && obj.Status.Phase != "Ready"; i++ {
			step()
		}
		if obj.Status.Phase != "Ready" || obj.Status.Plan.AppliedCount != 1 {
			t.Fatalf("workflow did not become ready: %#v", obj.Status)
		}
		settledRV := obj.ResourceVersion
		for i := 0; i < 4; i++ {
			step()
		}
		if obj.ResourceVersion != settledRV {
			t.Fatal("steady reconciliation rewrote status and would trigger an event loop")
		}
		copied := &corev1.ConfigMap{}
		if err := guest.client.Get(ctx, client.ObjectKey{Namespace: "integration", Name: "settings"}, copied); err != nil {
			t.Fatal(err)
		}
		copied.Data["mode"] = "experiment"
		if err := guest.client.Update(ctx, copied); err != nil {
			t.Fatal(err)
		}
		step()
		if obj.Status.DriftCount != 1 {
			t.Fatal("ordinary experiment drift was not reported")
		}
		if err := guest.client.Get(ctx, client.ObjectKeyFromObject(copied), copied); err != nil {
			t.Fatal(err)
		}
		if copied.Data["mode"] != "experiment" {
			t.Fatal("ordinary reconciliation reset the experiment")
		}

		access := &api.ReplicaAccess{ObjectMeta: metav1.ObjectMeta{Name: "viewer", Namespace: "lab"}, Spec: api.ReplicaAccessSpec{ReplicaName: obj.Name, ReplicaUID: string(obj.UID), Role: "viewer", DurationSeconds: 900}}
		if err := host.client.Create(ctx, access); err != nil {
			t.Fatal(err)
		}
		reconciler := &workflow.AccessReconciler{Engine: engine, Namespace: "lab"}
		for i := 0; i < 5 && access.Status.Phase != "Ready"; i++ {
			if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(access)}); err != nil {
				t.Fatal(err)
			}
			if err := host.client.Get(ctx, client.ObjectKeyFromObject(access), access); err != nil {
				t.Fatal(err)
			}
		}
		if access.Status.Phase != "Ready" {
			t.Fatalf("access did not become ready: %#v", access.Status)
		}
		readerConfig := rest.CopyConfig(host.cfg)
		readerConfig.Impersonate = rest.ImpersonationConfig{UserName: "fixture-reader"}
		reader, err := client.New(readerConfig, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		credential := &corev1.Secret{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: "lab", Name: access.Status.CredentialSecret}, credential); err != nil {
			t.Fatal("exact-Secret credential reader was denied")
		}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: "private", Name: state.KeySecret}, &corev1.Secret{}); !apierrors.IsForbidden(err) {
			t.Fatal("credential reader could read the state key")
		}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: "lab", Name: "unrelated"}, &corev1.Secret{}); !apierrors.IsForbidden(err) {
			t.Fatal("credential reader received wildcard Secret access")
		}
		sealedState := &corev1.Secret{}
		if err := host.client.Get(ctx, client.ObjectKey{Namespace: "private", Name: state.Name(string(access.UID))}, sealedState); err != nil {
			t.Fatal(err)
		}
		if err := host.client.Delete(ctx, sealedState); err != nil {
			t.Fatal(err)
		}
		if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(access)}); err != nil {
			t.Fatal(err)
		}
		if err := host.client.Get(ctx, client.ObjectKeyFromObject(access), access); err != nil {
			t.Fatal(err)
		}
		if access.Status.Phase != "Blocked" {
			t.Fatal("lost issued-access state was silently replaced")
		}
		sealedState.ResourceVersion = ""
		sealedState.UID = ""
		sealedState.CreationTimestamp = metav1.Time{}
		if err := host.client.Create(ctx, sealedState); err != nil {
			t.Fatal(err)
		}
		if err := host.client.Delete(ctx, access); err != nil {
			t.Fatal(err)
		}
		if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(access)}); err != nil {
			t.Fatal(err)
		}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: "lab", Name: access.Status.CredentialSecret}, &corev1.Secret{}); !apierrors.IsForbidden(err) {
			t.Fatal("credential read permission survived revocation")
		}
	})
}
