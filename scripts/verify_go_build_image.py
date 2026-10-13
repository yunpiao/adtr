"""Verify the one fixed Go build image through normal public Docker commands.

No login, credential handling, source fallback or container execution. Docker
verifies downloaded layer digests; this is not an independent layer-byte audit.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys


REPOSITORY = "docker.io/library/golang"
INDEX = "sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414"
REFERENCE = REPOSITORY + ":1.27.1-alpine@" + INDEX
MANIFEST = "sha256:cd9a32216aee5667f957a62d13a10032a63fd58e14b3f3d9cc8c2122f501e95e"
CONFIG = "sha256:340a7d186a598086366ea5264e8c6e74e4dee36dc2d70ac0f621b6b28930a786"
INDEX_TYPE = "application/vnd.oci.image.index.v1+json"
MANIFEST_TYPE = "application/vnd.oci.image.manifest.v1+json"
CONFIG_TYPE = "application/vnd.oci.image.config.v1+json"
LAYER_TYPE = "application/vnd.oci.image.layer.v1.tar+gzip"


class ImageVerificationError(RuntimeError):
    pass


def require(value, message):
    if not value:
        raise ImageVerificationError(message)


def digest(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def object_json(raw):
    try:
        value = json.loads(raw)
    except (ValueError, UnicodeError) as error:
        raise ImageVerificationError("Invalid image metadata JSON") from error
    require(isinstance(value, dict), "Expected image metadata object")
    return value


def validate_index(raw):
    require(isinstance(raw, bytes) and digest(raw) == INDEX, "Fixed index bytes mismatch")
    value = object_json(raw)
    require(value.get("schemaVersion") == 2 and value.get("mediaType") == INDEX_TYPE,
            "Unexpected index schema")
    entries = value.get("manifests")
    require(isinstance(entries, list) and all(isinstance(x, dict) for x in entries),
            "Invalid index descriptors")
    candidates = [x for x in entries if x.get("platform") == {"os": "linux", "architecture": "amd64"}]
    require(len(candidates) == 1, "Expected one exact linux/amd64 descriptor")
    child = candidates[0]
    require(child.get("digest") == MANIFEST and child.get("mediaType") == MANIFEST_TYPE
            and type(child.get("size")) is int and child["size"] == 1921,
            "Fixed platform descriptor mismatch")


def validate_manifest(raw):
    require(isinstance(raw, bytes) and len(raw) == 1921 and digest(raw) == MANIFEST,
            "Fixed manifest bytes mismatch")
    value = object_json(raw)
    require(value.get("schemaVersion") == 2 and value.get("mediaType") == MANIFEST_TYPE,
            "Unexpected manifest schema")
    config = value.get("config")
    require(isinstance(config, dict) and config.get("digest") == CONFIG
            and config.get("mediaType") == CONFIG_TYPE
            and type(config.get("size")) is int and config["size"] == 2187,
            "Fixed config descriptor mismatch")
    layers = value.get("layers")
    require(isinstance(layers, list) and len(layers) == 5 and all(
        isinstance(x, dict) and x.get("mediaType") == LAYER_TYPE
        and re.fullmatch(r"sha256:[0-9a-f]{64}", str(x.get("digest", "")))
        and type(x.get("size")) is int and x["size"] > 0 for x in layers),
        "Invalid layer descriptors")
    require(sum(x["size"] for x in layers) == 75175309, "Fixed layer size mismatch")
    return layers


def command(args, timeout, run):
    try:
        result = run(args, capture_output=True, check=False, timeout=timeout)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ImageVerificationError("Docker verification command unavailable or timed out") from error
    # Never log arbitrary Docker stderr, which can include authentication data.
    if result.returncode:
        error_text = (result.stderr or b"").decode("utf-8", errors="replace").lower()
        category = "failed"
        if "429" in error_text or "toomanyrequests" in error_text:
            category = "registry throttled"
        if any(x in error_text for x in ("unauthorized", "denied", "forbidden", "certificate", "x509")):
            category = "authorization or TLS rejected"
        raise ImageVerificationError("Docker verification command " + category)
    require(isinstance(result.stdout, bytes), "Docker output must remain raw bytes")
    return result.stdout


def verify(run=subprocess.run):
    # Binary stdout is deliberately hashed unchanged: no strip/decode/re-encode.
    index = command(["docker", "buildx", "imagetools", "inspect", "--raw", REFERENCE], 60, run)
    validate_index(index)
    manifest = command(["docker", "buildx", "imagetools", "inspect", "--raw",
                        REPOSITORY + "@" + MANIFEST], 60, run)
    layers = validate_manifest(manifest)
    command(["docker", "pull", "--platform=linux/amd64", REFERENCE], 180, run)
    raw = command(["docker", "image", "inspect", REFERENCE], 15, run)
    try:
        images = json.loads(raw)
    except (ValueError, UnicodeError) as error:
        raise ImageVerificationError("Invalid Docker image inspection") from error
    require(isinstance(images, list) and len(images) == 1 and isinstance(images[0], dict),
            "Expected one inspected image")
    image = images[0]
    require(image.get("Id") == CONFIG and image.get("Os") == "linux"
            and image.get("Architecture") == "amd64", "Loaded image identity mismatch")
    # Docker commonly normalizes docker.io/library/golang to golang.
    names = {REPOSITORY + "@" + INDEX, "golang@" + INDEX}
    repo_digests = image.get("RepoDigests")
    require(isinstance(repo_digests, list) and any(x in names for x in repo_digests),
            "Loaded image repository digest mismatch")
    report = {"reference": REFERENCE, "index_digest": digest(index),
              "manifest_digest": digest(manifest), "config_image_id": CONFIG,
              "platform": "linux/amd64", "layers": layers,
              "layer_validation": "Docker pull content-address verification",
              "independent_layer_byte_audit": False}
    return index, manifest, report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence-dir", type=Path, required=True)
    args = parser.parse_args()
    try:
        index, manifest, report = verify()
        args.evidence_dir.mkdir(parents=True, exist_ok=True)
        (args.evidence_dir / "index.json").write_bytes(index)
        (args.evidence_dir / "manifest.json").write_bytes(manifest)
        (args.evidence_dir / "identity.json").write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps(report, sort_keys=True))
    except (ImageVerificationError, OSError) as error:
        print("Go build image verification failed: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
