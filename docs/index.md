---
title: Kubernetes replica environments with vCluster
description: Replicove recreates selected Kubernetes operators, Helm components, and configuration in disposable vClusters. Start with Helm, YAML, or the CLI.
---

<div class="hero" markdown>

<p class="eyebrow">Replicove · Developer documentation</p>

# Your cluster’s tools. A fresh place to test.

<p class="lede">Recreate selected operators, Helm components, and configuration in a disposable vCluster. Give your integration tests, developers, and agents a familiar environment—with a lifetime you control.</p>

[Install with Helm](getting-started/helm.md){ .md-button .md-button--primary }
[Use YAML](getting-started/yaml.md){ .md-button }
[Start with the CLI](../QUICKSTART.md){ .md-button }

</div>

!!! info "Experimental portable alpha"
    Install public alpha images, a Helm chart, CLI binaries, or native YAML. Production and cloud certification remain future work. See [tested behavior and remaining work](project-status.md).

<div class="feature-grid" markdown>
<div markdown>

### Choose what matters

Administrator grants, namespace and label selectors, dependency planning, explicit secrets, and application overrides.

[Selection guide →](guides/selection.md)

</div>
<div markdown>

### Create a place to test

Replicove provisions a pinned vCluster, or connects to one an administrator has already registered.

[Runtime choices →](reference/compatibility.md)

</div>
<div markdown>

### Finish with cleanup

Bounded guest credentials, explicit refresh, ownership tracking, and cleanup on deletion or TTL expiry.

[Lifecycle guide →](guides/cleanup.md)

</div>
</div>

## One API, two ways to use it

The CLI submits Kubernetes resources. You can use those same resources directly with `kubectl`, GitOps, or a Kubernetes client:

| Resource | Who manages it | Purpose |
| --- | --- | --- |
| `ReplicaGrant` | Host administrator | Authorize sources, secrets, runtimes, TTL, and access roles |
| `ClusterReplica` | Developer, CI, or agent | Select configuration and request a temporary replica |
| `ReplicaAccess` | Authorized credential consumer | Request a bounded guest credential |
| `ReplicaMirror` | Developer, CI, or agent | Select workload/PVC copies, schedule resets, and bound their lifetime |
| `ReplicaMirrorRun` | Developer, CI, or agent | Request an immutable manual Sync or saved-revision Reset |

The [YAML quickstart](getting-started/yaml.md) covers the operator installation, the core replica resource types, connecting with `kubectl`, and verified cleanup. You do not need the Replicove or Helm CLI for that workflow.

## What a replica means

Replicove recreates **selected, authorized Kubernetes desired state**. It strips host identities, resolves known dependencies, applies your mappings and overrides, and creates new objects in the guest. A source capture is a series of API reads, not an atomic etcd snapshot.

The guest has its own API server and object identities. With the current shared-worker profiles, workload pods still use host compute and networking. The optional [workload mirror module](guides/mirrors.md) copies explicitly granted CSI-backed data into independent writable generations. Replicove does not automatically recreate cloud accounts or every managed-cluster feature. [Compatibility and limits](reference/compatibility.md) explains the boundaries.

## Find the right guide

- **First installation:** [installation choices](getting-started/installation.md), [Helm](getting-started/helm.md), [YAML](getting-started/yaml.md), or [CLI](../QUICKSTART.md).
- **Platform administrators:** [grants and RBAC](guides/grants.md), [secrets and storage](guides/secrets-storage.md), [security](../SECURITY.md).
- **Workload data copies:** [mirrors, schedules, and saved-revision resets](guides/mirrors.md).
- **Application developers:** [operators and Helm](guides/operators.md), [refresh and drift](guides/lifecycle.md), [troubleshooting](guides/troubleshooting.md).
- **Agent and CI developers:** [access](guides/access.md), [GitOps and CI](guides/gitops.md), [full API reference](reference/api.md).
- **Contributors:** [architecture](architecture.md), [local development](development/local.md), [runtime maintenance](maintaining-replicove.md).

Built in public by [Nimesh Builds](https://github.com/nimeshbuilds). Replicove is an independent Apache-2.0 project; it is not affiliated with the vCluster maintainers.
