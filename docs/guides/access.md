# Access for people, CI, and agents

Access has two layers: authenticate to the host to manage replica requests, then use a bounded guest credential to work inside the replica. Agents use the same Kubernetes APIs as people; no special agent framework integration is required.

## Choose a guest role

| Role | Guest ClusterRole | Intended use |
| --- | --- | --- |
| `viewer` | `view` | Inspect ordinary workloads without changing them |
| `deployer` | `edit` | Deploy and modify workloads; includes sensitive capabilities such as Secret access |
| `admin` | `cluster-admin` | Full guest administration for trusted tests |

The administrator must include the role in `ReplicaGrant.spec.accessRoles`. These are guest-cluster permissions, not host administrator permissions. Their exact rules come from the guest Kubernetes version and any applicable role aggregation.

## Request credentials in YAML

```yaml
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaAccess
metadata:
  name: test-session
  namespace: replica-lab
spec:
  replicaName: demo
  replicaUID: REPLACE_WITH_CURRENT_REPLICA_UID
  role: viewer
  durationSeconds: 900
```

Read the current UID from `ClusterReplica.metadata.uid`; do not reuse one from an earlier replica with the same name. The [YAML walkthrough](../getting-started/yaml.md) shows creating this manifest with the actual UID.

Wait for `status.phase: Ready`, then read the named Secret from `status.credentialSecret`. Its `data.config` is a base64-encoded kubeconfig. `status.expiresAt` is the actual session deadline. Do not log the Secret or token.

The request duration must be 600–3,600 seconds and is capped by the grant and replica lifetime. Issuance needs at least 600 seconds remaining. The controller checks the TokenRequest server's actual expiration; a server returning an unexpectedly longer token is not silently accepted. Spec changes require a new access request.

## Give consumers access to the exact Secret

Administrators can configure:

```yaml
# ReplicaGrant.spec
accessRoles: [viewer, deployer]
accessSubjects:
  - kind: ServiceAccount
    namespace: ci
    name: integration-runner
  - kind: Group
    name: integration-developers
maxAccessSeconds: 900
```

Replicove creates and later revokes exact-name Secret-get Roles and RoleBindings for those subjects. These subjects may read **every session under that grant**, not only the session they created. Namespace delegation does not identify or isolate individual callers.

If `accessSubjects` is omitted, an administrator must supply exact-name Secret permissions after issuance. Do not give consumers `list secrets` or unrestricted `get secrets` in the destination namespace; it also contains vCluster runtime administrator credentials.

Request management needs separate host RBAC. See [grants and permissions](grants.md). Keep the protected state namespace inaccessible to consumers.

## Connect from a workstation

The CLI offers a managed local tunnel:

```bash
bin/replicove connect demo --context YOUR_TEST_CONTEXT \
  --namespace replica-lab --role viewer --output demo.kubeconfig
```

It authenticates to the host using the caller's kubeconfig, creates a session, opens a loopback-only port-forward, validates guest TLS, and writes a new private mode-0600 file. It refuses to overwrite an existing file. Ctrl-C or expiration closes the tunnel and removes the file; transient disconnects reconnect on the same local port. Deleting the access request explicitly revokes the guest session; server-side expiration/revocation remains authoritative even if a local process is lost.

`connect` supports owned runtimes. For an existing target, use `access` and the administrator-configured route. Host RBAC must also allow the required service/pod lookup and pod port-forward operations for the intended runtime. Request-management RBAC alone is insufficient for a tunnel.

Without the CLI, retrieve the exact session Secret and run `kubectl port-forward` as in the [YAML quickstart](../getting-started/yaml.md). Keep the kubeconfig CA and `tls-server-name`; change only its server URL to the loopback port. Never solve certificate errors by disabling TLS verification.

## Connect from an in-cluster agent

A host-cluster agent can:

1. Use its service account and namespaced requester RBAC to create `ClusterReplica`.
2. Watch status until `Ready`, or stop and surface the condition reason on a blocked request.
3. Read the replica UID and create `ReplicaAccess` for a permitted role.
4. Read only the Secret named in the ready access status, using its granted exact-name permission.
5. Load the kubeconfig into a separate Kubernetes client and use the guest Service endpoint from an allowed network path.
6. Delete access and replica requests in a `finally`/cleanup handler, and wait for finalizers. TTL is a fallback if the runner disappears.

For an owned target, the issued in-cluster endpoint uses the runtime Service in the destination namespace. NetworkPolicy, DNS, routing, and host worker isolation remain administrator responsibilities. Avoid mounting the host administrator kubeconfig into an agent.

The kubeconfig is data-only: exec plugins, external file references, impersonation, proxy overrides, auth providers, and insecure TLS are rejected for target connections. Existing targets additionally pin their guest `kube-system` namespace UID.

## Revocation and failures

Deleting a session, deleting/expiring its replica, or changing its grant initiates revocation. Replicove removes exact-Secret reader permissions, guest identity/binding, credential Secret, and encrypted access state using recorded ownership. If the guest API cannot be reached, a finalizer remains until safe verification can complete. Token expiration still bounds use; never remove finalizers simply to hide an unavailable guest.

## Access to workload mirrors

`replicove mirror connect NAME` and `mirror access NAME` resolve the currently active generation and use the same Kubernetes identity, ReplicaGrant role limits, credential Secret distribution, and bounded TokenRequest flow. They do not add a separate agent identity system or MCP endpoint. A test lease can defer activation while an agent uses its current generation. Reconnect after a reset; a connection never silently follows the next generation.

Prepared candidates cannot issue access. The mirror's protected TTL caps sessions, and retired/expired generations revoke their credentials. In an administrator-qualified existing vCluster, mirror access binds roles only in that generation's owned namespaces, including when the requested role is `admin`. See [workload mirrors](mirrors.md) for prerequisites and lifecycle examples.
