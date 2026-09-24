# Chaos experiments

Replicove can run bounded, explicitly granted faults against a replica. A `ReplicaExperiment` identifies the **exact replica UID**, guest namespaces, and workload UIDs. Several faults can run together. The operator records intent and original scale counts in encrypted state before applying any effect, and keeps a cleanup finalizer until reversible effects have been removed.

Use experiments with [one-command test runs](test-runs.md), or submit their YAML directly. Replicove injects faults; your test suite decides whether the application behaved correctly.

## Available faults

| Kind | Effect | Recovery and constraints |
| --- | --- | --- |
| `PodDelete` | Deletes one exact guest Pod once. | Only Pods controlled by an inventoried Deployment or StatefulSet qualify. The workload controller creates a replacement; Replicove does not resurrect the deleted Pod. |
| `ScaleZero` | Sets an inventoried guest Deployment or StatefulSet to zero replicas. | Restores the recorded original replica count on expiry, cancellation, failure, or parent cleanup. Concurrent scale or ownership changes block rollback rather than being overwritten. |
| `NetworkIsolation` | Adds a host NetworkPolicy selecting only the managed runtime's Pods in the chosen guest namespace. | Deletes its own policy during cleanup. Requires a qualified host CNI and no conflicting host allow policies. |
| `CPUStress` | Runs a restricted Python Job with a busy loop. | Administrator-approved immutable image must contain `python3`. CPU and memory requests equal limits. |
| `MemoryStress` | Runs a restricted Python Job allocating half its memory limit. | Remaining memory is reserved for interpreter overhead. This is bounded container memory pressure, not node-memory exhaustion. |
| `CustomJob` | Runs an explicit command in an administrator-approved immutable image. | Restricted, tokenless, bounded Job. Arbitrary Kubernetes manifests, privileged containers, volume mounts, and host access are not accepted. |

`CPUStress`, `MemoryStress`, and `CustomJob` also isolate the **entire selected guest namespace** on the host before starting the Job. Account for this additional network fault when interpreting results. To keep application traffic running, place stress/custom Jobs in a separately replicated and explicitly granted namespace.

A request can contain one to eight simultaneous faults, with one active experiment per replica. Sequential fault orchestration and arbitrary node/kernel/cloud faults are not implemented. Shared-worker vClusters do not provide a safe boundary for host reboots, disk corruption, privileged daemons, or unrestricted Chaos Mesh/Litmus resources.

## Enable the optional module

For a new installation, add these values to the normal [Helm installation](../getting-started/installation.md):

```yaml
chaos:
  enabled: true
  # Set this only after qualifying NetworkPolicy enforcement on the host CNI.
  networkPolicyEnforced: true
```

`PodDelete` and `ScaleZero` work with `networkPolicyEnforced: false`. Network and Job faults are rejected in that mode.

You can add the module later. Use matching operator, chart, CLI, and CRD versions; update **all** Replicove CRDs before the operator. Then upgrade the existing Helm release with its existing values, state key, and destination preserved, enabling `chaos.enabled`. Follow the values-preserving upgrade and completed-bootstrap-Job instructions in [enabling an optional module later](enable-mirroring.md). The chaos module needs no snapshot controller or CSI dependency. Native YAML/GitOps installations must render the matching chart with these values so the controller flag and destination RBAC are installed together. A Deployment flag alone is insufficient.

The module does not automatically create experiments or change existing workloads. An administrator must also grant the specific namespace, fault kinds, duration, images, and resource budget. Changing an existing `ReplicaGrant` changes its captured delegation; create a fresh replica under the updated grant instead of bypassing that identity check.

## Delegate a bounded experiment scope

Merge the following into the source `ReplicaGrant` before creating a replica. The namespace names below are **guest** namespaces after namespace mapping:

```yaml
spec:
  chaos:
    namespaces: [integration]
    kinds: [PodDelete, ScaleZero, NetworkIsolation, CPUStress, MemoryStress, CustomJob]
    maxDurationSeconds: 300
    images:
      - docker.io/library/python@sha256:79e7a9b9ff1cbceff819f856fb374477792a5967759d94df266de7b7b4120e6f
    maxCPUMilli: 500
    maxMemoryMiB: 256
```

The example image is an immutable Python image used by the disposable test fixture. Administrators should approve images through their own image policy. An image tag is not enough: the exact `name@sha256:digest` must appear in the grant. Image, command, CPU and memory fields are accepted only for Job faults. Resource budgets apply to the **sum** of Job faults in one experiment, not to every experiment in the host cluster; use destination ResourceQuotas for aggregate capacity controls.

Guest namespaces must also have been created and exclusively inventoried by the selected replica. Pre-existing guest namespaces, `default`, and Kubernetes system namespaces are rejected. `ScaleZero` targets must match encrypted object inventory and live ownership markers. `PodDelete` traces a Pod's controller UIDs back to an inventoried workload. Naming an arbitrary object in the namespace does not grant permission to mutate it.

## Create from YAML or the CLI

Obtain identities through the host context and a [guest access session](access.md):

```bash
kubectl -n replica-lab get clusterreplica demo -o jsonpath='{.metadata.uid}'
kubectl --kubeconfig ./guest.kubeconfig -n integration get deployment echo \
  -o jsonpath='{.metadata.uid}'
```

Insert those values into a manifest:

```yaml
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaExperiment
metadata:
  name: outage
  namespace: replica-lab
spec:
  replicaRef:
    name: demo
    uid: REPLACE_WITH_REPLICA_UID
  durationSeconds: 60
  faults:
    - kind: ScaleZero
      namespace: integration
      target:
        kind: Deployment
        name: echo
        uid: REPLACE_WITH_GUEST_DEPLOYMENT_UID
```

```bash
kubectl apply -f experiment.yaml
# Equivalent creation through the CLI:
# replicove chaos create outage --file experiment.yaml

kubectl -n replica-lab wait replicaexperiment/outage \
  --for=jsonpath='{.status.phase}'=Active --timeout=2m
replicove chaos status outage
```

The spec is immutable. Create another request to change a fault or duration. The controller bounds duration to the administrator maximum, at most 900 seconds, and refuses requests that would outlive their replica. Duration starts when the protected intent is accepted; Job startup time consumes that duration. `Active` means the actions have been applied and Job containers are running or a custom Job has already succeeded. It does not certify traffic loss, desired application impact, or application recovery. A host CNI's treatment of existing connections can vary.

Coordinate `ScaleZero` with autoscalers and other reconcilers first. If an HPA, GitOps controller, manual refresh, or user changes the same scale field during an experiment, Replicove preserves that change and reports blocked rollback instead of competing for ownership. Expiry before a Job ever starts is reported as `Failed`, not as a successful injection.

Custom faults use the same CRD and cleanup lifecycle:

```yaml
faults:
  - kind: CustomJob
    namespace: integration
    image: docker.io/library/python@sha256:79e7a9b9ff1cbceff819f856fb374477792a5967759d94df266de7b7b4120e6f
    command: [python3, -c, 'import time; time.sleep(45)']
    cpuMilli: 100
    memoryMiB: 32
```

This sleep command demonstrates the restricted Job mechanism; it injects no application fault by itself. Supply an approved command that exercises the behavior you intend to test. Jobs use a dedicated ServiceAccount without an API token, non-root execution, dropped capabilities, a read-only root filesystem, no volume mounts, no retries, and a deadline. No production credentials or mounted storage are injected into custom Jobs.

## Network isolation and mirrors

The pinned managed vCluster profile does **not** sync guest NetworkPolicies to the host. Replicove therefore creates policies directly in its granted **host destination namespace**, with exact runtime and guest-namespace selectors. The source namespace and vCluster control-plane Pods are outside those selectors. Turning on guest NetworkPolicy sync can weaken other host isolation policies and is not part of this feature.

[Kubernetes NetworkPolicies are additive](https://kubernetes.io/docs/concepts/services-networking/network-policies/): a deny-all policy cannot cancel another policy's allow rules. This adapter conservatively rejects network and Job faults if **any host allow policy** exists in the destination namespace. It never deletes or edits those policies. Existing-target providers are not qualified for network/Job faults; the current adapter requires the managed Helm runtime.

A mirror's baseline policy deliberately allows selected peer/DNS/API traffic. Consequently, network, CPU, memory and custom Job faults currently reject that conflicting baseline. The same conservative rejection applies when a prepared database's host policy allows application traffic. `PodDelete` and `ScaleZero` remain available for eligible application workloads. Do not remove baseline isolation to bypass this restriction. Hold the active mirror generation during a test with `replicove mirror hold`; the one-command runner handles its lease. Resetting or expiring a mirror invokes experiment cleanup before replacing its runtime.

## Stop, expire, and deprovision

```bash
replicove chaos delete outage
kubectl -n replica-lab wait replicaexperiment/outage --for=delete --timeout=3m
```

Explicit deletion, experiment duration, parent TTL/deletion, and grant revocation all initiate rollback. Parent cleanup waits for experiment finalizers before removing the guest. Job cleanup verifies both guest resources and delayed physical fault Pods before removing the host isolation policy. Unrelated host policies and guest workloads are preserved.

Normal expiry retains a small completed `ReplicaExperiment` and encrypted outcome record for inspection. Delete the request to remove that record. Parent cleanup deletes its experiment requests automatically. This complements the existing [replica and mirror deprovisioning lifecycle](cleanup.md); it does not replace it.

Scale and network effects require a healthy operator and reachable host/guest APIs for rollback. Job deadlines are also enforced by Kubernetes, but an operator outage can delay scale/network restoration beyond the requested duration. On restart, the operator resumes from encrypted intent. `Blocked` means cleanup could not be proven, for example because an administrator replaced a workload UID, changed its scale during the fault, or removed protected state. Restore availability or the recorded expected ownership/scale, then let the controller retry. Never remove a finalizer or the encryption key to hide unfinished cleanup.

Before disabling `chaos.enabled` or uninstalling Replicove, delete all experiments and wait for their finalizers. Disabling the module while experiments exist leaves parent cleanup waiting until the module is re-enabled.

## Validation scope

`internal/chaos` tests cover immutable identity checks, controller-owned Pod selection, lost responses, restarts, grant revocation, concurrent experiments, partial multi-fault failure, restricted Job construction, startup readiness, foreign-object preservation, and cleanup after parent deletion. The disposable `hack/e2e-chaos.sh` fixture uses a real vCluster and Calico to exercise all six fault kinds, verify source connectivity is blocked only from the replica during network faults, and check recovery and cleanup.

A passing disposable fixture qualifies that tested runtime/CNI combination. It does not establish support for every cloud CNI, arbitrary existing vCluster, privileged chaos engine, database consistency property, or real-world application's resilience. See the release's [validation evidence](../validation.md) for tests actually completed for that revision.
