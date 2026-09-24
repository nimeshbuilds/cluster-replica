// Package dashboard serves a read-only, capability-protected local view. It
// never returns captured payloads, request specs, credentials or Secret values.
package dashboard

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/agentapi"
	"github.com/nimeshbuilds/cluster-replica/internal/diagnostics"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

//go:embed index.html
var page []byte

type Item struct {
	Name    string `json:"name"`
	UID     string `json:"uid"`
	Phase   string `json:"phase"`
	Expires string `json:"expires,omitempty"`
	Message string `json:"message,omitempty"`
}
type Snapshot struct {
	Namespace   string               `json:"namespace"`
	Updated     string               `json:"updated"`
	Replicas    []diagnostics.Report `json:"replicas"`
	Mirrors     []Item               `json:"mirrors"`
	Experiments []Item               `json:"experiments"`
	Warnings    []string             `json:"warnings,omitempty"`
}

func Load(ctx context.Context, k client.Client, namespace string) Snapshot {
	s := Snapshot{Namespace: namespace, Updated: time.Now().UTC().Format(time.RFC3339), Replicas: []diagnostics.Report{}, Mirrors: []Item{}, Experiments: []Item{}}
	if err := agentapi.ValidateNamespace(namespace); err != nil {
		s.Warnings = []string{err.Error()}
		return s
	}
	replicas := &api.ClusterReplicaList{}
	if err := k.List(ctx, replicas, client.InNamespace(namespace)); err != nil {
		s.Warnings = append(s.Warnings, "Replicas are unavailable or this identity cannot list them.")
	} else {
		for i := range replicas.Items {
			s.Replicas = append(s.Replicas, diagnostics.Explain(&replicas.Items[i]))
		}
	}
	mirrors := &api.ReplicaMirrorList{}
	if err := k.List(ctx, mirrors, client.InNamespace(namespace)); err != nil {
		s.Warnings = append(s.Warnings, "Mirrors are unavailable or this identity cannot list them.")
	} else {
		for _, o := range mirrors.Items {
			item := Item{Name: o.Name, UID: string(o.UID), Phase: o.Status.Phase}
			if o.Status.ExpiresAt != nil {
				item.Expires = o.Status.ExpiresAt.UTC().Format(time.RFC3339)
			}
			s.Mirrors = append(s.Mirrors, item)
		}
	}
	experiments := &api.ReplicaExperimentList{}
	if err := k.List(ctx, experiments, client.InNamespace(namespace)); err != nil {
		s.Warnings = append(s.Warnings, "Experiments are unavailable or this identity cannot list them.")
	} else {
		for _, o := range experiments.Items {
			item := Item{Name: o.Name, UID: string(o.UID), Phase: o.Status.Phase, Message: o.Status.Message}
			if o.Status.ExpiresAt != nil {
				item.Expires = o.Status.ExpiresAt.UTC().Format(time.RFC3339)
			}
			s.Experiments = append(s.Experiments, item)
		}
	}
	return s
}

func Handler(load func(context.Context) Snapshot, token, host string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if r.Host != host {
			http.Error(w, "unrecognized host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+host {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(page)
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(script)
		case "/style.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write(styles)
		case "/api/status":
			if len(token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Replicove-Token")), []byte(token)) != 1 {
				http.Error(w, "dashboard capability required", http.StatusUnauthorized)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(load(ctx))
		default:
			http.NotFound(w, r)
		}
	})
}

//go:embed app.js
var script []byte

//go:embed style.css
var styles []byte
