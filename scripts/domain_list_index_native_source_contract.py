"""Exact additive inverse for UI-0390 native evidence preparation only."""
import hashlib

BASE_SPEC = '8a987a9405764e2c3c5c24b70fece9db7ee9db04ed707335b19c928a48afd7d8'
BASE_SIZE = 43844
BLOCKS = (('type Authenticator = { secret: string; lastCounter: number };\n',
  '\n// AD-F-001 UI-0390: read-only positions from the existing accepted response.\n',
  '// End AD-F-001 UI-0390 native helpers.\n',
  '2a5999e0a6f99141e3b9d09fbe64f9a933d520d208095c1067ac5f7e873eb77b',
  3094),
 ('  const idField = persistedRow.getByText(`域 ID：${id}`, { exact: true });\n',
  '  // AD-F-001 UI-0390: retain the original persisted domain identity binding.\n',
  '  // End AD-F-001 UI-0390 identity and evidence binding.\n',
  '1453f6726dfd87880581c1e79d7c8d53b97d2894a7773a9e601292a0e888f576',
  485),
 ('  const measureListFacts = async () => {\n    const fields = {\n',
  '      // AD-F-001 UI-0390: reuse the original fragment and clipping thresholds.\n',
  '      // End AD-F-001 UI-0390 position geometry.\n',
  '0222f5cb7e715504d590977a6c475343d93e36c1e9aa9591229f521e729137bc',
  183),
 ('    expect(body.List).toHaveLength(includesVerified ? 1 : 0);\n',
  '    // AD-F-001 UI-0390: bind positions to this same real list response.\n',
  '    // End AD-F-001 UI-0390 response comparison.\n',
  'af5a2b19532ea43af3b6a43d63d46724244c312a6eb5b4651193d9a97190d9f4',
  656),
 ('        "web/e2e/domains.spec.ts",\n',
  '        // AD-F-001 UI-0390: bind the candidate contract and offline guards.\n',
  '        // End AD-F-001 UI-0390 source binding.\n',
  'c47d49b61ffd69981908e80da5abaafdb4a3d993fa664eba8ea76d4aeaae6d77',
  427),
 ('    mobileGeometry: { queueTitle, headingBox, guidanceBox, dangerContrast },\n',
  '    // AD-F-001 UI-0390: evidence only for the unchanged one-domain fixture.\n',
  '    // End AD-F-001 UI-0390 manifest.\n',
  'f5312cd77bf8c0eed3057adb6f1bcf5067be44f2e22f04d21f78bbd1055f0326',
  588))


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def require(condition):
    if not condition:
        raise ValueError('domain list index source differs from exact reviewed additions')


def restore_domain_list_index_spec(text):
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
