#!/usr/bin/env python3
"""Offline DAG replay, not a measured prediction of hosted-runner capacity."""
import argparse
from datetime import datetime
import json
from pathlib import Path


def epoch(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()


def schedule(durations, slots):
    if slots < 1:
        raise ValueError('slots must be positive')
    lanes = [0] * slots
    for duration in durations:
        if duration < 0:
            raise ValueError('negative duration')
        lanes[lanes.index(min(lanes))] += duration
    return max(lanes)


def replay(sample, browser_slots):
    jobs = sample['jobs']
    if len({j['name'] for j in jobs}) != len(jobs):
        raise ValueError('duplicate jobs')
    durations = {j['name']: epoch(j['completed_at']) - epoch(j['started_at']) for j in jobs}
    if any(j['conclusion'] != 'success' for j in jobs) or any(d < 0 for d in durations.values()):
        raise ValueError('successful nonnegative-duration baseline required')
    if len(set(sample['fixedOrder'])) != len(sample['fixedOrder']):
        raise ValueError('duplicate fixed order')
    browser = {n.split('browser (', 1)[1].split(',')[0]: d for n, d in durations.items() if n.startswith('browser (')}
    if set(browser) != set(sample['browserOrder']) or len(browser) != len(sample['browserOrder']):
        raise ValueError('browser coverage mismatch')
    fixed_names = [f'user-assets-fixed ({name})' for name in sample['fixedOrder']]
    expected = set(fixed_names) | {f'auth-integration ({i})' for i in range(1, 5)} | {'prepare-postgres', 'contracts', 'native-no-store', 'native-no-store-fixed', 'verify'}
    expected |= {j['name'] for j in jobs if j['name'].startswith('browser (')}
    if set(durations) != expected:
        raise ValueError('DAG coverage mismatch')
    prepare = durations['prepare-postgres']
    browser_span = schedule([browser[n] for n in sample['browserOrder']], browser_slots)
    fixed_span = schedule([durations[n] for n in fixed_names], 2)
    auth_span = schedule([durations[f'auth-integration ({i})'] for i in range(1, 5)], 2)
    tails = {'browser': prepare + browser_span, 'fixed': prepare + fixed_span,
             'auth': prepare + auth_span, 'contracts': prepare + durations['contracts'],
             'native': durations['native-no-store'], 'nativeFixed': durations['native-no-store-fixed']}
    return {'run': sample['number'], 'jobs': len(jobs), 'browserSlots': browser_slots,
            'modelElapsedSeconds': max(tails.values()) + durations['verify'],
            'browserSpanSeconds': browser_span, 'fixedSpanSeconds': fixed_span,
            'criticalDependency': max(tails, key=tails.get), 'dependencyEndSeconds': tails}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('input', nargs='?', default='docs/evidence/ci-eight-slot-model-input.json')
    args = parser.parse_args()
    data = json.loads(Path(args.input).read_text())
    print(json.dumps({'kind': 'offline-model-not-observed-eight-slot-performance',
                      'assumptions': data['assumptions'],
                      'results': [replay(sample, slots) for sample in data['runs'] for slots in (4, 8)]}, indent=2))


if __name__ == '__main__':
    main()
