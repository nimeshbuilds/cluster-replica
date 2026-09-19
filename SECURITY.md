# Replicove security

Replicove is an experimental prototype with no supported production release. Use a disposable namespace controlled by a trusted administrator. Anyone able to administer workloads or secrets in that namespace is within the prototype's trust boundary.

Report a vulnerability using [GitHub's private reporting form](https://github.com/nimeshbuilds/replicove/security/advisories/new). Do not open a public issue containing credentials, kubeconfigs, secret manifests, or instructions targeting a live private cluster. No response-time guarantee is currently offered.

Default-branch limits: broad namespaced execution permissions; no source secret replication; no complete guest-resource inventory; no short-lived access broker; no complete credential revocation; no certified cloud integrations. The `HelmReleaseOnly` policy does not promise deletion of PVCs, generated kubeconfig secrets, or external services.

The portable alpha has additional security controls under development in [PR #8](https://github.com/nimeshbuilds/replicove/pull/8); see [project status](docs/project-status.md) for separately tested capabilities. Private reports should identify the affected revision and include a minimal reproduction against a disposable environment, without live credentials or private data. Historical revisions have no maintenance guarantee.
