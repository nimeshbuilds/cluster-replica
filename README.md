# Replicove

<p align="center">
  <img src="assets/brand/replicove-social.png" alt="Replicove — Your cluster’s tools. A fresh place to test. Kubernetes replica environments, powered by vCluster." width="100%">
</p>

[![CI](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml?query=branch%3Amain)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-293FC9)](LICENSE)
[![Status: experimental](https://img.shields.io/badge/status-experimental-00A58E)](docs/project-status.md)
[![GitHub Discussions](https://img.shields.io/badge/discuss-on_GitHub-293FC9)](https://github.com/nimeshbuilds/replicove/discussions)

**Replicove is an open-source Kubernetes operator for building disposable integration-test environments with [vCluster](https://www.vcluster.com/).** It recreates the selected operators, configuration, and dependencies your application needs inside a virtual cluster, using a declarative `ClusterReplica` request.

[Helm quickstart](docs/getting-started/helm.md) · [Ten executable scenarios](docs/scenarios/index.md) · [Documentation](https://nimeshbuilds.github.io/replicove/) · [Project status](docs/project-status.md) · [Roadmap](ROADMAP.md) · [Contribute](CONTRIBUTING.md)

> **Experimental portable alpha.** Version `v0.3.0-alpha.1` adds test recipes, scoped PostgreSQL copies, bounded chaos and local agent/UI interfaces. [Validation](docs/validation.md) records exact source revisions and release-artifact checks. Production support and cloud certification are not available.

## Why Replicove?

An empty test cluster does not reproduce an application’s environment. Operators, admission rules, configuration, and credentials all influence whether an integration test tells you something useful.

Replicove provides a repeatable workflow: select what matters from an authorized source, inspect a plan, create a virtual environment, run your test, then expire the environment. vCluster supplies the virtual Kubernetes control plane; Replicove adds the selection, replication, access, and lifecycle workflow around it.

Useful scenarios include testing operator upgrades, reproducing configuration bugs, creating preview environments, and giving CI or coding agents a temporary Kubernetes workspace. The optional [workload mirror module](docs/guides/mirrors.md) adds writable CSI data copies with manual or scheduled resets to the host state.

Optional mirror, PostgreSQL, and chaos modules can be enabled through a values-preserving upgrade of the same installation. Browse the [complete feature map](docs/features.md) for their prerequisites and limits.

## What can I use today?

| Capability | Current source implementation |
| --- | --- |
| Provisioning | Pinned vCluster installation, persistent control plane, or an administrator-granted existing target |
| Selection | Source grants, selected Helm components/resources, dependency plans, namespace maps and overrides |
| Replication | Plan approval, readiness checks, explicit refresh, drift reporting and preserved guest experiments |
| Secrets | Explicitly granted snapshot or follow; service-account tokens excluded |
| Access | Short-lived viewer/deployer/admin credentials, exact-Secret reader permissions and a reconnecting CLI tunnel |
| Workload mirrors | Optional CSI data copies, saved/latest resets, schedules, test leases and bounded retention |
| Lifecycle | UID-based guest/host inventory, access revocation, owned cleanup, deletion and TTL |
| Diagnostics | Read-only preflight and plan evidence with source identities, dependencies, transformations and observed omissions |
| Test runs and pools | Reusable local recipes, bounded execution and cleanup, metadata/JUnit reports, destination selection and capacity admission |
| PostgreSQL 17 copies | Explicit database grants, consistent logical copies, approved masks and table filters, relationship validation before applications or access start |
| Chaos | Six bounded fault types on owned test workloads, grant limits, durable rollback and cleanup |
| Agents and local UI | Namespace-scoped stdio MCP, read-only by default, and a local read-only dashboard |

The diagnostics, test-runner/pool, PostgreSQL, chaos, MCP and dashboard paths have separate checks described in [validation](docs/validation.md). Test recipes are CLI documents, not another controller or a GitHub Action integration.

**All 16 source CI jobs passed at [`93bcfbc`](https://github.com/nimeshbuilds/replicove/commit/93bcfbcd7246f7f7bc8449e3f12cf4ff61642553)** in [run 35948951593](https://github.com/nimeshbuilds/replicove/actions/runs/35948951593): Kubernetes 1.35.8/1.36.4 core workflows, Helm/YAML installation, all four workload fixtures, three mirror upgrade paths, PostgreSQL, chaos, and test recipes. These are disposable functional results; exact-main and published-artifact checks remain separate release gates. See [scope and saved evidence](docs/validation.md), including the limits of the data and shared-worker tests.

## Quick start

```bash
helm upgrade --install replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.3.0-alpha.1 \
  --namespace replicove-system --create-namespace --wait --timeout 3m
```

**[Helm quickstart →](docs/getting-started/helm.md)** · **[CLI quickstart →](QUICKSTART.md)** · **[YAML quickstart →](docs/getting-started/yaml.md)**

Try the **[ten executable scenarios →](docs/scenarios/index.md)** for complete disposable labs spanning every current feature family. They use pinned release artifacts, assert expected behavior, and verify cleanup. Each documented variant runs through the same entry point in the documentation scenario workflow.

This command installs the versioned release artifacts. Use the [source build instructions](docs/development/local.md) when testing an unreleased revision. The chart installs all six CRDs, the operator, permissions, key, and destination namespace. Replicove provisions its pinned vCluster when you request a new replica, or uses an explicitly registered existing guest. A preinstalled vCluster is optional. Release artifacts pin the operator image by digest.

The guides walk through source permissions, replica creation, guest access, verification, and cleanup. Check [releases](https://github.com/nimeshbuilds/replicove/releases) for versioned CLI/manifests and their artifact-verification evidence. The [developer docs](https://nimeshbuilds.github.io/replicove/) cover each feature and generate API/CLI references from code.

The public name is Replicove. The Go module, prototype binary `cluster-replica`, and API group retain their original identifiers during the alpha so existing development workflows remain usable.

## Boundaries that matter

- Replication is **selected and authorized**. A virtual cluster cannot reproduce every host, cloud, storage, or operator behavior automatically.
- The CLI defaults to a persistent control plane and `DeleteOwned`. Cleanup follows recorded ownership and respects finalizers. Ordinary selected PVCs are empty; the separate PostgreSQL adapter makes logical copies into new managed targets. Optional mirrors capture explicitly granted CSI volumes, restore independent copies, and clean up owned snapshots; shared resources and external services remain outside the cleanup contract. The optional `emptyDir` lab profile can lose state on rescheduling; legacy `HelmReleaseOnly` removes only its Helm release.
- PostgreSQL masks and table filters cover only declared data and relationships; they are not general anonymization or an inferred tenant boundary. Database copies require host CNI enforcement and cannot be combined with mirrors, existing targets, or in-place refresh.
- Chaos is restricted to bounded owned workloads on shared workers; it does not provide node failures or privileged host faults. MCP uses the caller's Kubernetes identity over stdio; there is no remote certificate or CA service. The dashboard is local and read-only.
- IRSA and other cloud identity adapters, general database/PITR and atomic multi-volume recovery, vCluster Platform integration, and distribution-specific qualification remain future work.
- Compatibility depends on Kubernetes versions, APIs, and capabilities. EKS, AKS, GKE, OpenShift, and RKE2 are not currently certified.

Read [Security](SECURITY.md), [compatibility policy](docs/design/vcluster-compatibility-policy.md), and [the implementation plan](docs/design/cluster-replica-implementation-plan.md) before extending the project.

## Build with us

Built in public by [Nimesh Builds](https://github.com/nimeshbuilds). Share a reproducible cluster-testing problem in [Discussions](https://github.com/nimeshbuilds/replicove/discussions), [report a bug](https://github.com/nimeshbuilds/replicove/issues/new/choose), or improve a guide you tried. The [contribution guide](CONTRIBUTING.md) explains local checks and how to propose an adapter. If this solves a problem you care about, a star helps others discover it.

Replicove is an independent project, unaffiliated with the vCluster maintainers. Licensed under [Apache-2.0](LICENSE); upstream components retain their own licenses. [Brand assets and usage](docs/brand/README.md).
