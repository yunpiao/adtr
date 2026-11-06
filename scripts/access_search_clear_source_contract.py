"""Exact outer inverse for draft-only access search controls; old checks remain frozen."""
import hashlib

BASE_SPEC = '0ccedd019b23dd5a9ab6a30e29a27ddd41d7b6d8fa6e980405a6957af4410dc2'
BASE_SIZE = 60595
BLOCKS = (('import { createHmac } from "node:crypto";\n', '// F45 search clear: additive draft-only native checks.\n', '// End F45 search clear native helpers.\n', 'c7dfb5c3e4af438080ce9c9fb9a31f21b8f43df5e0fa3c3891d7e9fe81645b70', 1941), ('  await page.getByRole("button", { name: "角色管理", exact: true }).click();\n', '  // F45 search clear: existing role-list draft, no new read or mutation.\n', '  // End F45 search clear role-list check.\n', '57a8a067e4f5781808d5bf3644f85674226810b9631c216defb406e7ac854d51', 165), ('      bulkPhase = "safe_screenshot";\n', '      // F45 search clear: existing user-list draft, before the unchanged final query.\n', '      // End F45 search clear user-list check.\n', 'be0d46f1d9276050ec43f2ee0cbbbafa4aca360254bc4f849dfcdb4c4efce7ba', 186))


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def require(condition):
    if not condition:
        raise ValueError('access search clear source differs from exact reviewed additions')


def restore_search_clear_spec(text):
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
