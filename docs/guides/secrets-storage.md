# Secrets and storage

Secrets and volume contents are separate capabilities. Replicove supports explicitly granted Secret replication and fresh PVC provisioning. It does not currently restore source volume data or recreate cloud identities.

## Secret modes

| Mode | Behavior |
| --- | --- |
| `None` (default) | Do not copy raw source Secrets |
| `Snapshot` | Capture allowed Secrets with the plan; refresh explicitly for later changes |
| `Follow` | After initial apply, reconcile raw, individually granted Secret changes from the source |

Both the grant and the request must opt in:

```yaml
# ReplicaGrant.spec
secrets:
  - namespace: source-dev
    name: application-credentials
```

```yaml
# ClusterReplica.spec.replication
secrets: Snapshot
```

The operator needs the corresponding source Secret read permissions. Prefer exact grants; `name: "*"` is an explicit administrator capability and can expose unrelated credentials within the allowed source scope.

ServiceAccount tokens, bootstrap tokens, and Helm release Secrets are excluded from raw Secret copying. Helm releases use their own grant and capture path. Chart-generated credentials are part of the captured chart snapshot; `Follow` does not independently rotate them.

A copied credential may still point to the same external database or account. Supply isolated test credentials or override endpoints before running workloads. Copying a Secret does not create a safe external-service replica.

## Protect captured data

Captures and ownership records are compressed and encrypted with AES-256-GCM in the protected namespace. Owner UID is authenticated with the ciphertext; a keyed digest identifies the plan. Status exposes identities and counts, not Secret values or kubeconfigs.

Back up `replicove-state-key` together with encrypted state. Restrict the protected namespace to administrators. Do not put backups, kubeconfigs, or actual credential payloads in Git, CI artifacts, or issue reports. The [security policy](../../SECURITY.md) describes the shared-cluster boundary and reporting process.

## Fresh application volumes

```yaml
# ReplicaGrant.spec
allowEmptyVolumes: true
```

```yaml
# ClusterReplica.spec.replication
data: EmptyVolumes
storageClassMap:
  production-fast: standard
```

`EmptyVolumes` creates new PVCs; it does **not** copy source files, database state, CSI snapshots, or external storage. Application bootstrapping must tolerate an empty volume or supply its own test-data setup. `None` is the default data mode; a required unsupported volume dependency blocks replication.

The persistent **control-plane** PVC is distinct from application PVCs. The default persistent profile uses a fresh 1 GiB PVC on the host default StorageClass. It preserves the guest control-plane identity across pod rescheduling.

For recorded bound volumes, cleanup requires `Delete` reclaim behavior and waits for the exact PV identity to disappear. A Retain policy or unavailable storage controller can block cleanup; Replicove does not force-delete arbitrary PVs or promise physical erasure of provider backups.

## Cloud identity and host-specific resources

IRSA, EKS Pod Identity, Azure/GCP workload identity, cloud load balancers, CSI data restores, and external infrastructure recreation are not implemented/certified by the portable workflow. Unadapted cloud identity annotations, privileged containers, host paths, and host networking are blocked. A future adapter must demonstrate the full identity and cleanup lifecycle in a disposable cloud lab.

See [compatibility and limits](../reference/compatibility.md) before interpreting “replica” as an exact clone of every host capability.
