# Download the CLI or build from source

Download a prebuilt CLI from [Replicove releases](https://github.com/nimeshbuilds/replicove/releases/tag/v0.3.0-alpha.2). Installing the CLI needs no Go compiler, Git clone, Docker engine or GitHub login. Keep the CLI, operator, chart and CRDs on the same release. These instructions use **v0.3.0-alpha.2**.

## Download a release

| Your machine | CLI archive |
| --- | --- |
| macOS, Apple silicon | [darwin-arm64](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-v0.3.0-alpha.2-darwin-arm64.tar.gz) |
| macOS, Intel | [darwin-amd64](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-v0.3.0-alpha.2-darwin-amd64.tar.gz) |
| Linux, x86-64 | [linux-amd64](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-v0.3.0-alpha.2-linux-amd64.tar.gz) |
| Linux, ARM64 | [linux-arm64](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-v0.3.0-alpha.2-linux-arm64.tar.gz) |

Each archive contains the executable `replicove`, `LICENSE` and `QUICKSTART.md`. Windows executables are not currently published; use a Linux environment for the CLI. The [sixteen scenario variants](../scenarios/index.md) have a narrower, explicitly tested Linux amd64/Docker environment.

The following Bash commands select your platform, verify the archive against the release's `SHA256SUMS`, and install into `~/.local/bin`. They need `curl`, `awk`, `shasum`, `tar` and `install`, but no administrator privileges.

```bash
(
  set -eu
  version=v0.3.0-alpha.2
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) echo 'Supported release platforms: macOS and Linux' >&2; exit 1 ;;
  esac
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo 'Supported release architectures: amd64 and arm64' >&2; exit 1 ;;
  esac
  asset="replicove-$version-$os-$arch.tar.gz"
  base="https://github.com/nimeshbuilds/replicove/releases/download/$version"
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  curl --fail --location "$base/$asset" --output "$work/$asset"
  curl --fail --location "$base/SHA256SUMS" --output "$work/SHA256SUMS"
  awk -v asset="$asset" '$2 == asset { print; seen++ } END { if (seen != 1) exit 1 }' \
    "$work/SHA256SUMS" > "$work/selected.sha256"
  (cd "$work" && shasum -a 256 --check selected.sha256)
  tar -xzf "$work/$asset" -C "$work" replicove
  mkdir -p "$HOME/.local/bin"
  install -m 755 "$work/replicove" "$HOME/.local/bin/replicove"
  "$HOME/.local/bin/replicove" --version
)
export PATH="$HOME/.local/bin:$PATH"
replicove --help
```

The version output must contain `v0.3.0-alpha.2`. Keep `~/.local/bin` on your shell's PATH for later terminals, or invoke the executable by its full path. To upgrade, repeat with the desired released version and follow the [installation upgrade guidance](installation.md#upgrades-and-removal) for the cluster components. Downloading a CLI does not upgrade a running operator.

## What else is in the release?

| Artifact | Purpose |
| --- | --- |
| [Helm chart archive](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-0.3.0-alpha.2.tgz) | Install the operator with Helm; also published to `oci://ghcr.io/nimeshbuilds/charts/replicove` |
| [CRD manifests](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-crds.yaml) | Install or update all six API schemas |
| [Native installation manifests](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/replicove-install.yaml) | Install the operator using kubectl, including in-cluster state-key initialization |
| [SHA256SUMS](https://github.com/nimeshbuilds/replicove/releases/download/v0.3.0-alpha.2/SHA256SUMS) | Verify the downloaded binaries, chart and manifests |
| Source code archives | GitHub-generated archives of the tagged source; these require a compiler to produce an executable |

The multi-architecture operator image is published at `ghcr.io/nimeshbuilds/replicove:0.3.0-alpha.2`. Release chart and native manifest assets pin its digest. The release notes record the exact source commit, image digest and published-artifact verification run.

## Build the matching CLI from source

Use this option if you want to inspect or modify the code. It requires Git and the Go version in `go.mod` (Go **1.27.1** for this release).

```bash
git clone https://github.com/nimeshbuilds/replicove.git replicove-source
cd replicove-source
git switch -c build-v0.3.0-alpha.2 v0.3.0-alpha.2
export REPLICOVE_RELEASE=v0.3.0-alpha.2
CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=$REPLICOVE_RELEASE" \
  -o bin/replicove ./cmd/replicove
./bin/replicove --version
```

This creates a normal local branch at the release tag. Building this CLI does not build an operator container or install anything in a cluster. To develop unreleased changes on `main`, build both components and run the checks in [Build and test](../development/local.md); do not label a modified build as the published release.

## Continue

Use the [Helm quickstart](helm.md) for one-command operator installation, the [YAML quickstart](yaml.md) for native manifests, or the [CLI quickstart](../../QUICKSTART.md) for a disposable local demonstration. The local demo clones the repository for its sample applications and helper scripts; that checkout is separate from obtaining a prebuilt CLI.
