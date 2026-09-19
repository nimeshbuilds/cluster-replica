# Replicove roadmap

This is a build sequence, not a release promise. The [implementation ledger](docs/IMPLEMENTATION_STATUS.md) and [validation record](docs/validation.md) distinguish code from proven behavior. The [full design plan](docs/design/cluster-replica-implementation-plan.md) retains longer-term acceptance criteria.

| Phase | Outcome | Current status |
| --- | --- | --- |
| 0. Evidence and compatibility | Pinned upstream contract and real host/guest tests | Chart contracts and real runtime/workload evidence recorded; full 1.35/1.36 matrix passed at 8123f9a |
| 1. Foundation | CRDs, immutable requests, grants, protected state | Implemented with real API and policy tests; namespace delegation is the authorization model |
| 2. Runtime | Standalone and existing vCluster lifecycle | Helm and pinned existing-target paths implemented; Platform provisioning remains gated |
| 3. Discovery and plan | Source capture, dependencies, selectors, overrides, approval | Implemented for supported desired state and stored Helm revisions |
| 4. Replication | Owned apply, readiness, Secrets, refresh and drift | Implemented; arbitrary operator semantics/hooks and External Secrets backends remain incomplete |
| 5. Workloads and identity | Spark/Trino and cloud identity | Small real Spark/Trino workloads pass; cloud identity adapters and labs remain deferred/incomplete |
| 6. Cleanup and data | Guest/host inventory and data lifecycle | Owned runtime/fresh-volume cleanup implemented; content copying, snapshots and external data are incomplete |
| 7. Access and agents | Expiring credentials, local connection, revocation | CLI, guest roles/tokens, tunnel recovery and exact-Secret reader RBAC implemented; per-user gateway is future work |
| 8. Distribution qualification | EKS/AKS/GKE/OpenShift/RKE2 evidence | Cloud labs deferred; vendor claims require additional evidence |
| 9. Maintenance | Release profiles and upgrade/recovery tests | Pinned adapter boundary, chart contracts, key-preserving upgrade test and update checker implemented |
| 10. Public release and operations | Published artifacts, support and scale | Local four-platform CLI/chart packaging passes; publication, signing, scale and GA qualification remain |

The next release gate is green CI for the complete portable workflow and its access controls, accurate installation/recovery documentation, and review of the stacked implementation PRs. The brand is Replicove; module names and API groups remain stable to preserve existing manifests and links.

Kubernetes versions, served APIs and verified capabilities drive compatibility. Distribution names add adapter context; the generic controller has no AWS dependency.
