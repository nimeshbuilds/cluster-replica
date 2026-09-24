# 07. Run integration tests and collect cleanup-aware results

Use this lab to make environment creation part of a local test command. `replicove run` provisions a fresh replica, waits, obtains bounded access, runs an executable with a private guest `KUBECONFIG`, then waits for cleanup. It works from a terminal or any job runner; a GitHub Action is not required.

## Run

After the [shared prerequisites](index.md#prepare-once):

```bash
./examples/scenarios/run.sh 07
```

This creates a disposable host with the administrator installation, grant and synthetic source already configured. It then invokes the released `replicove run` for several real outcomes. Expected command failures are assertions inside the lab; the outer scenario succeeds only when their cleanup and reports are correct.

## Follow the workflow

1. Prepare the host with enforced Calico policies, Replicove's optional chaos module and a scoped grant. Generate strict local `TestRecipe` documents. These documents embed a request spec, execution/deadline choices and cleanup policy; they are CLI inputs, **not CRDs** to apply with `kubectl`.
2. Run a success recipe containing a `ScaleZero` experiment. The CLI resolves the guest Deployment UID, waits for the fault to become Active and starts an explicit shell command that confirms the guest scale is zero. After the command succeeds, teardown rolls back the fault, revokes credentials and deletes the exact owned request.
3. Run a command that deliberately exits `17`. The test result must be `Failed`, the process must exit nonzero, and cleanup must still be `Verified`. The failure must not modify the source workload.
4. Run a command that sleeps longer than its two-second execution deadline. Its result must be `Interrupted`; the runner terminates the local command and completes cleanup under its separate cleanup deadline.
5. Repeat the exit-17 command with `keepOnFailure: true`. Access and chaos are still cleaned, but the request is retained only until its original 45-minute TTL. The fixture compares the reported deadline with creation time plus that TTL, then explicitly deletes the retained replica and verifies cleanup.
6. After each run, verify the host kubeconfig bytes are unchanged, the source Deployment UID and scale remain intact, sessions/experiments are gone, and no owned runtime Pods/PVCs remain after completed cleanup. Inspect the separate JSON and JUnit reports rather than inferring success from the test's exit code alone.

## Expected result

The command exits zero and prints its test-run evidence directory. Per-run files include `report.json` and `junit.xml`. The expected outcomes are:

| Fixture command | Setup | Test | Cleanup |
| --- | --- | --- | --- |
| Guest scale assertion | Passed | Passed | Verified |
| Deliberate exit 17 | Passed | Failed | Verified |
| Execution timeout | Passed | Interrupted | Verified |
| Retain failed environment | Passed | Failed | Retained until the original TTL, then explicitly deleted by the fixture |

Reports contain identities, captured revision/time, runtime pins, resolved fault references and independent stage outcomes. They omit captured configuration, command arguments, credentials and arbitrary test output. JUnit describes provisioning, command and cleanup stages; it does not replace the test framework's own individual test cases.

## Inspect and adapt

Read the [fixture and generated recipes](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-testrun.sh), [basic recipe](https://github.com/nimeshbuilds/replicove/blob/main/examples/testing/smoke.yaml), [chaos recipe](https://github.com/nimeshbuilds/replicove/blob/main/examples/testing/chaos.yaml) and [mirror recipe](https://github.com/nimeshbuilds/replicove/blob/main/examples/testing/mirror.yaml). See [test runs](../guides/test-runs.md) for all fields and the prerequisites on a persistent installation. [Scenario 05](05-mirror-resets.md) runs that actual mirror recipe end to end, including the acknowledged lease, captured-generation evidence and storage cleanup. [Scenario 09](09-pools-capacity.md) runs a recipe that selects an available destination from a pool.

The test executable runs locally with your normal environment except the temporary `KUBECONFIG`. There is no implicit shell; the fixture explicitly requests one where its command needs shell syntax. Your test must honor that kubeconfig. A repeated recipe creates a fresh capture of current source intent; it is not an archival replay of historical external state.

If setup times out, inspect request conditions, remaining TTL and destination occupancy. If cleanup fails, treat the overall run as failed even when the command passed. Never remove finalizers to manufacture a successful report. The scenario closes temporary sessions and deletes its entire disposable host after recording results.
