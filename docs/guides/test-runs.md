# One-command integration test runs

`replicove run` creates a fresh environment, waits for readiness, issues scoped guest credentials, runs your local test command, and waits for verified cleanup. It works from your terminal or any automation system that can run the CLI. There is no GitHub Action dependency.

```sh
replicove run -n replica-lab -f examples/testing/smoke.yaml

# Override the recipe's command. Arguments after -- go directly to the executable.
replicove run -n replica-lab -f examples/testing/smoke.yaml \
  --artifacts ./smoke-results -- go test ./integration/... -count=1
```

The command runs **locally**, in the current working directory, with your normal environment except for a private, temporary `KUBECONFIG`. Replicove does not edit your current context or default kubeconfig. The command must use that kubeconfig when accessing Kubernetes; explicitly overriding it in your test code overrides the runner's destination too. No shell is inserted automatically. Use an explicit shell executable only when your command needs shell syntax.

## Prerequisites

Install Replicove and provision an administrator-approved destination namespace and [ReplicaGrant](grants.md). The selected grant must permit the source configuration, guest access role, credential duration, and requested lifetime. The caller needs the existing request and access permissions, exact credential-Secret reads, and runtime pod port-forward permission described in [access](access.md). A test runner does not grant itself these permissions.

The runner creates a managed runtime. It does not adopt an existing `ClusterReplica`, `ReplicaMirror`, or an administrator's existing target. vCluster's one-runtime-per-host-namespace limitation still applies: use an available granted destination, or separate destinations for concurrent runs. An occupied destination can prevent readiness and exhaust the setup deadline.

## Local recipe format

Recipes are versioned **local CLI documents**, not CRDs; do not apply them with `kubectl`. Unknown fields, duplicate fields and multiple documents are rejected. A recipe embeds exactly one `replica` (`ClusterReplicaSpec`) or `mirror` (`ReplicaMirrorSpec`). Metadata, status, finalizers and existing names cannot be imported.

```yaml
apiVersion: replicove.nimeshbuilds.dev/v1alpha1
kind: TestRecipe
namePrefix: integration
replica:
  profile: vcluster-0.37.1-persistent
  ttl: 2h
  cleanupPolicy: DeleteOwned
  approval: Automatic
  grantRef: source-dev
  target:
    provider: helm
  replication:
    namespaces: [source-dev]
    namespaceMap:
      source-dev: integration
    secrets: None
    data: None
execution:
  command: [go, test, ./integration/..., -count=1]
  timeout: 10m
  role: viewer
  credentialSeconds: 900
lifecycle:
  readyTimeout: 20m
  cleanupTimeout: 15m
  keepOnFailure: false
```

| Field | Behavior |
| --- | --- |
| `namePrefix` | Optional DNS label, at most 40 characters; defaults to `test`. A random run identity is appended. |
| `destinations` | Optional explicit namespace/grant pairs from an administrator-managed [pool](pools.md). The CLI chooses a readable member with the fewest requests. Cannot be combined with an explicit `--namespace`. |
| `replica` / `mirror` | Exactly one spec. Requires `DeleteOwned`, a supported pinned profile, a grant, and Automatic approval. Existing targets and `secrets: Follow` are rejected. |
| `execution.command` | Argument array. May be omitted when a command follows CLI `--`. No implicit shell expansion. |
| `execution.timeout` | Defaults to `10m`; positive and at most `27m`. Covers command execution, separately from readiness and cleanup. |
| `execution.role` | `viewer` by default; `deployer` or `admin` must be explicitly selected and permitted by the grant. |
| `execution.credentialSeconds` | Defaults to `900`; allowed range `600..3600`. Must cover the test timeout plus 30 seconds, and be within the grant. Issued credentials are checked again before execution. |
| `lifecycle.readyTimeout` | Defaults to `20m`; at most `2h`. Includes provisioning, mirror lease acceptance, access issuance and optional chaos activation. |
| `lifecycle.cleanupTimeout` | Defaults to `15m`; at most `1h`. Shared deadline for fault rollback, credential revocation, lease release and finalizer completion. |
| `lifecycle.keepOnFailure` | Defaults to `false`. After an executed command fails, keep only this request until its **original TTL**. Provisioning failures and cancellation still trigger deletion. |
| `chaos` | Optional bounded guest experiment, described below. Requires the chaos module and administrator delegation. |

Choose a request TTL that **exceeds** readiness + execution + cleanup timeouts and also leaves at least ten minutes for credential issuance after the readiness allowance. TTL starts when Kubernetes creates the request, including provisioning. The runner never extends TTL to keep failed environments alive.

## Storage-backed test environments

Use the [mirror recipe example](https://github.com/nimeshbuilds/replicove/blob/main/examples/testing/mirror.yaml) to create an owned mirror for one test run. The [mirror module](mirrors.md), qualified CSI snapshot/restore behavior, enforced NetworkPolicies and volume-data grants must already be configured.

The runner waits for an active generation, acquires a bounded mirror test lease, and waits for the operator to acknowledge it. It records the active replica UID, capture-run UID and capture timestamp before issuing access. Test recipes do not accept an initial hold, suspension or automatic reset interval. The runner watches the lease, active generation and plan revision while the command runs; a detected change stops the command. The lease is released during teardown.

Mirroring continues to mean per-volume crash consistency. A test recipe does not make CSI captures transaction-consistent across databases or multiple volumes. Administrator-authorized forced resets can bypass a mirror lease; the runner detects changed generation identity rather than silently switching the test to a new generation.

## Run tests with chaos

Add a `chaos` section to either kind of recipe. Faults in one experiment are simultaneous. The duration must be `1..900` seconds, no longer than the execution timeout, and permitted by the grant. There may be at most eight faults. The runner waits for `Active` before launching the command and waits for rollback during teardown.

```yaml
chaos:
  durationSeconds: 60
  faults:
    - kind: NetworkIsolation
      namespace: integration
    - kind: ScaleZero
      namespace: integration
      target:
        kind: Deployment
        name: orders
```

For named Pod/Deployment/StatefulSet targets, a **local recipe** may omit `target.uid`. The CLI resolves the exact named object using the scoped guest session, then submits an experiment with its current guest UID. The `ReplicaExperiment` CRD itself always requires a UID. A provided UID is used exactly; source/host UIDs must not be substituted for guest UIDs. Ownership and allowed-fault checks remain in the controller.

The same typed fault fields support `PodDelete`, `ScaleZero`, `NetworkIsolation`, `CPUStress`, `MemoryStress` and `CustomJob`. Custom Jobs are constrained by the administrator's image and resource policy; the runner does not accept arbitrary host manifests or privileged pod specifications. See the [chaos guide](chaos.md) for grant configuration, supported fault behavior and rollback limitations. The [chaos recipe example](https://github.com/nimeshbuilds/replicove/blob/main/examples/testing/chaos.yaml) demonstrates one network fault; replace its API smoke check with your application's actual resilience assertions.

## Results, provenance and reproducibility

Runs that reach provisioning write `report.json` and `junit.xml` into a new private directory. Use `--artifacts` to choose the path; an existing directory or file is never overwritten. Files are mode `0600`, and the created directory is mode `0700`. JUnit contains three stages: provisioning, command, and cleanup. It does not parse or replace your test framework's individual test-case output.

The report records the normalized recipe hash, exact request and guest UIDs, plan revision and capture time, safe resource identity/provenance, runtime profile/chart checksum/Kubernetes version, optional mirror capture and chaos target references, test exit code, and independent setup/test/cleanup outcomes. It excludes the recipe body, command arguments, captured configuration, arbitrary API error messages, credentials, kubeconfig paths and test output. Command stdout/stderr stream directly to your terminal; your test program controls what it prints.

Reusing a recipe repeats the **requested setup** against a fresh host capture. It does not guarantee historical image contents, external-service state, database contents or identical live source resource versions. A recorded plan revision identifies that specific capture and includes its capture time, so even unchanged source configuration receives a new revision on a fresh capture. It cannot compare configuration equality across runs or import a snapshot. Use immutable image digests, retain the original recipe in your own source control, and use [retained mirror reset revisions](mirrors.md) when you need captured data again. There is no automatic download or replay of the encrypted captured configuration.

## Cleanup and failed tests

Normal teardown closes the tunnel and erases the private credential file, deletes any owned chaos experiment and waits for its rollback finalizer, deletes the scoped access request and waits for credential revocation, releases the mirror lease, then deletes the owned replica or mirror and waits for its finalizer-backed removal. Delete calls carry exact UID preconditions. If a name has been reused, the replacement is preserved and cleanup is reported as unverified.

Cancellation and execution timeouts still run cleanup under a separate bounded context. On macOS and Linux, cancellation terminates the command's process group, including ordinary child processes. Force-killing the CLI or losing the machine prevents local cleanup; the original server-side TTL remains the fallback. No runner can guarantee cleanup while the Kubernetes API or cleanup controller is permanently unavailable.

The process exits successfully only when setup and the command pass **and** cleanup is verified. A passing command with unfinished cleanup is still a failed run, with separate fields in the report. `keepOnFailure: true` retains a failed test request until its original TTL after rolling back chaos and revoking the test session. Use the printed request name and [normal access commands](access.md) to request fresh, authorized debugging access. `Retained` is explicit, not reported as completed cleanup.

If cleanup cannot be verified, inspect the retained request and its conditions using `replicove status`, `replicove mirror status` or `kubectl`. Do not remove finalizers to make the runner appear successful. The [cleanup guide](cleanup.md) explains ownership checks and recovery.
