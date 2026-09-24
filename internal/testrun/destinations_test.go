package testrun

import (
	"context"
	"errors"
	"testing"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type destinationClient struct {
	client.Client
	denied string
}

func (k destinationClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	var options client.ListOptions
	for _, opt := range opts {
		opt.ApplyToList(&options)
	}
	if options.Namespace == k.denied {
		return errors.New("not granted")
	}
	return k.Client.List(ctx, list, opts...)
}
func TestDestinationSelectionStaysInExplicitReadablePool(t *testing.T) {
	_, f := runnerFixture(t)
	busy := &api.ClusterReplica{ObjectMeta: metav1.ObjectMeta{Namespace: "busy", Name: "existing"}}
	if err := f.Client.Create(context.Background(), busy); err != nil {
		t.Fatal(err)
	}
	members := []Destination{{Namespace: "busy", Grant: "first"}, {Namespace: "denied", Grant: "second"}, {Namespace: "available", Grant: "third"}}
	selected, err := SelectDestination(context.Background(), destinationClient{Client: f, denied: "denied"}, members)
	if err != nil || selected.Namespace != "available" || selected.Grant != "third" {
		t.Fatalf("%+v %v", selected, err)
	}
	if _, err := SelectDestination(context.Background(), destinationClient{Client: f, denied: "denied"}, members[1:2]); err == nil {
		t.Fatal("chose unreadable namespace")
	}
}

func TestDestinationRecipeValidation(t *testing.T) {
	r := recipeFixture()
	r.Replica.GrantRef = ""
	r.Destinations = []Destination{{Namespace: "test-a", Grant: "source-a"}, {Namespace: "test-b", Grant: "source-b"}}
	if err := r.DefaultAndValidate(); err != nil {
		t.Fatal(err)
	}
	r.Destinations[1].Namespace = "test-a"
	if err := r.DefaultAndValidate(); err == nil {
		t.Fatal("duplicate destination")
	}
}
