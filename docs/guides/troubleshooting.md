# Troubleshooting

Start with the request's conditions and observed state. A blocked replica is not necessarily a crashed operator; it often means a required permission, dependency, or safety condition is missing.

```bash
kubectl -n replica-lab get clusterreplica demo -o yaml
kubectl -n replica-lab get replicaaccesses
kubectl -n replicove-system logs deployment/replicove --tail=150
kubectl -n replica-lab get pods,pvc,services
```

Check the host context before running commands. Avoid dumping Secrets or kubeconfigs into logs or public issues.

## Installation problems

| Symptom | Check / next action |
| --- | --- |
| Operator `ImagePullBackOff` | The default registry tag is a placeholder. Build/load the demo image or use your own reachable pushed tag. Check image pull credentials and architecture. |
| Bootstrap Job fails | Read `kubectl -n replicove-system logs job/replicove-bootstrap`. Confirm Secret get/list/create permissions. An invalid existing key must be restored deliberately, not overwritten. |
| State exists but key is missing | Restore the original key and encrypted records from the same backup. Re-running bootstrap cannot recover lost encryption material. |
| Job update reports an immutable template | After confirming it is completed, remove only `replicove-bootstrap` and reapply the reviewed installer. Preserve the key. |
| “No matches for kind ClusterReplica” | Apply `config/crd/` and wait for CRDs to be established before requests. |
| Persistent vCluster PVC is Pending | Check the default StorageClass, provisioner, quota, scheduling and capacity. The profile requests 1 GiB for control-plane state. |

## Planning and replication problems

| Symptom / reason | Check / next action |
| --- | --- |
| `GrantChanged` | Delete and recreate the request after cleanup under the revised administrator grant. |
| Source reads denied | Check both host source RBAC and grant kinds/namespaces. A grant cannot create API permissions. |
| Missing or excluded dependency | Include/grant its known dependency, or adjust the source application and create a new request. |
| Secret not selected | Check the exact Secret grant and `Snapshot`/`Follow`; token and bootstrap Secret types remain excluded. |
| Capture/object limit exceeded | Narrow the selection or split tests. Increase grant limits only within API bounds and measured capacity. |
| Chart lifecycle hook blocks capture | Disable only a documented optional hook with the correct chart value. Unsupported required hooks need an adapter. |
| `AwaitingApproval` | Inspect `status.plan`, then approve exactly `status.plan.revision`. Refresh requires a new approval. |
| `PlatformQualificationRequired` | Platform provisioning is not qualified. Use an administrator-approved owned Helm runtime or registered existing target where policy permits. |
| `ProfileMismatch` / target identity conflict | Restore the intended runtime/credential and inspect the pinned UID. Do not redirect the request to another cluster under the same name. |
| Custom resource never Ready | Check operator Deployment/CRD readiness and the configured guest condition/status-field check. |

## Access problems

A session needs a Ready replica, the correct current replica UID, a granted role, and sufficient remaining lifetime. Create it with 600–3,600 seconds, within the administrator limit. After a replica is recreated, old UIDs must not be reused.

`Forbidden` while retrieving the session Secret means the host caller lacks exact-name read permission. Check `accessSubjects` or the administrator-created binding. Do not solve it by granting access to every Secret in the destination namespace.

A local TLS error often means the endpoint was changed without retaining the guest's CA or TLS server name. The CLI manages these fields. For manual port-forwarding, update only the server address in the issued kubeconfig. A refused localhost connection usually means the forward stopped or the selected port is occupied.

For existing targets, the operator and consumer both need appropriate routes. CLI `connect` is for owned runtimes; use `access` with an existing target's configured endpoint.

## Deletion appears stuck

Inspect the request's condition reason, guest API availability, protected state, workload finalizers, and relevant PVC/PV status. Keep the operator running. `Retain` reclaim behavior, an unreachable target, missing state, or a reused foreign UID can prevent verification. See [cleanup recovery](cleanup.md#recover-a-blocked-cleanup).

## Report a reproducible problem

Include the commit, host Kubernetes version, profile/guest version, installation method, sanitized request/grant YAML, and condition reason. For a CI failure, link the failing job. Remove credentials, source Secret values, connection material, and private endpoints before sharing. Use [Support](../../SUPPORT.md) for bugs/questions and [Security](../../SECURITY.md) for vulnerabilities.
