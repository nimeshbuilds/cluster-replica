# Upstream provenance and dependency updates

Initial candidate, 19 September 2026:

| Dependency | Pin | Source |
| --- | --- | --- |
| vCluster chart | 0.37.1 | [Release](https://github.com/loft-sh/vcluster/releases/tag/v0.37.1), [chart index](https://charts.loft.sh/index.yaml) |
| Chart SHA-256 | `afb57fb5f2e3088519ffa9112fa0bfc3543bc3465f86f7e7e655ba30aea0d969` | [Archive](https://charts.loft.sh/charts/vcluster-0.37.1.tgz) |
| Guest Kubernetes | v1.36.0 | [Pinned upstream values](https://github.com/loft-sh/vcluster/blob/v0.37.1/chart/values.yaml) |
| controller-runtime | v0.25.1 | [Release](https://github.com/kubernetes-sigs/controller-runtime/releases/tag/v0.25.1) |
| Helm SDK | v4.3.0 | [Release](https://github.com/helm/helm/releases/tag/v4.3.0) |
| Kubernetes Go libraries | v0.37.0 | Resolved in `go.mod` with the above releases |
| controller-gen | v0.22.0 | [Release](https://github.com/kubernetes-sigs/controller-tools/releases/tag/v0.22.0) |
| envtest API server | v1.37.0 | [Test binaries](https://github.com/kubernetes-sigs/controller-tools/releases/tag/envtest-v1.37.0) |

These are development pins, not a certified compatibility claim. No upstream source or chart archive is vendored here. Downloads are checked before use; upstream vCluster and guest images are still tag-pinned, so their digest locking remains runtime maintenance work. The published Replicove operator chart and YAML pin the operator image digest.

## Update checklist

1. Review upstream configuration, security, support, and licensing changes.
2. Add a new immutable profile/translator; retain old profiles while their requests exist. Update archive provenance and the hash.
3. Run schema/render tests and review the complete resource/RBAC diff. A new cluster-scoped resource or retained PVC changes the lifecycle contract.
4. Run real host/guest tests for provisioning, access, a workload, restart, source replication, and cleanup. Document the tested versions and capabilities.
5. Mark a profile certified only after the required behavioral evidence exists. Existing requests never move to the new profile automatically.

Automated dependency PRs help find releases. They must not automatically publish a new certified profile. Cleanup must continue without downloading an old chart.

## Guest TLS routing

The pinned [vCluster serving-certificate implementation](https://github.com/loft-sh/vcluster/blob/v0.37.1/pkg/server/cert/cert.go) signs `RELEASE.NAMESPACE`, not `RELEASE.NAMESPACE.svc`. Replicove routes to the Service's `.svc:443` address and verifies the signed `RELEASE.NAMESPACE` TLS name against the exported CA. The same verified name is retained in scoped guest kubeconfigs when the CLI opens a loopback tunnel. TLS verification is never disabled.
