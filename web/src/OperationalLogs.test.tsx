// Synthetic DOM/transport evidence only. The production emitter → PostgreSQL
// → worker → ZIP browser evidence is in e2e/operational-logs.spec.ts.
import { StrictMode } from "react";
import { Blob as NodeBlob } from "node:buffer";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import OperationalLogsWorkspace from "./OperationalLogsWorkspace";
import { type Profile } from "./api";
import {
  bundleDownloadPath,
  bundleFilename,
  emptyBundleQuery,
  emptyLogQuery,
  logAPI,
  logOperations,
  logQuery,
  parseBundleDetail,
  parseBundleHistory,
  parseLogList,
  parseSources,
  validSelection,
  type BundleDetail,
  type LogEvent,
  type Selection,
} from "./operational-log-api";
import {
  beginBundleIntent,
  bundleIntent,
  discardBundleIntent,
  markBundleAttempted,
} from "./operational-log-intent";

const id = "00000000-0000-4000-8000-000000000001",
  eventID = "00000000-0000-4000-8000-000000000002";
const profile: Profile = {
  ID: 1,
  username: "synthetic-admin",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "test-log-csrf",
};
const selection: Selection = {
  startTm: "2026-10-06T01:00:00.000001Z",
  endTm: "2026-10-07T01:00:00.000001Z",
  systemType: ["api", "worker"],
};
const coverage = {
  mode: "best_effort",
  gapsPossible: true,
  journalCreatedAt: "2026-10-07T00:00:00Z",
  firstRecordedAt: "2026-10-07T00:00:01Z",
};
const event = (): LogEvent => ({
  schemaVersion: 1,
  eventId: eventID,
  processId: id,
  module: "api",
  code: "service_start_requested",
  outcome: "attempted",
  severity: "info",
  reason: "",
  observedAt: "2026-10-07T00:00:00.000001Z",
  recordedAt: "2026-10-07T00:00:01.000002Z",
});
const page = (count = 1) => ({
  pageIdx: 1,
  pageSize: 20,
  total: count,
  totalPage: count ? 1 : 0,
});
const list = (events = [event()]) => ({
  page: page(events.length),
  List: events.map((event) => ({ event, log: JSON.stringify(event) })),
  exhausted: true,
  selection,
  coverage,
});
const sources = () => ({
  modules: [
    {
      module: "api",
      registered: true,
      firstRecordedAt: coverage.firstRecordedAt,
      lastRecordedAt: coverage.firstRecordedAt,
      recordedCount: 1,
    },
    {
      module: "worker",
      registered: true,
      firstRecordedAt: null,
      lastRecordedAt: null,
      recordedCount: 0,
    },
  ],
  coverage,
  reports: [
    {
      processId: id,
      module: "api",
      startedAt: "2026-10-07T00:00:00Z",
      observedAt: "2026-10-07T00:00:01Z",
      accepted: 3,
      acknowledged: 1,
      rejected: 0,
      queueFull: 1,
      writeFailures: 2,
      unacknowledged: 1,
      abandoned: 0,
      acceptanceStopped: false,
      reportedAt: "2026-10-07T00:00:02Z",
      evidence: "incomplete_process_observation",
    },
  ],
  reportsTruncated: false,
});
const detail = (
  state: BundleDetail["task"]["state"] = "succeeded",
): BundleDetail => ({
  task: {
    taskUUID: id,
    taskName: "system.logs_bundle",
    domainId: "platform",
    payloadVersion: 1,
    state,
    sourceState: state === "succeeded" ? "SUCCESS" : "PENDING",
    error: "",
    createdAt: "2026-10-07T00:00:02Z",
    updatedAt: "2026-10-07T00:00:03Z",
    attempt: 1,
    maxAttempts: 1,
    progress: state === "succeeded" ? 100 : 0,
    resultVersion: 1,
    result: {},
    cursor: {},
    parentTaskUUID: "",
    terminalAt: state === "succeeded" ? "2026-10-07T00:00:03Z" : null,
    archived: false,
    visibilityVersion: 0,
  },
  downloadReady: state === "succeeded",
  artifactStatus: state === "succeeded" ? "eligible" : "not_ready",
  ...(state === "succeeded"
    ? {
        rowCount: 1,
        snapshotAt: "2026-10-07T00:00:02Z",
        downloadPath: bundleDownloadPath(id),
        coverage: coverage as BundleDetail["coverage"],
      }
    : {}),
});
const history = (rows = [detail()]) => ({
  page: page(rows.length),
  list: rows,
  exhausted: true,
});
const response = (data: unknown, actor: string | null = "1", status = 200) =>
  ({
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers(actor === null ? {} : { "X-ADTR-User-ID": actor }),
    json: vi.fn(async () => data),
  }) as unknown as Response;
const signal = () => new AbortController().signal;
const zipResponse = (
  headers: Record<string, string> = {},
  chunks = [new Uint8Array([80, 75, 3, 4, 0])],
) =>
  ({
    ok: true,
    status: 200,
    headers: new Headers({
      "X-ADTR-User-ID": "1",
      "Content-Type": "application/zip",
      "Content-Disposition": `attachment; filename="${bundleFilename(id)}"`,
      ...headers,
    }),
    body: new ReadableStream({
      start(controller) {
        chunks.forEach((chunk) => controller.enqueue(chunk));
        controller.close();
      },
    }),
  }) as Response;
let override: (
  url: string,
  init: RequestInit,
) => Response | Promise<Response> | undefined;
let fetcher: ReturnType<typeof vi.fn>;
let createURL: ReturnType<typeof vi.fn>, revokeURL: ReturnType<typeof vi.fn>;
beforeEach(() => {
  vi.stubGlobal("Blob", NodeBlob);
  discardBundleIntent();
  sessionStorage.clear();
  window.history.replaceState({}, "", "#operational-logs");
  override = () => undefined;
  createURL = vi.fn(() => "blob:synthetic-log");
  revokeURL = vi.fn();
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    writable: true,
    value: createURL,
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    configurable: true,
    writable: true,
    value: revokeURL,
  });
  fetcher = vi.fn(async (url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/system/logs") return response(list());
    if (path === "/api/system/logs/sources") return response(sources());
    if (path === "/api/system/logs/bundles/history") return response(history());
    if (path === "/api/system/logs/bundles/detail") return response(detail());
    if (path === "/api/system/logs/bundles")
      return response({
        taskUUID: id,
        task: detail("queued").task,
        replayed: false,
      });
    if (path === "/api/system/logs/bundles/cancel")
      return response({ task: detail("cancel_requested").task });
    if (path === "/api/system/logs/bundles/download") return zipResponse();
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
function proof(code = "123456") {
  fill("操作者当前密码", "Synthetic password 123");
  fill("未使用的认证器验证码", code);
}
const submissions = () =>
  fetcher.mock.calls.filter(
    ([url, init]) =>
      url === "/api/system/logs/bundles" && init.method === "POST",
  );
async function start() {
  const rendered = render(
    <OperationalLogsWorkspace profile={profile} sessionChanged={vi.fn()} />,
  );
  await screen.findByRole("table", { name: "运行事件记录" });
  return rendered;
}
async function openDetail() {
  await start();
  click("打包历史");
  await screen.findByRole("table", { name: "诊断包历史记录" });
  click(`查看诊断包 ${id}`);
  await screen.findByRole("button", { name: "下载 ZIP" });
}

describe("strict operational journal transport", () => {
  it("preserves distinct microsecond timestamps and identical event JSON", async () => {
    const value = await logAPI.list(emptyLogQuery(), signal(), 1);
    expect(value.List[0].event.observedAt).toBe(event().observedAt);
    expect(value.selection).toEqual(selection);
    expect(JSON.parse(value.List[0].log)).toEqual(value.List[0].event);
  });
  it.each([
    "list",
    "sources",
    "history",
    "detail",
    "download",
    "submit",
    "cancel",
  ])(
    "binds %s to the actor before consuming successful data",
    async (operation) => {
      for (const actor of [null, "2"]) {
        const r = response({}, actor);
        override = () => r;
        const request =
          operation === "list"
            ? logAPI.list(emptyLogQuery(), signal(), 1)
            : operation === "sources"
              ? logAPI.sources(signal(), 1)
              : operation === "history"
                ? logAPI.history(emptyBundleQuery(), signal(), 1)
                : operation === "detail"
                  ? logAPI.detail(id, signal(), 1)
                  : operation === "download"
                    ? logAPI.download(id, signal(), 1)
                    : operation === "submit"
                      ? logAPI.submit(
                          { ...selection, idempotencyKey: id },
                          { actorPassword: "password", totpCode: "123456" },
                          "csrf",
                          signal(),
                          1,
                        )
                      : logAPI.cancel(
                          id,
                          { actorPassword: "password", totpCode: "123456" },
                          "csrf",
                          signal(),
                          1,
                        );
        await expect(request).rejects.toMatchObject({
          code: "unauthenticated",
        });
        expect(r.json).not.toHaveBeenCalled();
      }
    },
  );
  it("checks half-open microsecond windows without millisecond rounding", () => {
    expect(
      validSelection({
        ...selection,
        startTm: "2026-10-07T00:00:00.000001Z",
        endTm: "2026-10-07T00:00:00.000002Z",
      }),
    ).toBe(true);
    expect(
      validSelection({ ...selection, endTm: "2026-10-07T01:00:00.000002Z" }),
    ).toBe(false);
    for (const query of [
      { ...emptyLogQuery(), startTm: selection.startTm },
      { ...emptyLogQuery(), systemType: ["ldap"] },
      { ...emptyLogQuery(), pageSize: -1, pageIdx: 2 },
      {
        ...emptyLogQuery(),
        ...selection,
        endTm: "2026-10-07T01:00:00.0000001Z",
      },
    ])
      expect(() =>
        logQuery(query as ReturnType<typeof emptyLogQuery>),
      ).toThrow();
  });
  it("rejects hidden metadata, mismatched log aliases, unknown modules and wrong event taxonomy", () => {
    const invalid = [
      { ...list(), metadata: { path: "private" } },
      {
        ...list(),
        List: [
          {
            event: event(),
            log: JSON.stringify(event()).replace(
              '"reason":""',
              '"reason":"PRIVATE_DISCARDED_VALUE","reason":""',
            ),
          },
        ],
      },
      {
        ...list(),
        List: [
          {
            event: { ...event(), path: "private" },
            log: JSON.stringify(event()),
          },
        ],
      },
      {
        ...list(),
        List: [
          {
            event: event(),
            log: JSON.stringify({ ...event(), outcome: "completed" }),
          },
        ],
      },
      list([
        {
          ...event(),
          module: "worker",
          code: "service_failed",
          outcome: "failed",
          reason: "serve_failed",
          severity: "error",
        },
      ]),
      list([
        {
          ...event(),
          module: "api",
          code: "queue_cycle",
          outcome: "completed",
        },
      ]),
      list([{ ...event(), recordedAt: selection.endTm }]),
      { ...list(), selection: { ...selection, systemType: ["ldap"] } },
      { ...list(), page: { ...page(), total: 2 } },
    ];
    for (const value of invalid)
      expect(() => parseLogList(value, emptyLogQuery())).toThrow();
  });
  it("rejects unordered duplicate rows and a different echoed explicit selection", () => {
    expect(() =>
      parseLogList(list([event(), event()]), emptyLogQuery()),
    ).toThrow();
    expect(() =>
      parseLogList(list(), {
        ...emptyLogQuery(),
        ...selection,
        endTm: "2026-10-07T00:50:00Z",
      }),
    ).toThrow();
    expect(() =>
      parseLogList(
        list([{ ...event(), eventId: id }, event()]),
        emptyLogQuery(),
      ),
    ).toThrow();
  });
  it("accepts only incomplete source reports, never arbitrary health metadata", () => {
    expect(parseSources(sources()).reports[0].evidence).toBe(
      "incomplete_process_observation",
    );
    for (const v of [
      {
        ...sources(),
        reports: [{ ...sources().reports[0], health: "healthy" }],
      },
      {
        ...sources(),
        reports: [{ ...sources().reports[0], evidence: "complete" }],
      },
      {
        ...sources(),
        reports: [{ ...sources().reports[0], unacknowledged: -1 }],
      },
      { ...sources(), reportsTruncated: null },
    ])
      expect(() => parseSources(v)).toThrow();
  });
  it("validates task metadata and separates terminal state from artifact eligibility", () => {
    for (const state of ["queued", "running", "failed", "cancelled"] as const)
      expect(parseBundleDetail(detail(state)).downloadReady).toBe(false);
    const corrupt = {
      ...detail("failed"),
      task: detail().task,
      artifactStatus: "artifact_invalid",
    };
    expect(parseBundleDetail(corrupt).downloadReady).toBe(false);
    for (const value of [
      { ...detail(), downloadPath: "https://private.example" },
      { ...detail(), task: { ...detail().task, result: { token: 1 } } },
      { ...detail(), task: { ...detail().task, cursor: { path: "x" } } },
      { ...detail("failed"), rowCount: 3 },
      { ...detail(), rowCount: 10001 },
    ])
      expect(() => parseBundleDetail(value)).toThrow();
    expect(() =>
      parseBundleHistory(
        { ...history(), list: [detail(), detail()] },
        emptyBundleQuery(),
      ),
    ).toThrow();
  });
  it("verifies ZIP name, MIME, signature, declared and streamed byte caps", async () => {
    const valid = await logAPI.download(id, signal(), 1);
    expect(valid.size).toBe(5);
    for (const headers of [
      { "Content-Type": "text/html" },
      { "Content-Disposition": "attachment; filename=private.zip" },
      { "Content-Length": String(16 * 1024 * 1024 + 1) },
      { "Content-Length": "6" },
    ]) {
      override = () =>
        zipResponse(headers as unknown as Record<string, string>);
      await expect(logAPI.download(id, signal(), 1)).rejects.toMatchObject({
        code: "invalid_response",
      });
    }
    override = () => zipResponse({}, [new Uint8Array([1, 2, 3, 4])]);
    await expect(logAPI.download(id, signal(), 1)).rejects.toMatchObject({
      code: "invalid_response",
    });
    override = () =>
      zipResponse({}, [new Uint8Array(16 * 1024 * 1024), new Uint8Array(1)]);
    await expect(logAPI.download(id, signal(), 1)).rejects.toMatchObject({
      code: "invalid_response",
    });
  });
});

describe("operational journal UI and interruption safety", () => {
  it("explains provenance, separates timestamps and packages the confirmed server selection", async () => {
    await start();
    expect(screen.getByText(/部署后新增的 api \/ worker/)).toBeVisible();
    expect(screen.getByText(/Worker 周期完成可能只是空轮询/)).toBeVisible();
    expect(screen.getByText(event().observedAt)).toBeVisible();
    expect(screen.getByText(event().recordedAt)).toBeVisible();
    click("确认当前筛选并打包");
    expect(screen.getByText(/Worker 执行时才捕获/)).toBeVisible();
    proof();
    click("提交诊断包任务");
    await screen.findByRole("heading", { name: "诊断包详情" });
    const [_, init] = submissions()[0];
    expect(JSON.parse(init.body as string)).toMatchObject(selection);
    expect(init.headers).toMatchObject({ "X-CSRF-Token": profile.csrfToken });
    expect(
      sessionStorage.getItem(
        "adtr.pending-operational-bundle.1.synthetic-admin",
      ),
    ).toBeNull();
  });
  it("uses owner-aware access checks and supports reads without generic task permissions", async () => {
    override = (url, init) =>
      url === "/api/access/check"
        ? response({
            results: JSON.parse(init.body as string).paths.map((p: string) =>
              p.startsWith("GET "),
            ),
          })
        : undefined;
    await start();
    expect(
      screen.queryByRole("button", { name: "确认当前筛选并打包" }),
    ).toBeNull();
    click("打包历史");
    await screen.findByRole("table", { name: "诊断包历史记录" });
    expect(
      fetcher.mock.calls.some(([url]) => url.startsWith("/api/tasks")),
    ).toBe(false);
    expect(
      JSON.parse(
        fetcher.mock.calls.find(([url]) => url === "/api/access/check")![1]
          .body as string,
      ).paths,
    ).toEqual(logOperations);
  });
  it("does not read journal data when every checked operation is denied", async () => {
    override = (url) =>
      url === "/api/access/check"
        ? response({ results: logOperations.map(() => false) })
        : undefined;
    render(
      <OperationalLogsWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByText(/当前账户没有此运行日志操作权限/);
    expect(
      fetcher.mock.calls.some(([url]) => url.startsWith("/api/system/logs")),
    ).toBe(false);
  });
  it("distinguishes an empty observed range from a failed query", async () => {
    override = (url) =>
      url.startsWith("/api/system/logs?") ? response(list([])) : undefined;
    await start();
    expect(screen.getByText(/该范围没有已记录事件/)).toBeVisible();
    override = (url) =>
      url.startsWith("/api/system/logs?")
        ? response({ error: "schema_unavailable" }, null, 503)
        : undefined;
    click("刷新事件");
    await screen.findByRole("alert");
    expect(screen.queryByText(/该范围没有已记录事件/)).toBeNull();
    expect(screen.queryByRole("table", { name: "运行事件记录" })).toBeNull();
  });
  it("shows source counters only as incomplete possibly stale observations", async () => {
    await start();
    click("记录来源");
    await screen.findByRole("table", { name: "日志模块来源" });
    expect(screen.getByText(/可能过期的不完整观察/)).toBeVisible();
    expect(screen.getByText(/进程崩溃仍可能留下未知缺口/)).toBeVisible();
    expect(screen.queryByText("服务健康")).toBeNull();
  });
  it("retains an uncertain intent across reload, avoids replacement keys, and clears proof", async () => {
    const mounted = await start();
    click("确认当前筛选并打包");
    override = (url) =>
      url === "/api/system/logs/bundles"
        ? Promise.reject(new Error("lost response"))
        : undefined;
    proof();
    click("提交诊断包任务");
    await screen.findByRole("alert");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    const saved = sessionStorage.getItem(
      "adtr.pending-operational-bundle.1.synthetic-admin",
    )!;
    expect(saved).not.toContain("Synthetic password");
    expect(saved).not.toContain("123456");
    expect(saved).not.toContain(profile.csrfToken);
    const first = JSON.parse(submissions()[0][1].body as string);
    mounted.unmount();
    discardBundleIntent();
    sessionStorage.setItem(
      "adtr.pending-operational-bundle.1.synthetic-admin",
      saved,
    );
    render(
      <OperationalLogsWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("button", { name: "用原幂等键重试打包" });
    click("核对打包历史");
    await screen.findByRole("table", { name: "诊断包历史记录" });
    click("核对未确认打包");
    await screen.findByRole("button", { name: "用原幂等键重试打包" });
    override = () => undefined;
    proof("234567");
    click("用原幂等键重试打包");
    await screen.findByRole("heading", { name: "诊断包详情" });
    expect(JSON.parse(submissions()[1][1].body as string)).toEqual({
      ...first,
      totpCode: "234567",
    });
  });
  it("reconstructs only whitelisted intent fields and rejects another account", () => {
    const saved = {
      owner: 1,
      username: profile.username,
      input: {
        ...selection,
        idempotencyKey: id,
        actorPassword: "DO_NOT_SEND",
        tenant: "other",
      },
    };
    sessionStorage.setItem(
      "adtr.pending-operational-bundle.1.synthetic-admin",
      JSON.stringify(saved),
    );
    expect(bundleIntent(profile)?.input).toEqual({
      ...selection,
      idempotencyKey: id,
    });
    expect(
      bundleIntent({ ...profile, ID: 2, username: "other" }),
    ).toBeUndefined();
    expect(
      bundleIntent({ ...profile, csrfToken: "renewed" })?.input.idempotencyKey,
    ).toBe(id);
    expect(
      sessionStorage.getItem(
        "adtr.pending-operational-bundle.1.synthetic-admin",
      ),
    ).not.toBeNull();
  });
  it("stops before submitting when durable intent storage is unavailable", () => {
    const intent = beginBundleIntent(profile, selection);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("full");
    });
    expect(() => markBundleAttempted(profile, intent)).toThrow();
    expect(intent.attempted).toBe(false);
    expect(submissions()).toHaveLength(0);
  });
  it("discards an old account read even when its transport ignores abort", async () => {
    let resolve!: (v: Response) => void;
    override = (url) =>
      url.startsWith("/api/system/logs?")
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    const mounted = render(
      <OperationalLogsWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await waitFor(() => expect(resolve).toBeDefined());
    override = (url, init) =>
      url === "/api/access/check"
        ? response(
            { results: JSON.parse(init.body as string).paths.map(() => false) },
            "2",
          )
        : undefined;
    mounted.rerender(
      <OperationalLogsWorkspace
        profile={{ ...profile, ID: 2, csrfToken: "other" }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByText(/当前账户没有此运行日志操作权限/);
    await act(async () => resolve(response(list())));
    expect(screen.queryByRole("table", { name: "运行事件记录" })).toBeNull();
  });
  it("rejects a late body from another session and requests session refresh", async () => {
    const changed = vi.fn();
    override = (url) =>
      url.startsWith("/api/system/logs?") ? response(list(), "2") : undefined;
    render(
      <OperationalLogsWorkspace profile={profile} sessionChanged={changed} />,
    );
    await waitFor(() => expect(changed).toHaveBeenCalled());
    expect(screen.queryByText(event().observedAt)).toBeNull();
  });
  it("discards a pending ZIP after navigation even if download ignores abort", async () => {
    let finish!: (value: Blob) => void;
    vi.spyOn(logAPI, "download").mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    await openDetail();
    click("下载 ZIP");
    click("打包历史");
    await screen.findByRole("table", { name: "诊断包历史记录" });
    await act(async () => finish(new Blob(["late"])));
    expect(createURL).not.toHaveBeenCalled();
  });
  it("revokes created object URLs on account/navigation change", async () => {
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    await openDetail();
    click("下载 ZIP");
    await waitFor(() => expect(createURL).toHaveBeenCalledTimes(1));
    click("事件查询");
    await screen.findByRole("table", { name: "运行事件记录" });
    expect(revokeURL).toHaveBeenCalledWith("blob:synthetic-log");
  });
  it("rechecks metadata and removes readiness after a failed download", async () => {
    await openDetail();
    override = (url) =>
      url.startsWith("/api/system/logs/bundles/download")
        ? response({ error: "artifact_invalid" }, null, 409)
        : url.startsWith("/api/system/logs/bundles/detail")
          ? response({
              ...detail("failed"),
              task: detail().task,
              artifactStatus: "artifact_invalid",
            })
          : undefined;
    click("下载 ZIP");
    await screen.findByText(/任务已成功，但当前没有可下载的有效 ZIP/);
    expect(screen.queryByRole("button", { name: "下载 ZIP" })).toBeNull();
    expect(createURL).not.toHaveBeenCalled();
  });
  it("keeps failed, cancelled and metadata-invalid package rows visible", async () => {
    const failed = detail("failed"),
      cancelled = detail("cancelled");
    cancelled.task.taskUUID = eventID;
    override = (url) =>
      url.startsWith("/api/system/logs/bundles/history")
        ? response(history([failed, cancelled]))
        : undefined;
    await start();
    click("打包历史");
    await screen.findByRole("table", { name: "诊断包历史记录" });
    expect(screen.getByText(/已失败 ·/)).toBeVisible();
    expect(screen.getByText(/已取消 ·/)).toBeVisible();
    expect(screen.getAllByText("尚无已发布 ZIP")).toHaveLength(2);
  });
  it("uses the dedicated cancel route, fresh proof, and persisted task re-read", async () => {
    let cancelled = false;
    override = (url) =>
      url.startsWith("/api/system/logs/bundles/detail")
        ? response(detail(cancelled ? "cancel_requested" : "running"))
        : url === "/api/system/logs/bundles/cancel"
          ? ((cancelled = true),
            response({ task: detail("cancel_requested").task }))
          : undefined;
    window.history.replaceState({}, "", `#operational-logs/detail/${id}`);
    render(
      <OperationalLogsWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("button", { name: "请求取消诊断包" });
    click("请求取消诊断包");
    proof();
    click("确认取消诊断包");
    await screen.findByText(/已请求取消，尚未确认停止/);
    const request = fetcher.mock.calls.find(
      ([url]) => url === "/api/system/logs/bundles/cancel",
    )!;
    expect(JSON.parse(request[1].body as string)).toMatchObject({
      taskUUID: id,
      totpCode: "123456",
    });
    expect(
      fetcher.mock.calls.some(([url]) => url === "/api/tasks/cancel"),
    ).toBe(false);
  });
  it("reopens details after reload and browser Back using the root route", async () => {
    let finishDetail!: (value: Response) => void;
    override = (url) =>
      url.startsWith("/api/system/logs/bundles/detail")
        ? new Promise((resolve) => {
            finishDetail = resolve;
          })
        : undefined;
    window.history.replaceState({}, "", `#operational-logs/detail/${id}`);
    await act(async () => {
      render(
        <StrictMode>
          <App />
        </StrictMode>,
      );
    });
    await screen.findByRole("heading", { name: "诊断包详情" });
    // The route heading exists while its separate detail request is pending.
    expect(screen.queryByRole("button", { name: "下载 ZIP" })).toBeNull();
    await act(async () => finishDetail(response(detail())));
    expect(
      await screen.findByRole("button", { name: "下载 ZIP" }),
    ).toBeVisible();
    await act(async () => {
      window.history.replaceState({}, "", "#operational-logs/history");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("table", { name: "诊断包历史记录" });
    expect(screen.queryByRole("heading", { name: "诊断包详情" })).toBeNull();
  });
});
