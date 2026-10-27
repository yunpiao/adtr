"""Owned synthetic LDAP/TLS fixture for CI; never contacts a real directory."""
import base64
import ipaddress
import hashlib
import re
import stat
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import uuid

from ci_postgres_image import postgres_image_args
from test_integration import IMAGE


class LDAPFixture:
    def __init__(self, control_enabled=False, directory_enabled=False,
                 directory_empty=False, directory_slow=False, *, directory_v2=False, user_assets_v2=False, directory_v3=False, directory_v4=False, computer_assets_v3=False, group_assets_v3=False, empty_transition=False, group_members_v4_pagination=False, group_members_v4_refresh=False, user_export_failure_v3=False, group_export_failure_v3=False, computer_export_failure_v3=False, user_uac_v3_profile: str = ""):
        if type(user_uac_v3_profile) is not str or user_uac_v3_profile not in ("", "mixed-33-v1"):
            raise ValueError("uac_evidence_invalid:configuration")
        if user_uac_v3_profile and any(type(value) is not bool for value in (
                control_enabled, directory_enabled, directory_empty, directory_slow, directory_v2,
                user_assets_v2, directory_v3, directory_v4, computer_assets_v3, group_assets_v3,
                empty_transition, group_members_v4_pagination, group_members_v4_refresh,
                user_export_failure_v3, group_export_failure_v3, computer_export_failure_v3)):
            raise ValueError("uac_evidence_invalid:configuration")
        if user_uac_v3_profile and (not directory_enabled or not directory_v3 or
                directory_v2 or directory_v4 or user_assets_v2 or computer_assets_v3 or
                group_assets_v3 or directory_empty or directory_slow or control_enabled or
                empty_transition or group_members_v4_pagination or group_members_v4_refresh or
                user_export_failure_v3 or group_export_failure_v3 or computer_export_failure_v3):
            raise ValueError("uac_evidence_invalid:configuration")
        self.user_uac_v3_profile = user_uac_v3_profile
        self.uac_evidence_directory = None
        self._retained_uac_evidence = None
        self._uac_nonce = None
        self._uac_closed = False
        self._uac_close_failed = False
        if any(not isinstance(value, bool) for value in (directory_enabled, directory_empty, directory_slow, directory_v2, user_assets_v2, directory_v3, directory_v4, computer_assets_v3, group_assets_v3, empty_transition, group_members_v4_pagination, group_members_v4_refresh, user_export_failure_v3, group_export_failure_v3, computer_export_failure_v3)):
            raise TypeError("synthetic directory modes must be booleans")
        if user_export_failure_v3 and (not directory_enabled or not directory_v3 or
                                       directory_v2 or directory_v4 or user_assets_v2 or
                                       computer_assets_v3 or group_assets_v3 or directory_empty or
                                       directory_slow or control_enabled or empty_transition or
                                       group_members_v4_pagination or group_members_v4_refresh):
            raise ValueError("synthetic export failure requires exclusive nonempty fast dictionary3 mode")
        self.user_export_failure_v3 = user_export_failure_v3
        if group_export_failure_v3 and (not directory_enabled or not directory_v3 or not group_assets_v3 or
                                        user_export_failure_v3 or directory_v2 or directory_v4 or user_assets_v2 or
                                        computer_assets_v3 or directory_empty or directory_slow or control_enabled or
                                        empty_transition or group_members_v4_pagination or group_members_v4_refresh):
            raise ValueError("group export failure requires exclusive nonempty fast group dictionary3 mode")
        self.group_export_failure_v3 = group_export_failure_v3
        if computer_export_failure_v3 and (not directory_enabled or not directory_v3 or not computer_assets_v3 or
                                        group_export_failure_v3 or user_export_failure_v3 or directory_v2 or directory_v4 or user_assets_v2 or
                                        group_assets_v3 or directory_empty or directory_slow or control_enabled or
                                        empty_transition or group_members_v4_pagination or group_members_v4_refresh):
            raise ValueError("computer export failure requires exclusive nonempty fast computer dictionary3 mode")
        self.computer_export_failure_v3 = computer_export_failure_v3
        if empty_transition and not group_members_v4_refresh and (not directory_enabled or not directory_v3 or
                                 computer_assets_v3 == group_assets_v3 or
                                 directory_empty or directory_slow or directory_v2 or
                                 user_assets_v2 or control_enabled):
            raise ValueError("synthetic empty transition requires exactly one dictionary3 asset mode without empty, slow, v2 or B2 control modes")
        if directory_v2 and not directory_enabled:
            raise ValueError("synthetic dictionary 2 requires directory mode")
        if (directory_empty or directory_slow) and not directory_enabled:
            raise ValueError("synthetic empty and slow modes require directory mode")
        if user_assets_v2 and (not directory_enabled or not directory_v2 or directory_empty or directory_slow):
            raise ValueError("synthetic user assets require nonempty fast directory-v2 mode")
        if directory_v3 and (not directory_enabled or directory_v2 or user_assets_v2):
            raise ValueError("synthetic dictionary 3 requires exclusive directory mode")
        self.directory_v3 = directory_v3
        if directory_v4 and (not directory_enabled or directory_v2 or directory_v3 or user_assets_v2 or computer_assets_v3 or group_assets_v3 or (empty_transition and not group_members_v4_refresh) or control_enabled):
            raise ValueError("synthetic dictionary 4 requires exclusive directory mode")
        self.directory_v4 = directory_v4
        if group_members_v4_pagination and (not directory_enabled or not directory_v4 or directory_empty or directory_slow):
            raise ValueError("group member pagination requires exclusive nonempty fast dictionary4 mode")
        if group_members_v4_refresh and (not directory_enabled or not directory_v4 or not empty_transition or directory_empty or directory_slow or group_members_v4_pagination):
            raise ValueError("group member refresh requires exclusive nonempty fast dictionary4 and private empty transition")
        self.group_members_v4_refresh = group_members_v4_refresh
        self.group_members_v4_pagination = group_members_v4_pagination
        if computer_assets_v3 and (not directory_enabled or not directory_v3 or directory_empty or directory_slow or user_assets_v2 or group_assets_v3):
            raise ValueError("computer assets require dictionary3 nonempty fast mode")
        self.computer_assets_v3 = computer_assets_v3
        if group_assets_v3 and (not directory_enabled or not directory_v3 or directory_empty or directory_slow or user_assets_v2 or computer_assets_v3):
            raise ValueError("group assets require exclusive dictionary3 nonempty fast mode")
        self.group_assets_v3 = group_assets_v3
        self.user_assets_v2 = user_assets_v2
        self.directory_enabled = directory_enabled
        self.directory_v2 = directory_v2
        self.directory_empty = directory_empty
        self.directory_slow = directory_slow
        self.empty_transition = empty_transition
        self.name = "adtr-ldap-e2e-" + uuid.uuid4().hex[:12]
        self.network = self.name + "-net"
        self.created_network = False
        self.created_container = False
        self.directory = None
        self.control_directory = None
        self.empty_transition_directory = None
        try:
            self.directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-e2e-")
            self.control_directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-control-") if control_enabled else None
            # Fresh private host directory; only Node publishes the fixed marker.
            self.empty_transition_directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-empty-transition-") if empty_transition else None
        except BaseException:
            if self.user_uac_v3_profile:
                try:
                    self.close()
                except BaseException:
                    pass  # Preserve the fixed primary construction failure.
                raise RuntimeError("uac_evidence_invalid:configuration") from None
            self.close()
            raise

    @property
    def control_path(self):
        return Path(self.control_directory.name) if self.control_directory else None

    @property
    def empty_transition_path(self):
        return Path(self.empty_transition_directory.name) if self.empty_transition_directory else None

    @property
    def retained_uac_evidence(self) -> tuple[bytes, bytes, int] | None:
        return self._retained_uac_evidence

    def start(self, env):
        if not self.user_uac_v3_profile:
            self._start(env)
            return
        try:
            self._start(env)
        except Exception:
            raise RuntimeError("uac_evidence_invalid:configuration") from None

    def _start(self, env):
        # Only the runner's freshly generated nonce is accepted. No ambient
        # fallback, caller path, alternate nonce name or implicit profile.
        if self.user_uac_v3_profile:
            nonce = env.get("ADTR_LDAP_FIXTURE_UAC_RUN_NONCE")
            if type(nonce) is not str or re.fullmatch(r"[0-9a-f]{32}", nonce) is None or self.uac_evidence_directory is not None:
                raise RuntimeError("uac_evidence_invalid:configuration")
            self._uac_nonce = nonce
            self.uac_evidence_directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-uac-evidence-")
            env["ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE"] = self.user_uac_v3_profile
            env["ADTR_E2E_LDAP_UAC_EVIDENCE_DIR"] = self.uac_evidence_directory.name
            env["ADTR_E2E_USER_ASSET_EXPORT_V3_UAC"] = "true"
        else:
            for key in ("ADTR_LDAP_FIXTURE_UAC_RUN_NONCE", "ADTR_E2E_LDAP_DIRECTORY_USER_UAC_V3_PROFILE", "ADTR_E2E_LDAP_UAC_EVIDENCE_DIR", "ADTR_E2E_USER_ASSET_EXPORT_V3_UAC"):
                env.pop(key, None)
        root = Path(self.directory.name)
        # These short-lived keys and credentials belong only to this synthetic fixture.
        commands = [
            ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=ADTR synthetic root",
             "-keyout", str(root / "ca.key"), "-out", str(root / "ca.crt"),
             "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign"],
            ["openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes", "-subj", "/CN=dc.synthetic.invalid",
             "-keyout", str(root / "server.key"), "-out", str(root / "server.csr")],
            ["openssl", "x509", "-req", "-in", str(root / "server.csr"), "-CA", str(root / "ca.crt"),
             "-CAkey", str(root / "ca.key"), "-CAcreateserial", "-days", "1", "-out", str(root / "server.crt"),
             "-extfile", str(root / "server.ext")],
        ]
        (root / "server.ext").write_text("basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:dc.synthetic.invalid\n")
        for command in commands:
            subprocess.run(command, check=True, capture_output=True, timeout=30)
        binary = root / "ldap-fixture"
        subprocess.run(["go", "build", "-tags=integration", "-o", str(binary), "./internal/testldapfixture"],
                       env=dict(os.environ, CGO_ENABLED="0"), check=True, timeout=120)
        # A failed/timeout create can have executed. Mark our unique target
        # before attempting it so C cleanup never abandons an uncertain create.
        if self.user_uac_v3_profile:
            self.created_network = True
        subprocess.run(["docker", "network", "create", "--internal", self.network], check=True, capture_output=True, timeout=30)
        self.created_network = True
        username = "fixture@synthetic.invalid"
        password = secrets.token_urlsafe(24)
        fixture_env = dict(os.environ, ADTR_LDAP_FIXTURE_USERNAME=username, ADTR_LDAP_FIXTURE_PASSWORD=password)
        fixture_env.pop("ADTR_LDAP_FIXTURE_UAC_RUN_NONCE", None)
        if self.user_uac_v3_profile:
            fixture_env["ADTR_LDAP_FIXTURE_UAC_RUN_NONCE"] = self._uac_nonce
        command = ["docker", "run", "--detach", "--name", self.name, "--network", self.network,
                        "--user", f"{os.getuid()}:{os.getgid()}",
                        "--entrypoint", "/fixture/ldap-fixture", "-v", f"{root}:/fixture:ro",
                        "-e", "ADTR_LDAP_FIXTURE_USERNAME", "-e", "ADTR_LDAP_FIXTURE_PASSWORD"]
        if self.user_uac_v3_profile:
            command.extend(["-v", f"{self.uac_evidence_directory.name}:/uac-evidence:rw", "-e", "ADTR_LDAP_FIXTURE_UAC_RUN_NONCE"])
        if self.control_path is not None:
            command.extend(["-v", f"{self.control_path}:/control:rw"])
        if self.empty_transition_path is not None:
            command.extend(["-v", f"{self.empty_transition_path}:/empty-transition:ro"])
        command.extend([*postgres_image_args(IMAGE),
                        "-cert", "/fixture/server.crt", "-key", "/fixture/server.key",
                        "-starttls-listen", "0.0.0.0:389", "-ldaps-listen", "0.0.0.0:636"])
        if self.control_path is not None:
            command.extend(["-control-dir", "/control"])
        if self.empty_transition_path is not None:
            command.extend(["-empty-transition-dir", "/empty-transition"])
        if self.user_uac_v3_profile:
            command.extend(["-user-uac-v3-profile", self.user_uac_v3_profile, "-user-uac-evidence-dir", "/uac-evidence"])
        if self.directory_enabled:
            command.append("-directory-mode")
        if self.directory_v4:
            command.append("-directory-v4")
        if self.group_members_v4_refresh:
            command.append("-group-members-v4-refresh")
        if self.group_members_v4_pagination:
            command.append("-group-members-v4-pagination")
        if self.group_export_failure_v3:
            command.append("-group-export-failure-v3")
        if self.computer_export_failure_v3:
            command.append("-computer-export-failure-v3")
        if self.user_export_failure_v3:
            command.append("-user-export-failure-v3")
        if self.directory_v3:
            command.append("-directory-v3")
        if self.directory_v2:
            command.append("-directory-v2")
        if self.computer_assets_v3:
            command.append("-computer-assets-v3")
        if self.group_assets_v3:
            command.append("-group-assets-v3")
        if self.user_assets_v2:
            command.append("-user-assets-v2")
        if self.directory_empty:
            command.append("-directory-empty")
        if self.directory_slow:
            command.append("-directory-slow")
        if self.user_uac_v3_profile:
            self.created_container = True
        subprocess.run(command,
                       env=fixture_env, check=True, capture_output=True, timeout=60)
        self.created_container = True
        for _ in range(100):
            output = subprocess.check_output(["docker", "logs", self.name], text=True, stderr=subprocess.STDOUT, timeout=5)
            if "synthetic LDAP fixture ready" in output:
                break
            running = subprocess.check_output(["docker", "inspect", "-f", "{{.State.Running}}", self.name], text=True, timeout=5).strip()
            if running != "true":
                raise RuntimeError("synthetic LDAP fixture exited during startup")
            time.sleep(0.1)
        else:
            raise RuntimeError("synthetic LDAP fixture failed readiness")
        address = subprocess.check_output(["docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", self.name], text=True, timeout=5).strip()
        ip = ipaddress.ip_address(address)
        if ip.version != 4 or not ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_unspecified:
            raise RuntimeError("synthetic LDAP fixture has an unexpected address")
        policy = {"version": 1, "targets": [{"tenantId": "default", "domain": "synthetic.invalid", "serverNames": ["dc.synthetic.invalid"], "cidrs": [str(ip) + "/32"]}]}
        (root / "egress.json").write_text(json.dumps(policy))
        env.update(ADTR_DOMAIN_KEY_ID="synthetic-e2e", ADTR_DOMAIN_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
                   ADTR_DOMAIN_PROBE_ENABLED="true", ADTR_LDAP_CA_FILE=str(root / "ca.crt"),
                   ADTR_LDAP_EGRESS_POLICY_FILE=str(root / "egress.json"), ADTR_E2E_LDAP_IP=str(ip),
                   ADTR_E2E_LDAP_USERNAME=username, ADTR_E2E_LDAP_PASSWORD=password)
        for suffix, enabled in (("MODE", self.directory_enabled),
                                ("EMPTY", self.directory_empty), ("SLOW", self.directory_slow),
                                ("COMPUTER_EXPORT_FAILURE_V3", self.computer_export_failure_v3), ("GROUP_EXPORT_FAILURE_V3", self.group_export_failure_v3), ("USER_EXPORT_FAILURE_V3", self.user_export_failure_v3), ("GROUP_MEMBERS_V4_REFRESH", self.group_members_v4_refresh), ("GROUP_MEMBERS_V4_PAGINATION", self.group_members_v4_pagination), ("V4", self.directory_v4), ("V3", self.directory_v3), ("V2", self.directory_v2), ("USER_ASSETS_V2", self.user_assets_v2), ("COMPUTER_ASSETS_V3", self.computer_assets_v3), ("GROUP_ASSETS_V3", self.group_assets_v3)):
            key = "ADTR_E2E_LDAP_DIRECTORY_" + suffix
            if enabled:
                env[key] = "true"
            else:
                # An inherited marker must not misrepresent this fixture's mode.
                env.pop(key, None)
        if self.control_path is not None:
            env["ADTR_E2E_LDAP_CONTROL_DIR"] = str(self.control_path)
        if self.empty_transition_path is not None:
            env["ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR"] = str(self.empty_transition_path)
        else:
            env.pop("ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR", None)

    def close(self):
        if self.user_uac_v3_profile:
            self._close_uac()
            return
        error = None
        try:
            if self.created_container:
                subprocess.run(["docker", "stop", "--time", "10", self.name], check=True, capture_output=True, timeout=20)
                code = subprocess.check_output(["docker", "inspect", "-f", "{{.State.ExitCode}}", self.name], text=True, timeout=5).strip()
                if code != "0":
                    error = RuntimeError("synthetic LDAP fixture did not stop gracefully")
        finally:
            try:
                if self.created_container:
                    subprocess.run(["docker", "rm", "--force", self.name], check=True, capture_output=True, timeout=20)
            finally:
                try:
                    if self.created_network:
                        subprocess.run(["docker", "network", "rm", self.network], check=True, capture_output=True, timeout=20)
                finally:
                    try:
                        if self.directory is not None:
                            self.directory.cleanup()
                    finally:
                        try:
                            if self.control_directory is not None:
                                self.control_directory.cleanup()
                        finally:
                            if self.empty_transition_directory is not None:
                                self.empty_transition_directory.cleanup()
        if error:
            raise error

    def _read_uac_evidence(self):
        # Open only fixed basenames via the owned directory descriptor. NOFOLLOW
        # and NONBLOCK reject symlinks/FIFOs before a read can block.
        root = self.uac_evidence_directory
        if root is None:
            raise ValueError("uac_evidence_invalid:terminal_seal")
        directory = os.open(root.name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            if stat.S_IMODE(os.fstat(directory).st_mode) != 0o700:
                raise ValueError("uac_evidence_invalid:terminal_seal")
            values = []
            for name, cap in (("candidate-wire.json", 163840), ("terminal-seal.json", 8192)):
                descriptor = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
                try:
                    info = os.fstat(descriptor)
                    if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or not 0 < info.st_size <= cap or info.st_nlink != 1:
                        raise ValueError("uac_evidence_invalid:terminal_seal")
                    with os.fdopen(descriptor, "rb", closefd=False) as stream:
                        data = stream.read(cap + 1)
                    if len(data) != info.st_size or len(data) > cap:
                        raise ValueError("uac_evidence_invalid:terminal_seal")
                    values.append(data)
                finally:
                    os.close(descriptor)
        finally:
            os.close(directory)
        def unique(pairs):
            result = {}
            for key, value in pairs:
                if key in result:
                    raise ValueError("uac_evidence_invalid:terminal_seal")
                result[key] = value
            return result
        def invalid(_):
            raise ValueError("uac_evidence_invalid:terminal_seal")
        candidate, seal = (json.loads(value.decode("utf-8"), object_pairs_hook=unique, parse_constant=invalid) for value in values)
        for value in (candidate, seal):
            if type(value) is not dict or type(value.get("format")) is not int or value["format"] != 1 or value.get("profile") != self.user_uac_v3_profile or value.get("nonce") != self._uac_nonce:
                raise ValueError("uac_evidence_invalid:terminal_seal")
        seal_keys = {"format", "profile", "nonce", "candidateTranscriptSHA256", "claimCount", "completedExchangeCount", "directoryAttemptCount", "pageCount", "entryCount", "activeHandlers", "inflightWrites", "listenersClosed", "participantClosed", "poisoned", "poisonCode", "lateDirectoryAttempts", "participantRebinds", "terminalState", "completionSequence", "finalSequence"}
        candidate_keys = {"format", "profile", "nonce", "transport", "claimOrdinal", "entryCount", "pageCount", "completionSequence", "pages"}
        if set(seal) != seal_keys or set(candidate) != candidate_keys or seal["candidateTranscriptSHA256"] != hashlib.sha256(values[0]).hexdigest():
            raise ValueError("uac_evidence_invalid:terminal_seal")
        for key, expected in (("claimCount", 1), ("completedExchangeCount", 1), ("directoryAttemptCount", 4), ("pageCount", 4), ("entryCount", 51), ("activeHandlers", 0), ("inflightWrites", 0), ("lateDirectoryAttempts", 0), ("participantRebinds", 0)):
            if type(seal[key]) is not int or seal[key] != expected:
                raise ValueError("uac_evidence_invalid:terminal_seal")
        if seal["listenersClosed"] is not True or seal["participantClosed"] is not True or seal["poisoned"] is not False or seal["poisonCode"] != "none" or seal["terminalState"] != "sealed_ok":
            raise ValueError("uac_evidence_invalid:terminal_seal")
        if type(candidate["completionSequence"]) is not int or type(seal["completionSequence"]) is not int or type(seal["finalSequence"]) is not int or not 0 < candidate["completionSequence"] == seal["completionSequence"] <= seal["finalSequence"]:
            raise ValueError("uac_evidence_invalid:terminal_seal")
        return values[0], values[1], 0

    def _close_uac(self):
        if self._uac_closed:
            if self._uac_close_failed:
                raise RuntimeError("uac_evidence_invalid:cleanup") from None
            return
        self._retained_uac_evidence = None
        retained = None
        failed = False
        # Each cleanup is attempted even if an earlier stop/read/remove failed.
        # Keep the existing 10/20/5-second limits; no new retry or wait budget.
        try:
            if self.created_container:
                subprocess.run(["docker", "stop", "--time", "10", self.name], check=True, capture_output=True, timeout=20)
                code = subprocess.check_output(["docker", "inspect", "-f", "{{.State.ExitCode}}", self.name], text=True, timeout=5).strip()
                if code != "0":
                    raise ValueError("uac_evidence_invalid:cleanup")
                retained = self._read_uac_evidence()
            elif self._uac_nonce is not None:
                failed = True
        except BaseException:
            failed = True
        operations = []
        if self.created_container:
            operations.append(lambda: subprocess.run(["docker", "rm", "--force", self.name], check=True, capture_output=True, timeout=20))
        if self.created_network:
            operations.append(lambda: subprocess.run(["docker", "network", "rm", self.network], check=True, capture_output=True, timeout=20))
        operations.extend(directory.cleanup for directory in (self.directory, self.control_directory, self.empty_transition_directory, self.uac_evidence_directory) if directory is not None)
        for operation in operations:
            try:
                operation()
            except BaseException:
                failed = True
        self._uac_closed = True
        if failed:
            self._uac_close_failed = True
            raise RuntimeError("uac_evidence_invalid:cleanup") from None
        self._retained_uac_evidence = retained
