# Replicove security

Replicove is an experimental portable alpha with no supported production release. Use disposable environments operated by trusted administrators. Read the [project status](docs/project-status.md) and [configuration guide](docs/replicove-quickstart.md) before granting access.

Report vulnerabilities using [GitHub private reporting](https://github.com/nimeshbuilds/replicove/security/advisories/new). Include the affected revision and a minimal sanitized reproduction. Do not put credentials, kubeconfigs, source Secret data, or private cluster captures in public issues. No response-time or historical-version maintenance guarantee is offered.

## Authorization boundaries

- A host administrator installs CRDs and creates cluster-scoped `ReplicaGrant` objects. Request creators cannot expand a grant. Source reads require both installer-supplied RBAC and a matching grant; source permissions should remain read-only.
- The chart installs the operator and encrypted state in an administrator-only system namespace, separate from the runtime destination. Its runtime Role names the resources and verbs required by the pinned vCluster chart and cleanup inventory. It grants no wildcard resources, `bind`, `escalate`, or impersonation. Chart contracts detect upstream permission changes; real API and live-cluster tests exercise namespace boundaries and rejected privilege escalation.
- The operator has limited host cluster reads: its named grants, the `kube-system` namespace identity, and bound PV metadata for cleanup. Optional source cluster reads are explicit chart values. It cannot create host namespaces, CRDs, ClusterRoles, or ClusterRoleBindings with the default chart.
- Namespace delegation is the access model. Anyone able to administer destination workloads or read destination Secrets is trusted with the runtime, including its upstream administrator kubeconfig. The protected system namespace must remain outside that delegation.
- `ReplicaAccess` issues expiring guest viewer/deployer/admin credentials bounded by the grant and replica TTL. Administrator-selected subjects can receive exact-Secret read permissions; they can read sessions under that grant. This is not per-user request ownership. Consumers must not receive broad destination Secret access. Revocation removes credential-reader bindings, guest identities, and generated credential Secrets.

## Shared workers, networking and data

vCluster separates Kubernetes API state while guest workloads run on host workers. Host admission, Pod Security, CNI/NetworkPolicy enforcement, quotas, storage and node isolation remain the host administrator's responsibility. The portable planner rejects captured privileged/host-network/host-path workloads, but it is not an admission policy for every object a guest administrator may later create. Host namespace API authorization tests do not establish tenant network or kernel isolation. A fresh namespace alone is not a security boundary for hostile workloads.

Secret replication is denied by default and requires exact grants. Source service-account/bootstrap tokens are excluded. Captures and ownership inventories are encrypted with an immutable key in the system namespace; protect and back up that key with its state. Ordinary status and retained CI artifacts exclude credential payloads. Do not delete the key while finalizers remain.

`DeleteOwned` respects recorded UIDs and finalizers, revokes supported access, and removes supported owned resources. Bound volumes require Delete reclaim policy and verified disappearance. It does not erase source data, snapshots, retained/shared storage, or arbitrary external services. The legacy `HelmReleaseOnly` mode and broad lab bootstrap manifest are for runtime development and have a smaller cleanup and permission contract.

Cloud identity exchange, data restoration, vCluster Platform provisioning, signed releases, and production hardening remain [roadmap work](ROADMAP.md). Unsupported adapters block explicitly. A passing portable test does not certify cloud or distribution behavior.
