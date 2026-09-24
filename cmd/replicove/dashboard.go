package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/nimeshbuilds/cluster-replica/internal/agentapi"
	"github.com/nimeshbuilds/cluster-replica/internal/dashboard"
	"github.com/spf13/cobra"
	"net"
	"net/http"
	"time"
)

func (c *cli) dashboardCommand() *cobra.Command {
	var address string
	cmd := &cobra.Command{Use: "dashboard", Short: "Open a read-only local dashboard using the selected Kubernetes identity", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := agentapi.ValidateNamespace(c.Namespace); err != nil {
			return err
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("dashboard requires a literal loopback listen address, such as 127.0.0.1:0")
		}
		k, _, err := c.clients()
		if err != nil {
			return err
		}
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		token := hex.EncodeToString(nonce)
		ln, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		defer ln.Close()
		server := &http.Server{Handler: dashboard.Handler(func(ctx context.Context) dashboard.Snapshot { return dashboard.Load(ctx, k, c.Namespace) }, token, ln.Addr().String()), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
		fmt.Fprintf(cmd.OutOrStdout(), "Read-only dashboard: http://%s/#token=%s\nKeep this process running. The URL grants access to this dashboard session.\n", ln.Addr(), token)
		go func() {
			<-cmd.Context().Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		}()
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}}
	cmd.Flags().StringVar(&address, "listen", "127.0.0.1:0", "Loopback IP:port; port zero chooses an available port")
	return cmd
}
