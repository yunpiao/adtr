"""Exact outer inverse for the additive role-user browser consumer only."""
import hashlib

BASE_SPEC = 'af4cabcbbf242c09257033373936648e0bfebc0935f61b837b1ae2c65155503c'
BASE_SIZE = 52056
BLOCKS = (
    ('import { createHmac } from "node:crypto";\n',
     '// AD-F-176 role users: exact additive readonly consumer.\n',
     '// End AD-F-176 role users import.\n',
     '40be1dbf78f6efd9040515ac53ed28fd43652138bd043d53427827cf7b8b896a', 150),
    ('  // End AD-F-176 success writer.\n',
     '  // AD-F-176 role users: run only after all legacy cleanup and evidence.\n',
     '  // End AD-F-176 role users readonly consumer.\n',
     '6aea137dbf6c0010450ed77d6ec806f0c81589342f6c87c6c949d882b9adb6a3', 231),
)
LEGACY_READER = """    return Path(path).read_bytes().decode('utf-8')
"""
OUTER_READER = """    raw = Path(path).read_bytes().decode('utf-8')
    if str(path) == 'web/e2e/access.spec.ts':
        from role_users_read_source_contract import restore_role_users_spec
        return restore_role_users_spec(raw)
    return raw
"""
BASE_BULK_TEST = 'f662ee0c595cc546a83d0738e863fde0ddb34e74a85738b5e85935c269aae62a'


def require(condition):
    if not condition:
        raise ValueError('role users readonly source differs from exact reviewed additions')


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def restore_role_users_spec(text):
    require(type(text) is str)
    for anchor, begin, end, expected, size in BLOCKS:
        require(text.count(anchor) == text.count(begin) == text.count(end) == 1)
        left, right = text.index(begin), text.index(end) + len(end)
        require(left < right)
        block = text[left:right]
        require(len(block.encode()) == size and digest(block.encode()) == expected)
        require(text.count(anchor + block) == 1)
        text = text.replace(block, '', 1)
    require(len(text.encode()) == BASE_SIZE and digest(text.encode()) == BASE_SPEC)
    return text


def restore_role_users_bulk_reader(text):
    require(type(text) is str and text.count(OUTER_READER) == 1)
    return text.replace(OUTER_READER, LEGACY_READER, 1)


BROWSER_HELPER_SHA256 = '9a82f35f01571b540d97f127d9de767f585dbfe9efc898e29a8424a7aa506dd0'


def validate_role_users_browser_helper(raw):
    require(type(raw) is bytes and digest(raw) == BROWSER_HELPER_SHA256)


def restore_role_users_bulk_contract(text):
    from test_role_users_read_workflow_contract import restore_bulk_workflow_test_outer
    text = restore_role_users_bulk_reader(restore_bulk_workflow_test_outer(text))
    require(digest(text.encode()) == BASE_BULK_TEST)
    return text
