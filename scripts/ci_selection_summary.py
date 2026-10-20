"""Independently recompute the immutable selection before accepting job results."""
import json
import os
import re

from pathlib import Path
from ci_selection import make_plan, inventory_checked, parse_json

INVENTORY = inventory_checked(parse_json(Path('scripts/ci_inventory.json').read_text()))

GROUPS = ('prepare-postgres', 'contracts', 'auth-integration', 'browser',
          'native-no-store', 'native-no-store-fixed', 'user-assets-fixed')


def verify(plan, needs, event, base, head, expected_plan):
    if plan != expected_plan:
        raise ValueError('selection differs from independently recomputed revision dependencies')
    expected_keys = {'groups','browser','fixed','auth','reasons','version','event',
                     'base','head','tested','mergeBase','paths','inventorySHA256'}
    if type(plan) is not dict or set(plan) != expected_keys or type(plan['version']) is not int or plan['version'] != 1:
        raise ValueError('invalid selection plan')
    if (plan['event'],plan['base'],plan['head']) != (event,base,head):
        raise ValueError('selection revision mismatch')
    if event not in {'pull_request','push','merge_group','workflow_dispatch'} or not re.fullmatch('[0-9a-f]{40}',head):
        raise ValueError('invalid selection event')
    if type(plan['groups']) is not dict or set(plan['groups']) != set(GROUPS) or any(type(v) is not bool for v in plan['groups'].values()):
        raise ValueError('invalid selection groups')
    if not re.fullmatch('[0-9a-f]{64}',plan['inventorySHA256']):
        raise ValueError('invalid inventory binding')
    if not isinstance(plan['paths'],list) or not isinstance(plan['reasons'],list) or not plan['reasons'] or any(type(v) is not str or not v for v in plan['reasons']):
        raise ValueError('missing selection explanation')
    if event == 'pull_request':
        if not re.fullmatch('[0-9a-f]{40}',plan['mergeBase']) or not plan['paths']:
            raise ValueError('missing PR diff')
        if len(set(plan['paths'])) != len(plan['paths']) or any(type(p) is not str or not p or p.startswith('/') or '..' in p.split('/') or any(ord(c)<32 for c in p) for p in plan['paths']):
            raise ValueError('invalid PR paths')
    elif plan['paths'] or plan['mergeBase'] or not all(plan['groups'].values()):
        raise ValueError('full-event coverage reduced')
    if type(plan['browser']) is not list or type(plan['fixed']) is not list or type(plan['auth']) is not list:
        raise ValueError('invalid matrix plan')
    import hashlib
    digest=hashlib.sha256(json.dumps(INVENTORY,sort_keys=True,separators=(',', ':')).encode()).hexdigest()
    if plan['inventorySHA256'] != digest:
        raise ValueError('unknown inventory')
    names=[]
    for entry in plan['browser']:
        if type(entry) is not dict or set(entry) != {'name','command','specs'} or not re.fullmatch('[a-z0-9-]+',entry['name']):
            raise ValueError('invalid browser entry')
        if entry not in INVENTORY['browser']:
            raise ValueError('unreviewed browser command')
        names.append(entry['name'])
    if len(set(names)) != len(names) or len(set(plan['fixed'])) != len(plan['fixed']) or not set(plan['fixed']) <= set(names):
        raise ValueError('inconsistent matrices')
    if plan['fixed'] != [name for name in INVENTORY['fixed'] if name in names]:
        raise ValueError('unknown fixed suite')
    if event != 'pull_request' and (plan['browser'] != INVENTORY['browser'] or set(plan['fixed']) != set(INVENTORY['fixed'])):
        raise ValueError('full matrix reduced')
    groups=plan['groups']
    if groups['browser'] != bool(names) or groups['user-assets-fixed'] != bool(plan['fixed']) or plan['auth'] != (list(range(1,9)) if groups['auth-integration'] else []):
        raise ValueError('matrix coverage mismatch')
    if groups['native-no-store'] != groups['native-no-store-fixed'] or groups['prepare-postgres'] != any(groups[n] for n in ('contracts','auth-integration','browser','user-assets-fixed')):
        raise ValueError('dependency coverage mismatch')
    if not any(groups.values()) and any(not ((p=='README.md' or p.startswith('docs/')) and p.endswith('.md')) for p in plan['paths']):
        raise ValueError('empty non-documentation plan')
    if type(needs) is not dict or set(needs) != set(GROUPS)|{'plan'} or needs['plan']['result'] != 'success':
        raise ValueError('selection planning did not succeed')
    for name in GROUPS:
        expected='success' if groups[name] else 'skipped'
        if needs[name]['result'] != expected:
            raise ValueError('selected check missing, failed or unexpectedly executed: '+name)
    return [name+(': passed' if groups[name] else ': not run (not selected)') for name in GROUPS]


def main():
    expected = make_plan(os.environ['GITHUB_EVENT_NAME'],os.environ['ADTR_EVENT_BASE_SHA'],os.environ['ADTR_EVENT_HEAD_SHA'],INVENTORY,tested=os.environ['GITHUB_SHA'])
    report=verify(parse_json(os.environ['SELECTION_PLAN']),parse_json(os.environ['NEEDS_JSON']),
                  os.environ['GITHUB_EVENT_NAME'],os.environ['ADTR_EVENT_BASE_SHA'],os.environ['ADTR_EVENT_HEAD_SHA'],expected)
    with open(os.environ['GITHUB_STEP_SUMMARY'],'a') as out:
        out.write('## Selected CI results\n'+'\n'.join('- '+line for line in report)+'\n')
    print('\n'.join(report))

if __name__=='__main__':
    main()
