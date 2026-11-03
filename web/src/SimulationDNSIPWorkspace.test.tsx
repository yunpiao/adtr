// Client/DOM synthetic contracts; real PostgreSQL/Worker/browser acceptance is separate.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import {
  afterEach,
  beforeEach,
  expect,
  it,
  vi,
  type MockInstance,
} from "vitest";
import { createHash, webcrypto } from "node:crypto";
import { ApiError, type Profile } from "./api";
import { type Task } from "./task-api";
import { SimulationDNSIPDomainWorkspace as Workspace } from "./SimulationDNSIPWorkspace";
import {
  simulationDNSIPAPI as api,
  dnsIPContract,
  dnsIPKind,
  dnsIPColumns,
  validObservation,
  validManifest,
  validFilter,
  verifyPage,
  verifyTask,
  verifyReceipt,
  receiptIntent,
  type Observation,
  type DNSIPPage,
  type Query,
  type Receipt,
  type Write,
  type Payload,
  type Manifest,
} from "./simulation-dns-ip-api";
const profile = {
  ID: 9,
  username: "synthetic",
  csrfToken: "csrf-9",
} as Profile;
const h = "a".repeat(64);
const observation: Observation = {
  id: "observation-a",
  dnsDomain: "example.test",
  source: "synthetic",
  hash: h,
  recordCount: 25,
  complete: true,
  contract: dnsIPContract,
  simulation: true,
};
const query: Query = {
  observationId: observation.id,
  keyword: "",
  family: "",
  offset: 0,
  limit: 20,
};
const rows = Array.from({ length: 25 }, (_, i) => ({
  id: `row-${String(i + 1).padStart(3, "0")}`,
  domain: "example.test",
  dnsName: `host-${String(i + 1).padStart(3, "0")}.example.test`,
  ip: `192.0.2.${i + 1}`,
  observation: observation.id,
  source: observation.source,
  version: "dnsip-sim-v1",
  simulation: true,
}));
const page = (q: Query): DNSIPPage => ({
  observation,
  query: q,
  rows: rows.slice(q.offset, q.offset + q.limit),
  total: 25,
  exhausted: q.offset + q.limit >= 25,
});
const task: Task = {
  taskUUID: "task-a",
  taskName: dnsIPKind,
  domainId: "domain-a",
  payloadVersion: 1,
  state: "queued",
  sourceState: "",
  error: "",
  createdAt: "",
  updatedAt: "",
  attempt: 0,
  maxAttempts: 1,
  progress: 0,
  resultVersion: 0,
  result: null,
  cursor: null,
  parentTaskUUID: "",
  terminalAt: null,
  archived: false,
  visibilityVersion: 1,
};
const payload: Payload = {
  observationId: observation.id,
  observationHash: h,
  dnsDomain: observation.dnsDomain,
  keyword: "",
  family: "",
  contract: dnsIPContract,
};
const input: Write = {
  domainId: "domain-a",
  operationId: "op-a",
  observationId: observation.id,
  observationHash: h,
  keyword: "",
  family: "",
};
const receipt = (w: Write = input): Receipt => ({
  contract: dnsIPContract,
  simulation: true,
  operationId: w.operationId,
  action: "export",
  actorId: profile.ID,
  intent: receiptIntent("export", w),
  requestHash: "b".repeat(64),
  submission: { task, replayed: false },
});
const bytes = new Uint8Array([80, 75, 3, 4, 8, 10]);
const manifest: Manifest = {
  contract: dnsIPContract,
  simulation: true,
  taskId: task.taskUUID,
  payload,
  rowCount: 25,
  columns: dnsIPColumns,
  bytes: bytes.length,
  sha256: createHash("sha256").update(bytes).digest("hex"),
  filename: "SIMULATED-dns-ip-task-a.xlsx",
};
const succeeded: Task = {
  ...task,
  state: "succeeded",
  progress: 100,
  attempt: 1,
  resultVersion: 1,
  result: manifest,
  terminalAt: "2026-11-02T00:00:00Z",
};
let requests: MockInstance<typeof api.write>;
const sessionChanged = vi.fn();
beforeEach(() => {
  vi.stubGlobal("crypto", webcrypto);
  sessionChanged.mockClear();
  vi.spyOn(api, "observations").mockResolvedValue([observation]);
  vi.spyOn(api, "records").mockImplementation(async (_d, q) => page(q));
  requests = vi
    .spyOn(api, "write")
    .mockImplementation(async (_a, w) => receipt(w));
  vi.spyOn(api, "task").mockResolvedValue(task);
  vi.spyOn(api, "receipt").mockResolvedValue(receipt());
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    value: vi.fn(() => "blob:synthetic"),
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    configurable: true,
    value: vi.fn(),
  });
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function mount(p = profile) {
  return render(
    <Workspace
      profile={p}
      sessionChanged={sessionChanged}
      domainId="domain-a"
      permissions={{ read: true, manage: true }}
    />,
  );
}
async function select() {
  await screen.findByRole("option", { name: /observation-a/ });
  fireEvent.change(screen.getByLabelText("固定观测"), {
    target: { value: observation.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "应用筛选" }));
  await screen.findByTestId("dns-ip-page-summary");
}
function deferred<T>() {
  let resolve!: (x: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
function json(v: unknown, status = 200, actor = 9) {
  return new Response(JSON.stringify(v), {
    status,
    headers: {
      "X-ADTR-User-ID": String(actor),
      "Content-Type": "application/json",
    },
  });
}
it("uses trusted observations and exports all matches while paging only20 rows", async () => {
  mount();
  await select();
  expect(screen.getAllByRole("row")).toHaveLength(21);
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(6));
  fireEvent.click(
    screen.getByRole("button", { name: "导出全部 25 条匹配记录 XLSX" }),
  );
  await screen.findByRole("region", { name: "DNS/IP 导出任务" });
  const w = requests.mock.calls[0][1];
  expect(w).toMatchObject({
    domainId: "domain-a",
    observationId: observation.id,
    observationHash: h,
    keyword: "",
    family: "",
  });
  expect(w).not.toHaveProperty("offset");
  expect(w).not.toHaveProperty("limit");
  expect(w).not.toHaveProperty("dnsDomain");
});
it("does not export stale filters before explicit apply", async () => {
  mount();
  await select();
  fireEvent.change(screen.getByLabelText("DNS/IP 字面关键词"), {
    target: { value: "new" },
  });
  expect(
    screen.getByRole("button", { name: "导出全部 25 条匹配记录 XLSX" }),
  ).toBeDisabled();
  expect(screen.getByText(/筛选已修改/)).toBeVisible();
});
it("shows empty trusted observations without fabricated records", async () => {
  vi.mocked(api.observations).mockResolvedValue([]);
  mount();
  expect(await screen.findByText(/没有可信合成观测/)).toBeVisible();
  expect(screen.queryByRole("table")).toBeNull();
});
it("handles empty result with explicit header-only workbook", async () => {
  vi.mocked(api.records).mockImplementation(async (_d, q) => ({
    observation,
    query: q,
    rows: [],
    total: 0,
    exhausted: true,
  }));
  mount();
  await select();
  expect(screen.getByText(/八列表头工作簿/)).toBeVisible();
  expect(
    screen.getByRole("button", { name: "导出全部 0 条匹配记录 XLSX" }),
  ).toBeEnabled();
});
it("keeps one pending operation and recovers the same intent after response loss", async () => {
  requests.mockRejectedValueOnce(new ApiError("network"));
  vi.mocked(api.receipt).mockImplementation(async (_d, id) =>
    receipt({ ...input, operationId: id }),
  );
  mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByText(/操作结果待确认/);
  expect(screen.getByRole("button", { name: /导出全部 25/ })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "恢复原操作回执" }));
  await screen.findByRole("region", { name: "DNS/IP 导出任务" });
  expect(requests).toHaveBeenCalledTimes(1);
  expect(api.receipt).toHaveBeenCalledWith(
    "domain-a",
    requests.mock.calls[0][1].operationId,
    profile,
    expect.any(AbortSignal),
    expect.objectContaining({ action: "export" }),
  );
});
it("unlocks after receipt404 then definite retry409, retaining no false success", async () => {
  requests
    .mockRejectedValueOnce(new ApiError("network"))
    .mockRejectedValueOnce(new ApiError("revision_conflict", 409));
  vi.mocked(api.receipt).mockRejectedValue(
    new ApiError("operation_not_found", 404),
  );
  mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByText(/操作结果待确认/);
  fireEvent.click(screen.getByRole("button", { name: "恢复原操作回执" }));
  await screen.findByText(/版本或任务状态冲突/);
  expect(screen.queryByText(/操作结果待确认/)).toBeNull();
  expect(screen.getByRole("button", { name: /导出全部 25/ })).toBeEnabled();
  expect(requests.mock.calls[1][1]).toEqual(requests.mock.calls[0][1]);
});
it("keeps pending after retry503 and never invents a second operation", async () => {
  requests.mockRejectedValue(new ApiError("simulation_unavailable", 503));
  vi.mocked(api.receipt).mockRejectedValue(
    new ApiError("operation_not_found", 404),
  );
  mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByText(/操作结果待确认/);
  fireEvent.click(screen.getByRole("button", { name: "恢复原操作回执" }));
  await waitFor(() => expect(requests).toHaveBeenCalledTimes(2));
  expect(screen.getByText(/操作结果待确认/)).toBeVisible();
  expect(requests.mock.calls[1][1]).toEqual(requests.mock.calls[0][1]);
});
it("does not retry a source-not-found receipt ambiguity", async () => {
  requests.mockRejectedValue(new ApiError("network"));
  vi.mocked(api.receipt).mockRejectedValue(new ApiError("not_found", 404));
  mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByText(/操作结果待确认/);
  fireEvent.click(screen.getByRole("button", { name: "恢复原操作回执" }));
  await screen.findByText(/当前授权下未找到/);
  expect(requests).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/操作结果待确认/)).toBeVisible();
});
it("only enables download after current task succeeded with pinned manifest", async () => {
  vi.mocked(api.task).mockResolvedValue(succeeded);
  vi.spyOn(api, "download").mockResolvedValue({
    blob: new Blob([bytes]),
    filename: manifest.filename,
  });
  mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByRole("button", { name: "下载已验证 XLSX" });
  fireEvent.click(screen.getByRole("button", { name: "下载已验证 XLSX" }));
  await screen.findByText(/已核对实际字节数/);
  expect(api.download).toHaveBeenCalledWith(
    "domain-a",
    succeeded,
    profile,
    expect.any(AbortSignal),
    payload,
  );
  expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledOnce();
});
it("rejects task source mismatch rather than displaying success", async () => {
  vi.mocked(api.task).mockResolvedValue({
    ...succeeded,
    result: {
      ...manifest,
      payload: { ...payload, observationHash: "f".repeat(64) },
    },
  });
  mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByText(/服务器结果与当前观测/);
  expect(screen.queryByRole("button", { name: "下载已验证 XLSX" })).toBeNull();
});
it("aborts old identity and ignores its delayed source rows", async () => {
  const late = deferred<DNSIPPage>();
  vi.mocked(api.records).mockReturnValueOnce(late.promise);
  const view = mount();
  await screen.findByRole("option", { name: /observation-a/ });
  fireEvent.change(screen.getByLabelText("固定观测"), {
    target: { value: observation.id },
  });
  fireEvent.click(screen.getByRole("button", { name: "应用筛选" }));
  await waitFor(() => expect(api.records).toHaveBeenCalled());
  const signal = vi.mocked(api.records).mock.calls[0][3];
  view.rerender(
    <Workspace
      profile={{ ...profile, ID: 10, csrfToken: "new" }}
      sessionChanged={sessionChanged}
      domainId="domain-a"
      permissions={{ read: true, manage: true }}
    />,
  );
  await act(async () => late.resolve(page(query)));
  expect(signal.aborted).toBe(true);
  expect(screen.queryByRole("table")).toBeNull();
});
it("cannot publish a late download after identity changes", async () => {
  vi.mocked(api.task).mockResolvedValue(succeeded);
  const late = deferred<{ blob: Blob; filename: string }>();
  vi.spyOn(api, "download").mockReturnValue(late.promise);
  const view = mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByRole("button", { name: "下载已验证 XLSX" });
  fireEvent.click(screen.getByRole("button", { name: "下载已验证 XLSX" }));
  await waitFor(() => expect(api.download).toHaveBeenCalled());
  view.rerender(
    <Workspace
      profile={{ ...profile, ID: 10, csrfToken: "new" }}
      sessionChanged={sessionChanged}
      domainId="domain-a"
      permissions={{ read: true, manage: true }}
    />,
  );
  await act(async () =>
    late.resolve({ blob: new Blob([bytes]), filename: manifest.filename }),
  );
  expect(HTMLAnchorElement.prototype.click).not.toHaveBeenCalled();
});
it("read-only viewer cannot export", async () => {
  render(
    <Workspace
      profile={profile}
      sessionChanged={sessionChanged}
      domainId="domain-a"
      permissions={{ read: true, manage: false }}
    />,
  );
  await select();
  expect(screen.getByRole("button", { name: /导出全部 25/ })).toBeDisabled();
});
it("rejects missing synthetic provenance and invalid keyword/family", () => {
  expect(validObservation(observation)).toBe(true);
  expect(validObservation({ ...observation, simulation: false })).toBe(false);
  expect(validObservation({ ...observation, complete: false })).toBe(false);
  expect(validFilter("EXAMPLE", "ipv6")).toBe(true);
  expect(validFilter("非法", "")).toBe(false);
  expect(validFilter("x", "ipv7")).toBe(false);
});
it("validates page boundaries and source bindings", () => {
  expect(verifyPage(page(query), query, observation)).toEqual(page(query));
  for (const change of [
    { total: 10001 },
    { rows: [...rows.slice(0, 19), rows[0]] },
    { query: { ...query, offset: 20 } },
    { rows: rows.slice(0, 19) },
    { observation: { ...observation, hash: "b".repeat(64) } },
  ])
    expect(() =>
      verifyPage({ ...page(query), ...change }, query, observation),
    ).toThrow();
  expect(() =>
    verifyPage(
      {
        ...page(query),
        rows: page(query).rows.map((r) => ({ ...r, domain: "other.test" })),
      },
      query,
    ),
  ).toThrow();
});
it("validates ordered manifest columns/task/file identity and full payload", () => {
  expect(validManifest(manifest)).toBe(true);
  expect(
    validManifest({ ...manifest, columns: [...dnsIPColumns].reverse() }),
  ).toBe(false);
  expect(validManifest({ ...manifest, filename: "../bad.xlsx" })).toBe(false);
  expect(
    validManifest({ ...manifest, filename: "SIMULATED-dns-ip-other.xlsx" }),
  ).toBe(false);
  expect(() => verifyTask(succeeded, "other-domain")).toThrow();
  expect(() =>
    verifyTask(succeeded, "domain-a", undefined, {
      ...payload,
      keyword: "different",
    }),
  ).toThrow();
});
it("receipt binds actor, domain, operation and complete original export intent", () => {
  const r = receipt();
  expect(
    verifyReceipt(r, profile, "domain-a", "op-a", { action: "export", input }),
  ).toEqual(r);
  for (const changed of [
    { actorId: 10 },
    { operationId: "different" },
    { intent: { ...r.intent, domainId: "other" } },
    {
      intent: {
        ...r.intent,
        export: { ...r.intent.export!, keyword: "different" },
      },
    },
    {
      intent: {
        ...r.intent,
        export: { ...r.intent.export!, observationHash: "f".repeat(64) },
      },
    },
  ])
    expect(() =>
      verifyReceipt({ ...r, ...changed }, profile, "domain-a", "op-a", {
        action: "export",
        input,
      }),
    ).toThrow();
});
it("HTTP client preserves exact filter casing and checks identity on reads", async () => {
  vi.mocked(api.records).mockRestore();
  const q = { ...query, keyword: "HOST" };
  const fetch = vi.fn().mockResolvedValue(json(page(q)));
  vi.stubGlobal("fetch", fetch);
  expect(
    await api.records(
      "domain-a",
      q,
      profile,
      new AbortController().signal,
      observation,
    ),
  ).toEqual(page(q));
  expect(fetch.mock.calls[0][0]).toContain("keyword=HOST");
  fetch.mockResolvedValue(json(page(q), 200, 10));
  await expect(
    api.records("domain-a", q, profile, new AbortController().signal),
  ).rejects.toThrow();
});
it("HTTP export carries current CSRF and validates actual receipt", async () => {
  requests.mockRestore();
  const fetch = vi.fn().mockResolvedValue(json(receipt()));
  vi.stubGlobal("fetch", fetch);
  await api.write("export", input, profile, new AbortController().signal);
  expect(fetch.mock.calls[0][1]).toMatchObject({
    method: "POST",
    credentials: "same-origin",
    cache: "no-store",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": "csrf-9" },
  });
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(input);
});
it("downloads only matching actual bytes and headers", async () => {
  const headers = {
    "X-ADTR-User-ID": "9",
    "Content-Type":
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    "Content-Disposition": `attachment; filename="${manifest.filename}"`,
    "X-ADTR-SHA256": manifest.sha256,
    "X-ADTR-Row-Count": "25",
    "Content-Length": String(bytes.length),
  };
  const fetch = vi.fn().mockResolvedValue(new Response(bytes, { headers }));
  vi.stubGlobal("fetch", fetch);
  const f = await api.download(
    "domain-a",
    succeeded,
    profile,
    new AbortController().signal,
    payload,
  );
  expect(f.filename).toBe(manifest.filename);
  expect(f.blob.size).toBe(bytes.length);
  fetch.mockResolvedValue(
    new Response(new Uint8Array([80, 75, 3, 4, 8, 11]), { headers }),
  );
  await expect(
    api.download(
      "domain-a",
      succeeded,
      profile,
      new AbortController().signal,
      payload,
    ),
  ).rejects.toThrow();
  fetch.mockResolvedValue(
    new Response(bytes, { headers: { ...headers, "X-ADTR-Row-Count": "24" } }),
  );
  await expect(
    api.download(
      "domain-a",
      succeeded,
      profile,
      new AbortController().signal,
      payload,
    ),
  ).rejects.toThrow();
});
it("never downloads a queued candidate or mismatched user response", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        new Response(bytes, { headers: { "X-ADTR-User-ID": "10" } }),
      ),
  );
  await expect(
    api.download(
      "domain-a",
      task,
      profile,
      new AbortController().signal,
      payload,
    ),
  ).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
  await expect(
    api.download(
      "domain-a",
      succeeded,
      profile,
      new AbortController().signal,
      payload,
    ),
  ).rejects.toThrow();
});

function cancelReceipt(state: Task["state"]): Receipt {
  const w = {
    domainId: "domain-a",
    taskId: task.taskUUID,
    operationId: "cancel-a",
  };
  return {
    contract: dnsIPContract,
    simulation: true,
    action: "cancel",
    actorId: profile.ID,
    operationId: w.operationId,
    intent: receiptIntent("cancel", w),
    requestHash: "b".repeat(64),
    task: { ...task, state },
  };
}
async function recoverCancel() {
  mount();
  await screen.findByRole("option", { name: /observation-a/ });
  fireEvent.change(screen.getByLabelText("恢复操作 ID"), {
    target: { value: "cancel-a" },
  });
  fireEvent.click(screen.getByRole("button", { name: "恢复原操作回执" }));
  await screen.findByRole("region", { name: "DNS/IP 导出任务" });
}
it("recovers terminal cancel receipt on a fresh mount without an export manifest", async () => {
  vi.mocked(api.receipt).mockResolvedValue(cancelReceipt("cancelled"));
  vi.mocked(api.task).mockResolvedValue({ ...task, state: "cancelled" });
  await recoverCancel();
  expect(screen.getByRole("button", { name: "取消导出任务" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "下载已验证 XLSX" })).toBeNull();
  expect(screen.getByText(/历史操作回执/)).toBeVisible();
});
it("follows fresh cancel_requested recovery to terminal without an export pin", async () => {
  vi.mocked(api.receipt).mockResolvedValue(cancelReceipt("cancel_requested"));
  vi.mocked(api.task)
    .mockResolvedValueOnce({ ...task, state: "cancel_requested" })
    .mockResolvedValue({ ...task, state: "cancelled" });
  await recoverCancel();
  fireEvent.click(screen.getByRole("button", { name: "刷新导出任务" }));
  await waitFor(() => expect(api.task).toHaveBeenCalledTimes(2));
  await waitFor(() =>
    expect(screen.getByRole("heading", { name: /已取消/ })).toBeVisible(),
  );
  expect(screen.queryByRole("button", { name: "下载已验证 XLSX" })).toBeNull();
});
it("checks current trusted source before exposing success raced against recovered cancellation", async () => {
  vi.mocked(api.receipt).mockResolvedValue(cancelReceipt("cancel_requested"));
  vi.mocked(api.task).mockResolvedValue(succeeded);
  await recoverCancel();
  expect(api.observations).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("button", { name: "下载已验证 XLSX" })).toBeVisible();
});
it("rejects recovered cancel success when current trusted source differs", async () => {
  vi.mocked(api.receipt).mockResolvedValue(cancelReceipt("cancel_requested"));
  vi.mocked(api.task).mockResolvedValue(succeeded);
  vi.mocked(api.observations)
    .mockResolvedValueOnce([observation])
    .mockResolvedValue([{ ...observation, hash: "c".repeat(64) }]);
  mount();
  await screen.findByRole("option", { name: /observation-a/ });
  fireEvent.change(screen.getByLabelText("恢复操作 ID"), {
    target: { value: "cancel-a" },
  });
  fireEvent.click(screen.getByRole("button", { name: "恢复原操作回执" }));
  await screen.findByText(/服务器结果与当前观测/);
  expect(screen.queryByRole("button", { name: "下载已验证 XLSX" })).toBeNull();
});

it("keeps confirmed submission pending when its current task read is unavailable and recovers without resubmitting", async () => {
  vi.mocked(api.task)
    .mockRejectedValueOnce(new ApiError("simulation_unavailable", 503))
    .mockResolvedValue(task);
  mount();
  await select();
  fireEvent.click(screen.getByRole("button", { name: /导出全部 25/ }));
  await screen.findByText(/操作结果待确认/);
  await waitFor(() => expect(api.task).toHaveBeenCalledOnce());
  const submitted = requests.mock.calls[0][1];
  vi.mocked(api.receipt).mockResolvedValue(receipt(submitted));
  fireEvent.click(screen.getByRole("button", { name: "恢复原操作回执" }));
  await screen.findByRole("region", { name: "DNS/IP 导出任务" });
  expect(requests).toHaveBeenCalledOnce();
  expect(api.receipt).toHaveBeenCalledWith(
    "domain-a",
    submitted.operationId,
    profile,
    expect.any(AbortSignal),
    { action: "export", input: submitted },
  );
  expect(screen.queryByText(/操作结果待确认/)).toBeNull();
});
