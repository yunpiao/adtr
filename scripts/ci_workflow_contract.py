"""Exact compatibility projection for pre-selection workflow invariants.

Only this independently reviewed CI delta may be reversed. The old workflow's
complete hash still protects every command, timeout, permission and evidence
step. Selection behavior is checked separately against the actual workflow.
"""
import hashlib
import json
from pathlib import Path


def restore_selection(text):
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
