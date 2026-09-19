# Replicove portable alpha

Replicove captures an administrator-granted toolset and reconstructs it in a vCluster. It records source provenance, dependencies, target ownership and cleanup progress. The product name and icon are documented in [brand research](brand/research.md).

The supported workflow is portable Kubernetes desired state. Cloud identity exchange, cloud data copying, vCluster Platform provisioning, and broad operator certification are qualification work in progress. `platform` requests currently block with `PlatformQualificationRequired`; they never fall back to Helm. An existing vCluster can be selected using an administrator-provided, data-only kubeconfig pinned to its `kube-system` UID.

## Install and create

Build `make build`, build/push the operator image, and use a disposable cluster. The image tag below is your own pushed build until a release is published:

```sh
bin/replicove install --context YOUR_TEST_CONTEXT \
  --image YOUR_REGISTRY/replicove:dev --values operator-values.yaml
kubectl --context YOUR_TEST_CONTEXT apply -f administrator-grant.yaml
bin/replicove create integration --context YOUR_TEST_CONTEXT \
  --grant source-dev-lab --ttl 2h --manual \
  --replication-file replication.yaml
bin/replicove plan integration --context YOUR_TEST_CONTEXT
bin/replicove approve integration --context YOUR_TEST_CONTEXT
bin/replicove connect integration --context YOUR_TEST_CONTEXT \
  --role viewer --output integration.kubeconfig
```

The repository contains complete disposable fixtures: [operator values](../test/e2e/replicove-values.yaml), [grant](../test/e2e/grant.yaml), and [replication selection](../test/e2e/replication.yaml). Replace their source namespaces and rules deliberately. The installer embeds the chart and CRDs; it creates the protected system namespace and the destination namespace. Sources must already exist.

`connect` uses the user's authenticated host connection to open a loopback-only tunnel and writes a new mode-0600 guest kubeconfig. The default kubeconfig is never modified. Ctrl-C or access expiration closes the tunnel and removes the file. `access` writes credentials for use through an existing network route, including in-cluster agents. Existing targets use `access` and their administrator-configured route.

Access roles map to the guest's standard `view`, `edit`, and `cluster-admin` roles. Each request expires within its creation-time duration, the administrator's access limit, and the replica TTL. The operator verifies the token server's actual expiration. Destination users need permission to create/read their request and **get the exact returned Secret name**. Do not give credential consumers general Secret-read permission in the runtime namespace: it also contains vCluster administrator credentials. Namespace administrators can grant a Role with `resourceNames: [RETURNED_SECRET_NAME]`. This alpha uses namespace delegation; it does not infer per-user authority from annotations or claim isolation between administrators of the same destination namespace.

## Selection and reconstruction

- Source namespaces, API groups, kinds, names and labels can be selected. Exclusions win. Required references are added only from granted, non-excluded resources. A missing dependency blocks the plan.
- Namespace maps rewrite Kubernetes object identity and known ServiceAccount/webhook references. Arbitrary application strings are preserved. Use merge patches or Helm values for application-specific endpoints.
- Server metadata, owner references, status, source Service IPs and generated workload children are omitted. Source ServiceAccount and bootstrap tokens are never copied.
- Secrets require a separate administrator name grant and `Snapshot` or `Follow`. Helm releases require an explicit release grant because stored values and templates can contain secrets. Follow mode updates raw, individually granted Secrets; chart-generated credentials remain part of their chart snapshot.
- Helm's stored chart and exact revision values are captured and rendered for the pinned guest Kubernetes version. The resulting resources, including CRDs, join the same ownership inventory. These are **reconstructed resources, not guest Helm releases**. Upstream registry signatures/URLs cannot be recovered from Helm storage and are not invented. Lifecycle hooks block capture; disable an optional hook through a documented chart value before use. Test hooks do not run automatically.
- `data: EmptyVolumes`, when granted, provisions fresh PVCs with optional StorageClass mappings. It does not copy source volume contents. Source host paths, privileged containers, host networking, and unadapted cloud identity annotations block the portable plan.

Manual approval binds to the captured plan revision. `replicove refresh NAME` captures a new source snapshot; a manual request needs approval of that new revision. Refresh applies changes to source-owned fields and preserves unrelated added fields. It prunes removed inventoried objects. Ordinary reconciliation reports desired-state drift instead of resetting experiments.

## State, access and cleanup

The operator's system namespace must be separate from the source and destination. Its immutable 32-byte key encrypts compressed captures and inventories using AES-GCM. Owner UID is authenticated with the ciphertext. Plan revisions use a keyed digest; values and credentials do not enter status or ordinary diagnostics. Back up the encryption key with its encrypted state and restrict both to administrators. The chart retains the key on uninstall; do not delete it while any replica or access finalizer remains. To upgrade, use `helm upgrade` with the chart and existing release/system namespace; the chart reuses the existing key.

The grant is a cluster-scoped administrator object. Kubernetes RBAC decides who can create requests in its destination namespace. Changing a grant after capture blocks new mutations and access issuance; cleanup remains possible from the protected inventory. Recreate the request to use a revised grant.

Object intent is persisted before creation. Completed writes pin target UIDs. Name reuse and foreign ownership block updates and deletion. Existing targets preserve their runtime and preexisting namespaces; administrators must create the selected target namespaces first. Replicove removes only its inventoried additions from an existing target.

TTL starts at request creation, including planning and installation. Cleanup revokes access, deletes guest resources in reverse dependency order, respects finalizers, removes the owned runtime, follows UID-based host ownership references, and removes encrypted captures. Failure remains visible with the finalizer in place. Replicove does not forcibly remove finalizers or claim that deleting Kubernetes metadata erases arbitrary external services.

The CLI defaults to `vcluster-0.37.1-persistent`: a 1 GiB control-plane PVC using the host default StorageClass and StatefulSet deletion retention set to Delete. Its CI rescheduling and volume cleanup checks are being qualified. The optional `vcluster-0.37.1-lab` control plane uses `emptyDir`. Operator restart is supported; rescheduling the vCluster control-plane pod can lose its state and trigger a pinned-cluster-identity failure. Use it for disposable labs. Cloud cleanup requires separate evidence before a production claim. For inventoried bound volumes, cleanup requires Delete reclaim policy and waits for the exact PersistentVolume UID to disappear; it never deletes arbitrary host PVs directly.

## Evidence and current scope

`make check` runs race tests, the pinned upstream chart contract, real API-server admission/integration tests, vet, and both builds. `hack/e2e-replication.sh` runs the embedded installer, manual plan, source Helm reconstruction, real guest workload/Service, secret follow, restart, explicit refresh, guest viewer RBAC and cleanup in its own kind cluster. CI runs this against two pinned Kubernetes minors. Check the current PR's CI results; a fixture's existence is not evidence it passed.

See [implementation status](IMPLEMENTATION_STATUS.md) for remaining work and the user's decision to defer cloud labs.
