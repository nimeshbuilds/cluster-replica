# 04. Reuse a vCluster that already exists

Use this lab when an administrator already runs vCluster and wants Replicove to manage selected application copies inside it. The fixture deliberately installs vCluster **before** Replicove, then proves that replication, operator upgrades and replica cleanup preserve that runtime and unrelated resources.

## Run

After the [shared prerequisites](index.md#prepare-once):

```bash
./examples/scenarios/run.sh 04
```

The test creates both the preexisting runtime and its host in its own disposable Docker environment. You do not supply credentials for an actual shared cluster.

## Follow the workflow

1. Install the pinned vCluster independently into host namespace `preexisting`. Record its StatefulSet UID and guest `kube-system` namespace UID. Create an `integration` namespace inside it with a foreign ConfigMap that Replicove must preserve.
2. Create the destination namespace before the Replicove installation, label it as fixture-owned, and add a host sentinel. Create a selected source ConfigMap in `source-dev`.
3. Install Replicove in one Helm command with explicit source reads. Check that the chart did not adopt the preexisting destination namespace as its own Helm resource. Store a data-only guest kubeconfig in the protected operator namespace; its Service route is reachable from the operator and TLS is verified.
4. Register the target by name in `ReplicaGrant.existingTargets`, referencing the protected Secret and exact guest UID. Create a `ClusterReplica` with `target.provider: existing`. Map only the selected source configuration into the precreated guest namespace.
5. Verify the selected ConfigMap's value in the guest and that Replicove created no managed runtime in its destination. Reapply/upgrade the Replicove installation and verify the same state-key UID survives.
6. Delete the replica and wait for its cleanup. The copied ConfigMap disappears; the foreign ConfigMap, guest identity and original vCluster StatefulSet all retain their UIDs. Uninstall the drained Replicove release and check preservation of the state key, destination namespace, host sentinel and source ConfigMap.

## Expected result

The command exits zero and prints the existing-target evidence directory. The report records that vCluster predates the operator, selected configuration was copied, and cleanup preserved source, guest and unrelated host identities. It also verifies that installation upgrade/uninstall did not rotate the key or adopt the administrator-owned destination namespace.

Existing targets require explicit administrator registration. Replicove does not discover arbitrary vClusters and assume authority over them. A name collision with a foreign guest object blocks adoption; [scenario 01](01-governed-replica.md) also exercises that conflict path. TTL applies to the replica's additions and sessions, not to the externally managed runtime's lifetime.

## Inspect and adapt

The [complete fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-helm-existing.sh) generates its trusted kubeconfigs, grant and request after reading real runtime identities. Inspect the [Helm installation helper](https://github.com/nimeshbuilds/replicove/blob/main/test/e2e/helm.sh) for the release-artifact installation path.

Follow [existing targets](../guides/existing.md) on your own administrator-approved runtime. Create intended guest namespaces first, pin its `kube-system` UID and maintain the connection until cleanup completes. Kubeconfigs using exec plugins, local file references, impersonation or insecure TLS are rejected. A localhost address on your workstation is not reachable from the operator Pod.

`replicove connect` manages local tunnels only for owned runtimes; existing-target consumers use issued access credentials over the administrator's route. This fixture uses an explicit independent port-forward for its verification. If connection or identity validation fails, correct the registered route/credential/UID instead of removing finalizers or forcing target adoption. The final kind deletion happens only after preservation assertions and removes the entire disposable host.
