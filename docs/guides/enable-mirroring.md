# Enable mirroring on an existing installation

> **Candidate documentation:** these commands target v0.3.0-alpha.1, whose release qualification is pending. They require published candidate artifacts. Until publication, build the current source; v0.2.0-alpha.2 remains the published baseline and lacks the new candidate features.

You can add mirroring after installing Replicove. For a Helm installation, use an **in-place Helm upgrade** of the same release. Keep the existing encryption key, protected namespace, destination namespace, and custom values. There is no separate Replicove image to install: the current candidate includes the optional controller.

Enabling the module adds permissions and, when needed, snapshot infrastructure. The operator rolls out; existing `ClusterReplica` requests and vCluster workloads remain in place. It does **not** automatically convert them into mirrors, copy any source data, or reset their guest changes. Creating a `ReplicaMirror` is a separate, explicitly granted action.

## Before the upgrade

Use the host administrator context for the intended cluster. The commands below use release `replicove`, protected namespace `replicove-system`, and destination `replica-lab`. Substitute the existing installation's names; do not change them as part of enabling this module.

```bash
kubectl config current-context
helm list --all-namespaces
helm status replicove --namespace replicove-system
kubectl -n replica-lab get clusterreplicas,replicaaccesses
```

Check the [mirror prerequisites](mirrors.md#install-the-optional-module): a qualified snapshot/restore CSI driver, approved classes, host NetworkPolicy enforcement and sufficient capacity. A Kubernetes version match alone does not qualify storage or networking. The chart can supply the upstream snapshot controller/APIs; it does not install or replace the host CSI driver or CNI.

Save your Helm overrides privately and keep a recoverable backup of the immutable state key **together with** its encrypted state:

```bash
umask 077
helm get values replicove --namespace replicove-system --output yaml \
  > replicove-values-before.yaml
```

Use your existing protected backup process for state Secrets. Do not commit key material, credentials, Helm release records, or captured application data to Git. `helm get values` saves user overrides; do not use `--all` to turn old chart defaults into permanent overrides.

## 1. Update the CRDs

Use matching operator/chart/CRD/CLI versions. This example targets the **0.3.0-alpha.1** candidate after publication, including upgrades from 0.1.0-alpha.1:

```bash
helm show crds oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.3.0-alpha.1 > replicove-crds.yaml
kubectl apply -f replicove-crds.yaml
kubectl wait --for=condition=Established --timeout=60s -f replicove-crds.yaml
kubectl explain replicagrant.spec.mirror
kubectl explain replicamirror.spec
```

Update all six CRDs, including the volume-data policy on `ReplicaGrant`; merely adding `ReplicaMirror` and `ReplicaMirrorRun` is insufficient. [Helm does not upgrade existing CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/). The commands above use client-side apply for the versioned schemas; an initial missing-last-applied-annotation warning on Helm-installed CRDs is expected. If a GitOps system owns these schemas, update its desired manifests instead of creating competing reconcilers.

## 2. Add a small values overlay

Create `enable-mirrors.yaml`. Replace `source-dev` with the source namespaces the administrator approves. Set the CNI attestation to true only after confirming enforcement:

```yaml
image:
  repository: ghcr.io/nimeshbuilds/replicove
  tag: 0.3.0-alpha.1
  digest: ""
mirrors:
  enabled: true
  networkPolicyEnforced: true
  sources: [source-dev]
  snapshotController:
    mode: auto
```

The image is explicit because a previously saved `image.digest` overrides a new tag or chart default. The empty digest clears any old pin and selects the explicit 0.3.0-alpha.1 tag. For immutable pinning, copy the target release's `image.digest` from `helm show values oci://ghcr.io/nimeshbuilds/charts/replicove --version 0.3.0-alpha.1` into this overlay. If you use a private image mirror, substitute your reviewed equivalent image and digest. The older 0.1 operator cannot run the new controller.

`auto` installs snapshot infrastructure when the host snapshot APIs are absent, and otherwise reuses existing infrastructure. It retains this release's bundled controller on subsequent upgrades. Use `existing` when the host administrator manages the snapshot system. An incomplete existing installation needs repair; `managed` must not be used to add a competing controller. See the [mode reference](mirrors.md#install-the-optional-module).

The overlay deliberately leaves normal `sources`, `clusterReadRules`, resource limits and destination settings to the existing release. `mirrors.sources` supplies PVC-read and snapshot permissions, **not** workload/configuration reads. Ensure the normal `sources` rules already permit the kinds selected by your mirror. If you add rules, include the complete desired `sources` list in your overlay: **Helm replaces arrays; it does not append them.** Keep existing source permissions that active replicas still need. Exact data authorization also requires a `ReplicaGrant` later.

## 3. Upgrade the same release

If your saved Helm values explicitly set `stateKey.bootstrap: true`, an image change also changes the bootstrap Job's immutable pod template. After confirming the existing Job completed successfully, remove only that Job so Helm can recreate it with the target image and reuse the existing key:

```bash
# Only for Helm installations with stateKey.bootstrap: true.
kubectl -n replicove-system wait job/replicove-bootstrap \
  --for=condition=complete --timeout=5m && \
kubectl -n replicove-system delete job replicove-bootstrap --wait=true
```

If the wait fails, diagnose bootstrap instead of deleting an active or failed Job. The default Helm installation uses `stateKey.bootstrap: false` and skips this step. Preserve the key and protected state in either case.

```bash
helm upgrade replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.3.0-alpha.1 \
  --namespace replicove-system \
  --reset-then-reuse-values \
  --values enable-mirrors.yaml \
  --wait --timeout 5m
```

The tested Helm version is **4.3.0**. [`--reset-then-reuse-values`](https://helm.sh/docs/helm/helm_upgrade/) loads the new chart defaults, overlays the previous release's user settings, then applies your new overrides. Do not combine it with `--reset-values` or `--reuse-values`, which change that behavior. Use `helm upgrade` without `--install` here so a misspelled release name cannot silently create another operator.

Do not uninstall first, replace the state key, or switch the watched namespace. The operator's rollout can briefly pause reconciliation; it does not restart an existing guest control plane as part of enabling the module. This is separate from the interruption during a later managed mirror reset.

## 4. Verify, grant, then create

```bash
kubectl -n replicove-system rollout status deployment/replicove --timeout=180s
kubectl -n replicove-system get deployment replicove \
  -o jsonpath='{.spec.template.spec.containers[0].args}'
kubectl get crd volumesnapshots.snapshot.storage.k8s.io \
  volumesnapshotcontents.snapshot.storage.k8s.io \
  volumesnapshotclasses.snapshot.storage.k8s.io
kubectl get volumesnapshotclasses,storageclasses
kubectl -n replica-lab get clusterreplicas,replicaaccesses
```

The operator arguments must include `--mirrors=true` and `--mirror-network-policy-enforced=true`. When this release installed the snapshot controller, also wait for `deployment/replicove-snapshot-controller` in `replicove-system`. In `existing` mode, verify the host administrator's controller instead. CRD presence alone does not prove snapshots work.

Check that your pre-existing replica is still Ready and its guest data/access still work. Then follow [data grants and mirror creation](mirrors.md#grant-access-to-data), using exact source PVC names and qualified classes. An enabled controller with no `ReplicaMirror` requests captures no workload data.

**Choose the target deliberately.** vCluster 0.37.1 permits one runtime per host namespace. An existing ordinary managed replica may already occupy `replica-lab`. A new managed mirror then reports `RuntimeNamespaceInUse`; it does not remove that replica. For coexistence, use a separate operator installation with a distinct release, protected namespace and destination, or explicitly register a suitable independently managed vCluster as an [existing mirror target](mirrors.md#create-a-mirror-through-yaml-or-the-cli). Do not point an existing-target mirror at a disposable runtime whose separate owner's TTL could delete it. Enabling the module is not an ownership migration or automatic conversion of an ordinary replica.

## CLI, native YAML and GitOps installations

`replicove install` creates a Helm release. If you originally used the CLI, upgrade that existing release with the Helm procedure above. Rerunning `replicove install` is not an upgrade command. Use the matching [released CLI](https://github.com/nimeshbuilds/replicove/releases/tag/v0.3.0-alpha.1) for the mirror commands.

For a native YAML installation, keep managing it as native YAML. Switching it to Helm requires a separate ownership migration. Render the same released chart with the original installation's names/customizations and the mirror overlay, using these additional settings:

```yaml
stateKey:
  bootstrap: true
createDestinationNamespace: false
mirrors:
  snapshotController:
    mode: existing
```

Save these as `native-render.yaml`. This example assumes a qualified, administrator-managed snapshot installation. If none exists, first have the administrator select `managed` to include the bundled snapshot resources after checking for competing controllers. Offline `helm template` cannot safely make the live `auto` discovery decision.

For an otherwise default native installation, render a reviewable bundle:

```bash
helm template replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.3.0-alpha.1 --namespace replicove-system --include-crds \
  --values enable-mirrors.yaml --values native-render.yaml \
  > replicove-with-mirrors.yaml
```

Also pass your original native customizations if applicable. Review the complete bundle against the existing manifests, preserve separate namespace and source-RBAC manifests, apply the Replicove CRDs first, then apply the reviewed installation bundle. `stateKey.bootstrap: true` reuses the existing key; never render a new key offline. The bundle must include the mirror RBAC and any selected snapshot infrastructure, not just new Deployment flags. If the bootstrap Job's image/template changed, replace only its **completed** Job before applying; never replace an active bootstrap Job. Wait for bootstrap and controller readiness. See [YAML upgrades](../getting-started/installation.md#upgrades-and-removal) and [GitOps ordering/pruning](gitops.md#reconcile-installation-in-order).

The late-enable live suite covers the Helm upgrade mechanism used by Helm/CLI installations and a native rendered-manifest transition from 0.2.0-alpha.1, including completed-Job replacement and key reuse. It does not exercise every GitOps controller's reconciliation/pruning behavior or arbitrary custom overlays. Validate your controller's desired diff and apply ordering on a disposable cluster.

## Recovery and disabling

If the rollout fails, retain the existing key, state, destination and CRDs. Inspect `helm status`, Deployment events, snapshot-controller health, operator conditions and authorization. Correct the values/RBAC or missing dependency and repeat the upgrade. Do not use reinstall, forced ownership, finalizer removal, or an automatic downgrade to the old controller as recovery shortcuts once mirror requests exist.

Before setting `mirrors.enabled: false`, delete all `ReplicaMirror` requests and wait for their finalizers, generated replicas, sessions and recorded snapshots/volumes to finish cleanup. Keep source snapshot permissions and the responsible storage controllers available until that completes. Disabling the module is not a pause button; use `spec.suspend` to stop scheduling new work. Only then disable it with another values-preserving upgrade. See [cleanup](cleanup.md#remove-the-operator) for operator removal and retained CRDs/key behavior.

The bundled snapshot controller serves cluster-wide snapshot APIs. If other operators or applications reuse it, arrange an administrator-managed replacement or complete their snapshot lifecycles before removing it. Removing this release's controller while other consumers still depend on it can interrupt them. Retaining snapshot CRDs alone does not retain a functioning snapshot system.

## Verification scope

The [disposable mirror suite](../../hack/e2e-mirror.sh) starts with mirroring disabled, provisions an ordinary guest, changes guest data, and then enables the module. It has published-0.2.0-alpha.1 and published-0.1.0-alpha.1 Helm starting points and a rendered 0.2.0-alpha.1 native YAML starting point. It checks installation settings, key/namespace/runtime identity, original TTL, existing access and newly issued access before explicitly cleaning that fixture and running the full mirror lifecycle suite. Results and tested versions belong in the [validation record](../validation.md); fixture presence alone is not a passing result.
