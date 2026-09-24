package testrun

import (
	"context"
	"errors"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SelectDestination chooses only among explicitly configured, readable members.
// This is a placement hint. The operator's atomic admissions, runtime guard and
// host quota enforce capacity when concurrent clients choose the same member.
func SelectDestination(ctx context.Context, k client.Client, members []Destination) (Destination, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var chosen Destination
	minimum := int(^uint(0) >> 1)
	for _, member := range members {
		replicas := &api.ClusterReplicaList{}
		mirrors := &api.ReplicaMirrorList{}
		if err := k.List(ctx, replicas, client.InNamespace(member.Namespace)); err != nil {
			continue
		}
		if err := k.List(ctx, mirrors, client.InNamespace(member.Namespace)); err != nil {
			continue
		}
		count := len(replicas.Items) + len(mirrors.Items)
		if count < minimum {
			minimum = count
			chosen = member
		}
	}
	if chosen.Namespace == "" {
		return Destination{}, errors.New("none of the configured pool destinations is readable")
	}
	return chosen, nil
}
