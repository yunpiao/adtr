"""Offline evidence/source rejection tests, not PostgreSQL/browser acceptance."""
import copy
import io
import json
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zlib

import validate_role_users_read_evidence as evidence
import role_users_read_source_contract as source
from test_access_role_bulk_contract import png_fixture, runtime_fixture, object_paths, leaves, at

CANARY = 'SYNTHETIC_PRIVATE_CANARY_DO_NOT_EXPORT'


def encoded(value):
    return json.dumps(value, allow_nan=False, sort_keys=True).encode()


class RoleUsersReadEvidenceContract(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.folder = self.root / evidence.INPUT
        self.folder.mkdir(parents=True)
        self.sources = {}
        for path in evidence.SOURCE_PATHS:
            file = self.root / path
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_bytes(('synthetic source: ' + path).encode())
            self.sources[path] = evidence.digest(file.read_bytes())
        self.environment = dict(GITHUB_REPOSITORY='yunpiao/adtr', GITHUB_SHA='a' * 40,
            GITHUB_RUN_ID='123', GITHUB_RUN_ATTEMPT='1', GITHUB_EVENT_NAME='pull_request')
        self.tree = 'b' * 40
        self.identity = evidence.identity(self.environment, 'a' * 40, self.tree)
        self.images = [png_fixture(width=w, height=h) for w, h in evidence.DIMENSIONS]
        self.summary = dict(schemaVersion=2, case='role_users_readonly_workspace',
            status='browser_passed_after_legacy_context_cleanup', identity=self.identity,
            customRoleID='R' * 24, readerID=2, customUserID=3,
            requests=[dict(phase=phase, role=role, method='GET', status=200, roleBound=True, exactQuery=True,
                pageIdx=1, pageSize=size, searchKind=search, total=count, totalPage=count, exhausted=True,
                responseUsers=count, visibleUsers=count, userID=(2 if role == 'viewer' else 3) if count else None,
                rowFieldsMatch=True, previousDisabled=True, nextDisabled=True)
                for phase, role, search, size, count in evidence.READS],
            screenshots=[dict(file=file, bytes=len(image), sha256=evidence.digest(image), width=w, height=h,
                viewport=dict(width=w, height=h), fullPage=False, proofControls=0, visibleUsers=1,
                headingVisible=True, usernameVisible=True, roleVisible=True,
                layout=dict(mode='stacked' if w == 390 else 'table', tableDisplay='block' if w == 390 else 'table',
                    bodyDisplay='block' if w == 390 else 'table-row-group', rowDisplay='block' if w == 390 else 'table-row',
                    cellDisplay='block' if w == 390 else 'table-cell', cellsComputedDisplay=True, cellsWithinViewport=True,
                    contentFits=True, horizontalOverflow=False, headersVisuallyHidden=w == 390,
                    mobileFieldLabels=w == 390, visibleFields=6))
                for file, image, (w, h) in zip(evidence.FILES[1:], self.images, evidence.DIMENSIONS)],
            styleDelivery=dict(documentHTTP=200, documentSameOrigin=True, documentHTML=True,
                documentCSPExact=True, stylesheetSameOrigin=True, stylesheetRulePresent=True,
                inlineStyleElements=0, inlineStyleAttributes=0),
            checks=dict(mutatingRequests=0, readOnlyCheckPOSTs=0, userRequests=11, listClosed=True, detailReturned=True,
                viewerSelected=True, finalListVisible=True, localStorageEmpty=True, sessionStorageEmpty=True,
                extraSensitiveMutations=0, newContexts=0,
                pageNavigationBeyondSinglePage='not_exercised_one_user_per_role'), sourceSHA256=self.sources)
        self.runtime = runtime_fixture()
        (self.root / evidence.RUNTIME).parent.mkdir(parents=True)
        self.write_fixture()

    def write_fixture(self, summary=None, images=None, runtime=None):
        (self.folder / evidence.FILES[0]).write_bytes(encoded(self.summary if summary is None else summary))
        for name, image in zip(evidence.FILES[1:], self.images if images is None else images):
            (self.folder / name).write_bytes(image)
        (self.root / evidence.RUNTIME).write_bytes(encoded(self.runtime if runtime is None else runtime))

    def reject(self, summary=None):
        if summary is not None:
            (self.folder / evidence.FILES[0]).write_bytes(encoded(summary))
        with self.assertRaises((ValueError, OSError, zlib.error)) as failure:
            evidence.validate_and_stage(self.root, self.identity, self.tree)
        self.assertNotIn(CANARY, str(failure.exception))
        self.assertFalse((self.root / evidence.OUTPUT).exists())

    def invalid_summary(self, summary):
        with self.assertRaises(ValueError):
            evidence.validate_summary(summary, self.identity, self.sources, self.images)

    def test_positive_exact_three_file_stage_identity_sources_and_cleanup_boundary(self):
        result = evidence.validate_and_stage(self.root, self.identity, self.tree, self.sources)
        self.assertEqual(set(result), {'browser', 'artifactBinding', 'runnerObservation'})
        self.assertEqual(result['browser'], self.summary)
        self.assertEqual(result['artifactBinding'], dict(identity=self.identity, testedTree=self.tree,
            browserSummarySHA256=evidence.digest(encoded(self.summary)), sourceSHA256=self.sources))
        self.assertEqual(result['runnerObservation']['containerAbsence'], 'not_independently_attested')
        self.assertEqual(result['runnerObservation']['runtimeSHA256'], evidence.digest(encoded(self.runtime)))
        folder = self.root / evidence.OUTPUT
        self.assertEqual({p.name for p in folder.iterdir()}, set(evidence.FILES))
        for name, image in zip(evidence.FILES[1:], self.images):
            self.assertEqual((folder / name).read_bytes(), image)

    def test_all_object_keys_missing_wrong_type_and_extra_are_rejected(self):
        count = 0
        for path, original in object_paths(self.summary):
            for key in original:
                for mode in ('missing', 'wrong'):
                    bad = copy.deepcopy(self.summary)
                    target = at(bad, path)
                    if mode == 'missing':
                        del target[key]
                    else:
                        target[key] = {CANARY: CANARY}
                    with self.subTest(path=path, key=key, mode=mode):
                        self.invalid_summary(bad)
                    count += 1
            bad = copy.deepcopy(self.summary)
            at(bad, path)[CANARY] = CANARY
            self.invalid_summary(bad)
        self.assertGreater(count, 450)

    def test_integer_boolean_types_and_every_assertion_leaf_cannot_weaken(self):
        for path, value in leaves(self.summary):
            if type(value) not in (int, bool):
                continue
            bad = copy.deepcopy(self.summary)
            at(bad, path[:-1])[path[-1]] = (1 if value else 0) if type(value) is bool else True
            with self.subTest(path=path, mutation='type'):
                self.invalid_summary(bad)
            if type(value) is bool:
                bad = copy.deepcopy(self.summary)
                at(bad, path[:-1])[path[-1]] = not value
                with self.subTest(path=path, mutation='value'):
                    self.invalid_summary(bad)

    def test_requests_fixed_order_filters_paging_role_binding_and_user_identity(self):
        mutations = [lambda d: d['requests'].reverse(), lambda d: d['requests'].pop(),
            lambda d: d['requests'].append(copy.deepcopy(d['requests'][0])),
            lambda d: d.update(readerID=3), lambda d: d.update(customUserID=0),
            lambda d: d.update(customRoleID='viewer'), lambda d: d.update(customRoleID='../' + 'R' * 21),
            lambda d: d['checks'].update(mutatingRequests=1), lambda d: d['checks'].update(readOnlyCheckPOSTs=3),
            lambda d: d['checks'].update(extraSensitiveMutations=1), lambda d: d['checks'].update(newContexts=1),
            lambda d: d['checks'].update(pageNavigationBeyondSinglePage='passed'),
            lambda d: d['screenshots'].reverse(), lambda d: d['screenshots'].pop()]
        for index in range(len(evidence.READS)):
            for key, value in [('method', 'POST'), ('status', 403), ('pageIdx', 2), ('pageSize', -1),
                ('total', 2), ('visibleUsers', 2), ('userID', 999), ('role', 'platform_admin'),
                ('searchKind', 'wildcard'), ('phase', 'fabricated')]:
                bad = copy.deepcopy(self.summary)
                bad['requests'][index][key] = value
                self.invalid_summary(bad)
        for mutate in mutations:
            bad = copy.deepcopy(self.summary)
            mutate(bad)
            self.invalid_summary(bad)

    def test_run_source_and_screenshot_hashes_bound_to_current_exact_bytes(self):
        for key in self.identity:
            bad = copy.deepcopy(self.summary)
            bad['identity'][key] = CANARY
            self.invalid_summary(bad)
        for path in self.sources:
            bad = copy.deepcopy(self.summary)
            bad['sourceSHA256'][path] = '0' * 64
            self.invalid_summary(bad)
        for index in range(2):
            for key, value in [('sha256', '0' * 64), ('bytes', 1), ('file', '../private.png'), ('width', 1), ('height', 1)]:
                bad = copy.deepcopy(self.summary)
                bad['screenshots'][index][key] = value
                self.invalid_summary(bad)
        (self.root / evidence.SOURCE_PATHS[0]).write_bytes(b'changed source')
        self.reject()

    def test_csp_external_style_and_six_field_layout_evidence_cannot_regress_to_partial_columns(self):
        for index in range(2):
            for key, value in [('mode', 'unknown'), ('tableDisplay', 'grid'), ('bodyDisplay', 'flex'),
                ('rowDisplay', 'inline'), ('cellDisplay', 'contents'), ('visibleFields', 2),
                ('cellsComputedDisplay', False), ('cellsWithinViewport', False), ('contentFits', False),
                ('horizontalOverflow', True)]:
                bad = copy.deepcopy(self.summary)
                bad['screenshots'][index]['layout'][key] = value
                self.invalid_summary(bad)
        bad = copy.deepcopy(self.summary)
        bad['screenshots'][1]['layout'] = copy.deepcopy(bad['screenshots'][0]['layout'])
        self.invalid_summary(bad)
        for key, value in [('documentHTTP', 302), ('documentSameOrigin', False), ('documentHTML', False),
            ('documentCSPExact', False), ('stylesheetSameOrigin', False), ('stylesheetRulePresent', False),
            ('inlineStyleElements', 1), ('inlineStyleAttributes', 1)]:
            bad = copy.deepcopy(self.summary)
            bad['styleDelivery'][key] = value
            self.invalid_summary(bad)
        bad = copy.deepcopy(self.summary)
        bad['schemaVersion'] = 1
        self.invalid_summary(bad)
        # The original 43-source/partial-column manifest is insufficient for
        # this successor even when someone updates its hashes and PNG metadata.
        bad = copy.deepcopy(self.summary)
        del bad['styleDelivery']
        for item in bad['screenshots']:
            del item['layout']
        self.invalid_summary(bad)

    def test_source_snapshot_cannot_change_between_checkout_and_stage(self):
        target = self.root / evidence.SOURCE_PATHS[0]
        target.write_bytes(b'changed source')
        self.summary['sourceSHA256'][evidence.SOURCE_PATHS[0]] = evidence.digest(target.read_bytes())
        self.write_fixture()
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(self.root, self.identity, self.tree, dict(self.sources, **{evidence.SOURCE_PATHS[0]: '0' * 64}))
        self.assertFalse((self.root / evidence.OUTPUT).exists())

    def test_bounded_duplicate_free_json_rejected_without_staging(self):
        for raw in [b'', b'{' * 20, b'[]', b'{"a":1,"a":2}', b'{"a":NaN}', b' ' * (evidence.MAX_JSON + 1),
            b'[' * 13 + b']' * 13, b'\xff']:
            (self.folder / evidence.FILES[0]).write_bytes(raw)
            self.reject()

    def test_exact_membership_no_preexisting_output_or_symlink(self):
        extra = self.folder / 'private.log'
        extra.write_text(CANARY)
        self.reject()
        extra.unlink()
        for name in evidence.FILES:
            file = self.folder / name
            content = file.read_bytes()
            file.unlink()
            self.reject()
            outside = self.root / ('other-' + name)
            outside.write_bytes(content)
            file.symlink_to(outside)
            self.reject()
            file.unlink()
            file.write_bytes(content)
        (self.root / evidence.OUTPUT).mkdir(parents=True)
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(self.root, self.identity, self.tree)

    def test_symlinked_source_and_ancestor_and_root_rejected(self):
        file = self.root / evidence.SOURCE_PATHS[1]
        original = file.read_bytes()
        other = self.root / 'source-copy'
        other.write_bytes(original)
        file.unlink()
        file.symlink_to(other)
        self.reject()
        file.unlink()
        file.write_bytes(original)
        alias = self.root.parent / (self.root.name + '-alias')
        alias.symlink_to(self.root, target_is_directory=True)
        self.addCleanup(alias.unlink)
        with self.assertRaises(ValueError):
            evidence.validate_and_stage(alias, self.identity, self.tree)
        parent = self.root / 'web/playwright-report'
        elsewhere = self.root / 'elsewhere'
        elsewhere.mkdir()
        parent.symlink_to(elsewhere, target_is_directory=True)
        self.reject()

    def test_runtime_cleanup_success_required_and_raw_capture_disallowed(self):
        for mutate in [lambda d: d['phases'].pop(), lambda d: d['phases'].reverse(),
            lambda d: d['processes']['api'].update(after_cleanup=1),
            lambda d: d['processes']['api'].update(forced_kill=True),
            lambda d: d['processes']['worker'].update(started=True),
            lambda d: d.update(http_panic=True), lambda d: d.update(browser_capture='done'),
            lambda d: d.update(collection_errors=[CANARY])]:
            bad = copy.deepcopy(self.runtime)
            mutate(bad)
            self.write_fixture(runtime=bad)
            self.reject()

    def test_png_closed_structure_size_crc_dimensions_decompression_and_metadata(self):
        from test_access_role_bulk_contract import chunk
        for index, ((w, h), image) in enumerate(zip(evidence.DIMENSIONS, self.images)):
            variants = [b'', image[:-1], image + b'private', b'x' * (evidence.MAX_PNG + 1),
                image[:30] + bytes([image[30] ^ 1]) + image[31:],
                png_fixture(width=w + 1, height=h), png_fixture(width=w, height=h, before=chunk(b'tEXt', CANARY.encode())),
                png_fixture(width=w, height=h, compressed=zlib.compress(b'\0') + b'extra'),
                png_fixture(width=w, height=h, pixels=b'\0' * ((w * 4 + 1) * h + 1)),
                png_fixture(width=w, height=h, pixels=(b'\5' + b'\0' * w * 4) * h)]
            for bad in variants:
                images = list(self.images)
                images[index] = bad
                self.write_fixture(images=images)
                self.reject()

    def test_checkout_uses_fixed_bounded_git_and_immutable_head_blobs(self):
        blobs = {path: evidence.hashlib.sha1(b'blob ' + str(len((self.root / path).read_bytes())).encode() + b'\0' + (self.root / path).read_bytes()).hexdigest()
                 for path in evidence.SOURCE_PATHS}
        def run(args, **kwargs):
            self.assertEqual(kwargs, dict(cwd=self.root, check=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=10))
            if args == ['git', 'rev-parse', 'HEAD']: raw = b'a' * 40 + b'\n'
            elif args == ['git', 'rev-parse', 'HEAD^{tree}']: raw = self.tree.encode() + b'\n'
            elif args == ['git', 'diff', '--quiet', 'HEAD', '--']: raw = b''
            elif args == ['git', 'ls-files', '--error-unmatch', '--', *evidence.SOURCE_PATHS]: raw = ('\n'.join(evidence.SOURCE_PATHS) + '\n').encode()
            elif args == ['git', 'ls-tree', '-z', 'HEAD', '--', *evidence.SOURCE_PATHS]: raw = ''.join(f'100644 blob {blobs[p]}\t{p}\0' for p in evidence.SOURCE_PATHS).encode()
            else: self.fail('unexpected git command')
            return subprocess.CompletedProcess(args, 0, stdout=raw)
        with patch.object(evidence.subprocess, 'run', side_effect=run):
            self.assertEqual(evidence.validate_checkout(self.root, self.environment), (self.identity, self.tree, self.sources))
            (self.root / evidence.SOURCE_PATHS[0]).write_bytes(b'changed hidden worktree bytes')
            with self.assertRaises(ValueError):
                evidence.validate_checkout(self.root, self.environment)

    def test_cli_failure_is_closed_and_never_echoes_raw_environment_or_argv(self):
        out, err = io.StringIO(), io.StringIO()
        with patch('sys.stdout', out), patch('sys.stderr', err):
            self.assertEqual(evidence.main(['--' + CANARY], {CANARY: CANARY}), 1)
        self.assertEqual(out.getvalue(), '')
        self.assertEqual(err.getvalue(), evidence.ERROR + '\n')
        self.assertNotIn(CANARY, err.getvalue())


class RoleUsersReadSourceContract(unittest.TestCase):
    def test_exact_outer_recovers_both_frozen_predecessors(self):
        import test_access_role_bulk_contract as old
        current = Path('web/e2e/access.spec.ts').read_bytes().decode()
        predecessor = source.restore_role_users_spec(current)
        self.assertEqual(len(predecessor.encode()), old.SPEC_BYTES)
        self.assertEqual(source.digest(predecessor.encode()), old.SPEC_SHA256)
        self.assertEqual(source.digest(old.restore_access_spec(predecessor).encode()), old.BASE_SPEC)
        self.assertLess(current.index('await readerContext.close();'), current.index('await verifyRoleUsersRead('))
        self.assertLess(current.index('// End AD-F-176 success writer.'), current.index('await verifyRoleUsersRead('))

    def test_every_added_line_sentinel_anchor_and_old_byte_drift_rejects(self):
        text = Path('web/e2e/access.spec.ts').read_bytes().decode()
        variants = [text + '\n', text.replace('\n', '\r\n'), text.replace('test.setTimeout(300_000)', 'test.setTimeout(400_000)')]
        for anchor, begin, end, _, _ in source.BLOCKS:
            block = text[text.index(begin):text.index(end) + len(end)]
            variants += [text.replace(block, '', 1), text.replace(block, block * 2, 1), text.replace(block, '', 1) + block,
                text.replace(anchor, '', 1) + anchor]
            lines = block.splitlines(keepends=True)
            for index in range(len(lines)):
                variants += [text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1),
                    text.replace(block, ''.join(lines[:index] + [lines[index], lines[index]] + lines[index + 1:]), 1),
                    text.replace(block, ''.join(lines[:index] + [lines[index].rstrip('\n') + ' // changed\n'] + lines[index + 1:]), 1),
                    text.replace(block, ''.join(lines[:index] + lines[index + 1:]), 1) + lines[index]]
        for bad in variants:
            with self.assertRaises(ValueError):
                source.restore_role_users_spec(bad)

    def test_whole_original_bulk_contract_recovers_after_both_exact_outer_hooks(self):
        raw = Path('scripts/test_access_role_bulk_contract.py').read_bytes().decode()
        previous = source.restore_role_users_bulk_contract(raw)
        self.assertEqual(source.digest(previous.encode()), source.BASE_BULK_TEST)
        from test_role_users_read_workflow_contract import SETUP
        for hook in (source.OUTER_READER, SETUP):
            self.assertEqual(raw.count(hook), 1)
            variants = [raw.replace(hook, '', 1), raw.replace(hook, hook * 2, 1), raw.replace(hook, '', 1) + hook]
            lines = hook.splitlines(keepends=True)
            for index, line in enumerate(lines):
                variants += [raw.replace(hook, ''.join(lines[:index] + lines[index + 1:]), 1),
                    raw.replace(hook, ''.join(lines[:index] + [line, line] + lines[index + 1:]), 1),
                    raw.replace(hook, ''.join(lines[:index] + [line.rstrip('\n') + ' # changed\n'] + lines[index + 1:]), 1)]
            for bad in variants:
                with self.assertRaises(ValueError):
                    source.restore_role_users_bulk_contract(bad)
        with self.assertRaises(ValueError):
            source.restore_role_users_bulk_contract(raw.replace('SPEC_BYTES = 52056', 'SPEC_BYTES = 52057'))

    def test_entire_new_browser_helper_and_each_line_mutation_are_rejected(self):
        raw = Path('web/e2e/role-users-read.ts').read_bytes()
        source.validate_role_users_browser_helper(raw)
        lines = raw.splitlines(keepends=True)
        for index, line in enumerate(lines):
            variants = [b''.join(lines[:index] + lines[index + 1:]),
                b''.join(lines[:index] + [line, line] + lines[index + 1:]),
                b''.join(lines[:index] + [line.rstrip(b'\n') + b' // changed\n'] + lines[index + 1:]),
                b''.join(lines[:index] + lines[index + 1:] + [line])]
            for bad in variants:
                if bad != raw:
                    with self.assertRaises(ValueError):
                        source.validate_role_users_browser_helper(bad)
        for raw in (None, True, '', b'', raw + b'\n', raw.replace(b'\n', b'\r\n')):
            with self.assertRaises(ValueError):
                source.validate_role_users_browser_helper(raw)

    def test_exact_fixed_label_cssom_serializations_execute_actual_browser_constant(self):
        text = Path('web/e2e/role-users-read.ts').read_text()
        begin, end = '// ROLE-USERS-LABEL-CONTENT-BEGIN\n', '// ROLE-USERS-LABEL-CONTENT-END\n'
        self.assertEqual(text.count(begin), 1)
        self.assertEqual(text.count(end), 1)
        declaration = text[text.index(begin) + len(begin):text.index(end)]
        # No TypeScript/compiler dependency: this actual producer declaration is
        # JavaScript, executed by the repository's required Node runtime.
        program = declaration + r"""
const fixed = ["当前角色", "手机号", "邮箱", "账户状态", "MFA 状态"];
let accepted = 0, rejected = 0;
if (roleUsersLabelContent.length !== fixed.length) throw new Error("label count");
for (const [index, entry] of roleUsersLabelContent.entries()) {
  const label = fixed[index];
  const joined = JSON.stringify(label + "："), tokens = JSON.stringify(label) + " " + JSON.stringify("：");
  if (entry.label !== label || entry.content.length !== 2 || entry.content[0] !== joined || entry.content[1] !== tokens) throw new Error("labels differ");
  for (const content of [joined, tokens]) {
    if (!entry.content.includes(content)) throw new Error("valid serialization rejected");
    accepted++;
  }
  for (const content of ["", "none", "normal", null, 1, "attr(data-label) \"：\"", JSON.stringify(label),
      JSON.stringify("其他："), joined + " ", " " + joined, tokens.replace(" ", "  "), tokens + " \"extra\"",
      JSON.stringify("："), JSON.stringify(label + ":"), tokens.replace("：", ":")]) {
    if (entry.content.includes(content)) throw new Error("invalid serialization accepted");
    rejected++;
  }
}
console.log(JSON.stringify({accepted, rejected}));
"""
        result = subprocess.run(['node', '--input-type=module', '-'], input=program.encode(), check=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
        self.assertEqual(json.loads(result.stdout), dict(accepted=10, rejected=75))
        self.assertEqual(result.stderr, b'')

    def test_helper_source_identity_and_no_mock_retry_or_new_sensitive_action(self):
        text = Path('web/e2e/role-users-read.ts').read_text()
        block = text.split('  // ROLE-USERS-SOURCES-BEGIN\n')[1].split('  // ROLE-USERS-SOURCES-END\n')[0]
        paths = [json.loads(line.strip().rstrip(',')) for line in block.splitlines()]
        self.assertEqual(paths, list(evidence.SOURCE_PATHS))
        self.assertEqual(paths, sorted(set(paths)))
        for denied in ('.route(', '.fulfill(', '.request.', '.newContext(', 'freshCode(', 'waitForTimeout(', 'setTimeout(', 'process.env.ADTR_E2E_PASSWORD', 'actorPassword', 'storageState(', 'fetch(', 'readRoleUsers('):
            self.assertNotIn(denied, text)
        for required in ('Promise.allSettled([pending, action()])', 'const response = await request.response()', 'mutations === 0',
            'userRequests === requests.length', 'page.off("request", observer)', 'fullPage: false', 'flag: "wx"'):
            self.assertIn(required, text)
        self.assertIn('"not_exercised_one_user_per_role"', text)
        self.assertEqual(text.count('await page.screenshot('), 1)
        self.assertEqual(len(evidence.READS), 11)
        self.assertEqual(len(evidence.SOURCE_PATHS), 45)
        for path in ('cmd/adtr/main.go', 'web/src/role-users-read.css'):
            self.assertIn(path, evidence.SOURCE_PATHS)
        self.assertIn('maxRedirects: 0', text)
        self.assertEqual(text.count('requestContext.get('), 1)
        self.assertIn('documentResponse.dispose()', text)
        self.assertIn('headers["content-security-policy"]', text)
        self.assertIn('Array.from(document.styleSheets)', text)
        self.assertIn('field.getAttribute("data-label") === labelContent[index].label', text)
        self.assertIn('labelContent[index].content.includes(', text)
        self.assertIn('new URL(sheet.href).origin !== location.origin', text)
        for required in ('layout.cellsComputedDisplay', 'layout.cellsWithinViewport', 'layout.contentFits',
            '!layout.horizontalOverflow', 'layout.headersVisuallyHidden === (width === 390)',
            'layout.mobileFieldLabels === (width === 390)', 'layout.visibleFields === 6'):
            self.assertIn(required, text)
        self.assertIn('retries: 0', Path('web/playwright.config.ts').read_text())
        self.assertIn('else 420)', Path('scripts/test_auth_e2e.py').read_text())


if __name__ == '__main__':
    unittest.main()
