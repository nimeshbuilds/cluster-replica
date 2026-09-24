# Preflight and replica evidence

Run `replicove doctor -n replica-lab --grant source-dev` before creating a request. It checks host API discovery, Replicove API availability and the caller's request permissions through SelfSubjectAccessReview. It optionally inspects one grant without reading credential Secrets or provisioning workloads. Add `--replica NAME` to include a current replica's plan; `--output json` emits structured findings. A failed required API/permission check returns a nonzero exit code. An unknown check is not a pass.

This read-only command does not prove the operator's separate source permissions, CNI enforcement, CSI recovery, storage capacity, external identity, or application behavior. The controller performs capture and policy checks with its own identity. Network and storage qualification require disposable live probes; API discovery alone cannot establish those guarantees.

## Inspect the captured plan

```sh
replicove plan example -n replica-lab
replicove plan example -n replica-lab --json
replicove status example -n replica-lab
```

`plan` now renders an explanation rather than duplicating raw status. Its report contains the exact plan revision, capture time, expiry, pinned runtime profile, source object names/UIDs/resource versions, mapped identities, selection reasons, known dependency paths, transformation categories and observed omission counts. `status` continues to emit Kubernetes status JSON. The plan report is deliberately metadata-only: it never prints source/target values, Secret contents, Helm values, literal patches, or opaque custom field names. Older captures may lack the new provenance fields until a supported refresh or recreation.

Omission counts cover observed objects within the grant. They are not a census of the host cluster. Counts distinguish explicit exclusions, generated/infrastructure objects, and eligible objects not selected through a root or dependency. A required missing dependency blocks capture; it is not silently skipped. Helm inputs are identified as reconstructed desired resources, not an atomic copy of running controllers.

Readiness means the configured Kubernetes checks passed. The report explicitly identifies unverified external dependencies and distinguishes empty PVCs from copied data. Use a [test recipe](test-runs.md) to evaluate application behavior and retain a result tied to the captured revision. Source reads happen at multiple instants; the report cannot certify an exact whole-cluster snapshot.

Manual approval still applies to the captured revision. Review `plan` immediately before approving; if a concurrent refresh changes the revision, review the new report. Plans and source resource versions may disclose infrastructure names, so restrict read access to the request namespace appropriately.
