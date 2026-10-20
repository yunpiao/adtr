import ast
import copy
import hashlib
import itertools
import json
import re
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

import ci_selection as selection
import ci_selection_summary as summary
from ci_workflow_contract import restore_selection


class SelectionContract(unittest.TestCase):
    def setUp(self):
        self.inventory=json.loads(Path('scripts/ci_inventory.json').read_text())
        self.sources={p:'' for e in self.inventory['browser'] for p in e['specs']}
        self.sources['web/e2e/native-no-store-completion.spec.ts']=''
        self.sources.update({p:'' for p in self.inventory['frontend_roots']})
        self.graph={p:set() for p in self.sources}

    def choose(self, paths, old=None, new=None, uncertain=()):
        old=self.sources if old is None else old
        new=old if new is None else new
        with patch.object(selection,'dependency_graph',side_effect=[({p:self.graph.get(p,set()) for p in s},set(uncertain)) for s in (old,new)]):
            return selection.select(paths,self.inventory,[old,new])

    def names(self, plan):
        return {e['name'] for e in plan['browser']}

    def test_spec_aliases_bulk_and_fixed_consumers(self):
        cases={'auth':{'authentication','expired-password'},'resource':{'resources'},
               'messages-bulk':{'messages'},'messages':{'messages'},'user-assets-v3':{'user-assets-v3'}}
        for spec, expected in cases.items():
            with self.subTest(spec=spec):
                plan=self.choose(['web/e2e/'+spec+'.spec.ts'])
                self.assertEqual(self.names(plan),expected)
                self.assertEqual(plan['fixed'],sorted(expected & set(self.inventory['fixed'])))
                self.assertTrue(plan['groups']['contracts'])
                self.assertFalse(plan['groups']['auth-integration'])

    def test_recursive_helper_and_reexport_graph_uses_old_and_new(self):
        old=dict(self.sources,**{'web/e2e/old.ts':'','web/e2e/mid.ts':''})
        self.graph['web/e2e/messages.spec.ts']={'web/e2e/mid.ts'}
        self.graph['web/e2e/mid.ts']={'web/e2e/old.ts'}
        plan=self.choose(['web/e2e/old.ts'],old,self.sources)
        self.assertEqual(self.names(plan),{'messages'})

    def test_base_and_tested_merge_new_consumers_are_included(self):
        path='web/src/shared.ts';old=dict(self.sources,**{path:''})
        self.graph['web/src/MessagesWorkspace.tsx']={path}
        newer=dict(old,**{'web/src/NewBasePage.tsx':''})
        self.graph['web/src/NewBasePage.tsx']={path}
        snapshots=[old,old,newer,newer]
        with patch.object(selection,'dependency_graph',side_effect=[({p:self.graph.get(p,set()) for p in s},set()) for s in snapshots]):
            plan=selection.select([path],self.inventory,snapshots)
        self.assertEqual(len(plan['browser']),51)
        calls=[]
        def loader(sha):calls.append(sha);return old
        with patch.object(selection,'pr_diff',return_value=('c'*40,[path])),patch.object(selection,'dependency_graph',return_value=(self.graph,set())):
            selection.make_plan('pull_request','a'*40,'b'*40,self.inventory,loader=loader,tested='d'*40)
        self.assertEqual(calls,['c'*40,'b'*40,'a'*40,'d'*40])

    def test_frontend_root_transitive_dependencies(self):
        root='web/src/MessagesWorkspace.tsx';dep='web/src/messages-api.ts'
        src=dict(self.sources,**{dep:''});self.graph[root]={dep}
        self.assertEqual(self.names(self.choose([dep],src)),{'messages'})

    def test_unmapped_production_consumer_widens_shared_dependency(self):
        dep='web/src/shared.ts';other='web/src/UnmappedPage.tsx'
        src=dict(self.sources,**{dep:'',other:''})
        self.graph['web/src/MessagesWorkspace.tsx']={dep};self.graph[other]={dep}
        self.assertEqual(len(self.choose([dep],src)['browser']),51)

    def test_unit_test_only_and_production_import_of_test(self):
        path='web/src/Example.test.tsx';src=dict(self.sources,**{path:''})
        self.assertFalse(self.choose([path],src)['groups']['browser'])
        self.graph['web/src/MessagesWorkspace.tsx']={path}
        self.assertEqual(len(self.choose([path],src)['browser']),51)

    def test_native_both_engines_and_order_independence(self):
        native='web/e2e/native-no-store-completion.spec.ts';unknown='web/src/NewPage.tsx'
        src=dict(self.sources,**{unknown:''})
        plans=[self.choose(list(paths),src) for paths in itertools.permutations([native,unknown])]
        self.assertEqual(plans[0]['groups'],plans[1]['groups'])
        self.assertEqual(plans[0]['browser'],plans[1]['browser'])
        self.assertEqual(len(plans[0]['browser']),51)
        native_plan=self.choose([native])
        self.assertTrue(native_plan['groups']['native-no-store'])
        self.assertTrue(native_plan['groups']['native-no-store-fixed'])
        self.assertEqual(native_plan['browser'],[])

    def test_computed_production_consumer_cannot_hide_behind_known_hit(self):
        dep='web/src/messages-api.ts';root='web/src/MessagesWorkspace.tsx'
        other='web/src/UnknownPage.tsx';src=dict(self.sources,**{dep:'',other:''})
        self.graph[root]={dep}
        self.assertEqual(len(self.choose([dep],src,uncertain={other})['browser']),51)

    def test_uncertain_graph_fails_safe(self):
        p='web/e2e/messages.spec.ts'
        self.assertEqual(len(self.choose([p],uncertain={p})['browser']),51)

    def test_docs_only_is_explicit_without_fake_test(self):
        p=self.choose(['docs/design.md','README.md'])
        self.assertFalse(any(p['groups'].values()))
        self.assertTrue(all(r.startswith('documentation:') for r in p['reasons']))
        for path in ('docs/run.py','requirements/catalog.json','unknown.xyz','go.mod','.github/workflows/ci.yml','scripts/test_auth_e2e.py','web/package-lock.json'):
            self.assertTrue(all(self.choose([path])['groups'].values()))

    def test_owned_controller_selects_all_three_running_suites(self):
        p=self.choose(['scripts/owned_rescue_runner.py'])
        self.assertEqual(self.names(p),{k+'-asset-export-v3-running-cancel' for k in ('user','computer','group')})
        self.assertTrue(p['groups']['contracts'])

    def test_duplicate_json_keys_are_rejected_before_planning_or_summary(self):
        for value in ('{"version":1,"version":1}', '{"groups":{"browser":true,"browser":false}}'):
            with self.assertRaises(ValueError):selection.parse_json(value)

    def test_diff_rename_copy_delete_and_union(self):
        self.assertEqual(selection.changed_paths(b'R100\0old.ts\0new.ts\0D\0deleted.ts\0M\0new.ts\0C050\0a.ts\0b.ts\0'),['a.ts','b.ts','deleted.ts','new.ts','old.ts'])
        for data in (b'',b'M\0a',b'R100\0a\0',b'U\0a\0',b'M\0../x\0',b'M\0\xff\0',b'M\0a\nb\0'):
            with self.subTest(data=data),self.assertRaises((ValueError,UnicodeError)):
                selection.changed_paths(data)

    def test_diff_unreadable_ambiguous_or_invalid_event_fails(self):
        for side in [RuntimeError('read failure'),[b'a'*40+b'\n'+b'b'*40+b'\n']]:
            with patch.object(selection,'git',side_effect=side),self.assertRaises((ValueError,RuntimeError)):
                selection.pr_diff('a'*40,'b'*40)
        with self.assertRaises(ValueError):selection.pr_diff('bad','b'*40)
        with patch.object(selection,'git',return_value=b'a'*40+b'\n') as command:
            with self.assertRaises(ValueError):selection.pr_diff('a'*40,'b'*40)
            self.assertEqual(command.call_args_list[1].args[:4],('diff','--name-status','-z','--find-renames'))

    def test_inventory_covers_every_real_spec_and_fixed_variant(self):
        expected={str(p) for p in Path('web/e2e').glob('*.spec.ts')}
        actual={p for e in self.inventory['browser'] for p in e['specs']}|{'web/e2e/native-no-store-completion.spec.ts'}
        self.assertEqual(actual,expected)
        self.assertEqual(len(self.inventory['browser']),51)
        self.assertEqual(len(self.inventory['fixed']),7)
        self.assertEqual(summary.INVENTORY,self.inventory)
        old=restore_selection(Path('.github/workflows/ci.yml').read_text())
        matrix=old.split('  browser:\n',1)[1].split('    steps:\n',1)[0]
        self.assertEqual([(e['name'],e['command']) for e in self.inventory['browser']], re.findall(r'          - name: ([^\n]+)\n            command: ([^\n]+)',matrix))
        for command in ('true','python3 scripts/test_auth_e2e.py; true','python3 scripts/test_auth_e2e.py --suite messages || true'):
            bad=copy.deepcopy(self.inventory);bad['browser'][0]['command']=command
            with self.assertRaises(ValueError):selection.inventory_checked(bad)


    def plan_for_summary(self, paths=['web/e2e/messages.spec.ts']):
        plan=self.choose(paths)
        plan.update(version=1,event='pull_request',base='a'*40,head='b'*40,tested='d'*40,mergeBase='c'*40,paths=paths,
                    inventorySHA256=hashlib.sha256(json.dumps(self.inventory,sort_keys=True,separators=(',',':')).encode()).hexdigest())
        needs={g:{'result':'success' if on else 'skipped'} for g,on in plan['groups'].items()}
        needs['plan']={'result':'success'}
        return plan,needs

    def test_summary_success_and_docs_are_honest(self):
        for paths in (['web/e2e/messages.spec.ts'],['README.md']):
            p,n=self.plan_for_summary(paths)
            out=summary.verify(p,n,'pull_request','a'*40,'b'*40,p)
            self.assertTrue(any('not run' in line for line in out))

    def test_summary_rejects_skipped_cancelled_failed_selected_and_unselected_execution(self):
        p,n=self.plan_for_summary()
        for group in n:
            for state in ('failure','cancelled','skipped','success'):
                if state==n[group]['result']:continue
                bad=copy.deepcopy(n);bad[group]['result']=state
                with self.subTest(group=group,state=state),self.assertRaises(ValueError):
                    summary.verify(p,bad,'pull_request','a'*40,'b'*40,p)

    def test_summary_rejects_malformed_empty_unknown_or_rebound_plan(self):
        p,n=self.plan_for_summary()
        variants=[{},dict(p,version=True),dict(p,head='d'*40),dict(p,paths=[]),dict(p,inventorySHA256='0'*64),dict(p,browser=[]),dict(p,extra=True)]
        bad=copy.deepcopy(p);bad['browser'][0]['command']='true';variants.append(bad)
        for bad in variants:
            with self.subTest(bad=bad),self.assertRaises((ValueError,TypeError)):
                summary.verify(bad,n,'pull_request','a'*40,'b'*40,p)

    def test_self_consistent_underselection_still_fails_recomputation(self):
        for paths,group in [(['web/e2e/messages.spec.ts'],'browser'),(['web/e2e/user-assets-v3.spec.ts'],'user-assets-fixed')]:
            expected,needs=self.plan_for_summary(paths)
            bad=copy.deepcopy(expected);bad_needs=copy.deepcopy(needs)
            bad['browser' if group=='browser' else 'fixed']=[]
            bad['groups'][group]=False;bad_needs[group]['result']='skipped'
            with self.assertRaises(ValueError):
                summary.verify(bad,bad_needs,'pull_request','a'*40,'b'*40,expected)

    def test_full_events_retain_original_all_groups_and_matrices(self):
        for event in ('push','merge_group','workflow_dispatch'):
            p=selection.make_plan(event,'a'*40,'b'*40,self.inventory,loader=lambda _:self.fail('no PR snapshots'))
            n={g:{'result':'success'} for g in selection.GROUPS};n['plan']={'result':'success'}
            summary.verify(p,n,event,'a'*40,'b'*40,p)
            self.assertEqual(len(p['browser']),51)
            self.assertEqual(p['auth'],list(range(1,9)))
            self.assertEqual(p['fixed'],self.inventory['fixed'])

    def test_legacy_workflow_restore_never_consumes_a_raw_workflow_read(self):
        def raw_restore_calls(source):
            return [node for node in ast.walk(ast.parse(source))
                    if isinstance(node, ast.Call)
                    and isinstance(node.func, ast.Name)
                    and node.func.id == 'restore_workflow'
                    and any(isinstance(child, ast.Call)
                            and isinstance(child.func, ast.Attribute)
                            and child.func.attr == 'read_text'
                            for child in ast.walk(node))]

        # Both ROOT-based and cwd-based raw reads must be caught. Actual-workflow
        # assertions that do not enter a legacy restore chain remain untouched.
        for source in (
            "restore_workflow((ROOT / '.github/workflows/ci.yml').read_text())",
            "restore_workflow(Path('.github/workflows/ci.yml').read_text())",
        ):
            self.assertEqual(len(raw_restore_calls(source)), 1)
        self.assertEqual(raw_restore_calls('restore_workflow(reviewed_workflow())'), [])
        for path in Path('scripts').glob('test_*contract.py'):
            with self.subTest(path=str(path)):
                self.assertEqual(raw_restore_calls(path.read_text()), [])

    def test_workflow_exact_delta_and_all_original_steps_preserved(self):
        text=Path('.github/workflows/ci.yml').read_text()
        original=restore_selection(text)
        self.assertIn('run: make check',original)
        self.assertIn('shard: [1, 2, 3, 4, 5, 6, 7, 8]',original)
        for old,new in [('needs.plan.outputs.browser == \'true\'', 'false'),('needs: [plan, prepare-postgres]','needs: [plan]'),('fromJSON(needs.plan.outputs.browser_matrix)','fromJSON(\'{}\')'),('if: ${{ always() }}','if: ${{ success() }}'),('run: make check','run: true'),('contents: read','contents: write')]:
            self.assertIn(old,text)
            with self.assertRaises(ValueError):restore_selection(text.replace(old,new,1))
        self.assertIn('run: python3 scripts/ci_selection_summary.py',text)
        self.assertIn("expected = make_plan(",Path('scripts/ci_selection_summary.py').read_text())
        self.assertIn("node --test scripts/test_ci_dependencies.mjs",text)
        self.assertIn('fetch-depth: 0',text)

if __name__=='__main__':unittest.main()
