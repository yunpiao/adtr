// DOM/mock HTTP evidence only; real PostgreSQL/browser coverage is separate.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Entry, {
  SimulationDashboardDomainWorkspace as Workspace,
  type SimulationDashboardWorkspaceProps,
} from "./SimulationDashboardWorkspace";
import {
  parseDashboardResponse,
  simulationDashboardAPI,
  type DashboardSummary,
} from "./simulation-dashboard-api";
import SocNavigation from "./SocNavigation";
import type { Profile } from "./api";
import { SESSION_INVALIDATION_STORAGE } from "./session-invalidation";
const profile: Profile = {
  ID: 1,
  username: "synthetic-reader",
  role: "custom",
  priv: 0,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: false,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: "",
  csrfToken: "csrf",
};
const dataset = {
  id: "dataset-a",
  fixture: "fixture-a",
  fixtureHash: "a".repeat(64),
  contract: "adtr.securityevents.simulated.v1",
  simulation: true as const,
  eventCount: 2,
};
const stamp = "2026-11-02T20:00:00Z";
function fixture(): DashboardSummary {
  return {
    contract: "adtr.dashboard.simulated.v1",
    simulation: true,
    productAccepted: false,
    snapshotId: "b".repeat(64),
    asOf: stamp,
    window: {
      preset: "24h",
      start: "2026-11-01T20:00:00Z",
      end: stamp,
      bucketSeconds: 3600,
    },
    dataset,
    coverage: {
      complete: true,
      eventCount: 2,
      caseCount: 1,
      sourceFirstAt: "2026-11-02T19:00:00Z",
      sourceLastAt: "2026-11-02T19:01:00Z",
      excludedFutureEvents: 0,
    },
    cases: {
      scope: "dataset_current",
      total: 1,
      open: 1,
      closed: 0,
      marks: { unmarked: 1, benign: 0, suspicious: 0 },
    },
    eventActivity: {
      scope: "selected_window",
      total: 2,
      byKind: [
        { kind: "login_failure", count: 1 },
        { kind: "login_success", count: 1 },
        { kind: "service_access", count: 0 },
      ],
      buckets: [
        {
          start: "2026-11-02T19:00:00Z",
          countsByKind: {
            login_failure: 1,
            login_success: 1,
            service_access: 0,
          },
        },
      ],
      topActors: [{ name: "fixture-user", count: 2 }],
      topDevices: [{ name: "fixture-device", count: 2 }],
      topSourceIPs: [{ name: "192.0.2.1", count: 2 }],
      topLimit: 5,
      distinctActors: 1,
      distinctDevices: 1,
    },
    caseCloseThroughput: {
      status: "available",
      unit: "case_closes_per_minute",
      windowTotal: 0,
      last60Seconds: 0,
      buckets: [{ start: "2026-11-02T19:59:00Z", count: 0 }],
    },
    risk: { status: "not_selected", trend: null, resetBoundary: null },
    latestManualDetection: { status: "no_result" },
    unavailable: [
      { metric: "asset_inventory", reason: "no_inventory_producer" },
      { metric: "lastMinuteCount", reason: "no_request_counter" },
    ],
  };
}
const reply = (value: unknown, status = 200, actor = "1") =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
  });
const props = (
  overrides: Partial<SimulationDashboardWorkspaceProps> = {},
): SimulationDashboardWorkspaceProps => ({
  profile,
  domainId: "domain-a",
  permissions: { read: true },
  sessionChanged: vi.fn(),
  ...overrides,
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
let mock: ReturnType<
  typeof vi.fn<(url: string, init?: RequestInit) => Promise<Response>>
>;
beforeEach(() => {
  mock = vi.fn(async (url) => {
    if (url.includes("/datasets?"))
      return reply({
        simulation: true,
        datasets: [dataset, { ...dataset, id: "dataset-b" }],
        total: 2,
      });
    if (url.includes("/summary?")) {
      const value = fixture();
      value.dataset = {
        ...dataset,
        id: new URL(url, "http://localhost").searchParams.get("datasetId")!,
      };
      return reply(value);
    }
    throw new Error("unexpected URL: " + url);
  });
  vi.stubGlobal("fetch", mock);
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
async function selectAndLoad() {
  await screen.findByRole("option", { name: "dataset-a · fixture-a" });
  fireEvent.change(screen.getByLabelText("合成态势数据集"), {
    target: { value: "dataset-a" },
  });
  fireEvent.click(screen.getByRole("button", { name: "加载快照" }));
}
const article = () =>
  screen.queryByRole("article", { name: "SIMULATED 合成态势快照" });
describe("synthetic dashboard", () => {
  it("fetches one source summary and shows provenance, unavailable and actual zero distinctly", async () => {
    render(<Workspace {...props()} />);
    await selectAndLoad();
    await screen.findByText(/快照标识/);
    expect(
      mock.mock.calls.filter(([u]) => u.includes("/summary?")).length,
    ).toBe(1);
    expect(screen.getByText(/窗口关闭次数 0/)).toBeTruthy();
    expect(
      screen.getByText(/asset_inventory：不可用（尚无资产清单来源）/),
    ).toBeTruthy();
    expect(screen.getByText(/未选择风险主体，未计算分数/)).toBeTruthy();
    expect(screen.getByText(/这不是资产数量或攻击判定/)).toBeTruthy();
    expect(screen.getByText(/内容指纹不是持久重放凭证/)).toBeTruthy();
    const [url, init] = mock.mock.calls.find(([u]) => u.includes("/summary?"))!;
    expect(url).not.toContain("subject=");
    expect(init?.method).toBe("GET");
    expect(init?.body).toBeUndefined();
    expect(init?.cache).toBe("no-store");
  });
  it("does not fetch without explicit current read permission or while password gated", () => {
    const view = render(
      <Workspace {...props({ permissions: { read: false } })} />,
    );
    expect(mock).not.toHaveBeenCalled();
    view.rerender(
      <Workspace
        {...props({ profile: { ...profile, needChangePwd: true } })}
      />,
    );
    expect(mock).not.toHaveBeenCalled();
  });
  it.each([401, 403, 404])("clears old scope on status %i", async (status) => {
    const p = props();
    render(<Workspace {...p} />);
    await selectAndLoad();
    await screen.findByText(/快照标识/);
    mock.mockImplementationOnce(async () => reply({}, status));
    fireEvent.click(screen.getByRole("button", { name: "刷新快照" }));
    await screen.findByText(/已清除原快照/);
    expect(article()).toBeNull();
    expect(p.sessionChanged).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
  });
  it("retains a labelled stale snapshot on transient failure and clears stale after retry", async () => {
    render(<Workspace {...props()} />);
    await selectAndLoad();
    await screen.findByText(/快照标识/);
    mock.mockImplementationOnce(async () => reply({}, 503));
    fireEvent.click(screen.getByRole("button", { name: "刷新快照" }));
    await screen.findByText(/汇总暂时不可用/);
    expect(screen.getByText(/旧快照，尚未重新验证/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "刷新快照" }));
    await waitFor(() =>
      expect(screen.queryByText(/旧快照，尚未重新验证/)).toBeNull(),
    );
  });
  it("cancels and ignores a slow response when changing dataset", async () => {
    const pending = deferred<Response>();
    const original = mock.getMockImplementation()!;
    mock.mockImplementation((u, i) =>
      u.includes("/summary?") && u.includes("dataset-a")
        ? pending.promise
        : original(u, i),
    );
    render(<Workspace {...props()} />);
    await selectAndLoad();
    const signal = mock.mock.calls.find(([u]) => u.includes("/summary?"))![1]!
      .signal!;
    fireEvent.change(screen.getByLabelText("合成态势数据集"), {
      target: { value: "dataset-b" },
    });
    expect(signal.aborted).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "加载快照" }));
    await screen.findByText(/数据集：dataset-b/);
    await act(async () => pending.resolve(reply(fixture())));
    expect(screen.queryByText(/数据集：dataset-a/)).toBeNull();
  });
  it("clears old data on domain and account changes", async () => {
    const view = render(<Workspace {...props()} />);
    await selectAndLoad();
    await screen.findByText(/快照标识/);
    view.rerender(<Workspace {...props({ domainId: "domain-b" })} />);
    expect(article()).toBeNull();
    await screen.findByRole("option", { name: "dataset-a · fixture-a" });
    view.rerender(<Workspace {...props({ profile: { ...profile, ID: 2 } })} />);
    expect(article()).toBeNull();
  });
  it.each(["popstate", "hashchange"])(
    "does not restore private query or snapshot on %s",
    async (kind) => {
      render(<Workspace {...props()} />);
      await selectAndLoad();
      await screen.findByText(/快照标识/);
      act(() => window.dispatchEvent(new Event(kind)));
      expect(article()).toBeNull();
      expect(screen.getByLabelText("合成态势数据集")).toHaveValue("");
    },
  );
  it("clears and aborts after cross-tab session invalidation", async () => {
    const p = props();
    render(<Workspace {...p} />);
    await selectAndLoad();
    await screen.findByText(/快照标识/);
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: SESSION_INVALIDATION_STORAGE,
          newValue: "a".repeat(32),
          storageArea: localStorage,
        }),
      ),
    );
    expect(article()).toBeNull();
    expect(p.sessionChanged).toHaveBeenCalledTimes(1);
  });
  it("rejects a success response bound to a different actor", async () => {
    const p = props();
    const original = mock.getMockImplementation()!;
    mock.mockImplementation((u, i) =>
      u.includes("/summary?")
        ? Promise.resolve(reply(fixture(), 200, "2"))
        : original(u, i),
    );
    render(<Workspace {...p} />);
    await selectAndLoad();
    await screen.findByText(/已清除原快照/);
    expect(article()).toBeNull();
    expect(p.sessionChanged).toHaveBeenCalledTimes(1);
  });
  it("rejects incomplete dataset discovery instead of pretending no more sources exist", async () => {
    mock.mockResolvedValue(
      reply({ simulation: true, datasets: [dataset], total: 101 }),
    );
    render(<Workspace {...props()} />);
    await screen.findByText(/未截断为不完整总数/);
    expect(
      screen.queryByRole("option", { name: "dataset-a · fixture-a" }),
    ).toBeNull();
  });
  it("shows subject-not-found without a zero risk score", async () => {
    render(<Workspace {...props()} />);
    await screen.findByRole("option", { name: "dataset-a · fixture-a" });
    fireEvent.change(screen.getByLabelText("合成态势数据集"), {
      target: { value: "dataset-a" },
    });
    fireEvent.change(screen.getByLabelText("风险主体（可选）"), {
      target: { value: "unknown" },
    });
    const v = fixture();
    v.risk = {
      status: "subject_not_found",
      subject: "unknown",
      trend: null,
      resetBoundary: null,
    };
    mock.mockResolvedValueOnce(reply(v));
    fireEvent.click(screen.getByRole("button", { name: "加载快照" }));
    await screen.findByText(/所选主体在此来源中不存在/);
    expect(screen.queryByText(/示例分数 0/)).toBeNull();
  });
  it("shows available zero risk and a real generic result separately from unavailable original detectors", async () => {
    render(<Workspace {...props()} />);
    await screen.findByRole("option", { name: "dataset-a · fixture-a" });
    fireEvent.change(screen.getByLabelText("合成态势数据集"), {
      target: { value: "dataset-a" },
    });
    fireEvent.change(screen.getByLabelText("风险主体（可选）"), {
      target: { value: "fixture-user" },
    });
    const v = fixture();
    v.risk = {
      status: "available",
      subject: "fixture-user",
      mappingVersion: "illustrative-event-risk-v1",
      revision: 1,
      score: 0,
      trend: [{ at: stamp, score: 0 }],
      resetBoundary: null,
    };
    v.latestManualDetection = {
      status: "available",
      taskUUID: "task-a",
      completedAt: stamp,
      templateId: "template-a",
      templateVersion: 2,
      templateHash: "c".repeat(64),
      eventCount: 2,
      matchCount: 1,
    };
    mock.mockResolvedValueOnce(reply(v));
    fireEvent.click(screen.getByRole("button", { name: "加载快照" }));
    await screen.findByText(/示例分数 0/);
    expect(screen.getByText(/不是基线、漏洞或弱口令检测覆盖/)).toBeTruthy();
  });
  it("requires all three live menu grants even when admin profile hints are true", async () => {
    mock.mockImplementation(async (u) =>
      u.includes("/menu")
        ? reply({
            menu: ["domains", "simulation_events", "simulation_risk"].map(
              (mark) => ({ mark, auth: { readable: true } }),
            ),
          })
        : reply({ results: [true, true, true] }),
    );
    render(
      <Entry
        profile={{
          ...profile,
          role: "platform_admin",
          simulationEventsReadable: true,
          simulationRiskReadable: true,
          simulationDetectionReadable: true,
        }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByText(/未加载任何态势数据/);
    expect(mock.mock.calls.some(([u]) => u.includes("/datasets"))).toBe(false);
  });
  it("navigation needs the conjunction of all optional readable hints", () => {
    const nav = (p: Profile) => (
      <SocNavigation
        profile={p}
        active="account"
        forced={false}
        busy={false}
        gated={false}
        blocked={false}
        mobileOpen={false}
        onMobileOpenChange={vi.fn()}
        onNavigate={vi.fn()}
        onLogout={vi.fn()}
      />
    );
    const view = render(
      nav({
        ...profile,
        simulationEventsReadable: true,
        simulationRiskReadable: true,
      }),
    );
    expect(screen.queryByRole("button", { name: "合成态势总览" })).toBeNull();
    view.rerender(
      nav({
        ...profile,
        simulationEventsReadable: true,
        simulationRiskReadable: true,
        simulationDetectionReadable: true,
      }),
    );
    expect(screen.getByRole("button", { name: "合成态势总览" })).toBeTruthy();
  });
});
describe("dashboard response boundary", () => {
  it("accepts the frozen synthetic envelope", () => {
    expect(parseDashboardResponse(fixture()).dataset.id).toBe("dataset-a");
  });
  it.each([
    (v: DashboardSummary) => {
      v.eventActivity.byKind.pop();
    },
    (v: DashboardSummary) => {
      delete v.eventActivity.buckets[0].countsByKind.service_access;
    },
    (v: DashboardSummary) => {
      v.eventActivity.buckets[0].countsByKind.login_failure = 4;
    },
    (v: DashboardSummary) => {
      v.productAccepted = true as false;
    },
    (v: DashboardSummary) => {
      v.cases.total = 3;
    },
    (v: DashboardSummary) => {
      v.coverage.complete = false as true;
    },
    (v: DashboardSummary) => {
      v.eventActivity.total = 3;
    },
    (v: DashboardSummary) => {
      v.risk.score = 0;
    },
    (v: DashboardSummary) => {
      v.latestManualDetection.eventCount = 0;
    },
    (v: DashboardSummary) => {
      v.window.end = "2026-11-02T21:00:00Z";
    },
    (v: DashboardSummary) => {
      v.caseCloseThroughput.windowTotal = 5;
    },
  ])("rejects corrupted evidence %#", (change) => {
    const v = fixture();
    change(v);
    expect(() => parseDashboardResponse(v)).toThrow();
  });
  it("binds dataset, selected window and subject to the request", () => {
    expect(() =>
      parseDashboardResponse(fixture(), {
        domainId: "domain-a",
        datasetId: "wrong",
        window: "24h",
      }),
    ).toThrow();
    expect(() =>
      parseDashboardResponse(fixture(), {
        domainId: "domain-a",
        datasetId: "dataset-a",
        window: "1h",
      }),
    ).toThrow();
    expect(() =>
      parseDashboardResponse(fixture(), {
        domainId: "domain-a",
        datasetId: "dataset-a",
        subject: "x",
        window: "24h",
      }),
    ).toThrow();
  });
  it("rejects oversized response before parsing", async () => {
    mock.mockResolvedValue(
      new Response(" ".repeat(2 * 1024 * 1024 + 1), {
        headers: { "X-ADTR-User-ID": "1" },
      }),
    );
    await expect(
      simulationDashboardAPI.summary(
        { domainId: "domain-a", datasetId: "dataset-a", window: "24h" },
        profile,
        new AbortController().signal,
      ),
    ).rejects.toThrow();
  });
});
