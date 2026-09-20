#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$PWD/.cache/e2e-tools:$PATH"
version="${1:?Pass a release version, for example v0.1.0-alpha.1}"
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+([.-][a-zA-Z0-9]+)*)?$ ]]; then
  echo 'Expected a v-prefixed semantic version.' >&2; exit 2
fi
chart_version="${version#v}"
[[ "$(helm show chart charts/replicove | awk '$1 == "version:" {print $2}')" == "$chart_version" ]] || {
  echo 'Commit the matching chart/app/image version before releasing.' >&2; exit 1;
}
out="$PWD/dist/$version"
mkdir -p "$out"
[[ -z "$(ls -A "$out")" ]] || { echo 'Output directory must be empty; preserve or move previous artifacts first.' >&2; exit 1; }
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${platform%/*}; arch=${platform#*/}
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="-s -w -X main.version=$version" -o "$stage/replicove" ./cmd/replicove
  cp LICENSE "$stage/LICENSE"
  cp QUICKSTART.md "$stage/QUICKSTART.md"
  COPYFILE_DISABLE=1 tar -czf "$out/replicove-${version}-${os}-${arch}.tar.gz" -C "$stage" replicove LICENSE QUICKSTART.md
 done
cp -R charts/replicove "$stage/chart"
python3 - "$stage/chart/values.yaml" "$chart_version" "${RELEASE_IMAGE_DIGEST:-}" <<'PY'
from pathlib import Path
import re,sys
path=Path(sys.argv[1]); version,digest=sys.argv[2:]
assert not digest or re.fullmatch(r'sha256:[a-f0-9]{64}',digest), 'Invalid image digest'
text=re.sub(r'^  tag: .*$', '  tag: "'+version+'"', path.read_text(), flags=re.M)
path.write_text(re.sub(r'^  digest: .*$', '  digest: "'+digest+'"', text, flags=re.M))
PY
helm lint "$stage/chart" --strict
helm package "$stage/chart" --destination "$out"
python3 - "$out" "${RELEASE_IMAGE_DIGEST:-}" <<'PY'
from pathlib import Path
import hashlib,re,sys
out=Path(sys.argv[1]); digest=sys.argv[2]
out.joinpath('replicove-crds.yaml').write_text('\n---\n'.join(p.read_text() for p in sorted(Path('config/crd').glob('*.yaml'))))
text=Path('config/install/namespaces.yaml').read_text()+'\n---\n'+Path('config/install/operator.yaml').read_text()
if digest:
    text=re.sub(r'image: "ghcr.io/nimeshbuilds/replicove:[^"]+"', 'image: "ghcr.io/nimeshbuilds/replicove@'+digest+'"', text)
out.joinpath('replicove-install.yaml').write_text(text)
out.joinpath('SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in sorted(out.iterdir()) if p.is_file()))
PY
echo "Release artifacts: $out"
