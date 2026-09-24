package testrun

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Process executes argv directly, without a shell, in the caller's working
// directory. Only the child environment receives the temporary kubeconfig.
// Output is streamed, never retained in the provenance or JUnit report.
func Process(stdout, stderr io.Writer) func(context.Context, []string, string) (int, error) {
	return func(ctx context.Context, argv []string, kubeconfig string) (int, error) {
		if len(argv) == 0 {
			return -1, errors.New("no test executable")
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "KUBECONFIG=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "KUBECONFIG="+kubeconfig)
		cmd.WaitDelay = 5 * time.Second
		configureProcessGroup(cmd)
		defer cleanupProcessGroup(cmd)
		err := cmd.Run()
		if err == nil {
			return 0, nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), errors.New("test command exited unsuccessfully")
		}
		return -1, errors.New("test executable could not complete")
	}
}
