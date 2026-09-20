Replicove v0.2.0-alpha.1 adds optional workload mirrors: writable copies of selected host workloads and CSI volume data, with manual or scheduled resets. The host remains the source of truth; resets discard guest changes within the owned scope.

```bash
helm upgrade --install replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.2.0-alpha.1 \
  --namespace replicove-system --create-namespace --wait --timeout 3m
```

For data copies, follow the [mirror quickstart](https://nimeshbuilds.github.io/replicove/guides/mirrors/) to enable `mirrors.enabled`, qualify host NetworkPolicy/CSI support, and delegate exact source PVCs. The module uses the same operator image/chart, installs the pinned snapshot controller/APIs when absent, and reuses existing snapshot infrastructure.

- `ReplicaMirror` and `ReplicaMirrorRun` YAML APIs plus `replicove mirror` commands for creation, access, sync/reset, scheduling, test leases, retention and cleanup.
- Independent restored storage, encrypted snapshot identities, protection for active revisions, candidate readiness/isolation checks, and recovery from interrupted creation or partial restoration.
- Dedicated new vClusters or administrator-qualified existing runtimes with separate generation namespaces and namespaced guest access.
- Existing selected configuration/operator/Helm replication, secrets, plan approval, refresh, persistent control planes and ordinary replica lifecycle remain available.
- Stable local CLI listening socket across streaming tunnel reconnection, without replaying application requests.
- Generated API/CLI references, API-tested examples, a full mirror guide and strict documentation/link checks.
- Linux amd64/arm64 operator image, macOS/Linux amd64/arm64 CLI downloads, digest-pinned OCI chart/native manifests and SHA-256 checksums. OCI SBOM/provenance metadata is not a separate signed release attestation.

Upgrades must apply the release CRDs before the Helm upgrade; Helm does not upgrade its `crds/` directory automatically. Preserve the immutable state key. Keep the operator and storage/snapshot controllers running until mirror finalizers finish cleanup.

vCluster 0.37.1 allows one runtime per host namespace. Managed resets prepare recovery points, wait for test leases, then remove the old runtime before starting its replacement; expect an interruption and no automatic rollback. Existing-target mirrors can prepare separate generation namespaces within their registered runtime. An unrelated runtime blocks new managed installation with an explicit reason.

The exact main revision must pass the full CI suite. Published-artifact tests additionally install the anonymous OCI chart/image and exercise mirrors, real data resets, isolation, existing targets, access, expiry and cleanup before this GitHub prerelease is created. Check the linked test run and source commit below for evidence.

**Experimental:** CSI recovery points are per-volume crash-consistent, not atomic application/database or multi-volume snapshots. External services, IRSA/cloud identity, provider CSI certification, vCluster Platform, the proposed MCP identity service and dashboard remain outside this release. Read the [compatibility limits](https://nimeshbuilds.github.io/replicove/reference/compatibility/) and [security model](https://nimeshbuilds.github.io/replicove/security/).
