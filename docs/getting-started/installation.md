# Installation

Replicove is a Go operator installed on your host cluster. It watches one destination namespace and keeps encrypted capture and access state in a separate, administrator-only namespace.

Public alpha artifacts are available on [GitHub Releases](https://github.com/nimeshbuilds/replicove/releases/tag/v0.1.0-alpha.1) and GitHub Container Registry. Installing does not require a local image build or a GitHub login.

## Choose a path

| Path | Use it when | Tools |
| --- | --- | --- |
| [Helm quickstart](helm.md) | You want a single-command operator installation | Helm and kubectl on an existing disposable host |
| [YAML quickstart](yaml.md) | You want native manifests or GitOps | kubectl; Docker and repository tools for the local kind demo |
| [CLI quickstart](../../QUICKSTART.md) | You want plan/approval commands and a managed local tunnel | Published Replicove CLI; Docker and repository tools for the local demo |

The YAML installer is generated from the **same chart** as the CLI. Replicove uses the pinned vCluster chart internally; end users do not need a Helm CLI or preinstalled vCluster when using the YAML path.

## Host prerequisites

- Administrator permission to install three CRDs, protected/destination namespaces, and their RBAC.
- A separate source namespace with explicit read permissions for selected resources.
- For the default persistent profile: a working default StorageClass with dynamic provisioning and `Delete` reclaim policy. The control plane requests 1 GiB. Application volumes are additional.
- A working container runtime, cluster DNS, pod networking, and outbound access to the pinned chart and workload images.
- Enough worker CPU and memory for both control planes and selected workloads. The functional CI fixtures are small; there is no published sizing guarantee.

The live host tests cover Kubernetes 1.35.8 and 1.36.4, with vCluster 0.37.1 and guest Kubernetes 1.36.0. Vendor-specific identity, security admission, and CSI behavior require separate qualification. Read [compatibility](../reference/compatibility.md) before adapting to EKS, AKS, GKE, OpenShift, or RKE2.

## Install on your own disposable cluster

Use your administrator test-cluster context:

```bash
helm --kube-context YOUR_TEST_CONTEXT upgrade --install replicove \
  oci://ghcr.io/nimeshbuilds/charts/replicove --version 0.1.0-alpha.1 \
  --namespace replicove-system --create-namespace --wait --timeout 3m
```

The chart creates the destination namespace if needed. It starts with no source permissions; add explicit source RBAC and matching grants as shown in the [Helm quickstart](helm.md). An already-running vCluster can be [registered as an existing target](../guides/existing.md).

For native installation, use the versioned `replicove-crds.yaml` and `replicove-install.yaml` release assets in the [YAML walkthrough](yaml.md). Both Helm and the native release installer pin the operator image by digest. Build-from-source installation remains available for [contributors](../development/local.md).

## Key initialization and persistence

The Helm installation generates an immutable 32-byte encryption key. The native YAML installation runs a bounded bootstrap Job that creates the same key in the protected namespace. No key is embedded in the checked-in manifests.

Re-running the Job validates and reuses an existing key. An invalid key, or a missing key with encrypted state still present, fails initialization instead of replacing it. Back up the key **with** its encrypted state. A missing key cannot be recovered from ciphertext.

## Upgrades and removal

Use the same installation method for an existing operator. Switching a live installation between Helm and standalone manifests needs an explicit ownership migration and is not a supported automatic path.

For YAML upgrades, review the generated diff, preserve the state key and state Secrets, apply CRD updates, remove only the **completed** bootstrap Job if its image/template changed, then apply your updated overlay. Kubernetes Job pod templates are immutable. Wait for the new Job and Deployment and check existing replica conditions. Do not run upgrades while a bootstrap Job is still active.

For Helm upgrades, apply CRD updates separately (Helm does not automatically upgrade files in `crds/`) and use `helm upgrade` with the existing release name, protected namespace, and reviewed values. The chart reuses the existing key.

Before uninstalling, delete replica requests and wait for their finalizers and all access requests to finish. See the [cleanup guide](../guides/cleanup.md). **Do not delete the installation namespaces or CRDs to bypass cleanup.** They can contain unrelated resources and the ownership records needed for recovery.
