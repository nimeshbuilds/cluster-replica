# Security

ClusterReplica is an experimental prototype with no supported production release. Use a disposable namespace controlled by a trusted administrator. Anyone able to administer workloads or secrets in that namespace is within the prototype's trust boundary.

Report a vulnerability using [GitHub's private reporting form](https://github.com/nimeshbuilds/cluster-replica/security/advisories/new). Do not open a public issue containing credentials, kubeconfigs, secret manifests, or instructions targeting a live private cluster. No response-time guarantee is currently offered.

Current limits: broad namespaced execution permissions; no source secret replication; no complete guest-resource inventory; no short-lived access broker; no complete credential revocation; no certified cloud integrations. The `HelmReleaseOnly` policy does not promise deletion of PVCs, generated kubeconfig secrets, or external services.
