# 10. Inspect a live replica and automate it with an agent

Use this lab to verify the three inspection/automation surfaces against a real cluster: CLI diagnostics, stdio MCP and the local dashboard. It creates a short-lived Kubernetes ServiceAccount identity and changes its RBAC while the same processes remain connected, so authorization checks are exercised end to end.

## Run

After the [shared prerequisites](index.md#prepare-once):

```bash
./examples/scenarios/run.sh 10
```

The combined fixture also runs [scenario 09's pool checks](09-pools-capacity.md). You do not need an agent framework, API key, browser extension or external MCP manager. A small checked-in protocol client drives the actual released CLI and live Kubernetes API. Dashboard assertions use loopback HTTP and real live metadata; this is functional interface verification, not a browser rendering test.

## Follow the workflow

1. Prepare two native pool installations and a Ready replica in `test-a`. Run `doctor` for its grant/request and `plan --json`. Check that host/Replicove APIs and grant checks pass, while network-isolation qualification stays explicitly `unverified`. The plan must match the real replica UID, capture revision/time and runtime checksum, and retain source object UIDs/resource versions and dependency evidence without including the synthetic ConfigMap payload.
2. Create a short-lived host ServiceAccount credential with only the intended namespace reads, keeping its kubeconfig in the private fixture directory. Start `replicove mcp` over stdio, perform the actual MCP initialization/tool discovery, and call list, plan and bounded readiness wait. Only those three read-only tools may be exposed by default.
3. Start the same interface with `--allow-write` while the ServiceAccount is still read-only. Mutation calls must fail: the flag exposes tools but does not grant Kubernetes permissions. Add explicit request permissions and then create a replica and bounded access request. Check exact UID protection by trying to delete a request with the wrong UID. The access tool returns request metadata; the agent still cannot fetch the credential Secret without separately delegated Secret access.
4. Start the read-only dashboard using that identity. Verify the printed URL is loopback-only and carries a capability in its fragment. Requests without the correct capability are unauthorized; unexpected Host/Origin values are forbidden; mutation HTTP methods are rejected. Authorized reads contain current bounded-namespace metadata, and static UI assets are served with the expected security headers and no-store behavior.
5. Remove the ServiceAccount's RoleBinding while the same MCP/dashboard processes remain running. Subsequent MCP reads must fail under revoked RBAC; the dashboard must report unavailable collections rather than fabricate an empty successful view. Delete the issued access request and verify its revocation.
6. Complete the shared pool's test run and capacity/cleanup assertions, then close processes, remove temporary credentials and delete the named disposable host. The original source values/identities and the host kubeconfig must remain unchanged throughout the product workflows.

## Expected result

The command exits zero and prints the combined evidence directory. `interfaces.json` contains the live diagnostics, MCP protocol and dashboard assertions, and `report.json` includes them with the pool lifecycle. Saved `doctor.json` and `plan.json` contain metadata, not captured resource bodies or source credentials. The credential and dashboard capability stay in the private run directory, outside the published evidence set.

`doctor` distinguishes a passed permission/discovery check from an unverified CNI/CSI/cloud property. `plan` explains sanitized provenance; `status` prints Kubernetes status JSON. None of those reports independently establishes that your application passed an integration test.

## Inspect and adapt

Read the [combined fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-interfaces.sh) and [real MCP/HTTP client assertions](https://github.com/nimeshbuilds/replicove/blob/main/test/scenarios/interfaces/check.py). The complete [diagnostics](../guides/diagnostics.md), [MCP](../guides/agents-mcp.md) and [dashboard](../guides/dashboard.md) guides show their ordinary user commands and configuration. [Access](../guides/access.md) separates host request authority from reading bounded guest credentials.

MCP uses the caller process's Kubernetes identity over stdio. There is no Replicove CA, remote HTTP MCP listener or automatic kmcp deployment in this alpha. Different clients sharing the same kubeconfig share its authority. Use dedicated identities and explicit namespace delegation for trust groups.

The dashboard is a local read-only process, not a hosted multi-user service. Keep its complete capability URL private and do not expose it through a public ingress. If reads fail, inspect the caller's current RBAC and cluster route; restarting a process must not be used to bypass revoked authorization. The fixture stops its processes automatically and does not leave a dashboard running after completion.
