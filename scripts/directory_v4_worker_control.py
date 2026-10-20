"""Test-only, one-shot owned Worker admission for directory-v4-cancel.

No product endpoint, process discovery, shell, restart or termination lives here.
The runner captures launch()/ready(process, cancelled, deadline) before opening
this listener. launch must do only the fixed Popen and return its handle; ready
must honor cancellation and the monotonic deadline, with each I/O <= 1 second.
The runner is the sole terminate/wait owner, including on readiness failure.
"""
from dataclasses import dataclass
import hmac
import json
import secrets
import socket
import threading
import time


URL_ENV = "ADTR_E2E_V4_WORKER_CONTROL_URL"
TOKEN_ENV = "ADTR_E2E_V4_WORKER_CONTROL_TOKEN"
MAX_REQUEST_BYTES = 8192
REQUEST_SECONDS = 1.0
READY_SECONDS = 110.0  # Existing 100 * (1-second probe + .1-second interval).
JOIN_SECONDS = 2.0


def browser_environment(environment):
    """Chromium must not inherit the Node harness's private fixture inputs.

    Call at browser launch, not just at context creation. This conservative
    boundary removes all ADTR inputs, not merely the controller's two names.
    It returns a copy and leaves ordinary display/PATH/runtime inputs intact.
    """
    return {key: value for key, value in environment.items()
            if not key.upper().startswith(("ADTR_", "POSTGRES_", "PG"))}


@dataclass(frozen=True)
class OwnershipTransfer:
    process: object
    failures: tuple


class DelayedOwnedWorker:
    """Fixed callbacks only; no caller request can choose a process or URL.

    Public lifecycle: open -> Node GET /state, POST /start -> seal_and_take ->
    runner TERM/wait(30)/KILL/wait(10) -> finish_after_wait. seal_and_take is
    single-consumer: a second call raises rather than returning the child twice.
    Safe status deliberately omits URL, token, PID, exception and environment.
    """

    def __init__(self, launch, ready):
        self._launch = launch
        self._ready = ready
        self._lock = threading.Lock()
        self._cancelled = threading.Event()
        self._phase = "not_started"
        self._failure = None
        self._launch_count = 0
        self._process = None
        self._startup = None
        self._listener = None
        self._serving = None
        self._token = secrets.token_urlsafe(32)
        self._taken = False
        self._taking = False
        self._opened = False
        self._close_failures = ()

    def __repr__(self):
        return "<DelayedOwnedWorker private>"

    def open(self):
        """Bind only an ephemeral IPv4 loopback port. Return Node-only env.

        No input/environment is read to configure the bind address or token.
        The caller must not print, persist, or pass this mapping to Chromium.
        """
        with self._lock:
            if self._opened or self._phase != "not_started" or self._taken:
                raise RuntimeError("worker controller cannot reopen")
            listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            try:
                listener.bind(("127.0.0.1", 0))
                listener.listen(8)
                listener.settimeout(0.1)
                self._listener = listener
                self._serving = threading.Thread(target=self._serve,
                                                  name="directory-v4-control")
                self._serving.start()
                self._opened = True
            except Exception:
                listener.close()
                self._listener = None
                self._serving = None
                self._phase = "failed"
                self._failure = "listener_failed"
                raise RuntimeError("worker controller listener failed") from None
            return {URL_ENV: "http://127.0.0.1:%d" % listener.getsockname()[1],
                    TOKEN_ENV: self._token}

    def _state_locked(self):
        return {"state": self._phase, "launchCount": self._launch_count,
                "failure": self._failure}

    def state(self):
        with self._lock:
            return self._state_locked()

    def _start(self):
        with self._lock:
            if self._cancelled.is_set() or self._phase in ("closing", "closed"):
                return 409, self._state_locked()
            if self._phase == "not_started":
                self._phase = "starting"
                self._startup = threading.Thread(target=self._start_owned,
                                                 name="directory-v4-readiness")
                try:
                    self._startup.start()
                except Exception:
                    self._startup = None
                    self._phase = "failed"
                    self._failure = "startup_thread_failed"
            return 200, self._state_locked()

    def _start_owned(self):
        # Only the short, fixed Popen/capture boundary holds this mutex.
        # No readiness, joins, HTTP, process waits or termination under it.
        with self._lock:
            if self._cancelled.is_set() or self._phase != "starting":
                return
            try:
                self._process = self._launch()
                if self._process is None:
                    raise RuntimeError("missing owned handle")
                self._launch_count = 1
            except Exception:
                self._phase = "failed"
                self._failure = "launch_failed"
                return
            process = self._process
        try:
            deadline = time.monotonic() + READY_SECONDS
            if process.poll() is not None:
                raise RuntimeError("startup exit")
            ready = self._ready(process, self._cancelled, deadline)
            if ready is not True or time.monotonic() > deadline:
                raise RuntimeError("readiness failed")
            if process.poll() is not None:
                raise RuntimeError("startup exit")
        except Exception:
            with self._lock:
                if not self._cancelled.is_set() and self._phase == "starting":
                    self._phase = "failed"
                    self._failure = "readiness_failed"
            return
        with self._lock:
            if not self._cancelled.is_set() and self._phase == "starting":
                self._phase = "ready"

    def request(self, method, target, headers, body=b""):
        """Closed protocol validation, also usable by offline contracts.

        headers is an ordered pair list so duplicate authentication/framing
        fields cannot disappear into a dict. HTTP framing is bounded separately.
        """
        if (not isinstance(body, bytes) or body or len(headers) > 32
                or sum(len(k) + len(v) + 4 for k, v in headers) > MAX_REQUEST_BYTES):
            return 400, {"error": "invalid_request"}
        values = {}
        for key, value in headers:
            name = key.lower()
            if name in values or not name or any(c not in
                    "abcdefghijklmnopqrstuvwxyz0123456789-" for c in name):
                return 400, {"error": "invalid_request"}
            if any(ord(c) < 32 or ord(c) > 126 for c in value):
                return 400, {"error": "invalid_request"}
            values[name] = value
        expected = "Bearer " + self._token
        authorization = values.get("authorization", "")
        if not hmac.compare_digest(authorization, expected):
            return 401, {"error": "unauthorized"}
        if ("transfer-encoding" in values or "expect" in values
                or values.get("content-length", "0") != "0"):
            return 400, {"error": "invalid_request"}
        if target not in ("/state", "/start"):
            return 404, {"error": "not_found"}
        if (method, target) == ("GET", "/state"):
            return 200, self.state()
        if (method, target) == ("POST", "/start"):
            return self._start()
        return 405, {"error": "method_not_allowed"}

    def _receive(self, connection):
        deadline = time.monotonic() + REQUEST_SECONDS
        data = bytearray()
        while b"\r\n\r\n" not in data:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or len(data) >= MAX_REQUEST_BYTES:
                return 400, {"error": "invalid_request"}
            connection.settimeout(remaining)
            chunk = connection.recv(min(1024, MAX_REQUEST_BYTES - len(data)))
            if not chunk:
                return 400, {"error": "invalid_request"}
            data.extend(chunk)
        head, body = bytes(data).split(b"\r\n\r\n", 1)
        try:
            lines = head.decode("ascii").split("\r\n")
            method, target, version = lines[0].split(" ")
            if version != "HTTP/1.1":
                raise ValueError()
            headers = []
            for line in lines[1:]:
                key, value = line.split(":", 1)
                if value.startswith(" "):
                    value = value[1:]
                headers.append((key, value))
            if sum(key.lower() == "host" for key, _ in headers) != 1:
                raise ValueError()
        except (ValueError, UnicodeError):
            return 400, {"error": "invalid_request"}
        return self.request(method, target, headers, body)

    def _serve(self):
        while not self._cancelled.is_set():
            try:
                connection, address = self._listener.accept()
            except socket.timeout:
                continue
            except OSError:
                if not self._cancelled.is_set():
                    with self._lock:
                        if not self._cancelled.is_set():
                            if self._failure is None:
                                self._failure = "listener_failed"
                            self._phase = "failed"
                return
            with connection:
                try:
                    if address[0] != "127.0.0.1":
                        continue
                    status, value = self._receive(connection)
                    payload = json.dumps(value, separators=(",", ":")).encode("ascii")
                    connection.settimeout(REQUEST_SECONDS)
                    connection.sendall(("HTTP/1.1 %d Result\r\n"
                                        "Content-Type: application/json\r\n"
                                        "Cache-Control: no-store\r\n"
                                        "Connection: close\r\n"
                                        "Content-Length: %d\r\n\r\n" %
                                        (status, len(payload))).encode("ascii") + payload)
                except (OSError, ValueError, UnicodeError):
                    pass  # Lost response is reconciled by GET, never logged/replayed.

    def seal_and_take(self):
        """Reject starts, join bounded work, transfer exact child once.

        Failures are returned alongside the child, never raised in place of it:
        the runner must still clean that child if joining fails. A misbehaving
        readiness callback is a failed fixture, not permission for replacement.
        """
        self._cancelled.set()  # Stop admission even while the fixed spawn returns.
        with self._lock:
            if self._taken or self._taking:
                raise RuntimeError("worker ownership already transferred")
            self._taking = True
            self._phase = "closing"
            process = self._process
            startup, serving, listener = self._startup, self._serving, self._listener
        failures = []
        if listener is not None:
            try:
                listener.close()
            except Exception:
                failures.append("listener_close_failed")
        # All joins outside ownership mutex. No helper terminate/wait calls.
        for label, thread in (("listener", serving), ("readiness", startup)):
            if thread is not None:
                try:
                    thread.join(JOIN_SECONDS)
                    if thread.is_alive():
                        failures.append(label + "_join_failed")
                except Exception:
                    failures.append(label + "_join_failed")
        transfer = OwnershipTransfer(process, tuple(failures))
        with self._lock:
            self._close_failures = transfer.failures
            self._taking = False
            self._taken = True
        return transfer

    def finish_after_wait(self, *, reaped, returncode, forced_kill=False):
        """Called only after the runner's owned cleanup, with safe outcome facts.

        `closed` means listener/readiness joined and child reaped. Nonzero exit
        or forced kill is still a failed cleanup, even though ownership is closed.
        This method does not poll, signal, wait, or claim cancellation evidence.
        """
        with self._lock:
            if not self._taken or self._phase != "closing":
                raise RuntimeError("worker cleanup order invalid")
            threads_done = all(thread is None or not thread.is_alive()
                               for thread in (self._startup, self._serving))
            owned_reaped = self._process is None or (reaped is True
                                                     and returncode is not None)
            if threads_done and owned_reaped:
                self._phase = "closed"
            clean = (self._phase == "closed" and not self._close_failures
                     and not forced_kill
                     and (self._process is None or returncode == 0))
            if not clean:
                self._failure = "cleanup_failed"
            return clean
