Replicove v0.3.0-alpha.2 fixes cleanup of copied PVCs in replica-owned guest namespaces. A guest-created Pod can keep a PVC protected after its deletion is requested; cleanup now also starts deletion of the owned namespace so those consumers can drain. Existing borrowed namespaces and their unrelated resources remain protected. Custom-resource and controller cleanup keeps its dependency order, and all deletions still require the recorded object identity and ownership markers.

```bash
helm upgrade --install replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.3.0-alpha.2 \
  --namespace replicove-system --create-namespace --wait --timeout 3m
```

Prebuilt CLI archives for Linux and macOS on amd64 and arm64 are attached below; each includes the executable, license and a quickstart for this release. Download the matching platform archive and verify it with `SHA256SUMS`. Go is only needed when building from the tagged source. The Helm chart and native CRD/operator manifests are separate assets; the operator image is published to GHCR.

Use matching CLI, chart, image and CRDs from this release. The expanded lifecycle test covers selected dependencies, Secret Snapshot/Follow behavior, EmptyVolumes with independent guest writes, drift repair, pruning and complete storage cleanup. The release links the exact source commit and published-artifact verification run below. Use a source build for an unreleased revision.

- **Reusable test recipes:** create a fresh managed replica or mirror, wait for readiness, obtain a bounded guest session, execute an explicit local command and verify cleanup. Metadata/provenance JSON and JUnit reports distinguish command failure from cleanup failure. Recipes are local documents; there is no dedicated GitHub Action product integration.
- **Preflight and plan evidence:** inspect caller/API checks, captured source identities, dependencies, transformations and observed omissions without exposing resource payloads. A report is not an atomic source snapshot or an importable historical replay.
- **Destination pools and admission:** render separate operator/state/destination installations with quotas, choose among explicitly listed destinations, and reserve capacity durably in the operator. Queued requests retain their original TTL.
- **PostgreSQL 17 copies:** a separate administrator database grant authorizes read-only source access, approved column masks and explicit table equality filters. A consistent logical dump is restored in isolation, sanitized and validated, then copied into fresh final storage before applications or guest access start. Undeclared data can remain sensitive; no general anonymization or automatic tenant extraction is promised.
- **Six bounded chaos fault types:** PodDelete, ScaleZero, NetworkIsolation, CPUStress, MemoryStress and CustomJob operate only on qualified owned test targets, with grant limits, durable rollback and verified cleanup. Network/Job faults require enforced host policies and reject conflicting allow rules. These are shared-worker workload faults, not privileged host/node faults.
- **Optional agent and UI interfaces:** namespace-scoped stdio MCP uses the caller's Kubernetes identity, read-only by default with explicit mutation opt-in. The dashboard serves read-only metadata over loopback with a private session URL. There is no remote MCP certificate/CA service or shared dashboard deployment.

Mirrors, databases and chaos use the same operator image and are disabled by default. They can be enabled later with complete CRD, image, values and RBAC updates. Existing selected configuration/operator/Helm replication, access, persistent control planes and CSI mirror resets remain available.

Upgrade **all six CRDs** before Helm: ClusterReplica, ReplicaGrant, ReplicaAccess, ReplicaMirror, ReplicaMirrorRun and ReplicaExperiment. Helm does not upgrade its `crds/` directory automatically. Preserve the immutable encryption key, protected state, namespaces and reviewed values; replace a completed bootstrap Job only when its immutable template changes. An old saved image digest overrides a new tag. Keep affected modules, operator and storage controllers available until all finalizers finish.

PostgreSQL copies are limited to new managed targets and cannot be combined with CSI mirrors, existing targets or in-place refresh. The pinned vCluster permits one managed runtime per host namespace; use separate pool destinations for concurrency. Managed mirror resets interrupt availability. CSI captures remain per-volume crash-consistent, with no atomic application or multi-volume guarantee.

Release artifacts include Linux amd64/arm64 operator images, macOS/Linux amd64/arm64 CLI archives, digest-pinned OCI chart/native manifests and checksums. OCI build metadata is not a separate signed release attestation. The release workflow creates the version tag only after the exact main revision and published-artifact verification pass. The release links its source commit and artifact-verification run.

Read the [feature map](https://nimeshbuilds.github.io/replicove/features/), [installation and optional modules](https://nimeshbuilds.github.io/replicove/getting-started/installation/), [security model](https://nimeshbuilds.github.io/replicove/security/) and [validation record](https://nimeshbuilds.github.io/replicove/validation/). Cloud identity, broader data adapters, vCluster Platform, distribution certification and production support remain outside this experimental release.
