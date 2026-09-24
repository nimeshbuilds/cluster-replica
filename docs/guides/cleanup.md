# TTL and cleanup

Replicove records resource ownership before and after guest mutations. Deletion and TTL use those records to revoke sessions and remove resources it owns. It does not infer ownership from a name alone.

## Lifetime

`spec.ttl` begins at `metadata.creationTimestamp`, including time spent planning, waiting for approval or capacity, and provisioning. Use whole minutes or hours (`30m`, `2h`), from 5 minutes to 168 hours, within the grant's smaller limit. The spec is immutable; refresh does not renew the TTL.

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

1. Persist cleanup intent, stop issuing access and settle owned experiments. Roll back supported faults and verify physical Job Pods have stopped before removing their host isolation policies.
2. Revoke session Secret permissions and guest identities; remove session credentials/state.
3. Remove inventoried guest resources while the API is available. Database cleanup removes staging/final resources and waits for their translated host Pods to disappear before removing database-specific isolation policies. The application egress guard remains in place.
4. Remove the owned runtime's Helm release. Preserve an externally managed target runtime.
5. Follow recorded owner-reference UIDs for host descendants and verify relevant PVC/PV deletion. Remove the database application egress guard only after the runtime and its host Pods are gone.
6. Persist terminal status, remove encrypted captures, release the capacity reservation and finish the request finalizer.

UID and operation checks stop deletion when a name has been reused or ownership changed. Unrelated host and source resources remain untouched. Guest-resource cleanup preserves any pre-existing guest namespace that Replicove reused without adoption; an owned runtime is removed only after that cleanup completes.

## Guest-created volume consumers

**Version note:** the following cleanup progress change is included in published **v0.3.0-alpha.2**, which passed its exact-source and release checks. Published v0.3.0-alpha.1 can stall when a guest-created Pod, including a completed test Pod, still references a copied PVC: cleanup waits for the claim before requesting deletion of the owned namespace that would remove its consumer. The [validation record](../validation.md#owned-pvc-cleanup-regression) records the failing regression scenarios.

An exclusively owned guest namespace is part of the replica's disposable scope. Deleting that namespace also removes workloads users created there after replication, including extra Pods using its copied volumes. The alpha.2 fix lets cleanup continue past a pending ordinary PVC only when its exact guest namespace is itself in protected ownership inventory, so namespace deletion can release those consumers. Cleanup still waits until all inventoried resources are absent and the supported host/PV checks finish. It does not remove Kubernetes PVC protection or force volume deletion.

Pre-existing guest namespaces are reused without adoption. This includes `default` in a newly managed guest as well as namespaces borrowed from an existing target: Replicove did not create or inventory them and does not delete them during guest-resource cleanup. If a foreign Pod in such a namespace uses a replica-owned PVC, cleanup stays pending until that namespace's administrator removes or reconfigures the consumer. Merely using a managed runtime does not make every guest namespace owned. Existing-target mirrors create their own generation namespaces and follow the owned-namespace contract within that scope.

Experiment rollback, access revocation and data-adapter cleanup still precede ordinary guest cleanup. Pending namespaces, custom resources/controllers and PVCs with nonstandard finalizers retain their dependency ordering. Only ordinary Kubernetes PVC protection and foreground-deletion finalizers permit progress to owned-namespace deletion; UID or ownership-marker conflicts remain errors. A namespace is not a fallback for bypassing a known ownership conflict. Refresh continues to preserve namespaces and does not use teardown to discard guest experiments.

## Storage and external systems

For supported bound volumes, cleanup requires `Delete` reclaim behavior and waits for the recorded PV UID to disappear. A Retain policy, blocked finalizer, or unavailable CSI controller can stop completion.

The optional mirror module waits for deletion of its recorded restored PVs and source `VolumeSnapshot`/`VolumeSnapshotContent` objects before completing cleanup. Imported snapshot references use Retain so retiring one generation cannot delete a retained capture; the original owned capture uses Delete.

Deleting Kubernetes objects does not prove physical erasure of provider backups, external databases, object buckets, or application-created external infrastructure. Those capabilities require qualified adapters and are outside the portable contract. Source data is never deleted by replica cleanup.

## Test runs, experiments and capacity

The [test runner](test-runs.md) waits for fault rollback, access revocation, mirror lease release and exact-UID request cleanup. A local command failure and a cleanup failure are separate report outcomes. `keepOnFailure` can retain an environment only after the test command fails, and only until its original TTL; cancellation still cleans up.

Experiment cleanup can block replica deletion while rollback or physical Job cleanup is unresolved. Keep the chaos module and its host permissions available. Database cleanup similarly depends on its journal and host policy/Pod permissions. Do not disable optional modules with outstanding finalizers.

A capacity reservation is released only after verified cleanup. The empty protected capacity ledger is deliberately retained for concurrency control; it is not a leaked workload. The operator installation and state key also survive normal replica cleanup.

## Recover a blocked cleanup

Inspect the request's conditions, operator logs, guest reachability, and relevant storage/finalizer state. Restore the original protected key and encrypted state together if they were lost. Restore target credentials or connectivity if those are the problem. Let the responsible workload/storage controller finish its finalizers.

Replicove intentionally retains finalizers when ownership or cleanup cannot be verified. Force-removing a finalizer discards that safety boundary and can orphan resources; it is not a normal uninstall procedure.

## Remove the operator

First stop/delete active `ReplicaExperiment` requests and wait for rollback. Delete all `ClusterReplica` and `ReplicaMirror` requests in the watched namespace, allow their `ReplicaMirrorRun` work to settle, and verify `ReplicaAccess` cleanup. Keep the operator and protected state available until that completes.

Delete `ReplicaMirror` requests and wait for their finalizers before removing the mirror module or source snapshot permissions. An expired mirror keeps its terminal status but removes its owned data and access; it does not extend the TTL for an active test lease. See [mirror cleanup and retention](mirrors.md#retention-deletion-and-ttl).

If this release installed the shared snapshot controller, check for other consumers before disabling the module or uninstalling it. They need a functioning administrator-managed replacement or completed snapshot lifecycles first. Retained snapshot CRDs do not run a controller. See [module removal and recovery](enable-mirroring.md#recovery-and-disabling).

For Helm, uninstall the existing release only afterward. The key is retained by the chart. For YAML, remove the operator Deployment, completed bootstrap Job, and associated RBAC/service accounts after cleanup. The generated installation also contains namespace resources: **do not run a blanket `kubectl delete -k` on a shared cluster**. Delete namespaces or CRDs only after an administrator has confirmed they contain no resources that need preservation. Keep or securely destroy backed-up key/state together according to your retention requirements.

For the disposable kind walkthrough, deleting its uniquely named kind cluster after verified replica cleanup removes the entire test host.
