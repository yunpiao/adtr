"""Kind-specific offline reconstruction of first-checkpoint running cancellation.

Unit specimens are not acceptance evidence. Shared user helpers are pure and
are imported without replacing or mutating any of their module globals.
"""
import argparse
from datetime import datetime
from pathlib import Path
import json
import validate_group_asset_export_v3_evidence as base
import validate_group_asset_export_v3_terminal_evidence as terminal
from validate_group_asset_export_v3_evidence import require, exact, sha, strict_json, source_rows, no_secrets
from validate_user_asset_export_v3_running_cancel_evidence import (
    integer, number, digest, screenshot, worker_proof, sessions_proof,
    validate_crop_png, QUERY_HASHES,
)

SUITE = 'group-asset-export-v3-running-cancel'
LIVE_KEYS = (terminal.DB_KEYS - {'leaseOwner'}) | {'leaseOwnerSHA256', 'fencingToken', 'sourceSHA256', 'observationID', 'databaseClock', 'leaseUntilEpoch'}


def response(value, case, state, detail=False):
    require(value['taskID'] == case['create']['taskID'] and value['request'] == case['request'], 'response immutable identity/request')
    task = value['task']
    require(task['taskUUID'] == case['create']['taskID'] and task['taskName'] == 'group_assets.v3.export' and task['state'] == state and task['error'] == ('cancelled' if state == 'cancelled' else ''), 'response task state')
    if detail:
        require(value.get('downloadReady') is False and value.get('downloadStatus') == ('cancelled' if state == 'cancelled' else 'pending') and value.get('artifact') is None, 'non-downloadable detail')
    require(value['task']['attempt'] == (0 if state == 'queued' else 1) and type(value['task']['attempt']) is int, 'API execution attempt')
    require(value['task']['progress'] == (0 if state == 'queued' else 10) and type(value['task']['progress']) is int, 'API checkpoint progress')


def database(value, case, actor, state):
    live = state in ('running', 'cancel_requested')
    exact(value, LIVE_KEYS if live else terminal.DB_KEYS, 'running cancellation database fields')
    require(value['taskID'] == case['create']['taskID'] and value['actorId'] == str(actor) and value['idempotencyKey'] == case['idempotencyKey'] and value['kind'] == 'group_assets.v3.export', 'durable owner/task/request')
    require(all(integer(value[k]) for k in ('attempt','maxAttempts','progress','resultVersion','snapshotCount','manifestCount','chunkCount')), 'integer durable task facts')
    require(value['state'] == state and value['error'] == ('cancelled' if state == 'cancelled' else '') and value['attempt'] == (0 if state == 'queued' else 1) and value['maxAttempts'] == 3, 'durable state/error/execution attempt')
    require(value['progress'] == (0 if state == 'queued' else 10) and value['snapshotCount'] == int(live) and value['manifestCount'] == value['chunkCount'] == 0 and value['result'] == {}, 'checkpoint or all-fence artifact cleanup')
    if live:
        require(digest(value['leaseOwnerSHA256']) and integer(value['fencingToken'], 1), 'live owned fencing token')
        require(number(value['databaseClock']) and number(value['leaseUntilEpoch']) and value['leaseUntilEpoch'] - value['databaseClock'] > 10, 'real database clock live lease')
        require(type(value['leaseUntil']) is str and abs(datetime.fromisoformat(value['leaseUntil'].replace('Z','+00:00')).timestamp() - value['leaseUntilEpoch']) < .001, 'stored lease timestamp consistency')
    else:
        require(value['leaseOwner'] == '' and value['leaseUntil'] is None, 'cleared terminal/queued lease')
    actions = ['submitted'] if state == 'queued' else ['submitted','leased','started','progress'] + ([] if state == 'running' else ['cancel_requested']) + (['finished'] if state == 'cancelled' else [])
    events = value['events']
    require(type(events) is list and [e['action'] for e in events] == actions, 'exact actual lifecycle with no recovery/race/re-execution')
    start_version = events[0]['resultVersion']
    require(integer(start_version), 'submitted event version')
    for i,e in enumerate(events):
        exact(e,['action','state','attempt','resultVersion'],'event fields')
        expected_state = 'queued' if i < 2 else 'running' if i < 4 else 'cancel_requested' if i == 4 else 'cancelled'
        require(e['state'] == expected_state and integer(e['attempt']) and e['attempt'] == int(i >= 2), 'event states/attempts')
        require(integer(e['resultVersion']) and e['resultVersion'] == start_version + max(0,i-1), 'independently reconstructed event versions')
    require(value['resultVersion'] == events[-1]['resultVersion'], 'durable last event version')


def validate(data, read_artifact, provenance):
    exact(data,['format','suite','provenance','source','actorId','sensitiveBudget','worker','cases'],'running cancel envelope')
    require(type(data['format']) is int and data['format'] == 1 and data['suite'] == 'runningCancel','isolated versioned running suite')
    require(data['provenance'] == provenance,'trusted current commit/tree/run/source provenance')
    no_secrets(data)
    before = {'owner':7,'producer':9,'observations':1,'directoryUses':1,'exportTasks':0}
    require(data['sensitiveBudget'] == {'before':before,'after':{**before,'owner':9,'exportTasks':1}} and all(type(n) is int for point in data['sensitiveBudget'].values() for n in point.values()),'fixed sensitive budget without recollection')
    rows = source_rows(data['source'])
    require(len(rows) == 23 and integer(data['actorId'],1) and str(data['actorId']) != data['source']['actorId'],'real source and independent owner')
    require(type(data['cases']) is list and len(data['cases']) == 1,'exact one running cancellation')
    c = data['cases'][0]
    exact(c,terminal.COMMON_CASE | {'queued','running','pending','postResume','unavailableDownload','ui'},'running cancellation case')
    require(c['name'] == 'cancelled' and c['request']['screenColumns'] == terminal.SAFE_COLUMNS,'fixed safe columns')
    task_id = terminal.responses(c,data['source'],rows)
    response(c['create'], c, 'queued')
    database(c['queued'],c,data['actorId'],'queued')
    for key,state,filename in (('running','running','running-checkpoint.png'),('pending','cancel_requested','cancel-requested.png')):
        value=c[key]
        exact(value,['receipt','detail','database','screenshot'] + (['cancel'] if key == 'pending' else []),'live checkpoint evidence')
        response(value['receipt'],c,state)
        response(value['detail'],c,state,True)
        database(value['database'],c,data['actorId'],state)
        db=value['database']
        require(db['sourceSHA256'] == data['source']['sha256'] and db['observationID'] == data['source']['taskUUID'],'immutable source pinned at committed snapshot')
        screenshot(value['screenshot'],filename,task_id,state,read_artifact)
    run,pending=c['running']['database'],c['pending']['database']
    require(pending['events'][:-1] == run['events'] and run['events'][:1] == c['queued']['events'],'one continuous lifecycle')
    require(all(run[k] == pending[k] for k in ('fencingToken','leaseOwnerSHA256','leaseUntilEpoch','sourceSHA256','observationID')) and pending['databaseClock'] >= run['databaseClock'],'same live first fence through cancel request')
    cancel=c['pending']['cancel'];exact(cancel,['taskID','task'],'cancel response')
    require(cancel['taskID'] == task_id and cancel['task']['taskUUID'] == task_id and cancel['task']['taskName'] == 'group_assets.v3.export' and cancel['task']['state'] == 'cancel_requested' and type(cancel['task']['attempt']) is int and cancel['task']['attempt'] == 1 and type(cancel['task']['progress']) is int and cancel['task']['progress'] == 10 and cancel['task']['error'] == '', 'real pending cancellation HTTP response')
    for key in ('receipt','detail'):
        response(c[key],c,'cancelled',key=='detail')
    database(c['database'],c,data['actorId'],'cancelled')
    require(c['database']['events'][:-1] == pending['events'],'pending cancellation finishes without additional execution')
    terminal.unavailable(c['unavailableDownload'])
    exact(c['ui'],['downloadButtons','downloadEvents'],'download UI facts')
    require(all(type(v) is int and v == 0 for v in c['ui'].values()),'no download buttons or actual downloads')
    screenshot(c['screenshot'],'cancelled-terminal.png',task_id,'cancelled',read_artifact)
    post=c['postResume'];exact(post,['receipt','detail','database','unavailableDownload'],'post-resume facts')
    for key in ('receipt','detail'):
        response(post[key],c,'cancelled',key=='detail')
    require(post['database'] == c['database'],'unchanged after two same-worker queue cycles')
    terminal.unavailable(post['unavailableDownload'])
    worker_proof(data['worker'],c)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('evidence',type=Path,nargs='?')
    parser.add_argument('--repo',type=Path,default=Path(__file__).resolve().parents[1])
    parser.add_argument('--capture-provenance',action='store_true')
    parser.add_argument('--provenance',type=Path)
    args=parser.parse_args()
    if args.capture_provenance:
        print(json.dumps(base.checkout_provenance(args.repo),sort_keys=True));return
    require(args.evidence is not None and args.provenance is not None,'evidence and independent provenance required')
    trusted=strict_json(args.provenance.read_bytes())
    require(trusted == base.checkout_provenance(args.repo,trusted['runID'],trusted['runAttempt']),'trusted clean current checkout/run')
    def artifact(filename):
        path=args.evidence.parent/filename
        require(path.name == filename and path.is_file() and not path.is_symlink(),'regular safe artifact')
        return path.read_bytes()
    validate(strict_json(args.evidence.read_bytes()),artifact,trusted)
    print(SUITE+' evidence validated')

if __name__ == '__main__':
    main()
