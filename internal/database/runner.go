package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
	"github.com/nimeshbuilds/cluster-replica/internal/target"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type Source struct {
	Grant              api.DatabaseGrant
	Username, Password string
}
type Destination struct {
	Connection               *target.Connection
	Namespace, Pod, Database string
}
type Runner interface {
	Capture(context.Context, Source, Destination) (string, error)
	SQL(context.Context, Destination, string) error
	Copy(context.Context, Destination, Destination, int64) (string, error)
}
type PostgreSQL struct{ DumpBinary string }

func dumpArgs(database string) []string {
	return []string{"--dbname=" + database, "--format=custom", "--compress=0", "--no-owner", "--no-acl", "--no-comments", "--no-publications", "--no-subscriptions", "--no-password", "--lock-wait-timeout=5000"}
}
func restoreArgs(database string) []string {
	return []string{"pg_restore", "--dbname=" + database, "--username=replicove", "--no-owner", "--no-acl", "--exit-on-error", "--single-transaction"}
}

func sourceEnvironment(s Source) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "LC_ALL=C", "PGHOST=" + s.Grant.Host, "PGPORT=" + strconv.Itoa(int(s.Grant.Port)), "PGUSER=" + s.Username, "PGPASSWORD=" + s.Password, "PGSSLMODE=" + s.Grant.SSLMode, "PGCONNECT_TIMEOUT=10", "PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=" + strconv.Itoa(int(s.Grant.TimeoutSeconds)*1000) + " -c lock_timeout=5000"}
	if s.Grant.SSLMode == "verify-full" {
		env = append(env, "PGSSLROOTCERT=system")
	}
	return env
}

func (p PostgreSQL) Capture(ctx context.Context, s Source, to Destination) (string, error) {
	bin := p.DumpBinary
	if bin == "" {
		bin = "pg_dump"
	}
	return transfer(ctx, s.Grant.MaxBytes, func(ctx context.Context, w io.Writer) error {
		cmd := exec.CommandContext(ctx, bin, dumpArgs(s.Grant.Database)...)
		cmd.Env = sourceEnvironment(s)
		cmd.Stdout = w
		cmd.Stderr = io.Discard
		if cmd.Run() != nil {
			return ErrFailed
		}
		return nil
	}, func(ctx context.Context, r io.Reader) error {
		return remote(ctx, to, restoreArgs(to.Database), r, io.Discard)
	})
}
func (p PostgreSQL) SQL(ctx context.Context, d Destination, sql string) error {
	return remote(ctx, d, []string{"psql", "--no-psqlrc", "--quiet", "--username=replicove", "--dbname=" + d.Database, "--set=ON_ERROR_STOP=1"}, strings.NewReader(sql), io.Discard)
}
func (p PostgreSQL) Copy(ctx context.Context, from, to Destination, max int64) (string, error) {
	return transfer(ctx, max, func(ctx context.Context, w io.Writer) error {
		return remote(ctx, from, append([]string{"pg_dump", "--username=replicove"}, dumpArgs(from.Database)...), nil, w)
	}, func(ctx context.Context, r io.Reader) error {
		return remote(ctx, to, restoreArgs(to.Database), r, io.Discard)
	})
}

// transfer bounds bytes and couples cancellation in both directions. Archive and
// database error output never reaches controller logs, disk, or public status.
func transfer(ctx context.Context, max int64, produce func(context.Context, io.Writer) error, consume func(context.Context, io.Reader) error) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	h := sha256.New()
	bound := &boundedWriter{next: io.MultiWriter(w, h), left: max}
	done := make(chan error, 1)
	go func() {
		err := produce(ctx, bound)
		if err != nil {
			_ = w.CloseWithError(ErrFailed)
		} else {
			_ = w.Close()
		}
		done <- err
	}()
	err := consume(ctx, r)
	_ = r.CloseWithError(ErrFailed)
	if err != nil {
		cancel()
	}
	producerErr := <-done
	if err != nil || producerErr != nil {
		return "", ErrFailed
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type boundedWriter struct {
	next io.Writer
	left int64
}

func (b *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > b.left {
		return 0, ErrFailed
	}
	n, e := b.next.Write(data)
	b.left -= int64(n)
	return n, e
}

func remote(ctx context.Context, d Destination, args []string, input io.Reader, output io.Writer) error {
	if d.Connection == nil || d.Connection.Config == nil {
		return ErrFailed
	}
	cfg := rest.CopyConfig(d.Connection.Config)
	cfg.Timeout = 0
	u := d.Connection.Kubernetes.CoreV1().RESTClient().Post().Resource("pods").Namespace(d.Namespace).Name(d.Pod).SubResource("exec").VersionedParams(&corev1.PodExecOptions{Container: "postgres", Command: args, Stdin: input != nil, Stdout: true, Stderr: true}, scheme.ParameterCodec).URL()
	x, err := remotecommand.NewSPDYExecutor(cfg, "POST", u)
	if err != nil {
		return ErrFailed
	}
	if x.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: input, Stdout: output, Stderr: io.Discard}) != nil {
		return ErrFailed
	}
	return nil
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
