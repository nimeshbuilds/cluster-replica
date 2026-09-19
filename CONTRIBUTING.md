# Contributing to Replicove

Replicove welcomes documentation, reproducible testing problems, design feedback, and code. Read the [project status](docs/project-status.md) and [roadmap](ROADMAP.md) first: the portable alpha is being developed in [PR #8](https://github.com/nimeshbuilds/replicove/pull/8), while `main` contains the runtime prototype.

## Find a useful first contribution

- Try the [runtime quickstart](docs/runtime-quickstart.md) and clarify a step that was difficult to follow.
- Reproduce an [open issue](https://github.com/nimeshbuilds/replicove/issues) with sanitized manifests and exact versions.
- Share a real operator/configuration workflow in [Discussions](https://github.com/nimeshbuilds/replicove/discussions).

For a new adapter or API change, describe the workflow and proposed acceptance evidence in an issue before a large implementation. Check open pull requests to avoid duplicating ongoing work.

## Local development

Use Go 1.27.1 or newer. Fork the repository, create a focused branch, and build from source:

```sh
git clone https://github.com/YOUR-USERNAME/replicove.git
cd replicove
make build
make check
```

Run `make generate` after changing API types. Keep generated CRDs and DeepCopy code with their source changes. `make check` runs race-enabled tests, pinned-chart contract checks, local API-server integration, vet, and the build. The integration suite downloads a local API server and does not use your kubeconfig. See [testing](docs/testing.md) for the boundary between these checks and real-cluster evidence.

For documentation-only changes, check links, commands, and rendered Markdown. Full cluster tests are unnecessary unless the documented behavior or commands changed.

## Send a focused pull request

Explain the concrete problem, resulting behavior, and validation actually run. Keep chart-specific values in the catalog/provider and source discovery independent of runtime provisioning. Ownership, deletion, or access changes need meaningful failure and recovery tests. Do not turn chart rendering alone into a support claim.

Small contributions and AI-assisted contributions are welcome; the author remains responsible for correctness and verification. Never submit credentials, kubeconfigs, private cluster captures, or rendered Secret data. Use the [private security process](SECURITY.md) for vulnerabilities.

Participation follows the [code of conduct](CODE_OF_CONDUCT.md). Contributions are licensed under the repository’s [Apache-2.0 license](LICENSE); no separate CLA is required.
