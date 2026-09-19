package workflow

import (
	"context"
	"reflect"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (w *Engine) follow(ctx context.Context, obj *api.ClusterReplica, grant *api.ReplicaGrant, conn *target.Connection, st *state.State) error {
	changed := false
	for i := range st.Plan.Objects {
		desired := &st.Plan.Objects[i]
		if desired.APIVersion != "v1" || desired.Kind != "Secret" || desired.SourceUID == "" {
			continue
		}
		source := &unstructured.Unstructured{}
		source.SetAPIVersion("v1")
		source.SetKind("Secret")
		if err := w.Client.Get(ctx, client.ObjectKey{Namespace: desired.SourceNamespace, Name: desired.SourceName}, source); err != nil {
			return failure("SecretFollowUnavailable", "A followed source Secret is unavailable; its previous guest value was retained.")
		}
		if !planner.CredentialAllowed(source, grant, obj.Spec.Replication) {
			return failure("SecretFollowDenied", "A followed Secret is no longer allowed.")
		}
		transformed, err := planner.Transform(source, obj.Spec.Replication)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(desired.Desired, transformed.Object) {
			desired.Desired = transformed.Object
			desired.SourceVersion = source.GetResourceVersion()
			desired.SourceUID = string(source.GetUID())
			changed = true
		}
	}
	if changed {
		if err := w.Store.Save(ctx, st); err != nil {
			return err
		}
	}
	for _, desired := range st.Plan.Objects {
		if desired.Kind == "Secret" && desired.SourceUID != "" {
			if _, err := w.apply(ctx, conn, st, desired, true); err != nil {
				return err
			}
		}
	}
	return nil
}
