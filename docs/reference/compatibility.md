# Compatibility and limits

Replicove compatibility depends on Kubernetes APIs, vCluster profile, storage, admission policy, networking, and workload dependencies. A distribution label alone cannot establish compatibility.

## Runtime profiles

| Profile | vCluster chart | Guest Kubernetes | Control-plane storage | Intended use |
| --- | --- | --- | --- | --- |
| `vcluster-0.37.1-persistent` | 0.37.1 | v1.36.0 | StatefulSet, fresh 1 GiB PVC, Delete retention | Default alpha workflow; survives control-plane pod rescheduling |
| `vcluster-0.37.1-lab` | 0.37.1 | v1.36.0 | Deployment and emptyDir | Disposable runtime testing; rescheduling can lose guest state |

The pinned chart archive SHA-256 is `afb57fb5f2e3088519ffa9112fa0bfc3543bc3465f86f7e7e655ba30aea0d969`. Runtime catalog provenance is recorded in [upstream notes](../upstream.md) and [`internal/catalog`](../../internal/catalog/). Images are not all digest-locked; that work remains tracked in [issue #6](https://github.com/nimeshbuilds/replicove/issues/6).

## What has been tested

| Dimension | Evidence |
| --- | --- |
| Host Kubernetes 1.35.8 | Full portable kind workflow |
| Host Kubernetes 1.36.4 | Full portable kind workflow and workload fixtures |
| Host Kubernetes 1.37 | Chart rendering/contract checks only; not live host qualification |
| Guest Kubernetes v1.36.0 | Pinned guest used in the live workflows |
| cert-manager, Spark, Trino, admission policy | Small functional fixtures; no production-scale certification |
| EKS, AKS, GKE, OpenShift, RKE2 | No vendor-specific certification yet |

Exact revisions, CI runs, and scope are linked on [project status](../project-status.md) and [validation](../validation.md). A green chart render does not prove scheduler, CNI, CSI, identity, or admission behavior on a real distribution.

## Supported portable capabilities

- Administrator-bounded source namespaces, kinds, selected secrets, and Helm releases.
- Dependency planning, namespace/StorageClass mapping, merge patches and Helm overrides.
- Plan approval, readiness checks, explicit refresh and drift reporting.
- New pinned vClusters or explicitly registered existing guests.
- Short-lived guest roles, exact-session Secret permissions, and an optional local CLI tunnel.
- TTL, durable ownership records, access revocation and verified supported cleanup.

## Current boundaries

- No atomic/full-cluster clone, source volume data copy, CSI restore, or external backend recreation.
- No IRSA/EKS Pod Identity/Azure/GCP identity adapter certification.
- No qualified vCluster Platform provisioning; configured requests block explicitly.
- No arbitrary privileged/host-network/host-path workload replication or system namespace mapping.
- No inference of application-specific string references; use explicit overrides.
- No guest Helm release reconstruction in Helm storage, lifecycle-hook execution, or recovered upstream signature claim.
- No per-user ownership authentication inside a shared destination grant; authorization is delegated by namespace.
- No independent kernel, worker-node, network, or cloud-account isolation from the host through the current profiles.
- No published production release, container image, binary support policy, performance SLO, or broad operator certification.

## Bounded requests

| Limit | API/default behavior |
| --- | --- |
| Replica TTL | 5 minutes–168 hours, whole minutes/hours; grant default maximum 24 hours |
| Source namespaces | 1–32 in a grant |
| Planned object limit | Grant default 500, maximum 2,000 |
| Protected capture size | Grant default 524,288 bytes, maximum 716,800 bytes; decoded state also bounded |
| Access duration | 600–3,600 seconds; grant/default session limit 900 seconds |
| Access subjects | At most 32 per grant |

The [API reference](api.md) contains exact field defaults, enums, patterns, and validation rules. Semantic checks can impose additional restrictions beyond schema validation; use the feature guides as well as `kubectl explain`.

## Adding a Kubernetes/vCluster version

Add a new exact runtime profile, preserve old cleanup support, inspect upstream changes, and pass local/chart/API plus live cluster/workload tests before changing defaults. Follow [maintenance](../maintaining-replicove.md). Automatic floating updates would make existing captures and cleanup ambiguous, so they are deliberately not the current model.
