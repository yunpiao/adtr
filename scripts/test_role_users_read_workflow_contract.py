"""Exact current evidence wiring and byte-preserved historical guard execution.

This is an offline source contract, not browser, PostgreSQL or visual acceptance.
Only the two explicitly named legacy classes use the historical fixture below.
"""
import copy
import hashlib
import json
from pathlib import Path
import unittest
from unittest.mock import patch

import ci_workflow_contract as workflow
import role_users_read_workflow as outer

PATHS = {'workflow': '.github/workflows/ci.yml', 'helper': 'scripts/ci_workflow_contract.py'}
PINS = {
    'workflow': ('a3b5ec391fb478352d289c032e19e52d617d9b08d769403562b10a63219e7a3b',
                 '90091e86fc5da2d73f931b417a411725555a61608bb7117020c3bf88acc1b9e9'),
    'helper': ('ba9c22970570006d3bf720cae5c8bd0512ce32be9f9b7d06d9047eecbbce5484',
               '35dddef9007222fa2bcacd954a24d06ef7ef7f8808f2af4299ddf9d5527788fc'),
}
DELTA_SHA = 'f0d56c5cd88ffe54f82ddb653d52d70728639920900cc3b45f140b8503dc42f9'
MODULE_SHA = '41a5d3274119b9a1639bb466ce36c063559e7e6c467292d08832419db22571a6'
SETUP = ('    def setUp(self):\n'
         '        from test_role_users_read_workflow_contract import historical_workflow_fixture\n'
         '        historical_workflow_fixture(self)\n\n')
UAC_WRAPPER = ("def _restore(text, kind, domain):\n"
               "    if kind == 'ci' and domain in ('workflow', 'helper'):\n"
               "        from role_users_read_workflow import restore_role_users_read\n"
               "        text = restore_role_users_read(text, domain)\n"
               "    return _restore_predecessor(text, kind, domain)\n\n\n"
               "def _restore_predecessor(text, kind, domain):\n")


def digest(raw):
    return hashlib.sha256(raw).hexdigest()



def restore_bulk_workflow_test_outer(text):
    """Remove this exact class setup; caller pins its remaining whole file."""
    try:
        marker = 'class AccessRoleBulkWorkflowContract(unittest.TestCase):\n'
        end = '# Frozen source additions;'
        if type(text) is not str or text.count(marker) != 1 or text.count(end) != 1:
            raise ValueError
        block = text[text.index(marker):text.index(end)]
        if not block.startswith(marker + SETUP) or block.count(SETUP) != 1:
            raise ValueError
        previous = block.replace(SETUP, '', 1)
        if digest(previous.encode('utf-8')) != '30cad7b41041c43b73d78936b80cabc850e32aa0df272a618b87604dddf2d2ef':
            raise ValueError
        return text.replace(block, previous, 1)
    except (ValueError, UnicodeError):
        raise ValueError('role_users_read_bulk_test_projection_invalid') from None


def historical_workflow_fixture(case):
    """Run unchanged old assertions against exact prior bytes and actual guards.

    First validate both actual current files. Then patch only these two paths and
    the two explicit old entrypoints, with unconditional per-test cleanup. This
    does not widen the current-only entrypoints or normalize old mutations.
    """
    import test_user_asset_export_v3_uac_runner_contract as uac
    previous = {path: outer.restore_role_users_read(Path(path).read_bytes().decode('utf-8'), domain).encode('utf-8')
                for domain, path in PATHS.items()}
    original_bytes, original_text = Path.read_bytes, Path.read_text

    def read_bytes(path, *args, **kwargs):
        if str(path) in previous:
            return previous[str(path)]
        return original_bytes(path, *args, **kwargs)

    def read_text(path, *args, **kwargs):
        if str(path) in previous:
            return previous[str(path)].decode('utf-8')
        return original_text(path, *args, **kwargs)

    for patcher in (patch.object(Path, 'read_bytes', read_bytes),
                    patch.object(Path, 'read_text', read_text),
                    patch.object(workflow, 'restore_user_uac_workflow', workflow.restore_user_uac_workflow_predecessor),
                    patch.object(uac, '_restore', uac._restore_predecessor)):
        patcher.start()
        case.addCleanup(patcher.stop)


class RoleUsersReadWorkflowContract(unittest.TestCase):
    def setUp(self):
        self.sources = {domain: Path(path).read_bytes().decode('utf-8') for domain, path in PATHS.items()}
        self.delta = json.loads(Path(outer.DELTA).read_bytes())

    def test_complete_current_prior_fixture_and_module_are_independently_pinned(self):
        self.assertEqual(digest(Path(outer.DELTA).read_bytes()), DELTA_SHA)
        self.assertEqual(digest(Path('scripts/role_users_read_workflow.py').read_bytes()), MODULE_SHA)
        self.assertEqual(outer.ANCHORS, PINS)
        for domain, source in self.sources.items():
            self.assertEqual(digest(source.encode()), PINS[domain][1])
            previous = outer.restore_role_users_read(source, domain)
            self.assertEqual(digest(previous.encode()), PINS[domain][0])
            with self.assertRaises(ValueError):
                outer.restore_role_users_read(previous, domain)
            for value in (None, True, 1, b'', [], {}, source + '\n', source.replace('\n', '\r\n')):
                with self.subTest(domain=domain, value=type(value).__name__), self.assertRaises(ValueError):
                    outer.restore_role_users_read(value, domain)
        for domain in (None, True, [], {}, 'runner', 'inventory'):
            with self.assertRaises(ValueError):
                outer.restore_role_users_read(self.sources['workflow'], domain)

    def test_exact_two_changes_preserve_steps_order_gates_budgets_permissions_and_old_files(self):
        old = outer.restore_role_users_read(self.sources['workflow'])
        changes = self.delta['files']['workflow']['replacements']
        self.assertEqual(len(changes), 2)
        self.assertEqual(changes[0], {
            'old': '        run: python3 scripts/validate_access_role_bulk_evidence.py --source-root .\n',
            'new': '        run: |\n          python3 scripts/validate_access_role_bulk_evidence.py --source-root .\n          python3 scripts/validate_role_users_read_evidence.py --source-root .\n'})
        self.assertEqual(changes[1], {
            'old': '            web/playwright-report/access-role-bulk-validated/access-role-bulk-users-1440.png\n',
            'new': '            web/playwright-report/access-role-bulk-validated/access-role-bulk-users-1440.png\n'
                   '            web/playwright-report/role-users-read-validated/role-users-read-evidence.json\n'
                   '            web/playwright-report/role-users-read-validated/role-users-read-1440.png\n'
                   '            web/playwright-report/role-users-read-validated/role-users-read-390.png\n'})
        rebuilt = old
        for change in changes:
            self.assertEqual(rebuilt.count(change['old']), 1)
            rebuilt = rebuilt.replace(change['old'], change['new'], 1)
        self.assertEqual(rebuilt, self.sources['workflow'])
        # Every byte outside the two exact evidence replacements is protected by
        # the independent complete predecessor pin above, not a parsed YAML subset.
        for prefix in ('      - ', '    needs:', '    permissions:', '    timeout-minutes:',
                       '        timeout-minutes:', '        if:', '          name:', '    runs-on:'):
            self.assertEqual([line for line in old.splitlines() if line.startswith(prefix)],
                             [line for line in rebuilt.splitlines() if line.startswith(prefix)])

    def test_all_current_consumers_enter_outer_once_and_reject_previous_workflow(self):
        current = self.sources['workflow']
        previous = outer.restore_role_users_read(current)
        consumers = (workflow.restore_user_uac_workflow, workflow.restore_native_phase_budgets,
                     workflow.restore_auth_shards16, workflow.restore_access_role_bulk,
                     workflow.restore_archive_target_observation, workflow.restore_health_recovery,
                     workflow.restore_messages_isolation, workflow.restore_directory_v4, workflow.restore_selection)
        for consumer in consumers:
            with self.subTest(consumer=consumer.__name__):
                with patch.object(outer, 'restore_role_users_read', wraps=outer.restore_role_users_read) as inverse:
                    restored = consumer(current)
                    self.assertEqual(inverse.call_count, 1)
                with self.assertRaises(ValueError):
                    consumer(previous)
                with patch.object(workflow, 'restore_user_uac_workflow', workflow.restore_user_uac_workflow_predecessor):
                    expected = (workflow.restore_user_uac_workflow_predecessor(previous)
                                if consumer.__name__ == 'restore_user_uac_workflow' else consumer(previous))
                self.assertEqual(restored, expected)

    def test_every_added_line_and_unrelated_current_byte_mutations_reject(self):
        for domain, source in self.sources.items():
            variants = [source + '\n', source[:-1], source.replace('\n', '\r\n')]
            for change in self.delta['files'][domain]['replacements']:
                block = change['new']; lines = block.splitlines(keepends=True)
                variants.extend(source.replace(block, value, 1) for value in ('', block * 2, change['old']))
                variants.append(source.replace(block, '', 1) + block)
                for index, line in enumerate(lines):
                    for altered in (lines[:index] + lines[index + 1:],
                                    lines[:index] + [line] + lines[index:],
                                    lines[:index] + [line.rstrip('\n') + ' # drift\n'] + lines[index + 1:]):
                        variants.append(source.replace(block, ''.join(altered), 1))
            if domain == 'workflow':
                for before, after in [('contents: read', 'contents: write'), ('run: make check', 'run: true'),
                                      ('timeout-minutes: 1', 'timeout-minutes: 2'), ('max-parallel: 8', 'max-parallel: 9'),
                                      ('success()', 'always()'), ('needs.plan.outputs', 'needs.other.outputs'),
                                      ('if-no-files-found: error', 'if-no-files-found: warn')]:
                    self.assertIn(before, source)
                    variants.append(source.replace(before, after, 1))
            for bad in variants:
                with self.subTest(domain=domain, sha=digest(bad.encode())), self.assertRaises(ValueError):
                    outer.restore_role_users_read(bad, domain)
                if domain == 'workflow':
                    for consumer in (workflow.restore_user_uac_workflow, workflow.restore_native_phase_budgets,
                                     workflow.restore_auth_shards16, workflow.restore_access_role_bulk,
                                     workflow.restore_archive_target_observation, workflow.restore_health_recovery,
                                     workflow.restore_messages_isolation, workflow.restore_directory_v4, workflow.restore_selection):
                        with self.subTest(consumer=consumer.__name__), self.assertRaises(ValueError):
                            consumer(bad)

    def test_fixture_mutations_and_attacker_rehash_never_authorize(self):
        raw = Path(outer.DELTA).read_bytes()
        variants = [b'', b' ' * 16385, b'{' , b'\xff', raw + b'\n', raw[:-1],
                    raw.replace(b'\n', b'\r\n'), b'{"format":1,"format":1}', b'[' * 20000,
                    json.dumps(self.delta).encode()]
        for domain in PATHS:
            bad = copy.deepcopy(self.delta)
            bad['files'][domain]['replacements'][0]['new'] += '# unauthorized\n'
            bad['files'][domain]['currentSHA256'] = digest((self.sources[domain] + '# unauthorized\n').encode())
            variants.append((json.dumps(bad, indent=2) + '\n').encode())
        original = Path.read_bytes
        for bad in variants:
            def read(path):
                return bad if str(path) == outer.DELTA else original(path)
            with patch.object(Path, 'read_bytes', read):
                for domain, source in self.sources.items():
                    with self.subTest(domain=domain, sha=digest(bad)), self.assertRaises(ValueError):
                        outer.restore_role_users_read(source, domain)

    def test_all_original_uac_and_bulk_workflow_assertions_and_mutations_are_byte_identical(self):
        text = Path('scripts/test_user_asset_export_v3_uac_runner_contract.py').read_bytes().decode('utf-8')
        self.assertEqual(text.count(UAC_WRAPPER), 1)
        self.assertEqual(text.count(SETUP), 1)
        old = text.replace(UAC_WRAPPER, 'def _restore(text, kind, domain):\n', 1).replace(SETUP, '', 1)
        self.assertEqual(digest(old.encode()), '8effb30057e8364dd5662ca83a9088fee3a1dd55299270372cf3acd5d7b8956c')
        text = Path('scripts/test_access_role_bulk_contract.py').read_bytes().decode('utf-8')
        block = text[text.index('class AccessRoleBulkWorkflowContract'):text.index('# Frozen source additions;')]
        self.assertEqual(block.count(SETUP), 1)
        self.assertEqual(digest(block.replace(SETUP, '', 1).encode()),
                         '30cad7b41041c43b73d78936b80cabc850e32aa0df272a618b87604dddf2d2ef')

    def test_historical_fixture_is_exact_and_cleanup_restores_actual_current_even_on_failure(self):
        import test_user_asset_export_v3_uac_runner_contract as uac
        original = (Path.read_bytes, Path.read_text, workflow.restore_user_uac_workflow, uac._restore)
        case = unittest.TestCase()
        try:
            historical_workflow_fixture(case)
            for domain, path in PATHS.items():
                self.assertEqual(digest(Path(path).read_bytes()), PINS[domain][0])
                self.assertEqual(Path(path).read_text().encode(), Path(path).read_bytes())
            self.assertIs(workflow.restore_user_uac_workflow, workflow.restore_user_uac_workflow_predecessor)
            self.assertIs(uac._restore, uac._restore_predecessor)
            with self.assertRaises(AssertionError):
                self.fail('synthetic failing historical case still cleans up')
        finally:
            case.doCleanups()
        self.assertEqual((Path.read_bytes, Path.read_text, workflow.restore_user_uac_workflow, uac._restore), original)
        for domain, path in PATHS.items():
            self.assertEqual(digest(Path(path).read_bytes()), PINS[domain][1])

    def test_legacy_guard_removal_remains_sensitive_and_uses_actual_predecessor(self):
        import test_user_asset_export_v3_uac_runner_contract as uac
        case = uac.UACProjectionContract('test_auth_schema_negative_test_detects_removed_schema_guard')
        try:
            case.setUp()
            with patch.object(outer, 'restore_role_users_read', side_effect=AssertionError('outer reentry')):
                # The fixture retained the actual original predecessor function;
                # the original test must detect the removed auth schema guard.
                case.test_auth_schema_negative_test_detects_removed_schema_guard()
        finally:
            case.doCleanups()


if __name__ == '__main__':
    unittest.main()
