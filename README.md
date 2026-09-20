# Replicove

<p align="center">
  <img src="assets/brand/replicove-social.png" alt="Replicove — Your cluster’s tools. A fresh place to test. Kubernetes replica environments, powered by vCluster." width="100%">
</p>

[![CI](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml?query=branch%3Amain)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-293FC9)](LICENSE)
[![Status: experimental](https://img.shields.io/badge/status-experimental-00A58E)](docs/project-status.md)
[![GitHub Discussions](https://img.shields.io/badge/discuss-on_GitHub-293FC9)](https://github.com/nimeshbuilds/replicove/discussions)

**Replicove is an open-source Kubernetes operator for building disposable integration-test environments with [vCluster](https://www.vcluster.com/).** It recreates the selected operators, configuration, and dependencies your application needs inside a virtual cluster, using a declarative `ClusterReplica` request.

[Helm quickstart](docs/getting-started/helm.md) · [Documentation](https://nimeshbuilds.github.io/replicove/) · [Project status](docs/project-status.md) · [Roadmap](ROADMAP.md) · [Contribute](CONTRIBUTING.md)

> **Experimental portable alpha.** The operator and CLI support selected source replication, persistent or existing vClusters, scoped guest access, and owned-resource cleanup on deletion or TTL expiry. Install the public `v0.2.0-alpha.1` artifacts using Helm, YAML, or the CLI. Production support and cloud certification are not available.

## Why Replicove?

An empty test cluster does not reproduce an application’s environment. Operators, admission rules, configuration, and credentials all influence whether an integration test tells you something useful.

Replicove provides a repeatable workflow: select what matters from an authorized source, inspect a plan, create a virtual environment, run your test, then expire the environment. vCluster supplies the virtual Kubernetes control plane; Replicove adds the selection, replication, access, and lifecycle workflow around it.

Useful scenarios include testing operator upgrades, reproducing configuration bugs, creating preview environments, and giving CI or coding agents a temporary Kubernetes workspace. The optional [workload mirror module](docs/guides/mirrors.md) adds writable CSI data copies with manual or scheduled resets to the host state.

## What can I use today?

| Capability | Available in the portable alpha |
| --- | --- |
| Provisioning | Pinned vCluster installation, persistent control plane, or an administrator-granted existing target |
| Selection | Source grants, selected Helm components/resources, dependency plans, namespace maps and overrides |
| Replication | Plan approval, readiness checks, explicit refresh, drift reporting and preserved guest experiments |
| Secrets | Explicitly granted snapshot or follow; service-account tokens excluded |
| Access | Short-lived viewer/deployer/admin credentials, exact-Secret reader permissions and a reconnecting CLI tunnel |
| Workload mirrors | Optional CSI data copies, saved/latest resets, schedules, test leases and bounded retention |
| Lifecycle | UID-based guest/host inventory, access revocation, owned cleanup, deletion and TTL |

The integrated alpha’s [eight-job CI run](https://github.com/nimeshbuilds/replicove/actions/runs/35472630195) passed on commit `c2c5ea3`, including host Kubernetes 1.35.8 and 1.36.4, vCluster 0.37.1, and small cert-manager, Spark, Trino, and admission-policy scenarios. These are **functional test results**, not production-scale or cloud-platform certification. See [project status and evidence](docs/project-status.md).

## Quick start

```bash
helm upgrade --install replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.2.0-alpha.1 \
  --namespace replicove-system --create-namespace --wait --timeout 3m
```

**[Helm quickstart →](docs/getting-started/helm.md)** · **[CLI quickstart →](QUICKSTART.md)** · **[YAML quickstart →](docs/getting-started/yaml.md)**

The Helm command installs the CRDs, operator, permissions, key, and destination namespace. Replicove provisions its pinned vCluster when you request a new replica, or uses an explicitly registered existing guest. A preinstalled vCluster is optional. The public operator image is `ghcr.io/nimeshbuilds/replicove:0.2.0-alpha.1`; the published chart pins its digest.

The guides walk through source permissions, replica creation, guest access, verification, and cleanup. CLI binaries and native manifests are available on the [alpha release](https://github.com/nimeshbuilds/replicove/releases/tag/v0.2.0-alpha.1). The [developer docs](https://nimeshbuilds.github.io/replicove/) cover each feature and generate API/CLI references from code.

The public name is Replicove. The Go module, prototype binary `cluster-replica`, and API group retain their original identifiers during the alpha so existing development workflows remain usable.

## Boundaries that matter

- Replication is **selected and authorized**. A virtual cluster cannot reproduce every host, cloud, storage, or operator behavior automatically.
- The CLI defaults to a persistent control plane and `DeleteOwned`. Cleanup follows recorded ownership and respects finalizers. Ordinary replicas create empty volumes only. Optional mirrors capture explicitly granted CSI volumes, restore independent copies, and clean up owned snapshots; shared resources and external services remain outside the cleanup contract. The optional `emptyDir` lab profile can lose state on rescheduling; legacy `HelmReleaseOnly` removes only its Helm release.
- IRSA and other cloud identity adapters, application-consistent/database restore adapters, vCluster Platform integration, and distribution-specific qualification remain future work.
- Compatibility depends on Kubernetes versions, APIs, and capabilities. EKS, AKS, GKE, OpenShift, and RKE2 are not currently certified.

Read [Security](SECURITY.md), [compatibility policy](docs/design/vcluster-compatibility-policy.md), and [the implementation plan](docs/design/cluster-replica-implementation-plan.md) before extending the project.

## Build with us

Built in public by [Nimesh Builds](https://github.com/nimeshbuilds). Share a reproducible cluster-testing problem in [Discussions](https://github.com/nimeshbuilds/replicove/discussions), [report a bug](https://github.com/nimeshbuilds/replicove/issues/new/choose), or improve a guide you tried. The [contribution guide](CONTRIBUTING.md) explains local checks and how to propose an adapter. If this solves a problem you care about, a star helps others discover it.

Replicove is an independent project, unaffiliated with the vCluster maintainers. Licensed under [Apache-2.0](LICENSE); upstream components retain their own licenses. [Brand assets and usage](docs/brand/README.md).
