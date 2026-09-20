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
  hk -n replica-lab get clusterreplica full -o jsonpath='{.status}' > "$work/artifacts/status.json" || true
  hk -n replica-lab get clusterreplicas,replicaaccesses -o json | python3 test/e2e/inventory.py > "$work/artifacts/requests.json" || true
  hk -n replica-lab get pods,services,secrets,persistentvolumeclaims,deployments -o json | python3 test/e2e/inventory.py > "$work/artifacts/host-inventory.json" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig" "$work/guest-access.kubeconfig" "$work/existing.kubeconfig"
 echo "Sanitized replica evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
node_image="${REPLICOVE_KIND_IMAGE:-kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed}"
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then docker build --tag cluster-replica:e2e .; fi
if kind get clusters | grep -Fxq "$cluster";then exit 1;fi
created=true
kind create cluster --name "$cluster" --image "$node_image" --kubeconfig "$KUBECONFIG" --wait 180s
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then kind load docker-image cluster-replica:e2e --name "$cluster"; fi
hk create namespace source-dev
if [[ "${REPLICOVE_USE_RELEASE_CLI:-false}" != true ]]; then make build; fi
if [[ "${REPLICOVE_INSTALLER:-cli}" == helm ]]; then
 source test/e2e/helm.sh
 replicove_helm_install test/e2e/replicove-values.yaml
else
 bin/replicove install --image "${REPLICOVE_IMAGE:-cluster-replica:e2e}" --values test/e2e/replicove-values.yaml
fi
hk -n replicove-system rollout status deployment/replicove --timeout=180s
# Verify the installed operator identity's host authorization in a real cluster.
operator_identity=system:serviceaccount:replicove-system:replicove
[[ "$(hk --as="$operator_identity" -n source-dev auth can-i list configmaps)" == yes ]]
expect_forbidden(){
 echo "Checking rejected operation: $*"
 if "$@" > "$work/denial.txt" 2>&1; then
  echo 'Expected Kubernetes to reject the unauthorized operation' >&2; exit 1
 fi
 # kubectl create wraps API errors with a lowercase "is forbidden:" message;
 # kubectl get uses "(Forbidden)". Other failures must not satisfy this check.
 if ! grep -Eq '\(Forbidden\)| is forbidden:' "$work/denial.txt"; then
  cat "$work/denial.txt" >&2
  exit 1
 fi
}
expect_forbidden hk --as="$operator_identity" -n source-dev create configmap forbidden --from-literal=x=y
expect_forbidden hk --as="$operator_identity" -n kube-system get secrets
expect_forbidden hk --as="$operator_identity" create namespace forbidden
expect_forbidden hk --as="$operator_identity" -n replica-lab create role forbidden --verb='*' --resource='*'
expect_forbidden hk --as="$operator_identity" -n replica-lab create rolebinding forbidden --clusterrole=cluster-admin --user=fixture-reader
hk apply -f test/e2e/source.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
go run ./test/e2e/seed test/e2e/chart source-dev fixture
hk apply -f test/e2e/grant.yaml
hk -n replica-lab create configmap unrelated-sentinel --from-literal=keep=yes
bin/replicove create full --grant source-dev-lab --ttl 30m --manual --replication-file test/e2e/replication.yaml
hk -n replica-lab wait clusterreplica/full --for=jsonpath='{.status.phase}'=AwaitingApproval --timeout=120s
[[ -z "$(hk -n replica-lab get deployment,statefulset -o name)" ]]
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
gk -n integration run probe --image=registry.k8s.io/e2e-test-images/busybox:1.37.0-1 --restart=Never --command -- sh -c 'wget -qO- http://echo:8080/hostname'
gk -n integration wait pod/probe --for=jsonpath='{.status.phase}'=Succeeded --timeout=120s
# Experiments must survive a controller restart and ordinary reconciliation.
gk -n integration annotate configmap settings experiment=keep
runtime_uid=$(hk -n replica-lab get clusterreplica full -o jsonpath='{.status.runtime.releaseName}')
if [[ "${REPLICOVE_INSTALLER:-cli}" == helm ]]; then
 key_uid=$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')
 destination_uid=$(hk get namespace replica-lab -o jsonpath='{.metadata.uid}')
 replicove_helm_install test/e2e/replicove-values.yaml
 [[ "$key_uid" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
 [[ "$destination_uid" == "$(hk get namespace replica-lab -o jsonpath='{.metadata.uid}')" ]]
fi
hk -n replicove-system rollout restart deployment/replicove
hk -n replicove-system rollout status deployment/replicove --timeout=120s
sleep 15
[[ "$(gk -n integration get configmap settings -o jsonpath='{.metadata.annotations.experiment}')" == keep ]]
[[ "$(hk -n replica-lab get clusterreplica full -o jsonpath='{.status.runtime.releaseName}')" == "$runtime_uid" ]]
# The durable profile must survive rescheduling its own control-plane pod.
guest_cluster_uid=$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')
guest_workload_uid=$(gk -n integration get deployment echo -o jsonpath='{.metadata.uid}')
hk -n replica-lab delete pod "$runtime_uid-0" --wait=true
hk -n replica-lab rollout status statefulset/"$runtime_uid" --timeout=180s
# The existing CLI process must recover on the same loopback port and file.
for i in $(seq 1 120);do
 if gk --request-timeout=2s get namespace kube-system >/dev/null 2>&1;then break;fi
 sleep 1
done
[[ "$i" -lt 120 ]]
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
viewer_secret=$(hk -n replica-lab get replicaaccesses -o json | python3 -c 'import json,sys;items=[a for a in json.load(sys.stdin)["items"] if a["spec"]["role"]=="viewer"];assert len(items)==1;print(items[0]["status"]["credentialSecret"])')
[[ "$(hk --as=replica-developer -n replica-lab auth can-i get "secret/$viewer_secret")" == yes ]]
if hk --as=replica-developer -n replica-lab auth can-i get "secret/vc-$runtime_uid";then exit 1;fi
if hk --as=replica-developer -n replica-lab auth can-i list secrets;then exit 1;fi
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
# Register this disposable runtime as an externally managed target for a second
# request. Only the original full request owns its runtime lifecycle.
gk create namespace retained
gk -n retained create configmap settings --from-literal=foreign=preserve
foreign_uid=$(gk -n retained get configmap settings -o jsonpath='{.metadata.uid}')
external_runtime_uid=$(hk -n replica-lab get statefulset "$runtime_uid" -o jsonpath='{.metadata.uid}')
hk -n replica-lab get secret "vc-$runtime_uid" -o json | python3 -c 'import base64,json,sys;open(sys.argv[1],"wb").write(base64.b64decode(json.load(sys.stdin)["data"]["config"]))' "$work/existing.kubeconfig"
python3 - "$work/existing.kubeconfig" "$runtime_uid" <<'PYEXISTING'
import json,subprocess,sys
path,release=sys.argv[1:]
config=json.loads(subprocess.check_output(['kubectl','--kubeconfig',path,'config','view','--raw','-o','json']))
config['clusters'][0]['cluster']['server']='https://'+release+'.replica-lab.svc:443'
config['clusters'][0]['cluster']['tls-server-name']=release+'.replica-lab'
with open(path,'w') as f:json.dump(config,f)
PYEXISTING
hk -n replicove-system create secret generic existing-runtime --from-file="config=$work/existing.kubeconfig"
rm -f "$work/existing.kubeconfig"
cat <<YAML | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaGrant
metadata:
  name: existing-lab
spec:
  targetNamespace: replica-lab
  sourceNamespaces: [source-dev]
  resources:
    - {group: "", kind: ConfigMap}
  existingTargets:
    - name: retained
      kubeconfigSecret: {namespace: replicove-system, name: existing-runtime}
      clusterUID: "$guest_cluster_uid"
YAML
cat > "$work/existing-selection.yaml" <<YAML
namespaceMap: {source-dev: retained}
include:
  - names: [settings]
YAML
bin/replicove create conflicting --grant existing-lab --existing retained --ttl 15m --replication-file "$work/existing-selection.yaml"
hk -n replica-lab wait clusterreplica/conflicting --for=jsonpath='{.status.phase}'=Blocked --timeout=120s
[[ "$(hk -n replica-lab get clusterreplica conflicting -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}')" == OwnershipConflict ]]
bin/replicove delete conflicting
hk -n replica-lab wait clusterreplica/conflicting --for=delete --timeout=120s
[[ "$(gk -n retained get configmap settings -o jsonpath='{.metadata.uid}')" == "$foreign_uid" ]]
cat > "$work/existing-selection.yaml" <<YAML
namespaceMap: {source-dev: retained}
include:
  - names: [chart-settings]
YAML
bin/replicove create borrowed --grant existing-lab --existing retained --ttl 15m --replication-file "$work/existing-selection.yaml"
hk -n replica-lab wait clusterreplica/borrowed --for=condition=Ready --timeout=120s
[[ "$(gk -n retained get configmap chart-settings -o jsonpath='{.data.message}')" == source-chart ]]
bin/replicove delete borrowed
hk -n replica-lab wait clusterreplica/borrowed --for=delete --timeout=120s
[[ -z "$(gk -n retained get configmap chart-settings --ignore-not-found -o name)" ]]
[[ "$(gk -n retained get configmap settings -o jsonpath='{.metadata.uid}')" == "$foreign_uid" ]]
[[ "$(hk -n replica-lab get statefulset "$runtime_uid" -o jsonpath='{.metadata.uid}')" == "$external_runtime_uid" ]]
hk -n replicove-system delete secret existing-runtime
hk delete replicagrant existing-lab
bin/replicove status full > "$work/artifacts/ready.json"
# Stop the local proxy before deleting the guest. The access controller still
# revokes both guest identities and their host credential Secrets.
kill "$tunnel_pid" 2>/dev/null || true;wait "$tunnel_pid" || true;tunnel_pid=''
bin/replicove delete full
hk -n replica-lab wait clusterreplica/full --for=delete --timeout=300s
if hk --as=replica-developer -n replica-lab auth can-i get "secret/$viewer_secret";then exit 1;fi
[[ -z "$(hk -n replica-lab get role "$viewer_secret" --ignore-not-found -o name)" ]]
[[ -z "$(hk -n replica-lab get rolebinding "$viewer_secret" --ignore-not-found -o name)" ]]
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
if [[ "${REPLICOVE_INSTALLER:-cli}" == helm ]]; then
 bin/replicove delete ttl
 hk -n replica-lab wait clusterreplica/ttl --for=delete --timeout=60s
 helm uninstall replicove --namespace replicove-system --wait --timeout 2m
 [[ "$key_uid" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
 [[ "$destination_uid" == "$(hk get namespace replica-lab -o jsonpath='{.metadata.uid}')" ]]
 hk -n replica-lab get configmap unrelated-sentinel >/dev/null
 replicove_helm_install test/e2e/replicove-values.yaml
 [[ "$key_uid" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
fi
cat > "$work/artifacts/report.json" <<JSON
{"result":"passed","scenarios":["${REPLICOVE_INSTALLER:-cli}-installer","operator-namespace-rbac","escalation-bind-denied","manual-plan","real-vcluster","source-helm-reconstruction","secret-snapshot-follow","namespace-mapping","overrides","guest-workload-service","restart","explicit-refresh","viewer-rbac","access-revocation","owned-cleanup","source-preservation","durable-control-plane-reschedule","full-workflow-ttl","control-plane-pvc-cleanup","existing-target-preservation","existing-target-conflict","tunnel-reconnect","exact-secret-reader-rbac"]}
JSON
