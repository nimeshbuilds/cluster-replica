# Legacy runtime-only quickstart

> For a new installation, use the [first replica quickstart](../QUICKSTART.md). It installs the full alpha with a protected state namespace, explicit runtime permissions, and owned-resource cleanup. This older guide uses broad lab permissions and `HelmReleaseOnly` for runtime-adapter development only.

This guide runs the legacy runtime-only mode: create a vCluster, connect to its API, and delete its Helm release. For a working example of **source configuration replication**, use the [first replica quickstart](../QUICKSTART.md), which checks out the tested portable alpha.

## Before you start

Use a disposable host cluster/context administered by someone you trust. You need Git, `make`, Go 1.27.1+, `kubectl`, and the [vCluster CLI v0.37.1](https://github.com/loft-sh/vcluster/releases/tag/v0.37.1). You do not need a Helm CLI or a published operator image: the operator runs locally and provisions vCluster through the Helm SDK.

The lab role is broad inside `replica-lab`. When running locally, the operator uses your selected kubeconfig identity. The example is for trusted administrator-operated labs; see [Security](../SECURITY.md).

## 1. Build and select the host

In **Terminal A**, choose your disposable context deliberately. Replace `YOUR_DISPOSABLE_CONTEXT` with its exact name before continuing:

```bash
git clone https://github.com/nimeshbuilds/replicove.git replicove-runtime
cd replicove-runtime
git switch main
make build

kubectl config get-contexts
export REPLICOVE_HOST_CONTEXT='YOUR_DISPOSABLE_CONTEXT'
kubectl --context "$REPLICOVE_HOST_CONTEXT" cluster-info

umask 077
mkdir -p .cache/runtime-quickstart
chmod 700 .cache/runtime-quickstart
kubectl --context "$REPLICOVE_HOST_CONTEXT" config view --minify --flatten --raw \
  > .cache/runtime-quickstart/host.kubeconfig
```

The final command writes credentials only to a private, Git-ignored file. Do not print or share it. The following steps use that file explicitly, so connecting to the guest cannot change their target.

## 2. Install the API and start the operator

Still in Terminal A:

```bash
kubectl --kubeconfig .cache/runtime-quickstart/host.kubeconfig \
  apply -f config/crd/replica.nimeshbuilds.dev_clusterreplicas.yaml
kubectl --kubeconfig .cache/runtime-quickstart/host.kubeconfig \
  wait crd/clusterreplicas.replica.nimeshbuilds.dev --for=condition=Established --timeout=60s
kubectl --kubeconfig .cache/runtime-quickstart/host.kubeconfig \
  apply -f config/rbac/lab.yaml

KUBECONFIG="$PWD/.cache/runtime-quickstart/host.kubeconfig" \
  ./bin/cluster-replica --watch-namespace=replica-lab
```

Leave this operator process running through replica cleanup. If port 8081 is occupied, add `--health-probe-bind-address=:8082` when starting it.

## 3. Request a vCluster

Open **Terminal B in the same `replicove-runtime` checkout**:

```bash
hk() { kubectl --kubeconfig "$PWD/.cache/runtime-quickstart/host.kubeconfig" "$@"; }
hk apply -f config/samples/replica.yaml
hk -n replica-lab wait clusterreplica/integration \
  --for=condition=RuntimeReady --timeout=5m
hk -n replica-lab get clusterreplica integration
```

The [sample](../config/samples/replica.yaml) requests profile `vcluster-0.37.1-lab`, a two-hour TTL, and `HelmReleaseOnly` cleanup. The operator installs the pinned vCluster chart itself.

**Success:** `RuntimeReady=True` means the managed control-plane Deployment reports current, ready replicas. The next step verifies that the guest API is reachable. Neither condition establishes source parity or cloud identity behavior.

## 4. Connect and verify the guest API

In Terminal B:

```bash
export REPLICOVE_RELEASE=$(hk -n replica-lab get clusterreplica integration \
  -o jsonpath='{.status.runtime.releaseName}')

KUBECONFIG="$PWD/.cache/runtime-quickstart/host.kubeconfig" \
  vcluster connect "$REPLICOVE_RELEASE" --namespace replica-lab --driver helm \
  --config "$PWD/.cache/runtime-quickstart/vcluster.json" --background-proxy=false \
  -- bash --noprofile --norc
```

This opens a child shell with a temporary **guest** kubeconfig and an active tunnel. In that child shell:

```bash
kubectl get --raw /readyz
kubectl get namespaces
exit
```

Expect `ok` and the guest’s namespaces. `exit` closes the guest shell and connection, returning to Terminal B. This uses upstream [vCluster’s command mode](https://www.vcluster.com/docs/vcluster/cli/vcluster_connect), so no global context switch is needed. The prototype connection uses guest administrator credentials; bounded viewer/deployer sessions belong to the portable alpha.

## 5. Clean up on the host

After leaving the guest shell, use `hk` in Terminal B:

```bash
hk -n replica-lab delete clusterreplica integration --timeout=180s
hk -n replica-lab get deployments,services,persistentvolumeclaims
```

Keep Terminal A running until deletion succeeds. Then stop its operator with Ctrl-C. The CRD, namespace, and lab RBAC remain for another run; these commands do not delete your host cluster.

```bash
rm -f .cache/runtime-quickstart/host.kubeconfig .cache/runtime-quickstart/vcluster.json
```

The two-hour TTL also starts at request creation, including provisioning time. At expiry the controller removes the owned Helm release and leaves an `Expired` request record. It must be running to carry out cleanup.

**Prototype limits:** `HelmReleaseOnly` does not inventory or guarantee deletion of guest-synced workloads, generated access Secrets, PVCs, snapshots, or external resources. The lab control plane uses `emptyDir`; rescheduling can lose its state. This API-only walkthrough creates no application workload. Use the [portable alpha quickstart](../QUICKSTART.md) for selected toolsets, a persistent control plane, and owned-resource cleanup.

## Troubleshooting

```bash
hk -n replica-lab describe clusterreplica integration
hk -n replica-lab get pods
hk -n replica-lab get events --sort-by=.lastTimestamp
```

Check Terminal A’s operator logs as well. An unknown kind usually means the CRD was not installed in this host context. A readiness timeout needs pod/events inspection; it is not proof of success. For a connection error, verify the release name, CLI version, and control-plane readiness. Do not disable TLS verification or strip a finalizer to hide a failed operation.

For an in-cluster operator, build/push your own image and replace the placeholder in [the Deployment manifest](../config/manager/deployment.yaml) before applying it. No official release image is published yet.

[First replica quickstart](../QUICKSTART.md) · [Project status](project-status.md) · [Testing](testing.md) · [Support](../SUPPORT.md)
