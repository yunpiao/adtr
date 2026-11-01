// Synthetic DOM and HTTP mocks only; no real browser/PostgreSQL/AD evidence.
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
  SimulationEventsDomainWorkspace as SimulationEventsWorkspace,
  type SimulationEventsWorkspaceProps,
} from "./SimulationEventsWorkspace";
import { type Profile } from "./api";
import { parseSimulationResponse } from "./simulation-event-api";
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
const provenance = {
  Contract: "adtr.securityevents.simulated.v1",
  Fixture: "fixture-a",
  Synthetic: true,
};
const dataset = {
  id: "dataset-a",
  fixture: "fixture-a",
  fixtureHash: "a".repeat(64),
  contract: provenance.Contract,
  simulation: true,
  eventCount: 1,
};
const event = {
  ID: "event-a",
  Tenant: "synthetic",
  Domain: "domain-a",
  Kind: "login_failure",
  Actor: "synthetic-user",
  Device: "synthetic-device",
  Session: "synthetic-session",
  SourceIP: "192.0.2.1",
  OccurredAt: "2026-11-01T00:00:00Z",
  Provenance: provenance,
};
const caseView = {
  ID: "case-a",
  State: "open",
  Mark: "",
  Revision: 1,
  Provenance: provenance,
};
const detail = {
  simulation: true,
  dataset,
  case: caseView,
  evidence: [event],
  history: [],
  historyTotal: 0,
};
const audit = {
  CaseID: "case-a",
  Action: "mark_suspicious",
  Actor: "reader",
  Reason: "synthetic reason",
  PreviousState: "open",
  State: "open",
  PreviousMark: "",
  Mark: "suspicious",
  At: "2026-11-01T00:00:01Z",
  Revision: 2,
  Provenance: provenance,
};
const receipt = (operationId: string) => ({
  simulation: true,
  dataset,
  datasetId: dataset.id,
  operationId,
  case: { ...caseView, Mark: "suspicious", Revision: 2 },
  audit,
});
const reply = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": "1" },
  });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
let fetchMock: ReturnType<
  typeof vi.fn<(url: string, init?: RequestInit) => Promise<Response>>
>;
const props = (
  overrides: Partial<SimulationEventsWorkspaceProps> = {},
): SimulationEventsWorkspaceProps => ({
  profile,
  domainId: "domain-a",
  permissions: { read: true, manage: true },
  sessionChanged: vi.fn(),
  ...overrides,
});
async function selectDataset() {
  await screen.findByRole("option", { name: "fixture-a (dataset-a)" });
  fireEvent.change(screen.getByLabelText("模拟数据集"), {
    target: { value: "dataset-a" },
  });
}
async function openDetail() {
  await selectDataset();
  fireEvent.change(screen.getByLabelText("模拟案例 ID"), {
    target: { value: "case-a" },
  });
  fireEvent.click(screen.getByRole("button", { name: "读取案例详情" }));
  await screen.findByRole("article", { name: "模拟案例详情" });
}
const urlPath = (url: string) => new URL(url, "http://localhost").pathname;
beforeEach(() => {
  fetchMock = vi.fn(async (url: string, _init?: RequestInit) => {
    const path = urlPath(url);
    if (path.endsWith("/datasets"))
      return reply({ simulation: true, datasets: [dataset], total: 1 });
    if (path.endsWith("/detail")) return reply(detail);
    if (path.endsWith("/query"))
      return reply({
        simulation: true,
        dataset,
        cases: [caseView],
        page: { Events: [event], Total: 1, Provenance: provenance },
      });
    return reply({ error: "not_found" }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
describe("simulation events HTTP workspace", () => {
  it("defaults deny, never fetches or grants admin bypass", () => {
    render(
      <SimulationEventsWorkspace
        {...props({
          permissions: undefined,
          profile: { ...profile, role: "admin" },
        })}
      />,
    );
    expect(
      screen.getByText("尚未获得当前域的模拟事件读取权限。"),
    ).toBeVisible();
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it("uses HTTP query, explicit provenance, empty state and refresh without fixtures", async () => {
    render(<SimulationEventsWorkspace {...props()} />);
    await selectDataset();
    fireEvent.change(screen.getByLabelText("操作者筛选"), {
      target: { value: "synthetic-user" },
    });
    fireEvent.click(screen.getByText("查询模拟事件"));
    await screen.findByRole("cell", { name: "event-a" });
    const call = fetchMock.mock.calls.find(([url]) =>
      urlPath(url).endsWith("/query"),
    )!;
    const init = call[1] as RequestInit;
    expect(init.credentials).toBe("same-origin");
    expect(init.headers).toEqual({
      "Content-Type": "application/json",
      "X-CSRF-Token": "csrf",
    });
    expect(JSON.parse(init!.body as string)).toEqual({
      domainId: "domain-a",
      datasetId: "dataset-a",
      offset: 0,
      limit: 100,
      actor: "synthetic-user",
    });
    fetchMock.mockResolvedValueOnce(
      reply({ simulation: true, datasets: [], total: 0 }),
    );
    fireEvent.click(screen.getByText("刷新数据集"));
    await screen.findByText("没有可显示的模拟数据集；不会自动生成样例。");
    expect(
      screen.queryByRole("cell", { name: "event-a" }),
    ).not.toBeInTheDocument();
  });
  it("read-only users can read details but cannot change state", async () => {
    render(
      <SimulationEventsWorkspace
        {...props({ permissions: { read: true, manage: false } })}
      />,
    );
    await openDetail();
    expect(screen.getByRole("button", { name: "标记可疑" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "关闭案例" })).toBeDisabled();
  });
  it("locks repeated writes, sends expected revision and recovers lost POST response by GET", async () => {
    const lost = deferred<Response>();
    let operationId = "";
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (urlPath(url).endsWith("/transition")) {
        const body = JSON.parse(init!.body as string);
        expect(body.expectedRevision).toBe(1);
        operationId = body.operationId;
        return lost.promise;
      }
      if (urlPath(url).endsWith("/operation")) {
        expect(
          [...new URL(url, "http://localhost").searchParams.keys()].sort(),
        ).toEqual(["datasetId", "domainId", "operationId"]);
        expect(
          new URL(url, "http://localhost").searchParams.get("operationId"),
        ).toBe(operationId);
        return Promise.resolve(reply(receipt(operationId)));
      }
      return original(url, init);
    });
    render(<SimulationEventsWorkspace {...props()} />);
    await openDetail();
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "synthetic reason" },
    });
    const button = screen.getByText("标记可疑");
    fireEvent.click(button);
    fireEvent.click(button);
    await act(async () => lost.resolve(reply({ error: "internal" }, 503)));
    await screen.findByText(/模拟操作已由服务端确认/);
    expect(
      fetchMock.mock.calls.filter(([url]) =>
        urlPath(url).endsWith("/transition"),
      ),
    ).toHaveLength(1);
    expect(screen.getByText(/版本：2/)).toBeVisible();
  });
  it("only retries exact operation after authoritative not_found lookup", async () => {
    const bodies: string[] = [];
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (urlPath(url).endsWith("/transition")) {
        bodies.push(init!.body as string);
        return Promise.resolve(
          bodies.length === 1
            ? reply({ error: "internal" }, 503)
            : reply(receipt(JSON.parse(bodies[0]).operationId)),
        );
      }
      return original(url, init);
    });
    render(<SimulationEventsWorkspace {...props()} />);
    await openDetail();
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "synthetic reason" },
    });
    fireEvent.click(screen.getByText("标记可疑"));
    await screen.findByText(/操作结果未确认/);
    expect(bodies).toHaveLength(1);
    fireEvent.click(screen.getByText("核验原操作并安全重试"));
    await screen.findByText(/模拟操作已由服务端确认/);
    expect(bodies).toHaveLength(2);
    expect(bodies[0]).toBe(bodies[1]);
    expect(
      fetchMock.mock.calls.filter(([url]) =>
        urlPath(url).endsWith("/operation"),
      ),
    ).toHaveLength(2);
  });
  it("recovers the same operation when delayed original POST commits after lookup404 before scope detail", async () => {
    const bodies: string[] = [];
    const original = fetchMock.getMockImplementation()!;
    let receiptMissing = false;
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (urlPath(url).endsWith("/transition")) {
        bodies.push(init!.body as string);
        return bodies.length === 1
          ? reply({ error: "internal" }, 503)
          : reply(receipt(JSON.parse(bodies[0]).operationId));
      }
      if (urlPath(url).endsWith("/operation")) {
        receiptMissing = true;
        return reply({ error: "not_found" }, 404);
      }
      if (urlPath(url).endsWith("/detail") && receiptMissing)
        return reply({
          ...detail,
          case: { ...caseView, Mark: "suspicious", Revision: 2 },
          history: [audit],
          historyTotal: 1,
        });
      return original(url, init);
    });
    render(<SimulationEventsWorkspace {...props()} />);
    await openDetail();
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "synthetic reason" },
    });
    fireEvent.click(screen.getByText("标记可疑"));
    await screen.findByText(/操作结果未确认/);
    fireEvent.click(screen.getByText("核验原操作并安全重试"));
    await screen.findByText(/模拟操作已由服务端确认/);
    expect(bodies).toHaveLength(2);
    expect(bodies[1]).toBe(bodies[0]);
    expect(JSON.parse(bodies[1]).expectedRevision).toBe(1);
    expect(screen.queryByText(/待核验操作/)).not.toBeInTheDocument();
  });
  it("cancellation and browser back discard late details even when transport ignores abort", async () => {
    const late = deferred<Response>();
    render(<SimulationEventsWorkspace {...props()} />);
    await selectDataset();
    fetchMock.mockReturnValueOnce(late.promise);
    fireEvent.change(screen.getByLabelText("模拟案例 ID"), {
      target: { value: "case-a" },
    });
    fireEvent.click(screen.getByText("读取案例详情"));
    fireEvent.click(screen.getByText("取消等待"));
    await act(async () => late.resolve(reply(detail)));
    expect(screen.queryByRole("article")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("模拟案例 ID"), {
      target: { value: "case-a" },
    });
    fireEvent.click(screen.getByText("读取案例详情"));
    await screen.findByRole("article");
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    expect(screen.queryByRole("article")).not.toBeInTheDocument();
  });
  it.each(["domain", "session", "permission"])(
    "clears old data and ignores late reads on %s change",
    async (change) => {
      const late = deferred<Response>();
      const p = props();
      const view = render(<SimulationEventsWorkspace {...p} />);
      await selectDataset();
      fetchMock.mockReturnValueOnce(late.promise);
      fireEvent.click(screen.getByText("查询模拟事件"));
      const next =
        change === "domain"
          ? { ...p, domainId: "domain-b" }
          : change === "session"
            ? { ...p, profile: { ...profile, csrfToken: "new-session" } }
            : { ...p, permissions: { read: false, manage: false } };
      view.rerender(<SimulationEventsWorkspace {...next} />);
      await act(async () =>
        late.resolve(
          reply({
            simulation: true,
            dataset,
            cases: [caseView],
            page: { Events: [event], Total: 1, Provenance: provenance },
          }),
        ),
      );
      expect(
        screen.queryByRole("cell", { name: "event-a" }),
      ).not.toBeInTheDocument();
    },
  );
  it("clears private result on forbidden and does not retry a rejected write", async () => {
    render(<SimulationEventsWorkspace {...props()} />);
    await openDetail();
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "reason" },
    });
    fetchMock.mockResolvedValueOnce(reply({ error: "forbidden" }, 403));
    fireEvent.click(screen.getByText("关闭案例"));
    await screen.findByText(
      "当前会话或权限已失效，已清除结果；请重新验证权限。",
    );
    expect(screen.queryByRole("article")).not.toBeInTheDocument();
    expect(
      fetchMock.mock.calls.filter(([url]) =>
        urlPath(url).endsWith("/operation"),
      ),
    ).toHaveLength(0);
  });
  it("does not retry POST when operation lookup is unavailable", async () => {
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url: string, init?: RequestInit) =>
      /\/(transition|operation)$/.test(urlPath(url))
        ? Promise.resolve(reply({ error: "internal" }, 503))
        : original(url, init),
    );
    render(<SimulationEventsWorkspace {...props()} />);
    await openDetail();
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "reason" },
    });
    fireEvent.click(screen.getByText("标记可疑"));
    await screen.findByText(/操作结果未确认/);
    fireEvent.click(screen.getByText("返回列表"));
    expect(screen.getByText("刷新数据集")).toBeDisabled();
    fireEvent.click(screen.getByText("核验原操作并安全重试"));
    await waitFor(() =>
      expect(screen.getByText("核验原操作并安全重试")).not.toBeDisabled(),
    );
    expect(
      fetchMock.mock.calls.filter(([url]) =>
        urlPath(url).endsWith("/transition"),
      ),
    ).toHaveLength(1);
  });
  it("refreshes authoritative revision and disables terminal closed state", async () => {
    render(<SimulationEventsWorkspace {...props()} />);
    await openDetail();
    fetchMock.mockResolvedValueOnce(
      reply({ ...detail, case: { ...caseView, State: "closed", Revision: 3 } }),
    );
    fireEvent.click(screen.getByText("刷新详情"));
    await screen.findByText(/版本：3/);
    expect(screen.getByText("关闭案例")).toBeDisabled();
    expect(screen.getByText("标记可疑")).toBeDisabled();
  });
  it("revision conflict requires fresh detail and never probes/replays a rejected operation", async () => {
    render(<SimulationEventsWorkspace {...props()} />);
    await openDetail();
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "reason" },
    });
    fetchMock.mockResolvedValueOnce(reply({ error: "revision_conflict" }, 409));
    fireEvent.click(screen.getByText("标记可疑"));
    await screen.findByText("事件版本已变化。请刷新详情后重新决定操作。");
    expect(screen.getByText("标记可疑")).toBeDisabled();
    expect(
      fetchMock.mock.calls.filter(([url]) =>
        urlPath(url).endsWith("/operation"),
      ),
    ).toHaveLength(0);
  });
  it.each([null, "2"])(
    "clears state and reverifies session when response identity is %s",
    async (identity) => {
      const p = props();
      render(<SimulationEventsWorkspace {...p} />);
      await openDetail();
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
      };
      if (identity !== null) headers["X-ADTR-User-ID"] = identity;
      fetchMock.mockResolvedValueOnce(
        new Response(JSON.stringify(detail), { status: 200, headers }),
      );
      fireEvent.click(screen.getByText("刷新详情"));
      await screen.findByText(
        "当前会话或权限已失效，已清除结果；请重新验证权限。",
      );
      expect(screen.queryByRole("article")).not.toBeInTheDocument();
      expect(p.sessionChanged).toHaveBeenCalledTimes(1);
    },
  );
  it.each(["transition", "recovery"])(
    "clears scoped data after domain revoke at %s and does not replay",
    async (flow) => {
      const original = fetchMock.getMockImplementation()!;
      render(<SimulationEventsWorkspace {...props()} />);
      await openDetail();
      fireEvent.change(screen.getByLabelText("操作原因"), {
        target: { value: "reason" },
      });
      fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
        if (urlPath(url).endsWith("/transition"))
          return reply(
            { error: flow === "transition" ? "not_found" : "internal" },
            flow === "transition" ? 404 : 503,
          );
        if (
          urlPath(url).endsWith("/operation") ||
          urlPath(url).endsWith("/detail")
        )
          return reply({ error: "not_found" }, 404);
        return original(url, init);
      });
      fireEvent.click(screen.getByText("标记可疑"));
      if (flow === "recovery") {
        await screen.findByText(/操作结果未确认/);
        fireEvent.click(screen.getByText("核验原操作并安全重试"));
      }
      await screen.findByText("当前授权范围内未找到该模拟记录。");
      expect(screen.queryByRole("article")).not.toBeInTheDocument();
      expect(
        screen.queryByRole("option", { name: "fixture-a (dataset-a)" }),
      ).not.toBeInTheDocument();
      expect(
        fetchMock.mock.calls.filter(([url]) =>
          urlPath(url).endsWith("/transition"),
        ),
      ).toHaveLength(1);
    },
  );
  it("reports disabled service without fabricating records", async () => {
    fetchMock.mockResolvedValueOnce(
      reply({ error: "simulation_disabled" }, 404),
    );
    render(<SimulationEventsWorkspace {...props()} />);
    await screen.findByText("模拟事件功能未启用。");
    expect(screen.queryByText("fixture-a (dataset-a)")).not.toBeInTheDocument();
  });
});
describe("simulation response validation", () => {
  it("rejects missing simulation marker, non-synthetic provenance and unsafe revision", () => {
    expect(() =>
      parseSimulationResponse("detail", { ...detail, simulation: false }),
    ).toThrow();
    expect(() =>
      parseSimulationResponse("detail", {
        ...detail,
        case: { ...caseView, Revision: Number.MAX_SAFE_INTEGER + 1 },
      }),
    ).toThrow();
    expect(() =>
      parseSimulationResponse("query", {
        simulation: true,
        dataset,
        cases: [caseView],
        page: {
          Events: [
            { ...event, Provenance: { ...provenance, Synthetic: false } },
          ],
          Total: 1,
          Provenance: provenance,
        },
      }),
    ).toThrow();
  });
});

describe("simulation entry authorization", () => {
  it("denies missing server menu grants even when checks say yes", async () => {
    fetchMock.mockImplementation(async (url: string) =>
      url.endsWith("/menu")
        ? reply({ menu: [] })
        : reply({ results: Array(7).fill(true) }),
    );
    render(
      <SimulationEntry
        profile={{ ...profile, role: "admin" }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByText("当前账户未获得模拟事件与域选择读取权限。");
    expect(
      fetchMock.mock.calls.some(([url]) => url.includes("/simulation/events")),
    ).toBe(false);
  });
  it("rejects malformed permission-check response", async () => {
    fetchMock.mockImplementation(async (url: string) =>
      url.endsWith("/menu")
        ? reply({
            menu: [
              { mark: "domains", auth: { readable: true } },
              { mark: "simulation_events", auth: { readable: true } },
            ],
          })
        : reply({ results: [true] }),
    );
    render(<SimulationEntry profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("未能确认模拟事件及域选择权限，请重新核对。");
    expect(
      fetchMock.mock.calls.some(([url]) => url.includes("/simulation/events")),
    ).toBe(false);
  });
});
