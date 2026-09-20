# TTL and cleanup

Replicove records resource ownership before and after guest mutations. Deletion and TTL use those records to revoke sessions and remove resources it owns. It does not infer ownership from a name alone.

## Lifetime

`spec.ttl` begins at `metadata.creationTimestamp`, including time spent planning, waiting for approval, and provisioning. Use whole minutes or hours (`30m`, `2h`), from 5 minutes to 168 hours, within the grant's smaller limit. The spec is immutable; refresh does not renew the TTL.

On expiry, the operator cleans up and retains the request with terminal `Expired` status. On explicit deletion, its finalizer performs cleanup before the Kubernetes request disappears:

```bash
kubectl -n replica-lab delete clusterreplica demo --wait=true --timeout=360s
```

If your client times out, the controller may still be cleaning up. Inspect conditions and the runtime before retrying or changing infrastructure.

## Cleanup policies

| Policy | Contract |
| --- | --- |
| `DeleteOwned` | Required for replication and existing targets; revoke access, remove inventoried guest additions, and remove an owned runtime with verified host descendants |
| `HelmReleaseOnly` | Legacy runtime-only mode; removes the Helm release, without the full replication/owned-cleanup contract |

New full-workflow examples use `DeleteOwned`. Do not use the legacy `config/samples/replica.yaml` as a complete replication request.

## Cleanup order

1. Persist cleanup intent and stop issuing new access.
2. Revoke session Secret permissions and guest identities; remove session credentials/state.
3. Remove inventoried guest resources in reverse dependency order while the guest API is still available.
4. Remove the owned runtime's Helm release. Preserve an externally managed target runtime.
5. Follow recorded owner-reference UIDs for host descendants and verify relevant PVC/PV deletion.
6. Persist terminal status, remove encrypted captures, and finish the request finalizer.

UID and operation checks stop deletion when a name has been reused or ownership changed. Unrelated host resources, source resources, and preexisting existing-target namespaces remain untouched.

## Storage and external systems

For supported bound volumes, cleanup requires `Delete` reclaim behavior and waits for the recorded PV UID to disappear. A Retain policy, blocked finalizer, or unavailable CSI controller can stop completion.

Deleting Kubernetes objects does not prove that cloud backups, snapshots, external databases, object buckets, or application-created external infrastructure are erased. Those capabilities require qualified adapters and are outside the portable contract. Source data is never deleted by replica cleanup.

## Recover a blocked cleanup

Inspect the request's conditions, operator logs, guest reachability, and relevant storage/finalizer state. Restore the original protected key and encrypted state together if they were lost. Restore target credentials or connectivity if those are the problem. Let the responsible workload/storage controller finish its finalizers.

Replicove intentionally retains finalizers when ownership or cleanup cannot be verified. Force-removing a finalizer discards that safety boundary and can orphan resources; it is not a normal uninstall procedure.

## Remove the operator

First delete all replica requests in its watched namespace and verify their access requests have been cleaned. Keep the operator and protected state available until that completes.

For Helm, uninstall the existing release only afterward. The key is retained by the chart. For YAML, remove the operator Deployment, completed bootstrap Job, and associated RBAC/service accounts after cleanup. The generated installation also contains namespace resources: **do not run a blanket `kubectl delete -k` on a shared cluster**. Delete namespaces or CRDs only after an administrator has confirmed they contain no resources that need preservation. Keep or securely destroy backed-up key/state together according to your retention requirements.

For the disposable kind walkthrough, deleting its uniquely named kind cluster after verified replica cleanup removes the entire test host.
