"""Bounded reconciliation of only the disposable container created by this run."""
import re
import subprocess

OWNER_LABEL = "adtr.e2e.owner"


def owned_container_id(name, owner, run=subprocess.run):
    if not re.fullmatch(r"adtr-auth-e2e-[0-9a-f]{12}", name) or not re.fullmatch(r"[0-9a-f]{32}", owner):
        raise ValueError("invalid owned test container identity")
    result = run(["docker", "ps", "--all", "--no-trunc", "--filter", f"name={name}",
                  "--filter", f"label={OWNER_LABEL}={owner}", "--format", "{{.ID}} {{.Names}}"],
                 check=True, capture_output=True, text=True, timeout=10)
    matches = []
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1] == name:
            if not re.fullmatch(r"[0-9a-f]{64}", fields[0]):
                raise RuntimeError("invalid owned test container result")
            matches.append(fields[0])
    if len(matches) > 1:
        raise RuntimeError("ambiguous owned test container result")
    return matches[0] if matches else None


def remove_owned_container(name, owner, run=subprocess.run):
    # A timed-out create can already have succeeded. Reconcile the ownership
    # label and exact name, then use immutable ID so name replacement is safe.
    identity = owned_container_id(name, owner, run)
    if identity is None:
        return
    try:
        run(["docker", "rm", "--force", identity], check=True, capture_output=True, timeout=30)
    except (subprocess.SubprocessError, OSError):
        # A timed-out removal can likewise have completed. Never retry a write
        # blindly or silently report an unverified cleanup as successful.
        if owned_container_id(name, owner, run) is not None:
            raise RuntimeError("owned test database cleanup remains incomplete")
