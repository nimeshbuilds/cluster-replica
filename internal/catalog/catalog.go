// Package catalog owns exact upstream versions and their values translation.
package catalog

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"time"

	v1alpha1 "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
)

const (
	Profile      = "vcluster-0.37.1-lab"
	ChartVersion = "0.37.1"
	ChartURL     = "https://charts.loft.sh/charts/vcluster-0.37.1.tgz"
	ChartSHA256  = "afb57fb5f2e3088519ffa9112fa0bfc3543bc3465f86f7e7e655ba30aea0d969"
	GuestVersion = "v1.36.0"
	OwnerLabel   = "replica.nimeshbuilds.dev/uid"
)

var ttlPattern = regexp.MustCompile(`^([1-9][0-9]{0,3}m|[1-9][0-9]{0,2}h)$`)

func Validate(spec v1alpha1.ClusterReplicaSpec) (time.Duration, error) {
	if spec.Profile != Profile {
		return 0, fmt.Errorf("unknown profile: use %s", Profile)
	}
	if spec.CleanupPolicy != "HelmReleaseOnly" {
		return 0, fmt.Errorf("cleanupPolicy must acknowledge HelmReleaseOnly")
	}
	ttl, err := time.ParseDuration(spec.TTL)
	if err != nil || !ttlPattern.MatchString(spec.TTL) || ttl < 5*time.Minute || ttl > 168*time.Hour {
		return 0, fmt.Errorf("ttl must be a whole number of minutes or hours between 5m and 168h")
	}
	return ttl, nil
}

// The name is derived from the immutable Kubernetes UID, never from a reusable CR name.
func Resolve(uid string) v1alpha1.RuntimeReference {
	hash := sha256.Sum256([]byte(uid))
	return v1alpha1.RuntimeReference{
		ReleaseName: fmt.Sprintf("cr-%x", hash[:12]), Profile: Profile,
		ChartVersion: ChartVersion, ChartSHA256: ChartSHA256,
		GuestKubernetesVersion: GuestVersion,
	}
}

// Values is the only chart-specific translation surface. The lab control plane
// deliberately uses emptyDir and has no host-wide discovery permissions.
func Values(uid string) map[string]any {
	return map[string]any{
		"rbac": map[string]any{
			"clusterRole":               map[string]any{"enabled": false},
			"enableVolumeSnapshotRules": map[string]any{"enabled": false},
		},
		"controlPlane": map[string]any{
			"distro": map[string]any{"k8s": map[string]any{"enabled": true, "image": map[string]any{"tag": GuestVersion}}},
			"statefulSet": map[string]any{
				"image":       map[string]any{"repository": "loft-sh/vcluster-oss", "tag": ChartVersion},
				"labels":      map[string]any{OwnerLabel: uid},
				"pods":        map[string]any{"labels": map[string]any{OwnerLabel: uid}},
				"persistence": map[string]any{"volumeClaim": map[string]any{"enabled": false}},
			},
		},
	}
}
