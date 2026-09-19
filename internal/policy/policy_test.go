package policy

import (
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestNamespaceDelegationCannotBeExpandedByRequest(t *testing.T) {
	g := &api.ReplicaGrant{ObjectMeta: metav1.ObjectMeta{Name: "team"}, Spec: api.ReplicaGrantSpec{TargetNamespace: "target", SourceNamespaces: []string{"source"}, MaxTTL: "1h"}}
	r := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Namespace: "target"}, Spec: api.ClusterReplicaSpec{GrantRef: "team", Profile: "vcluster-0.37.1-lab", TTL: "30m", CleanupPolicy: "DeleteOwned", Replication: &api.ReplicationSpec{}}}
	if _, err := Resolve(g, r, "private"); err != nil {
		t.Fatal(err)
	}
	r.Spec.Replication.Namespaces = []string{"other"}
	if _, err := Resolve(g, r, "private"); err == nil {
		t.Fatal("cross-namespace grant expansion accepted")
	}
	r.Spec.Replication.Namespaces = nil
	r.Spec.TTL = "2h"
	if _, err := Resolve(g, r, "private"); err == nil {
		t.Fatal("TTL grant expansion accepted")
	}
	r.Spec.TTL = "30m"
	r.Spec.Replication.HelmReleases = []api.NamespacedName{{Namespace: "source", Name: "credentials"}}
	if _, err := Resolve(g, r, "private"); err == nil {
		t.Fatal("ungranted Helm secrets accepted")
	}
}
func TestPlatformFailureCannotFallBackToHelm(t *testing.T) {
	g := &api.ReplicaGrant{ObjectMeta: metav1.ObjectMeta{Name: "team"}, Spec: api.ReplicaGrantSpec{TargetNamespace: "target", SourceNamespaces: []string{"source"}, DefaultProvider: "platform"}}
	r := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Namespace: "target"}, Spec: api.ClusterReplicaSpec{GrantRef: "team", Profile: "vcluster-0.37.1-lab", TTL: "30m", CleanupPolicy: "DeleteOwned", Target: &api.TargetSpec{Provider: "helm"}}}
	if _, err := Resolve(g, r, "private"); err == nil {
		t.Fatal("Platform policy bypass accepted")
	}
	r.Spec.Target.Provider = "auto"
	if _, err := Resolve(g, r, "private"); err == nil {
		t.Fatal("missing Platform configuration accepted")
	}
}
