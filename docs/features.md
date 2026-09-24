# Features and their limits

This map describes the implemented scope targeted for **Replicove v0.3.0-alpha.2**, including its owned-PVC cleanup fix. Release qualification is [pending verification](validation.md#owned-pvc-cleanup-regression-alpha2-verification-pending). PostgreSQL, chaos, test recipes/pools, diagnostics and local agent interfaces were introduced in v0.3.0-alpha.1. That version's historical 16-job source pass at `93bcfbc` remains separate from the new regression and published-artifact checks.

Use the **[ten executable scenarios and coverage matrix](scenarios/index.md)** to try these feature families in disposable clusters. The scenarios cover supported workflows, not every possible API field combination or host distribution.

## Installation and lifecycle

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| Installation | Helm, CLI installer, or native YAML with protected key bootstrap | [Installation](getting-started/installation.md); all six CRDs share one operator image/chart |
| New vCluster | Provisions a pinned persistent control plane or disposable lab profile | [Helm quickstart](getting-started/helm.md); one managed vCluster 0.37.1 runtime per destination |
| Existing vCluster | Uses an explicitly registered guest with pinned identity and credentials | [Existing targets](guides/existing.md); no runtime adoption or deletion |
| Bounded lifetime | TTL includes planning, admission, approval and provisioning; cleanup runs on expiry/deletion | [Cleanup](guides/cleanup.md); refresh/reset does not renew the lifetime |
| State persistence | Encrypted captures, access, capacity and ownership records in a protected namespace | [Key persistence](getting-started/installation.md#key-initialization-and-persistence); back up key and state together |
| Optional modules | Mirrors, PostgreSQL and chaos are disabled by default and can be enabled later | [Installation](getting-started/installation.md#optional-modules); update CRDs, image, values and RBAC together |
| Capacity and pools | Durable destination reservations and grant caps; offline pool rendering and least-occupied destination selection | [Pools](guides/pools.md); selection is a hint, not a reservation; queue time consumes TTL |

## Selecting and reproducing configuration

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| Administrator policy | Bounds source namespaces/kinds, secrets, targets, profiles, TTL and guest roles | [Grants and RBAC](guides/grants.md); does not replace host RBAC |
| Selection and dependencies | Namespace/name/label selectors, known references, mappings and explicit patches | [Selection](guides/selection.md); arbitrary operator dependencies and embedded strings are not inferred |
| Helm and operators | Reconstructs selected Helm desired resources and values, CRDs and authorized operators | [Operators](guides/operators.md); no generic lifecycle-hook or external-side-effect cloning |
| Secrets | Explicit snapshot/follow modes within exact administrator grants | [Secrets and storage](guides/secrets-storage.md); host service-account tokens and cloud identities are not cloned |
| Plan approval and evidence | Approves a captured revision; reports source UID/resourceVersion, dependency paths, transformations and observed omissions | [Diagnostics](guides/diagnostics.md); metadata evidence is not a historical replay bundle or an atomic source snapshot |
| Preflight | Checks API discovery and caller authorization before provisioning | [Diagnostics](guides/diagnostics.md); unknown results are explicit; no proof of operator permissions, CNI, CSI or cloud identity |
| Refresh and drift | Recaptures source configuration; preserves foreign ownership and guest additions | [Lifecycle](guides/lifecycle.md); database-enabled replicas require recreation instead of refresh |
| Ordinary PVCs | Explicitly authorized empty destination claims | [Secrets and storage](guides/secrets-storage.md); copying contents requires a separate data grant |

## Data copies

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| CSI mirrors | Copies exact granted filesystem PVCs into independent writable generations | [Mirrors](guides/mirrors.md); qualified driver/classes and host NetworkPolicy enforcement required |
| Mirror reset and schedules | Manual/scheduled latest-source sync or reset to a retained revision | [Mirrors](guides/mirrors.md); per-volume recovery points, no atomic multi-volume or database consistency guarantee |
| Mirror leases and retention | Bounds testing and retained captures; access resolves one active generation | [Mirrors](guides/mirrors.md); whole-mirror TTL wins; reconnect after replacement |
| PostgreSQL 17 | Consistent logical dump, isolated staging, sanitized second dump and new persistent target | [PostgreSQL](guides/postgresql.md); explicit database/source credentials, managed target only, no mirror combination or refresh |
| Approved masks | Null, constant or salted text token masks with declared equality domains and relationship validation | [PostgreSQL](guides/postgresql.md); no general anonymization; undeclared fields may remain sensitive |
| Approved subsetting | Equality filters delete rows only from explicitly selected staging tables before masking | [PostgreSQL](guides/postgresql.md); undeclared tables remain intact; no inferred tenant graph; invalid relationships reject publication |
| Data cleanup | Durable ownership records, staging removal and owned storage cleanup | [Cleanup](guides/cleanup.md); no source deletion or claim of physical erasure of provider backups |

## Repeatable tests and bounded faults

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| Test recipes | `replicove run` creates a fresh replica or mirror, waits, obtains access, runs a local command and verifies cleanup | [Test runs](guides/test-runs.md); immutable TTL; no existing-target adoption; local command is not sandboxed |
| Reports | Automatic metadata/provenance JSON and JUnit artifacts distinguish execution and cleanup outcomes | [Test runs](guides/test-runs.md); no raw captures, credentials, logs or exact source replay |
| Faults | `PodDelete`, `ScaleZero`, `NetworkIsolation`, `CPUStress`, `MemoryStress`, `CustomJob` | [Chaos](guides/chaos.md); 1–8 simultaneous faults, one active experiment per replica, exact owned targets and bounded duration/resources |
| Fault rollback | Durable journals, cancellation/expiry rollback and verified Job cleanup | [Chaos](guides/chaos.md); shared-worker restrictions, no privileged host/node faults; isolation/Jobs reject overlapping host allow policies |

## Humans and agents

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| Guest authorization | Expiring viewer/deployer/admin sessions and exact credential-Secret reader RBAC | [Access](guides/access.md); host request permission and guest roles are separate |
| CLI and YAML | Same Kubernetes resources for humans, CI and agents | [CLI](../QUICKSTART.md), [YAML](getting-started/yaml.md), [GitOps](guides/gitops.md); no dedicated GitHub Action product integration |
| Stdio MCP | Namespace-scoped listing, plan inspection and readiness waiting; mutations require `--allow-write` | [Agent interface](guides/agents-mcp.md); caller's Kubernetes identity; no remote transport, CA, certificate issuance or Secret-reading tools |
| Local dashboard | Loopback-only, read-only lifecycle, plan, mirror and experiment views | [Dashboard](guides/dashboard.md); private session URL; no credentials/data payloads or shared hosted service |
| References | Fields for all six CRDs plus command and operator help generated from source | [API](reference/api.md), [CLI](reference/cli.md), [operator](reference/operator.md); `TestRecipe` and `ReplicaPool` are local CLI documents, not CRDs |

## Not implemented or not qualified

- Automatic cloud identity exchange, generic external database/bucket/queue cloning, PITR, atomic multi-volume capture, and automatic relational tenant extraction.
- A remote multi-user MCP gateway, Replicove certificate authority, per-user request ownership, or hosted dashboard.
- Exact arbitrary-cluster cloning, independent host-kernel/worker isolation, vCluster Platform or distribution-specific certification.
- Production support and large-scale recovery qualification. Disposable results apply only to their recorded revisions, fixtures and host capabilities.

The [implementation ledger](IMPLEMENTATION_STATUS.md), [project status](project-status.md) and [roadmap](../ROADMAP.md) track implementation and remaining gates separately.
