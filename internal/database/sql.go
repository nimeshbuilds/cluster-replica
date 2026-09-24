package database

import (
	"fmt"
	"strings"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
)

func ident(s string) string             { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func literal(s string) string           { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }
func table(c api.DatabaseColumn) string { return ident(c.Schema) + "." + ident(c.Table) }

// MaskSQL is generated from validated identifiers and bounded literal values.
// It runs only inside the isolated staging database, never against the source.
func MaskSQL(g api.DatabaseGrant, salt string) (string, error) {
	if len(salt) != 64 {
		return "", ErrDenied
	}
	for _, c := range salt {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", ErrDenied
		}
	}
	var b strings.Builder
	b.WriteString("BEGIN;\nSET LOCAL standard_conforming_strings=on;\nSET LOCAL session_replication_role=replica;\n")
	for _, filter := range g.Subsets {
		if !validColumn(filter.DatabaseColumn) || len(filter.Equals) > 256 || strings.ContainsRune(filter.Equals, 0) {
			return "", ErrDenied
		}
		fmt.Fprintf(&b, "DELETE FROM %s WHERE %s::text IS DISTINCT FROM %s;\n", table(filter.DatabaseColumn), ident(filter.Column), literal(filter.Equals))
	}
	for _, m := range g.Masking {
		if !validColumn(m.DatabaseColumn) {
			return "", ErrDenied
		}
		col := ident(m.Column)
		expr := ""
		switch m.Strategy {
		case "Token":
			if !identifier.MatchString(m.Domain) {
				return "", ErrDenied
			}
			expr = "CASE WHEN " + col + " IS NULL THEN NULL ELSE encode(sha256(convert_to(" + literal(salt+":"+m.Domain+":") + " || " + col + "::text, 'UTF8')), 'hex') END"
		case "Null":
			expr = "NULL"
		case "Constant":
			if len(m.Value) > 256 || strings.ContainsRune(m.Value, 0) {
				return "", ErrDenied
			}
			expr = literal(m.Value)
		default:
			return "", ErrDenied
		}
		fmt.Fprintf(&b, "UPDATE %s SET %s = %s;\n", table(m.DatabaseColumn), col, expr)
	}
	b.WriteString("SET LOCAL session_replication_role=origin;\n")
	// Re-add all native foreign keys as validated constraints. Disabling FK
	// triggers during a coordinated key rewrite must not leave unchecked rows.
	b.WriteString(`DO $replicove$
DECLARE r record;
BEGIN
  FOR r IN SELECT n.nspname, t.relname, c.conname, pg_get_constraintdef(c.oid) AS definition
    FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace
    WHERE c.contype='f' AND c.conparentid=0 AND n.nspname NOT LIKE 'pg_%' AND n.nspname<>'information_schema'
  LOOP
    EXECUTE format('ALTER TABLE %I.%I DROP CONSTRAINT %I',r.nspname,r.relname,r.conname);
    EXECUTE format('ALTER TABLE %I.%I ADD CONSTRAINT %I %s',r.nspname,r.relname,r.conname,r.definition);
    EXECUTE format('ALTER TABLE %I.%I VALIDATE CONSTRAINT %I',r.nspname,r.relname,r.conname);
  END LOOP;
END $replicove$;
`)
	for _, r := range g.Relationships {
		if !validColumn(r.From) || !validColumn(r.To) {
			return "", ErrDenied
		}
		fmt.Fprintf(&b, "DO $replicove$ BEGIN IF EXISTS (SELECT 1 FROM %s f WHERE f.%s IS NOT NULL AND NOT EXISTS (SELECT 1 FROM %s t WHERE t.%s = f.%s)) THEN RAISE EXCEPTION 'relationship validation failed'; END IF; END $replicove$;\n", table(r.From), ident(r.From.Column), table(r.To), ident(r.To.Column), ident(r.From.Column))
	}
	b.WriteString("COMMIT;\n")
	return b.String(), nil
}

// OpenSQL publishes TCP access only after raw staging resources have disappeared.
// Local sockets remain trusted solely for controller exec and the readiness probe.
const OpenSQL = `COPY (SELECT line FROM (VALUES (1,'local all all trust'),(2,'host all all 0.0.0.0/0 scram-sha-256'),(3,'host all all ::0/0 scram-sha-256')) AS h(ord,line) ORDER BY ord) TO '/var/lib/postgresql/data/pgdata/pg_hba.conf';
SELECT pg_reload_conf();
`
