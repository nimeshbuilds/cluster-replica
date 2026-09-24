# Validation record

## Alpha.2 release qualification

[**v0.3.0-alpha.2**](https://github.com/nimeshbuilds/replicove/releases/tag/v0.3.0-alpha.2) is published from [`afa15cb`](https://github.com/nimeshbuilds/replicove/commit/afa15cb1723ccb9d2a204b8aeafe88231e782d24). All **16 exact-source CI jobs** passed in [run 36045569511](https://github.com/nimeshbuilds/replicove/actions/runs/36045569511); all **11 release workflow jobs** then passed in [run 36049603331](https://github.com/nimeshbuilds/replicove/actions/runs/36049603331). The release gates cover source verification, host-minor workflows, workload adapters and published image/chart/CLI/native installation paths. They are separate from the expanded documentation scenarios below.

The [download verification record](validation/2026-09-24-downloads.json) records anonymous public downloads, SHA-256 checks, the four CLI archive contents/architectures/source revision, all six CRDs and matching chart/native image digests. The actual download guide and matching tagged source build were smoke-tested on macOS arm64, including `--version` and `--help`. This does not claim native execution on all four platforms. The [download guide](getting-started/download.md) offers prebuilt binaries and a normal-branch source-build option.

The ten guides expand to **16 released-artifact variants**. Their [sanitized scenario evidence](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-v0.3.0-alpha.2-scenarios.json) records the exact fixture revision, every job outcome, pinned release checksums, functional assertions and cleanup outcomes. This supplemental record is published only after all 16 variants pass at the same revision. It preserves the expanded YAML viewer/deployer checks, all three PostgreSQL mask strategies, mirror schedule suspension/resumption and retention, live pools, MCP and dashboard checks separately from the earlier release fixtures. The [scenario workflow](https://github.com/nimeshbuilds/replicove/actions/workflows/scenarios.yaml) shows subsequent runs; a pending or failed run is not passing evidence.

These are small disposable kind-cluster checks using the recorded Kubernetes, vCluster, CNI and CSI versions. They cover the documented workflows, not every API combination, production scale, arbitrary host distributions, cloud identity or cloud storage erasure. Read the [scenario prerequisites and limits](scenarios/index.md) before running them.

## Owned PVC cleanup regression

The expanded governed-replica lab exposed an untested cleanup case in published **v0.3.0-alpha.1**. At fixture revision [`1466f64`](https://github.com/nimeshbuilds/replicove/commit/1466f6462299543aff20db572f98a8c4f516fce3), both the [CLI variant](https://github.com/nimeshbuilds/replicove/actions/runs/36035987682/job/107756280891) and [Helm variant](https://github.com/nimeshbuilds/replicove/actions/runs/36035987682/job/107756280937) failed their owned-cleanup wait. A guest-created completed probe still referenced the copied PVC; serial cleanup waited for that claim before requesting deletion of the owned guest namespace, leaving its consumer in place.

The **v0.3.0-alpha.2** fix permits progress past a pending ordinary PVC only when its guest namespace is also inventoried as owned. Pre-existing guest namespaces reused without adoption and their foreign consumers remain protected, ownership conflicts remain blocking, and custom-resource/controller dependencies retain their cleanup order. Its [source qualification passed](#cleanup-fix-source-qualification), followed by the exact-source and release gates [recorded above](#alpha2-release-qualification). The separate scenario record identifies the expanded variants and their matching fixture revision.

This failure does not rewrite earlier passing records: those fixtures did not exercise this additional volume-consumer case. Their exact revision, tested behavior and limits remain below. See [cleanup behavior and recovery](guides/cleanup.md#guest-created-volume-consumers) for the alpha.1 limitation and published alpha.2 fix.

## Cleanup fix source qualification

**All 16 source CI jobs passed** at [`3edb5db`](https://github.com/nimeshbuilds/replicove/commit/3edb5db856df0d30c631f98a851334e9f265e121) in [run 36039203779](https://github.com/nimeshbuilds/replicove/actions/runs/36039203779). The [sanitized source record](validation/2026-09-24-cleanup-source.json) retains exact job and artifact references, runtime pins, the two core reports' 33 assertions each, volume counts and TTL outcomes. It contains no resource bodies, logs, volume identities or credentials. The [strict documentation build](https://github.com/nimeshbuilds/replicove/actions/runs/36039203845/job/107766837974) also passed at that revision; the PR's Pages deployment job was skipped.

| Expanded core workflow | Observed result |
| --- | --- |
| [Kubernetes 1.36.4, Helm installer](https://github.com/nimeshbuilds/replicove/actions/runs/36039203779/job/107766838574) | 33 assertions passed; both recorded owned PVs disappeared after explicit deletion, and both new PVs disappeared after the real 300-second TTL |
| [Kubernetes 1.35.8, CLI installer](https://github.com/nimeshbuilds/replicove/actions/runs/36039203779/job/107766838585) | 33 assertions passed; both recorded owned PVs disappeared after explicit deletion, and both new PVs disappeared after the real 300-second TTL |

These workflows exercised the extra guest-created volume consumer that stalled alpha.1 cleanup. Both observed `kubernetes.io/pvc-protection` on the copied claim before deletion, verified source volume identity/data preservation, and checked cleanup before removing the disposable kind host. The expanded assertions also cover name/label/expression selection, dependency-only resources, omissions, plan metadata, independent empty volumes, Secret Snapshot/Follow behavior, guest drift, refresh and pruning. They use vCluster 0.37.1 and guest Kubernetes 1.36.0. Unknown PVC finalizers and pre-existing guest namespaces retain their cleanup barriers; these results do not qualify arbitrary CSI drivers or external storage erasure.

[PR #17](https://github.com/nimeshbuilds/replicove/pull/17) merged this source into [`aa996fc`](https://github.com/nimeshbuilds/replicove/commit/aa996fc1eb92446581833ad2e3baef09381cd2a2). At the time that source record was saved, [exact-main CI](https://github.com/nimeshbuilds/replicove/actions/runs/36042461247) was still running; it subsequently passed all 16 jobs. The final release source and published artifacts passed the later gates [recorded above](#alpha2-release-qualification). PostgreSQL Null/Constant additions, mirror retention/resumption and live pool/MCP/dashboard scenario fixtures are outside this historical source record; their results belong to the separate scenario evidence.

## v0.3.0-alpha.1 source qualification

**All 16 jobs passed** at source revision [`93bcfbc`](https://github.com/nimeshbuilds/replicove/commit/93bcfbcd7246f7f7bc8449e3f12cf4ff61642553) in [CI run 35948951593](https://github.com/nimeshbuilds/replicove/actions/runs/35948951593). The [sanitized aggregate record](validation/2026-09-24-features.json) preserves job links, PostgreSQL/chaos/runner reports, and all three mirror transition/lifecycle/queue reports. This is source-build PR evidence, not a published-release assertion. Verification includes race-enabled Go tests, pinned chart contracts, real API-server integration, vet and builds. Source CI is separate from the required exact-main and published-artifact release gates.

| Source job | Observed behavior | Scope |
| --- | --- | --- |
| Core and installation | Both host-minor workflows, original live vCluster lifecycle, Helm existing-target installation, native YAML and all four cert-manager/Spark/Trino/admission-policy fixtures passed | Same pinned runtime; small functional workloads, no scale or cloud certification |
| Mirrors | All three current/native/previous paths passed 11 late-enable checks, 25 lifecycle scenarios and the explicit CapacityLimit queue/TTL assertion | CSI host-path and Calico; independent per-volume recovery points; no atomic multi-volume or database-consistency claim |
| [PostgreSQL](https://github.com/nimeshbuilds/replicove/actions/runs/35948951593/job/107473092764) | Actual PostgreSQL 17 dump/restore, masks and explicit table subsets, validated foreign keys, source read-only account/preservation, raw fixture value absent from final storage; 15 operator scenarios include late enablement, application/access staging gates, Calico isolation, restart, rejected refresh and owned cleanup | One generated database, new managed target, host-path storage; no general anonymization, private-CA or arbitrary-schema qualification; deletion tested, not a real database TTL wait |
| [Chaos](https://github.com/nimeshbuilds/replicove/actions/runs/35948951593/job/107473092601) | 16 scenarios covering late enablement, target/source denial, six fault types, restart/expiry rollback, host isolation/conflict checks, source preservation and parent cleanup | Bounded shared-worker workloads; no privileged host/node faults or production reliability claim |
| [Test runner](https://github.com/nimeshbuilds/replicove/actions/runs/35948951593/job/107473092788) | Fresh success, exit-17 failure, timeout and retain-on-failure runs; resolved ScaleZero rollback, JSON/JUnit outcomes, original TTL, credential revocation and owned cleanup | Managed ClusterReplica runs; mirror leases covered by local tests and the separate mirror suite, not this runner fixture |

These new live paths use a Kubernetes 1.36.4 kind host, vCluster 0.37.1 and guest Kubernetes 1.36.0. PostgreSQL and chaos use pinned Calico v3.32.2. The source PostgreSQL fixture explicitly selects TLS `require`; it does not qualify private-CA `verify-full` deployment. Local metadata/API tests cover preflight, plan provenance, pool rendering/admission, stdio MCP and the loopback dashboard. They do not certify a remote multi-user service.

After this source run, both raw-data absence probes were strengthened to require `PG_VERSION` and distinguish grep errors from no matches. Local tests cover found/absent/error/missing-directory cases; the strengthened fixtures must pass the later exact-main and release suites. The `93bcfbc` report retains its original probe scope.

The release workflow creates the version tag only after exact-main CI and published-artifact verification pass. The release notes link that separate artifact run. All earlier records below remain evidence for their named historical revisions and versions.

## Enabling mirrors after installation

[CI run 35520786306](https://github.com/nimeshbuilds/replicove/actions/runs/35520786306) passed all **13 jobs** at `36947dd`. The [saved transition and lifecycle reports](validation/2026-09-20-enable-mirroring.json) cover three starting points: published 0.1.0-alpha.1 Helm, published 0.2.0-alpha.1 Helm, and rendered 0.2.0-alpha.1 native YAML, all initially without mirroring enabled. The target chart/image was built from the 0.2.0-alpha.2 source in that revision.

Each path passed 11 transition checks and the full 25-scenario mirror lifecycle. A persistent ordinary replica kept its runtime/guest identity, changed guest configuration, original TTL, encryption key, destination, installation settings and existing access across enablement. A fresh guest session verified that the upgraded operator could read its earlier encrypted state. The native path also replaced its completed bootstrap Job and reused the key without creating a Helm release. The fixture explicitly cleaned this ordinary replica only after those assertions to free the one-runtime destination for the subsequent mirror suite.

The same local API regression caught and now covers exclusion of the mirror source RoleBinding during broad application capture. Chart contracts require infrastructure labels on all bundled mirror resources. The [upgrade guide](guides/enable-mirroring.md) records CRD ordering, image selection, value preservation, source authorization and cleanup requirements.

An additional local Helm/API regression covers opt-in `stateKey.bootstrap: true`: changing the Job image is rejected as immutable, explicitly removing the old Job permits the upgrade, and Helm preserves the retained key's UID and bytes. This API test has no Job controller; actual bootstrap execution and completed-Job replacement are covered by the native live path above.

These live paths use the mirror fixture's Kubernetes 1.36.4 host, vCluster 0.37.1/guest 1.36.0, CSI host-path and Calico. They do not certify arbitrary GitOps pruning, cloud drivers, application-consistent databases or production scale. The alpha release workflow requires all three paths again against the published target image, OCI chart and CLI; the [0.2.0-alpha.2 release](https://github.com/nimeshbuilds/replicove/releases/tag/v0.2.0-alpha.2) links that separate artifact-verification run.

## Workload mirror release qualification

[CI run 35513679668](https://github.com/nimeshbuilds/replicove/actions/runs/35513679668) passed all 11 jobs at `6c08d5c`, merged as `35e40ee`. Its live mirror job passed all 25 scenarios in the [saved mirror report](validation/2026-09-20-mirrors.json), including copied file contents, latest/saved/scheduled resets, guest isolation and complete owned cleanup with source identity and data preserved. Subsequent main and published-artifact runs must pass independently before release.

The optional module's `workload-mirrors` job is mandatory in the [current CI workflow](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml). The alpha publishing workflow requires the exact main commit to pass all jobs, then runs the mirror fixture again against the published image, OCI chart and released CLI before creating the GitHub release. Its release notes link the exact source and artifact-verification run.

The fixture uses Kubernetes 1.36.4, vCluster 0.37.1/Kubernetes 1.36.0, upstream CSI host-path manifests v1.18.0 (driver image v1.17.1), snapshot-controller v8.6.0, and Calico v3.32.2. It checks actual file contents, independent guest writes, latest-source and retained-revision resets, scheduling, leases, cancellation, source-egress denial and guest DNS, source authorization, existing-runtime namespace/RBAC boundaries, minimum TTL, restarts, Helm upgrade/reuse and owned cleanup. Source PVC/PV identity and contents must survive. The small host-path fixture does not qualify cloud drivers, application-consistent databases, arbitrary CNI behavior or large data sets.

Local checks cover ownership forgery, source/backend identity changes, protected active revision retention, recovery after interrupted enrollment, partial restore inventory/deletion, bounded lifetime, queue overflow cleanup, API admission, example manifests, dependency checksums and optional chart modes. The full CLI reference is discovered from the binary; API reference comes from every generated CRD. Strict documentation builds check links and anchors.

The older records below remain historical evidence for the original portable alpha, separate from the new mirror suite.

## Native YAML installation and lifecycle

The [kubectl-only YAML job](https://github.com/nimeshbuilds/replicove/actions/runs/35477718800/job/105989750205) passed at [`b615d8a`](https://github.com/nimeshbuilds/replicove/commit/b615d8ab36bc03baf2ec714d503353b61976fffc). It applied the public CRDs and Kustomize installer, ran the restricted state-key bootstrap Job, created a persistent vCluster, verified mapped configuration and source preservation, issued and revoked a bounded viewer session, and verified owned cleanup. Re-running the bootstrap Job preserved the same immutable key while encrypted state existed.

The [sanitized report](validation/2026-09-19-yaml.json) contains only outcome flags. This fixture uses the pinned Kubernetes 1.36.4 kind host, vCluster 0.37.1 and Kubernetes 1.36.0 guest. It requires neither the Replicove CLI nor Helm CLI. This deletion scenario does not replace the separate core workflow's real TTL, restart, secret-follow and existing-target evidence.

## Integrated portable alpha

[CI run 35472630195](https://github.com/nimeshbuilds/replicove/actions/runs/35472630195) passed **all eight jobs** at `c2c5ea3`. The same file contents were merged into `main` as `c5d4d62`. Both full host workflows verified explicit operator permissions, rejection of wildcard Role escalation and cluster-admin bindings, exact-Secret credential readers, access revocation, owned deletion, and a real five-minute TTL with control-plane PVC cleanup. The runtime lifecycle and all four functional workload suites also passed.

The [first replica quickstart](../QUICKSTART.md) now builds from the normal `main` branch. This record preserves exact tested revisions for traceability; following the guide does not require a detached checkout. [Current main CI](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml?query=branch%3Amain) reports subsequent runs.

## Earlier complete portable workflow

[CI run 35466203137](https://github.com/nimeshbuilds/replicove/actions/runs/35466203137) passed **all eight jobs** at commit `8123f9a`: verification, the original runtime lifecycle, both full-workflow host minors, and cert-manager/Spark/Trino/native-policy workloads. The [saved portable evidence](validation/2026-09-19-portable.json) records versions, runtime pins, TTL timestamps, scenarios and observed workload images.

The full workflow passed on Kubernetes **1.35.8 and 1.36.4**, with a pinned vCluster 0.37.1 / Kubernetes 1.36.0 guest. It verified embedded installation, manual approval, source Helm reconstruction, Secret snapshot/follow, namespace maps/overrides, a guest workload and HTTP Service probe, operator restart, durable control-plane replacement, tunnel reconnection, explicit refresh, viewer RBAC, access revocation, owned cleanup, existing-target conflict/preservation, source preservation, and a real five-minute TTL with control-plane PVC removal.

| Workload | Passed guest behavior and cleanup |
| --- | --- |
| cert-manager | Recreated Issuer/Certificate and generated a TLS Secret |
| Spark Operator | Recreated SparkApplication and completed a real Spark Pi calculation |
| Trino | Returned `25` from `SELECT count(*) FROM tpch.tiny.nation`; retained usable access after the query |
| Native admission policy | Rejected an unlabelled ConfigMap probe and accepted a labelled probe |

These are small disposable-cluster integration checks. They do not certify production scale, cloud identity exchange, cloud storage erasure, external data restoration, Platform, or vendor-specific behavior. Later code changes must pass current-head CI.

The optional exact-Secret credential-reader RBAC addition came after `8123f9a` and passed the later integrated workflow recorded above; it is not retroactively attributed to this earlier run. Local tests also cover Helm installation/key-preserving upgrade, exact-grant capture, target identity, stable status, preserved drift, and missing access-state recovery. Linux/macOS amd64/arm64 packaging was built and its archives/checksums inspected.

Earlier failed runs exposed installer wait-strategy, nil Helm values, infrastructure-capture, TLS name, test image and tunnel teardown problems. Those issues were corrected before the all-green run. The [earlier individual workload evidence](validation/2026-09-19-workloads.json) remains historical.

## Live host and guest lifecycle

The [19 September 2026 live run](https://github.com/nimeshbuilds/replicove/actions/runs/35454520096) passed at commit `dd999a07c99ce6fcad175830229c5d50323f853b`. It built the actual operator image and deployed it under the sample namespace-scoped service account on a disposable kind cluster. Both `verify` and `live-vcluster` completed successfully.

| Component | Observed version |
| --- | --- |
| Host | Kubernetes `v1.36.4`, kind `v0.33.0`, Linux amd64 |
| vCluster chart and OSS image tag | `0.37.1` |
| Guest API server | Kubernetes `v1.36.0` |
| Profile and cleanup policy | `vcluster-0.37.1-lab`, `HelmReleaseOnly` |

The [saved report](validation/2026-09-19-kind.json) records the exact node image, chart hash, observed container image IDs, TTL timestamps, and inventory delta. Detailed metadata-only inventories and host API discovery are also retained as CI artifacts for seven days.

Passed checks:

- Apply one `ClusterReplica`; the operator downloads the pinned chart, installs a real vCluster, and reports runtime readiness. No preinstalled vCluster is used.
- Connect to the guest API with `vcluster connect`; schedule a Deployment and complete an HTTP probe using guest DNS and a Service.
- Restart the operator, retain the release identity and original expiry, and repeat the guest HTTP/DNS probe successfully.
- Expire the request using its real eight-minute TTL, remove the chart resources and Helm history, and observe no subsequent installation during the post-expiry check.
- Explicitly delete another running replica; then induce an installation failure with a Deployment quota and delete that failed replica.
- Preserve the host namespace, an unrelated ConfigMap, and the test's ResourceQuota.

The request was created at `16:21:00Z` and reached `Expired` at its original `16:29:00Z` deadline. The inventory delta after TTL contained only the retained `ClusterReplica` record and the replacement operator Pod/ReplicaSet from the restart. No guest pods, Services, or Secrets remained among the enumerated resource types in `replica-lab` for this fixture.

This fixture creates no PVCs, snapshots, cloud identities, or external data. Their cleanup and credential revocation remain unverified. This is a stateless lifecycle smoke test for one exact host/guest combination, not a general compatibility certification. That original fixture does not cover source capture, scoped access, identity, data or operator replication. The newer portable workload evidence above is separate; cloud/distribution qualification remains incomplete.

The [first live attempt](https://github.com/nimeshbuilds/replicove/actions/runs/35454086084) installed and connected to vCluster but failed because the test replaced the workload image's entrypoint with a subcommand. The smoke command was corrected to `/agnhost netexec`; the successful run above uses that correction. Guest diagnostics are now retained on failure, with Secret payloads and arbitrary manifests excluded.

## Initial local and API-server validation

Local validation completed on 19 September 2026 using Go 1.27.1 on macOS arm64:

- `make generate`: DeepCopy code and CRD generated from the API types.
- `go test -race ./...`: passed.
- `make test-contract`: the pinned upstream vCluster 0.37.1 chart passed schema/render checks for host capability inputs 1.35, 1.36 and 1.37.
- `make test-integration`: passed against envtest Kubernetes 1.37.0. Covered API admission and immutable spec, durable preparation before side effects, status/finalizers/expiry, actual Helm chart resource installation, repeated observation without chart access, refusal to delete a foreign object, and preservation/purge of Helm history around partial deletion.
- `go vet ./...` and `make build`: passed. The binary help command was also checked.

These initial local checks did not run a vCluster pod or guest workload; the live evidence above is a separate suite. There is still no complete cleanup claim. The [testing guide](testing.md) defines the broader acceptance gates. Ongoing results are visible in [GitHub Actions](https://github.com/nimeshbuilds/replicove/actions).
