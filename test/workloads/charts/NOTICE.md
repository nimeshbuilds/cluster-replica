# Unmodified upstream test charts

These chart archives are redistributed solely as reproducible integration-test inputs. They are unmodified copies from their official upstream locations and retain their upstream licensing; Replicove does not claim authorship.

- cert-manager v1.20.4: https://charts.jetstack.io/charts/cert-manager-v1.20.4.tgz — https://github.com/cert-manager/cert-manager/tree/v1.20.4 — Apache-2.0, see LICENSE.cert-manager.
- Kubeflow Spark Operator 2.5.2: https://github.com/kubeflow/spark-operator/releases/tag/v2.5.2 — Apache-2.0, see LICENSE.spark-operator.
- Trino chart 1.42.2: https://github.com/trinodb/charts/releases/tag/trino-1.42.2 — Apache-2.0, see LICENSE.trino.

Their official chart-index/release-asset SHA-256 digests are pinned in `hack/fetch-workload-charts.py`. CI verifies the vendored bytes before loading them. Container images remain upstream images and are pulled separately. The small chart inputs are vendored because the upstream chart CDN returned HTTP 403 to disposable CI runners.
