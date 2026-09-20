# Quickstart with YAML

Install Replicove and create a replica using native Kubernetes manifests. The Replicove CLI and Helm CLI are not required. Replicove installs vCluster inside the host cluster for you.

This walkthrough uses a new disposable kind cluster and the published alpha image. You need macOS or Linux, Bash, Git, Python 3, curl, shasum, and a running Docker engine. The repository downloads checksum-verified kind and kubectl binaries. Use two terminals for the connection step.

## 1. Create a disposable host

In Terminal A:

```bash
git clone --branch main https://github.com/nimeshbuilds/replicove.git replicove-yaml
cd replicove-yaml
./hack/fetch-e2e-tools.sh
export PATH="$PWD/.cache/e2e-tools:$PATH"
docker info >/dev/null

umask 077
mkdir -p .cache/yaml-demo
export REPLICOVE_YAML_CLUSTER="replicove-yaml-$(date +%s)-$$"
export REPLICOVE_YAML_HOST="$PWD/.cache/yaml-demo/host.kubeconfig"
kind create cluster --name "$REPLICOVE_YAML_CLUSTER" \
  --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed \
  --kubeconfig "$REPLICOVE_YAML_HOST" --wait 180s
hk() { kubectl --kubeconfig "$REPLICOVE_YAML_HOST" --context "kind-$REPLICOVE_YAML_CLUSTER" "$@"; }
```

Keep Terminal A open; subsequent commands use its variables. Stop if any command fails. The cluster pulls the published image automatically. An existing checkout can use `git switch main` and `git pull --ff-only` after cleaning up its previous demo.

## 2. Install the operator from manifests

```bash
export REPLICOVE_RELEASE_URL=https://github.com/nimeshbuilds/replicove/releases/download/v0.2.0-alpha.2
hk apply -f "$REPLICOVE_RELEASE_URL/replicove-crds.yaml"
hk wait --for=condition=Established --timeout=60s \
  crd/clusterreplicas.replica.nimeshbuilds.dev \
  crd/replicagrants.replica.nimeshbuilds.dev \
  crd/replicaaccesses.replica.nimeshbuilds.dev
hk apply -f "$REPLICOVE_RELEASE_URL/replicove-install.yaml"
hk -n replicove-system wait job/replicove-bootstrap \
  --for=condition=Complete --timeout=180s
hk -n replicove-system rollout status deployment/replicove --timeout=180s
```

The versioned release installer pins the operator image digest. It is generated from the [base manifests](../../config/install/) and creates `replicove-system`, `replica-lab`, explicit RBAC, the key bootstrap Job, and the operator Deployment. The protected key stays inside the cluster. There is no pre-generated key in Git.

## 3. Grant a source

Apply the disposable fixture, its read-only source permissions, and the administrator grant:

```bash
hk apply -f examples/yaml/source.yaml
hk apply -f examples/yaml/source-rbac.yaml
hk apply -f examples/yaml/grant.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
```

The fixture contains an echo Deployment, a ServiceAccount, a Service, a ConfigMap, and a **dummy demonstration Secret**. Never replace that dummy value with a real credential in Git. The grant separately authorizes this one Secret and bounds replicas to two hours.

## 4. Create the replica

The complete request is [examples/yaml/replica.yaml](../../examples/yaml/replica.yaml):

```yaml
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ClusterReplica
metadata:
  name: yaml-demo
  namespace: replica-lab
spec:
  profile: vcluster-0.37.1-persistent
  ttl: 1h
  cleanupPolicy: DeleteOwned
  approval: Automatic
  grantRef: yaml-demo
  target:
    provider: auto
  replication:
    namespaces: [source-dev]
    namespaceMap:
      source-dev: integration
    secrets: Snapshot
    data: None
    patches:
      - apiVersion: v1
        kind: ConfigMap
        namespace: source-dev
        name: settings
        patch:
          data:
            mode: guest-yaml
```

```bash
hk apply -f examples/yaml/replica.yaml
hk -n replica-lab wait clusterreplica/yaml-demo --for=condition=Ready --timeout=420s
hk -n replica-lab get clusterreplica yaml-demo
```

Patch selectors use the **source** identity; the object is created in mapped guest namespace `integration`. This profile provisions a persistent vCluster control plane using the kind host's default StorageClass. Workload volume contents are not copied.

For review before provisioning, set `approval: Manual` **before first applying the request**. Wait for `status.phase=AwaitingApproval`, inspect `status.plan`, and annotate the exact revision as shown in [plans and approval](../guides/lifecycle.md). The spec is immutable once created.

## 5. Request temporary access with YAML

The request must bind to the current replica UID so a reused name cannot inherit credentials. Read that UID and include it in the manifest:

```bash
replica_uid=$(hk -n replica-lab get clusterreplica yaml-demo -o jsonpath='{.metadata.uid}')
cat <<YAML | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaAccess
metadata:
  name: yaml-session
  namespace: replica-lab
spec:
  replicaName: yaml-demo
  replicaUID: "$replica_uid"
  role: viewer
  durationSeconds: 900
YAML
hk -n replica-lab wait replicaaccess/yaml-session \
  --for=jsonpath='{.status.phase}'=Ready --timeout=180s
```

This is still a native YAML resource; its parent UID is runtime data. It requests 15 minutes of viewer access, capped by the grant and remaining replica TTL. Request access while at least ten minutes of replica lifetime remain.

The demo uses the disposable host's administrator credentials to retrieve the exact credential Secret:

```bash
credential=$(hk -n replica-lab get replicaaccess yaml-session -o jsonpath='{.status.credentialSecret}')
export REPLICOVE_YAML_GUEST="$PWD/.cache/yaml-demo/guest.kubeconfig"
( set -o noclobber
  hk -n replica-lab get secret "$credential" -o jsonpath='{.data.config}' \
    | python3 -c 'import base64,sys;sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))' \
    > "$REPLICOVE_YAML_GUEST"
)
chmod 600 "$REPLICOVE_YAML_GUEST"
kubectl --kubeconfig "$REPLICOVE_YAML_GUEST" config set-cluster replicove \
  --server=https://127.0.0.1:18443
```

The CA and TLS server name stay unchanged. For real users and agents, configure [exact-Secret reader permissions](../guides/access.md); do not grant general Secret reads in the destination namespace.

## 6. Connect and verify

In **Terminal B**, enter the same checkout directory, then run:

```bash
export PATH="$PWD/.cache/e2e-tools:$PATH"
yaml_host="$PWD/.cache/yaml-demo/host.kubeconfig"
yaml_runtime=$(kubectl --kubeconfig "$yaml_host" -n replica-lab \
  get clusterreplica yaml-demo -o jsonpath='{.status.runtime.releaseName}')
kubectl --kubeconfig "$yaml_host" -n replica-lab port-forward \
  "service/$yaml_runtime" 18443:443 --address 127.0.0.1
```

Keep the forward running. If port 18443 is occupied, choose another free local port and update the guest kubeconfig's server to match. In **Terminal A**:

```bash
gk() { kubectl --kubeconfig "$REPLICOVE_YAML_GUEST" "$@"; }
gk -n integration get deployments,services,configmaps
gk -n integration get configmap settings -o jsonpath='{.data.mode}'
hk -n source-dev get configmap settings -o jsonpath='{.data.mode}'
gk -n integration auth can-i create deployments
```

Expect `guest-yaml`, `source`, and `no`, respectively. The last command exits nonzero because the viewer cannot create Deployments. Unlike the CLI's `connect`, a raw `kubectl port-forward` does not automatically reconnect or remove the credential file when stopped.

## 7. Revoke and clean up

Stop Terminal B's forward with Ctrl-C. In Terminal A:

```bash
hk -n replica-lab delete replicaaccess yaml-session --wait=true --timeout=180s
hk -n replica-lab delete clusterreplica yaml-demo --wait=true --timeout=360s
hk -n replica-lab get pods,services,pvc,statefulsets,secrets
hk -n source-dev get deployment echo
kind delete cluster --name "$REPLICOVE_YAML_CLUSTER"
rm -f "$REPLICOVE_YAML_HOST" "$REPLICOVE_YAML_GUEST"
```

The destination should have no owned runtime resources and the source Deployment should remain until the disposable kind cluster is deleted. If you leave the replica running, its one-hour TTL initiates cleanup; the expired request remains as status evidence. Cleanup can be blocked by finalizers, unavailable APIs, or unsupported storage behavior—see [TTL and cleanup](../guides/cleanup.md).

## Automated verification

For optional data mirroring after this installation, see [native YAML and GitOps enablement](../guides/enable-mirroring.md#cli-native-yaml-and-gitops-installations). Keep native ownership and the existing state key, and apply the complete optional RBAC/dependencies alongside the flags. The [feature map](../features.md) separates shipped functionality from proposed integrations.

[`hack/e2e-yaml.sh`](../../hack/e2e-yaml.sh) exercises these checked-in installation and request manifests in a fresh kind cluster, including idempotent key bootstrap, guest access, configuration checks, access revocation, source preservation, and owned cleanup. It does not use the Replicove or Helm CLI. The release workflow repeats it with the published digest-pinned YAML and image. See the **yaml-workflow** job in [CI](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml).
