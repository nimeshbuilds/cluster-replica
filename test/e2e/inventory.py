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
        "conditions": [
            {k: c.get(k) for k in ("type", "status", "reason")}
            for c in status.get("conditions", [])
        ],
        "containers": [
            {"name": c["name"], "ready": c.get("ready"),
             "imageID": c.get("imageID"),
             "restartCount": c.get("restartCount"),
             "waitingReason": c.get("state", {}).get("waiting", {}).get("reason"),
             "terminatedReason": c.get("state", {}).get("terminated", {}).get("reason"),
             "exitCode": c.get("state", {}).get("terminated", {}).get("exitCode")}
             | {"lastTermination": {k: c.get("lastState", {}).get("terminated", {}).get(k)
                                    for k in ("reason", "exitCode", "signal")}}
            for c in status.get("containerStatuses", []) + status.get("initContainerStatuses", [])
        ],
    })
json.dump(sorted(records, key=lambda x: (x["kind"], x["name"])), sys.stdout, indent=2)
print()
