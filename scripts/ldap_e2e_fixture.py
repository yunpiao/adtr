"""Owned synthetic LDAP/TLS fixture for CI; never contacts a real directory."""
import base64
import ipaddress
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
                 directory_empty=False, directory_slow=False, *, directory_v2=False, user_assets_v2=False, directory_v3=False, directory_v4=False, computer_assets_v3=False, group_assets_v3=False, empty_transition=False, group_members_v4_pagination=False):
        if any(not isinstance(value, bool) for value in (directory_enabled, directory_empty, directory_slow, directory_v2, user_assets_v2, directory_v3, directory_v4, computer_assets_v3, group_assets_v3, empty_transition, group_members_v4_pagination)):
            raise TypeError("synthetic directory modes must be booleans")
        if empty_transition and (not directory_enabled or not directory_v3 or
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
        if directory_v4 and (not directory_enabled or directory_v2 or directory_v3 or user_assets_v2 or computer_assets_v3 or group_assets_v3 or empty_transition or control_enabled):
            raise ValueError("synthetic dictionary 4 requires exclusive directory mode")
        self.directory_v4 = directory_v4
        if group_members_v4_pagination and (not directory_enabled or not directory_v4 or directory_empty or directory_slow):
            raise ValueError("group member pagination requires exclusive nonempty fast dictionary4 mode")
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
            self.close()
            raise

    @property
    def control_path(self):
        return Path(self.control_directory.name) if self.control_directory else None

    @property
    def empty_transition_path(self):
        return Path(self.empty_transition_directory.name) if self.empty_transition_directory else None

    def start(self, env):
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
        subprocess.run(["docker", "network", "create", "--internal", self.network], check=True, capture_output=True, timeout=30)
        self.created_network = True
        username = "fixture@synthetic.invalid"
        password = secrets.token_urlsafe(24)
        fixture_env = dict(os.environ, ADTR_LDAP_FIXTURE_USERNAME=username, ADTR_LDAP_FIXTURE_PASSWORD=password)
        command = ["docker", "run", "--detach", "--name", self.name, "--network", self.network,
                        "--user", f"{os.getuid()}:{os.getgid()}",
                        "--entrypoint", "/fixture/ldap-fixture", "-v", f"{root}:/fixture:ro",
                        "-e", "ADTR_LDAP_FIXTURE_USERNAME", "-e", "ADTR_LDAP_FIXTURE_PASSWORD"]
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
        if self.directory_enabled:
            command.append("-directory-mode")
        if self.directory_v4:
            command.append("-directory-v4")
        if self.group_members_v4_pagination:
            command.append("-group-members-v4-pagination")
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
                                ("GROUP_MEMBERS_V4_PAGINATION", self.group_members_v4_pagination), ("V4", self.directory_v4), ("V3", self.directory_v3), ("V2", self.directory_v2), ("USER_ASSETS_V2", self.user_assets_v2), ("COMPUTER_ASSETS_V3", self.computer_assets_v3), ("GROUP_ASSETS_V3", self.group_assets_v3)):
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
