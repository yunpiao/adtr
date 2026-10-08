"""Fail-closed plan and redacted evidence contract; never provisions resources.

The eventual provider adapter must enforce network isolation and own resource
IDs, not infer safety from a caller-supplied private address or a DNS name.
"""
from __future__ import annotations
import argparse
import json
import re
from datetime import datetime, timezone, timedelta
from pathlib import Path

CASES = frozenset({"domain_join", "readonly_write_denied", "ldaps", "starttls",
    "fixture_values", "bad_credential", "disabled_account", "wrong_tls_identity",
    "revoked_authority", "cancelled", "untrusted_ca"})


def validate_plan(plan: dict, now: datetime | None = None) -> dict:
    now = now or datetime.now(timezone.utc)
    if type(plan) is not dict or set(plan) != {"schema", "lab_id", "source_sha", "created_at", "expires_at", "network", "machines"}:
        raise ValueError("unexpected plan fields")
    if type(plan["schema"]) is not int or plan["schema"] != 1 or not re.fullmatch(r"[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}", plan["lab_id"]):
        raise ValueError("invalid lab identity")
    if not re.fullmatch(r"[a-f0-9]{40}", plan["source_sha"]):
        raise ValueError("a full immutable source SHA is required")
    created = datetime.fromisoformat(plan["created_at"])
    expiry = datetime.fromisoformat(plan["expires_at"])
    if created.tzinfo is None or expiry.tzinfo is None:
        raise ValueError("timestamps require timezone")
    if not (created <= now < expiry <= created + timedelta(hours=2)):
        raise ValueError("lab lease missing, expired, or excessive")
    if type(plan["network"]) is not dict:
        raise ValueError("network must be a fixed object")
    if type(plan["network"].get("internet")) is not bool or type(plan["network"].get("production_routes")) is not bool:
        raise ValueError("network decisions must be explicit booleans")
    if plan["network"] != {"cidr": "192.168.77.0/24", "internet": False, "production_routes": False}:
        raise ValueError("isolated fixed network required")
    if plan["machines"] != [
        {"name": "dc01", "address": "192.168.77.10", "role": "domain-controller"},
        {"name": "member01", "address": "192.168.77.20", "role": "domain-member"}]:
        raise ValueError("unexpected lab topology")
    return plan


def redact_report(plan: dict, results: dict, cleanup_verified: bool) -> dict:
    """Whitelist only booleans/statuses; never carry raw logs/errors/identities."""
    if not re.fullmatch(r"[a-f0-9]{40}", plan["source_sha"]) or not re.fullmatch(r"[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}", plan["lab_id"]):
        raise ValueError("invalid report provenance")
    if set(results) != CASES or any(type(v) is not bool for v in results.values()):
        raise ValueError("complete boolean case evidence required")
    if type(cleanup_verified) is not bool:
        raise ValueError("cleanup verification must be explicit")
    return {"schema": 1, "source_sha": plan["source_sha"], "lab_id": plan["lab_id"],
            "evidence_kind": "real-ad-ds-transport", "cases": dict(sorted(results.items())),
            "cleanup_verified": cleanup_verified,
            "passed": all(results.values()) and cleanup_verified,
            "full_product_acceptance": False}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("plan", type=Path)
    args = parser.parse_args()
    try:
        validate_plan(json.loads(args.plan.read_text(encoding="utf-8")))
    except (ValueError, TypeError, KeyError, OSError):
        raise SystemExit("invalid lab plan") from None
    print("lab plan contract validated; no resources created")

if __name__ == "__main__":
    main()
