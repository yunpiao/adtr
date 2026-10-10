"""Prepare the existing fixed PostgreSQL image with bounded, fail-closed retries.

This helper performs no registry fallback or authentication changes. It is also
usable by an eventual same-run artifact producer; it does not implement image
sharing and must not be treated as proof that Docker save/load retains digests.
"""
import json
import re
import subprocess
import sys
import time

from test_integration import IMAGE

# Independently byte-verified linux/amd64 config in container-image-provenance.md.
EXPECTED_IMAGE_ID = "sha256:d741b376874687de90374fd34f55c6b2760e8f7bd7e4ae5cd47f50757fc08cf8"
EXPECTED_REFERENCE = (
    "public.ecr.aws/docker/library/postgres:17.6-alpine@sha256:"
    "ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"
)
RETRY_DELAYS = (10, 30)
PULL_TIMEOUT_SECONDS = 180
INSPECT_TIMEOUT_SECONDS = 15


class ImagePreparationError(RuntimeError):
    pass


def diagnostic(result):
    def text(value):
        return value.decode(errors="replace") if isinstance(value, bytes) else value or ""
    return "\n".join(filter(None, (text(result.stdout), text(result.stderr))))


def forbidden_failure(message):
    """Identity/auth/TLS failures always take priority over transient signals."""
    lowered = message.lower()
    forbidden = (
        "unauthorized", "authentication required", "access denied", "permission denied",
        "requested access", "forbidden", "denied:", "digest mismatch", "digest did not match",
        "manifest unknown", "manifest invalid", "no matching manifest", "not found",
        "certificate", "x509", "tls verification", "unsupported", "invalid reference",
    )
    return any(word in lowered for word in forbidden)


def retryable(message):
    """Only identified throttling or transient network diagnostics may retry."""
    lowered = message.lower()
    if forbidden_failure(message):
        return False
    transient = (
        "toomanyrequests", "too many requests", "tls handshake timeout", "i/o timeout",
        "connection reset by peer", "temporary failure in name resolution",
        "temporary failure resolving", "network is unreachable", "connection timed out",
    )
    return (any(word in lowered for word in transient)
            or re.search(r"\b(?:http(?:/\S+)?\s+|status(?: code)?[=: ]+)429\b", lowered) is not None)


def inspect_image(run=subprocess.run):
    if IMAGE != EXPECTED_REFERENCE:
        raise ImagePreparationError("PostgreSQL source/tag/digest changed; review the fixed policy first")
    try:
        result = run(["docker", "image", "inspect", IMAGE], capture_output=True,
                     text=True, timeout=INSPECT_TIMEOUT_SECONDS, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ImagePreparationError(f"Docker image inspect failed: {error}") from error
    if result.returncode:
        message = diagnostic(result)
        # Only the daemon's exact missing-image form permits a pull. In
        # particular, daemon access errors must never be retried as a miss.
        if re.fullmatch(r"(?:Error response from daemon: )?No such (?:image|object): .+\s*",
                        result.stderr.strip(), flags=re.IGNORECASE) and result.stdout.strip() in ("", "[]"):
            return None
        raise ImagePreparationError(f"Docker image inspect failed ({result.returncode}): {message}")
    try:
        images = json.loads(result.stdout)
        if not isinstance(images, list) or len(images) != 1 or not isinstance(images[0], dict):
            raise ValueError("expected exactly one inspected image")
        image = images[0]
        repository, digest = EXPECTED_REFERENCE.split("@")
        expected_digest = repository.rsplit(":", 1)[0] + "@" + digest
        if image.get("Id") != EXPECTED_IMAGE_ID:
            raise ValueError("fixed linux/amd64 image config digest mismatch")
        if image.get("Os") != "linux" or image.get("Architecture") != "amd64":
            raise ValueError("expected linux/amd64 image platform")
        digests = image.get("RepoDigests")
        if not isinstance(digests, list) or expected_digest not in digests:
            raise ValueError("fixed repository digest missing or mismatched")
        return image
    except (ValueError, TypeError) as error:
        raise ImagePreparationError(f"Docker image identity validation failed: {error}") from error


def prepare_image(run=subprocess.run, pause=time.sleep):
    for attempt in range(len(RETRY_DELAYS) + 1):
        # Recheck before every pull, including after backoff, so a completed
        # previous operation is never repeated solely because its client failed.
        image = inspect_image(run)
        if image is not None:
            return image
        print(f"Pull fixed PostgreSQL image, attempt {attempt + 1}/{len(RETRY_DELAYS) + 1}", flush=True)
        timed_out = False
        try:
            result = run(["docker", "pull", "--platform=linux/amd64", IMAGE],
                         capture_output=True, text=True, timeout=PULL_TIMEOUT_SECONDS, check=False)
            message = diagnostic(result)
        except subprocess.TimeoutExpired as error:
            result = error
            message = diagnostic(error)
            timed_out = True
        except OSError as error:
            raise ImagePreparationError(f"Docker image pull could not start: {error}") from error
        if message:
            print(message, flush=True)
        if not timed_out and result.returncode == 0:
            image = inspect_image(run)
            if image is None:
                raise ImagePreparationError("Docker reported pull success but the fixed image is absent")
            return image
        # A client deadline is a known timeout even after normal progress.
        # Explicit auth/identity/TLS errors retain priority over that timeout.
        if not (retryable(message) or (timed_out and not forbidden_failure(message))):
            raise ImagePreparationError(f"Non-retryable fixed-image pull failure: {message or 'no diagnostic'}")
        image = inspect_image(run)
        if image is not None:
            return image
        if attempt == len(RETRY_DELAYS):
            raise ImagePreparationError("Fixed-image pull exhausted its bounded transient retries")
        delay = RETRY_DELAYS[attempt]
        print(f"Recognized transient pull failure; recheck after {delay}s", flush=True)
        pause(delay)
    raise AssertionError("unreachable image preparation state")


def main():
    try:
        image = prepare_image()
    except ImagePreparationError as error:
        print(str(error), file=sys.stderr)
        return 1
    print(json.dumps({"reference": IMAGE, "image_id": image["Id"],
                      "platform": "linux/amd64", "repo_digests": image["RepoDigests"]}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
