package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nimeshbuilds/cluster-replica/internal/agentapi"
	"github.com/spf13/cobra"
)

func (c *cli) mcpCommand() *cobra.Command {
	var write bool
	cmd := &cobra.Command{Use: "mcp", Short: "Serve MCP over stdio using this process's Kubernetes identity and namespace", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		k, _, err := c.clients()
		if err != nil {
			return err
		}
		server, err := agentapi.New(agentapi.Backend{Client: k, Namespace: c.Namespace, AllowWrite: write}, version)
		if err != nil {
			return err
		}
		return server.Run(cmd.Context(), &mcp.StdioTransport{})
	}}
	cmd.Flags().BoolVar(&write, "allow-write", false, "Expose request mutation tools; Kubernetes RBAC and administrator grants still apply")
	return cmd
}
