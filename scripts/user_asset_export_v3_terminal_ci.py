"""Independent terminal/recovery CI provenance and validated artifact allowlist."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

from user_asset_export_v3_ci import configure as configure_success

SUITES = {'user-asset-export-v3-failure', 'user-asset-export-v3-cancel'}
VALIDATOR = 'scripts/validate_user_asset_export_v3_terminal_evidence.py'


def paths(suite):
    if suite not in SUITES:
        raise ValueError('unknown terminal export suite')
    return (Path('.e2e-diagnostics') / (suite + '-provenance.json'),
            Path('web/playwright-report') / (suite + '-validated'))


def configure(env, suite):
    paths(suite)
    configure_success(env)
    env['ADTR_E2E_USER_ASSET_EXPORT_V3_TERMINAL'] = suite


def capture(suite):
    provenance, artifacts = paths(suite)
    provenance.parent.mkdir(parents=True, exist_ok=True)
    provenance.unlink(missing_ok=True)
    if artifacts.exists():
        shutil.rmtree(artifacts)
    result = subprocess.run([sys.executable, VALIDATOR, '--suite', suite,
                             '--capture-provenance', '--repo', '.'],
                            check=True, capture_output=True, timeout=30)
    # Publish the complete reference atomically. No failed capture can retain
    # an earlier run or leave a partial JSON file for the browser to consume.
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=provenance.parent, prefix='.export-provenance-', delete=False) as output:
            temporary = Path(output.name)
            output.write(result.stdout)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, provenance)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def validate(suite):
    provenance, artifacts = paths(suite)
    if artifacts.exists():
        shutil.rmtree(artifacts)
    evidence = sorted(Path('web/test-results').rglob(suite + '-evidence.json'))
    if len(evidence) != 1:
        raise RuntimeError('exactly one current terminal export evidence file required')
    subprocess.run([sys.executable, VALIDATOR, str(evidence[0]), '--suite', suite,
                    '--provenance', str(provenance), '--repo', '.'], check=True, timeout=30)
    data = json.loads(evidence[0].read_bytes())
    filenames = [case['screenshot']['filename'] for case in data['cases']]
    filenames += [case['download']['filename'] for case in data['cases'] if 'download' in case]
    if len(set(filenames)) != len(filenames) or any(Path(name).name != name for name in filenames):
        raise RuntimeError('ambiguous terminal artifact allowlist')
    artifacts.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=artifacts.parent, prefix='.terminal-staging-') as temporary:
        staging = Path(temporary) / artifacts.name
        staging.mkdir()
        shutil.copyfile(provenance, staging / provenance.name)
        shutil.copyfile(evidence[0], staging / evidence[0].name)
        for filename in filenames:
            source = evidence[0].parent / filename
            if source.is_symlink() or not source.is_file():
                raise RuntimeError('terminal artifact must be a regular file')
            shutil.copyfile(source, staging / filename)
        os.replace(staging, artifacts)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['capture', 'validate'])
    parser.add_argument('--suite', required=True, choices=sorted(SUITES))
    args = parser.parse_args()
    (capture if args.action == 'capture' else validate)(args.suite)
