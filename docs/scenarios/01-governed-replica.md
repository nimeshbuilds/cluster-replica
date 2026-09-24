# 01. Create, approve and refresh a governed replica

Use this lab when a team wants an integration environment containing selected host configuration, with an approval boundary and a finite lifetime. It exercises the full portable replica lifecycle, including a real expiry rather than only an explicit delete.

## Run

Complete the [shared prerequisites](index.md#prepare-once), then run from the repository root:

```bash
./examples/scenarios/run.sh 01
```

This selects the one-command Helm installer. To test the embedded CLI installer against the same workflow, run:

```bash
./examples/scenarios/run.sh 01 cli
```

Each invocation creates a separate disposable host. No existing cluster, vCluster or application is required.

## Follow the workflow

1. The fixture creates a kind host, a `source-dev` namespace and synthetic application configuration. It installs Replicove with explicit source-read permissions and creates an administrator `ReplicaGrant` for the destination. Real API requests prove that the operator can read delegated inputs but cannot write source ConfigMaps, read system Secrets, create arbitrary namespaces or grant itself unrestricted RBAC.
2. It creates a manually approved `ClusterReplica`. The request reaches `AwaitingApproval`, with **no runtime yet provisioned**. The CLI reads the sanitized plan and approves its current revision. Source namespace mapping, ConfigMap patches and captured Helm value overrides produce a distinct guest configuration.
3. The new persistent vCluster becomes ready. A scoped connection reaches the guest application, and an HTTP probe uses its Service and DNS. Name, `matchLabels` and `matchExpressions` selectors choose the Deployment; its ConfigMap, ServiceAccount, Secret and PVC appear as dependencies. Exclusions win even when a root selector matches, and an unselected ConfigMap stays out. An `EmptyVolumes` request maps the source storage class to `standard`: the source marker is absent from the new guest PVC, guest writes remain independent, and the source volume identity/data are preserved. Explicitly granted Secrets are compared without printing their values.
4. A guest edit to a source-owned field produces reported drift and survives ordinary reconciliation and an operator restart. Replacing the vCluster control-plane Pod preserves guest identity and workload identity, and the CLI tunnel recovers. The fixture changes source configuration, requests refresh, approves the new plan and verifies the source-owned field is restored while an unrelated guest annotation survives. A source-deleted, previously inventoried ConfigMap is pruned.
5. Secret Snapshot and Follow behavior are checked separately: an explicitly registered borrowed target's Snapshot copy retains the previous Secret after the main replica's Follow copy rotates; refreshing the Snapshot request updates it. Viewer credentials can read workloads but cannot deploy. The host reader can retrieve only its issued credential Secret, not enumerate Secrets or read the runtime administrator credential.
6. Explicit deletion revokes access and verifies removal of owned resources, while source objects and a host sentinel remain. The completed guest volume probe stays in place when deletion starts, so owned-namespace teardown must release its PVC protection and remove the recorded application/control-plane PVs. A separate replica then runs through its actual five-minute TTL. Its terminal phase is `Expired`; runtime resources and control-plane storage are gone. The Helm variant additionally exercises installation reuse, key preservation and reinstall after draining requests.

The fixture also checks an existing-target ownership conflict and preservation; [scenario 04](04-existing-vcluster.md) gives that workflow its own complete installation lab.

## Expected result

The command exits zero and prints the replica evidence directory. Its success report records the lifecycle assertions. The live application probe must succeed; a `Ready` status alone is insufficient. Explicit-delete and TTL paths must leave no owned runtime Pods, Services, session Secrets or PVCs among the checked resources. Exact-name credential-reader permissions are revoked.

Manual approval is revision-specific. Refresh reuses the immutable selection and does not extend TTL. Drift describes inventoried configuration differences, not every difference in the host, external database or volume contents. Source snapshots are taken through API reads; this is not an atomic whole-cluster snapshot.

## Inspect and adapt

Read the executable [fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-replication.sh), [base source](https://github.com/nimeshbuilds/replicove/blob/main/test/e2e/source.yaml), [scenario resources](https://github.com/nimeshbuilds/replicove/blob/main/test/e2e/scenario-source.yaml), [workload dependency patch](https://github.com/nimeshbuilds/replicove/blob/main/test/e2e/scenario-echo-patch.yaml), [scenario selection](https://github.com/nimeshbuilds/replicove/blob/main/test/e2e/scenario-replication.yaml), [grant](https://github.com/nimeshbuilds/replicove/blob/main/test/e2e/grant.yaml) and [installation values](https://github.com/nimeshbuilds/replicove/blob/main/test/e2e/replicove-values.yaml). Runtime identities and additional probes generated by the fixture are visible in its source.

For your own granted destination, use the [selection](../guides/selection.md), [plans/refresh](../guides/lifecycle.md), [secrets/storage](../guides/secrets-storage.md) and [access](../guides/access.md) guides. A copied Secret can still authorize access to the original external service; these fixtures use disposable values.

If approval waits unexpectedly, inspect the current phase/revision instead of approving a stale revision. If a required dependency is excluded or not granted, correct the administrator-reviewed request rather than broadening permissions indiscriminately. If storage cleanup blocks, inspect the exact PVC/PV and controller state. The lab deletes its disposable host after saving evidence; [cleanup](../guides/cleanup.md) explains how the same lifecycle differs on a persistent host.
