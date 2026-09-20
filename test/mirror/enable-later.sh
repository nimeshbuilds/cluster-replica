# Sourced only by the disposable mirror runner after its host/CNI is ready.
# Keep a real ordinary replica and its encrypted state alive through opt-in.
mirror_base="${REPLICOVE_MIRROR_BASE:-current}"
native_mirror_render() {
 local image="${REPLICOVE_IMAGE:-cluster-replica:e2e}" args=()
 if [[ "$image" == *@sha256:* ]]; then
  args+=(--set-string "image.repository=${image%@*}" --set-string "image.digest=${image#*@}")
 else
  args+=(--set-string "image.repository=${image%:*}" --set-string "image.tag=${image##*:}" --set-string 'image.digest=')
 fi
 if [[ -n "${REPLICOVE_CHART_VERSION:-}" ]]; then args+=(--version "$REPLICOVE_CHART_VERSION"); fi
 helm template replicove "${REPLICOVE_CHART:-./charts/replicove}" --namespace replicove-system --include-crds \
  --values test/mirror/disabled-values.yaml --set stateKey.bootstrap=true --set createDestinationNamespace=false \
  "${args[@]}" "$@"
}
case "$mirror_base" in
 current)
  mirror_base_version=0.2.0-alpha.1
  REPLICOVE_CHART=oci://ghcr.io/nimeshbuilds/charts/replicove \
  REPLICOVE_CHART_VERSION=0.2.0-alpha.1 \
  REPLICOVE_IMAGE=ghcr.io/nimeshbuilds/replicove@sha256:306ca6cd22b77d890ef3db0fe0039a7a7aea3c3553dfb21c4ce3ee715f3be0a1 \
   replicove_helm_install test/mirror/disabled-values.yaml
  ;;
 previous)
  mirror_base_version=0.1.0-alpha.1
  REPLICOVE_CHART=oci://ghcr.io/nimeshbuilds/charts/replicove \
  REPLICOVE_CHART_VERSION=0.1.0-alpha.1 \
  REPLICOVE_IMAGE=ghcr.io/nimeshbuilds/replicove@sha256:1efc1bf2167c356bdbf532b3df30d47b7404a8583a7e1925ef3bbcffd92c18a4 \
   replicove_helm_install test/mirror/disabled-values.yaml
  [[ -z "$(hk get crd replicamirrors.replica.nimeshbuilds.dev --ignore-not-found -o name)" ]]
  ;;
 native)
  mirror_base_version=0.2.0-alpha.1
  hk create namespace replicove-system
  hk create namespace replica-lab
  REPLICOVE_CHART=oci://ghcr.io/nimeshbuilds/charts/replicove \
  REPLICOVE_CHART_VERSION=0.2.0-alpha.1 \
  REPLICOVE_IMAGE=ghcr.io/nimeshbuilds/replicove@sha256:306ca6cd22b77d890ef3db0fe0039a7a7aea3c3553dfb21c4ce3ee715f3be0a1 \
   native_mirror_render > "$work/native-before.yaml"
  hk apply -f "$work/native-before.yaml"
  hk -n replicove-system wait job/replicove-bootstrap --for=condition=complete --timeout=120s
  ;;
 *) echo 'REPLICOVE_MIRROR_BASE must be current, previous or native' >&2; exit 2 ;;
esac
hk -n replicove-system rollout status deployment/replicove --timeout=180s
[[ -z "$(hk -n replicove-system get deployment replicove-snapshot-controller --ignore-not-found -o name)" ]]
[[ -z "$(hk get crd volumesnapshots.snapshot.storage.k8s.io --ignore-not-found -o name)" ]]
[[ -z "$(hk -n source-dev get role replicove-system-replicove-mirror --ignore-not-found -o name)" ]]
hk -n replicove-system get deployment replicove -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);assert "--mirrors=true" not in d["spec"]["template"]["spec"]["containers"][0]["args"]'
hk -n source-dev create configmap before-mirrors --from-literal=mode=source
cat <<'YAML' | hk apply -f -
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaGrant
metadata: {name: before-mirrors}
spec:
  targetNamespace: replica-lab
  sourceNamespaces: [source-dev]
  resources: [{group: '', kind: ConfigMap}]
  accessRoles: [admin, viewer]
  maxTTL: 2h
  maxAccessSeconds: 1800
---
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ClusterReplica
metadata: {name: before-mirrors, namespace: replica-lab}
spec:
  profile: vcluster-0.37.1-persistent
  ttl: 2h
  cleanupPolicy: DeleteOwned
  approval: Automatic
  grantRef: before-mirrors
  replication:
    namespaces: [source-dev]
    namespaceMap: {source-dev: before-mirrors}
    include: [{kinds: [ConfigMap], names: [before-mirrors]}]
YAML
hk -n replica-lab wait clusterreplica/before-mirrors --for=condition=Ready --timeout=420s
bin/replicove connect before-mirrors --role admin --duration-seconds 1800 --output "$work/guest.kubeconfig" > "$work/artifacts/enable-later-connect.log" 2>&1 &
tunnel_pid=$!
for i in $(seq 1 120); do
 if [[ -f "$work/guest.kubeconfig" ]] && gk --request-timeout=3s get --raw=/readyz >/dev/null 2>&1; then break; fi
 if ! kill -0 "$tunnel_pid" 2>/dev/null; then cat "$work/artifacts/enable-later-connect.log"; exit 1; fi
 sleep 1
done
[[ "$(gk -n before-mirrors get configmap before-mirrors -o jsonpath='{.data.mode}')" == source ]]
gk -n before-mirrors patch configmap before-mirrors --type=merge -p '{"data":{"mode":"guest-experiment"}}'
before_release=$(hk -n replica-lab get clusterreplica before-mirrors -o jsonpath='{.status.runtime.releaseName}')
before_runtime=$(hk -n replica-lab get statefulset "$before_release" -o jsonpath='{.metadata.uid}')
before_guest=$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')
before_expiry=$(hk -n replica-lab get clusterreplica before-mirrors -o jsonpath='{.status.expiresAt}')
before_key=$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')
before_destination=$(hk get namespace replica-lab -o jsonpath='{.metadata.uid}')
before_operator=$(hk -n replicove-system get deployment replicove -o jsonpath='{.metadata.uid}')
if [[ "$mirror_base" != native ]]; then helm get values replicove -n replicove-system -o json > "$work/values-before.json"; fi
hk -n replicove-system get deployment replicove -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);json.dump(d["spec"]["template"]["spec"]["containers"][0]["resources"],sys.stdout)' > "$work/resources-before.json"

# Helm does not upgrade CRDs. Install the exact target chart schemas first,
# including ReplicaGrant's new volume-data policy when upgrading from 0.1.
crd_args=()
if [[ -n "${REPLICOVE_CHART_VERSION:-}" ]]; then crd_args+=(--version "$REPLICOVE_CHART_VERSION"); fi
helm show crds "${REPLICOVE_CHART:-./charts/replicove}" "${crd_args[@]}" > "$work/replicove-crds.yaml"
hk apply -f "$work/replicove-crds.yaml"
hk wait --for=condition=Established --timeout=60s -f "$work/replicove-crds.yaml"
cat > "$work/enable-mirrors.yaml" <<'YAML'
mirrors:
  enabled: true
  networkPolicyEnforced: true
  sources: [source-dev]
  snapshotController:
    mode: auto
YAML
# Helper supplies the reviewed target image explicitly, replacing any previous
# image digest. Retain all other user settings, while loading new chart defaults.
if [[ "$mirror_base" == native ]]; then
 # Offline rendering needs an explicit snapshot ownership decision. This fixture
 # established there is no host snapshot system before selecting managed mode.
 native_mirror_render --values "$work/enable-mirrors.yaml" --set mirrors.snapshotController.mode=managed > "$work/native-enabled.yaml"
 hk -n replicove-system wait job/replicove-bootstrap --for=condition=complete --timeout=120s
 hk -n replicove-system delete job replicove-bootstrap --wait=true --timeout=120s
 hk apply -f "$work/native-enabled.yaml"
 hk -n replicove-system wait job/replicove-bootstrap --for=condition=complete --timeout=120s
else
 replicove_helm_install "$work/enable-mirrors.yaml" --reset-then-reuse-values
fi
hk -n replicove-system rollout status deployment/replicove --timeout=180s
hk -n replicove-system rollout status deployment/replicove-snapshot-controller --timeout=180s
if [[ "$mirror_base" != native ]]; then
helm get values replicove -n replicove-system -o json > "$work/values-after.json"
python3 - "$work/values-before.json" "$work/values-after.json" <<'PY'
import json,sys
before,after=[json.load(open(p)) for p in sys.argv[1:]]
for key,value in before.items():
 if key not in ('mirrors','image'):
  assert after[key]==value, 'Existing installation values changed: '+key
assert after['mirrors']['enabled'] is True
PY
fi
hk -n replicove-system get deployment replicove -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);json.dump(d["spec"]["template"]["spec"]["containers"][0]["resources"],sys.stdout)' > "$work/resources-after.json"
cmp "$work/resources-before.json" "$work/resources-after.json"
[[ "$before_key" == "$(hk -n replicove-system get secret replicove-state-key -o jsonpath='{.metadata.uid}')" ]]
[[ "$before_destination" == "$(hk get namespace replica-lab -o jsonpath='{.metadata.uid}')" ]]
[[ "$before_operator" == "$(hk -n replicove-system get deployment replicove -o jsonpath='{.metadata.uid}')" ]]
[[ "$before_runtime" == "$(hk -n replica-lab get statefulset "$before_release" -o jsonpath='{.metadata.uid}')" ]]
[[ "$before_guest" == "$(gk get namespace kube-system -o jsonpath='{.metadata.uid}')" ]]
[[ "$before_expiry" == "$(hk -n replica-lab get clusterreplica before-mirrors -o jsonpath='{.status.expiresAt}')" ]]
[[ "$(gk -n before-mirrors get configmap before-mirrors -o jsonpath='{.data.mode}')" == guest-experiment ]]
[[ "$(hk -n source-dev get configmap before-mirrors -o jsonpath='{.data.mode}')" == source ]]
[[ "$(hk --as=system:serviceaccount:replicove-system:replicove -n source-dev auth can-i list configmaps)" == yes ]]

# A fresh session proves the new operator can decrypt/use the old replica state.
bin/replicove access before-mirrors --role viewer --output "$work/enable-later-access.kubeconfig"
python3 - "$work/guest.kubeconfig" "$work/enable-later-access.kubeconfig" <<'PY'
import json,subprocess,sys
configs=[json.loads(subprocess.check_output(['kubectl','--kubeconfig',p,'config','view','--raw','-o','json'])) for p in sys.argv[1:]]
configs[1]['clusters'][0]['cluster']['server']=configs[0]['clusters'][0]['cluster']['server']
with open(sys.argv[2],'w') as f:json.dump(configs[1],f)
PY
[[ "$(kubectl --kubeconfig "$work/enable-later-access.kubeconfig" -n before-mirrors get configmap before-mirrors -o jsonpath='{.data.mode}')" == guest-experiment ]]
python3 - "$mirror_base" "$mirror_base_version" > "$work/artifacts/enable-later.json" <<'PY'
import json,sys
json.dump({'result':'passed','initialInstall':sys.argv[1],'initialVersion':sys.argv[2],'scenarios':['mirrors-initially-disabled','snapshot-components-added-on-upgrade','crds-updated-before-operator','installation-settings-preserved','encryption-key-preserved','destination-and-operator-uid-preserved','active-runtime-and-guest-uid-preserved','guest-experiment-and-source-preserved','original-ttl-preserved','existing-session-preserved','fresh-session-from-existing-state']},sys.stdout)
PY

# Explicit fixture cleanup frees the one-runtime host namespace for the managed
# mirror suite. Enabling the module itself did not remove or convert this replica.
kill "$tunnel_pid" 2>/dev/null || true; wait "$tunnel_pid" 2>/dev/null || true; tunnel_pid=''
bin/replicove delete before-mirrors
hk -n replica-lab wait clusterreplica/before-mirrors --for=delete --timeout=360s
rm -f "$work/guest.kubeconfig" "$work/enable-later-access.kubeconfig"
