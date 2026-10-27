"""Real browser/API/PostgreSQL acceptance on an isolated, owned test database."""
import argparse
import base64
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
import zlib

from user_asset_export_v3_ci import configure as configure_user_export, capture as capture_user_export, validate as validate_user_export
from user_asset_export_v3_terminal_ci import SUITES as EXPORT_TERMINAL_SUITES, configure as configure_terminal_export, capture as capture_terminal_export, validate as validate_terminal_export
from user_asset_export_v3_worker_guard import OwnedExportWorkerGuard
from asset_export_v3_running_cancel_guard import RunningExportCancelGuard
from owned_rescue_runner import OwnedCIWorker, ENV_FDS
from user_asset_export_v3_running_cancel_ci import SUITE as RUNNING_CANCEL_SUITE, configure as configure_running_cancel, capture as capture_running_cancel, validate as validate_running_cancel
from group_asset_export_v3_running_cancel_ci import SUITE as GROUP_RUNNING_CANCEL_SUITE, configure as configure_group_running_cancel, capture as capture_group_running_cancel, validate as validate_group_running_cancel
from computer_asset_export_v3_running_cancel_ci import SUITE as COMPUTER_RUNNING_CANCEL_SUITE, configure as configure_computer_running_cancel, capture as capture_computer_running_cancel, validate as validate_computer_running_cancel
from computer_asset_export_v3_ci import configure as configure_computer_export, capture as capture_computer_export, validate as validate_computer_export
from computer_asset_export_v3_terminal_ci import SUITES as COMPUTER_EXPORT_TERMINAL_SUITES, configure as configure_computer_terminal, capture as capture_computer_terminal, validate as validate_computer_terminal
from group_asset_export_v3_ci import configure as configure_group_export, capture as capture_group_export, validate as validate_group_export
from group_asset_export_v3_terminal_ci import SUITES as GROUP_EXPORT_TERMINAL_SUITES, configure as configure_group_terminal, capture as capture_group_terminal, validate as validate_group_terminal
from ci_postgres_image import postgres_image_args
from test_integration import IMAGE
from ldap_e2e_fixture import LDAPFixture, uac_ldap_failure_stage
from user_asset_export_v3_uac_ci import (configure_uac, capture_uac_provenance,
    discover_uac_candidate, finalize_uac_evidence, uac_diagnostic_error,
    uac_failure_code, uac_failure_stage, uac_failure_message)
from account_barrier_e2e_fixture import AccountBarrierFixture
from directory_e2e_credentials import configure_directory_credentials
from directory_v4_worker_control import DelayedOwnedWorker, URL_ENV, TOKEN_ENV
from e2e_owned_container import OWNER_LABEL, remove_owned_container
from e2e_runtime_diagnostics import http_handler_panicked, RuntimeDiagnostics
from e2e_container_startup_diagnostics import report_container_startup_failure


REAL_TAB_LIFECYCLE_SUITES = {"account-references", "session-invalidation", "directory-readers", "directory-v2-readers", "user-assets-v2-readers", "user-assets-v3", "computer-assets-v3-readers", "group-assets-v3-readers", "directory-v3-readers"}
USER_ASSETS_V2_SUITES = {"user-assets-v2", "user-assets-v2-readers"}
DIRECTORY_V2_SUITES = {"directory-v2", "directory-v2-controls", "directory-v2-readers"} | USER_ASSETS_V2_SUITES
COMPUTER_ASSETS_V3_SUITES = {"computer-assets-v3", "computer-assets-v3-readers"}
COMPUTER_EXPORT_SUITES = COMPUTER_EXPORT_TERMINAL_SUITES | {"computer-asset-export-v3"}
GROUP_EXPORT_SUITES = GROUP_EXPORT_TERMINAL_SUITES | {"group-asset-export-v3"}
GROUP_ASSETS_V3_SUITES = {"group-assets-v3", "group-assets-v3-readers"}
EMPTY_TRANSITION_SUITES = {"group-assets-v3", "computer-assets-v3"}
KIND_RUNNING_CANCEL_SUITES = {GROUP_RUNNING_CANCEL_SUITE, COMPUTER_RUNNING_CANCEL_SUITE}
UAC_SUITE = "user-asset-export-v3-uac-mixed33"
DIRECTORY_V3_SUITES = {UAC_SUITE} | KIND_RUNNING_CANCEL_SUITES | {RUNNING_CANCEL_SUITE} | COMPUTER_EXPORT_SUITES | GROUP_EXPORT_SUITES | EXPORT_TERMINAL_SUITES | {"user-asset-export-v3", "directory-v3-backend", "user-assets-v3", "directory-v3", "directory-v3-controls", "directory-v3-readers"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES
DIRECTORY_V4_SUITES = {"directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh"}
DIRECTORY_SUITES = DIRECTORY_V4_SUITES | DIRECTORY_V3_SUITES | {"directory", "directory-controls", "directory-readers"} | DIRECTORY_V2_SUITES
EMPTY_DIRECTORY_SUITES = {"directory-controls", "directory-readers", "directory-v2-controls", "directory-v2-readers", "directory-v3-controls", "directory-v3-readers"}
SLOW_DIRECTORY_SUITES = {"directory-controls", "directory-v2-controls", "directory-v3-controls"}

V4_CONTROLS_BUDGETS = {
    "directory-v4-controls": 660,
    "directory-v4-cancel": 930,
    "directory-v4-readers": 660,
}
DIRECTORY_V4_SUITES |= V4_CONTROLS_BUDGETS.keys()
DIRECTORY_SUITES |= V4_CONTROLS_BUDGETS.keys()
SLOW_DIRECTORY_SUITES.add("directory-v4-cancel")
REAL_TAB_LIFECYCLE_SUITES.add("directory-v4-readers")


def v4_delayed_worker(launch, ready_url):
    """Capture the runner's fixed spawn and bounded cooperative readiness."""
    def ready(process, cancelled, deadline):
        for _ in range(100):
            remaining = deadline - time.monotonic()
            if cancelled.is_set() or remaining <= 0 or process.poll() is not None:
                return False
            healthy = False
            try:
                with urllib.request.urlopen(ready_url, timeout=min(1, remaining)) as response:
                    healthy = response.status == 200
            except (OSError, urllib.error.URLError):
                pass
            if cancelled.is_set() or time.monotonic() >= deadline or process.poll() is not None:
                return False
            if healthy:
                return True
            if cancelled.wait(min(.1, max(0, deadline - time.monotonic()))):
                return False
        return False
    return DelayedOwnedWorker(launch, ready)


def cleanup_v4_process(process, record):
    """Called only by runner finally; never used by the delayed controller."""
    if process is None:
        return True, None, False, ()
    failures = []
    forced = False
    reaped = False
    code = None
    try:
        if process.poll() is None:
            process.terminate()
        code = process.wait(timeout=30)
        reaped = True
    except Exception:
        failures.append("process_graceful_cleanup")
        # Even failed terminate/wait must not skip a bounded owned kill/reap.
        try:
            if process.poll() is None:
                forced = True
                process.kill()
        except Exception:
            failures.append("process_kill")
        try:
            code = process.wait(timeout=10)
            reaped = True
        except Exception:
            failures.append("process_reap")
    record["forced_kill"] = forced
    record["after_cleanup"] = code
    if reaped and code != 0:
        failures.append("process_nonzero_exit")
    return reaped, code, forced, tuple(failures)


def cleanup_v4_run(controller, worker, server, diagnostics, logs, ldap_fixture,
                   message_fixture, creation_attempted, name, owner):
    """Finally's single owner: transfer before snapshot, then all resources."""
    failures = []
    transferred = controller is None
    if controller is not None:
        try:
            transfer = controller.seal_and_take()
            worker = transfer.process
            transferred = True
            failures.extend(transfer.failures)
        except Exception:
            # The helper promises ordinary close/join failures as a transfer,
            # not an exception. A violated contract still cannot skip API/LDAP/DB.
            failures.append("controller_transfer")
    try:
        diagnostics.snapshot(server, worker)
    except Exception:
        failures.append("snapshot")
        diagnostics.report["collection_errors"].append("snapshot")
    reaped, code, forced, process_failures = cleanup_v4_process(
        worker, diagnostics.report["processes"]["worker"])
    failures.extend(process_failures)
    if controller is not None:
        try:
            if not controller.finish_after_wait(reaped=reaped and transferred, returncode=code, forced_kill=forced):
                failures.append("controller_cleanup")
        except Exception:
            failures.append("controller_cleanup")
    _, _, _, process_failures = cleanup_v4_process(
        server, diagnostics.report["processes"]["api"])
    failures.extend(process_failures)
    if ldap_fixture is not None:
        try:
            ldap_fixture.close()
        except Exception:
            failures.append("ldap_cleanup")
    try:
        panicked = http_handler_panicked(logs)
        diagnostics.report["http_panic"] = panicked
        if panicked:
            failures.append("http_panic")
    except Exception:
        failures.append("runtime_diagnostics")
    try:
        logs.close()
    except Exception:
        failures.append("log_cleanup")
    if message_fixture is not None:
        try:
            message_fixture.cleanup()
        except Exception:
            failures.append("message_cleanup")
    if creation_attempted:
        try:
            remove_owned_container(name, owner)
        except Exception:
            failures.append("container_cleanup")
    # Existing runtime.json has a closed schema. Keep additional safe cleanup
    # categories separate rather than silently changing its evidence contract.
    allowed = {"listener_close_failed", "listener_join_failed", "readiness_join_failed",
               "snapshot", "process_graceful_cleanup", "process_reap", "process_kill",
               "process_nonzero_exit", "controller_cleanup", "controller_transfer", "ldap_cleanup",
               "http_panic", "runtime_diagnostics", "log_cleanup",
               "message_cleanup", "container_cleanup"}
    for failure in failures:
        print("Directory v4 cleanup: " + (failure if failure in allowed else "cleanup_failed"),
              file=sys.stderr)
    return failures


def health_environment(source):
    """Keep real CI image/browser inputs, never inherit application credentials."""
    allowed = {"ADTR_CI_POSTGRES_BUNDLE", "ADTR_CI_POSTGRES_ARCHIVE_SHA",
               "ADTR_CI_POSTGRES_PRODUCER_ATTEMPT", "ADTR_E2E_CHROMIUM_PATH",
               "ADTR_E2E_HEALTH_JOB_DEADLINE", "ADTR_EVENT_HEAD_SHA", "ADTR_EVENT_BASE_SHA"}
    return {key: value for key, value in source.items()
            if key in allowed or not key.upper().startswith(("ADTR_", "POSTGRES_", "PG"))}


def health_digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False,
        separators=(",", ":"), allow_nan=False).encode("utf-8")).hexdigest()


def health_git(*args, deadline):
    return subprocess.run(["git", *args], check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=health_timeout(30, deadline)).stdout


def health_file(path, deadline=None):
    if deadline is not None:
        health_timeout(1, deadline)
    target = Path(path)
    if target.is_symlink() or any(p.is_symlink() for p in target.parents) or not target.is_file():
        raise RuntimeError("health_nonregular_file")
    raw = target.read_bytes()
    if deadline is not None:
        health_timeout(1, deadline)
    if len(raw) > 1024**3:
        raise RuntimeError("health_invalid_file_size")
    return {"path": target.as_posix(), "sha256": hashlib.sha256(raw).hexdigest(), "bytes": len(raw)}


def health_source_snapshot(deadline):
    """Observe the actual complete tracked source tree, not browser claims."""
    if health_git("status", "--porcelain", "--untracked-files=normal", deadline=deadline):
        raise RuntimeError("health_dirty_checkout")
    commit = health_git("rev-parse", "HEAD", deadline=deadline).decode().strip()
    tree = health_git("rev-parse", "HEAD^{tree}", deadline=deadline).decode().strip()
    sources = []
    for record in health_git("ls-tree", "-rz", "--full-tree", "HEAD", deadline=deadline).split(b"\0"):
        health_timeout(1, deadline)
        if not record:
            continue
        header, name = record.split(b"\t", 1)
        mode, kind, blob = header.decode().split()
        path = name.decode("utf-8", "strict")
        if path in {"Makefile", "go.mod", "go.sum"} or path.startswith((".github/workflows/", "cmd/", "internal/", "scripts/", "web/")):
            if kind != "blob" or mode not in {"100644", "100755"}:
                raise RuntimeError("health_nonregular_source")
            item = health_file(path, deadline)
            raw = Path(path).read_bytes()
            if hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest() != blob:
                raise RuntimeError("health_source_byte_drift")
            sources.append(dict(item, gitBlob=blob))
    health_timeout(1, deadline)
    sources.sort(key=lambda entry: entry["path"])
    if not sources:
        raise RuntimeError("health_source_inventory_empty")
    return {"commit": commit, "tree": tree, "inventorySHA256": health_digest(sources)}


def health_deadline(environment):
    raw = environment.get("ADTR_E2E_HEALTH_JOB_DEADLINE", "")
    if not raw.isdigit():
        raise RuntimeError("health_job_deadline_missing")
    wall_remaining = int(raw) - time.time()
    if wall_remaining > 1620:
        raise RuntimeError("health_job_deadline_invalid")
    remaining = min(1500, wall_remaining)
    if remaining <= 300:
        raise RuntimeError("health_job_deadline_exhausted")
    return time.monotonic() + remaining


def health_timeout(seconds, deadline, reserve=300):
    if deadline is None:
        return seconds
    remaining = deadline - time.monotonic() - reserve
    if remaining <= 0:
        raise RuntimeError("health_deadline_exhausted")
    return min(seconds, remaining)


def health_postgres_args(env, deadline):
    health_timeout(1, deadline)
    def bounded_run(command, **kwargs):
        kwargs["timeout"] = health_timeout(kwargs.get("timeout", 15), deadline)
        kwargs["env"] = env
        return subprocess.run(command, **kwargs)
    args = postgres_image_args(IMAGE, environ=env, run=bounded_run)
    health_timeout(1, deadline)
    return args


def health_build_process(command, env, timeout, capture=True):
    """Own build or browser groups; timeout cannot leave descendant writers alive."""
    process = subprocess.Popen(command, env=env, stdout=subprocess.PIPE if capture else None,
                               stderr=subprocess.PIPE if capture else None, start_new_session=True)
    try:
        process.communicate(timeout=timeout)
        code = process.returncode
        # The direct build must join its children. A surviving owned process
        # group is failure, even if npm/go already returned zero.
        try:
            os.killpg(process.pid, 0)
        except ProcessLookupError:
            return code
        raise RuntimeError("health_owned_descendant_survived")
    except BaseException:
        # pgid is the exact handle created by start_new_session, never a search
        # or external PID. Attempt every bounded cleanup despite earlier errors.
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except (ProcessLookupError, OSError):
            pass
        try:
            process.communicate(timeout=5)
        except Exception:
            pass
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except (ProcessLookupError, OSError):
            pass
        try:
            process.communicate(timeout=5)
        except Exception:
            pass
        raise RuntimeError("health_owned_process_failed") from None


def health_build(env, deadline):
    """Own both bounded builds and freeze output bytes before API/browser start."""
    source = health_source_snapshot(deadline)
    nonce = str(uuid.uuid4())
    identity = dict(commit=source["commit"], tree=source["tree"],
        workflowSHA=env.get("GITHUB_WORKFLOW_SHA", ""), runID=env.get("GITHUB_RUN_ID", ""),
        runAttempt=env.get("GITHUB_RUN_ATTEMPT", ""), nonce=nonce)
    if (env.get("GITHUB_SHA") != source["commit"]
            or len(identity["workflowSHA"]) != 40
            or any(c not in "0123456789abcdef" for c in identity["workflowSHA"])
            or not identity["runID"].isdigit() or int(identity["runID"]) < 1
            or not identity["runAttempt"].isdigit() or int(identity["runAttempt"]) < 1):
        raise RuntimeError("health_ci_identity_invalid")
    steps = []
    commands = [
        ("backend", "go_build", ["go", "build", "-buildvcs=false", "-trimpath", "-o", "bin/adtr", "./cmd/adtr"], 180),
        ("frontend", "npm_build", ["npm", "run", "build", "--prefix", "web"], 120),
    ]
    for target, operation, command, timeout in commands:
        before = health_source_snapshot(deadline)
        exit_code = health_build_process(command, env, health_timeout(timeout, deadline))
        after = health_source_snapshot(deadline)
        health_timeout(1, deadline)
        if exit_code != 0 or before != source or after != source:
            raise RuntimeError("health_build_failed_or_source_drift")
        steps.append(dict(target=target, operation=operation, exitCode=exit_code,
            sourceBeforeSHA256=before["inventorySHA256"], sourceAfterSHA256=after["inventorySHA256"]))
    frontend = []
    for path in Path("web/dist").rglob("*"):
        health_timeout(1, deadline)
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise RuntimeError("health_nonregular_bundle")
        if path.is_file():
            frontend.append(health_file(path, deadline))
    frontend.sort(key=lambda item: item["path"])
    health_timeout(1, deadline)
    if not any(item["path"] == "web/dist/index.html" for item in frontend):
        raise RuntimeError("health_frontend_missing")
    record = dict(producer="owned_runner", mode="built_in_run", identity=identity,
        source=source, steps=steps, outputs=dict(binary=health_file("bin/adtr", deadline), frontend=frontend))
    env["ADTR_E2E_HEALTH_RUN_NONCE"] = nonce
    # The workflow must independently validate using this same nonsecret nonce.
    github_env = env.get("GITHUB_ENV")
    if not github_env or not Path(github_env).is_file() or Path(github_env).is_symlink():
        raise RuntimeError("health_ci_environment_file_missing")
    with open(github_env, "a", encoding="utf-8") as stream:
        stream.write("ADTR_E2E_HEALTH_RUN_NONCE=" + nonce + "\n")
    return record


def cleanup_health_run(controller, worker, server, diagnostics, logs,
                       creation_attempted, name, owner):
    """Always try independent cleanup; only completed operations become facts."""
    operations, failures = [], []
    transferred = worker_reaped = api_reaped = removed = False
    state = {"state": "absent", "launchCount": 0}
    if controller is not None:
        try:
            transfer = controller.seal_and_take()
            worker = transfer.process
            transferred = True
            operations.append("seal_and_take")
            if transfer.failures:
                failures.append("controller_seal_failed")
        except Exception:
            failures.append("controller_seal_failed")
    try:
        diagnostics.snapshot(server, worker)
    except Exception:
        diagnostics.report["collection_errors"].append("snapshot")
    reaped, code, forced, problems = cleanup_v4_process(worker, diagnostics.report["processes"]["worker"])
    worker_reaped = worker is not None and reaped
    if worker_reaped:
        operations.append("worker_reap")
    if problems:
        failures.append("worker_reap_failed")
    if controller is not None:
        try:
            finished = controller.finish_after_wait(reaped=reaped and transferred,
                                                    returncode=code, forced_kill=forced)
            if finished:
                operations.append("finish_after_wait")
            else:
                failures.append("controller_finish_failed")
            state = controller.state()
        except Exception:
            failures.append("controller_finish_failed")
    reaped, _, _, problems = cleanup_v4_process(server, diagnostics.report["processes"]["api"])
    api_reaped = server is not None and reaped
    if api_reaped:
        operations.append("api_reap")
    if problems:
        failures.append("api_reap_failed")
    try:
        if http_handler_panicked(logs):
            failures.append("fixture_close_failed")
    except Exception:
        failures.append("fixture_close_failed")
    try:
        logs.close()
    except Exception:
        failures.append("log_close_failed")
    if creation_attempted:
        try:
            remove_owned_container(name, owner)
            removed = True
            operations.append("owned_container_remove")
        except Exception:
            failures.append("owned_container_remove_failed")
    return dict(launchCount=state["launchCount"], ownershipTransferred=transferred,
        workerReaped=worker_reaped, apiReaped=api_reaped, controllerFinalState=state["state"],
        ownedContainerRemoved=removed, operations=operations, failures=failures)


def health_cleanup_summary(outcome):
    """Closed diagnostics only; never substitute this log for a cleanup receipt."""
    statuses = {name: "unknown" for name in
                ("seal_and_take", "worker_reap", "finish_after_wait", "api_reap", "owned_container_remove")}
    summary = dict(event="health_cleanup", **statuses, overall="unknown")
    if type(outcome) is not dict:
        return summary
    operations, failures = outcome.get("operations"), outcome.get("failures")
    allowed_failures = {"controller_seal_failed", "worker_reap_failed", "controller_finish_failed",
                        "api_reap_failed", "owned_container_remove_failed", "fixture_close_failed", "log_close_failed"}
    if (type(operations) is not list or type(failures) is not list
            or any(type(value) is not str or value not in statuses for value in operations)
            or any(type(value) is not str or value not in allowed_failures for value in failures)
            or operations != [name for name in statuses if name in operations]):
        return summary
    bindings = (("seal_and_take", "controller_seal_failed", "ownershipTransferred", True),
                ("worker_reap", "worker_reap_failed", "workerReaped", True),
                ("finish_after_wait", "controller_finish_failed", "controllerFinalState", "closed"),
                ("api_reap", "api_reap_failed", "apiReaped", True),
                ("owned_container_remove", "owned_container_remove_failed", "ownedContainerRemoved", True))
    for operation, failure, field, expected in bindings:
        value = outcome.get(field)
        if failure in failures:
            summary[operation] = "failed"
        elif operation in operations and type(value) is type(expected) and value == expected:
            summary[operation] = "completed"
    if failures:
        summary["overall"] = "failed"
    elif all(summary[name] == "completed" for name in statuses):
        summary["overall"] = "completed"
    return summary


def close_health_evidence(cleanup, build, env, deadline):
    """Called only after finally; no success receipt for a skipped cleanup branch."""
    health_timeout(1, deadline, reserve=0)
    required = ["seal_and_take", "worker_reap", "finish_after_wait", "api_reap", "owned_container_remove"]
    if (cleanup["failures"] or cleanup["operations"] != required
            or cleanup["launchCount"] != 1 or cleanup["controllerFinalState"] != "closed"
            or not all(cleanup[key] is True for key in
                       ("ownershipTransferred", "workerReaped", "apiReaped", "ownedContainerRemoved"))):
        raise RuntimeError("health_cleanup_incomplete")
    root = Path("web/test-results/tasks-health-recovery")
    if root.is_symlink() or not root.is_dir():
        raise RuntimeError("health_evidence_missing")
    manifests = []
    for path in root.rglob("*"):
        health_timeout(1, deadline, reserve=0)
        if path.is_symlink():
            raise RuntimeError("health_evidence_symlink")
        if path.name == "manifest.json" and path.is_file():
            manifests.append(path)
    if len(manifests) != 1:
        raise RuntimeError("health_manifest_count")
    manifest = manifests[0]
    health_timeout(1, deadline, reserve=0)
    receipt = dict(schemaVersion=1, suite="tasks-health-recovery", identity=build["identity"],
        manifestSHA256=hashlib.sha256(manifest.read_bytes()).hexdigest(), buildSource=build, **cleanup)
    with (manifest.parent / "cleanup-receipt.json").open("x", encoding="utf-8") as stream:
        json.dump(receipt, stream, sort_keys=True, ensure_ascii=False, allow_nan=False)
        stream.write("\n")
    subprocess.run([sys.executable, "scripts/validate_tasks_health_recovery_evidence.py",
        "--evidence-dir", str(manifest.parent), "--source-root", "."], env=env, check=True, timeout=health_timeout(60, deadline, reserve=0))
    publish = Path("web/playwright-report/tasks-health-recovery-validated")
    publish.mkdir(parents=True, exist_ok=False)
    for filename in ("manifest.json", "cleanup-receipt.json", "parent-failed-desktop.png",
                     "parent-failed-mobile.png", "child-succeeded-desktop.png", "child-succeeded-mobile.png"):
        health_timeout(1, deadline, reserve=0)
        shutil.copyfile(manifest.parent / filename, publish / filename)
    # Copying is not acceptance: independently re-read the precise published bytes.
    subprocess.run([sys.executable, "scripts/validate_tasks_health_recovery_evidence.py",
        "--evidence-dir", str(publish), "--source-root", "."], env=env, check=True, timeout=health_timeout(60, deadline, reserve=0))


def browser_command(suite):
    command = ["npm", "run", "test:e2e", "--prefix", "web", "--", f"e2e/{suite}.spec.ts"]
    if suite in {"messages-bulk", "messages-isolation", "tasks-health-recovery"}:
        command.extend(["--output", f"test-results/{suite}"])
    if suite in REAL_TAB_LIFECYCLE_SUITES:
        # These regressions require genuine background/foreground tab changes.
        # Headless shell keeps tabs visible even after focus emulation is off.
        # --headed overrides the ordinary suites' Playwright headless default.
        command.append("--headed")
        if sys.platform.startswith("linux") and not os.environ.get("DISPLAY"):
            if shutil.which("xvfb-run") is None:
                raise RuntimeError(
                    f"{suite} requires headed Chromium and a display; install Xvfb "
                    "with `cd web && npx playwright install --with-deps chromium` "
                    "or provide DISPLAY"
                )
            command = ["xvfb-run", "-a", *command]
    return command


def validate_messages_isolation_evidence():
    """Validate successful evidence, never infer acceptance from an upload glob.

    Fixed diagnostics deliberately do not echo malformed manifest values. Pixel
    review remains independent; proofEmpty is only the suite's DOM safety gate.
    Failure phases below are source-literal categories, never exception text,
    field values, manifest contents or dynamically supplied labels.
    """
    failure = "messages_isolation_evidence_invalid:file_discovery"
    try:
        def safe_path(path):
            path = Path(path).absolute()
            relative = path.relative_to(Path.cwd())
            current = Path.cwd()
            for part in relative.parts:
                current /= part
                if current.is_symlink():
                    raise ValueError()
            return path
        root = safe_path("web/test-results/messages-isolation").resolve()
        manifests = list(root.rglob("messages-soc-isolation-evidence.json"))
        if len(manifests) != 1:
            raise ValueError()
        manifest = safe_path(manifests[0])
        if not 0 < manifest.stat().st_size <= 5_000_000:
            raise ValueError()
        def unique(pairs):
            result = {}
            for key, value in pairs:
                if key in result:
                    raise ValueError()
                result[key] = value
            return result
        data = json.loads(manifest.read_text(), object_pairs_hook=unique)
        def require(ok):
            if not ok:
                raise ValueError()
        def digest(path):
            return hashlib.sha256(Path(path).read_bytes()).hexdigest()
        def revision(*args):
            return subprocess.check_output(["git", *args], stderr=subprocess.DEVNULL,
                                           timeout=10, text=True).strip()
        failure = "messages_isolation_evidence_invalid:checkout_binding"
        require(type(data["formatVersion"]) is int and data["formatVersion"] == 1 and data["suite"] == "messages-isolation"
                and data["status"] == "passed")
        require(data["checkoutCommit"] == revision("rev-parse", "HEAD")
                and data["checkoutTree"] == revision("rev-parse", "HEAD^{tree}")
                and data["dirty"] is False and revision("status", "--porcelain") == "")
        for field, variable in (("workflowCommit", "GITHUB_SHA"),
                                ("workflowRun", "GITHUB_RUN_ID"),
                                ("workflowAttempt", "GITHUB_RUN_ATTEMPT")):
            value = data[field]
            require(type(value) is str and value == os.environ.get(variable, ""))
            if field == "workflowCommit":
                require(len(value) == 40 and all(char in "0123456789abcdef" for char in value))
            else:
                require(value.isascii() and value.isdigit() and int(value) > 0)
        require(type(data["browser"]) is str and 0 < len(data["browser"]) <= 128)
        failure = "messages_isolation_evidence_invalid:source_binding"
        required_sources = {
            "web/e2e/messages-isolation.spec.ts",
            "scripts/test_messages_isolation_browser_contract.py",
            "scripts/test_auth_e2e.py",
            "scripts/ci_inventory.json",
            "scripts/ci_selection.py",
            "scripts/test_ci_selection_contract.py",
            ".github/workflows/ci.yml",
            "docs/messages-authenticated-isolation-evidence.md",
            "web/playwright.config.ts",
            "scripts/ci_workflow_contract.py",
            "scripts/fixtures/messages_isolation_ci_delta.json",
            "scripts/test_directory_v4_controls_ci_contract.py",
            "scripts/test_directory_v4_runner_contract.py",
            "scripts/fixtures/messages_isolation_runner_delta.json",
            "internal/auth/message_http.go",
            "internal/messages/projection.go",
            "internal/messages/snapshot.go",
            "internal/messages/read.go",
            "internal/messages/bulk.go",
            "web/e2e/messages-bulk.spec.ts",
            "web/e2e/messages.spec.ts",
            "web/e2e/access.spec.ts",
            "internal/testmessagefixture/main.go",
            "internal/auth/access_users.go",
            "internal/auth/access_roles.go",
            "internal/auth/task_authorization.go",
            "internal/auth/task_http.go",
            "internal/messages/types.go",
            "docs/messages-other-user-read-history-evidence.md",
            "internal/auth/access_http.go",
            "internal/auth/service.go",
            "internal/messagecore/types.go",
            "web/e2e/navigation.ts",
            "web/e2e/safe-runtime-diagnostics.ts",
            "web/test-helpers/navigation.ts",
            "web/src/MessagesWorkspace.tsx",
            "web/src/TaskWorkspace.tsx",
            "web/src/message-api.ts",
            "web/src/message-read.tsx",
            "web/src/message-read-intent.ts",
            "web/package-lock.json",
            "internal/messages/schema.go",
            "internal/messages/bulk_schema.go",
            "internal/messagecore/result.go",
        }
        require(required_sources == set(data["sourceSHA256"]))
        for path, sha in data["sourceSHA256"].items():
            source = safe_path(path)
            require(not Path(path).is_absolute() and ".." not in Path(path).parts and source.is_file())
            require(sha == digest(source))
        failure = "messages_isolation_evidence_invalid:bundle_binding"
        require(data["artifactsSHA256"]["apiWorker"] == digest("bin/adtr")
                and data["artifactsSHA256"]["frontendEntry"] == digest("web/dist/index.html"))
        frontend_root = safe_path("web/dist")
        frontend_files = {path.relative_to(Path.cwd()).as_posix(): digest(path)
                          for path in frontend_root.rglob("*") if safe_path(path).is_file()}
        require(data["artifactsSHA256"]["frontendFiles"] == frontend_files
                and "web/dist/index.html" in frontend_files
                and any(path.startswith("web/dist/assets/") and path.endswith(".js") for path in frontend_files))
        def validate_png(content):
            offset, chunks, compressed = 8, [], bytearray()
            width = height = channels = 0
            while offset < len(content):
                require(offset + 12 <= len(content))
                size = int.from_bytes(content[offset:offset+4], "big")
                kind = content[offset+4:offset+8]
                require(kind in {b"IHDR", b"IDAT", b"IEND", b"sRGB", b"gAMA", b"cHRM", b"pHYs"})
                end = offset + 8 + size
                require(end + 4 <= len(content))
                payload = content[offset+8:end]
                require(zlib.crc32(kind + payload) & 0xffffffff == int.from_bytes(content[end:end+4], "big"))
                if not chunks:
                    require(kind == b"IHDR" and size == 13)
                    width, height = int.from_bytes(payload[:4], "big"), int.from_bytes(payload[4:8], "big")
                    require(0 < width <= 1440 and 0 < height <= 12000 and payload[8] == 8
                            and payload[9] in (2, 6) and payload[10:] == b"\0\0\0")
                    channels = 3 if payload[9] == 2 else 4
                else:
                    require(kind != b"IHDR")
                if kind == b"IDAT":
                    compressed.extend(payload)
                if kind == b"IEND":
                    require(size == 0 and end + 4 == len(content))
                chunks.append(kind)
                offset = end + 4
            require(chunks[-1] == b"IEND" and b"IDAT" in chunks and chunks.count(b"IEND") == 1)
            decoder = zlib.decompressobj()
            expected = (width * channels + 1) * height
            pixels = decoder.decompress(bytes(compressed), expected + 1)
            require(len(pixels) == expected and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail)
            require(all(pixels[row * (width * channels + 1)] <= 4 for row in range(height)))
        failure = "messages_isolation_evidence_invalid:png_evidence"
        screenshots = data["screenshots"]
        require(len(screenshots) == 4)
        observed = set()
        for shot in screenshots:
            filename = shot["file"]
            require(Path(filename).name == filename and filename.startswith("messages-soc-isolation-")
                    and filename.endswith(".png") and shot["proofEmpty"] is True
                    and shot["fullPage"] is True)
            path = safe_path(manifest.parent / filename)
            require(not path.is_symlink() and path.resolve().is_relative_to(root))
            content = path.read_bytes()
            require(content[:8] == b"\x89PNG\r\n\x1a\n" and 24 < len(content) <= 20_000_000)
            validate_png(content)
            viewport = shot["viewport"]
            require(type(viewport["width"]) is int and type(viewport["height"]) is int)
            require(int.from_bytes(content[16:20], "big") == viewport["width"]
                    and viewport["height"] <= int.from_bytes(content[20:24], "big") <= 12000)
            require(shot["sha256"] == hashlib.sha256(content).hexdigest())
            observed.add((shot["identity"], viewport["width"], viewport["height"]))
        require(observed == {(identity, width, height) for identity in ("A", "B")
                             for width, height in ((1440, 900), (390, 844))})
        failure = "messages_isolation_evidence_invalid:payload_schema"
        identities = data["identities"]
        require(len(identities) == 2 and {x["label"] for x in identities} == {"A", "B"}
                and len({x["userId"] for x in identities}) == 2)
        tasks = set()
        for identity in identities:
            require(identity["sensitiveActions"] == {"A": 8, "B": 6}[identity["label"]]
                    and 5 <= identity["snapshotAdmissions"] <= 7)
            require(len(identity["taskIds"]) == 3 and len(set(identity["taskIds"])) == 3
                    and bool(identity["provenance"]) and bool(identity["baseline"]))
            tasks.update(identity["taskIds"])
        require(len(tasks) == 6)
        stages = data["stages"]
        require(bool(stages))
        for stage in stages:
            require((stage["status"], stage["body"].get("error")) in
                    {(404, "not_found"), (400, "target_not_in_result")})
            require(set(stage["body"]) == {"error"} and stage["before"] == stage["after"])
        bulks = data["positiveBulks"]
        require(len(bulks) == 4 and {(x["actor"], x["selection"]) for x in bulks}
                == {(actor, selection) for actor in ("A", "B") for selection in ("selected", "all")})
        for bulk in bulks:
            require(bulk["operation"]["state"] == "completed"
                    and bool(bulk["operation"]["receipt"])
                    and bulk["protectedBefore"] == bulk["protectedAfter"])
        budget = data["phaseBudget"]
        require(budget["limitMs"] == 60000 and 0 <= budget["snapshotElapsedMs"] <= 60000
                and budget["totpWaits"] == 8)
        def instant(value):
            require(type(value) is str and 20 <= len(value) <= 40)
            normalized = value[:-1] + "+00:00" if value.endswith("Z") else value
            require(normalized.endswith("+00:00"))
            seconds, separator, fraction = normalized[:-6].partition(".")
            require(len(seconds) == 19 and seconds[10] == "T"
                    and (not separator or (fraction.isascii() and fraction.isdigit() and 1 <= len(fraction) <= 9)))
            parsed = datetime.fromisoformat(seconds + "+00:00")
            # Preserve up to nanoseconds instead of silently truncating to
            # Python datetime's microsecond resolution.
            return parsed, fraction.ljust(9, "0")
        def keys(value, expected):
            require(type(value) is dict and set(value) == set(expected.split()))
        keys(data, "formatVersion suite status checkoutCommit checkoutTree dirty workflowCommit workflowRun workflowAttempt browser sourceSHA256 artifactsSHA256 screenshots identities stages positiveBulks phaseBudget limitations")
        keys(data["artifactsSHA256"], "apiWorker frontendEntry frontendFiles")
        keys(data["phaseBudget"], "snapshotElapsedMs limitMs totpWaits setupSpent")
        require(all(type(value) is int for value in data["phaseBudget"].values())
                and 0 <= data["phaseBudget"]["setupSpent"] <= 75000)
        require(data["limitations"] == ["synthetic tasks-writer role, not reader-only",
                "API pagination, not two full UI pages", "completed-operation foreign cancellation only",
                "screenshots require independent pixel review"])
        for shot in screenshots:
            keys(shot, "file viewport fullPage identity proofEmpty sha256")
            keys(shot["viewport"], "width height")
        failure = "messages_isolation_evidence_invalid:artifact_files"
        allowed_files = {manifest.resolve()} | {(manifest.parent / shot["file"]).resolve() for shot in screenshots}
        actual_files = {safe_path(path).resolve() for path in Path("web/test-results").rglob("messages-soc-*")}
        require(actual_files == allowed_files and not manifest.is_symlink())
        failure = "messages_isolation_evidence_invalid:ledger_schema"
        row_keys = {
            "identity": "id tenant_id authorization_version",
            "messages": "message_id tenant_id recipient_user_id source_task_id source_event_id source_result_version message_type sub_type title description occurred_at ingested_at",
            "reads": "tenant_id user_id message_id first_read_at",
            "single": "tenant_id user_id operation_id result_id message_id first_read_at committed_at newly_read",
            "bulk": "tenant_id user_id operation_id queue_id result_id selection authorization_version accepted_at updated_at state target processed newly_read already_read",
            "targets": "tenant_id user_id operation_id ordinal message_id",
            "receipts": "tenant_id user_id operation_id ordinal first_read_at committed_at newly_read",
        }
        by_label = {identity["label"]: identity for identity in identities}
        message_sets = {}
        def state(value, owner):
            keys(value, "identity messages reads single bulk targets receipts")
            for table, columns in row_keys.items():
                require(type(value[table]) is list)
                for row in value[table]:
                    keys(row, columns)
                    integer_columns = {"id", "user_id", "recipient_user_id", "source_event_id", "source_result_version",
                                       "authorization_version", "queue_id", "ordinal", "target", "processed", "newly_read", "already_read"}
                    for column, cell in row.items():
                        if column == "newly_read" and table in ("single", "receipts"):
                            require(type(cell) is bool)
                        elif column in integer_columns:
                            require(type(cell) is int and 0 <= cell <= 9_007_199_254_740_991)
                        else:
                            require(type(cell) is str and 0 < len(cell) <= 512)
                    require(row["tenant_id"] == owner["tenantId"])
                    user_column = "id" if table == "identity" else "recipient_user_id" if table == "messages" else "user_id"
                    require(type(row[user_column]) is int and row[user_column] == owner["userId"])
                    for column in ("first_read_at", "committed_at", "accepted_at", "updated_at", "occurred_at", "ingested_at"):
                        if column in row:
                            instant(row[column])
                    for column in ("message_id", "operation_id", "result_id"):
                        if column in row:
                            require(type(row[column]) is str and 0 < len(row[column]) <= 128
                                    and row[column].isascii() and all(char.isalnum() or char in "_-" for char in row[column]))
            require(value["identity"] == [{"id": owner["userId"], "tenant_id": owner["tenantId"],
                                           "authorization_version": owner["authorizationVersion"]}])
            messages = value["messages"]
            require(len(messages) == 3 and {r["source_task_id"] for r in messages} == set(owner["taskIds"]))
            ids = {r["message_id"] for r in messages}
            require(len(ids) == 3)
            for row in messages:
                require(type(row["source_event_id"]) is int and row["source_event_id"] > 0
                        and row["message_id"] == "health-" + str(row["source_event_id"])
                        and row["message_type"] == "task_result" and row["sub_type"] == "succeeded"
                        and row["title"] == "Infrastructure health task succeeded"
                        and row["description"] == "The infrastructure health task completed successfully.")
            for table in ("reads", "single", "targets"):
                require(all(row["message_id"] in ids for row in value[table]))
            reads = {row["message_id"]: row for row in value["reads"]}
            require(len(reads) == len(value["reads"]) and 1 <= len(reads) <= 3)
            require(len(value["single"]) == 1)
            single = value["single"][0]
            require(single["newly_read"] is True and single["message_id"] in reads
                    and single["first_read_at"] == reads[single["message_id"]]["first_read_at"])
            operations = {row["operation_id"]: row for row in value["bulk"]}
            require(len(operations) == len(value["bulk"]) and len(operations) <= 2)
            require(all(row["operation_id"] in operations for table in ("targets", "receipts") for row in value[table]))
            for operation_id, operation in operations.items():
                require(operation["authorization_version"] == owner["authorizationVersion"]
                        and operation["state"] == "completed" and operation["selection"] in ("selected", "all"))
                targets = [row for row in value["targets"] if row["operation_id"] == operation_id]
                receipts = [row for row in value["receipts"] if row["operation_id"] == operation_id]
                expected_target = 1 if operation["selection"] == "selected" else 3
                require(operation["target"] == operation["processed"] == expected_target
                        and operation["newly_read"] == 1 and operation["already_read"] == expected_target - 1)
                require(len(targets) == len(receipts) == expected_target
                        and len({row["ordinal"] for row in targets}) == expected_target
                        and {row["ordinal"] for row in targets} == {row["ordinal"] for row in receipts}
                        and len({row["message_id"] for row in targets}) == expected_target)
                require(sum(row["newly_read"] is True for row in receipts) == 1
                        and all(type(row["newly_read"]) is bool for row in receipts))
                for receipt in receipts:
                    target = next(row for row in targets if row["ordinal"] == receipt["ordinal"])
                    require(target["message_id"] in reads
                            and receipt["first_read_at"] == reads[target["message_id"]]["first_read_at"])
            if "baseline" in owner and value is not owner["baseline"]:
                require(value["identity"] == owner["baseline"]["identity"]
                        and messages == owner["baseline"]["messages"]
                        and value["single"] == owner["baseline"]["single"])
        failure = "messages_isolation_evidence_invalid:provenance"
        for identity in identities:
            keys(identity, "label userId tenantId authorizationVersion sensitiveActions snapshotAdmissions taskIds provenance baseline")
            require(type(identity["userId"]) is int and identity["userId"] > 0
                    and type(identity["authorizationVersion"]) is int and identity["authorizationVersion"] >= 1)
            require(type(identity["sensitiveActions"]) is int and type(identity["snapshotAdmissions"]) is int)
            require(type(identity["tenantId"]) is str and 0 < len(identity["tenantId"]) <= 128
                    and identity["tenantId"].isascii())
            for task_id in identity["taskIds"]:
                require(str(uuid.UUID(task_id)) == task_id)
            baseline = identity["baseline"]
            state(baseline, identity)
            require(len(baseline["reads"]) == 1 and not baseline["bulk"] and not baseline["targets"] and not baseline["receipts"])
            provenance = identity["provenance"]
            require(len(provenance) == 3 and {p["taskUUID"] for p in provenance} == set(identity["taskIds"]))
            require(len({p["eventId"] for p in provenance}) == len({p["outboxId"] for p in provenance}) == 3)
            for proof in provenance:
                keys(proof, "messageId taskUUID title description occurredAt eventId outboxId sourceResultVersion eventResultVersion kind action state taskDomain eventDomain tenantEqual actorEqual taskEqual eventEqual messageTenant taskTenant eventTenant userTenant recipientUserId taskActorId eventTaskId outboxTaskId outboxEventId")
                numeric_fields = {"eventId", "outboxId", "sourceResultVersion", "eventResultVersion", "recipientUserId", "taskActorId", "outboxEventId"}
                boolean_fields = {"tenantEqual", "actorEqual", "taskEqual", "eventEqual"}
                for field, value in proof.items():
                    if field in numeric_fields:
                        require(type(value) is int and 0 < value <= 9_007_199_254_740_991)
                    elif field in boolean_fields:
                        require(type(value) is bool)
                    else:
                        require(type(value) is str and 0 < len(value) <= 512)
                instant(proof["occurredAt"])
                require(all(proof[field] is True for field in ("tenantEqual", "actorEqual", "taskEqual", "eventEqual")))
                require(proof["kind"] == "infrastructure.health" and proof["action"] == "finished"
                        and proof["state"] == "succeeded" and proof["taskDomain"] == proof["eventDomain"] == "platform"
                        and type(proof["sourceResultVersion"]) is int and proof["sourceResultVersion"] > 0
                        and proof["sourceResultVersion"] == proof["eventResultVersion"])
                require(proof["messageTenant"] == proof["taskTenant"] == proof["eventTenant"] == proof["userTenant"] == identity["tenantId"]
                        and proof["recipientUserId"] == proof["taskActorId"] == identity["userId"]
                        and proof["taskUUID"] == proof["eventTaskId"] == proof["outboxTaskId"]
                        and proof["eventId"] == proof["outboxEventId"])
                matches = [row for row in baseline["messages"] if row["message_id"] == proof["messageId"]]
                require(len(matches) == 1)
                row = matches[0]
                require(row["source_task_id"] == proof["taskUUID"] and row["source_event_id"] == proof["eventId"]
                        and row["source_result_version"] == proof["sourceResultVersion"]
                        and row["title"] == proof["title"] and row["description"] == proof["description"]
                        and row["occurred_at"] == proof["occurredAt"])
            require({p["messageId"] for p in provenance} == {row["message_id"] for row in baseline["messages"]})
            message_sets[identity["label"]] = {p["messageId"] for p in provenance}
        require(not message_sets["A"] & message_sets["B"] and by_label["A"]["tenantId"] == by_label["B"]["tenantId"])
        failure = "messages_isolation_evidence_invalid:negative_matrix"
        expected_suffixes = {"foreign-page", "foreign-locate", "foreign-single-receipt", "foreign-read",
            "foreign-single-replay", "foreign-bulk-selected", "foreign-bulk-all", "own-result-foreign-single",
            "own-result-foreign-selected-false", "own-result-foreign-selected-true"}
        expected_suffixes |= {f"{name}-{mode}" for name in ("foreign-bulk-receipt", "foreign-bulk-replay", "foreign-completed-cancel")
                              for mode in ("selected", "all")}
        require(len(stages) == 32 and {stage["label"] for stage in stages}
                == {f"{label}-{suffix}" for label in ("A", "B") for suffix in expected_suffixes})
        allowed_states = {label: [identity["baseline"]] for label, identity in by_label.items()}
        by_bulk = {(bulk["actor"], bulk["selection"]): bulk for bulk in bulks}
        failure = "messages_isolation_evidence_invalid:bulk_ledger"
        for label in ("A", "B"):
            owner = by_label[label]
            other = by_label["B" if label == "A" else "A"]
            previous = owner["baseline"]
            for mode in ("selected", "all"):
                bulk = by_bulk[label, mode]
                keys(bulk, "actor selection operation before after protectedBefore protectedAfter")
                state(bulk["before"], owner)
                state(bulk["after"], owner)
                state(bulk["protectedBefore"], other)
                state(bulk["protectedAfter"], other)
                require(bulk["before"] == previous)
                expected_protected = other["baseline"] if label == "A" else by_bulk["A", "all"]["after"]
                require(bulk["protectedBefore"] == bulk["protectedAfter"] == expected_protected)
                operation = bulk["operation"]
                keys(operation, "receipt progress state updatedAt")
                keys(operation["receipt"], "acceptedAt operationId resultId selection target")
                keys(operation["progress"], "target processed newlyRead alreadyRead failed remaining")
                require(all(type(value) is int for value in operation["progress"].values()))
                count = 1 if mode == "selected" else 3
                require(operation["progress"] == dict(target=count, processed=count, newlyRead=1, alreadyRead=count-1, failed=0, remaining=0))
                receipt = operation["receipt"]
                require(type(receipt["target"]) is int and receipt["selection"] == mode and receipt["target"] == count)
                before, after = bulk["before"], bulk["after"]
                require(len(after["reads"]) == len(before["reads"]) + 1
                        and len(after["reads"]) == (2 if mode == "selected" else 3)
                        and all(row in after["reads"] for row in before["reads"]))
                for table, increment in (("bulk", 1), ("targets", count), ("receipts", count)):
                    require(len(after[table]) == len(before[table]) + increment and all(row in after[table] for row in before[table]))
                added = [row for row in after["bulk"] if row not in before["bulk"]]
                require(len(added) == 1)
                persisted = added[0]
                require(persisted["operation_id"] == receipt["operationId"] and persisted["result_id"] == receipt["resultId"]
                        and persisted["selection"] == mode and instant(persisted["accepted_at"]) == instant(receipt["acceptedAt"])
                        and instant(persisted["updated_at"]) == instant(operation["updatedAt"]))
                if mode == "all":
                    require(receipt["resultId"] == by_bulk[label, "selected"]["operation"]["receipt"]["resultId"])
                new_targets = [row["message_id"] for row in after["targets"] if row["operation_id"] == receipt["operationId"]]
                require(set(new_targets) <= message_sets[label])
                if mode == "selected":
                    require(not set(new_targets) & {row["message_id"] for row in before["reads"]})
                else:
                    require(set(new_targets) == message_sets[label])
                previous = after
                allowed_states[label].append(after)
        failure = "messages_isolation_evidence_invalid:negative_ledger"
        for stage in stages:
            keys(stage, "label status body before after")
            caller, suffix = stage["label"].split("-", 1)
            expected_status = 400 if suffix.startswith("own-result-") else 404
            require(type(stage["status"]) is int and stage["status"] == expected_status and stage["body"] == {"error": "target_not_in_result" if expected_status == 400 else "not_found"})
            require(len(stage["before"]) == len(stage["after"]) == 2)
            for index, label in enumerate(("A", "B")):
                state(stage["before"][index], by_label[label])
                state(stage["after"][index], by_label[label])
                require(stage["before"][index] in allowed_states[label])
            if suffix.startswith(("foreign-bulk-receipt-", "foreign-bulk-replay-", "foreign-completed-cancel-")):
                owner = "B" if caller == "A" else "A"
                mode = suffix.rsplit("-", 1)[1]
                bulk = by_bulk[owner, mode]
                expected_states = [bulk["after"] if label == owner else bulk["protectedAfter"] for label in ("A", "B")]
            else:
                expected_states = [by_label[label]["baseline"] for label in ("A", "B")]
            require(stage["before"] == stage["after"] == expected_states)
    except Exception:
        raise RuntimeError(failure) from None


def validate_asset_refresh_evidence(suite):
    if suite in EMPTY_TRANSITION_SUITES:
        subprocess.run([sys.executable, "scripts/validate_asset_empty_refresh.py",
                        "--kind", suite.split("-")[0],
                        "--evidence-root", "web/test-results", "--source-root", "."],
                       check=True, timeout=30)


def cleanup_uac_run(worker, server, diagnostics, logs, ldap_fixture,
                    creation_attempted, name, owner, nonce, diagnostic_dir):
    """Collect actual owned teardown facts; no failure can authorize evidence."""
    receipt = dict(format=1, profile="mixed-33-v1", nonce=nonce,
                   workerExitCode=None, apiExitCode=None, ldapExitCode=None,
                   ldapContainerRemoved=False, ldapNetworkRemoved=False,
                   ldapPrivateDirectoriesRemoved=False, databaseContainerRemoved=False,
                   logsClosed=False, safeDiagnosticsPassed=False,
                   allOwnedCleanupSucceeded=False)
    failed = False
    first_stage = None
    try:
        diagnostics.snapshot(server, worker)
    except Exception:
        failed = True
        first_stage = first_stage or "cleanup_snapshot"
    for label, process in (("worker", worker), ("api", server)):
        try:
            reaped, code, forced, problems = cleanup_v4_process(
                process, diagnostics.report["processes"][label])
            if process is None or not reaped or forced or problems or type(code) is not int or code != 0:
                failed = True
                first_stage = first_stage or ("cleanup_worker" if label == "worker" else "cleanup_api")
            else:
                receipt[label + "ExitCode"] = code
        except Exception:
            failed = True
            first_stage = first_stage or ("cleanup_worker" if label == "worker" else "cleanup_api")
    ldap_stage = "cleanup_ldap_close"
    try:
        if ldap_fixture is None:
            raise RuntimeError("uac_evidence_invalid:cleanup")
        ldap_fixture.close()
        ldap_stage = "cleanup_ldap_retained"
        retained = ldap_fixture.retained_uac_evidence
        if (type(retained) is not tuple or len(retained) != 3
                or type(retained[0]) is not bytes or type(retained[1]) is not bytes
                or type(retained[2]) is not int or retained[2] != 0):
            raise RuntimeError("uac_evidence_invalid:cleanup")
        # F publishes this tuple only after successful stop/inspect and all its
        # owned container/network/private-directory cleanup; None is not proof.
        receipt.update(ldapExitCode=0, ldapContainerRemoved=True,
                       ldapNetworkRemoved=True, ldapPrivateDirectoriesRemoved=True)
    except Exception as error:
        failed = True
        if first_stage is None:
            first_stage = ldap_stage
            if ldap_stage == "cleanup_ldap_close":
                # F owns its exact-type, fixed-message, private stage tag.
                # Even a broken diagnostic accessor cannot interrupt cleanup.
                try:
                    owned_stage = uac_ldap_failure_stage(error)
                except Exception:
                    owned_stage = None
                if owned_stage is not None:
                    first_stage = owned_stage
    diagnostic_ok = False
    try:
        panicked = http_handler_panicked(logs)
        diagnostics.report["http_panic"] = panicked
        diagnostic_ok = panicked is False
        if not diagnostic_ok:
            failed = True
            first_stage = first_stage or "cleanup_log_scan"
    except Exception:
        failed = True
        first_stage = first_stage or "cleanup_log_scan"
    try:
        logs.close()
        receipt["logsClosed"] = logs.closed is True
        if not receipt["logsClosed"]:
            failed = True
            first_stage = first_stage or "cleanup_log_close"
    except Exception:
        failed = True
        first_stage = first_stage or "cleanup_log_close"
    try:
        if not creation_attempted:
            raise RuntimeError("uac_evidence_invalid:cleanup")
        remove_owned_container(name, owner)
        receipt["databaseContainerRemoved"] = True
    except Exception:
        failed = True
        first_stage = first_stage or "cleanup_database_remove"
    diagnostic_stage = "cleanup_diagnostic_phase"
    try:
        diagnostics.phase("after-cleanup")
        diagnostic_stage = "cleanup_diagnostic_write"
        diagnostics.write(diagnostic_dir / "runtime.json")
        diagnostic_stage = "cleanup_diagnostic_result"
        receipt["safeDiagnosticsPassed"] = diagnostic_ok and not diagnostics.report["collection_errors"]
        if not receipt["safeDiagnosticsPassed"]:
            failed = True
            first_stage = first_stage or diagnostic_stage
    except Exception:
        failed = True
        first_stage = first_stage or diagnostic_stage
    if failed:
        raise uac_diagnostic_error("cleanup", first_stage, kind="runtime") from None
    receipt["allOwnedCleanupSucceeded"] = True
    return receipt


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", choices=["user-asset-export-v3-uac-mixed33", "tasks-health-recovery", "directory-v4-controls", "directory-v4-cancel", "directory-v4-readers", "group-asset-export-v3-running-cancel", "computer-asset-export-v3-running-cancel", "user-asset-export-v3-running-cancel", "computer-asset-export-v3", "computer-asset-export-v3-failure", "computer-asset-export-v3-cancel", "group-asset-export-v3", "group-asset-export-v3-failure", "group-asset-export-v3-cancel", "user-asset-export-v3-failure", "user-asset-export-v3-cancel", "user-asset-export-v3", "auth", "access", "resource", "tasks", "messages", "messages-bulk", "messages-isolation", "audit", "system", "maintenance", "domains", "operations", "profile", "credential-use", "account-references", "operational-logs", "session-invalidation", "account-reference-barrier", "directory", "directory-controls", "directory-readers", "directory-v2", "directory-v2-controls", "directory-v2-readers", "directory-v3-backend", "directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", "user-assets-v3", "computer-assets-v3", "computer-assets-v3-readers", "group-assets-v3", "group-assets-v3-readers", "directory-v3", "directory-v3-controls", "directory-v3-readers", "user-assets-v2", "user-assets-v2-readers"], default="auth")
    parser.add_argument("--expired", action="store_true")
    args = parser.parse_args()
    if args.suite == "tasks-health-recovery" and args.expired:
        raise RuntimeError("health_expired_mode_unsupported")
    health_run_deadline = health_deadline(os.environ) if args.suite == "tasks-health-recovery" else None
    # Only the three explicit running-cancel suites install the CI controller.
    # The default make build remains untagged. Build before source provenance.
    if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:
        subprocess.run(["make", "build-ci-running-cancel"], check=True, timeout=180)
    # Check display support before creating a disposable database or fixtures.
    command = browser_command(args.suite)
    name = "adtr-auth-e2e-" + uuid.uuid4().hex[:12]
    password = secrets.token_hex(24)
    env = dict(health_environment(os.environ) if args.suite == "tasks-health-recovery" else os.environ, POSTGRES_PASSWORD=password)
    health_build_record = health_build(health_environment(env), health_run_deadline) if args.suite == "tasks-health-recovery" else None
    if health_build_record is not None:
        env["ADTR_E2E_HEALTH_RUN_NONCE"] = health_build_record["identity"]["nonce"]
    health_cleanup = None
    uac_nonce = secrets.token_hex(16) if args.suite == UAC_SUITE else None
    uac_provenance = None
    uac_cleanup = None
    # Ignore inherited controller handles and exact-suite selectors.
    for key in (URL_ENV, TOKEN_ENV, "ADTR_E2E_V4_CONTROLS_SUITE"):
        env.pop(key, None)
    if args.suite in V4_CONTROLS_BUDGETS:
        env["ADTR_E2E_V4_CONTROLS_SUITE"] = args.suite
    creation_attempted = False
    owner = uuid.uuid4().hex
    server = None
    worker = None
    v4_worker_controller = None
    owned_ci_worker = None
    export_worker_guard = None
    running_cancel_guard = None
    ldap_fixture = None
    account_barrier = None
    message_fixture = None
    diagnostics = RuntimeDiagnostics()
    diagnostic_dir = Path(".e2e-diagnostics")
    diagnostic_dir.mkdir(parents=True, exist_ok=True)
    for filename in ["runtime.json", "browser.json"]:
        (diagnostic_dir / filename).unlink(missing_ok=True)
    env["ADTR_E2E_SAFE_DIAGNOSTICS"] = str((diagnostic_dir / "browser.json").resolve())
    diagnostics.phase("setup")
    if args.suite == UAC_SUITE:
        if args.expired:
            raise RuntimeError("uac_evidence_invalid:configuration")
        configure_uac(env, nonce=uac_nonce)
        uac_provenance = capture_uac_provenance(repo=Path.cwd())
    if args.suite == "user-asset-export-v3":
        configure_user_export(env)
        capture_user_export()
    if args.suite in EXPORT_TERMINAL_SUITES:
        configure_terminal_export(env, args.suite)
        capture_terminal_export(args.suite)
    if args.suite == "group-asset-export-v3":
        configure_group_export(env)
        capture_group_export()
    if args.suite in GROUP_EXPORT_TERMINAL_SUITES:
        configure_group_terminal(env, args.suite)
        capture_group_terminal(args.suite)
    if args.suite == "computer-asset-export-v3":
        configure_computer_export(env)
        capture_computer_export()
    if args.suite in COMPUTER_EXPORT_TERMINAL_SUITES:
        configure_computer_terminal(env, args.suite)
        capture_computer_terminal(args.suite)
    if args.suite == RUNNING_CANCEL_SUITE:
        configure_running_cancel(env)
        capture_running_cancel()
    if args.suite == GROUP_RUNNING_CANCEL_SUITE:
        configure_group_running_cancel(env)
        capture_group_running_cancel()
    if args.suite == COMPUTER_RUNNING_CANCEL_SUITE:
        configure_computer_running_cancel(env)
        capture_computer_running_cancel()
    logs = tempfile.TemporaryFile(mode="w+")
    try:
        creation_attempted = True
        try:
            subprocess.run(["docker", "run", "--detach", "--rm", "--name", name,
                            "--label", f"{OWNER_LABEL}={owner}",
                            "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=adtr_e2e",
                            "-p", "127.0.0.1::5432", *(health_postgres_args(env, health_run_deadline) if args.suite == "tasks-health-recovery" else postgres_image_args(IMAGE))], env=env, check=True, capture_output=True, timeout=health_timeout(60, health_run_deadline))
        except subprocess.CalledProcessError as error:
            report_container_startup_failure(error, password)
            raise
        for _ in range(60):
            if subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "adtr_e2e"], capture_output=True, timeout=health_timeout(5, health_run_deadline)).returncode == 0:
                break
            time.sleep(0.5)
        else:
            raise RuntimeError("isolated authentication database did not become ready")
        db_address = subprocess.check_output(["docker", "port", name, "5432/tcp"], text=True, timeout=health_timeout(10, health_run_deadline)).strip()
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        env.update(ADTR_DATABASE_URL=f"postgres://postgres:{password}@{db_address}/adtr_e2e?sslmode=disable",
                   ADTR_AUTH_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
                   ADTR_DEVELOPMENT="true", ADTR_DIRECTORY_READ_ENABLED="false", ADTR_DIRECTORY_READ_V2_ENABLED="false", ADTR_ORIGIN=base, ADTR_LISTEN_ADDR=f"127.0.0.1:{port}",
                   ADTR_WEB_DIR=str(Path("web/dist").resolve()), ADTR_BOOTSTRAP_USERNAME="e2e-admin",
                   ADTR_BOOTSTRAP_PASSWORD=secrets.token_urlsafe(24), ADTR_E2E_BASE_URL=base)
        env["ADTR_E2E_USERNAME"] = env["ADTR_BOOTSTRAP_USERNAME"]
        env["ADTR_E2E_PASSWORD"] = env["ADTR_BOOTSTRAP_PASSWORD"]
        configure_directory_credentials(env, args.suite)
        if not Path("web/dist/index.html").is_file():
            raise RuntimeError("build the real frontend before running acceptance")
        for mode in ["migrate", "bootstrap"]:
            subprocess.run(["./bin/adtr", "-mode", mode], env=env, check=True, timeout=health_timeout(60, health_run_deadline))
        if args.suite == "account-references":
            # Isolated synthetic role: real function permission with no resource
            # groups. The browser creates its user and authenticates normally.
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "BEGIN; INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('default','source_no_scope_role_001','Source no domain scope'); INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('default','source_no_scope_role_001','domains',true,true),('default','source_no_scope_role_001','operation_accounts',true,false); COMMIT;"],
                           check=True, capture_output=True, timeout=30)
        if args.suite == "resource":
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "INSERT INTO adtr.resource_domains(tenant_id,id,name,active) VALUES('default','synthetic-domain-a','Synthetic Domain A',true)"],
                           check=True, capture_output=True, timeout=30)
        if args.suite in {"messages", "messages-isolation", "tasks-health-recovery"}:
            # Read-only provenance checks wait for the real health projection; no message fixtures.
            env["ADTR_E2E_DB_CONTAINER"] = name
        if args.suite == "messages-bulk":
            message_fixture = tempfile.TemporaryDirectory(prefix="adtr-message-fixture-")
            fixture_binary = str(Path(message_fixture.name) / "message-fixture")
            subprocess.run(["go", "build", "-tags=integration", "-o", fixture_binary,
                            "./internal/testmessagefixture"], check=True, capture_output=True, timeout=120)
            env.update(ADTR_E2E_DB_CONTAINER=name, ADTR_E2E_MESSAGE_FIXTURE="owned-isolated-health",
                       ADTR_E2E_MESSAGE_FIXTURE_BINARY=fixture_binary)
        if args.expired:
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "UPDATE adtr.users SET must_change=false,password_updated_at=now()-interval '91 days' WHERE username='e2e-admin'"],
                           check=True, capture_output=True, timeout=30)
            env["ADTR_E2E_EXPIRED"] = "1"
        if args.suite in {"domains", "operations", "credential-use", "account-references", "account-reference-barrier"} | DIRECTORY_SUITES:
            subprocess.run(["docker", "exec", name, "psql", "-U", "postgres", "-d", "adtr_e2e", "-v", "ON_ERROR_STOP=1", "-c",
                            "INSERT INTO adtr.resource_tenant_config(tenant_id,max_ad_count,expire_time,uid,name) VALUES('default',2,extract(epoch FROM clock_timestamp())::bigint+86400,'synthetic-e2e','Synthetic tenant')"],
                           check=True, capture_output=True, timeout=30)
            if args.suite in {"domains", "account-references", "account-reference-barrier"} | DIRECTORY_SUITES:
                ldap_fixture = LDAPFixture(control_enabled=args.suite == "account-reference-barrier",
                                           empty_transition=args.suite in EMPTY_TRANSITION_SUITES or args.suite == "group-members-v4-refresh",
                                           directory_enabled=args.suite in DIRECTORY_SUITES,
                                           directory_empty=args.suite in EMPTY_DIRECTORY_SUITES or args.suite == "group-members-v4-empty",
                                           directory_slow=args.suite in SLOW_DIRECTORY_SUITES,
                                           directory_v2=args.suite in DIRECTORY_V2_SUITES,
                                           directory_v3=args.suite in DIRECTORY_V3_SUITES,
                                           directory_v4=args.suite in DIRECTORY_V4_SUITES,
                                           group_members_v4_pagination=args.suite == "group-members-v4-pagination",
                                           group_members_v4_refresh=args.suite == "group-members-v4-refresh",
                                           computer_assets_v3=args.suite in COMPUTER_ASSETS_V3_SUITES | COMPUTER_EXPORT_SUITES | {COMPUTER_RUNNING_CANCEL_SUITE},
                                           group_assets_v3=args.suite in GROUP_ASSETS_V3_SUITES | GROUP_EXPORT_SUITES | {GROUP_RUNNING_CANCEL_SUITE},
                                           user_assets_v2=args.suite in USER_ASSETS_V2_SUITES,
                                           user_export_failure_v3=args.suite == "user-asset-export-v3-failure",
                                           group_export_failure_v3=args.suite == "group-asset-export-v3-failure",
                                           computer_export_failure_v3=args.suite == "computer-asset-export-v3-failure",
                                           user_uac_v3_profile="mixed-33-v1" if args.suite == UAC_SUITE else "")
                ldap_fixture.start(env)
                if args.suite in DIRECTORY_SUITES:
                    env["ADTR_DIRECTORY_READ_ENABLED"] = "true"
                if args.suite in DIRECTORY_V4_SUITES:
                    env["ADTR_DIRECTORY_READ_V4_ENABLED"] = "true"
                    env["ADTR_E2E_DB_CONTAINER"] = name
                if args.suite in DIRECTORY_V3_SUITES:
                    env["ADTR_DIRECTORY_READ_V3_ENABLED"] = "true"
                    env["ADTR_E2E_DB_CONTAINER"] = name
                if args.suite in DIRECTORY_V2_SUITES:
                    env["ADTR_DIRECTORY_READ_V2_ENABLED"] = "true"
                if args.suite in EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES:
                    env["ADTR_E2E_DB_CONTAINER"] = name
                if args.suite in {"directory-readers", "directory-v2-readers", "directory-v3-readers"}:
                    env["ADTR_E2E_LDAP_DIRECTORY_SLOW"] = "false"
                if args.suite == "account-reference-barrier":
                    account_barrier = AccountBarrierFixture()
                    account_barrier.start(env, ldap_fixture.control_path)
            else:
                env.update(ADTR_DOMAIN_KEY_ID="synthetic-operations", ADTR_DOMAIN_KEY=base64.b64encode(secrets.token_bytes(32)).decode(), ADTR_DOMAIN_PROBE_ENABLED="false")
                for key in ["ADTR_LDAP_CA_FILE", "ADTR_LDAP_EGRESS_POLICY_FILE"]:
                    env.pop(key, None)
        # Credentials are only needed by explicit bootstrap; never pass them to the server.
        runtime_env = {k: v for k, v in env.items() if not k.startswith("ADTR_BOOTSTRAP_") and not k.startswith("ADTR_E2E_") and k not in ENV_FDS}
        diagnostics.phase("api-start")
        health_timeout(1, health_run_deadline)
        server = subprocess.Popen(["./bin/adtr", "-mode", "api"], env=runtime_env, stdout=logs, stderr=logs)
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError("authentication API exited during startup")
            try:
                with urllib.request.urlopen(base + "/readyz", timeout=health_timeout(1, health_run_deadline)) as response:
                    if response.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError("authentication API failed readiness")
        diagnostics.phase("api-ready")
        if args.suite in {"tasks", "tasks-health-recovery", "messages", "messages-bulk", "messages-isolation", "audit", "system", "maintenance", "domains", "account-references", "operational-logs", "account-reference-barrier"} | DIRECTORY_SUITES:
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0))
                worker_port = listener.getsockname()[1]
            worker_env = dict(runtime_env, ADTR_LISTEN_ADDR=f"127.0.0.1:{worker_port}")
            if args.suite == "messages-bulk":
                # Identify only our worker connections for quiescent SIGSTOP tests.
                worker_env["ADTR_DATABASE_URL"] += "&application_name=adtr-message-bulk-worker"
            if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", "group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"} | KIND_RUNNING_CANCEL_SUITES:
                export_worker_nonce = uuid.uuid4().hex
                export_worker_app = "adtr-user-export-" + export_worker_nonce
                worker_env["ADTR_E2E_EXPORT_WORKER_NONCE"] = export_worker_nonce
                worker_env["ADTR_DATABASE_URL"] += "&application_name=" + export_worker_app
            # Execution authorization needs persisted actors, not browser secrets.
            for key in ["ADTR_AUTH_KEY", "ADTR_ORIGIN", "ADTR_WEB_DIR"]:
                worker_env.pop(key, None)
            if args.suite == "tasks-health-recovery":
                # API is ready, but no worker exists until the explicit browser start.
                binary = str(Path("bin/adtr").resolve(strict=True))
                launch = lambda binary=binary, worker_env=dict(worker_env), logs=logs: subprocess.Popen(
                    [binary, "-mode", "worker"], env=worker_env, stdout=logs, stderr=logs)
                v4_worker_controller = v4_delayed_worker(
                    launch, f"http://127.0.0.1:{worker_port}/readyz")
                env.update(v4_worker_controller.open())
            elif args.suite == "directory-v4-cancel":
                # Freeze actual binary, private env, logs and URL before admission.
                binary = str(Path("bin/adtr").resolve(strict=True))
                launch = lambda binary=binary, worker_env=dict(worker_env), logs=logs: subprocess.Popen(
                    [binary, "-mode", "worker"], env=worker_env, stdout=logs, stderr=logs)
                v4_worker_controller = v4_delayed_worker(
                    launch, f"http://127.0.0.1:{worker_port}/readyz")
                env.update(v4_worker_controller.open())
            else:
                diagnostics.phase("worker-start")
                if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:
                    owned_ci_worker = OwnedCIWorker.spawn(["./bin/adtr", "-mode", "worker"],
                        env=worker_env, stdout=logs, stderr=logs, binary=Path("bin/adtr").resolve())
                    worker = owned_ci_worker.process
                else:
                    worker = subprocess.Popen(["./bin/adtr", "-mode", "worker"], env=worker_env, stdout=logs, stderr=logs)
                if args.suite in {RUNNING_CANCEL_SUITE, "user-asset-export-v3-cancel", "group-asset-export-v3-cancel", "computer-asset-export-v3-cancel"} | KIND_RUNNING_CANCEL_SUITES:
                    export_worker_guard = OwnedExportWorkerGuard(worker, export_worker_nonce, Path("bin/adtr").resolve())
                    export_worker_guard.start()
                    env.update(ADTR_E2E_WORKER_PID=str(worker.pid), ADTR_E2E_HARNESS_PID=str(os.getpid()),
                               ADTR_E2E_EXPORT_WORKER_STARTTIME=export_worker_guard.starttime,
                               ADTR_E2E_EXPORT_WORKER_NONCE=export_worker_nonce,
                               ADTR_E2E_EXPORT_WORKER_BINARY=str(Path("bin/adtr").resolve()),
                               ADTR_E2E_EXPORT_WORKER_APPLICATION_NAME=export_worker_app,
                               ADTR_E2E_EXPORT_WORKER_READY_URL=f"http://127.0.0.1:{worker_port}/readyz")
                    if args.suite in {RUNNING_CANCEL_SUITE} | KIND_RUNNING_CANCEL_SUITES:
                        asset_kind = {GROUP_RUNNING_CANCEL_SUITE: "group", COMPUTER_RUNNING_CANCEL_SUITE: "computer"}.get(args.suite, "user")
                        running_cancel_guard = RunningExportCancelGuard(worker, export_worker_nonce, Path("bin/adtr").resolve(), name, export_worker_app, env["ADTR_DATABASE_URL"], asset_kind=asset_kind, controller=owned_ci_worker.controller)
                        export_worker_guard.resume_delegate = running_cancel_guard._resume_capture_owned
                        running_cancel_guard.start()
                        env.update(ADTR_E2E_RUNNING_CANCEL_GUARD_URL=running_cancel_guard.url,
                                   ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN=running_cancel_guard.token)
                for _ in range(100):
                    if worker.poll() is not None:
                        raise RuntimeError("task worker exited during startup")
                    try:
                        with urllib.request.urlopen(f"http://127.0.0.1:{worker_port}/readyz", timeout=1) as response:
                            if response.status == 200:
                                break
                    except (OSError, urllib.error.URLError):
                        pass
                    time.sleep(0.1)
                else:
                    raise RuntimeError("task worker failed readiness")
                diagnostics.phase("worker-ready")
        if args.suite in {"system", "operational-logs", "messages-bulk"}:
            env["ADTR_E2E_WORKER_PID"] = str(worker.pid)
            if args.suite == "messages-bulk":
                env["ADTR_E2E_HARNESS_PID"] = str(os.getpid())
        # Trace/screenshot settings do not disable Playwright's automatic failure
        # DOM snapshot. Suppress that separate artifact so proof fields cannot
        # enter an error-context prompt; explicit safe screenshots remain intact.
        env["PLAYWRIGHT_NO_COPY_PROMPT"] = "1"
        diagnostics.phase("browser-start")
        if args.suite == "tasks-health-recovery":
            exit_code = health_build_process(command, env, health_timeout(660, health_run_deadline), capture=False)
            if exit_code != 0:
                raise RuntimeError("health_browser_failed")
        else:
            subprocess.run(command, env=env, check=True, timeout=720 if args.suite == "messages-isolation" else V4_CONTROLS_BUDGETS[args.suite] if args.suite in V4_CONTROLS_BUDGETS else 930 if args.suite in KIND_RUNNING_CANCEL_SUITES else 930 if args.suite == RUNNING_CANCEL_SUITE else 930 if args.suite in COMPUTER_EXPORT_SUITES else 930 if args.suite in GROUP_EXPORT_SUITES else 930 if args.suite in EXPORT_TERMINAL_SUITES else 930 if args.suite in {"user-asset-export-v3", UAC_SUITE} else 660 if args.suite == "account-references" else 930 if args.suite in EMPTY_DIRECTORY_SUITES | USER_ASSETS_V2_SUITES | {"user-assets-v3"} | COMPUTER_ASSETS_V3_SUITES | GROUP_ASSETS_V3_SUITES else 660 if args.suite in {"directory", "directory-v2", "directory-v3-backend", "directory-v4-backend", "group-members-v4", "group-members-v4-empty", "group-members-v4-pagination", "group-members-v4-refresh", "directory-v3"} else 600 if args.suite == "audit" else 540 if args.suite in {"maintenance", "domains", "operations", "credential-use", "account-references", "operational-logs", "account-reference-barrier"} else 480 if args.suite == "system" else 420)
        if args.suite == "messages-isolation":
            validate_messages_isolation_evidence()
        if args.suite == "user-asset-export-v3":
            validate_user_export()
        if export_worker_guard is not None:
            export_worker_guard.check()
        if running_cancel_guard is not None:
            running_cancel_guard.check()
            if args.suite == GROUP_RUNNING_CANCEL_SUITE:
                validate_group_running_cancel()
            elif args.suite == COMPUTER_RUNNING_CANCEL_SUITE:
                validate_computer_running_cancel()
            else:
                validate_running_cancel()
        if args.suite in EXPORT_TERMINAL_SUITES:
            validate_terminal_export(args.suite)
        if args.suite == "group-asset-export-v3":
            validate_group_export()
        if args.suite in GROUP_EXPORT_TERMINAL_SUITES:
            validate_group_terminal(args.suite)
        if args.suite == "computer-asset-export-v3":
            validate_computer_export()
        if args.suite in COMPUTER_EXPORT_TERMINAL_SUITES:
            validate_computer_terminal(args.suite)
        validate_asset_refresh_evidence(args.suite)
        if args.suite == "group-members-v4":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        if args.suite == "group-members-v4-empty":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_empty_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        if args.suite == "group-members-v4-pagination":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_pagination_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        if args.suite == "group-members-v4-refresh":
            subprocess.run([sys.executable, "scripts/validate_group_members_v4_refresh_evidence.py",
                            "--source-root", "."], check=True, timeout=30)
        if v4_worker_controller is not None:
            state = v4_worker_controller.state()
            if state != {"state": "ready", "launchCount": 1, "failure": None}:
                raise RuntimeError("directory v4 worker did not reach single-launch readiness")
        diagnostics.phase("browser-pass")
        chain = "real browser -> API -> worker/PostgreSQL" if worker is not None or v4_worker_controller is not None else "real browser -> API -> PostgreSQL"
    finally:
        if args.suite == "tasks-health-recovery":
            primary_error = sys.exc_info()[0] is not None
            try:
                health_cleanup = cleanup_health_run(v4_worker_controller, worker, server,
                    diagnostics, logs, creation_attempted, name, owner)
                if health_cleanup["failures"] and not primary_error:
                    raise RuntimeError("health_owned_cleanup_failed")
            except Exception:
                if not primary_error:
                    raise
                print("health_owned_cleanup_failed", file=sys.stderr)
            finally:
                # Fixed keys/enums only. Reporting must never replace the primary
                # exception, and this diagnostic cannot authorize publication.
                try:
                    print(json.dumps(health_cleanup_summary(health_cleanup), sort_keys=True), file=sys.stderr)
                except Exception:
                    pass
                try:
                    diagnostics.phase("after-cleanup")
                    diagnostics.write(diagnostic_dir / "runtime.json")
                except Exception:
                    print("health_runtime_diagnostics_failed", file=sys.stderr)
        elif args.suite == UAC_SUITE:
            primary_error = sys.exc_info()[0] is not None
            try:
                uac_cleanup = cleanup_uac_run(worker, server, diagnostics, logs,
                    ldap_fixture, creation_attempted, name, owner, uac_nonce, diagnostic_dir)
            except Exception as error:
                cleanup_error = uac_diagnostic_error("cleanup",
                    uac_failure_stage(error, fallback="runner_main"), kind="runtime")
                if not primary_error:
                    raise cleanup_error from None
                print(uac_failure_message(cleanup_error), file=sys.stderr)
        elif args.suite in V4_CONTROLS_BUDGETS:
            primary_error = sys.exc_info()[0] is not None
            try:
                failures = cleanup_v4_run(v4_worker_controller, worker, server,
                    diagnostics, logs, ldap_fixture, message_fixture,
                    creation_attempted, name, owner)
                if failures and not primary_error:
                    raise RuntimeError("directory v4 owned cleanup failed")
            except Exception:
                if not primary_error:
                    raise
                print("Directory v4 cleanup: cleanup_failed", file=sys.stderr)
            finally:
                try:
                    diagnostics.phase("after-cleanup")
                    diagnostics.write(diagnostic_dir / "runtime.json")
                except Exception:
                    print("Safe runtime diagnostics could not be retained", file=sys.stderr)
        else:
            if sys.exc_info()[0] is not None and owned_ci_worker is not None:
                owned_ci_worker.report_failure_diagnostic()
            try:
                diagnostics.snapshot(server, worker)
            except Exception:
                # Optional evidence must never prevent owned cleanup or mask the
                # original browser failure. Never print diagnostic exception text.
                diagnostics.report["collection_errors"].append("snapshot")
            try:
                shutdown_error = None
                if running_cancel_guard is not None:
                    try:
                        running_cancel_guard.close()
                    except (OSError, RuntimeError):
                        shutdown_error = RuntimeError("running export worker cleanup failed")
                if export_worker_guard is not None:
                    try:
                        export_worker_guard.close()
                    except (OSError, RuntimeError):
                        shutdown_error = RuntimeError("owned export worker cleanup failed")
                if account_barrier is not None:
                    try:
                        account_barrier.close()
                    except Exception as error:
                        shutdown_error = error
                if owned_ci_worker is not None and worker.poll() is None:
                    try:
                        owned_ci_worker.controller.resume_and_release(abort=True)
                    except (OSError, RuntimeError):
                        shutdown_error = RuntimeError("owned CI rescue cleanup failed")
                worker_cancel_acknowledged = False
                worker_shutdown_deadline = time.monotonic() + 30 if owned_ci_worker is not None else None
                if (owned_ci_worker is not None and running_cancel_guard is not None
                        and shutdown_error is None and worker.poll() is None):
                    try:
                        owned_ci_worker.prepare_final_shutdown(worker_shutdown_deadline)
                        running_cancel_guard.verify_final_quiescence(worker_shutdown_deadline)
                        owned_ci_worker.cancel_final_shutdown(worker_shutdown_deadline)
                        worker_cancel_acknowledged = True
                    except Exception:
                        shutdown_error = RuntimeError("owned CI final quiescence failed")
                        try:
                            owned_ci_worker.abort_final_shutdown()
                        except Exception:
                            pass  # Preserve failure and continue bounded TERM/KILL recovery.
                if (running_cancel_guard is not None and shutdown_error is None
                        and not worker_cancel_acknowledged):
                    shutdown_error = RuntimeError("owned CI final cancellation acknowledgement missing")
                for label, process in [("worker", worker), ("API", server)]:
                    if process is None:
                        continue
                    if args.suite == "messages-bulk" and label == "worker" and process.poll() is None:
                        # A failed browser may leave our own child stopped. Resume before shutdown.
                        process.send_signal(signal.SIGCONT)
                    process_record = diagnostics.report["processes"]["api" if label == "API" else "worker"]
                    if label != "worker" or not worker_cancel_acknowledged:
                        process.terminate()
                    try:
                        wait_budget = (max(0, worker_shutdown_deadline - time.monotonic())
                                       if label == "worker" and worker_shutdown_deadline is not None else 30)
                        code = process.wait(timeout=wait_budget)
                        process_record["after_cleanup"] = code
                        if code != 0:
                            shutdown_error = RuntimeError(f"{label} did not exit successfully")
                    except subprocess.TimeoutExpired:
                        process_record["forced_kill"] = True
                        process.kill()
                        process_record["after_cleanup"] = process.wait(timeout=10)
                        shutdown_error = RuntimeError(f"{label} did not stop gracefully")
                if owned_ci_worker is not None:
                    try:
                        owned_ci_worker.close_after_wait()
                    except (OSError, RuntimeError, subprocess.TimeoutExpired):
                        shutdown_error = RuntimeError("owned CI final witness or cleanup failed")
                    owned_ci_worker.report_first_invalidation_after_wait(logs)
                if running_cancel_guard is not None and worker is not None and worker.returncode is not None:
                    try:
                        running_cancel_guard.cleanup_activity_fixture_after_wait(worker.returncode)
                    except Exception:
                        shutdown_error = RuntimeError("owned CI activity fixture cleanup failed")
                if running_cancel_guard is not None and shutdown_error is None:
                    try:
                        running_cancel_guard.verify_recovery_cleanup_after_wait(worker.returncode, owned_ci_worker.recovery_cleanup_after_wait(logs))
                    except Exception:
                        shutdown_error = RuntimeError("owned CI recovery cleanup proof failed")
                if running_cancel_guard is not None and shutdown_error is None:
                    try:
                        # The worker's successful Wait includes journal.Close/drain.
                        # Query its final report while the isolated database is alive.
                        running_cancel_guard.verify_journal_after_wait(worker.returncode, owned_ci_worker.final_journal_after_wait(logs))
                    except Exception:
                        shutdown_error = RuntimeError("owned CI final journal proof failed")
                if ldap_fixture is not None:
                    try:
                        ldap_fixture.close()
                    except Exception as error:
                        shutdown_error = error
                try:
                    panicked = http_handler_panicked(logs)
                    diagnostics.report["http_panic"] = panicked
                except (OSError, UnicodeError, ValueError):
                    shutdown_error = RuntimeError(
                        "browser acceptance runtime diagnostics could not be checked"
                    )
                else:
                    if panicked:
                        shutdown_error = RuntimeError(
                            "browser acceptance runtime observed an HTTP handler panic; raw diagnostics withheld"
                        )
                finally:
                    logs.close()
                if message_fixture is not None:
                    message_fixture.cleanup()
                if creation_attempted:
                    remove_owned_container(name, owner)
                if shutdown_error is not None:
                    raise shutdown_error
            finally:
                try:
                    diagnostics.phase("after-cleanup")
                    diagnostics.write(diagnostic_dir / "runtime.json")
                except Exception:
                    print("Safe runtime diagnostics could not be retained", file=sys.stderr)
    if args.suite == UAC_SUITE:
        try:
            try:
                uac_candidate = discover_uac_candidate(repo=Path.cwd())
            except Exception as error:
                raise uac_diagnostic_error(uac_failure_code(error),
                    uac_failure_stage(error, fallback="candidate_discovery")) from None
            finalize_uac_evidence(repo=Path.cwd(), run_nonce=uac_nonce,
                provenance_bytes=uac_provenance, candidate_path=uac_candidate,
                retained_ldap=ldap_fixture.retained_uac_evidence, cleanup_receipt=uac_cleanup)
        finally:
            # Release retained private bytes without mutating F's read-only slot.
            ldap_fixture = None
            uac_provenance = None
    if args.suite == "tasks-health-recovery":
        close_health_evidence(health_cleanup, health_build_record, env, health_run_deadline)
    if args.suite == "messages":
        # Additive sequential acceptance, fresh owned database; never recurse from bulk.
        subprocess.run([sys.executable, __file__, "--suite", "messages-bulk"], check=True)
    print(f"{args.suite} (expired={args.expired}): {chain} acceptance PASS")



if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        if ("--suite=" + UAC_SUITE in sys.argv[1:]
                or any(a == "--suite" and b == UAC_SUITE
                       for a, b in zip(sys.argv[1:], sys.argv[2:]))):
            # Never print the underlying exception/cause or a request/SQL value.
            print(uac_failure_message(error), file=sys.stderr)
            sys.exit(1)
        if ("--suite=tasks-health-recovery" in sys.argv[1:]
                or any(a == "--suite" and b == "tasks-health-recovery"
                       for a, b in zip(sys.argv[1:], sys.argv[2:]))):
            print("tasks_health_recovery_failed", file=sys.stderr)
            sys.exit(1)
        raise
