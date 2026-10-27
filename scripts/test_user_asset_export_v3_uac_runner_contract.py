"""C-only runner wiring, immutable predecessor projections and teardown contracts.

These offline contracts are not LDAP/browser evidence. Each raw-current entry
strips C exactly once; explicit predecessor functions retain historical tests.
No imports of historical test modules at module load (avoids recursive chains).
"""
import ast
import copy
import hashlib
import io
import json
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import Mock, patch

SUITE = 'user-asset-export-v3-uac-mixed33'
BASE_COMMIT = '59716a3d9dc7dae83f6c8c45cb0d222926dc066b'
BASE_TREE = 'c7d2a54d325ddcf38e9b59087e691845191fb64b'

DELTA_SHA256 = {'ci': '5beab043c94c3e833508d8577fe2a2317620083cc69e11b397f93203f2d8cb26', 'runner': 'd87aec5853b0ef3659776c902af1f56af8fc155abed14f3baa872fce2584c5a0'}

# Fixed anchors for independent review, outside both mutable delta files. Never rederive at runtime.
ANCHORS = {'ci': {'workflow': {'path': '.github/workflows/ci.yml',
                     'baseline': 'cc1a98f8f385e43f0498635c1b8c0138184b2e23600c963a490ec83f876f79ff',
                     'current': 'a3b5ec391fb478352d289c032e19e52d617d9b08d769403562b10a63219e7a3b',
                     'hunks': (('bbde010baa2a4019f39a7b4706a6720755a7c7bb0168ec8b987c02e502358b9c',
                                'e2a2fd8501d14d7d7860b64e4cec027ca3f4db626a23f31af289eb56477d37e1'),
                               ('48a0a4f778b7cbaf08f5e729731d192bcf3aba7339935c6da92e03b229ea8411',
                                'bc7c39e3ab9cb4ae4c04c3b9ceaa3093b5feef0c1a07e6d37c0c70bb95e31297'))},
        'helper': {'path': 'scripts/ci_workflow_contract.py',
                   'baseline': 'db43de4f6a8668e14d2c9b2053fa115196a12e0399eea73decbdcc57a56b0f21',
                   'current': 'ba9c22970570006d3bf720cae5c8bd0512ce32be9f9b7d06d9047eecbbce5484',
                   'hunks': (('3a06a3dad77242b00f5ac8003aac102ecbc5c5c9221cf52a7abc5f340a44450e',
                              '31ac90d2bcf9ad52b63933939468bde121e1fdabd7989e3d1441e2eacee41aa7'),
                             ('104a744828f3b8c4303d01aeb8c38c901ee4fa3ebb3c9981025d9c52b61ff883',
                              'ac52b56a818f1f89d772a12fcde85e4ddb4773bebc3bd94ca37fe611456897e8'),
                             ('c9ed65e6682dec91818d7d58d6fcee275e86a507e80b5ce246f48dd1368db906',
                              '03fa2638ed8ac7c63d560080350cb700508be390d53b3f34f3e27a7e696119d1'))},
        'inventory': {'path': 'scripts/ci_inventory.json',
                      'baseline': '1d9ef348def9d9b55ee4d0f8bb7da340a3c9049f9e90b5826bd71f3a39c47f24',
                      'current': '23548656f02df5edd476107c65869cc8f00826004c82a6e8a0647528c36feaba',
                      'hunks': (('59e3a3232d188b89419c266d35303596d76f19410a35d0d28c3de5f049a268f0',
                                 '08c05135ec6aa9e6476758a9209b2ed3dee34fb004a1ef3cb1fd0a575c426196'),
                                ('cba97ea960b9724437f6288eb21480f201b71757bb8e5efbecfcef547205d532',
                                 '05b34f2c9bf0a23c5cba9ff2ebe94d4fa5fd73a9a9d839e3d89cd9a11750c2a9'),
                                ('3e4092653479f08bf53369fa321059ff3599fa808e4c69ef89d8ce3f6b357d62',
                                 'f863d89b7a290bf67ba2e698ebcfa761d49e08ce3934119ebb5871e3162f9b84'))},
        'authTest': {'path': 'scripts/test_auth_shards16_contract.py',
                     'baseline': '543cb239b7799593f31b9636a245398f5bce84c2b358ab8bacfaeb617f4e7a20',
                     'current': 'b42b963e79480997a56aae92e47c39ef723676e739ec68e54e44902c5b2f8b84',
                     'hunks': (('2c07627147ca4990fb6f881c8ebb6542eac4748b0f5a9d617ffe7c917cd5ed02',
                                '45f3067b987746e126b705db9de1ff2e9529706e44b8e093adb4eb96a18440cf'),
                               ('85384d22fd8358a199f877ed77dfcdc39797abab6790b8e6435166169a51d5d6',
                                'df4cbad4910c2ffa11c92f59ab65b51491aada49c559147e6d87d655dbfb3208'),
                               ('337aa3246cdb5c5d0861446f037ad8dc869170472278c108ed2d57af08f2983f',
                                'c8d2c7efbce65d59e16e8cf153fc7d01b1a1a58dc1c613f9d0f4cfa30f7869c0'),
                               ('2b2f816e249c0d1a11e049f9aa3c7d0528a366a48ff31de4d18818bad8c268f1',
                                '887c924423785ddeb5105df27412ec968e318bcf522d4060c0ab5559f1ef888f'),
                               ('975a4c92072a6d88e34d099d4094dc5171781328712e35714639230eb7e4ebf1',
                                '119fb3719450a8bfa533e751e81a84256ab5d95eabeabe2888742ff83f91df9c'),
                               ('3ece8b5ddf4498f71da454aabe2d7c8d661e41b3058efdf1ab19098b86607bf4',
                                '94070048008f22e237a322e0e0b0cc860fbd94f8d3ee268a7a60abd6f923d373'),
                               ('1b8846f9a6c0b2c917deaafc4aba0ac733e95e9143adcb98579525779317e20f',
                                'd22fdc880a70407480914864e413674483c48cdffa459f36129205169d894b6d'),
                               ('c40a2cbab856c6614080b99d6810a822dbd680d9a92af231b99853651bd78c44',
                                '36be8574f37d470baa4c88548592e04db84efd18faeda2f4f937920c014f08ea'))}},
 'runner': {'runner': {'path': 'scripts/test_auth_e2e.py',
                       'baseline': '6d55979bcaff97f5ab23b4c3d8eafbb65a080a3c49555eb2e17823750f6aa2a9',
                       'current': '7e116749cb729e530907918dfbc10bf1b32075d035c26f403f46d09bcfb69ba9',
                       'hunks': (('d72b2528f556cd87cf1bf00ba9fba45d2b78bb331c87eb47fa95c2d685014109',
                                  '83b15a5e8151a6f188502f375e00d03f143e1a420d731078e58b808caa88d936'),
                                 ('7c5769439d5522c91f9c233bc8bd417f0c2006921f5c2460460dd40e55c8a343',
                                  '94a4ff25d09ab71e01e95b7ae776b1e6276f0f24872a081801187b9b6b9f945c'),
                                 ('0f5b542146a77ea266b96dfb181c53c27c949eec99d34189bd42864c19fad71d',
                                  '88dd5fab22aa6d0a094229ad153d028c76198799430f1ddfc2612ff35061c169'),
                                 ('6456246cd35582ce23f7f7ad83d8a16f71f3c6b7fb402fa4daec8c51e563f871',
                                  '9700e31dd6aa40b417d60395b3a236e926fb7c7a4d30301ab8affd6dcc2558f5'),
                                 ('7b2e8ca1da68223efe2be0a7eaaa995a1b5512f947bd966cc4ec589f32d9df83',
                                  '2e1cb3a09ad6baf51369b1c6990e370843d27f0f21e59fdc5008d04fdc68315e'),
                                 ('4cffe866e1b60ea98aa12799fe3fa6a5850eceea987dc6ac704085ea9a561e8c',
                                  '33aa534d0364367d0d156f07fd4b7156f241809da6a47a88d9161c6e039948c5'),
                                 ('9d8716aa3a0eca79909803e74d82531af603bb4061242b16d53143de03d42945',
                                  '1f2c013fda310e429ffa871b2fc689596251b1cd1dd7f0d1b5b59fe198095d54'),
                                 ('648a6eb9fc2ccc617953a5f1d692a8cbccc94cabae3cc8bfee4c33fd9f56fa73',
                                  '422a01576ed893bcaf2855c4f9e381add10e6218e283bfe87e0c8135c17dca9f'),
                                 ('7d97adc64dbda4d35364e1499a8bb47029aac2ad734ee99331c9e0943aa5ea82',
                                  'de78ff6554f3b7fc052c990659af161bbea687305ab268b33e9333f7dfd8dd39'),
                                 ('d7f971e764d6edf385e545028231f87e4c601895ce0eee15f07d5c56370040e7',
                                  'efad1defb868c220de19b40676762468f31dea855417d91bc7e9c53daf6cec3f'))}}}

def _require(value):
    if not value:
        raise ValueError('uac_evidence_invalid:configuration')


def _sha(raw):
    return hashlib.sha256(raw).hexdigest()


def _unique(pairs):
    result = {}
    for key, value in pairs:
        _require(key not in result)
        result[key] = value
    return result


def _load_delta(kind):
    """Bound before parsing; close every domain, key, type, byte and hunk cap."""
    _require(type(kind) is str and kind in ANCHORS)
    try:
        raw = Path('scripts/fixtures/user_uac_v3_' + kind + '_delta.json').read_bytes()
        _require(type(raw) is bytes and 0 < len(raw) <= 262144 and not raw.startswith(b'\xef\xbb\xbf'))
        text = raw.decode('utf-8', errors='strict')
        depth, quoted, escaped = 0, False, False
        for char in text:
            if quoted:
                if escaped:
                    escaped = False
                elif char == '\\':
                    escaped = True
                elif char == '"':
                    quoted = False
            elif char == '"':
                quoted = True
            elif char in '[{':
                depth += 1
                _require(depth <= 8)
            elif char in ']}':
                depth -= 1
                _require(depth >= 0)
        _require(not quoted and depth == 0)
        delta = json.loads(text, object_pairs_hook=_unique,
                           parse_constant=lambda _: _require(False))
        _require(type(delta) is dict and set(delta) == {'format', 'profile', 'baselineCommit', 'baselineTree', 'files'})
        _require(type(delta['format']) is int and delta['format'] == 1)
        _require(delta['profile'] == 'mixed-33-v1' and delta['baselineCommit'] == BASE_COMMIT
                 and delta['baselineTree'] == BASE_TREE)
        _require(type(delta['files']) is dict and set(delta['files']) == set(ANCHORS[kind]))
        cumulative = 0
        for domain, expected in ANCHORS[kind].items():
            item = delta['files'][domain]
            _require(type(item) is dict and set(item) == {'path', 'baselineSHA256', 'replacements'})
            _require(item['path'] == expected['path'] and item['baselineSHA256'] == expected['baseline'])
            changes = item['replacements']
            _require(type(changes) is list and 1 <= len(changes) <= 32
                     and len(changes) == len(expected['hunks']))
            for change, hashes in zip(changes, expected['hunks']):
                _require(type(change) is dict and set(change) == {'old', 'new'})
                _require(type(change['old']) is str and type(change['new']) is str
                         and change['old'] != change['new'])
                for key, digest in zip(('old', 'new'), hashes):
                    encoded = change[key].encode('utf-8', errors='strict')
                    _require(0 < len(encoded) <= 16384)
                    cumulative += len(encoded)
                    _require(cumulative <= 196608)
                    _require(_sha(encoded) == digest)
        _require(_sha(raw) == DELTA_SHA256[kind])
        return delta
    except (OSError, UnicodeError, ValueError, TypeError, RecursionError, KeyError):
        raise ValueError('uac_evidence_invalid:configuration') from None


def _restore(text, kind, domain):
    _require(type(text) is str and type(kind) is str and type(domain) is str
             and kind in ANCHORS and domain in ANCHORS[kind])
    delta = _load_delta(kind)
    expected = ANCHORS[kind][domain]
    try:
        _require(_sha(text.encode('utf-8', errors='strict')) == expected['current'])
        for change in reversed(delta['files'][domain]['replacements']):
            _require(text.count(change['new']) == 1)
            text = text.replace(change['new'], change['old'], 1)
        _require(_sha(text.encode('utf-8')) == expected['baseline'])
        return text
    except (UnicodeError, ValueError):
        raise ValueError('uac_evidence_invalid:configuration') from None


def restore_user_uac_workflow(text):
    return _restore(text, 'ci', 'workflow')


def restore_user_uac_helper_source(text):
    return _restore(text, 'ci', 'helper')


def restore_user_uac_inventory(raw):
    _require(type(raw) is bytes)
    try:
        return _restore(raw.decode('utf-8', errors='strict'), 'ci', 'inventory').encode('utf-8')
    except UnicodeError:
        raise ValueError('uac_evidence_invalid:configuration') from None


def restore_user_uac_auth_test_source(text):
    return _restore(text, 'ci', 'authTest')


def restore_user_uac_runner(text):
    return _restore(text, 'runner', 'runner')


def _function(name, **scope):
    tree = ast.parse(Path('scripts/test_auth_e2e.py').read_text())
    node = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == name)
    exec(compile(ast.Module(body=[node], type_ignores=[]), '<actual-uac-runner>', 'exec'), scope)
    return scope[name]


class UACProjectionContract(unittest.TestCase):
    def test_exact_current_only_inverse_and_hardcoded_five_predecessors(self):
        for kind, domains in ANCHORS.items():
            for domain, anchor in domains.items():
                with self.subTest(kind=kind, domain=domain):
                    text = Path(anchor['path']).read_text()
                    previous = _restore(text, kind, domain)
                    self.assertEqual(_sha(previous.encode()), anchor['baseline'])
                    self.assertNotEqual(previous, text)
                    with self.assertRaises(ValueError):
                        _restore(previous, kind, domain)
                    for invalid in (None, b'', 1, True, {}, []):
                        with self.assertRaises(ValueError):
                            _restore(invalid, kind, domain)

    def test_each_hunk_missing_duplicate_partial_line_byte_and_unrelated_drift(self):
        for kind, domains in ANCHORS.items():
            delta = _load_delta(kind)
            for domain, anchor in domains.items():
                source = Path(anchor['path']).read_text()
                mutations = [source + '\n', source.replace('\n', '\r\n'), source[:-1]]
                for change in delta['files'][domain]['replacements']:
                    current = change['new']
                    mutations += [source.replace(current, replacement, 1)
                                  for replacement in ('', current * 2, change['old'], current + '# drift\n')]
                    lines = current.splitlines(keepends=True)
                    for index in range(len(lines)):
                        mutations.append(source.replace(current, ''.join(lines[:index] + lines[index + 1:]), 1))
                for bad in mutations:
                    with self.subTest(kind=kind, domain=domain, sha=_sha(bad.encode())), self.assertRaises(ValueError):
                        _restore(bad, kind, domain)

    def test_cross_domain_and_wrong_input_type_never_project(self):
        sources = [(kind, domain, Path(anchor['path']).read_text())
                   for kind, domains in ANCHORS.items() for domain, anchor in domains.items()]
        for kind, domain, source in sources:
            for other_kind, other_domain, _ in sources:
                if (kind, domain) != (other_kind, other_domain):
                    with self.assertRaises(ValueError):
                        _restore(source, other_kind, other_domain)
        for value in ('{}', bytearray(b'{}'), None, True):
            with self.assertRaises(ValueError):
                restore_user_uac_inventory(value)

    def test_closed_delta_schema_caps_duplicate_keys_and_mutable_rehash_cannot_authorize(self):
        for kind, domains in ANCHORS.items():
            valid = _load_delta(kind)
            mutations = [None, [], {}, dict(valid, extra=1), dict(valid, format=True),
                         dict(valid, format=1.0), dict(valid, baselineCommit='0' * 40),
                         dict(valid, baselineTree='0' * 40), dict(valid, profile='default')]
            for key in valid:
                bad = copy.deepcopy(valid); del bad[key]; mutations.append(bad)
            for domain in domains:
                for field in ('path', 'baselineSHA256', 'replacements'):
                    bad = copy.deepcopy(valid); del bad['files'][domain][field]; mutations.append(bad)
                for replacement in ([], None, {}, valid['files'][domain]['replacements'] * 33):
                    bad = copy.deepcopy(valid); bad['files'][domain]['replacements'] = replacement; mutations.append(bad)
                for key in ('old', 'new'):
                    for replacement in ('', None, 1, True, {}, [], '\ud800', '中' * 16384):
                        bad = copy.deepcopy(valid); bad['files'][domain]['replacements'][0][key] = replacement; mutations.append(bad)
                bad = copy.deepcopy(valid); bad['files'][domain]['baselineSHA256'] = '0' * 64; mutations.append(bad)
                bad = copy.deepcopy(valid); bad['files'][domain]['extra'] = 1; mutations.append(bad)
                bad = copy.deepcopy(valid); bad['files'][domain]['replacements'][0]['extra'] = 1; mutations.append(bad)
                bad = copy.deepcopy(valid); bad['files'][domain]['replacements'].reverse(); mutations.append(bad)
            raws = [json.dumps(value).encode() for value in mutations]
            raws += [b'', b' ' * 262145, b'[' * 9 + b'0' + b']' * 9,
                     b'{"format":1,"format":1}', b'\xef\xbb\xbf{}', b'\xff', b'{', b'NaN']
            original = Path.read_bytes
            target = 'scripts/fixtures/user_uac_v3_' + kind + '_delta.json'
            for raw in raws:
                def read(path):
                    return raw if str(path) == target else original(path)
                with self.subTest(kind=kind, sha=_sha(raw)), patch.object(Path, 'read_bytes', read), self.assertRaises(ValueError):
                    _load_delta(kind)
            # Rehash both the mutable predecessor and an authorized hunk. The
            # independent current/hunk/predecessor anchors still reject it.
            domain = next(iter(domains)); anchor = domains[domain]
            source = Path(anchor['path']).read_text(); badsource = source + '# unauthorized\n'
            bad = copy.deepcopy(valid)
            bad['files'][domain]['replacements'][-1]['new'] += '# unauthorized\n'
            bad['files'][domain]['baselineSHA256'] = _sha((source + '# alternate\n').encode())
            raw = json.dumps(bad).encode()
            def read(path):
                return raw if str(path) == target else original(path)
            with patch.object(Path, 'read_bytes', read), self.assertRaises(ValueError):
                _restore(badsource, kind, domain)

    def test_delta_file_bytes_reject_crlf_trailing_space_and_reordered_keys(self):
        import ci_workflow_contract as workflow
        original = Path.read_bytes
        current_workflow = Path('.github/workflows/ci.yml').read_text()
        for kind in ('ci', 'runner'):
            target = 'scripts/fixtures/user_uac_v3_' + kind + '_delta.json'
            valid = original(Path(target))
            self.assertEqual(_sha(valid), DELTA_SHA256[kind])
            reordered = json.loads(valid)
            reordered = {key: reordered[key] for key in reversed(reordered)}
            variants = [valid.replace(b'\n', b'\r\n'), valid + b'\n', valid + b' ', valid[:-1],
                        (json.dumps(reordered, indent=2) + '\n').encode()]
            for mutated in variants:
                def read(path):
                    return mutated if str(path) == target else original(path)
                with patch.object(Path, 'read_bytes', read), self.assertRaises(ValueError):
                    _load_delta(kind)
                if kind == 'ci' and mutated != variants[-1]:
                    with patch.object(Path, 'read_bytes', read), self.assertRaises(ValueError):
                        workflow.restore_user_uac_workflow(current_workflow)

    def test_auth_schema_negative_test_detects_removed_schema_guard(self):
        import ci_workflow_contract as workflow
        from test_auth_shards16_contract import AuthShards16Contract
        tree = ast.parse(Path('scripts/ci_workflow_contract.py').read_text())
        function = next(node for node in tree.body if isinstance(node, ast.FunctionDef)
                        and node.name == 'restore_auth_shards16_predecessor')
        guards = [node for node in function.body if isinstance(node, ast.If)
                  and ast.unparse(node.test) == 'type(delta) is not dict or delta != expected']
        self.assertEqual(len(guards), 1)
        function.body.remove(guards[0])
        scope = dict(vars(workflow))
        exec(compile(ast.Module(body=[function], type_ignores=[]), '<auth-guard-mutation>', 'exec'), scope)
        case = AuthShards16Contract('test_bounded_delta_schema_cannot_authorize_an_alternate_matrix_or_hash')
        case.setUp()
        with patch.object(workflow, 'restore_auth_shards16_predecessor', scope[function.name]):
            with self.assertRaises(AssertionError):
                case.test_bounded_delta_schema_cannot_authorize_an_alternate_matrix_or_hash()

    def test_exact_full_historical_chains_and_single_c_entry(self):
        import ci_workflow_contract as workflow
        from test_directory_v4_runner_contract import restore_health_runner, restore_v4_runner
        from test_ci_selection_contract import restore_log_navigation_inventory
        from test_native_phase_budgets_ci_contract import restore_native_budget_helper_source
        from test_auth_shards16_contract import restore_auth_helper_source
        current = Path('.github/workflows/ci.yml').read_text()
        with patch.object(workflow, 'restore_user_uac_workflow', wraps=workflow.restore_user_uac_workflow) as strip:
            workflow.restore_selection(current)
            self.assertEqual(strip.call_count, 1)
        self.assertEqual(_sha(workflow.restore_native_phase_budgets(current).encode()),
                         '0fd9599e994b86e28d72d2fbf28be4fffa14f7bbff171b3d47c96069e592b195')
        self.assertEqual(_sha(workflow.restore_auth_shards16(current).encode()),
                         '80c650cfd2184e2c9604d1a222843b64c642cb300ba63e2b1ffe9df20a899e61')
        runner = Path('scripts/test_auth_e2e.py').read_text()
        with patch('test_user_asset_export_v3_uac_runner_contract.restore_user_uac_runner', wraps=restore_user_uac_runner) as strip:
            restore_v4_runner(runner)
            self.assertEqual(strip.call_count, 1)
        self.assertEqual(_sha(restore_health_runner(runner).encode()),
                         '1a90e44c24886a7815efb724dadbe694d07f49da7c89136e0f875ab101aeba2c')
        inventory = Path('scripts/ci_inventory.json').read_bytes()
        with patch('test_user_asset_export_v3_uac_runner_contract.restore_user_uac_inventory', wraps=restore_user_uac_inventory) as strip:
            self.assertEqual(_sha(restore_log_navigation_inventory(inventory)),
                             '410601823ee66f9f84caa6847e46206b9c66c0fca1382edb6afcad22fb262ae4')
            self.assertEqual(strip.call_count, 1)
        helper = Path('scripts/ci_workflow_contract.py').read_text()
        self.assertEqual(_sha(restore_native_budget_helper_source(helper).encode()),
                         '8cfce8da1c1bcd81b2d81b8dde6e27803a87195d5e33ccb3be817e0eb2d30244')
        self.assertEqual(_sha(restore_auth_helper_source(helper).encode()),
                         '2b08139edc5c0613a62976ddfb6de8cb816c5610bc43c327db83f92f602a2542')

    def test_legacy_negative_layers_are_reachable_without_reentering_c(self):
        import ci_workflow_contract as workflow
        from test_directory_v4_runner_contract import restore_health_runner_predecessor
        from test_ci_selection_contract import restore_log_navigation_inventory_predecessor
        prew = restore_user_uac_workflow(Path('.github/workflows/ci.yml').read_text())
        prea = workflow.restore_native_phase_budgets_predecessor(prew)
        prer = restore_user_uac_runner(Path('scripts/test_auth_e2e.py').read_text())
        prei = restore_user_uac_inventory(Path('scripts/ci_inventory.json').read_bytes())
        pairs = [(workflow.restore_native_phase_budgets_predecessor, prew,
                  prew.replace('    timeout-minutes: 41\n', '    timeout-minutes: 40\n')),
                 (workflow.restore_auth_shards16_predecessor, prea,
                  prea.replace('shard: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]', 'shard: [1]')),
                 (restore_health_runner_predecessor, prer, prer.replace('health_timeout(660, health_run_deadline)', '661')),
                 (restore_log_navigation_inventory_predecessor, prei, prei.replace(b'      "operational-logs"\n', b'      "system"\n', 1))]
        with patch.object(workflow, 'restore_user_uac_workflow', side_effect=AssertionError('C reentered')), \
             patch('test_user_asset_export_v3_uac_runner_contract.restore_user_uac_runner', side_effect=AssertionError('C reentered')), \
             patch('test_user_asset_export_v3_uac_runner_contract.restore_user_uac_inventory', side_effect=AssertionError('C reentered')):
            for function, valid, invalid in pairs:
                self.assertNotEqual(valid, invalid)
                function(valid)
                with self.assertRaises(ValueError):
                    function(invalid)


class UACRunnerWiringContract(unittest.TestCase):
    def test_exact_single_suite_inventory_roots_and_all86_conservative_plan(self):
        import ci_selection as selection
        import ci_selection_summary as summary
        current = json.loads(Path('scripts/ci_inventory.json').read_bytes())
        previous = json.loads(restore_user_uac_inventory(Path('scripts/ci_inventory.json').read_bytes()))
        entry = dict(name=SUITE, command='python3 scripts/test_auth_e2e.py --suite ' + SUITE,
                     specs=['web/e2e/' + SUITE + '.spec.ts'])
        self.assertEqual(current['browser'], previous['browser'] + [entry])
        self.assertEqual((len(previous['browser']), len(current['browser'])), (56, 57))
        self.assertEqual(current['fixed'], previous['fixed'])
        self.assertEqual(len(current['fixed']), 7)
        self.assertNotIn(SUITE, current['fixed'])
        for root, consumers in previous['frontend_roots'].items():
            expected = consumers + [SUITE] if root in ('web/src/TaskWorkspace.tsx', 'web/src/UserAssetsV3Workspace.tsx') else consumers
            self.assertEqual(current['frontend_roots'][root], expected)
        self.assertEqual(len(current['frontend_roots']['web/src/TaskWorkspace.tsx']), 20)
        self.assertEqual(len(current['frontend_roots']['web/src/UserAssetsV3Workspace.tsx']), 7)
        for event in ('push', 'merge_group', 'workflow_dispatch'):
            plan = selection.make_plan(event, 'a' * 40, 'b' * 40, current,
                                       loader=lambda _: self.fail('full plan must not read snapshots'))
            self.assertEqual(plan['browser'], current['browser'])
            self.assertEqual(plan['auth'], list(range(1, 17)))
            self.assertEqual(len(plan['browser']) + len(plan['auth']) + len(plan['fixed']) + 6, 86)
            needs = {name: {'result': 'success'} for name in summary.GROUPS + ('plan',)}
            summary.verify(plan, needs, event, 'a' * 40, 'b' * 40, plan)
        snapshots = [{p: '' for row in current['browser'] for p in row['specs']}] * 2
        graphs = [({p: set() for p in snapshot}, set()) for snapshot in snapshots]
        for path in ('scripts/test_auth_e2e.py', 'scripts/ci_inventory.json', '.github/workflows/ci.yml',
                     'scripts/user_asset_export_v3_uac_ci.py', 'scripts/validate_user_asset_export_v3_uac_evidence.py',
                     'web/e2e/group-assets-v3-fixture.ts', 'internal/testldapfixture/user_uac_v3.go'):
            with patch.object(selection, 'dependency_graph', side_effect=graphs):
                plan = selection.select([path], current, snapshots)
            self.assertEqual(plan['browser'], current['browser'])
            self.assertTrue(all(plan['groups'].values()))

    def test_runner_registers_only_opt_in_profile_and_single_nonce_before_capture_start(self):
        source = Path('scripts/test_auth_e2e.py').read_text()
        tree = ast.parse(source)
        choices = next(kw.value for node in ast.walk(tree) if isinstance(node, ast.Call)
                       and isinstance(node.func, ast.Attribute) and node.func.attr == 'add_argument'
                       and node.args and isinstance(node.args[0], ast.Constant) and node.args[0].value == '--suite'
                       for kw in node.keywords if kw.arg == 'choices')
        self.assertEqual(ast.literal_eval(choices).count(SUITE), 1)
        self.assertIn('DIRECTORY_V3_SUITES = {UAC_SUITE} | ', source)
        self.assertEqual(source.count('uac_nonce = secrets.token_hex(16) if args.suite == UAC_SUITE else None'), 1)
        self.assertLess(source.index('uac_nonce = secrets.token_hex(16)'), source.index('configure_uac(env, nonce=uac_nonce)'))
        self.assertLess(source.index('configure_uac(env, nonce=uac_nonce)'), source.index('capture_uac_provenance(repo=Path.cwd())'))
        self.assertLess(source.index('capture_uac_provenance(repo=Path.cwd())'), source.index('ldap_fixture.start(env)'))
        self.assertIn('user_uac_v3_profile="mixed-33-v1" if args.suite == UAC_SUITE else ""', source)
        self.assertIn('else 930 if args.suite in {"user-asset-export-v3", UAC_SUITE}', source)
        self.assertIn('env["PLAYWRIGHT_NO_COPY_PROMPT"] = "1"', source)
        self.assertIn('configure_user_export(env)\n        capture_user_export()', source)
        self.assertNotIn('ADTR_LDAP_FIXTURE_UAC_RUN_NONCE', source)  # E owns its sole env name; no alias.

    def test_no_finalization_or_pass_before_outer_cleanup_or_on_failure(self):
        tree = ast.parse(Path('scripts/test_auth_e2e.py').read_text())
        main = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'main')
        outer = next(n for n in main.body if isinstance(n, ast.Try))
        finals = [n for n in ast.walk(main) if isinstance(n, ast.Call) and isinstance(n.func, ast.Name)
                  and n.func.id == 'finalize_uac_evidence']
        self.assertEqual(len(finals), 1)
        final = finals[0]
        self.assertGreater(final.lineno, outer.end_lineno)
        self.assertEqual({kw.arg for kw in final.keywords},
                         {'repo', 'run_nonce', 'provenance_bytes', 'candidate_path', 'retained_ldap', 'cleanup_receipt'})
        self.assertEqual(next(ast.unparse(kw.value) for kw in final.keywords if kw.arg == 'retained_ldap'),
                         'ldap_fixture.retained_uac_evidence')
        calls = [n for n in ast.walk(main) if isinstance(n, ast.Call) and isinstance(n.func, ast.Name)]
        cleanup = next(n for n in calls if n.func.id == 'cleanup_uac_run')
        self.assertGreater(cleanup.lineno, outer.body[-1].lineno)
        self.assertLess(cleanup.lineno, final.lineno)
        # A cleanup failure with no primary exception raises; an existing primary
        # resumes after finally. Neither path falls through to finalization.
        branch = next(n for n in ast.walk(outer) if isinstance(n, ast.If)
                      and ast.unparse(n.test) == 'args.suite == UAC_SUITE'
                      and any(isinstance(c, ast.Call) and isinstance(c.func, ast.Name)
                              and c.func.id == 'cleanup_uac_run' for c in ast.walk(n)))
        self.assertIn('primary_error = sys.exc_info()[0] is not None', ast.unparse(branch))
        self.assertIn('if not primary_error:', ast.unparse(branch))
        self.assertTrue(any(isinstance(n, ast.Raise) for body in branch.body for n in ast.walk(body)))
        self.assertFalse(any(isinstance(n, (ast.Return, ast.Continue, ast.Break)) for body in branch.body for n in ast.walk(body)))
        output = next(n for n in calls if n.func.id == 'print' and any('acceptance PASS' in ast.unparse(a) for a in n.args))
        self.assertGreater(output.lineno, final.lineno)

    def test_workflow_stable_body_and_success_only_exact_paths_no_glob(self):
        source = Path('.github/workflows/ci.yml').read_text()
        self.assertEqual(source.count("matrix.name == '" + SUITE + "'"), 3)
        self.assertIn("matrix.name == '" + SUITE + "' || matrix.name == 'user-asset-export-v3'", source)
        begin = source.index('      - name: Validate nonempty UAC mixed33 post-cleanup evidence\n')
        end = source.index('      - name: Validate refreshed group membership dictionary4 browser evidence\n')
        block = source[begin:end]
        for token in ('id: user-uac-evidence', 'validate --repo . --github-output',
                      "steps.user-uac-evidence.outcome == 'success'", 'success() &&',
                      'path: ${{ steps.user-uac-evidence.outputs.artifact_paths }}',
                      'if-no-files-found: error', 'retention-days: 14',
                      'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02'):
            self.assertIn(token, block)
        for forbidden in ('always()', '*.xlsx', 'path: |', 'continue-on-error', 'timeout-minutes:'):
            self.assertNotIn(forbidden, block)
        self.assertEqual(10 + 20 + 20 + 16 * 20 + 56 * 20 + 30 + 41 + 51 + 7 * 20 + 20, 1772)


class UACCleanupContract(unittest.TestCase):
    def setup_cleanup(self, failure=None):
        calls = []
        report = {'processes': {'worker': {}, 'api': {}}, 'collection_errors': []}
        diagnostics = Mock(report=report)
        def event(name):
            calls.append(name)
            if failure == name:
                raise RuntimeError('PRIVATE_PROOF_DO_NOT_OUTPUT')
        diagnostics.snapshot.side_effect = lambda *args: event('snapshot')
        diagnostics.phase.side_effect = lambda *args: event('diagnostics-phase')
        diagnostics.write.side_effect = lambda *args: event('diagnostics-write')
        logs = io.StringIO('safe synthetic log')
        worker, server = object(), object()
        def process(value, record):
            name = 'worker' if value is worker else 'api'
            event(name)
            if failure == name + '-bad':
                return True, 1, False, ('process_nonzero_exit',)
            return True, 0, False, ()
        ldap = Mock(retained_uac_evidence=(b'candidate', b'seal', 0))
        ldap.close.side_effect = lambda: event('ldap')
        def panic(log):
            event('panic')
            return failure == 'panic-true'
        def remove(*args):
            event('database')
        function = _function('cleanup_uac_run', cleanup_v4_process=process,
                             http_handler_panicked=panic, remove_owned_container=remove)
        args = (worker, server, diagnostics, logs, ldap, True, 'owned', 'owner', 'a' * 32, Path('.e2e-diagnostics'))
        return function, args, calls, logs, ldap, report

    def test_success_exact13_facts_and_actual_cleanup_order(self):
        function, args, calls, logs, ldap, _ = self.setup_cleanup()
        receipt = function(*args)
        self.assertEqual(receipt, dict(format=1, profile='mixed-33-v1', nonce='a' * 32,
            workerExitCode=0, apiExitCode=0, ldapExitCode=0, ldapContainerRemoved=True,
            ldapNetworkRemoved=True, ldapPrivateDirectoriesRemoved=True, databaseContainerRemoved=True,
            logsClosed=True, safeDiagnosticsPassed=True, allOwnedCleanupSucceeded=True))
        self.assertTrue(logs.closed)
        self.assertEqual(calls, ['snapshot', 'worker', 'api', 'ldap', 'panic', 'database',
                                 'diagnostics-phase', 'diagnostics-write'])
        ldap.close.assert_called_once_with()

    def test_each_failure_keeps_remaining_cleanup_and_fixed_error(self):
        for failed in ('snapshot', 'worker', 'api', 'ldap', 'panic', 'panic-true', 'database',
                       'diagnostics-phase', 'diagnostics-write', 'worker-bad', 'api-bad'):
            function, args, calls, logs, ldap, _ = self.setup_cleanup(failed)
            with self.subTest(failure=failed), self.assertRaisesRegex(RuntimeError, '^uac_evidence_invalid:cleanup$'):
                function(*args)
            self.assertTrue(logs.closed)
            self.assertIn('database', calls)
            self.assertIn('diagnostics-phase', calls)
            self.assertIn('api', calls)
            self.assertIn('ldap', calls)
            self.assertIn('panic', calls)

    def test_log_close_failure_still_attempts_database_and_diagnostics(self):
        function, args, calls, real_logs, _, _ = self.setup_cleanup()
        broken = Mock(closed=False)
        broken.close.side_effect = RuntimeError('PRIVATE_PROOF_DO_NOT_OUTPUT')
        args = list(args); args[3] = broken
        try:
            with self.assertRaisesRegex(RuntimeError, '^uac_evidence_invalid:cleanup$'):
                function(*args)
            self.assertIn('database', calls)
            self.assertIn('diagnostics-write', calls)
            broken.close.assert_called_once_with()
        finally:
            real_logs.close()

    def test_missing_or_invalid_ldap_retention_never_creates_receipt(self):
        for value in (None, [], (b'a', b'b'), (b'a', b'b', 1), (b'a', b'b', False),
                      ('a', b'b', 0), (b'a', 'b', 0), (b'a', b'b', 0.0)):
            function, args, calls, logs, ldap, _ = self.setup_cleanup()
            ldap.retained_uac_evidence = value
            with self.subTest(retention=type(value).__name__), self.assertRaises(RuntimeError):
                function(*args)
            self.assertTrue(logs.closed)
            self.assertIn('database', calls)

    def test_missing_resources_or_collection_errors_cannot_be_claimed_successful(self):
        for index, value in ((0, None), (1, None), (4, None), (5, False)):
            function, args, calls, logs, _, _ = self.setup_cleanup()
            args = list(args); args[index] = value
            with self.assertRaises(RuntimeError):
                function(*args)
            self.assertTrue(logs.closed)
        function, args, _, logs, _, report = self.setup_cleanup()
        report['collection_errors'].append('snapshot')
        with self.assertRaises(RuntimeError):
            function(*args)
        self.assertTrue(logs.closed)


if __name__ == '__main__':
    unittest.main()
