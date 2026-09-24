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

1. Commit a new `Chart.yaml` version/appVersion and default image tag, update `docs/releases/alpha.md` and the relevant feature guides, then run `make generate` and `make docs`. Mark unreleased behavior clearly. Prepare the matching public quickstart/scenario version changes, but keep their qualification claims pending until the release assets and those exact commands are verified.
2. Merge after CI passes; wait for the exact merged `main` commit to pass all CI jobs too. The workflow enforces that commit gate.
3. Dispatch **Publish alpha release** from `main` with the new committed version, such as `v0.3.0-alpha.2`. It publishes only explicit alpha tags and refuses an existing operator tag or release. It never moves `latest`.
4. The workflow builds Linux amd64/arm64 images with source/version/revision labels and OCI SBOM/provenance metadata. It packages a digest-pinned chart and YAML plus four macOS/Linux CLI archives and checksums. The OCI chart is published at `ghcr.io/nimeshbuilds/charts/replicove`.
5. For the first publication of each GHCR package, set its visibility to **Public** in GitHub package settings. Repository visibility alone does not make a new package public. Anonymous verification waits for this setup and fails if access remains private.
6. Fresh runners pull without registry login and verify platforms, source commit and checksums. They exercise Helm installation with new/existing vClusters, native YAML, all three mirror upgrade paths, PostgreSQL, chaos and test recipes, and start both Linux arm64 binaries. Only then is the GitHub prerelease created with its test-run link.
7. Pin `examples/scenarios/catalog.json` to the public version, source commit, immutable image digest and verified CLI/chart/YAML asset checksums. Run all 16 documented variants through `examples/scenarios/run.sh`, including any strengthened feature assertions. Update matching quickstart commands, the validation record and sanitized evidence after those runs pass; build the docs strictly before publishing the collection. Source-built results do not substitute for this released-artifact run.

For local packaging, run `./hack/fetch-helm.sh` then `./hack/release-artifacts.sh vVERSION`. Output goes into a new, empty `dist/vVERSION/` directory. Set `RELEASE_IMAGE_DIGEST` to the verified published image digest when preparing distributable chart/YAML artifacts. The release workflow supplies it automatically.

If verification fails after publication, retain the failed run and rerun **failed jobs** after fixing the external issue. Do not replace an existing version tag. A source change requires a new version and a newly tested commit. OCI build metadata is not a separate signed release attestation; upstream runtime image digest locking remains tracked in issue #6.

For an operator chart upgrade, retain its system namespace and immutable encryption key. Test restoring the key and sealed records before changing storage implementation. Never delete state or force finalizers to make an upgrade appear successful.

## Mirror dependency upgrades

Keep snapshot APIs/controller versions in `charts/replicove/files/NOTICE.md`, vendored schema checksums, `mirrors.snapshotController.image`, and the mirror guide aligned. `TestMirrorDependencyContract` checks the exact upstream schemas, optional modes, and bounded source permissions. New drivers require real capture/restore/delete qualification rather than a chart-only test.

Preserve mirror infrastructure labels so broad source capture cannot select installation RBAC/controllers as application resources. Require the late-enable matrix from both pinned older Helm releases and native YAML, with a running guest and state-key preservation. When updating the matrix's starting versions, retain a pre-mirror release case while that upgrade remains documented and keep the fixture's digest/version pins together.

A vCluster upgrade must also qualify PVC snapshot dataSource translation, the explicit skip-translation annotation, guest object labels/UIDs, host egress selectors, and existing-runtime namespace separation. Run the complete mirror suite for data contents, guest-only writes, latest and saved resets, scheduling, leases, cancellation, existing-runtime access, TTL, restarts and upgrades. Do not remove old cleanup paths while outstanding mirrors use them.

Documentation CI generates every CRD field and discovers the complete CLI command tree from the current binary. It builds strictly and checks every local link/anchor. Feature changes must also update the guide, examples, status/evidence and release notes in the same change; automation cannot verify prose claims about cloud compatibility.
