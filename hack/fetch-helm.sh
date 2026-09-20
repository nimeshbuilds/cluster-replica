#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
destination="$PWD/.cache/e2e-tools"
mkdir -p "$destination"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'Supported Helm hosts: macOS/Linux on amd64/arm64' >&2; exit 1 ;;
esac
case "$os/$arch" in
  darwin/amd64) checksum=347a784877e0e20eac865e8d1c36a80f6bb0861d6f29abd34defb6570ef95d92 ;;
  darwin/arm64) checksum=d3870437e1e95b67f8edbde964156c84a26503f560821d40c542441658934fba ;;
  linux/amd64) checksum=86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb ;;
  linux/arm64) checksum=31c5794dd55c66a51e6b7d2e2ac7a114ae8b1de41ff1d9ba51748ac973b06a08 ;;
  *) exit 1 ;;
esac
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
curl --fail --silent --show-error --location --retry 3 --retry-all-errors --connect-timeout 15 --max-time 180 \
  "https://get.helm.sh/helm-v4.3.0-$os-$arch.tar.gz" -o "$stage/helm.tgz"
[[ "$(shasum -a 256 "$stage/helm.tgz" | cut -d ' ' -f 1)" == "$checksum" ]] || {
  echo 'Helm archive checksum mismatch' >&2; exit 1;
}
tar -xzf "$stage/helm.tgz" -C "$stage" "$os-$arch/helm"
install -m 755 "$stage/$os-$arch/helm" "$destination/helm"
"$destination/helm" version --short
