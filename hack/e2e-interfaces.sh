#!/usr/bin/env bash
# Disposable real-vCluster qualification for pool, diagnostics, MCP and dashboard labs.
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/interfaces-e2e
work=$(mktemp -d "$PWD/.cache/interfaces-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-interfaces-$(date +%s)-$$"
created=false
hk(){ kubectl --kubeconfig "$KUBECONFIG" --context "kind-$cluster" "$@"; }
cleanup(){
 result=$?; trap - EXIT
 if [[ "$created" == true ]]; then
  for member in a b; do
   hk -n "replicove-$member" logs deployment/replicove --tail=200 > "$work/artifacts/operator-$member.log" 2>&1 || true
   hk -n "test-$member" get clusterreplicas,replicaaccesses,replicaexperiments,pods,services,secrets,persistentvolumeclaims,statefulsets -o json | python3 test/e2e/inventory.py > "$work/artifacts/inventory-$member.json" || true
  done
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/agent.kubeconfig"
 echo "Pool and live interface evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then docker build --tag cluster-replica:e2e .; fi
if [[ "${REPLICOVE_USE_RELEASE_CLI:-false}" != true ]]; then make build; fi
# Native pool render uses the CLI's embedded chart. A supplied published chart
# is checked for version parity; the pool is never converted to Helm ownership.
if [[ -n "${REPLICOVE_CHART:-}" ]]; then
 chart_args=()
 if [[ -n "${REPLICOVE_CHART_VERSION:-}" ]]; then chart_args+=(--version "$REPLICOVE_CHART_VERSION"); fi
 helm show chart "$REPLICOVE_CHART" "${chart_args[@]}" > "$work/chart-metadata.yaml"
 python3 - "$work/chart-metadata.yaml" "${REPLICOVE_CHART_VERSION:-}" "$(bin/replicove --version)" <<'PY'
import pathlib,sys
metadata={key.strip():value.strip().strip('"\'') for line in pathlib.Path(sys.argv[1]).read_text().splitlines()
          if not line.startswith(' ') and ':' in line for key,value in [line.split(':',1)]}
assert metadata['name']=='replicove'
if sys.argv[2]:
 assert metadata['version']==sys.argv[2]
 assert sys.argv[3].split()[-1].removeprefix('v')==sys.argv[2]
PY
fi
if kind get clusters | grep -Fxq "$cluster"; then exit 1; fi
created=true
kind create cluster --name "$cluster" --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed --kubeconfig "$KUBECONFIG" --wait 180s
if [[ -z "${REPLICOVE_IMAGE:-}" ]]; then kind load docker-image cluster-replica:e2e --name "$cluster"; fi
hk create namespace source-dev
hk apply -f test/chaos/source.yaml
hk -n source-dev create configmap interface-settings --from-literal=mode=interfaces-private-payload
hk -n source-dev rollout status deployment/echo --timeout=180s
hk -n source-dev get deployment/echo configmap/interface-settings -o json > "$work/source-before.json"
python3 - "$work/pool.json" "${REPLICOVE_IMAGE:-cluster-replica:e2e}" <<'PY'
import json,pathlib,sys
pool=json.loads(pathlib.Path('test/scenarios/interfaces/pool.json').read_text())
image=sys.argv[2]
if '@sha256:' in image:
 repository,digest=image.split('@',1); image_values={'repository':repository,'digest':digest,'tag':''}
else:
 repository,tag=image.rsplit(':',1); image_values={'repository':repository,'tag':tag,'digest':''}
for member in pool['members']: member['values']['image']=image_values
pathlib.Path(sys.argv[1]).write_text(json.dumps(pool))
PY
bin/replicove pool render -f "$work/pool.json" > "$work/pool-install.yaml"
hk apply -f "$work/pool-install.yaml"
hk wait --for=condition=Established --timeout=60s crd/clusterreplicas.replica.nimeshbuilds.dev crd/replicagrants.replica.nimeshbuilds.dev crd/replicaaccesses.replica.nimeshbuilds.dev
for member in a b; do
 hk -n "replicove-$member" wait job/replicove-bootstrap --for=condition=Complete --timeout=180s
 hk -n "replicove-$member" rollout status deployment/replicove --timeout=180s
 hk -n "test-$member" get resourcequota/replicove-capacity limitrange/replicove-defaults >/dev/null
done
hk apply -f test/scenarios/interfaces/grants.yaml
host_hash=$(shasum -a 256 "$KUBECONFIG" | cut -d ' ' -f 1)
bin/replicove --namespace test-a create interfaces-ready --grant source-dev-a --ttl 1h --replication-file test/scenarios/interfaces/replication.json
hk -n test-a wait clusterreplica/interfaces-ready --for=condition=Ready --timeout=900s
bin/replicove --namespace test-a doctor --grant source-dev-a --replica interfaces-ready --output json > "$work/artifacts/doctor.json"
bin/replicove --namespace test-a plan interfaces-ready --json > "$work/artifacts/plan.json"

# This oversized Pod is rejected by real quota admission, including dry-run.
# A failed admission creates no workload and is not a simulated quota check.
cat > "$work/over-quota.json" <<'JSON'
{"apiVersion":"v1","kind":"Pod","metadata":{"name":"over-quota","namespace":"test-a"},"spec":{"containers":[{"name":"too-large","image":"registry.k8s.io/e2e-test-images/busybox:1.37.0-1","resources":{"requests":{"cpu":"5","memory":"16Mi"},"limits":{"cpu":"5","memory":"16Mi"}}}]}}
JSON
if hk create --dry-run=server -f "$work/over-quota.json" > "$work/quota-result.txt" 2>&1; then
 echo 'Expected host ResourceQuota admission to reject the oversized Pod' >&2; exit 1
fi
grep -Fq 'exceeded quota' "$work/quota-result.txt"
[[ -z "$(hk -n test-a get pod over-quota --ignore-not-found -o name)" ]]

# Exercises a real ServiceAccount, actual stdio protocol, and loopback HTTP.
# It creates interfaces-queued using MCP, without reading guest credentials.
python3 test/scenarios/interfaces/check.py --work "$work" --binary bin/replicove --context "kind-$cluster"
hk -n test-a wait clusterreplica/interfaces-queued --for=jsonpath='{.status.phase}'=Queued --timeout=90s
ledger_uid=$(hk -n replicove-a get secrets -l replicove.nimeshbuilds.dev/state-kind=capacity -o jsonpath='{.items[0].metadata.uid}')
[[ -n "$ledger_uid" ]]
ready_runtime=$(hk -n test-a get clusterreplica interfaces-ready -o jsonpath='{.status.runtime.releaseName}')
[[ -n "$ready_runtime" ]]
hk -n replicove-a rollout restart deployment/replicove
hk -n replicove-a rollout status deployment/replicove --timeout=180s
hk -n test-a wait clusterreplica/interfaces-queued --for=jsonpath='{.status.phase}'=Queued --timeout=90s
[[ "$(hk -n replicove-a get secrets -l replicove.nimeshbuilds.dev/state-kind=capacity -o jsonpath='{.items[0].metadata.uid}')" == "$ledger_uid" ]]
[[ "$(hk -n test-a get clusterreplica interfaces-ready -o jsonpath='{.status.runtime.releaseName}')" == "$ready_runtime" ]]
queued_runtime=$(hk -n test-a get clusterreplica interfaces-queued -o jsonpath='{.status.runtime.releaseName}')
[[ -n "$queued_runtime" && "$queued_runtime" != "$ready_runtime" ]]
[[ -z "$(hk -n test-a get statefulset "$queued_runtime" --ignore-not-found -o name)" ]]
[[ -z "$(hk -n test-a get service "$queued_runtime" --ignore-not-found -o name)" ]]
[[ "$(hk -n test-a get statefulsets -o json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["items"]))')" == 1 ]]

# Pool selection sees two occupied requests in A and selects independent B.
bin/replicove run -f test/scenarios/interfaces/recipe.yaml --artifacts "$work/artifacts/pool-run" > "$work/pool-run.log" 2>&1
python3 - "$work/artifacts/pool-run/report.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1]))
assert r['namespace']=='test-b', r
assert r['setup']['status']==r['test']['status']=='Passed', r
assert r['cleanup']['status']=='Verified', r
assert r['planRevision'] and r['runtime']['chartSHA256'], r
PY
[[ "$(hk -n test-b get clusterreplicas,replicaaccesses,pods,statefulsets,persistentvolumeclaims -o jsonpath='{.items}')" == '[]' ]]

# Releasing A's exact owner lets the queued immutable request acquire its slot.
bin/replicove --namespace test-a delete interfaces-ready
hk -n test-a wait clusterreplica/interfaces-ready --for=delete --timeout=600s
hk -n test-a wait clusterreplica/interfaces-queued --for=condition=Ready --timeout=900s
[[ "$(hk -n test-a get clusterreplica interfaces-queued -o jsonpath='{.status.runtime.releaseName}')" != "$ready_runtime" ]]
bin/replicove --namespace test-a delete interfaces-queued
hk -n test-a wait clusterreplica/interfaces-queued --for=delete --timeout=600s
for member in a b; do
 [[ "$(hk -n "test-$member" get clusterreplicas,replicaaccesses,pods,statefulsets,persistentvolumeclaims -o jsonpath='{.items}')" == '[]' ]]
 hk -n "test-$member" get resourcequota/replicove-capacity limitrange/replicove-defaults >/dev/null
 hk -n "replicove-$member" get secret/replicove-state-key >/dev/null
done
[[ "$(hk get persistentvolumes -o jsonpath='{.items}')" == '[]' ]]
[[ "$(shasum -a 256 "$KUBECONFIG" | cut -d ' ' -f 1)" == "$host_hash" ]]
hk -n source-dev get deployment/echo configmap/interface-settings -o json > "$work/source-after.json"
python3 - "$work" <<'PY'
import json,pathlib,sys
root=pathlib.Path(sys.argv[1])
before=json.loads((root/'source-before.json').read_text())['items']
after=json.loads((root/'source-after.json').read_text())['items']
def important(items):
 return {(o['kind'],o['metadata']['name']):{'uid':o['metadata']['uid'],'spec':o.get('spec'),'data':o.get('data')} for o in items}
assert important(before)==important(after), 'source configuration changed'
for o in after:
 if o['kind']=='Deployment': assert o['status']['availableReplicas']==1
interfaces=json.loads((root/'artifacts/interfaces.json').read_text())['passed']
report={'suite':'pool-and-live-interfaces','passed':[
 'native-two-member-pool-render-and-install','independent-protected-state-and-grants','real-host-quota-admission',
 'durable-capacity-queue-survives-operator-restart','least-occupied-pool-selection','pool-run-live-guest-command',
 'capacity-released-only-after-owner-cleanup','queued-request-acquires-slot-and-becomes-ready',
 'both-members-owned-resources-cleaned','administrator-pool-infrastructure-preserved',
 'source-workload-config-and-uid-preserved','host-kubeconfig-unchanged',*interfaces]}
(root/'artifacts/report.json').write_text(json.dumps(report,indent=2)+'\n')
print(f"Verified {len(report['passed'])} pool and live interface assertions")
PY
