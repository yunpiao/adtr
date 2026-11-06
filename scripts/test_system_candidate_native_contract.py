"""Offline source inverse and exact sidecar-writer contracts, not browser QA."""
import json
from pathlib import Path
import subprocess
import unittest

from system_candidate_native_source_contract import (
    BASE_SIZE, BASE_SPEC, BLOCKS, digest, restore_system_candidate_spec,
)

ROOT = Path(__file__).resolve().parents[1]


class SystemCandidateNativeContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = (ROOT / 'web/e2e/system.spec.ts').read_text()

    def rejected(self, text):
        with self.assertRaises(ValueError):
            restore_system_candidate_spec(text)

    def test_exact_predecessor_restored(self):
        restored = restore_system_candidate_spec(self.source).encode()
        self.assertEqual(len(restored), BASE_SIZE)
        self.assertEqual(digest(restored), BASE_SPEC)

    def test_every_addition_line_is_frozen(self):
        count = 0
        for start, end, *_ in BLOCKS:
            a = self.source.index(start)
            b = self.source.index(end) + len(end)
            block = self.source[a:b]
            offset = a
            for line in block.splitlines(keepends=True):
                self.rejected(self.source[:offset] + '// changed\n' + self.source[offset+len(line):])
                self.rejected(self.source[:offset] + self.source[offset+len(line):])
                offset += len(line)
                count += 2
        self.assertGreater(count, 90)

    def test_duplicate_missing_and_moved_blocks_rejected(self):
        for start, end, *_ in BLOCKS:
            a = self.source.index(start)
            b = self.source.index(end) + len(end)
            block = self.source[a:b]
            without = self.source[:a] + self.source[b:]
            self.rejected(without)
            self.rejected(self.source + block)
            self.rejected(without + block)

    def test_predecessor_safety_and_budget_mutations_rejected(self):
        for old, new in [
            ('test.setTimeout(450_000)', 'test.setTimeout(900_000)'),
            ('expect(visuals).toHaveLength(12)', 'expect(visuals).toHaveLength(4)'),
            ('trace: "off"', 'trace: "on"'),
            ('screenshot: "off"', 'screenshot: "only-on-failure"'),
            ('15_000', '30_000'),
            ('expectedRevision', 'ignoredRevision'),
        ]:
            self.assertIn(old, self.source)
            self.rejected(self.source.replace(old, new, 1))

    def test_success_only_capture_and_no_extra_requests(self):
        source = (ROOT/'web/e2e/system-candidate-evidence.ts').read_text()
        for expected in [
            '"desktop", { width: 1440, height: 900 }',
            '"mobile", { width: 390, height: 844 }',
            'input[type="password"]', '未使用的认证器验证码', '认证器验证码', '设置密钥',
            'expect(documentWidth).toBeLessThanOrEqual(viewport.width)',
            'system-soc-candidate-${device}-${observed.kind}.png',
            'responseSHA256: observed.responseSHA256',
            'await assertSystemCandidate(page, observed);',
            'String(item.ordinal)', 'toHaveText(item.id)',
            '`${disk!.percent!.toFixed(2)}%`', '当前使用率未知',
        ]:
            self.assertIn(expected, source)
        self.assertRegex(source, r'toHaveText\(\s*String\(item\.ordinal\),?\s*\)')
        self.assertEqual(source.count('await assertSystemCandidate(page, observed);'), 2)
        for forbidden in ['fetch(', '.request.get(', '.route(', 'waitForTimeout(', 'context.request']:
            self.assertNotIn(forbidden, source)

    def test_exact_writer_safe_projection_and_source_bindings(self):
        # Compile and execute the actual writer; stub only filesystem/attachment
        # sinks and Playwright's assertions. This is not a browser simulation.
        program = r'''
const fs = require('node:fs'), assert = require('node:assert/strict');
const ts = require('./web/node_modules/typescript');
const source = fs.readFileSync('web/e2e/system-candidate-evidence.ts', 'utf8');
const js = ts.transpileModule(source, {compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
const writes = [], attachments = [];
const expect = value => ({toEqual: other => assert.deepEqual(value,other), toBe: other => assert.equal(value,other), toHaveLength: n => assert.equal(value.length,n)});
const mod = {exports:{}};
new Function('require','module','exports',js)(name => name==='@playwright/test'?{expect}:name==='node:fs'?{writeFileSync:(path,body)=>writes.push({path,body})}:require(name),mod,mod.exports);
const hash = 'a'.repeat(64), sha = 'b'.repeat(40);
const original = {checkoutCommit:sha,checkoutTree:sha,checkoutParents:[sha],eventHeadCommit:sha,eventBaseCommit:sha,dirty:true,workflowCommit:null,workflowRun:null,workflowAttempt:null,runtime:{node:'fixture'},sourceSHA256:{'web/e2e/system.spec.ts':hash},artifactsSHA256:{apiWorker:hash},frontendAssetsSHA256:{'web/dist/assets/test.js':hash},secretProof:'MUST_NOT_LEAK',screenshots:['old'],checks:['original-only'],suite:'system'};
const observations = ['overview-disk','storage-ordinals'].map(kind=>({kind,responseSHA256:hash,nativeFinished:true,current:{checkedAt:'2026-11-06T08:00:00Z',availability:'available',stale:false}}));
const images=observations.flatMap(o=>['desktop','mobile'].map(device=>({file:`system-soc-candidate-${device}-${o.kind}.png`,state:o.kind,sha256:hash,viewport:{width:device==='desktop'?1440:390,height:900},documentWidth:390,responseSHA256:hash})));
const info={outputPath:file=>file,attach:async(name,value)=>attachments.push({name,value})};
(async()=>{
 await mod.exports.writeSystemCandidateEvidence(info,original,observations,images,path=>{assert(fs.existsSync(path),path);return hash;});
 assert.equal(writes.length,1); assert.equal(attachments.length,1);
 const result=JSON.parse(writes[0].body);
 assert.equal(result.suite,'system-candidates'); assert.equal(result.screenshots.length,4);
 assert.equal(result.checks,undefined); assert.equal(result.secretProof,undefined);
 assert(!writes[0].body.includes('MUST_NOT_LEAK')); assert(!writes[0].body.includes('original-only'));
 assert.equal(Object.keys(result.sourceSHA256).length,12);
 for(const key of ['web/e2e/system-candidate-observations.ts','web/e2e/system-candidate-evidence.ts','web/src/SystemDiskOverview.tsx','web/src/SystemStorageRowNumbers.test.tsx','scripts/system_candidate_native_source_contract.py']) assert.equal(result.sourceSHA256[key],hash);
 for(const [reads,pics] of [[observations,images.slice(1)],[observations.slice(1),images],[observations,images.map((v,i)=>i? v:{...v,responseSHA256:'c'.repeat(64)})]]) await assert.rejects(()=>mod.exports.writeSystemCandidateEvidence(info,original,reads,pics,()=>hash));
 assert.equal(writes.length,1); assert.equal(attachments.length,1);
 console.log('Exact sidecar writer: safe fields, sources, four images, three rejected mutations.');
})().catch(error=>{console.error(error);process.exitCode=1;});
'''
        result = subprocess.run(['node', '-e', program], cwd=ROOT, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stdout+result.stderr)


if __name__ == '__main__':
    unittest.main()
