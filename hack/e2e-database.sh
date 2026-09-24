#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/database-e2e
work=$(mktemp -d "$PWD/.cache/database-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-database-$(date +%s)-$$"
created=false
tunnel_pid=''
hk(){ kubectl --kubeconfig "$KUBECONFIG" --context "kind-$cluster" "$@"; }
gk(){ kubectl --kubeconfig "$work/guest.kubeconfig" "$@"; }
cleanup(){
 result=$?; trap - EXIT
 if [[ -n "$tunnel_pid" ]]; then kill "$tunnel_pid" 2>/dev/null || true; wait "$tunnel_pid" 2>/dev/null || true; fi
 if [[ "$created" == true ]]; then
  hk -n replicove-system logs deployment/replicove --tail=250 > "$work/artifacts/operator.log" 2>&1 || true
  hk -n replica-lab get clusterreplicas -o json > "$work/artifacts/status.json" || true
  hk get pods -A -o wide > "$work/artifacts/pods.txt" || true
  hk -n replica-lab get networkpolicies -o yaml > "$work/artifacts/networkpolicies.yaml" || true
  hk get events -A --field-selector type=Warning > "$work/artifacts/warnings.txt" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig" "$work/server.key" "$work/server.crt"
 echo "Database evidence: $work/artifacts"
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
if [[ "${REPLICOVE_USE_RELEASE_CLI:-false}" != true ]]; then make build; fi
hk create namespace source-database
source test/e2e/helm.sh
replicove_helm_install test/database/values.yaml
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/server.key" -out "$work/server.crt" -days 1 -subj '/CN=source-db.source-database.svc' >/dev/null 2>&1
hk -n source-database create secret tls source-db-tls --key "$work/server.key" --cert "$work/server.crt"
hk -n replicove-system create secret generic source-database-reader --from-literal=username=replica_reader --from-literal=password=disposable-reader-password
hk apply -f test/database/source.yaml
hk -n source-database rollout status deployment/source-db --timeout=240s
hk -n source-database rollout status deployment/app --timeout=180s
source_uid=$(hk -n source-database get deployment source-db -o jsonpath='{.metadata.uid}')
source_ip=$(hk -n source-database get pod -l app=source-db -o jsonpath='{.items[0].status.podIP}')
source_service_ip=$(hk -n source-database get service source-db -o jsonpath='{.spec.clusterIP}')
# Prove both source endpoints are reachable before testing guest isolation;
# a failed guest probe must not pass merely because the source is unavailable.
for endpoint in "$source_ip" "$source_service_ip"; do
 hk -n source-database exec deployment/app -- pg_isready -h "$endpoint" -t 2 >/dev/null
done
hk apply -f test/database/grant.yaml
bin/replicove create database-lab --grant database-lab --ttl 45m --replication-file test/database/replication.yaml
# A request cannot bypass the default-off module. Opting in preserves its request.
hk -n replica-lab wait clusterreplica/database-lab --for=jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'=DatabasesDisabled --timeout=120s
[[ "$(hk -n replica-lab get pods -l app=db-consumer -o jsonpath='{.items}')" == '[]' ]]
replicove_helm_install test/database/values.yaml --set databases.enabled=true
hk -n replicove-system rollout status deployment/replicove --timeout=180s
identity=system:serviceaccount:replicove-system:replicove
[[ "$(hk --as="$identity" -n source-database auth can-i delete pods)" == no ]]
[[ "$(hk --as="$identity" -n source-database auth can-i get secrets)" == no ]]
[[ "$(hk --as="$identity" -n replica-lab auth can-i create networkpolicies)" == yes ]]
# Continuously assert that source-data staging and application Pods never overlap.
for i in $(seq 1 600); do
 stage_count=$(hk -n replica-lab get pods -l replicove.nimeshbuilds.dev/database-role=stage -o name | wc -l | tr -d ' ')
 app_count=$(hk -n replica-lab get pods -l app=db-consumer -o name | wc -l | tr -d ' ')
 [[ "$stage_count" == 0 || "$app_count" == 0 ]]
 phase=$(hk -n replica-lab get clusterreplica database-lab -o jsonpath='{.status.phase}')
 if [[ "$phase" == Ready ]]; then break; fi
 sleep 1
done
[[ "$i" -lt 600 ]]
hk -n replica-lab wait clusterreplica/database-lab --for=condition=Ready --timeout=30s
bin/replicove connect database-lab --role admin --duration-seconds 3600 --output "$work/guest.kubeconfig" > "$work/artifacts/connect.log" 2>&1 &
tunnel_pid=$!
for i in $(seq 1 180); do if [[ -f "$work/guest.kubeconfig" ]] && gk --request-timeout=5s get --raw=/readyz >/dev/null 2>&1; then break; fi; sleep 1; done
[[ "$i" -lt 180 ]]
gk -n integration rollout status deployment/app --timeout=120s
[[ "$(gk -n integration get pods -l replicove.nimeshbuilds.dev/database-role=stage -o jsonpath='{.items}')" == '[]' ]]
db=replicove-db-test-database
[[ "$(gk -n integration exec "$db" -- psql -U replicove -d application -qAt -c 'SELECT count(*) FROM public.orders o JOIN public.customers c ON o.customer_email=c.email WHERE length(c.email)=64')" == 1 ]]
[[ "$(gk -n integration exec "$db" -- psql -U replicove -d application -qAt -c "SELECT count(*) FROM pg_constraint WHERE contype='f' AND convalidated")" == 1 ]]
# Verify host CNI enforcement from the actual copied PostgreSQL Pod to production.
if gk -n integration exec "$db" -- pg_isready -h "$source_ip" -t 2 >/dev/null 2>&1; then echo 'Database Pod could reach source through host policy' >&2; exit 1; fi
# Application connectivity must resolve the generated Service and reach the
# sanitized copy, while original Pod IP and Service IP endpoints stay blocked.
[[ "$(gk -n integration exec deployment/app -- psql -qAt -c 'SELECT count(*) FROM public.customers WHERE length(email)=64')" == 1 ]]
for endpoint in "$source_ip" "$source_service_ip"; do
 if gk -n integration exec deployment/app -- pg_isready -h "$endpoint" -t 2 >/dev/null 2>&1; then echo 'Replicated application could reach the source database' >&2; exit 1; fi
 hk -n source-database exec deployment/app -- pg_isready -h "$endpoint" -t 2 >/dev/null
done
gk -n integration exec "$db" -- psql -U replicove -d application -qAt -c CHECKPOINT >/dev/null
[[ "$(gk -n integration exec "$db" -- sh -ec '
test -f "$1/PG_VERSION" || exit 3
if grep -a -r -F -l -- "$2" "$1" >/dev/null; then
  printf FOUND
else
  result=$?
  test "$result" -eq 1 || exit "$result"
  printf CLEAN
fi' probe /var/lib/postgresql/data/pgdata original-user@example.invalid)" == CLEAN ]]
source_intact(){
 [[ "$(hk -n source-database get deployment source-db -o jsonpath='{.metadata.uid}')" == "$source_uid" ]]
 [[ "$(hk -n source-database exec deployment/source-db -c postgres -- psql -U fixture_admin -d application -qAt -c 'SELECT email FROM public.customers WHERE id=1')" == original-user@example.invalid ]]
 [[ "$(hk -n source-database exec deployment/source-db -c postgres -- psql -U fixture_admin -d application -qAt -c 'SELECT count(*) FROM public.customers')" == 2 ]]
}
source_intact
if hk -n source-database exec deployment/source-db -c postgres -- psql -U replica_reader -d application -v ON_ERROR_STOP=1 -c "UPDATE public.customers SET email='modified'" >/dev/null 2>&1; then echo 'Source role has write privileges' >&2; exit 1; fi
# Restart cannot trigger a second source read or discard the successful copy.
pod_uid=$(gk -n integration get pod "$db" -o jsonpath='{.metadata.uid}')
hk -n replicove-system rollout restart deployment/replicove
hk -n replicove-system rollout status deployment/replicove --timeout=180s
hk -n replica-lab wait clusterreplica/database-lab --for=condition=Ready --timeout=120s
[[ "$(gk -n integration get pod "$db" -o jsonpath='{.metadata.uid}')" == "$pod_uid" ]]
bin/replicove refresh database-lab
hk -n replica-lab wait clusterreplica/database-lab --for=jsonpath='{.status.phase}'=Blocked --timeout=120s
# Save exact owned data volume identities before removing the request.
hk -n replica-lab get pvc -o json > "$work/claims.json"
python3 - "$work/claims.json" > "$work/volumes.txt" <<'PY'
import json,sys
for p in json.load(open(sys.argv[1]))['items']:
    if p.get('spec',{}).get('volumeName'): print(p['spec']['volumeName'])
PY
hk -n replica-lab delete clusterreplica database-lab --wait=false
hk -n replica-lab wait clusterreplica/database-lab --for=delete --timeout=300s
while IFS= read -r pv; do [[ -z "$(hk get pv "$pv" --ignore-not-found -o name)" ]]; done < "$work/volumes.txt"
[[ "$(hk -n replica-lab get networkpolicies -o jsonpath='{.items}')" == '[]' ]]
[[ "$(hk -n replica-lab get pvc -o jsonpath='{.items}')" == '[]' ]]
source_intact
cat > "$work/artifacts/report.json" <<'JSON'
{"result":"passed","scenarios":["default-off module and late opt-in","explicit read-only source account and RBAC","host TLS source connection","application blocked during raw staging","actual PostgreSQL schema/data restore","domain masking and validated foreign keys","raw staging removed before access","no original fixture rows in final data or WAL","host Calico egress isolation from copied database","application DNS and sanitized database connectivity","application denied source Pod and Service IPs while source remains reachable","source unchanged","operator restart retains sanitized database","in-place refresh denied","TTL-compatible owned PVC/PV/host-policy cleanup"]}
JSON
