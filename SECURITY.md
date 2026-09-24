# Replicove security

Replicove is an experimental portable alpha with no supported production release. Use disposable environments operated by trusted administrators. Read the [project status](docs/project-status.md) and [configuration guide](docs/replicove-quickstart.md) before granting access.

Report vulnerabilities using [GitHub private reporting](https://github.com/nimeshbuilds/replicove/security/advisories/new). Include the affected revision and a minimal sanitized reproduction. Do not put credentials, kubeconfigs, source Secret data, or private cluster captures in public issues. No response-time or historical-version maintenance guarantee is offered.

## Authorization boundaries

- A host administrator installs CRDs and creates cluster-scoped `ReplicaGrant` objects. Request creators cannot expand a grant. Source reads require both installer-supplied RBAC and a matching grant; ordinary source permissions remain read-only. Optional mirrors additionally require PVC reads and snapshot create/get/delete in explicitly listed source namespaces. They never receive source PVC update/delete permission.
- The chart installs the operator and encrypted state in an administrator-only system namespace, separate from the runtime destination. Its runtime Role names the resources and verbs required by the pinned vCluster chart and cleanup inventory. It grants no wildcard resources, `bind`, `escalate`, or impersonation. Chart contracts detect upstream permission changes; real API and live-cluster tests exercise namespace boundaries and rejected privilege escalation.
- The operator has limited host cluster reads: its named grants, the `kube-system` namespace identity, and bound PV metadata for cleanup. Optional source cluster reads are explicit chart values. Database enablement adds StorageClass reads for the Delete-policy requirement, and optional modules add reviewed host NetworkPolicy permissions. It cannot create host namespaces, CRDs, ClusterRoles, or ClusterRoleBindings with the default chart.
- Namespace delegation is the access model. Anyone able to administer destination workloads or read destination Secrets is trusted with the runtime, including its upstream administrator kubeconfig. The protected system namespace must remain outside that delegation.
- `ReplicaAccess` issues expiring guest viewer/deployer/admin credentials bounded by the grant and replica TTL. Administrator-selected subjects can receive exact-Secret read permissions; they can read sessions under that grant. This is not per-user request ownership. Consumers must not receive broad destination Secret access. Revocation removes credential-reader bindings, guest identities, and generated credential Secrets.

## Shared workers, networking and data

vCluster separates Kubernetes API state while guest workloads run on host workers. Host admission, Pod Security, CNI/NetworkPolicy enforcement, quotas, storage and node isolation remain the host administrator's responsibility. The portable planner rejects captured privileged/host-network/host-path workloads, but it is not an admission policy for every object a guest administrator may later create. Host namespace API authorization tests do not establish tenant network or kernel isolation. A fresh namespace alone is not a security boundary for hostile workloads.

Secret replication is denied by default and requires exact grants. Source service-account/bootstrap tokens are excluded. Captures and ownership inventories are encrypted with an immutable key in the system namespace; protect and back up that key with its state. Ordinary status and retained CI artifacts exclude credential payloads. Do not delete the key while finalizers remain.

`DeleteOwned` respects recorded UIDs and finalizers, revokes supported access, and removes supported owned resources. Bound volumes require Delete reclaim policy and verified disappearance. Optional mirrors delete only their own UID-bound restored volumes, snapshot imports, and original captures after retained references are released. Cleanup does not erase source data, unrelated snapshots, retained/shared storage, or arbitrary external services. The legacy `HelmReleaseOnly` mode and broad lab bootstrap manifest are for runtime development and have a smaller cleanup and permission contract.

Cloud identity exchange, general external data restoration, vCluster Platform provisioning, separate signed release attestations, and production hardening remain [roadmap work](ROADMAP.md). Unsupported adapters block explicitly. A passing portable test does not certify cloud or distribution behavior.

## Optional mirror module

Mirrors add an exact PVC data grant and administrator-approved CSI snapshot/storage classes. Snapshot backend handles remain in encrypted state; public status contains references and recovery-point timestamps. The operator's optional cluster permissions create/get/delete snapshot contents and read snapshot/storage classes. Its per-source Role can create/get/delete snapshots and read PVCs; it cannot modify source workloads or mount their volumes.

The separately deployed upstream snapshot controller needs its own cluster-wide reconciliation permissions, including PVC finalizer updates. Helm `auto` reuses a preexisting snapshot API/controller installation; `existing` always delegates to the host, and `managed` explicitly installs the bundled controller. This optional dependency has broader permissions than the Replicove operator itself. Review its [vendored upstream provenance](charts/replicove/files/NOTICE.md).

Host egress policies restrict initial mirrored workload namespaces to their generation, guest DNS, and guest API. Enabling mirrors requires an explicit administrator attestation that the host CNI enforces NetworkPolicy. Policies are additive: an unrelated host policy can widen access. This is not a sandbox for malicious guest administrators, who can create additional namespaces or workloads; use admission controls and stronger runtime/node isolation for untrusted code. Copied credentials can still point at external services, so use test credentials and validate application behavior. Never treat cloud identities, external backends, or multiple PVCs as an automatically isolated consistent snapshot.


## PostgreSQL copy and sanitization boundary

Database access requires a separate administrator `DatabaseGrant` within `ReplicaGrant.spec.databases`, including exact source/database selection and a protected credential Secret. A PVC grant, raw Secret grant or source resource selector does not authorize database contents. Use a dedicated source role with only required connection/schema/table/sequence reads and no ownership or write membership. The operator uses PostgreSQL 17 `pg_dump` with read-only session options; it does not run arbitrary source SQL or hooks. Source TLS defaults to `verify-full` with system trust; a private CA must be present in the qualified image trust store. Explicit `require` encrypts without verifying server identity.

Raw contents are restored only into a new isolated staging Pod with memory-backed storage. Host deny-ingress/egress policies are installed and checked before copying; guest NetworkPolicies are not sufficient in the pinned profile. TCP database access is rejected during preparation. Approved masks and explicit table equality filters execute only in staging, in one transaction, followed by foreign-key and declared relationship checks. A second logical dump creates the persistent final target so raw tuples were never written to its storage. Staging guest and translated host Pods must disappear before final publication. Application resources and guest sessions remain blocked until preparation succeeds.

These controls require a host CNI whose enforcement the administrator has qualified, and trusted host administrators who cannot add conflicting allow policies during preparation. They do not defend against a hostile host administrator, compromised node or arbitrary database extension code. Source schema and restore code are trusted inputs, and target images must be reviewed and digest-pinned. Existing targets, CSI-mirror combinations and in-place refresh are rejected for this adapter.

Masks cover only declared columns; token domains preserve equality only where configured. Table filters leave undeclared tables intact and reject invalid relationships rather than infer a tenant boundary. Unmasked strings, JSON, binaries, schema names or embedded credentials can still be sensitive. This is not a general anonymization guarantee. Review the complete application data before authorizing access. Status, logs and metadata reports omit source credentials, rows and masking constants; protected state still requires administrator-only access. See the [PostgreSQL guide](docs/guides/postgresql.md).

## Bounded chaos on shared workers

`ReplicaExperiment` requires exact replica/workload identities and an administrator fault grant. The six implemented types are PodDelete, ScaleZero, NetworkIsolation, CPUStress, MemoryStress and CustomJob. Duration, simultaneous faults, images and aggregate CPU/memory are bounded. Stress/custom Jobs are tokenless and restricted; there are no privileged host, node-reboot, disk-exhaustion or kernel fault presets. CustomJob remains administrator-approved code and does not create independent compute isolation.

Network and Job faults use host policies in managed runtimes, require qualified CNI enforcement, and reject overlapping host allow policies, including mirror and prepared-database policies. PodDelete and ScaleZero can operate on other eligible owned application targets. Durable rollback journals and physical Pod deletion barriers keep temporary isolation until the workload has stopped. Source workloads are never fault targets. See the [chaos guide](docs/guides/chaos.md).

## Local automation interfaces

The namespace-scoped stdio [MCP server](docs/guides/agents-mcp.md) uses its caller's Kubernetes identity. Listing, plan inspection and readiness waiting are read-only; `--allow-write` exposes bounded lifecycle tools without granting RBAC. It cannot edit grants, read Secrets, invoke arbitrary shell commands or mint its own identity. It is not a remote authentication/certificate service. Different clients sharing one kubeconfig share its authority.

The [dashboard](docs/guides/dashboard.md) binds only to loopback and requires its private session URL. It has read-only metadata endpoints, no captured payload or credential view, and no shared-service authentication model. Keep its URL private and do not expose it through an ingress or public proxy.

The [test runner](docs/guides/test-runs.md) executes an explicitly supplied local command with the caller's normal process authority and a temporary guest kubeconfig; it is not a command sandbox. The recipe and command must be trusted. Reports exclude raw captures/logs/credentials, but the test command itself can print sensitive data to its own output. TTL and verified cleanup bound the environment, not arbitrary external side effects of that command.
