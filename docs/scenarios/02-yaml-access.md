# 02. Provision and connect using YAML

Use this lab for a GitOps or Kubernetes-native workflow where the installation and requests are manifests. It installs the release's native manifests, lets the operator bring up vCluster, and uses `ReplicaAccess` manifests to obtain bounded viewer and deployer sessions.

## Run

With the [shared prerequisites](index.md#prepare-once) available:

```bash
./examples/scenarios/run.sh 02
```

The lab harness prepares Docker/kind and downloads the release assets. Within the cluster workflow, installation, request creation, access and deletion use `kubectl`; neither the Replicove CLI nor Helm CLI is required to perform those operations.

## Follow the workflow

1. Apply the release CRDs and native operator installation to a fresh kind host. The restricted in-cluster bootstrap Job creates the protected state key, and the operator becomes available. No local encryption key is generated or committed to a manifest.
2. Apply the checked-in source Deployment, ConfigMap, source RBAC, `ReplicaGrant` and `ClusterReplica`. The operator provisions a persistent vCluster and recreates the application in guest namespace `integration`. Its ConfigMap contains `guest-yaml`, while the source retains `source`.
3. Delete and rerun the completed bootstrap Job while encrypted replica state exists. The original key UID must remain unchanged. Reapplying an installation must not rotate away the key needed to read existing captures and cleanup records.
4. Read the current replica UID and place it in a `ReplicaAccess` request for role `viewer` and 900 seconds. Wait for `Ready`, read only the Secret named in its status, and write the kubeconfig to the private fixture directory.
5. Open a loopback `kubectl port-forward` to the runtime Service. Change only the kubeconfig server URL to the local tunnel; the CA and TLS server name continue to verify the guest. The viewer reads the replicated Deployment and ConfigMap; deployment-write authorization returns `no`, and an actual ConfigMap creation must be forbidden. Delete that access request, verify its credential Secret disappears, and confirm the old credential can no longer read guest resources.
6. Create a second YAML access request with role `deployer`. Its credential can create, update, read and delete a guest ConfigMap. Actual namespace and unrestricted ClusterRole creation attempts must be forbidden. The source configuration remains unchanged. Delete this session and verify its Secret removal and guest authorization revocation too.
7. Delete the replica and wait for finalizers. The destination's owned runtime resources are removed; the source Deployment and protected state key remain until the whole disposable host is deleted.

## Expected result

The command exits zero and prints the YAML evidence directory. `report.json` records installation, bootstrap/key reuse, runtime creation, configuration replication, source preservation, viewer/deployer permission boundaries, live credential revocation and owned cleanup. A connection that only works with disabled TLS verification would fail this workflow. [Scenario 01](01-governed-replica.md) exercises an administrator session separately.

The viewer session is an actual guest Kubernetes credential. Authorization to create `ReplicaAccess` on the host does not automatically authorize reading every Secret there. For a persistent installation, configure exact-session readers through `accessSubjects` or an administrator-owned exact-name RoleBinding. Keep guest administrative credentials out of requester permissions.

## Inspect and adapt

Read the [executable fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-yaml.sh) and complete [YAML examples](https://github.com/nimeshbuilds/replicove/tree/main/examples/yaml). The fixture generates the access manifest after discovering the current UID; a copied UID from an earlier request is intentionally invalid.

The [YAML quickstart](../getting-started/yaml.md) walks through each individual command for an administrator-approved host. [Access](../guides/access.md) explains host requester RBAC, exact credential reads, guest roles and revocation. This scenario proves deletion; [scenario 01](01-governed-replica.md) separately waits through real TTL expiry.

If the viewer cannot connect, check the port-forward log, its authorized host route and the actual access expiration. Do not disable certificate checks. A completed bootstrap Job may need explicit replacement when upgrading its immutable Pod template; preserve the original state key. The exit handler closes the lab tunnel, removes temporary credentials and deletes only this lab's named kind host.
