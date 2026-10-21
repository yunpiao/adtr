"""Exact compatibility projection for pre-selection workflow invariants.

Only this independently reviewed CI delta may be reversed. The old workflow's
complete hash still protects every command, timeout, permission and evidence
step. Selection behavior is checked separately against the actual workflow.
"""
import hashlib
import json
from pathlib import Path


def restore_health_recovery(text):
    """Reverse exactly the reviewed health-only build and six-file evidence wiring."""
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
