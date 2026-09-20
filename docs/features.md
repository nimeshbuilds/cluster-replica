# Features and their limits

This is the feature map for **Replicove v0.2.0-alpha.2**. The optional mirror controller ships in the same operator image and chart; it is disabled by default and can be [enabled later](guides/enable-mirroring.md). Replicove is an experimental alpha. [Validation](validation.md) separates actual live evidence from unqualified integrations.

## Installation and lifecycle

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| One-shot installation | Public OCI Helm chart, CLI installer, or native YAML with protected key bootstrap | [Installation](getting-started/installation.md); CLI installation uses Helm internally, and end users of native YAML do not need either CLI |
| New vCluster | Downloads the pinned chart and provisions a guest when an approved request needs one | [Helm quickstart](getting-started/helm.md); one vCluster 0.37.1 runtime per host namespace |
| Existing vCluster | Uses an explicitly registered guest with administrator-pinned identity and credentials | [Existing targets](guides/existing.md); no automatic discovery/adoption, no removal of the independently managed runtime |
| Bounded lifetime | TTL includes planning, approval and provisioning; owned cleanup runs on expiry/deletion | [TTL and cleanup](guides/cleanup.md); refresh/reset does not renew the request's lifetime |
| Key and state persistence | Encrypted capture/access state with an immutable key in a separate protected namespace | [Key persistence](getting-started/installation.md#key-initialization-and-persistence); back up key and state together |
| Upgrades and optional modules | Reuses installation identity, destination and key; mirroring can be added after installation | [Enable mirroring later](guides/enable-mirroring.md); update CRDs and preserve reviewed values |

## Selecting and reproducing configuration

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| Administrator policy | `ReplicaGrant` bounds source namespaces/kinds, secrets, existing targets, profiles, TTL and guest roles | [Grants and RBAC](guides/grants.md); a grant does not replace host Kubernetes RBAC |
| Selection and dependencies | Namespace/name/label selectors, known dependency planning, mappings and explicit patches | [Selection](guides/selection.md); does not infer every arbitrary operator dependency or embedded configuration string |
| Helm releases and operators | Reconstructs selected Helm desired resources and values, CRDs and authorized operator resources | [Operators](guides/operators.md); external side effects and arbitrary hooks are not a generic clone contract |
| Secrets | Explicit snapshot/follow modes within an administrator's exact permissions | [Secrets and storage](guides/secrets-storage.md); host service-account tokens are excluded and cloud identity is not automatically cloned |
| Plans and approval | Capture a bounded plan and approve the exact revision when manual approval is selected | [Lifecycle](guides/lifecycle.md); host capture is multiple API reads, not an atomic etcd snapshot |
| Refresh and drift | Explicitly capture and apply updated source selection after deployment; report conflicts and preserve foreign ownership | [Lifecycle](guides/lifecycle.md); refresh is not a PVC data reset |
| Ordinary application PVCs | Explicitly authorized empty destination claims | [Secrets and storage](guides/secrets-storage.md); use the optional mirror adapter to copy supported source data |

## Workload data mirrors

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| Granted CSI data capture | Copies selected workloads and exact granted filesystem PVCs into independent writable generations | [Mirrors](guides/mirrors.md); qualified driver/classes and host networking are required |
| Manual and scheduled sync | Capture latest source state and replace the test generation via immutable `ReplicaMirrorRun` requests or an interval | [Sync/reset](guides/mirrors.md#sync-latest-data-or-reset-to-a-saved-capture); scheduling is a trigger, not an instantaneous copy |
| Saved revision reset | Recreate a retained capture's configuration and per-volume recovery points | [Mirrors](guides/mirrors.md); no memory checkpoint, atomic multi-volume state or application-consistent database guarantee |
| Test leases and retention | Bound active testing, defer replacement, and keep a grant-limited number of captures | [Mirrors](guides/mirrors.md); whole-mirror TTL still wins and forced reset needs explicit authorization |
| Generation access | Scoped expiring access resolves one active generation; reconnect after replacement | [Mirrors](guides/mirrors.md); sessions are not silently moved to the next generation |
| Managed or existing target | Dedicated replacement runtime, or separate generation namespaces in an explicitly registered runtime | [Target choices](guides/mirrors.md#create-a-mirror-through-yaml-or-the-cli); managed resets interrupt availability |
| Deletion and expiry | Revoke access and verify deletion of owned restores, captures and Kubernetes storage records | [Cleanup](guides/cleanup.md); does not certify physical erasure of external backups or other provider systems |

## Humans, agents and contributors

| Feature | What it does | Guide and limits |
| --- | --- | --- |
| Kubernetes authentication | Humans/CI/agents use kubeconfig or ServiceAccount credentials against the host API | [Access](guides/access.md); Kubernetes RBAC controls requests, and grants constrain what Replicove may reproduce |
| Guest authorization | `ReplicaAccess` issues expiring viewer/deployer/admin sessions, with exact credential-Secret reader RBAC | [Access](guides/access.md); guest roles are separate from host API permissions |
| CLI or YAML workflows | CLI commands and Kubernetes manifests operate the same CRDs; the CLI can maintain a local tunnel | [CLI quickstart](../QUICKSTART.md), [YAML quickstart](getting-started/yaml.md), [GitOps](guides/gitops.md) |
| Complete field/command references | API tables generated from all five CRDs; command help generated from the CLI binary | [API](reference/api.md), [CLI](reference/cli.md), [operator values/flags](reference/operator.md) |
| Compatibility maintenance | Pinned runtime profiles, schema/RBAC contracts, real API tests, disposable host/guest suites | [Compatibility](reference/compatibility.md), [maintenance](maintaining-replicove.md); cloud and distribution certification is separate |

## Not implemented or not qualified

- No optional MCP server, Replicove certificate-authority/agent-identity service, or UI dashboard is shipped. Agents can use the existing Kubernetes API/CLI today.
- No automatic IRSA, EKS Pod Identity, Azure/GCP workload-identity exchange or cloud IAM cloning. Unadapted identity inputs are blocked; copying an annotation is not a working identity adapter.
- No exact clone of every host setting, cloud service or distribution feature. Shared-worker vClusters also share host compute/network boundaries.
- No generic external database/bucket/queue cloning, application-consistent backup adapter or atomic multi-PVC capture. Cloud CSI behavior still needs cloud-lab qualification.
- vCluster Platform, OpenShift/RKE2-specific behavior, scale/chaos hardening and production support are not qualified by the disposable kind tests.

The [implementation ledger](IMPLEMENTATION_STATUS.md), [project status](project-status.md) and [roadmap](../ROADMAP.md) track these gaps. New releases must update guides, generated references and matching test evidence together; a proposed feature is not listed as delivered until it exists and its stated validation passes.
