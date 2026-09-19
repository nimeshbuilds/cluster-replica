#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:-dev}"
if [[ ! "$version" =~ ^(dev|v?[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.]+)?)$ ]];then
  echo 'Expected dev or a semantic version.' >&2
  exit 2
fi
mkdir -p dist
for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64;do
  os=${platform%/*}
  arch=${platform#*/}
  stage=$(mktemp -d)
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="-s -w -X main.version=$version" -o "$stage/replicove" ./cmd/replicove
  cp LICENSE "$stage/LICENSE"
  cp docs/replicove-quickstart.md "$stage/QUICKSTART.md"
  COPYFILE_DISABLE=1 tar -czf "dist/replicove-${version}-${os}-${arch}.tar.gz" -C "$stage" replicove LICENSE QUICKSTART.md
  rm -rf "$stage"
done
# The chart remains inspectable and installable independently of the CLI.
chart_stage=$(mktemp -d)
cp -R charts/replicove "$chart_stage/replicove"
python3 - "$chart_stage/replicove/Chart.yaml" "$version" <<'PYCHART'
from pathlib import Path
import re,sys
path=Path(sys.argv[1]);version=sys.argv[2].removeprefix('v')
if version!='dev':
    text=re.sub(r'^version: .*$', 'version: '+version, path.read_text(), flags=re.M)
    path.write_text(re.sub(r'^appVersion: .*$', 'appVersion: '+version, text, flags=re.M))
PYCHART
COPYFILE_DISABLE=1 tar --exclude=assets.go -czf "dist/replicove-chart-${version}.tgz" -C "$chart_stage" replicove
rm -rf "$chart_stage"
python3 - <<'PY'
from pathlib import Path
import hashlib
files=sorted([*Path('dist').glob('*.tar.gz'),*Path('dist').glob('*.tgz')])
Path('dist/SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in files))
PY
