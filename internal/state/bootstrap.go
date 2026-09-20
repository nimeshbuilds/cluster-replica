package state

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BootstrapKey is an explicit, one-shot installation operation. Controllers must
// never regenerate a missing key: doing so would strand the ownership inventory.
// The caller must keep this namespace administrator-only, including during setup.
func BootstrapKey(ctx context.Context, c client.Client, namespace string) error {
	if namespace == "" {
		return errors.New("state namespace is required")
	}
	key := client.ObjectKey{Namespace: namespace, Name: KeySecret}
	existing := &corev1.Secret{}
	if err := c.Get(ctx, key, existing); err == nil {
		return validateBootstrapKey(existing)
	} else if !apierrors.IsNotFound(err) {
		return errors.New("cannot read protected state key")
	}
	// Do not turn a lost key into a new, apparently healthy installation. Check
	// both the durable record name and payload, including unlabelled records.
	records := &corev1.SecretList{}
	if err := c.List(ctx, records, client.InNamespace(namespace)); err != nil {
		return errors.New("cannot check for existing protected state")
	}
	for _, record := range records.Items {
		if record.Name == KeySecret {
			return validateBootstrapKey(&record)
		}
		if strings.HasPrefix(record.Name, "replicove-state-") || len(record.Data["sealed"]) != 0 {
			return errors.New("protected state exists without its key; restore the original key from backup")
		}
	}
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return errors.New("cannot generate protected state key")
	}
	immutable := true
	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: KeySecret, Namespace: namespace,
			Labels:      map[string]string{"replicove.nimeshbuilds.dev/infrastructure": "true"},
			Annotations: map[string]string{"helm.sh/resource-policy": "keep"}},
		Type: corev1.SecretTypeOpaque, Immutable: &immutable, Data: map[string][]byte{"key": data},
	}
	if err := c.Create(ctx, obj); apierrors.IsAlreadyExists(err) {
		// A concurrent installer won; validate its key without modifying it.
		if err := c.Get(ctx, key, existing); err != nil {
			return errors.New("cannot read protected state key after concurrent bootstrap")
		}
		return validateBootstrapKey(existing)
	} else if err != nil {
		return errors.New("cannot create protected state key")
	}
	return nil
}

func validateBootstrapKey(secret *corev1.Secret) error {
	if secret.Immutable == nil || !*secret.Immutable || len(secret.Data["key"]) != 32 || secret.Type != corev1.SecretTypeOpaque {
		return errors.New("existing protected state key must be an immutable Opaque Secret with a 32-byte key; refusing to replace it")
	}
	return nil
}
