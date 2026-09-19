package catalog

import (
	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"testing"
)

func TestValidateLifetimeAndExplicitCleanupContract(t *testing.T) {
	for _, ttl := range []string{"5m", "1h", "168h", "9999m"} {
		if _, err := Validate(v1alpha1.ClusterReplicaSpec{Profile: Profile, TTL: ttl, CleanupPolicy: "HelmReleaseOnly"}); err != nil {
			t.Errorf("valid %s: %v", ttl, err)
		}
	}
	for _, ttl := range []string{"", "4m", "169h", "-1h", "1h30m", "0h", "1.5h", "99999m"} {
		if _, err := Validate(v1alpha1.ClusterReplicaSpec{Profile: Profile, TTL: ttl, CleanupPolicy: "HelmReleaseOnly"}); err == nil {
			t.Errorf("accepted invalid lifetime %q", ttl)
		}
	}
	for _, spec := range []v1alpha1.ClusterReplicaSpec{
		{Profile: "latest", TTL: "1h", CleanupPolicy: "HelmReleaseOnly"},
		{Profile: Profile, TTL: "1h", CleanupPolicy: "DeleteEverything"},
	} {
		if _, err := Validate(spec); err == nil {
			t.Fatal("accepted unsupported contract")
		}
	}
}

func TestReleaseIdentitySurvivesRestartsButNotUIDReuse(t *testing.T) {
	a, b := Resolve("uid-first"), Resolve("uid-second")
	if a != Resolve("uid-first") {
		t.Fatal("unstable identity")
	}
	if a.ReleaseName == b.ReleaseName {
		t.Fatal("UID reuse collided")
	}
	if len(a.ReleaseName) > 53 {
		t.Fatal("invalid Helm name length")
	}
}
