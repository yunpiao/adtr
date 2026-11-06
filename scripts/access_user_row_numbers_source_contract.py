"""Exact additive inverse for platform-user display ordinals only."""
import hashlib

BASE_SPEC = '0585ed4d0dc660f73eb9664e2a1b349df8d439440fb5e03c17016d91e6bfb57c'
BASE_SIZE = 59731
ANCHOR = '      // End AD-F-171 user times native comparison.\n'
BEGIN = '      // AD-F-171 user rows: observe positions on the same validated user response.\n'
END = '      // End AD-F-171 user rows native comparison.\n'
BLOCK_SHA256 = 'afe445759f88c252185d8456e308201772b89a3b88ba8a9e8c04608b93344d54'
BLOCK_SIZE = 864


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def require(condition):
    if not condition:
        raise ValueError('access user row number source differs from exact reviewed addition')


def restore_user_row_numbers_spec(text):
    from access_search_clear_source_contract import restore_search_clear_spec
    text = restore_search_clear_spec(text)
    require(type(text) is str)
    require(text.count(ANCHOR) == text.count(BEGIN) == text.count(END) == 1)
    left, right = text.index(BEGIN), text.index(END) + len(END)
    require(left < right)
    block = text[left:right]
    require(len(block.encode()) == BLOCK_SIZE and digest(block.encode()) == BLOCK_SHA256)
    require(text.count(ANCHOR + block) == 1)
    text = text.replace(block, '', 1)
    require(len(text.encode()) == BASE_SIZE and digest(text.encode()) == BASE_SPEC)
    return text
