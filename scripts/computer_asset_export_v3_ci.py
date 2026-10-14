"""Isolated dictionary3 export CI provenance and secret-safe artifact gate.

Required only for computer-asset-export-v3. No production setting or rate budget is
changed. Evidence binds the clean checkout/run before browser execution and is
revalidated against the same source afterward. Only validated synthetic assets
may be uploaded; failed runs retain the existing sanitized diagnostics instead.
"""
import argparse
import json
import shutil
from pathlib import Path
import subprocess
import sys

PROVENANCE = Path('.e2e-diagnostics/computer-asset-export-v3-provenance.json')
ARTIFACTS = Path('web/playwright-report/computer-export-validated')
VALIDATOR = 'scripts/validate_computer_asset_export_v3_evidence.py'
MIXED_MODES = ('GROUP_ASSETS_V3', 'V2', 'V4', 'EMPTY', 'SLOW', 'USER_ASSETS_V2', 'GROUP_MEMBERS_V4_PAGINATION', 'GROUP_MEMBERS_V4_REFRESH', 'USER_EXPORT_FAILURE_V3', 'GROUP_EXPORT_FAILURE_V3')


def configure(env):
    if any(env.get(key) == 'true' for key in ('ADTR_E2E_USER_ASSET_EXPORT_V3', 'ADTR_E2E_GROUP_ASSET_EXPORT_V3')) or any(env.get('ADTR_E2E_LDAP_DIRECTORY_' + mode) == 'true' for mode in MIXED_MODES) or env.get('ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR') or any(env.get(key) == 'true' for key in ('ADTR_DIRECTORY_READ_V2_ENABLED', 'ADTR_DIRECTORY_READ_V4_ENABLED')):
        raise RuntimeError('Computer asset export requires the isolated computer dictionary3 fixture')
    env['ADTR_E2E_COMPUTER_ASSET_EXPORT_V3'] = 'true'
    env['ADTR_E2E_LDAP_DIRECTORY_COMPUTER_ASSETS_V3'] = 'true'


def capture():
    PROVENANCE.parent.mkdir(parents=True, exist_ok=True)
    PROVENANCE.unlink(missing_ok=True)
    # Capture fully before publishing the independent reference; failures cannot
    # leave a previous run's provenance in place.
    result = subprocess.run([sys.executable, VALIDATOR, '--capture-provenance', '--repo', '.'],
                            check=True, capture_output=True, timeout=30)
    PROVENANCE.write_bytes(result.stdout)


def validate():
    if ARTIFACTS.exists():
        shutil.rmtree(ARTIFACTS)
    evidence = sorted(Path('web/test-results').rglob('computer-asset-export-v3-evidence.json'))
    if len(evidence) != 1:
        raise RuntimeError('Exactly one current computer asset export evidence file is required')
    subprocess.run([sys.executable, VALIDATOR, str(evidence[0]), '--provenance', str(PROVENANCE), '--repo', '.'],
                   check=True, timeout=30)
    # The strict validator has checked filenames, bytes, PNG structure, schema
    # and forbidden secret fields. Stage only those referenced files, never a broad glob of
    # failed traces or other test output. This path is ignored and outside the
    # evidence discovery root, so a second validation cannot find duplicates.
    data = json.loads(evidence[0].read_bytes())
    ARTIFACTS.mkdir(parents=True)
    shutil.copyfile(PROVENANCE, ARTIFACTS / PROVENANCE.name)
    shutil.copyfile(evidence[0], ARTIFACTS / evidence[0].name)
    for case in data['cases']:
        for filename in [case['download']['filename'], case['screenshot']['filename']]:
            shutil.copyfile(evidence[0].parent / filename, ARTIFACTS / filename)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['capture', 'validate'])
    args = parser.parse_args()
    (capture if args.action == 'capture' else validate)()
