"""Offline preservation and synthetic helper checks, not native runtime evidence."""
import hashlib
import json
from pathlib import Path
import subprocess
import unittest

import domain_list_index_native_source_contract as source

ROOT = Path(__file__).resolve().parents[1]
SPEC = 'web/e2e/domains.spec.ts'


def read(path):
    return (ROOT / path).read_bytes().decode('utf-8')


def blocks(text):
    return [text[text.index(begin):text.index(end) + len(end)]
            for _, begin, end, _, _ in source.BLOCKS]


class DomainListIndexNativeContract(unittest.TestCase):
    def test_exact_inverse_restores_all_original_bytes(self):
        restored = source.restore_domain_list_index_spec(read(SPEC))
        self.assertEqual(len(restored.encode()), 43844)
        self.assertEqual(hashlib.sha256(restored.encode()).hexdigest(), source.BASE_SPEC)

    def test_each_added_line_block_and_anchor_fail_closed_on_mutation(self):
        text = read(SPEC)
        cases = []
        for (anchor, _, _, _, _), block in zip(source.BLOCKS, blocks(text)):
            cases.extend([
                text.replace(block, '', 1),
                text.replace(block, block * 2, 1),
                text.replace(block, '', 1) + block,
                text.replace(anchor, '', 1) + anchor,
            ])
            lines = block.splitlines(keepends=True)
            for index, line in enumerate(lines):
                omitted = ''.join(lines[:index] + lines[index + 1:])
                doubled = ''.join(lines[:index] + [line, line] + lines[index + 1:])
                changed = ''.join(lines[:index] + [line.rstrip('\n') + ' // changed\n']
                                  + lines[index + 1:])
                cases.extend([
                    text.replace(block, omitted, 1),
                    text.replace(block, doubled, 1),
                    text.replace(block, changed, 1),
                    text.replace(block, omitted, 1) + line,
                ])
        self.assertGreater(len(cases), 500)
        for index, bad in enumerate(cases):
            with self.subTest(mutation=index):
                self.assertNotEqual(bad, text)
                with self.assertRaises(ValueError):
                    source.restore_domain_list_index_spec(bad)

    def test_prior_security_flow_budget_and_byte_drift_fail_closed(self):
        text = read(SPEC)
        replacements = [
            ('test.setTimeout(420_000)', 'test.setTimeout(500_000)'),
            ('trace: "off", screenshot: "off", video: "off"',
             'trace: "on", screenshot: "off", video: "off"'),
            ('await freshCode(authenticator)', '"000000"'),
            ('await proof(page, changed, auth);', '// proof omitted'),
            ('expect(filterWrites).toBe(0);', 'expect(filterWrites).toBe(1);'),
            ('expect(geometry.overflowWrap).toBe("anywhere");', '// removed'),
            ('await capture("domains-soc-queue-mobile.png");', '// removed'),
        ]
        cases = [text + '\n', text.replace('\n', '\r\n')]
        for before, after in replacements:
            self.assertIn(before, text)
            cases.append(text.replace(before, after, 1))
        for bad in cases:
            with self.assertRaises(ValueError):
                source.restore_domain_list_index_spec(bad)
        for bad in (None, b'', 1, []):
            with self.assertRaises(ValueError):
                source.restore_domain_list_index_spec(bad)

    def test_same_response_formula_identity_empty_and_geometry_wiring(self):
        text = read(SPEC)
        helper, identity, geometry, comparison, binding, manifest = blocks(text)
        for fragment in (
            'body.List.map((connection, index)',
            '(body.page.pageIdx - 1) * body.page.pageSize + index + 1',
            'rows.nth(index)', 'td[data-label="序号"]',
            'new Set(expectedRows.map((row) => row.domainId))',
            'await expect(rows).toHaveCount(expectedRows.length)',
            'await expect(indexCells).toHaveCount(expectedRows.length)',
            'if (expectedRows.length === 0)', 'await expect(table).toHaveCount(0)',
            'toHaveText(String(expected.position))',
            'toHaveText(`域 ID：${expected.domainId}`)',
            'toHaveAttribute("data-domain-id", expected.domainId)',
            'getByRole("columnheader", { includeHidden: true })',
            'toHaveAttribute("scope", "col")',
        ):
            self.assertIn(fragment, helper)
        self.assertIn('persistedRow.locator(\'td[data-label="序号"]\')', identity)
        self.assertIn('position: await measureListFact(positionField)', geometry)
        self.assertLess(text.index('    safe(body);', text.index('const applyStatusDraft')),
                        text.index(comparison))
        self.assertLess(text.index('expect(body.List).toHaveLength(includesVerified ? 1 : 0)'),
                        text.index(comparison))
        self.assertIn('pageIdx: Number(query.get("pageIdx"))', comparison)
        self.assertIn('pageSize: Number(query.get("pageSize"))', comparison)
        self.assertIn('query: Array.from(query)', comparison)
        self.assertIn('...(await verifyDomainListIndex(page, body))', comparison)
        for label in ('desktop', 'mobile'):
            self.assertLess(text.index(f'const {label}ListFacts = await measureListFacts()'),
                            text.index(f'await capture("domains-soc-queue-{label}'))
        self.assertIn('cases: listIndexEvidence', manifest)
        self.assertIn('one isolated domain; page 1 position 1 or empty only', manifest)
        self.assertIn('position: facts.fields.position', manifest)
        self.assertIn('domainId: facts.fields.domainId', manifest)

    def test_added_blocks_have_no_new_business_actions_or_budgets(self):
        additions = ''.join(blocks(read(SPEC)))
        for forbidden in (
            'page.request', 'context.request', 'fetch(', '.route(', '.newContext(',
            '.goto(', '.setViewportSize(', '.screenshot(', 'writeFileSync(',
            'Date.now(', 'new Date(', '.setTimeout(', 'test.setTimeout(',
            '.post(', '.delete(', '.fill(', '.selectOption(', '.click(', '.scroll',
            '.setChecked(', '.keyboard.', 'test.skip(', '.mock(', '.fulfill(',
        ):
            self.assertNotIn(forbidden, additions)

    def test_manifest_hashes_all_new_sources_without_replacing_old_bindings(self):
        text = read(SPEC)
        binding = blocks(text)[4]
        for path in (
            'web/src/DomainListIndex.test.tsx', 'docs/domain-contract.md',
            'docs/domain-list-index-contract.md', 'docs/domain-list-index-native-evidence.md',
            'scripts/domain_list_index_native_source_contract.py',
            'scripts/test_domain_list_index_native_contract.py',
        ):
            self.assertEqual(binding.count(f'"{path}"'), 1)
            self.assertTrue((ROOT / path).is_file())
        self.assertIn('].map((file) => [file, hashFile(file)])', text)
        self.assertIn('"web/src/DomainsWorkspace.tsx"', text)
        self.assertIn('"web/src/domain-soc.css"', text)
        source.restore_domain_list_index_spec(text)

    def test_offline_typescript_helper_with_synthetic_locator_values(self):
        # Execute only the exact added helper in Node. No Playwright import,
        # browser, HTTP, database, npm command or production module is run.
        program = r'''
const assert = require('node:assert/strict');
const vm = require('node:vm');
const ts = require('./web/node_modules/typescript');
const input = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
const compiled = ts.transpileModule(input.helper, {
  compilerOptions: {target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS},
  reportDiagnostics: true,
});
assert.equal(compiled.diagnostics.length, 0);
const plain = value => JSON.parse(JSON.stringify(value));
class Locator {
  constructor(nodes) { this.nodes = nodes; }
  nth(index) { return new Locator(this.nodes.slice(index, index + 1)); }
  locator(selector) {
    const children = this.nodes.flatMap(node => {
      if (selector === 'tbody tr') return node.rows ?? [];
      if (selector === 'button[data-domain-id]') return (node.rows ?? []).flatMap(row => row.actions);
      if (selector === 'td[data-label="序号"]') return node.positions ?? [];
      if (selector === '.domain-row-id') return node.identities ?? [];
      throw new Error('Unexpected synthetic selector: ' + selector);
    });
    return new Locator(children);
  }
  getByRole(role, options) {
    if (role === 'columnheader') {
      assert.equal(options.includeHidden, true);
      return new Locator(this.nodes.flatMap(node => node.headers ?? []));
    }
    assert.equal(role, 'button');
    assert.equal(options.exact, true);
    return new Locator(this.nodes.flatMap(node => node.actions ?? [])
      .filter(node => node.name === options.name));
  }
  async getAttribute(name) {
    assert.equal(this.nodes.length, 1);
    return this.nodes[0].attrs[name] ?? null;
  }
  async textContent() {
    assert.equal(this.nodes.length, 1);
    return this.nodes[0].text;
  }
}
const expect = actual => ({
  toBe: expected => assert.equal(actual, expected),
  toHaveCount: expected => assert.equal(actual.nodes.length, expected),
  toHaveText: expected => {
    if (Array.isArray(expected)) assert.deepEqual(actual.nodes.map(node => node.text), plain(expected));
    else { assert.equal(actual.nodes.length, 1); assert.equal(actual.nodes[0].text, expected); }
  },
  toHaveAttribute: (name, expected) => {
    assert.equal(actual.nodes.length, 1); assert.equal(actual.nodes[0].attrs[name], expected);
  },
  toBeVisible: () => {
    assert.equal(actual.nodes.length, 1); assert.equal(actual.nodes[0].visible, true);
  },
});
const sandbox = {expect};
vm.runInNewContext(compiled.outputText +
  ';this.project = domainListIndexRows; this.verify = verifyDomainListIndex;', sandbox);
const headings = ['域名', '序号', '配置的域控', '加密连接', '状态', '创建时间（UTC）', '上次检测（UTC）', '操作'];
const body = (pageIdx, pageSize, names, total) => ({
  page: {pageIdx, pageSize, total, totalPage: Math.ceil(total / pageSize)},
  List: names.map(name => ({domainId: 'id-' + name, domain: name + '.invalid'})),
});
function pageFor(value, positions) {
  const table = {
    headers: headings.map(text => ({text, attrs: {scope: 'col'}})),
    rows: value.List.map((row, index) => ({
      positions: [{text: String(positions[index]), visible: true}],
      identities: [{text: '域 ID：' + row.domainId}],
      actions: [{name: '查看 ' + row.domain, attrs: {'data-domain-id': row.domainId}}],
    })),
  };
  return {
    table,
    getByRole(role, options) {
      assert.equal(role, 'table'); assert.equal(options.name, '获授权的域连接');
      assert.equal(options.exact, true);
      return new Locator(table.rows.length ? [table] : []);
    },
    locator(selector) {
      assert.equal(selector, '.domain-table-scroll td[data-label="序号"]');
      return new Locator(table.rows.flatMap(row => row.positions));
    },
  };
}
(async () => {
  const cases = [
    [body(1, 10, ['persisted'], 1), [1]],
    [body(1, 10, [], 0), []],
    [body(4, 20, ['zulu', 'alpha', 'mike'], 63), [61, 62, 63]],
    [body(3, 20, ['alpha', 'zulu'], 42), [41, 42]],
    [body(4, 10, [], 3), []],
    [body(1000000, 100, ['upper'], 99999901), [99999901]],
  ];
  for (const size of [10, 20, 30, 40, 50])
    cases.push([body(3, size, ['zulu', 'alpha'], 2 * size + 2), [2 * size + 1, 2 * size + 2]]);
  for (const [value, positions] of cases) {
    const expected = value.List.map((row, index) => ({domainId: row.domainId, position: positions[index]}));
    assert.deepEqual(plain(sandbox.project(value)), expected);
    const result = plain(await sandbox.verify(pageFor(value, positions), value));
    assert.deepEqual(result.responsePage, value.page);
    assert.deepEqual(result.responseRows, expected);
    assert.deepEqual(result.renderedRows, value.List.map((row, index) => ({
      domainId: row.domainId, identityText: '域 ID：' + row.domainId,
      positionText: String(positions[index]),
    })));
  }
  const value = body(3, 20, ['zulu', 'alpha'], 42);
  const badChanges = [
    page => { page.table.rows[0].positions[0].text = '1'; },
    page => { page.table.rows.reverse(); },
    page => { page.table.rows[0].actions[0].attrs['data-domain-id'] = '41'; },
    page => { page.table.rows[0].identities[0].text = '域 ID：41'; },
    page => { page.table.rows.pop(); },
    page => { page.table.rows.push(page.table.rows[0]); },
    page => { page.table.rows[0].positions = []; },
    page => { page.table.rows[0].positions[0].visible = false; },
    page => { page.table.headers.splice(1, 1); },
    page => { page.table.headers[1].attrs.scope = 'row'; },
    page => { page.table.rows[0].actions[0].name = '查看 wrong.invalid'; },
  ];
  for (const change of badChanges) {
    const page = pageFor(value, [41, 42]); change(page);
    await assert.rejects(() => sandbox.verify(page, value));
  }
  await assert.rejects(() => sandbox.verify(pageFor(value, [41, 42]), body(1, 10, [], 0)));
  const duplicate = body(1, 10, ['zulu', 'zulu'], 2);
  await assert.rejects(() => sandbox.verify(pageFor(duplicate, [1, 2]), duplicate));
  process.stdout.write(JSON.stringify({syntheticPositiveCases: cases.length,
    syntheticRejectedCases: badChanges.length + 2, browserExecuted: false}) + '\n');
})().catch(error => { console.error(error); process.exitCode = 1; });
'''
        result = subprocess.run(
            ['node', '-e', program], cwd=ROOT,
            input=json.dumps({'helper': blocks(read(SPEC))[0]}),
            text=True, capture_output=True, timeout=30, check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(json.loads(result.stdout), {
            'syntheticPositiveCases': 11, 'syntheticRejectedCases': 13,
            'browserExecuted': False,
        })


if __name__ == '__main__':
    unittest.main()
