# Offline synthetic directory-change CLI (AD-F-035 / Issue #25)

This adapter makes the [pure core](simulated-directory-change-assumptions.md)
usable from stdin/stdout. It implements no real AD listener, file access,
network access, database writes, API, Worker, migration or authorization.
It does not accept a caller's snapshot as authoritative State. Every replay
starts with `directorychanges.New` and reconstructs state through `Apply`.
Output history is synthetic before/after evidence, not database/audit proof.
Product acceptance and persistence/real-AD acceptance remain open.

## Frozen v1 wire contract

Invoke `adtr-simulate directory-changes < input.json`. The command accepts exactly
one JSON document. Wrapper keys are case-sensitive lower camel case; core
Baseline/Batch/Object/Change/Fields keys are exact PascalCase (matching the core
hash schema). No unknown, duplicate (including case-folded duplicates), omitted
required field, or null input value is accepted. Names may be explicitly `""`
and Enabled may be explicitly `false`; missing/null never means known empty/false.
Numbers must decode as uint64 integers, not strings, decimals or negatives.

Both modes require `contract: "adtr.synthetic.directorychanges.cli.v1"`,
`synthetic: true`, and `mode`. The declaration is required to prevent accidental
use as a production contract; it authenticates nothing. Removing it loses that
explicit intent guard. There is no `sourceScope` field: a caller label cannot
provide tenant/domain isolation. SourceEpoch identifies a synthetic stream,
not an authenticated source, authorization grant, or LDAP cookie.

**replay** additionally requires `baseline` and `batches` (an array, possibly `[]`),
and forbids `batch`. Baseline requires SourceEpoch, Complete, Objects. Each object
requires ID, Kind, Version, Fields, Deleted. Version must be 1 and Deleted false.
Every batch requires SourceEpoch, Sequence, PreviousHash, Complete, Changes and
BatchHash. Every change requires ID, Operation, Kind, ExpectedVersion and Fields;
Fields always requires Name and Enabled. Complete must be true for baseline and
all batches, including empty batches; removing completeness checks would make
partial observations appear authoritative. Missing objects do not mean deleted.

**hash** instead requires `batch`, forbids `baseline`/`batches`, and forbids
BatchHash in its input batch. All other batch/change fields remain required.
It calls `HashBatch` and returns `result.batch` ready to insert in replay.batches.
`result.stateValidated` is always false: structural validity and hash construction
DO NOT establish previous-cursor, sequence, epoch or existing-object validity.
Only replay validates those against reconstructed state. Rejecting irrelevant
mode fields prevents silently ignoring caller-supplied state or claimed hashes.

Create requires ExpectedVersion=0, update/tombstone require the exact live version.
Update provides a full replacement Fields payload, not a patch. Tombstone requires
Fields={"Name":"","Enabled":false}; nonzero payload is rejected, not ignored.
It preserves prior scalar data in current objects and history, increments version,
and marks Deleted=true. It is not physical deletion and IDs cannot be reused.

Successful stdout is one JSON line in the existing envelope:
`simulation:true`, `contract:"adtr.offline-simulation.v1"`,
`command:"directory-changes"`, `result`. Both result modes include the CLI contract,
synthetic=true and mode. Replay adds `snapshot` (core Cursor, Objects, Records)
and `trace` (one entry per invocation: invocation, sequence, status, newRecords,
cursor). Invocation is 1-based. `applied` means a new accepted batch advanced the
cursor, including empty batches with zero records. `replayed` means an exact
previous canonical batch was accepted without advancing the current cursor or
adding records, even after later batches. Snapshot is detached from core state;
Before=null means creation. Empty core Objects/Records serialize as null; trace
is always an array. Output snapshots/history are not accepted as replay inputs.

A malformed or rejected invocation returns exit 2, generic diagnostic on stderr,
and **zero stdout**, including when its last batch fails after earlier valid ones.
All validation and JSON marshaling precede output. An actual stdout writer failure
returns exit 1; the OS may already have received bytes, so transport failure is
not claimed to be atomic. There is no durable state between processes.

## Required bounds and side effects

These fixed guards are mandatory, not tuning flags. Existing shared 4 MiB stdin
and nesting-depth 64 limits are unchanged. The adapter additionally caps replay at
64 batch invocations, including exact duplicates: core's 32 accepted-batch budget
alone cannot bound repeated Apply calls. Removing this separate cap permits
unbounded replay work/trace growth up to the shared input ceiling. Side effect:
65 otherwise valid repetitions fail whole; reduce the fixture, do not truncate.

Core bounds remain 64 lifetime object IDs (including tombstones), 32 accepted
batches, 32 changes/batch, 256 UTF-8 bytes per Name, 64 ASCII bytes per ID/epoch,
and lowercase 64-hex hashes. Their purpose is bounded memory/history/computation
and stable identity; removing them changes the finite model. Exceeding any bound
rejects the whole invocation; nothing is evicted or silently reset. Strict field
presence and key spelling prevent ambiguous hash inputs, with the side effect
that older/partial/misspelled payloads must be explicitly corrected. UTF-8 bytes
are checked before JSON decoding. After syntax validation, an adapter-local escape
scanner also rejects unpaired UTF-16 surrogate escapes: ASCII JSON bytes alone
do not prove a valid encoded Unicode scalar. Valid high/low pairs, literal or
escaped U+FFFD, and escaped-backslash text remain valid. Removing either check
lets malformed names silently normalize before hashing; rejecting malformed
fixtures rather than repairing them is the intentional side effect.
Hashes bind canonical content, not authenticity. See the core document for exact
Go JSON escaping, canonical ID ordering, and operation semantics.

## Runnable independent binary example

From the repository root with the required Go 1.27.1 toolchain:

```sh
go build -o /tmp/adtr-directory-changes-cli ./cmd/adtr-simulate
python3 - <<'PY'
import copy, json, subprocess
binary = '/tmp/adtr-directory-changes-cli'
def call(mode, **payload):
    request = dict(contract='adtr.synthetic.directorychanges.cli.v1',
                   synthetic=True, mode=mode, **payload)
    result = subprocess.run([binary, 'directory-changes'],
                            input=json.dumps(request), text=True,
                            capture_output=True)
    assert result.returncode == 0, result.stderr
    assert not result.stderr
    envelope = json.loads(result.stdout)
    assert envelope['simulation'] is True
    return envelope['result']
def reject(request):
    result = subprocess.run([binary, 'directory-changes'],
                            input=json.dumps(request), text=True,
                            capture_output=True)
    assert result.returncode == 2 and result.stdout == '' and result.stderr
baseline = dict(SourceEpoch='lab-1', Complete=True, Objects=[
    dict(ID='keep', Kind='group', Version=1,
         Fields=dict(Name='Keep', Enabled=True), Deleted=False)])
batches = []
initial = call('replay', baseline=baseline, batches=[])
assert initial['snapshot']['Cursor']['Sequence'] == 0
assert initial['snapshot']['Objects'] == baseline['Objects']
for sequence, operation, name, enabled in [
        (1, 'create', 'Created', True),
        (2, 'update', 'Updated', False),
        (3, 'tombstone', '', False)]:
    current = call('replay', baseline=baseline, batches=batches)
    candidate = dict(SourceEpoch='lab-1', Sequence=sequence,
                     PreviousHash=current['snapshot']['Cursor']['Hash'],
                     Complete=True, Changes=[dict(ID='user-1', Kind='user',
                     Operation=operation, ExpectedVersion=sequence-1,
                     Fields=dict(Name=name, Enabled=enabled))])
    prepared = call('hash', batch=candidate)
    assert prepared['stateValidated'] is False
    batches.append(prepared['batch'])
result = call('replay', baseline=baseline, batches=batches+[batches[0]])
expected = [baseline['Objects'][0], dict(ID='user-1', Kind='user', Version=3,
            Fields=dict(Name='Updated', Enabled=False), Deleted=True)]
assert result['snapshot']['Objects'] == expected
records = result['snapshot']['Records']
assert [r['Operation'] for r in records] == ['create', 'update', 'tombstone']
assert records[0]['Before'] is None
assert records[1]['Before']['Fields'] == dict(Name='Created', Enabled=True)
assert records[2]['Before']['Fields'] == dict(Name='Updated', Enabled=False)
assert records[2]['After'] == expected[1]
assert [t['status'] for t in result['trace']] == ['applied']*3+['replayed']
assert result['trace'][-1]['newRecords'] == 0
assert result['trace'][-1]['cursor']['Sequence'] == 3
conflict = copy.deepcopy(batches[0])
del conflict['BatchHash']
conflict['Changes'][0]['Fields']['Name'] = 'Changed'
conflict = call('hash', batch=conflict)['batch'] # valid hash, conflicting receipt
request = dict(contract='adtr.synthetic.directorychanges.cli.v1', synthetic=True,
               mode='replay', baseline=baseline, batches=batches+[conflict])
reject(request) # zero stdout despite three prior valid batches
request['batches'] = copy.deepcopy(batches)
del request['batches'][0]['Changes'][0]['Fields']['Enabled']
reject(request) # missing is not known false
request['batches'] = copy.deepcopy(batches)
request['batches'][1]['PreviousHash'] = '0'*64
reject(request)
print('PASS: baseline/create/update/tombstone/exact replay/conflict/invalid; failure stdout empty')
PY
```

The Python harness uses files only to launch the built binary; the command itself
reads stdin and writes stdout/stderr. The example verifies handwritten projections
and before/after values rather than deriving expected output by calling the core.
Focused/full CLI tests, race and vet are adapter correctness evidence only; no
PostgreSQL, browser, real AD, persistence, deployment or product acceptance claim.

## Local verification (2026-11-03)

Base: reviewed core commit `17a5a43babc77490b43b28f94f054fcc3a41d658`;
Go `go1.27.1 linux/amd64`. Baseline full CLI tests passed before adapter edits.
Final focused tests (`go test -count=1 ./cmd/adtr-simulate -run Directory -v`),
full CLI/core race (`go test -race -count=1 ./cmd/adtr-simulate ./internal/directorychanges`),
and corresponding `go vet` passed. Full CLI package statement coverage was 82.8%;
new adapter helpers were 100%, its dispatcher 98.0%. Coverage is not acceptance.
The built-binary example above passed with actual pipes, independent expected
objects/history/trace, and zero-output conflict/invalid failures. `git diff --check`
passed. Tests additionally remove and null every required field in both modes,
exercise exact-key/unknown/duplicate rejection, invalid UTF-8/types, complete empty
batches, 64/65 duplicate invocations, 32/33 accepted batches, oversized objects,
changes, names, and the shared 4 MiB stdin cap. Initial test-fixture mutation
patterns missed three final JSON fields; those test patterns were corrected and
the final suite rerun. No production implementation defect was inferred from those
ineffective test mutations. Full repository make check, database/browser tests
and remote CI were not run for this uncommitted adapter candidate.


### Independent review correction: escaped surrogates

The first independent review reproduced a real adapter defect in the built binary:
ASCII JSON `Fields.Name:"\ud800"` (likewise a lone low surrogate) exited 0 and
silently emitted U+FFFD instead of rejecting it. The raw-byte UTF-8 check did not
cover UTF-16 escapes inside JSON. This failure evidence is retained here; the
original focused passes did not establish this Unicode boundary.

The adapter now scans JSON string escapes after syntax validation and rejects
unpaired high/low surrogates without changing the shared parser or core.
`TestDirectoryChangesSurrogatesBinary` builds the actual command and exercises
both the in-process runner and subprocess pipes: lone high/low, high+high,
high+ordinary scalar, high+text, high+escaped-backslash and reversed pairs fail
with exit 2 and zero stdout; valid paired emoji, literal/escaped U+FFFD,
escaped-backslash text and escaped quotes survive exactly. CLI usage now lists
the new command. Final focused/full CLI+core race/vet and documented binary
workflow were rerun after this fix; no publication or integration is claimed.

## Final repository-level verification

After the independent surrogate correction and final source freeze, full repository
Go race/vet and the canonical Python contract aggregate passed (1202 tests,
150.886 seconds). Requirements integrity still verifies209 records. The adapter
and documentation remain offline only; no database/browser runtime acceptance,
remote CI or merge is claimed by these local results. Full make check was not
invoked. Exact-head CI and normal dependency/main/postmerge gates remain pending.
