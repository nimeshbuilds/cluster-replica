# Replicove project status

Replicove is experimental. There is no supported production release, published operator image, or public binary release yet.

## Default branch

The implementation on `main` is a runtime prototype. It accepts a namespaced `ClusterReplica`, installs a pinned standalone vCluster through Helm, reports readiness, and removes the owned Helm release on deletion or TTL expiry. The [runtime quickstart](runtime-quickstart.md) matches this code.

Source discovery, guest replication, complete owned-resource cleanup, and scoped access are not implemented on this branch. The example permissions are for trusted administrator-operated labs; `HelmReleaseOnly` is a limited cleanup contract.

## Portable alpha under development

Follow the [first replica quickstart](../QUICKSTART.md) for a complete local walkthrough at the tested revision.

[PR #7](https://github.com/nimeshbuilds/replicove/pull/7) adds a real vCluster lifecycle suite. [PR #8](https://github.com/nimeshbuilds/replicove/pull/8), stacked on that work, adds source grants, capture and planning, selected Helm and resource replication, overrides, secrets, refresh, scoped guest access, and owned cleanup.

At tested revision [`5ffeb42`](https://github.com/nimeshbuilds/replicove/commit/5ffeb4243f7e6588fb5a04afe906f1cc40c47624), **all eight jobs passed** in [this disposable-cluster CI run](https://github.com/nimeshbuilds/replicove/actions/runs/35467194302):

| Evidence | Scope |
| --- | --- |
| Verification | Generated files, Go tests and race checks, chart contract checks, local API integration, vet, builds |
| Original vCluster lifecycle | Actual Helm provisioning, a reachable guest, and teardown |
| Core workflow, Kubernetes 1.35.8 and 1.36.4 hosts | Plan/approval, selected Helm resources and secrets, refresh, access and revocation, operator restart, persistent control-plane recovery, owned cleanup and TTL |
| cert-manager | Replicated operator issues a certificate and creates its TLS Secret |
| Spark | Replicated Spark Operator completes a small Spark Pi job |
| Trino | A single coordinator answers a TPCH query |
| Admission policy | A replicated native policy denies an invalid object and accepts a valid one |

These scenarios use vCluster 0.37.1 and guest Kubernetes v1.36.0. Chart rendering for other host versions is not live compatibility evidence. The small workloads do not establish throughput, production isolation, large-cluster reliability, or cloud identity behavior.

The [alpha quickstart](https://github.com/nimeshbuilds/replicove/blob/5ffeb4243f7e6588fb5a04afe906f1cc40c47624/docs/replicove-quickstart.md), [implementation ledger](https://github.com/nimeshbuilds/replicove/blob/5ffeb4243f7e6588fb5a04afe906f1cc40c47624/docs/IMPLEMENTATION_STATUS.md), and [workload details](https://github.com/nimeshbuilds/replicove/blob/5ffeb4243f7e6588fb5a04afe906f1cc40c47624/docs/workload-adapters.md) are pinned to that revision. The CI run above is newer than some historical evidence files in the branch.

## Remaining release gates

- Review and land the portable alpha, then publish installable binaries and a verified operator image.
- Qualify cloud identity adapters such as IRSA, cloud storage/data restoration, and external-resource cleanup in dedicated cloud labs.
- Qualify vCluster Platform integration and additional Kubernetes/distribution combinations.
- Complete release maintenance, scale, recovery, and production hardening in the [roadmap](../ROADMAP.md).

No one-click exact clone of an arbitrary cluster is promised. Users select components within administrator-granted access, and unsupported capabilities must be surfaced explicitly.

## Keeping this page current

When a feature lands on `main`, move its entry from the alpha section and link verification for the merged revision. Publish support claims only with matching live evidence. Keep the README, quickstarts, security policy, and release notes in agreement.

[Back to documentation](README.md)
