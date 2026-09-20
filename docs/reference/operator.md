# Operator configuration

The operator chart is in [`charts/replicove`](../../charts/replicove/). The CLI embeds this chart. `make manifests` renders the native YAML installer from the same templates, with in-cluster key bootstrap enabled.

## Helm values

| Value | Default | Meaning |
| --- | --- | --- |
| `image.repository` | `ghcr.io/nimeshbuilds/replicove` | Public operator image repository |
| `image.tag` | `0.1.0-alpha.1` | Versioned alpha image tag |
| `image.digest` | Empty in source; set by release packaging | SHA-256 image digest; takes precedence over tag |
| `image.pullPolicy` | `IfNotPresent` | Kubernetes image pull behavior |
| `destinationNamespace` | `replica-lab` | The one namespace watched by this operator |
| `createDestinationNamespace` | `true` | Create if absent; retain on uninstall; reuse existing namespaces without adoption |
| `stateKey.bootstrap` | `false` | Helm creates/reuses the key Secret by default; `true` uses the bounded bootstrap Job instead |
| `sources` | `[]` | Source namespace and read-only RBAC rule entries |
| `clusterReadRules` | `[]` | Additional explicit read-only cluster resource rules |
| `resources.requests.cpu` | `100m` | Operator CPU request |
| `resources.requests.memory` | `256Mi` | Operator memory request |
| `resources.limits.memory` | `1Gi` | Operator memory limit |

The release namespace is the protected state namespace. It must differ from `destinationNamespace`. The native installer fixes the default names; customize every corresponding namespace/RBAC/argument reference together if changing them. This alpha does not provide a single native-manifest namespace switch.

`sources` entries have `namespace` and Kubernetes `rules`; the chart creates a Role and RoleBinding in each source. Sources must already exist. Use `get` and `list` only for desired source reads. Add cluster reads only for inputs your grant permits. The cluster grant reader needs `get replicagrants`; host `kube-system` identity and bound PV inspection are also explicit built-in reads.

The chart does not expose arbitrary vCluster values. Runtime configuration is an exact catalog profile. The published operator chart pins its multiarchitecture image by digest. Upstream vCluster and guest-image digest locking remains separate work. See [remaining release gates](../project-status.md).

## Operator process flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--watch-namespace` | Required for controller mode | One destination namespace |
| `--state-namespace` | Empty | Enable full workflow with a separate protected namespace; installed chart sets it |
| `--chart-path` | Empty | Administrator-supplied vCluster archive; SHA-256 must still match the profile |
| `--health-probe-bind-address` | `:8081` | HTTP health/readiness probe listener |
| `--bootstrap-state-key` | `false` | One-shot immutable key initialization/validation, then exit |

Running without a state namespace retains only the legacy runtime controller. It is not a complete replica deployment. Use the supported installers for full functionality.

The binary also exposes controller-runtime's Zap logging flags; run `bin/cluster-replica --help` for their exact names. Logs and status should not contain Secret payloads. Metrics serving is disabled in the current operator. Leadership uses a namespaced Lease; the chart runs one operator replica.

## Deployment and bootstrap security

The operator and bootstrap Job run as non-root UID/GID 65532, use RuntimeDefault seccomp, drop capabilities, prohibit privilege escalation, and use a read-only root filesystem. The operator has a memory-backed `/tmp`. The bootstrap Job's separate service account can only get/list/create Secrets in the protected namespace; it cannot update or delete an existing key. It has a five-minute active deadline and three retries.

The key is an immutable Opaque Secret with 32 random bytes, generated inside the cluster for YAML installations. Reinitialization must preserve it; missing-key recovery requires the original key if protected records exist.

Probe success indicates the process is available, not that every replica or external storage dependency is healthy. Observe individual request conditions as well as Deployment readiness.

## Generated files and upgrades

Edit the chart templates and run `make generate` to regenerate CRDs, embedded chart schemas, and native installation manifests. CI fails if checked-in generated files differ. Review permissions alongside the pinned upstream chart contract.

Follow [installation upgrades](../getting-started/installation.md#upgrades-and-removal) and [runtime maintenance](../maintaining-replicove.md) for key retention, CRD updates, immutable Job replacement, and compatibility qualification.
