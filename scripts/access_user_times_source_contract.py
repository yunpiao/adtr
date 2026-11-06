"""Exact additive inverse for user-list time facts; no old gate is relaxed."""
import hashlib

BASE_SPEC = '418d3b9d4d24fcf3a2640d1e7a5e37c9c2b8b3504dda671d204d8acc67cf9476'
BASE_SIZE = 52437
BLOCKS = (('// End AD-F-176 role users import.\n',
  '// AD-F-171 user times: additive observation of the existing final users GET.\n',
  '// End AD-F-171 user times observation helpers.\n',
  '53bc7fe840eab8dcba1c9c7b6e86b2a623ce7bcc08e85ff030b068783e90e1ec',
  6854),
 ('      bulkPhase = "safe_screenshot";\n',
  '      // AD-F-171 user times: observe the unchanged final filter request.\n',
  '      // End AD-F-171 user times observer registration.\n',
  '865447f157cec1d082db977c15849fd24579ec7c24d0a3c6f8175ce1ee0488fe',
  192),
 ('      await table.scrollIntoViewIfNeeded();\n',
  '      // AD-F-171 user times: compare the native response before the existing image.\n',
  '      // End AD-F-171 user times native comparison.\n',
  'de1468dc0cf0e974c717f06b967c38b7d376693b9a77b142ebfb0e49094ee906',
  248))


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def require(condition):
    if not condition:
        raise ValueError('access user time source differs from exact reviewed additions')


def restore_user_times_spec(text):
    from access_user_row_numbers_source_contract import restore_user_row_numbers_spec
    text = restore_user_row_numbers_spec(text)
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
