#!/usr/bin/env python3
"""Fail-closed PR CI planning. No labels, PR text, or user-supplied test commands."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess

GROUPS = ('prepare-postgres', 'contracts', 'auth-integration', 'browser',
          'native-no-store', 'native-no-store-fixed', 'user-assets-fixed')
SHA = re.compile(r'[0-9a-f]{40}')


def parse_json(value):
    def unique(pairs):
        out = {}
        for key, item in pairs:
            if key in out:
                raise ValueError('duplicate JSON key')
            out[key] = item
        return out
    return json.loads(value, object_pairs_hook=unique)


def git(*args):
    return subprocess.run(['git', *args], check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=60).stdout


def path_checked(value):
    if not isinstance(value, str) or not value or '\\' in value or any(ord(c) < 32 for c in value):
        raise ValueError('invalid changed path')
    p = PurePosixPath(value)
    if p.is_absolute() or '..' in p.parts or str(p) != value:
        raise ValueError('invalid changed path')
    return value


def changed_paths(data):
    """Parse --name-status -z, retaining both names of a rename/copy."""
    if not data or not data.endswith(b'\0'):
        raise ValueError('missing or incomplete diff')
    tokens = data[:-1].decode('utf-8', errors='strict').split('\0')
    paths = set()
    while tokens:
        status = tokens.pop(0)
        if not re.fullmatch(r'(?:[AMDT]|[RC][0-9]{1,3})', status):
            raise ValueError('unsupported diff status')
        count = 2 if status[0] in 'RC' else 1
        if len(tokens) < count:
            raise ValueError('incomplete changed path')
        for _ in range(count):
            paths.add(path_checked(tokens.pop(0)))
    if not paths:
        raise ValueError('empty PR diff')
    return sorted(paths)


def pr_diff(base, head):
    if not SHA.fullmatch(base) or not SHA.fullmatch(head):
        raise ValueError('invalid event revision')
    merge = git('merge-base', '--all', base, head).decode().strip()
    if not SHA.fullmatch(merge):
        raise ValueError('ambiguous merge base')
    return merge, changed_paths(git('diff', '--name-status', '-z', '--find-renames', merge, head, '--'))


def web_sources(revision):
    names = git('ls-tree', '-r', '--name-only', '-z', revision, '--', 'web').decode().split('\0')
    return {p: git('show', revision + ':' + p).decode('utf-8', errors='strict')
            for p in names if p.endswith(('.ts', '.tsx', '.css'))}


def dependency_graph(sources):
    result = subprocess.run(['node', 'scripts/ci_dependencies.mjs'],
                            input=json.dumps(sources).encode(), stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, check=True, timeout=60)
    value = parse_json(result.stdout)
    if type(value) is not dict or set(value) != {'graph', 'uncertain'} or set(value['graph']) != set(sources):
        raise ValueError('incomplete dependency graph')
    graph = {p: set(edges) for p, edges in value['graph'].items()}
    if any(not edges <= set(sources) for edges in graph.values()) or not set(value['uncertain']) <= set(sources):
        raise ValueError('invalid dependency graph')
    return graph, set(value['uncertain'])


def closure(graph, roots):
    seen = set()
    todo = list(roots)
    while todo:
        p = todo.pop()
        if p in seen:
            continue
        seen.add(p)
        todo.extend(graph.get(p, ()))
    return seen


def test_only_dependencies(graph, uncertain, inventory):
    """Prove helpers are used exclusively by unit tests, without naming helpers.

    Unknown runtime edges can hide another importer. Unknown outgoing edges of
    an isolated unit-test root only add test consumers. Cycles and orphan non-test
    modules deliberately stay unproved.
    """
    importers = {path: set() for path in graph}
    for importer, dependencies in graph.items():
        for dependency in dependencies:
            importers[dependency].add(importer)
    runtime_roots = set(inventory['frontend_roots']) | {
        'web/src/App.tsx', 'web/src/main.tsx',
    } | {path for path in graph if path.startswith('web/e2e/')}
    runtime = closure(graph, runtime_roots)
    proved = {path for path in graph
              if re.search(r'\.test\.tsx?$', path)
              and path.startswith('web/src/')
              and not importers[path] and path not in runtime}
    if uncertain - proved:
        return set()
    while True:
        added = {path for path in graph
                 if path.startswith('web/src/') and path not in runtime | proved
                 and importers[path] and importers[path] <= proved}
        if not added:
            return proved
        proved.update(added)


def inventory_checked(inventory):
    if type(inventory) is not dict or set(inventory) != {'browser', 'fixed', 'frontend_roots'}:
        raise ValueError('invalid inventory')
    names = set()
    for entry in inventory['browser']:
        if type(entry) is not dict or set(entry) != {'name', 'command', 'specs'}:
            raise ValueError('invalid browser inventory')
        if not re.fullmatch('[a-z0-9-]+', entry['name']) or entry['name'] in names:
            raise ValueError('duplicate browser inventory')
        names.add(entry['name'])

        if not entry['specs']:
            raise ValueError('missing suite specs')
        for spec in entry['specs']:
            path_checked(spec)
        suffix = {'authentication': '', 'expired-password': ' --expired', 'resources': ' --suite resource'}.get(entry['name'], ' --suite ' + entry['name'])
        if entry['command'] != 'python3 scripts/test_auth_e2e.py' + suffix:
            raise ValueError('invalid browser command')
    if type(inventory['fixed']) is not list or len(set(inventory['fixed'])) != len(inventory['fixed']) or not names or not set(inventory['fixed']) <= names:
        raise ValueError('invalid fixed inventory')
    for root, suites in inventory['frontend_roots'].items():
        path_checked(root)
        if not suites or not set(suites) <= names:
            raise ValueError('invalid frontend roots')
    return inventory


def select(paths, inventory, snapshots, full=False):
    inventory_checked(inventory)
    all_names = {e['name'] for e in inventory['browser']}
    selected = set()
    contracts = auth = native = False
    reasons = []
    graphs = [dependency_graph(s) for s in snapshots]
    def all_checks(reason):
        nonlocal contracts, auth, native
        contracts = auth = native = True
        selected.update(all_names)
        reasons.append(reason)
    if full:
        all_checks('non-PR event retains full coverage')
    else:
        if not paths or len(graphs) < 2:
            raise ValueError('incomplete PR inputs')
        for path in paths:
            path_checked(path)
            if (path == 'README.md' or path.startswith('docs/')) and path.endswith('.md'):
                reasons.append('documentation: ' + path)
                continue
            contracts = True
            if path.startswith('web/') and path.endswith(('.ts', '.tsx', '.css')):
                hits = set()
                unknown = False
                seen = False
                for sources, (graph, uncertain) in zip(snapshots, graphs):
                    if path not in sources:
                        continue
                    seen = True
                    test_only = test_only_dependencies(graph, uncertain, inventory)
                    if any(p.startswith('web/src/') and not re.search(r'\.test\.tsx?$', p) for p in uncertain):
                        unknown = True
                    local_hits = set()
                    native_hit = False
                    for entry in inventory['browser']:
                        deps = closure(graph, entry['specs'])
                        if path in deps:
                            local_hits.add(entry['name'])
                        if deps & uncertain:
                            unknown = True
                    native_deps = closure(graph, ['web/e2e/native-no-store-completion.spec.ts'])
                    if path in native_deps:
                        native_hit = True
                        native = True
                    if native_deps & uncertain:
                        unknown = True
                    for root, suites in inventory['frontend_roots'].items():
                        deps = closure(graph, [root])
                        if path in deps:
                            local_hits.update(suites)
                        if deps & uncertain:
                            unknown = True
                    covered = closure(graph, inventory['frontend_roots'])
                    composition = {'web/src/App.tsx', 'web/src/main.tsx'}
                    if path in composition or any(path in graph.get(root, ()) and path not in inventory['frontend_roots'] for root in composition):
                        unknown = True
                    # A module imported by an unclassified production page has
                    # consumers beyond the reviewed page-to-suite inventory.
                    for consumer in graph:
                        if consumer.startswith('web/src/') and not re.search(r'\.test\.tsx?$', consumer) and consumer not in covered | composition | test_only:
                            if path in closure(graph, [consumer]):
                                unknown = True
                    is_test = bool(re.search(r'\.test\.tsx?$', path))
                    if is_test:
                        consumers = [p for p in graph if p != path and not re.search(r'\.test\.tsx?$', p) and path in closure(graph, [p])]
                        if consumers:
                            unknown = True
                    elif not local_hits and not native_hit and path not in test_only:
                        unknown = True
                    hits.update(local_hits)
                if not seen or unknown:
                    selected.update(all_names)
                    native = True
                    reasons.append('unproved frontend dependency: ' + path)
                else:
                    selected.update(hits)
                    reasons.append('frontend dependency closure: ' + path)
            elif path in {'scripts/owned_rescue_runner.py', 'scripts/asset_export_v3_running_cancel_guard.py'}:
                selected.update(n for n in all_names if n.endswith('-running-cancel'))
                reasons.append('owned running-cancel controller: ' + path)
            elif path.startswith('scripts/test_') and path.endswith('_contract.py'):
                reasons.append('offline contract test: ' + path)
            else:
                all_checks('shared or unclassified dependency: ' + path)
    fixed = [name for name in inventory['fixed'] if name in selected]
    groups = {g: False for g in GROUPS}
    groups.update({'contracts': contracts, 'auth-integration': auth, 'browser': bool(selected),
                   'native-no-store': native, 'native-no-store-fixed': native, 'user-assets-fixed': bool(fixed)})
    groups['prepare-postgres'] = contracts or auth or bool(selected) or bool(fixed)
    return {'groups': groups, 'browser': [e for e in inventory['browser'] if e['name'] in selected],
            'fixed': fixed, 'auth': list(range(1, 9)) if auth else [], 'reasons': reasons}


def make_plan(event, base, head, inventory, loader=web_sources, tested=None):
    tested = head if tested is None else tested
    if not SHA.fullmatch(tested):
        raise ValueError('invalid tested revision')
    if event not in {'pull_request', 'push', 'merge_group', 'workflow_dispatch'}:
        raise ValueError('unsupported CI event')
    if not SHA.fullmatch(head):
        raise ValueError('invalid event head')
    merge, paths = '', []
    snapshots = []
    if event == 'pull_request':
        merge, paths = pr_diff(base, head)
        snapshots = [loader(revision) for revision in dict.fromkeys([merge, head, base, tested])]
        if len(snapshots) == 1:
            raise ValueError('empty PR revision range')
    plan = select(paths, inventory, snapshots, full=event != 'pull_request')
    plan.update({'version': 1, 'event': event, 'base': base, 'head': head, 'tested': tested,
                 'mergeBase': merge, 'paths': paths,
                 'inventorySHA256': hashlib.sha256(json.dumps(inventory, sort_keys=True, separators=(',', ':')).encode()).hexdigest()})
    return plan


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--check-current', action='store_true')
    parser.add_argument('--inventory', default='scripts/ci_inventory.json')
    args = parser.parse_args()
    inventory = inventory_checked(parse_json(Path(args.inventory).read_text()))
    if args.check_current:
        revision = git('rev-parse', 'HEAD').decode().strip()
        sources = web_sources(revision)
        actual_graph, uncertain = dependency_graph(sources)
        if 'web/src/computer-asset-export-v3-fixtures.ts' not in test_only_dependencies(actual_graph, uncertain, inventory):
            raise ValueError('current test-helper proof drift')
        cases = {
            'web/src/MessagesWorkspace.tsx': {'messages'},
            'web/e2e/messages-bulk.spec.ts': {'messages'},
            'web/src/ComputerAssetsV3Workspace.tsx': set(inventory['frontend_roots']['web/src/ComputerAssetsV3Workspace.tsx']),
            'web/src/UserAssetExportV3.tsx': set(inventory['frontend_roots']['web/src/UserAssetsV3Workspace.tsx']),
            'web/src/TaskWorkspace.tsx': set(inventory['frontend_roots']['web/src/TaskWorkspace.tsx']),
            'web/src/computer-asset-export-v3-api.ts': (
                set(inventory['frontend_roots']['web/src/ComputerAssetsV3Workspace.tsx'])
                | (set(inventory['frontend_roots']['web/src/TaskWorkspace.tsx'])
                   if 'web/src/computer-asset-export-v3-api.ts' in closure(actual_graph, ['web/src/TaskWorkspace.tsx']) else set())
            ),
        }
        for path, expected in cases.items():
            actual = select([path], inventory, [sources, sources])
            if ({e['name'] for e in actual['browser']} != expected
                    or actual['fixed'] != [name for name in inventory['fixed'] if name in expected]
                    or actual['groups']['native-no-store'] or actual['groups']['native-no-store-fixed']):
                raise ValueError('current dependency selection drift: ' + path)
        print('Actual TypeScript dependency graph: reviewed common modules selected precisely')
        return
    plan = make_plan(os.environ['GITHUB_EVENT_NAME'], os.environ['ADTR_EVENT_BASE_SHA'],
                     os.environ['ADTR_EVENT_HEAD_SHA'], inventory, tested=os.environ['GITHUB_SHA'])
    encoded = json.dumps(plan, sort_keys=True, separators=(',', ':'))
    if len(encoded.encode()) > 200000:
        raise ValueError('selection plan too large')
    with open(os.environ['GITHUB_OUTPUT'], 'a') as out:
        out.write('plan=' + encoded + '\n')
        for name, enabled in plan['groups'].items():
            out.write(name.replace('-', '_') + '=' + str(enabled).lower() + '\n')
        # Job-level if is evaluated before matrix expansion. Empty arrays are
        # retained as empty, never padded with a fake successful test.
        out.write('browser_matrix=' + json.dumps({'include': [{k:v for k,v in e.items() if k != 'specs'} for e in plan['browser']]}) + '\n')
        out.write('fixed_matrix=' + json.dumps({'suite': plan['fixed']}) + '\n')
    with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as out:
        out.write('## CI selection\n')
        for reason in plan['reasons']:
            out.write('- ' + reason.replace('<', '&lt;').replace('>', '&gt;') + '\n')
        out.write('\nSelected browser suites: ' + ', '.join(e['name'] for e in plan['browser']) + '\n')
        out.write('Unselected browser suites (not run): ' + ', '.join(e['name'] for e in inventory['browser'] if e not in plan['browser']) + '\n')


if __name__ == '__main__':
    main()
