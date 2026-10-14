"""Independent offline reconstruction of real running-cancel evidence.

Unit specimens are not acceptance evidence. The gate covers the first committed
snapshot/progress checkpoint, not a chunk-stage cancellation or fencing race.
"""
import argparse
from datetime import datetime
from decimal import Decimal
import math
from pathlib import Path
import re
import struct
import zlib
import json
import validate_user_asset_export_v3_evidence as base
import validate_user_asset_export_v3_terminal_evidence as terminal
from validate_user_asset_export_v3_evidence import require, exact, sha, strict_json, source_rows, no_secrets

SUITE = 'user-asset-export-v3-running-cancel'
LIVE_KEYS = (terminal.DB_KEYS - {'leaseOwner'}) | {'leaseOwnerSHA256', 'fencingToken', 'sourceSHA256', 'observationID', 'databaseClock', 'leaseUntilEpoch'}
CHECKPOINT_KEYS = {'taskID','kind','state','attempt','progress','snapshotCount','manifestCount','chunkCount','fencingToken','leaseOwnerSHA256','workerIDSHA256','applicationNameSHA256','leaseRemainingSeconds','databaseClock','leaseUntilEpoch','allThreadsStopped','threadCount','ownedSessionCount','allowedActivityTransactions','unsafeTransactions','blockedOwnedBackends','activityLockReleased','observerStartedBeforeResume','checkpointCaught','observerStartedMonotonic','resumedForCheckpointMonotonic','checkpointObservedMonotonic','stopIssuedMonotonic','stopStartedMonotonic','barrierBackendPID','sessions'}
ACTIVITY_SQL = """WITH observed AS (SELECT clock_timestamp() AS at)
INSERT INTO adtr.system_worker_activity(tenant_id,worker_id,cycle,status,error_code,last_activity_at,last_success_at)
SELECT $1,$2,$3,$4,$5,at,CASE WHEN $4='success' THEN at ELSE NULL END FROM observed
ON CONFLICT(tenant_id,worker_id,cycle) DO UPDATE SET status=EXCLUDED.status,error_code=EXCLUDED.error_code,
 last_activity_at=EXCLUDED.last_activity_at,
 last_success_at=CASE WHEN EXCLUDED.status='success' THEN EXCLUDED.last_activity_at ELSE adtr.system_worker_activity.last_success_at END"""
QUERY_HASHES = {k: sha(' '.join(v.split()).encode()) for k, v in {
    'activity': ACTIVITY_SQL, 'schema': 'SELECT version FROM adtr.schema_version WHERE singleton=true FOR SHARE',
    'schemaGate': 'SELECT pg_advisory_xact_lock_shared($1::bigint)'}.items()}
RELATIONS = {'adtr.schema_version','adtr.schema_version_pkey','adtr.system_worker_activity','adtr.system_worker_activity_pkey','adtr.system_worker_activity_recent'}


def integer(value, minimum=0):
    return type(value) is int and value >= minimum


def number(value):
    return type(value) in (int, float) and math.isfinite(value)


def digest(value):
    return type(value) is str and re.fullmatch('[0-9a-f]{64}', value) is not None


def validate_crop_png(raw, width, height):
    """Strict RGB/RGBA CSS-pixel crop parser; legacy viewport gate is untouched."""
    require(raw.startswith(b'\x89PNG\r\n\x1a\n') and len(raw) <= 10 << 20, 'crop PNG signature/size')
    offset, kinds, compressed, channels = 8, [], bytearray(), None
    while offset < len(raw):
        require(offset + 12 <= len(raw), 'crop PNG truncated chunk')
        size = int.from_bytes(raw[offset:offset+4], 'big')
        kind = raw[offset+4:offset+8]
        end = offset + 12 + size
        require(end <= len(raw), 'crop PNG chunk bounds')
        require(zlib.crc32(raw[offset+4:end-4]) == int.from_bytes(raw[end-4:end], 'big'), 'crop PNG CRC')
        body = raw[offset+8:end-4]
        if not kinds:
            require(kind == b'IHDR' and size == 13, 'crop PNG header')
            w,h,depth,color,compression,filter_method,interlace = struct.unpack('>IIBBBBB',body)
            require((w,h) == (width,height), 'PNG pixels match exact captured CSS rectangle')
            require(depth == 8 and color in (2,6) and compression == filter_method == interlace == 0, 'bounded noninterlaced truecolor crop')
            channels = 3 if color == 2 else 4
        elif kind == b'IDAT':
            require(kinds[-1] in (b'IHDR',b'IDAT',b'sRGB',b'gAMA',b'cHRM',b'pHYs'), 'contiguous crop PNG image stream')
            compressed.extend(body)
        elif kind == b'IEND':
            require(size == 0 and end == len(raw), 'final empty PNG terminator')
        else:
            require(kind in (b'sRGB',b'gAMA',b'cHRM',b'pHYs') and b'IDAT' not in kinds and kind not in kinds, 'only bounded color/density metadata')
            require(size == {b'sRGB':1,b'gAMA':4,b'cHRM':32,b'pHYs':9}[kind], 'PNG ancillary size')
        kinds.append(kind)
        offset=end
    require(kinds[-1:] == [b'IEND'] and b'IDAT' in kinds and kinds.count(b'IHDR') == kinds.count(b'IEND') == 1, 'complete crop PNG structure')
    expected = height * (1 + width * channels)
    decoder = zlib.decompressobj()
    try:
        pixels = decoder.decompress(bytes(compressed), expected + 1)
    except zlib.error as error:
        raise ValueError('invalid crop compressed image') from error
    require(len(pixels) == expected and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail, 'exact bounded decoded crop scanline bytes')
    stride = 1 + width * channels
    require(all(pixels[offset] <= 4 for offset in range(0,len(pixels),stride)), 'valid PNG scanline filters')


def screenshot(value, filename, task_id, state, read_artifact):
    exact(value, ['filename','sha256','inputsExcluded','taskID','state','crop','inputRects'], 'safe screenshot fields')
    require(value['filename'] == filename and value['taskID'] == task_id and value['state'] == state and value['inputsExcluded'] is True, 'genuine state screenshot identity and excluded proof inputs')
    crop = value['crop']
    exact(crop,['x','y','width','height','viewportWidth','viewportHeight'],'captured CSS geometry')
    require(all(integer(n) for n in crop.values()) and crop['width'] > 0 and crop['height'] > 0, 'integer positive captured crop')
    require(crop['viewportWidth'] == 1440 and crop['viewportHeight'] == 900 and crop['x'] + crop['width'] <= 1440 and crop['y'] + crop['height'] <= 900, 'crop entirely inside actual fixed viewport')
    require(type(value['inputRects']) is list, 'captured input geometry list')
    for box in value['inputRects']:
        exact(box,['x','y','width','height'],'input geometry only, no values')
        require(all(number(n) for n in box.values()) and box['width'] >= 0 and box['height'] >= 0, 'finite input rectangle')
        intersects = (box['x'] < crop['x']+crop['width'] and box['x']+box['width'] > crop['x'] and
                      box['y'] < crop['y']+crop['height'] and box['y']+box['height'] > crop['y'])
        require(not intersects, 'all captured proof-input rectangles outside screenshot')
    raw = read_artifact(filename)
    require(sha(raw) == value['sha256'], 'screenshot digest')
    validate_crop_png(raw,crop['width'],crop['height'])


def response(value, case, state, detail=False):
    require(value['taskID'] == case['create']['taskID'] and value['request'] == case['request'], 'response immutable identity/request')
    task = value['task']
    require(task['taskUUID'] == case['create']['taskID'] and task['taskName'] == 'user_assets.v3.export' and task['state'] == state and task['error'] == ('cancelled' if state == 'cancelled' else ''), 'response task state')
    if detail:
        require(value.get('downloadReady') is False and value.get('downloadStatus') == ('cancelled' if state == 'cancelled' else 'pending') and value.get('artifact') is None, 'non-downloadable detail')
    require(value['task']['attempt'] == (0 if state == 'queued' else 1) and type(value['task']['attempt']) is int, 'API execution attempt')
    require(value['task']['progress'] == (0 if state == 'queued' else 10) and type(value['task']['progress']) is int, 'API checkpoint progress')


def database(value, case, actor, state):
    live = state in ('running', 'cancel_requested')
    exact(value, LIVE_KEYS if live else terminal.DB_KEYS, 'running cancellation database fields')
    require(value['taskID'] == case['create']['taskID'] and value['actorId'] == str(actor) and value['idempotencyKey'] == case['idempotencyKey'] and value['kind'] == 'user_assets.v3.export', 'durable owner/task/request')
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


def sessions_proof(checkpoint):
    sessions = checkpoint['sessions']
    require(type(sessions) is list and len(sessions) == checkpoint['ownedSessionCount'] and len(sessions) > 0, 'all owned sessions represented')
    pids, blocked, transactions = set(), 0, 0
    barrier = checkpoint['barrierBackendPID']
    require(integer(barrier, 1), 'barrier backend identity')
    reachable = {barrier}
    for _ in sessions:
        for candidate in sessions:
            if (candidate['blockers'] and set(candidate['blockers']) <= reachable and
                    candidate['queryKind'] == 'activity' and candidate['querySHA256'] == QUERY_HASHES['activity'] and
                    candidate['transaction'] is True and candidate['waitType'] == 'Lock'):
                reachable.add(candidate['pid'])
    for s in sessions:
        exact(s,['pid','transaction','state','waitType','blockers','locks','queryKind','querySHA256'],'session fields')
        require(integer(s['pid'],1) and s['pid'] not in pids and s['pid'] != barrier, 'distinct owned backend identity')
        pids.add(s['pid'])
        require(type(s['transaction']) is bool and type(s['blockers']) is list and all(integer(p,1) for p in s['blockers']), 'transaction and blocker facts')
        require(digest(s['querySHA256']) and type(s['locks']) is list, 'query digest and locks')
        kind = s['queryKind']
        if s['transaction']:
            transactions += 1
            require(kind in QUERY_HASHES and s['querySHA256'] == QUERY_HASHES[kind], 'exact allowlisted production activity/schema SQL')
        else:
            require(s['state'] == 'idle' and s['locks'] == [], 'idle sessions hold no transaction locks')
        for lock in s['locks']:
            exact(lock,['type','mode','granted','relation','classid','objid','objsubid'],'lock fields')
            require(type(lock['granted']) is bool, 'lock granted boolean')
            typ, mode, granted = lock['type'], lock['mode'], lock['granted']
            if typ == 'relation':
                require(lock['relation'] in RELATIONS and granted and mode in ('AccessShareLock','RowShareLock','RowExclusiveLock'),'only schema/activity relation locks')
            elif typ == 'advisory':
                require(lock['classid'] == '0' and lock['objid'] == '734192801' and type(lock['objsubid']) is int and lock['objsubid'] == 1 and mode == 'ShareLock' and granted,'only shared schema migration gate')
            elif typ == 'virtualxid':
                require(mode == 'ExclusiveLock' and granted,'virtual transaction lock')
            elif typ == 'transactionid':
                require((mode == 'ExclusiveLock' and granted) or (kind == 'activity' and mode == 'ShareLock' and not granted and s['pid'] in reachable),'activity transaction lock')
            elif typ == 'tuple':
                require(kind == 'activity' and lock['relation'] == 'adtr.system_worker_activity' and mode in ('ExclusiveLock','AccessExclusiveLock') and s['pid'] in reachable,'activity tuple lock only')
            else:
                raise ValueError('unexpected owned backend lock')
        if s['blockers']:
            require(s['pid'] in reachable, 'unowned or cyclic barrier blockers')
            require(kind == 'activity' and s['transaction'] and s['waitType'] == 'Lock', 'actual blocked owned activity callback')
            blocked += 1
    require(blocked > 0 and checkpoint['blockedOwnedBackends'] == blocked and checkpoint['allowedActivityTransactions'] == transactions and checkpoint['unsafeTransactions'] == 0, 'reconstructed blocked/transaction counts')


def worker_proof(worker, case):
    exact(worker,['pause','checkpoint','resume','cycles'],'owned worker evidence')
    pause, cp, resume, cycles = (worker[k] for k in ('pause','checkpoint','resume','cycles'))
    exact(pause,['pid','parentPID','starttime','binarySHA256','nonceSHA256','applicationNameSHA256','allThreadsStopped','databaseIdentityVerified','threadCount','connections','activeTransactions','workerIDSHA256','queueSuccessBefore'],'strict initial queued pause')
    exact(cycles,['sameIdentity','allThreadsRunning','readyStatus','workerIDSHA256','queueSuccessAfterFirst','queueSuccessAfter'],'actual resumed queue cycles')
    require(all(integer(pause[k],1) for k in ('pid','parentPID','threadCount')) and pause['pid'] > 1 and pause['parentPID'] > 1 and pause['pid'] != pause['parentPID'],'owned child numeric identity')
    require(type(pause['starttime']) is str and re.fullmatch('[0-9]+',pause['starttime']),'process birth identity')
    require(all(digest(pause[k]) for k in ('binarySHA256','nonceSHA256','applicationNameSHA256','workerIDSHA256')),'owned process identity hashes')
    require(pause['allThreadsStopped'] is True and pause['databaseIdentityVerified'] is True and integer(pause['connections']) and type(pause['activeTransactions']) is int and pause['activeTransactions'] == 0,'initial queued no-transaction pause')
    require(cycles['sameIdentity'] is True and cycles['allThreadsRunning'] is True and type(cycles['readyStatus']) is int and cycles['readyStatus'] == 200 and cycles['workerIDSHA256'] == pause['workerIDSHA256'],'same actual worker resumes')
    times = [pause['queueSuccessBefore'],cycles['queueSuccessAfterFirst'],cycles['queueSuccessAfter']]
    require(all(type(t) is str and re.fullmatch('[0-9]+\\.[0-9]+',t) for t in times),'real database queue timestamps')
    require(Decimal(times[0]) < Decimal(times[1]) < Decimal(times[2]),'two actual queue success cycles')
    exact(cp,CHECKPOINT_KEYS,'checkpoint fields')
    for k in ('taskID','kind','state','attempt','progress','snapshotCount','manifestCount','chunkCount','fencingToken','leaseOwnerSHA256','leaseUntilEpoch'):
        require(cp[k] == case['running']['database'][k],'checkpoint and independent database agree: '+k)
    require(cp['workerIDSHA256'] == pause['workerIDSHA256'] and cp['applicationNameSHA256'] == pause['applicationNameSHA256'],'checkpoint same owner identity')
    require(all(integer(cp[k]) for k in ('attempt','progress','snapshotCount','manifestCount','chunkCount','fencingToken','threadCount','ownedSessionCount','allowedActivityTransactions','unsafeTransactions','blockedOwnedBackends')) and cp['threadCount'] > 0,'checkpoint integer observations')
    require(all(cp[k] is True for k in ('allThreadsStopped','activityLockReleased','observerStartedBeforeResume','checkpointCaught')),'safe captured/released checkpoint')
    require(all(number(cp[k]) for k in ('leaseRemainingSeconds','databaseClock','leaseUntilEpoch','observerStartedMonotonic','resumedForCheckpointMonotonic','checkpointObservedMonotonic','stopIssuedMonotonic','stopStartedMonotonic')),'finite database and monotonic clocks')
    require(cp['leaseRemainingSeconds'] > 20 and abs(cp['leaseUntilEpoch']-cp['databaseClock']-cp['leaseRemainingSeconds']) < .01,'independently reconstructed >20s real lease')
    require(cp['observerStartedMonotonic'] <= cp['resumedForCheckpointMonotonic'] <= cp['checkpointObservedMonotonic'] <= cp['stopIssuedMonotonic'] == cp['stopStartedMonotonic'] and cp['stopIssuedMonotonic']-cp['resumedForCheckpointMonotonic'] < 1.5,'persistent observer precedes resume and stop meets actual callback window')
    require(cp['databaseClock'] <= case['running']['database']['databaseClock'] <= case['pending']['database']['databaseClock'], 'ordered independent database observation clocks')
    sessions_proof(cp)
    exact(resume,['resumed','runningStoppedSeconds','watchdogExpired','stopStartedMonotonic','resumedMonotonic'],'running lease-safe resume')
    require(resume['resumed'] is True and resume['watchdogExpired'] is False and all(number(resume[k]) for k in ('runningStoppedSeconds','stopStartedMonotonic','resumedMonotonic')),'successful controlled resume')
    require(resume['stopStartedMonotonic'] == cp['stopStartedMonotonic'] and 0 < resume['runningStoppedSeconds'] < 10 and abs(resume['resumedMonotonic']-resume['stopStartedMonotonic']-resume['runningStoppedSeconds']) < .001,'independent running STOP duration below10s')
    require(Decimal(times[1]) > Decimal(str(case['pending']['database']['databaseClock'])),'both resumed queue cycles follow pending cancellation')


def validate(data, read_artifact, provenance):
    exact(data,['format','suite','provenance','source','actorId','sensitiveBudget','worker','cases'],'running cancel envelope')
    require(type(data['format']) is int and data['format'] == 1 and data['suite'] == 'runningCancel','isolated versioned running suite')
    require(data['provenance'] == provenance,'trusted current commit/tree/run/source provenance')
    no_secrets(data)
    before = {'owner':7,'producer':9,'observations':1,'directoryUses':1,'exportTasks':0}
    require(data['sensitiveBudget'] == {'before':before,'after':{**before,'owner':9,'exportTasks':1}} and all(type(n) is int for point in data['sensitiveBudget'].values() for n in point.values()),'fixed sensitive budget without recollection')
    rows = source_rows(data['source'])
    require(len(rows) == 12 and integer(data['actorId'],1) and str(data['actorId']) != data['source']['actorId'],'real source and independent owner')
    require(type(data['cases']) is list and len(data['cases']) == 1,'exact one running cancellation')
    c = data['cases'][0]
    exact(c,terminal.COMMON_CASE | {'queued','running','pending','postResume','unavailableDownload','ui'},'running cancellation case')
    require(c['name'] == 'cancelled' and c['request']['screenColumns'] == terminal.SAFE_COLUMNS,'fixed safe columns')
    task_id = terminal.responses(c,data['source'],rows)
    require(c['create']['task']['state'] == 'queued','task created during initial queued STOP')
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
    require(cancel['taskID'] == task_id and cancel['task']['taskUUID'] == task_id and cancel['task']['taskName'] == 'user_assets.v3.export' and cancel['task']['state'] == 'cancel_requested' and cancel['task']['attempt'] == 1,'real pending cancellation HTTP response')
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
