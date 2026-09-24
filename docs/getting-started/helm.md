# Quickstart with Helm

> **Versioned instructions:** these commands use v0.3.0-alpha.1. Use matching CLI, chart, image and CRDs from that release. Check the release and validation record for source and published-artifact evidence; use a source build when testing an unreleased revision.

Install Replicove with one Helm command on a disposable Kubernetes host. It works whether the host has no vCluster or already runs independently managed vClusters. The release chart, operator image, and CLI use public artifact distribution; no GitHub login is required after publication.

## 1. Install

Use a host administrator context and check it before installing:

```bash
kubectl config current-context
```

Then run:

```bash
helm upgrade --install replicove oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.3.0-alpha.1 \
  --namespace replicove-system --create-namespace \
  --wait --timeout 3m
```

This installs all Replicove CRDs, including the mirror and experiment APIs, the operator, its explicit RBAC, and an immutable encryption key in `replicove-system`. It creates `replica-lab` if absent. Existing destination namespaces are reused without taking ownership; namespaces created by the chart are retained on uninstall. The published chart pins the operator by image digest.

The default values grant no access to source namespaces. An administrator grants those reads separately. Replicove downloads and installs its pinned OSS vCluster chart when an approved replica requires a new runtime. You do not need the vCluster CLI, a preinstalled vCluster, or vCluster Platform.

Helm 4.3.0 is tested. The chart uses the v2 chart format and OCI distribution; other Helm versions are not part of the recorded live test matrix. Host prerequisites and the tested Kubernetes versions are in [installation](installation.md).

## 2. Create the first replica

On a disposable host with a working default StorageClass, apply the same small fixture used by the YAML walkthrough:

```bash
export REPLICOVE_EXAMPLES=https://raw.githubusercontent.com/nimeshbuilds/replicove/v0.3.0-alpha.1/examples/yaml
kubectl apply -f "$REPLICOVE_EXAMPLES/source.yaml" \
  -f "$REPLICOVE_EXAMPLES/source-rbac.yaml" \
  -f "$REPLICOVE_EXAMPLES/grant.yaml" \
  -f "$REPLICOVE_EXAMPLES/replica.yaml"
kubectl -n replica-lab wait clusterreplica/yaml-demo \
  --for=condition=Ready --timeout=420s
```

The fixture creates a source echo application, a dummy Secret, bounded source permissions, and a one-hour `ClusterReplica`. Replicove provisions a persistent vCluster and recreates the selection in guest namespace `integration`. A 1 GiB control-plane PVC uses the host's default StorageClass. Source volume contents are not copied.

To connect locally, download and extract your platform’s `replicove` CLI archive from the [alpha release](https://github.com/nimeshbuilds/replicove/releases/tag/v0.3.0-alpha.1) into your working directory. Keep this command running in the same host context:

```bash
./replicove -n replica-lab connect yaml-demo --role viewer \
  --output "$PWD/replicove-guest.kubeconfig"
```

After it reports the tunnel is listening, run in a second terminal in that directory:

```bash
kubectl --kubeconfig "$PWD/replicove-guest.kubeconfig" -n integration \
  get configmap settings -o jsonpath='{.data.mode}'
```

Expect `guest-yaml`. The session lasts at most 15 minutes. Stop the tunnel with Ctrl-C before cleanup; the CLI removes its credential file. The [access guide](../guides/access.md) also explains direct YAML requests and network routes for agents. The fixture grants are for this disposable example; replace them with your team's explicit source and access policy.

## 3. Use a vCluster that already exists

Install the same chart with the same command. Register the existing guest once using an administrator-held, data-only kubeconfig and its `kube-system` namespace UID, as described in [existing vClusters](../guides/existing.md). Then select it in a `ClusterReplica`:

```yaml
# ClusterReplica.spec
target:
  provider: existing
  existingRef: shared-integration
```

The name must match `ReplicaGrant.spec.existingTargets[].name`. Pre-create the selected namespaces inside that guest. Replicove will populate them and later clean up its recorded additions. The existing vCluster runtime, foreign objects, and pre-existing namespaces remain in place. TTL expires the replica's additions and access sessions; it does not destroy the independently managed vCluster.

Registering an existing guest is explicit. Installing the operator does not automatically adopt vClusters it discovers. vCluster Platform provisioning still requires separate qualification.

## Customize the installation

Download the chart values for review:

```bash
helm show values oci://ghcr.io/nimeshbuilds/charts/replicove \
  --version 0.3.0-alpha.1 > operator-values.yaml
```

Set `destinationNamespace` to the one host namespace this operator should watch. Set `createDestinationNamespace: false` if another system manages that namespace; it must already exist. Add explicit read rules under `sources` for your existing source namespaces and pair them with a matching `ReplicaGrant`. Full definitions are in [operator configuration](../reference/operator.md).

Pass the reviewed file to the same installation command with `--values operator-values.yaml`. Preserve that file for upgrades. Changing the watched namespace while live replicas remain is not a migration: finish those lifecycles first or use a separate operator installation.

For GitOps controllers that render templates without a live Kubernetes lookup, manage the destination namespace separately and set `createDestinationNamespace: false`. Key initialization also needs care: follow [GitOps](../guides/gitops.md) and the [native YAML bootstrap path](yaml.md).

## Cleanup and upgrades

Mirroring is optional at initial installation. To add it to this release later, follow [enable mirroring on an existing installation](../guides/enable-mirroring.md). That procedure preserves saved values and active ordinary replicas while adding snapshot permissions and dependencies.

For the disposable example:

```bash
kubectl -n replica-lab delete clusterreplica yaml-demo --wait=true --timeout=360s
kubectl -n replica-lab get clusterreplicas,replicaaccesses,pods,persistentvolumeclaims
```

After **all** mirrors, generated and ordinary replicas, and access requests managed by the operator have finished cleanup:

```bash
helm uninstall replicove --namespace replicove-system --wait --timeout 2m
```

Helm retains the state key and chart-created destination namespace. It also leaves CRDs and source fixtures. Remove only fixtures and namespaces you own after verifying they are empty of active lifecycles. Do not delete namespaces or CRDs to bypass finalizers.

For later releases, apply their CRD bundle before `helm upgrade --install`: Helm does not upgrade CRDs automatically. Keep the same release name, system namespace, encryption key, destination, and reviewed values. See [upgrade and recovery details](installation.md#upgrades-and-removal).

## Verification

The live suite exercises Helm installation on a fresh host, automatic vCluster provisioning, upgrades while a replica exists, TTL cleanup, and installation after an independent vCluster already exists. It checks that reuse and uninstall preserve existing resources. The release workflow repeats the new/existing tests using anonymous OCI chart and image pulls. See [CI](https://github.com/nimeshbuilds/replicove/actions/workflows/ci.yaml) and the test link on the [alpha release](https://github.com/nimeshbuilds/replicove/releases/tag/v0.3.0-alpha.1).
