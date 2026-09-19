#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/replication-e2e
work=$(mktemp -d "$PWD/.cache/replication-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-e2e-$(date +%s)-$$"
created=false
tunnel_pid=''
hk(){ kubectl --kubeconfig "$work/host.kubeconfig" --context "kind-$cluster" "$@"; }
gk(){ kubectl --kubeconfig "$work/guest.kubeconfig" "$@"; }
cleanup(){
 result=$?;trap - EXIT
 if [[ -n "$tunnel_pid" ]];then kill "$tunnel_pid" 2>/dev/null || true;wait "$tunnel_pid" 2>/dev/null || true;fi
 if [[ "$created" == true ]];then
  hk -n replicove-system logs deployment/replicove --tail=200 > "$work/artifacts/operator.log" 2>&1 || true
  hk -n replica-lab get clusterreplicas,replicaaccesses -o json | python3 test/e2e/inventory.py > "$work/artifacts/requests.json" || true
  hk -n replica-lab get pods,services,secrets,persistentvolumeclaims,deployments -o json | python3 test/e2e/inventory.py > "$work/artifacts/host-inventory.json" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig" "$work/guest-access.kubeconfig"
 echo "Sanitized replica evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
node_image="${REPLICOVE_KIND_IMAGE:-kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed}"
docker build --tag cluster-replica:e2e .
if kind get clusters | grep -Fxq "$cluster";then exit 1;fi
created=true
kind create cluster --name "$cluster" --image "$node_image" --kubeconfig "$KUBECONFIG" --wait 180s
kind load docker-image cluster-replica:e2e --name "$cluster"
hk create namespace source-dev
make build
bin/replicove install --image cluster-replica:e2e --values test/e2e/replicove-values.yaml
hk -n replicove-system rollout status deployment/replicove --timeout=180s
hk apply -f test/e2e/source.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
go run ./test/e2e/seed test/e2e/chart source-dev fixture
hk apply -f test/e2e/grant.yaml
hk -n replica-lab create configmap unrelated-sentinel --from-literal=keep=yes
bin/replicove create full --grant source-dev-lab --ttl 30m --manual --replication-file test/e2e/replication.yaml
hk -n replica-lab wait clusterreplica/full --for=jsonpath='{.status.phase}'=AwaitingApproval --timeout=120s
[[ -z "$(hk -n replica-lab get deployment -o name)" ]]
bin/replicove plan full > "$work/artifacts/plan.json"
bin/replicove approve full
hk -n replica-lab wait clusterreplica/full --for=condition=Ready --timeout=420s
bin/replicove connect full --role admin --output "$work/guest.kubeconfig" > "$work/artifacts/connect.txt" 2>&1 &
tunnel_pid=$!
for i in $(seq 1 180);do if [[ -f "$work/guest.kubeconfig" ]];then break;fi;sleep 1;done
[[ -f "$work/guest.kubeconfig" ]]
gk -n integration rollout status deployment/echo --timeout=180s
[[ "$(gk -n integration get configmap settings -o jsonpath='{.data.mode}')" == guest ]]
[[ "$(gk -n integration get configmap chart-settings -o jsonpath='{.data.message}')" == guest-chart ]]
# Secret contents are compared in memory and are never printed or uploaded.
hk -n source-dev get secret fixture-password -o json | python3 -c 'import json,subprocess,sys;source=json.load(sys.stdin);guest=json.loads(subprocess.check_output(["kubectl","--kubeconfig",sys.argv[1],"-n","integration","get","secret","fixture-password","-o","json"]));assert source["data"]==guest["data"]' "$work/guest.kubeconfig"
gk -n integration run probe --image=busybox:1.37.0-1 --restart=Never --command -- sh -c 'wget -qO- http://echo:8080/hostname'
gk -n integration wait pod/probe --for=jsonpath='{.status.phase}'=Succeeded --timeout=120s
# Experiments must survive a controller restart and ordinary reconciliation.
gk -n integration annotate configmap settings experiment=keep
runtime_uid=$(hk -n replica-lab get clusterreplica full -o jsonpath='{.status.runtime.releaseName}')
hk -n replicove-system rollout restart deployment/replicove
hk -n replicove-system rollout status deployment/replicove --timeout=120s
sleep 15
[[ "$(gk -n integration get configmap settings -o jsonpath='{.metadata.annotations.experiment}')" == keep ]]
[[ "$(hk -n replica-lab get clusterreplica full -o jsonpath='{.status.runtime.releaseName}')" == "$runtime_uid" ]]
# The durable profile must survive rescheduling its own control-plane pod.
guest_cluster_uid=$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')
guest_workload_uid=$(gk -n integration get deployment echo -o jsonpath='{.metadata.uid}')
kill "$tunnel_pid";wait "$tunnel_pid" || true;tunnel_pid=''
hk -n replica-lab delete pod "$runtime_uid-0" --wait=true
hk -n replica-lab rollout status statefulset/"$runtime_uid" --timeout=180s
bin/replicove connect full --role admin --output "$work/guest.kubeconfig" > "$work/artifacts/reconnect.txt" 2>&1 &
tunnel_pid=$!
for i in $(seq 1 180);do if [[ -f "$work/guest.kubeconfig" ]];then break;fi;sleep 1;done
[[ -f "$work/guest.kubeconfig" ]]
[[ "$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')" == "$guest_cluster_uid" ]]
[[ "$(gk -n integration get deployment echo -o jsonpath='{.metadata.uid}')" == "$guest_workload_uid" ]]
# Snapshot refresh changes only source-owned fields and preserves added fields.
hk -n source-dev patch configmap settings --type merge -p '{"data":{"extra":"refreshed"}}'
bin/replicove refresh full
hk -n replica-lab wait clusterreplica/full --for=jsonpath='{.status.phase}'=AwaitingApproval --timeout=120s
bin/replicove approve full
hk -n replica-lab wait clusterreplica/full --for=condition=Ready --timeout=180s
[[ "$(gk -n integration get configmap settings -o jsonpath='{.data.extra}')" == refreshed ]]
[[ "$(gk -n integration get configmap settings -o jsonpath='{.metadata.annotations.experiment}')" == keep ]]
# Follow mode updates a granted Secret without refreshing workloads.
hk -n source-dev patch secret fixture-password --type merge -p '{"stringData":{"password":"ci-rotated-value"}}' > /dev/null
for i in $(seq 1 60);do
 if hk -n source-dev get secret fixture-password -o json | python3 -c 'import json,subprocess,sys;source=json.load(sys.stdin);guest=json.loads(subprocess.check_output(["kubectl","--kubeconfig",sys.argv[1],"-n","integration","get","secret","fixture-password","-o","json"]));sys.exit(0 if source["data"]==guest["data"] else 1)' "$work/guest.kubeconfig";then break;fi
 sleep 2
done
[[ "$i" -lt 60 ]]
# Least-privilege guest viewer credentials can read workloads but cannot create them.
bin/replicove access full --role viewer --output "$work/guest-access.kubeconfig"
python3 - "$work/guest.kubeconfig" "$work/guest-access.kubeconfig" <<'PY'
# Kubeconfigs are handled in the private test directory only; kubectl emits JSON
# to this process, never to logs or evidence artifacts.
import json,subprocess,sys
admin=json.loads(subprocess.check_output(['kubectl','--kubeconfig',sys.argv[1],'config','view','--raw','-o','json']))
viewer=json.loads(subprocess.check_output(['kubectl','--kubeconfig',sys.argv[2],'config','view','--raw','-o','json']))
viewer['clusters'][0]['cluster']['server']=admin['clusters'][0]['cluster']['server']
with open(sys.argv[2],'w') as f:json.dump(viewer,f)
PY
[[ "$(kubectl --kubeconfig "$work/guest-access.kubeconfig" auth can-i get deployments -n integration)" == yes ]]
if kubectl --kubeconfig "$work/guest-access.kubeconfig" auth can-i create deployments -n integration;then exit 1;fi
bin/replicove status full > "$work/artifacts/ready.json"
# Stop the local proxy before deleting the guest. The access controller still
# revokes both guest identities and their host credential Secrets.
kill "$tunnel_pid";wait "$tunnel_pid" || true;tunnel_pid=''
bin/replicove delete full
hk -n replica-lab wait clusterreplica/full --for=delete --timeout=300s
[[ -z "$(hk -n replicove-system get secret -l app.kubernetes.io/managed-by=replicove -o name)" ]]
[[ -z "$(hk -n replica-lab get pods,services,secrets,persistentvolumeclaims -o name)" ]]
hk -n replica-lab get configmap unrelated-sentinel >/dev/null
hk -n source-dev get deployment echo >/dev/null
# Exercise actual full-workflow TTL and persistent control-plane PVC cleanup.
bin/replicove create ttl --grant source-dev-lab --ttl 5m --replication-file test/e2e/replication.yaml
hk -n replica-lab wait clusterreplica/ttl --for=condition=Ready --timeout=180s
hk -n replica-lab get clusterreplica ttl -o json > "$work/artifacts/ttl-before.json"
hk -n replica-lab wait clusterreplica/ttl --for=jsonpath='{.status.phase}'=Expired --timeout=420s
hk -n replica-lab annotate clusterreplica ttl e2e-after-expiry=yes
sleep 15
[[ -z "$(hk -n replica-lab get pods,services,secrets,persistentvolumeclaims -o name)" ]]
[[ -z "$(hk -n replicove-system get secret -l app.kubernetes.io/managed-by=replicove -o name)" ]]
hk -n replica-lab get clusterreplica ttl -o json > "$work/artifacts/ttl-after.json"
cat > "$work/artifacts/report.json" <<JSON
{"result":"passed","scenarios":["embedded-installer","manual-plan","real-vcluster","source-helm-reconstruction","secret-snapshot-follow","namespace-mapping","overrides","guest-workload-service","restart","explicit-refresh","viewer-rbac","access-revocation","owned-cleanup","source-preservation","durable-control-plane-reschedule","full-workflow-ttl","control-plane-pvc-cleanup"]}
JSON
