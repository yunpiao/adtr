"""Offline preservation contracts, not native browser/PostgreSQL evidence."""
from pathlib import Path
import unittest
import access_search_clear_source_contract as source
import access_user_row_numbers_source_contract as rows
import access_user_times_source_contract as times
import role_users_read_source_contract as roles
import test_access_role_bulk_contract as bulk

HOOK = '    from access_search_clear_source_contract import restore_search_clear_spec\n    text = restore_search_clear_spec(text)\n'
ROW_SOURCE_SHA256 = 'f1df836c80b5c77c4ad0a7a43e8b44b0b7bef262476bdf5cbb5abc7a4448f7f8'


def read(path):
    return Path(path).read_bytes().decode('utf-8')


class AccessSearchClearContract(unittest.TestCase):
    def test_all_previous_layers_restore_exactly(self):
        text = read('web/e2e/access.spec.ts')
        self.assertEqual(source.digest(source.restore_search_clear_spec(text).encode()), source.BASE_SPEC)
        self.assertEqual(source.digest(rows.restore_user_row_numbers_spec(text).encode()), rows.BASE_SPEC)
        self.assertEqual(source.digest(times.restore_user_times_spec(text).encode()), times.BASE_SPEC)
        legacy = roles.restore_role_users_spec(text)
        self.assertEqual(source.digest(legacy.encode()), bulk.SPEC_SHA256)
        self.assertEqual(source.digest(bulk.restore_access_spec(legacy).encode()), bulk.BASE_SPEC)

    def test_only_exact_outer_hook_changes_existing_inverse(self):
        text = read('scripts/access_user_row_numbers_source_contract.py')
        self.assertEqual(text.count(HOOK), 1)
        self.assertIn('def restore_user_row_numbers_spec(text):\n' + HOOK, text)
        self.assertEqual(source.digest(text.replace(HOOK, '', 1).encode()), ROW_SOURCE_SHA256)

    def test_drift_changed_moved_missing_and_duplicate_lines_reject(self):
        text = read('web/e2e/access.spec.ts')
        mutations = [text + '\n', text.replace('test.setTimeout(300_000)', 'test.setTimeout(400_000)'), text.replace('await clear.click();', 'await clear.dblclick();')]
        for anchor, begin, end, _, _ in source.BLOCKS:
            block = text[text.index(begin):text.index(end) + len(end)]
            mutations += [text.replace(block, '', 1), text.replace(block, block * 2, 1), text.replace(block, '', 1) + block]
            lines = block.splitlines(keepends=True)
            for index in range(len(lines)):
                mutations += [text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1), text.replace(block, ''.join(lines[:index] + [lines[index]] + lines[index:]), 1)]
        for bad in mutations:
            with self.assertRaises(ValueError):
                roles.restore_role_users_spec(bad)

    def test_native_checks_remain_draft_only_and_observe_real_page(self):
        text = read('web/e2e/access.spec.ts')
        helper = text[text.index(source.BLOCKS[0][1]):text.index(source.BLOCKS[0][2])]
        for required in ('page.on("request", observe)', 'page.off("request", observe)', 'finally {', 'await input.fill(original)', 'expect(requests).toBe(0)', 'expect(await table.innerText()).toBe(beforeRows)', 'expect(await otherFields()).toEqual(beforeFields)', 'await clear.press("Enter")', 'toBeFocused()', 'toHaveValue("")'):
            self.assertIn(required, helper)
        for forbidden in ('page.request', 'context.request', '.route(', '.newContext(', '.goto(', '.post(', '.delete(', 'fetch(', '.screenshot(', '.setTimeout(', 'test.setTimeout(', '.setViewportSize('):
            self.assertNotIn(forbidden, helper)
        self.assertEqual(text.count('await verifyAccessSearchClear(page, "roles");'), 1)
        self.assertEqual(text.count('await verifyAccessSearchClear(page, "users");'), 1)
        self.assertLess(text.index('await verifyAccessSearchClear(page, "users");'), text.index('const userTimesResponse = observeAccessUserTimes(page);'))


if __name__ == '__main__':
    unittest.main()
