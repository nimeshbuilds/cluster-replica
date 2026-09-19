# Replicove implementation ledger

Requested scope: the full ClusterReplica product plan, plus researched product branding and an icon. Cloud lab validation is explicitly deferred by the user; use disposable CI clusters now. This ledger tracks implementation and evidence without treating interface stubs or planned adapters as completed features.

## Delivery checklist

- [x] Research product-name collisions; select Replicove and record sources/limits.
- [x] Generate, inspect, save, and integrate the product icon.
- [x] Existing standalone Helm vCluster lifecycle with real guest workload/restart/TTL tests (PR #7).
- [ ] Administrator-controlled source/destination grants and bounded encrypted capture storage.
- [ ] Kubernetes version/API capabilities and deterministic automatic discovery/planning.
- [ ] Namespace/kind/name/label selection, exclusions, dependency checks, mappings, overrides.
- [ ] Generic desired-state and Helm replication with conflict protection and readiness verification.
- [ ] Secret snapshot/follow behavior, safe token exclusions, refresh and drift reporting.
- [ ] Existing-vCluster provider with preservation of its external lifecycle.
- [ ] Configured Platform provider without automatic bypass of Platform policy.
- [ ] Owned-object inventory, finalizer-aware cleanup, protected capture cleanup, PVC evidence.
- [ ] Human/CI/agent access with bounded credentials and a usable CLI.
- [ ] Cert-manager, policy-engine, Spark and Trino fixtures/adapters with real workload evidence.
- [ ] Identity and data adapter contracts, explicit mappings and cloud qualification harnesses.
- [ ] Installable operator chart, CLI/container packaging, release and compatibility automation.
- [ ] Second Kubernetes minor, failure/recovery scenarios, documentation and final CI evidence.

Cloud identity exchanges, cloud storage teardown, OpenShift/RKE2 qualification, external design-partner testing, and broader GA certification require their own environments and evidence. Keep those gates visible; do not mark them passed from kind tests.

Namespace delegation is the first authorization model: a cluster administrator grants a destination namespace explicit source capabilities, and Kubernetes RBAC decides who may create requests there. The operator must not infer the original user from user-editable annotations. Per-user gateway delegation is a separate access path.
