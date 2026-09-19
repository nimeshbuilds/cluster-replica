#!/usr/bin/env python3
"""Emit only an explicit metadata/status allowlist, never manifests or Secrets."""
import json
import sys

records = []
for obj in json.load(sys.stdin).get("items", []):
    meta = obj.get("metadata", {})
    status = obj.get("status", {})
    records.append({
        "apiVersion": obj["apiVersion"], "kind": obj["kind"],
        "name": meta["name"], "namespace": meta.get("namespace"),
        "uid": meta["uid"], "deletionTimestamp": meta.get("deletionTimestamp"),
        "ownerReferences": [
            {k: ref.get(k) for k in ("apiVersion", "kind", "name", "uid")}
            for ref in meta.get("ownerReferences", [])
        ],
        "release": meta.get("annotations", {}).get("meta.helm.sh/release-name"),
        "replicaUID": meta.get("labels", {}).get("replica.nimeshbuilds.dev/uid"),
        "phase": status.get("phase"),
        "containers": [
            {"name": c["name"], "ready": c.get("ready"),
             "imageID": c.get("imageID"),
             "waitingReason": c.get("state", {}).get("waiting", {}).get("reason")}
            for c in status.get("containerStatuses", []) + status.get("initContainerStatuses", [])
        ],
    })
json.dump(sorted(records, key=lambda x: (x["kind"], x["name"])), sys.stdout, indent=2)
print()
