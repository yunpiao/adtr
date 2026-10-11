import { revealNavigation } from "./test-navigation";
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
import { observationReason } from "./system-common";
import { ApiError, type Profile } from "./api";
import { labels, marks, type Permission } from "./access-api";
import { PermissionEditor } from "./access-common";
import {
  systemAPI,
  systemOperations,
  validCurrent,
  validContainerMemory,
  validHistory,
  type ContainerMemoryMetric,
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
    await revealNavigation("系统健康");
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

describe("system SOC diagnostic workspace", () => {
  async function openHealth() {
    await start();
    click("平台依赖健康");
    return screen.findByRole("region", { name: "依赖诊断工作台" });
  }
  function workerHealth() {
    const raw = health();
    raw.dependencies.push({
      id: "worker",
      name: "ADTR worker",
      type: "service",
      status: "unhealthy",
      reason: "stale_or_incomplete_worker_activity",
      source: "worker_cycle_activity",
      scope: "configured_dependency",
      checkedAt: now,
      observedAt: now,
    });
    return raw;
  }
  it("separates actionable anomalies from configured successes and unconfigured capabilities", async () => {
    override = (url) =>
      url === "/api/system/health" ? response(workerHealth()) : undefined;
    const dashboard = await openHealth();
    const cards = dashboard.querySelectorAll(".system-findings > li");
    expect(cards).toHaveLength(1);
    expect(cards[0]).toHaveTextContent("ADTR worker");
    expect(cards[0]).toHaveTextContent("Worker 活动已过期或必需循环不完整");
    expect(cards[0]).toHaveTextContent("处理建议：");
    expect(cards[0]).toHaveTextContent("核对下方各循环的最近活动与成功时间");
    expect(cards[0]).toHaveTextContent("worker_cycle_activity");
    expect(cards[0]).toHaveTextContent(new Date(now).toISOString());
    expect(cards[0]).not.toHaveTextContent("Cache");
    const unconfigured = within(dashboard)
      .getByText("未配置能力（1）")
      .closest("details")!;
    fireEvent.click(within(dashboard).getByText("未配置能力（1）"));
    expect(unconfigured).toHaveTextContent("Cache · 未配置");
    expect(unconfigured).toHaveTextContent("没有可用检查证据");
  });
  it("filters dependency rows without altering server status or making a new request", async () => {
    override = (url) =>
      url === "/api/system/health" ? response(workerHealth()) : undefined;
    await openHealth();
    const table = screen.getByRole("table", { name: "实际依赖检查" });
    expect(within(table).getAllByRole("row")).toHaveLength(4);
    fireEvent.click(screen.getByLabelText("只看异常或未知依赖"));
    expect(within(table).getAllByRole("row")).toHaveLength(2);
    expect(table).toHaveTextContent("ADTR worker");
    expect(table).not.toHaveTextContent("Cache");
    expect(calls("/health")).toHaveLength(1);
    fireEvent.click(screen.getByLabelText("只看异常或未知依赖"));
    expect(within(table).getAllByRole("row")).toHaveLength(4);
  });
  it("never calls an all-unconfigured filtered response healthy", async () => {
    const raw = health();
    raw.result = "healthy";
    raw.dependencies = raw.dependencies.filter(
      (d) => d.status === "not_configured",
    );
    override = (url) =>
      url === "/api/system/health" ? response(raw) : undefined;
    await openHealth();
    expect(
      screen.getByText(/当前检查范围没有已配置依赖，健康无法判定/),
    ).toBeVisible();
    expect(
      screen.getByText(/当前检查范围没有已配置依赖，健康无法判定/),
    ).toHaveClass("warning");
    fireEvent.click(screen.getByLabelText("只看异常或未知依赖"));
    expect(screen.getByText("筛选范围内没有异常或未知依赖。")).toBeVisible();
  });
  it("replaces stale anomaly evidence only after a confirmed recovery read", async () => {
    let raw = workerHealth();
    override = (url) =>
      url === "/api/system/health" ? response(raw) : undefined;
    const dashboard = await openHealth();
    expect(dashboard.querySelectorAll(".system-findings > li")).toHaveLength(1);
    raw = workerHealth();
    raw.result = "healthy";
    const worker = raw.dependencies.find((d) => d.id === "worker")!;
    worker.status = "healthy";
    worker.reason = "fresh_worker_cycles";
    click("刷新平台依赖健康");
    await waitFor(() =>
      expect(
        screen
          .getByRole("region", { name: "依赖诊断工作台" })
          .querySelectorAll(".system-findings > li"),
      ).toHaveLength(0),
    );
    expect(screen.getByText(/当前返回的依赖中没有异常或未知项/)).toBeVisible();
    expect(calls("/health")).toHaveLength(2);
  });
  it.each([403, 503])(
    "clears anomaly evidence on HTTP %s and recovers via an explicit fresh read",
    async (status) => {
      override = (url) =>
        url === "/api/system/health" ? response(workerHealth()) : undefined;
      await openHealth();
      override = (url) =>
        url === "/api/system/health"
          ? response(
              { error: status === 403 ? "forbidden" : "database_unavailable" },
              status,
            )
          : undefined;
      click("刷新平台依赖健康");
      await screen.findByRole("alert");
      expect(
        screen.queryByRole("region", { name: "依赖诊断工作台" }),
      ).not.toBeInTheDocument();
      override = (url) =>
        url === "/api/system/health" ? response(workerHealth()) : undefined;
      click("刷新平台依赖健康");
      expect(
        await screen.findByRole("region", { name: "依赖诊断工作台" }),
      ).toHaveTextContent("ADTR worker");
    },
  );
  it("keeps unknown evidence codes visible without inventing a root cause", async () => {
    const raw = workerHealth();
    raw.dependencies[2].id = "other";
    raw.dependencies[2].reason = "new_backend_reason";
    override = (url) =>
      url === "/api/system/health" ? response(raw) : undefined;
    const dashboard = await openHealth();
    expect(dashboard).toHaveTextContent(
      "服务器原因：new_backend_reason；具体根因需结合对应服务日志核查",
    );
    expect(dashboard).toHaveTextContent("当前接口没有提供更具体的修复证据");
  });
  it("distinguishes a gap before retained observations from an unexplained later gap", async () => {
    override = (url) => {
      if (!url.startsWith("/api/system/resources/history?")) return undefined;
      const raw = history(new URLSearchParams(url.split("?")[1]));
      raw.gaps.push({ startTime: seconds - 10, endTime: seconds });
      return response(raw);
    };
    await start();
    click("资源与历史");
    const summary = await screen.findByText("查看 2 个实际缺测区间");
    fireEvent.click(summary);
    expect(screen.getByText(/早于此指标最早可用记录/)).toBeVisible();
    expect(screen.getByText(/历史接口未提供根因/)).toBeVisible();
    expect(screen.getByText(/历史接口未提供根因/)).toHaveTextContent(
      "数据库写入记录定位",
    );
  });
  it.each(["current", "storage", "worker", "cycle"])(
    "rejects malformed optional %s reasons before rendering",
    async (target) => {
      override = (url) => {
        if (
          target === "current" &&
          url.startsWith("/api/system/resources/current")
        )
          return response({ ...current(), reason: { unsafe: true } });
        if (target === "storage" && url.startsWith("/api/system/storage?"))
          return response({ ...storage(), reason: 123 });
        if (
          (target === "worker" || target === "cycle") &&
          url === "/api/system/health"
        ) {
          const raw = health();
          return response({
            ...raw,
            worker:
              target === "worker"
                ? { ...raw.worker, reason: [] }
                : {
                    ...raw.worker,
                    cycles: [
                      {
                        workerId: "synthetic",
                        cycle: "queue",
                        status: "success",
                        evidence: "completed_cycle",
                        availability: "available",
                        lastActivityAt: now,
                        lastSuccessAt: now,
                        reason: {},
                      },
                    ],
                  },
          });
        }
        return undefined;
      };
      const signal = new AbortController().signal;
      await expect(
        target === "current"
          ? systemAPI.current(signal)
          : target === "storage"
            ? systemAPI.storage(1, 20, signal)
            : systemAPI.health(signal),
      ).rejects.toMatchObject({ code: "invalid_response" });
    },
  );
});

describe("system observation explanations", () => {
  it.each(["__proto__", "constructor", "toString"])(
    "treats prototype key %s as an unknown evidence code",
    (code) => {
      expect(observationReason(code)).toBe(
        `服务器原因：${code}；具体根因需结合对应服务日志核查。`,
      );
    },
  );
  it.each([
    ["no_samples", "尚无持久化采样"],
    ["stale_sample", "最近采样已超过有效期"],
    ["clock_skew", "观测时间晚于检查时间"],
    ["no_worker_activity", "尚未观察到 Worker 活动"],
    ["stale_worker_activity", "Worker 最近活动已超过有效期"],
    ["stale_or_incomplete_worker_activity", "必需循环不完整"],
    ["worker_cycle_failed", "Worker 循环报告失败"],
    ["partial_resource_observation", "部分资源指标缺少可用采样"],
    ["unavailable_resource_observation", "资源指标暂无可用观测"],
    ["unsupported_platform", "当前平台不支持"],
    ["not_configured", "尚无检查证据"],
    ["read_failed", "来源读取失败"],
    ["invalid_data", "格式无法验证"],
    ["clock_reset", "时钟未向前推进"],
    ["counter_reset", "计数器回退或计数字段数量变化"],
    ["zero_delta", "计数在采样区间没有增长"],
    ["first_observation", "CPU 需要两次计数"],
  ])("explains the verified meaning of %s", (code, explanation) => {
    expect(observationReason(code)).toContain(explanation);
    expect(observationReason(code)).toMatch(/请|核对|检查|配置/);
  });
});

describe("system table accessibility", () => {
  it("declares explicit column scopes for node, dependency and worker evidence headers", async () => {
    override = (url) =>
      url === "/api/system/health"
        ? response({
            ...health(),
            worker: {
              ...health().worker,
              cycles: [
                {
                  workerId: "synthetic-worker",
                  cycle: "queue",
                  status: "success",
                  evidence: "completed_cycle",
                  lastActivityAt: now,
                  lastSuccessAt: now,
                  availability: "available",
                },
              ],
            },
          })
        : undefined;
    await start();
    const assertColumns = (table: HTMLElement, count: number) => {
      const columns = within(table).getAllByRole("columnheader");
      expect(columns).toHaveLength(count);
      for (const header of columns)
        expect(header).toHaveAttribute("scope", "col");
      for (const header of within(table).getAllByRole("rowheader"))
        expect(header).toHaveAttribute("scope", "row");
    };
    assertColumns(screen.getByRole("table", { name: "已登记节点" }), 5);
    click("平台依赖健康");
    assertColumns(
      await screen.findByRole("table", { name: "实际依赖检查" }),
      6,
    );
    assertColumns(screen.getByRole("table", { name: "Worker 循环观测" }), 7);
  });
});

const containerMetric = (): ContainerMemoryMetric => ({
  observedAt: now,
  source: "cgroup_v2",
  scope: "runtime_cgroup",
  availability: "available",
  usageBytes: "1250",
  limitBytes: "1000",
  percent: 125,
  limitState: "finite",
  hierarchy: "visible",
});
describe("container memory contract and interface", () => {
  it("accepts finite, unlimited, unknown and legacy observations while preserving kernel memory", () => {
    const raw = current();
    expect(validCurrent(raw)).toBe(true);
    raw.snapshot!.containerMemory = containerMetric();
    expect(validCurrent(raw)).toBe(true);
    expect(raw.snapshot!.memory.percent).toBe(20);
    expect(
      validContainerMemory({
        ...containerMetric(),
        source: "cgroup_v1",
        usageBytes: "0",
        percent: 0,
      }),
    ).toBe(true);
    expect(
      validContainerMemory({
        ...containerMetric(),
        availability: "unavailable",
        reason: "unlimited",
        limitState: "unlimited",
        limitBytes: null,
        percent: null,
      }),
    ).toBe(true);
    for (const availability of ["unsupported", "unavailable"])
      expect(
        validContainerMemory({
          ...containerMetric(),
          availability,
          reason: "read_failed",
          limitState: "unknown",
          usageBytes: null,
          limitBytes: null,
          percent: null,
        }),
      ).toBe(true);
  });
  it.each([
    { percent: 0 },
    { reason: "unlimited" },
    { observedAt: "2026-10-06T08:00:00Z" },
    { percent: -1 },
    { percent: Infinity },
    { percent: NaN },
    { percent: null },
    { limitBytes: "0" },
    { usageBytes: "-1" },
    { usageBytes: "18446744073709551616" },
    { limitBytes: "1.5" },
    { limitBytes: 1000 },
    { scope: "kernel_visible" },
    { hierarchy: "effective" },
    { source: "linux_proc" },
    { source: "cgroup" },
    { observedAt: "invalid" },
    { availability: "unavailable" },
    { limitState: "unknown" },
    {
      limitState: "unlimited",
      availability: "unavailable",
      reason: "unlimited",
      percent: null,
    },
    {
      limitState: "unknown",
      availability: "unsupported",
      usageBytes: null,
      limitBytes: null,
      percent: null,
    },
  ])("rejects malformed or contradictory container data: %j", (patch) => {
    const raw = current();
    raw.snapshot!.containerMemory = {
      ...containerMetric(),
      ...patch,
    } as ContainerMemoryMetric;
    expect(validCurrent(raw)).toBe(false);
  });
  it("permits above-100 container history but keeps kernel graph validation bounded", () => {
    const query: HistoryInput = {
      instance: "local-api",
      graphType: "container_ram",
      startTime: seconds - 900,
      endTime: seconds,
    };
    const raw = history(
      new URLSearchParams({
        graphType: query.graphType,
        startTime: String(query.startTime),
      }),
    );
    raw.info[0].data.value = ["125", "130"];
    raw.info[0].data.dataStatistics = {
      max: 130,
      min: 125,
      avg: 127.5,
      current: 130,
    };
    expect(validHistory(raw, query)).toBe(true);
    raw.graphType = "ram_basic";
    raw.info[0].mode = "ram_basic";
    expect(validHistory(raw, { ...query, graphType: "ram_basic" })).toBe(false);
  });
  it("shows actual container usage and visible hierarchy without relabeling kernel RAM", async () => {
    const value = info();
    value.current.snapshot!.containerMemory = containerMetric();
    override = (url) =>
      url === "/api/system/info" ? response(value) : undefined;
    await start();
    const card = screen.getByRole("region", { name: "容器内存观测" });
    expect(within(card).getByText("125.00%")).toBeVisible();
    expect(within(card).getByText(/可见层级上限：1,000 B/)).toBeVisible();
    expect(within(card).getByText(/隐藏祖先约束与同组进程竞争/)).toBeVisible();
    expect(within(card).queryByText("健康")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "内存使用率" })).toBeVisible();
  });
  it.each(["legacy", "unlimited", "unknown", "stale"])(
    "keeps %s container state explicitly unconfirmed",
    async (state) => {
      const value = info();
      if (state !== "legacy")
        value.current.snapshot!.containerMemory = containerMetric();
      if (state === "unlimited")
        Object.assign(value.current.snapshot!.containerMemory!, {
          availability: "unavailable",
          reason: "unlimited",
          limitState: "unlimited",
          limitBytes: null,
          percent: null,
        });
      if (state === "unknown")
        Object.assign(value.current.snapshot!.containerMemory!, {
          availability: "unsupported",
          reason: "memory_controller_missing",
          limitState: "unknown",
          usageBytes: null,
          limitBytes: null,
          percent: null,
        });
      if (state === "stale") value.current.stale = true;
      override = (url) =>
        url === "/api/system/info" ? response(value) : undefined;
      await start();
      const card = screen.getByRole("region", { name: "容器内存观测" });
      expect(card.querySelector('[data-state="unknown"]')).not.toBeNull();
      expect(card.querySelector('[data-state="healthy"]')).toBeNull();
      const expected = {
        legacy: /没有容器内存观测字段/,
        unlimited: /无法计算配额使用率/,
        unknown: /容器内存压力未知/,
        stale: /当前压力未知/,
      }[state]!;
      expect(within(card).getByText(expected)).toBeVisible();
    },
  );
  it("clears invalid container API output instead of showing healthy data", async () => {
    const value = info();
    value.current.snapshot!.containerMemory = {
      ...containerMetric(),
      percent: -1,
    };
    override = (url) =>
      url === "/api/system/info" ? response(value) : undefined;
    render(<SystemWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText(/服务器结果无法确认/);
    expect(
      screen.queryByRole("region", { name: "容器内存观测" }),
    ).not.toBeInTheDocument();
  });
  it("queries persisted container history and scales actual points above 100%", async () => {
    override = (url) => {
      if (!url.includes("/resources/history") || !url.includes("container_ram"))
        return undefined;
      const raw = history(new URLSearchParams(url.split("?")[1]));
      raw.info[0].data.value = ["125", "130"];
      raw.info[0].data.dataStatistics = {
        max: 130,
        min: 125,
        avg: 127.5,
        current: 130,
      };
      return response(raw);
    };
    await start();
    click("资源与历史");
    await screen.findByRole("img", { name: /2 个实际样本/ });
    fill("历史指标", "container_ram");
    const graph = await screen.findByRole("img", {
      name: /容器内存使用率：2 个实际样本/,
    });
    expect(within(graph).getByText("130%")).toBeInTheDocument();
    for (const point of graph.querySelectorAll("circle"))
      expect(Number(point.getAttribute("cy"))).toBeGreaterThanOrEqual(15);
    expect(calls("/resources/history").at(-1)![0]).toContain(
      "graphType=container_ram",
    );
    expect(screen.getByText(/仅使用已持久化的有限上限观测/)).toBeVisible();
  });
});

describe("container memory health diagnostics", () => {
  it.each([
    ["unknown", "unlimited"],
    ["unhealthy", "visible_limit_reached"],
  ])("explains %s evidence and next steps", async (status, reason) => {
    const raw = health();
    raw.dependencies.push({
      id: "container-memory",
      name: "Runtime cgroup memory",
      type: "service",
      status,
      reason,
      source: "cgroup_v2",
      scope: "runtime_cgroup",
      checkedAt: now,
      observedAt: now,
    });
    override = (url) =>
      url === "/api/system/health" ? response(raw) : undefined;
    await start();
    click("平台依赖健康");
    const dashboard = await screen.findByRole("region", {
      name: "依赖诊断工作台",
    });
    const finding = within(dashboard)
      .getByText("Runtime cgroup memory")
      .closest("li")!;
    expect(finding).toHaveTextContent(observationReason(reason));
    expect(finding).toHaveTextContent("隐藏祖先仍可能施加额外约束");
    expect(finding.querySelector('[data-state="healthy"]')).toBeNull();
  });
});

describe("container observation trust and boundaries", () => {
  const query: HistoryInput = {
    instance: "local-api",
    graphType: "container_ram",
    startTime: seconds - 900,
    endTime: seconds,
  };
  const segmented = () => ({
    ...history(
      new URLSearchParams({
        graphType: query.graphType,
        startTime: String(query.startTime),
      }),
    ),
    segments: [
      {
        startTime: seconds - 30,
        source: "cgroup_v2",
        limitBytes: "1000",
        sourceID: "a".repeat(64),
      },
      { startTime: seconds - 15, source: "cgroup_v1", limitBytes: "2000" },
    ],
  });
  it.each([undefined, "a".repeat(64), "0123456789abcdef".repeat(4)])(
    "accepts legacy or valid source identity %s",
    (sourceID) => {
      expect(validContainerMemory({ ...containerMetric(), sourceID })).toBe(
        true,
      );
    },
  );
  it.each(["", "a".repeat(63), "a".repeat(65), "z".repeat(64), 123, null])(
    "rejects invalid source identity %s",
    (sourceID) => {
      expect(validContainerMemory({ ...containerMetric(), sourceID })).toBe(
        false,
      );
    },
  );
  it("validates real ordered boundaries including legacy identity absence", () => {
    expect(validHistory(segmented(), query)).toBe(true);
    const raw = segmented();
    raw.segments = [];
    expect(validHistory(raw, query)).toBe(false);
    const empty = {
      ...raw,
      info: [
        {
          mode: query.graphType,
          data: {
            timestamp: [],
            value: [],
            dataStatistics: { max: null, min: null, avg: null, current: null },
          },
        },
      ],
    };
    expect(validHistory(empty, query)).toBe(true);
  });
  it.each([
    { startTime: seconds - 29 },
    { startTime: seconds },
    { startTime: seconds - 901 },
    { source: "cgroup" },
    { source: "linux_proc" },
    { limitBytes: "0" },
    { limitBytes: "-1" },
    { limitBytes: "18446744073709551616" },
    { sourceID: "x".repeat(64) },
    { sourceID: "" },
  ])("rejects invalid boundary %j", (patch) => {
    const raw = segmented();
    Object.assign(raw.segments[0], patch);
    expect(validHistory(raw, query)).toBe(false);
  });
  it("rejects unordered, repeated, missing-first and excessive boundaries", () => {
    const raw = segmented();
    raw.segments.reverse();
    expect(validHistory(raw, query)).toBe(false);
    raw.segments = [segmented().segments[1]];
    expect(validHistory(raw, query)).toBe(false);
    raw.segments = [segmented().segments[0], segmented().segments[0]];
    expect(validHistory(raw, query)).toBe(false);
    raw.segments = [...segmented().segments, segmented().segments[1]];
    expect(validHistory(raw, query)).toBe(false);
  });
  it("preserves distinct boundary points recorded in the same Unix second", () => {
    const raw = segmented();
    raw.info[0].data.timestamp[1] = seconds - 30;
    raw.segments[1].startTime = seconds - 30;
    expect(validHistory(raw, query)).toBe(true);
  });
  it("keeps clock-skew samples unavailable even when stale is false", async () => {
    const value = info();
    value.current.snapshot!.containerMemory = containerMetric();
    value.current.availability = "unavailable";
    value.current.stale = false;
    value.current.reason = "clock_skew";
    override = (url) =>
      url === "/api/system/info" ? response(value) : undefined;
    await start();
    const card = screen.getByRole("region", { name: "容器内存观测" });
    expect(card.querySelector('[data-state="observed"]')).toBeNull();
    expect(within(card).getByText("不可用 · 观测未确认")).toBeVisible();
    expect(
      within(card).getByText(/当前容器内存观测不可用，压力未知/),
    ).toHaveTextContent("时钟证据不一致");
  });
  it("explains source and limit boundary segments separately from sampling gaps", async () => {
    override = (url) =>
      url.includes("/resources/history") && url.includes("container_ram")
        ? response(segmented())
        : undefined;
    await start();
    click("资源与历史");
    await screen.findByRole("img", { name: /2 个实际样本/ });
    fill("历史指标", "container_ram");
    await screen.findByRole("img", {
      name: /容器内存使用率：2 个实际样本，1 个缺测区间/,
    });
    expect(screen.getByText(/观测分段：2 段/)).toHaveTextContent(
      "段边界表示计量依据变化，与缺测区间分别展示",
    );
  });
  it("explains a zero limit without claiming malformed kernel data", () => {
    expect(observationReason("zero_limit")).toContain("内存上限为零");
    expect(observationReason("zero_limit")).toContain(
      "核对运行环境的内存限制配置",
    );
  });
  it("translates the actual missing-observation backend reason", () => {
    expect(observationReason("no_cgroup_observation")).toContain(
      "此记录没有容器内存观测",
    );
  });
});
