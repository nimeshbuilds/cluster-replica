package testrun

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessUsesOnlyChildKubeconfigAndDirectArguments(t *testing.T) {
	t.Setenv("KUBECONFIG", "/host/config")
	var output bytes.Buffer
	code, err := Process(&output, &output)(context.Background(), []string{"/bin/sh", "-c", `printf '%s\n%s' "$KUBECONFIG" "$1"`, "fixture", "literal; $(never-execute)"}, "/private/test-config")
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	if output.String() != "/private/test-config\nliteral; $(never-execute)" {
		t.Fatal(output.String())
	}
	if os.Getenv("KUBECONFIG") != "/host/config" {
		t.Fatal("mutated parent kubeconfig")
	}
}

func TestProcessCancellationStopsChildProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("release platforms use process groups")
	}
	marker := filepath.Join(t.TempDir(), "child-finished")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	code, err := Process(nil, nil)(ctx, []string{"/bin/sh", "-c", `(sleep 0.4; touch "$1") & wait`, "fixture", marker}, "/private/test-config")
	if err == nil || code == 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation was not bounded: %d %v", code, err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("background process survived cancellation")
	}
}

func TestProcessErrorsDoNotDiscloseExecutableOrArguments(t *testing.T) {
	_, err := Process(nil, nil)(context.Background(), []string{"/not-present/sensitive-path", "secret-token"}, "/private/test-config")
	if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSuccessfulProcessDoesNotLeaveBackgroundChildren(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("release platforms use process groups")
	}
	marker := filepath.Join(t.TempDir(), "child-finished")
	code, err := Process(nil, nil)(context.Background(), []string{"/bin/sh", "-c", `(sleep 0.3; touch "$1") & exit 0`, "fixture", marker}, "/private/test-config")
	if err != nil || code != 0 {
		t.Fatalf("%d %v", code, err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("background child outlived successful command")
	}
}
