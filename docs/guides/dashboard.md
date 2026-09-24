# Read-only dashboard

```sh
replicove dashboard --context integration-host --namespace replica-lab
```

Open the complete URL printed by the command. The dashboard binds to a randomly selected loopback port by default, uses the selected Kubernetes identity, and refreshes every five seconds. Keep the process running while using it. `--listen 127.0.0.1:8181` selects a fixed local port; non-loopback addresses are rejected.

The overview shows replica counts, readiness, requests needing attention and active chaos experiments. Each replica includes a lifecycle display, metadata-only plan and dependency evidence, and a copyable CLI inspection command. Mirror and experiment panels show phases and expiry. It does not display captured resource bodies, request specs, Secret contents, database rows or credentials, and it has no write endpoints.

The URL contains an ephemeral capability in its fragment. JavaScript removes it from the visible URL after opening and sends it only in a same-origin header. Treat the complete URL as access to that dashboard session. Reloading after the fragment is removed requires reopening the original URL. Closing the CLI invalidates the session. Kubernetes permissions still govern each read; unavailable collections are shown explicitly instead of appearing as empty successful lists.

The server checks Host and Origin headers, rejects cross-origin requests, disables caching and framing, and uses a restrictive Content Security Policy with no external scripts. It is a local inspection tool, not a shared web service. Do not publish it through an ingress or a public reverse proxy. A shared organizational dashboard requires a separately authenticated per-user deployment.

For mutations, use the [CLI or YAML APIs](../features.md) and inspect the resulting plan. A lifecycle bar shows reported controller progress; it is not proof that a test passed or physical storage was erased.
