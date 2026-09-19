> **Design scope:** This document describes the complete intended product. The [current prototype](../../README.md) implements only a small runtime/lifecycle subset. Examples in this directory are proposals, not installable manifests.

# ClusterReplica: vCluster compatibility and maintenance policy

Proposed policy, 19 September 2026. This describes the repository and release process to build; no runtime version is certified yet. Read alongside the [implementation plan](cluster-replica-implementation-plan.md) and [product architecture](vcluster-wrapper-design.md).

## 1. Maintenance objective

Most compatible upstream releases should require a catalog change, tests, and a release review. Changes to vCluster configuration should usually require edits to one translator. Discovery, component installation, identity, data management, and the public ClusterReplica API should not need changes merely because a new vCluster version exists.

This is an architectural objective, not a guarantee. Changes in Kubernetes behavior, synchronization, authentication, storage, or upstream licensing can require implementation work and new tests. Keep the supported matrix small enough to verify. Do not fork vCluster or attempt to maintain every historical version.

## 2. Boundaries that keep changes contained

| Boundary | Contract we use | What stays behind the boundary |
| --- | --- | --- |
| Runtime lifecycle | Versioned Helm chart and Kubernetes resources | Chart names, values, image selection, installation and upgrade details |
| Platform integration | Documented management API | Platform API versions, project/template constraints, connection details |
| Guest operation | Kubernetes discovery and supported resource APIs | Guest endpoint, transport, authentication, Kubernetes version differences |
| Workload synchronization | Explicit capabilities and observed behavior | Version-specific service-account, metadata, networking and volume behavior |
| Component replication | Our adapter contract and captured package provenance | A particular operator's installation, configuration and verification rules |

Do not import upstream private Go packages, edit the vCluster control plane, duplicate its sync controller, or distribute chart-field conditionals throughout reconcilers. Avoid depending on generated host resource names. If a required mapping is not exposed reliably, the compatibility module must establish and test that mapping explicitly, or mark the capability unsupported.

The public CRD expresses intent: provider selection, a certified version policy, source selection, identity requirements, and data/access behavior. The compatibility module translates that intent into the exact runtime configuration. Validate generated values against the schema from the selected chart archive; vCluster distributes a JSON schema with its chart. Schema validation is followed by behavioral tests because valid values alone do not prove correct operation. [vCluster configuration reference](https://www.vcluster.com/docs/vcluster/configure/vcluster-yaml)

An optional advanced values override belongs to the runtime configuration surface. Validate it against that exact schema and reject overrides to protected ownership, access, identity, and lifecycle settings. Record the override in the capture and compatibility result. An arbitrary override does not inherit certification from the default profile.

## 3. Version resolution and support boundaries

Kubernetes version and API compatibility are the primary support axes. The generic controller and replication path must not require an EKS, AKS, GKE, OpenShift or RKE2 label. An observed distribution is context for diagnostics, known constraints and adapter selection. Support for its additional behavior is recorded separately.

Track these version axes separately:

1. ClusterReplica operator, CLI and API/storage schema.
2. vCluster chart, runtime image digest and required configuration translator.
3. Guest Kubernetes version and backing-store profile.
4. Host Kubernetes version/build, observed node versions, and required admission/networking/storage capabilities; retain distribution identity as diagnostic evidence.
5. Platform API/version when Platform manages the target.
6. Component adapters, operator/chart versions and container images.
7. Cloud identity and data adapter profiles.

`vcluster.versionPolicy: certified` resolves a new replica to an exact approved runtime version for its requested capabilities and environment. `kubernetesVersionPolicy: match-host` requests the host Kubernetes minor; resolution fails with alternatives if no certified guest combination exists. Pin a verified guest patch, preserving the host's raw vendor version for diagnostics without using it as an image tag or claiming the same vendor patches. Neither policy means downloading an upstream `latest` tag.

Resolution happens once, before creating the runtime, and is persisted with artifact identities. Record the runtime chart checksum, runtime image digest, guest version, provider, compatibility catalog revision, adapter versions, capture revision, and selected profile. An operator restart, catalog update, or new upstream release does not change an existing replica's runtime version. An explicit source refresh also preserves the runtime unless an explicit upgrade is requested.

The host can be upgraded independently. Reassess the host/guest/runtime pair and capability evidence after a detected host change; `match-host` is an initial selection rule, not a continuous guest-upgrade command. Report incompatibility, stop affected new mutations and preserve observation/cleanup. Include host upgrade drift and mixed node versions in qualification scenarios. Kubernetes component skew rules are relevant constraints, but they do not by themselves certify a virtual host/guest combination. [Kubernetes version skew policy](https://kubernetes.io/releases/version-skew-policy/)

For externally managed targets, record the observed versions and ownership. Recheck them before mutation and periodically while reconciling. If another manager upgrades the target to an unsupported combination, report incompatibility and stop dependent changes; do not downgrade it. TTL must still clean the resources we own without deleting the external target.

### Initial support policy

- Begin with two adjacent Kubernetes minors from the overlap of supported Kubernetes and the selected vCluster runtime. Test matching host/guest minors first. Treat a different guest minor as a separately requested and tested capability; the upstream matrix marks some cross-minor combinations as likely compatible rather than tested. [vCluster Kubernetes compatibility matrix](https://www.vcluster.com/docs/vcluster/manage/upgrade/supported_versions#kubernetes-compatibility-matrix)
- Aim to support the newest two **certified** vCluster minor lines that are within upstream support, subject to actual compatibility and available maintenance capacity.
- Approve exact patches and image digests within each line. A passing `0.x.a` does not certify every `0.x.*` patch.
- Publish exact tested host/guest/provider/component combinations. Do not imply the Cartesian product of several individually tested version lists is supported.
- For the existing host, publish supported Kubernetes version constraints and known exclusions, retaining exact tested host builds as evidence. The wrapper does not install or pin the host's vendor build. Passing generic preflight establishes eligibility for that capability profile; it does not establish full vendor-specific certification or substitute for workload verification.
- A Platform-managed combination is separate from a standalone Helm combination. Platform upgrades remain the administrator's responsibility.
- Separate OSS and licensed capability profiles. Verify the chosen artifacts and entitlements; a feature in the documentation is not proof that the OSS runtime includes it. [OSS and Free editions](https://www.vcluster.com/docs/vcluster/introduction/oss-vs-free)
- Stop accepting new replicas on retired combinations while retaining the code and resource records required to observe, export, migrate, and clean up existing replicas. Publish a migration deadline before removing normal reconciliation support.

Upstream currently describes three months of active support per minor, followed by three months of critical security fixes. Align support dates with upstream and revise the policy if its lifecycle changes. Do not promise to maintain an unsupported upstream runtime indefinitely. [vCluster version lifecycle](https://www.vcluster.com/docs/vcluster/manage/upgrade/supported_versions)

### Capability assessment supplements the version matrix

Use an automatically generated internal ClusterCapabilities report, not a user-authored distribution profile. Discover served APIs and schemas, inspect the selected component requirements, and collect relevant permission, storage, admission, networking and identity evidence. Unknown facts remain Unknown until resolved. The Kubernetes discovery and OpenAPI endpoints expose API availability and shapes; they do not establish every runtime feature or successful backend operation. [Kubernetes API discovery](https://kubernetes.io/docs/concepts/overview/kubernetes-api/)

| Layer | What is certified | How the core uses it |
| --- | --- | --- |
| Kubernetes/runtime | Host minor, guest minor, exact vCluster release, guest image and Kubernetes API behavior | Select and validate the virtual control plane |
| Components | Operator/chart versions, CRDs, webhooks and Kubernetes requirements | Decide whether the selected toolset can be recreated |
| Storage/network/admission | Required CSI/snapshot behavior, routing, policy enforcement and admission constraints | Choose supported configuration and data/capability adapters |
| Cloud integration | Requested identity binding and external service behavior | Invoke AWS/Azure/GCP adapters only for requests requiring them |

EKS is the first AWS integration lab. Cloud tests certify the AWS adapter and its exercised combinations; they do not gate release of a separately verified generic Kubernetes path. Conversely, a successful generic run cannot certify IRSA or another cloud integration. Two clusters on the same Kubernetes minor can need different capabilities: OpenShift, for example, documents additional security and endpoint permission requirements. [vCluster on OpenShift](https://www.vcluster.com/docs/vcluster/deploy/control-plane/kubernetes-pod/environment/openshift)

For shared-node replicas, a different guest API version still uses the host's worker infrastructure. Advertise an explicit cross-minor mode as API/operator compatibility testing, not a complete host-upgrade simulation.

## 4. Compatibility catalog

Ship a versioned catalog with the operator. Its entries contain exact artifact references, translator identity, required permissions, capability flags, tested environment tuples, supported upgrade edges, support dates, and test evidence. The controller must not need a remote catalog service to reconcile or delete existing replicas.

| State | Meaning | New replica behavior |
| --- | --- | --- |
| Candidate | An upstream release has been identified; testing is incomplete | Rejected by the normal certified policy; allowed only in an administrator-enabled evaluation profile |
| Certified | Required evidence exists for explicitly listed combinations | Eligible for version resolution within those combinations |
| Deprecated | Still usable for a defined transition period | Avoid by default; explicit selection reports the deadline and migration path |
| Blocked | A known defect, security issue, licensing mismatch, or retirement prevents new use | Reject new creation and affected upgrades; preserve ownership records and cleanup |

A certification record includes the wrapper build, catalog revision, exact versions/digests, environment profile, scenario results, run date, and immutable CI artifact references. Empty or stale required evidence blocks promotion. Documentation is generated from this catalog so the advertised support matrix matches what the controller accepts.

Release immutable, verified catalog artifacts. For the first release, bundle catalog changes with operator patch releases; this is simpler than hot updates. A later administrator-triggered import can support independently released signed catalogs after signature validation, schema validation, and wrapper compatibility checks. Never silently replace the active catalog from an unauthenticated remote feed.

Persist sufficient deletion metadata and the required cleanup handler version independently of the current catalog selection. Reconciliation and expiry cannot depend on a retired chart remaining in a public repository. Mirror or retain authorized runtime/package artifacts according to their distribution terms.

## 5. New upstream release workflow

Implement this as repository CI beginning in Phase 1. This document does not create a scheduled automation.

1. **Discover:** a scheduled repository workflow or manual dispatch checks official vCluster releases and opens a candidate update PR. Independently track Kubernetes releases and API removals; add a Kubernetes candidate only when the selected runtime can support it. Record the exact release and artifact identities; never promote a moving tag.
2. **Inspect:** retrieve the exact chart and image metadata. Compare values, JSON schema, rendered resources, RBAC, CRDs, defaults, dependencies, release notes and edition requirements with the previous certified version. Verify artifact provenance or signatures where available; record when only a digest/checksum can be established.
3. **Validate contracts:** render supported profiles, validate values, run deterministic planner fixtures, and test provider/API response handling. Unknown or removed fields must fail visibly.
4. **Exercise Kubernetes:** run the core matrix across the supported host/guest minor pairs using exact test artifacts. Provision through the wrapper, connect, install the supported toolset, and execute representative workloads. Test creation failures, retries, expiration during provisioning, operator restarts, host upgrade drift, API removals and complete owned-resource cleanup.
5. **Exercise affected providers:** test identity, storage, networking and provider behavior on the actual environments advertised for that candidate. Reusing unit-test results is not a substitute for an AWS identity test or an OpenShift admission test.
6. **Exercise upgrades and recovery:** run every new supported upgrade edge, wrapper upgrade with existing replicas, and the associated backup/restore recovery procedure. Ephemeral replacement tests must use the preserved capture.
7. **Classify:** decide whether the change is catalog-only, needs a configuration translator, changes a capability, or requires architectural work. Mark unsupported profiles explicitly instead of blocking all progress or overstating coverage.
8. **Promote:** a maintainer reviews evidence and the support matrix. Merge and publish the new catalog/operator release with known limitations and migration notes. This authorizes new compatible replicas; it does not upgrade existing customer runtimes.

Use fast local and generic Kubernetes tests on ordinary PRs. Run the selected version matrix for relevant candidates, targeted capability/cloud suites when their adapters change, and broader environment qualification before expanding advertised integrations. A Kubernetes minor release can change API and feature behavior; a vCluster release can change synchronization/configuration; an operator release can change its CRDs and dependencies. Route each change to the relevant suites. Only mark the combinations that actually passed. Keep a manual release path for urgent fixes; it uses the same evidence gates.

Cloud jobs use short-lived credentials, constrained permissions, quotas, cost limits and bounded execution. Do not provide cloud credentials to untrusted fork code. A separate lab cleanup process uses recorded ownership and run identifiers to remove abandoned test infrastructure. Test-data deletion does not rely solely on a successful test exit.

## 6. Runtime upgrades and recovery

**Ephemeral replicas:** default to replacement. Create a replacement from the saved capture with an explicitly selected certified runtime, verify it, and give the user the new endpoint. Delete the old replica when its policy permits. Never let replacement delete data still used by either instance; data ownership and any transfer must be explicit.

**Longer-lived replicas:** expose a proposed `replica upgrade plan` command and an explicit upgrade operation in the API. The plan shows the source/target versions, intermediate steps, configuration changes, downtime expectations, backing-store requirements, data handling, and recovery procedure. Do not start an upgrade too close to the expiry deadline to complete or recover; TTL remains authoritative unless explicitly changed within policy.

Use only certified sequential upgrade edges. Upstream recommends one minor version at a time and warns that larger jumps are not explicitly tested. Its documentation also describes configuration and datastore constraints, so a Helm upgrade alone is insufficient proof of a safe migration. [vCluster upgrade guidance](https://www.vcluster.com/docs/vcluster/manage/upgrade/upgrade-version)

Before an in-place upgrade, verify a usable backup and record its runtime/configuration requirements. Distinguish guest control-plane state from application data, cloud identities, and externally managed services. Test recovery on the actual backing-store profile. A chart rollback does not establish that an older runtime can read a newer datastore. Where reversal is unsafe, recover into a compatible replacement from the backup and capture, then verify data and access before changing endpoints.

Upgrade through the selected owner: Helm for wrapper-owned releases, Platform's supported API for authorized Platform resources. For an externally owned vCluster, produce the compatibility plan and wait for its manager to perform lifecycle changes. Do not fight GitOps or the existing manager.

## 7. Upgrading ClusterReplica itself

Operator releases must continue to reconcile existing persisted plans, ownership records and operation states. Test an old operator creating a replica, replace it with the candidate operator, then observe, connect, refresh within policy and expire the replica without an unintended runtime upgrade.

Version capture and inventory schemas. Prefer additive changes, provide explicit migrations, and test partial migrations with restarts. For CRD version changes, provide conversion and a storage migration procedure before removing a served version. Preserve the admission/conversion service during its rollout. Uninstall documentation must prevent deleting CRDs while active replicas and cleanup obligations remain.

Keep one operator binary with the small set of supported compatibility handlers. Remove an obsolete handler only when the published migration window has passed and cleanup remains possible through a supported path. Document operator downgrade limits; a previous binary may not understand newer persisted state.

## 8. Triage and ownership

| Failure after an upstream change | First module to inspect | Required proof before accepting the fix |
| --- | --- | --- |
| Removed or renamed chart field | vCluster compatibility translator | Schema/render checks and a real create/connect/delete run |
| Platform API or template behavior change | Platform provider | Policy-preserving lifecycle tests against the affected Platform version |
| Changed host service-account mapping | Compatibility and identity adapters | Correct cloud identity plus negative permission tests |
| Guest Kubernetes API removal | Discovery/planner and affected component adapter | Capture, transformation and representative component behavior |
| Changed storage or snapshot behavior | Runtime/data adapter | Workload I/O, recovery and verified deletion of owned storage |
| Access endpoint or token behavior change | Runtime/access provider | Human/CI connection, refresh, expiration and streaming tests |
| A source operator changes its CRDs or defaults | That component adapter | Installation ordering, selected settings and its workload probe |

Assign maintainers by these module boundaries, with a second reviewer for identity, access and deletion changes. Each adapter includes a support declaration, fixtures, behavioral probes, cleanup requirements and troubleshooting notes. An adapter without a maintainer or passing evidence can remain experimental without being advertised as generally supported.

Proposed working cadence: triage new upstream patches within two business days and start minor-release qualification within one to two weeks. These are planning targets, not a service guarantee. Certification has no fixed deadline when a release breaks required behavior. Security fixes receive immediate impact assessment; publish temporary restrictions or upgrade guidance when certification is not yet possible.

Track maintenance effort per candidate: investigation time, files/modules changed, CI cost, regressions and time to certification. If each routine release requires broad changes, narrow the boundary or support matrix before adding more integrations. The practical success measure is that an ordinary compatible release becomes a small reviewed update with reproducible evidence.

## 9. Required shipping artifacts

- A certified version catalog and generated compatibility table.
- A stable public CRD schema with documented version resolution and lifecycle behavior.
- A tested runtime translation layer and provider contracts.
- Reusable test scenarios for creation, replication, access, identity, workload execution, expiry, upgrade and recovery.
- A contributor guide explaining how to add one runtime version or one component adapter.
- Upgrade, recovery, deprecation, troubleshooting and uninstall procedures.
- Reproducible releases with image/chart identities, provenance where supported, an SBOM, and a security reporting process.

These artifacts let contributors maintain specific integrations without learning or rewriting the entire controller. They also make it clear which combinations work today and which still require implementation or testing.
