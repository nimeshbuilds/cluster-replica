#!/usr/bin/env python3
"""Execute the published documentation labs; never accept a host kubeconfig."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
CATALOG = Path(__file__).with_name("catalog.json")


def catalog():
    return json.loads(CATALOG.read_text())


def select(data, scenario_id, variant):
    scenario = next((s for s in data["scenarios"] if s["id"] == scenario_id), None)
    if scenario is None:
        raise ValueError("Choose a scenario from 01 through 10; use --list.")
    variant = variant or next(iter(scenario["variants"]))
    if variant not in scenario["variants"]:
        raise ValueError(f"Scenario {scenario_id} variants: {', '.join(scenario['variants'])}")
    return scenario, variant


def command_for(scenario, variant):
    command = ["bash", scenario["script"]]
    if scenario.get("argumentVariant"):
        command.append(variant)
    return command


def preflight():
    # The tutorials promise the platform actually exercised by the live matrix.
    # Native CLI archives support more platforms; these Docker labs do not yet.
    if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
        raise RuntimeError("These labs are qualified on Linux amd64. Use a Linux amd64 VM with Docker; the regular installation guides support other CLI platforms.")
    required = ("bash", "curl", "docker", "go", "make", "openssl", "tar", "sha256sum", "shasum")
    missing = [name for name in required if shutil.which(name) is None]
    if missing:
        raise RuntimeError("Install the prerequisites first: " + ", ".join(missing))
    info = subprocess.run(["docker", "info", "--format", "{{json .}}"], capture_output=True, text=True, timeout=30)
    if info.returncode:
        raise RuntimeError("Docker is not reachable. Start a Linux Docker engine before running a lab.")
    daemon = json.loads(info.stdout)
    if daemon.get("OSType") != "linux" or daemon.get("Architecture") not in ("x86_64", "amd64"):
        raise RuntimeError("Use a Linux amd64 Docker engine for these qualified labs.")
    print("Docker lab resources: " + str(daemon.get("NCPU", "?")) + " CPUs, " + str(round(daemon.get("MemTotal", 0) / 2**30, 1)) + " GiB RAM", flush=True)
    if daemon.get("NCPU", 0) < 4 or daemon.get("MemTotal", 0) < 7 * 2**30:
        raise RuntimeError("Allocate at least 4 CPUs and 8 GiB of Docker memory (7 GiB usable). These are disposable integration labs, not minimum product requirements.")


def download(url, path, expected):
    if path.exists() and hashlib.sha256(path.read_bytes()).hexdigest() == expected:
        return
    request = urllib.request.Request(url, headers={"User-Agent": "replicove-scenarios"})
    for attempt in range(3):
        temporary = path.with_suffix(path.suffix + ".download")
        try:
            with urllib.request.urlopen(request, timeout=180) as response, temporary.open("wb") as target:
                shutil.copyfileobj(response, target)
            if hashlib.sha256(temporary.read_bytes()).hexdigest() != expected:
                raise RuntimeError("Release checksum mismatch: " + path.name)
            temporary.replace(path)
            return
        except Exception:
            temporary.unlink(missing_ok=True)
            if attempt == 2:
                raise
            time.sleep(2)


def prepare_release(release, path):
    path.mkdir(parents=True, exist_ok=True)
    base = "https://github.com/nimeshbuilds/replicove/releases/download/" + release["version"]
    for name, digest in release["assets"].items():
        download(base + "/" + name, path / name, digest)
    archive = path / ("replicove-" + release["version"] + "-linux-amd64.tar.gz")
    # Extract only the reviewed executable, never archive paths or symlinks.
    with tarfile.open(archive, "r:gz") as bundle:
        member = bundle.getmember("replicove")
        if not member.isfile():
            raise RuntimeError("CLI archive does not contain a regular executable.")
        with bundle.extractfile(member) as source, (path / "replicove").open("wb") as target:
            shutil.copyfileobj(source, target)
    (path / "replicove").chmod(0o700)
    actual = subprocess.check_output([str(path / "replicove"), "--version"], text=True).strip()
    if actual != "replicove version " + release["version"]:
        raise RuntimeError("CLI version does not match the pinned release.")


def clean_environment(work, release, release_path, variant_environment):
    # Helm connection overrides can supersede even an explicit kubeconfig.
    # A lab must never inherit a user's host, token, impersonation or context.
    env = {k: v for k, v in os.environ.items() if not k.startswith(("REPLICOVE_", "HELM_", "KUBERNETES_")) and k not in ("KUBECONFIG", "RELEASE_IMAGE_DIGEST")}
    env.update({
        "KUBECONFIG": str(work / "unused-host.kubeconfig"),
        "REPLICOVE_IMAGE": release["image"],
        "REPLICOVE_CHART": str(release_path / ("replicove-" + release["version"][1:] + ".tgz")),
        "REPLICOVE_CHART_VERSION": release["version"][1:],
        "REPLICOVE_USE_RELEASE_CLI": "true",
        "REPLICOVE_MANIFEST_DIR": str(release_path),
        "HELM_REGISTRY_CONFIG": str(work / "helm-registry.json"),
        "PATH": str(ROOT / ".cache/e2e-tools") + os.pathsep + env.get("PATH", ""),
    })
    env.update(variant_environment)
    (work / "helm-registry.json").write_text("{}\n")
    return env


def clusters(env):
    return set(subprocess.check_output([str(ROOT / ".cache/e2e-tools/kind"), "get", "clusters"], env=env, text=True).splitlines())


def evidence_passed(report):
    if "result" in report:
        return report["result"] == "passed"
    if "passed" in report:
        return isinstance(report["passed"], list) and bool(report["passed"]) and not report.get("failed")
    # Older YAML/existing-target fixtures consist of named boolean assertions.
    flags = [value for value in report.values() if isinstance(value, bool)]
    return report.get("ownedCleanup") is True and bool(flags) and all(flags)


def run(data, scenario, variant):
    preflight()
    os.umask(0o077)
    cache = ROOT / ".cache/scenarios"
    cache.mkdir(parents=True, exist_ok=True)
    lock = cache / "active.lock"
    try:
        lock.mkdir()
    except FileExistsError:
        raise RuntimeError("Another lab owns .cache/scenarios/active.lock. Run one lab per checkout. After an interrupted process, check the recorded PID before removing this directory.")
    (lock / "pid").write_text(str(os.getpid()) + "\n")
    work = Path(tempfile.mkdtemp(prefix="run.", dir=cache))
    binary = ROOT / "bin/replicove"
    backup = work / "previous-replicove"
    had_binary = binary.exists()
    report = {"scenario": scenario["id"], "variant": variant, "release": data["release"]["version"], "image": data["release"]["image"], "result": "failed", "reports": []}
    result = 1
    try:
        release_path = cache / data["release"]["version"]
        prepare_release(data["release"], release_path)
        for helper in ("hack/fetch-e2e-tools.sh", "hack/fetch-helm.sh"):
            subprocess.run(["bash", helper], cwd=ROOT, check=True)
        binary.parent.mkdir(exist_ok=True)
        if had_binary:
            shutil.copy2(binary, backup)
        shutil.copy2(release_path / "replicove", binary)
        env = clean_environment(work, data["release"], release_path, scenario["variants"][variant])
        before_clusters = clusters(env)
        evidence = ROOT / ".cache" / scenario["evidence"]
        before_reports = set(evidence.glob("run.*/artifacts/report.json"))
        print(f"Running scenario {scenario['id']} ({variant}): {scenario['title']}", flush=True)
        print("Only fresh Docker/kind clusters are used. The lab removes its cluster on exit.", flush=True)
        result = subprocess.run(command_for(scenario, variant), cwd=ROOT, env=env).returncode
        created = clusters(env) - before_clusters
        remaining = sorted(name for name in created if name.startswith("replicove-"))
        report["remainingLabClusters"] = remaining
        new_reports = sorted(set(evidence.glob("run.*/artifacts/report.json")) - before_reports)
        report["reports"] = [str(p.relative_to(ROOT)) for p in new_reports]
        if result == 0 and not new_reports:
            raise RuntimeError("The lab exited without its required assertion report.")
        for path in new_reports:
            evidence_report = json.loads(path.read_text())
            # Suites use either result, an explicit passed-list, or the original
            # existing-target report's boolean assertions.
            if not evidence_passed(evidence_report):
                raise RuntimeError("The lab assertion report did not indicate success: " + str(path))
        if remaining:
            raise RuntimeError("Lab cluster cleanup was incomplete: " + ", ".join(remaining))
        if result == 0:
            report["result"] = "passed"
        return result
    finally:
        if backup.exists():
            shutil.copy2(backup, binary)
            backup.unlink()
        elif not had_binary:
            binary.unlink(missing_ok=True)
        (work / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        shutil.rmtree(lock)
        print("Scenario receipt: " + str(work / "report.json"), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("scenario", nargs="?")
    parser.add_argument("variant", nargs="?")
    parser.add_argument("--list", action="store_true", help="List the ten labs and all variants without starting Docker")
    parser.add_argument("--matrix", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--describe", action="store_true", help="Print the selected executable and pinned release without running it")
    args = parser.parse_args()
    data = catalog()
    if args.matrix:
        print(json.dumps({"include": [{"scenario": s["id"], "variant": v} for s in data["scenarios"] for v in s["variants"]]}))
        return 0
    if args.list:
        for scenario in data["scenarios"]:
            print(scenario["id"] + "  " + scenario["title"] + "  [" + ", ".join(scenario["variants"]) + "]")
        return 0
    scenario, variant = select(data, args.scenario, args.variant)
    if args.describe:
        print(json.dumps({"scenario": scenario["id"], "variant": variant, "release": data["release"], "command": command_for(scenario, variant)}, indent=2))
        return 0
    return run(data, scenario, variant)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, RuntimeError, OSError, subprocess.SubprocessError) as error:
        print("Scenario failed: " + str(error), file=sys.stderr)
        sys.exit(1)
