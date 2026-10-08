import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import SystemWorkspace from "./SystemWorkspace";
import { ApiError, type Profile } from "./api";
import { labels, marks, type Permission } from "./access-api";
import { PermissionEditor } from "./access-common";
import {
  systemAPI,
  systemOperations,
  type Current,
  type HistoryInput,
  type StorageItem,
} from "./system-api";

const now = "2026-10-07T08:00:00Z",
  seconds = Date.parse(now) / 1000;
const meta = {
  observedAt: now,
  source: "linux_proc",
  scope: "kernel_visible",
  availability: "available" as const,
};
const disk: StorageItem = {
  ...meta,
  source: "linux_statfs",
  scope: "runtime_filesystem",
  id: "runtime-root",
  mount: "/",
  fs: "overlay",
  totalBytes: "9007199254740993",
  usedBytes: "1801439850948198",
  freeBytes: "7205759403792795",
  reservedBytes: "0",
  percent: 20,
  alarmPercent: 85,
  revision: 0,
  alarmExceeded: false,
};
const current = (): Current => ({
  instance: "local-api",
  availability: "available",
  stale: false,
  availableSince: now,
  checkedAt: now,
  snapshot: {
    instance: "local-api",
    observedAt: now,
    cpu: { ...meta, percent: 4, intervalStart: "2026-10-07T07:59:45Z" },
    memory: {
      ...meta,
      percent: 20,
      totalBytes: "100000",
      usedBytes: "20000",
      availableBytes: "80000",
    },
    uptime: { ...meta, seconds: 123 },
    load: { ...meta, values: { one: 1, five: 2, fifteen: 3 } },
    storage: [disk],
  },
});
const storage = (item = disk) => ({
  instance: "local-api",
  storage: [item],
  total: 1,
  page: 1,
  pageSize: 20,
  availability: "available",
  stale: false,
  observedAt: now,
  checkedAt: now,
});
const info = () => ({
  status: {
    memoryUsagePercent: 20,
    cpuUsagePercent: 4,
    diskUsagePercent: 20,
    bootTime: 123,
    LoadAverage: { one: 1, five: 2, fifteen: 3 },
  },
  version: {
    engineVersion: null,
    majorVersion: null,
    engineVersionTimestamp: null,
    majorVersionTimestamp: null,
    OSPlatform: "linux",
    OSVersion: null,
    goVersion: "go1.27.1",
    revision: null,
    modified: null,
  },
  basic: {
    ip: null,
    systemName: "ADTR",
    companyName: null,
    officialWebsite: null,
  },
  upgrade: { upgradeEngineVersion: null, upgradeMajorVersion: null },
  systemCurrentTime: now,
  current: current(),
  capabilities: {
    engine: "not_configured",
    license: "unsupported",
    upgrade: "not_configured",
    cleanup: "unsupported",
  },
});
const history = (query: URLSearchParams) => ({
  instance: "local-api",
  graphType: query.get("graphType"),
  info: [
    {
      mode: query.get("graphType"),
      data: {
        timestamp: [seconds - 30, seconds - 15],
        value: ["4.125", "5.375"],
        dataStatistics: { max: 5.375, min: 4.125, avg: 4.75, current: 5.375 },
      },
    },
  ],
  gaps: [{ startTime: Number(query.get("startTime")), endTime: seconds - 30 }],
  availableSince: "2026-10-07T07:59:30Z",
  sampleIntervalSeconds: 15,
});
const health = () => ({
  result: "degraded",
  checkedAt: now,
  dependencies: [
    {
      id: "postgresql",
      name: "PostgreSQL",
      type: "port",
      status: "healthy",
      reason: "protocol_and_schema_verified",
      source: "postgresql_protocol",
      scope: "configured_dependency",
      checkedAt: now,
      observedAt: now,
    },
    {
      id: "cache",
      name: "Cache",
      type: "service",
      status: "not_configured",
      reason: "not_configured",
      source: "server_configuration",
      scope: "configured_dependency",
      checkedAt: now,
      observedAt: null,
    },
  ],
  worker: {
    cycles: [],
    availability: "unavailable",
    reason: "no_worker_activity",
    checkedAt: now,
    staleAfterSeconds: 15,
  },
});
const metadata = (): Permission[] =>
  marks.map((mark) => ({
    mark,
    name: labels[mark],
    auth: { readable: true, writeable: true },
    allow_auth: { readable: true, writeable: true },
    paths: [],
    children: [],
    checked: true,
    icon: mark,
  }));
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response);
let profile: Profile,
  fetcher: ReturnType<typeof vi.fn>,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined;
beforeEach(() => {
  profile = {
    ID: 1,
    username: "admin",
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
    pwdUpdateTm: now,
    csrfToken: "system-csrf",
  };
  window.history.replaceState({}, "", "#account");
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const [path, query] = url.split("?");
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu: metadata() });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/system/info") return response(info());
    if (path === "/api/system/nodes")
      return response({
        nodeList: [
          {
            instance: "local-api",
            hostName: "synthetic-runtime",
            ip: null,
            scope: "kernel_visible",
            availability: "available",
          },
        ],
      });
    if (path === "/api/system/resources/current") return response(current());
    if (path === "/api/system/resources/history")
      return response(history(new URLSearchParams(query)));
    if (path === "/api/system/storage") return response(storage());
    if (path === "/api/system/services" || path === "/api/system/health")
      return response(health());
    if (path === "/api/system/storage/settings") {
      const v = JSON.parse(init.body as string);
      return response({
        result: 1,
        setting: {
          instance: v.instance,
          storageId: v.storageId,
          alarmPercent: v.percent,
          revision: v.expectedRevision + 1,
          updatedAt: now,
        },
      });
    }
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const calls = (path: string) =>
  fetcher.mock.calls.filter(
    ([url]) => url.split("?")[0] === `/api/system${path}`,
  );
async function start() {
  render(<SystemWorkspace profile={profile} sessionChanged={vi.fn()} />);
  await screen.findByRole("table", { name: "已登记节点" });
}
async function edit() {
  await start();
  click("存储管理");
  await screen.findByRole("table", { name: "实际文件系统存储" });
  click("调整阈值 runtime-root");
}
const proof = () => {
  fill("操作者当前密码", "Synthetic Actor Password 123");
  fill("未使用的认证器验证码", "123456");
};

describe("system health contract", () => {
  it("preserves distinct observations recorded within the same Unix second", async () => {
    const query: HistoryInput = {
      instance: "local-api",
      graphType: "cpu_basic",
      startTime: seconds - 900,
      endTime: seconds,
    };
    const raw = history(
      new URLSearchParams({
        graphType: "cpu_basic",
        startTime: String(query.startTime),
      }),
    );
    raw.info[0].data.timestamp = [seconds - 15, seconds - 15];
    override = (url) =>
      url.includes("/resources/history") ? response(raw) : undefined;
    const result = await systemAPI.history(query, new AbortController().signal);
    expect(result.info[0].data.value).toEqual(["4.125", "5.375"]);
  });

  it("validates empty history as null statistics, and rejects malformed strings and mismatched times", async () => {
    const query: HistoryInput = {
      instance: "local-api",
      graphType: "cpu_basic",
      startTime: seconds - 900,
      endTime: seconds,
    };
    const raw = history(
      new URLSearchParams({
        graphType: "cpu_basic",
        startTime: String(query.startTime),
      }),
    );
    raw.info[0].data.value = ["NaN", "5"];
    override = (url) =>
      url.includes("/resources/history") ? response(raw) : undefined;
    await expect(
      systemAPI.history(query, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
    const empty = {
      ...raw,
      info: [
        {
          mode: "cpu_basic",
          data: {
            timestamp: [],
            value: [],
            dataStatistics: { max: null, min: null, avg: null, current: null },
          },
        },
      ],
    };
    override = (url) =>
      url.includes("/resources/history") ? response(empty) : undefined;
    expect(
      (await systemAPI.history(query, new AbortController().signal)).info[0]
        .data.dataStatistics.current,
    ).toBeNull();
    override = (url) =>
      url.includes("/resources/history")
        ? response({
            ...empty,
            info: [
              {
                mode: "cpu_basic",
                data: {
                  timestamp: [],
                  value: [],
                  dataStatistics: { max: 0, min: 0, avg: 0, current: 0 },
                },
              },
            ],
          })
        : undefined;
    await expect(
      systemAPI.history(query, new AbortController().signal),
    ).rejects.toBeInstanceOf(ApiError);
  });
  it("sends only registered target, alarm type, CAS revision and fresh proof, with cookie/CSRF/no-store", async () => {
    await systemAPI.settings(
      {
        instance: "local-api",
        storageId: "runtime-root",
        setType: "alarm",
        percent: 90,
        expectedRevision: 0,
      },
      { actorPassword: "Synthetic Password 123", totpCode: "123456" },
      profile.csrfToken,
      new AbortController().signal,
    );
    const [url, init] = calls("/storage/settings")[0];
    expect(url).toBe("/api/system/storage/settings");
    expect(init).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      redirect: "error",
      cache: "no-store",
      headers: { "X-CSRF-Token": "system-csrf" },
    });
    expect(JSON.parse(init.body as string)).toEqual({
      instance: "local-api",
      storageId: "runtime-root",
      setType: "alarm",
      percent: 90,
      expectedRevision: 0,
      actorPassword: "Synthetic Password 123",
      totpCode: "123456",
    });
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.getItem("system-csrf")).toBeNull();
  });
  it("rejects false save confirmation and mismatching revision", async () => {
    override = (url) =>
      url.endsWith("/settings")
        ? response({
            result: 1,
            setting: {
              instance: "local-api",
              storageId: "runtime-root",
              alarmPercent: 89,
              revision: 8,
              updatedAt: now,
            },
          })
        : undefined;
    await expect(
      systemAPI.settings(
        {
          instance: "local-api",
          storageId: "runtime-root",
          setType: "alarm",
          percent: 89,
          expectedRevision: 0,
        },
        { actorPassword: "Synthetic Password 123", totpCode: "123456" },
        "csrf",
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
});
describe("system health interface", () => {
  it("does not invent disk history targets when trusted storage registration is empty", async () => {
    override = (url) =>
      url.split("?")[0] === "/api/system/storage"
        ? response({ ...storage(), storage: [], total: 0 })
        : undefined;
    await start();
    click("资源与历史");
    await screen.findByRole("img", { name: /实际样本/ });
    fill("历史指标", "disk_usage");
    await screen.findByText(
      "没有可读取的已登记存储目标，无法查询文件系统历史。",
    );
    expect(screen.getByLabelText("已登记存储目标")).toBeDisabled();
    expect(
      calls("/resources/history").some(([url]) => url.includes("disk_usage")),
    ).toBe(false);
  });

  it("renders actual scopes, nullable metadata, timestamps and no fabricated license/engine", async () => {
    await start();
    expect(screen.getByText("synthetic-runtime")).toBeVisible();
    expect(screen.getByText(/不等同于容器配额/)).toBeVisible();
    expect(screen.getByText("4.00%")).toBeVisible();
    expect(screen.getAllByText("未提供").length).toBeGreaterThan(1);
    expect(
      within(screen.getByText("license").closest("div")!).getByText(
        "unsupported",
      ),
    ).toBeVisible();
    expect(
      within(screen.getByText("cleanup").closest("div")!).getByText(
        "unsupported",
      ),
    ).toBeVisible();
    expect(
      fetcher.mock.calls.find(([u]) => u === "/api/access/check")?.[1].body,
    ).toBe(JSON.stringify({ paths: systemOperations }));
  });
  it("does not query health when the menu or operation gate denies it", async () => {
    override = (url) =>
      url === "/api/access/menu"
        ? response({
            menu: metadata().map((p) => ({
              ...p,
              auth: { readable: p.mark !== "system", writeable: false },
            })),
          })
        : undefined;
    render(<SystemWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("当前账户没有系统健康读取权限。");
    expect(calls("/info")).toHaveLength(0);
  });
  it("shows unavailable CPU without zero and labels stale measurements", async () => {
    const value = info();
    value.current.stale = true;
    value.current.snapshot!.cpu = {
      ...meta,
      percent: null,
      availability: "warming_up",
      reason: "insufficient_samples",
    };
    override = (url) =>
      url === "/api/system/info" ? response(value) : undefined;
    await start();
    expect(screen.getByText(/数据已过期/)).toBeVisible();
    expect(screen.queryByText("0.00%")).not.toBeInTheDocument();
    expect(screen.getByText(/insufficient_samples/)).toBeVisible();
  });
  it("preserves byte strings beyond JS safe integers and readonly settings", async () => {
    override = (url, init) =>
      url === "/api/access/check"
        ? response({
            results: JSON.parse(init.body as string).paths.map(
              (p: string) => p !== "POST /api/system/storage/settings",
            ),
          })
        : undefined;
    await start();
    click("存储管理");
    const table = await screen.findByRole("table", {
      name: "实际文件系统存储",
    });
    expect(within(table).getByText("9,007,199,254,740,993 B")).toBeVisible();
    expect(within(table).getByText("只读")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "调整阈值 runtime-root" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/不代表 PostgreSQL 数据盘/)).toBeVisible();
  });
  it("uses explicit history windows, real gaps, actual decimal values and stops stale query replies", async () => {
    await start();
    click("资源与历史");
    await screen.findByRole("img", { name: /2 个实际样本/ });
    const q = new URL(calls("/resources/history")[0][0], "http://local")
      .searchParams;
    expect(Number(q.get("endTime")) - Number(q.get("startTime"))).toBe(900);
    let release!: (r: Response) => void;
    override = (url) =>
      url.includes("/resources/history") && url.includes("ram_basic")
        ? new Promise((r) => {
            release = r;
          })
        : undefined;
    fill("历史指标", "ram_basic");
    await waitFor(() => expect(release).toBeTypeOf("function"));
    fill("历史指标", "disk_usage");
    await screen.findByRole("heading", { name: "API 文件系统使用率历史" });
    await act(async () =>
      release(
        await response(
          history(
            new URLSearchParams({
              graphType: "ram_basic",
              startTime: String(seconds - 900),
            }),
          ),
        ),
      ),
    );
    expect(
      screen.queryByRole("heading", { name: "内存使用率历史" }),
    ).not.toBeInTheDocument();
    fill("历史时间范围", "86400");
    await waitFor(() =>
      expect(
        new URL(
          calls("/resources/history").at(-1)![0],
          "http://local",
        ).searchParams.get("startTime"),
      ).toBe(String(seconds - 86400)),
    );
    expect(screen.getByText(/灰色区域为缺测区间/)).toBeVisible();
  });
  it("renders empty history with unavailable statistics", async () => {
    override = (url) =>
      url.includes("/resources/history")
        ? response({
            instance: "local-api",
            graphType: "cpu_basic",
            info: [
              {
                mode: "cpu_basic",
                data: {
                  timestamp: [],
                  value: [],
                  dataStatistics: {
                    max: null,
                    min: null,
                    avg: null,
                    current: null,
                  },
                },
              },
            ],
            gaps: [],
            availableSince: null,
            sampleIntervalSeconds: 15,
          })
        : undefined;
    await start();
    click("资源与历史");
    await screen.findByText(/此范围没有真实历史样本/);
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getAllByText("不可用")).toHaveLength(4);
  });
  it.each(["刷新资源与历史", "资源与历史"])(
    "keeps a current CPU sample out of history until %s reads a window containing it",
    async (reload) => {
      const measured = current();
      measured.checkedAt = "2026-10-07T08:00:00.900Z";
      override = (url) => {
        const [path, query] = url.split("?");
        if (path === "/api/system/resources/current") return response(measured);
        if (path !== "/api/system/resources/history") return undefined;
        const params = new URLSearchParams(query),
          endTime = Number(params.get("endTime")),
          included = seconds < endTime;
        return response({
          instance: "local-api",
          graphType: "cpu_basic",
          info: [
            {
              mode: "cpu_basic",
              data: {
                timestamp: included ? [seconds] : [],
                value: included ? ["4"] : [],
                dataStatistics: {
                  max: included ? 4 : null,
                  min: included ? 4 : null,
                  avg: included ? 4 : null,
                  current: included ? 4 : null,
                },
              },
            },
          ],
          gaps: [
            {
              startTime: Number(params.get("startTime")),
              endTime: included ? seconds : endTime,
            },
          ],
          availableSince: now,
          sampleIntervalSeconds: 15,
        });
      };
      await start();
      click("资源与历史");
      await screen.findByText(/此范围没有真实历史样本/);
      expect(
        screen.getByRole("heading", { name: "CPU 使用率历史" }),
      ).toBeVisible();
      expect(screen.queryByRole("img")).not.toBeInTheDocument();
      expect(
        new URL(
          calls("/resources/history")[0][0],
          "http://local",
        ).searchParams.get("endTime"),
      ).toBe(String(seconds));
      expect(screen.getAllByText("不可用")).toHaveLength(4);

      // Only the server's check time advances; no new sample is manufactured.
      measured.checkedAt = "2026-10-07T08:00:01Z";
      click(reload);
      const chart = await screen.findByRole("img", { name: /1 个实际样本/ });
      expect(chart.querySelectorAll("circle")).toHaveLength(1);
      expect(chart).toHaveTextContent(`${new Date(now).toISOString()}：4%`);
      expect(calls("/resources/current")).toHaveLength(2);
      expect(
        new URL(
          calls("/resources/history").at(-1)![0],
          "http://local",
        ).searchParams.get("endTime"),
      ).toBe(String(seconds + 1));
      expect(
        screen.queryByText(/此范围没有真实历史样本/),
      ).not.toBeInTheDocument();
    },
  );
  it("locks repeated writes, clears proof immediately, and reloads after server confirmation", async () => {
    let release!: (r: Response) => void;
    override = (url) =>
      url.endsWith("/settings")
        ? new Promise((r) => {
            release = r;
          })
        : undefined;
    await edit();
    fill("存储告警阈值（%）", "89");
    proof();
    const form = screen
      .getByRole("button", { name: "保存告警阈值" })
      .closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(calls("/storage/settings")).toHaveLength(1);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    override = (url) =>
      url.split("?")[0] === "/api/system/storage"
        ? response(storage({ ...disk, alarmPercent: 89, revision: 1 }))
        : undefined;
    await act(async () =>
      release(
        await response({
          result: 1,
          setting: {
            instance: "local-api",
            storageId: "runtime-root",
            alarmPercent: 89,
            revision: 1,
            updatedAt: now,
          },
        }),
      ),
    );
    await screen.findByText(/已由服务器保存/);
    expect(screen.getByText("89%")).toBeVisible();
    expect(screen.getByText("版本 1")).toBeVisible();
  });
  it.each([
    ["revision_conflict", 409],
    ["network", 0],
    ["invalid_response", 200],
  ])(
    "requires explicit server reread after %s instead of retry",
    async (code, status) => {
      override = (url) =>
        url.endsWith("/settings")
          ? status === 0
            ? Promise.reject(new TypeError("offline"))
            : response(status === 200 ? {} : { error: code }, status)
          : undefined;
      await edit();
      proof();
      click("保存告警阈值");
      await screen.findByText(/本次结果或版本尚未确认/);
      expect(
        screen.getByRole("button", { name: "保存告警阈值" }),
      ).toBeDisabled();
      fireEvent.submit(
        screen.getByRole("button", { name: "保存告警阈值" }).closest("form")!,
      );
      expect(calls("/storage/settings")).toHaveLength(1);
      click("重新读取存储设置");
      await screen.findByRole("table", { name: "实际文件系统存储" });
      expect(calls("/storage")).toHaveLength(2);
      click("调整阈值 runtime-root");
      expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    },
  );
  it("validates integer boundaries before sending and preserves same editor", async () => {
    await edit();
    proof();
    for (const n of ["84", "91", "85.5", ""]) {
      fill("存储告警阈值（%）", n);
      fireEvent.submit(
        screen.getByRole("button", { name: "保存告警阈值" }).closest("form")!,
      );
    }
    expect(calls("/storage/settings")).toHaveLength(0);
    expect(screen.getByRole("alert")).toHaveTextContent("85–90");
  });
  it("discards late mutation after cancel and refetches instead of reopening", async () => {
    let release!: (r: Response) => void;
    override = (url) =>
      url.endsWith("/settings")
        ? new Promise((r) => {
            release = r;
          })
        : undefined;
    await edit();
    proof();
    click("保存告警阈值");
    click("取消");
    await screen.findByRole("table", { name: "实际文件系统存储" });
    await act(async () =>
      release(
        await response({
          result: 1,
          setting: {
            instance: "local-api",
            storageId: "runtime-root",
            alarmPercent: 85,
            revision: 1,
            updatedAt: now,
          },
        }),
      ),
    );
    expect(screen.queryByText(/已由服务器保存/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(
      (calls("/storage/settings")[0][1].signal as AbortSignal).aborted,
    ).toBe(true);
  });
  it.each([503, 403])(
    "clears prior health values on refresh failure %s",
    async (status) => {
      await start();
      click("平台依赖健康");
      await screen.findByRole("table", { name: "实际依赖检查" });
      expect(screen.getByText("未配置")).toBeVisible();
      expect(screen.getByText(/尚无 Worker 实际循环活动/)).toBeVisible();
      override = (url) =>
        url === "/api/system/health"
          ? response(
              { error: status === 503 ? "database_unavailable" : "forbidden" },
              status,
            )
          : undefined;
      click("刷新平台依赖健康");
      await screen.findByRole("alert");
      expect(
        screen.queryByRole("table", { name: "实际依赖检查" }),
      ).not.toBeInTheDocument();
      expect(screen.queryByText("PostgreSQL")).not.toBeInTheDocument();
    },
  );
  it("navigation and Back/Forward clear pending proof and discard late responses", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    click("系统健康");
    await screen.findByRole("table", { name: "已登记节点" });
    click("存储管理");
    await screen.findByRole("table", { name: "实际文件系统存储" });
    click("调整阈值 runtime-root");
    proof();
    let release!: (r: Response) => void;
    override = (url) =>
      url.endsWith("/settings")
        ? new Promise((r) => {
            release = r;
          })
        : undefined;
    click("保存告警阈值");
    const pendingSignal = calls("/storage/settings")[0][1]
      .signal as AbortSignal;
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#system/storage"));
    await screen.findByRole("table", { name: "已登记节点" });
    expect(pendingSignal.aborted).toBe(true);
    await act(async () =>
      release(
        await response({
          result: 1,
          setting: {
            instance: "local-api",
            storageId: "runtime-root",
            alarmPercent: 85,
            revision: 1,
            updatedAt: now,
          },
        }),
      ),
    );
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(screen.queryByText(/已由服务器保存/)).not.toBeInTheDocument();
    act(() => window.history.forward());
    await waitFor(() =>
      expect(window.location.hash).toBe(
        "#system/storage/settings/runtime-root",
      ),
    );
    await screen.findByRole("table", { name: "已登记节点" });
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(screen.queryByText(/已由服务器保存/)).not.toBeInTheDocument();
  });
  it("returns to session check for 401 without retaining content", async () => {
    const changed = vi.fn();
    override = (url) =>
      url === "/api/system/info"
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    render(<SystemWorkspace profile={profile} sessionChanged={changed} />);
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("synthetic-runtime")).not.toBeInTheDocument();
  });
  it("preserves server system grant when another permission changes", () => {
    const onChange = vi.fn();
    render(
      <PermissionEditor
        metadata={metadata()}
        value={marks.map((mark) => ({
          mark,
          auth: { readable: mark === "system", writeable: false },
        }))}
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByLabelText("用户管理：读取"));
    expect(onChange.mock.calls[0][0]).toContainEqual({
      mark: "system",
      auth: { readable: true, writeable: false },
    });
    expect(screen.getByLabelText("系统健康：写入")).toBeEnabled();
  });
});
