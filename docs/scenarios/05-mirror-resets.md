# 05. Copy workload data and reset a mirror

Use this lab when an integration test needs a disposable copy of a workload's PVC contents, can modify that copy freely, and later needs to return to source state. It exercises the optional mirror module, adding it to an existing installation, and both new and retained recovery points.

## Run the upgrade variants

Complete the [shared prerequisites](index.md#prepare-once). The default path starts with published Helm 0.2.0-alpha.1, mirroring disabled, and enables it while upgrading to the selected release:

```bash
./examples/scenarios/run.sh 05
```

The other complete paths start from Helm 0.1.0-alpha.1 and native YAML 0.2.0-alpha.1 respectively:

```bash
./examples/scenarios/run.sh 05 previous
./examples/scenarios/run.sh 05 native
```

`current` is the fixture's name for that 0.2.0 starting point; it does not mean an unpinned latest release. Every command runs the enablement checks and the mirror lifecycle in a fresh kind host with pinned Calico and CSI host-path fixtures. It does not modify your current cluster's CNI or CSI driver.

## Follow the workflow

1. Start with an ordinary persistent replica while mirroring is disabled. Change guest configuration and record the runtime/guest IDs, original expiration, state-key UID, destination and installation settings. Install matching target CRDs before upgrading the controller. Enable the module with values and state preserved; the native path replaces its completed bootstrap Job and reapplies native YAML without adopting the installation into Helm.
2. Verify the original replica and its changed guest configuration survive. Existing access remains valid, and a fresh session proves the upgraded operator can decrypt earlier state. Drain this replica deliberately to free the one-runtime destination before starting the mirror.
3. Create a source `orders` workload with a PVC containing `host-A`. An exact-volume grant delegates the approved snapshot and destination storage classes. Creating `ReplicaMirror` captures the source, restores a separate PVC, and starts the guest workload. The guest reads `host-A`; after guest writes `guest-only`, the source still reads `host-A`.
4. Prove network enforcement against a reachable source endpoint. Guest-local application and DNS work, while source egress is denied. Operator RBAC permits delegated snapshot operations but rejects mutation of source workloads/PVCs. An operator restart preserves the active guest experiment.
5. Exercise idempotent manual Sync, saved-revision Reset, scheduled capture/reset, suspension/resumption, lease-protected activation/replacement and candidate cancellation. Fresh Sync captures later source data; Reset uses a retained capture. Managed replacement waits for the lease, retires the previous owned runtime and creates its successor. Queued capacity consumes the original TTL instead of extending it.
6. Register a supported existing runtime and exercise a mirror there. Owned generation namespaces and namespaced guest access remain separate from preexisting resources. Cancellation cleans prepared resources, and a real existing-mirror TTL removes owned generations and access while preserving the external runtime.
7. Delete mirrors and verify owned restored volumes and capture objects disappear. Source PVC/PV identities and data remain. Snapshot-controller upgrade/reuse and reinstall with retained APIs are checked without creating duplicate controllers.
8. Run the published `examples/testing/mirror.yaml` through `replicove run`. It creates a fresh mirror for a local test command, obtains an acknowledged test lease and bounded guest access, then verifies cleanup. The JSON/JUnit results must record the mirror capture and lease as well as successful setup, execution and cleanup.

## Expected result

The command exits zero and prints its mirror evidence directory. Transition evidence records preservation across late enablement; lifecycle evidence records actual copied file content, source isolation, reset behavior, leases, cancellation, access and cleanup. The `mirror-runner` artifact directory adds the real mirror recipe's JSON/JUnit result. The test inspects volume identities and snapshot objects; creating a `VolumeSnapshot` object alone is insufficient.

A mirror reset intentionally discards guest changes. The protected mirror lifetime includes every generation and is not renewed by Sync or Reset. Managed resets have downtime because the pinned runtime permits only one vCluster per host destination namespace. A reset after replacement begins has no automatic rollback to the old running guest if provisioning fails.

## Inspect and adapt

Read the [executable fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-mirror.sh), [late-enable assertions](https://github.com/nimeshbuilds/replicove/blob/main/test/mirror/enable-later.sh), and [complete source/grant/mirror/values files](https://github.com/nimeshbuilds/replicove/tree/main/test/mirror). The [mirror guide](../guides/mirrors.md) describes supported APIs; [enable mirroring later](../guides/enable-mirroring.md) explains values preservation, CRD order and draining before removal.

This lab uses one small filesystem PVC and a test driver. The consistency contract is **per-volume crash consistency**, not coordinated application transactions or a whole-cluster snapshot. EBS, Azure Disk, GCE PD and other drivers need their own qualification. If restore or deletion stalls, inspect driver/controller readiness and the exact owned snapshot/PV; never replace a failed restore with an empty volume or remove finalizers to declare cleanup complete. The exit handler removes the disposable host after saving evidence.
