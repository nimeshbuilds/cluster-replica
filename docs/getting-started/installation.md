# Installation

Replicove is a Go operator installed on your host cluster. It watches one destination namespace and keeps encrypted capture and access state in a separate, administrator-only namespace.

The commands below use **v0.3.0-alpha.1**. Obtain matching CLI, chart, image and CRDs from [releases](https://github.com/nimeshbuilds/replicove/releases), or use a [source build](../development/local.md) for an unreleased revision. [Validation](../validation.md) records source tests separately from published-artifact checks. Public installation needs no GitHub login.

## Choose a path

| Path | Use it when | Tools |
| --- | --- | --- |
| [Helm quickstart](helm.md) | You want a single-command operator installation | Helm and kubectl on an existing disposable host |
| [YAML quickstart](yaml.md) | You want native manifests or GitOps | kubectl; Docker and repository tools for the local kind demo |
| [CLI quickstart](../../QUICKSTART.md) | You want plan/approval commands and a managed local tunnel | Published Replicove CLI; Docker and repository tools for the local demo |

The YAML installer is generated from the **same chart** as the CLI. Replicove uses the pinned vCluster chart internally; end users do not need a Helm CLI or preinstalled vCluster when using the YAML path.

## APIs and local tools

The current chart installs six CRDs: `ClusterReplica`, `ReplicaGrant`, `ReplicaAccess`, `ReplicaMirror`, `ReplicaMirrorRun`, and `ReplicaExperiment`. Only `ReplicaGrant` is cluster-scoped. Update all six schemas together during upgrades, including grants for optional modules.

`TestRecipe` and `ReplicaPool` are local CLI documents, not CRDs. The runner, stdio MCP and dashboard execute in the caller's process with its selected Kubernetes identity; they require no additional operator deployment.

## Host prerequisites

- Administrator permission to install all Replicove CRDs, protected/destination namespaces, and their RBAC.
- A separate source namespace with explicit read permissions for selected resources.
- For the default persistent profile: a working default StorageClass with dynamic provisioning and `Delete` reclaim policy. The control plane requests 1 GiB. Application volumes are additional.
- A working container runtime, cluster DNS, pod networking, and outbound access to the pinned chart and workload images.
- Enough worker CPU and memory for both control planes and selected workloads. The functional CI fixtures are small; there is no published sizing guarantee.

The live host tests cover Kubernetes 1.35.8 and 1.36.4, with vCluster 0.37.1 and guest Kubernetes 1.36.0. Vendor-specific identity, security admission, and CSI behavior require separate qualification. Read [compatibility](../reference/compatibility.md) before adapting to EKS, AKS, GKE, OpenShift, or RKE2.

## Install on your own disposable cluster

Use your administrator test-cluster context:

```bash
helm --kube-context YOUR_TEST_CONTEXT upgrade --install replicove \
  oci://ghcr.io/nimeshbuilds/charts/replicove --version 0.3.0-alpha.1 \
  --namespace replicove-system --create-namespace --wait --timeout 3m
```

The chart creates the destination namespace if needed. It starts with no source permissions; add explicit source RBAC and matching grants as shown in the [Helm quickstart](helm.md). An already-running vCluster can be [registered as an existing target](../guides/existing.md).

For native installation, use the versioned `replicove-crds.yaml` and `replicove-install.yaml` release assets in the [YAML walkthrough](yaml.md). Both Helm and the native release installer pin the operator image by digest. Build-from-source installation remains available for [contributors](../development/local.md).

## Optional modules

All modules use the same operator image and installation. Mirrors, PostgreSQL and chaos are disabled by default. They can be enabled after installation without replacing the state key or existing requests, but setting a Deployment flag alone does not install their permissions or prerequisites.

| Module | Enablement | Administrator prerequisites |
| --- | --- | --- |
| CSI mirrors | `mirrors.enabled: true` | Exact PVC grants, qualified snapshot driver/classes, snapshot controller ownership and enforced host CNI; [late-enable guide](../guides/enable-mirroring.md) |
| PostgreSQL 17 | `databases.enabled: true` | Explicit database grants, protected source credentials, reviewed PostgreSQL image, Delete StorageClass and enforced host NetworkPolicy; [database guide](../guides/postgresql.md) |
| Chaos | `chaos.enabled: true` | Exact fault/target/resource grants; `chaos.networkPolicyEnforced: true` only after qualifying the host CNI for network/Job faults; [chaos guide](../guides/chaos.md) |

Use matching CLI, image, chart and CRDs from the same source revision or verified release. Preserve reviewed custom values and explicitly choose the new image: an old saved `image.digest` overrides a new image tag. For Helm/CLI installations, upgrade the existing Helm release; for native installations, render/apply the complete updated manifests through their existing owner. Apply CRDs first, preserve namespaces and key/state, handle a completed bootstrap Job as described below, and wait for the operator rollout before submitting new feature requests.

Database copies require a new managed target and cannot run alongside CSI mirrors or use in-place refresh. Network/Job chaos faults reject overlapping host allow policies, including a mirror's or ready database's policy; PodDelete/ScaleZero have different prerequisites. Neither module turns shared workers into a sandbox for hostile code. Drain all affected requests before disabling a module or removing its RBAC.

For concurrent managed replicas, [render a pool](../guides/pools.md) of separate destinations with protected state and host quotas. Pool choice is advisory; the operator makes the durable admission reservation and queued requests retain their original TTL.

## Key initialization and persistence

The Helm installation generates an immutable 32-byte encryption key. The native YAML installation runs a bounded bootstrap Job that creates the same key in the protected namespace. No key is embedded in the checked-in manifests.

Re-running the Job validates and reuses an existing key. An invalid key, or a missing key with encrypted state still present, fails initialization instead of replacing it. Back up the key **with** its encrypted state. A missing key cannot be recovered from ciphertext.

## Upgrades and removal

Use the same installation method for an existing operator. Switching a live installation between Helm and standalone manifests needs an explicit ownership migration and is not a supported automatic path.

For YAML upgrades, review the generated diff, preserve the state key and state Secrets, apply CRD updates, remove only the **completed** bootstrap Job if its image/template changed, then apply your updated overlay. Kubernetes Job pod templates are immutable. Wait for the new Job and Deployment and check existing replica conditions. Do not run upgrades while a bootstrap Job is still active.

For Helm upgrades, apply CRD updates separately (Helm does not automatically upgrade files in `crds/`) and use `helm upgrade` with the existing release name, protected namespace, and reviewed values. The chart reuses the existing key.

If you opted into `stateKey.bootstrap: true` in Helm values, changing its image also requires replacing the completed bootstrap Job; its pod template is immutable under Helm too. Follow the [conditional bootstrap step](../guides/enable-mirroring.md#3-upgrade-the-same-release). Default Helm installations do not create that Job.

For optional workload data copies, follow [enable mirroring later](../guides/enable-mirroring.md). It covers existing Helm/CLI installations, CRD changes from the earlier alpha, value preservation, snapshot dependencies, and native/GitOps rendering. `replicove install` creates a Helm release; rerunning it does not upgrade that release.

Before uninstalling, stop/rollback experiments, delete replica and mirror requests, and wait for their finalizers and all access requests to finish. See the [cleanup guide](../guides/cleanup.md). **Do not delete the installation namespaces or CRDs to bypass cleanup.** They can contain unrelated resources and the ownership records needed for recovery.
