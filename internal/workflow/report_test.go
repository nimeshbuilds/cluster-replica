package workflow

import (
	"context"
	"fmt"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/database"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
)

func TestDatabaseStatusPreservesDiagnosticWithoutPayload(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{{database.ErrDenied, "DatabasePolicyDenied"}, {database.ErrFailed, "DatabasePreparationFailed"}, {database.ErrOwnership, "DatabaseOwnershipChanged"}} {
		t.Run(tc.reason, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = api.AddToScheme(scheme)
			obj := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "lab", UID: "fixture"}}
			k := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
			engine := &Engine{Client: k}
			if _, err := engine.report(context.Background(), obj, "PreparingData", fmt.Errorf("opaque-source-payload: %w", tc.err), false); err != nil {
				t.Fatal(err)
			}
			if len(obj.Status.Conditions) != 1 || obj.Status.Conditions[0].Reason != tc.reason || strings.Contains(obj.Status.Conditions[0].Message, "opaque-source-payload") {
				t.Fatalf("unsafe or unusable diagnostic: %#v", obj.Status.Conditions)
			}
		})
	}
}
