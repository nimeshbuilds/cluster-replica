package main

import (
	"bytes"
	"context"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/testrun"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommandRequiresRecipeAndExplicitCommandSeparator(t *testing.T) {
	for _, args := range [][]string{{}, {"-f", "unused.yaml", "kubectl", "get", "pods"}} {
		cmd := (&cli{}).runCommand()
		cmd.SetArgs(args)
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestRunRejectsInvalidRecipeBeforeKubernetesOrArtifacts(t *testing.T) {
	dir := t.TempDir()
	recipe := filepath.Join(dir, "recipe.yaml")
	artifacts := filepath.Join(dir, "artifacts")
	if err := os.WriteFile(recipe, []byte("apiVersion: replicove.nimeshbuilds.dev/v1alpha1\nkind: TestRecipe\nunknown: secret-source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := (&cli{Kubeconfig: "/host/never-read"}).runCommand()
	cmd.SetArgs([]string{"-f", recipe, "--artifacts", artifacts, "--", "echo", "test"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "recipe YAML") || strings.Contains(err.Error(), "secret-source") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(artifacts); !os.IsNotExist(err) {
		t.Fatal("artifacts were created before validation")
	}
}

func TestRunHelpDocumentsRecipeAndArtifacts(t *testing.T) {
	cmd := (&cli{}).runCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--artifacts") || !strings.Contains(out.String(), "--file") {
		t.Fatal(out.String())
	}
}

func TestRunChaosTargetsResolveOnlyExactNamedGuestObjects(t *testing.T) {
	k := kubefake.NewClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "guest", Name: "app", UID: "guest-deployment"}}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "guest", Name: "pod", UID: "guest-pod"}})
	faults := []api.ChaosFault{
		{Kind: "ScaleZero", Namespace: "guest", Target: &api.ChaosTarget{Kind: "Deployment", Name: "app"}},
		{Kind: "PodDelete", Namespace: "guest", Target: &api.ChaosTarget{Kind: "Pod", Name: "pod"}},
		{Kind: "ScaleZero", Namespace: "guest", Target: &api.ChaosTarget{Kind: "Deployment", Name: "app", UID: "explicit-old-uid"}},
	}
	resolved, err := resolveRunFaultsWithClient(context.Background(), k, faults)
	if err != nil || resolved[0].Target.UID != "guest-deployment" || resolved[1].Target.UID != "guest-pod" || resolved[2].Target.UID != "explicit-old-uid" {
		t.Fatalf("%+v %v", resolved, err)
	}
	if len(k.Actions()) != 2 {
		t.Fatal("explicit UID was replaced or unrelated resources enumerated")
	}
	if _, err := resolveRunFaultsWithClient(context.Background(), k, []api.ChaosFault{{Namespace: "host-source", Target: &api.ChaosTarget{Kind: "Deployment", Name: "app"}}}); err == nil {
		t.Fatal("adopted a different namespace")
	}
}

func TestTestingRecipeExamplesRemainParseable(t *testing.T) {
	for _, name := range []string{"smoke.yaml", "mirror.yaml", "chaos.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "testing", name))
		if err != nil {
			t.Fatal(err)
		}
		r, err := testrun.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := r.DefaultAndValidate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "testing", "pool.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsePool(data); err != nil {
		t.Fatal(err)
	}
}
