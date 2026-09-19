package state

import (
	"bytes"
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestCaptureAuthenticationAndBounds(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := []byte(`{"password":"do-not-export"}`)
	sealed, err := Seal(key, "owner-a", plain, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("do-not-export")) {
		t.Fatal("plaintext leaked")
	}
	got, err := Open(key, "owner-a", sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := Open(key, "owner-b", sealed); err != ErrIntegrity {
		t.Fatal("cross-owner replay accepted")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := Open(key, "owner-a", sealed); err != ErrIntegrity {
		t.Fatal("tampering accepted")
	}
	if _, err := Seal(key, "owner-a", make([]byte, MaxPlainBytes+1), 1024); err != ErrTooLarge {
		t.Fatal("unbounded capture accepted")
	}
}

func TestStorePersistsAndProtectsIdentity(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "private", Name: KeySecret}, Data: map[string][]byte{"key": bytes.Repeat([]byte{4}, 32)}}).Build()
	s := &Store{Client: c, Namespace: "private"}
	ctx := context.Background()
	st := &State{OwnerUID: "a", OwnerNamespace: "team", OwnerName: "demo", Plan: &Plan{Notes: []string{"sensitive"}}}
	if err := s.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(ctx, "a")
	if err != nil || got.Plan.Notes[0] != "sensitive" {
		t.Fatalf("load: %v", err)
	}
	got.AppliedRevision = "revision"
	if err := s.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load(ctx, "a"); err != nil || got != nil {
		t.Fatal("state not deleted")
	}
}
