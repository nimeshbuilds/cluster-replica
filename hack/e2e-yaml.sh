#!/usr/bin/env bash
# Exercise the public YAML walkthrough without the Replicove or Helm CLI.
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/yaml-e2e
work=$(mktemp -d "$PWD/.cache/yaml-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
cluster="replicove-yaml-$(date +%s)-$$"
created=false
forward_pid=''
hk(){ kubectl --kubeconfig "$work/host.kubeconfig" --context "kind-$cluster" "$@"; }
gk(){ kubectl --kubeconfig "$work/guest.kubeconfig" "$@"; }
dk(){ kubectl --kubeconfig "$work/deployer.kubeconfig" "$@"; }
expect_guest_forbidden(){
 if "$@" > "$work/denial.txt" 2>&1; then echo 'Expected guest API permission denial' >&2; exit 1; fi
 grep -Eq '\(Forbidden\)| is forbidden:' "$work/denial.txt"
}
wait_guest_revocation(){
 for attempt in $(seq 1 60); do
  if "$@" > "$work/revocation.txt" 2>&1; then sleep 1; continue; fi
  if grep -Eq '\(Forbidden\)| is forbidden:|\(Unauthorized\)|must be logged in' "$work/revocation.txt"; then return 0; fi
  cat "$work/revocation.txt" >&2; return 1
 done
 echo 'Guest credential remained authorized after revocation' >&2; return 1
}
cleanup(){
 result=$?; trap - EXIT
 if [[ -n "$forward_pid" ]]; then kill "$forward_pid" 2>/dev/null || true; wait "$forward_pid" 2>/dev/null || true; fi
 if [[ "$created" == true ]]; then
  hk -n replicove-system logs deployment/replicove --tail=150 > "$work/artifacts/operator.log" 2>&1 || true
  hk -n replicove-system logs job/replicove-bootstrap > "$work/artifacts/bootstrap.log" 2>&1 || true
  hk -n replica-lab get clusterreplicas,replicaaccesses -o json | python3 test/e2e/inventory.py > "$work/artifacts/requests.json" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig" "$work/deployer.kubeconfig"
 echo "Sanitized YAML evidence: $work/artifacts"
 exit "$result"
}
trap cleanup EXIT
if [[ -z "${REPLICOVE_MANIFEST_DIR:-}" ]]; then docker build --tag replicove:yaml .; fi
if kind get clusters | grep -Fxq "$cluster"; then exit 1; fi
created=true
kind create cluster --name "$cluster" --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed --kubeconfig "$work/host.kubeconfig" --wait 180s
if [[ -z "${REPLICOVE_MANIFEST_DIR:-}" ]]; then
 kind load docker-image replicove:yaml --name "$cluster"
 hk apply -f config/crd/
else
 hk apply -f "$REPLICOVE_MANIFEST_DIR/replicove-crds.yaml"
fi
hk wait --for=condition=Established --timeout=60s crd/clusterreplicas.replica.nimeshbuilds.dev crd/replicagrants.replica.nimeshbuilds.dev crd/replicaaccesses.replica.nimeshbuilds.dev
install_manifests(){
 if [[ -z "${REPLICOVE_MANIFEST_DIR:-}" ]]; then
  hk apply -k examples/yaml/install
 else
  hk apply -f "$REPLICOVE_MANIFEST_DIR/replicove-install.yaml"
 fi
}
install_manifests
hk -n replicove-system wait job/replicove-bootstrap --for=condition=Complete --timeout=180s
hk -n replicove-system rollout status deployment/replicove --timeout=180s
# Reinstall the bootstrap Job while state exists: it must retain the original key.
key_uid=$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')
hk apply -f examples/yaml/source.yaml
hk apply -f examples/yaml/source-rbac.yaml
hk apply -f examples/yaml/grant.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
hk apply -f examples/yaml/replica.yaml
hk -n replica-lab wait clusterreplica/yaml-demo --for=condition=Ready --timeout=420s
hk -n replicove-system delete job replicove-bootstrap --wait=true
install_manifests
hk -n replicove-system wait job/replicove-bootstrap --for=condition=Complete --timeout=180s
[[ "$key_uid" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
replica_uid=$(hk -n replica-lab get clusterreplica yaml-demo -o jsonpath='{.metadata.uid}')
cat <<YAML | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaAccess
metadata:
  name: yaml-session
  namespace: replica-lab
spec:
  replicaName: yaml-demo
  replicaUID: "$replica_uid"
  role: viewer
  durationSeconds: 900
YAML
hk -n replica-lab wait replicaaccess/yaml-session --for=jsonpath='{.status.phase}'=Ready --timeout=180s
credential=$(hk -n replica-lab get replicaaccess yaml-session -o jsonpath='{.status.credentialSecret}')
hk -n replica-lab get secret "$credential" -o jsonpath='{.data.config}' | python3 -c 'import base64,sys;sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))' > "$work/guest.kubeconfig"
runtime=$(hk -n replica-lab get clusterreplica yaml-demo -o jsonpath='{.status.runtime.releaseName}')
hk -n replica-lab port-forward "service/$runtime" 18443:443 --address 127.0.0.1 > "$work/forward.log" 2>&1 &
forward_pid=$!
gk config set-cluster replicove --server=https://127.0.0.1:18443 >/dev/null
viewer_ready=false
for attempt in $(seq 1 60); do
 if gk --request-timeout=2s -n integration get deployment echo >/dev/null 2>&1; then viewer_ready=true; break; fi
 sleep 1
done
[[ "$viewer_ready" == true ]]
[[ "$(gk -n integration get configmap settings -o jsonpath='{.data.mode}')" == guest-yaml ]]
[[ "$(hk -n source-dev get configmap settings -o jsonpath='{.data.mode}')" == source ]]
[[ "$(gk -n integration auth can-i create deployments)" == no ]]
expect_guest_forbidden gk --request-timeout=5s -n integration create configmap viewer-write-probe --from-literal=mode=denied
hk -n replica-lab delete replicaaccess yaml-session --wait=true --timeout=180s
[[ -z "$(hk -n replica-lab get secret "$credential" --ignore-not-found -o name)" ]]
wait_guest_revocation gk --request-timeout=5s -n integration get configmap settings
# Exercise the other role delegated by this YAML grant through real API writes.
cat <<YAML | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaAccess
metadata:
  name: yaml-deployer
  namespace: replica-lab
spec:
  replicaName: yaml-demo
  replicaUID: "$replica_uid"
  role: deployer
  durationSeconds: 900
YAML
hk -n replica-lab wait replicaaccess/yaml-deployer --for=jsonpath='{.status.phase}'=Ready --timeout=180s
deployer_credential=$(hk -n replica-lab get replicaaccess yaml-deployer -o jsonpath='{.status.credentialSecret}')
hk -n replica-lab get secret "$deployer_credential" -o jsonpath='{.data.config}' | python3 -c 'import base64,sys;sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))' > "$work/deployer.kubeconfig"
dk config set-cluster replicove --server=https://127.0.0.1:18443 >/dev/null
deployer_ready=false
for attempt in $(seq 1 60); do
 if [[ "$(dk --request-timeout=5s -n integration auth can-i create configmaps)" == yes ]]; then deployer_ready=true; break; fi
 sleep 1
done
[[ "$deployer_ready" == true ]]
dk -n integration create configmap deployer-probe --from-literal=mode=created
dk -n integration patch configmap deployer-probe --type merge -p '{"data":{"mode":"updated"}}'
[[ "$(dk -n integration get configmap deployer-probe -o jsonpath='{.data.mode}')" == updated ]]
expect_guest_forbidden dk --request-timeout=5s create namespace deployer-namespace-probe
expect_guest_forbidden dk --request-timeout=5s create clusterrole deployer-escalation --verb='*' --resource='*'
dk -n integration delete configmap deployer-probe --wait=true
[[ "$(hk -n source-dev get configmap settings -o jsonpath='{.data.mode}')" == source ]]
hk -n replica-lab delete replicaaccess yaml-deployer --wait=true --timeout=180s
[[ -z "$(hk -n replica-lab get secret "$deployer_credential" --ignore-not-found -o name)" ]]
wait_guest_revocation dk --request-timeout=5s -n integration get configmap settings
hk -n replica-lab delete clusterreplica yaml-demo --wait=true --timeout=360s
[[ -z "$(hk -n replica-lab get pods,services,pvc,statefulsets,secrets -o name)" ]]
hk -n source-dev get deployment echo >/dev/null
[[ -n "$(hk -n replicove-system get secret replicove-state-key -o name)" ]]
cat > "$work/artifacts/report.json" <<'JSON'
{"scenario":"yaml-only","operatorInstall":true,"inClusterKeyBootstrap":true,"idempotentBootstrap":true,"vclusterProvisioned":true,"replicatedConfig":true,"sourcePreserved":true,"boundedAccess":true,"viewerWriteDenied":true,"deployerWrite":true,"deployerPrivilegeDenied":true,"deployerRevocation":true,"revocation":true,"ownedCleanup":true}
JSON
