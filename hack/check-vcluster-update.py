#!/usr/bin/env python3
"""Read-only upstream check. Never changes the catalog or deploys a new version."""
import json
import re
import urllib.request
from pathlib import Path

source = Path(__file__).resolve().parents[1] / "internal/catalog/catalog.go"
current = re.search(r'ChartVersion\s*=\s*"([^"]+)"', source.read_text()).group(1)
request = urllib.request.Request(
    "https://api.github.com/repos/loft-sh/vcluster/releases/latest",
    headers={"Accept": "application/vnd.github+json", "User-Agent": "replicove-maintenance"},
)
with urllib.request.urlopen(request, timeout=30) as response:
    release = json.load(response)
latest = release["tag_name"].removeprefix("v")
print(json.dumps({"pinned": current, "upstream": latest, "updateAvailable": current != latest,
                  "release": release["html_url"], "automaticallyApplied": False}, indent=2))
