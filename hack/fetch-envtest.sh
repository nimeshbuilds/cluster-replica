#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -x .cache/envtest/controller-tools/envtest/kube-apiserver && -x .cache/envtest/controller-tools/envtest/etcd ]]; then
  exit 0
fi
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'Supported test hosts: macOS/Linux on amd64/arm64' >&2; exit 1 ;;
esac
archive="envtest-v1.37.0-${os}-${arch}.tar.gz"
base=https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v1.37.0
mkdir -p .cache/envtest
curl --fail --location --max-time 60 "$base/$archive" --output ".cache/envtest/$archive"
curl --fail --location --max-time 60 "$base/$archive.sha512" --output ".cache/envtest/$archive.sha512"
cd .cache/envtest
expected=$(cut -d ' ' -f 1 "$archive.sha512")
actual=$(shasum -a 512 "$archive" | cut -d ' ' -f 1)
if [[ "$actual" != "$expected" ]]; then
  echo 'envtest checksum mismatch' >&2
  exit 1
fi
tar -xzf "$archive"
