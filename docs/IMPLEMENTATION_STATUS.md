# Replicove implementation ledger

Requested scope: the full ClusterReplica product plan, researched product branding and a product icon. The user selected disposable CI clusters now and deferred cloud labs. This is a substantial portable implementation, not completion of every phase in the original plan.

## v0.3.0-alpha.2 cleanup correction

The implementation now allows cleanup to progress past an ordinary protected PVC when its inventoried guest namespace is also still owned. This lets namespace deletion remove guest-created volume consumers. Borrowed namespaces, unknown PVC finalizers and resource/controller dependency barriers remain protected. The published alpha.2 passed all 16 exact-source CI jobs and all 11 release jobs. [Current validation](validation.md#alpha2-release-qualification) separates those gates from the released-artifact scenario matrix; the historical records below retain their original scope.

## Implemented portable alpha

| Area | Implementation evidence | Live qualification |
| --- | --- | --- |
| Replicove name and icon | Collision research and generated asset in `docs/brand` / `assets/brand` | Delivered |
| Standalone vCluster lifecycle | Pinned Helm SDK provider, ownership, deletion and TTL | Passed original real kind/vCluster fixture repeatedly |
| Administrator grants and encrypted state | Namespace policy, explicit operator RBAC, exact credential grants, AES-GCM, bounded records, UID/RV checks | Passed at c2c5ea3 on hosts 1.35.8/1.36.4 |
| Discovery and planning | Kubernetes/Helm capture, selectors, graph, mappings, patches, overrides | Real API regression and both full workflows pass |
| Generic replication | Intent before writes, UID ownership, CRDs, readiness and foreign-object refusal | Both full workflows and all four workload fixtures pass |
| Secrets and refresh | Snapshot/follow, token exclusions, explicit refresh, drift, preserved added fields | Passed at c2c5ea3 on hosts 1.35.8/1.36.4 |
| Existing target | Protected data-only credentials, pinned guest identity, no runtime adoption | Real API tests and live preservation/conflict checks pass |
| Ephemeral cleanup | Guest/host inventory, finalizers, access revocation, bound volume checks | Stateless lifecycle and both durable TTL/PVC cleanup workflows pass |
| Human/CI/agent access | Expiring guest role/token, exact-Secret reader RBAC, CLI/tunnel recovery, private output file | Viewer permissions, exact-Secret reader authorization/revocation and cleanup passed on both host minors |
| Workload mirrors | Optional CSI captures, independent generations, manual/scheduled reset, leases, exact volume grants, access and cleanup | See the versioned [mirror verification record](validation.md) |
| Workloads | Pinned cert-manager, native policy, Spark Operator and Trino fixtures with functional probes | All four functional suites and cleanup passed at c2c5ea3 |
| Maintenance and packaging | Embedded operator chart, four CLI builds, chart archive/checksums, read-only update checker | Local packaging and actual Helm key-preserving upgrade pass |
| Kubernetes compatibility | Host 1.35/1.36 live matrix, pinned 1.36 guest, chart render 1.35–1.37 | Full 1.35.8/1.36.4 workflows pass with 1.36.0 guest |

The original portable-alpha local checks covered: race tests, exact upstream chart contracts, real API-server integration, vet and both builds. Packaging for Linux/macOS amd64/arm64 was built and its archive checksums inspected. These checks do not replace real vCluster and workload execution.

The [eight-job integrated-alpha run at c2c5ea3](https://github.com/nimeshbuilds/replicove/actions/runs/35472630195) passed, including operator authorization and credential cleanup. Its file contents were merged unchanged into main. The [CI workflow](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml?query=branch%3Amain) records subsequent checks. The [runtime validation record](validation.md) identifies narrower evidence already obtained. Do not infer that a test passed simply because its fixture is present.

## Implemented v0.3.0-alpha.1 features

At `93bcfbc`, all 16 source CI jobs passed. [Validation](validation.md) links the jobs and sanitized reports; later exact-main and published-artifact checks remain separate gates. Keep historical records above attached to their original revisions.

| Area | Implementation and guide | Recorded checks |
| --- | --- | --- |
| Preflight and plan evidence | `internal/diagnostics`, source provenance in capture/planner; [diagnostics](guides/diagnostics.md) | Local/API tests passed |
| Test recipes | `internal/testrun`, fresh environments, bounded commands/leases/access, report/JUnit and verified cleanup; [test runs](guides/test-runs.md) | Four fresh managed runs passed with distinct execution/cleanup outcomes |
| Pools and admission | `internal/capacity`, encrypted reservations and grant caps, offline CLI pool renderer; [pools](guides/pools.md) | Local concurrent admission/offline render checks and three live mirror queue/TTL checks passed |
| PostgreSQL 17 | `internal/database`, explicit source grants, staging isolation, approved masks/subsets, relationship checks and second clean restore; [database guide](guides/postgresql.md) | Real PostgreSQL and 15 operator scenarios passed |
| Six chaos fault types | `internal/chaos`, `ReplicaExperiment`, bounded grants and durable cleanup; [chaos](guides/chaos.md) | 16 live scenarios passed |
| Namespace-scoped stdio MCP | `internal/agentapi`, caller Kubernetes identity, read-only default and explicit mutation opt-in; [agents](guides/agents-mcp.md) | Local/API tests passed; no remote-service claim |
| Local read-only dashboard | `internal/dashboard`, loopback capability session and metadata views; [dashboard](guides/dashboard.md) | Local/API tests passed; no remote-service claim |

PostgreSQL and chaos join mirrors as optional modules that can be enabled on the existing installation with reviewed prerequisites, CRDs and RBAC. The API now contains six CRDs. No new service is needed for test recipes, pools, MCP or the dashboard.

## Still incomplete

- Remote multi-user MCP transport/delegation, Replicove-issued agent certificates/CA, and a shared authenticated dashboard. Current stdio and loopback interfaces deliberately use the caller's Kubernetes authority.

- Configured vCluster Platform provisioning and lifecycle qualification. Requests currently report `PlatformQualificationRequired`; Helm fallback is blocked.
- Cloud identity adapters and authenticated exchanges: IRSA, EKS Pod Identity, Azure/GCP workload identity. Source tokens are not cloned; unadapted annotations block capture.
- General database/PITR and grouped-volume recovery, automatic relational tenant extraction, non-CSI file adapters, cloud CSI qualification, and external data lifecycle. The scoped PostgreSQL 17 adapter does not cover these broader capabilities. The optional per-volume CSI mirror implementation is tracked in the [mirror guide](guides/mirrors.md); its disposable-cluster qualification is separate from the original alpha evidence.
- External Secrets backend recreation and arbitrary operator-specific dependencies, lifecycle hooks or bootstrap cycles.
- Per-user gateway delegation. Exact-Secret RBAC distribution is implemented for administrator-selected subjects; the authorization model remains namespace delegation.
- Digest-locked runtime images and automated candidate schema/resource/RBAC diffs ([maintenance issue #6](https://github.com/nimeshbuilds/replicove/issues/6)).
- Separately signed release attestations, large encrypted object-store captures, scale, fault/recovery qualification beyond the recorded small fixtures and GA readiness. Public alpha containers, OCI charts, CLI binaries, native YAML, checksums, and OCI build metadata are distributed by the release workflow.
- OpenShift/RKE2/cloud distribution qualification and external design-partner validation.

Cloud labs being deferred does not mark cloud features complete. Kubernetes minor versions and actual API/capability evidence determine the portable path; a distribution's name alone is not a compatibility guarantee.
