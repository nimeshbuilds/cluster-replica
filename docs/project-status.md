# Replicove project status

Replicove is experimental. There is no supported production release, published operator image, or public binary release yet.

## Portable alpha

The operator and CLI on `main` support source grants, capture and planning, selected Helm and resource replication, namespace/value overrides, selected secrets, explicit refresh, scoped guest access, and owned cleanup. The CLI defaults to a persistent vCluster; existing vClusters can be registered through administrator-pinned credentials. Follow the [first replica quickstart](../QUICKSTART.md) for a complete local walkthrough.

The installed operator uses explicit runtime resource/verb permissions. Chart contracts verify coverage of the pinned vCluster Role without wildcard permissions or escalation bypasses. Real API tests and the two live host workflows check source/destination authorization and reject wildcard Role escalation and cluster-admin bindings. Host API permissions do not establish isolation of shared workers or networks; see [Security](../SECURITY.md).

## Recorded behavior evidence

At integrated alpha revision [`c2c5ea3`](https://github.com/nimeshbuilds/replicove/commit/c2c5ea387c74964dd8f12c9c0d81a0c96c1c673c), **all eight jobs passed** in [this disposable-cluster CI run](https://github.com/nimeshbuilds/replicove/actions/runs/35472630195). Its file contents were merged unchanged into `main` as [`c5d4d62`](https://github.com/nimeshbuilds/replicove/commit/c5d4d627e5e16da22085305f591c9fea305cd236):

| Evidence | Scope |
| --- | --- |
| Verification | Generated files, Go tests and race checks, chart contract checks, local API integration, vet, builds |
| Original vCluster lifecycle | Actual Helm provisioning, a reachable guest, and teardown |
| Core workflow, Kubernetes 1.35.8 and 1.36.4 hosts | Plan/approval, selected Helm resources and secrets, refresh, scoped operator permissions, rejected privilege escalation, access and revocation, operator restart, persistent control-plane recovery, owned cleanup and TTL |
| cert-manager | Replicated operator issues a certificate and creates its TLS Secret |
| Spark | Replicated Spark Operator completes a small Spark Pi job |
| Trino | A single coordinator answers a TPCH query |
| Admission policy | A replicated native policy denies an invalid object and accepts a valid one |

These scenarios use vCluster 0.37.1 and guest Kubernetes v1.36.0. Chart rendering for other host versions is not live compatibility evidence. The small workloads do not establish throughput, production isolation, large-cluster reliability, or cloud identity behavior.

See the [configuration guide](replicove-quickstart.md), [implementation ledger](IMPLEMENTATION_STATUS.md), and [workload details](workload-adapters.md). The [CI workflow](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml?query=branch%3Amain) reports checks for subsequent changes to `main`; the linked run above records the integrated alpha's behavior evidence.

## Remaining release gates

- Publish installable binaries and a verified operator image from the integrated alpha.
- Qualify cloud identity adapters such as IRSA, cloud storage/data restoration, and external-resource cleanup in dedicated cloud labs.
- Qualify vCluster Platform integration and additional Kubernetes/distribution combinations.
- Finish image-digest locking and automated runtime candidate diffs in [#6](https://github.com/nimeshbuilds/replicove/issues/6), plus scale, recovery, and production hardening in the [roadmap](../ROADMAP.md).

No one-click exact clone of an arbitrary cluster is promised. Users select components within administrator-granted access, and unsupported capabilities must be surfaced explicitly.

## Keeping this page current

When capabilities change, update this page and link verification for the tested revision. Publish support claims only with matching live evidence. Keep the README, quickstarts, security policy, and release notes in agreement.

[Back to documentation](README.md)
