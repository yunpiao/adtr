"""Read actual finite/unconfigured Docker cgroups without changing host limits."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid


def verify_cgroup_memory(compose):
    api = compose("ps", "--all", "--quiet", "api", capture=True).stdout.strip()
    if not api or "\n" in api:
        raise AssertionError("exact existing API container required")
    image = subprocess.check_output(["docker", "inspect", "--format", "{{.Image}}", api], text=True).strip()
    if not image.startswith("sha256:"):
        raise AssertionError("immutable local API image required")
    with tempfile.TemporaryDirectory(prefix="adtr-cgroup-") as directory:
        binary = Path(directory) / "cgroup.test"
        subprocess.run([os.environ.get("GO", "go"), "test", "-c", "-tags=cgroupdocker", "-o", str(binary), "./internal/systemhealth"], env=dict(os.environ, CGO_ENABLED="0"), check=True)
        for limit in ("268435456", "unconfigured"):
            owner = uuid.uuid4().hex
            name = "adtr-cgroup-" + owner
            cid = None
            try:
                command = ["docker", "create", "--name", name, "--label", "adtr.cgroup-test=" + owner, "--network", "none", "--cgroupns", "private", "--read-only", "--mount", f"type=bind,src={binary},dst=/cgroup.test,readonly", "--entrypoint", "/cgroup.test", "-e", "ADTR_TEST_CGROUP_LIMIT=" + limit]
                if limit != "unconfigured":
                    command += ["--memory", limit]
                cid = subprocess.check_output(command + [image, "-test.run=^TestDockerActualCgroupMemory$", "-test.v"], text=True, timeout=30).strip()
                for _ in range(2):
                    subprocess.run(["docker", "start", "--attach", cid], check=True, timeout=60)
                    state = json.loads(subprocess.check_output(["docker", "inspect", "--format", "{{json .State}}", cid], text=True))
                    if state["ExitCode"] != 0 or state["OOMKilled"]:
                        raise AssertionError("actual cgroup test failed")
                print(f"Actual Docker memory {limit}: same-container restart verified", flush=True)
            finally:
                # Also reconcile an uncertain create before cleanup, by unique
                # test label and immutable identity; never remove other data.
                result = subprocess.run(["docker", "inspect", name], capture_output=True, text=True)
                if result.returncode == 0:
                    record = json.loads(result.stdout)[0]
                    if record["Config"]["Labels"].get("adtr.cgroup-test") != owner:
                        raise AssertionError("refusing to clean unowned container")
                    subprocess.run(["docker", "rm", "-f", record["Id"]], check=True)
