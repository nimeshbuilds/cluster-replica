# PostgreSQL copies with declared masking

Replicove can prepare a PostgreSQL database before applying application workloads
or issuing guest sessions. This is an opt-in logical copy for a **new managed
vCluster**. Existing targets, CSI mirror combinations, and in-place recapture or
refresh are rejected. Recreate the replica to capture a newer database snapshot.

The first adapter uses PostgreSQL 17 `pg_dump` and `pg_restore`. PostgreSQL supplies
the consistent snapshot of one database. This does not establish a coordinated
snapshot across independent databases, queues, files, or Kubernetes object capture.
The protected capture timestamp records when the operation started, not an exact
transaction LSN. There are no retained raw database archives or database reset
revisions in this adapter.

## Administrator setup

Use [the grant example](../../examples/postgresql/grant.yaml) as a starting point.
Each `DatabaseGrant` explicitly names a source host, database, source namespace,
credential Secret, target image and storage class, byte/time limits, and required
masking rules. A PVC grant, a general Secret grant, or access to a source namespace
does not delegate database reads. The source namespace must also be in the
ReplicaGrant's `sourceNamespaces`.

The source Secret must be in the protected operator state namespace, with
`username` and `password` keys. Provision it through your normal secret-management
process. The role needs LOGIN, CONNECT, schema USAGE, table SELECT, and the
sequence reads needed for the selected database. It must not own source objects,
have write grants, inherit privileged roles, or be a superuser. Source credentials
remain in the operator process and its `pg_dump` environment; they are never
copied to the guest, written into status, or put into command arguments.

The operator also sets `default_transaction_read_only=on`, a statement timeout,
and a lock timeout for the source session. It runs only fixed `pg_dump` options;
the API accepts no arbitrary source SQL, source hooks, or connection strings.
The underlying account privileges remain the administrator's responsibility.

`sslMode: verify-full` uses the operator image's trusted system CAs and verifies
the source hostname. For private PKI, qualify an operator image with the proper
trust store. An administrator may explicitly select `require` for encryption
without hostname verification. Plaintext network source connections are rejected.

Qualify the target image as docker-library PostgreSQL 17, running as UID/GID 999,
and pin its sha256 digest. The provided example pins both supported architectures.
The target storage class must support dynamically provisioned, independently owned
volumes with Delete reclamation. The final database uses a PVC; the raw staging
database uses a memory-backed `emptyDir`. `maxBytes` limits each uncompressed
archive stream, the staging volume, and requested final storage. Indexes and WAL
consume space beyond the archive size; leave sufficient headroom. The target Pod
memory limit includes `maxBytes` plus 256 MiB. Source databases too large for this
bounded staging method need a separate qualified adapter.

Set `networkPolicyEnforced: true` only after confirming host CNI enforcement.
Replicove creates **host** ingress and egress deny policies before database Pods,
checks translated vCluster Pod labels, and rejects overlapping allow policies
before transfer. Guest NetworkPolicies alone are not a security boundary because
the pinned runtime does not synchronize them to the host. PostgreSQL TCP auth also
rejects all connections during staging. The operator uses guest exec and a local
Unix socket to restore and validate.

Replicated application Pods also receive a host egress boundary before capture.
In every guest namespace recorded in the plan, applications can reach peers in
that replica's planned namespaces, its CoreDNS on ports 53/1053, and its virtual
API on ports 443/6443/8443. Other host services, original database endpoints, and
external destinations are denied. The synthesized database Pods are excluded
from these allowances and retain zero egress throughout staging and after
publication. All additional host egress allow policies in the destination
namespace are conservatively rejected, because Kubernetes policies are additive.
This adapter therefore requires a destination without those conflicting policies.

This boundary protects captured application namespaces under the qualified host
CNI. Host administrators and guest cluster-admin recipients remain trusted;
creating workloads in an additional guest namespace is outside the captured
policy scope. Use namespace-scoped sessions for less-trusted test clients. Plans
containing workloads in Kubernetes system namespaces are rejected. The boundary
does not replace review of copied Secrets, URLs, application behavior, or access
roles. External dependency allowances are not part of this adapter.

## Request and application wiring

Adapt [the replication example](../../examples/postgresql/replication.yaml), including
the application image and full container configuration. JSON merge patches replace
arrays; the example's `containers` array is illustrative and must include any
existing sidecars, ports, mounts, probes, and other required fields.

Each requested database names its admin grant and mapped guest namespace. Replicove
synthesizes a Service and Secret using the request's `name`. The Secret contains
`host`, `port`, `database`, `username`, and `password`. Reference those keys from
application environment variables. The target credentials are newly generated and
work only against the isolated copy. The generated PostgreSQL role owns that lab
database; these are disposable development credentials, not production users.

Exclude the original database workload, its Service, PVCs, and credential Secrets
from source capture. A captured Secret or Service colliding with a synthesized
name is rejected; Replicove never silently replaces or adopts it. Other original
application connection strings must be patched explicitly. The adapter does not
automatically discover every embedded database URL. An unchanged original endpoint
will fail behind the application egress boundary; it is not transparently redirected.

```sh
replicove database validate \
  --grant-file examples/postgresql/grant.yaml \
  --replication-file examples/postgresql/replication.yaml

replicove create pg-lab --grant postgres-lab \
  --replication-file examples/postgresql/replication.yaml --ttl 1h
```

Validation checks policy structure locally. Storage, CNI, credentials, native
database constraints, and declared relationships are checked during preparation.

## What masking guarantees

Masking belongs to the administrator grant, so a requester cannot omit its rules.
The initial strategies are `Null`, `Constant`, and `Token`. Token maps a non-null
value to salted SHA-256 hexadecimal text. Columns assigned the same token domain
within one copy receive the same token for the same text value. Each copy uses a
new salt. This supports declared text equality relationships; it does not preserve
sort order, numeric arithmetic, original lengths, distributions, or relationships
across separate database copies. Column type, length, uniqueness, and check
constraints can reject a rule rather than silently alter semantics.

Declared relationships must either remain unmasked on both sides or use the same
Token domain on both sides. Replicove verifies that every non-null child has a
matching parent. Native foreign keys are rebuilt and validated inside the masking
transaction. Composite relationships should use actual database foreign keys;
the additional declaration covers single-column equality relationships.

Optional admin `subsets` entries retain rows whose named column, cast to text,
equals the declared `equals` string. They run as DELETE operations only in the
isolated staging copy, before masking. Multiple entries on a table are combined
as AND filters. NULL values do not match a string. Declare compatible filters for
each related table; native FK and relationship validation reject orphaned results.
Undeclared tables remain intact: this is explicit selected-table filtering, not an
inferred tenant boundary or automatic relational graph traversal. The source dump
still reads the whole database; filtering reduces the final dataset, not the source
capture cost or required staging space.

This is **policy-scoped masking, not a general anonymization or PII discovery
system**. Undeclared columns, free text, JSON, large objects, and schema definitions
can still contain sensitive content. Administrators must review the full dataset
and rules for the intended recipients.

## Preparation and failure behavior

1. Journal ownership and establish host network isolation.
2. Stream one consistent source dump directly into the staging database.
3. Apply masking in a transaction and validate native and declared relationships.
4. Stream a second logical dump into a fresh final database volume.
5. Delete the staging Pod and wait for its disappearance.
6. Enable final TCP auth and same-guest-namespace ingress, create its Service,
   then let application installation and normal readiness checks proceed.

The second logical copy is necessary: UPDATE alone leaves earlier values in dead
tuples and WAL. Final storage never receives those staging pages or staging WAL.
The staging memory volume is deleted with its Pod; this is resource removal, not
a certified secure-erasure claim about host hardware or administrator access.

Failures keep application installation and sessions blocked. Database error output
and restored rows are discarded rather than exposed in status or controller logs.
An interrupted transfer is recorded as failed; reconciliation does not silently
read a different source snapshot or replay a partially completed transfer. Delete
and recreate the request after correcting its cause. Cleanup uses durable intent
records, ownership markers, and UID checks; TTL deletion removes owned database
Pods, PVCs, credentials, Services, host policies, and protected state. Application
egress policies remain until the managed runtime and its translated workload Pods
have disappeared. Disabling the database module does not bypass this cleanup;
re-enable it to finish deleting owned resources and policies.

## Disposable verification

Run `examples/postgresql/test-disposable.sh` on a Docker-enabled machine. It uses
three fresh PostgreSQL containers with no network and no host mounts, then removes
them even on failure. It tests the real dump/restore and generated masking SQL,
source read-only credentials, unchanged source rows, foreign-key integrity,
TCP rejection before publication, and absence of the original test string from
the final database files. Controller unit tests cover restart/failure gating and
overlapping network policy rejection. The Docker fixture does not qualify a real
cluster's CNI, vCluster translation, CSI provisioner, or TLS trust configuration.

`hack/e2e-database.sh` additionally installs a disposable kind cluster with Calico
and a real vCluster. It checks application access to the sanitized database through
guest DNS, denies source Pod and Service IPs from that application while proving
the source endpoints remain available, and verifies complete policy/data cleanup.
See the [validation record](../validation.md) for results actually obtained for
the current revision.
