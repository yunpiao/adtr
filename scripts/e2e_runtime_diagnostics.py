"""Strict panic detection and bounded allowlisted CI evidence, without log export."""
import json
from pathlib import Path
import re
import time


# The owned API/worker use Go's unchanged default logger and loopback listener.
# Match its complete line prefix, not an incidental phrase in a log message.
HTTP_PANIC_HEADER = re.compile(
    r"^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} http: panic serving "
    r"(?:127\.0\.0\.1|\[::1\]):[1-9]\d{0,4}: "
)


def http_handler_panicked(logs):
    # net/http recovers panics per connection, so process exit alone cannot
    # establish health. Scan after owned processes stop; do not emit panic
    # values, stack arguments, request headers, or any other raw diagnostics.
    logs.flush()
    logs.seek(0)
    return any(HTTP_PANIC_HEADER.match(line) for line in logs)


# Artifact data is constructed from enums and bounded integers, never log text.
# Validation is repeated at the upload boundary; unknown fields are rejected.
MAX_BYTES = 65536
MAX_EVENTS = 256
MAX_MS = 86400000
PHASES = {"setup", "api-start", "api-ready", "worker-start", "worker-ready",
          "browser-start", "browser-pass", "before-cleanup", "after-cleanup"}
ROUTES = {"/api/domain-selection", "/api/domain-selection/resolve", "/api/access/menu", "/api/access/check",
          "/api/user-assets/v2", "/api/user-assets/v3", "unknown", "/api/resources/tenant", "/api/auth/me", "/api/domains/detail",
          "/api/directory-credential-use/effective", "/api/directory-credential-use/v2/effective",
          "/api/credential-use/effective", "/api/credential-use/grant", "/api/tasks", "/api/tasks/detail",
          "/api/directory/v2/task", "/api/directory/v2/receipt",
          "/api/directory/observation", "/api/directory/v2/observation"}
BROWSER_PHASES = {"read-json", "totp-wait", "grant-v2-complete"}


def integer(value, low, high):
    return type(value) is int and low <= value <= high


def validate_event(event):
    if type(event) is dict and event.get("origin") == "navigation":
        return validate_navigation_event(event)
    if type(event) is dict and "origin" in event:
        return validate_request_event(event)
    if type(event) is not dict or set(event) != {"phase", "method", "route", "elapsed_ms", "at_ms", "outcome", "status"}:
        raise ValueError("invalid safe browser event")
    if (event["phase"] not in BROWSER_PHASES or event["method"] not in {"GET", "none"}
            or event["route"] not in ROUTES or event["outcome"] not in {"start", "response", "transport-error", "complete"}
            or not integer(event["elapsed_ms"], 0, MAX_MS) or not integer(event["at_ms"], 0, MAX_MS)
            or not (event["status"] is None or integer(event["status"], 100, 599))):
        raise ValueError("invalid safe browser event")


NAVIGATION_STAGES = {"start", "layout-ready", "menu-open", "target-ready", "failed"}
NAVIGATION_BOOLEANS = {"compact", "panelVisible", "panelHidden", "panelInert", "panelDialog",
                       "toggleVisible", "toggleEnabled", "toggleExpanded", "targetVisible",
                       "targetEnabled", "groupOpen", "pageClosed", "unavailable"}


def validate_navigation_event(event):
    # A separate closed schema preserves every legacy event's strict contract.
    if type(event) is not dict or set(event) != {"origin", "stage", "operation", "at_ms", "elapsed_ms", "snapshot"}:
        raise ValueError("invalid safe navigation event")
    if (event["origin"] != "navigation" or type(event["stage"]) is not str or event["stage"] not in NAVIGATION_STAGES
            or not integer(event["operation"], 1, 1000000)
            or not integer(event["at_ms"], 0, MAX_MS)
            or not integer(event["elapsed_ms"], 0, MAX_MS)):
        raise ValueError("invalid safe navigation fields")
    snapshot = event["snapshot"]
    if (type(snapshot) is not dict or set(snapshot) != NAVIGATION_BOOLEANS | {"width", "height"}
            or any(type(snapshot[key]) is not bool for key in NAVIGATION_BOOLEANS)):
        raise ValueError("invalid safe navigation snapshot")
    if snapshot["unavailable"]:
        valid = (type(snapshot["width"]) is int and snapshot["width"] == 0
                 and type(snapshot["height"]) is int and snapshot["height"] == 0
                 and all(not snapshot[key] for key in NAVIGATION_BOOLEANS - {"unavailable", "pageClosed"}))
    else:
        valid = (not snapshot["pageClosed"] and integer(snapshot["width"], 1, 16384)
                 and integer(snapshot["height"], 1, 16384))
    if not valid:
        raise ValueError("invalid safe navigation snapshot relation")


REQUEST_PHASES = {"directory-get", "directory-v2-verification-get", "fixture-get", "page-request"}
SUBMIT_PHASES = {"grant-wait", "grant-click"}
STEP_PHASES = {"test-start", "test-finish", "administrator-login", "version-switch", "source-select"}
ERROR_CLASSES = {"none", "econnreset", "socket_hang_up", "response_aborted", "context_disposed", "target_closed", "connection_refused", "timeout", "unknown_error", "non_error", "page_request_failed"}


def validate_snapshot(value):
    if type(value) is not dict or set(value) != {"view", "picker", "source", "alert", "permission_denied", "page_closed", "unavailable"}:
        raise ValueError("invalid safe snapshot")
    if (value["view"] not in {"v2", "v3", "login", "other", "unavailable"}
            or value["picker"] not in {"visible", "hidden", "absent", "unavailable"}
            or value["source"] not in {"loading", "empty", "radio-present", "absent", "unavailable"}
            or any(type(value[key]) is not bool for key in ["alert", "permission_denied", "page_closed", "unavailable"])):
        raise ValueError("invalid safe snapshot")
    if value["unavailable"]:
        valid = all(value[key] == "unavailable" for key in ["view", "picker", "source"]) and not value["alert"] and not value["permission_denied"]
    else:
        valid = all(value[key] != "unavailable" for key in ["view", "picker", "source"]) and not value["page_closed"]
    if not valid:
        raise ValueError("invalid safe snapshot relation")


def validate_request_event(event):
    if set(event) != {"origin", "phase", "operation", "attempt", "method", "route", "outcome", "status", "error_class", "at_ms", "elapsed_ms", "snapshot"}:
        raise ValueError("invalid safe request event")
    if (event["origin"] not in {"api-request", "page-request", "step"}
            or event["phase"] not in REQUEST_PHASES | STEP_PHASES | SUBMIT_PHASES
            or not integer(event["operation"], 1, 1000000) or not integer(event["attempt"], 0, 2)
            or event["method"] not in {"GET", "POST", "other", "none"} or event["route"] not in ROUTES
            or event["outcome"] not in {"start", "registered", "response", "finished", "transport-error", "complete", "failed"}
            or event["error_class"] not in ERROR_CLASSES
            or not (event["status"] is None or integer(event["status"], 100, 599))
            or not integer(event["at_ms"], 0, MAX_MS) or not integer(event["elapsed_ms"], 0, MAX_MS)):
        raise ValueError("invalid safe request fields")
    if event["origin"] == "step" and event["phase"] in SUBMIT_PHASES:
        if (event["method"] != "none" or event["route"] != "/api/credential-use/grant"
                or event["attempt"] != 0 or event["status"] is not None or event["error_class"] != "none" or event["snapshot"] is not None
                or not (event["outcome"] in {"start", "complete", "failed"} or (event["phase"] == "grant-wait" and event["outcome"] == "registered"))):
            raise ValueError("invalid safe submit relation")
        return
    if event["origin"] == "step":
        if (event["phase"] not in STEP_PHASES or event["method"] != "none" or event["route"] != "unknown"
                or event["attempt"] != 0 or event["status"] is not None or event["error_class"] != "none"
                or event["outcome"] not in {"start", "complete", "failed"}):
            raise ValueError("invalid safe step relation")
        validate_snapshot(event["snapshot"])
        return
    if event["snapshot"] is not None or event["method"] == "none":
        raise ValueError("invalid safe request relation")
    if event["origin"] == "api-request":
        if (event["phase"] not in {"directory-get", "directory-v2-verification-get", "fixture-get"} or event["method"] != "GET"
                or not integer(event["attempt"], 1, 1 if event["phase"] == "directory-get" else 2)
                or event["outcome"] not in {"start", "response", "transport-error"}):
            raise ValueError("invalid safe API request relation")
    elif event["phase"] != "page-request" or event["attempt"] != 0 or event["outcome"] not in {"start", "response", "finished", "transport-error"}:
        raise ValueError("invalid safe page request relation")
    if event["outcome"] == "start":
        valid = event["status"] is None and event["error_class"] == "none"
    elif event["outcome"] in {"response", "finished"}:
        valid = event["status"] is not None and event["error_class"] == "none"
    elif event["origin"] == "api-request":
        valid = event["status"] is None and event["error_class"] not in {"none", "page_request_failed"}
    else:
        valid = event["error_class"] == "page_request_failed"
    if not valid:
        raise ValueError("invalid safe outcome relation")


def validate_report(report):
    if type(report) is not dict or set(report) != {"schema", "phases", "processes", "http_panic", "browser", "browser_capture", "browser_dropped", "collection_errors"} or type(report["schema"]) is not int or report["schema"] != 1:
        raise ValueError("invalid safe runtime report")
    if type(report["phases"]) is not list or len(report["phases"]) > len(PHASES):
        raise ValueError("invalid safe phases")
    for phase in report["phases"]:
        if type(phase) is not dict or set(phase) != {"phase", "at_ms"} or phase["phase"] not in PHASES or not integer(phase["at_ms"], 0, MAX_MS):
            raise ValueError("invalid safe phase")
    if type(report["processes"]) is not dict or set(report["processes"]) != {"api", "worker"}:
        raise ValueError("invalid safe processes")
    for process in report["processes"].values():
        if type(process) is not dict or set(process) != {"started", "before_cleanup", "after_cleanup", "forced_kill"} or type(process["started"]) is not bool or type(process["forced_kill"]) is not bool:
            raise ValueError("invalid safe process")
        for field in ["before_cleanup", "after_cleanup"]:
            if process[field] is not None and not integer(process[field], -255, 255):
                raise ValueError("invalid safe process exit")
    if report["http_panic"] is not None and type(report["http_panic"]) is not bool:
        raise ValueError("invalid safe panic flag")
    if not integer(report["browser_dropped"], 0, 1000000):
        raise ValueError("invalid safe dropped count")
    if type(report["collection_errors"]) is not list or len(report["collection_errors"]) > 1 or any(error not in {"snapshot"} for error in report["collection_errors"]):
        raise ValueError("invalid safe collection errors")
    if report["browser_capture"] not in {"pending", "present", "missing"}:
        raise ValueError("invalid safe capture state")
    if type(report["browser"]) is not list or len(report["browser"]) > MAX_EVENTS:
        raise ValueError("invalid safe browser count")
    for event in report["browser"]:
        validate_event(event)
    encoded = json.dumps(report, separators=(",", ":"), sort_keys=True).encode()
    if len(encoded) > MAX_BYTES:
        raise ValueError("safe diagnostics exceed byte budget")
    return encoded


class RuntimeDiagnostics:
    def __init__(self):
        self.started = time.monotonic()
        self.report = {"schema": 1, "phases": [], "http_panic": None, "browser": [], "browser_capture": "pending", "browser_dropped": 0, "collection_errors": [],
                       "processes": {name: {"started": False, "before_cleanup": None,
                                            "after_cleanup": None, "forced_kill": False}
                                     for name in ["api", "worker"]}}

    def phase(self, phase):
        self.report["phases"].append({"phase": phase, "at_ms": min(MAX_MS, max(0, int((time.monotonic() - self.started) * 1000)))})

    def snapshot(self, api, worker):
        self.phase("before-cleanup")
        for name, process in [("api", api), ("worker", worker)]:
            self.report["processes"][name]["started"] = process is not None
            self.report["processes"][name]["before_cleanup"] = process.poll() if process is not None else None

    def write(self, path):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(validate_report(self.report))


def load_bounded(path):
    with path.open("rb") as source:
        raw = source.read(MAX_BYTES + 1)
    if len(raw) > MAX_BYTES:
        raise ValueError("safe diagnostics exceed byte budget")
    return raw


def publish(source, destination):
    # Fixed filenames only. Never copy source files or pass through unknown keys.
    (destination / "diagnostics.json").unlink(missing_ok=True)
    report = json.loads(load_bounded(source / "runtime.json"))
    validate_report(report)
    browser = source / "browser.json"
    report["browser_capture"] = "missing"
    if browser.exists():
        report["browser_capture"] = "present"
        capture = json.loads(load_bounded(browser))
        if type(capture) is not dict or set(capture) != {"events", "dropped"}:
            raise ValueError("invalid safe browser capture")
        report["browser"] = capture["events"]
        report["browser_dropped"] = capture["dropped"]
    encoded = validate_report(report)
    destination.mkdir(parents=True, exist_ok=True)
    (destination / "diagnostics.json").write_bytes(encoded)


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=Path)
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    try:
        publish(args.source, args.destination)
    except (OSError, ValueError, TypeError, KeyError):
        raise SystemExit("Safe diagnostics rejected; no artifact published") from None
