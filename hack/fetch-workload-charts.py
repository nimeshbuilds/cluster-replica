#!/usr/bin/env python3
"""Fetch exact upstream test charts and verify published SHA-256 digests."""
import hashlib
import urllib.request
from pathlib import Path

pins = {
    'cert-manager': ('cert-manager-v1.20.4.tgz', 'https://charts.jetstack.io/charts/cert-manager-v1.20.4.tgz', 'aa916e8fb726ee573f8c2fbed6899de7dbb77a017ac1deef63fee91cc5bfcd03'),
    'spark': ('spark-operator-2.5.2.tgz', 'https://github.com/kubeflow/spark-operator/releases/download/v2.5.2/spark-operator-2.5.2.tgz', '762be5b8632ecfe12eb20fff54450ddae0427f09506422107c508a0d1d38655b'),
    'trino': ('trino-1.42.2.tgz', 'https://github.com/trinodb/charts/releases/download/trino-1.42.2/trino-1.42.2.tgz', '0b33da82a36becd8913fef23d99f470bb6b87bce29bbc219089a5bbd4159266d'),
}
root = Path(__file__).resolve().parents[1] / '.cache/workload-charts'
root.mkdir(parents=True, exist_ok=True)
for name, (file, url, digest) in pins.items():
    path = root / file
    if not path.exists():
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read(16 * 1024 * 1024 + 1)
        if len(data) > 16 * 1024 * 1024:
            raise SystemExit('Chart exceeds test download limit')
        if hashlib.sha256(data).hexdigest() != digest:
            raise SystemExit('Chart digest mismatch: ' + name)
        path.write_bytes(data)
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise SystemExit('Cached chart digest mismatch: ' + name)
    print(name + ': verified ' + file)
