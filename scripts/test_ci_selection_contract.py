import ast
import copy
from datetime import datetime
import hashlib
import itertools
import json
import os
import subprocess
import tempfile
import re
from pathlib import Path
import sys
import unittest
import zlib
import uuid
from unittest.mock import patch

import ci_selection as selection
import ci_selection_summary as summary
from ci_workflow_contract import (restore_selection, restore_messages_isolation,
                                  restore_messages_isolation_predecessor, restore_health_recovery,
                                  restore_selection_predecessor)


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
               'tasks-health-recovery':{'tasks-health-recovery'},'messages-isolation':{'messages-isolation'},'messages-bulk':{'messages'},'messages':{'messages'},'user-assets-v3':{'user-assets-v3'}}
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
        path='web/src/shared.ts';old=dict(self.sources,**{path:''});self.graph[path]=set()
        self.graph['web/src/MessagesWorkspace.tsx']={path}
        newer=dict(old,**{'web/src/NewBasePage.tsx':''})
        self.graph['web/src/NewBasePage.tsx']={path}
        snapshots=[old,old,newer,newer]
        with patch.object(selection,'dependency_graph',side_effect=[({p:self.graph.get(p,set()) for p in s},set()) for s in snapshots]):
            plan=selection.select([path],self.inventory,snapshots)
        self.assertEqual(plan['browser'],self.inventory['browser'])
        self.assertEqual(plan['fixed'],self.inventory['fixed'])
        self.assertEqual(plan['auth'],list(range(1,9)))
        self.assertTrue(all(plan['groups'].values()))
        calls=[]
        def loader(sha):calls.append(sha);return old
        with patch.object(selection,'pr_diff',return_value=('c'*40,[path])),patch.object(selection,'dependency_graph',return_value=(self.graph,set())):
            selection.make_plan('pull_request','a'*40,'b'*40,self.inventory,loader=loader,tested='d'*40)
        self.assertEqual(calls,['c'*40,'b'*40,'a'*40,'d'*40])

    def test_frontend_root_transitive_dependencies(self):
        root='web/src/MessagesWorkspace.tsx';dep='web/src/messages-api.ts'
        src=dict(self.sources,**{dep:''});self.graph[root]={dep}
        self.assertEqual(self.names(self.choose([dep],src)),{'messages','messages-isolation'})

    def test_task_workspace_only_keeps_original_audit_detail_consumer(self):
        # Browser navigation is not a TypeScript import edge. Keep this explicit
        # consumer even when audit.spec.ts itself is absent from the changed set.
        root='web/src/TaskWorkspace.tsx'
        expected={
            'tasks', 'audit', 'messages', 'messages-isolation', 'maintenance', 'user-assets-v2',
            'directory-v3', 'group-members-v4',
            'user-asset-export-v3', 'user-asset-export-v3-failure',
            'user-asset-export-v3-cancel', 'computer-asset-export-v3',
            'computer-asset-export-v3-failure', 'computer-asset-export-v3-cancel',
            'group-asset-export-v3', 'group-asset-export-v3-failure',
            'group-asset-export-v3-cancel',
        }
        self.assertEqual(self.graph['web/e2e/audit.spec.ts'],set())
        plan=self.choose([root])
        self.assertEqual(self.names(plan),expected)
        self.assertEqual(plan['fixed'],['user-assets-v2'])
        self.assertTrue(plan['groups']['contracts'])
        self.assertTrue(plan['groups']['browser'])
        self.assertFalse(plan['groups']['auth-integration'])
        self.assertFalse(plan['groups']['native-no-store'])
        self.assertFalse(plan['groups']['native-no-store-fixed'])
        # The mapping edit itself must still receive full shared-CI validation.
        self.assertTrue(all(self.choose(['scripts/ci_inventory.json'])['groups'].values()))

    def test_unmapped_production_consumer_widens_shared_dependency(self):
        dep='web/src/shared.ts';other='web/src/UnmappedPage.tsx'
        src=dict(self.sources,**{dep:'',other:''})
        self.graph['web/src/MessagesWorkspace.tsx']={dep};self.graph[other]={dep}
        self.assertEqual(len(self.choose([dep],src)['browser']),56)

    def helper_graph(self):
        api='web/src/messages-api.ts'; helper='web/src/sample-data.ts'
        test='web/src/Feature.test.tsx'; bridge='web/src/test-data-bridge.ts'
        src=dict(self.sources, **{api:'',helper:'',test:'',bridge:''})
        self.graph.update({api:set(),test:{bridge},bridge:{helper},helper:{api},
                           'web/src/MessagesWorkspace.tsx':{api}})
        return src, api, helper, test, bridge

    def test_proved_test_only_helpers_do_not_widen_production_api(self):
        src,api,helper,test,bridge=self.helper_graph()
        proved=selection.test_only_dependencies(self.graph,set(),self.inventory)
        self.assertTrue({test,bridge,helper} <= proved)
        self.assertNotIn(api,proved)
        self.assertEqual(self.names(self.choose([api],src)),{'messages','messages-isolation'})
        plan=self.choose([helper],src)
        self.assertTrue(plan['groups']['contracts'])
        self.assertFalse(plan['groups']['browser'])
        self.assertFalse(plan['groups']['native-no-store'])

    def test_helpers_with_unproved_runtime_importers_remain_conservative(self):
        for consumer in ('web/src/UnmappedPage.tsx', 'web/e2e/messages.spec.ts'):
            src,api,helper,test,bridge=self.helper_graph()
            src[consumer]='';self.graph[consumer]={helper}
            with self.subTest(consumer=consumer):
                self.assertNotIn(helper,selection.test_only_dependencies(self.graph,set(),self.inventory))
                self.assertEqual(len(self.choose([api],src)['browser']),56)
            self.graph.pop(consumer)

    def test_unknown_edges_or_cycles_or_orphans_never_prove_helpers(self):
        src,api,helper,test,bridge=self.helper_graph()
        self.assertEqual(self.names(self.choose([api],src,uncertain={test})),{'messages','messages-isolation'})
        for unknown in (helper, 'web/src/MessagesWorkspace.tsx', 'web/e2e/messages.spec.ts'):
            with self.subTest(unknown=unknown):
                self.assertEqual(len(self.choose([api],src,uncertain={unknown})['browser']),56)
        self.graph[helper].add(bridge)
        self.assertEqual(len(self.choose([api],src)['browser']),56)
        self.graph[helper].remove(bridge)
        self.graph[test]=set()
        self.assertEqual(len(self.choose([api],src)['browser']),56)

    def test_uncertain_test_imported_by_runtime_is_not_a_test_only_seed(self):
        for consumer in ('web/src/MessagesWorkspace.tsx', 'web/e2e/messages.spec.ts'):
            src,api,helper,test,bridge=self.helper_graph()
            self.graph[consumer]={test,api}
            with self.subTest(consumer=consumer):
                self.assertEqual(selection.test_only_dependencies(self.graph,{test},self.inventory),set())
                self.assertEqual(len(self.choose([api],src,uncertain={test})['browser']),56)
            self.graph[consumer]=set()

    def test_old_or_base_runtime_consumer_cannot_be_erased_by_new_test_only_graph(self):
        src,api,helper,test,bridge=self.helper_graph()
        old=dict(src, **{'web/src/OldPage.tsx':''})
        new_graph={p:self.graph.get(p,set()) for p in src}
        old_graph=dict(new_graph, **{'web/src/OldPage.tsx':{helper}})
        for position in range(4):
            snapshots=[src]*4;graphs=[(new_graph,set())]*4
            snapshots[position]=old;graphs[position]=(old_graph,set())
            with self.subTest(position=position),patch.object(selection,'dependency_graph',side_effect=graphs):
                plan=selection.select([api],self.inventory,snapshots)
                self.assertEqual(plan['browser'],self.inventory['browser'])
                self.assertEqual(plan['fixed'],self.inventory['fixed'])
                self.assertEqual(plan['auth'],list(range(1,9)))
                self.assertTrue(all(plan['groups'].values()))

    def test_unit_test_only_and_production_import_of_test(self):
        path='web/src/Example.test.tsx';src=dict(self.sources,**{path:''})
        self.assertFalse(self.choose([path],src)['groups']['browser'])
        self.graph['web/src/MessagesWorkspace.tsx']={path}
        self.assertEqual(len(self.choose([path],src)['browser']),56)

    def test_native_both_engines_and_order_independence(self):
        native='web/e2e/native-no-store-completion.spec.ts';unknown='web/src/NewPage.tsx'
        src=dict(self.sources,**{unknown:''})
        plans=[self.choose(list(paths),src) for paths in itertools.permutations([native,unknown])]
        self.assertEqual(plans[0]['groups'],plans[1]['groups'])
        self.assertEqual(plans[0]['browser'],plans[1]['browser'])
        self.assertEqual(len(plans[0]['browser']),56)
        native_plan=self.choose([native])
        self.assertTrue(native_plan['groups']['native-no-store'])
        self.assertTrue(native_plan['groups']['native-no-store-fixed'])
        self.assertEqual(native_plan['browser'],[])

    def test_computed_production_consumer_cannot_hide_behind_known_hit(self):
        dep='web/src/messages-api.ts';root='web/src/MessagesWorkspace.tsx'
        other='web/src/UnknownPage.tsx';src=dict(self.sources,**{dep:'',other:''})
        self.graph[root]={dep}
        self.assertEqual(len(self.choose([dep],src,uncertain={other})['browser']),56)

    def test_uncertain_graph_fails_safe(self):
        p='web/e2e/messages.spec.ts'
        self.assertEqual(len(self.choose([p],uncertain={p})['browser']),56)

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

    def test_dictionary4_suites_and_unmapped_roots_remain_complete(self):
        expected = {'directory-v4-controls', 'directory-v4-cancel', 'directory-v4-readers'}
        for suite in expected:
            plan = self.choose(['web/e2e/' + suite + '.spec.ts'])
            self.assertEqual(self.names(plan), {suite})
            self.assertEqual(plan['fixed'], [])
            self.assertEqual(plan['auth'], [])
            self.assertFalse(plan['groups']['native-no-store'])
            self.assertFalse(plan['groups']['native-no-store-fixed'])
        for path in ('web/src/DirectoryV4Workspace.tsx', 'web/src/DirectoryV4TaskResult.tsx',
                     'web/src/directory-v4-controls-api.ts', 'web/src/directory-v4-controls-json.ts',
                     'web/src/GroupMembersV4Workspace.tsx', 'web/src/group-members-v4-api.ts'):
            sources = dict(self.sources, **{path: ''})
            with self.subTest(path=path):
                plan = self.choose([path], sources)
                self.assertEqual(plan['browser'], self.inventory['browser'])
                self.assertEqual(plan['fixed'], self.inventory['fixed'])
                self.assertEqual(plan['auth'], list(range(1, 9)))
                self.assertTrue(all(plan['groups'].values()))
        self.assertNotIn('web/src/DirectoryV4Workspace.tsx', self.inventory['frontend_roots'])

    def test_full_unknown_fallback_preserves_proved_narrow_groups(self):
        src,api,helper,test,bridge=self.helper_graph()
        cases=[(['web/e2e/messages.spec.ts'],src,{'messages'},False),
               ([api],src,{'messages','messages-isolation'},False),
               ([helper],src,set(),False),
               ([test],src,set(),False),
               (['web/e2e/native-no-store-completion.spec.ts'],src,set(),True)]
        for paths,sources,names,native in cases:
            with self.subTest(paths=paths):
                plan=self.choose(paths,sources)
                self.assertEqual(self.names(plan),names)
                self.assertEqual(plan['fixed'],[])
                self.assertEqual(plan['auth'],[])
                self.assertEqual(plan['groups'],{
                    'contracts':True,'auth-integration':False,'browser':bool(names),
                    'native-no-store':native,'native-no-store-fixed':native,
                    'user-assets-fixed':False,'prepare-postgres':True})
        # A missing/deleted path cannot establish a safe frontend boundary.
        # The uncertain-import case also must not silently omit auth shards.
        for path,uncertain in [('web/src/Missing.ts',()),
                               ('web/e2e/messages.spec.ts',('web/e2e/messages.spec.ts',))]:
            with self.subTest(path=path):
                plan=self.choose([path],src,uncertain=uncertain)
                self.assertEqual(plan['browser'],self.inventory['browser'])
                self.assertEqual(plan['fixed'],self.inventory['fixed'])
                self.assertEqual(plan['auth'],list(range(1,9)))
                self.assertTrue(all(plan['groups'].values()))
                self.assertIn('unproved frontend dependency: '+path,plan['reasons'])

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
        self.assertEqual(len(self.inventory['browser']),56)
        self.assertEqual(len(self.inventory['fixed']),7)
        self.assertEqual(summary.INVENTORY,self.inventory)
        old=restore_selection(Path('.github/workflows/ci.yml').read_text())
        matrix=old.split('  browser:\n',1)[1].split('    steps:\n',1)[0]
        self.assertEqual([(e['name'],e['command']) for e in self.inventory['browser'] if e['name'] not in {'tasks-health-recovery', 'messages-isolation', 'directory-v4-controls', 'directory-v4-cancel', 'directory-v4-readers'}], re.findall(r'          - name: ([^\n]+)\n            command: ([^\n]+)',matrix))
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
            self.assertEqual(len(p['browser']),56)
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

    def test_isolation_evidence_validator_rejects_invalid_success_without_raw_errors(self):
        # Compile only the pure post-run validator. Never import/start the harness.
        runner = ast.parse(Path('scripts/test_auth_e2e.py').read_text())
        function = next(node for node in runner.body if isinstance(node, ast.FunctionDef)
                        and node.name == 'validate_messages_isolation_evidence')
        required = next(ast.literal_eval(node.value) for node in ast.walk(function)
                        if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name)
                        and target.id == 'required_sources' for target in node.targets))
        scope = dict(Path=Path, hashlib=hashlib, json=json, os=os, subprocess=subprocess, zlib=zlib, datetime=datetime, uuid=uuid)
        exec(compile(ast.Module(body=[function], type_ignores=[]), '<isolated-validator>', 'exec'), scope)
        validate = scope['validate_messages_isolation_evidence']
        phases = {'file_discovery', 'checkout_binding', 'source_binding', 'bundle_binding',
                  'png_evidence', 'payload_schema', 'artifact_files', 'ledger_schema',
                  'provenance', 'negative_matrix', 'bulk_ledger', 'negative_ledger'}
        expected_failures = {'messages_isolation_evidence_invalid:' + phase for phase in phases}
        assignments = [node for node in ast.walk(function) if isinstance(node, ast.Assign)
                       and any(isinstance(target, ast.Name) and target.id == 'failure' for target in node.targets)]
        self.assertEqual({ast.literal_eval(node.value) for node in assignments}, expected_failures)
        self.assertTrue(all(isinstance(node.value, ast.Constant) for node in assignments))
        failure_pattern = '^messages_isolation_evidence_invalid:(' + '|'.join(sorted(phases)) + ')$'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            output = root / 'web/test-results/messages-isolation'
            output.mkdir(parents=True)
            for name in required | {'bin/adtr', 'web/dist/index.html', 'web/dist/assets/app.js'}:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'synthetic fixture')
            sha = hashlib.sha256(b'synthetic fixture').hexdigest()
            shots = []
            for label in ('A', 'B'):
                for width, height in ((1440, 900), (390, 844)):
                    filename = f'messages-soc-isolation-{label}-{width}.png'
                    def chunk(kind, payload):
                        return len(payload).to_bytes(4, 'big') + kind + payload + (zlib.crc32(kind+payload) & 0xffffffff).to_bytes(4, 'big')
                    ihdr = width.to_bytes(4, 'big') + height.to_bytes(4, 'big') + bytes([8,6,0,0,0])
                    png = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr) + chunk(b'IDAT', zlib.compress(b'\0' * ((width*4+1)*height))) + chunk(b'IEND', b'')
                    (output / filename).write_bytes(png)
                    shots.append(dict(file=filename, identity=label, viewport=dict(width=width, height=height),
                                      proofEmpty=True, fullPage=True, sha256=hashlib.sha256(png).hexdigest()))
            timestamp = '2026-10-21T00:00:00+00:00'
            def identity_fixture(label, user):
                task_ids = [str(uuid.UUID(int=user*10+n)) for n in range(3)]
                messages = [dict(message_id=f'health-{user*10+n}', tenant_id='owned', recipient_user_id=user,
                                 source_task_id=task_ids[n], source_event_id=user*10+n, source_result_version=1,
                                 message_type='task_result', sub_type='succeeded', title='Infrastructure health task succeeded', description='The infrastructure health task completed successfully.',
                                 occurred_at=timestamp, ingested_at=timestamp) for n in range(3)]
                reads = [dict(tenant_id='owned', user_id=user, message_id=messages[0]['message_id'], first_read_at=timestamp)]
                single = [dict(tenant_id='owned', user_id=user, operation_id=label+'-single', result_id=label+'-initial',
                               message_id=messages[0]['message_id'], first_read_at=timestamp, committed_at=timestamp, newly_read=True)]
                baseline = dict(identity=[dict(id=user, tenant_id='owned', authorization_version=1)],
                                messages=messages, reads=reads, single=single, bulk=[], targets=[], receipts=[])
                provenance = [dict(messageId=row['message_id'], taskUUID=row['source_task_id'], title=row['title'],
                                   description=row['description'], occurredAt=timestamp, eventId=row['source_event_id'],
                                   outboxId=row['source_event_id']+100, sourceResultVersion=1, eventResultVersion=1,
                                   kind='infrastructure.health', action='finished', state='succeeded', taskDomain='platform',
                                   eventDomain='platform', tenantEqual=True, actorEqual=True, taskEqual=True, eventEqual=True,
                                   messageTenant='owned', taskTenant='owned', eventTenant='owned', userTenant='owned',
                                   recipientUserId=user, taskActorId=user, eventTaskId=row['source_task_id'],
                                   outboxTaskId=row['source_task_id'], outboxEventId=row['source_event_id'])
                              for row in messages]
                return dict(label=label, userId=user, tenantId='owned', authorizationVersion=1,
                            sensitiveActions={'A':8,'B':6}[label], snapshotAdmissions=5,
                            taskIds=task_ids, provenance=provenance, baseline=baseline)
            identities = [identity_fixture('A', 1), identity_fixture('B', 2)]
            current = {identity['label']: copy.deepcopy(identity['baseline']) for identity in identities}
            stages, bulks = [], []
            initial_suffixes = ['foreign-page', 'foreign-locate', 'foreign-single-receipt', 'foreign-read',
                                'foreign-single-replay', 'foreign-bulk-selected', 'foreign-bulk-all',
                                'own-result-foreign-single', 'own-result-foreign-selected-false', 'own-result-foreign-selected-true']
            def stage(label, suffix):
                error = 'target_not_in_result' if suffix.startswith('own-result-') else 'not_found'
                rows = [copy.deepcopy(current[actor]) for actor in ('A','B')]
                return dict(label=label+'-'+suffix, status=400 if error=='target_not_in_result' else 404,
                            body=dict(error=error), before=rows, after=copy.deepcopy(rows))
            for label in ('A','B'):
                stages.extend(stage(label, suffix) for suffix in initial_suffixes)
            for identity in identities:
                label, user = identity['label'], identity['userId']
                other = 'B' if label == 'A' else 'A'
                for mode in ('selected','all'):
                    before = copy.deepcopy(current[label])
                    after = copy.deepcopy(before)
                    count = 1 if mode == 'selected' else 3
                    operation_id, result_id = label+'-'+mode, label+'-frozen'
                    selected = [identity['baseline']['messages'][1]] if mode == 'selected' else identity['baseline']['messages']
                    for ordinal, row in enumerate(selected):
                        newly = row['message_id'] not in {read['message_id'] for read in after['reads']}
                        if newly:
                            after['reads'].append(dict(tenant_id='owned', user_id=user, message_id=row['message_id'], first_read_at=timestamp))
                        after['targets'].append(dict(tenant_id='owned', user_id=user, operation_id=operation_id, ordinal=ordinal, message_id=row['message_id']))
                        after['receipts'].append(dict(tenant_id='owned', user_id=user, operation_id=operation_id, ordinal=ordinal,
                                                      first_read_at=timestamp, committed_at=timestamp, newly_read=newly))
                    after['bulk'].append(dict(tenant_id='owned', user_id=user, operation_id=operation_id, queue_id=user*10+count,
                                             result_id=result_id, selection=mode, authorization_version=1,
                                             accepted_at=timestamp, updated_at=timestamp, state='completed', target=count,
                                             processed=count, newly_read=1, already_read=count-1))
                    operation = dict(state='completed', updatedAt=timestamp,
                                     receipt=dict(acceptedAt=timestamp, operationId=operation_id, resultId=result_id, selection=mode, target=count),
                                     progress=dict(target=count, processed=count, newlyRead=1, alreadyRead=count-1, failed=0, remaining=0))
                    current[label] = after
                    protected = copy.deepcopy(current[other])
                    bulks.append(dict(actor=label, selection=mode, operation=operation, before=before, after=after,
                                      protectedBefore=protected, protectedAfter=copy.deepcopy(protected)))
                    stages.extend(stage(other, name+'-'+mode) for name in ('foreign-bulk-receipt','foreign-bulk-replay','foreign-completed-cancel'))
            good = dict(formatVersion=1, suite='messages-isolation', status='passed',
                        checkoutCommit='a'*40, checkoutTree='b'*40, dirty=False,
                        workflowCommit='a'*40, workflowRun='123', workflowAttempt='1', browser='synthetic',
                        sourceSHA256={name: sha for name in required},
                        artifactsSHA256=dict(apiWorker=sha, frontendEntry=sha, frontendFiles={"web/dist/index.html":sha,"web/dist/assets/app.js":sha}), screenshots=shots,
                        identities=identities, stages=stages, positiveBulks=bulks,
                        phaseBudget=dict(limitMs=60000, snapshotElapsedMs=100, totpWaits=8, setupSpent=100),
                        limitations=['synthetic tasks-writer role, not reader-only', 'API pagination, not two full UI pages',
                                     'completed-operation foreign cancellation only', 'screenshots require independent pixel review'])
            manifest = output / 'messages-soc-isolation-evidence.json'
            original = Path.cwd()
            try:
                os.chdir(root)
                with patch.dict(os.environ, {'GITHUB_SHA':'a'*40, 'GITHUB_RUN_ID':'123', 'GITHUB_RUN_ATTEMPT':'1'}), \
                     patch.object(subprocess, 'check_output', side_effect=lambda command, **_: '' if command[-1] == '--porcelain' else 'b'*40 if command[-1] == 'HEAD^{tree}' else 'a'*40):
                    manifest.write_text(json.dumps(good))
                    validate()
                    # Playwright path attachments create additional prefixed
                    # copies; even byte-identical copies must not widen upload.
                    attachments = output / 'attachments'
                    attachments.mkdir()
                    for artifact in [manifest] + [output / shot['file'] for shot in shots]:
                        (attachments / (artifact.stem + '-copy' + artifact.suffix)).write_bytes(artifact.read_bytes())
                    with self.assertRaisesRegex(RuntimeError, '^messages_isolation_evidence_invalid:artifact_files$'):
                        validate()
                    for artifact in attachments.iterdir():
                        artifact.unlink()
                    attachments.rmdir()
                    validate()
                    mutations = [lambda d: d.update(status='failed'),
                                 lambda d: d.update(dirty=True),
                                 lambda d: d.update(formatVersion=True),
                                 lambda d: d.update(workflowRun=''),
                                 lambda d: d.update(workflowRun='stale'),
                                 lambda d: d['sourceSHA256'].pop(next(iter(required))),
                                 lambda d: d['artifactsSHA256'].update(apiWorker='0'*64),
                                 lambda d: d['artifactsSHA256']['frontendFiles'].pop('web/dist/assets/app.js'),
                                 lambda d: d['screenshots'][0].update(proofEmpty=False),
                                 lambda d: d['screenshots'][0].update(sha256='0'*64),
                                 lambda d: d['screenshots'][0].update(file='../outside.png'),
                                 lambda d: d['identities'][0].update(snapshotAdmissions=8),
                                 lambda d: d['identities'][0].update(taskIds=[]),
                                 lambda d: d['identities'][0].update(userId=True),
                                 lambda d: d['identities'][0].update(sensitiveActions=True),
                                 lambda d: d['identities'][0]['baseline']['reads'][0].update(first_read_at='not-time'),
                                 lambda d: d['identities'][0]['baseline']['messages'][0].update(title={'private':'value'}),
                                 lambda d: d['identities'][0]['provenance'][0].update(eventId=True),
                                 lambda d: d['identities'][0]['provenance'][0].update(actorEqual=False),
                                 lambda d: d['identities'][0]['provenance'][0].update(taskActorId=2),
                                 lambda d: d['identities'][0]['provenance'][0].update(outboxTaskId='foreign'),
                                 lambda d: d['identities'][0]['provenance'][0].update(eventId=-1),
                                 lambda d: d['identities'][0]['baseline']['messages'][0].update(recipient_user_id=2),
                                 lambda d: d['identities'][0]['baseline']['single'][0].update(newly_read=False),
                                 lambda d: d.update(stages=d['stages'][:-1]),
                                 lambda d: d['stages'][0].update(label='A-invented'),
                                 lambda d: d['positiveBulks'][0]['operation']['progress'].update(newlyRead=0),
                                 lambda d: d['positiveBulks'][0]['operation'].update(updatedAt='2026-10-21T00:00:00.000000001Z'),
                                 lambda d: d['positiveBulks'][0]['after']['targets'][0].update(message_id='foreign'),
                                 lambda d: d['positiveBulks'][0]['after']['receipts'][0].update(first_read_at='drift'),
                                 lambda d: d.update(extra='not allowed'),
                                 lambda d: d['identities'][0]['baseline']['messages'][0].update(secret='not allowed'),
                                 lambda d: d['sourceSHA256'].update(unexpected=sha),
                                 lambda d: d['stages'][0].update(after=[3]),
                                 lambda d: d['stages'][0]['body'].update(private='must not echo'),
                                 lambda d: d['positiveBulks'][0].update(protectedAfter=[2]),
                                 lambda d: d['phaseBudget'].update(snapshotElapsedMs=60001)]
                    for mutate in mutations:
                        invalid = copy.deepcopy(good)
                        mutate(invalid)
                        manifest.write_text(json.dumps(invalid))
                        with self.assertRaisesRegex(RuntimeError, failure_pattern) as caught:
                            validate()
                        self.assertTrue(caught.exception.__suppress_context__)
                    manifest.write_text(json.dumps(good))
                    extra = root / 'web/test-results/messages-soc-unvalidated.txt'
                    extra.write_text('must not upload')
                    with self.assertRaisesRegex(RuntimeError, failure_pattern):
                        validate()
                    extra.unlink()
                    with patch.object(subprocess, 'check_output', side_effect=RuntimeError('private sentinel')):
                        with self.assertRaisesRegex(RuntimeError, '^messages_isolation_evidence_invalid:checkout_binding$') as caught:
                            validate()
                        self.assertTrue(caught.exception.__suppress_context__)
                        self.assertIsNone(caught.exception.__cause__)
                        self.assertNotIn('private sentinel', str(caught.exception))
                    shot_path = output / shots[0]['file']
                    original_png = shot_path.read_bytes()
                    shot_path.write_bytes(original_png[:-1])
                    with self.assertRaisesRegex(RuntimeError, failure_pattern):
                        validate()
                    shot_path.write_bytes(original_png)
                    outside = root / 'outside.png'
                    outside.write_bytes(original_png)
                    shot_path.unlink()
                    shot_path.symlink_to(outside)
                    with self.assertRaisesRegex(RuntimeError, failure_pattern):
                        validate()
                    shot_path.unlink()
                    shot_path.write_bytes(original_png)
                    corrupted = original_png[:-1] + bytes([original_png[-1] ^ 1])
                    shot_path.write_bytes(corrupted)
                    invalid = copy.deepcopy(good)
                    invalid['screenshots'][0]['sha256'] = hashlib.sha256(corrupted).hexdigest()
                    manifest.write_text(json.dumps(invalid))
                    with self.assertRaisesRegex(RuntimeError, failure_pattern):
                        validate()
                    shot_path.write_bytes(original_png)
                    manifest.write_text(json.dumps(good))
                    with patch.object(subprocess, 'check_output', side_effect=lambda command, **_: ' M tracked' if command[-1] == '--porcelain' else 'b'*40 if command[-1] == 'HEAD^{tree}' else 'a'*40):
                        with self.assertRaisesRegex(RuntimeError, failure_pattern):
                            validate()
                    manifest.write_text('{"private":"must not echo","private":2}')
                    with self.assertRaisesRegex(RuntimeError, failure_pattern):
                        validate()
            finally:
                os.chdir(original)

    def test_health_inventory_is_exact_addition_and_dependencies_stay_conservative(self):
        name = 'tasks-health-recovery'
        entry = {'name': name, 'command': 'python3 scripts/test_auth_e2e.py --suite ' + name,
                 'specs': ['web/e2e/' + name + '.spec.ts']}
        self.assertEqual([e for e in self.inventory['browser'] if e['name'] == name], [entry])
        self.assertNotIn(name, self.inventory['fixed'])
        self.assertIn(name, self.inventory['frontend_roots']['web/src/TaskWorkspace.tsx'])
        predecessor = copy.deepcopy(self.inventory)
        predecessor['browser'].remove(entry)
        predecessor['frontend_roots']['web/src/TaskWorkspace.tsx'].remove(name)
        self.assertEqual(hashlib.sha256((json.dumps(predecessor, indent=2) + '\n').encode()).hexdigest(),
                         '20df16aa0b9168369ddafe611bdf4bd316106b5cc0923233a84dcc811457afb4')
        self.assertEqual(self.names(self.choose(entry['specs'])), {name})
        self.assertIn(name, self.names(self.choose(['web/src/TaskWorkspace.tsx'])))
        for path in ['scripts/test_auth_e2e.py', 'scripts/ci_inventory.json',
                     'scripts/ci_selection.py', '.github/workflows/ci.yml',
                     'scripts/directory_v4_worker_control.py',
                     'scripts/validate_tasks_health_recovery_evidence.py',
                     'scripts/fixtures/tasks_health_recovery_evidence.json',
                     'scripts/fixtures/tasks_health_recovery_ci_delta.json',
                     'scripts/fixtures/tasks_health_recovery_runner_delta.json',
                     'internal/auth/access_http.go', 'internal/tasks/worker.go',
                     'cmd/adtr/main.go', 'web/playwright.config.ts', 'web/package-lock.json']:
            plan = self.choose([path])
            self.assertEqual(plan['browser'], self.inventory['browser'], path)
            self.assertTrue(all(plan['groups'].values()), path)
        for path in ['scripts/test_tasks_health_recovery_browser_contract.py',
                     'scripts/test_tasks_health_recovery_evidence_contract.py',
                     'scripts/test_tasks_health_recovery_runner_contract.py']:
            self.assertTrue(self.choose([path])['groups']['contracts'], path)
        helper = 'web/test-helpers/health-shared.ts'
        src = dict(self.sources, **{helper: ''})
        self.graph[entry['specs'][0]] = {helper}
        self.assertIn(name, self.names(self.choose([helper], src)))

    def test_actual_health_workflow_build_identity_deadline_and_six_file_upload(self):
        text = Path('.github/workflows/ci.yml').read_text()
        block = text.split('  browser:\n', 1)[1].split('\n  native-no-store:', 1)[0]
        self.assertIn("timeout-minutes: ${{ matrix.name == 'tasks-health-recovery' && 30 || 20 }}", block)
        self.assertIn('matrix: ${{ fromJSON(needs.plan.outputs.browser_matrix) }}', block)
        self.assertIn('max-parallel: 8', block)
        self.assertIn('contents: read', block)
        build = block.split('      - name: Build real API and frontend\n', 1)[1].split('      - name:', 1)[0]
        self.assertIn("if: ${{ matrix.name != 'tasks-health-recovery' }}", build)
        self.assertIn('run: go mod verify && make build web-build', build)
        locked = block.split('      - name: Install locked health recovery dependencies', 1)[1].split('      - name:', 1)[0]
        self.assertIn("if: ${{ matrix.name == 'tasks-health-recovery' }}", locked)
        self.assertIn('run: go mod verify && npm ci --prefix web', locked)
        self.assertNotIn('npm run build', locked)
        deadline = block.split('      - name: Bound health recovery job', 1)[1].split('      - name:', 1)[0]
        self.assertIn("if: ${{ matrix.name == 'tasks-health-recovery' }}", deadline)
        self.assertIn('ADTR_E2E_HEALTH_JOB_DEADLINE=', deadline)
        self.assertIn('int(time.time()) + 1620', deadline)
        self.assertIn('GITHUB_ENV', deadline)
        self.assertLess(block.index('Bound health recovery job'), block.index('uses: actions/checkout@'))
        # The nonce is generated in the owned runner, never a public VITE input or workflow literal.
        self.assertNotIn('ADTR_E2E_HEALTH_RUN_NONCE', block)
        validator = 'python3 scripts/validate_tasks_health_recovery_evidence.py --evidence-dir web/playwright-report/tasks-health-recovery-validated --source-root .'
        self.assertLess(block.index('run: ${{ matrix.command }}'), block.index(validator))
        start = block.index('      - name: Validate joint health recovery source')
        end = block.index('      - name: Validate safe failure diagnostics', start)
        evidence = block[start:end]
        self.assertIn("if: ${{ success() && matrix.name == 'tasks-health-recovery' }}", evidence)
        self.assertIn("success() && matrix.name == 'tasks-health-recovery' && steps.health-recovery-evidence.outcome == 'success'", evidence)
        self.assertIn('actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02', evidence)
        files = ['manifest.json', 'cleanup-receipt.json', 'parent-failed-desktop.png',
                 'parent-failed-mobile.png', 'child-succeeded-desktop.png', 'child-succeeded-mobile.png']
        self.assertEqual(json.loads(Path('scripts/fixtures/tasks_health_recovery_evidence.json').read_text())['files'], files)
        self.assertEqual(re.findall(r'^            (web/[^\n]+)$', evidence, re.M),
                         ['web/playwright-report/tasks-health-recovery-validated/' + f for f in files])
        self.assertNotIn('*', evidence)
        self.assertNotIn('always()', evidence)
        self.assertIn('if-no-files-found: error', evidence)
        self.assertIn('retention-days: 7', evidence)
        self.assertEqual(evidence.count('timeout-minutes: 1'), 2)
        diagnostics = block[end:].split('      - name: Validate real empty', 1)[0]
        self.assertIn("failure() && matrix.name != 'tasks-health-recovery'", diagnostics)
        self.assertIn("failure() && matrix.name != 'tasks-health-recovery' && steps.safe-diagnostics.outcome == 'success'", diagnostics)

    def test_health_outer_projection_preserves_complete_predecessor_and_rejects_mutations(self):
        text = Path('.github/workflows/ci.yml').read_text()
        delta = json.loads(Path('scripts/fixtures/tasks_health_recovery_ci_delta.json').read_text())
        self.assertEqual(delta['baselineSHA256'], 'f4e8111f36a7441951012027b1371059ca8f86c5a21663f1546ff78b64b89c13')
        self.assertEqual(len(delta['replacements']), 5)
        predecessor = restore_health_recovery(text)
        self.assertEqual(hashlib.sha256(predecessor.encode()).hexdigest(), delta['baselineSHA256'])
        self.assertEqual(restore_selection(text), restore_selection_predecessor(predecessor))
        variants = [predecessor]
        for change in delta['replacements']:
            self.assertEqual(text.count(change['new']), 1)
            variants.extend([text.replace(change['new'], '', 1),
                             text.replace(change['new'], change['new'] * 2, 1),
                             text.replace(change['new'], change['old'], 1),
                             text.replace(change['new'], change['new'] + ' tampered\n', 1)])
        for old, new in [('contents: read', 'contents: write'), ('run: make check', 'run: true'),
                         ('max-parallel: 8', 'max-parallel: 9'),
                         ('ADTR_E2E_HEALTH_JOB_DEADLINE=', 'VITE_HEALTH_JOB_DEADLINE='),
                         ('if-no-files-found: error', 'if-no-files-found: warn'),
                         ('web/playwright-report/tasks-health-recovery-validated/manifest.json', 'web/test-results/**'),
                         ('cache: true', 'cache: false'), ('retention-days: 7', 'retention-days: 8')]:
            self.assertIn(old, text)
            variants.append(text.replace(old, new, 1))
        for variant in variants:
            with self.subTest(sha256=hashlib.sha256(variant.encode()).hexdigest()):
                with self.assertRaises(ValueError):
                    restore_health_recovery(variant)
                with self.assertRaises(ValueError):
                    restore_selection(variant)

    def test_isolation_workflow_delta_is_exact_and_cannot_mask_other_changes(self):
        text = restore_health_recovery(Path('.github/workflows/ci.yml').read_text())
        delta = json.loads(Path('scripts/fixtures/messages_isolation_ci_delta.json').read_text())
        self.assertEqual(delta['baselineSHA256'], 'c6ecd0591b3b8eff1b9d653aa19c6bcb14232616a9f1ff12930705825fd4f03a')
        self.assertEqual(len(delta['replacements']), 2)
        restored = restore_messages_isolation_predecessor(text)
        self.assertEqual(hashlib.sha256(restored.encode()).hexdigest(), delta['baselineSHA256'])
        # Missing, duplicate, partially reversed and tampered reviewed blocks
        # must fail, as must any unrelated command, permission or budget change.
        variants = [restored]
        for change in delta['replacements']:
            self.assertEqual(text.count(change['new']), 1)
            variants.extend([text.replace(change['new'], '', 1),
                             text.replace(change['new'], change['new'] * 2, 1),
                             text.replace(change['new'], change['old'], 1),
                             text.replace(change['new'], change['new'].rstrip() + ' tampered\n', 1)])
        for old, new in [('contents: read', 'contents: write'),
                         ('run: make check', 'run: true'),
                         ('timeout-minutes: 20', 'timeout-minutes: 21'),
                         ('path: web/test-results/**/messages-soc-*', 'path: web/test-results/**')]:
            self.assertIn(old, text)
            variants.append(text.replace(old, new, 1))
        for variant in variants:
            with self.subTest(variantSHA256=hashlib.sha256(variant.encode()).hexdigest()):
                with self.assertRaises(ValueError):
                    restore_messages_isolation_predecessor(variant)
                with self.assertRaises(ValueError):
                    restore_selection_predecessor(variant)
        block = text.split('      - name: Retain synthetic message workspace evidence\n', 1)[1].split('      - name:', 1)[0]
        self.assertIn("success() && (matrix.name == 'messages' || matrix.name == 'messages-isolation')", block)
        self.assertIn('name: ${{ matrix.name }}-soc-evidence-${{ github.run_id }}-${{ github.run_attempt }}', block)
        self.assertIn('path: web/test-results/**/messages-soc-*', block)
        self.assertIn('if-no-files-found: error', block)
        self.assertIn('retention-days: 7', block)

    def test_workflow_exact_delta_and_all_original_steps_preserved(self):
        actual=Path('.github/workflows/ci.yml').read_text()
        text=restore_health_recovery(actual)
        original=restore_selection_predecessor(text)
        self.assertEqual(original, restore_selection(actual))
        self.assertIn('run: make check',original)
        self.assertIn('shard: [1, 2, 3, 4, 5, 6, 7, 8]',original)
        for old,new in [('needs.plan.outputs.browser == \'true\'', 'false'),('needs: [plan, prepare-postgres]','needs: [plan]'),('fromJSON(needs.plan.outputs.browser_matrix)','fromJSON(\'{}\')'),('if: ${{ always() }}','if: ${{ success() }}'),('run: make check','run: true'),('contents: read','contents: write')]:
            self.assertIn(old,text)
            with self.assertRaises(ValueError):restore_selection_predecessor(text.replace(old,new,1))
        self.assertIn('run: python3 scripts/ci_selection_summary.py',text)
        self.assertIn("expected = make_plan(",Path('scripts/ci_selection_summary.py').read_text())
        self.assertIn("node --test scripts/test_ci_dependencies.mjs",text)
        self.assertIn('fetch-depth: 0',text)

if __name__=='__main__':unittest.main()
