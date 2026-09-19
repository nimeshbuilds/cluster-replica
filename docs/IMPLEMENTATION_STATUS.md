# Replicove implementation ledger

Requested scope: the full ClusterReplica product plan, researched product branding and a product icon. The user selected disposable CI clusters now and deferred cloud labs. This is a substantial portable implementation, not completion of every phase in the original plan.

## Implemented and under qualification

| Area | Implementation evidence | Live qualification |
| --- | --- | --- |
| Replicove name and icon | Collision research and generated asset in `docs/brand` / `assets/brand` | Delivered |
| Standalone vCluster lifecycle | Pinned Helm SDK provider, ownership, deletion and TTL | Passed original real kind/vCluster fixture repeatedly |
| Administrator grants and encrypted state | Namespace policy, exact credential grants, AES-GCM, bounded records, UID/RV checks | Full workflow CI in progress |
| Discovery and planning | Kubernetes/Helm capture, selectors, graph, mappings, patches, overrides | Real API capture regression passes; full workflow CI in progress |
| Generic replication | Intent before writes, UID ownership, CRDs, readiness and foreign-object refusal | Full workflow and real operator fixtures in progress |
| Secrets and refresh | Snapshot/follow, token exclusions, explicit refresh, drift, preserved added fields | Full workflow CI in progress |
| Existing target | Protected data-only credentials, pinned guest identity, no runtime adoption | Two real API servers pass identity, full apply, stable status and drift checks; preservation/conflict CI added |
| Ephemeral cleanup | Guest/host inventory, finalizers, access revocation, bound volume checks | Original stateless lifecycle passes; durable full-workflow TTL in progress |
| Human/CI/agent access | Expiring guest role/token, CLI, loopback tunnel, exclusive private output file | Viewer permission and cleanup CI in progress |
| Workloads | Pinned cert-manager, native policy, Spark Operator and Trino fixtures with functional probes | cert-manager, Spark and native policy passed at fa2889c; Trino query passed but teardown failed |
| Maintenance and packaging | Embedded operator chart, four CLI builds, chart archive/checksums, read-only update checker | Local packaging and actual Helm key-preserving upgrade pass |
| Kubernetes compatibility | Host 1.35/1.36 live matrix, pinned 1.36 guest, chart render 1.35–1.37 | Original 1.36 runtime path passed; new full matrix in progress |

`make check` passes locally: race tests, exact upstream chart contracts, real API-server integration, vet and both builds. Packaging for Linux/macOS amd64/arm64 was built and its archive checksums inspected. These checks do not replace real vCluster and workload execution.

The [current draft PR](https://github.com/nimeshbuilds/cluster-replica/pull/8) records ongoing CI. The [runtime validation record](validation.md) identifies narrower evidence already obtained. Do not infer that a test passed simply because its fixture is present.

## Still incomplete

- Configured vCluster Platform provisioning and lifecycle qualification. Requests currently report `PlatformQualificationRequired`; Helm fallback is blocked.
- Cloud identity adapters and authenticated exchanges: IRSA, EKS Pod Identity, Azure/GCP workload identity. Source tokens are not cloned; unadapted annotations block capture.
- Data content copying, CSI snapshot/restore workflows and external data lifecycle. Current data support provisions explicitly granted fresh volumes only.
- External Secrets backend recreation and arbitrary operator-specific dependencies, lifecycle hooks or bootstrap cycles.
- Per-user gateway delegation and automated exact-Secret RBAC distribution. The current model is administrator-granted namespace delegation.
- Public container/release publication, signed provenance, large encrypted object-store captures, scale/chaos qualification and GA readiness.
- OpenShift/RKE2/cloud distribution qualification and external design-partner validation.

Cloud labs being deferred does not mark cloud features complete. Kubernetes minor versions and actual API/capability evidence determine the portable path; a distribution's name alone is not a compatibility guarantee.
