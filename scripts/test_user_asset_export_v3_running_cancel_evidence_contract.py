"""Offline corruption specimens only; no browser/PG acceptance evidence."""
import copy
from datetime import datetime, timezone
import unittest
import validate_user_asset_export_v3_running_cancel_evidence as v
import test_user_asset_export_v3_terminal_evidence_contract as old


def specimen():
    data, files = old.specimen(False)
    data['suite']='runningCancel'
    c=data['cases'][0]
    c.pop('cancel')
    request, task_id = c['request'], c['create']['taskID']
    for state,key in [('running','running'),('cancel_requested','pending')]:
        db=copy.deepcopy(c['queued'])
        db.update(state=state,attempt=1,progress=10,snapshotCount=1,resultVersion=2 if state=='running' else 3,
                  fencingToken=1,sourceSHA256=data['source']['sha256'],observationID=data['source']['taskUUID'],
                  databaseClock=2000 if state=='running' else 2001,leaseUntilEpoch=2029,
                  leaseUntil=datetime.fromtimestamp(2029,timezone.utc).isoformat(),leaseOwnerSHA256='4'*64)
        db.pop('leaseOwner')
        actions=['submitted','leased','started','progress']+(['cancel_requested'] if state=='cancel_requested' else [])
        db['events']=[{'action':a,'state':'queued' if i<2 else 'running' if i<4 else 'cancel_requested','attempt':int(i>=2),'resultVersion':max(0,i-1)} for i,a in enumerate(actions)]
        receipt=old.response(task_id,request,state)
        receipt['task'].update(attempt=1,progress=10)
        detail={**copy.deepcopy(receipt),'downloadReady':False,'downloadStatus':'pending'}
        filename='running-checkpoint.png' if state=='running' else 'cancel-requested.png'
        files[filename]=files['cancelled-terminal.png']
        c[key]={'receipt':receipt,'detail':detail,'database':db,'screenshot':{'filename':filename,'sha256':v.sha(files[filename]),'inputsExcluded':True,'taskID':task_id,'state':state}}
    c['pending']['cancel']={'taskID':task_id,'task':copy.deepcopy(c['pending']['receipt']['task'])}
    c['database'].update(attempt=1,progress=10,resultVersion=4,events=copy.deepcopy(c['pending']['database']['events'])+[{'action':'finished','state':'cancelled','attempt':1,'resultVersion':4}])
    for key in ('receipt','detail'):
        c[key]['task'].update(attempt=1,progress=10)
    c['postResume']={k:copy.deepcopy(c[k]) for k in ('receipt','detail','database','unavailableDownload')}
    c['screenshot'].update(inputsExcluded=True,taskID=task_id,state='cancelled')
    pause=data['worker']['pause'];pause['workerIDSHA256']='5'*64;pause.pop('workerID')
    cycles=data['worker'].pop('resume');cycles['workerIDSHA256']='5'*64;cycles.pop('workerID')
    cycles.update(queueSuccessAfterFirst='2003.000000',queueSuccessAfter='2004.000000')
    lock={'type':'transactionid','mode':'ShareLock','granted':False,'relation':None,'classid':None,'objid':None,'objsubid':None}
    session={'pid':300,'transaction':True,'state':'active','waitType':'Lock','blockers':[200],'locks':[lock],'queryKind':'activity','querySHA256':v.QUERY_HASHES['activity']}
    cp={k:c['running']['database'][k] for k in ('taskID','kind','state','attempt','progress','snapshotCount','manifestCount','chunkCount','fencingToken','leaseOwnerSHA256')}
    cp.update(workerIDSHA256='5'*64,applicationNameSHA256=pause['applicationNameSHA256'],leaseRemainingSeconds=30,databaseClock=1999,leaseUntilEpoch=2029,
              allThreadsStopped=True,threadCount=6,ownedSessionCount=1,allowedActivityTransactions=1,unsafeTransactions=0,blockedOwnedBackends=1,
              activityLockReleased=True,observerStartedBeforeResume=True,checkpointCaught=True,observerStartedMonotonic=90,resumedForCheckpointMonotonic=100,
              checkpointObservedMonotonic=100.5,stopIssuedMonotonic=100.6,stopStartedMonotonic=100.6,barrierBackendPID=200,sessions=[session])
    data['worker'].update(checkpoint=cp,cycles=cycles,resume={'resumed':True,'runningStoppedSeconds':3,'watchdogExpired':False,'stopStartedMonotonic':100.6,'resumedMonotonic':103.6})
    return data,files


class RunningEvidenceTests(unittest.TestCase):
    def check(self,data,files):
        v.validate(data,files.__getitem__,{'fixtureOnly':True})

    def test_positive_independent_specimen(self):
        self.check(*specimen())

    def test_reject_corrupted_checkpoint_lease_and_stop(self):
        changes=[{'attempt':0},{'snapshotCount':0},{'progress':9},{'fencingToken':2},{'leaseRemainingSeconds':20},
                 {'databaseClock':2005},{'leaseUntilEpoch':2050},{'leaseUntilEpoch':2030,'leaseRemainingSeconds':31},{'databaseClock':2000.5,'leaseRemainingSeconds':28.5},{'stopIssuedMonotonic':101.6,'stopStartedMonotonic':101.6},{'allThreadsStopped':False},{'unsafeTransactions':1},
                 {'activityLockReleased':False},{'observerStartedMonotonic':101},{'stopIssuedMonotonic':103},
                 {'ownedSessionCount':2},{'blockedOwnedBackends':0},{'allowedActivityTransactions':0},{'checkpointCaught':False},
                 {'workerIDSHA256':'0'*64},{'applicationNameSHA256':'0'*64},{'threadCount':True}]
        for change in changes:
            with self.subTest(change=change):
                data,files=specimen();data['worker']['checkpoint'].update(change)
                with self.assertRaises(ValueError):self.check(data,files)
        for change in [{'runningStoppedSeconds':10},{'resumedMonotonic':110.6},{'stopStartedMonotonic':100},{'watchdogExpired':True},{'resumed':False}]:
            data,files=specimen();data['worker']['resume'].update(change)
            with self.assertRaises(ValueError):self.check(data,files)

    def test_transitive_four_slot_wait_graph(self):
        data,files=specimen();cp=data['worker']['checkpoint'];first=cp['sessions'][0]
        for i in range(1,4):
            session=copy.deepcopy(first);session.update(pid=300+i,blockers=[299+i]);cp['sessions'].append(session)
        cp.update(ownedSessionCount=4,allowedActivityTransactions=4,blockedOwnedBackends=4)
        self.check(data,files)
        for blockers in ([999],[303],[200,999]):
            bad=copy.deepcopy(data);bad['worker']['checkpoint']['sessions'][0]['blockers']=blockers
            with self.assertRaises(ValueError):self.check(bad,files)

    def test_task_authorization_unknown_locks_and_queries_fail(self):
        for change in [{'type':'relation','relation':'adtr.tasks','mode':'RowShareLock','granted':True},
                       {'type':'relation','relation':'adtr.auth_users','mode':'AccessShareLock','granted':True},
                       {'type':'advisory','classid':'0','objid':'734192802','objsubid':1,'mode':'ShareLock','granted':True},
                       {'type':'advisory','classid':'0','objid':'734192801','objsubid':1,'mode':'ExclusiveLock','granted':True},
                       {'type':'unknown'}]:
            data,files=specimen();data['worker']['checkpoint']['sessions'][0]['locks'][0].update(change)
            with self.assertRaises(ValueError):self.check(data,files)
        for change in [{'querySHA256':'0'*64},{'queryKind':'idle'},{'transaction':False},{'waitType':None},{'blockers':[]}]:
            data,files=specimen();data['worker']['checkpoint']['sessions'][0].update(change)
            with self.assertRaises(ValueError):self.check(data,files)

    def test_lifecycle_source_and_artifact_corruptions(self):
        mutations=[lambda d:d.update(format=True),lambda d:d.update(provenance={'wrong':True}),
          lambda d:d['source'].update(sha256='0'*64),lambda d:d['sensitiveBudget']['after'].update(owner=10),
          lambda d:d['cases'][0]['running']['database'].update(sourceSHA256='0'*64),
          lambda d:d['cases'][0]['pending']['database'].update(observationID='wrong'),
          lambda d:d['cases'][0]['pending']['database'].update(fencingToken=2),
          lambda d:d['cases'][0]['pending']['database'].update(leaseUntilEpoch=2005),
          lambda d:d['cases'][0]['pending']['cancel']['task'].update(state='cancelled'),
          lambda d:d['cases'][0]['database'].update(attempt=0),lambda d:d['cases'][0]['database'].update(snapshotCount=1),
          lambda d:d['cases'][0]['database'].update(chunkCount=1),lambda d:d['cases'][0]['database'].update(manifestCount=1),
          lambda d:d['cases'][0]['database'].update(leaseOwner='owned'),lambda d:d['cases'][0]['database'].update(result={'partial':True}),
          lambda d:d['cases'][0]['database']['events'][-1].update(action='lease_recovered'),
          lambda d:d['cases'][0]['postResume']['database'].update(attempt=2),
          lambda d:d['cases'][0]['ui'].update(downloadEvents=1),lambda d:d['cases'][0]['unavailableDownload'].update(status=200),
          lambda d:d['cases'][0]['unavailableDownload'].update(cacheControl='public'),
          lambda d:d['cases'][0]['running']['screenshot'].update(inputsExcluded=False),
          lambda d:d['cases'][0]['pending']['screenshot'].update(filename='../proof.png'),
          lambda d:d['cases'][0]['screenshot'].update(taskID='wrong'),
          lambda d:d['worker']['cycles'].update(queueSuccessAfterFirst='1999.500000'),
          lambda d:d['worker']['cycles'].update(queueSuccessAfter='2003.000000')]
        for i,mutate in enumerate(mutations):
            with self.subTest(i=i):
                data,files=specimen();mutate(data)
                with self.assertRaises(ValueError):self.check(data,files)
        data,files=specimen();files['running-checkpoint.png']=b'not PNG'
        with self.assertRaises(ValueError):self.check(data,files)

if __name__=='__main__':unittest.main()
