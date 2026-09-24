package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapabilityOriginHostAndReadOnly(t *testing.T) {
	calls := 0
	token := strings.Repeat("a", 64)
	handler := Handler(func(context.Context) Snapshot { calls++; return Snapshot{Namespace: "lab"} }, token, "127.0.0.1:8181")
	for _, tt := range []struct {
		name, method, host, origin, token string
		want                              int
	}{{"valid", "GET", "127.0.0.1:8181", "", token, 200}, {"no capability", "GET", "127.0.0.1:8181", "", "", 401}, {"cross origin", "GET", "127.0.0.1:8181", "https://example.com", token, 403}, {"dns rebinding", "GET", "attacker.example:8181", "", token, 403}, {"mutation", "POST", "127.0.0.1:8181", "", token, 405}} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "http://"+tt.host+"/api/status", nil)
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("X-Replicove-Token", tt.token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status %d", w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("data may be cached")
			}
		})
	}
	if calls != 1 {
		t.Fatal("unauthenticated request reached Kubernetes")
	}
}

func TestUIHasNoInlineCodeOrHTMLInjectionSink(t *testing.T) {
	if strings.Contains(string(page), "<script>") {
		t.Fatal("inline scripts violate CSP")
	}
	for _, sink := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "eval("} {
		if strings.Contains(string(script), sink) {
			t.Fatalf("unsafe UI sink: %s", sink)
		}
	}
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8181"+path, nil)
		Handler(nil, strings.Repeat("x", 32), "127.0.0.1:8181").ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("static asset security headers missing")
		}
	}
}

func TestEmptyNamespaceCannotReadCluster(t *testing.T) {
	got := Load(context.Background(), nil, "")
	if len(got.Warnings) != 1 || len(got.Replicas) != 0 {
		t.Fatal("empty namespace must reject before API access")
	}
}
