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
ROUTES = {"unknown", "/api/resources/tenant", "/api/auth/me", "/api/domains/detail",
          "/api/directory-credential-use/effective", "/api/directory-credential-use/v2/effective",
          "/api/credential-use/effective", "/api/tasks", "/api/tasks/detail",
          "/api/directory/v2/task", "/api/directory/v2/receipt",
          "/api/directory/observation", "/api/directory/v2/observation"}
BROWSER_PHASES = {"read-json", "totp-wait", "grant-v2-complete"}


def integer(value, low, high):
    return type(value) is int and low <= value <= high


def validate_event(event):
    if type(event) is not dict or set(event) != {"phase", "method", "route", "elapsed_ms", "at_ms", "outcome", "status"}:
        raise ValueError("invalid safe browser event")
    if (event["phase"] not in BROWSER_PHASES or event["method"] not in {"GET", "none"}
            or event["route"] not in ROUTES or event["outcome"] not in {"start", "response", "transport-error", "complete"}
            or not integer(event["elapsed_ms"], 0, MAX_MS) or not integer(event["at_ms"], 0, MAX_MS)
            or not (event["status"] is None or integer(event["status"], 100, 599))):
        raise ValueError("invalid safe browser event")


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
