# Helm releases and operators

Replicove can capture selected Helm releases from the source cluster's stored release data, including their chart and revision values. It renders those inputs for the pinned guest Kubernetes version and applies the resulting resources through the same dependency and ownership workflow as other captured objects.

## Authorize and select a release

The administrator must grant the release by namespace and name:

```yaml
# ReplicaGrant.spec
helmReleases:
  - namespace: operator-system
    name: cert-manager
```

The operator also needs source read permissions for Helm's release storage and any separately selected custom resources. Stored Helm templates and values can include sensitive data, so release grants are separate from ordinary resource grants.

Select the release and adapt its values:

```yaml
# ClusterReplica.spec.replication
helmReleases:
  - namespace: operator-system
    name: cert-manager
namespaceMap:
  operator-system: test-cert-manager
helmOverrides:
  - namespace: operator-system
    name: cert-manager
    values:
      crds:
        enabled: true
      startupapicheck:
        enabled: false
      global:
        leaderElection:
          namespace: test-cert-manager
```

This fragment illustrates the pinned cert-manager fixture's optional-hook adaptation; use the [complete fixture](../../test/workloads/) for all required grants and settings. Chart values are version-specific. A different chart version may need different values.

## Reconstruction is not a Helm install in the guest

The source release's chart, revision, and values become protected plan inputs. Rendered CRDs, RBAC, operator workloads, webhooks, and custom resources are ordered and checked as applicable. **No guest Helm release record is created.** `helm list` in the guest will not list a reconstructed release, and `helm upgrade` is not the management path for those resources.

Helm storage does not prove the original registry URL or signature. Replicove does not invent that provenance. Source capture is not an atomic snapshot across separate releases and Kubernetes resources.

## Hooks and dependencies

Lifecycle hooks block portable capture. Disable a chart's optional lifecycle hook through a documented value when the chart supports it. Test hooks are ignored and are not automatically executed. Missing dependencies, foreign ownership, unsafe workloads, and unadapted cloud integrations block the plan.

CRDs must become established before their custom resources. Controller workloads must become ready where the dependency graph requires them. Add custom readiness checks for application-specific outcomes such as a Certificate becoming Ready or a SparkApplication reaching COMPLETED.

## Workloads validated today

| Workload | What the fixture demonstrates | What remains outside that result |
| --- | --- | --- |
| cert-manager | Captured operator issues a guest certificate and Secret | Arbitrary issuers, external DNS, production CA integrations |
| Spark Operator | Captured operator runs a small Spark Pi application | Large executor fleets, object storage identity, throughput |
| Trino | Recreated single coordinator answers a TPCH query | Distributed sizing, external catalogs, production storage |
| Native admission policy | Replicated policy accepts and rejects specific probes | Every third-party policy engine or admission configuration |

Yes, Spark and Trino can run in a vCluster when their dependencies and host resources are available. The current fixtures establish small functional scenarios. They do not certify heavy-workload performance, dedicated worker isolation, or cloud-specific access. Versions and evidence are listed under [workload qualification](../workload-adapters.md).
