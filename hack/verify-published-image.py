#!/usr/bin/env python3
"""Verify anonymous GHCR access, immutable digest, platforms, and source labels."""
import hashlib
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request

repo = 'nimeshbuilds/replicove'
expected = os.environ['RELEASE_IMAGE_DIGEST']
version = os.environ['RELEASE_VERSION'].removeprefix('v')
revision = os.environ['GITHUB_SHA']
accept = 'application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json'


def read(url, headers=None):
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers or {}), timeout=30) as response:
        return response.read()


for attempt in range(60):
    try:
        # Deliberately no PAT, GITHUB_TOKEN, Docker login, or stored credentials.
        query = urllib.parse.urlencode({'service': 'ghcr.io', 'scope': 'repository:'+repo+':pull'})
        token = json.loads(read('https://ghcr.io/token?'+query))['token']
        headers = {'Authorization': 'Bearer '+token, 'Accept': accept}
        raw = read('https://ghcr.io/v2/'+repo+'/manifests/'+version, headers)
        break
    except (urllib.error.URLError, KeyError):
        if attempt == 59:
            raise SystemExit('Anonymous image pull failed. Set the new GHCR package visibility to Public and rerun the verification job.')
        print('Waiting for anonymous GHCR image access...', flush=True)
        time.sleep(10)

assert 'sha256:'+hashlib.sha256(raw).hexdigest() == expected, 'Published tag differs from the build digest'
index = json.loads(raw)
platforms = {}
for entry in index['manifests']:
    platform = entry.get('platform', {})
    if platform.get('os') == 'linux':
        platforms[platform['architecture']] = entry['digest']
assert set(platforms) == {'amd64', 'arm64'}, 'Expected exactly linux/amd64 and linux/arm64'
for arch, digest in platforms.items():
    manifest = json.loads(read('https://ghcr.io/v2/'+repo+'/manifests/'+digest, headers))
    config = json.loads(read('https://ghcr.io/v2/'+repo+'/blobs/'+manifest['config']['digest'], headers))
    assert config['architecture'] == arch
    labels = config['config']['Labels']
    assert labels['org.opencontainers.image.revision'] == revision
    assert labels['org.opencontainers.image.version'] == version
    assert labels['org.opencontainers.image.source'] == 'https://github.com/'+repo
print('Anonymous image verified:', expected, 'linux/amd64, linux/arm64; source revision', revision)
