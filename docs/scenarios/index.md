# Ten end-to-end scenarios

Start with the [quickstart](../../QUICKSTART.md) to create your first replica. These ten labs then exercise Replicove's current feature groups with generated workloads and data. Each command creates its own disposable kind host, installs Replicove, runs assertions against real Kubernetes APIs and vClusters, verifies the relevant cleanup, and removes that host. You do not need an existing cluster or cloud account.

The walkthrough and its test are the same checked-in program: [`examples/scenarios/run.sh`](https://github.com/nimeshbuilds/replicove/blob/main/examples/scenarios/run.sh). A failed assertion exits nonzero. The pages explain what it creates, what it checks and where to inspect the manifests, so you can understand the complete workflow before adapting it to an administrator-approved cluster.

## Prepare once

Use a **Linux amd64 machine and Linux amd64 Docker engine**, with Docker running, Bash, Git, curl, Python 3, OpenSSL, make, and Go **1.27.1** as declared in [`go.mod`](https://github.com/nimeshbuilds/replicove/blob/main/go.mod). Normal Unix utilities, including tar, gzip, `sha256sum` and `shasum`, must be available. The runner fetches the pinned Kubernetes, kind and Helm helper tools. Network access is needed for GitHub, GHCR, Kubernetes images, and the fixture dependencies.

```bash
git clone https://github.com/nimeshbuilds/replicove.git
cd replicove
./examples/scenarios/run.sh 01
```

Use the normal `main` checkout. The runner pins the published **v0.3.0-alpha.1** CLI, operator image, chart and native manifests; the scenario scripts themselves come from this checkout. It verifies downloaded asset checksums and uses the recorded operator image digest. Go is used for fixture helpers, not to silently replace the released CLI. Any existing `bin/replicove` is backed up and restored. Inspect the choices without starting a cluster:

```bash
./examples/scenarios/run.sh --list
./examples/scenarios/run.sh --describe 01
```

Run labs sequentially on a local machine; a checkout lock prevents two lab runners from changing the same checkout's CLI at once. The preflight requires at least **4 Docker CPUs and 7 GiB usable Docker memory** (typically an 8 GiB allocation). **16 GiB memory and 40 GiB free disk** are recommended for the heavier labs. Several create more than one runtime during their lifecycle, and Spark, Trino, CSI snapshots and PostgreSQL consume appreciable memory, disk and image-download space. Provisioning deadlines assume enough resources for the selected fixture. A slow image pull or a Pending Pod is a failed prerequisite, not a successful product test. The full matrix is substantially longer than a quickstart; scenario 01 also waits for a real five-minute TTL.

This runner deliberately accepts only the Linux amd64 qualification environment. On macOS or an ARM workstation, use a Linux amd64 VM or remote machine for these labs. Replicove's ordinary CLI remains available for the published Linux/macOS architectures; that does not by itself qualify every Docker Desktop, CNI, CSI and workload-image combination. Check the [validation record](../validation.md) for the actual environment and completed run evidence.

## Choose a lab

| Scenario | Run from the repository root | Features exercised |
| --- | --- | --- |
| [01. Governed replica](01-governed-replica.md) | `./examples/scenarios/run.sh 01` | Helm/CLI installation, grants and host RBAC, selection and dependencies, approval, maps/patches, Secret modes, fresh PVCs, drift/refresh, access, restart, deletion and TTL |
| [02. YAML and scoped access](02-yaml-access.md) | `./examples/scenarios/run.sh 02` | Native manifests, in-cluster key bootstrap/reapply, ClusterReplica and ReplicaAccess CRDs, TLS tunnel, viewer RBAC and revocation |
| [03. Operators and applications](03-operators-workloads.md) | `./examples/scenarios/run.sh 03` | Captured Helm inputs, CRDs/RBAC/webhooks, value overrides and real cert-manager/Spark/Trino/admission-policy behavior |
| [04. Existing vCluster](04-existing-vcluster.md) | `./examples/scenarios/run.sh 04` | Explicit target registration, pinned guest identity, one-command install and upgrade, preservation of foreign resources and runtime |
| [05. Storage mirrors and reset](05-mirror-resets.md) | `./examples/scenarios/run.sh 05` | Late enablement/upgrades, CSI copies, independent guest writes, Sync/Reset, saved revisions, scheduling, leases, existing-target mirror access, retention and cleanup |
| [06. Sanitized PostgreSQL](06-postgresql-data.md) | `./examples/scenarios/run.sh 06` | Late opt-in, read-only source role, TLS logical copy, declared masks/subsets, relationship checks, application isolation, restart and storage cleanup |
| [07. One-command tests](07-one-command-tests.md) | `./examples/scenarios/run.sh 07` | TestRecipe, bounded credentials, local commands, JSON/JUnit, failure/timeout/retention, integrated chaos and verified cleanup |
| [08. Chaos experiments](08-chaos-experiments.md) | `./examples/scenarios/run.sh 08` | Late opt-in, all six faults, simultaneous stress, custom Jobs, target policy, network denial and rollback |
| [09. Pools and capacity](09-pools-capacity.md) | `./examples/scenarios/run.sh 09` | Native pool rendering, independent installations, quotas, destination selection and capacity admission |
| [10. Agents and inspection](10-agents-observability.md) | `./examples/scenarios/run.sh 10` | Doctor, plan/provenance and status, caller-authorized stdio MCP, local read-only dashboard |

The default for 01 is Helm installation. Run its CLI installer variant too:

```bash
./examples/scenarios/run.sh 01 cli
```

Scenario 03 defaults to cert-manager. Cover its other workload adapters with:

```bash
./examples/scenarios/run.sh 03 spark
./examples/scenarios/run.sh 03 trino
./examples/scenarios/run.sh 03 policy
```

Scenario 05 defaults to the `current` upgrade fixture. Cover both other starting points with:

```bash
./examples/scenarios/run.sh 05 previous
./examples/scenarios/run.sh 05 native
```

Scenarios 09 and 10 share one combined interface fixture, because the same real pool provides the host identities and ready replica used for agent and dashboard checks. Each entry runs that complete fixture. The index groups its behavior by user goal; it does not count two independent implementations as additional coverage.

## Read results and understand cleanup

The runner writes a receipt under `.cache/scenarios/run.*/report.json`, including the result and fixture-report paths, and checks that the lab left no new kind clusters behind. Each fixture also prints its evidence-directory path. Inspect its `artifacts/report.json` and associated sanitized status/inventory reports. A success report is written only after that fixture's assertions complete; an exit failure or missing success report is not a pass. Scenario 07 records a separate JSON report and JUnit XML for each intentionally different test outcome. Scenarios 05 and 09 also exercise real `replicove run` recipes. Expected denied operations and intentionally failed test commands appear in logs; the outer lab still succeeds only if those outcomes match its assertions.

The test verifies Replicove's owned-resource cleanup **before** the final kind-host deletion. Deleting the disposable host alone would not prove Replicove cleanup. Operator infrastructure, the state encryption key, and an empty capacity ledger intentionally survive individual replica removal. The final host deletion removes the complete lab, including those retained installation resources. Temporary kubeconfigs and tunnels are removed by the fixture's exit handler. Keep the printed evidence private; review it before sharing.

If a lab fails, start with its operator log and request conditions. Check Docker availability, image-download errors, Pending Pods, port conflicts and storage/CNI startup. Re-running creates a new uniquely named host; it does not repair an existing live cluster. If the process was forcibly killed before its exit handler ran, identify the exact lab cluster with `.cache/e2e-tools/kind get clusters` and remove only that named disposable cluster. A stale `.cache/scenarios/active.lock` may remain: check its recorded PID and confirm that lab has stopped before removing the lock. Do not remove Replicove finalizers to force a passing outcome. See [troubleshooting](../guides/troubleshooting.md) and [cleanup](../guides/cleanup.md).

## Coverage and boundaries

Together, the ten labs cover the currently implemented feature groups and their documented primary workflows. The [validation record](../validation.md) ties completed checks to their actual revision, released artifacts and environment. This is not an assertion that every API-field combination, operator, cloud, CNI or CSI driver works.

The fixtures use a pinned vCluster 0.37.1 / Kubernetes 1.36.0 guest. Network-sensitive labs install pinned Calico only inside their disposable kind host; mirrors use the test CSI host-path driver. They do not install test infrastructure into your normal kubeconfig context. Cloud identity, vendor certification, privileged host/node chaos, vCluster Platform provisioning and a remote certificate-authenticated MCP gateway remain outside this alpha. See [compatibility](../reference/compatibility.md) before extending a fixture to your own infrastructure.
