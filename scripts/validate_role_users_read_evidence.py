"""Strict safe evidence for the live role-user read panel; no API execution.

This validates source/run identity and runner cleanup observations. It cannot
attest semantic screenshot pixels or independently witness Docker absence.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import struct
import subprocess
import sys
import zlib

from validate_access_role_bulk_evidence import (
    MAX_JSON, MAX_PNG, MAX_SOURCE, RUNTIME, bounded_file, checked_path, digest,
    equals, exact, hex_value, identity, integer, require, safe_json, validate_runtime,
)

ERROR = 'role users read evidence validation failed'
INPUT = 'web/test-results/role-users-read'
OUTPUT = 'web/playwright-report/role-users-read-validated'
FILES = ('role-users-read-evidence.json', 'role-users-read-1440.png', 'role-users-read-390.png')
DIMENSIONS = ((1440, 1000), (390, 1200))
SOURCE_PATHS = (
    '.github/workflows/ci.yml',
    'AGENTS.md',
    'README.md',
    'cmd/adtr/main.go',
    'docs/access-contract.md',
    'docs/architecture.md',
    'docs/delivery.md',
    'docs/task-contract.md',
    'internal/auth/access_http.go',
    'internal/auth/access_roles.go',
    'internal/auth/access_types.go',
    'internal/auth/access_users.go',
    'scripts/ci_workflow_contract.py',
    'scripts/e2e_runtime_diagnostics.py',
    'scripts/fixtures/role_users_read_ci_delta.json',
    'scripts/role_users_read_source_contract.py',
    'scripts/role_users_read_workflow.py',
    'scripts/test_access_role_bulk_contract.py',
    'scripts/test_auth_e2e.py',
    'scripts/test_role_users_read_contract.py',
    'scripts/test_role_users_read_workflow_contract.py',
    'scripts/test_user_asset_export_v3_uac_runner_contract.py',
    'scripts/validate_access_role_bulk_evidence.py',
    'scripts/validate_role_users_read_evidence.py',
    'web/e2e/access.spec.ts',
    'web/e2e/navigation.ts',
    'web/e2e/role-users-read.ts',
    'web/package.json',
    'web/playwright.config.ts',
    'web/src/Access.test.tsx',
    'web/src/AccessRoles.tsx',
    'web/src/AccessWorkspace.tsx',
    'web/src/App.tsx',
    'web/src/RoleUsersReadPanel.test.tsx',
    'web/src/RoleUsersReadPanel.tsx',
    'web/src/SocNavigation.tsx',
    'web/src/access-api.ts',
    'web/src/access-common.tsx',
    'web/src/api.ts',
    'web/src/role-users-read.css',
    'web/src/role-users-read.test.ts',
    'web/src/role-users-read.ts',
    'web/src/session-invalidation.ts',
    'web/src/soc-shell.css',
    'web/src/style.css',
)
# Each tuple fixes one actual UI-triggered request, including empty/search state.
READS = (
    ('custom_initial', 'users_read_only', 'empty', 20, 1),
    ('custom_search', 'users_read_only', 'case_insensitive', 20, 1),
    ('custom_literal', 'users_read_only', 'literal_percent', 20, 0),
    ('custom_clear', 'users_read_only', 'empty', 20, 1),
    ('custom_empty', 'users_read_only', 'unmatched', 20, 0),
    ('custom_refresh_empty', 'users_read_only', 'unmatched', 20, 0),
    ('custom_reset', 'users_read_only', 'empty', 20, 1),
    ('custom_page_size', 'users_read_only', 'empty', 10, 1),
    ('custom_refresh', 'users_read_only', 'empty', 10, 1),
    ('custom_detail_reentry', 'users_read_only', 'empty', 20, 1),
    ('viewer_initial', 'viewer', 'empty', 20, 1),
)
ROOT_KEYS = ('schemaVersion', 'case', 'status', 'identity', 'customRoleID', 'readerID',
             'customUserID', 'requests', 'screenshots', 'checks', 'sourceSHA256', 'styleDelivery')


def png_size(raw, dimensions):
    require(dimensions in DIMENSIONS)
    require(type(raw) is bytes and 32 < len(raw) <= MAX_PNG and raw[:8] == b'\x89PNG\r\n\x1a\n')
    offset, metadata, compressed, ended = 8, set(), bytearray(), False
    color = None
    image_started = False
    lengths = {b'sRGB': 1, b'gAMA': 4, b'cHRM': 32, b'pHYs': 9}
    while offset < len(raw):
        require(not ended and offset + 12 <= len(raw))
        length = int.from_bytes(raw[offset:offset + 4], 'big')
        kind = raw[offset + 4:offset + 8]
        end = offset + length + 12
        require(length <= MAX_PNG and end <= len(raw))
        payload = raw[offset + 8:end - 4]
        require(zlib.crc32(kind + payload) & 0xffffffff == int.from_bytes(raw[end - 4:end], 'big'))
        if offset == 8:
            require(kind == b'IHDR' and length == 13)
            width, height, depth, color, compression, filtering, interlace = struct.unpack('>IIBBBBB', payload)
            require((width, height) == dimensions and depth == 8 and color in (2, 6)
                    and compression == filtering == interlace == 0)
        elif kind == b'IDAT':
            image_started = True
            compressed.extend(payload)
        elif kind == b'IEND':
            require(length == 0 and image_started and len(compressed) > 0 and end == len(raw))
            ended = True
        else:
            # Only this bounded nontext metadata, once each and before IDAT.
            # Therefore IDAT chunks cannot be interrupted and then restarted.
            require(not image_started and kind in lengths and kind not in metadata and length == lengths[kind])
            metadata.add(kind)
            if kind == b'sRGB':
                require(payload[0] <= 3)
            elif kind == b'gAMA':
                require(0 < int.from_bytes(payload, 'big') <= 2_147_483_647)
            elif kind == b'cHRM':
                values = struct.unpack('>8I', payload)
                require(all(0 <= number <= 100_000 for number in values)
                        and all(values[index] + values[index + 1] <= 100_000 for index in range(0, 8, 2)))
            else:
                x, y, unit = struct.unpack('>IIB', payload)
                require(x <= 2_147_483_647 and y <= 2_147_483_647 and unit in (0, 1))
        offset = end
    require(ended and color in (2, 6))
    stride = dimensions[0] * (3 if color == 2 else 4) + 1
    expected = stride * dimensions[1]
    decoder = zlib.decompressobj()
    pixels = decoder.decompress(bytes(compressed), expected + 1)
    require(len(pixels) == expected and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail)
    require(all(pixels[row * stride] <= 4 for row in range(dimensions[1])))
    return dimensions



def validate_summary(data, current, sources, images):
    exact(data, ROOT_KEYS)
    equals(data['schemaVersion'], 2)
    equals(data['case'], 'role_users_readonly_workspace')
    equals(data['status'], 'browser_passed_after_legacy_context_cleanup')
    equals(data['identity'], current)
    require(type(data['customRoleID']) is str and re.fullmatch(r'[A-Za-z0-9_-]{24}', data['customRoleID']) is not None)
    require(integer(data['readerID']) and integer(data['customUserID']) and data['readerID'] != data['customUserID'])
    require(type(data['requests']) is list and len(data['requests']) == len(READS))
    for actual, (phase, role, search, size, count) in zip(data['requests'], READS):
        user_id = (data['readerID'] if role == 'viewer' else data['customUserID']) if count else None
        equals(actual, dict(phase=phase, role=role, method='GET', status=200, roleBound=True, exactQuery=True,
            pageIdx=1, pageSize=size, searchKind=search, total=count, totalPage=count, exhausted=True,
            responseUsers=count, visibleUsers=count, userID=user_id, rowFieldsMatch=True,
            previousDisabled=True, nextDisabled=True))
    require(type(data['screenshots']) is list and len(data['screenshots']) == 2)
    require(type(images) is list and len(images) == 2)
    for item, image, name, (width, height) in zip(data['screenshots'], images, FILES[1:], DIMENSIONS):
        png_size(image, (width, height))
        equals(item, dict(file=name, bytes=len(image), sha256=digest(image), width=width, height=height,
            viewport=dict(width=width, height=height), fullPage=False, proofControls=0, visibleUsers=1,
            headingVisible=True, usernameVisible=True, roleVisible=True,
            layout=dict(mode='stacked' if width == 390 else 'table', tableDisplay='block' if width == 390 else 'table',
                bodyDisplay='block' if width == 390 else 'table-row-group', rowDisplay='block' if width == 390 else 'table-row',
                cellDisplay='block' if width == 390 else 'table-cell', cellsComputedDisplay=True, cellsWithinViewport=True,
                contentFits=True, horizontalOverflow=False, headersVisuallyHidden=width == 390,
                mobileFieldLabels=width == 390, visibleFields=6)))
    equals(data['styleDelivery'], dict(documentHTTP=200, documentSameOrigin=True, documentHTML=True,
        documentCSPExact=True, stylesheetSameOrigin=True, stylesheetRulePresent=True,
        inlineStyleElements=0, inlineStyleAttributes=0))
    checks = data['checks']
    exact(checks, ('mutatingRequests', 'readOnlyCheckPOSTs', 'userRequests', 'listClosed', 'detailReturned',
        'viewerSelected', 'finalListVisible', 'localStorageEmpty', 'sessionStorageEmpty',
        'extraSensitiveMutations', 'newContexts', 'pageNavigationBeyondSinglePage'))
    require(integer(checks['readOnlyCheckPOSTs'], 0, 2))
    equals(checks, dict(mutatingRequests=0, readOnlyCheckPOSTs=checks['readOnlyCheckPOSTs'], userRequests=len(READS),
        listClosed=True, detailReturned=True, viewerSelected=True, finalListVisible=True, localStorageEmpty=True,
        sessionStorageEmpty=True, extraSensitiveMutations=0, newContexts=0,
        pageNavigationBeyondSinglePage='not_exercised_one_user_per_role'))
    exact(sources, SOURCE_PATHS)
    require(all(hex_value(value) for value in sources.values()))
    equals(data['sourceSHA256'], sources)


def validate_and_stage(root, current, tree, checkout_sources=None):
    require(hex_value(tree, 40))
    equals(current, identity(dict(GITHUB_REPOSITORY=current['repository'], GITHUB_SHA=current['testedCommit'],
        GITHUB_RUN_ID=current['runID'], GITHUB_RUN_ATTEMPT=current['runAttempt'], GITHUB_EVENT_NAME=current['event']),
        current['testedCommit'], tree))
    folder = checked_path(root, INPUT)
    require(folder.is_dir() and {item.name for item in folder.iterdir()} == set(FILES))
    destination = checked_path(root, OUTPUT)
    require(not destination.exists())
    raw = bounded_file(root, INPUT + '/' + FILES[0], MAX_JSON)
    images = [bounded_file(root, INPUT + '/' + name, MAX_PNG) for name in FILES[1:]]
    sources = {path: digest(bounded_file(root, path, MAX_SOURCE)) for path in SOURCE_PATHS}
    if checkout_sources is not None:
        equals(sources, checkout_sources)
    data = safe_json(raw)
    validate_summary(data, current, sources, images)
    observation = validate_runtime(bounded_file(root, RUNTIME, MAX_JSON))
    staged = dict(browser=data, artifactBinding=dict(identity=current, testedTree=tree,
        browserSummarySHA256=digest(raw), sourceSHA256=sources), runnerObservation=observation)
    encoded = (json.dumps(staged, sort_keys=True, ensure_ascii=True, allow_nan=False, indent=2) + '\n').encode()
    require(len(encoded) <= MAX_JSON)
    destination = checked_path(root, OUTPUT)
    destination.mkdir(parents=True, exist_ok=False)
    outputs = dict(zip(FILES, [encoded, *images]))
    for name, content in outputs.items():
        with checked_path(root, OUTPUT + '/' + name).open('xb') as stream:
            stream.write(content)
    require({item.name for item in destination.iterdir()} == set(FILES))
    for name, content in outputs.items():
        equals(bounded_file(root, OUTPUT + '/' + name, MAX_JSON if name == FILES[0] else MAX_PNG), content)
    return staged


def validate_checkout(root, environment):
    checked_path(root, '.')
    def git(arguments, strip=True):
        result = subprocess.run(['git', *arguments], cwd=root, check=True, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, timeout=10)
        require(len(result.stdout) <= 16_384)
        text = result.stdout.decode('ascii')
        return text.strip() if strip else text
    commit = git(['rev-parse', 'HEAD'])
    tree = git(['rev-parse', 'HEAD^{tree}'])
    current = identity(environment, commit, tree)
    equals(git(['diff', '--quiet', 'HEAD', '--']), '')
    equals(sorted(git(['ls-files', '--error-unmatch', '--', *SOURCE_PATHS]).splitlines()), sorted(SOURCE_PATHS))
    records = git(['ls-tree', '-z', 'HEAD', '--', *SOURCE_PATHS], strip=False).split('\0')
    require(records[-1] == '')
    blobs = {}
    for record in records[:-1]:
        match = re.fullmatch(r'100644 blob ([0-9a-f]{40})\t([^\0]+)', record)
        require(match is not None)
        blob, path = match.groups()
        require(path in SOURCE_PATHS and path not in blobs)
        blobs[path] = blob
    exact(blobs, SOURCE_PATHS)
    sources = {}
    for path in SOURCE_PATHS:
        raw = bounded_file(root, path, MAX_SOURCE)
        require(hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest() == blobs[path])
        sources[path] = digest(raw)
    return current, tree, sources


class ClosedParser(argparse.ArgumentParser):
    def error(self, message):
        raise ValueError(ERROR)


def main(argv=None, environment=None):
    try:
        parser = ClosedParser()
        parser.add_argument('--source-root', type=Path, required=True)
        args = parser.parse_args(argv)
        root = args.source_root.absolute()
        current, tree, sources = validate_checkout(root, os.environ if environment is None else environment)
        validate_and_stage(root, current, tree, sources)
    except (OSError, ValueError, TypeError, KeyError, OverflowError, RecursionError, zlib.error, subprocess.SubprocessError):
        print(ERROR, file=sys.stderr)
        return 1
    print('role users read evidence validated')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
