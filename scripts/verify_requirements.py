"""Fail CI if the complete 209-feature source mapping is reduced or duplicated."""
import json
from pathlib import Path
import re


def require(valid, message):
    # This gate must be identical when Python is run with optimization.
    if not valid:
        raise SystemExit(f"Requirements: {message}")


REPO_URL = "https://github.com/yunpiao/adtr"
ISSUE_URL = re.compile(re.escape(REPO_URL) + r"/issues/[1-9][0-9]*")
PR_URL = re.compile(re.escape(REPO_URL) + r"/pull/[1-9][0-9]*")
CI_URL = re.compile(re.escape(REPO_URL) + r"/actions/runs/[1-9][0-9]*")
COMMIT_SHA = re.compile(r"[a-f0-9]{40}")


def matches(pattern, value):
    return isinstance(value, str) and pattern.fullmatch(value) is not None


def clean_notes(value):
    return isinstance(value, list) and all(isinstance(note, str) and note.strip() for note in value)


def check_evidence(feature):
    requirement = feature["id"]
    evidence = feature.get("evidence")
    require(isinstance(evidence, dict), f"{requirement}: missing implementation evidence")
    require(matches(COMMIT_SHA, evidence.get("commit")), f"{requirement}: invalid evidence commit")
    require(matches(PR_URL, evidence.get("pr")), f"{requirement}: invalid evidence PR")
    require(matches(CI_URL, evidence.get("ci")), f"{requirement}: invalid evidence CI run")
    verified = evidence.get("verified")
    require(clean_notes(verified) and bool(verified), f"{requirement}: invalid verified evidence")
    remaining = evidence.get("remaining")
    require(clean_notes(remaining), f"{requirement}: invalid remaining gates")
    if feature["product_accepted"]:
        require(not remaining, f"{requirement}: unresolved acceptance gates")
    else:
        require(bool(remaining), f"{requirement}: verified slice needs remaining acceptance gates")


def main():
    ledger = json.loads(Path("requirements/catalog.json").read_text(encoding="utf-8"))
    features = ledger["features"]
    expected = {f"AD-F-{index:03d}" for index in range(1, 210)}
    actual = [feature["id"] for feature in features]
    require(len(actual) == len(set(actual)) == 209, "requirement duplication or scope reduction")
    require(set(actual) == expected, "missing or invented source requirement")
    require(len({feature["group"] for feature in features}) == 57, "feature-group coverage changed")
    rows = []
    for feature in features:
        row = feature["source_row"]
        require(
            type(row) is int and row > 1 and feature["source_sheet"] == "AD 功能清单",
            "invalid workbook source row or sheet",
        )
        rows.append(row)
    require(len(rows) == len(set(rows)), "duplicate workbook source row")
    issue_by_group = {}
    group_by_issue = {}
    for feature in features:
        group, issue = feature["group"], feature["issue"]
        require(matches(ISSUE_URL, issue), f"{feature['id']}: invalid requirement issue link")
        require(
            issue_by_group.setdefault(group, issue) == issue,
            f"{group}: feature group maps to multiple issues",
        )
        require(
            group_by_issue.setdefault(issue, group) == group,
            f"{issue}: feature issue maps to multiple groups",
        )
    field_ids = [record for feature in features for record in feature["field_records"]]
    unique_fields = set(field_ids)
    require(len(field_ids) == len(unique_fields), "duplicate field record")
    require(unique_fields == {f"FLD-{index:04d}" for index in range(1, 3259)}, "field mapping reduced")
    # Shared UI controls may legitimately be referenced by multiple features.
    ui_ids = {record for feature in features for record in feature["ui_records"]}
    require(ui_ids == {f"UI-{index:04d}" for index in range(1, 805)}, "UI mapping reduced")
    require(re.fullmatch(r"[a-f0-9]{64}", ledger["source_workbook"]["sha256"]), "invalid workbook SHA-256")
    allowed = {"not_started", "in_progress", "implemented_verified_slice", "accepted"}
    for feature in features:
        require(feature["status"] in allowed, "invalid requirement status")
        require(
            type(feature["product_accepted"]) is bool
            and feature["product_accepted"] == (feature["status"] == "accepted"),
            f"{feature['id']}: inconsistent acceptance status",
        )
        if feature["status"] in {"implemented_verified_slice", "accepted"}:
            check_evidence(feature)
    accepted_count = ledger["scope"]["product_accepted"]
    require(
        type(accepted_count) is int and accepted_count == sum(f["product_accepted"] for f in features),
        "inconsistent total product acceptance",
    )
    print("Requirements: 209 unique AD-F records, 57 groups, 3258 fields, 804 controls, source rows and evidence boundaries verified")


if __name__ == "__main__":
    main()
