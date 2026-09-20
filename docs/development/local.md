# Build and test

Clone `main` and use the Go version pinned in `go.mod`. Current development uses Go 1.27.1. For docs, use Python 3.12 and the pinned documentation requirements. Real cluster tests require Docker.

```bash
git clone --branch main https://github.com/nimeshbuilds/replicove.git
cd replicove
make build
```

`bin/replicove` is the CLI. `bin/cluster-replica` is the operator binary (the original internal name is retained). The Go module/API group also retain their original identifiers during the alpha.

## Repository layout

| Path | Purpose |
| --- | --- |
| `api/v1alpha1` | CRD types, defaults and validation markers |
| `cmd/operator`, `cmd/replicove` | Operator and human/CI command line |
| `internal/policy`, `capture`, `planner` | Delegation, bounded reads and transformation/dependency planning |
| `internal/state` | Encryption, durable intent/inventory and explicit key bootstrap |
| `internal/workflow`, `target` | Apply/readiness/refresh/access/cleanup and verified guest clients |
| `internal/catalog`, `runtime/helm` | Exact upstream profiles and runtime lifecycle |
| `charts/replicove`, `config/install` | Chart and generated native installer |
| `examples/yaml` | Public kubectl-only walkthrough inputs |
| `test/integration`, `test/e2e`, `test/workloads` | API contracts and live fixtures |
| `docs`, `mkdocs.yml`, `hack/docs.py` | Documentation content, navigation and generated references |

Read [architecture](../architecture.md) before adding behavior across boundaries.

## Local checks

```bash
make generate
make check
```

`make generate` regenerates DeepCopy code, CRD schemas, embedded chart CRDs, and native installation YAML. It formats Go sources. Review the resulting diff.

`make check` runs race-enabled Go tests, actual pinned chart contracts, a real local envtest API server, vet, and both builds. Downloader scripts verify pinned inputs and use ignored `.cache/` storage. Chart contracts cover values, ownership, images and namespaced RBAC; API tests validate admission, immutable requests, status/finalizers, authorization and target identity.

No Docker engine is needed for those local checks. They do not prove a live vCluster can start.

## Live disposable-cluster tests

```bash
./hack/fetch-e2e-tools.sh
make test-e2e
./hack/e2e-yaml.sh
./hack/e2e-replication.sh
./hack/e2e-workload.sh cert-manager
./hack/e2e-workload.sh spark
./hack/e2e-workload.sh trino
./hack/e2e-workload.sh policy
```

Each script creates and deletes its own uniquely named kind host. Use a machine with sufficient Docker CPU, memory and disk for the selected workload. The portable workflow defaults to the pinned 1.36.4 host; CI also runs it on 1.35.8. Scripts store private connection files under `.cache/`, remove them during cleanup, and retain only sanitized evidence for upload.

The YAML scenario uses the public manifests and no Replicove/Helm CLI. It checks real bootstrap Job execution and key reuse as well as runtime creation, copied configuration, a bounded viewer credential, revocation and cleanup.

## Test a change at the right boundary

- Policy and transformation changes: exercise allowed behavior and denied cases, including dependency and ownership conflicts.
- State or access changes: verify restart/retry, UID reuse, expiration and failure recovery without exposing credentials.
- Chart/RBAC changes: regenerate native manifests, run chart and real-API authorization checks, then live installation.
- Runtime/profile changes: qualify actual provision, access, restart/rescheduling, TTL and deletion with the pinned chart.
- Documentation changes: run the [strict site build and link checks](docs.md).

A unit test of a mock runtime is useful but is not evidence that an actual vCluster works. Record the commit and matching live run when making a support claim.

## Submit a change

Keep generated files, docs, and examples aligned with behavior. Run relevant checks and explain their scope in the PR. Follow [Contributing](../../CONTRIBUTING.md), report security issues through [Security](../../SECURITY.md), and use [runtime maintenance](../maintaining-replicove.md) for upstream releases.
