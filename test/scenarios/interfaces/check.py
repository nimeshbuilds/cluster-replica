#!/usr/bin/env python3
"""Exercise the public stdio/HTTP interfaces with a real, bounded host identity.

Only Python's standard library is required. Credentials, process output, and the
dashboard capability remain in the private run directory, outside artifacts.
"""
import argparse
import contextlib
import json
import os
import pathlib
import queue
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


def require(ok, message):
    if not ok:
        raise RuntimeError(message)


def poll(operation, predicate, message, seconds=60):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = operation()
        if predicate(value):
            return value
        time.sleep(1)
    raise RuntimeError(message)


class Process:
    def __init__(self, command, log):
        self.log = open(log, "w", encoding="utf-8")
        self.proc = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=self.log, text=True, bufsize=1)
        self.lines = queue.Queue()
        self.thread = threading.Thread(target=self.read, daemon=True)
        self.thread.start()

    def read(self):
        for line in self.proc.stdout:
            self.lines.put(line)
        self.lines.put(None)

    def line(self, timeout=60):
        try:
            line = self.lines.get(timeout=timeout)
        except queue.Empty:
            raise RuntimeError("interface subprocess did not respond before its deadline") from None
        require(line is not None, "interface subprocess exited unexpectedly; inspect private stderr")
        return line

    def close(self):
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=5)
        self.proc.stdin.close()
        self.proc.stdout.close()
        self.log.close()


class MCP(Process):
    def __init__(self, command, log):
        super().__init__(command, log)
        self.number = 0
        response = self.request("initialize", {
            "protocolVersion": "2025-06-18", "capabilities": {},
            "clientInfo": {"name": "replicove-live-scenario", "version": "1"},
        })
        require(response.get("serverInfo", {}).get("name") == "replicove", "wrong MCP server")
        self.proc.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
        self.proc.stdin.flush()

    def request(self, method, params):
        self.number += 1
        self.proc.stdin.write(json.dumps({"jsonrpc": "2.0", "id": self.number,
                                         "method": method, "params": params}) + "\n")
        self.proc.stdin.flush()
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            response = json.loads(self.line(max(1, deadline - time.monotonic())))
            if response.get("id") != self.number:
                continue
            require("error" not in response, "unexpected JSON-RPC protocol error")
            return response["result"]
        raise RuntimeError("MCP response deadline exceeded")

    def call(self, name, arguments=None):
        return self.request("tools/call", {"name": name, "arguments": arguments or {}})


def structured(result):
    require(not result.get("isError", False), "MCP tool returned an unexpected error")
    if "structuredContent" in result:
        return result["structuredContent"]
    for block in result.get("content", []):
        if block.get("type") == "text":
            return json.loads(block["text"])
    raise RuntimeError("MCP tool did not return structured JSON")


def metadata_only(value):
    encoded = json.dumps(value)
    require("interfaces-private-payload" not in encoded, "source payload leaked through a metadata interface")
    prohibited = {"data", "stringData", "spec", "token", "kubeconfig", "certificate-authority-data",
                  "client-key-data", "client-certificate-data", "credentialSecret"}
    def visit(obj):
        if isinstance(obj, dict):
            require(not (prohibited & obj.keys()), "non-metadata field leaked through an interface")
            for child in obj.values():
                visit(child)
        elif isinstance(obj, list):
            for child in obj:
                visit(child)
    visit(value)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--work", type=pathlib.Path, required=True)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--context", required=True)
    args = parser.parse_args()
    work = args.work.resolve()
    binary = str(pathlib.Path(args.binary).resolve())
    host = ["kubectl", "--kubeconfig", str(work / "host.kubeconfig"), "--context", args.context]
    def kubectl(*command, input=None):
        return subprocess.check_output(host + list(command), input=input, text=True, timeout=180)
    def apply(obj):
        kubectl("apply", "-f", "-", input=json.dumps(obj))
    def get_replica(name):
        return json.loads(kubectl("-n", "test-a", "get", "clusterreplica", name, "-o", "json"))
    ready = get_replica("interfaces-ready")
    uid = ready["metadata"]["uid"]
    require(ready["status"]["phase"] == "Ready", "interface fixture requires a ready real replica")
    expected = {(x["kind"], x["metadata"]["name"]): x["metadata"]["uid"] for x in json.loads(
        kubectl("-n", "source-dev", "get", "deployment,service,configmap", "-o", "json"))["items"]}
    def check_plan(plan):
        require(plan["uid"] == uid and plan["phase"] == "Ready", "interface returned the wrong replica")
        require(plan.get("capturedAt") and plan.get("revision") and plan.get("runtime", {}).get("chartSHA256"),
                "interface omitted capture/runtime provenance")
        objects = [x for x in plan.get("resources", []) if x.get("sourceName") in ("echo", "interface-settings")]
        require(any(x["kind"] == "Deployment" for x in objects), "replicated deployment missing from plan")
        for obj in objects:
            require(obj.get("sourceUID") == expected[(obj["kind"], obj["sourceName"])], "source UID provenance mismatch")
            require(obj.get("sourceVersion") and obj.get("selectionReason"), "source provenance incomplete")
            require("namespace-mapped" in obj.get("transformations", []), "namespace transformation not recorded")
        metadata_only(plan)

    plan = json.loads((work / "artifacts" / "plan.json").read_text())
    check_plan(plan)
    doctor = json.loads((work / "artifacts" / "doctor.json").read_text())
    metadata_only(doctor)
    require(doctor["uid"] == uid, "doctor inspected the wrong request")
    findings = {x["check"]: x["result"] for x in doctor["findings"]}
    require(findings["host-api"] == findings["replicove-api"] == findings["grant"] == "passed", "doctor preflight failed")
    require(findings["network-isolation"] == "unverified", "doctor incorrectly claims live CNI qualification")

    apply({"apiVersion": "v1", "kind": "ServiceAccount", "metadata": {"name": "scenario-agent", "namespace": "test-a"}})
    rules = [{"apiGroups": ["replica.nimeshbuilds.dev"],
              "resources": ["clusterreplicas", "replicamirrors", "replicaexperiments"], "verbs": ["get", "list"]}]
    role = {"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role",
            "metadata": {"name": "scenario-agent", "namespace": "test-a"}, "rules": rules}
    apply(role)
    apply({"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding",
           "metadata": {"name": "scenario-agent", "namespace": "test-a"},
           "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "scenario-agent"},
           "subjects": [{"kind": "ServiceAccount", "name": "scenario-agent", "namespace": "test-a"}]})
    token = kubectl("-n", "test-a", "create", "token", "scenario-agent", "--duration=1h").strip()
    config = json.loads(kubectl("config", "view", "--minify", "--raw", "-o", "json"))
    cluster = config["clusters"][0]
    agent_config = {"apiVersion": "v1", "kind": "Config", "clusters": [cluster],
                    "users": [{"name": "scenario-agent", "user": {"token": token}}],
                    "contexts": [{"name": "scenario-agent", "context": {"cluster": cluster["name"], "user": "scenario-agent", "namespace": "test-a"}}],
                    "current-context": "scenario-agent"}
    agent_path = work / "agent.kubeconfig"
    agent_path.write_text(json.dumps(agent_config))
    agent_path.chmod(0o600)
    command = [binary, "--kubeconfig", str(agent_path), "--namespace", "test-a"]
    agent_kubectl = ["kubectl", "--kubeconfig", str(agent_path)]
    passed = ["live-doctor-preflight", "live-plan-source-provenance", "metadata-only-diagnostics"]
    try:
        with contextlib.closing(MCP(command + ["mcp"], work / "mcp-reader.stderr")) as mcp:
            names = {x["name"] for x in mcp.request("tools/list", {})["tools"]}
            require(names == {"replica_list", "replica_plan", "replica_wait"}, "read-only MCP exposed mutation tools")
            result = poll(lambda: mcp.call("replica_plan", {"name": "interfaces-ready"}),
                          lambda x: not x.get("isError", False), "reader RBAC never became usable")
            check_plan(structured(result))
            inventory = structured(mcp.call("replica_list"))
            require(any(x["uid"] == uid for x in inventory["replicas"]), "MCP list missed live replica")
            check_plan(structured(mcp.call("replica_wait", {"name": "interfaces-ready"})))
            metadata_only(inventory)
        passed += ["stdio-mcp-initialize-list-plan-wait", "mcp-read-only-default"]
        with contextlib.closing(MCP(command + ["mcp", "--allow-write"], work / "mcp-writer.stderr")) as mcp:
            spec = {"profile": "vcluster-0.37.1-persistent", "ttl": "1h", "cleanupPolicy": "DeleteOwned",
                    "approval": "Automatic", "grantRef": "source-dev-a",
                    "replication": json.loads(pathlib.Path("test/scenarios/interfaces/replication.json").read_text())}
            for name, arguments in (
                ("replica_delete", {"name": "interfaces-ready", "uid": uid}),
                ("replica_create", {"name": "interfaces-denied", "spec": json.dumps(spec)}),
                ("replica_access_request", {"name": "interfaces-ready", "uid": uid, "role": "viewer", "durationSeconds": 900}),
            ):
                require(mcp.call(name, arguments).get("isError") is True, "--allow-write bypassed Kubernetes RBAC")
            require(get_replica("interfaces-ready")["metadata"]["uid"] == uid, "denied delete changed state")
            require(not kubectl("-n", "test-a", "get", "clusterreplica", "interfaces-denied", "--ignore-not-found", "-o", "name").strip(),
                    "denied create changed state")
            passed.append("mcp-write-flag-does-not-grant-rbac")
            role["rules"] = rules + [
                {"apiGroups": ["replica.nimeshbuilds.dev"], "resources": ["clusterreplicas"], "verbs": ["create", "delete"]},
                {"apiGroups": ["replica.nimeshbuilds.dev"], "resources": ["replicaaccesses"], "verbs": ["create", "get"]},
            ]
            apply(role)
            poll(lambda: subprocess.run(agent_kubectl + ["auth", "can-i", "create", "clusterreplicas.replica.nimeshbuilds.dev"],
                                        text=True, capture_output=True, timeout=30),
                 lambda p: p.returncode == 0 and p.stdout.strip() == "yes", "write RBAC never became usable")
            created = structured(mcp.call("replica_create", {"name": "interfaces-queued", "spec": json.dumps(spec)}))
            require(created["uid"] == get_replica("interfaces-queued")["metadata"]["uid"], "MCP creation UID mismatch")
            require(mcp.call("replica_delete", {"name": "interfaces-queued", "uid": uid}).get("isError") is True,
                    "MCP accepted a mismatched UID")
            deleted = structured(mcp.call("replica_delete", {"name": "interfaces-queued", "uid": created["uid"]}))
            require(deleted["uid"] == created["uid"], "MCP delete receipt referred to another UID")
            kubectl("-n", "test-a", "wait", "clusterreplica/interfaces-queued", "--for=delete", "--timeout=150s")
            replacement = structured(mcp.call("replica_create", {"name": "interfaces-queued", "spec": json.dumps(spec)}))
            require(replacement["uid"] != created["uid"], "recreated request reused the old UID")
            require(mcp.call("replica_delete", {"name": "interfaces-queued", "uid": created["uid"]}).get("isError") is True,
                    "MCP accepted stale UID after name reuse")
            access = structured(mcp.call("replica_access_request", {"name": "interfaces-ready", "uid": uid,
                                                                     "role": "viewer", "durationSeconds": 900}))
            require(access.get("uid") and access.get("name"), "MCP access response has no exact request identity")
            metadata_only(access)
            kubectl("-n", "test-a", "wait", "replicaaccess/" + access["name"], "--for=jsonpath={.status.phase}=Ready", "--timeout=150s")
            secret = json.loads(kubectl("-n", "test-a", "get", "replicaaccess", access["name"], "-o", "json"))["status"]["credentialSecret"]
            denied = subprocess.run(agent_kubectl + ["-n", "test-a", "get", "secret", secret], capture_output=True, text=True, timeout=30)
            require(denied.returncode != 0 and "Forbidden" in denied.stderr, "MCP identity gained credential Secret access")
            passed += ["mcp-authorized-create-and-access", "mcp-authorized-delete-and-name-reuse", "mcp-exact-uid-guard", "agent-cannot-read-credential-secret"]
            with contextlib.closing(Process(command + ["dashboard"], work / "dashboard.stderr")) as dashboard:
                prefix = "Read-only dashboard: "
                first = dashboard.line()
                require(first.startswith(prefix), "dashboard did not print its local URL")
                parsed = urllib.parse.urlsplit(first[len(prefix):].strip())
                require(parsed.hostname == "127.0.0.1" and parsed.fragment.startswith("token="), "invalid dashboard URL")
                capability = parsed.fragment.removeprefix("token=")
                base = f"http://{parsed.netloc}"
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                def request(path="/api/status", headers=None, method="GET"):
                    req = urllib.request.Request(base + path, headers=headers or {}, method=method)
                    try:
                        response = opener.open(req, timeout=30)
                    except urllib.error.HTTPError as e:
                        response = e
                    with response:
                        return response.status, response.headers, response.read()
                auth = {"X-Replicove-Token": capability}
                require(request()[0] == 401, "dashboard accepted missing capability")
                require(request(headers={"X-Replicove-Token": "wrong"})[0] == 401, "dashboard accepted invalid capability")
                require(request(headers=auth | {"Origin": "https://example.invalid"})[0] == 403, "dashboard accepted cross-origin read")
                require(request(headers=auth | {"Host": "example.invalid"})[0] == 403, "dashboard accepted an invalid Host")
                for method in ("POST", "PATCH", "DELETE"):
                    require(request(headers=auth, method=method)[0] == 405, "dashboard accepted a mutation method")
                status, headers, body = request(headers=auth)
                require(status == 200 and headers["Cache-Control"] == "no-store", "dashboard metadata read failed")
                require("frame-ancestors 'none'" in headers["Content-Security-Policy"], "dashboard CSP missing")
                snapshot = json.loads(body)
                require(snapshot["namespace"] == "test-a" and not snapshot.get("warnings"), "dashboard did not read bounded namespace")
                check_plan(next(x for x in snapshot["replicas"] if x["name"] == "interfaces-ready"))
                metadata_only(snapshot)
                for path, marker in (("/", b"Replicove"), ("/app.js", b"X-Replicove-Token"), ("/style.css", b"body")):
                    status, _, body = request(path)
                    require(status == 200 and marker in body, "dashboard static UI asset unavailable")
                passed += ["live-dashboard-metadata-and-assets", "dashboard-capability-host-origin-validation", "dashboard-read-only-http"]
                kubectl("-n", "test-a", "delete", "rolebinding", "scenario-agent", "--wait=true")
                poll(lambda: mcp.call("replica_plan", {"name": "interfaces-ready"}),
                     lambda x: x.get("isError") is True, "same MCP session retained revoked RBAC")
                snapshot = poll(lambda: json.loads(request(headers=auth)[2]),
                                lambda x: bool(x.get("warnings")) and x["replicas"] == [],
                                "same dashboard retained revoked RBAC")
                metadata_only(snapshot)
                passed += ["mcp-same-session-rbac-revocation", "dashboard-live-rbac-revocation"]
            kubectl("-n", "test-a", "delete", "replicaaccess", access["name"], "--wait=true", "--timeout=150s")
            require(not kubectl("-n", "test-a", "get", "secret", secret, "--ignore-not-found", "-o", "name").strip(),
                    "revoked access credential still exists")
            passed.append("agent-access-revocation")
    finally:
        agent_path.unlink(missing_ok=True)
    (work / "artifacts" / "interfaces.json").write_text(json.dumps({"suite": "live-interfaces", "passed": passed}, indent=2) + "\n")
    print(f"Verified {len(passed)} live diagnostics, MCP, and dashboard assertions")


if __name__ == "__main__":
    main()
