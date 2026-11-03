import { readFileSync } from "node:fs";
// Synthetic HTTP/DOM contracts, not live PostgreSQL or browser acceptance.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, type Profile } from "./api";
import { SimulationAssetAssessmentDomainWorkspace as Workspace } from "./SimulationAssetAssessmentWorkspace";
import {
  simulationAssetAssessmentAPI as api,
  assessmentContract,
  verifyReceipt,
  validReport,
  canonical,
  type Observation,
  type Report,
  type CreateInput,
  type Receipt,
  type AssetRow,
  type Summary,
} from "./simulation-asset-assessment-api";
const profile = { ID: 7, csrfToken: "csrf" } as Profile;
const at = "2026-11-01T12:00:00Z",
  hash = "a".repeat(64),
  domainId = "example.test";
// These object names / findings match core.go's trusted baseline fixture subset.
const row: AssetRow = {
  version: "simulated-asset-v1",
  source: "synthetic",
  tenantId: "tenant",
  domainId,
  objectId: "user-enabled-recent",
  observationId: "synthetic-assets-v1",
  kind: "user",
  observedAt: at,
  asOf: at,
  accountStatus: { value: "enabled", reason: "explicit_flags_clear" },
  activity: { value: "recent", reason: "last_login_within_window" },
  groupType: { value: "not_applicable", reason: "asset_kind" },
  groupScope: { value: "not_applicable", reason: "asset_kind" },
};
const summary: Summary = {
  total: 1,
  complete: false,
  kinds: { user: 1 },
  accountStatus: { enabled: 1 },
  activity: { recent: 1 },
  groupType: { not_applicable: 1 },
  groupScope: { not_applicable: 1 },
  reasons: {
    explicit_flags_clear: 1,
    last_login_within_window: 1,
    asset_kind: 2,
  },
};
const report: Report = {
  contract: assessmentContract,
  simulation: true,
  id: "report-1",
  tenantId: "tenant",
  domainId,
  actorId: 7,
  epoch: 1,
  observationId: "synthetic-assets-v1",
  sourceHash: hash,
  scenarioId: "baseline",
  version: "simulated-asset-v1",
  asOf: at,
  createdAt: at,
  rows: [row],
  summary,
  hash: "b".repeat(64),
};
const observation: Observation = {
  contract: assessmentContract,
  simulation: true,
  id: "synthetic-assets-v1",
  tenantId: "tenant",
  domainId,
  complete: false,
  completenessReason: "fixed_synthetic_subset_not_directory_inventory",
  objects: [],
  scenarios: [
    { id: "baseline", asOf: at },
    { id: "stale", asOf: "2026-11-02T12:00:00.000000001Z" },
  ],
  hash,
};
const input: CreateInput = {
  id: report.id,
  operationId: "op-1",
  observationId: observation.id,
  expectedSourceHash: hash,
  scenarioId: "baseline",
};
function receipt(i: CreateInput = input): Receipt {
  return {
    contract: assessmentContract,
    simulation: true,
    operationId: i.operationId,
    requestHash: "c".repeat(64),
    input: i,
    report: { ...report, id: i.id, scenarioId: i.scenarioId },
  };
}
beforeEach(() => {
  vi.spyOn(api, "observation").mockResolvedValue(observation);
  vi.spyOn(api, "history").mockResolvedValue({
    offset: 0,
    limit: 20,
    total: 0,
    reports: [],
  });
  vi.spyOn(api, "rows").mockImplementation(async (_d, r, f) => ({
    reportId: r.id,
    reportHash: r.hash,
    summary: r.summary,
    matched: f.kind === "group" ? 0 : 1,
    offset: f.offset,
    limit: f.limit,
    rows: f.kind === "group" ? [] : r.rows,
  }));
  vi.spyOn(api, "create").mockImplementation(async (_d, i) => receipt(i));
  vi.spyOn(api, "receipt").mockResolvedValue(receipt());
  vi.spyOn(api, "detail").mockResolvedValue(report);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function mount(manage = true) {
  render(
    <Workspace
      profile={profile}
      domainId={domainId}
      manage={manage}
      sessionChanged={vi.fn()}
    />,
  );
  await screen.findByLabelText("固定评估场景");
}
it("creates only server IDs/hash/scenario and separates full summary from filtered page", async () => {
  await mount();
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  await screen.findByRole("region", { name: "完整报告统计" });
  await screen.findByText("user-enabled-recent");
  const sent = vi.mocked(api.create).mock.calls[0][1];
  expect(Object.keys(sent).sort()).toEqual(
    [
      "id",
      "operationId",
      "observationId",
      "expectedSourceHash",
      "scenarioId",
    ].sort(),
  );
  expect(sent.expectedSourceHash).toBe(hash);
  fireEvent.change(screen.getByLabelText("对象类别"), {
    target: { value: "group" },
  });
  await waitFor(() =>
    expect(screen.queryByText("user-enabled-recent")).not.toBeInTheDocument(),
  );
  expect(
    within(screen.getByRole("region", { name: "完整报告统计" })).getByText(
      /已观察对象总数：1/,
    ),
  ).toBeInTheDocument();
  expect(screen.getByText(/本页 0 条/)).toBeInTheDocument();
});
it("read-only cannot create", async () => {
  await mount(false);
  expect(screen.getByText("创建不可变模拟报告")).toBeDisabled();
  expect(api.create).not.toHaveBeenCalled();
});
it("reselection clears historical detail and filter state", async () => {
  await mount();
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  await screen.findByRole("region", { name: "报告详情" });
  fireEvent.change(screen.getByLabelText("固定评估场景"), {
    target: { value: "stale" },
  });
  expect(
    screen.queryByRole("region", { name: "报告详情" }),
  ).not.toBeInTheDocument();
});
it("ambiguous POST retains intent; retry checks receipt before same input; definitive reject unlocks", async () => {
  vi.mocked(api.create)
    .mockRejectedValueOnce(new ApiError("network"))
    .mockRejectedValueOnce(new ApiError("report_conflict", 409));
  vi.mocked(api.receipt).mockRejectedValue(new ApiError("not_found", 404));
  await mount();
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  await screen.findByText(/提交结果不确定/);
  expect(screen.getByLabelText("固定评估场景")).toBeDisabled();
  const first = vi.mocked(api.create).mock.calls[0][1];
  fireEvent.click(screen.getByText("先查回执，再重试同一操作"));
  await screen.findByText(/服务器明确拒绝/);
  expect(api.receipt).toHaveBeenCalledWith(
    domainId,
    first.operationId,
    profile,
    expect.any(AbortSignal),
    first,
  );
  expect(vi.mocked(api.create).mock.calls[1][1]).toEqual(first);
  expect(screen.getByLabelText("固定评估场景")).not.toBeDisabled();
});
it("successful receipt recovery does not duplicate POST", async () => {
  vi.mocked(api.create).mockRejectedValue(new ApiError("network"));
  vi.mocked(api.receipt).mockImplementation(async (_d, _o, _p, _s, i) =>
    receipt(i),
  );
  await mount();
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  await screen.findByText(/提交结果不确定/);
  fireEvent.click(screen.getByText("查询原操作回执"));
  await screen.findByRole("region", { name: "报告详情" });
  expect(api.create).toHaveBeenCalledTimes(1);
});
it("fresh mount can recover an existing operation without reconstructing input", async () => {
  await mount(false);
  fireEvent.change(screen.getByLabelText("operationId"), {
    target: { value: "op-1" },
  });
  fireEvent.click(screen.getByText("查询原操作回执"));
  await screen.findByRole("region", { name: "报告详情" });
  expect(api.create).not.toHaveBeenCalled();
});
it("late identity response is discarded after remount", async () => {
  let done!: (r: Receipt) => void;
  vi.mocked(api.create).mockImplementation(
    () =>
      new Promise((resolve) => {
        done = resolve;
      }),
  );
  const changed = vi.fn();
  const view = render(
    <Workspace
      profile={profile}
      domainId={domainId}
      manage
      sessionChanged={changed}
    />,
  );
  await screen.findByLabelText("固定评估场景");
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  view.rerender(
    <Workspace
      profile={{ ...profile, ID: 8, csrfToken: "new" }}
      domainId={domainId}
      manage
      sessionChanged={changed}
    />,
  );
  await act(async () => done(receipt()));
  expect(
    screen.queryByRole("region", { name: "报告详情" }),
  ).not.toBeInTheDocument();
});
it("forbidden clears data and disables dependent work", async () => {
  vi.mocked(api.create).mockRejectedValue(new ApiError("forbidden", 403));
  await mount();
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  await screen.findByText("请退出此域并重新核对当前权限。");
  expect(screen.queryByLabelText("固定评估场景")).not.toBeInTheDocument();
});
it.each([
  "id",
  "operationId",
  "observationId",
  "expectedSourceHash",
  "scenarioId",
] as const)("receipt binds %s", (key) => {
  const r = receipt();
  r.input = { ...r.input, [key]: "different" };
  expect(verifyReceipt(r, input, domainId, 7, "op-1")).toBe(false);
});
it("receipt rejects wrong actor/domain/epoch, report count mismatch and non-synthetic source", () => {
  expect(verifyReceipt(receipt(), input, domainId, 7, "op-1")).toBe(true);
  expect(verifyReceipt(receipt(), input, domainId, 8, "op-1")).toBe(false);
  expect(verifyReceipt(receipt(), input, "other", 7, "op-1")).toBe(false);
  expect(validReport({ ...report, epoch: 0 })).toBe(false);
  expect(validReport({ ...report, rows: [] })).toBe(false);
  expect(validReport({ ...report, rows: [{ ...row, source: "real" }] })).toBe(
    false,
  );
  expect(canonical({ b: 1, a: 2 })).toBe(canonical({ a: 2, b: 1 }));
});
it("HTTP uses exact endpoint, CSRF and identity and rejects unknown receipt intent", async () => {
  vi.restoreAllMocks();
  const fetcher = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(receipt()), {
      status: 200,
      headers: { "X-ADTR-User-ID": "7" },
    }),
  );
  vi.stubGlobal("fetch", fetcher);
  await api.create(domainId, input, profile, new AbortController().signal);
  expect(fetcher).toHaveBeenCalledWith(
    "/api/simulation/asset-assessment/create",
    expect.objectContaining({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": "csrf" },
      body: JSON.stringify({ domainId, ...input }),
    }),
  );
  fetcher.mockResolvedValue(
    new Response(JSON.stringify(receipt()), {
      status: 200,
      headers: { "X-ADTR-User-ID": "8" },
    }),
  );
  await expect(
    api.create(domainId, input, profile, new AbortController().signal),
  ).rejects.toMatchObject({ status: 401 });
});
it("successful create remains known if subsequent page read fails", async () => {
  vi.mocked(api.rows).mockRejectedValue(new ApiError("internal", 503));
  await mount();
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  await screen.findByRole("region", { name: "完整报告统计" });
  await screen.findByText(/请求未完成：internal/);
  expect(api.create).toHaveBeenCalledTimes(1);
  expect(screen.getByText("创建不可变模拟报告")).not.toBeDisabled();
  expect(screen.getByLabelText("operationId")).toHaveValue(
    vi.mocked(api.create).mock.calls[0][1].operationId,
  );
});
it("history uses bounded offset pages rather than accumulating previous results", async () => {
  vi.mocked(api.history).mockImplementation(async (_d, offset) => ({
    offset,
    limit: 20,
    total: 21,
    reports: [{ ...report, id: offset === 0 ? "first-report" : "last-report" }],
  }));
  await mount();
  await screen.findByRole("button", { name: /first-report/ });
  fireEvent.click(screen.getByText("历史下一页"));
  await screen.findByRole("button", { name: /last-report/ });
  expect(
    screen.queryByRole("button", { name: /first-report/ }),
  ).not.toBeInTheDocument();
  expect(api.history).toHaveBeenLastCalledWith(
    domainId,
    20,
    profile,
    expect.any(AbortSignal),
  );
  fireEvent.click(screen.getByRole("button", { name: /last-report/ }));
  await screen.findByRole("region", { name: "报告详情" });
  expect(api.detail).toHaveBeenCalledWith(
    domainId,
    "last-report",
    profile,
    expect.any(AbortSignal),
  );
});
it("transport rejects mismatched page rows, summary, scope, and malformed report counts", async () => {
  vi.restoreAllMocks();
  const respond = (v: unknown) =>
    new Response(JSON.stringify(v), { headers: { "X-ADTR-User-ID": "7" } });
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const s = new AbortController().signal;
  const filters = {
    kind: "group",
    accountStatus: "",
    activity: "",
    groupType: "",
    groupScope: "",
    offset: 0,
    limit: 20,
  };
  fetcher.mockResolvedValue(
    respond({
      reportId: report.id,
      reportHash: report.hash,
      summary,
      matched: 0,
      offset: 0,
      limit: 20,
      rows: [row],
    }),
  );
  await expect(
    api.rows(domainId, report, filters, profile, s),
  ).rejects.toMatchObject({ code: "invalid_response" });
  fetcher.mockResolvedValue(
    respond({
      reportId: report.id,
      reportHash: report.hash,
      summary,
      matched: 0,
      offset: 0,
      limit: 20,
      rows: [],
    }),
  );
  await expect(
    api.rows(domainId, report, filters, profile, s),
  ).resolves.toMatchObject({ matched: 0 });
  const url = String(fetcher.mock.calls.at(-1)![0]);
  expect(
    new URL(url, "http://localhost").searchParams.get("accountStatus"),
  ).toBe("");
  expect(new URL(url, "http://localhost").searchParams.get("limit")).toBe("20");
  fetcher.mockResolvedValue(respond({ ...report, domainId: "wrong" }));
  await expect(
    api.detail(domainId, report.id, profile, s),
  ).rejects.toMatchObject({ code: "invalid_response" });
  expect(
    validReport({
      ...report,
      summary: { ...summary, accountStatus: { disabled: 1 } },
    }),
  ).toBe(false);
  expect(
    validReport({
      ...report,
      summary: { ...summary, reasons: { missing_evidence: 4 } },
    }),
  ).toBe(false);
});
it("network receipt failures do not trigger a blind second POST", async () => {
  vi.mocked(api.create).mockRejectedValue(new ApiError("network"));
  vi.mocked(api.receipt).mockRejectedValue(new ApiError("internal", 503));
  await mount();
  fireEvent.click(screen.getByText("创建不可变模拟报告"));
  await screen.findByText(/提交结果不确定/);
  fireEvent.click(screen.getByText("先查回执，再重试同一操作"));
  await screen.findByText(/请求未完成：internal/);
  expect(api.create).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("固定评估场景")).toBeDisabled();
});

// Produced by the backend's real trusted fixture + Evaluate. This checks cross-
// language transport compatibility, not an independent oracle for classification.
it("accepts the actual full eleven-object backend baseline report", async () => {
  const actual: unknown = JSON.parse(
    readFileSync("src/test-fixtures/simulated-asset-baseline.json", "utf8"),
  );
  expect(validReport(actual)).toBe(true);
  if (!validReport(actual))
    throw new Error("backend report does not satisfy the client contract");
  expect(actual.rows).toHaveLength(11);
  expect(actual.summary.total).toBe(11);
  expect(actual.summary.complete).toBe(false);
  vi.restoreAllMocks();
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(actual), {
        headers: { "X-ADTR-User-ID": "7" },
      }),
    ),
  );
  await expect(
    api.detail(
      actual.domainId,
      actual.id,
      profile,
      new AbortController().signal,
    ),
  ).resolves.toEqual(actual);
});
