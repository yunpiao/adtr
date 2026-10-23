"""Exclusive suite wiring, owned-process endpoints and post-cleanup admission.

There is no import-time I/O, worker/controller, polling, signal or upload. Samples
span the full browser subprocess, including its login and fixture setup, not an
export-only interval. Only the original successful runner permits final staging.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time

SUITE = 'computer-asset-export-v3-multichunk'
MARKER = 'ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_MULTICHUNK'
LDAP_MARKER = 'ADTR_E2E_LDAP_DIRECTORY_COMPUTER_EXPORT_MULTICHUNK_V3'
PROVENANCE = Path('.e2e-diagnostics/computer-export-multichunk-provenance.json')
PRE_RESOURCES = Path('.e2e-diagnostics/computer-export-multichunk-resources-pre.json')
OUTPUT = Path('web/playwright-report/computer-export-multichunk-validated')
VALIDATOR = 'scripts/validate_computer_asset_export_v3_multichunk_evidence.py'
DELTA = 'scripts/fixtures/computer_asset_export_v3_multichunk_delta.json'
ERROR = 'multichunk_gate_rejected'


def require(condition):
    if not condition:
        raise ValueError(ERROR)


def safe_failure(function):
    """Never propagate a proc/parser/subprocess exception or its secret cause."""
    def guarded(*args, **kwargs):
        try:
            return function(*args, **kwargs)
        except Exception:
            raise ValueError(ERROR) from None
    return guarded


def restore_multichunk_source(text, path):
    """One exact current raw UTF-8 layer; never an idempotent drift eraser."""
    require(type(text) is str and '\r' not in text)
    from validate_computer_asset_export_v3_multichunk_evidence import strict_json
    raw = Path(DELTA).read_bytes()
    require(0 < len(raw) <= 256 << 10 and hashlib.sha256(raw).hexdigest() == DELTA_SHA256)
    delta = strict_json(raw, 256 << 10)
    require(type(delta) is dict and set(delta) == {'format', 'paths'} and type(delta['format']) is int and delta['format'] == 1)
    require(set(delta['paths']) == set(PREDECESSORS) and path in PREDECESSORS)
    record = delta['paths'][path]
    require(type(record) is dict and set(record) == {'baselineSHA256', 'candidateSHA256', 'replacements'})
    require(record['baselineSHA256'] == PREDECESSORS[path])
    require(hashlib.sha256(text.encode('utf-8')).hexdigest() == record['candidateSHA256'])
    require(type(record['replacements']) is list and 1 <= len(record['replacements']) <= 32)
    for change in reversed(record['replacements']):
        require(type(change) is dict and set(change) == {'old', 'new', 'offset'})
        old, new, offset = change['old'], change['new'], change['offset']
        require(type(old) is str and type(new) is str and old and new and old != new)
        require(type(offset) is int and 0 <= offset <= len(text) and text.count(new) == 1)
        require(text[offset:offset + len(new)] == new)
        text = text[:offset] + old + text[offset + len(new):]
    require(hashlib.sha256(text.encode('utf-8')).hexdigest() == PREDECESSORS[path])
    return text


@safe_failure
def configure(env, suite):
    if suite != SUITE:
        env.pop(MARKER, None)
        env.pop(LDAP_MARKER, None)
        return
    conflicts = ('GROUP_ASSETS_V3', 'V2', 'V4', 'EMPTY', 'SLOW', 'USER_ASSETS_V2',
                 'GROUP_MEMBERS_V4_PAGINATION', 'GROUP_MEMBERS_V4_REFRESH',
                 'USER_EXPORT_FAILURE_V3', 'GROUP_EXPORT_FAILURE_V3', 'COMPUTER_EXPORT_FAILURE_V3')
    require(not any(env.get('ADTR_E2E_LDAP_DIRECTORY_' + key) == 'true' for key in conflicts))
    require(not any(env.get(key) == 'true' for key in ('ADTR_E2E_USER_ASSET_EXPORT_V3',
                'ADTR_E2E_GROUP_ASSET_EXPORT_V3', 'ADTR_DIRECTORY_READ_V2_ENABLED', 'ADTR_DIRECTORY_READ_V4_ENABLED')))
    require(not env.get('ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR') and not env.get('ADTR_E2E_LDAP_CONTROL_DIR'))
    env[MARKER] = 'true'
    env[LDAP_MARKER] = 'true'
    env['ADTR_E2E_COMPUTER_ASSET_EXPORT_V3'] = 'true'
    env['ADTR_E2E_LDAP_DIRECTORY_COMPUTER_ASSETS_V3'] = 'true'


@safe_failure
def capture(repo=Path('.'), output=PROVENANCE):
    from validate_computer_asset_export_v3_multichunk_evidence import capture_provenance, encoded, safe_path
    body = encoded(capture_provenance(repo)) + b'\n'
    destination = safe_path(output)
    destination.parent.mkdir(parents=True, exist_ok=True)
    require(not destination.exists())
    with destination.open('xb') as stream:
        stream.write(body)


def parse_stat(raw, expected_pid):
    """Comm may contain spaces and parentheses. Never retain or print it."""
    require(type(raw) is bytes and 0 < len(raw) <= 65536)
    match = re.fullmatch(rb'([1-9][0-9]*) \((.*)\) ([A-Za-z]) ([^\n]+)\n?', raw, re.S)
    require(match is not None and int(match[1]) == expected_pid)
    fields = match[4].split(b' ')
    require(49 <= len(fields) <= 61 and all(re.fullmatch(rb'-?[0-9]{1,20}', value) for value in fields))
    # fields starts at Linux stat field 4; utime=14, stime=15, starttime=22.
    output = []
    for index in (10, 11, 18):
        value = fields[index]
        require(re.fullmatch(rb'0|[1-9][0-9]{0,18}', value) and int(value) <= (1 << 63) - 1)
        output.append(value.decode('ascii'))
    return {'userTicks': output[0], 'systemTicks': output[1], 'startTicks': output[2]}


def parse_status(raw):
    require(type(raw) is bytes and 0 < len(raw) <= 65536)
    result = {}
    for line in raw.splitlines():
        if line.startswith((b'VmRSS:', b'VmHWM:')):
            match = re.fullmatch(rb'(VmRSS|VmHWM):[ \t]+(0|[1-9][0-9]{0,12}) kB', line)
            require(match is not None)
            key = 'vmRSSKiB' if match[1] == b'VmRSS' else 'vmHWMKiB'
            require(key not in result and int(match[2]) <= 1 << 40)
            result[key] = int(match[2])
    require(set(result) == {'vmRSSKiB', 'vmHWMKiB'} and result['vmHWMKiB'] >= result['vmRSSKiB'])
    return result


@safe_failure
def sample(process, expected=None):
    """Only the runner's actual Popen establishes ownership, never input JSON."""
    require(isinstance(process, subprocess.Popen) and process.poll() is None)
    pid = process.pid
    require(type(pid) is int and 1 <= pid < 1 << 31)
    from validate_computer_asset_export_v3_multichunk_evidence import read_file
    stat_values = parse_stat(read_file(Path('/proc') / str(pid) / 'stat', 65536), pid)
    memory = parse_status(read_file(Path('/proc') / str(pid) / 'status', 65536))
    require(process.pid == pid and process.poll() is None)
    start = stat_values.pop('startTicks')
    value = {'pid': pid, 'startTicks': start, 'sample': dict(stat_values, **memory)}
    if expected is not None:
        require(pid == expected['pid'] and start == expected['startTicks'])
        for key in ('userTicks', 'systemTicks', 'vmHWMKiB'):
            require(int(value['sample'][key]) >= int(expected['sample'][key]))
    return value


@safe_failure
def start_samples(api, worker):
    require(api is not worker)
    hz = os.sysconf('SC_CLK_TCK')
    require(type(hz) is int and 1 <= hz <= 1000000)
    return {'started': time.monotonic(), 'hz': hz, 'api': sample(api), 'worker': sample(worker)}


@safe_failure
def finish_samples(before, api, worker):
    from validate_computer_asset_export_v3_multichunk_evidence import (encoded, read_file,
        digest, LIMITS, LIMITATIONS, validate_resources, safe_path)
    after = {'api': sample(api, before['api']), 'worker': sample(worker, before['worker'])}
    reference = read_file(PROVENANCE, LIMITS['provenance.json'])
    value = {'format': 1, 'suite': SUITE, 'provenanceSHA256': digest(reference),
             'scope': 'full_browser_phase', 'clockTicksPerSecond': before['hz'],
             'elapsedMs': int((time.monotonic() - before['started']) * 1000),
             'limitations': LIMITATIONS}
    for name in ('api', 'worker'):
        first, last = before[name], after[name]
        value[name] = {'pid': first['pid'], 'startTicks': first['startTicks'],
                       'before': first['sample'], 'after': last['sample'],
                       'userCpuSeconds': (int(last['sample']['userTicks']) - int(first['sample']['userTicks'])) / before['hz'],
                       'systemCpuSeconds': (int(last['sample']['systemTicks']) - int(first['sample']['systemTicks'])) / before['hz']}
    validate_resources(value, reference, final=False)
    destination = safe_path(PRE_RESOURCES)
    require(not destination.exists())
    with destination.open('xb') as stream:
        stream.write(encoded(value) + b'\n')


def discover():
    from validate_computer_asset_export_v3_multichunk_evidence import safe_path
    root = safe_path('web/test-results')
    # Each browser job starts with an empty dedicated output. Only one fixed-name
    # evidence is admissible; no data from another successful test is selected.
    matches = list(root.rglob('evidence.json'))
    require(len(matches) == 1)
    return safe_path(matches[0]).parent


@safe_failure
def validate_core():
    from validate_computer_asset_export_v3_multichunk_evidence import (read_file, LIMITS,
        strict_json, capture_provenance, validate_resources)
    directory = discover()
    reference = read_file(PROVENANCE, LIMITS['provenance.json'])
    require(strict_json(reference, LIMITS['provenance.json']) == capture_provenance(Path('.')))
    sample_data = strict_json(read_file(PRE_RESOURCES, 32 << 10), 32 << 10)
    validate_resources(sample_data, reference, final=False)
    subprocess.run([sys.executable, VALIDATOR, str(directory), '--provenance', str(PROVENANCE),
                    '--repo', '.', '--core'], check=True, capture_output=True, timeout=30)


@safe_failure
def publish():
    from validate_computer_asset_export_v3_multichunk_evidence import (FILES, LIMITS, read_file,
        strict_json, capture_provenance, validate_resources, validate_bundle, safe_path, encoded)
    destination = safe_path(OUTPUT)
    require(not destination.exists())
    directory = discover()
    reference = read_file(PROVENANCE, LIMITS['provenance.json'])
    require(strict_json(reference, LIMITS['provenance.json']) == capture_provenance(Path('.')))
    original = validate_bundle(directory, reference, final=False)
    resources = strict_json(read_file(PRE_RESOURCES, 32 << 10), 32 << 10)
    validate_resources(resources, reference, final=False)
    resources['runtime'] = strict_json(read_file('.e2e-diagnostics/runtime.json', 64 << 10), 64 << 10)
    validate_resources(resources, reference)
    resource_bytes = encoded(resources) + b'\n'
    require(len(resource_bytes) <= LIMITS['resources.json'])
    # All admission has completed before a staged directory or byte is created.
    # A failed readback is removed; never expose a partial success directory.
    try:
        destination.mkdir(parents=True)
        for name in FILES:
            raw = resource_bytes if name == 'resources.json' else original[name]
            with (destination / name).open('xb') as stream:
                stream.write(raw)
        staged = validate_bundle(destination, reference)
        require(all(staged[name] == original[name] for name in FILES[:-1]))
        require(all(read_file(directory / name, LIMITS[name]) == original[name] for name in FILES[:-1]))
        require(strict_json(reference, LIMITS['provenance.json']) == capture_provenance(Path('.')))
    except Exception:
        if destination.exists() and not destination.is_symlink():
            shutil.rmtree(destination)
        raise ValueError(ERROR) from None



@safe_failure
def validate_final():
    """Bound final admission, staging and readback together to thirty seconds."""
    from validate_computer_asset_export_v3_multichunk_evidence import safe_path
    destination = safe_path(OUTPUT)
    require(not destination.exists())
    try:
        # run() kills and waits for this exact child on TimeoutExpired. Only the
        # child performs admission/staging, and its raw output never escapes.
        subprocess.run([sys.executable, str(Path(__file__).resolve()), 'publish-owned'],
                       check=True, capture_output=True, timeout=30)
    except Exception:
        if destination.exists() and not destination.is_symlink():
            shutil.rmtree(destination)
        raise ValueError(ERROR) from None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['capture', 'validate', 'core', 'publish-owned'])
    parser.add_argument('--repo', default=Path('.'), type=Path)
    parser.add_argument('--output', default=PROVENANCE, type=Path)
    args = parser.parse_args()
    try:
        if args.action == 'capture':
            capture(args.repo, args.output)
        elif args.action == 'core':
            validate_core()
        elif args.action == 'publish-owned':
            publish()
        else:
            validate_final()
    except Exception:
        raise SystemExit(ERROR) from None

# Frozen outer inverse constants.
DELTA_SHA256 = 'f7287ae36dc67f85eea972e5957e9f22fd5c60a2bf2f9ed73138a8fe5c3c3cf1'
PREDECESSORS = {'scripts/test_auth_e2e.py': '6d55979bcaff97f5ab23b4c3d8eafbb65a080a3c49555eb2e17823750f6aa2a9', 'scripts/ci_inventory.json': '1d9ef348def9d9b55ee4d0f8bb7da340a3c9049f9e90b5826bd71f3a39c47f24', '.github/workflows/ci.yml': '80c650cfd2184e2c9604d1a222843b64c642cb300ba63e2b1ffe9df20a899e61', 'scripts/ci_workflow_contract.py': '2b08139edc5c0613a62976ddfb6de8cb816c5610bc43c327db83f92f602a2542'}

if __name__ == "__main__":
    main()
