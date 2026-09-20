# GitOps and CI

Replicove is declarative. A GitOps controller or Kubernetes client can submit the same `ClusterReplica` and `ReplicaAccess` resources as the CLI. No Replicove CLI is required in an agent or pipeline.

## Reconcile installation in order

1. Install/update the three CRDs and wait for them to become established.
2. Apply the protected/destination namespaces, operator RBAC, bootstrap Job, and Deployment.
3. Wait for successful key bootstrap and the Deployment rollout.
4. Apply source read RBAC and administrator grants.
5. Submit replica requests only after their dependencies are ready.

The native [installer](../../config/install/) is generated from the operator chart. Use the versioned release manifests with a digest-pinned image, or a Kustomize overlay for your own build. When rendering the Helm chart offline, manage the destination namespace separately (`createDestinationNamespace: false`) and use the explicit bootstrap Job (`stateKey.bootstrap: true`) so repeated renders do not generate new encryption keys. If changing the completed bootstrap Job's pod template, explicitly replace that Job; Kubernetes does not allow an in-place Job template update. Reinitialization reuses the immutable key.

Exclude dynamically created encryption/state/credential Secrets and runtime resources from declarative pruning. Those are controller-owned runtime state, not desired manifests to copy into Git. Do not enable namespace or CRD pruning as a shortcut for replica cleanup.

## Long-lived GitOps versus ephemeral requests

Use GitOps for the operator, RBAC, and grants. Treat per-run replicas as ephemeral resources with unique names. The request spec is immutable, so changing its TTL, selections, or profile in place will fail admission.

An expired request remains with terminal status; reapplying its YAML does not renew it. If an external reconciler recreates a deleted request, that creates a new lifetime and potentially a new runtime. Explicitly model that intent instead of relying on pruning/recreation loops.

For manual approval, the approved revision is runtime status data. Your approval step must read and review the current plan before writing its annotation. Never put a guessed or permanently reusable plan revision in Git.

## Pipeline algorithm

```text
create ClusterReplica with a unique run name and bounded TTL
watch until Ready, or surface Blocked/Rejected conditions and fail the run
read metadata.uid
create ReplicaAccess bound to that UID with a permitted role
watch until the session is Ready
read the exact named credential Secret into protected process memory/file
run integration tests through the guest route
finally:
  delete ReplicaAccess and wait for revocation
  delete ClusterReplica and wait for verified cleanup
  remove local credential files
```

TTL supplies a fallback when the runner vanishes. It does not replace a pipeline cleanup handler or monitoring for stuck finalizers. Grant the CI identity request permissions and exact session-Secret access, not host administrator access. See [access](access.md).

## Repository examples and tests

- [YAML fixtures](../../examples/yaml/): installation, source read permissions, grant, and full replica request.
- [YAML end-to-end runner](../../hack/e2e-yaml.sh): native installation, key reuse, provisioning, access and cleanup.
- [Portable workflow runner](../../hack/e2e-replication.sh): approval, refresh, secret follow, restarts, existing targets and TTL.
- [Workload runners](../../hack/e2e-workload.sh): cert-manager, Spark, Trino and admission-policy scenarios.

The disposable test runners choose unique kind cluster names, private kubeconfig paths, and sanitized artifacts. They remove only the cluster they created. Build-specific runtime artifacts stay under ignored `.cache/` paths.
