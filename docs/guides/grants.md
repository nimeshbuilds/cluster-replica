# Grants and permissions

Two independent controls must allow a source read: the operator's host Kubernetes RBAC and an administrator-created `ReplicaGrant`. Giving a request more selectors cannot expand either boundary.

## Namespace model

| Namespace | Contains | Who should control it |
| --- | --- | --- |
| `replicove-system` | Operator, encryption key, captures, ownership and access state | Administrators only |
| `replica-lab` | Runtime resources, replica/mirror/access requests, session Secrets | Carefully scoped users and the operator |
| `source-dev` | Original workloads and configuration; optional owned snapshots | Source owners; workload/data reads plus explicitly delegated snapshot operations |
| `integration` inside the guest | Recreated application resources | Authorized guest users and workloads |

The operator watches one destination namespace. The protected namespace must differ from destination and source namespaces. Source selection rejects protected/system boundaries; a namespace map cannot route objects into `kube-*` guest namespaces.

## Create an administrator grant

```yaml
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaGrant
metadata:
  name: team-integration
spec:
  targetNamespace: replica-lab
  sourceNamespaces: [source-dev]
  resources:
    - {group: "", kind: ConfigMap}
    - {group: "", kind: ServiceAccount}
    - {group: "", kind: Service}
    - {group: apps, kind: Deployment}
  maxTTL: 2h
  maxObjects: 500
  maxCaptureBytes: 524288
  accessRoles: [viewer]
  maxAccessSeconds: 900
```

`ReplicaGrant` is cluster-scoped. `resources` uses API **kinds**, while RBAC rules use plural API **resources** (`Deployment` versus `deployments`). An empty `group` means the core API. Wildcards are explicit administrator opt-ins; prefer named groups and kinds.

For cluster-scoped inputs, also populate `clusterResources` and install matching read-only ClusterRole rules. Examples include selected CRDs, ClusterRoles, and admission policies. This does not authorize host cluster-scoped writes. Nodes, PVs, API authorization reviews, and reserved control-plane resources are not cloneable inputs.

Secrets, Helm releases, and existing targets each require their own named grants. See [secrets](secrets-storage.md), [operators](operators.md), and [existing targets](existing.md). Volume-data mirrors additionally require exact PVC names and approved snapshot/storage classes in `spec.mirror`; ordinary resource reads and empty-volume permission do not authorize source data capture. See [mirror grants](mirrors.md#grant-access-to-data).

## Let a user or agent create requests

Install a namespaced Role and bind it to your authenticated user, group, or service account. This example defines only request management:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: replicove-requester
  namespace: replica-lab
rules:
  - apiGroups: [replica.nimeshbuilds.dev]
    resources: [clusterreplicas, replicaaccesses]
    verbs: [get, list, watch, create, patch, update, delete]
```

Administrators choose the RoleBinding subjects. Do not grant users write access to `ReplicaGrant`, the protected namespace, or runtime administrative credentials. Request creation is delegated at **namespace scope**, not per-user scope: authorized users in a shared destination namespace can act within that namespace's grants. Separate trust groups accordingly.

For mirror consumers, add this rule to the same Role. They also need the original `clusterreplicas` and `replicaaccesses` rule to resolve the active generation and request credentials:

```yaml
  - apiGroups: [replica.nimeshbuilds.dev]
    resources: [replicamirrors, replicamirrorruns]
    verbs: [get, list, watch, create, patch, update, delete]
```

For example, bind that Role to an existing in-cluster integration agent:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: integration-runner-requests
  namespace: replica-lab
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: replicove-requester
subjects:
  - kind: ServiceAccount
    namespace: ci
    name: integration-runner
```

Mirror request permission does not grant source PVC access, snapshot-controller access, or permission to modify the host's source workloads. The operator performs only operations permitted by the administrator's separate volume-data grant.

Credential retrieval is a separate operation. Configure grant `accessSubjects` so Replicove creates exact-name Secret-get permissions per session, or have an administrator supply that binding. [Access](access.md) describes both host authentication and guest authorization.

## Grant changes

A captured plan pins the grant's UID and resourceVersion. Editing or recreating the grant after capture blocks new mutations and access issuance for that replica. Cleanup continues from protected ownership records. Delete the old request, wait for cleanup, and create a new request to use the updated grant.

## Limits

The grant can reduce TTL, object count, captured byte size, access duration, and available roles. API maxima are 168 hours for a replica, 2,000 planned objects, 716,800 bytes for protected capture storage, and one hour for a session. Defaults and every field are in the [API reference](../reference/api.md). A large cluster is not an unlimited replication target; narrow your selection or split the scenario.

The installed operator has explicit destination permissions, without wildcard `bind`, `escalate`, or `impersonate` bypasses. Contract tests compare them with the pinned vCluster chart. These API restrictions do not isolate shared worker nodes or pod networking; see [Security](../../SECURITY.md).
