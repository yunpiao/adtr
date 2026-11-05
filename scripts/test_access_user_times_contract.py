"""Offline source preservation only, not browser/database or pixel acceptance."""
import hashlib
from pathlib import Path
import unittest

import access_user_times_source_contract as source
import role_users_read_source_contract as roles
import test_access_role_bulk_contract as bulk

SPEC = 'web/e2e/access.spec.ts'
ROLE_INVERSE = 'scripts/role_users_read_source_contract.py'
ROLE_INVERSE_SHA = '2e5b7a6afb8258aa3d42f56a437125ce4d4cd62be26e59dc2b68a810fd43c5d0'
OUTER_HOOK = ('    from access_user_times_source_contract import restore_user_times_spec\n'
              '    text = restore_user_times_spec(text)\n')


def read(path):
    return Path(path).read_bytes().decode('utf-8')


class AccessUserTimesSourceContract(unittest.TestCase):
    def test_exact_outer_restores_current_spec_and_both_older_layers(self):
        current = read(SPEC)
        predecessor = source.restore_user_times_spec(current)
        self.assertEqual(len(predecessor.encode()), 52437)
        self.assertEqual(source.digest(predecessor.encode()), source.BASE_SPEC)
        older = roles.restore_role_users_spec(current)
        self.assertEqual(len(older.encode()), bulk.SPEC_BYTES)
        self.assertEqual(source.digest(older.encode()), bulk.SPEC_SHA256)
        self.assertEqual(source.digest(bulk.restore_access_spec(older).encode()), bulk.BASE_SPEC)
        roles.validate_role_users_browser_helper(Path('web/e2e/role-users-read.ts').read_bytes())

    def test_original_inverse_changes_only_by_strict_outer_hook(self):
        current = read(ROLE_INVERSE)
        self.assertEqual(current.count(OUTER_HOOK), 1)
        self.assertIn('def restore_role_users_spec(text):\n' + OUTER_HOOK, current)
        restored = current.replace(OUTER_HOOK, '', 1)
        self.assertEqual(hashlib.sha256(restored.encode()).hexdigest(), ROLE_INVERSE_SHA)

    def test_every_added_line_anchor_and_prior_byte_drift_rejects(self):
        text = read(SPEC)
        variants = [text + '\n', text.replace('\n', '\r\n'),
                    text.replace('test.setTimeout(300_000)', 'test.setTimeout(400_000)')]
        for anchor, begin, end, _, _ in source.BLOCKS:
            block = text[text.index(begin):text.index(end) + len(end)]
            variants += [text.replace(block, '', 1), text.replace(block, block * 2, 1),
                         text.replace(block, '', 1) + block, text.replace(anchor, '', 1) + anchor]
            lines = block.splitlines(keepends=True)
            for index in range(len(lines)):
                variants += [text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1),
                             text.replace(block, ''.join(lines[:index] + [lines[index], lines[index]] + lines[index + 1:]), 1),
                             text.replace(block, ''.join(lines[:index] + [lines[index].rstrip('\n') + ' // changed\n'] + lines[index + 1:]), 1),
                             text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1) + lines[index]]
        for bad in variants:
            with self.assertRaises(ValueError):
                roles.restore_role_users_spec(bad)

    def test_original_request_screenshot_budget_and_cleanup_are_untouched(self):
        text = read(SPEC)
        self.assertEqual(text.count('const userTimesResponse = observeAccessUserTimes(page);'), 1)
        registration = text.index('const userTimesResponse = observeAccessUserTimes(page);')
        comparison = text.index('await verifyAccessUserTimes(page, userTimesResponse, [')
        click = text.index('await page.getByRole("button", { name: "应用筛选", exact: true }).click();', registration)
        self.assertLess(registration, click)
        self.assertLess(click, comparison)
        self.assertLess(comparison, text.index('const screenshot = await page.screenshot({', comparison))
        added = ''.join(text[text.index(begin):text.index(end) + len(end)] for _, begin, end, _, _ in source.BLOCKS)
        for forbidden in ('page.request', 'context.request', 'fetch(', '.route(', '.newContext(',
                          '.goto(', '.setViewportSize(', '.screenshot(', 'writeFileSync(',
                          'Date.now(', 'new Date(', '.setTimeout(', 'test.setTimeout(',
                          '.post(', '.delete(', '.fill(', '.selectOption('):
            self.assertNotIn(forbidden, added)
        self.assertIn('timeout: 15_000', added)
        self.assertIn('.catch(() => null)', added)
        self.assertIn('finally {', added)
        self.assertIn('details[open]', added)
        self.assertIn('request.frame() === page.mainFrame()', added)
        self.assertIn('url.searchParams.getAll(key).length === 1', added)
        self.assertIn('response.status() === 200', added)
        self.assertIn('value.ID === ids[index]', added)
        self.assertIn('matched[0].value === time', added)
        self.assertIn('box.right <= innerWidth', added)
        self.assertIn('throw new Error("access_user_times_check_failed")', added)


if __name__ == '__main__':
    unittest.main()
