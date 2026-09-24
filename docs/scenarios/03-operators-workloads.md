# 03. Recreate operators and run real applications

Use this lab to answer a practical question: can the captured toolset do useful work inside the replica? The four variants recreate pinned operator or application inputs and then test their behavior in the guest, beyond checking Deployment readiness.

## Run every workload variant

Prepare the [shared prerequisites](index.md#prepare-once) and run these sequentially:

```bash
./examples/scenarios/run.sh 03
./examples/scenarios/run.sh 03 spark
./examples/scenarios/run.sh 03 trino
./examples/scenarios/run.sh 03 policy
```

The first command defaults to `cert-manager`. Each variant gets a fresh disposable host and guest. Spark and Trino need enough Docker memory and CPU for both source and guest instances; they are small functional fixtures, not performance benchmarks.

## Follow the workflow

The harness verifies the pinned workload chart contracts, installs Replicove, and seeds the source with the selected chart/release data and any custom resources. An explicit grant authorizes that release and the selected Kubernetes kinds. A replica request supplies the required values overrides and readiness checks. After capture and dependency ordering, Replicove applies the rendered desired resources to a newly provisioned vCluster.

| Variant | Guest operation that must pass | Feature demonstrated |
| --- | --- | --- |
| `cert-manager` | A self-signed Issuer produces a Ready Certificate and a Secret containing both `tls.crt` and `tls.key` | Operator Deployment, CRDs, RBAC, webhook admission, custom resources and readiness ordering |
| `spark` | A SparkApplication reaches `COMPLETED` after the recreated operator runs the driver/executor | Operator reconstruction and application-specific field readiness |
| `trino` | The real Trino CLI runs `SELECT count(*) FROM tpch.tiny.nation` and receives `25`; the guest API remains usable afterward | A real application query against the recreated coordinator |
| `policy` | Admission rejects an unlabelled ConfigMap and accepts a correctly labelled probe | Captured native admission policy and binding enforce actual API requests |

The source cert-manager path also waits for actual webhook admission before capturing the fixture, rather than assuming a ready Pod means the Service is immediately reachable. The fixture uses pinned charts and known values adaptations. Optional lifecycle hooks are disabled where the fixture chart supports that; unsupported lifecycle hooks block general reconstruction.

After the guest check, the connection closes and the replica is deleted. The fixture waits for cleanup and asserts that no owned runtime resources or non-capacity protected records remain among the inspected resources, then removes the disposable host.

## Expected result

Each command exits zero and writes its own workload `report.json`. The report identifies the variant and records source Helm capture, actual guest execution and owned cleanup. A successful cert-manager run is not evidence that Spark or Trino passed; run all four variants for this feature group.

Replicove reconstructs chart-rendered resources from the captured chart and revision values. It does **not** create a guest Helm release record, run chart tests automatically or recover an upstream signature/registry URL that Helm storage did not prove. Guest `helm list` is therefore not the success criterion.

## Inspect and adapt

Read the [fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-workload.sh), [shared grant](https://github.com/nimeshbuilds/replicove/blob/main/test/workloads/grant.yaml) and variant directories for [cert-manager](https://github.com/nimeshbuilds/replicove/tree/main/test/workloads/cert-manager), [Spark](https://github.com/nimeshbuilds/replicove/tree/main/test/workloads/spark), [Trino](https://github.com/nimeshbuilds/replicove/tree/main/test/workloads/trino) and [policy](https://github.com/nimeshbuilds/replicove/tree/main/test/workloads/policy). Each directory contains the capture choices and any chart values/custom resources used by its command.

Use [Helm releases and operators](../guides/operators.md) and [workload qualification](../workload-adapters.md) when adapting your own chart. A blocked plan may expose an ungranted CRD, unsupported hook, unsafe Pod setting or missing dependency. Inspect that reason before changing the grant. These small tests do not qualify large Spark fleets, external object-storage identity, production issuers, distributed Trino catalogs or every admission engine.
