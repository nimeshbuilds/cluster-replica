# 09. Share a bounded pool of test destinations

Use this lab when multiple developers or agents need integration environments without granting them arbitrary host namespace creation. It renders two independent Replicove installations, admits one runtime per destination, and sends a test run to the less occupied member.

## Run

After the [shared prerequisites](index.md#prepare-once):

```bash
./examples/scenarios/run.sh 09
```

This runs the same combined live fixture used by [scenario 10](10-agents-observability.md). A single host provides both real pool members and the scoped agent identity used for the interface checks. Running 10 later repeats the whole fixture in a new disposable host.

## Follow the workflow

1. Create a synthetic source Deployment and ConfigMap. Use `replicove pool render` with a local `ReplicaPool` document to emit shared CRDs once and native manifests for destination/operator pairs `test-a` / `replicove-a` and `test-b` / `replicove-b`. Apply the result; both bootstrap Jobs and operators must become ready, each with separate protected state.
2. Create one explicit grant per destination, both authorizing the intended source but setting `maxConcurrentReplicas: 1`. The rendered members include ResourceQuota and LimitRange values. A real API-server dry-run of an oversized Pod must fail specifically for `exceeded quota`; no Pod is created by that probe.
3. Create a replica in A and wait for its real guest to become Ready. The scoped MCP exercise creates a second request in A. It must queue without getting a second runtime. Restart A's operator; the capacity-ledger UID, existing runtime and queued state must persist.
4. Run the pool `TestRecipe` without an explicit namespace. It lists only its configured namespace/grant pairs, observes A's two requests and chooses empty B. The local command waits for the guest Deployment rollout. Its report must name `test-b`, show Passed setup/test and Verified cleanup, and record its plan/runtime evidence.
5. Delete A's first replica and wait for verified cleanup. Only then may the immutable queued request acquire capacity and become Ready with its own runtime. Delete that request and verify both destinations are drained.
6. Confirm no checked replica/access/runtime/PVC resources or PVs remain. Administrator-owned quotas, limit ranges, state keys and installations must remain, and the original source configuration and host kubeconfig bytes must be unchanged. The final kind-host deletion removes the complete lab.

## Expected result

The command exits zero and prints the shared pool/interface evidence directory. `report.json` includes pool installation, real quota admission, restart-safe queuing, placement, command execution, capacity release and preservation assertions. `pool-run/report.json` and `pool-run/junit.xml` describe the actual test run in B. The interface checks also run and are explained in [scenario 10](10-agents-observability.md).

A pool is an explicit administrator-owned list, not an autoscaler or a new CRD. Its document is rendered locally; grants remain separate. Selection is a least-occupied hint, not an atomic reservation: two clients can choose the same member and one may wait. Operator admission enforces the slot, and queueing consumes the original TTL. A ResourceQuota limits admitted requests independently; it does not prove that a particular workload can be scheduled.

## Inspect and adapt

Read the [combined executable fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-interfaces.sh), [pool document](https://github.com/nimeshbuilds/replicove/blob/main/test/scenarios/interfaces/pool.json), [member grants](https://github.com/nimeshbuilds/replicove/blob/main/test/scenarios/interfaces/grants.yaml), [replication input](https://github.com/nimeshbuilds/replicove/blob/main/test/scenarios/interfaces/replication.json) and [pool test recipe](https://github.com/nimeshbuilds/replicove/blob/main/test/scenarios/interfaces/recipe.yaml). The published CLI's embedded chart renders these native installations; the harness verifies chart-version parity and pins the operator image.

The [pool guide](../guides/pools.md) explains required budgets, unique namespaces, per-member source RBAC and updates. A native rendered installation stays native during upgrade; do not convert it into Helm ownership. For pools with mirrors, shared snapshot-controller ownership requires explicit configuration and is covered by the mirror guide, not by this non-mirror pool fixture.

If a request queues, inspect current owners and wait for their finalizers instead of removing a capacity record. If a Pod is Pending, check quota, LimitRange defaults and actual host resources. Size budgets for the guest control plane and its synchronized workloads. Normal replica deletion preserves pool infrastructure; remove a pool member only after it is drained and removed from recipes.
