"""Offline exact source preservation; this is not native runtime evidence."""
import hashlib
from pathlib import Path
import unittest

import access_user_row_numbers_source_contract as source
import access_user_times_source_contract as times
import role_users_read_source_contract as roles
import test_access_role_bulk_contract as bulk

SPEC = 'web/e2e/access.spec.ts'
TIME_INVERSE = 'scripts/access_user_times_source_contract.py'
TIME_INVERSE_SHA = '73400bcd67097fe74aa5c098539bf84df833859fb2e9adcaee43dff844e7cf84'
OUTER_HOOK = ('    from access_user_row_numbers_source_contract import restore_user_row_numbers_spec\n'
              '    text = restore_user_row_numbers_spec(text)\n')


def read(path):
    return Path(path).read_bytes().decode('utf-8')


class AccessUserRowNumbersContract(unittest.TestCase):
    def test_exact_inverse_restores_every_prior_layer(self):
        current = read(SPEC)
        prior = source.restore_user_row_numbers_spec(current)
        self.assertEqual(len(prior.encode()), 59731)
        self.assertEqual(source.digest(prior.encode()), source.BASE_SPEC)
        self.assertEqual(source.digest(times.restore_user_times_spec(current).encode()), times.BASE_SPEC)
        older = roles.restore_role_users_spec(current)
        self.assertEqual(source.digest(older.encode()), bulk.SPEC_SHA256)
        self.assertEqual(source.digest(bulk.restore_access_spec(older).encode()), bulk.BASE_SPEC)

    def test_old_inverse_has_only_exact_outer_hook(self):
        current = read(TIME_INVERSE)
        self.assertEqual(current.count(OUTER_HOOK), 1)
        self.assertIn('def restore_user_times_spec(text):\n' + OUTER_HOOK, current)
        restored = current.replace(OUTER_HOOK, '', 1)
        self.assertEqual(hashlib.sha256(restored.encode()).hexdigest(), TIME_INVERSE_SHA)

    def test_added_line_anchor_and_prior_drift_fail_closed(self):
        text = read(SPEC)
        block = text[text.index(source.BEGIN):text.index(source.END) + len(source.END)]
        variants = [text + '\n', text.replace('\n', '\r\n'),
                    text.replace('test.setTimeout(300_000)', 'test.setTimeout(400_000)'),
                    text.replace(block, '', 1), text.replace(block, block * 2, 1),
                    text.replace(block, '', 1) + block,
                    text.replace(source.ANCHOR, '', 1) + source.ANCHOR]
        lines = block.splitlines(keepends=True)
        for index in range(len(lines)):
            variants += [text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1),
                         text.replace(block, ''.join(lines[:index] + [lines[index], lines[index]] + lines[index + 1:]), 1),
                         text.replace(block, ''.join(lines[:index] + [lines[index].rstrip('\n') + ' // changed\n'] + lines[index + 1:]), 1),
                         text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1) + lines[index]]
        for bad in variants:
            with self.assertRaises(ValueError):
                roles.restore_role_users_spec(bad)

    def test_assertions_use_the_current_validated_rows_before_original_image(self):
        text = read(SPEC)
        left, right = text.index(source.BEGIN), text.index(source.END) + len(source.END)
        block = text[left:right]
        self.assertLess(text.index('await verifyAccessUserTimes(page, userTimesResponse, ['), left)
        self.assertLess(right, text.index('const screenshot = await page.screenshot({', right))
        for fragment in ('name: "本页序号"', 'getAttribute("scope")', '(await rows.count()) === 2',
                         'rows.nth(index)', 'td[data-field="rowNumber"]',
                         '=== String(index + 1)', 'toBeInViewport({ ratio: 1 })'):
            self.assertIn(fragment, block)
        for forbidden in ('page.request', 'context.request', 'fetch(', '.route(', '.newContext(',
                          '.goto(', '.setViewportSize(', '.screenshot(', 'writeFileSync(',
                          'Date.now(', 'new Date(', '.setTimeout(', 'test.setTimeout(',
                          '.post(', '.delete(', '.fill(', '.selectOption(', '.click(', '.scroll'):
            self.assertNotIn(forbidden, block)


if __name__ == '__main__':
    unittest.main()
