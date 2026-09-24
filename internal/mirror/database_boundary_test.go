package mirror

import (
	"context"
	"errors"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/planner"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLogicalDatabaseMirrorRejectedBeforeCapture(t *testing.T) {
	r, m, g := fixture(t)
	m.Spec.Template.Replication.Databases = []api.DatabaseCopy{{Name: "accounts", Namespace: "test", Grant: "database"}}
	_, err := r.authorize(m, g)
	var problem *planner.Problem
	if !errors.As(err, &problem) || problem.Reason != "DatabaseCopyUnsupported" {
		t.Fatalf("logical/CSI mix was not rejected explicitly: %v", err)
	}
	ctx := context.Background()
	sourceStorage(t, r)
	if err := r.Client.Create(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(m)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
		t.Fatal(err)
	}
	if m.Status.Phase != "Blocked" || len(m.Finalizers) != 0 {
		t.Fatal("unsupported combination enrolled capture/cleanup authority")
	}
	st, err := r.Store.Load(ctx, string(m.UID))
	if err != nil || st != nil {
		t.Fatal("unsupported combination persisted capture intent")
	}
	for _, kind := range []string{"VolumeSnapshotList", "VolumeSnapshotContentList"} {
		list := &unstructured.UnstructuredList{}
		list.SetAPIVersion(snapshotAPI)
		list.SetKind(kind)
		if err := r.Client.List(ctx, list); err != nil {
			t.Fatal(err)
		}
		if len(list.Items) > 0 {
			t.Fatal("logical database request created source snapshots before rejection")
		}
	}
	runs := &api.ReplicaMirrorRunList{}
	if err := r.Client.List(ctx, runs); err != nil {
		t.Fatal(err)
	}
	if len(runs.Items) > 0 {
		t.Fatal("unsupported combination created a mirror run")
	}
}
