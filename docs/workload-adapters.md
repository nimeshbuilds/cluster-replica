# Workload qualification fixtures

Replicove's portable adapter uses CRDs, explicit Helm revision capture, dependency ordering, value overrides, and custom readiness checks. Workload-specific settings stay in inspectable fixture YAML. These profiles are being exercised in disposable CI; the fixture files alone are not a certification.

| Fixture | Pinned upstream | Adaptation | Functional check |
| --- | --- | --- | --- |
| cert-manager | Chart v1.20.4 | Install CRDs; disable the optional startup Helm hook; use the selected namespace for leader election; small lab resource limits | Reconstructed Issuer/Certificate reaches Ready and creates a guest TLS Secret |
| Kubeflow Spark Operator | Chart 2.5.2; Spark 4.0.4 | Disable optional webhook; grant one job namespace; use a named Spark service account and a small Pi calculation | Captured SparkApplication completes through the guest operator |
| Trino | Chart 1.42.2 | One coordinator with 512 MiB heap, built-in TPCH catalog, and coordinator scheduling enabled | Real Trino query returns 25 rows from `tpch.tiny.nation` |

Sources: [cert-manager Helm instructions](https://cert-manager.io/v1.20-docs/installation/helm/), [cert-manager chart index](https://charts.jetstack.io/index.yaml), [Spark Operator release](https://github.com/kubeflow/spark-operator/releases/tag/v2.5.2), [upstream Spark example](https://github.com/kubeflow/spark-operator/blob/v2.5.2/examples/spark-pi.yaml), [Trino chart release](https://github.com/trinodb/charts/releases/tag/trino-1.42.2), and [Trino chart configuration](https://trinodb.github.io/charts/charts/trino/).

`hack/fetch-workload-charts.py` pins the chart archives by SHA-256 from the official chart index or GitHub release asset digest. `hack/e2e-workload.sh NAME` installs the source release, captures its chart/values and selected custom resources, creates a new vCluster, checks real guest behavior, and verifies owned cleanup. It preserves source resources and uploads only sanitized evidence.

These are integration correctness checks on small workloads. They do not certify production Spark/Trino scale, throughput, cloud catalogs, object-store permissions, distributed storage, or arbitrary operator versions. User-supplied Helm values and resource patches remain necessary for application-specific endpoints and credentials.
