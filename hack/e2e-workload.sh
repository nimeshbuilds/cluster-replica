#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
workload="${1:?Select cert-manager, spark, trino, or policy}"
case "$workload" in
 cert-manager) chart=cert-manager-v1.20.4.tgz ;;
 spark) chart=spark-operator-2.5.2.tgz ;;
 trino) chart=trino-1.42.2.tgz ;;
 policy) chart=policy ;;
 *) exit 2 ;;
esac
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/workload-e2e
work=$(mktemp -d "$PWD/.cache/workload-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-workload-$(date +%s)-$$"
created=false
tunnel_pid=''
hk(){ kubectl --kubeconfig "$work/host.kubeconfig" --context "kind-$cluster" "$@"; }
gk(){ kubectl --kubeconfig "$work/guest.kubeconfig" "$@"; }
cleanup(){
 result=$?;trap - EXIT
 if [[ -n "$tunnel_pid" ]];then kill "$tunnel_pid" 2>/dev/null || true;wait "$tunnel_pid" 2>/dev/null || true;fi
 if [[ "$created" == true ]];then
  hk -n replicove-system logs deployment/replicove --tail=200 > "$work/artifacts/operator.log" 2>&1 || true
  # These CR statuses contain only Replicove's deliberately sanitized summaries.
  hk -n replica-lab get clusterreplica workload -o jsonpath='{.status}' > "$work/artifacts/status.json" || true
  hk -n replica-lab get pods,services,secrets,persistentvolumeclaims -o json | python3 test/e2e/inventory.py > "$work/artifacts/inventory.json" || true
  hk -n source-dev get pods,deployments -o json | python3 test/e2e/inventory.py > "$work/artifacts/source-inventory.json" || true
  if [[ "$workload" == spark ]];then
   hk -n source-dev get sparkapplication spark-pi -o jsonpath='{.status.applicationState.state}' > "$work/artifacts/source-spark-state.txt" || true
  fi
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig"
 echo "Sanitized workload evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
python3 hack/fetch-workload-charts.py
WORKLOAD_CHARTS="$PWD/.cache/workload-charts" go test -count=1 -run TestWorkloadChartContracts ./test/workloads
docker build --tag cluster-replica:e2e .
if kind get clusters | grep -Fxq "$cluster";then exit 1;fi
created=true
kind create cluster --name "$cluster" --image 'kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed' --kubeconfig "$KUBECONFIG" --wait 180s
kind load docker-image cluster-replica:e2e --name "$cluster"
hk create namespace source-dev
make build
bin/replicove install --image cluster-replica:e2e --values test/workloads/operator-values.yaml
hk -n replicove-system rollout status deployment/replicove --timeout=180s
chart_path=".cache/workload-charts/$chart"
if [[ "$workload" == policy ]];then chart_path=test/e2e/chart;fi
go run ./test/e2e/seed "$chart_path" source-dev fixture "test/workloads/$workload/values.yaml"
while IFS= read -r deployment;do hk -n source-dev rollout status "$deployment" --timeout=240s;done < <(hk -n source-dev get deployment -o name)
if [[ "$workload" == cert-manager ]];then
 # Pod readiness can precede Service routing and webhook CA propagation. Probe
 # the real admission path without creating resources before source capture.
 admission_deadline=$((SECONDS + 120))
 until hk --request-timeout=10s apply --dry-run=server -f test/workloads/cert-manager/resources.yaml > /dev/null 2> "$work/admission-error.txt";do
  if [[ "$SECONDS" -ge "$admission_deadline" ]] || ! grep -Eq 'failed calling webhook|failed to call webhook|context deadline exceeded|Client.Timeout exceeded' "$work/admission-error.txt";then
   cat "$work/admission-error.txt" >&2
   exit 1
  fi
  echo 'Waiting for source cert-manager admission to become reachable'
  sleep 2
 done
fi
if [[ -f "test/workloads/$workload/resources.yaml" ]];then hk apply -f "test/workloads/$workload/resources.yaml";fi
case "$workload" in
 cert-manager) hk -n source-dev wait certificate/integration-certificate --for=condition=Ready --timeout=120s ;;
 spark) hk -n source-dev wait sparkapplication/spark-pi --for=jsonpath='{.status.applicationState.state}'=COMPLETED --timeout=420s ;;
esac
hk apply -f test/workloads/grant.yaml
bin/replicove create workload --grant workload-lab --ttl 1h --replication-file "test/workloads/$workload/replication.yaml"
hk -n replica-lab wait clusterreplica/workload --for=condition=Ready --timeout=600s
bin/replicove connect workload --role admin --output "$work/guest.kubeconfig" > "$work/artifacts/connect.txt" 2>&1 &
tunnel_pid=$!
for i in $(seq 1 180);do if [[ -f "$work/guest.kubeconfig" ]];then break;fi;sleep 1;done
[[ -f "$work/guest.kubeconfig" ]]
case "$workload" in
 policy)
  # Give the admission dispatcher one polling interval to observe the binding.
  sleep 10
  if gk -n source-dev create configmap policy-probe-denied --from-literal=mode=deny;then exit 1;fi
  cat <<YAML | gk apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: policy-probe-allowed
  namespace: source-dev
  labels:
    replicove.test/allow: "true"
data:
  mode: allow
YAML
  ;;
 cert-manager)
  gk -n source-dev wait certificate/integration-certificate --for=condition=Ready --timeout=120s
  gk -n source-dev get secret integration-certificate -o json | python3 -c 'import json,sys;assert set(json.load(sys.stdin)["data"])>={"tls.crt","tls.key"}'
  ;;
 spark)
  gk -n source-dev wait sparkapplication/spark-pi --for=jsonpath='{.status.applicationState.state}'=COMPLETED --timeout=120s
  # A completed SparkApplication proves the guest operator created and ran its driver/executor.
  gk -n source-dev get sparkapplication spark-pi -o jsonpath='{.status.applicationState.state}' > "$work/artifacts/spark-result.txt"
  ;;
 trino)
  # The real Trino CLI executes a query against the copied coordinator.
  result=$(gk -n source-dev exec deployment/fixture-trino-coordinator -- trino --server http://localhost:8080 --execute 'SELECT count(*) FROM tpch.tiny.nation' --output-format TSV)
  [[ "$result" == 25 ]]
  printf '%s\n' "$result" > "$work/artifacts/trino-result.txt"
  for i in $(seq 1 30);do
   if gk --request-timeout=2s get namespace source-dev >/dev/null 2>&1;then break;fi
   sleep 1
  done
  [[ "$i" -lt 30 ]]
  ;;
esac
bin/replicove status workload > "$work/artifacts/ready.json"
kill "$tunnel_pid" 2>/dev/null || true;wait "$tunnel_pid" || true;tunnel_pid=''
bin/replicove delete workload
hk -n replica-lab wait clusterreplica/workload --for=delete --timeout=420s
[[ -z "$(hk -n replicove-system get secret -l app.kubernetes.io/managed-by=replicove,replicove.nimeshbuilds.dev/state-kind!=capacity -o name)" ]]
[[ -z "$(hk -n replica-lab get pods,services,secrets,persistentvolumeclaims -o name)" ]]
echo "{\"workload\":\"$workload\",\"result\":\"passed\",\"sourceHelmCapture\":true,\"guestExecution\":true,\"ownedCleanup\":true}" > "$work/artifacts/report.json"
