#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export E2E_REPO="$PWD"
export PATH="$PWD/.cache/e2e-tools:$PATH"
for command in docker kind kubectl vcluster python3; do
  command -v "$command" >/dev/null || { echo "Missing $command; see docs/testing.md" >&2; exit 1; }
done
docker info >/dev/null
mkdir -p .cache/e2e
work_dir=$(mktemp -d "$PWD/.cache/e2e/run.XXXXXX")
export E2E_RESULTS="$work_dir/artifacts"
mkdir -p "$E2E_RESULTS"
# A separate credential file and explicit context keep every command away from
# the caller's existing clusters and kubeconfig.
export KUBECONFIG="$work_dir/host.kubeconfig"
cluster="cr-e2e-$(date +%s)-$$"
namespace=replica-lab
node_image='kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed'
created=false
hk() { kubectl --kubeconfig "$work_dir/host.kubeconfig" --context "kind-$cluster" "$@"; }

inventory() {
  hk -n "$namespace" get pods,configmaps,secrets,services,serviceaccounts,persistentvolumeclaims,deployments,statefulsets,replicasets,jobs,roles,rolebindings,leases,networkpolicies,ingresses,resourcequotas,limitranges,poddisruptionbudgets,clusterreplicas -o json \
    | python3 test/e2e/inventory.py > "$E2E_RESULTS/$1.json"
}
cleanup() {
  result=$?
  trap - EXIT
  if [[ "$created" == true ]]; then
    inventory final-inventory || true
    # Only this test's operator emits logs; never dump kubeconfigs, Secrets,
    # Helm release storage, arbitrary pod specs, or vCluster credential files.
    hk -n "$namespace" logs deployment/cluster-replica --tail=200 > "$E2E_RESULTS/operator.log" 2>&1 || true
    kind delete cluster --name "$cluster" || true
  fi
  rm -f "$work_dir/host.kubeconfig" "$work_dir/vcluster.json"
  echo "Sanitized test evidence: $E2E_RESULTS"
  exit "$result"
}
trap cleanup EXIT

echo 'Building the actual operator container image.'
docker build --tag cluster-replica:e2e .
echo "Creating disposable cluster $cluster."
# This generated name cannot target an existing cluster; only mark ownership
# after checking it is unused. Cleanup also handles partial kind creation.
if kind get clusters | grep -Fxq "$cluster"; then echo 'Test cluster name collision' >&2; exit 1; fi
created=true
kind create cluster --name "$cluster" --image "$node_image" --kubeconfig "$KUBECONFIG" --wait 180s
kind load docker-image cluster-replica:e2e --name "$cluster"
hk version --output=json > "$E2E_RESULTS/host-version.json"
python3 - "$E2E_RESULTS/host-version.json" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))['serverVersion']['gitVersion'] == 'v1.36.4'
PY
hk api-resources -o wide > "$E2E_RESULTS/host-apis.txt"
hk apply -f config/crd/replica.nimeshbuilds.dev_clusterreplicas.yaml
hk wait --for=condition=Established crd/clusterreplicas.replica.nimeshbuilds.dev --timeout=60s
hk apply -f config/rbac/lab.yaml
sed 's|YOUR_REGISTRY/cluster-replica:dev|cluster-replica:e2e|' config/manager/deployment.yaml | hk apply -f -
hk -n "$namespace" rollout status deployment/cluster-replica --timeout=120s
hk -n "$namespace" create configmap unrelated-sentinel --from-literal=keep=yes
inventory baseline
vcluster telemetry disable --config "$work_dir/vcluster.json"

create_replica() {
  cat <<YAML | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ClusterReplica
metadata:
  name: $1
  namespace: $namespace
spec:
  profile: vcluster-0.37.1-lab
  ttl: $2
  cleanupPolicy: HelmReleaseOnly
YAML
}
reference() { hk -n "$namespace" get clusterreplica "$1" -o jsonpath='{.status.runtime.releaseName}'; }
connect_guest() {
  vcluster connect "$1" --namespace "$namespace" --context "kind-$cluster" --driver helm \
    --config "$work_dir/vcluster.json" --background-proxy=false -- bash "$E2E_REPO/test/e2e/guest.sh" "$2"
}
assert_release_absent() {
  local release="$1" owner="$2"
  [[ -z "$(hk -n "$namespace" get deployment -l "replica.nimeshbuilds.dev/uid=$owner" -o name)" ]]
  [[ -z "$(hk -n "$namespace" get secret -l "owner=helm,name=$release" -o name)" ]]
  # All chart manifest resources bear Helm's release annotation. Dynamic guest
  # resources may remain and are reported separately, never silently discarded.
  hk -n "$namespace" get services,secrets,configmaps,serviceaccounts,roles,rolebindings,deployments,limitranges,resourcequotas,poddisruptionbudgets,networkpolicies -o json \
    | python3 -c 'import json,sys; release=sys.argv[1]; found=[(x["kind"],x["metadata"]["name"]) for x in json.load(sys.stdin)["items"] if x["metadata"].get("annotations",{}).get("meta.helm.sh/release-name")==release]; assert not found, found' "$release"
  hk -n "$namespace" get configmap unrelated-sentinel >/dev/null
  hk get namespace "$namespace" >/dev/null
}

echo 'Testing a real vCluster, guest workload, and operator restart.'
create_replica ttl 8m
hk -n "$namespace" wait clusterreplica/ttl --for=condition=RuntimeReady --timeout=300s
release=$(reference ttl)
owner=$(hk -n "$namespace" get clusterreplica ttl -o jsonpath='{.metadata.uid}')
expires=$(hk -n "$namespace" get clusterreplica ttl -o jsonpath='{.status.expiresAt}')
hk -n "$namespace" get clusterreplica ttl -o json > "$E2E_RESULTS/ttl-before.json"
connect_guest "$release" create
inventory before-restart
hk -n "$namespace" rollout restart deployment/cluster-replica
hk -n "$namespace" rollout status deployment/cluster-replica --timeout=120s
[[ "$(reference ttl)" == "$release" ]]
[[ "$(hk -n "$namespace" get clusterreplica ttl -o jsonpath='{.status.expiresAt}')" == "$expires" ]]
connect_guest "$release" check
inventory before-expiry

echo 'Waiting for the actual creation-time TTL (no mocked clock or patched status).'
hk -n "$namespace" wait clusterreplica/ttl --for=jsonpath='{.status.phase}'=Expired --timeout=600s
hk -n "$namespace" get clusterreplica ttl -o json > "$E2E_RESULTS/ttl-after.json"
python3 - "$E2E_RESULTS/ttl-after.json" <<'PY'
import datetime, json, sys
obj=json.load(open(sys.argv[1]))
expiry=datetime.datetime.fromisoformat(obj['status']['expiresAt'].replace('Z','+00:00'))
assert datetime.datetime.now(datetime.timezone.utc) >= expiry, 'expired prematurely'
PY
assert_release_absent "$release" "$owner"
inventory after-expiry
# At least two reconcile periods must pass without recreating the release.
sleep 35
assert_release_absent "$release" "$owner"

echo 'Testing explicit deletion of another running vCluster.'
create_replica manual 10m
hk -n "$namespace" wait clusterreplica/manual --for=condition=RuntimeReady --timeout=180s
manual_release=$(reference manual)
manual_owner=$(hk -n "$namespace" get clusterreplica manual -o jsonpath='{.metadata.uid}')
[[ "$manual_release" != "$release" ]]
hk -n "$namespace" delete clusterreplica manual --timeout=180s
assert_release_absent "$manual_release" "$manual_owner"
inventory after-manual-delete

echo 'Testing a failed installation and cleanup of partial Helm resources.'
cat <<YAML | hk apply -f -
apiVersion: v1
kind: ResourceQuota
metadata:
  name: block-vcluster-deployment
  namespace: $namespace
spec:
  hard:
    count/deployments.apps: "1"
YAML
create_replica rejected-install 5m
hk -n "$namespace" wait clusterreplica/rejected-install --for=jsonpath='{.status.phase}'=Blocked --timeout=120s
failed_release=$(reference rejected-install)
failed_owner=$(hk -n "$namespace" get clusterreplica rejected-install -o jsonpath='{.metadata.uid}')
hk -n "$namespace" get clusterreplica rejected-install -o json > "$E2E_RESULTS/failed-install.json"
hk -n "$namespace" delete clusterreplica rejected-install --timeout=180s
assert_release_absent "$failed_release" "$failed_owner"
hk -n "$namespace" get resourcequota block-vcluster-deployment >/dev/null
inventory after-failed-install

python3 - "$E2E_RESULTS" <<'PY'
import json, pathlib, sys
p=pathlib.Path(sys.argv[1])
baseline={x['uid'] for x in json.loads((p/'baseline.json').read_text())}
remaining=[x for x in json.loads((p/'after-expiry.json').read_text()) if x['uid'] not in baseline]
host=json.loads((p/'host-version.json').read_text())['serverVersion']['gitVersion']
guest=json.loads((p/'guest-version.json').read_text())['serverVersion']['gitVersion']
runtime=json.loads((p/'ttl-before.json').read_text())['status']['runtime']
report={'result':'passed', 'hostKubernetesVersion':host, 'guestKubernetesVersion':guest, 'runtime':runtime, 'cleanupPolicy':'HelmReleaseOnly', 'checks':['real image build and service-account deployment','runtime readiness','guest API connection','guest Deployment and HTTP/DNS probe','operator restart preserves runtime and TTL','real TTL expiry and no resurrection','explicit deletion','failed installation and partial cleanup','unrelated host resource preservation'], 'resourcesNotInBaselineAfterTTL':remaining, 'inventoryNote':'Includes the retained ClusterReplica record and replacement operator pods as well as any guest leftovers; this is not yet an ownership classification.', 'fullDataCleanupVerified':False}
(p/'report.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report,indent=2))
PY
echo 'Live vCluster lifecycle checks passed; leftover inventory remains part of the report.'
