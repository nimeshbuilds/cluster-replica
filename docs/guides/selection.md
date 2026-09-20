# Select and transform resources

`ClusterReplica.spec.replication` describes what to capture and how to adapt it. All selections remain inside the referenced grant.

## Namespace, kind, name, and label selectors

```yaml
replication:
  namespaces: [source-dev]
  include:
    - groups: [apps]
      kinds: [Deployment]
      names: [echo]
    - groups: [""]
      kinds: [ConfigMap]
      labelSelector:
        matchLabels:
          integration-test: enabled
  exclude:
    - kinds: [ConfigMap]
      names: [production-only]
  namespaceMap:
    source-dev: integration
```

An empty `namespaces` list selects the grant's source namespaces. With no `include` selectors, eligible granted desired-state roots are selected. Entries in `include` are alternatives; fields within one selector narrow that match. `labelSelector` supports Kubernetes `matchLabels` and `matchExpressions`. Exclusions win.

Known references—such as a Deployment's ServiceAccount, ConfigMap, or Secret—are resolved as dependencies. They can be added even if they were not selected as roots, but they still must be granted and not excluded. A required excluded, missing, or unauthorized dependency blocks the plan instead of being silently omitted.

## What is preserved and removed

Desired object fields, application labels, and eligible annotations are captured. Server identities and generated state are removed: UID, resourceVersion, managed fields, status, owner references, finalizers, source Service IPs, and other controller metadata. Objects already owned by another controller and generated workload children are not independent capture roots.

Namespace, Node, PV, Pod, ReplicaSet, Event, Endpoint, lease, authorization-review, Replicove, and reserved Loft API resources are not generic clone inputs. A Deployment is captured and creates new guest Pods through its controller.

## Map namespaces and storage classes

```yaml
replication:
  namespaceMap:
    source-dev: integration
    operator-system: test-operators
  storageClassMap:
    production-fast: standard
```

Map source namespaces to valid, non-system guest namespaces. Replicove rewrites object identity and known ServiceAccount/webhook references. It does not guess arbitrary strings inside ConfigMaps, application URLs, scripts, or custom resources. Change those through explicit patches or chart values.

StorageClass mappings apply to selected PVC requests under the granted `EmptyVolumes` mode. They do not clone a host StorageClass or copy its volume data. See [storage](secrets-storage.md).

## Apply object patches

```yaml
replication:
  patches:
    - apiVersion: apps/v1
      kind: Deployment
      namespace: source-dev
      name: echo
      patch:
        spec:
          replicas: 1
    - apiVersion: v1
      kind: ConfigMap
      namespace: source-dev
      name: settings
      patch:
        data:
          mode: integration
```

Patch selectors match the **original source** API version, kind, namespace, and name. The patch is a JSON merge patch, not JSON Patch or Kubernetes strategic merge: arrays are replaced as a whole, and `null` removes a field. Safety and identity checks still apply after transformation.

For captured Helm components, prefer [Helm values overrides](operators.md) when the chart exposes the change. An immutable request cannot have its selection or patches edited later. Delete and recreate it for a new configuration; use [refresh](lifecycle.md) to recapture source changes with the same selection.

## Readiness checks

Standard workloads have built-in readiness evaluation. Add checks for custom resources:

```yaml
replication:
  checks:
    - apiVersion: cert-manager.io/v1
      kind: Certificate
      namespace: source-dev
      name: integration-cert
      condition: Ready
    - apiVersion: sparkoperator.k8s.io/v1beta2
      kind: SparkApplication
      namespace: source-dev
      name: spark-pi
      field: status.applicationState.state
      equals: COMPLETED
```

Readiness check selectors, like patches, identify the **original source** object. The check is evaluated against its mapped live guest counterpart. Use a condition (which must be `True`) or a dot-separated field and expected string. This is a field traversal, not a general JSONPath expression. A check applies only to a matching captured object; it does not add objects to the selection. See the checked-in [workload fixtures](../workload-adapters.md) for complete grants, charts, and matching names.
