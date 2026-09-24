"""Offline contracts for the executable documentation labs; no host API calls."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock


SPEC = importlib.util.spec_from_file_location("scenario_runner", Path(__file__).with_name("runner.py"))
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)


class CatalogContract(unittest.TestCase):
    def test_every_documented_variant_has_an_executable_fixture(self):
        data = runner.catalog()
        self.assertEqual([s["id"] for s in data["scenarios"]], [f"{n:02}" for n in range(1, 11)])
        index = (runner.ROOT / "docs/scenarios/index.md").read_text()
        for scenario in data["scenarios"]:
            with self.subTest(scenario=scenario["id"]):
                script = Path(scenario["script"])
                self.assertFalse(script.is_absolute())
                self.assertNotIn("..", script.parts)
                self.assertTrue((runner.ROOT / script).is_file())
                page = (runner.ROOT / "docs/scenarios" / scenario["page"]).read_text()
                self.assertIn(scenario["page"], index)
                self.assertIn(f"./examples/scenarios/run.sh {scenario['id']}", page)
                self.assertTrue(scenario["variants"])
                for variant, environment in scenario["variants"].items():
                    self.assertTrue(re.fullmatch(r"[a-z-]+", variant))
                    self.assertTrue(all(k.startswith("REPLICOVE_") for k in environment))
                    if variant != next(iter(scenario["variants"])):
                        self.assertIn(f"./examples/scenarios/run.sh {scenario['id']} {variant}", page)
                    command = runner.command_for(scenario, variant)
                    self.assertEqual(command[:2], ["bash", str(script)])
                    self.assertEqual(command[2:], [variant] if scenario.get("argumentVariant") else [])

    def test_release_is_immutable_and_assets_are_version_matched(self):
        release = runner.catalog()["release"]
        self.assertRegex(release["version"], r"^v\d+\.\d+\.\d+(?:-[a-z]+\.\d+)?$")
        self.assertRegex(release["sourceCommit"], r"^[a-f0-9]{40}$")
        self.assertRegex(release["image"], r"^ghcr\.io/nimeshbuilds/replicove@sha256:[a-f0-9]{64}$")
        self.assertEqual(set(release["assets"]), {
            f"replicove-{release['version']}-linux-amd64.tar.gz",
            f"replicove-{release['version'][1:]}.tgz",
            "replicove-crds.yaml", "replicove-install.yaml",
        })
        for digest in release["assets"].values():
            self.assertRegex(digest, r"^[a-f0-9]{64}$")

    def test_matrix_enumerates_all_variants_without_starting_a_lab(self):
        output = io.StringIO()
        with mock.patch.object(runner.sys, "argv", ["runner.py", "--matrix"]), \
             mock.patch.object(runner, "preflight") as preflight, \
             mock.patch.object(runner.subprocess, "run") as process, \
             contextlib.redirect_stdout(output):
            self.assertEqual(runner.main(), 0)
        expected = [(s["id"], v) for s in runner.catalog()["scenarios"] for v in s["variants"]]
        actual = [(s["scenario"], s["variant"]) for s in json.loads(output.getvalue())["include"]]
        self.assertEqual(actual, expected)
        preflight.assert_not_called()
        process.assert_not_called()

    def test_bad_ids_and_variants_fail_before_preflight(self):
        for args in [[], ["11"], ["01", "production"], ["../host"]]:
            with self.subTest(args=args), \
                 mock.patch.object(runner.sys, "argv", ["runner.py", *args]), \
                 mock.patch.object(runner, "preflight") as preflight:
                with self.assertRaises(ValueError):
                    runner.main()
                preflight.assert_not_called()

    def test_list_and_describe_do_not_access_docker_or_kubernetes(self):
        for args in [["--list"], ["05", "native", "--describe"]]:
            with self.subTest(args=args), \
                 mock.patch.object(runner.sys, "argv", ["runner.py", *args]), \
                 mock.patch.object(runner.subprocess, "run") as process, \
                 mock.patch.object(runner.subprocess, "check_output") as output, \
                 contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(runner.main(), 0)
                process.assert_not_called()
                output.assert_not_called()


class DownloadContract(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.path = Path(self.temporary.name) / "asset.tgz"
        self.payload = b"verified fixture bytes"
        self.digest = hashlib.sha256(self.payload).hexdigest()
        self.url = "https://github.com/nimeshbuilds/replicove/releases/download/v1.2.3/asset.tgz"

    def test_valid_cached_bytes_skip_network(self):
        self.path.write_bytes(self.payload)
        with mock.patch.object(runner.urllib.request, "urlopen") as network:
            runner.download(self.url, self.path, self.digest)
        network.assert_not_called()

    def test_corrupt_cache_is_replaced_only_after_verification(self):
        self.path.write_bytes(b"corrupt cache")
        with mock.patch.object(runner.urllib.request, "urlopen", return_value=io.BytesIO(self.payload)) as network:
            runner.download(self.url, self.path, self.digest)
        self.assertEqual(self.path.read_bytes(), self.payload)
        self.assertEqual(network.call_args.args[0].full_url, self.url)
        self.assertFalse(self.path.with_suffix(".tgz.download").exists())

    def test_checksum_mismatch_never_replaces_previous_file(self):
        self.path.write_bytes(b"original bytes")
        with mock.patch.object(runner.urllib.request, "urlopen", side_effect=lambda *a, **k: io.BytesIO(b"wrong bytes")) as network, \
             mock.patch.object(runner.time, "sleep"):
            with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
                runner.download(self.url, self.path, self.digest)
        self.assertEqual(network.call_count, 3)
        self.assertEqual(self.path.read_bytes(), b"original bytes")
        self.assertFalse(self.path.with_suffix(".tgz.download").exists())

    def test_network_retry_still_checks_successful_bytes(self):
        with mock.patch.object(runner.urllib.request, "urlopen", side_effect=[OSError("fixture unavailable"), io.BytesIO(self.payload)]), \
             mock.patch.object(runner.time, "sleep"):
            runner.download(self.url, self.path, self.digest)
        self.assertEqual(self.path.read_bytes(), self.payload)


class ArchiveContract(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.path = Path(self.temporary.name) / "release"
        self.path.mkdir()
        self.release = {"version": "v1.2.3", "assets": {"replicove-v1.2.3-linux-amd64.tar.gz": "fixture-digest"}}
        self.archive = self.path / next(iter(self.release["assets"]))

    def archive_with(self, member_type=tarfile.REGTYPE, extra=False):
        with tarfile.open(self.archive, "w:gz") as bundle:
            entry = tarfile.TarInfo("replicove")
            entry.type = member_type
            payload = b"reviewed executable"
            if member_type == tarfile.REGTYPE:
                entry.size = len(payload)
                bundle.addfile(entry, io.BytesIO(payload))
            else:
                entry.linkname = "../outside"
                bundle.addfile(entry)
            if extra:
                traversal = tarfile.TarInfo("../../outside")
                traversal.size = 9
                bundle.addfile(traversal, io.BytesIO(b"malicious"))

    def test_extracts_only_regular_cli_and_checks_exact_version(self):
        self.archive_with(extra=True)
        with mock.patch.object(runner, "download") as download, \
             mock.patch.object(runner.subprocess, "check_output", return_value="replicove version v1.2.3\n") as execute:
            runner.prepare_release(self.release, self.path)
        self.assertEqual((self.path / "replicove").read_bytes(), b"reviewed executable")
        self.assertEqual((self.path / "replicove").stat().st_mode & 0o777, 0o700)
        self.assertFalse((self.path.parent / "outside").exists())
        self.assertEqual(execute.call_args.args[0], [str(self.path / "replicove"), "--version"])
        self.assertEqual(download.call_args.args[2], "fixture-digest")

    def test_links_and_directories_cannot_be_executed(self):
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.DIRTYPE):
            with self.subTest(kind=kind):
                self.archive_with(kind)
                with mock.patch.object(runner, "download"), mock.patch.object(runner.subprocess, "check_output") as execute:
                    with self.assertRaisesRegex(RuntimeError, "regular executable"):
                        runner.prepare_release(self.release, self.path)
                execute.assert_not_called()
                self.assertFalse((self.path / "replicove").exists())

    def test_wrong_cli_version_is_rejected(self):
        self.archive_with()
        with mock.patch.object(runner, "download"), \
             mock.patch.object(runner.subprocess, "check_output", return_value="replicove version v9.9.9\n"):
            with self.assertRaisesRegex(RuntimeError, "pinned release"):
                runner.prepare_release(self.release, self.path)


class EnvironmentContract(unittest.TestCase):
    def test_host_and_release_overrides_are_replaced_or_removed(self):
        with tempfile.TemporaryDirectory() as folder:
            work = Path(folder)
            release = runner.catalog()["release"]
            hostile = {
                "PATH": "/usr/bin", "SAFE_FIXTURE_VALUE": "keep",
                "KUBECONFIG": "/private/production.kubeconfig",
                "REPLICOVE_IMAGE": "unexpected-image", "REPLICOVE_MIRROR_BASE": "unexpected",
                "REPLICOVE_HELM_NAMESPACE": "production", "RELEASE_IMAGE_DIGEST": "unexpected",
                "KUBERNETES_MASTER": "https://production.invalid",
                "HELM_KUBEAPISERVER": "https://production.invalid", "HELM_KUBECONTEXT": "production",
                "HELM_KUBETOKEN": "fixture-token", "HELM_KUBEASUSER": "fixture-admin",
            }
            with mock.patch.dict(os.environ, hostile, clear=True):
                env = runner.clean_environment(work, release, work / "release", {"REPLICOVE_INSTALLER": "helm"})
            self.assertEqual(env["REPLICOVE_IMAGE"], release["image"])
            self.assertEqual(env["REPLICOVE_USE_RELEASE_CLI"], "true")
            self.assertEqual(env["REPLICOVE_INSTALLER"], "helm")
            self.assertEqual(env["REPLICOVE_CHART_VERSION"], release["version"][1:])
            self.assertEqual(env["KUBECONFIG"], str(work / "unused-host.kubeconfig"))
            self.assertEqual(env["SAFE_FIXTURE_VALUE"], "keep")
            self.assertFalse((work / "unused-host.kubeconfig").exists())
            self.assertEqual(json.loads((work / "helm-registry.json").read_text()), {})
            self.assertFalse(any(k.startswith("HELM_KUBE") for k in env))
            for forbidden in ("REPLICOVE_MIRROR_BASE", "REPLICOVE_HELM_NAMESPACE", "RELEASE_IMAGE_DIGEST", "KUBERNETES_MASTER"):
                self.assertNotIn(forbidden, env)


class PreflightContract(unittest.TestCase):
    def test_unsupported_platform_fails_without_running_a_command(self):
        with mock.patch.object(runner.platform, "system", return_value="Darwin"), \
             mock.patch.object(runner.subprocess, "run") as execute:
            with self.assertRaisesRegex(RuntimeError, "Linux amd64"):
                runner.preflight()
        execute.assert_not_called()

    def test_missing_prerequisite_fails_before_docker(self):
        with mock.patch.object(runner.platform, "system", return_value="Linux"), \
             mock.patch.object(runner.platform, "machine", return_value="x86_64"), \
             mock.patch.object(runner.shutil, "which", side_effect=lambda name: None if name == "docker" else "/usr/bin/" + name), \
             mock.patch.object(runner.subprocess, "run") as execute:
            with self.assertRaisesRegex(RuntimeError, "prerequisites.*docker"):
                runner.preflight()
        execute.assert_not_called()

    def test_daemon_architecture_and_resources_are_read_only_checks(self):
        cases = [
            ({"OSType": "linux", "Architecture": "arm64", "NCPU": 8, "MemTotal": 16 * 2**30}, False),
            ({"OSType": "linux", "Architecture": "x86_64", "NCPU": 2, "MemTotal": 16 * 2**30}, False),
            ({"OSType": "linux", "Architecture": "x86_64", "NCPU": 4, "MemTotal": 4 * 2**30}, False),
            ({"OSType": "linux", "Architecture": "x86_64", "NCPU": 4, "MemTotal": 8 * 2**30}, True),
        ]
        for daemon, success in cases:
            with self.subTest(daemon=daemon), \
                 mock.patch.object(runner.platform, "system", return_value="Linux"), \
                 mock.patch.object(runner.platform, "machine", return_value="x86_64"), \
                 mock.patch.object(runner.shutil, "which", return_value="/fixture/tool"), \
                 mock.patch.object(runner.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, json.dumps(daemon), "")) as execute, \
                 contextlib.redirect_stdout(io.StringIO()):
                if success:
                    runner.preflight()
                else:
                    with self.assertRaises(RuntimeError):
                        runner.preflight()
                self.assertEqual(execute.call_args.args[0], ["docker", "info", "--format", "{{json .}}"])
                self.assertEqual(execute.call_count, 1)


class ExecutionContract(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.binary = self.root / "bin/replicove"
        self.binary.parent.mkdir()
        self.binary.write_bytes(b"user binary")
        self.binary.chmod(0o751)
        self.root_patch = mock.patch.object(runner, "ROOT", self.root)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.data = runner.catalog()
        self.scenario, self.variant = runner.select(self.data, "01", "helm")

    def fake_release(self, release, path):
        path.mkdir(parents=True)
        (path / "replicove").write_bytes(b"pinned release")
        (path / "replicove").chmod(0o700)

    def run_mock(self, report, code=0, leftover=False, during_run=None):
        def execute(command, **kwargs):
            if command == runner.command_for(self.scenario, self.variant):
                self.assertEqual(self.binary.read_bytes(), b"pinned release")
                self.assertEqual(kwargs["env"]["REPLICOVE_IMAGE"], self.data["release"]["image"])
                if during_run is not None:
                    during_run()
                if report is not None:
                    location = self.root / ".cache" / self.scenario["evidence"] / "run.fixture/artifacts/report.json"
                    location.parent.mkdir(parents=True)
                    location.write_text(json.dumps(report))
                return subprocess.CompletedProcess(command, code)
            return subprocess.CompletedProcess(command, 0)

        after = {"personal-cluster", "replicove-leftover"} if leftover else {"personal-cluster"}
        with mock.patch.object(runner, "preflight"), \
             mock.patch.object(runner, "prepare_release", side_effect=self.fake_release), \
             mock.patch.object(runner, "clusters", side_effect=[{"personal-cluster"}, after]), \
             mock.patch.object(runner.subprocess, "run", side_effect=execute), \
             contextlib.redirect_stdout(io.StringIO()):
            return runner.run(self.data, self.scenario, self.variant)

    def assert_restored(self):
        self.assertEqual(self.binary.read_bytes(), b"user binary")
        self.assertEqual(self.binary.stat().st_mode & 0o777, 0o751)
        self.assertFalse((self.root / ".cache/scenarios/active.lock").exists())

    def receipt(self):
        paths = list((self.root / ".cache/scenarios").glob("run.*/report.json"))
        self.assertEqual(len(paths), 1)
        return json.loads(paths[0].read_text())

    def test_success_restores_existing_binary_and_records_cleanup(self):
        self.assertEqual(self.run_mock({"result": "passed", "scenarios": ["fixture"]}), 0)
        self.assert_restored()
        self.assertEqual(self.receipt()["result"], "passed")
        self.assertEqual(self.receipt()["remainingLabClusters"], [])
        self.assertEqual(len(self.receipt()["reports"]), 1)

    def test_command_failure_preserves_exit_status_and_binary(self):
        self.assertEqual(self.run_mock(None, code=17), 17)
        self.assert_restored()
        self.assertEqual(self.receipt()["result"], "failed")

    def test_existing_executable_link_is_restored_without_writing_target(self):
        target = self.root / "external-cli"
        self.binary.replace(target)
        self.binary.symlink_to(target)
        self.assertEqual(self.run_mock({"result": "passed"}, during_run=lambda: self.assertEqual(target.read_bytes(), b"user binary")), 0)
        self.assertTrue(self.binary.is_symlink())
        self.assertEqual(self.binary.readlink(), target)
        self.assertEqual(target.read_bytes(), b"user binary")
        self.assert_restored()

    def test_bin_directory_link_is_rejected_before_downloads(self):
        directory = self.root / "external-bin"
        self.binary.parent.rename(directory)
        self.binary.parent.symlink_to(directory, target_is_directory=True)
        with mock.patch.object(runner, "preflight"), \
             mock.patch.object(runner, "prepare_release") as prepare:
            with self.assertRaisesRegex(RuntimeError, "directory symlink"):
                runner.run(self.data, self.scenario, self.variant)
        prepare.assert_not_called()
        self.assertEqual((directory / "replicove").read_bytes(), b"user binary")
        self.assertFalse((self.root / ".cache").exists())

    def test_missing_receipt_cannot_pass(self):
        with self.assertRaisesRegex(RuntimeError, "required assertion report"):
            self.run_mock(None)
        self.assert_restored()
        self.assertEqual(self.receipt()["result"], "failed")

    def test_explicit_failed_receipt_cannot_be_overruled_by_partial_passes(self):
        with self.assertRaisesRegex(RuntimeError, "did not indicate success"):
            self.run_mock({"result": "failed", "passed": ["partial"], "ownedCleanup": True})
        self.assert_restored()
        self.assertEqual(self.receipt()["result"], "failed")

    def test_remaining_disposable_cluster_cannot_pass(self):
        with self.assertRaisesRegex(RuntimeError, "cleanup was incomplete"):
            self.run_mock({"result": "passed"}, leftover=True)
        self.assert_restored()
        self.assertEqual(self.receipt()["remainingLabClusters"], ["replicove-leftover"])

    def test_rejected_preflight_leaves_files_untouched(self):
        with mock.patch.object(runner, "preflight", side_effect=RuntimeError("fixture refusal")), \
             mock.patch.object(runner, "prepare_release") as prepare, \
             mock.patch.object(runner.subprocess, "run") as process:
            with self.assertRaisesRegex(RuntimeError, "fixture refusal"):
                runner.run(self.data, self.scenario, self.variant)
        prepare.assert_not_called()
        process.assert_not_called()
        self.assertEqual(self.binary.read_bytes(), b"user binary")
        self.assertFalse((self.root / ".cache").exists())

    def test_existing_lab_lock_prevents_artifact_or_binary_changes(self):
        lock = self.root / ".cache/scenarios/active.lock"
        lock.mkdir(parents=True)
        (lock / "pid").write_text("1234\n")
        with mock.patch.object(runner, "preflight"), \
             mock.patch.object(runner, "prepare_release") as prepare:
            with self.assertRaisesRegex(RuntimeError, "Another lab owns"):
                runner.run(self.data, self.scenario, self.variant)
        prepare.assert_not_called()
        self.assertEqual((lock / "pid").read_text(), "1234\n")
        self.assertEqual(self.binary.read_bytes(), b"user binary")


class EvidenceContract(unittest.TestCase):
    def test_supported_report_formats_require_success_without_partial_failures(self):
        for report, expected in [
            ({"result": "passed", "scenarios": ["fixture"]}, True),
            ({"result": "failed", "passed": ["partial"], "ownedCleanup": True}, False),
            ({"passed": ["fixture"]}, True),
            ({"passed": ["partial"], "failed": ["cleanup"]}, False),
            ({"passed": []}, False),
            ({"passed": "not a list"}, False),
            ({"scenario": "yaml", "ownedCleanup": True, "sourcePreserved": True}, True),
            ({"scenario": "yaml", "ownedCleanup": True, "sourcePreserved": False}, False),
            ({"sourcePreserved": True}, False),
            ({}, False),
        ]:
            with self.subTest(report=report):
                self.assertIs(runner.evidence_passed(report), expected)


if __name__ == "__main__":
    unittest.main()
