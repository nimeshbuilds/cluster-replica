# Replicove brand guide

**Replicove** (REH-pli-kohv) combines “replica” and “cove”: a fresh place to test with familiar cluster tools.

| Element | Value |
| --- | --- |
| Name | Replicove; lowercase `replicove` in URLs and commands |
| Tagline | Your cluster’s tools. A fresh place to test. |
| Descriptor | Kubernetes replica environments, powered by vCluster |
| Primary color | Indigo `#293FC9` |
| Accent | Teal `#00BFA5`; use a darker shade for small text on white |
| Repository | https://github.com/nimeshbuilds/replicove |
| Maintainer organization | [Nimesh Builds](https://github.com/nimeshbuilds) |

## Assets

- [Product icon](../../assets/brand/replicove-icon.png): two matching cubes inside an open cove. Use on a light background and preserve its aspect ratio.
- [Repository hero and social preview](../../assets/brand/replicove-social.png): a wide image with the wordmark, tagline, and replica-environment illustration.
- [Name research](research.md) and [icon provenance](icon-prompt.md).
- [Social-preview generation prompt](social-prompt.md).

Keep clear space around the mark. Use the supplied assets when describing Replicove; do not imply affiliation or endorsement for another project. Repository assets are provided under the [repository license](../../LICENSE); third-party names and marks belong to their respective owners. The name research is a preliminary collision check, not trademark clearance.

## Describe the project accurately

Short description:

> Replicove is an experimental open-source Kubernetes operator for disposable integration-test environments, powered by vCluster.

Long description:

> Replicove is building a declarative workflow to recreate selected Kubernetes operators and configuration in disposable virtual clusters. It adds source selection, replication plans, access, and lifecycle management around vCluster. The default branch contains a runtime prototype, with the broader portable alpha under development and tested in disposable CI clusters.

Link to [project status](../project-status.md) whenever describing compatibility or availability. Avoid “exact clone of any cluster,” production-readiness claims, unverified cloud support, or claims that TTL guarantees deletion of all external data.

## Maintain the public repository

Keep the repository description, topics, README introduction, and status page aligned. Use specific terms naturally: Kubernetes, virtual clusters, vCluster, integration testing, operators, and ephemeral environments. GitHub controls search indexing; metadata and clear documentation improve discoverability but cannot guarantee rankings or stars.

When changing the social image, commit it here and upload it in **Settings → General → Social preview**. GitHub recommends a 2:1 image, at least 640×320 pixels. Check the rendered README and a repository link preview after publishing. Keep descriptions readable instead of adding repeated keyword lists.

The repository was renamed from `cluster-replica` to `replicove`. The original Go module, prototype binary, and CRD identifiers remain compatible; update public links to the canonical repository URL. Do not create a new repository at the old path, which would interfere with GitHub's redirect.
