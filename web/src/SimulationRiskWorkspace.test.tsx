// Synthetic DOM + mocked HTTP only, not real browser/AD/PostgreSQL evidence.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import SimulationEntry, {
  SimulationRiskDomainWorkspace as Workspace,
  type SimulationRiskWorkspaceProps,
} from "./SimulationRiskWorkspace";
import { type Profile } from "./api";
import { parseRiskResponse } from "./simulation-risk-api";
const profile: Profile = {
  ID: 1,
  username: "reader",
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
  simulation: true,
  eventCount: 1,
};
const result = {
  simulated: true,
  dataset,
  version: "simulated-risk-v1",
  contract: "adtr.risk.simulated.v1",
  mappingVersion: "illustrative-event-risk-v1",
  revision: 1,
  scope: {
    tenant_id: "synthetic",
    domain_id: "domain-a",
    user_id: "subject-a",
  },
  as_of: "2026-11-01T00:00:00Z",
  score: 5,
  evidence_count: 1,
  duplicates: 0,
  ignored_future: 0,
  sourceEventCount: 1,
  noRiskEventCount: 0,
  distribution: [{ kind: "low", count: 1, weight: 5, raw_points: 5 }],
  records: [
    {
      id: "event-a",
      at: "2026-11-01T00:00:00Z",
      kind: "low",
      before: 0,
      after: 5,
      delta: 5,
    },
  ],
  trend: [{ at: "2026-11-01T00:00:00Z", score: 5 }],
};
const receipt = (operationId: string) => ({
  simulation: true,
  operationId,
  actor: "1",
  reason: "synthetic reason",
  result: {
    ...result,
    revision: 2,
    score: 0,
    records: [
      ...result.records,
      {
        id: "reset-a",
        at: "2026-11-01T00:00:01Z",
        kind: "reset",
        reason: "synthetic reason",
        before: 5,
        after: 0,
        delta: -5,
      },
    ],
  },
});
const reply = (data: unknown, status = 200, actor = "1") =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
  });
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
let fetchMock: ReturnType<
  typeof vi.fn<(url: string, init?: RequestInit) => Promise<Response>>
>;
const path = (url: string) => new URL(url, "http://localhost").pathname;
const props = (
  overrides: Partial<SimulationRiskWorkspaceProps> = {},
): SimulationRiskWorkspaceProps => ({
  profile,
  domainId: "domain-a",
  permissions: { read: true, manage: true },
  sessionChanged: vi.fn(),
  ...overrides,
});
async function query() {
  await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
  fireEvent.change(screen.getByLabelText("风险数据集"), {
    target: { value: "dataset-a" },
  });
  fireEvent.change(screen.getByLabelText("证据操作者"), {
    target: { value: "subject-a" },
  });
  fireEvent.click(screen.getByText("查询模拟风险"));
}
async function loaded() {
  await query();
  await screen.findByText("模拟分数：5");
}
function reset() {
  fireEvent.change(screen.getByLabelText("重置原因"), {
    target: { value: "synthetic reason" },
  });
  fireEvent.click(screen.getByText("重置模拟风险"));
}
beforeEach(() => {
  fetchMock = vi.fn(async (url: string) => {
    if (path(url).endsWith("/datasets"))
      return reply({ simulation: true, datasets: [dataset], total: 1 });
    if (path(url).endsWith("/query")) return reply(result);
    return reply({ error: "not_found" }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
describe("simulation risk workspace", () => {
  it("defaults deny even administrator and missing grants", () => {
    render(
      <Workspace
        {...props({
          permissions: undefined,
          profile: { ...profile, role: "admin" },
        })}
      />,
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it("loads explicit selected dataset and actor; renders provenance, mapping, distribution, trend and ledger", async () => {
    render(<Workspace {...props()} />);
    await loaded();
    expect(screen.getByText("风险分布（服务端权重）")).toBeVisible();
    expect(screen.getByText("重置账本")).toBeVisible();
    expect(screen.getByText(/login_failure → low/)).toBeVisible();
    const call = fetchMock.mock.calls.find(([u]) =>
      path(u).endsWith("/query"),
    )!;
    expect(JSON.parse(call[1]!.body as string)).toEqual({
      domainId: "domain-a",
      datasetId: "dataset-a",
      subject: "subject-a",
    });
    expect(call[1]!.headers).toEqual({
      "Content-Type": "application/json",
      "X-CSRF-Token": "csrf",
    });
  });
  it("read-only has no reset", async () => {
    render(
      <Workspace {...props({ permissions: { read: true, manage: false } })} />,
    );
    await loaded();
    expect(screen.getByText("重置模拟风险")).toBeDisabled();
  });
  it.each([401, 403, 404])("clears scope on query %s", async (status) => {
    const p = props();
    render(<Workspace {...p} />);
    await loaded();
    fetchMock.mockResolvedValueOnce(reply({ error: "not_found" }, status));
    fireEvent.click(screen.getByText("查询模拟风险"));
    await screen.findByText(/当前会话、权限或记录范围已失效/);
    expect(screen.queryByText("模拟分数：5")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: "fixture-a (dataset-a)" }),
    ).not.toBeInTheDocument();
    if (status === 401) expect(p.sessionChanged).toHaveBeenCalled();
  });
  it.each(["", "2"])(
    "rejects missing or different actor header %s",
    async (actor) => {
      render(<Workspace {...props()} />);
      await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
      fetchMock.mockResolvedValueOnce(reply(result, 200, actor));
      await query();
      await screen.findByText(/当前会话、权限或记录范围已失效/);
      expect(screen.queryByText("模拟分数：5")).not.toBeInTheDocument();
    },
  );
  it.each([
    { ...result, simulated: false },
    { ...result, score: -1 },
    { ...result, score: 101 },
    { ...result, mappingVersion: "unknown" },
    { ...result, trend: null },
    { ...result, dataset: { ...dataset, simulation: false } },
  ])("rejects malformed/unproven simulated results", (v) => {
    expect(() => parseRiskResponse("query", v)).toThrow();
  });
  it("does not manufacture zero for unavailable storage", async () => {
    render(<Workspace {...props()} />);
    await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
    fetchMock.mockResolvedValueOnce(
      reply({ error: "simulation_unavailable" }, 503),
    );
    await query();
    await screen.findByText(/没有可确认分数/);
    expect(screen.queryByText(/模拟分数：/)).not.toBeInTheDocument();
  });
  it("locks repeated reset and recovers lost response receipt-first with exact GET keys", async () => {
    const lost = deferred<Response>();
    let id = "";
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, init) => {
      if (path(url).endsWith("/reset")) {
        const q = JSON.parse(init!.body as string);
        id = q.operationId;
        expect(q.expectedRevision).toBe(1);
        expect(q.reason).toBe("synthetic reason");
        return lost.promise;
      }
      if (path(url).endsWith("/operation")) {
        expect(
          [...new URL(url, "http://localhost").searchParams.keys()].sort(),
        ).toEqual(["datasetId", "domainId", "operationId"]);
        return Promise.resolve(reply(receipt(id)));
      }
      return original(url, init);
    });
    render(<Workspace {...props()} />);
    await loaded();
    reset();
    fireEvent.click(screen.getByText("重置模拟风险"));
    await act(async () => lost.resolve(reply({ error: "temporary" }, 503)));
    await screen.findByText("模拟分数：0");
    expect(
      fetchMock.mock.calls.filter(([u]) => path(u).endsWith("/reset")),
    ).toHaveLength(1);
  });
  it("recovers same ID after missing receipt and changed preflight revision without client veto", async () => {
    let writes = 0;
    let originalBody = "";
    let lookup = 0;
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, init) => {
      if (path(url).endsWith("/reset")) {
        writes++;
        if (writes === 1) {
          originalBody = init!.body as string;
          return Promise.resolve(reply({ error: "temporary" }, 503));
        }
        expect(init!.body).toBe(originalBody);
        return Promise.resolve(
          reply(receipt(JSON.parse(originalBody).operationId)),
        );
      }
      if (path(url).endsWith("/operation")) {
        lookup++;
        return Promise.resolve(reply({ error: "not_found" }, 404));
      }
      if (path(url).endsWith("/query") && writes) {
        expect(JSON.parse(init!.body as string)).toEqual({
          domainId: "domain-a",
          datasetId: "dataset-a",
          subject: "subject-a",
        });
        return Promise.resolve(reply({ ...result, revision: 2 }));
      }
      return original(url, init);
    });
    render(<Workspace {...props()} />);
    await loaded();
    reset();
    await screen.findByText(/重置结果未确认/);
    fireEvent.click(screen.getByText("核验原操作并安全重试"));
    await screen.findByText("模拟分数：0");
    expect(writes).toBe(2);
    expect(lookup).toBe(2);
    // Recovery clears selectors before scoped preflight. Provenance must come
    // from the confirmed receipt, not from the now-empty dataset catalogue.
    expect(screen.getByLabelText("风险数据集")).toHaveValue("");
    const provenance = screen.getByLabelText("结果证据来源");
    expect(provenance).toHaveTextContent("SIMULATED");
    expect(provenance).toHaveTextContent(dataset.id);
    expect(provenance).toHaveTextContent(dataset.fixture);
    expect(provenance).toHaveTextContent(dataset.fixtureHash);
    expect(provenance).toHaveTextContent(dataset.contract);
  });
  it("does not replay if scope preflight is revoked", async () => {
    let writes = 0;
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, init) => {
      if (path(url).endsWith("/reset")) {
        writes++;
        return Promise.resolve(reply({ error: "temporary" }, 503));
      }
      if (
        path(url).endsWith("/operation") ||
        (path(url).endsWith("/query") && writes)
      )
        return Promise.resolve(reply({ error: "not_found" }, 404));
      return original(url, init);
    });
    render(<Workspace {...props()} />);
    await loaded();
    reset();
    await screen.findByText(/重置结果未确认/);
    fireEvent.click(screen.getByText("核验原操作并安全重试"));
    await screen.findByText(/当前会话、权限或记录范围已失效/);
    expect(writes).toBe(1);
    expect(screen.queryByText(/模拟分数：/)).not.toBeInTheDocument();
  });
  it("cancelled query ignores late response", async () => {
    const late = deferred<Response>();
    render(<Workspace {...props()} />);
    await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
    fetchMock.mockReturnValueOnce(late.promise);
    await query();
    fireEvent.click(screen.getByText("取消等待"));
    await act(async () => late.resolve(reply(result)));
    expect(screen.queryByText("模拟分数：5")).not.toBeInTheDocument();
  });
  it("domain identity switch discards pending old response", async () => {
    const late = deferred<Response>();
    const p = props();
    const view = render(<Workspace {...p} />);
    await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
    fetchMock.mockReturnValueOnce(late.promise);
    await query();
    view.rerender(<Workspace {...p} domainId="domain-b" />);
    await act(async () => late.resolve(reply(result)));
    expect(screen.queryByText("模拟分数：5")).not.toBeInTheDocument();
    expect(screen.getByLabelText("证据操作者")).toHaveValue("");
  });
  it("does not infer risk grant from event grant", async () => {
    fetchMock.mockImplementation(async (url) =>
      path(url).endsWith("/menu")
        ? reply({
            menu: [
              { mark: "domains", auth: { readable: true } },
              { mark: "simulation_events", auth: { readable: true } },
            ],
          })
        : reply({ results: Array(6).fill(true) }),
    );
    render(<SimulationEntry profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("当前账户未获得模拟风险与域选择读取权限。");
    expect(
      fetchMock.mock.calls.some(([u]) => path(u).endsWith("/datasets")),
    ).toBe(false);
  });
  it("actor mismatch in query is rejected", async () => {
    render(<Workspace {...props()} />);
    await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
    fetchMock.mockResolvedValueOnce(
      reply({ ...result, scope: { ...result.scope, user_id: "other" } }),
    );
    await query();
    await waitFor(() =>
      expect(screen.queryByRole("article")).not.toBeInTheDocument(),
    );
    expect(screen.queryByText("模拟分数：5")).not.toBeInTheDocument();
  });
  it("cancelled reset preserves receipt recovery and ignores late POST", async () => {
    const late = deferred<Response>();
    let id = "";
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, init) => {
      if (path(url).endsWith("/reset")) {
        id = JSON.parse(init!.body as string).operationId;
        return late.promise;
      }
      if (path(url).endsWith("/operation"))
        return Promise.resolve(reply(receipt(id)));
      return original(url, init);
    });
    render(<Workspace {...props()} />);
    await loaded();
    reset();
    fireEvent.click(screen.getByText("取消等待"));
    await act(async () => late.resolve(reply(receipt(id))));
    expect(screen.queryByText("模拟分数：0")).not.toBeInTheDocument();
    expect(screen.getByText("查询模拟风险")).toBeDisabled();
    fireEvent.click(screen.getByText("核验原操作并安全重试"));
    await screen.findByText("模拟分数：0");
    expect(
      fetchMock.mock.calls.filter(([u]) => path(u).endsWith("/reset")),
    ).toHaveLength(1);
  });
  it("Back cancels pending read and session identity remount clears filters", async () => {
    const late = deferred<Response>();
    const p = props();
    const view = render(<Workspace {...p} />);
    await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
    fetchMock.mockReturnValueOnce(late.promise);
    await query();
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    await act(async () => late.resolve(reply(result)));
    expect(screen.queryByText("模拟分数：5")).not.toBeInTheDocument();
    view.rerender(
      <Workspace {...p} profile={{ ...profile, csrfToken: "new-session" }} />,
    );
    expect(screen.getByLabelText("证据操作者")).toHaveValue("");
  });
  it("revision conflict clears score and requires a fresh query before another reset", async () => {
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, init) =>
      path(url).endsWith("/reset")
        ? Promise.resolve(reply({ error: "revision_conflict" }, 409))
        : original(url, init),
    );
    render(<Workspace {...props()} />);
    await loaded();
    reset();
    await screen.findByText(/风险版本已变化/);
    expect(screen.queryByText("重置模拟风险")).not.toBeInTheDocument();
    expect(screen.queryByText("核验原操作并安全重试")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("查询模拟风险"));
    await screen.findByText("模拟分数：5");
  });
  it("bad receipt identity never confirms success and retains unresolved original operation", async () => {
    let id = "";
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url, init) => {
      if (path(url).endsWith("/reset")) {
        id = JSON.parse(init!.body as string).operationId;
        return Promise.resolve(
          reply({ ...receipt(id), actor: "different-user" }),
        );
      }
      if (path(url).endsWith("/operation"))
        return Promise.resolve(
          reply({ ...receipt(id), operationId: "different-operation" }),
        );
      return original(url, init);
    });
    render(<Workspace {...props()} />);
    await loaded();
    reset();
    await screen.findByText(/重置结果未确认/);
    expect(screen.queryByText("模拟分数：0")).not.toBeInTheDocument();
    expect(screen.getByText("核验原操作并安全重试")).toBeEnabled();
  });
  it("disabled service clears data and reports the actual disabled state", async () => {
    fetchMock.mockResolvedValueOnce(
      reply({ error: "simulation_disabled" }, 404),
    );
    render(<Workspace {...props()} />);
    await screen.findByText("模拟风险功能未启用。");
    expect(screen.queryByText(/模拟分数：/)).not.toBeInTheDocument();
  });
  it("rejects malformed gate and checks risk-specific operations", async () => {
    fetchMock.mockImplementation(async (url, init) => {
      if (path(url).endsWith("/menu"))
        return reply({
          menu: [
            { mark: "domains", auth: { readable: true } },
            { mark: "simulation_risk", auth: { readable: true } },
          ],
        });
      expect(JSON.parse(init!.body as string).paths).toEqual([
        "GET /api/domain-selection",
        "GET /api/domain-selection/resolve",
        "GET /api/simulation/risk/datasets",
        "POST /api/simulation/risk/query",
        "POST /api/simulation/risk/reset",
        "GET /api/simulation/risk/operation",
      ]);
      return reply({ results: [true] });
    });
    render(<SimulationEntry profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("未能确认模拟风险及域选择权限，请重新核对。");
    expect(fetchMock.mock.calls).toHaveLength(2);
  });
});
