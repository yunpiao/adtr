"""Fail CI if the complete 209-feature source mapping is reduced or duplicated."""
import hashlib
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
# G01 through G07 are separate roadmap gates, not business feature groups.
ROADMAP_RELEASE_GATE_COUNT = 7
FROZEN_MAPPING_SHA256 = "8cd9614e1d158721da653d99252c7d742f054563a5e05eaa9d17a9f650c129ec"

# Preserve the checked-in workbook descriptors separately from delivery evidence.
DESCRIPTOR_KEYS = ("module", "name", "description", "priority")
FROZEN_DESCRIPTORS_SHA256 = "0fe2be3db640b08830d00ca2b7e1c549eecdfaf0bfe9cfde21eadcfdefab082b"


def matches(pattern, value):
    return isinstance(value, str) and pattern.fullmatch(value) is not None


def clean_notes(value):
    return isinstance(value, list) and all(isinstance(note, str) and note.strip() for note in value)


def frozen_mapping_sha256(workbook_sha, features):
    """Bind relationships, not JSON formatting or mutable acceptance evidence."""
    rows = sorted(
        [
            f["id"], f["group"], f["issue"], f["source_sheet"], f["source_row"],
            sorted(f["field_records"]), sorted(set(f["ui_records"])),
        ]
        for f in features
    )
    encoded = json.dumps([workbook_sha, rows], ensure_ascii=False, separators=(",", ":"))
    return hashlib.sha256(encoded.encode("utf-8")).hexdigest()


def check_descriptors(workbook, features):
    name = workbook.get("name")
    require(isinstance(name, str) and name.strip(), "invalid workbook name")
    rows = []
    for f in features:
        row = [f["id"]]
        for key in DESCRIPTOR_KEYS:
            value = f.get(key)
            require(
                isinstance(value, str) and value.strip(),
                f"{f['id']}: invalid requirement descriptor {key}",
            )
            row.append(value)
        rows.append(row)
    encoded = json.dumps([name, sorted(rows)], ensure_ascii=False, separators=(",", ":"))
    require(
        hashlib.sha256(encoded.encode("utf-8")).hexdigest() == FROZEN_DESCRIPTORS_SHA256,
        "frozen requirement descriptors changed",
    )


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
    groups = {feature["group"] for feature in features}
    require(len(groups) == 57, "feature-group coverage changed")
    scope = ledger.get("scope")
    require(isinstance(scope, dict), "invalid ledger scope")
    for key, actual_count, label in (
        ("requirements", len(features), "requirement"),
        ("feature_groups", len(groups), "feature-group"),
        ("release_gates", ROADMAP_RELEASE_GATE_COUNT, "release-gate"),
    ):
        declared_count = scope.get(key)
        require(
            type(declared_count) is int and declared_count == actual_count,
            f"inconsistent declared {label} count",
        )
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
    workbook = ledger.get("source_workbook")
    require(isinstance(workbook, dict), "invalid source workbook")
    workbook_sha = workbook.get("sha256")
    require(matches(re.compile(r"[a-f0-9]{64}"), workbook_sha), "invalid workbook SHA-256")
    require(
        frozen_mapping_sha256(workbook_sha, features) == FROZEN_MAPPING_SHA256,
        "frozen workbook mapping changed",
    )
    check_descriptors(workbook, features)
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
    accepted_count = scope.get("product_accepted")
    require(
        type(accepted_count) is int and accepted_count == sum(f["product_accepted"] for f in features),
        "inconsistent total product acceptance",
    )
    print("Requirements: 209 unique AD-F records, 57 groups, 3258 fields, 804 controls, source rows and evidence boundaries verified")


if __name__ == "__main__":
    main()
