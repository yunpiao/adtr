import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac, randomUUID } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";

// This suite requires the actual migrated API and worker from the owning
// disposable harness. It never inserts journal rows or substitutes a producer.
type Event = {
  schemaVersion: number;
  eventId: string;
  processId: string;
  module: string;
  code: string;
  outcome: string;
  severity: string;
  reason: string;
  observedAt: string;
  recordedAt: string;
};
type Selection = { startTm: string; endTm: string; systemType: string[] };
type Authenticator = { secret: string; lastCounter: number };
function totp(secret: string, now = Date.now()) {
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
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30000)));
  const digest = createHmac("sha1", Buffer.from(bytes))
      .update(counter)
      .digest(),
    offset = digest[digest.length - 1] & 15;
  return ((digest.readUInt32BE(offset) & 0x7fffffff) % 1000000)
    .toString()
    .padStart(6, "0");
}
async function freshCode(auth: Authenticator) {
  const earliest = (auth.lastCounter + 1) * 30000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  auth.lastCounter = Math.floor(now / 30000);
  return totp(auth.secret, now);
}
async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}
async function changePassword(page: Page, old: string, next: string) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await page.getByLabel("当前密码", { exact: true }).fill(old);
  await page.getByLabel("新密码", { exact: true }).fill(next);
  await page.getByLabel("确认新密码", { exact: true }).fill(next);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}
async function enroll(page: Page, password: string): Promise<Authenticator> {
  await page.getByRole("button", { name: "多因素认证", exact: true }).click();
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
      .getByLabel("设置密钥", { exact: true })
      .inputValue(),
    now = Date.now();
  await page
    .getByLabel("认证器验证码", { exact: true })
    .fill(totp(secret, now));
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30000) };
}
function readHeaders(headers: Record<string, string>, actor: number) {
  expect(headers["x-adtr-user-id"]).toBe(String(actor));
  expect(headers["cache-control"]).toContain("no-store");
  expect(headers["x-content-type-options"]).toBe("nosniff");
}
async function read(context: BrowserContext, path: string, actor?: number) {
  const r = await context.request.get(path);
  expect(r.status(), `GET ${path}`).toBe(200);
  if (actor) readHeaders(r.headers(), actor);
  return r.json();
}
async function proof(page: Page, auth: Authenticator, password: string) {
  await page.getByLabel("操作者当前密码", { exact: true }).fill(password);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(auth));
}
function inspectZip(
  bytes: Buffer,
  expected: { taskUUID: string; selection: Selection },
  initialIDs: string[] = [],
) {
  // Independently inspect the real container, entry names, digest, row count,
  // safe schema, half-open range, deterministic order and first/last records.
  const result = JSON.parse(
    execFileSync(
      "python3",
      [
        "-c",
        String.raw`
import sys,io,json,zipfile,hashlib,datetime
raw=sys.stdin.buffer.read()
assert 0<len(raw)<=16*1024*1024
archive=zipfile.ZipFile(io.BytesIO(raw))
assert archive.namelist()==['manifest.json','events.jsonl']
assert len(archive.infolist())==2 and all(not i.is_dir() for i in archive.infolist())
assert archive.testzip() is None
manifest=json.loads(archive.read('manifest.json'))
payload=archive.read('events.jsonl')
assert manifest['formatVersion']==1
assert set(manifest)=={'formatVersion','taskUUID','snapshotAt','rowCount','selection','coverage','jsonlSHA256'}
assert manifest['jsonlSHA256']==hashlib.sha256(payload).hexdigest()
assert manifest['coverage']['mode']=='best_effort' and manifest['coverage']['gapsPossible'] is True
events=[json.loads(line) for line in payload.splitlines()]
assert manifest['rowCount']==len(events)<=10000
fields={'schemaVersion','eventId','processId','module','code','outcome','severity','reason','observedAt','recordedAt'}
stamp=lambda x:datetime.datetime.fromisoformat(x.replace('Z','+00:00'))
start,end=stamp(manifest['selection']['startTm']),stamp(manifest['selection']['endTm'])
assert start<end and end-start<=datetime.timedelta(hours=24)
ids=set()
for event in events:
 assert set(event)==fields and event['schemaVersion']==1
 assert event['module'] in manifest['selection']['systemType']
 assert start<=stamp(event['recordedAt'])<end
 assert event['eventId'] not in ids
 ids.add(event['eventId'])
 assert event['code'] in ['service_start_requested','service_stopped','service_failed','queue_cycle','recovery_cycle','scheduler_cycle','queue_progress']
 assert event['reason'] in ['','serve_failed','worker_failed','cycle_failed']
assert events==sorted(events,key=lambda e:(stamp(e['recordedAt']),e['eventId']),reverse=True)
print(json.dumps({'manifest':manifest,'events':events,'first':events[0] if events else None,'last':events[-1] if events else None}))
`,
      ],
      { input: bytes, maxBuffer: 20 * 1024 * 1024 },
    ).toString(),
  );
  expect(result.manifest.taskUUID).toBe(expected.taskUUID);
  expect(result.manifest.selection).toEqual(expected.selection);
  expect(result.events.map((event: Event) => event.eventId)).toEqual(
    expect.arrayContaining(initialIDs),
  );
  return result as {
    manifest: { rowCount: number; selection: Selection };
    events: Event[];
    first: Event | null;
    last: Event | null;
  };
}

test("real API/worker journal → lost-submit recovery → protected ZIP and owner/read boundaries", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(480000);
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD,
    workerPID = Number(process.env.ADTR_E2E_WORKER_PID);
  if (
    !username ||
    !password ||
    !Number.isSafeInteger(workerPID) ||
    workerPID <= 1
  )
    throw new Error(
      "Provide disposable bootstrap credentials and the owning harness ADTR_E2E_WORKER_PID",
    );
  const command = readFileSync(`/proc/${workerPID}/cmdline`, "utf8").split(
    "\0",
  );
  expect(command[0]).toMatch(/(?:^|\/)adtr$/);
  expect(command).toEqual(expect.arrayContaining(["-mode", "worker"]));
  const changed = "Synthetic Operational Password 123";
  await page.goto("/");
  await login(page, username, password);
  await changePassword(page, password, changed);
  const mfa = await enroll(page, changed),
    me = await read(context, "/api/auth/me");
  const headers = {
    Origin: new URL(page.url()).origin,
    "X-CSRF-Token": me.csrfToken,
  };
  await page
    .getByRole("button", { name: "运行日志与诊断包", exact: true })
    .click();
  await expect(page.getByRole("table", { name: "运行事件记录" })).toBeVisible();
  await expect(page.getByText(/部署后新增的 api \/ worker/)).toBeVisible();
  let produced: { List: { event: Event; log: string }[]; selection: Selection };
  await expect
    .poll(
      async () => {
        produced = await read(context, "/api/system/logs?pageSize=100", me.ID);
        return (
          produced.List.some(
            ({ event }) =>
              event.module === "api" &&
              event.code === "service_start_requested",
          ) &&
          produced.List.some(
            ({ event }) =>
              event.module === "worker" &&
              ["queue_cycle", "recovery_cycle", "scheduler_cycle"].includes(
                event.code,
              ),
          )
        );
      },
      { timeout: 30000 },
    )
    .toBe(true);
  produced = await read(context, "/api/system/logs?pageSize=100", me.ID);
  let apiStart = produced.List.find(
      ({ event }) =>
        event.module === "api" && event.code === "service_start_requested",
    ),
    workerCycle = produced.List.find(
      ({ event }) =>
        event.module === "worker" &&
        ["queue_cycle", "recovery_cycle", "scheduler_cycle"].includes(
          event.code,
        ),
    );
  expect(apiStart?.event.outcome).toBe("attempted");
  expect(workerCycle).toBeTruthy();
  for (const row of produced.List)
    expect(JSON.parse(row.log)).toEqual(row.event);
  const source = await read(context, "/api/system/logs/sources", me.ID);
  expect(
    source.modules.map((m: { module: string }) => m.module).sort(),
  ).toEqual(["api", "worker"]);
  expect(source.reports.length).toBeGreaterThan(0);
  expect(
    source.reports.every(
      (r: { evidence: string }) =>
        r.evidence === "incomplete_process_observation",
    ),
  ).toBe(true);
  await page.getByRole("button", { name: "记录来源", exact: true }).click();
  await expect(page.getByText(/可能过期的不完整观察/)).toBeVisible();
  await page.getByRole("button", { name: "事件查询", exact: true }).click();
  await expect(page.getByRole("table", { name: "运行事件记录" })).toBeVisible();
  // Capture the actual UI query after observing both real producers. Its
  // echoed window is the one the user will explicitly confirm for packaging.
  await page.getByLabel("每页事件数", { exact: true }).selectOption("100");
  const [uiQuery] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/system/logs" &&
        r.request().method() === "GET",
    ),
    page.getByRole("button", { name: "查询事件", exact: true }).click(),
  ]);
  expect(uiQuery.status()).toBe(200);
  produced = await uiQuery.json();
  apiStart = produced.List.find(
    ({ event }) =>
      event.module === "api" && event.code === "service_start_requested",
  );
  workerCycle = produced.List.find(
    ({ event }) =>
      event.module === "worker" &&
      ["queue_cycle", "recovery_cycle", "scheduler_cycle"].includes(event.code),
  );
  expect(apiStart?.event.outcome).toBe("attempted");
  expect(workerCycle).toBeTruthy();
  await expect(
    page.getByRole("button", { name: "确认当前筛选并打包", exact: true }),
  ).toBeVisible();
  for (const query of [
    "systemType=api&systemType=ldap",
    "pageSize=-1&pageIdx=2",
    "startTm=2026-10-07T00:00:00Z",
    "userInfo=1",
  ]) {
    const r = await context.request.get(`/api/system/logs?${query}`);
    expect(r.status()).toBe(400);
  }
  const wrongOrigin = await context.request.post("/api/system/logs/bundles", {
    headers: { ...headers, Origin: "https://untrusted.invalid" },
    data: {
      ...produced.selection,
      idempotencyKey: randomUUID(),
      actorPassword: changed,
      totpCode: "000000",
    },
  });
  expect(wrongOrigin.status()).toBe(403);

  // Lose only the response after a real authenticated mutation has committed.
  let submitted:
      | { taskUUID: string; task: { taskUUID: string }; replayed: boolean }
      | undefined,
    submittedBody: Record<string, unknown> | undefined;
  await page.route("**/api/system/logs/bundles", async (route) => {
    submittedBody = route.request().postDataJSON();
    const result = await route.fetch();
    expect(result.status()).toBe(200);
    readHeaders(result.headers(), me.ID);
    submitted = await result.json();
    await route.abort("failed");
  });
  await page
    .getByRole("button", { name: "确认当前筛选并打包", exact: true })
    .click();
  await proof(page, mfa, changed);
  await page
    .getByRole("button", { name: "提交诊断包任务", exact: true })
    .click();
  await expect(page.getByRole("alert")).toContainText("结果尚未确认");
  expect(submitted?.taskUUID).toBeTruthy();
  const taskID = submitted!.taskUUID,
    selected = {
      startTm: submittedBody!.startTm,
      endTm: submittedBody!.endTm,
      systemType: submittedBody!.systemType,
    } as Selection;
  expect(selected).toEqual(produced.selection);
  await page.unroute("**/api/system/logs/bundles");
  await page.reload();
  await expect(
    page.getByRole("button", { name: "用原幂等键重试打包", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("操作者当前密码", { exact: true })).toHaveValue(
    "",
  );
  await page.getByRole("button", { name: "核对打包历史", exact: true }).click();
  await expect(
    page.getByRole("button", { name: `查看诊断包 ${taskID}`, exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "核对未确认打包", exact: true })
    .click();
  await proof(page, mfa, changed);
  const [replay] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/system/logs/bundles" &&
        r.request().method() === "POST",
    ),
    page
      .getByRole("button", { name: "用原幂等键重试打包", exact: true })
      .click(),
  ]);
  expect(replay.status()).toBe(200);
  expect(await replay.json()).toMatchObject({
    taskUUID: taskID,
    replayed: true,
  });
  expect(replay.request().postDataJSON().idempotencyKey).toBe(
    submittedBody!.idempotencyKey,
  );
  await expect(
    page.getByRole("button", { name: "下载 ZIP", exact: true }),
  ).toBeVisible({ timeout: 30000 });
  const persisted = await read(
    context,
    `/api/system/logs/bundles/detail?taskUUID=${taskID}`,
    me.ID,
  );
  expect(persisted).toMatchObject({
    task: {
      state: "succeeded",
      attempt: 1,
      maxAttempts: 1,
      result: {},
      cursor: {},
    },
    downloadReady: true,
    artifactStatus: "eligible",
  });
  const generic = await read(context, `/api/tasks/detail?taskUUID=${taskID}`);
  expect(generic.task.result).toEqual({});
  expect(generic.task.cursor).toEqual({});
  const artifact = await context.request.get(
    `/api/system/logs/bundles/download?taskUUID=${taskID}`,
  );
  expect(artifact.status()).toBe(200);
  readHeaders(artifact.headers(), me.ID);
  expect(artifact.headers()["content-type"]).toBe("application/zip");
  expect(artifact.headers()["content-disposition"]).toBe(
    `attachment; filename="system-logs-${taskID}.zip"`,
  );
  const bytes = await artifact.body(),
    inspected = inspectZip(bytes, { taskUUID: taskID, selection: selected }, [
      apiStart!.event.eventId,
      workerCycle!.event.eventId,
    ]);
  expect(inspected.manifest.rowCount).toBe(persisted.rowCount);
  expect(inspected.first).toBeTruthy();
  expect(inspected.last).toBeTruthy();
  const producerEvidence = Buffer.from(
    JSON.stringify(
      {
        first: inspected.first,
        last: inspected.last,
        rowCount: inspected.manifest.rowCount,
      },
      null,
      2,
    ),
  );
  writeFileSync(
    testInfo.outputPath("production-journal-first-last.json"),
    producerEvidence,
  );
  await testInfo.attach("production-journal-first-last.json", {
    body: producerEvidence,
    contentType: "application/json",
  });
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "下载 ZIP", exact: true }).click(),
  ]);
  expect(download.suggestedFilename()).toBe(`system-logs-${taskID}.zip`);
  const downloadPath = await download.path();
  expect(downloadPath).toBeTruthy();
  expect(readFileSync(downloadPath!)).toEqual(bytes);
  await page.screenshot({
    path: testInfo.outputPath("operational-logs.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "打包历史", exact: true }).click();
  await expect(
    page.getByRole("button", { name: `查看诊断包 ${taskID}`, exact: true }),
  ).toBeVisible();
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "诊断包详情", exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "下载 ZIP", exact: true }),
  ).toBeVisible();

  // A genuine empty time window still yields a real two-entry ZIP with an
  // explicit zero-row manifest, not fake producer records.
  await page.getByRole("button", { name: "事件查询", exact: true }).click();
  // datetime-local canonicalizes zero seconds away; Playwright fill requires
  // the exact canonical input value. These remain the same UTC instants.
  await page
    .getByLabel("入库开始时间（UTC）", { exact: true })
    .fill("2000-01-01T00:00");
  await page
    .getByLabel("入库结束时间（UTC，不包含）", { exact: true })
    .fill("2000-01-01T01:00");
  await page.getByRole("button", { name: "查询事件", exact: true }).click();
  await expect(page.getByText(/该范围没有已记录事件/)).toBeVisible();
  await page
    .getByRole("button", { name: "确认当前筛选并打包", exact: true })
    .click();
  await proof(page, mfa, changed);
  const [emptySubmit] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/system/logs/bundles" &&
        r.request().method() === "POST",
    ),
    page.getByRole("button", { name: "提交诊断包任务", exact: true }).click(),
  ]);
  expect(emptySubmit.status()).toBe(200);
  const emptyID = (await emptySubmit.json()).taskUUID;
  await expect(page.getByText(/此快照没有已记录事件/)).toBeVisible({
    timeout: 30000,
  });
  const emptyZIP = await context.request.get(
    `/api/system/logs/bundles/download?taskUUID=${emptyID}`,
  );
  expect(emptyZIP.status()).toBe(200);
  const empty = inspectZip(await emptyZIP.body(), {
    taskUUID: emptyID,
    selection: {
      startTm: "2000-01-01T00:00:00Z",
      endTm: "2000-01-01T01:00:00Z",
      systemType: ["api", "worker"],
    },
  });
  expect(empty.events).toEqual([]);
  expect(empty.first).toBeNull();
  expect(empty.last).toBeNull();
  const control = await read(
    context,
    "/api/audit?filterEvent=system_logs_bundle_submit&pageSize=100",
  );
  expect(
    control.List.filter(
      (row: { source: string }) => row.source === "operational_log",
    ),
  ).toHaveLength(2);
  expect(
    control.List.every((row: { deletable: boolean }) => !row.deletable),
  ).toBe(true);

  // Create a real default-denied custom role, then grant only dedicated reads.
  async function adminWrite(path: string, data: Record<string, unknown>) {
    const r = await context.request.post(path, {
      headers,
      data: { ...data, actorPassword: changed, totpCode: await freshCode(mfa) },
    });
    expect(r.status(), path).toBe(200);
    return r.json();
  }
  const role = await adminWrite("/api/access/roles/save", {
    roleName: "Synthetic log reader",
    permissions: [
      { mark: "system", auth: { readable: true, writeable: false } },
    ],
  });
  const roleDetail = await read(
    context,
    `/api/access/roles/detail?roleID=${role.roleID}`,
  );
  expect(
    roleDetail.permissions.find(
      (p: { mark: string }) => p.mark === "system_logs",
    ).auth,
  ).toEqual({ readable: false, writeable: false });
  const readerName = "synthetic.logs.reader",
    readerInitial = "Initial Logs Reader 123",
    readerChanged = "Changed Logs Reader 456";
  await adminWrite("/api/access/users/create", {
    username: readerName,
    password: readerInitial,
    roleID: role.roleID,
  });
  const readerContext = await context.browser()!.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
  });
  try {
    const reader = await readerContext.newPage();
    await reader.goto("/");
    await login(reader, readerName, readerInitial);
    await changePassword(reader, readerInitial, readerChanged);
    await reader
      .getByRole("button", { name: "运行日志与诊断包", exact: true })
      .click();
    await expect(
      reader.getByText(/当前账户没有此运行日志操作权限/),
    ).toBeVisible();
    expect((await readerContext.request.get("/api/system/logs")).status()).toBe(
      403,
    );
    await adminWrite("/api/access/permissions/save", {
      roleID: role.roleID,
      permissions: [
        { mark: "system", auth: { readable: true, writeable: false } },
        { mark: "system_logs", auth: { readable: true, writeable: false } },
      ],
    });
    await reader.reload();
    await login(reader, readerName, readerChanged);
    await expect(
      reader.getByRole("heading", { name: "账户概览", exact: true }),
    ).toBeVisible();
    await reader
      .getByRole("button", { name: "运行日志与诊断包", exact: true })
      .click();
    await expect(
      reader.getByRole("table", { name: "运行事件记录" }),
    ).toBeVisible();
    await expect(
      reader.getByRole("button", { name: "确认当前筛选并打包", exact: true }),
    ).toHaveCount(0);
    const readerMe = await read(readerContext, "/api/auth/me");
    await read(readerContext, "/api/system/logs/sources", readerMe.ID);
    const readerHistory = await read(
      readerContext,
      "/api/system/logs/bundles/history",
      readerMe.ID,
    );
    expect(readerHistory.page.total).toBe(0);
    expect((await readerContext.request.get("/api/tasks")).status()).toBe(403);
    expect(
      (
        await readerContext.request.get(
          `/api/system/logs/bundles/detail?taskUUID=${taskID}`,
        )
      ).status(),
    ).toBe(404);
    expect(
      (
        await readerContext.request.get(
          `/api/system/logs/bundles/download?taskUUID=${taskID}`,
        )
      ).status(),
    ).toBe(404);
    const denied = await readerContext.request.post(
      "/api/system/logs/bundles",
      {
        headers: {
          Origin: new URL(reader.url()).origin,
          "X-CSRF-Token": readerMe.csrfToken,
        },
        data: {
          ...selected,
          idempotencyKey: randomUUID(),
          actorPassword: readerChanged,
          totpCode: "000000",
        },
      },
    );
    expect(denied.status()).toBe(403);
  } finally {
    await readerContext.close();
  }
});
