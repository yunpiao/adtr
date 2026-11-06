"""Exact inverse: retain every predecessor native guard, write and budget."""
import hashlib
from pathlib import Path

BASE_SPEC = '062b82b3e3c351e6749ce815df136a2ab460532d805435c8a553894666470227'
BASE_SIZE = 70294
BLOCKS = (('// F52 candidate evidence imports.\n',
  '// End F52 candidate evidence imports.\n',
  '7494777345c4b5016c31d4b38c34db249d5c3ce86de0069e12fd38eeb2132b50',
  324,
  '',
  'import { navigateTo } from "./navigation";\n',
  'import {\n  test,\n  expect,\n  type BrowserContext,\n  type Frame,\n  type Locator,\n'),
 ('  // F52 candidate evidence overview.\n',
  '  // End F52 candidate evidence overview.\n',
  'dc42326ff41eab2a571f090896e058279fb6f762811648e1264d366ef14e120a',
  580,
  '  await navigateTo(page, "系统健康");\n'
  '  await expect(page.getByRole("table", { name: "已登记节点" })).toBeVisible();\n',
  'assword(page, password, changed);\n  const mfa = await enrollMfa(page, changed);\n',
  '  const nodes = await readJSON(context, "/api/system/nodes");\n  expect(nodes.nod'),
 ('  // F52 candidate evidence storage.\n',
  '  // End F52 candidate evidence storage.\n',
  '48be40df5461e068a45e959d4260435b4e63bea0b7da1a284757d57b1e73bcad',
  526,
  '  await page.getByRole("button", { name: "存储管理", exact: true }).click();\n'
  '  await expect(\n'
  '    page.getByRole("table", { name: "实际文件系统存储" }),\n'
  '  ).toBeVisible();\n',
  '=${endTime - 86401}&endTime=${endTime}`,\n      )\n    ).status(),\n  ).toBe(400);\n',
  '  const initial: StorageView = await readJSON(\n    context,\n    "/api/system/sto'),
 ('  // F52 candidate evidence sidecar.\n',
  '  // End F52 candidate evidence sidecar.\n',
  '75d7b7537b1e6e052933f62a5df77e823b5e6e489dfac054e5a9d3c84de84397',
  205,
  '',
  'evidence", {\n    path: manifestPath,\n    contentType: "application/json",\n  });\n',
  '});\n'))

def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def require(condition):
    if not condition:
        raise ValueError('system candidate source differs from reviewed additions')


def restore_system_candidate_spec(text):
    require(type(text) is str)
    # Validate locations against the complete candidate before removing blocks.
    for start, end, sha, size, old, before, after in BLOCKS:
        require(text.count(start) == text.count(end) == 1)
        a, b = text.index(start), text.index(end) + len(end)
        require(a < b)
        block = text[a:b]
        require(len(block.encode()) == size and digest(block.encode()) == sha)
        require(text[max(0, a-len(before)):a] == before and text[b:b+len(after)] == after)
    for start, end, sha, size, old, before, after in BLOCKS:
        a, b = text.index(start), text.index(end) + len(end)
        text = text[:a] + old + text[b:]
    require(len(text.encode()) == BASE_SIZE and digest(text.encode()) == BASE_SPEC)
    return text


if __name__ == '__main__':
    root = Path(__file__).resolve().parents[1]
    restore_system_candidate_spec((root/'web/e2e/system.spec.ts').read_text())
    print('System predecessor restored exactly; no native execution claimed.')
