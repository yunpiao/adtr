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
                 directory_empty=False, directory_slow=False, *, directory_v2=False, user_assets_v2=False):
        if any(not isinstance(value, bool) for value in (directory_enabled, directory_empty, directory_slow, directory_v2, user_assets_v2)):
            raise TypeError("synthetic directory modes must be booleans")
        if directory_v2 and not directory_enabled:
            raise ValueError("synthetic dictionary 2 requires directory mode")
        if (directory_empty or directory_slow) and not directory_enabled:
            raise ValueError("synthetic empty and slow modes require directory mode")
        if user_assets_v2 and (not directory_enabled or not directory_v2 or directory_empty or directory_slow):
            raise ValueError("synthetic user assets require nonempty fast directory-v2 mode")
        self.user_assets_v2 = user_assets_v2
        self.directory_enabled = directory_enabled
        self.directory_v2 = directory_v2
        self.directory_empty = directory_empty
        self.directory_slow = directory_slow
        self.name = "adtr-ldap-e2e-" + uuid.uuid4().hex[:12]
        self.network = self.name + "-net"
        self.directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-e2e-")
        self.control_directory = tempfile.TemporaryDirectory(prefix="adtr-ldap-control-") if control_enabled else None
        self.created_network = False
        self.created_container = False

    @property
    def control_path(self):
        return Path(self.control_directory.name) if self.control_directory else None

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
        command.extend([*postgres_image_args(IMAGE),
                        "-cert", "/fixture/server.crt", "-key", "/fixture/server.key",
                        "-starttls-listen", "0.0.0.0:389", "-ldaps-listen", "0.0.0.0:636"])
        if self.control_path is not None:
            command.extend(["-control-dir", "/control"])
        if self.directory_enabled:
            command.append("-directory-mode")
        if self.directory_v2:
            command.append("-directory-v2")
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
                                ("V2", self.directory_v2), ("USER_ASSETS_V2", self.user_assets_v2)):
            key = "ADTR_E2E_LDAP_DIRECTORY_" + suffix
            if enabled:
                env[key] = "true"
            else:
                # An inherited marker must not misrepresent this fixture's mode.
                env.pop(key, None)
        if self.control_path is not None:
            env["ADTR_E2E_LDAP_CONTROL_DIR"] = str(self.control_path)

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
                    self.directory.cleanup()
                    if self.control_directory:
                        self.control_directory.cleanup()
        if error:
            raise error
