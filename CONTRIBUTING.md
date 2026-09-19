# Contributing

Start with the [roadmap](ROADMAP.md) and [open issues](https://github.com/nimeshbuilds/cluster-replica/issues). For a new adapter or API change, describe the real workflow and proposed acceptance evidence before implementing a large change.

Use Go 1.27.1 or newer. Run `make generate` after changing API types and `make check` before submitting code. Keep the generated CRD and DeepCopy code in the same commit as their source. Integration tests download a local API server; they do not use your kubeconfig.

Keep chart-specific values in the catalog and provider, and keep source discovery independent of runtime provisioning. Include meaningful failure/recovery tests for ownership, deletion or access changes. Never add a support claim based only on chart rendering.

Pull requests should explain the user-visible change and the tests run. Small, reviewable contributions are welcome. AI-assisted contributions are welcome too; the author remains responsible for correctness and verification.

Do not submit credentials, kubeconfigs, private cluster captures, or rendered Secret manifests. See [SECURITY.md](SECURITY.md) for vulnerability reports. Participation follows the [Nimesh Builds community standards](https://github.com/nimeshbuilds/.github/blob/main/CODE_OF_CONDUCT.md).

Contributions are licensed under this repository's Apache-2.0 license.
