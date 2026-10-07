"""Fail CI if the complete 209-feature source mapping is reduced or duplicated."""
import json
from pathlib import Path
import re


def main():
    ledger = json.loads(Path("requirements/catalog.json").read_text())
    features = ledger["features"]
    expected = {f"AD-F-{index:03d}" for index in range(1, 210)}
    actual = [feature["id"] for feature in features]
    assert len(actual) == len(set(actual)) == 209, "requirement duplication or scope reduction"
    assert set(actual) == expected, "missing or invented source requirement"
    assert len({feature["group"] for feature in features}) == 57, "feature-group coverage changed"
    assert all(feature["source_row"] > 1 and feature["source_sheet"] == "AD 功能清单" for feature in features)
    assert all(re.fullmatch(r"https://github.com/yunpiao/adtr/issues/\d+", feature["issue"]) for feature in features)
    field_ids = {record for feature in features for record in feature["field_records"]}
    ui_ids = {record for feature in features for record in feature["ui_records"]}
    assert field_ids == {f"FLD-{index:04d}" for index in range(1, 3259)}, "field mapping reduced"
    assert ui_ids == {f"UI-{index:04d}" for index in range(1, 805)}, "UI mapping reduced"
    assert re.fullmatch(r"[a-f0-9]{64}", ledger["source_workbook"]["sha256"])
    allowed = {"not_started", "in_progress", "implemented_verified_slice", "accepted"}
    for feature in features:
        assert feature["status"] in allowed
        assert feature["product_accepted"] == (feature["status"] == "accepted")
        if feature["status"] in {"implemented_verified_slice", "accepted"}:
            evidence = feature["evidence"]
            assert re.fullmatch(r"[a-f0-9]{40}", evidence["commit"])
            assert evidence["pr"] and evidence["ci"] and evidence["verified"]
            if feature["product_accepted"]:
                assert not evidence["remaining"], "unresolved acceptance gates"
    assert ledger["scope"]["product_accepted"] == sum(feature["product_accepted"] for feature in features)
    print("Requirements: 209 unique AD-F records, 57 groups, 3258 fields, 804 controls, source rows and evidence boundaries verified")


if __name__ == "__main__":
    main()
