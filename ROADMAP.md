# Roadmap

This is a build sequence, not a release promise. [The full plan](docs/design/cluster-replica-implementation-plan.md) contains acceptance criteria and dependencies for all phases.

| Phase | Outcome | Status |
| --- | --- | --- |
| 0. Evidence and compatibility baseline | Pin upstream contracts; establish a real host/guest test | Chart candidate pinned; live validation pending |
| 1. API and operator foundation | CRD, immutable requests, status, namespace grants | Minimal lab CRD/controller implemented; grants and production RBAC pending |
| 2. Runtime and initial lifecycle | Provision/connect/expire; standalone, existing, Platform providers | Standalone Helm adapter implemented; full cleanup and other providers pending |
| 3. Discovery and plan | Read selected source components, detect dependencies, produce an inspectable plan | Next |
| 4. Replication engine | Install supported operators/configuration with overrides and verification | Planned |
| 5. Workloads and identities | Spark/Trino scenarios, selected secrets and cloud identity mappings | Planned |
| 6. Complete cleanup | Ownership inventory, guest teardown, storage/external-resource deletion evidence | Planned; required before promising fully ephemeral replicas |
| 7. Access and agents | Short-lived, scoped human/CI/agent credentials | Planned |
| 8. Cloud and distribution validation | Validate adapters on EKS, AKS, GKE, OpenShift, RKE2 as needed | Planned |
| 9. Release maintenance | Candidate-to-certified pipeline, drift and upgrade testing | Pinned translator and chart contract tests started |
| 10. Public release and operations | Installable releases, docs, diagnostics, support process | Planned |

## Immediate milestones

1. Run a reproducible real-cluster demo: CR → reachable vCluster → small guest workload → deletion, with an inventory of every leftover. Do not call the cleanup contract complete until it is demonstrated.
2. Add read-only host discovery and a reviewable plan for one explicitly selected Helm-installed component. Start with a fixture operator and then validate cert-manager as a real example.
3. Apply that plan to the guest, verify behavior, and record ownership. Implement selection and value overrides before expanding adapters.
4. Design the complete TTL teardown and least-privilege access contract before copying secrets or provisioning cloud identities.

Kubernetes versions and capabilities are the core compatibility axes. EKS is a candidate for the first AWS identity adapter; the generic controller has no AWS dependency.
