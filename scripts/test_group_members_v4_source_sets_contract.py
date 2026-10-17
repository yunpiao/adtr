"""Offline source-list parity; never substitutes for real browser evidence."""
from pathlib import Path
import re
import unittest

import validate_group_members_v4_evidence as primary
import validate_group_members_v4_empty_evidence as empty
import validate_group_members_v4_pagination_evidence as pagination
import validate_group_members_v4_refresh_evidence as refresh


def source_list(text, name, inherited=None):
    marker = f"export const {name} = ["
    if text.count(marker) != 1:
        raise ValueError("source list declaration must be unique")
    body = text.split(marker, 1)[1].split("];", 1)[0]
    paths = []
    for line in body.splitlines():
        line = line.strip()
        if not line:
            continue
        if line == "...emptyMembershipSources," and inherited is not None:
            paths.extend(inherited)
            continue
        match = re.fullmatch(r'"([^"\n]+)",', line)
        if not match:
            raise ValueError("unsupported source-list entry")
        paths.append(match[1])
    return paths


class MembershipSourceSets(unittest.TestCase):
    def test_all_producer_sets_equal_strict_validators(self):
        root = Path("web/e2e")
        base = source_list((root / "group-members-v4-helpers.ts").read_text(), "membershipSources")
        empties = source_list((root / "group-members-v4-empty-helpers.ts").read_text(), "emptyMembershipSources")
        pages = source_list((root / "group-members-v4-pagination-helpers.ts").read_text(), "paginationMembershipSources")
        refreshed = source_list((root / "group-members-v4-refresh-helpers.ts").read_text(), "refreshMembershipSources", empties)
        for label, paths, expected in (("primary", base, primary.SOURCES),
                                       ("empty", empties, empty.SOURCES),
                                       ("pagination", pages, pagination.SOURCES),
                                       ("refresh", refreshed, refresh.SOURCES)):
            with self.subTest(suite=label):
                self.assertEqual(set(paths), expected)
                if label != "refresh":
                    self.assertEqual(len(paths), len(set(paths)))
                self.assertTrue(all(Path(p).is_file() for p in paths))
                # A missing or unexpected source remains a hard failure.
                for path in expected:
                    self.assertNotEqual(set(paths) - {path}, expected)
                self.assertNotEqual(set(paths) | {"unexpected-source.ts"}, expected)

    def test_parser_rejects_unsupported_or_duplicate_declarations(self):
        source = 'export const example = [\n  "one.ts",\n];'
        self.assertEqual(source_list(source, "example"), ["one.ts"])
        for changed in (source + source, source.replace('"one.ts"', 'dynamicPath'),
                        source.replace('"one.ts"', '...otherSources')):
            with self.assertRaises(ValueError):
                source_list(changed, "example")


if __name__ == "__main__":
    unittest.main()
