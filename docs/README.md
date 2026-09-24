# Replicove documentation

**[Read the complete developer docs →](https://nimeshbuilds.github.io/replicove/)**

The searchable site includes Helm, YAML, and CLI quickstarts, detailed feature guides, generated Kubernetes API and CLI references, compatibility boundaries, troubleshooting, and contributor documentation.

| Start here | Guide |
| --- | --- |
| Install and create using native manifests | [YAML quickstart](getting-started/yaml.md) |
| Create with the CLI and managed tunnel | [CLI quickstart](../QUICKSTART.md) |
| Choose an installation method | [Installation](getting-started/installation.md) |
| Create writable data copies with scheduled resets | [Workload mirrors](guides/mirrors.md) |
| Add mirrors after installing Replicove | [Enable mirroring later](guides/enable-mirroring.md) |
| Inspect preflight and source/dependency evidence | [Diagnostics](guides/diagnostics.md) |
| Run reusable tests and save metadata/JUnit results | [Test recipes](guides/test-runs.md) |
| Copy and sanitize explicitly granted PostgreSQL data | [PostgreSQL](guides/postgresql.md) |
| Exercise bounded faults and rollback | [Chaos](guides/chaos.md) |
| Add concurrent managed destinations | [Pools and capacity](guides/pools.md) |
| Connect an agent or inspect local status | [Stdio MCP](guides/agents-mcp.md) and [dashboard](guides/dashboard.md) |
| Explore every current capability and limit | [Feature map](features.md) |
| Understand tested behavior and remaining work | [Project status](project-status.md) |
| Build and extend Replicove | [Local development](development/local.md) and [architecture](architecture.md) |
| Update or preview the website | [Docs maintenance](development/docs.md) |

The current v0.3.0-alpha.1 candidate adds features beyond the published v0.2.0-alpha.2 baseline; [validation](validation.md) identifies pending qualification. Historical [design proposals](design/cluster-replica-implementation-plan.md) include unimplemented ideas. Current guides and the status page distinguish those from implemented behavior.
