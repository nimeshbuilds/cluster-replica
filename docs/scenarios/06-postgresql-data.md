# 06. Prepare a masked PostgreSQL integration database

Use this lab when application tests need relational data with explicit tenant selection and masked fields. It prepares a new PostgreSQL 17 database for a replicated application, proves that application access waits until raw staging is removed, and verifies isolation from the source.

## Run

With the [shared prerequisites](index.md#prepare-once), including OpenSSL:

```bash
./examples/scenarios/run.sh 06
```

The fixture creates its own kind host, Calico installation, TLS certificate and synthetic two-tenant database. All database passwords and rows are disposable fixture data. No external database credentials are requested or read.

## Follow the workflow

1. Install Replicove with the database module disabled. Seed a PostgreSQL source and an application. The source database has two customers and their orders in tenants `10` and `20`, with an email-based foreign key, a nullable phone column and required display names. Tenant `10` has a populated phone; tenant `20` starts with a null phone. A dedicated `replica_reader` role receives read privileges; an attempted source update with that role must fail.
2. Create an explicit database grant. It authorizes this one source database, credentials in the protected namespace, TLS `require`, the pinned PostgreSQL image, storage/bounds and tenant `10` filters. Customer/order email fields share a `Token` mask domain, the nullable phone uses a quoted `'Null'` strategy, and the required display name uses `Constant` with the value `Integration Test Customer`. Quoting `'Null'` keeps it a strategy string in YAML. The replica selects the application, not the source database Deployment, and patches its connection to generated target credentials.
3. Submit the request before enabling the module and verify `DatabasesDisabled`. Upgrade the installation to enable the module with the same request intact. The operator's host RBAC must still reject source Pod deletion and source Secret reads; its separately granted protected credential is the database read path.
4. Take the logical dump, restore into isolated memory-backed staging, apply the approved subset/mask rules and validate relationships. A second logical copy creates the fresh persistent final database. The fixture continuously checks that raw-stage Pods and copied application Pods do not coexist; raw staging must be absent before guest access and the application are ready.
5. Query the copied database and application. Exactly one customer and one order remain, both in tenant `10`; their equality-linked email fields have the same 64-character hexadecimal token and the foreign key is validated. The formerly populated phone is SQL `NULL`, and the required display name is exactly `Integration Test Customer`. Application readiness checks these masks before becoming healthy, and a query through the generated database Service checks the joined data again. All five original email, phone and display-name markers must be absent from the final PostgreSQL data directory, including its WAL; a missing directory or failed read is an error, not a clean result.
6. Verify that the copied database and application cannot reach the original source through its Pod/Service endpoints while the source remains reachable from its own application. Restart the operator and preserve the prepared database Pod UID. Requesting in-place refresh must block rather than silently recopy data. Delete the replica and verify its recorded PVC/PV identities and host isolation policies are removed, with original source rows and deployment identity preserved.

## Expected result

The command exits zero and prints the database evidence directory. `report.json` records the preparation, access boundary, data assertions, source preservation, restart, refused refresh and owned cleanup. This fixture tests **explicit deletion** of the database-backed replica. General real TTL behavior is covered in [scenario 01](01-governed-replica.md); a deletion result alone does not claim a separate database TTL wait.

The test qualifies a consistent logical snapshot of this single database and its declared schema. It does not infer all sensitive fields, automatically extract tenant graphs, guarantee anonymization or coordinate transactions with queues/files/other databases. Its source TLS mode is explicitly `require`, not private-CA `verify-full` qualification.

## Inspect and adapt

Read the [fixture](https://github.com/nimeshbuilds/replicove/blob/main/hack/e2e-database.sh), [synthetic schema/source](https://github.com/nimeshbuilds/replicove/blob/main/test/database/source.yaml), [grant](https://github.com/nimeshbuilds/replicove/blob/main/test/database/grant.yaml), [application wiring](https://github.com/nimeshbuilds/replicove/blob/main/test/database/replication.yaml) and [installation values](https://github.com/nimeshbuilds/replicove/blob/main/test/database/values.yaml). The [PostgreSQL guide](../guides/postgresql.md) explains `Token`, `Null` and `Constant` strategies, approved subsets, native/declared relationships and storage sizing. This fixture exercises all three mask strategies with explicit tenant filters and linked tokens; it does not claim every mask/schema combination is represented by these rows. The [validation record](../validation.md) identifies completed runs; the expanded mask checks still require their matching released-artifact scenario run.

If preparation blocks, inspect the grant, source account, relation validation, staging bounds and host policy conflicts. Existing targets, CSI mirror combinations and in-place refresh are rejected by this adapter. Recreate the request to capture newer data. Keep the operator and storage controllers running through cleanup; the lab's final host deletion follows its owned-resource assertions.
