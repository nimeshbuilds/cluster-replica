#!/usr/bin/env bash
# Install the operator into a host that already has an independent vCluster.
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
source test/e2e/helm.sh
mkdir -p .cache/helm-existing-e2e
work=$(mktemp -d "$PWD/.cache/helm-existing-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-existing-$(date +%s)-$$"
created=false
forward_pid=''
hk(){ kubectl --kubeconfig "$KUBECONFIG" --context "kind-$cluster" "$@"; }
gk(){ kubectl --kubeconfig "$work/guest.kubeconfig" "$@"; }
cleanup(){
 result=$?; trap - EXIT
 if [[ -n "$forward_pid" ]]; then kill "$forward_pid" 2>/dev/null || true; wait "$forward_pid" 2>/dev/null || true; fi
 if [[ "$created" == true ]]; then
  hk -n replicove-system logs deployment/replicove --tail=150 > "$work/artifacts/operator.log" 2>&1 || true
  hk -n replica-lab get clusterreplicas,replicaaccesses -o json | python3 test/e2e/inventory.py > "$work/artifacts/requests.json" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig" "$work/target.kubeconfig"
 echo "Sanitized Helm existing-target evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then docker build --tag cluster-replica:e2e .; fi
if kind get clusters | grep -Fxq "$cluster"; then exit 1; fi
created=true
kind create cluster --name "$cluster" --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed --kubeconfig "$KUBECONFIG" --wait 180s
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then kind load docker-image cluster-replica:e2e --name "$cluster"; fi
./hack/fetch-chart.sh
# Same reviewed OSS runtime as the portable profile, installed independently.
helm install preexisting .cache/vcluster-0.37.1.tgz --namespace preexisting --create-namespace \
 --set rbac.clusterRole.enabled=false --set rbac.enableVolumeSnapshotRules.enabled=false \
 --set controlPlane.distro.k8s.enabled=true --set-string controlPlane.distro.k8s.image.tag=v1.36.0 \
 --set-string controlPlane.statefulSet.image.repository=loft-sh/vcluster-oss \
 --set-string controlPlane.statefulSet.image.tag=0.37.1 \
 --set controlPlane.statefulSet.persistence.volumeClaim.enabled=true \
 --set-string controlPlane.statefulSet.persistence.volumeClaim.size=1Gi \
 --wait --timeout 5m
runtime_uid=$(hk -n preexisting get statefulset preexisting -o jsonpath='{.metadata.uid}')
# Workload readiness does not imply that vCluster has exported its credential.
hk -n preexisting wait secret/vc-preexisting --for=create --timeout=120s
hk -n preexisting wait secret/vc-preexisting --for=jsonpath='{.data.config}' --timeout=60s
hk -n preexisting get secret vc-preexisting -o json | python3 -c 'import base64,json,sys;open(sys.argv[1],"wb").write(base64.b64decode(json.load(sys.stdin)["data"]["config"]))' "$work/target.kubeconfig"
python3 - "$work/target.kubeconfig" "$work/guest.kubeconfig" <<'PY'
import json,subprocess,sys
config=json.loads(subprocess.check_output(['kubectl','--kubeconfig',sys.argv[1],'config','view','--raw','-o','json']))
cluster=config['clusters'][0]['cluster']
cluster['server']='https://preexisting.preexisting.svc:443'
cluster['tls-server-name']='preexisting.preexisting'
with open(sys.argv[1],'w') as f: json.dump(config,f)
cluster['server']='https://127.0.0.1:18444'
with open(sys.argv[2],'w') as f: json.dump(config,f)
PY
hk -n preexisting port-forward service/preexisting 18444:443 --address 127.0.0.1 > "$work/forward.log" 2>&1 &
forward_pid=$!
for i in $(seq 1 120); do
 if gk --request-timeout=2s get namespace kube-system >/dev/null 2>&1; then break; fi
 sleep 1
done
[[ "$i" -lt 120 ]]
guest_uid=$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')
gk create namespace integration
gk -n integration create configmap foreign --from-literal=keep=yes
foreign_uid=$(gk -n integration get configmap foreign -o jsonpath='{.metadata.uid}')
hk create namespace replica-lab
hk label namespace replica-lab owned-by=fixture
destination_uid=$(hk get namespace replica-lab -o jsonpath='{.metadata.uid}')
hk -n replica-lab create configmap host-sentinel --from-literal=keep=yes
hk create namespace source-dev
hk -n source-dev create configmap selected --from-literal=message=from-host
cat > "$work/values.yaml" <<'YAML'
sources:
  - namespace: source-dev
    rules:
      - apiGroups: [""]
        resources: [configmaps]
        verbs: [get, list]
YAML
# No Replicove namespace, CRDs, service account, or state key exists yet.
replicove_helm_install "$work/values.yaml"
key_uid=$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')
[[ "$(hk get namespace replica-lab -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}')" == '' ]]
hk -n replicove-system create secret generic existing-guest --from-file="config=$work/target.kubeconfig"
rm -f "$work/target.kubeconfig"
cat <<YAML | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaGrant
metadata:
  name: helm-existing
spec:
  targetNamespace: replica-lab
  sourceNamespaces: [source-dev]
  resources:
    - {group: "", kind: ConfigMap}
  existingTargets:
    - name: preexisting
      kubeconfigSecret: {namespace: replicove-system, name: existing-guest}
      clusterUID: "$guest_uid"
---
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ClusterReplica
metadata:
  name: helm-existing
  namespace: replica-lab
spec:
  profile: vcluster-0.37.1-persistent
  ttl: 15m
  cleanupPolicy: DeleteOwned
  grantRef: helm-existing
  target: {provider: existing, existingRef: preexisting}
  replication:
    namespaces: [source-dev]
    namespaceMap: {source-dev: integration}
    include:
      - names: [selected]
YAML
hk -n replica-lab wait clusterreplica/helm-existing --for=condition=Ready --timeout=180s
[[ "$(gk -n integration get configmap selected -o jsonpath='{.data.message}')" == from-host ]]
[[ -z "$(hk -n replica-lab get statefulsets,deployments -o name)" ]]
replicove_helm_install "$work/values.yaml"
[[ "$key_uid" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
hk -n replica-lab delete clusterreplica helm-existing --wait=true --timeout=180s
[[ -z "$(gk -n integration get configmap selected --ignore-not-found -o name)" ]]
[[ "$foreign_uid" == "$(gk -n integration get configmap foreign -o jsonpath='{.metadata.uid}')" ]]
[[ "$runtime_uid" == "$(hk -n preexisting get statefulset preexisting -o jsonpath='{.metadata.uid}')" ]]
[[ "$guest_uid" == "$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')" ]]
helm uninstall replicove --namespace replicove-system --wait --timeout 2m
[[ "$key_uid" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
[[ "$destination_uid" == "$(hk get namespace replica-lab -o jsonpath='{.metadata.uid}')" ]]
[[ "$(hk get namespace replica-lab -o jsonpath='{.metadata.labels.owned-by}')" == fixture ]]
hk -n replica-lab get configmap host-sentinel >/dev/null
hk -n source-dev get configmap selected >/dev/null
gk -n integration get configmap foreign >/dev/null
cat > "$work/artifacts/report.json" <<'JSON'
{"scenario":"helm-existing-vcluster","vclusterPredatesOperator":true,"oneCommandInstall":true,"existingNamespaceNotAdopted":true,"keyRetainedOnUpgradeAndUninstall":true,"replicatedConfig":true,"sourcePreserved":true,"guestIdentityPreserved":true,"foreignGuestResourcePreserved":true,"ownedCleanup":true,"hostSentinelPreserved":true}
JSON
