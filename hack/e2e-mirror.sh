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
  hk -n replica-lab get pods -o json | python3 test/e2e/inventory.py > "$work/artifacts/runtime-status.json" || true
  while read -r pod; do
   [[ -n "$pod" ]] || continue
   hk -n replica-lab logs "$pod" -c syncer --tail=250 > "$work/artifacts/${pod#pod/}.log" 2>&1 || true
   hk -n replica-lab logs "$pod" -c syncer --previous --tail=250 > "$work/artifacts/${pod#pod/}-previous.log" 2>&1 || true
  done < <(hk -n replica-lab get pods -l app=vcluster -o name)
  docker exec "$cluster-control-plane" sysctl fs.inotify.max_user_instances fs.inotify.max_user_watches > "$work/artifacts/node-limits.txt" 2>&1 || true
  hk -n replica-lab get pods --show-labels > "$work/artifacts/labels.txt" || true
  hk -n replica-lab get networkpolicies -o yaml > "$work/artifacts/networkpolicies.yaml" || true
  hk get events -A --field-selector type=Warning > "$work/artifacts/warnings.txt" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig" "$work/existing.kubeconfig" "$work/shared.kubeconfig"
 echo "Mirror evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then docker build --tag cluster-replica:e2e .; fi
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
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then kind load docker-image cluster-replica:e2e --name "$cluster"; fi
hk create namespace source-dev
if [[ "${REPLICOVE_USE_RELEASE_CLI:-false}" != true ]]; then make build; fi
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
source_ip=$(hk -n source-dev get pod -l app=orders -o jsonpath='{.items[0].status.podIP}')
[[ "$(hk -n source-dev exec deployment/orders -- wget -qO- "http://$source_ip:8080/revision")" == host-A ]]
# The mirror operator gets snapshot mutation but no source workload/data mutation.
operator_identity=system:serviceaccount:replicove-system:replicove
[[ "$(hk --as="$operator_identity" -n source-dev auth can-i create volumesnapshots.snapshot.storage.k8s.io)" == yes ]]
if hk --as="$operator_identity" -n source-dev auth can-i delete persistentvolumeclaims; then exit 1; fi
if hk --as="$operator_identity" -n source-dev auth can-i create pods; then exit 1; fi
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
wait_phase(){
 local run="$1" expected="$2" phase child release
 for i in $(seq 1 120); do
  phase=$(hk -n replica-lab get replicamirrorrun "$run" -o jsonpath='{.status.phase}')
  if [[ "$phase" == "$expected" ]]; then return; fi
  child=$(hk -n replica-lab get replicamirrorrun "$run" -o jsonpath='{.status.replicaRef.name}')
  if [[ -n "$child" ]]; then
   release=$(hk -n replica-lab get clusterreplica "$child" -o jsonpath='{.status.runtime.releaseName}')
   if [[ -n "$release" ]] && hk -n replica-lab get pods -l "app=vcluster,release=$release" -o json | python3 -c 'import json,sys;sys.exit(0 if any(c.get("restartCount",0)>=3 for p in json.load(sys.stdin)["items"] for c in p.get("status",{}).get("containerStatuses",[])) else 1)'; then
    echo "Runtime $release repeatedly restarted while waiting for $run/$expected; retaining startup evidence." >&2
    return 1
   fi
  fi
  sleep 5
 done
 echo "Timed out waiting for $run/$expected" >&2
 return 1
}
wait_run(){ wait_phase "$1" Active; }
connect
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == host-A ]]
# Prove policies enforce denial against a reachable source endpoint, while
# local application and guest DNS still work. A transport failure cannot pass.
gk -n orders exec deployment/orders -- nslookup kubernetes.default.svc.cluster.local
[[ "$(gk -n orders exec deployment/orders -- wget -qO- http://127.0.0.1:8080/revision)" == host-A ]]
gk -n orders exec deployment/orders -- sh -c 'if wget -T 3 -qO- "$1"; then exit 42; else echo egress-denied; fi' sh "http://$source_ip:8080/revision" | tee "$work/artifacts/egress.txt"
[[ "$(cat "$work/artifacts/egress.txt")" == egress-denied ]]
gk -n orders exec deployment/orders -- sh -c 'echo guest-only > /data/revision; echo mutation > /data/guest-only; sync'
[[ "$(hk -n source-dev exec deployment/orders -- cat /data/revision)" == host-A ]]
hk -n replicove-system rollout restart deployment/replicove
hk -n replicove-system rollout status deployment/replicove --timeout=120s
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == guest-only ]]
# Upgrades must reuse the snapshot controller and its CRDs.
controller_uid=$(hk -n replicove-system get deployment replicove-snapshot-controller -o jsonpath='{.metadata.uid}')
replicove_helm_install test/mirror/values.yaml
[[ "$controller_uid" == "$(hk -n replicove-system get deployment replicove-snapshot-controller -o jsonpath='{.metadata.uid}')" ]]
# Another installation reuses host snapshot APIs/controller without adopting them.
REPLICOVE_HELM_RELEASE=mirror-probe REPLICOVE_HELM_NAMESPACE=mirror-probe-system REPLICOVE_DESTINATION_NAMESPACE=mirror-probe-lab replicove_helm_install test/mirror/values.yaml
[[ -z "$(hk -n mirror-probe-system get deployment mirror-probe-snapshot-controller --ignore-not-found -o name)" ]]
[[ "$(hk get crd volumesnapshots.snapshot.storage.k8s.io -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}')" == replicove ]]
helm uninstall mirror-probe --namespace mirror-probe-system --wait --timeout 120s
[[ "$controller_uid" == "$(hk -n replicove-system get deployment replicove-snapshot-controller -o jsonpath='{.metadata.uid}')" ]]

# A separate managed request reports the occupied namespace without installing
# a second vCluster or adopting/deleting the working mirror runtime.
hk -n replica-lab get clusterreplica "$first_replica" -o json | python3 -c 'import json,sys;r=json.load(sys.stdin);r["metadata"]={"namespace":"replica-lab","name":"occupied-runtime"};r.pop("status",None);json.dump(r,sys.stdout)' | hk apply -f -
hk -n replica-lab wait clusterreplica/occupied-runtime --for=jsonpath='{.status.conditions[0].reason}'=RuntimeNamespaceInUse --timeout=120s
[[ "$(hk -n replica-lab get statefulsets -l app=vcluster -o name | wc -l | tr -d ' ')" == 1 ]]
hk -n replica-lab delete clusterreplica occupied-runtime --wait=true --timeout=120s
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == guest-only ]]

# Captures can be prepared while leased. Managed replacement must keep the old
# runtime until release, then retire it before starting the next runtime.
bin/replicove mirror hold orders --duration 15m
bin/replicove mirror sync orders --run-name cancel-candidate
wait_phase cancel-candidate AwaitingReplacement
[[ -z "$(hk -n replica-lab get replicamirrorrun cancel-candidate -o jsonpath='{.status.replicaRef.name}')" ]]
bin/replicove mirror cancel cancel-candidate
hk -n replica-lab wait replicamirrorrun/cancel-candidate --for=delete --timeout=300s
[[ "$first_replica" == "$(hk -n replica-lab get replicamirror orders -o jsonpath='{.status.activeReplica.name}')" ]]
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == guest-only ]]
bin/replicove mirror release orders

# Register the running, independently owned runtime for a second mirror. Its
# TTL must remove only that mirror's generation, credentials and restored data.
runtime_release=$(hk -n replica-lab get clusterreplica "$first_replica" -o jsonpath='{.status.runtime.releaseName}')
runtime_uid=$(hk -n replica-lab get statefulset "$runtime_release" -o jsonpath='{.metadata.uid}')
guest_uid=$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')
hk -n replica-lab get secret "vc-$runtime_release" -o json | python3 -c 'import base64,json,sys;open(sys.argv[1],"wb").write(base64.b64decode(json.load(sys.stdin)["data"]["config"]))' "$work/existing.kubeconfig"
python3 - "$work/existing.kubeconfig" "$runtime_release" <<'PYTARGET'
import json,subprocess,sys
config=json.loads(subprocess.check_output(['kubectl','--kubeconfig',sys.argv[1],'config','view','--raw','-o','json']))
config['clusters'][0]['cluster']['server']='https://'+sys.argv[2]+'.replica-lab.svc:443'
config['clusters'][0]['cluster']['tls-server-name']=sys.argv[2]+'.replica-lab'
with open(sys.argv[1],'w') as f:json.dump(config,f)
PYTARGET
hk -n replicove-system create secret generic mirror-existing --from-file="config=$work/existing.kubeconfig"
hk get replicagrant mirror-lab -o json | python3 -c 'import json,sys;g=json.load(sys.stdin);g["metadata"]={"name":"mirror-existing"};g.pop("status",None);g["spec"]["existingTargets"]=[{"name":"shared","kubeconfigSecret":{"namespace":"replicove-system","name":"mirror-existing"},"clusterUID":sys.argv[1],"mirrorReleaseName":sys.argv[2]}];json.dump(g,sys.stdout)' "$guest_uid" "$runtime_release" | hk apply -f -
hk -n replica-lab get replicamirror orders -o json | python3 -c 'import json,sys;m=json.load(sys.stdin);m["metadata"]={"namespace":"replica-lab","name":"shared"};m.pop("status",None);m["spec"]["template"]["grantRef"]="mirror-existing";m["spec"]["template"]["ttl"]="15m";m["spec"]["template"]["target"]={"provider":"existing","existingRef":"shared"};json.dump(m,sys.stdout)' | hk apply -f -
hk -n replica-lab wait replicamirror/shared --for=condition=Ready --timeout=180s
shared_child=$(hk -n replica-lab get replicamirror shared -o jsonpath='{.status.activeReplica.name}')
shared_ns=$(hk -n replica-lab get clusterreplica "$shared_child" -o jsonpath='{.spec.replication.namespaceMap.source-dev}')
[[ "$shared_ns" != orders && -n "$shared_ns" ]]
[[ "$(gk -n "$shared_ns" exec deployment/orders -- cat /data/revision)" == host-A ]]
bin/replicove mirror access shared --role admin --output "$work/shared.kubeconfig"
python3 - "$work/guest.kubeconfig" "$work/shared.kubeconfig" <<'PYACCESS'
import json,subprocess,sys
configs=[json.loads(subprocess.check_output(['kubectl','--kubeconfig',p,'config','view','--raw','-o','json'])) for p in sys.argv[1:]]
configs[1]['clusters'][0]['cluster']['server']=configs[0]['clusters'][0]['cluster']['server']
with open(sys.argv[2],'w') as f:json.dump(configs[1],f)
PYACCESS
[[ "$(kubectl --kubeconfig "$work/shared.kubeconfig" auth can-i create deployments -n "$shared_ns")" == yes ]]
if kubectl --kubeconfig "$work/shared.kubeconfig" auth can-i get secrets -n orders; then exit 1; fi
if kubectl --kubeconfig "$work/shared.kubeconfig" auth can-i create namespaces; then exit 1; fi
# Existing-target candidates can restore into a separate namespace before the
# switch. Cancelling that restored candidate must remove its data and namespace.
bin/replicove mirror hold shared --duration 15m
bin/replicove mirror sync shared --run-name shared-cancel
wait_phase shared-cancel AwaitingActivation
cancelled_child=$(hk -n replica-lab get replicamirrorrun shared-cancel -o jsonpath='{.status.replicaRef.name}')
cancelled_ns=$(hk -n replica-lab get clusterreplica "$cancelled_child" -o jsonpath='{.spec.replication.namespaceMap.source-dev}')
[[ "$(gk -n "$cancelled_ns" exec deployment/orders -- cat /data/revision)" == host-A ]]
bin/replicove mirror cancel shared-cancel
hk -n replica-lab wait replicamirrorrun/shared-cancel --for=delete --timeout=300s
[[ -z "$(gk get namespace "$cancelled_ns" --ignore-not-found -o name)" ]]
[[ "$shared_child" == "$(hk -n replica-lab get replicamirror shared -o jsonpath='{.status.activeReplica.name}')" ]]
bin/replicove mirror delete shared
hk -n replica-lab wait replicamirror/shared --for=delete --timeout=240s
[[ -z "$(gk get namespace "$shared_ns" --ignore-not-found -o name)" ]]
[[ "$runtime_uid" == "$(hk -n replica-lab get statefulset "$runtime_release" -o jsonpath='{.metadata.uid}')" ]]
[[ "$(gk -n orders exec deployment/orders -- cat /data/revision)" == guest-only ]]
if kubectl --kubeconfig "$work/shared.kubeconfig" --request-timeout=5s get deployments -n "$shared_ns"; then echo 'Expired access survived' >&2; exit 1; fi
# Separately prove a minimum five-minute mirror TTL, without requesting a token
# shorter than Kubernetes' ten-minute TokenRequest minimum.
hk -n replica-lab get replicamirror orders -o json | python3 -c 'import json,sys;m=json.load(sys.stdin);m["metadata"]={"namespace":"replica-lab","name":"shared-ttl"};m.pop("status",None);m["spec"]["template"]["grantRef"]="mirror-existing";m["spec"]["template"]["ttl"]="5m";m["spec"]["template"]["target"]={"provider":"existing","existingRef":"shared"};json.dump(m,sys.stdout)' | hk apply -f -
hk -n replica-lab wait replicamirror/shared-ttl --for=condition=Ready --timeout=180s
ttl_child=$(hk -n replica-lab get replicamirror shared-ttl -o jsonpath='{.status.activeReplica.name}')
ttl_ns=$(hk -n replica-lab get clusterreplica "$ttl_child" -o jsonpath='{.spec.replication.namespaceMap.source-dev}')
[[ "$(gk -n "$ttl_ns" exec deployment/orders -- cat /data/revision)" == host-A ]]
hk -n replica-lab wait replicamirror/shared-ttl --for=jsonpath='{.status.phase}'=Expired --timeout=420s
[[ -z "$(gk get namespace "$ttl_ns" --ignore-not-found -o name)" ]]
[[ "$runtime_uid" == "$(hk -n replica-lab get statefulset "$runtime_release" -o jsonpath='{.metadata.uid}')" ]]
bin/replicove mirror delete shared-ttl
hk -n replica-lab wait replicamirror/shared-ttl --for=delete --timeout=120s
hk -n source-dev exec deployment/orders -- sh -c 'echo host-B > /data/revision; sync'
bin/replicove mirror hold orders --duration 15m
mirror_uid=$(hk -n replica-lab get replicamirror orders -o jsonpath='{.metadata.uid}')
cat <<YAML | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaMirrorRun
metadata: {name: sync-b, namespace: replica-lab}
spec:
  mirrorRef: {name: orders, uid: "$mirror_uid"}
  action: Sync
YAML
bin/replicove mirror sync orders --run-name sync-b
bin/replicove mirror sync orders --run-name sync-b
wait_phase sync-b AwaitingReplacement
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
for i in $(seq 1 150); do active=$(hk -n replica-lab get replicamirror orders -o jsonpath='{.status.activeRun.name}'); if [[ -n "$active" && "$active" != reset-a ]]; then break; fi; sleep 3; done
[[ -n "$active" && "$active" != reset-a ]]
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
# Reinstall after finalizers finish: retained APIs must not make auto mode
# incorrectly assume that the removed snapshot controller is still running.
state_key_uid=$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')
helm uninstall replicove --namespace replicove-system --wait --timeout 120s
hk get crd volumesnapshots.snapshot.storage.k8s.io >/dev/null
replicove_helm_install test/mirror/values.yaml
hk -n replicove-system rollout status deployment/replicove-snapshot-controller --timeout=180s
[[ "$state_key_uid" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
cat > "$work/artifacts/report.json" <<'JSON'
{"result":"passed","scenarios":["helm-bundled-snapshot-controller","helm-upgrade-reuse","existing-snapshot-controller-reuse","reinstall-with-retained-snapshot-apis","real-csi-source-capture","guest-data-copy","independent-guest-writes","source-egress-denied","guest-dns-allowed","source-writes-forbidden","existing-runtime-mirror","existing-namespace-rbac","existing-mirror-ttl","cancelled-candidate-cleanup","cancelled-restored-namespace-cleanup","yaml-sync","operator-restart","idempotent-manual-sync","test-lease","one-runtime-per-host-namespace","latest-source-reset","saved-revision-reset","scheduled-reset","owned-volume-snapshot-cleanup","source-identity-and-data-preserved"]}
JSON
