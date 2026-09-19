# Quick start: your first replica

Create a disposable Kubernetes cluster, install Replicove, and recreate a small application’s configuration and Helm component inside a vCluster. You will inspect the plan, connect to the replica, verify the copied resources, and remove the environment.

> **Developer preview:** this guide pins alpha revision `5c8e390`, including explicit runtime permissions and the full replication workflow. The [CI workflow](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml) runs it in disposable clusters. There is no published release image yet, so you build one locally.

## Before you start

- A macOS or Linux machine with Bash, Git, `make`, `curl`, `shasum`, and [Go 1.27.1+](https://go.dev/doc/install).
- A running Docker engine with room for a local Kubernetes cluster and image builds. Check `docker info` first.
- Internet access for Go modules, tool downloads, container images, and the pinned vCluster chart.
- Two terminal windows. Run the steps in order and stop if a command fails.

The tool downloader installs checksum-verified kind, kubectl, and vCluster binaries under this checkout’s `.cache`, without a system-wide installation. The demo uses a **new kind cluster and separate kubeconfig files**; it does not need access to your existing clusters or a paid cloud account. No Helm CLI or registry push is required.

## 1. Get the tested code and build

In **Terminal A**:

```bash
git clone https://github.com/nimeshbuilds/replicove.git replicove-demo
cd replicove-demo
git checkout --detach 5c8e39083026a949ab1805db64e803e1aea5db1d

./hack/fetch-e2e-tools.sh
export PATH="$PWD/.cache/e2e-tools:$PATH"
make build
docker info >/dev/null
docker build --tag replicove:quickstart .
```

The detached checkout is intentional: the guide’s commands and fixtures match a specific tested revision. The first build can take several minutes while dependencies download.

## 2. Create the disposable host cluster

Continue in Terminal A. Keep this terminal open; later steps use these variables and helper functions.

```bash
umask 077
export REPLICOVE_LAB="$PWD/.cache/quickstart"
export REPLICOVE_CLUSTER="replicove-demo-$(date +%s)-$$"
mkdir -p "$REPLICOVE_LAB"
chmod 700 "$REPLICOVE_LAB"
printf '%s\n' "$REPLICOVE_CLUSTER" > "$REPLICOVE_LAB/cluster-name"

kind create cluster --name "$REPLICOVE_CLUSTER" \
  --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed \
  --kubeconfig "$REPLICOVE_LAB/host.kubeconfig" --wait 180s
kind load docker-image replicove:quickstart --name "$REPLICOVE_CLUSTER"

hk() { kubectl --kubeconfig "$REPLICOVE_LAB/host.kubeconfig" --context "kind-$REPLICOVE_CLUSTER" "$@"; }
rk() { ./bin/replicove --kubeconfig "$REPLICOVE_LAB/host.kubeconfig" --context "kind-$REPLICOVE_CLUSTER" -n replica-lab "$@"; }
gk() { kubectl --kubeconfig "$REPLICOVE_LAB/guest.kubeconfig" "$@"; }

hk get nodes
```

Wait for the node to report `Ready`. `hk` always targets the host, `rk` manages Replicove on that host, and `gk` will target the guest. The host runs Kubernetes 1.36.4; the pinned vCluster 0.37.1 profile runs guest Kubernetes 1.36.0.

## 3. Install Replicove and the sample source

```bash
hk create namespace source-dev
rk install --image replicove:quickstart --values test/e2e/replicove-values.yaml
hk -n replicove-system rollout status deployment/replicove --timeout=180s

hk apply -f test/e2e/source.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
KUBECONFIG="$REPLICOVE_LAB/host.kubeconfig" \
  go run ./test/e2e/seed test/e2e/chart source-dev fixture

hk apply -f test/e2e/grant.yaml
```

The embedded installer creates the operator’s CRDs, the `replicove-system` namespace, and the `replica-lab` destination. The sample contains an echo Deployment, Service, ConfigMap, ServiceAccount, a **dummy test Secret**, and a small Helm-installed ConfigMap. The grant authorizes that source and its named Secret/Helm release. These are disposable fixtures, not permissions to reuse unchanged in a shared cluster.

## 4. Inspect and approve the replica

```bash
rk create demo --grant source-dev-lab --ttl 1h --manual \
  --replication-file test/e2e/replication.yaml
hk -n replica-lab wait clusterreplica/demo \
  --for=jsonpath='{.status.phase}'=AwaitingApproval --timeout=120s
rk plan demo
```

Inspect the plan. The selection maps `source-dev` to guest namespace `integration`, changes the application ConfigMap’s mode to `guest`, and overrides the Helm component’s message to `guest-chart`. No vCluster is provisioned until this manual plan is approved.

```bash
rk approve demo
hk -n replica-lab wait clusterreplica/demo --for=condition=Ready --timeout=420s
rk status demo
```

**Success:** the request reaches `Ready`. Replicove has installed vCluster itself and applied the selected resources. The default persistent profile uses a 1 GiB control-plane PVC and kind’s default StorageClass.

## 5. Connect to the guest

Open **Terminal B in the same `replicove-demo` checkout**:

```bash
./bin/replicove --kubeconfig "$PWD/.cache/quickstart/host.kubeconfig" \
  -n replica-lab connect demo --role viewer \
  --output "$PWD/.cache/quickstart/guest.kubeconfig"
```

Leave it running until cleanup. Once it prints `Tunnel listening on 127.0.0.1`, the guest kubeconfig is ready. The file has private permissions, your default kubeconfig is unchanged, and the session expires after at most 15 minutes. Ctrl-C stops the tunnel and removes its kubeconfig file.

The `viewer` role is sufficient for this walkthrough. For application development, an administrator can grant `deployer` and you can connect with `--role deployer`. CI and agents can use the same CLI; remotely routed or in-cluster clients can use `replicove access`. On shared hosts, administrators must also grant request permissions and exact-name access to the issued credential Secret; this local walkthrough uses the new kind cluster’s administrator identity.

## 6. Verify the replica

Back in **Terminal A**:

```bash
gk -n integration rollout status deployment/echo --timeout=180s
gk -n integration get deployments,services,configmaps

hk -n source-dev get configmap settings -o jsonpath='{.data.mode}{"\n"}'
gk -n integration get configmap settings -o jsonpath='{.data.mode}{"\n"}'
gk -n integration get configmap chart-settings -o jsonpath='{.data.message}{"\n"}'
```

The Deployment should become ready. The final three commands should print, in order:

```text
source
guest
guest-chart
```

You now have a running guest workload with copied and customized configuration; the source’s mode is still `source`. The guest `viewer` role intentionally cannot read Secrets or create workloads.

## 7. Delete the replica and the lab

Press **Ctrl-C in Terminal B**. In **Terminal A**:

```bash
rk delete demo
hk -n replica-lab wait clusterreplica/demo --for=delete --timeout=300s
hk -n replica-lab get pods,services,secrets,persistentvolumeclaims
hk -n source-dev get deployment echo
```

In this fresh demo, the listed destination resources should be empty, while the source Deployment still exists. Replicove removes its owned resources; it keeps the source, operator installation, and host namespaces. Keep the operator running until cleanup finishes.

Now delete **only the kind cluster created by this guide**, including its source fixtures and operator:

```bash
KUBECONFIG="$REPLICOVE_LAB/host.kubeconfig" \
  kind delete cluster --name "$REPLICOVE_CLUSTER"
rm -f "$REPLICOVE_LAB/host.kubeconfig" "$REPLICOVE_LAB/guest.kubeconfig" \
  "$REPLICOVE_LAB/cluster-name"
```

If you leave the replica running, its one-hour TTL starts at request creation and the operator later records `Expired`. TTL does **not** delete the host kind cluster, source fixtures, or operator. If Terminal A was closed, recover its paths/name from `.cache/quickstart/host.kubeconfig` and `.cache/quickstart/cluster-name` before cleanup; do not guess another cluster name.

## If a step fails

Use these **host** diagnostics from Terminal A; share only sanitized output:

```bash
hk -n replica-lab get clusterreplicas,replicaaccesses
hk -n replica-lab describe clusterreplica demo
hk -n replica-lab get pods,persistentvolumeclaims
hk -n replicove-system logs deployment/replicove --tail=100
```

| Symptom | Next check |
| --- | --- |
| `bin/replicove` or a fixture is missing | Confirm `git rev-parse HEAD` is the revision in step 1 and run `make build`. `main` does not yet contain the alpha CLI. |
| Docker is unreachable | Start your Docker engine, confirm `docker info`, and rerun the failed step. |
| Operator `ImagePullBackOff` | Load `replicove:quickstart` into this guide’s kind cluster; the same tag must be passed to `install`. |
| `AwaitingApproval` | Read `rk plan demo`, then approve that plan with `rk approve demo`. |
| A PVC is `Pending` | Check `hk get storageclass` and the pod/PVC events. The persistent profile needs a functioning default StorageClass. |
| `Blocked` | Read the request’s condition reason. Grant, ownership, capability, and capture limits are intentional checks. |
| Guest connection refused or kubeconfig missing | Keep Terminal B running and wait for its tunnel message. Start a new session after access expires. |
| Credential output file already exists | Use a new output filename and update `gk` to point to it; the CLI does not overwrite files. |
| Deletion is stuck | Check operator health and request conditions. Resolve the reported dependency; do not strip finalizers to conceal unfinished cleanup. |

For a scripted verification instead of the interactive walkthrough, install Python 3 and run `./hack/e2e-replication.sh` after step 1. It creates and deletes its own separate kind cluster and exercises the broader lifecycle, including refresh, revocation, and TTL. It does not leave a demo cluster running.

## Next steps

- [Selection, secrets, access, existing targets, and cleanup details](https://github.com/nimeshbuilds/replicove/blob/5c8e39083026a949ab1805db64e803e1aea5db1d/docs/replicove-quickstart.md).
- [cert-manager, Spark, Trino, and admission-policy examples](https://github.com/nimeshbuilds/replicove/blob/5c8e39083026a949ab1805db64e803e1aea5db1d/docs/workload-adapters.md).
- [Project status](docs/project-status.md), [support](SUPPORT.md), and [contributing](CONTRIBUTING.md).

This walkthrough uses the existing tested source and fixtures. Cloud identity, source volume contents, and arbitrary external resources are outside its scope. See the upstream [kind guide](https://kind.sigs.k8s.io/docs/user/quick-start/) for the local host-cluster tooling.
