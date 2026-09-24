// Package database prepares isolated PostgreSQL copies before application access.
package database

import (
	"errors"
	"net"
	"regexp"
	"strings"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
	"k8s.io/apimachinery/pkg/util/validation"
)

var ErrDenied = errors.New("database request is outside the explicit database grant")
var ErrFailed = errors.New("database preparation failed; recreate the replica after checking administrator configuration")
var ErrOwnership = errors.New("database resource ownership could not be verified")
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
var imageDigest = regexp.MustCompile(`^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$`)

// Resolve validates policy without reading credentials or changing any resources.
func Resolve(copy api.DatabaseCopy, grants []api.DatabaseGrant, protectedNamespace string) (api.DatabaseGrant, error) {
	if len(copy.Name) > 35 || len(validation.IsDNS1123Label(copy.Name)) > 0 || len(validation.IsDNS1123Label(copy.Namespace)) > 0 {
		return api.DatabaseGrant{}, ErrDenied
	}
	var found *api.DatabaseGrant
	for i := range grants {
		if grants[i].Name == copy.Grant {
			if found != nil {
				return api.DatabaseGrant{}, ErrDenied
			}
			found = &grants[i]
		}
	}
	if found == nil {
		return api.DatabaseGrant{}, ErrDenied
	}
	g := *found
	if g.Port == 0 {
		g.Port = 5432
	}
	if g.SSLMode == "" {
		g.SSLMode = "verify-full"
	}
	if g.TimeoutSeconds == 0 {
		g.TimeoutSeconds = 300
	}
	if !identifier.MatchString(g.Database) || g.Database == "postgres" || g.Database == "template0" || g.Database == "template1" || g.Port < 1 || g.Port > 65535 || !imageDigest.MatchString(g.Image) || !g.NetworkPolicyEnforced || g.StorageClass == "" || g.MaxBytes < 16<<20 || g.MaxBytes > 4<<30 || g.TimeoutSeconds < 30 || g.TimeoutSeconds > 1800 || (g.SSLMode != "require" && g.SSLMode != "verify-full") {
		return g, ErrDenied
	}
	if g.CredentialsSecret.Namespace != protectedNamespace || len(validation.IsDNS1123Subdomain(g.CredentialsSecret.Name)) > 0 || g.SourceNamespace == "" {
		return g, ErrDenied
	}
	if net.ParseIP(g.Host) == nil && len(validation.IsDNS1123Subdomain(g.Host)) > 0 {
		return g, ErrDenied
	}
	if len(g.Masking) == 0 || len(g.Masking) > 128 || len(g.Relationships) > 64 || len(g.Subsets) > 64 {
		return g, ErrDenied
	}
	for _, filter := range g.Subsets {
		if !validColumn(filter.DatabaseColumn) || len(filter.Equals) > 256 || strings.ContainsRune(filter.Equals, 0) {
			return g, ErrDenied
		}
	}
	masks := map[api.DatabaseColumn]api.DatabaseMask{}
	for _, m := range g.Masking {
		if !validColumn(m.DatabaseColumn) {
			return g, ErrDenied
		}
		if _, ok := masks[m.DatabaseColumn]; ok {
			return g, ErrDenied
		}
		masks[m.DatabaseColumn] = m
		switch m.Strategy {
		case "Token":
			if !identifier.MatchString(m.Domain) || m.Value != "" {
				return g, ErrDenied
			}
		case "Null":
			if m.Domain != "" || m.Value != "" {
				return g, ErrDenied
			}
		case "Constant":
			if m.Domain != "" || len(m.Value) > 256 || strings.ContainsRune(m.Value, 0) {
				return g, ErrDenied
			}
		default:
			return g, ErrDenied
		}
	}
	for _, r := range g.Relationships {
		if !validColumn(r.From) || !validColumn(r.To) {
			return g, ErrDenied
		}
		a, aok := masks[r.From]
		b, bok := masks[r.To]
		if aok != bok || aok && (a.Strategy != "Token" || b.Strategy != "Token" || a.Domain != b.Domain) {
			return g, ErrDenied
		}
	}
	return g, nil
}
func validColumn(c api.DatabaseColumn) bool {
	return identifier.MatchString(c.Schema) && identifier.MatchString(c.Table) && identifier.MatchString(c.Column) && c.Schema != "information_schema" && !strings.HasPrefix(c.Schema, "pg_")
}

// ValidatePlan rejects existing application state, shared targets, and collisions.
// Database resources are synthesized before the normal plan is applied.
func ValidatePlan(copies []api.DatabaseCopy, st *state.State) error {
	if len(copies) == 0 {
		return nil
	}
	if len(copies) > 4 || st.Provider != "helm" || st.MirrorRunUID != "" || st.Plan == nil {
		return ErrDenied
	}
	seen := map[string]bool{}
	for _, c := range copies {
		key := c.Namespace + "/" + c.Name
		if seen[key] {
			return ErrDenied
		}
		seen[key] = true
		present := false
		for _, o := range st.Plan.Objects {
			if o.Namespace != c.Namespace {
				continue
			}
			present = true
			if (o.Kind == "Secret" || o.Kind == "Service") && o.Name == c.Name || strings.HasPrefix(o.Name, "replicove-db-"+c.Name) {
				return ErrDenied
			}
		}
		if !present {
			return ErrDenied
		}
	}
	for _, d := range st.Databases {
		if d.PlanRevision != st.Plan.Revision {
			return ErrDenied
		}
	}
	if len(st.Databases) == 0 {
		for _, e := range st.Entries {
			if !e.Deleted && e.Kind != "Namespace" {
				return ErrDenied
			}
		}
	}
	return nil
}
