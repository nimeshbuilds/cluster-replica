# Use an existing vCluster

An administrator can register an existing guest instead of asking Replicove to install a new runtime. Replicove applies and cleans up its own additions while preserving the existing runtime and its preexisting namespaces/resources.

It does not automatically discover and adopt arbitrary vClusters. Registration requires a trusted route, data-only kubeconfig, and pinned guest identity.

## 1. Prepare the target

Create the destination namespaces **inside the guest** before requesting replication. The configured target credential must authorize the guest resources and access identities Replicove will manage. Start with a disposable target and a minimal selection.

Obtain a standalone kubeconfig whose server is reachable from the Replicove operator. It must contain inline CA and supported credential data, with verified TLS. Kubeconfigs that invoke exec plugins, refer to local files, impersonate users, or disable TLS verification are rejected.

Read the guest identity using that credential:

```bash
kubectl --kubeconfig existing-guest.kubeconfig get namespace kube-system \
  -o jsonpath='{.metadata.uid}'
```

Store the config in the protected host namespace:

```bash
kubectl --context YOUR_TEST_CONTEXT -n replicove-system create secret generic existing-guest \
  --from-file=config=existing-guest.kubeconfig
```

Protect and remove local credential material according to your credential policy. Do not commit it to Git. The operator must be able to reach the endpoint; a localhost address on your workstation is not a valid in-cluster route.

## 2. Register it in the grant

```yaml
# ReplicaGrant.spec
existingTargets:
  - name: shared-integration
    kubeconfigSecret:
      namespace: replicove-system
      name: existing-guest
    clusterUID: REPLACE_WITH_GUEST_KUBE_SYSTEM_UID
```

The credential Secret must be in the protected state namespace. Replicove rejects the host as its own guest and validates the pinned guest identity before mutation. The grant's source, resource, TTL, and access limits still apply.

## 3. Select it in the request

```yaml
apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ClusterReplica
metadata:
  name: existing-demo
  namespace: replica-lab
spec:
  profile: vcluster-0.37.1-persistent
  ttl: 1h
  cleanupPolicy: DeleteOwned
  grantRef: team-integration
  target:
    provider: existing
    existingRef: shared-integration
  replication:
    namespaces: [source-dev]
    namespaceMap:
      source-dev: integration
```

The profile field is still required by this alpha API even though no runtime is provisioned for `existing`. Use a currently allowed profile and check the actual target version/capabilities; this path is not blanket certification for every Kubernetes or vCluster version.

If a target name already contains foreign objects needed by the replica, ownership checks block adoption or overwrite. Choose an empty guest namespace or names that do not conflict. Cleanup removes only recorded additions; it preserves the existing guest runtime and preexisting namespaces.

## Access and lifetime

Create `ReplicaAccess` or use `replicove access`. Consumers need a valid network route to the registered endpoint. CLI `connect` only manages tunnels for runtimes Replicove owns.

TTL applies to the replica's additions and sessions, not to the externally managed vCluster's lifetime. Keep its API and credentials available until cleanup completes.

## vCluster Platform

`auto` honors the grant's `defaultProvider`. A grant selecting Platform cannot be bypassed with an explicit Helm target. Platform provisioning currently reports `PlatformQualificationRequired` when configured, or `PlatformNotConfigured` when required configuration is absent. It does not silently fall back to Helm. Platform integration remains future qualification work; register a reachable existing guest for the implemented reuse path.
