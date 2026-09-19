# What the tests prove

| Suite | Runs against | Evidence | Does not prove |
| --- | --- | --- | --- |
| `make test` | Fake Kubernetes client / provider; actual lifecycle code | Ordering, UID ownership, expiry, recovery, error redaction, cleanup boundaries | API admission or real vCluster behavior |
| `make test-contract` | Actual pinned upstream chart and Helm SDK | Hash, JSON schema, rendered resource scope, labels, storage profile, exact image tags | Scheduling, guest connectivity, workload execution |
| `make test-integration` | Local Kubernetes API server and etcd; fake lifecycle provider plus real Helm SDK | CRD admission/CEL, immutable spec, status, finalizers, actual chart resource installation, repeated observation and partial Helm cleanup | Running vCluster pods, a host scheduler, guest connectivity, cloud or storage behavior |

The upstream render suite uses host capability versions 1.35, 1.36 and 1.37. Those versions are render inputs, not a certified support range. The API integration suite uses envtest 1.37.0.

## Live acceptance gate still to build

Use a disposable cluster with explicit capacity and credentials. Record the exact host version, API discovery, vCluster/guest version and image digests. Apply one request, connect to the guest, run a small workload, restart the operator, then expire/delete the request. Inventory remaining workloads, secrets, PVCs and external resources and reconcile them against the promised cleanup scope.

Only then add a source-component fixture, replication, scoped access, durable storage, and cloud-specific scenarios. Spark and Trino require separate successful workload tests; a rendered chart is insufficient.

Never use production kubeconfigs or secrets as test fixtures. Failed test output, status and public issues must not include credentials.
