package database

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/state"
)

func grantFixture() api.DatabaseGrant {
	return api.DatabaseGrant{Name: "accounts", SourceNamespace: "source", Host: "postgres.source.svc", Database: "accounts", CredentialsSecret: api.NamespacedName{Namespace: "system", Name: "source-reader"}, Image: "postgres:17@sha256:" + strings.Repeat("a", 64), StorageClass: "standard", MaxBytes: 256 << 20, NetworkPolicyEnforced: true, Masking: []api.DatabaseMask{{DatabaseColumn: api.DatabaseColumn{Schema: "public", Table: "users", Column: "email"}, Strategy: "Token", Domain: "email"}, {DatabaseColumn: api.DatabaseColumn{Schema: "public", Table: "orders", Column: "email"}, Strategy: "Token", Domain: "email"}}, Relationships: []api.DatabaseRelationship{{From: api.DatabaseColumn{Schema: "public", Table: "orders", Column: "email"}, To: api.DatabaseColumn{Schema: "public", Table: "users", Column: "email"}}}}
}
func copyFixture() api.DatabaseCopy {
	return api.DatabaseCopy{Name: "test-db", Namespace: "test", Grant: "accounts"}
}

func TestGrantRequiresExplicitDatabaseDelegation(t *testing.T) {
	c := copyFixture()
	g := grantFixture()
	if _, err := Resolve(c, nil, "system"); !errors.Is(err, ErrDenied) {
		t.Fatal("missing explicit grant accepted")
	}
	if _, err := Resolve(c, []api.DatabaseGrant{g}, "system"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*api.DatabaseGrant){
		"credentials outside protected namespace": func(g *api.DatabaseGrant) { g.CredentialsSecret.Namespace = "source" },
		"source connstring injection":             func(g *api.DatabaseGrant) { g.Database = "host=other dbname=secret" },
		"host options injection":                  func(g *api.DatabaseGrant) { g.Host = "source,other" },
		"mutable image":                           func(g *api.DatabaseGrant) { g.Image = "postgres:17" },
		"unqualified CNI":                         func(g *api.DatabaseGrant) { g.NetworkPolicyEnforced = false },
		"missing sanitization":                    func(g *api.DatabaseGrant) { g.Masking = nil },
		"different relationship token domains":    func(g *api.DatabaseGrant) { g.Masking[1].Domain = "other" },
		"SQL expression column":                   func(g *api.DatabaseGrant) { g.Masking[0].Column = "email; DROP TABLE users" },
	} {
		t.Run(name, func(t *testing.T) {
			g := grantFixture()
			mutate(&g)
			if _, err := Resolve(c, []api.DatabaseGrant{g}, "system"); !errors.Is(err, ErrDenied) {
				t.Fatal("unsafe grant accepted")
			}
		})
	}
}

func TestMaskSQLPreservesDomainsAndValidatesConstraints(t *testing.T) {
	g := grantFixture()
	sql, err := MaskSQL(g, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sql, ":email:") != 2 {
		t.Fatal("shared domain must produce same transform")
	}
	for _, want := range []string{"BEGIN;", "session_replication_role=replica", "session_replication_role=origin", "VALIDATE CONSTRAINT", "relationship validation failed", "COMMIT;"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %s", want)
		}
	}
	g.Masking = []api.DatabaseMask{{DatabaseColumn: g.Masking[0].DatabaseColumn, Strategy: "Constant", Value: "O'Reilly\\s"}}
	sql, err = MaskSQL(g, strings.Repeat("b", 64))
	if err != nil || !strings.Contains(sql, "'O''Reilly\\s'") {
		t.Fatal("literal not safely escaped")
	}
	if _, err := MaskSQL(g, "x');DROP"); err == nil {
		t.Fatal("invalid salt accepted")
	}
}

func TestPlanRejectsRefreshCollisionsAndSharedTargets(t *testing.T) {
	good := func() *state.State {
		return &state.State{Provider: "helm", Plan: &state.Plan{Revision: "r1", Objects: []state.Object{{Namespace: "test", Kind: "Deployment", Name: "app"}}}}
	}
	if err := ValidatePlan([]api.DatabaseCopy{copyFixture()}, good()); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*state.State){"existing": func(s *state.State) { s.Provider = "existing" }, "mirror": func(s *state.State) { s.MirrorRunUID = "mirror" }, "live app": func(s *state.State) { s.Entries = []state.Entry{{Kind: "Deployment"}} }, "refresh": func(s *state.State) { s.Databases = []state.Database{{PlanRevision: "old"}} }, "secret collision": func(s *state.State) {
		s.Plan.Objects = append(s.Plan.Objects, state.Object{Namespace: "test", Kind: "Secret", Name: "test-db"})
	}} {
		t.Run(name, func(t *testing.T) {
			s := good()
			edit(s)
			if !errors.Is(ValidatePlan([]api.DatabaseCopy{copyFixture()}, s), ErrDenied) {
				t.Fatal("unsafe plan accepted")
			}
		})
	}
}

func TestTransferBoundsCancelsAndRedacts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		max        int64
		consumeErr bool
		wantErr    bool
	}{{"success", 16, false, false}, {"limit", 2, false, true}, {"consumer failure", 16, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var got bytes.Buffer
			hash, err := transfer(ctx, tc.max, func(ctx context.Context, w io.Writer) error { _, e := io.WriteString(w, "secret row"); return e }, func(ctx context.Context, r io.Reader) error {
				if tc.consumeErr {
					return errors.New("sensitive database error")
				}
				_, e := io.Copy(&got, r)
				return e
			})
			if tc.wantErr {
				if !errors.Is(err, ErrFailed) || hash != "" || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("unclassified failure: %v", err)
				}
			} else if err != nil || got.String() != "secret row" || len(hash) != 64 {
				t.Fatal("transfer failed")
			}
		})
	}
}
func TestSourceReadOnlyEnvironmentAndNoInheritedPGOptions(t *testing.T) {
	t.Setenv("PGSERVICE", "untrusted")
	t.Setenv("PGOPTIONS", "-c default_transaction_read_only=off")
	g := grantFixture()
	g.Port = 5432
	g.SSLMode = "require"
	g.TimeoutSeconds = 300
	env := strings.Join(sourceEnvironment(Source{Grant: g, Username: "reader", Password: "secret"}), "\n")
	if strings.Contains(env, "PGSERVICE") || !strings.Contains(env, "default_transaction_read_only=on") || strings.Contains(env, "PGSSLROOTCERT=system") {
		t.Fatal("unsafe source environment")
	}
	if strings.Contains(strings.Join(dumpArgs(g.Database), " "), "secret") {
		t.Fatal("password leaked into command args")
	}
}

func TestSubsetOnlyDeletesDeclaredTableWithSafeLiteral(t *testing.T) {
	g := grantFixture()
	g.Subsets = []api.DatabaseSubset{{DatabaseColumn: api.DatabaseColumn{Schema: "public", Table: "users", Column: "tenant"}, Equals: "team'one"}}
	sql, err := MaskSQL(g, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `DELETE FROM "public"."users" WHERE "tenant"::text IS DISTINCT FROM 'team''one';`) {
		t.Fatal("subset not literal equality")
	}
	if strings.Index(sql, "DELETE FROM") > strings.Index(sql, "UPDATE ") {
		t.Fatal("subset must precede masking")
	}
	if !strings.Contains(sql, "VALIDATE CONSTRAINT") {
		t.Fatal("subset must revalidate native relationships")
	}
	g.Subsets[0].Column = "tenant; DROP DATABASE application"
	if _, err := MaskSQL(g, strings.Repeat("a", 64)); err == nil {
		t.Fatal("subset accepted SQL expression")
	}
}
