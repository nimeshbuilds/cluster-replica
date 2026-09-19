# Replicove naming research

Decision: **Replicove**, pronounced “REH-pli-kohv.” Research date: 19 September 2026.

The name combines replica with cove: a separate place to build and test with the tools from an existing Kubernetes cluster. The descriptor is “Kubernetes replica environments, powered by vCluster.” The short tagline is “Your cluster’s tools. A fresh place to test.” The name does not imply a complete clone of host infrastructure or automatic isolation of external data.

## Checks performed

| Candidate | Findings | Decision |
| --- | --- | --- |
| Twinplane | GitHub returned existing `joo-okkim/twinplane` and `twinplane-backend` software repositories. Broader web results also included aviation and measurement uses. | Reject: existing software name. |
| ClusterMint | Web search found an existing ClusterMint Strategy business. GitHub also returned a partial-name match, ClusterMintic. | Reject: avoid the existing exact business name. |
| KubeMorrow | No exact GitHub repository, npm package, or PyPI package was returned. | Reserve as an alternative; less directly connected to replication. |
| Replicove | No GitHub repository matching `replicove in:name`; npm and PyPI exact package endpoints returned 404. Web queries for the exact name plus Kubernetes/software/cloud/company and a trademark keyword returned no exact software/product match. A Czech inflection of “replica” appeared as an unrelated search result. | Select: distinctive, pronounceable, relevant. |

The `.com` RDAP endpoint returned 404 for both Replicove and KubeMorrow. This records the lookup result only: no domain was purchased or reserved. Search indexes and registry results can be incomplete or change; these checks are not legal trademark clearance.

## Reproducible sources

- [Existing Twinplane repository](https://github.com/joo-okkim/twinplane)
- [Existing Twinplane backend](https://github.com/joo-okkim/twinplane-backend)
- [ClusterMint Strategy](https://hub-site.one/)
- GitHub REST search: `GET https://api.github.com/search/repositories?q=replicove+in:name` (zero results at the time of research).
- [npm exact package lookup](https://registry.npmjs.org/replicove) (404).
- [PyPI exact package lookup](https://pypi.org/pypi/replicove/json) (404).
- [Verisign .com RDAP lookup](https://rdap.verisign.com/com/v1/domain/replicove.com) (404).
- Web queries: `"Replicove" Kubernetes OR software OR cloud OR company`; `"Replicove" site:artifacthub.io OR site:npmjs.com OR site:pypi.org`; `"Replicove" trademark`.

The public repository is now `nimeshbuilds/replicove`. Retain the existing Kubernetes API group and Go module identity during the alpha to preserve current manifests and imports. Replicove is the product brand; `ClusterReplica` remains the Kubernetes resource kind.
