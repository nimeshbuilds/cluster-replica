# Maintaining Replicove

Upstream vCluster behavior is concentrated in `internal/catalog` (pins and values) and `internal/runtime/helm` (install, observe, ownership, teardown). The generic capture/planner/workflow and public CRDs do not embed arbitrary upstream values. Existing captures retain their exact profile and runtime reference; an operator upgrade does not silently move a running replica to a new chart.

1. Run `python3 hack/check-vcluster-update.py` to compare the pinned release with the official upstream release. This is read-only.
2. Review upstream release notes and the OSS chart/image. Add a new profile alongside the old one; preserve old cleanup adapters for outstanding finalizers.
3. Download the exact chart, verify its provenance and SHA-256, and update the relevant catalog entry and tool pins. Never use a floating chart tag or automatically trust a new hash from an unrelated mirror.
4. Run `make generate` to update CRDs and the native YAML installer from the same chart, then `make docs` to regenerate and check API/CLI references. Render every profile through `make test-contract`. Check supported Kubernetes API versions, namespaced RBAC, image pins, hooks, workload type, PVC retention and cleanup inventory.
5. Run `make check`, then the real kind workflows. Require actual installation, workload, operator restart, control-plane rescheduling, guest access, deletion and TTL results. Chart rendering alone does not qualify a release.
6. Review resource compatibility changes and keep migration/recovery notes. Only then change a default profile.

Kubernetes compatibility follows served APIs and tested minors. A host distribution name is not sufficient proof. The matrix currently targets Kubernetes 1.35/1.36 hosts and a pinned 1.36 guest; the chart contract also renders against 1.37. Record actual CI results separately. Cloud identity, CSI drivers, SCC/admission policy, CNI behavior, and Platform are additional adapter qualification dimensions.

Dependabot already groups Kubernetes/controller-runtime/Helm module updates and checks GitHub Actions and base images. These changes go through review and the same CI; dependency updates do not authorize automatic rollout.

## Publish an alpha

1. Commit a new `Chart.yaml` version/appVersion and default image tag, update quickstart/release links and `docs/releases/alpha.md`, then run `make generate` and `make docs`.
2. Merge after CI passes; wait for the exact merged `main` commit to pass all CI jobs too. The workflow enforces that commit gate.
3. Dispatch **Publish alpha release** from `main` with a new version such as `v0.1.0-alpha.2`. It publishes only explicit alpha tags and refuses an existing operator tag or release. It never moves `latest`.
4. The workflow builds Linux amd64/arm64 images with source/version/revision labels and OCI SBOM/provenance metadata. It packages a digest-pinned chart and YAML plus four macOS/Linux CLI archives and checksums. The OCI chart is published at `ghcr.io/nimeshbuilds/charts/replicove`.
5. For the first publication of each GHCR package, set its visibility to **Public** in GitHub package settings. Repository visibility alone does not make a new package public. Anonymous verification waits for this setup and fails if access remains private.
6. Fresh runners pull without registry login, verify platforms, source commit and checksums, exercise real Helm installation with new/existing vClusters, and start both Linux arm64 binaries. Only then is the GitHub prerelease created with its test-run link.

For local packaging, run `./hack/fetch-helm.sh` then `./hack/release-artifacts.sh vVERSION`. Output goes into a new, empty `dist/vVERSION/` directory. Set `RELEASE_IMAGE_DIGEST` to the verified published image digest when preparing distributable chart/YAML artifacts. The release workflow supplies it automatically.

If verification fails after publication, retain the failed run and rerun **failed jobs** after fixing the external issue. Do not replace an existing version tag. A source change requires a new version and a newly tested commit. OCI build metadata is not a separate signed release attestation; upstream runtime image digest locking remains tracked in issue #6.

For an operator chart upgrade, retain its system namespace and immutable encryption key. Test restoring the key and sealed records before changing storage implementation. Never delete state or force finalizers to make an upgrade appear successful.
