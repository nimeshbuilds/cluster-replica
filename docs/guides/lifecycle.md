# Plans, refresh, and drift

A replica request has an immutable spec and a time-bounded lifecycle. The operator validates its grant, captures eligible source resources, builds a dependency plan, optionally waits for approval, provisions/connects the runtime, applies resources, and evaluates readiness.

## Inspect status and the plan

```bash
kubectl -n replica-lab get clusterreplica demo -o yaml
kubectl -n replica-lab get clusterreplica demo -o jsonpath='{.status.plan}'
```

`status.plan` includes its revision, capture time, object/package counts, resource identities, and dependency references. It deliberately omits captured values and credentials. CLI `plan` and `status` currently print this same sanitized status object; they are not a full payload diff viewer.

`status.runtime` records the runtime release and exact profile/chart/guest version. `status.sourceVersion` and `status.targetVersion` record discovered Kubernetes versions. `status.conditions` provides the actionable reason/message when the workflow cannot continue.

## Manual approval

Set `spec.approval: Manual` when creating the request. No runtime provisioning occurs before its first approved plan. With `Automatic` (the default), the operator proceeds after planning without a separate approval.

```bash
kubectl -n replica-lab wait clusterreplica/demo \
  --for=jsonpath='{.status.phase}'=AwaitingApproval --timeout=120s
kubectl -n replica-lab get clusterreplica demo -o yaml
revision=$(kubectl -n replica-lab get clusterreplica demo -o jsonpath='{.status.plan.revision}')
kubectl -n replica-lab annotate clusterreplica demo \
  "replicove.nimeshbuilds.dev/approved-plan=$revision" --overwrite
kubectl -n replica-lab wait clusterreplica/demo --for=condition=Ready --timeout=420s
```

The annotation must equal the current keyed revision. Approval of an older plan does not authorize a newly captured revision. Review status before recording approval; for a CI approval gate, ensure the revision has not changed between review and annotation.

## Refresh source configuration

Ordinary reconciliation reports drift and verifies readiness; it does not repeatedly overwrite your guest experiments. Explicitly change the refresh annotation to request a new capture:

```bash
kubectl -n replica-lab annotate clusterreplica demo \
  "replicove.nimeshbuilds.dev/refresh=$(date +%s)-$$" --overwrite
```

Each refresh token must differ from the last processed token. In automation, use a unique run ID or UUID. A manual replica returns to `AwaitingApproval`; inspect and approve the new revision. Refresh applies changes to fields owned by the source, preserves unrelated fields added in the guest, and prunes inventoried objects removed from the source plan. Ownership checks reject name reuse or foreign replacements.

`status.driftCount` indicates tracked differences. It is not a general cluster drift scanner, and it does not compare external databases, volume contents, or every untracked guest object.

## Change a request's shape

`ClusterReplica.spec` is immutable, including TTL, profile, selectors, grant, and overrides. Create a new request to change those choices. Refresh uses the existing choices against new source state; it does not change the spec or reset the TTL.

Editing the grant after capture is also not an in-place authorization expansion. It blocks new changes under the previous capture. Delete and recreate the request under the revised grant after cleanup.

## Expiration

TTL starts at creation, including planning and provisioning time. The operator begins cleanup on expiration or deletion. Expired requests retain terminal status after their runtime/state is removed; deleting the request also removes its Kubernetes object after finalizers complete. Read [cleanup](cleanup.md) for failure and recovery behavior.
