from ci_workflow_contract import reviewed_workflow
"""Guard dictionary3's additive real browser gate; never substitute for CI."""
import ast
import re
from pathlib import Path
import unittest

from test_auth_e2e import (DIRECTORY_SUITES, DIRECTORY_V2_SUITES,
                           DIRECTORY_V3_SUITES, EMPTY_DIRECTORY_SUITES,
                           REAL_TAB_LIFECYCLE_SUITES, SLOW_DIRECTORY_SUITES)


class UserAssetsV3BrowserContract(unittest.TestCase):
    def test_bounded_headed_nonempty_real_v3_fixture(self):
        suite = 'user-assets-v3'
        for suites in [DIRECTORY_SUITES, DIRECTORY_V3_SUITES, REAL_TAB_LIFECYCLE_SUITES]:
            self.assertIn(suite, suites)
        for suites in [DIRECTORY_V2_SUITES, EMPTY_DIRECTORY_SUITES, SLOW_DIRECTORY_SUITES]:
            self.assertNotIn(suite, suites)
        runner = Path('scripts/test_auth_e2e.py').read_text()
        tree = ast.parse(runner)
        choices = [kw.value for node in ast.walk(tree) if isinstance(node, ast.Call)
                   for kw in node.keywords if kw.arg == 'choices']
        self.assertTrue(any(suite in ast.literal_eval(value) for value in choices))
        self.assertIn('directory_v3=args.suite in DIRECTORY_V3_SUITES', runner)
        self.assertIn('if args.suite in DIRECTORY_V3_SUITES:', runner)
        self.assertIn('env["ADTR_DIRECTORY_READ_V3_ENABLED"] = "true"', runner)
        self.assertIn('env["ADTR_E2E_DB_CONTAINER"] = name', runner)

    def test_original_and_fixed_engines_remain_required(self):
        workflow = reviewed_workflow()
        self.assertEqual(workflow.count('command: python3 scripts/test_auth_e2e.py --suite user-assets-v3\n'), 1)
        self.assertIn("matrix.name == 'user-assets-v3' || matrix.name == 'computer-assets-v3' || matrix.name == 'computer-assets-v3-readers' || matrix.name == 'group-assets-v3' || matrix.name == 'group-assets-v3-readers') && 'stable-body'", workflow)
        self.assertIn('suite: [user-assets-v2, user-assets-v2-readers, user-assets-v3, computer-assets-v3, computer-assets-v3-readers, group-assets-v3, group-assets-v3-readers]', workflow)
        self.assertIn('path: web/test-results/**/user-assets-v3-*\n', workflow)
        self.assertIn('            web/test-results/**/user-assets-v3-*\n', workflow)
        self.assertIn('if-no-files-found: error', workflow)
        self.assertNotIn('continue-on-error', workflow)

    def test_uac_filter_uses_original_gets_and_restores_unrestricted_state(self):
        code = Path('web/e2e/user-assets-v3.spec.ts').read_text()
        block = code.split('await test.step("UAC disabled-bit draft,', 1)[1].split(
            'await test.step("native clipboard', 1)[0]
        for required in ['getByLabel("本次观测的禁用位"', 'control.selectOption("clear")',
                         '["set", "禁用位已设置"]', '["not_returned", "UAC未返回"]',
                         'expect(draftReads).toBe(0)', 'expect([...actual.query].sort())',
                         'expectedRevision: selection.revision',
                         'expectedCredentialRevision: selection.credentialRevision',
                         'observationId: taskUUID', 'uacDisableBit: bit',
                         'assertRead(clearLast, "clear", 2)',
                         'expect([...clearFirst.value.list, ...clearLast.value.list]).toEqual(',
                         'originalUsers', 'actual.query.getAll("uacDisableBit")',
                         '第 1 页 · 共 0 页 · 匹配用户 0 个',
                         'response.request() === heldRequest',
                         'recordAssetResponse(originalResponse)', '"complete-consumed"',
                         'uac-clear-superseded-by-set', 'expect(restored.value).toEqual(first.value)',
                         'not nonempty set/null LDAP']:
            self.assertIn(required, block)
        for forbidden in ['.fulfill(', 'context.request', 'fixtureGET(', 'setTimeout(',
                          'waitForTimeout(', 'test.setTimeout(', '"POST"']:
            self.assertNotIn(forbidden, block)
        self.assertIn('return recordAssetResponse(response, detail, status)', code)
        self.assertIn('expect(response.request().method()).toBe("GET")', code)

    def test_page_local_row_numbers_bind_actual_response_order_without_query_changes(self):
        code = Path('web/e2e/user-assets-v3.spec.ts').read_text()
        block = code.split('const expectUserPositions = async', 1)[1].split(
            'const panel =', 1)[0]
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
        for response in ['first', 'secondPage', 'back', 'found', 'descending',
                         'descendingLast', 'resized', 'clearLast', 'restored',
                         'refreshed']:
            self.assertIn(f'await expectUserPositions({response}.value.list)', code)
        capture = code.split('const capture = async', 1)[1].split(
            'const captureSPNGeometry', 1)[0]
        self.assertIn('if (!object) await expectUserPositions(list.list)', capture)
        self.assertIn('user-assets-v3-desktop-list.png', code)
        self.assertIn('user-assets-v3-mobile-list.png', code)
        production = Path('web/src/UserAssetsV3Workspace.tsx').read_text()
        columns = re.findall(
            r'<th scope="col" style=\{\{ width: "(\d+)%" \}\}>\s*([^<]+?)\s*</th>',
            production,
        )
        self.assertEqual(columns, [('9', '本页序号'), ('14', 'SAM'),
                                   ('17', 'GUID'), ('19', 'DN'), ('16', 'SID'),
                                   ('15', 'mail'), ('10', '详情')])
        self.assertEqual(sum(int(width) for width, _ in columns), 100)
        self.assertIn('{list.list.map((row, index) => (', production)
        self.assertIn('<tr key={row.objectGUID}>', production)
        self.assertIn('<td data-label="本页序号">{index + 1}</td>', production)

    def assert_mobile_row_capture(self, code):
        block = code.split(
            'await test.step("mobile real data and drawer remain bounded at 390 by 844",',
            1,
        )[1].split('const actual = await assetAction', 1)[0]
        ordered = [
            'await page.setViewportSize({ width: 390, height: 844 })',
            'await page.evaluate(() => scrollTo(0, 0))',
            'document.documentElement.scrollWidth <= innerWidth',
            'const firstMobileRow = table.locator("tbody tr").first()',
            'await expect(firstMobileRow.getByRole("cell").nth(2)).toHaveText(',
            'first.value.list[0].objectGUID',
            'await firstMobileRow.scrollIntoViewIfNeeded()',
            'const firstMobilePosition = firstMobileRow.getByRole("cell").first()',
            'await expect(firstMobilePosition).toHaveAttribute("data-label", "本页序号")',
            'await expect(firstMobilePosition).toHaveText("1")',
            'await expect(firstMobilePosition).toBeVisible()',
            'await expect(firstMobilePosition).toBeInViewport({ ratio: 1 })',
            'await capture("user-assets-v3-mobile-list.png", first.value)',
        ]
        for text in ordered:
            self.assertEqual(block.count(text), 1, text)
        self.assertEqual([block.index(text) for text in ordered],
                         sorted(block.index(text) for text in ordered))
        for forbidden in ['.fulfill(', 'context.request', 'fetch(', 'setTimeout(',
                          'waitForTimeout(', 'test.setTimeout(', 'fullPage: true']:
            self.assertNotIn(forbidden, block)
        capture = code.split('const capture = async', 1)[1].split(
            'const captureSPNGeometry', 1)[0]
        self.assertNotIn('scrollTo(', capture)
        self.assertNotIn('scrollIntoView', capture)
        self.assertIn('await page.screenshot({ path, fullPage: false })', capture)

    def test_mobile_row_number_is_response_bound_and_fully_in_capture_viewport(self):
        self.assert_mobile_row_capture(Path('web/e2e/user-assets-v3.spec.ts').read_text())

    def test_mobile_row_capture_rejects_missing_identity_scroll_or_visibility(self):
        code = Path('web/e2e/user-assets-v3.spec.ts').read_text()
        mutations = [
            ('const firstMobileRow = table.locator("tbody tr").first()',
             'const firstMobileRow = table.locator("tbody tr").last()'),
            ('first.value.list[0].objectGUID', 'first.value.list[1].objectGUID'),
            ('await firstMobileRow.scrollIntoViewIfNeeded();', ''),
            ('firstMobileRow.getByRole("cell").first()',
             'firstMobileRow.getByRole("cell").nth(1)'),
            ('firstMobilePosition).toHaveAttribute("data-label", "本页序号")',
             'firstMobilePosition).toHaveAttribute("data-label", "SAM")'),
            ('firstMobilePosition).toHaveText("1")', 'firstMobilePosition).toHaveText("2")'),
            ('await expect(firstMobilePosition).toBeVisible();', ''),
            ('firstMobilePosition).toBeInViewport({ ratio: 1 })',
             'firstMobilePosition).toBeInViewport({ ratio: 0 })'),
            ('await page.screenshot({ path, fullPage: false })',
             'await page.screenshot({ path, fullPage: true })'),
        ]
        for old, new in mutations:
            with self.subTest(mutation=old):
                self.assertIn(old, code)
                with self.assertRaises(AssertionError):
                    self.assert_mobile_row_capture(code.replace(old, new, 1))
        capture = 'await capture("user-assets-v3-mobile-list.png", first.value);'
        moved = code.replace(capture, '', 1).replace(
            'await firstMobileRow.scrollIntoViewIfNeeded();',
            capture + '\n    await firstMobileRow.scrollIntoViewIfNeeded();', 1)
        with self.assertRaises(AssertionError):
            self.assert_mobile_row_capture(moved)

    def test_producer_provenance_and_browser_evidence_are_explicit(self):
        code = Path('web/e2e/user-assets-v3.spec.ts').read_text()
        for required in ['storedEvidence(taskUUID)', 'storedEvidence(newerId)',
                         'createHash("sha256")', 'primaryGroupIDBytes',
                         'servicePrincipalNameBytes', 'executor_returned',
                         '/api/directory/v3/sync', 'user-assets-v3-workspace-evidence.json',
                         'screenshotSHA256', 'observationSHA256', 'browserReads',
                         'width: 1440, height: 900', 'width: 390, height: 844',
                         'useNativeTabLifecycle', 'requireSameNativeWindow',
                         'complete-consumed', 'selection_changed',
                         'Input.dispatchDragEvent', 'navigator.clipboard.writeText']:
            self.assertIn(required, code)
        for forbidden in ['.fulfill(', 'INSERT INTO adtr.', 'UPDATE adtr.', 'DELETE FROM adtr.',
                          'test.skip(', 'test.fixme(']:
            self.assertNotIn(forbidden, code)
        for protection in ['trace: "off"', 'screenshot: "off"', 'video: "off"']:
            self.assertIn(protection, code)
        for helper in ['directory-v2-response-observer.ts', 'user-assets-v2-abort-isolation.ts',
                       'user-assets-v2-held-response.ts']:
            content = Path('web/e2e', helper).read_text()
            for route in ['/api/user-assets/v2', '/api/user-assets/v2/detail',
                          '/api/user-assets/v3', '/api/user-assets/v3/detail']:
                self.assertIn(f'"{route}"', content)


if __name__ == '__main__':
    unittest.main()
