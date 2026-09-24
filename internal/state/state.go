// Package state stores captures and durable ownership records in an administrator-only namespace.
package state

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kjson "sigs.k8s.io/json"
)

const (
	SchemaVersion   = 1
	KeySecret       = "replicove-state-key"
	MaxPlainBytes   = 16 << 20
	DefaultMaxBytes = 512 << 10
)

var (
	ErrUnavailable = errors.New("protected state unavailable")
	ErrIntegrity   = errors.New("protected state integrity check failed")
	ErrTooLarge    = errors.New("capture exceeds protected storage limit")
)

type Object struct {
	SelectionReason string         `json:"selectionReason,omitempty"`
	Transformations []string       `json:"transformations,omitempty"`
	ID              string         `json:"id"`
	APIVersion      string         `json:"apiVersion"`
	Kind            string         `json:"kind"`
	Resource        string         `json:"resource"`
	SourceNamespace string         `json:"sourceNamespace,omitempty"`
	SourceName      string         `json:"sourceName"`
	SourceUID       string         `json:"sourceUID"`
	SourceVersion   string         `json:"sourceVersion"`
	Namespace       string         `json:"namespace,omitempty"`
	Name            string         `json:"name"`
	Desired         map[string]any `json:"desired"`
	Dependencies    []string       `json:"dependencies,omitempty"`
}

type Package struct {
	ID              string         `json:"id"`
	SourceNamespace string         `json:"sourceNamespace"`
	SourceName      string         `json:"sourceName"`
	SourceRevision  int            `json:"sourceRevision"`
	Namespace       string         `json:"namespace"`
	Name            string         `json:"name"`
	ChartName       string         `json:"chartName"`
	ChartVersion    string         `json:"chartVersion"`
	ChartArchive    []byte         `json:"chartArchive"`
	Values          map[string]any `json:"values"`
	Dependencies    []string       `json:"dependencies,omitempty"`
}

type Plan struct {
	ExternalDependencies []string         `json:"externalDependencies,omitempty"`
	Omitted              map[string]int32 `json:"omitted,omitempty"`
	Revision             string           `json:"revision"`
	CapturedAt           time.Time        `json:"capturedAt"`
	SourceVersion        string           `json:"sourceVersion"`
	Objects              []Object         `json:"objects"`
	Packages             []Package        `json:"packages,omitempty"`
	Order                []string         `json:"order"`
	Notes                []string         `json:"notes,omitempty"`
}

type Entry struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	APIVersion  string         `json:"apiVersion,omitempty"`
	Resource    string         `json:"resource,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Name        string         `json:"name"`
	UID         string         `json:"uid,omitempty"`
	OperationID string         `json:"operationID"`
	Deleted     bool           `json:"deleted,omitempty"`
	Applied     map[string]any `json:"applied,omitempty"`
}

type Volume struct {
	Name     string `json:"name"`
	UID      string `json:"uid"`
	ClaimUID string `json:"claimUID"`
}

type State struct {
	Capacity   *Capacity   `json:"capacity,omitempty"`
	Databases  []Database  `json:"databases,omitempty"`
	Experiment *Experiment `json:"experiment,omitempty"`
	Mirror     *Mirror     `json:"mirror,omitempty"`
	MirrorRun  *MirrorRun  `json:"mirrorRun,omitempty"`
	// Nonempty only on controller-prepared generation children. Their plan is pinned.
	MirrorRunUID          string   `json:"mirrorRunUID,omitempty"`
	Volumes               []Volume `json:"volumes,omitempty"`
	Version               int      `json:"version"`
	OwnerUID              string   `json:"ownerUID"`
	OwnerNamespace        string   `json:"ownerNamespace"`
	OwnerName             string   `json:"ownerName"`
	GrantUID              string   `json:"grantUID,omitempty"`
	GrantVersion          string   `json:"grantVersion,omitempty"`
	Provider              string   `json:"provider,omitempty"`
	Plan                  *Plan    `json:"plan,omitempty"`
	Entries               []Entry  `json:"entries,omitempty"`
	HostEntries           []Entry  `json:"hostEntries,omitempty"`
	TargetClusterUID      string   `json:"targetClusterUID,omitempty"`
	TargetSecretNamespace string   `json:"targetSecretNamespace,omitempty"`
	TargetSecretName      string   `json:"targetSecretName,omitempty"`
	RuntimeRootUID        string   `json:"runtimeRootUID,omitempty"`
	RuntimeWorkloadUID    string   `json:"runtimeWorkloadUID,omitempty"`
	RefreshToken          string   `json:"refreshToken,omitempty"`
	AppliedRevision       string   `json:"appliedRevision,omitempty"`
	CleanupStarted        bool     `json:"cleanupStarted,omitempty"`
	GuestCleaned          bool     `json:"guestCleaned,omitempty"`
	// resourceVersion is an optimistic concurrency token, never part of the payload.
	AccessExpiresAt     time.Time `json:"accessExpiresAt,omitempty"`
	CredentialSecretUID string    `json:"credentialSecretUID,omitempty"`
	ReplicaUID          string    `json:"replicaUID,omitempty"`
	RuntimeName         string    `json:"runtimeName,omitempty"`
	resourceVersion     string
}

type Store struct {
	Client    client.Client
	Namespace string
	MaxBytes  int
}

func Name(uid string) string {
	sum := sha256.Sum256([]byte(uid))
	return "replicove-state-" + hex.EncodeToString(sum[:12])
}

func (s *Store) key(ctx context.Context) ([]byte, error) {
	obj := &corev1.Secret{}
	if s.Namespace == "" || s.Client == nil {
		return nil, ErrUnavailable
	}
	if err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: KeySecret}, obj); err != nil {
		return nil, ErrUnavailable
	}
	if len(obj.Data["key"]) != 32 {
		return nil, ErrUnavailable
	}
	return obj.Data["key"], nil
}

// Seal authenticates both ciphertext and its immutable owner identity. Compression
// precedes encryption and is independently bounded on decode.
func Seal(key []byte, uid string, plain []byte, maxBytes int) ([]byte, error) {
	if len(plain) > MaxPlainBytes {
		return nil, ErrTooLarge
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write(plain); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{SchemaVersion}, nonce...)
	out = aead.Seal(out, nonce, compressed.Bytes(), []byte("replicove/v1/"+uid))
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if len(out) > maxBytes {
		return nil, ErrTooLarge
	}
	return out, nil
}

func Open(key []byte, uid string, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	n := aead.NonceSize()
	if len(data) < 1+n || data[0] != SchemaVersion {
		return nil, ErrIntegrity
	}
	compressed, err := aead.Open(nil, data[1:1+n], data[1+n:], []byte("replicove/v1/"+uid))
	if err != nil {
		return nil, ErrIntegrity
	}
	z, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, ErrIntegrity
	}
	defer z.Close()
	plain, err := io.ReadAll(io.LimitReader(z, MaxPlainBytes+1))
	if err != nil {
		return nil, ErrIntegrity
	}
	if len(plain) > MaxPlainBytes {
		return nil, ErrTooLarge
	}
	return plain, nil
}

func (s *Store) Revision(ctx context.Context, value any) (string, error) {
	key, err := s.key(ctx)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *Store) Load(ctx context.Context, uid string) (*State, error) {
	obj := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: Name(uid)}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, ErrUnavailable
	}
	key, err := s.key(ctx)
	if err != nil {
		return nil, err
	}
	data, err := Open(key, uid, obj.Data["sealed"])
	if err != nil {
		return nil, err
	}
	value := &State{}
	if err := kjson.UnmarshalCaseSensitivePreserveInts(data, value); err != nil {
		return nil, ErrIntegrity
	}
	if value.OwnerUID != uid || value.Version != SchemaVersion {
		return nil, ErrIntegrity
	}
	value.resourceVersion = obj.ResourceVersion
	return value, nil
}

func (s *Store) Save(ctx context.Context, value *State) error {
	if value.OwnerUID == "" || value.OwnerNamespace == "" || value.OwnerName == "" {
		return ErrIntegrity
	}
	key, err := s.key(ctx)
	if err != nil {
		return err
	}
	value.Version = SchemaVersion
	plain, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sealed, err := Seal(key, value.OwnerUID, plain, s.MaxBytes)
	if err != nil {
		return err
	}
	obj := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: Name(value.OwnerUID), ResourceVersion: value.resourceVersion, Labels: map[string]string{"app.kubernetes.io/managed-by": "replicove", "replicove.nimeshbuilds.dev/owner": value.OwnerUID}}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"sealed": sealed}}
	if value.resourceVersion == "" {
		err = s.Client.Create(ctx, obj)
	} else {
		err = s.Client.Update(ctx, obj)
	}
	if err != nil {
		if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) {
			return err
		}
		return ErrUnavailable
	}
	value.resourceVersion = obj.ResourceVersion
	return nil
}

func (s *Store) Delete(ctx context.Context, uid string) error {
	obj := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: Name(uid)}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return ErrUnavailable
	}
	// Authenticate the ownership record before deleting even this private object.
	if _, err := s.Load(ctx, uid); err != nil {
		return err
	}
	if err := s.Client.Delete(ctx, obj, client.Preconditions{UID: &obj.UID, ResourceVersion: &obj.ResourceVersion}); err != nil && !apierrors.IsNotFound(err) {
		return ErrUnavailable
	}
	return nil
}

func OperationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("operation identity unavailable")
	}
	return hex.EncodeToString(b), nil
}
