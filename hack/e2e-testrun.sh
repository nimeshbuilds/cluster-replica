#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/testrun-e2e
work=$(mktemp -d "$PWD/.cache/testrun-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-testrun-$(date +%s)-$$"
created=false
hk(){ kubectl --kubeconfig "$KUBECONFIG" --context "kind-$cluster" "$@"; }
cleanup(){
 result=$?; trap - EXIT
 if [[ "$created" == true ]]; then
  hk -n replicove-system logs deployment/replicove --tail=200 > "$work/artifacts/operator.log" 2>&1 || true
  hk -n replica-lab get clusterreplicas,replicaaccesses,replicaexperiments -o json | python3 test/e2e/inventory.py > "$work/artifacts/request-inventory.json" || true
  hk -n replica-lab get pods,services,secrets,persistentvolumeclaims,statefulsets -o json | python3 test/e2e/inventory.py > "$work/artifacts/host-inventory.json" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig"
 echo "Test runner evidence: $work/artifacts"
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
curl -fsSL --retry 3 'https://raw.githubusercontent.com/projectcalico/calico/db255c554b929afd73552fd3ac81d691107a1607/manifests/calico.yaml' -o "$work/calico.yaml"
hk apply -f "$work/calico.yaml"
hk -n kube-system rollout status daemonset/calico-node --timeout=300s
hk wait nodes --all --for=condition=Ready --timeout=120s
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then kind load docker-image cluster-replica:e2e --name "$cluster"; fi
hk create namespace source-dev
if [[ "${REPLICOVE_USE_RELEASE_CLI:-false}" != true ]]; then make build; fi
source test/e2e/helm.sh
replicove_helm_install test/chaos/values.yaml
hk -n replicove-system rollout status deployment/replicove --timeout=180s
hk apply -f test/chaos/source.yaml -f test/chaos/grant.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
source_uid=$(hk -n source-dev get deployment echo -o jsonpath='{.metadata.uid}')
host_hash=$(sha256sum "$KUBECONFIG" | cut -d ' ' -f 1)
python3 - "$work" <<'PY'
import copy, json, pathlib, sys
root=pathlib.Path(sys.argv[1])
base={"apiVersion":"replicove.nimeshbuilds.dev/v1alpha1","kind":"TestRecipe","replica":{"profile":"vcluster-0.37.1-persistent","ttl":"45m","cleanupPolicy":"DeleteOwned","approval":"Automatic","grantRef":"chaos-lab","replication":{"namespaces":["source-dev"],"namespaceMap":{"source-dev":"integration"},"secrets":"None","data":"None"}},"execution":{"timeout":"30s","role":"admin","credentialSeconds":900},"lifecycle":{"readyTimeout":"15m","cleanupTimeout":"10m"}}
for name in ["success","failure","timeout","retained"]:
 recipe=copy.deepcopy(base);recipe['namePrefix']=name
 if name=='success':
  recipe['chaos']={'durationSeconds':30,'faults':[{'kind':'ScaleZero','namespace':'integration','target':{'kind':'Deployment','name':'echo'}}]}
  recipe['execution']['command']=['sh','-c','test "$(kubectl --namespace integration get deployment echo -o jsonpath=\'{.spec.replicas}\')" = 0']
 elif name=='timeout':
  recipe['execution']['timeout']='2s';recipe['execution']['command']=['python3','-c','import time; time.sleep(60)']
 else:
  recipe['execution']['command']=['sh','-c','exit 17']
  recipe['lifecycle']['keepOnFailure']=name=='retained'
 (root/(name+'.json')).write_text(json.dumps(recipe))
PY
for scenario in success failure timeout retained; do
 set +e
 bin/replicove run -f "$work/$scenario.json" --artifacts "$work/artifacts/$scenario" > "$work/$scenario-output.txt" 2>&1
 result=$?
 set -e
 python3 - "$work/artifacts/$scenario/report.json" "$scenario" "$result" <<'PY'
import json,sys
r=json.load(open(sys.argv[1]));scenario=sys.argv[2];code=int(sys.argv[3])
assert r['setup']['status']=='Passed', r
assert r['request']['uid']==r['replica']['uid']
assert r['planRevision'] and r['capturedAt'] and r['runtime']['chartSHA256']
assert r['cleanup']['status']==('Retained' if scenario=='retained' else 'Verified'),r
assert r['test']['status']=={'success':'Passed','failure':'Failed','timeout':'Interrupted','retained':'Failed'}[scenario],r
assert (code==0)==(scenario=='success'),r
if scenario=='success':
 assert r['experiment']['uid'] and r['chaosFaults'][0]['target']['uid']
if scenario in ('failure','retained'): assert r['test']['exitCode']==17
PY
 [[ "$(sha256sum "$KUBECONFIG" | cut -d ' ' -f 1)" == "$host_hash" ]]
 [[ "$(hk -n source-dev get deployment echo -o jsonpath='{.metadata.uid}')" == "$source_uid" ]]
 [[ "$(hk -n source-dev get deployment echo -o jsonpath='{.spec.replicas}')" == 1 ]]
 [[ "$(hk -n replica-lab get replicaaccesses,replicaexperiments -o jsonpath='{.items}')" == '[]' ]]
 if [[ "$scenario" == retained ]]; then
  request=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["request"]["name"])' "$work/artifacts/$scenario/report.json")
  hk -n replica-lab get clusterreplica "$request" -o json > "$work/retained-request.json"
  python3 - "$work/artifacts/$scenario/report.json" "$work/retained-request.json" <<'PY'
import datetime,json,sys
r=json.load(open(sys.argv[1]));obj=json.load(open(sys.argv[2]))
assert obj['metadata']['uid']==r['request']['uid'] and obj['spec']['ttl']=='45m'
created=datetime.datetime.fromisoformat(obj['metadata']['creationTimestamp'].replace('Z','+00:00'))
retained=datetime.datetime.fromisoformat(r['retainedUntil'].replace('Z','+00:00'))
assert retained-created==datetime.timedelta(minutes=45)
PY
  bin/replicove delete "$request"
  hk -n replica-lab wait "clusterreplica/$request" --for=delete --timeout=600s
 fi
 [[ "$(hk -n replica-lab get clusterreplicas -o jsonpath='{.items}')" == '[]' ]]
 [[ "$(hk -n replica-lab get pods,statefulsets,persistentvolumeclaims -o jsonpath='{.items}')" == '[]' ]]
done
python3 - "$work/artifacts" <<'PY'
import json,pathlib,sys
root=pathlib.Path(sys.argv[1])
out={'suite':'testrun','passed':['successful-command-and-chaos-rollback','command-failure-cleanup','timeout-cleanup','bounded-retention-and-explicit-delete','host-kubeconfig-unchanged','source-workload-unchanged','guest-credential-revocation','no-owned-pods-or-pvcs-remain']}
(root/'report.json').write_text(json.dumps(out,indent=2)+'\n')
PY
