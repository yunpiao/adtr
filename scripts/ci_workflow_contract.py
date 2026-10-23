"""Exact compatibility projection for pre-selection workflow invariants.

Only this independently reviewed CI delta may be reversed. The old workflow's
complete hash still protects every command, timeout, permission and evidence
step. Selection behavior is checked separately against the actual workflow.
"""
import hashlib
import json
from pathlib import Path
from computer_asset_export_v3_multichunk_ci import restore_multichunk_source


def restore_access_role_bulk(text):
    """Reverse exactly the reviewed access-only validated two-file upload."""
    text = restore_multichunk_source(text, ".github/workflows/ci.yml")
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('access bulk workflow delta is invalid')
            result[key] = value
        return result
    raw = Path('scripts/fixtures/access_role_bulk_ci_delta.json').read_bytes()
    if not 0 < len(raw) <= 8192:
        raise ValueError('access bulk workflow delta is invalid')
    decoded = raw.decode('utf-8')
    depth, quoted, escaped = 0, False, False
    for char in decoded:
        if quoted:
            if escaped:
                escaped = False
            elif char == '\\':
                escaped = True
            elif char == '"':
                quoted = False
        elif char == '"':
            quoted = True
        elif char in '[{':
            depth += 1
            if depth > 3:
                raise ValueError('access bulk workflow delta is invalid')
        elif char in ']}':
            depth -= 1
            if depth < 0:
                raise ValueError('access bulk workflow delta is invalid')
    if quoted or depth != 0:
        raise ValueError('access bulk workflow delta is invalid')
    delta = json.loads(decoded, object_pairs_hook=unique)
    baseline = 'adca3ced51556884f35c1603dbfc86713280ec01abc3c6a275d2d42a7b6e2a47'
    if (type(delta) is not dict or set(delta) != {'baselineSHA256', 'replacements'}
            or delta['baselineSHA256'] != baseline or type(delta['replacements']) is not list
            or len(delta['replacements']) != 1):
        raise ValueError('access bulk workflow delta is invalid')
    change = delta['replacements'][0]
    if (type(change) is not dict or set(change) != {'old', 'new'}
            or change['old'] != '      - name: Validate exact maintenance target observation evidence\n'
            or type(change['new']) is not str
            or hashlib.sha256(change['new'].encode('utf-8')).hexdigest() != '559821546900e8a45ce73d7e487b25ae7c5c60eede6d95b31f8163c39e4bb928'
            or type(text) is not str or text.count(change['new']) != 1):
        raise ValueError('access bulk workflow block differs from reviewed delta')
    text = text.replace(change['new'], change['old'], 1)
    if hashlib.sha256(text.encode('utf-8')).hexdigest() != baseline:
        raise ValueError('non-access-bulk workflow bytes changed')
    return text


def restore_archive_target_observation(text):
    """Reverse only the exact maintenance three-file validation/upload wiring."""
    text = restore_access_role_bulk(text)
    delta = json.loads(Path('scripts/fixtures/archive_target_observation_ci_delta.json').read_text())
    if set(delta) != {'baselineSHA256', 'replacements'} or len(delta['replacements']) != 1:
        raise ValueError('archive observation workflow delta is invalid')
    for change in reversed(delta['replacements']):
        if set(change) != {'old', 'new'} or not change['old'] or not change['new'] or text.count(change['new']) != 1:
            raise ValueError('archive observation workflow block differs from reviewed delta')
        text = text.replace(change['new'], change['old'], 1)
    if hashlib.sha256(text.encode()).hexdigest() != delta['baselineSHA256']:
        raise ValueError('non-archive-observation workflow bytes changed')
    return text


def restore_health_recovery(text):
    """Reverse exactly the reviewed health-only build and six-file evidence wiring."""
    text = restore_archive_target_observation(text)
    delta = json.loads(Path('scripts/fixtures/tasks_health_recovery_ci_delta.json').read_text())
    for change in reversed(delta['replacements']):
        if text.count(change['new']) != 1:
            raise ValueError('health recovery workflow block differs from reviewed delta')
        text = text.replace(change['new'], change['old'], 1)
    if hashlib.sha256(text.encode()).hexdigest() != delta['baselineSHA256']:
        raise ValueError('non-health workflow bytes changed')
    return text


def restore_messages_isolation(text):
    return restore_messages_isolation_predecessor(restore_health_recovery(text))


def restore_messages_isolation_predecessor(text):
    """Reverse only the reviewed two-line isolation evidence routing delta."""
    delta = json.loads(Path('scripts/fixtures/messages_isolation_ci_delta.json').read_text())
    for change in reversed(delta['replacements']):
        if text.count(change['new']) != 1:
            raise ValueError('messages isolation workflow block differs from reviewed delta')
        text = text.replace(change['new'], change['old'], 1)
    if hashlib.sha256(text.encode()).hexdigest() != delta['baselineSHA256']:
        raise ValueError('non-isolation workflow bytes changed')
    return text


def restore_directory_v4(text):
    """Reverse only the separately reviewed additive dictionary4 evidence steps."""
    return restore_directory_v4_predecessor(restore_health_recovery(text))


def restore_directory_v4_predecessor(text):
    text = restore_messages_isolation_predecessor(text)
    delta = json.loads(Path('scripts/fixtures/directory_v4_ci_delta.json').read_text())
    for change in reversed(delta['replacements']):
        if text.count(change['new']) != 1:
            raise ValueError('dictionary4 workflow block differs from reviewed delta')
        text = text.replace(change['new'], change['old'], 1)
    if hashlib.sha256(text.encode()).hexdigest() != delta['baselineSHA256']:
        raise ValueError('non-dictionary4 workflow bytes changed')
    return text


def restore_selection(text):
    return restore_selection_predecessor(restore_health_recovery(text))


def restore_selection_predecessor(text):
    text = restore_directory_v4_predecessor(text)
    delta = json.loads(Path('scripts/ci_workflow_selection_delta.json').read_text())
    for change in reversed(delta['replacements']):
        if text.count(change['new']) != 1:
            raise ValueError('selection workflow block differs from reviewed delta')
        text = text.replace(change['new'], change['old'], 1)
    if hashlib.sha256(text.encode()).hexdigest() != delta['baselineSHA256']:
        raise ValueError('non-selection workflow bytes changed')
    return text


def reviewed_workflow():
    return restore_selection(Path('.github/workflows/ci.yml').read_text())


def reviewed_source(path):
    return reviewed_workflow() if str(path) == '.github/workflows/ci.yml' else Path(path).read_text()
