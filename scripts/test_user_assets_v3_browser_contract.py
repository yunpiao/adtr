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

    def assert_keyword_clear_action(self, code):
        block = code.split('aria-label="清除用户资产关键词"', 1)[1].split(
            '</button>', 1)[0]
        flat = re.sub(r'\s+', ' ', block)
        for required in ['keywordComposition || stale || (draft === "" && !filterTransferErrors.search)',
                         'resetKeyword()',
                         'setDraft("")', 'const next = { ...current }',
                         'delete next.search', 'return next', 'setInputError("")',
                         'clearDetail()', 'searchInput.current?.focus()', '清空关键词']:
            self.assertIn(required, flat)
        for forbidden in ['setFilterDraft(', 'setFilterTransferErrors({})',
                          'cancelList(', 'loadList(', 'setQuery(', 'queryRef.current',
                          'setList(', 'fetch(', 'pageIdx:', 'pageSize:']:
            self.assertNotIn(forbidden, block)
        before = code.split('aria-label="清除用户资产关键词"', 1)[0]
        self.assertIn('type="button"', before.rsplit('<button', 1)[1])
        self.assertIn('className="field asset-page-size"', code)
        self.assertLess(code.index('className="field asset-page-size"'),
                        code.index('aria-label="清除用户资产关键词"'))

    def test_keyword_clear_is_draft_only_and_preserves_other_state_channels(self):
        self.assert_keyword_clear_action(Path('web/src/UserAssetsV3Workspace.tsx').read_text())

    def test_keyword_clear_action_rejects_query_abort_or_other_filter_changes(self):
        code = Path('web/src/UserAssetsV3Workspace.tsx').read_text()
        for old, new in [
            ('delete next.search;', 'delete next.startTm;'),
            ('clearDetail();\n            searchInput.current?.focus();',
             'cancelList();\n            searchInput.current?.focus();'),
            ('searchInput.current?.focus();\n          }}', 'setFilterDraft(emptyFilters);\n          }}'),
            ('(draft === "" && !filterTransferErrors.search)', 'draft === ""'),
        ]:
            with self.subTest(mutation=old):
                self.assertIn(old, code)
                with self.assertRaises(AssertionError):
                    prefix, block = code.split('aria-label="清除用户资产关键词"', 1)
                    self.assert_keyword_clear_action(prefix + 'aria-label="清除用户资产关键词"' + block.replace(old, new, 1))

    def assert_keyword_limit_action(self, code):
        change = code.split('const changeKeyword =', 1)[1].split('const resetKeyword =', 1)[0]
        flat = re.sub(r'\s+', ' ', change)
        for required in ['!keywordActive.current', 'composingKeyword.current',
                         'setDraft(value)', 'clearDetail()',
                         'value === rejected.value || value === rejected.restored',
                         'finishKeywordComposition', '!composingKeyword.current',
                         'composingKeyword.current = false', 'setKeywordComposition(false)',
                         'restored: acceptedKeyword.current', 'editKeyword(value)']:
            self.assertIn(required, flat)
        self.assertEqual(change.count('value.length > 50'), 2)
        for forbidden in ['.slice(', '.substring(', '.trim(', '.normalize(',
                          'loadList(', 'cancelList(', 'setQuery(', 'queryRef.current']:
            self.assertNotIn(forbidden, change)
        restore = code.split('const restoreKeyword =', 1)[1].split('const changeKeyword =', 1)[0]
        for required in ['acceptedKeyword.current', 'input.value = previous',
                         'input.setSelectionRange(start, end, direction)',
                         '...current', 'search: "关键词最多 50 个 UTF-16 单元', 'clearDetail()']:
            self.assertIn(required, re.sub(r'\s+', ' ', restore))
        input_block = code.split('placeholder="按 SAM、SID、mail 或 DN 搜索"', 1)[1].split('/>', 1)[0]
        self.assertNotIn('maxLength=', input_block)
        self.assertIn('onBlur={(event) =>', input_block)
        self.assertEqual(input_block.count('finishKeywordComposition(event.currentTarget.value)'), 2)
        self.assertIn('input.value.length - (end - start) + inserted.length > 50', input_block)
        self.assertIn('event.dataTransfer.getData("text/plain").length > 50', input_block)
        for kind in ['Paste', 'Drop']:
            block = input_block.split(f'on{kind}={{', 1)[1]
            self.assertLess(block.index(f'keywordGuard.on{kind}(event)'), block.index('event.defaultPrevented'))
            self.assertLess(block.index('event.defaultPrevented'), block.index('rejectKeywordLength()'))
        dirty = code.split('const dirty =', 1)[1].split('const changeFilter =', 1)[0]
        self.assertIn('keywordComposition ||', dirty)
        self.assertIn('if (', code.split('const submit =', 1)[1])
        self.assertIn('composingKeyword.current ||', code.split('const submit =', 1)[1].split('validUserAssetsV3Search', 1)[0])
        self.assertEqual(code.count('resetKeyword();'), 2)
        self.assertIn('key={keywordGeneration}', code)
        self.assertIn('setKeywordGeneration((value) => value + 1)', code)
        self.assertIn('keywordActive.current = true', code.split('const loadList =', 1)[1].split('const loadDetail =', 1)[0])
        self.assertIn('keywordActive.current = false', code.split('return () => {', 1)[1])
        self.assertEqual(code.split('const handleFailure =', 1)[1].split('const loadList =', 1)[0].count('keywordActive.current = false'), 2)

    def test_keyword_limit_preserves_channels_and_handles_composition(self):
        self.assert_keyword_limit_action(Path('web/src/UserAssetsV3Workspace.tsx').read_text())

    def test_keyword_limit_action_negative_mutations(self):
        code = Path('web/src/UserAssetsV3Workspace.tsx').read_text()
        for old, new in [
            ('value.length > 50', 'value.length > 51'),
            ('value === rejected.value || value === rejected.restored', 'value === rejected.value'),
            ('input.setSelectionRange(start, end, direction)', 'input.setSelectionRange(start, end)'),
            ('input.value.length - (end - start) + inserted.length > 50', 'input.value.length + inserted.length > 50'),
            ('event.dataTransfer.getData("text/plain").length > 50', 'event.dataTransfer.getData("text/plain").length > 51'),
            ('keywordComposition ||\n    !!transferError', '!!transferError'),
        ]:
            with self.subTest(mutation=old):
                self.assertIn(old, code)
                with self.assertRaises(AssertionError):
                    self.assert_keyword_limit_action(code.replace(old, new, 1))

    def assert_keyword_limit_browser(self, code):
        block = code.split('await test.step("whole-change keyword length:', 1)[1].split(
            'await test.step("mobile real data', 1)[0]
        flat = re.sub(r'\s+', ' ', block)
        for required in ['width: 1440', 'width: 390',
                         'page.keyboard.type("a".repeat(50))', 'page.keyboard.type("Z")',
                         'navigator.clipboard.writeText(value)', 'page.keyboard.press("Control+V")',
                         'trusted: true, prevented', 'event.isTrusted',
                         'await select(10, 12, "backward")', 'await paste("XYZ", true)',
                         'await paste("😀", false)', 'await paste("😀".repeat(26), true)',
                         'await paste("😀".repeat(25), false)',
                         'await paste("x".repeat(51) + "\\n", true)',
                         'not.toContainText(limitMessage)',
                         'new CompositionEvent("compositionstart"', 'new CompositionEvent("compositionend"',
                         'isComposing: true', 'requestSubmit()',
                         '"汉".repeat(51), "previous", "汉".repeat(51)',
                         'await expect(field).toHaveValue("previous")',
                         'await expect(clearKeyword).toBeDisabled()',
                         'await expect(search).toBeDisabled()',
                         'expect(implicitRequests).toBe(0)', 'await expectUserPositions(first.value.list)',
                         'user-assets-v3-desktop-keyword-limit.png',
                         'user-assets-v3-mobile-keyword-limit.png',
                         'syntheticCompositionIsNotOSIMEAcceptance: true']:
            self.assertIn(required, flat)
        for forbidden in ['.fulfill(', 'context.request', 'fixtureGET(', 'fetch(',
                          'waitForTimeout(', 'test.setTimeout(', 'test.skip(']:
            self.assertNotIn(forbidden, block)

    def test_keyword_limit_real_keyboard_clipboard_and_explicit_synthetic_ime(self):
        self.assert_keyword_limit_browser(Path('web/e2e/user-assets-v3.spec.ts').read_text())

    def test_keyword_limit_browser_negative_mutations(self):
        code = Path('web/e2e/user-assets-v3.spec.ts').read_text()
        for old, new in [
            ('await paste("😀".repeat(26), true)', 'await paste("😀".repeat(25), true)'),
            ('await paste("😀".repeat(25), false)', 'await paste("😀".repeat(24), false)'),
            ('trusted: true, prevented', 'trusted: false, prevented'),
            ('syntheticCompositionIsNotOSIMEAcceptance: true', 'syntheticCompositionIsNotOSIMEAcceptance: false'),
        ]:
            with self.subTest(mutation=old):
                self.assertIn(old, code)
                with self.assertRaises(AssertionError):
                    prefix, block = code.split('await test.step("whole-change keyword length:', 1)
                    self.assert_keyword_limit_browser(prefix + 'await test.step("whole-change keyword length:' + block.replace(old, new, 1))

    def assert_keyword_clear_browser(self, code):
        block = code.split('await test.step("keyword-only clear preserves', 1)[1].split(
            'await test.step("UAC disabled-bit draft,', 1)[0]
        flat = re.sub(r'\s+', ' ', block)
        for required in ['name: "清除用户资产关键词"', 'await clearKeyword.click()',
                         'await expect(keyword).toHaveValue("")', 'toBeFocused()',
                         'expect(implicitRequests).toBe(0)',
                         'path.startsWith("/api/user-assets/v3")',
                         'expect([...appliedLast.query].sort())',
                         'Object.entries(appliedQuery).sort()',
                         'Object.entries({ ...appliedQuery, search: "", pageIdx: "1" }).sort()',
                         'await expectUserPositions(appliedLast.value.list)',
                         'await expectUserPositions(current.value.list)',
                         'getByLabel("精确账户名（SAM）"', 'getByLabel("本次观测的禁用位"',
                         'getByLabel("用户资产每页条数"', 'getByLabel("创建时间开始（含）"',
                         'getByLabel("创建时间结束（不含）"', 'getByLabel("创建时间排序"',
                         'await keyword.scrollIntoViewIfNeeded()',
                         'await clearKeyword.scrollIntoViewIfNeeded()',
                         'keyword).toBeInViewport({ ratio: 1 })',
                         'clearKeyword).toBeInViewport({ ratio: 1 })',
                         'width: 390, height: 844', 'width: 1440, height: 900',
                         'document.documentElement.scrollWidth <= innerWidth',
                         '"user-assets-v3-desktop-keyword-clear.png", appliedLast.value',
                         '"user-assets-v3-mobile-keyword-clear.png", appliedLast.value',
                         '"user-assets-v3-desktop-keyword-clear-list.png", appliedLast.value',
                         '"user-assets-v3-mobile-keyword-clear-list.png", appliedLast.value',
                         'const clearedRow = table.locator("tbody tr").first()',
                         'appliedLast.value.list[0].objectGUID',
                         'clearedPosition).toHaveText("1")',
                         'clearedPosition).toHaveAttribute("data-label", "本页序号")',
                         'await clearedRow.scrollIntoViewIfNeeded()',
                         'clearedPosition).toBeInViewport({ ratio: 1 })',
                         'expect(current.value).toEqual(first.value)',
                         'expect(outcome.application).toBe("complete-consumed")',
                         'scenario: "keyword-clear-apply-supersedes-list"']:
            self.assertIn(required, flat)
        for forbidden in ['.fulfill(', 'context.request', 'fixtureGET(', 'setTimeout(',
                          'waitForTimeout(', 'test.setTimeout(', '"POST"']:
            self.assertNotIn(forbidden, block)
        native = code.split('await test.step("native clipboard', 1)[1].split(
            'await test.step("mobile real data', 1)[0]
        self.assertIn('name: "清除用户资产关键词"', native)
        self.assertIn('await expect(field).toBeFocused()', native)
        self.assertIn('expect(reads).toBe(0)', native)

    def test_keyword_clear_real_browser_evidence_is_response_bound(self):
        self.assert_keyword_clear_browser(Path('web/e2e/user-assets-v3.spec.ts').read_text())

    def test_keyword_clear_browser_rejects_missing_request_focus_pin_or_capture_checks(self):
        code = Path('web/e2e/user-assets-v3.spec.ts').read_text()
        for old, new in [
            ('expect(implicitRequests).toBe(0)', 'expect(implicitRequests).toBe(1)'),
            ('await expect(keyword).toBeFocused();', ''),
            ('Object.entries(appliedQuery).sort()', '[]'),
            ('...appliedQuery, search: "", pageIdx: "1"', '...appliedQuery, search: "", pageIdx: "2"'),
            ('"user-assets-v3-mobile-keyword-clear.png"', '"unbound.png"'),
            ('"user-assets-v3-mobile-keyword-clear-list.png"', '"unbound-list.png"'),
            ('clearedPosition).toHaveText("1")', 'clearedPosition).toHaveText("2")'),
            ('appliedLast.value.list[0].objectGUID', 'appliedLast.value.list[1].objectGUID'),
            ('keyword-clear-apply-supersedes-list', 'unrelated'),
        ]:
            with self.subTest(mutation=old):
                self.assertIn(old, code)
                with self.assertRaises(AssertionError):
                    self.assert_keyword_clear_browser(code.replace(old, new, 1))

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
