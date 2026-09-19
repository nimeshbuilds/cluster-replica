# Maintaining Replicove

Upstream vCluster behavior is concentrated in `internal/catalog` (pins and values) and `internal/runtime/helm` (install, observe, ownership, teardown). The generic capture/planner/workflow and public CRDs do not embed arbitrary upstream values. Existing captures retain their exact profile and runtime reference; an operator upgrade does not silently move a running replica to a new chart.

1. Run `python3 hack/check-vcluster-update.py` to compare the pinned release with the official upstream release. This is read-only.
2. Review upstream release notes and the OSS chart/image. Add a new profile alongside the old one; preserve old cleanup adapters for outstanding finalizers.
3. Download the exact chart, verify its provenance and SHA-256, and update the relevant catalog entry and tool pins. Never use a floating chart tag or automatically trust a new hash from an unrelated mirror.
4. Render every profile through `make test-contract`. Check supported Kubernetes API versions, namespaced RBAC, image pins, hooks, workload type, PVC retention and cleanup inventory.
5. Run `make check`, then the real kind workflows. Require actual installation, workload, operator restart, control-plane rescheduling, guest access, deletion and TTL results. Chart rendering alone does not qualify a release.
6. Review resource compatibility changes and keep migration/recovery notes. Only then change a default profile.

Kubernetes compatibility follows served APIs and tested minors. A host distribution name is not sufficient proof. The matrix currently targets Kubernetes 1.35/1.36 hosts and a pinned 1.36 guest; the chart contract also renders against 1.37. Record actual CI results separately. Cloud identity, CSI drivers, SCC/admission policy, CNI behavior, and Platform are additional adapter qualification dimensions.

Dependabot already groups Kubernetes/controller-runtime/Helm module updates and checks GitHub Actions and base images. These changes go through review and the same CI; dependency updates do not authorize automatic rollout.

`hack/release-artifacts.sh VERSION` produces Linux/macOS binaries for amd64/arm64, an installable chart, and SHA-256 checksums. The manual Release artifacts workflow builds these with read-only repository permissions. Publishing a public release/container and making it the documented default is a separate release action after validation; artifact packaging does not imply publication.

For an operator chart upgrade, retain its system namespace and immutable encryption key. Test restoring the key and sealed records before changing storage implementation. Never delete state or force finalizers to make an upgrade appear successful.
