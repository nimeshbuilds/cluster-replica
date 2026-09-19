# ClusterReplica

**Your Kubernetes toolset. A fresh vCluster. One request.**

[![CI](https://github.com/nimeshbuilds/cluster-replica/actions/workflows/ci.yaml/badge.svg)](https://github.com/nimeshbuilds/cluster-replica/actions/workflows/ci.yaml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Integration tests need more than an empty cluster. They need the operators and configuration your application depends on. ClusterReplica is an experimental Kubernetes operator building toward recreating that selected environment on top of [vCluster](https://www.vcluster.com/).

**Current status: early runtime prototype.** The code provisions a standalone vCluster through Helm, reports control-plane readiness, and removes its Helm release on deletion or TTL expiry. Automatic host discovery and toolset replication are the next milestones. No Kubernetes/vCluster pair is behaviorally certified yet.

[Roadmap](ROADMAP.md) · [Architecture](docs/architecture.md) · [Full implementation plan](docs/design/cluster-replica-implementation-plan.md) · [Contribute](CONTRIBUTING.md) · [Discuss](https://github.com/orgs/nimeshbuilds/discussions)

## The first working surface

```yaml
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ClusterReplica
metadata:
  name: integration
  namespace: replica-lab
spec:
  profile: vcluster-0.37.1-lab
  ttl: 2h
  cleanupPolicy: HelmReleaseOnly
```

The namespace must already exist and be granted to the operator. A request creates its own vCluster; it does not adopt an existing release or require a preinstalled vCluster binary. Helm runs through the Go SDK.

| Available in this prototype | Planned |
| --- | --- |
| Namespaced `ClusterReplica` CRD with immutable spec | Source discovery and inspectable replication plans |
| Pinned vCluster chart and SHA-256 verification | Selectors, dependencies, and per-component overrides |
| Standalone Helm provisioning and readiness status | Existing vCluster and Platform providers |
| UID-bound release ownership; no name-based adoption | Secret, identity, and data adapters |
| TTL measured from request creation, durable finalizer | Complete inventory and deletion of owned guest resources |
| Namespace administrator access using `vcluster connect` | Short-lived credentials for humans, CI, and agents |

## Run the prototype

Use a **disposable, trusted lab namespace**. Prerequisites: Go 1.27.1+, `kubectl`, and an existing Kubernetes cluster/context. A host administrator must install the CRD and grant the namespaced permissions once. The operator does not install Kubernetes itself.

```sh
git clone https://github.com/nimeshbuilds/cluster-replica.git
cd cluster-replica
make build

kubectl apply -f config/crd/replica.nimeshbuilds.dev_clusterreplicas.yaml
kubectl apply -f config/rbac/lab.yaml

# Terminal 1: uses your current kubeconfig. Limit that identity to the lab namespace.
./bin/cluster-replica --watch-namespace replica-lab

# Terminal 2:
kubectl apply -f config/samples/replica.yaml
kubectl get clusterreplicas -n replica-lab -w
kubectl wait -n replica-lab clusterreplica/integration \
  --for=condition=RuntimeReady --timeout=5m
```

`RuntimeReady` means the managed Deployment reports current, ready replicas. It does **not** certify source parity, guest workload behavior, or a cloud identity mapping.

To run in the cluster, build and push your own operator image, then set it in [config/manager/deployment.yaml](config/manager/deployment.yaml). The example image is deliberately a placeholder; no release image is published yet. Apply the deployment after replacing it. Its service account is bound only inside `replica-lab`.

The example Role has broad permissions **within that namespace**, including secrets and workload creation, to install the upstream chart and its Role. It is intended for administrator-operated labs. Namespace grants, quotas, admission policy, and narrower production RBAC are still required before multi-tenant use. The local command uses your kubeconfig's privileges, not the example service account.

## Connect and expire

Install the [vCluster CLI](https://www.vcluster.com/docs/vcluster/manage/cli) and connect as the namespace administrator:

```sh
RELEASE_NAME=$(kubectl get clusterreplica integration -n replica-lab \
  -o jsonpath='{.status.runtime.releaseName}')
vcluster connect "$RELEASE_NAME" --namespace replica-lab
```

This delegates the connection flow to upstream vCluster. The CR's status never contains a kubeconfig or token. Scoped agent access and revocation are not implemented yet.

TTL starts at CR creation, including provisioning time. After expiry, the CR remains as an `Expired` record and will not reinstall its runtime. Deleting the CR also triggers release cleanup:

```sh
kubectl delete clusterreplica integration -n replica-lab
```

**`HelmReleaseOnly` is a limited cleanup contract.** The operator checks ownership, retains Helm history during partial cleanup, and purges it only when the chart's manifest objects are absent. It never deletes the namespace. Guest-synced workloads, generated access secrets, PVCs, snapshots, and external cloud resources are not inventoried by this prototype and may remain. Removing the release is not proof of access revocation or complete data deletion. Do not strip a stuck finalizer without examining the remaining resources.

The lab control plane uses `emptyDir` so no host StorageClass is required to start it. **Rescheduling its pod can lose the virtual cluster's control-plane data.** This profile is for disposable experiments; durable backing stores and their cleanup contracts are future profiles.

## Compatibility and maintenance

The initial **candidate** profile pins vCluster `0.37.1`, the OSS control-plane image tag, and guest Kubernetes `v1.36.0`. The chart archive hash is checked before parsing. The host version is not automatically matched in this prototype. Image tags are not yet locked to digests.

All upstream values live in [internal/catalog](internal/catalog); the reconciler uses a small [runtime interface](internal/runtime/runtime.go). New chart versions must enter through a new tested profile. Existing requests persist their resolved version and never silently upgrade. Old catalog adapters must remain available while their requests exist; cleanup uses stored Helm manifests and does not fetch charts.

Chart rendering against host API versions 1.35–1.37 is a schema/template check, **not a support matrix**. EKS, AKS, GKE, OpenShift, RKE2, Spark, Trino, IRSA, and complete toolset replication still need live validation. Kubernetes versions, APIs, and required capabilities will determine support; distribution names provide adapter context.

See the [compatibility policy](docs/design/vcluster-compatibility-policy.md) and [upstream profile provenance](docs/upstream.md).

## Develop and verify

```sh
make generate         # regenerate DeepCopy code and the CRD
make test             # unit tests with the race detector
make test-contract    # download/hash-check and render the real pinned chart
make test-integration # real local API server: CRD validation and lifecycle tests
make vet
make build
```

The integration suite uses envtest's local API server and etcd. It exercises CR lifecycle with a fake provider and installs the actual chart through the Helm SDK to verify resource creation, repeated observation, and cleanup recovery. It requires no Docker and does not run vCluster pods or guest workloads. A real host + guest end-to-end suite is the next validation milestone. See [testing](docs/testing.md) for the exact boundaries.

## Build in public

Built by [Nimesh Builds](https://github.com/nimeshbuilds). Useful contributions include reproducible cluster-testing problems, design reviews, and tests for the first real host/guest workflow. Start with the [roadmap](ROADMAP.md) and [contribution guide](CONTRIBUTING.md).

ClusterReplica is an independent project. It is not affiliated with or endorsed by the vCluster maintainers. This repository is Apache-2.0 licensed; upstream vCluster artifacts retain their own licenses.
