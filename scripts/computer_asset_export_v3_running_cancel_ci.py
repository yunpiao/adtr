"""Fail-closed provenance capture and exact running-cancel artifact staging."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from computer_asset_export_v3_ci import configure as configure_success

SUITE = 'computer-asset-export-v3-running-cancel'
PROVENANCE = Path('.e2e-diagnostics') / (SUITE + '-provenance.json')
ARTIFACTS = Path('web/playwright-report') / (SUITE + '-validated')
VALIDATOR = 'scripts/validate_computer_asset_export_v3_running_cancel_evidence.py'
FILENAMES = ('running-checkpoint.png', 'cancel-requested.png', 'cancelled-terminal.png')


def configure(env):
    # Reject cross-kind and terminal fixtures before mutating the caller's env.
    for other in ('USER', 'GROUP', 'COMPUTER'):
        prefix = 'ADTR_E2E_' + other + '_ASSET_EXPORT_V3'
        if env.get(prefix + '_TERMINAL') or (other != 'COMPUTER' and
                (env.get(prefix) == 'true' or env.get(prefix + '_RUNNING_CANCEL') == 'true')):
            raise RuntimeError('running cancellation requires one isolated kind')
        if env.get('ADTR_E2E_LDAP_DIRECTORY_' + other + '_EXPORT_FAILURE_V3') == 'true':
            raise RuntimeError('running cancellation requires an unpoisoned source')
    configure_success(env)
    env['ADTR_E2E_COMPUTER_ASSET_EXPORT_V3_RUNNING_CANCEL'] = 'true'


def clear_artifacts():
    if ARTIFACTS.is_symlink():
        raise RuntimeError('artifact directory must not be a symlink')
    if ARTIFACTS.exists():
        shutil.rmtree(ARTIFACTS)


def capture():
    PROVENANCE.parent.mkdir(parents=True, exist_ok=True)
    PROVENANCE.unlink(missing_ok=True)
    clear_artifacts()
    result = subprocess.run([sys.executable, VALIDATOR, '--capture-provenance', '--repo', '.'],
                            check=True, capture_output=True, timeout=30)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=PROVENANCE.parent, prefix='.running-provenance-', delete=False) as output:
            temporary = Path(output.name)
            output.write(result.stdout)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, PROVENANCE)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def validate():
    clear_artifacts()
    found = sorted(Path('web/test-results').rglob(SUITE + '-evidence.json'))
    if len(found) != 1 or found[0].is_symlink() or not found[0].is_file():
        raise RuntimeError('exactly one regular current running cancellation evidence required')
    evidence = found[0]
    subprocess.run([sys.executable, VALIDATOR, str(evidence), '--provenance', str(PROVENANCE), '--repo', '.'],
                   check=True, timeout=30)
    data = json.loads(evidence.read_bytes())
    case = data['cases'][0]
    references = tuple(v['filename'] for v in (case['running']['screenshot'], case['pending']['screenshot'], case['screenshot']))
    if references != FILENAMES:
        raise RuntimeError('exact running cancellation PNG allowlist required')
    ARTIFACTS.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=ARTIFACTS.parent, prefix='.running-staging-') as temporary:
        staging = Path(temporary) / ARTIFACTS.name
        staging.mkdir()
        for source in (PROVENANCE, evidence, *(evidence.parent / name for name in FILENAMES)):
            if source.is_symlink() or not source.is_file():
                raise RuntimeError('validated artifact must be a regular file')
            shutil.copyfile(source, staging / source.name)
        os.replace(staging, ARTIFACTS)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['capture', 'validate'])
    args = parser.parse_args()
    (capture if args.action == 'capture' else validate)()
