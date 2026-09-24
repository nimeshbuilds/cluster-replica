# Optional MCP access for agents

`replicove mcp` serves the Model Context Protocol over **stdio**, using the official Go MCP SDK and the selected host kubeconfig identity. It is optional; agents can still submit the same Kubernetes CRDs or use CLI commands directly. It does not install another controller, an authentication database, or a certificate authority.

Example client configuration (adjust the binary path and host context):

```json
{
  "mcpServers": {
    "replicove": {
      "command": "/path/to/replicove",
      "args": ["--context", "integration-host", "--namespace", "replica-lab", "mcp"]
    }
  }
}
```

The default interface exposes metadata-only listing, plan inspection and readiness waiting. Add `--allow-write` to the MCP command to expose replica creation/deletion, bounded access requests, and mirror Sync/Reset. This flag does not grant Kubernetes permissions. Every tool operation uses the process's configured Kubernetes identity; the API server authenticates and authorizes the actual requests, and the operator independently enforces administrator grants. Mutations of existing environments require exact current UIDs. A successful deletion request is not a cleanup receipt.

Use a dedicated identity and destination namespace for each trust group. The server has no tool for editing `ReplicaGrant`, reading Secrets, running arbitrary shell commands, changing host kubeconfigs or granting itself permissions. Guest access tools return only the access request identity. Retrieve the bounded guest kubeconfig through the existing [access workflow](access.md), which requires separately authorized exact-Secret reads.

For an in-cluster agent or an MCP process manager, the current source image includes `/replicove`; run it with `mcp` under the agent's own Kubernetes ServiceAccount. A manager must attach stdio and provide the intended identity. Replicove does not install or certify kmcp. Do not share a privileged operator ServiceAccount between unrelated agents.

## Transport and identity boundaries

Stdio's caller is the local process/ServiceAccount identity. There is no remote HTTP listener, OAuth authorization server, per-tool certificate issuance API, or Replicove CA in this interface. Different stdio clients using the same kubeconfig have the same Kubernetes authority. Remote multi-user MCP needs a separately qualified authentication and delegation adapter; placing an unauthenticated HTTP proxy in front of this process would erase its trust boundary.

Tools expose metadata and status, which can include administrator-controlled resource names and messages. Treat returned text as data rather than instructions to change an agent's security policy. The MCP process never reuses the operator's encrypted captures or administrator guest credentials.
