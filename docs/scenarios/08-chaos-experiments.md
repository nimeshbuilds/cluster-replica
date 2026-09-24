# 08. Inject bounded faults and verify rollback

Use this lab to exercise an application's replica under failure without targeting the source workload. It enables the optional chaos module after a replica already exists, then applies all six supported fault kinds, simultaneous stress and a restricted custom Job.

## Run

With the [shared prerequisites](index.md#prepare-once):

```bash
./examples/scenarios/run.sh 08
```

The fixture creates a new kind host and installs pinned Calico for real host-side network enforcement. It uses disposable source and guest workloads plus an administrator-approved immutable Python image for stress/Job faults.

## Follow the workflow

1. Install Replicove with chaos disabled and create a normal ready replica. Record request, runtime and state-key UIDs. Enable chaos through the same Helm installation; all three identities must remain unchanged. Verify operator RBAC gains the intended destination policy permission and still cannot delete source Pods or create source policies.
2. Use `ScaleZero` against the exact guest Deployment UID. The guest reaches zero while the source remains at one replica. Restart the operator during the experiment and wait for normal duration expiry; encrypted rollback intent must restore the guest's original scale.
3. Submit an experiment using the **source** Deployment UID and another using `kube-system`. Both must be rejected. Correctly naming a workload or namespace does not bypass grant and ownership checks.
4. Apply `PodDelete` to a guest Pod controlled by the inventoried Deployment. Confirm that exact Pod disappears and its controller recovers. Replicove does not recreate a deleted Pod identity.
5. Before `NetworkIsolation`, prove guest-to-source traffic succeeds. During the fault it must fail while the source still serves its own probe; after deletion it must recover. Add a foreign allow policy and submit another network experiment. The request must be rejected while preserving that policy, because additive Kubernetes policy rules cannot be overridden safely by a deny rule.
6. Run `CPUStress` and `MemoryStress` together and wait for Active Jobs. Delete the experiment and verify Job removal. Run and delete a separate `CustomJob` with a constrained sleep command. This demonstrates approved Job execution/cleanup; that custom command itself does not model an application outage.
7. Start another scale fault and delete the parent replica. Its cleanup must settle the experiment before the request disappears. Verify no host fault policies remain and the source retains its identity, scale and response.

## Expected result

The command exits zero and prints the chaos evidence directory. Its report covers late enablement, all six kinds, target/namespace denial, real network loss/recovery, policy conflict preservation, concurrent stress, restart/expiry rollback and parent cleanup. Merely creating an experiment or seeing `Active` would not prove the network behavior checked by this lab.

One experiment supports up to eight simultaneous faults, with one active experiment per replica. The live stress probe uses two concurrent faults; the upper schema/policy bound is not a claim of eight distinct live stress workloads in this scenario.

## Inspect and adapt

Read the [fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-chaos.sh), [grant](https://github.com/nimeshbuilds/replicove/blob/main/test/chaos/grant.yaml), [values](https://github.com/nimeshbuilds/replicove/blob/main/test/chaos/values.yaml) and [source/replication inputs](https://github.com/nimeshbuilds/replicove/tree/main/test/chaos). The [experiment example](https://github.com/nimeshbuilds/replicove/blob/main/examples/chaos/experiment.yaml) and [custom Job example](https://github.com/nimeshbuilds/replicove/blob/main/examples/chaos/custom-job.yaml) show reusable structures. Substitute actual current replica/guest UIDs when using the CRD directly.

The [chaos guide](../guides/chaos.md) explains scope and rollback. Job faults also isolate the **entire selected guest namespace**; put them in a separately replicated/granted namespace when application networking must remain available. Network/Job faults reject overlapping host allow policies, including mirror or prepared-database baselines. Eligible application `PodDelete`/`ScaleZero` remain the narrower options there.

This is bounded workload chaos on shared workers. It does not authorize privileged node/kernel/cloud faults or arbitrary chaos-engine manifests. Rollback needs the operator and relevant APIs available; concurrent changes to an owned scale field block recovery instead of being overwritten. Inspect blocked ownership/scale conditions, preserve the encryption key, and keep the module enabled until experiments finish cleanup. The lab saves evidence and removes its named kind host on exit.
