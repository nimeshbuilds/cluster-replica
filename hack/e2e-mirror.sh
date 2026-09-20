#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/mirror-e2e
work=$(mktemp -d "$PWD/.cache/mirror-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-mirror-$(date +%s)-$$"
created=false
tunnel_pid=''
hk(){ kubectl --kubeconfig "$KUBECONFIG" --context "kind-$cluster" "$@"; }
gk(){ kubectl --kubeconfig "$work/guest.kubeconfig" "$@"; }
cleanup(){
 result=$?; trap - EXIT
 if [[ -n "$tunnel_pid" ]]; then kill "$tunnel_pid" 2>/dev/null || true; wait "$tunnel_pid" 2>/dev/null || true; fi
 if [[ "$created" == true ]]; then
  hk -n replicove-system logs deployment/replicove --tail=250 > "$work/artifacts/operator.log" 2>&1 || true
  hk -n replicove-system logs deployment/replicove-snapshot-controller --tail=100 > "$work/artifacts/snapshots.log" 2>&1 || true
  hk -n replica-lab get replicamirrors,replicamirrorruns,clusterreplicas -o json > "$work/artifacts/status.json" || true
  hk get pods -A -o wide > "$work/artifacts/pods.txt" || true
  hk get events -A --field-selector type=Warning > "$work/artifacts/warnings.txt" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig"
 echo "Mirror evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
docker build --tag cluster-replica:e2e .
cat > "$work/kind.yaml" <<'YAML'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  disableDefaultCNI: true
  podSubnet: 192.168.0.0/16
nodes:
  - role: control-plane
YAML
created=true
kind create cluster --name "$cluster" --config "$work/kind.yaml" --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed --kubeconfig "$KUBECONFIG"
# Pinned Calico is fixture infrastructure, never installed on users' hosts.
curl -fsSL --retry 3 'https://raw.githubusercontent.com/projectcalico/calico/db255c554b929afd73552fd3ac81d691107a1607/manifests/calico.yaml' -o "$work/calico.yaml"
hk apply -f "$work/calico.yaml"
hk -n kube-system rollout status daemonset/calico-node --timeout=300s
hk wait nodes --all --for=condition=Ready --timeout=120s
kind load docker-image cluster-replica:e2e --name "$cluster"
hk create namespace source-dev
make build
source test/e2e/helm.sh
replicove_helm_install test/mirror/values.yaml
hk -n replicove-system rollout status deployment/replicove-snapshot-controller --timeout=180s
hk wait crd/volumesnapshots.snapshot.storage.k8s.io --for=condition=Established --timeout=60s
# Use the upstream CSI host-path driver only in this disposable CI host.
curl -fsSL --retry 3 'https://api.github.com/repos/kubernetes-csi/csi-driver-host-path/tarball/cc78ee78ae23908c9e0607df2fe09c7ecfa52597' -o "$work/driver.tar.gz"
mkdir "$work/driver"
tar -xzf "$work/driver.tar.gz" --strip-components=1 -C "$work/driver"
INSTALL_CRD=false bash "$work/driver/deploy/kubernetes-1.34/deploy.sh"
hk apply -f test/mirror/source.yaml
hk -n source-dev rollout status deployment/orders --timeout=180s
hk -n source-dev exec deployment/orders -- sh -c 'echo host-A > /data/revision; sync'
source_pvc_uid=$(hk -n source-dev get pvc orders-data -o jsonpath='{.metadata.uid}')
source_pv=$(hk -n source-dev get pvc orders-data -o jsonpath='{.spec.volumeName}')
hk apply -f test/mirror/grant.yaml
bin/replicove mirror create orders --file test/mirror/mirror.yaml
hk -n replica-lab wait replicamirror/orders --for=condition=Ready --timeout=600s
first_run=$(hk -n replica-lab get replicamirror orders -o jsonpath='{.status.activeRun.name}')
first_replica=$(hk -n replica-lab get replicamirror orders -o jsonpath='{.status.activeReplica.name}')
connect(){
 if [[ -n "$tunnel_pid" ]]; then kill "$tunnel_pid" 2>/dev/null || true; wait "$tunnel_pid" 2>/dev/null || true; fi
 rm -f "$work/guest.kubeconfig"
 bin/replicove mirror connect orders --role admin --duration-seconds 3600 --output "$work/guest.kubeconfig" > "$work/artifacts/connect.log" 2>&1 &
 tunnel_pid=$!
 for i in $(seq 1 120); do if [[ -f "$work/guest.kubeconfig" ]] && gk --request-timeout=5s get --raw=/readyz >/dev/null 2>&1; then return; fi; if ! kill -0 "$tunnel_pid" 2>/dev/null; then cat "$work/artifacts/connect.log"; return 1; fi; sleep 1; done
 return 1
}
wait_run(){ hk -n replica-lab wait "replicamirrorrun/$1" --for=jsonpath='{.status.phase}'=Active --timeout=600s; }
connect
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == host-A ]]
gk -n orders exec deployment/orders -- sh -c 'echo guest-only > /data/revision; echo mutation > /data/guest-only; sync'
[[ "$(hk -n source-dev exec deployment/orders -- cat /data/revision)" == host-A ]]
hk -n replicove-system rollout restart deployment/replicove
hk -n replicove-system rollout status deployment/replicove --timeout=120s
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == guest-only ]]
# Upgrades must reuse the snapshot controller and its CRDs.
controller_uid=$(hk -n replicove-system get deployment replicove-snapshot-controller -o jsonpath='{.metadata.uid}')
replicove_helm_install test/mirror/values.yaml
[[ "$controller_uid" == "$(hk -n replicove-system get deployment replicove-snapshot-controller -o jsonpath='{.metadata.uid}')" ]]
hk -n source-dev exec deployment/orders -- sh -c 'echo host-B > /data/revision; sync'
bin/replicove mirror hold orders --duration 15m
bin/replicove mirror sync orders --run-name sync-b
bin/replicove mirror sync orders --run-name sync-b
hk -n replica-lab wait replicamirrorrun/sync-b --for=jsonpath='{.status.phase}'=AwaitingActivation --timeout=600s
[[ "$first_replica" == "$(hk -n replica-lab get replicamirror orders -o jsonpath='{.status.activeReplica.name}')" ]]
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == guest-only ]]
bin/replicove mirror release orders
wait_run sync-b
connect
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == host-B ]]
if gk -n orders exec deployment/orders -- test -e /data/guest-only; then echo 'Guest mutation survived reset' >&2; exit 1; fi
[[ "$(hk -n source-dev exec deployment/orders -- cat /data/revision)" == host-B ]]
bin/replicove mirror reset orders --revision "$first_run" --run-name reset-a
wait_run reset-a
connect
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == host-A ]]
[[ "$(hk -n source-dev exec deployment/orders -- cat /data/revision)" == host-B ]]
# A periodic request is coalesced, never a second concurrent restore.
hk -n replica-lab patch replicamirror orders --type=merge -p '{"spec":{"interval":"1m"}}'
for i in $(seq 1 150); do active=$(hk -n replica-lab get replicamirror orders -o jsonpath='{.status.activeRun.name}'); if [[ "$active" != reset-a ]]; then break; fi; sleep 3; done
[[ "$active" != reset-a ]]
bin/replicove mirror suspend orders
connect
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == host-B ]]
kill "$tunnel_pid" 2>/dev/null || true; wait "$tunnel_pid" 2>/dev/null || true; tunnel_pid=''
bin/replicove mirror delete orders
hk -n replica-lab wait replicamirror/orders --for=delete --timeout=420s
[[ -z "$(hk -n replica-lab get clusterreplicas -o name)" ]]
[[ -z "$(hk -n source-dev get volumesnapshots -o name)" ]]
[[ -z "$(hk -n replica-lab get volumesnapshots -o name)" ]]
[[ -z "$(hk get volumesnapshotcontents -o name)" ]]
[[ -z "$(hk -n replica-lab get networkpolicies -o name)" ]]
[[ "$source_pvc_uid" == "$(hk -n source-dev get pvc orders-data -o jsonpath='{.metadata.uid}')" ]]
[[ "$source_pv" == "$(hk -n source-dev get pvc orders-data -o jsonpath='{.spec.volumeName}')" ]]
[[ "$(hk -n source-dev exec deployment/orders -- cat /data/revision)" == host-B ]]
cat > "$work/artifacts/report.json" <<'JSON'
{"result":"passed","scenarios":["helm-bundled-snapshot-controller","helm-upgrade-reuse","real-csi-source-capture","guest-data-copy","independent-guest-writes","operator-restart","idempotent-manual-sync","test-lease","latest-source-reset","saved-revision-reset","scheduled-reset","owned-volume-snapshot-cleanup","source-identity-and-data-preserved"]}
JSON
