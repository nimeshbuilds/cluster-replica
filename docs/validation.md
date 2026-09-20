# Validation record

## Enabling mirrors after installation

[CI run 35520786306](https://github.com/nimeshbuilds/replicove/actions/runs/35520786306) passed all **13 jobs** at `36947dd`. The [saved transition and lifecycle reports](validation/2026-09-20-enable-mirroring.json) cover three starting points: published 0.1.0-alpha.1 Helm, published 0.2.0-alpha.1 Helm, and rendered 0.2.0-alpha.1 native YAML, all initially without mirroring enabled. The target chart/image was built from the 0.2.0-alpha.2 source in that revision.

Each path passed 11 transition checks and the full 25-scenario mirror lifecycle. A persistent ordinary replica kept its runtime/guest identity, changed guest configuration, original TTL, encryption key, destination, installation settings and existing access across enablement. A fresh guest session verified that the upgraded operator could read its earlier encrypted state. The native path also replaced its completed bootstrap Job and reused the key without creating a Helm release. The fixture explicitly cleaned this ordinary replica only after those assertions to free the one-runtime destination for the subsequent mirror suite.

The same local API regression caught and now covers exclusion of the mirror source RoleBinding during broad application capture. Chart contracts require infrastructure labels on all bundled mirror resources. The [upgrade guide](guides/enable-mirroring.md) records CRD ordering, image selection, value preservation, source authorization and cleanup requirements.

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
