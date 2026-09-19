# What the tests prove

| Suite | Runs against | Evidence | Does not prove |
| --- | --- | --- | --- |
| `make test` | Fake Kubernetes client / provider; actual lifecycle code | Ordering, UID ownership, expiry, recovery, error redaction, cleanup boundaries | API admission or real vCluster behavior |
| `make test-contract` | Actual pinned upstream chart and Helm SDK | Hash, JSON schema, rendered resource scope, labels, storage profile, exact image tags, runtime RBAC coverage without wildcards or escalation verbs | Scheduling, guest connectivity, workload execution |
| `make test-integration` | Two local API servers/etcd plus real Helm SDK | CRD/CEL, immutable spec, lifecycle, installer and key-preserving upgrade, source capture, existing-target identity/apply/drift, exact-Secret RBAC and revocation | Running vCluster pods, a host scheduler, cloud or storage-controller behavior |

The upstream render suite uses host capability versions 1.35, 1.36 and 1.37. Those versions are render inputs, not a certified support range. The API integration suite uses envtest 1.37.0.

## Live end-to-end suite

`make test-e2e` requires a reachable Docker daemon, curl, Python 3 and standard shell tools. It downloads hash-verified kind 0.33.0, kubectl 1.36.4 and vCluster CLI 0.37.1 into `.cache/e2e-tools`. The separate GitHub Actions `live-vcluster` job runs it on a disposable Ubuntu runner.

The script builds the actual operator Dockerfile, creates a uniquely named kind cluster using a digest-pinned Kubernetes 1.36.4 node, and uses a separate kubeconfig. It never selects an existing cluster or edits the caller's kubeconfig. It deploys the operator with the sample namespaced service account and waits for a real vCluster control-plane Deployment.

The checks cover guest API access through `vcluster connect`, a guest Deployment, an HTTP probe through guest DNS and a Service, operator restart, real eight-minute TTL expiry, absence of the Helm release, no resurrection, explicit deletion, and a quota-induced partial installation failure. Actual host and guest versions are asserted. The vCluster Deployment UID and TTL must survive the operator restart. The failure case requires failed Helm storage and an existing chart Service before checking their removal. An unrelated host ConfigMap and the namespace must survive.

The trap deletes only the freshly created kind cluster. Metadata-only before/after inventories and test results live in `.cache/e2e/run.*/artifacts/`; CI retains that directory for seven days. Secrets, kubeconfig files, Helm storage payloads and arbitrary pod manifests are excluded. Inventory includes leftover guest pods and generated credentials **by object name only**. A passing HelmReleaseOnly test does not establish full data cleanup or credential revocation.

Live results must be linked in the [validation record](validation.md) before claiming these checks have passed. See [kind's cluster isolation options](https://kind.sigs.k8s.io/docs/user/quick-start/#interacting-with-your-cluster) and the upstream [vCluster connect command](https://www.vcluster.com/docs/vcluster/cli/vcluster_connect).

## Broader acceptance gates

Use a disposable cluster with explicit capacity and credentials. Record the exact host version, API discovery, vCluster/guest version and image digests. Apply one request, connect to the guest, run a small workload, restart the operator, then expire/delete the request. Inventory remaining workloads, secrets, PVCs and external resources and reconcile them against the promised cleanup scope.

The repository now has separate full-workflow and workload suites:

- `hack/e2e-replication.sh`: embedded installer; operator namespace isolation and rejected Role escalation/cluster-admin binding; manual plan/approval; real source Helm capture; Secret snapshot/follow; namespace maps and overrides; guest Deployment/Service/HTTP; operator restart; persistent control-plane replacement and tunnel recovery; refresh; viewer authorization; existing-target conflict and preservation; owned deletion; five-minute TTL and control-plane PVC cleanup. CI targets pinned 1.35.8 and 1.36.4 hosts.
- `hack/e2e-workload.sh cert-manager|spark|trino|policy`: independently install the source toolset, capture it, reconstruct it in a persistent vCluster, execute a real functional probe, and verify cleanup. Pinned chart archives are vendored with upstream licenses and hashes.
- `hack/release-artifacts.sh VERSION`: package Linux/macOS amd64/arm64 CLI binaries, operator chart and checksums. This does not publish a release.

The [recorded results](validation.md) identify the passing commit and scenario. Cloud identity, CSI snapshot/content restoration, production data workloads and broader vendor/scale behavior remain separate gates.

Never use production kubeconfigs or secrets as test fixtures. Failed test output, status and public issues must not include credentials.
