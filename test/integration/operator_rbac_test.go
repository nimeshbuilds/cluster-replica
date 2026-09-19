//go:build integration

package integration

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Exercise Kubernetes' authorization and privilege-escalation admission with
// the installed chart's actual identity, including successful bounded grants.
func assertOperatorAuthorization(t *testing.T, admin *rest.Config) {
	t.Helper()
	config := rest.CopyConfig(admin)
	config.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:replicove-system:replicove"}
	op, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Run("destination mutation allowed", func(t *testing.T) {
		_, err := op.CoreV1().ConfigMaps("replica-lab").Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "rbac-proof"}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("bounded credential delegation allowed", func(t *testing.T) {
		_, err := op.RbacV1().Roles("replica-lab").Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "rbac-proof"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"one-session"}, Verbs: []string{"get"}}}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = op.RbacV1().RoleBindings("replica-lab").Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "rbac-proof"}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "rbac-proof"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: "fixture-reader"}}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("source read allowed and writes forbidden", func(t *testing.T) {
		if _, err := op.CoreV1().ConfigMaps("source-dev").List(ctx, metav1.ListOptions{}); err != nil {
			t.Fatal(err)
		}
		_, err := op.CoreV1().ConfigMaps("source-dev").Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "forbidden"}}, metav1.CreateOptions{})
		mustForbid(t, err)
	})
	t.Run("ungranted namespace secrets forbidden", func(t *testing.T) {
		_, err := op.CoreV1().Secrets("kube-system").List(ctx, metav1.ListOptions{})
		mustForbid(t, err)
	})
	t.Run("cluster mutation forbidden", func(t *testing.T) {
		_, err := op.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "forbidden"}}, metav1.CreateOptions{})
		mustForbid(t, err)
		_, err = op.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "forbidden"}}, metav1.CreateOptions{})
		mustForbid(t, err)
	})
	t.Run("wildcard role escalation forbidden", func(t *testing.T) {
		_, err := op.RbacV1().Roles("replica-lab").Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "forbidden"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}}}}, metav1.CreateOptions{})
		mustForbid(t, err)
	})
	t.Run("cluster admin binding forbidden", func(t *testing.T) {
		_, err := op.RbacV1().RoleBindings("replica-lab").Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "forbidden"}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: "fixture-reader"}}}, metav1.CreateOptions{})
		mustForbid(t, err)
	})
}

func mustForbid(t *testing.T, err error) {
	t.Helper()
	if !apierrors.IsForbidden(err) {
		t.Fatalf("wanted an authorization denial, got %v", err)
	}
}
