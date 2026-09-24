# Concurrent test destinations and resource budgets

Each managed vCluster runtime needs an available host destination namespace. A pool is an administrator-configured list of independent Replicove installations. Each member has its own destination, protected operator/state namespace and host resource budget. It does not add a controller that can create arbitrary namespaces or expand grants for tenants.

## Render installations offline

Use the [two-member example](https://github.com/nimeshbuilds/replicove/blob/main/examples/testing/pool.yaml), adjust source RBAC and quotas, and render it locally:

```sh
replicove pool render -f examples/testing/pool.yaml > pool-install.yaml

# Administrator: review the YAML, then apply it to the selected host context.
kubectl apply -f pool-install.yaml
kubectl -n replicove-a rollout status deployment/replicove --timeout=180s
kubectl -n replicove-b rollout status deployment/replicove --timeout=180s
```

`ReplicaPool` is a **local CLI document**, not a CRD. Rendering performs no Kubernetes API calls. The file has `apiVersion: replicove.nimeshbuilds.dev/v1alpha1`, `kind: ReplicaPool`, an optional `kubernetesVersion` (default `v1.36.0`), and `members`. Each member requires `namespace`, `systemNamespace`, and `values` containing ordinary chart values; optional `releaseName` defaults to `replicove`.

The renderer emits shared Replicove CRDs once, both namespaces for each member, and native chart manifests. Every destination and system namespace must be distinct across the pool; `default` and `kube-*` names are rejected. It forces `stateKey.bootstrap: true` so an encryption key is generated inside each protected namespace, not written into the output. It overrides `destinationNamespace` and `createDestinationNamespace` because namespace ownership is explicit in the pool file. It does not create `ReplicaGrant` objects or authorize test users.

For mirrors, set `mirrors.snapshotController.mode` explicitly to `existing`, or select **one** member as `managed` and all other enabled members as `existing`. Offline `auto` is rejected, as are multiple bundled snapshot controllers. CSI drivers and an enforced CNI still require administrator qualification. Do not remove the member managing shared snapshot infrastructure while other consumers depend on it.

## Grant members explicitly

Create a separate [ReplicaGrant](grants.md) for each destination, delegating the same intended source toolset and appropriate access role. The grant's `targetNamespace` must match its member. Source read RBAC in the chart and delegated resources in the grant both apply. Keep the system namespaces inaccessible to tenants and do not include them in source reads.

Users need permission to list `ClusterReplica` and `ReplicaMirror` requests in the members they may select, plus the ordinary create/get/delete/access permissions. Grants and destination membership are configured by administrators. The runner does not discover unrelated namespaces or change its host cluster context while choosing a member.

## Select a destination per run

Add explicit namespace/grant pairs to a [test recipe](test-runs.md):

```yaml
destinations:
  - namespace: test-a
    grant: source-dev-a
  - namespace: test-b
    grant: source-dev-b
```

Run it without `--namespace`:

```sh
replicove run -f integration.yaml -- go test ./integration/... -count=1
```

The CLI chooses the readable configured member with the fewest current replica and mirror requests, preferring empty members and using listed order to break ties. Deleting/queued requests still count. The chosen grant replaces the template's `grantRef`; with `destinations`, the template may omit that field. An explicit `--namespace` and a recipe pool cannot be combined.

Selection is a placement hint, not an atomic capacity reservation. Concurrent clients may pick the same member. The operator's admission records and runtime checks enforce the available slot; another request may queue. Waiting requests keep their original TTL, and the runner's readiness deadline still applies. Selection does not move already-created requests between members, retry failed grants against a broader destination, or create new members automatically.

## Bound resources on the host

Every rendered pool member requires `capacity.enabled: true` and positive `capacity.hard` limits for `requests.cpu`, `requests.memory`, `requests.storage`, `pods`, and `persistentvolumeclaims`. Size these limits for the guest control plane **and** synchronized workloads/PVCs. The renderer validates that limits are present; it cannot prove a particular workload fits.

`capacity.defaultContainer` can supply normal Kubernetes LimitRange defaults for workloads that omit resource requests. Host ResourceQuota admission enforces these totals even when guest users submit workloads directly. A workload can remain Pending when it exceeds quota or needs a storage class not available on the host. CPU request quotas bound admitted requests, not instantaneous CPU consumption; set appropriate limits/defaults and host scheduling policies as well.

`ReplicaGrant.spec.maxConcurrentReplicas` additionally caps admitted replicas using that grant. It does not replace runtime compatibility checks or resource quotas. Mirrors consume a slot through their active/candidate replica generation. ResourceQuota and LimitRange objects persist with the pool after test runs; deleting an individual test replica does not deprovision its administrator-owned destination.

## Updates and removal

These are native installations. Regenerate manifests with the target CLI/chart version and follow the [native upgrade instructions](enable-mirroring.md), preserving each member's system namespace, destination and state key. Apply new CRDs before updating deployments. A completed bootstrap Job must be removed before applying a changed pod template; its replacement reuses the existing immutable key. Do not translate native installations into Helm ownership during an update.

Drain and delete active test requests, wait for verified finalizer cleanup, and revoke sessions before removing a member. Remove its entry from recipes before admitting more work. Pool rendering has no automatic prune or uninstall operation. Review shared snapshot-controller dependencies before deleting rendered infrastructure. Never delete protected state keys or remove cleanup finalizers to make draining appear complete.
