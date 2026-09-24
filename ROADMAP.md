# Replicove roadmap

The portable alpha implements a declarative workflow for disposable Kubernetes environments that reproduce selected tools and configuration. See [project status](docs/project-status.md) for evidence and [the full design](docs/design/cluster-replica-implementation-plan.md) for longer-term acceptance criteria. This roadmap is not a release-date promise.

## Implemented portable alpha

| Area | Available implementation |
| --- | --- |
| Runtime | Pinned standalone Helm provisioning, persistent or lab control plane, existing-target registration, readiness and UID ownership |
| Discovery and plan | Administrator grants, bounded read-only capture, selected Helm provenance/resources, dependency order, mappings, overrides and manual approval |
| Replication | Guest resource reconstruction, explicit refresh, drift reporting, preserved experiments and refusal to adopt foreign resources |
| Secrets and access | Explicit snapshot/follow grants, expiring guest roles, exact-Secret reader permissions, revocation and reconnecting CLI tunnel |
| Cleanup | Guest and host ownership inventory, finalizer-aware deletion, bound-volume checks and real TTL/PVC cleanup tests |
| Workloads | Functional cert-manager, Spark Pi, Trino TPCH and native admission-policy scenarios |
| Compatibility | Kubernetes 1.35.8/1.36.4 hosts with a 1.36.0 guest; chart rendering separately checks 1.35–1.37 |
| Packaging and docs | Embedded operator chart, four CLI build targets, checksums, branding and a complete first-replica quickstart |

The original implementation checklists (#1–#5) are resolved with merged code and test evidence. The unfinished version-maintenance pipeline remains tracked in [#6](https://github.com/nimeshbuilds/replicove/issues/6).

## Implemented next-candidate scope

Current source targets **v0.3.0-alpha.1**. It adds read-only preflight and plan evidence; reusable test recipes with provenance/JUnit reports; destination pools and durable capacity admission; scoped PostgreSQL 17 copies with approved masks and table filters; six bounded chaos fault types; namespace-scoped stdio MCP; and a local read-only dashboard. All six CRDs share the operator, and database/chaos modules can be enabled later like mirrors. See the [feature map](docs/features.md) for limits.

Local checks have passed. The next gates are exact-revision disposable tests and published-artifact verification, not more feature claims. There is no dedicated GitHub Action integration. The MCP interface has no remote certificate/CA service, and PostgreSQL masks/subsets do not promise general anonymization or an inferred tenant boundary.

## Runtime version maintenance

Before treating a profile as certified, finish immutable image-digest pins and provenance review, automated candidate schema/resource/RBAC comparison, and retained-profile upgrade/recovery tests. Existing live replicas must keep their resolved identity, guest version, TTL and desired values. Cleanup must remain possible without fetching a chart. The current chart checksum, contract tests, read-only upstream checker and live matrix are the foundation; [the maintenance guide](docs/maintaining-replicove.md) records the current manual process.

## Next public alpha release

The v0.2.0-alpha.2 baseline already publishes binaries, images, charts and native YAML. Release v0.3.0-alpha.1 only after its exact main revision passes all required CI jobs, followed by installation, upgrades and feature tests against its published artifacts. Update validation with those run links before changing pending claims. Separate signed attestations remain future work; OCI build metadata and checksums do not establish that claim.

## Additional adapters

- Cloud identities: IRSA, EKS Pod Identity, Azure/GCP workload identity with authenticated exchange and revocation tests.
- Data: qualify the scoped PostgreSQL 17 path and cloud CSI drivers; add broader database engines/PITR, grouped-volume adapters, non-CSI transfer, and explicit external-resource cleanup only with matching ownership and recovery tests. The optional per-volume mirror implementation and its qualification scope are described in [workload mirrors](docs/guides/mirrors.md).
- vCluster Platform: qualified provisioning and lifecycle integration; current requests block without falling back to Helm.
- Operators: External Secrets backend recreation, lifecycle hooks, bootstrap cycles and additional behavior tests.

## Production qualification

Add stronger host admission/network isolation guidance and tests, per-user gateway delegation, larger encrypted capture storage, scale and recovery coverage beyond the bounded shared-worker chaos implementation, distribution-specific admission/storage adapters, and external design-partner validation.

Kubernetes versions, served APIs and required capabilities are the core compatibility axes. EKS, AKS, GKE, OpenShift and RKE2 require separate evidence for their identity, storage, admission and networking behavior. Cloud labs are deferred; current validation uses disposable CI clusters.

Have a concrete test-environment problem? [Describe it in Discussions](https://github.com/nimeshbuilds/replicove/discussions) or [propose a feature](https://github.com/nimeshbuilds/replicove/issues/new/choose).
