"""UI-0495 v2 presentation guard; static evidence, never a browser substitute."""
import hashlib
from pathlib import Path
import re
import unittest


WORKSPACE = Path('web/src/UserAssetsV2Workspace.tsx')
BROWSER = Path('web/e2e/user-assets-v2.spec.ts')


class UserAssetsV2RowNumbersContract(unittest.TestCase):
    def assert_table(self, code):
        columns = re.findall(
            r'<th scope="col" style=\{\{ width: "(\d+)%" \}\}>\s*([^<]+?)\s*</th>', code)
        self.assertEqual(columns, [('9', '本页序号'), ('14', 'SAM'),
                                   ('17', 'GUID'), ('19', 'DN'), ('16', 'SID'),
                                   ('15', 'mail'), ('10', '详情')])
        self.assertEqual(sum(int(width) for width, _ in columns), 100)
        self.assertEqual(code.count('{list.list.map((row, index) => ('), 1)
        self.assertEqual(code.count('<tr key={row.objectGUID}>'), 1)
        self.assertEqual(code.count('<td data-label="本页序号">{index + 1}</td>'), 1)
        table = code.split('<caption>用户资产列表</caption>', 1)[1].split('</table>', 1)[0]
        for forbidden in ['.sort(', 'aria-sort', 'pageSize *', 'pageIdx *', 'key={index}']:
            self.assertNotIn(forbidden, table)
        self.assertIn('objectGUID: row.objectGUID', table)

    def assert_response_binding(self, code):
        block = code.split('async function expectUserPositions(', 1)[1].split(
            'async function assetAction(', 1)[0]
        for required in ['expected.length === 0', 'expect(table).toHaveCount(0)',
                         'name: "本页序号"', '"scope", "col"', '"aria-sort"',
                         'header.getByRole("button")', '"tbody tr"',
                         'cells).toHaveCount(7)', '"data-label", "本页序号"',
                         'cells.nth(0)).toHaveText(String(index + 1))',
                         'cells.nth(2)).toHaveText(expected[index].objectGUID)']:
            self.assertIn(required, block)
        for forbidden in ['pageIdx', 'pageSize', '.click(', '.evaluate(',
                          'context.request', 'fetch(', '.fulfill(']:
            self.assertNotIn(forbidden, block)
        action = code.split('async function assetAction(', 1)[1].split(
            'async function openAssets(', 1)[0]
        ordered = ['candidate.request().method() === "GET"',
                   'const value = await directoryV2ResponseJSON(response)',
                   'if (!detail && status === 200)',
                   'await expectUserPositions(page, value.list)',
                   'return {']
        self.assertEqual([action.index(text) for text in ordered],
                         sorted(action.index(text) for text in ordered))
        late = code.split('await test.step("late list cannot replace a newer search",', 1)[1].split(
            '// A second genuine producer run', 1)[0]
        self.assertLess(late.index('"complete-consumed"'),
                        late.index('await expectUserPositions(page, current.value.list)'))

    def assert_capture(self, code, kind):
        title = kind.title()
        start = f'const first{title}Row = socTable.locator("tbody tr").first()'
        frame = code.split(start, 1)[1].split('fullPage: false,', 1)[0]
        ordered = [
            f'first{title}Row.getByRole("cell").nth(2)).toHaveText(',
            f'{kind}.value.list[0].objectGUID',
            f'await first{title}Row.scrollIntoViewIfNeeded()',
            f'first{title}Row).toBeInViewport({{ ratio: 1 }})',
            f'const first{title}Position = first{title}Row.getByRole("cell").first()',
            f'first{title}Position).toHaveAttribute(',
            '"data-label",', '"本页序号",',
            f'first{title}Position).toHaveText("1")',
            f'first{title}Position).toBeVisible()',
            f'first{title}Position).toBeInViewport({{ ratio: 1 }})',
            f'first{title}Row.getByRole("cell").nth(2)',
            f'"user-assets-v2-{kind}-row-numbers.png"',
        ]
        # GUID appears before and after scrolling; use the last assertion for its viewport check.
        prefix = ordered[:-2]
        self.assertEqual([frame.index(text) for text in prefix],
                         sorted(frame.index(text) for text in prefix))
        self.assertIn(').toBeInViewport({ ratio: 1 })', frame)
        self.assertIn(ordered[-1], frame)
        self.assertEqual(frame.count('ratio: 1'), 3)
        old_frame = f'"user-assets-v2-soc-{kind}-user-assets-first-screen.png"'
        self.assertLess(code.index(old_frame), code.index(start))
        for forbidden in ['.fulfill(', 'context.request', 'fetch(', 'setTimeout(',
                          'waitForTimeout(', 'test.setTimeout(', 'fullPage: true']:
            self.assertNotIn(forbidden, frame)

    def test_display_only_table_uses_accepted_order_and_guid_key(self):
        self.assert_table(WORKSPACE.read_text())

    def test_every_successful_existing_list_read_binds_guid_and_position(self):
        self.assert_response_binding(BROWSER.read_text())

    def test_both_new_pixel_frames_preserve_old_first_screen_and_show_complete_real_row(self):
        code = BROWSER.read_text()
        for kind in ['desktop', 'mobile']:
            with self.subTest(kind=kind):
                self.assert_capture(code, kind)
        for required in ['expect(await soc.evaluate(() => scrollY)).toBe(0)',
                         'firstMobileSAMBounds!.y + firstMobileSAMBounds!.height,',
                         ').toBeLessThanOrEqual(844)',
                         'firstRowBounds!.y + firstRowBounds!.height).toBeLessThanOrEqual(']:
            self.assertIn(required, code)

    def test_old_v2_overlength_retention_submit_rejection_is_unchanged(self):
        code = BROWSER.read_text()
        block = code.split('  await test.step("overlong native input remains intact', 1)[1].split(
            '  await test.step("SAM SID mail and DN search', 1)[0]
        self.assertEqual(hashlib.sha256(block.encode()).hexdigest(), 'c22451e8f5ec9248b470d9b49045100fd15b42535e9fddbc83c466900e68778d')
        production = WORKSPACE.read_text()
        self.assertNotIn('maxLength=', production)
        self.assertNotIn('清除用户资产关键词', production)

    def test_table_mutations_fail_closed(self):
        code = WORKSPACE.read_text()
        for old, new in [('{index + 1}', '{index + 11}'),
                         ('<tr key={row.objectGUID}>', '<tr key={index}>'),
                         ('width: "9%"', 'width: "10%"'),
                         ('list.list.map((row, index)', 'list.list.sort().map((row, index)'),
                         ('objectGUID: row.objectGUID', 'objectGUID: String(index)')]:
            with self.subTest(mutation=old):
                self.assertIn(old, code)
                with self.assertRaises(AssertionError):
                    self.assert_table(code.replace(old, new, 1))

    def test_response_binding_mutations_fail_closed(self):
        code = BROWSER.read_text()
        for old, new in [('String(index + 1)', 'String(index + 11)'),
                         ('expected[index].objectGUID', 'expected[0].objectGUID'),
                         ('if (!detail && status === 200)', 'if (!detail)'),
                         ('await expectUserPositions(page, value.list);', ''),
                         ('await expectUserPositions(page, current.value.list);', '')]:
            with self.subTest(mutation=old):
                self.assertIn(old, code)
                with self.assertRaises((AssertionError, ValueError)):
                    self.assert_response_binding(code.replace(old, new, 1))

    def test_pixel_frame_mutations_fail_closed(self):
        code = BROWSER.read_text()
        for kind in ['desktop', 'mobile']:
            title = kind.title()
            for old, new in [
                (f'const first{title}Row = socTable.locator("tbody tr").first()',
                 f'const first{title}Row = socTable.locator("tbody tr").last()'),
                (f'{kind}.value.list[0].objectGUID', f'{kind}.value.list[1].objectGUID'),
                (f'await first{title}Row.scrollIntoViewIfNeeded();', ''),
                (f'first{title}Row).toBeInViewport({{ ratio: 1 }})',
                 f'first{title}Row).toBeInViewport({{ ratio: 0 }})'),
                (f'first{title}Position).toHaveText("1")', f'first{title}Position).toHaveText("2")'),
                (f'first{title}Position).toBeInViewport({{ ratio: 1 }})',
                 f'first{title}Position).toBeInViewport({{ ratio: 0 }})'),
            ]:
                with self.subTest(kind=kind, mutation=old):
                    self.assertIn(old, code)
                    with self.assertRaises((AssertionError, IndexError, ValueError)):
                        self.assert_capture(code.replace(old, new, 1), kind)


if __name__ == '__main__':
    unittest.main()
