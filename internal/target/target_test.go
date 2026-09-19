package target

import (
	"strings"
	"testing"
)

const fixture = `apiVersion: v1
kind: Config
current-context: guest
clusters:
- name: guest
  cluster:
    server: https://guest.example
    certificate-authority-data: Y2E=
contexts:
- name: guest
  context:
    cluster: guest
    user: guest
users:
- name: guest
  user:
    token: test-only
`

func TestDataOnlyKubeconfig(t *testing.T) {
	if _, err := Parse([]byte(fixture)); err != nil {
		t.Fatal(err)
	}
	tests := []string{
		strings.Replace(fixture, "token: test-only", "exec:\n      command: arbitrary\n      apiVersion: client.authentication.k8s.io/v1\n      interactiveMode: Never", 1),
		strings.Replace(fixture, "token: test-only", "tokenFile: /tmp/token", 1),
		strings.Replace(fixture, "certificate-authority-data: Y2E=", "certificate-authority: /tmp/ca", 1),
		strings.Replace(fixture, "server: https://guest.example", "server: http://guest.example", 1),
		strings.Replace(fixture, "certificate-authority-data: Y2E=", "certificate-authority-data: Y2E=\n    insecure-skip-tls-verify: true", 1),
		strings.Replace(fixture, "token: test-only", "token: test-only\n    as: system:admin", 1),
		strings.Replace(fixture, "token: test-only", "token: test-only\n    as-uid: other-uid", 1),
		strings.Replace(fixture, "server: https://guest.example", "server: https://user:secret@guest.example", 1),
	}
	for i, data := range tests {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("unsafe kubeconfig %d accepted", i)
		}
	}
}
