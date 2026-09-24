package database

import (
	"bytes"
	"context"
	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This fixture creates only disposable containers with no network or host mounts.
// Run through examples/postgresql/test-disposable.sh on a Docker-enabled runner.
func TestPostgreSQLDisposable(t *testing.T) {
	if os.Getenv("REPLICOVE_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set REPLICOVE_POSTGRES_INTEGRATION=1 with disposable Docker available")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("Docker is required for explicitly requested integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	image := os.Getenv("REPLICOVE_POSTGRES_IMAGE")
	if image == "" {
		image = "docker.io/library/postgres:17-bookworm@sha256:639ab7ceb90e13123085b741fb31ef493fba25463002f6da665352e7b534b652"
	}
	token, err := random()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"replicove-pg-source-" + token[:10], "replicove-pg-stage-" + token[:10], "replicove-pg-final-" + token[:10]}
	call := func(input io.Reader, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = input
		cmd.Stderr = io.Discard
		return cmd.Output()
	}
	for _, name := range names {
		name := name
		t.Cleanup(func() {
			clean, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			cmd := exec.CommandContext(clean, "docker", "rm", "--force", "--volumes", name)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Run(); err != nil {
				t.Error("disposable PostgreSQL container cleanup failed")
			}
		})
		if _, err := call(nil, "run", "--detach", "--name", name, "--network", "none", "--tmpfs", "/var/lib/postgresql/data:rw,size=536870912", "--env", "POSTGRES_USER=replicove", "--env", "POSTGRES_DB=accounts", "--env", "POSTGRES_PASSWORD=disposable-fixture-password", "--env", "POSTGRES_HOST_AUTH_METHOD=reject", "--env", "POSTGRES_INITDB_ARGS=--auth-host=reject --auth-local=trust", image, "postgres", "-c", "log_min_messages=panic", "-c", "log_min_error_statement=panic"); err != nil {
			t.Fatal("could not start disposable PostgreSQL container")
		}
		ready := false
		for i := 0; i < 60; i++ {
			if _, err := call(nil, "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "replicove", "-d", "accounts"); err == nil {
				ready = true
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("PostgreSQL fixture timeout")
			case <-time.After(300 * time.Millisecond):
			}
		}
		if !ready {
			t.Fatal("disposable PostgreSQL not ready")
		}
	}
	sql := func(name, text string) []byte {
		out, err := call(strings.NewReader(text), "exec", "--interactive", name, "psql", "--no-psqlrc", "-qAt", "-U", "replicove", "-d", "accounts", "-v", "ON_ERROR_STOP=1")
		if err != nil {
			t.Fatal("fixture SQL failed (output suppressed)")
		}
		return out
	}
	sql(names[0], `CREATE TABLE public.users(id integer PRIMARY KEY,email text UNIQUE NOT NULL,tenant_id integer); CREATE TABLE public.orders(id integer PRIMARY KEY,email text REFERENCES public.users(email),tenant_id integer); INSERT INTO public.users VALUES(1,'raw-person@example.invalid',10),(2,'excluded-person@example.invalid',20); INSERT INTO public.orders VALUES(1,'raw-person@example.invalid',10),(2,'excluded-person@example.invalid',20); CREATE ROLE replica_reader; GRANT CONNECT ON DATABASE accounts TO replica_reader; GRANT USAGE ON SCHEMA public TO replica_reader; GRANT SELECT ON ALL TABLES IN SCHEMA public TO replica_reader;`)
	// Reject TCP even with the correct disposable password before publication.
	if _, err := call(nil, "exec", "--env", "PGPASSWORD=disposable-fixture-password", names[1], "psql", "-h", "127.0.0.1", "-U", "replicove", "-d", "accounts", "-c", "SELECT 1"); err == nil {
		t.Fatal("staging accepts TCP before sanitization")
	}
	stream := func(from, to, user string) {
		t.Helper()
		_, err := transfer(ctx, 256<<20, func(ctx context.Context, w io.Writer) error {
			args := []string{"exec", "--env", "PGOPTIONS=-c default_transaction_read_only=on", from, "pg_dump", "--username=" + user}
			args = append(args, dumpArgs("accounts")...)
			cmd := exec.CommandContext(ctx, "docker", args...)
			cmd.Stdout = w
			cmd.Stderr = io.Discard
			return cmd.Run()
		}, func(ctx context.Context, r io.Reader) error {
			args := append([]string{"exec", "--interactive", to}, restoreArgs("accounts")...)
			cmd := exec.CommandContext(ctx, "docker", args...)
			cmd.Stdin = r
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			return cmd.Run()
		})
		if err != nil {
			t.Fatal("logical transfer failed (output suppressed)")
		}
	}
	stream(names[0], names[1], "replica_reader")
	grant := grantFixture()
	grant.Subsets = []api.DatabaseSubset{{DatabaseColumn: api.DatabaseColumn{Schema: "public", Table: "users", Column: "tenant_id"}, Equals: "10"}, {DatabaseColumn: api.DatabaseColumn{Schema: "public", Table: "orders", Column: "tenant_id"}, Equals: "10"}}
	mask, err := MaskSQL(grant, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	sql(names[1], mask)
	stream(names[1], names[2], "replicove")
	if !bytes.Equal(sql(names[0], "SELECT email FROM public.users WHERE id=1;"), []byte("raw-person@example.invalid\n")) {
		t.Fatal("source was changed")
	}
	if !bytes.Equal(sql(names[2], "SELECT count(*) FROM public.orders o JOIN public.users u USING(email) WHERE length(u.email)=64;"), []byte("1\n")) {
		t.Fatal("declared relationship did not survive")
	}
	if !bytes.Equal(sql(names[2], "SELECT count(*) FROM pg_constraint WHERE contype='f' AND convalidated;"), []byte("1\n")) {
		t.Fatal("foreign key not validated")
	}
	if !bytes.Equal(sql(names[0], "SELECT count(*) FROM public.users;"), []byte("2\n")) || !bytes.Equal(sql(names[2], "SELECT count(*) FROM public.users;"), []byte("1\n")) {
		t.Fatal("subset changed source or retained excluded rows")
	}
	// Force WAL/data to disk; the final storage must never have held original rows.
	sql(names[2], "CHECKPOINT;")
	out, err := call(nil, "exec", names[2], "sh", "-c", "if grep -a -r -l 'raw-person@example.invalid' /var/lib/postgresql/data >/dev/null 2>&1; then printf FOUND; else printf CLEAN; fi")
	if err != nil || string(out) != "CLEAN" {
		t.Fatal("original row found in final PostgreSQL storage")
	}
	// Source read role cannot write even without the operator's read-only PGOPTIONS.
	if _, err := call(strings.NewReader("UPDATE public.users SET email='changed';"), "exec", "--interactive", names[0], "psql", "-U", "replica_reader", "-d", "accounts", "-v", "ON_ERROR_STOP=1"); err == nil {
		t.Fatal("fixture source role unexpectedly has write authority")
	}
}
