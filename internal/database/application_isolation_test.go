package database

import (
	"context"
	"errors"
	"testing"

	"github.com/nimeshbuilds/cluster-replica/internal/catalog"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestApplicationIsolationPreservesDatabaseZeroEgress(t *testing.T) {
	c, obj, grant, st, conn, runner := controllerFixture(t)
	ctx := context.Background()
	if _, err := c.Prepare(ctx, obj, grant, st, conn); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 {
		t.Fatal("source capture did not execute behind isolation")
	}
	policies := &networkingv1.NetworkPolicyList{}
	if err := c.Client.List(ctx, policies, client.InNamespace("lab")); err != nil {
		t.Fatal(err)
	}
	release := catalog.Resolve(st.OwnerUID).ReleaseName
	app := map[string]string{"vcluster.loft.sh/managed-by": release, "vcluster.loft.sh/namespace": "test", "app": "consumer"}
	db := hostLabels(st, &st.Databases[0], false)
	source := map[string]string{"app": "source-db"}
	dns := map[string]string{"vcluster.loft.sh/managed-by": release, "vcluster.loft.sh/namespace": "kube-system", "k8s-app": "vcluster-kube-dns"}
	api := map[string]string{"app": "vcluster", "release": release}
	for _, tc := range []struct {
		name      string
		from, to  map[string]string
		targetNS  string
		port      int32
		protocol  corev1.Protocol
		permitted bool
	}{
		{"source database denied", app, source, "source", 5432, corev1.ProtocolTCP, false},
		{"unrelated host service denied", app, source, "lab", 5432, corev1.ProtocolTCP, false},
		{"same replica database allowed", app, db, "lab", 5432, corev1.ProtocolTCP, true},
		{"runtime DNS backend allowed", app, dns, "lab", 1053, corev1.ProtocolUDP, true},
		{"runtime DNS service allowed", app, dns, "lab", 53, corev1.ProtocolTCP, true},
		{"DNS other port denied", app, dns, "lab", 8080, corev1.ProtocolTCP, false},
		{"runtime API allowed", app, api, "lab", 6443, corev1.ProtocolTCP, true},
		{"API other port denied", app, api, "lab", 80, corev1.ProtocolTCP, false},
		{"final database source denied", db, source, "source", 5432, corev1.ProtocolTCP, false},
		{"final database peer denied", db, app, "lab", 80, corev1.ProtocolTCP, false},
		{"final database DNS denied", db, dns, "lab", 53, corev1.ProtocolUDP, false},
		{"staging database peer denied", hostLabels(st, &st.Databases[0], true), app, "lab", 80, corev1.ProtocolTCP, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := egressPermitted(t, policies.Items, tc.from, tc.to, tc.targetNS, tc.port, tc.protocol); got != tc.permitted {
				t.Fatalf("egress allowed=%v, want %v", got, tc.permitted)
			}
		})
	}
	// Adding a reserved database label must tighten connectivity, not escape the
	// broad deny policy when the application allow selector stops matching.
	app["replicove.nimeshbuilds.dev/database"] = "forged"
	if egressPermitted(t, policies.Items, app, source, "source", 5432, corev1.ProtocolTCP) || egressPermitted(t, policies.Items, app, db, "lab", 5432, corev1.ProtocolTCP) {
		t.Fatal("database label escaped the default deny policy")
	}
	loaded, err := c.Store.Load(ctx, st.OwnerUID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Controller{Client: c.Client, Store: c.Store}
	if err := restarted.ensureApplicationIsolation(ctx, loaded); err != nil {
		t.Fatalf("restart could not verify durable application policies: %v", err)
	}
}

// Evaluate the small NetworkPolicy subset emitted here, including Kubernetes'
// additive allowance rule and same-namespace PodSelector behavior.
func egressPermitted(t *testing.T, policies []networkingv1.NetworkPolicy, from, to map[string]string, targetNS string, port int32, protocol corev1.Protocol) bool {
	t.Helper()
	isolated := false
	for _, p := range policies {
		selector, err := metav1.LabelSelectorAsSelector(&p.Spec.PodSelector)
		if err != nil {
			t.Fatal(err)
		}
		if !selector.Matches(klabels.Set(from)) {
			continue
		}
		for _, kind := range p.Spec.PolicyTypes {
			isolated = isolated || kind == networkingv1.PolicyTypeEgress
		}
		for _, rule := range p.Spec.Egress {
			peerAllowed := len(rule.To) == 0
			for _, peer := range rule.To {
				if peer.NamespaceSelector != nil || peer.IPBlock != nil || peer.PodSelector == nil {
					t.Fatal("unexpected broad or cross-namespace egress peer")
				}
				selector, err := metav1.LabelSelectorAsSelector(peer.PodSelector)
				if err != nil {
					t.Fatal(err)
				}
				peerAllowed = peerAllowed || p.Namespace == targetNS && selector.Matches(klabels.Set(to))
			}
			portAllowed := len(rule.Ports) == 0
			for _, allowed := range rule.Ports {
				portAllowed = portAllowed || allowed.Port != nil && allowed.Port.IntVal == port && allowed.Protocol != nil && *allowed.Protocol == protocol
			}
			if peerAllowed && portAllowed {
				return true
			}
		}
	}
	return !isolated
}

func TestApplicationOnlyForeignAllowanceBlocksBeforeSourceRead(t *testing.T) {
	c, obj, grant, st, conn, runner := controllerFixture(t)
	ctx := context.Background()
	p := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "app-only-allow", Namespace: "lab"}, Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "consumer"}}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}, Egress: []networkingv1.NetworkPolicyEgressRule{{}}}}
	if err := c.Client.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	if ready, err := c.Prepare(ctx, obj, grant, st, conn); ready || !errors.Is(err, ErrDenied) || len(runner.calls) != 0 {
		t.Fatalf("application egress bypass accepted before capture: %v %v", ready, err)
	}
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(p), &networkingv1.NetworkPolicy{}); err != nil {
		t.Fatal("foreign allowance was changed")
	}
}

func TestApplicationIsolationCleanupWaitsForPhysicalApplicationPods(t *testing.T) {
	c, obj, grant, st, conn, _ := controllerFixture(t)
	ctx := context.Background()
	if _, err := c.Prepare(ctx, obj, grant, st, conn); err != nil {
		t.Fatal(err)
	}
	app := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "lagging-application", Namespace: "lab", Labels: map[string]string{"vcluster.loft.sh/managed-by": catalog.Resolve(st.OwnerUID).ReleaseName, "vcluster.loft.sh/namespace": "test"}}}
	if err := c.Client.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"replicove-db-test-db-stage-host", "replicove-db-test-db-host"} {
		if err := c.Client.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lab"}}); err != nil {
			t.Fatal(err)
		}
	}
	done := false
	for i := 0; i < 20; i++ {
		var err error
		done, err = c.Cleanup(ctx, st, conn)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if !done {
		t.Fatal("database cleanup did not complete")
	}
	if done, err := c.CleanupIsolation(ctx, st); done || err != nil {
		t.Fatalf("application boundary removed before host Pod disappeared: %v %v", done, err)
	}
	for _, e := range st.Databases[0].ApplicationPolicies {
		if err := c.Client.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.Name}, &networkingv1.NetworkPolicy{}); err != nil {
			t.Fatal("data cleanup removed application boundary")
		}
	}
	if err := c.Client.Delete(ctx, app); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		var err error
		done, err = c.CleanupIsolation(ctx, st)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if !done {
		t.Fatal("application boundary cleanup did not finish")
	}
	remaining := &networkingv1.NetworkPolicyList{}
	if err := c.Client.List(ctx, remaining, client.InNamespace("lab")); err != nil || len(remaining.Items) != 0 {
		t.Fatal("owned isolation leaked after all Pods disappeared")
	}
}

type policyUIDClient struct{ client.Client }

func (c policyUIDClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*networkingv1.NetworkPolicy); ok {
		obj.SetUID(types.UID("original-" + obj.GetName()))
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestApplicationIsolationPreservesReplacedPolicy(t *testing.T) {
	c, _, _, st, _, _ := controllerFixture(t)
	c.Client = policyUIDClient{Client: c.Client}
	c.Store.Client = c.Client
	st.Databases = []state.Database{{Name: "test-db", Namespace: "test"}}
	ctx := context.Background()
	if err := c.ensureApplicationIsolation(ctx, st); err != nil {
		t.Fatal(err)
	}
	e := st.Databases[0].ApplicationPolicies[0]
	live := &networkingv1.NetworkPolicy{}
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.Name}, live); err != nil {
		t.Fatal(err)
	}
	if err := c.Client.Delete(ctx, live); err != nil {
		t.Fatal(err)
	}
	live.ResourceVersion = ""
	live.UID = "foreign-replacement"
	if err := c.Client.(policyUIDClient).Client.Create(ctx, live); err != nil {
		t.Fatal(err)
	}
	if err := c.ensureApplicationIsolation(ctx, st); !errors.Is(err, ErrOwnership) {
		t.Fatalf("replacement policy adopted: %v", err)
	}
	// Remove fixture physical database Pods so cleanup reaches ownership checks.
	if err := c.Client.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace("lab")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupIsolation(ctx, st); !errors.Is(err, ErrOwnership) {
		t.Fatalf("replacement policy deleted: %v", err)
	}
}
