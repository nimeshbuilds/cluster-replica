# Run the Replicove runtime prototype

Use a **disposable, trusted lab namespace**. Prerequisites: Go 1.27.1+, `kubectl`, and an existing Kubernetes cluster/context. A host administrator must install the CRD and grant the namespaced permissions once. The operator does not install Kubernetes itself.

```sh
git clone https://github.com/nimeshbuilds/replicove.git
cd replicove
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

To run in the cluster, build and push your own operator image, then set it in [config/manager/deployment.yaml](../config/manager/deployment.yaml). The example image is deliberately a placeholder; no release image is published yet. Apply the deployment after replacing it. Its service account is bound only inside `replica-lab`.

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

All upstream values live in [internal/catalog](../internal/catalog); the reconciler uses a small [runtime interface](../internal/runtime/runtime.go). New chart versions must enter through a new tested profile. Existing requests persist their resolved version and never silently upgrade. Old catalog adapters must remain available while their requests exist; cleanup uses stored Helm manifests and does not fetch charts.

Chart rendering against host API versions 1.35–1.37 is a schema/template check, **not a support matrix**. EKS, AKS, GKE, OpenShift, RKE2, Spark, Trino, IRSA, and complete toolset replication still need live validation. Kubernetes versions, APIs, and required capabilities will determine support; distribution names provide adapter context.

See the [compatibility policy](../docs/design/vcluster-compatibility-policy.md) and [upstream profile provenance](../docs/upstream.md).


[Back to Replicove](../README.md) · [Project status](project-status.md) · [Testing](testing.md)
