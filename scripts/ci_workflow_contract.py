"""Exact compatibility projection for pre-selection workflow invariants.

Only this independently reviewed CI delta may be reversed. The old workflow's
complete hash still protects every command, timeout, permission and evidence
step. Selection behavior is checked separately against the actual workflow.
"""
import hashlib
import json
from pathlib import Path


def restore_native_phase_budgets(text):
    """Validate current native phase limits, then reverse only their reviewed delta.

    This is a historical source projection, NOT preservation of old job budgets.
    The raw workflow now permits 41/51 minutes instead of 10/10 minutes.
    """
    import re

    def require(condition):
        if not condition:
            raise ValueError('native phase budget workflow differs from reviewed delta')

    baseline = '0fd9599e994b86e28d72d2fbf28be4fffa14f7bbff171b3d47c96069e592b195'
    # Literal reviewed step identities/limits and whole-block hashes: the mutable
    # fixture cannot authorize a different limit, command or replacement.
    jobs = (('native-no-store',
      41,
      (('      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2', 1),
       ('      - uses: actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020 # v4.4.0', 2),
       ('      - name: Install locked browser test dependencies', 2),
       ('      - name: Install pinned Chromium and headed dependencies', 20),
       ('      - name: Require complete application reads and record native outcomes', 10),
       ('      - name: Validate both production readers and exact native evidence', 2),
       ('      - name: Retain synthetic native stream diagnostics', 2)),
      '657d3a6be156163e9fa09f2833e7999bff2f76525edd7b10f18b5f0bb4df0743',
      'e1f510bdb30a35e7630fe0e327e3a0f5b1e45bb1f196614a14d0b94aaf60ce93'),
     ('native-no-store-fixed',
      51,
      (('      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2', 1),
       ('      - uses: actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020 # v4.4.0', 2),
       ('      - name: Install locked browser test dependencies', 2),
       ('      - name: Install headed browser system dependencies', 20),
       ('      - name: Verify official fixed-build diagnostic browser', 10),
       ('      - name: Require identical native stream proof on the fixed engine', 10),
       ('      - name: Validate both production readers and exact native evidence', 2),
       ('      - name: Retain fixed-build synthetic diagnostics and provenance', 2)),
      '08ea09e66ffbc85e2f11946b585dc0b99b832ddd74829879f786a92017e7852f',
      'f43edcc9e8a027c910e850e4a665cd6398994daca9bb9224cbc87e36dfeb7c4b'))
    require(type(text) is str)
    actual = []
    for job, outer, steps, old_sha, new_sha in jobs:
        marker = '  ' + job + ':\n'
        require(text.count(marker) == 1)
        tail = text[text.index(marker):]
        block = re.split(r'(?=^  [a-z][a-z-]+:\n)', tail, flags=re.M)[1]
        require(re.findall(r'^    timeout-minutes: (.+)$', block, re.M) == [str(outer)])
        parts = re.split(r'(?=^      - )', block, flags=re.M)
        require(len(parts) == len(steps) + 1)
        require(block.count('timeout-minutes:') == len(steps) + 1)
        for part, (header, minutes) in zip(parts[1:], steps):
            require(part.splitlines()[0] == header)
            require(re.findall(r'^        timeout-minutes: (.+)$', part, re.M) == [str(minutes)])
        require(hashlib.sha256(block.encode('utf-8')).hexdigest() == new_sha)
        actual.append(block)

    def unique(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result)
            result[key] = value
        return result

    raw = Path('scripts/fixtures/native_phase_budgets_ci_delta.json').read_bytes()
    # Two complete native blocks, old and new: intentionally larger than the
    # one-line auth delta's 8192-byte cap, still bounded before JSON parsing.
    require(0 < len(raw) <= 16384)
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
            require(depth <= 3)
        elif char in ']}':
            depth -= 1
            require(depth >= 0)
    require(not quoted and depth == 0)
    delta = json.loads(decoded, object_pairs_hook=unique)
    require(type(delta) is dict and set(delta) == {'baselineSHA256', 'replacements'})
    require(delta['baselineSHA256'] == baseline and type(delta['replacements']) is list)
    require(len(delta['replacements']) == 2)
    for change, current, (_, _, _, old_sha, new_sha) in zip(delta['replacements'], actual, jobs):
        require(type(change) is dict and set(change) == {'old', 'new'})
        require(type(change['old']) is str and type(change['new']) is str)
        require(hashlib.sha256(change['old'].encode('utf-8')).hexdigest() == old_sha)
        require(hashlib.sha256(change['new'].encode('utf-8')).hexdigest() == new_sha)
        require(change['new'] == current and text.count(current) == 1)
        text = text.replace(current, change['old'], 1)
    require(hashlib.sha256(text.encode('utf-8')).hexdigest() == baseline)
    return text


def restore_auth_shards16(text):
    """Reverse only the reviewed 16-to-8 auth matrix line, not runtime coverage."""
    text = restore_native_phase_budgets(text)
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('auth shards workflow delta is invalid')
            result[key] = value
        return result
    raw = Path('scripts/fixtures/auth_shards16_ci_delta.json').read_bytes()
    if not 0 < len(raw) <= 8192:
        raise ValueError('auth shards workflow delta is invalid')
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
                raise ValueError('auth shards workflow delta is invalid')
        elif char in ']}':
            depth -= 1
            if depth < 0:
                raise ValueError('auth shards workflow delta is invalid')
    if quoted or depth != 0:
        raise ValueError('auth shards workflow delta is invalid')
    delta = json.loads(decoded, object_pairs_hook=unique)
    baseline = '80c650cfd2184e2c9604d1a222843b64c642cb300ba63e2b1ffe9df20a899e61'
    old = '        shard: [1, 2, 3, 4, 5, 6, 7, 8]\n'
    new = '        shard: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]\n'
    expected = {'baselineSHA256': baseline, 'replacements': [{'old': old, 'new': new}]}
    if type(delta) is not dict or delta != expected:
        raise ValueError('auth shards workflow delta is invalid')
    if type(text) is not str or text.count(new) != 1:
        raise ValueError('auth shards workflow block differs from reviewed delta')
    text = text.replace(new, old, 1)
    if hashlib.sha256(text.encode('utf-8')).hexdigest() != baseline:
        raise ValueError('non-auth-shards workflow bytes changed')
    return text


def restore_access_role_bulk(text):
    """Reverse exactly the reviewed access-only validated two-file upload."""
    text = restore_auth_shards16(text)
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
