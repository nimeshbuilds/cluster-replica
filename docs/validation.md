# Validation record

## Live host and guest lifecycle

The [19 September 2026 live run](https://github.com/nimeshbuilds/cluster-replica/actions/runs/35454520096) passed at commit `dd999a07c99ce6fcad175830229c5d50323f853b`. It built the actual operator image and deployed it under the sample namespace-scoped service account on a disposable kind cluster. Both `verify` and `live-vcluster` completed successfully.

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

This fixture creates no PVCs, snapshots, cloud identities, or external data. Their cleanup and credential revocation remain unverified. This is a stateless lifecycle smoke test for one exact host/guest combination, not a general compatibility certification. Source discovery, configuration/operator/secret replication, existing-vCluster integration, scoped agent access, IRSA, Spark, Trino, and cloud/distribution scenarios are not covered or implemented by this change.

The [first live attempt](https://github.com/nimeshbuilds/cluster-replica/actions/runs/35454086084) installed and connected to vCluster but failed because the test replaced the workload image's entrypoint with a subcommand. The smoke command was corrected to `/agnhost netexec`; the successful run above uses that correction. Guest diagnostics are now retained on failure, with Secret payloads and arbitrary manifests excluded.

## Initial local and API-server validation

Local validation completed on 19 September 2026 using Go 1.27.1 on macOS arm64:

- `make generate`: DeepCopy code and CRD generated from the API types.
- `go test -race ./...`: passed.
- `make test-contract`: the pinned upstream vCluster 0.37.1 chart passed schema/render checks for host capability inputs 1.35, 1.36 and 1.37.
- `make test-integration`: passed against envtest Kubernetes 1.37.0. Covered API admission and immutable spec, durable preparation before side effects, status/finalizers/expiry, actual Helm chart resource installation, repeated observation without chart access, refusal to delete a foreign object, and preservation/purge of Helm history around partial deletion.
- `go vet ./...` and `make build`: passed. The binary help command was also checked.

These initial local checks did not run a vCluster pod or guest workload; the live evidence above is a separate suite. There is still no complete cleanup claim. The [testing guide](testing.md) defines the broader acceptance gates. Ongoing results are visible in [GitHub Actions](https://github.com/nimeshbuilds/cluster-replica/actions).
