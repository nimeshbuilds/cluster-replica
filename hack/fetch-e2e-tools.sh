#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
destination="$PWD/.cache/e2e-tools"
mkdir -p "$destination"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'Supported test hosts: macOS/Linux on amd64/arm64' >&2; exit 1 ;;
esac

verify() {
  local expected="$1" file="$2"
  [[ "$(shasum -a 256 "$file" | cut -d ' ' -f 1)" == "$expected" ]] || {
    echo "Checksum mismatch for $(basename "$file")" >&2; exit 1;
  }
  chmod +x "$file"
}

kind_asset="kind-${os}-${arch}"
kind_base=https://github.com/kubernetes-sigs/kind/releases/download/v0.33.0
curl --fail --silent --show-error --location --retry 3 "$kind_base/$kind_asset" -o "$destination/kind"
curl --fail --silent --show-error --location --retry 3 "$kind_base/$kind_asset.sha256sum" -o "$destination/kind.sha256"
verify "$(cut -d ' ' -f 1 "$destination/kind.sha256")" "$destination/kind"

kubectl_base="https://dl.k8s.io/release/v1.36.4/bin/$os/$arch"
curl --fail --silent --show-error --location --retry 3 "$kubectl_base/kubectl" -o "$destination/kubectl"
curl --fail --silent --show-error --location --retry 3 "$kubectl_base/kubectl.sha256" -o "$destination/kubectl.sha256"
verify "$(cat "$destination/kubectl.sha256")" "$destination/kubectl"

vcluster_asset="vcluster-${os}-${arch}"
vcluster_base=https://github.com/loft-sh/vcluster/releases/download/v0.37.1
curl --fail --silent --show-error --location --retry 3 "$vcluster_base/$vcluster_asset" -o "$destination/vcluster"
curl --fail --silent --show-error --location --retry 3 "$vcluster_base/checksums.txt" -o "$destination/vcluster.sha256"
verify "$(awk -v asset="$vcluster_asset" '$2 == asset {print $1}' "$destination/vcluster.sha256")" "$destination/vcluster"
