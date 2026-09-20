package state

import (
	"bytes"
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBootstrapKeyPreservesEncryptedState(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	ctx := context.Background()
	if err := BootstrapKey(ctx, c, "protected"); err != nil {
		t.Fatal(err)
	}
	obj := &corev1.Secret{}
	key := client.ObjectKey{Namespace: "protected", Name: KeySecret}
	if err := c.Get(ctx, key, obj); err != nil {
		t.Fatal(err)
	}
	initial := append([]byte(nil), obj.Data["key"]...)
	if len(initial) != 32 || bytes.Equal(initial, make([]byte, 32)) || !*obj.Immutable {
		t.Fatal("key is not a random immutable 256-bit value")
	}
	store := &Store{Client: c, Namespace: "protected"}
	if err := store.Save(ctx, &State{OwnerUID: "test-owner", OwnerName: "demo", OwnerNamespace: "lab"}); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapKey(ctx, c, "protected"); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, obj); err != nil || !bytes.Equal(initial, obj.Data["key"]) {
		t.Fatal("bootstrap changed the key")
	}
	if _, err := store.Load(ctx, "test-owner"); err != nil {
		t.Fatal("reinstallation made encrypted state unreadable")
	}
	if err := c.Delete(ctx, obj); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapKey(ctx, c, "protected"); err == nil {
		t.Fatal("regenerated a lost key despite existing protected state")
	}
}

func TestBootstrapRejectsInvalidKeyWithoutReplacing(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	for _, size := range []int{0, 31, 32} {
		// The 32-byte case is deliberately mutable and must also be refused.
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: KeySecret, Namespace: "protected"},
			Data:       map[string][]byte{"key": bytes.Repeat([]byte{1}, size)},
		}).Build()
		if err := BootstrapKey(context.Background(), c, "protected"); err == nil {
			t.Fatalf("accepted invalid key of size %d", size)
		}
		obj := &corev1.Secret{}
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: "protected", Name: KeySecret}, obj); err != nil || len(obj.Data["key"]) != size {
			t.Fatal("changed an invalid key")
		}
	}
}
