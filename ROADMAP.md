# Replicove roadmap

The goal is a declarative workflow for disposable Kubernetes environments that reproduce selected tools and configuration. This is a development sequence, not a release-date promise. See [project status](docs/project-status.md) for the tested revision and [the full implementation plan](docs/design/cluster-replica-implementation-plan.md) for acceptance criteria.

| Area | On `main` | Portable alpha / remaining work |
| --- | --- | --- |
| Runtime | Pinned Helm provisioning, readiness, ownership, release deletion and TTL | Real lifecycle evidence in [#7](https://github.com/nimeshbuilds/replicove/pull/7); persistent runtime and existing-target workflow in [#8](https://github.com/nimeshbuilds/replicove/pull/8) |
| Discovery and plan | Design | Grants, source capture, selection, dependency planning and approval in #8 |
| Replication | Design | Selected Helm components, resources, overrides and refresh in #8 |
| Secrets and access | Namespace administrator access | Selected secrets and scoped guest credentials/revocation in #8; cloud identity remains future work |
| Cleanup | `HelmReleaseOnly` | Owned guest/runtime inventory and TTL scenarios in #8; external data and cloud resources remain future work |
| Workloads | No live workload qualification | Small cert-manager, Spark, Trino and native admission-policy scenarios passed in #8 |
| Compatibility | Pinned chart contract | Two live host Kubernetes minors in #8; wider capability/distribution and cloud qualification pending |
| Public releases | Source builds | Packaging in #8; public binaries/images, signing and release qualification pending |

## Next milestones

1. Review and land the tested portable alpha with reproducible installation, scoped grants, and honest cleanup boundaries.
2. Publish a usable alpha release and a verified operator image with the matching quickstart and evidence.
3. Strengthen version maintenance, retained compatibility profiles, image provenance, and upgrade/recovery coverage.
4. Add cloud identity and data adapters with explicit permissions and disposable cloud-lab evidence.

Kubernetes versions, available APIs, and required capabilities are the core compatibility axes. Distribution names add adapter context. Cloud labs are deferred; current validation uses disposable CI clusters.

Have a concrete test-environment problem? [Describe it in Discussions](https://github.com/nimeshbuilds/replicove/discussions) or [propose a feature](https://github.com/nimeshbuilds/replicove/issues/new/choose).
