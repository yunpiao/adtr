import {
  test as base,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
} from "@playwright/test";
import { createHmac, randomUUID } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

const test = base.extend<{ managerContext: BrowserContext }>({
  managerContext: async ({ browser, baseURL }, use) => {
    const context = await browser.newContext({ baseURL });
    try {
      await use(context);
    } finally {
      await context.close();
    }
  },
});

// Proof-bearing DOM/network captures must never become failure artifacts.
test.use({ trace: "off", screenshot: "off", video: "off" });

type Permission = {
  mark: string;
  auth: { readable: boolean; writeable: boolean };
};
type Authenticator = { secret: string; lastCounter: number };

function totp(secret: string, now = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const char of secret.replace(/=+$/, "").toUpperCase()) {
    const index = alphabet.indexOf(char);
    if (index < 0) throw new Error("Invalid synthetic TOTP secret");
    bits += index.toString(2).padStart(5, "0");
  }
  const bytes = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8)
    bytes.push(parseInt(bits.slice(offset, offset + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30_000)));
  const digest = createHmac("sha1", Buffer.from(bytes))
    .update(counter)
    .digest();
  const offset = digest[digest.length - 1] & 15;
  return ((digest.readUInt32BE(offset) & 0x7fffffff) % 1_000_000)
    .toString()
    .padStart(6, "0");
}

async function freshCode(authenticator: Authenticator): Promise<string> {
  // Enrollment and every privileged write consume a counter. Use real time;
  // never freeze the browser clock or bypass the server's replay protection.
  const earliest = (authenticator.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  authenticator.lastCounter = Math.floor(now / 30_000);
  return totp(authenticator.secret, now);
}

async function fillSecret(field: Locator, value: string) {
  try {
    await field.fill(value);
  } catch {
    throw new Error("Unable to enter synthetic authentication proof");
  }
}

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await fillSecret(page.getByLabel("密码", { exact: true }), password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function changeInitialPassword(
  page: Page,
  initial: string,
  changed: string,
) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await fillSecret(page.getByLabel("当前密码", { exact: true }), initial);
  await fillSecret(page.getByLabel("新密码", { exact: true }), changed);
  await fillSecret(page.getByLabel("确认新密码", { exact: true }), changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
  await fillSecret(page.getByLabel("当前密码", { exact: true }), password);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const now = Date.now();
  await fillSecret(
    page.getByLabel("认证器验证码", { exact: true }),
    totp(secret, now),
  );
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function readJSON(context: BrowserContext, path: string) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return response.json();
}

async function submitMutation(page: Page, path: string, button: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "POST",
    ),
    page.getByRole("button", { name: button, exact: true }).click(),
  ]);
  expect(response.status(), `POST ${path}`).toBe(200);
  const result = await response.json();
  expect(result).toMatchObject({ result: "SUCCESS", sessionRevoked: false });
  return result;
}

type AuditRow = {
  ID: string;
  loginUser: string | null;
  sourceIp: string | null;
  event: string;
  eventArgs: string;
  eventResult: string;
  CreateTm: string;
  logType: number;
  logTypeName: string;
  userId: number | null;
  source: string;
  domainId: string | null;
  availability: Record<string, boolean>;
  deleted: boolean;
  visibilityVersion: number;
};
type AuditList = {
  page: { pageIdx: number; pageSize: number; total: number; totalPage: number };
  List: AuditRow[];
  exhausted: boolean;
};
type ExportHistoryRow = {
  taskUUID: string;
  fileName: string;
  modelType: "Audit";
  fileType: "xlsx";
  state: string;
  progress: number;
  error: string;
  createdAt: string;
  updatedAt: string;
  attempt: number;
  maxAttempts: number;
  nextAttemptAt?: string;
  downloadReady: boolean;
  downloadStatus:
    | "eligible"
    | "not_ready"
    | "snapshot_changed"
    | "artifact_invalid";
  rowCount?: number;
  snapshotAt?: string;
  downloadPath?: string;
};
type ExportHistory = {
  page: AuditList["page"];
  list: ExportHistoryRow[];
  exhausted: boolean;
};

function expectExportReadHeaders(
  headers: Record<string, string>,
  actorID: number,
) {
  expect(headers["x-adtr-user-id"]).toBe(String(actorID));
  expect(headers["cache-control"]).toContain("no-store");
  expect(headers["x-content-type-options"]).toBe("nosniff");
}

function expectHistoryDTO(history: ExportHistory) {
  expect(Object.keys(history).sort()).toEqual(["exhausted", "list", "page"]);
  expect(Array.isArray(history.list)).toBe(true);
  for (const row of history.list) {
    // Reject accidental generic-task fields or artifact storage metadata.
    expect(Object.keys(row).sort()).toEqual(
      [
        "taskUUID",
        "fileName",
        "modelType",
        "fileType",
        "state",
        "progress",
        "error",
        "createdAt",
        "updatedAt",
        "attempt",
        "maxAttempts",
        "downloadReady",
        "downloadStatus",
        ...(row.nextAttemptAt === undefined ? [] : ["nextAttemptAt"]),
        ...(row.downloadReady
          ? ["rowCount", "snapshotAt", "downloadPath"]
          : []),
      ].sort(),
    );
    expect(row.fileName).toBe(`audit-${row.taskUUID}.xlsx`);
    expect(row.modelType).toBe("Audit");
    expect(row.fileType).toBe("xlsx");
    expect(row.createdAt).toMatch(/T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$/);
    expect(row.updatedAt).toMatch(/T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$/);
    expect(row.error).toMatch(/^(?:[a-z][a-z0-9_]{0,63})?$/);
    expect(row.downloadReady).toBe(row.downloadStatus === "eligible");
  }
}

async function readExportHistory(
  context: BrowserContext,
  actorID: number,
  query = "",
): Promise<ExportHistory> {
  const response = await context.request.get(
    `/api/audit/exports/history${query ? `?${query}` : ""}`,
  );
  expect(response.status()).toBe(200);
  expectExportReadHeaders(response.headers(), actorID);
  const history = (await response.json()) as ExportHistory;
  expectHistoryDTO(history);
  return history;
}

const exportColumns = [
  { prop: "userId", label: "用户ID" },
  { prop: "loginUser", label: "登录用户" },
  { prop: "sourceIp", label: "登录IP" },
  { prop: "logTypeName", label: "审计类型" },
  { prop: "event", label: "事件" },
  { prop: "eventArgs", label: "事件参数" },
  { prop: "eventResult", label: "事件结果" },
  { prop: "CreateTm", label: "审计时间" },
] as const;

// Open the actual downloaded package with the standard-library ZIP/XML parsers.
// A .xlsx suffix or PK header alone does not establish a readable workbook.
function workbookRows(bytes: Buffer): string[][] {
  return JSON.parse(
    execFileSync(
      "python3",
      [
        "-c",
        String.raw`
import io, json, sys, zipfile
import xml.etree.ElementTree as ET
ns = {"s": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
with zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read())) as archive:
    assert archive.testzip() is None, "XLSX ZIP checksum failure"
    expected = {"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"}
    assert set(archive.namelist()) == expected, "Unexpected XLSX package parts"
    roots = {name: ET.fromstring(archive.read(name)) for name in expected}
    workbook = roots["xl/workbook.xml"]
    sheets = workbook.findall("s:sheets/s:sheet", ns)
    assert len(sheets) == 1 and sheets[0].attrib["name"] == "Audit"
    for name, root in roots.items():
        if name.endswith(".rels"):
            assert all(element.get("TargetMode") != "External" for element in root)
    sheet = roots["xl/worksheets/sheet1.xml"]
    assert not sheet.findall(".//s:f", ns), "Formula cells are forbidden"
    assert not sheet.findall(".//s:hyperlink", ns), "Hyperlinks are forbidden"
    rows = []
    for index, row in enumerate(sheet.findall("s:sheetData/s:row", ns), 1):
        assert row.get("r") == str(index), "Worksheet rows are not contiguous"
        cells = row.findall("s:c", ns)
        assert 1 <= len(cells) <= 8, "Invalid worksheet column count"
        values = []
        for column, cell in enumerate(cells):
            assert cell.get("r") == chr(65 + column) + str(index)
            assert cell.get("t") == "inlineStr", "Audit values must remain text"
            text = cell.find("s:is/s:t", ns)
            assert text is not None
            values.append(text.text or "")
        rows.append(values)
    assert rows, "Workbook must contain a header row"
    print(json.dumps(rows, ensure_ascii=False))
`,
      ],
      { input: bytes, encoding: "utf8", maxBuffer: 8 * 1024 * 1024 },
    ),
  ) as string[][];
}

async function fillProof(page: Page, password: string, mfa: Authenticator) {
  await fillSecret(
    page.getByLabel("操作者当前密码", { exact: true }),
    password,
  );
  await fillSecret(
    page.getByLabel("未使用的认证器验证码", { exact: true }),
    await freshCode(mfa),
  );
}

async function postFromButton(page: Page, path: string, button: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "POST",
    ),
    page.getByRole("button", { name: button, exact: true }).click(),
  ]);
  expect(response.status(), `POST ${path}`).toBe(200);
  return response.json();
}

async function filteredAudit(page: Page, keyword: string) {
  await page.getByLabel("审计关键词", { exact: true }).fill(keyword);
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === "/api/audit" &&
        candidate.request().method() === "GET",
    ),
    page.getByRole("button", { name: "筛选审计", exact: true }).click(),
  ]);
  expect(response.status()).toBe(200);
  return (await response.json()) as AuditList;
}

async function assertExportCompleted(
  page: Page,
  context: BrowserContext,
  id: string,
  expectedRows: number,
  actorID: number,
) {
  await expect(
    page.getByRole("heading", { name: "审计导出任务", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("导出持久化进度", { exact: true }),
  ).toHaveJSProperty("value", 100, { timeout: 30_000 });
  await expect(
    page.getByRole("button", { name: "下载 XLSX", exact: true }),
  ).toBeVisible();
  const response = await context.request.get(
    `/api/audit/exports/detail?taskUUID=${encodeURIComponent(id)}`,
  );
  expect(response.status()).toBe(200);
  expectExportReadHeaders(response.headers(), actorID);
  const persisted = await response.json();
  expect(persisted).toMatchObject({
    task: {
      taskUUID: id,
      taskName: "audit.export",
      state: "succeeded",
      sourceState: "SUCCESS",
      progress: 100,
      attempt: 1,
    },
    downloadReady: true,
    rowCount: expectedRows,
  });
  const task = await readJSON(
    context,
    `/api/tasks/detail?taskUUID=${encodeURIComponent(id)}`,
  );
  expect(task.events.map((event: { action: string }) => event.action)).toEqual(
    expect.arrayContaining([
      "submitted",
      "leased",
      "started",
      "progress",
      "finished",
    ]),
  );
  expect(persisted.downloadPath).toBe(
    `/api/audit/exports/download?taskUUID=${encodeURIComponent(id)}`,
  );
  return persisted.downloadPath as string;
}

async function reopenExportHistory(page: Page, actorID: number) {
  await page.getByRole("button", { name: "账户概览", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
  // Reopening the workspace must discover persisted exports automatically.
  // No copied task ID, manually filled UUID field or intercepted response.
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === "/api/audit/exports/history" &&
        candidate.request().method() === "GET",
    ),
    page.getByRole("button", { name: "操作审计", exact: true }).click(),
  ]);
  expect(response.status()).toBe(200);
  expectExportReadHeaders(response.headers(), actorID);
  const history = (await response.json()) as ExportHistory;
  expectHistoryDTO(history);
  await page.getByRole("button", { name: "导出历史", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "导出历史", exact: true }),
  ).toBeVisible();
  await expect(page).toHaveURL(/#audit\/history$/);
  return history;
}

// Fresh disposable bootstrap account, API, real Worker and real PostgreSQL are
// mandatory. No route interception, forged permission state, fake clock, direct
// SQL success fixture or fabricated artifact participates in this acceptance.
test("real browser audit history, visibility, XLSX worker export and persisted read-only role", async ({
  page,
  context,
  managerContext,
}, testInfo) => {
  test.setTimeout(600_000);
  const username = process.env.ADTR_E2E_USERNAME;
  const password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "ADTR_E2E_USERNAME and ADTR_E2E_PASSWORD must identify a fresh disposable synthetic bootstrap account",
    );
  const changed = "Synthetic Audit Password 123";
  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const mfa = await enrollMfa(page, changed);
  const me = await readJSON(context, "/api/auth/me");
  const headers = {
    Origin: new URL(page.url()).origin,
    "X-CSRF-Token": me.csrfToken,
  };
  let loginEvent: AuditRow;
  let exportDownloadPath = "";
  const exportIDs: string[] = [];
  const stableQuery = new URLSearchParams({
    keyword: username,
    filterEvent: "login",
    logTypeList: "9",
    createSort: "1",
    pageSize: "-1",
  });

  await test.step("server-captured history, missing metadata, filters and input boundaries", async () => {
    await page.getByRole("button", { name: "操作审计", exact: true }).click();
    const table = page.getByRole("table", {
      name: "操作审计记录",
      exact: true,
    });
    await expect(table).toBeVisible();
    expect(await readExportHistory(context, me.ID)).toEqual({
      page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
      list: [],
      exhausted: true,
    });
    const initial = (await readJSON(
      context,
      "/api/audit?pageSize=-1",
    )) as AuditList;
    expect(initial.page.total).toBeGreaterThanOrEqual(5);
    const bootstrap = initial.List.find((row) => row.event === "bootstrap");
    // Bootstrap runs after migration and captures the new account's username.
    // It has no HTTP request, so only its request metadata remains absent.
    expect(bootstrap).toMatchObject({
      source: "auth",
      loginUser: username,
      userId: me.ID,
      sourceIp: null,
      eventResult: "SUCCESS",
      availability: {
        loginUser: true,
        sourceIp: false,
        path: false,
        requestId: false,
        eventResult: true,
      },
    });
    const missingRow = table
      .getByRole("row")
      .filter({ hasText: bootstrap!.ID });
    await expect(missingRow).toContainText(username);
    await expect(missingRow).toContainText("未记录");
    expect(JSON.parse(bootstrap!.eventArgs)).toEqual({ targetId: me.ID });
    const loginRows = (await readJSON(
      context,
      `/api/audit?${stableQuery}`,
    )) as AuditList;
    expect(loginRows.page).toMatchObject({
      pageIdx: 1,
      pageSize: -1,
      total: 1,
      totalPage: 1,
    });
    expect(loginRows.exhausted).toBe(true);
    loginEvent = loginRows.List[0];
    expect(loginEvent).toMatchObject({
      loginUser: username,
      sourceIp: "127.0.0.1",
      event: "login",
      eventResult: "SUCCESS",
      logType: 9,
      logTypeName: "系统设置",
      userId: me.ID,
      source: "auth",
      domainId: null,
      deleted: false,
      visibilityVersion: 0,
      availability: {
        loginUser: true,
        sourceIp: true,
        path: true,
        requestId: true,
        eventResult: true,
      },
    });
    expect(JSON.parse(loginEvent.eventArgs)).toMatchObject({
      targetId: me.ID,
      path: "/api/auth/login",
      requestId: expect.any(String),
    });
    for (const secret of [password, changed, mfa.secret, me.csrfToken])
      expect(JSON.stringify(initial)).not.toContain(secret);
    const catalogue = await readJSON(context, "/api/audit/types");
    expect(catalogue.events).toEqual(
      expect.arrayContaining([
        "bootstrap",
        "login",
        "password_change",
        "mfa_enable",
      ]),
    );
    expect(catalogue.List).toHaveLength(9);
    expect(
      catalogue.List.find((item: { logType: number }) => item.logType === 9),
    ).toMatchObject({
      logTypeName: "系统设置",
      eventNameList: expect.arrayContaining(["login"]),
    });
    expect(await readJSON(context, "/api/audit/columns")).toEqual({
      columns: exportColumns,
      defaultColumns: exportColumns.map(({ prop }) => prop),
      maxColumns: 8,
    });
    await expect(
      page.getByLabel("审计关键词", { exact: true }),
    ).toHaveAttribute("maxlength", "50");
    await page
      .getByLabel("审计事件筛选", { exact: true })
      .selectOption(["login"]);
    await page.getByLabel("审计类型筛选", { exact: true }).selectOption(["9"]);
    await page.getByLabel("审计排序", { exact: true }).selectOption("1");
    const filtered = await filteredAudit(page, username);
    expect(filtered.List.map((row) => row.ID)).toEqual([loginEvent.ID]);
    await expect(table.getByRole("rowheader")).toHaveCount(1);
    await expect(table).toContainText(loginEvent.ID);
    const timeQuery = new URLSearchParams(stableQuery);
    timeQuery.set("startTm", loginEvent.CreateTm);
    timeQuery.set(
      "endTm",
      new Date(Date.parse(loginEvent.CreateTm) + 1000).toISOString(),
    );
    expect(
      (await readJSON(context, `/api/audit?${timeQuery}`)).List.map(
        (row: AuditRow) => row.ID,
      ),
    ).toEqual([loginEvent.ID]);
    const excluded = new URLSearchParams(stableQuery);
    excluded.set("endTm", loginEvent.CreateTm);
    expect((await readJSON(context, `/api/audit?${excluded}`)).List).toEqual(
      [],
    );
    const second = new URLSearchParams(stableQuery);
    second.set("pageSize", "1");
    second.set("pageIdx", "2");
    expect(await readJSON(context, `/api/audit?${second}`)).toMatchObject({
      page: { total: 1, pageIdx: 2, totalPage: 1 },
      List: [],
      exhausted: true,
    });
    for (const literal of ["%", "_", "not-a-synthetic-user"])
      expect(
        (
          await readJSON(
            context,
            `/api/audit?keyword=${encodeURIComponent(literal)}`,
          )
        ).page.total,
      ).toBe(0);
    for (const query of [
      "pageIdx=0",
      "pageIdx=-1",
      "pageSize=0",
      "createSort=0",
      "pageSize=101",
      "pageSize=-2",
      "pageIdx=2&pageSize=-1",
      "createSort=2",
      "visibility=deleted",
      "logTypeList=0",
      "logTypeList=10",
      "logTypeList=9&logTypeList=9",
      "filterEvent=login&filterEvent=login",
      "filterEvent=%00",
      "startTm=not-a-date",
      "startTm=2026-10-08T00%3A00%3A00Z&endTm=2026-10-07T00%3A00%3A00Z",
      "pageIdx=1&pageIdx=2",
      "unknown=value",
      `keyword=${"x".repeat(51)}`,
    ]) {
      const response = await context.request.get(`/api/audit?${query}`);
      expect(response.status(), query).toBe(400);
      expect(await response.json(), query).toEqual({ error: "invalid_input" });
    }
  });

  // The production ten-sensitive-actions window follows user IDs, not tabs.
  // Bootstrap: password/enroll/confirm (1-3), manager creation (4), all three
  // exports (5-7), snapshot hide/restore (8-9). The export owner never changes.
  // Manager: password/enroll/confirm (1-3), hide/restore/protected rejection
  // (4-6), reader role/user (7-8). The rejected protected write also counts.
  const managerName = "synthetic.audit.manager";
  const managerInitial = "Initial Audit Manager 123";
  const managerChanged = "Changed Audit Manager 456";
  await page.getByRole("button", { name: "访问管理", exact: true }).click();
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(managerName);
  await fillSecret(
    page.getByLabel("初始密码", { exact: true }),
    managerInitial,
  );
  await fillSecret(
    page.getByLabel("确认初始密码", { exact: true }),
    managerInitial,
  );
  await page.getByLabel("角色", { exact: true }).fill("platform_admin");
  await fillProof(page, changed, mfa);
  const manager = await submitMutation(
    page,
    "/api/access/users/create",
    "创建用户",
  );
  expect(manager.ID).toBeGreaterThan(0);
  expect(manager.ID).not.toBe(me.ID);
  const managerPage = await managerContext.newPage();
  await managerPage.goto("/");
  await login(managerPage, managerName, managerInitial);
  await changeInitialPassword(managerPage, managerInitial, managerChanged);
  const managerMfa = await enrollMfa(managerPage, managerChanged);
  const managerMe = await readJSON(managerContext, "/api/auth/me");
  expect(managerMe.ID).toBe(manager.ID);
  expect(managerMe.hasMfa).toBe(true);
  const managerHeaders = {
    Origin: new URL(managerPage.url()).origin,
    "X-CSRF-Token": managerMe.csrfToken,
  };
  await page.getByRole("button", { name: "操作审计", exact: true }).click();

  await test.step("hide and restore preserve the source and protect their own history", async () => {
    const page = managerPage,
      context = managerContext;
    const changed = managerChanged,
      mfa = managerMfa,
      headers = managerHeaders;
    await page.getByRole("button", { name: "操作审计", exact: true }).click();
    await page
      .getByLabel("审计事件筛选", { exact: true })
      .selectOption(["login"]);
    await page.getByLabel("审计类型筛选", { exact: true }).selectOption(["9"]);
    await page.getByLabel("审计排序", { exact: true }).selectOption("1");
    await filteredAudit(page, username);
    await page
      .getByRole("checkbox", { name: `选择 ${loginEvent.ID}`, exact: true })
      .check();
    await page
      .getByRole("button", { name: "隐藏所选记录", exact: true })
      .click();
    await page
      .getByLabel("操作原因", { exact: true })
      .fill("Synthetic visibility acceptance");
    await fillProof(page, changed, mfa);
    expect(
      await postFromButton(page, "/api/audit/delete", "确认隐藏"),
    ).toMatchObject({ result: "success", changed: 1 });
    expect((await readJSON(context, `/api/audit?${stableQuery}`)).List).toEqual(
      [],
    );
    await page.getByLabel("审计可见性", { exact: true }).selectOption("hidden");
    const hidden = await filteredAudit(page, username);
    expect(hidden.List).toHaveLength(1);
    expect(hidden.List[0]).toMatchObject({
      ...loginEvent,
      deleted: true,
      visibilityVersion: 1,
    });
    await page
      .getByRole("checkbox", { name: `选择 ${loginEvent.ID}`, exact: true })
      .check();
    await page
      .getByRole("button", { name: "恢复所选记录", exact: true })
      .click();
    await page
      .getByLabel("操作原因", { exact: true })
      .fill("Synthetic restore acceptance");
    await fillProof(page, changed, mfa);
    expect(
      await postFromButton(page, "/api/audit/restore", "确认恢复"),
    ).toMatchObject({ result: "success", changed: 1 });
    const restored = (await readJSON(
      context,
      `/api/audit?${stableQuery}`,
    )) as AuditList;
    expect(restored.List).toHaveLength(1);
    expect(restored.List[0]).toEqual({
      ...loginEvent,
      deleted: false,
      visibilityVersion: 2,
    });
    const controls = (await readJSON(
      context,
      "/api/audit?filterEvent=audit_hide&filterEvent=audit_restore&createSort=1",
    )) as AuditList;
    expect(controls.List.map((row) => row.event)).toEqual([
      "audit_hide",
      "audit_restore",
    ]);
    expect(JSON.parse(controls.List[0].eventArgs)).toMatchObject({
      targetId: loginEvent.ID,
      reason: "Synthetic visibility acceptance",
      visibilityVersion: 1,
    });
    expect(JSON.parse(controls.List[1].eventArgs)).toMatchObject({
      targetId: loginEvent.ID,
      reason: "Synthetic restore acceptance",
      visibilityVersion: 2,
    });
    const protectedResponse = await context.request.post("/api/audit/delete", {
      headers,
      data: {
        id: [controls.List[0].ID],
        reason: "Synthetic protected history rejection",
        actorPassword: changed,
        totpCode: await freshCode(mfa),
      },
    });
    expect(protectedResponse.status()).toBe(409);
    expect(await protectedResponse.json()).toEqual({
      error: "protected_audit_event",
    });
    expect(
      (await readJSON(context, "/api/audit?filterEvent=audit_hide")).List,
    ).toEqual([controls.List[0]]);
    await page
      .getByRole("button", { name: "清除审计筛选", exact: true })
      .click();
    await expect(
      page.getByRole("checkbox", {
        name: `选择 ${controls.List[0].ID}`,
        exact: true,
      }),
    ).toBeDisabled();
    await expect(page.getByLabel("审计可见性", { exact: true })).toHaveValue(
      "visible",
    );
    await page
      .getByLabel("审计事件筛选", { exact: true })
      .selectOption(["login"]);
    await page.getByLabel("审计类型筛选", { exact: true }).selectOption(["9"]);
    await page.getByLabel("审计排序", { exact: true }).selectOption("1");
    await filteredAudit(page, username);
  });

  await test.step("invalid exports create no task, and 1/8-column exports contain the actual filtered rows", async () => {
    const initialTasks = await readJSON(context, "/api/tasks?pageSize=100");
    for (const selection of [
      undefined,
      null,
      [],
      ["path"],
      ["event", "event"],
      [...exportColumns.map(({ prop }) => prop), "event"],
    ]) {
      const response = await context.request.post("/api/audit/exports", {
        headers,
        data: {
          selectColumn: selection,
          idempotencyKey: randomUUID(),
          actorPassword: changed,
          totpCode: totp(mfa.secret),
        },
      });
      expect(response.status()).toBe(400);
      expect(await response.json()).toEqual({
        error: selection == null ? "invalid_input" : "invalid_columns",
      });
    }
    expect(
      (await readJSON(context, "/api/tasks?pageSize=100")).page.total,
    ).toBe(initialTasks.page.total);
    for (const selected of [exportColumns, [exportColumns[4]]]) {
      // The preceding export leaves/reopens the workspace, resetting its record
      // filters. Keep the next real submission bound to the same known row.
      await page
        .getByLabel("审计事件筛选", { exact: true })
        .selectOption(["login"]);
      await page
        .getByLabel("审计类型筛选", { exact: true })
        .selectOption(["9"]);
      await page.getByLabel("审计排序", { exact: true }).selectOption("1");
      await filteredAudit(page, username);
      const expectedData = (
        (await readJSON(context, `/api/audit?${stableQuery}`)) as AuditList
      ).List;
      await page
        .getByRole("button", { name: "当前筛选导出", exact: true })
        .click();
      for (const column of exportColumns) {
        const checkbox = page.getByRole("checkbox", {
          name: `导出列：${column.label}`,
          exact: true,
        });
        if (selected.some(({ prop }) => prop === column.prop))
          await checkbox.check();
        else await checkbox.uncheck();
      }
      await expect(page.getByLabel("导出幂等键", { exact: true })).toHaveValue(
        /^[0-9a-f-]{36}$/,
      );
      await fillProof(page, changed, mfa);
      const created = await postFromButton(
        page,
        "/api/audit/exports",
        "确认创建导出",
      );
      expect(created.replayed).toBe(false);
      expect(created.taskUUID).toMatch(/^[0-9a-f-]{36}$/);
      exportDownloadPath = await assertExportCompleted(
        page,
        context,
        created.taskUUID,
        expectedData.length,
        me.ID,
      );
      exportIDs.push(created.taskUUID);
      const history = await reopenExportHistory(page, me.ID);
      expect(history.page).toEqual({
        pageIdx: 1,
        pageSize: 20,
        total: exportIDs.length,
        totalPage: 1,
      });
      expect(history.exhausted).toBe(true);
      expect(history.list.map((row) => row.taskUUID)).toEqual(
        [...exportIDs].reverse(),
      );
      const saved = history.list.find(
        (row) => row.taskUUID === created.taskUUID,
      )!;
      expect(saved).toMatchObject({
        taskUUID: created.taskUUID,
        state: "succeeded",
        progress: 100,
        error: "",
        attempt: 1,
        maxAttempts: 3,
        downloadReady: true,
        downloadStatus: "eligible",
        rowCount: expectedData.length,
        snapshotAt: expect.any(String),
        downloadPath: exportDownloadPath,
      });
      if (exportIDs.length === 1) {
        await page.reload();
        await expect(
          page.getByRole("heading", { name: "导出历史", exact: true }),
        ).toBeVisible();
        await expect(page).toHaveURL(/#audit\/history$/);
      }
      const historyRow = page
        .getByRole("table", { name: "导出历史记录", exact: true })
        .getByRole("row")
        .filter({ hasText: created.taskUUID });
      await expect(historyRow).toContainText(saved.fileName);
      await expect(historyRow).toContainText("Audit");
      await expect(historyRow).toContainText(/xlsx/i);
      await historyRow
        .getByRole("button", { name: "查看导出详情", exact: true })
        .click();
      await expect(page).toHaveURL(
        new RegExp(`#audit/detail/${created.taskUUID}$`),
      );
      if (exportIDs.length === 1) {
        await page.goBack();
        await expect(
          page.getByRole("heading", { name: "导出历史", exact: true }),
        ).toBeVisible();
        await page.goForward();
        await expect(page).toHaveURL(
          new RegExp(`#audit/detail/${created.taskUUID}$`),
        );
        await page.reload();
      }
      expect(
        await assertExportCompleted(
          page,
          context,
          created.taskUUID,
          expectedData.length,
          me.ID,
        ),
      ).toBe(exportDownloadPath);
      const [download] = await Promise.all([
        page.waitForEvent("download"),
        page.getByRole("button", { name: "下载 XLSX", exact: true }).click(),
      ]);
      expect(await download.failure()).toBeNull();
      expect(download.suggestedFilename()).toBe(saved.fileName);
      const savedPath = testInfo.outputPath(
        `audit-${selected.length}-columns.xlsx`,
      );
      await download.saveAs(savedPath);
      const browserBytes = readFileSync(savedPath);
      const response = await context.request.get(exportDownloadPath);
      expect(response.status()).toBe(200);
      expectExportReadHeaders(response.headers(), me.ID);
      expect(response.headers()["content-type"]).toContain(
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      );
      expect(response.headers()["content-disposition"]).toBe(
        `attachment; filename="${saved.fileName}"`,
      );
      expect(await response.body()).toEqual(browserBytes);
      const rows = workbookRows(browserBytes);
      expect(rows).toEqual([
        selected.map(({ label }) => label),
        ...expectedData.map((row) =>
          selected.map(({ prop }) => String(row[prop] ?? "")),
        ),
      ]);
      await page
        .getByRole("button", { name: "返回导出历史", exact: true })
        .click();
      await expect(historyRow).toBeVisible();
      await page
        .getByRole("button", { name: "返回审计列表", exact: true })
        .click();
    }
  });

  await test.step("worker audit retains missing login and request metadata", async () => {
    // These records come from the real export worker, whose transaction has no
    // HTTP metadata. The task actor ID must not fabricate a login username.
    await page
      .getByLabel("审计事件筛选", { exact: true })
      .selectOption(["finished"]);
    await page.getByLabel("审计类型筛选", { exact: true }).selectOption(["8"]);
    const finished = await filteredAudit(page, "");
    expect(finished.page.total).toBe(exportIDs.length);
    expect(
      finished.List.map((row) => JSON.parse(row.eventArgs).taskUUID).sort(),
    ).toEqual([...exportIDs].sort());
    const table = page.getByRole("table", {
      name: "操作审计记录",
      exact: true,
    });
    for (const row of finished.List) {
      expect(row).toMatchObject({
        source: "task",
        event: "finished",
        userId: me.ID,
        loginUser: null,
        sourceIp: null,
        eventResult: "SUCCESS",
        availability: {
          loginUser: false,
          sourceIp: false,
          path: false,
          requestId: false,
          eventResult: true,
        },
      });
      expect(JSON.parse(row.eventArgs)).toEqual({
        taskUUID: expect.any(String),
        state: "succeeded",
        attempt: 1,
      });
      const missingRow = table.getByRole("row").filter({
        has: page.getByRole("checkbox", {
          name: `选择 ${row.ID}`,
          exact: true,
        }),
      });
      await expect(
        missingRow
          .getByRole("cell")
          .filter({ hasText: /^未记录（历史数据缺失）/ }),
      ).toHaveCount(2);
      await expect(missingRow).toContainText(`用户 ID：${me.ID}`);
      await missingRow
        .getByText(`查看元数据 ${row.ID}`, { exact: true })
        .click();
      await expect(missingRow).toContainText(
        "未记录：登录用户、登录IP、请求路径、请求标识。历史缺失信息不会用当前账户信息补写。",
      );
    }
    await page.getByLabel("审计事件筛选", { exact: true }).selectOption([]);
    await page.getByLabel("审计类型筛选", { exact: true }).selectOption([]);
    await filteredAudit(page, "");
  });

  await test.step("persisted export history filters and pages use task creation time and canonical states", async () => {
    const first = await readExportHistory(
      context,
      me.ID,
      "modelType=Audit&status=succeeded&sortTm=1&pageSize=1",
    );
    expect(first.page).toEqual({
      pageIdx: 1,
      pageSize: 1,
      total: 2,
      totalPage: 2,
    });
    expect(first.list.map((row) => row.taskUUID)).toEqual([exportIDs[0]]);
    expect(first.exhausted).toBe(false);
    const second = await readExportHistory(
      context,
      me.ID,
      "status=succeeded&sortTm=1&pageSize=1&pageIdx=2",
    );
    expect(second.list.map((row) => row.taskUUID)).toEqual([exportIDs[1]]);
    expect(second.exhausted).toBe(true);
    expect(
      await readExportHistory(context, me.ID, "pageSize=1&pageIdx=3"),
    ).toEqual({
      page: { pageIdx: 3, pageSize: 1, total: 2, totalPage: 2 },
      list: [],
      exhausted: true,
    });
    const creationWindow = new URLSearchParams({
      startTm: first.list[0].createdAt,
      endTm: second.list[0].createdAt,
      pageSize: "-1",
    });
    const bounded = await readExportHistory(
      context,
      me.ID,
      String(creationWindow),
    );
    expect(bounded.page).toEqual({
      pageIdx: 1,
      pageSize: -1,
      total: 1,
      totalPage: 1,
    });
    expect(bounded.list.map((row) => row.taskUUID)).toEqual([exportIDs[0]]);
    expect(
      await readExportHistory(context, me.ID, "status=failed&status=cancelled"),
    ).toEqual({
      page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
      list: [],
      exhausted: true,
    });
    for (const [query, error] of [
      ["pageIdx=01", "invalid_input"],
      ["pageIdx=2&pageSize=-1", "invalid_input"],
      ["status=success", "invalid_input"],
      ["status=succeeded&status=succeeded", "invalid_input"],
      ["visibility=all", "invalid_input"],
      ["modelType=Audit&modelType=Alert", "unsupported_model_type"],
      ["appType=1", "unsupported_app_type"],
    ]) {
      const response = await context.request.get(
        `/api/audit/exports/history?${query}`,
      );
      expect(response.status(), query).toBe(400);
      expect(await response.json(), query).toEqual({ error });
    }
  });

  await test.step("empty filtered export is a header-only workbook, distinct from no selected columns", async () => {
    const empty = await filteredAudit(page, "synthetic-missing-audit-user");
    expect(empty.page.total).toBe(0);
    expect(empty.List).toEqual([]);
    await page
      .getByRole("button", { name: "当前筛选导出", exact: true })
      .click();
    for (const column of exportColumns)
      await page
        .getByRole("checkbox", { name: `导出列：${column.label}`, exact: true })
        .uncheck();
    await expect(page.locator("form button[type=submit]")).toBeDisabled();
    await page
      .getByRole("checkbox", { name: "导出列：事件", exact: true })
      .check();
    await fillProof(page, changed, mfa);
    const emptyExport = await postFromButton(
      page,
      "/api/audit/exports",
      "确认创建导出",
    );
    const path = await assertExportCompleted(
      page,
      context,
      emptyExport.taskUUID,
      0,
      me.ID,
    );
    exportIDs.push(emptyExport.taskUUID);
    const history = await readExportHistory(context, me.ID);
    expect(history.page.total).toBe(exportIDs.length);
    expect(
      history.list.find((row) => row.taskUUID === emptyExport.taskUUID),
    ).toMatchObject({
      state: "succeeded",
      progress: 100,
      downloadReady: true,
      downloadStatus: "eligible",
      rowCount: 0,
      downloadPath: path,
    });
    const response = await context.request.get(path);
    expect(response.status()).toBe(200);
    expectExportReadHeaders(response.headers(), me.ID);
    expect(workbookRows(await response.body())).toEqual([["事件"]]);
    await page
      .getByRole("button", { name: "返回审计列表", exact: true })
      .click();
  });

  await test.step("visibility changes invalidate downloaded snapshots and restoring never revives a stale artifact", async () => {
    await filteredAudit(page, username);
    await page
      .getByRole("checkbox", { name: `选择 ${loginEvent.ID}`, exact: true })
      .check();
    await page
      .getByRole("button", { name: "隐藏所选记录", exact: true })
      .click();
    await page
      .getByLabel("操作原因", { exact: true })
      .fill("Synthetic snapshot revocation acceptance");
    await fillProof(page, changed, mfa);
    expect(
      await postFromButton(page, "/api/audit/delete", "确认隐藏"),
    ).toMatchObject({ result: "success", changed: 1 });
    const stale = await context.request.get(exportDownloadPath);
    expect(stale.status()).toBe(409);
    expect(await stale.json()).toEqual({ error: "export_snapshot_changed" });
    const taskID = new URL(exportDownloadPath, page.url()).searchParams.get(
      "taskUUID",
    )!;
    const changedHistory = await readExportHistory(context, me.ID);
    expect(changedHistory.page.total).toBe(exportIDs.length);
    expect(
      changedHistory.list.find((row) => row.taskUUID === taskID),
    ).toMatchObject({
      state: "succeeded",
      progress: 100,
      downloadReady: false,
      downloadStatus: "snapshot_changed",
    });
    const detail = await context.request.get(
      `/api/audit/exports/detail?taskUUID=${encodeURIComponent(taskID)}`,
    );
    expect(detail.status()).toBe(409);
    expect(await detail.json()).toEqual({ error: "export_snapshot_changed" });
    const genericDetail = await readJSON(
      context,
      `/api/tasks/detail?taskUUID=${encodeURIComponent(taskID)}`,
    );
    expect(genericDetail.task).toMatchObject({
      taskUUID: taskID,
      state: "succeeded",
      result: {},
      cursor: {},
    });
    await page.getByLabel("导出任务 ID", { exact: true }).fill(taskID);
    await page
      .getByRole("button", { name: "查看导出任务", exact: true })
      .click();
    await expect(page.getByRole("alert")).toContainText(
      "导出快照已因隐藏或恢复操作失效",
    );
    await expect(
      page.getByRole("button", { name: "下载 XLSX", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByLabel("导出持久化进度", { exact: true }),
    ).toHaveCount(0);
    await expect(page.getByLabel("导出实际结果", { exact: true })).toHaveCount(
      0,
    );
    await expect(page.getByText("实际导出行数", { exact: true })).toHaveCount(
      0,
    );
    await page
      .getByRole("button", { name: "返回审计列表", exact: true })
      .click();
    await page.getByLabel("审计可见性", { exact: true }).selectOption("hidden");
    const hidden = await filteredAudit(page, username);
    expect(hidden.List).toHaveLength(1);
    expect(hidden.List[0]).toMatchObject({
      ID: loginEvent.ID,
      deleted: true,
      visibilityVersion: 3,
    });
    await expect(
      page.getByRole("button", { name: "当前筛选导出", exact: true }),
    ).toBeDisabled();
    await page
      .getByRole("checkbox", { name: `选择 ${loginEvent.ID}`, exact: true })
      .check();
    await page
      .getByRole("button", { name: "恢复所选记录", exact: true })
      .click();
    await page
      .getByLabel("操作原因", { exact: true })
      .fill("Synthetic source restore after stale export");
    await fillProof(page, changed, mfa);
    expect(
      await postFromButton(page, "/api/audit/restore", "确认恢复"),
    ).toMatchObject({ result: "success", changed: 1 });
    expect((await readJSON(context, `/api/audit?${stableQuery}`)).List).toEqual(
      [{ ...loginEvent, deleted: false, visibilityVersion: 4 }],
    );
    expect((await context.request.get(exportDownloadPath)).status()).toBe(409);
    const restoredHistory = await readExportHistory(context, me.ID);
    expect(restoredHistory.page.total).toBe(exportIDs.length);
    expect(
      restoredHistory.list.find((row) => row.taskUUID === taskID),
    ).toMatchObject({
      state: "succeeded",
      progress: 100,
      downloadReady: false,
      downloadStatus: "snapshot_changed",
    });
  });

  await test.step("persisted read-only audit grants survive save/readback and deny valid authenticated writes", async () => {
    const page = managerPage,
      context = managerContext;
    const changed = managerChanged,
      mfa = managerMfa;
    await page.getByRole("button", { name: "访问管理", exact: true }).click();
    await page.getByRole("button", { name: "角色管理", exact: true }).click();
    await page.getByRole("button", { name: "新增角色", exact: true }).click();
    await page
      .getByLabel("角色名称", { exact: true })
      .fill("Synthetic Audit Reader");
    await page
      .getByRole("checkbox", { name: "操作审计：读取", exact: true })
      .check();
    await page
      .getByRole("checkbox", { name: "审计导出：读取", exact: true })
      .check();
    await fillProof(page, changed, mfa);
    const saved = await submitMutation(
      page,
      "/api/access/roles/save",
      "保存角色",
    );
    const role = await readJSON(
      context,
      `/api/access/roles/detail?roleID=${encodeURIComponent(saved.roleID)}`,
    );
    expect(
      role.permissions.map(({ mark, auth }: Permission) => ({ mark, auth })),
    ).toEqual([
      { mark: "users", auth: { readable: false, writeable: false } },
      { mark: "roles", auth: { readable: false, writeable: false } },
      { mark: "permissions", auth: { readable: false, writeable: false } },
      { mark: "tasks", auth: { readable: false, writeable: false } },
      { mark: "audit", auth: { readable: true, writeable: false } },
      { mark: "audit_exports", auth: { readable: true, writeable: false } },
      { mark: "system", auth: { readable: false, writeable: false } },
      { mark: "schedules", auth: { readable: false, writeable: false } },
      { mark: "task_archive", auth: { readable: false, writeable: false } },
      { mark: "domains", auth: { readable: false, writeable: false } },
      {
        mark: "operation_accounts",
        auth: { readable: false, writeable: false },
      },
      { mark: "system_logs", auth: { readable: false, writeable: false } },
    ]);
    await page.getByRole("button", { name: "用户管理", exact: true }).click();
    await page.getByRole("button", { name: "新增用户", exact: true }).click();
    const readerName = "synthetic.audit.reader";
    const readerInitial = "Initial Audit Reader 123";
    const readerChanged = "Changed Audit Reader 456";
    await page.getByLabel("用户名", { exact: true }).fill(readerName);
    await fillSecret(
      page.getByLabel("初始密码", { exact: true }),
      readerInitial,
    );
    await fillSecret(
      page.getByLabel("确认初始密码", { exact: true }),
      readerInitial,
    );
    await page.getByLabel("角色", { exact: true }).fill(saved.roleID);
    await fillProof(page, changed, mfa);
    await submitMutation(page, "/api/access/users/create", "创建用户");
    const readerContext = await context.browser()!.newContext({
      baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
    });
    try {
      const exportDetailPath = exportDownloadPath.replace(
        "/exports/download?",
        "/exports/detail?",
      );
      // No cookie/session can discover a history or read a known artifact.
      for (const path of [
        "/api/audit/exports/history",
        exportDetailPath,
        exportDownloadPath,
      ]) {
        const response = await readerContext.request.get(path);
        expect(response.status(), path).toBe(401);
        expect(response.headers()["x-adtr-user-id"]).toBeUndefined();
      }
      const reader = await readerContext.newPage();
      await reader.goto("/");
      await login(reader, readerName, readerInitial);
      await changeInitialPassword(reader, readerInitial, readerChanged);
      const readerMfa = await enrollMfa(reader, readerChanged);
      await reader
        .getByRole("button", { name: "操作审计", exact: true })
        .click();
      await expect(
        reader.getByRole("table", { name: "操作审计记录", exact: true }),
      ).toBeVisible();
      await expect(
        reader.getByRole("button", { name: "隐藏所选记录", exact: true }),
      ).toHaveCount(0);
      await expect(
        reader.getByRole("button", { name: "恢复所选记录", exact: true }),
      ).toHaveCount(0);
      await expect(
        reader.getByRole("button", { name: "当前筛选导出", exact: true }),
      ).toHaveCount(0);
      const readerMe = await readJSON(readerContext, "/api/auth/me");
      expect(readerMe.hasMfa).toBe(true);
      const readerHeaders = {
        Origin: new URL(reader.url()).origin,
        "X-CSRF-Token": readerMe.csrfToken,
      };
      const permissionCheck = await readerContext.request.post(
        "/api/access/check",
        {
          headers: readerHeaders,
          data: {
            paths: [
              "GET /api/audit",
              "GET /api/audit/types",
              "GET /api/audit/columns",
              "POST /api/audit/delete",
              "POST /api/audit/restore",
              "POST /api/audit/exports",
              "GET /api/audit/exports/history",
              "GET /api/audit/exports/detail",
              "GET /api/audit/exports/download",
            ],
          },
        },
      );
      expect(permissionCheck.status()).toBe(200);
      expect(await permissionCheck.json()).toEqual({
        results: [true, true, true, false, false, false, true, true, true],
      });
      // Dedicated read grants suffice without tasks.readable or write grants;
      // the same tenant's other actor still cannot discover the owner's rows.
      expect((await readerContext.request.get("/api/tasks")).status()).toBe(
        403,
      );
      expect(await readExportHistory(readerContext, readerMe.ID)).toEqual({
        page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
        list: [],
        exhausted: true,
      });
      const forgedActor = await readerContext.request.get(
        "/api/audit/exports/history",
        { headers: { "X-ADTR-User-ID": String(me.ID) } },
      );
      expect(forgedActor.status()).toBe(200);
      expectExportReadHeaders(forgedActor.headers(), readerMe.ID);
      expect(await forgedActor.json()).toEqual({
        page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
        list: [],
        exhausted: true,
      });
      const proof = {
        actorPassword: readerChanged,
        totpCode: await freshCode(readerMfa),
      };
      for (const [path, data] of [
        [
          "/api/audit/delete",
          { id: [loginEvent.ID], reason: "Synthetic denied hide" },
        ],
        [
          "/api/audit/restore",
          { id: [loginEvent.ID], reason: "Synthetic denied restore" },
        ],
        [
          "/api/audit/exports",
          { selectColumn: ["event"], idempotencyKey: randomUUID() },
        ],
      ] as const) {
        const denied = await readerContext.request.post(path, {
          headers: readerHeaders,
          data: { ...data, ...proof },
        });
        expect(denied.status(), path).toBe(403);
        expect(await denied.json(), path).toEqual({ error: "forbidden" });
      }
      for (const visibility of ["hidden", "all"])
        expect(
          (
            await readerContext.request.get(
              `/api/audit?visibility=${visibility}`,
            )
          ).status(),
        ).toBe(403);
      const visible = (await readJSON(
        readerContext,
        `/api/audit?${stableQuery}`,
      )) as AuditList;
      expect(visible.List.map((row) => row.ID)).toEqual([loginEvent.ID]);
      expect(visible.List[0]).toMatchObject({
        deleted: false,
        visibilityVersion: 4,
      });
      // Permission alone never grants another user's artifact ownership.
      for (const path of [exportDetailPath, exportDownloadPath]) {
        const response = await readerContext.request.get(path);
        expect(response.status(), path).toBe(404);
        expect(response.headers()["x-adtr-user-id"]).toBeUndefined();
      }
      await reader
        .getByRole("button", { name: "导出历史", exact: true })
        .click();
      await expect(
        reader.getByRole("heading", { name: "导出历史", exact: true }),
      ).toBeVisible();
      await expect(
        reader.getByRole("table", { name: "导出历史记录", exact: true }),
      ).toBeVisible();
      await expect(
        reader
          .getByRole("table", { name: "导出历史记录", exact: true })
          .locator("tbody tr"),
      ).toHaveCount(0);
      await expect(
        reader.getByText("当前页没有符合条件的可见导出。", { exact: true }),
      ).toBeVisible();
      for (const id of exportIDs)
        await expect(
          reader.getByText(`audit-${id}.xlsx`, { exact: true }),
        ).toHaveCount(0);
      await reader
        .getByRole("button", { name: "退出登录", exact: true })
        .click();
      await expect(
        reader.getByRole("heading", { name: "登录账户", exact: true }),
      ).toBeVisible();
      expect((await readerContext.request.get("/api/audit")).status()).toBe(
        401,
      );
      for (const path of [
        "/api/audit/exports/history",
        exportDetailPath,
        exportDownloadPath,
      ])
        expect((await readerContext.request.get(path)).status(), path).toBe(
          401,
        );
      await expect(
        reader.getByRole("table", { name: "导出历史记录", exact: true }),
      ).toHaveCount(0);
    } finally {
      await readerContext.close();
    }
  });
});
