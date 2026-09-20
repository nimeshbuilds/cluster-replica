Replicove's first packaged alpha makes selected Kubernetes replica environments installable through a public Helm chart, native YAML, or the CLI.

```bash
helm upgrade --install replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.1.0-alpha.1 \
  --namespace replicove-system --create-namespace --wait --timeout 3m
```

- Operator: `ghcr.io/nimeshbuilds/replicove:0.1.0-alpha.1` for Linux amd64 and arm64. The chart and release YAML pin its digest.
- Installs CRDs, operator, permissions, encryption key, and a destination namespace. Creates new vClusters on demand; explicitly registered existing vClusters are preserved.
- Selected resources and Helm components, grants, overrides, secrets, plans/approval, refresh, scoped access, persistent control planes, and TTL cleanup.
- CLI downloads for macOS/Linux amd64/arm64, standalone CRDs/install YAML, Helm package, and SHA-256 checksums are attached below.
- Image build provenance and SBOM are attached as OCI attestations. These are build metadata, not a separate signed release attestation.

The complete main CI suite must pass before publication. Published-artifact checks verify anonymous pulls and source labels, exercise Helm provisioning and existing-vCluster reuse, and start the operator/CLI on native arm64. Live Kubernetes coverage remains the documented disposable amd64 kind hosts; this is not production or cloud certification.

**Experimental:** no blanket cluster cloning, source volume data restoration, qualified IRSA/cloud identity, or vCluster Platform provisioning. Retain keys and ownership records until cleanup finishes. Read the [Helm quickstart](https://nimeshbuilds.github.io/replicove/getting-started/helm/), [feature docs](https://nimeshbuilds.github.io/replicove/), and [compatibility limits](https://nimeshbuilds.github.io/replicove/reference/compatibility/).
