#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
mkdir -p .cache/chaos-e2e
work=$(mktemp -d "$PWD/.cache/chaos-e2e/run.XXXXXX")
mkdir -p "$work/artifacts"
export KUBECONFIG="$work/host.kubeconfig"
cluster="replicove-chaos-$(date +%s)-$$"
created=false
tunnel_pid=''
hk(){ kubectl --kubeconfig "$KUBECONFIG" --context "kind-$cluster" "$@"; }
gk(){ kubectl --kubeconfig "$work/guest.kubeconfig" "$@"; }
cleanup(){
 result=$?; trap - EXIT
 if [[ -n "$tunnel_pid" ]]; then kill "$tunnel_pid" 2>/dev/null || true; wait "$tunnel_pid" 2>/dev/null || true; fi
 if [[ "$created" == true ]]; then
  hk -n replicove-system logs deployment/replicove --tail=250 > "$work/artifacts/operator.log" 2>&1 || true
  hk -n replica-lab get replicaexperiments,clusterreplicas -o json > "$work/artifacts/status.json" || true
  hk get pods -A -o wide > "$work/artifacts/pods.txt" || true
  hk -n replica-lab get networkpolicies -o yaml > "$work/artifacts/networkpolicies.yaml" || true
  hk get events -A --field-selector type=Warning > "$work/artifacts/warnings.txt" || true
  kind delete cluster --name "$cluster" || true
 fi
 rm -f "$work/host.kubeconfig" "$work/guest.kubeconfig"
 echo "Chaos evidence: $work/artifacts"
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
replicove_helm_install test/chaos/values.yaml --set chaos.enabled=false
hk -n replicove-system rollout status deployment/replicove --timeout=180s
identity=system:serviceaccount:replicove-system:replicove
[[ "$(hk --as="$identity" -n source-dev auth can-i delete pods)" == no ]]
[[ "$(hk --as="$identity" -n source-dev auth can-i create networkpolicies)" == no ]]
[[ "$(hk --as="$identity" -n replica-lab auth can-i create networkpolicies)" == no ]]
hk apply -f test/chaos/source.yaml
hk -n source-dev rollout status deployment/echo --timeout=180s
source_uid=$(hk -n source-dev get deployment echo -o jsonpath='{.metadata.uid}')
source_ip=$(hk -n source-dev get pod -l app=echo -o jsonpath='{.items[0].status.podIP}')
hk apply -f test/chaos/grant.yaml
bin/replicove create chaos-lab --grant chaos-lab --ttl 45m --replication-file test/chaos/replication.yaml
hk -n replica-lab wait clusterreplica/chaos-lab --for=condition=Ready --timeout=600s
replica_uid=$(hk -n replica-lab get clusterreplica chaos-lab -o jsonpath='{.metadata.uid}')
# Enable the optional module after an ordinary runtime already exists. Helm
# updates the same installation; it must preserve request, runtime and state key.
runtime_name=$(hk -n replica-lab get clusterreplica chaos-lab -o jsonpath='{.status.runtime.releaseName}')
runtime_uid=$(hk -n replica-lab get statefulset "$runtime_name" -o jsonpath='{.metadata.uid}')
state_key_uid=$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')
replicove_helm_install test/chaos/values.yaml --set chaos.enabled=true
hk -n replicove-system rollout status deployment/replicove --timeout=180s
[[ "$(hk -n replica-lab get clusterreplica chaos-lab -o jsonpath='{.metadata.uid}')" == "$replica_uid" ]]
[[ "$(hk -n replica-lab get statefulset "$runtime_name" -o jsonpath='{.metadata.uid}')" == "$runtime_uid" ]]
[[ "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" == "$state_key_uid" ]]
[[ "$(hk --as="$identity" -n replica-lab auth can-i create networkpolicies)" == yes ]]
hk -n replica-lab wait clusterreplica/chaos-lab --for=condition=Ready --timeout=180s
bin/replicove connect chaos-lab --role admin --duration-seconds 3600 --output "$work/guest.kubeconfig" > "$work/artifacts/connect.log" 2>&1 &
tunnel_pid=$!
for i in $(seq 1 180); do if [[ -f "$work/guest.kubeconfig" ]] && gk --request-timeout=5s get --raw=/readyz >/dev/null 2>&1; then break; fi; sleep 1; done
[[ "$i" -lt 180 ]]
gk -n integration rollout status deployment/echo --timeout=180s
workload_uid=$(gk -n integration get deployment echo -o jsonpath='{.metadata.uid}')
write_experiment(){
 local name="$1" duration="$2" faults="$3"
 cat > "$work/$name.yaml" <<YAML
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaExperiment
metadata:
  name: $name
  namespace: replica-lab
spec:
  replicaRef: {name: chaos-lab, uid: $replica_uid}
  durationSeconds: $duration
  faults: $faults
YAML
 bin/replicove chaos create "$name" -f "$work/$name.yaml"
}
phase(){ hk -n replica-lab wait "replicaexperiment/$1" --for=jsonpath='{.status.phase}'="$2" --timeout=180s; }
remove(){ bin/replicove chaos delete "$1"; hk -n replica-lab wait "replicaexperiment/$1" --for=delete --timeout=180s; }
source_intact(){
 [[ "$(hk -n source-dev get deployment echo -o jsonpath='{.metadata.uid}')" == "$source_uid" ]]
 [[ "$(hk -n source-dev get deployment echo -o jsonpath='{.spec.replicas}')" == 1 ]]
 [[ "$(hk -n source-dev exec deployment/echo -- wget -qO- -T 3 "http://$source_ip:8080")" == source-intact ]]
}
# The restart must recover the original scale count solely from encrypted intent.
write_experiment scale-restart 90 "[{kind: ScaleZero, namespace: integration, target: {kind: Deployment, name: echo, uid: $workload_uid}}]"
phase scale-restart Active
[[ "$(gk -n integration get deployment echo -o jsonpath='{.spec.replicas}')" == 0 ]]
source_intact
hk -n replicove-system rollout restart deployment/replicove
hk -n replicove-system rollout status deployment/replicove --timeout=180s
phase scale-restart Completed
gk -n integration rollout status deployment/echo --timeout=180s
[[ "$(gk -n integration get deployment echo -o jsonpath='{.spec.replicas}')" == 1 ]]
remove scale-restart
# A source UID is never accepted as the guest target identity.
write_experiment wrong-uid 30 "[{kind: ScaleZero, namespace: integration, target: {kind: Deployment, name: echo, uid: $source_uid}}]"
phase wrong-uid Rejected
remove wrong-uid
write_experiment wrong-namespace 30 '[{kind: NetworkIsolation, namespace: kube-system}]'
phase wrong-namespace Rejected
remove wrong-namespace
pod=$(gk -n integration get pods -l app=echo -o jsonpath='{.items[0].metadata.name}')
pod_uid=$(gk -n integration get pod "$pod" -o jsonpath='{.metadata.uid}')
write_experiment pod-recovery 30 "[{kind: PodDelete, namespace: integration, target: {kind: Pod, name: $pod, uid: $pod_uid}}]"
phase pod-recovery Active
gk -n integration wait "pod/$pod" --for=delete --timeout=60s
gk -n integration rollout status deployment/echo --timeout=180s
remove pod-recovery
# Prove host-side network enforcement; merely creating a guest policy is not enough.
[[ "$(gk -n integration exec deployment/echo -- wget -qO- -T 3 "http://$source_ip:8080")" == source-intact ]]
write_experiment network 90 '[{kind: NetworkIsolation, namespace: integration}]'
phase network Active
sleep 5
if gk -n integration exec deployment/echo -- wget -qO- -T 3 "http://$source_ip:8080" > "$work/network-attempt.txt" 2>&1; then echo 'Source remained reachable under network fault' >&2; exit 1; fi
source_intact
remove network
for i in $(seq 1 30); do if gk -n integration exec deployment/echo -- wget -qO- -T 2 "http://$source_ip:8080" >/dev/null 2>&1; then break; fi; sleep 1; done
[[ "$i" -lt 30 ]]
# Existing host allow policies must never be removed or silently overruled.
cat > "$work/allow.yaml" <<'YAML'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: foreign-allow, namespace: replica-lab}
spec:
  podSelector: {matchLabels: {unrelated: keep}}
  policyTypes: [Egress]
  egress: [{}]
YAML
hk apply -f "$work/allow.yaml"
write_experiment additive-conflict 30 '[{kind: NetworkIsolation, namespace: integration}]'
phase additive-conflict Rejected
hk -n replica-lab get networkpolicy foreign-allow >/dev/null
remove additive-conflict
hk delete -f "$work/allow.yaml"
# Stress fixtures use an immutable Python image; custom Jobs expose no raw Pod spec.
image='docker.io/library/python@sha256:79e7a9b9ff1cbceff819f856fb374477792a5967759d94df266de7b7b4120e6f'
write_experiment stress 120 "[{kind: CPUStress, namespace: integration, image: '$image', cpuMilli: 100, memoryMiB: 32}, {kind: MemoryStress, namespace: integration, image: '$image', cpuMilli: 100, memoryMiB: 64}]"
phase stress Active
gk -n integration get jobs -o json > "$work/artifacts/stress-jobs.json"
source_intact
remove stress
[[ "$(gk -n integration get jobs -o jsonpath='{.items}')" == '[]' ]]
write_experiment custom 90 "[{kind: CustomJob, namespace: integration, image: '$image', command: [python3, '-c', 'import time; time.sleep(60)'], cpuMilli: 50, memoryMiB: 32}]"
phase custom Active
remove custom
# Parent deletion must first restore a running scale fault and finish its finalizer.
write_experiment parent-cleanup 240 "[{kind: ScaleZero, namespace: integration, target: {kind: Deployment, name: echo, uid: $workload_uid}}]"
phase parent-cleanup Active
hk -n replica-lab delete clusterreplica chaos-lab --wait=false
hk -n replica-lab wait replicaexperiment/parent-cleanup --for=delete --timeout=180s
hk -n replica-lab wait clusterreplica/chaos-lab --for=delete --timeout=300s
[[ "$(hk -n replica-lab get networkpolicies -o jsonpath='{.items}')" == '[]' ]]
source_intact
cat > "$work/artifacts/report.json" <<'JSON'
{"result":"passed","scenarios":["late chaos enablement preserves existing request runtime and state key","source RBAC denial","scale outage","operator restart rollback","duration expiry","source UID rejection","protected namespace rejection","pod controller recovery","real host CNI isolation","source workload preservation","network recovery","additive policy conflict preservation","simultaneous CPU and memory stress","restricted custom Job","parent teardown waits for experiment cleanup","no host fault policy leak"]}
JSON
