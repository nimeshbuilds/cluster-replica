# Quick start: your first replica

> **Versioned instructions:** these commands use v0.3.0-alpha.2. Use matching CLI, chart, image and CRDs from that release. Check the release and validation record for source and published-artifact evidence; use a source build when testing an unreleased revision.

Create a disposable Kubernetes cluster, install Replicove, and recreate a small application’s configuration and Helm component inside a vCluster. You will inspect the plan, connect to the replica, verify the copied resources, and remove the environment.

> **Experimental alpha:** this guide targets the `v0.3.0-alpha.2` CLI and operator image with the repository’s disposable fixtures. Replicove installs vCluster for you when you approve the replica; you do not need an existing vCluster.

Prefer a one-command Helm installation? Start with the **[Helm quickstart](docs/getting-started/helm.md)**. Prefer native manifests? Use the **[YAML quickstart](docs/getting-started/yaml.md)** for installation, replication, access, and cleanup without the Replicove or Helm CLI. See the **[developer docs](https://nimeshbuilds.github.io/replicove/)** for feature guides and API references.

## Before you start

- A macOS or Linux machine with Bash, Git, `curl`, `shasum`, and `tar`.
- A running Docker engine with room for a local Kubernetes cluster. Check `docker info` first.
- Internet access for release/tool downloads, container images, and the pinned vCluster chart.
- Two terminal windows. Run the steps in order and stop if a command fails.

The tool downloader installs checksum-verified kind, kubectl, vCluster, and Helm binaries under this checkout’s `.cache`, without a system-wide installation. The demo uses a **new kind cluster and separate kubeconfig files**; it does not need access to your existing clusters or a paid cloud account. Helm is used only to install the sample source component; no image build or registry push is required.

## 1. Get the CLI and examples

This path downloads a prebuilt release CLI; Go is not required. The clone supplies the example applications and lab tools.

In **Terminal A**:

```bash
git clone --branch main https://github.com/nimeshbuilds/replicove.git replicove-demo
cd replicove-demo

./hack/fetch-e2e-tools.sh
./hack/fetch-helm.sh
export PATH="$PWD/.cache/e2e-tools:$PATH"
export REPLICOVE_RELEASE=v0.3.0-alpha.2
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'Use macOS or Linux on amd64/arm64'; exit 1 ;;
esac
asset="replicove-$REPLICOVE_RELEASE-$os-$arch.tar.gz"
mkdir -p .cache/release bin
curl -fL "https://github.com/nimeshbuilds/replicove/releases/download/$REPLICOVE_RELEASE/$asset" -o ".cache/release/$asset"
curl -fL "https://github.com/nimeshbuilds/replicove/releases/download/$REPLICOVE_RELEASE/SHA256SUMS" -o .cache/release/SHA256SUMS
awk -v asset="$asset" '$2 == asset { print; seen++ } END { if (seen != 1) exit 1 }' \
  .cache/release/SHA256SUMS > .cache/release/selected.sha256
(cd .cache/release && shasum -a 256 --check selected.sha256)
tar -xzf ".cache/release/$asset" -C bin replicove
./bin/replicove --version
docker info >/dev/null
```

You stay on the normal `main` branch. The CLI reports `v0.3.0-alpha.2`; the operator image is pulled automatically from GHCR. The Kubernetes and vCluster versions used by the demo remain pinned in the repository.

<details>
<summary>Already cloned the repository or followed the older guide?</summary>

After cleaning up any previous demo using step 7, run these commands from your existing `replicove-demo` directory:

```bash
git switch main
git pull --ff-only
```

This also returns an older detached checkout to `main`. Then rerun step 1 starting at `./hack/fetch-e2e-tools.sh` to download the CLI and tools, and continue with step 2.

</details>

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

hk() { kubectl --kubeconfig "$REPLICOVE_LAB/host.kubeconfig" --context "kind-$REPLICOVE_CLUSTER" "$@"; }
rk() { ./bin/replicove --kubeconfig "$REPLICOVE_LAB/host.kubeconfig" --context "kind-$REPLICOVE_CLUSTER" -n replica-lab "$@"; }
gk() { kubectl --kubeconfig "$REPLICOVE_LAB/guest.kubeconfig" "$@"; }

hk get nodes
```

Wait for the node to report `Ready`. `hk` always targets the host, `rk` manages Replicove on that host, and `gk` will target the guest. The host runs Kubernetes 1.36.4; the pinned vCluster 0.37.1 profile runs guest Kubernetes 1.36.0.

## 3. Install Replicove and the sample source

```bash
hk create namespace source-dev
rk install --image ghcr.io/nimeshbuilds/replicove:0.3.0-alpha.2 --values test/e2e/replicove-values.yaml
hk -n replicove-system rollout status deployment/replicove --timeout=180s

hk apply -f test/e2e/source.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
helm --kubeconfig "$REPLICOVE_LAB/host.kubeconfig" install fixture test/e2e/chart --namespace source-dev

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
| `bin/replicove` or a fixture is missing | Run from the repository root. `git branch --show-current` should print `main`; follow the existing-checkout instructions in step 1 to update and download. |
| Docker is unreachable | Start your Docker engine, confirm `docker info`, and rerun the failed step. |
| Operator `ImagePullBackOff` | Check node access to `ghcr.io` and confirm the image is `ghcr.io/nimeshbuilds/replicove:0.3.0-alpha.2`. |
| `AwaitingApproval` | Read `rk plan demo`, then approve that plan with `rk approve demo`. |
| A PVC is `Pending` | Check `hk get storageclass` and the pod/PVC events. The persistent profile needs a functioning default StorageClass. |
| `Blocked` | Read the request’s condition reason. Grant, ownership, capability, and capture limits are intentional checks. |
| Guest connection refused or kubeconfig missing | Keep Terminal B running and wait for its tunnel message. Start a new session after access expires. |
| Credential output file already exists | Use a new output filename and update `gk` to point to it; the CLI does not overwrite files. |
| Deletion is stuck | Check operator health and request conditions. Resolve the reported dependency; do not strip finalizers to conceal unfinished cleanup. |

For a scripted verification instead of the interactive walkthrough, install Go 1.27.1+, make, and Python 3, then run `./hack/e2e-replication.sh` after step 1. This contributor test builds its own image. It creates and deletes its own separate kind cluster and exercises the broader lifecycle, including refresh, revocation, and TTL. It does not leave a demo cluster running. The [v0.3.0-alpha.1 source passed all 16 CI checks at 93bcfbc](https://github.com/nimeshbuilds/replicove/actions/runs/35948951593), including this workflow on Kubernetes 1.35.8 and 1.36.4. [Validation](docs/validation.md) separates source results from release-artifact checks. [Current `main` CI](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml?query=branch%3Amain) reports checks for subsequent changes.

## Next steps

- [Selection, secrets, access, existing targets, and cleanup details](docs/replicove-quickstart.md).
- [cert-manager, Spark, Trino, and admission-policy examples](docs/workload-adapters.md).
- [Project status](docs/project-status.md), [support](SUPPORT.md), and [contributing](CONTRIBUTING.md).

This walkthrough uses the existing tested source and fixtures. Cloud identity, source volume contents, and arbitrary external resources are outside its scope. See the upstream [kind guide](https://kind.sigs.k8s.io/docs/user/quick-start/) for the local host-cluster tooling.

## Copy workload data and reset it on a schedule

For selected CSI-backed workload data, use the optional [workload mirror guide](docs/guides/mirrors.md). It covers the Helm module, exact PVC grants, YAML requests, manual/scheduled sync, saved-revision reset, access, leases, retention, and TTL cleanup. Ordinary selected PVCs continue to provision fresh volumes; selecting a Secret or PVC does not implicitly grant access to its data.

If you completed this CLI installation with mirroring disabled, [enable it later with an in-place Helm upgrade](docs/guides/enable-mirroring.md). Do not rerun `replicove install` or delete the existing release. The [feature map](docs/features.md) links every implemented capability and its current limits.

## Extend the workflow

Version v0.3.0-alpha.1 introduced optional features through the same operator and CLI. Start with [preflight and plan evidence](docs/guides/diagnostics.md), then use [test recipes](docs/guides/test-runs.md) to create an environment, run your command, write metadata/JUnit artifacts and verify cleanup. Recipes repeat a requested setup against a fresh capture; they do not replay exact historical source state.

- [PostgreSQL 17 copies](docs/guides/postgresql.md): explicit source credentials and database grants, approved masks/table filters, and validated relationships before application/access startup.
- [Bounded chaos](docs/guides/chaos.md): six fault types on owned test workloads with limits and rollback.
- [Destination pools](docs/guides/pools.md): separate managed-runtime destinations, host quotas and operator admission.
- [Stdio MCP](docs/guides/agents-mcp.md): namespace-scoped caller authority, read-only by default; no remote identity/CA service.
- [Local dashboard](docs/guides/dashboard.md): read-only metadata and lifecycle inspection.

Database and chaos modules are disabled by default and can be [enabled later](docs/getting-started/installation.md#optional-modules). Check [validation](docs/validation.md) for the tested revisions and scope.
