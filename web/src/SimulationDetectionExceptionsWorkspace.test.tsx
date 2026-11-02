// Synthetic client/DOM contracts. Actual PG/API/Worker/browser acceptance is separate.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, type Profile } from "./api";
import { SimulationDetectionExceptionsDomainWorkspace as Workspace } from "./SimulationDetectionExceptionsWorkspace";
import {
  simulationDetectionExceptionsAPI as api,
  exceptionContract,
  validDeclaration,
  parseDraftImport,
  validEvaluation,
  parseResponse,
  receiptIntent,
  verifyReceipt,
  downloadExceptions,
  type Declaration,
  type ExceptionRecord,
  type Receipt,
  type Evaluation,
  type Write,
  type Action,
} from "./simulation-detection-exceptions-api";
const profile = {
  ID: 9,
  csrfToken: "csrf-9",
  username: "synthetic",
} as Profile;
const h = "a".repeat(64),
  scope = { tenant: "t", domain: "d" },
  permissions = { read: true, manage: true, source: true };
const declaration: Declaration = {
  id: "exception-1",
  global: true,
  sourceTaskId: "",
  ruleId: "",
  reason: "maintenance",
  notBefore: "2026-01-01T00:00:00Z",
  expiresAt: "2027-01-01T00:00:00Z",
  conditions: [{ field: "event.outcome", operator: "eq", values: ["failure"] }],
};
const entry = {
  id: declaration.id,
  scope,
  global: true,
  enabled: false,
  archived: false,
  reason: declaration.reason,
  not_before: declaration.notBefore,
  expires_at: declaration.expiresAt,
  conditions: declaration.conditions,
};
const base: ExceptionRecord = {
  contract: exceptionContract,
  simulation: true,
  snapshot: {
    contract: "adtr.detection-exceptions.simulated.v1",
    provenance: "synthetic-business-contract-2026-11-01",
    scope,
    revision: 1,
    entries: [entry],
  },
  sourceTasks: {},
  actorId: 9,
  action: "create",
  at: "2026-11-02T00:00:00Z",
  hash: h,
};
const match = {
  contract: "adtr.detection.simulated.v1",
  provenance: "synthetic-business-contract-2026-11-01",
  rule_id: "r1",
  rule_version: 1,
  rule_digest: h,
  event_id: "event-1",
  scope,
};
const evaluation: Evaluation = {
  contract: exceptionContract,
  simulation: true,
  id: "evaluation-1",
  actorId: 9,
  sourceTaskId: "task-1",
  sourceResultHash: h,
  source: {
    simulation: true,
    contract: "adtr.detection.simulated.v1",
    matches: [match],
  },
  revision: 1,
  snapshotHash: h,
  evaluatedAt: "2026-11-02T00:00:00Z",
  matches: [
    {
      match,
      exceptions: [
        { exception_id: entry.id, reason: "maintenance", simulated: true },
      ],
    },
  ],
  hash: h,
};
const result = (a: Action, w: Write): Receipt => ({
  contract: exceptionContract,
  simulation: true,
  operationId: w.operationId,
  action: a,
  actorId: 9,
  at: "2026-11-02T00:00:00Z",
  intent: receiptIntent(a, w),
  requestHash: h,
  revision: a === "evaluate" ? 1 : 2,
  snapshotHash: h,
  ...(a === "evaluate"
    ? {
        evaluation: { ...evaluation, id: w.id!, sourceTaskId: w.sourceTaskId! },
      }
    : {}),
});
function view(p: Partial<React.ComponentProps<typeof Workspace>> = {}) {
  return render(
    <Workspace
      profile={profile}
      sessionChanged={() => {}}
      domainId="d"
      permissions={permissions}
      {...p}
    />,
  );
}
async function ready() {
  await screen.findByText(/当前修订 1/);
}
async function createForm() {
  fireEvent.click(screen.getByRole("button", { name: "新增禁用草稿" }));
  fireEvent.change(screen.getByLabelText("例外 ID"), {
    target: { value: "new-global" },
  });
  fireEvent.change(screen.getByLabelText("理由"), {
    target: { value: "explicit domain config" },
  });
}
beforeEach(() => {
  vi.spyOn(api, "snapshot").mockResolvedValue(structuredClone(base));
  vi.spyOn(api, "history").mockResolvedValue(structuredClone(base));
  vi.spyOn(api, "export").mockResolvedValue(structuredClone(base));
  vi.spyOn(api, "write").mockImplementation(async (a, w) => result(a, w));
  vi.spyOn(api, "receipt").mockRejectedValue(
    new ApiError("operation_not_found", 404),
  );
  vi.spyOn(api, "evaluation").mockResolvedValue(structuredClone(evaluation));
  vi.spyOn(api, "evaluations").mockResolvedValue([
    {
      id: evaluation.id,
      sourceTaskId: evaluation.sourceTaskId,
      revision: 1,
      evaluatedAt: evaluation.evaluatedAt,
    },
  ]);
  vi.spyOn(api, "preview").mockResolvedValue({
    ...structuredClone(declaration),
    global: false,
    sourceTaskId: "task-1",
    ruleId: "r1",
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it("uses current revision and declaration-only create DTO, with disabled creation semantics", async () => {
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await screen.findByLabelText("已确认回执");
  expect(api.write).toHaveBeenCalledTimes(1);
  const [a, w] = vi.mocked(api.write).mock.calls[0];
  expect(a).toBe("create");
  expect(w).toMatchObject({
    domainId: "d",
    expectedRevision: 1,
    declaration: { id: "new-global", global: true },
  });
  expect(w).not.toHaveProperty("id");
  expect(w.declaration).not.toHaveProperty("enabled");
});
it("resets editor when switching entries and edits with no top-level id", async () => {
  vi.mocked(api.snapshot).mockResolvedValue({
    ...structuredClone(base),
    snapshot: {
      ...base.snapshot,
      entries: [entry, { ...entry, id: "second", reason: "second reason" }],
    },
  });
  view();
  await ready();
  fireEvent.click(screen.getByRole("button", { name: /^exception-1 ·/ }));
  fireEvent.change(screen.getByLabelText("理由"), {
    target: { value: "unsaved" },
  });
  fireEvent.click(screen.getByRole("button", { name: /^second ·/ }));
  expect(screen.getByLabelText("理由")).toHaveValue("second reason");
  fireEvent.click(screen.getByRole("button", { name: "保存编辑并停用" }));
  await screen.findByLabelText("已确认回执");
  expect(vi.mocked(api.write).mock.calls[0][1]).toMatchObject({
    declaration: { id: "second", reason: "second reason" },
  });
  expect(vi.mocked(api.write).mock.calls[0][1]).not.toHaveProperty("id");
});
it("enables and archives selected config with exact id and expected revision", async () => {
  view();
  await ready();
  fireEvent.click(screen.getByRole("button", { name: /^exception-1 ·/ }));
  fireEvent.click(screen.getByRole("button", { name: "启用所选例外" }));
  await screen.findByLabelText("已确认回执");
  expect(api.write).toHaveBeenCalledWith(
    "enable",
    expect.objectContaining({ id: entry.id, expectedRevision: 1 }),
    profile,
    expect.any(AbortSignal),
  );
  fireEvent.click(screen.getByRole("button", { name: "归档所选例外" }));
  await waitFor(() => expect(api.write).toHaveBeenCalledTimes(2));
  expect(vi.mocked(api.write).mock.calls[1][0]).toBe("archive");
});
it("retains ambiguous operation and reads receipt before any same-operation replay", async () => {
  vi.mocked(api.write).mockRejectedValueOnce(new ApiError("network"));
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await screen.findByRole("region", { name: "未确认操作" });
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "查无回执后重试原操作" }),
    ).toBeEnabled(),
  );
  const first = vi.mocked(api.write).mock.calls[0][1];
  fireEvent.click(screen.getByRole("button", { name: "查无回执后重试原操作" }));
  await screen.findByLabelText("已确认回执");
  expect(api.receipt).toHaveBeenCalledWith(
    "d",
    first.operationId,
    profile,
    expect.any(AbortSignal),
  );
  expect(vi.mocked(api.write).mock.calls[1][1]).toEqual(first);
  expect(vi.mocked(api.receipt).mock.invocationCallOrder[0]).toBeLessThan(
    vi.mocked(api.write).mock.invocationCallOrder[1],
  );
});
it("recovers existing receipt without duplicate POST", async () => {
  vi.mocked(api.write).mockImplementationOnce(async (a, w) => {
    vi.mocked(api.receipt).mockResolvedValueOnce(result(a, w));
    throw new ApiError("network");
  });
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "读取原回执" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "读取原回执" }));
  await screen.findByLabelText("已确认回执");
  expect(api.write).toHaveBeenCalledTimes(1);
});
it("does not replay on receipt errors other than404", async () => {
  vi.mocked(api.write).mockRejectedValueOnce(new ApiError("network"));
  vi.mocked(api.receipt).mockRejectedValue(new ApiError("internal", 500));
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "查无回执后重试原操作" }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "查无回执后重试原操作" }));
  await waitFor(() => expect(api.receipt).toHaveBeenCalledTimes(1));
  expect(api.write).toHaveBeenCalledTimes(1);
});
it("rejects mismatched recovered intent and preserves unresolved operation", async () => {
  vi.mocked(api.write).mockImplementationOnce(async (a, w) => {
    const r = result(a, w);
    r.intent.declaration = { ...w.declaration!, reason: "different" };
    return r;
  });
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await screen.findByRole("region", { name: "未确认操作" });
  expect(screen.queryByLabelText("已确认回执")).not.toBeInTheDocument();
});
it("rejects disabled-flag or malformed imports and sends one atomic valid array", async () => {
  view();
  await ready();
  fireEvent.change(screen.getByLabelText("声明 JSON 数组"), {
    target: { value: JSON.stringify([{ ...declaration, enabled: true }]) },
  });
  fireEvent.click(screen.getByRole("button", { name: "原子导入（全部禁用）" }));
  expect(api.write).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("声明 JSON 数组"), {
    target: { value: JSON.stringify([declaration]) },
  });
  fireEvent.click(screen.getByRole("button", { name: "原子导入（全部禁用）" }));
  await screen.findByLabelText("已确认回执");
  expect(api.write).toHaveBeenCalledWith(
    "import",
    expect.objectContaining({ entries: [declaration] }),
    profile,
    expect.any(AbortSignal),
  );
});
it("evaluates actual source UUID and shows original matches alongside annotations", async () => {
  view();
  await ready();
  fireEvent.change(screen.getByLabelText("检测任务 UUID"), {
    target: { value: "task-1" },
  });
  fireEvent.change(screen.getByLabelText("评估记录 ID"), {
    target: { value: "evaluation-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "按当前修订显式评估" }));
  await screen.findByLabelText("评估结果");
  expect(screen.getByText(/原始命中 1 条，解释结果 1 条/)).toBeInTheDocument();
  expect(screen.getByText(/r1 \/ event-1/)).toHaveTextContent(
    "exception-1: maintenance",
  );
  expect(vi.mocked(api.write).mock.calls[0][1]).not.toHaveProperty("matches");
  expect(vi.mocked(api.write).mock.calls[0][1]).not.toHaveProperty("digest");
});
it("loads stored evaluation summaries lazily and retains fixed historical revision", async () => {
  view();
  await ready();
  expect(api.evaluations).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "读取本人评估记录列表" }));
  fireEvent.click(
    await screen.findByRole("button", { name: /^evaluation-1 ·/ }),
  );
  fireEvent.click(screen.getByRole("button", { name: "读取已保存评估" }));
  await screen.findByText(/固定修订 1/);
  expect(api.evaluation).toHaveBeenCalledWith(
    "d",
    "evaluation-1",
    profile,
    expect.any(AbortSignal),
  );
});
it("reads revision history lazily and never substitutes it for current CAS revision", async () => {
  view();
  await ready();
  expect(api.history).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "读取历史修订" }));
  await screen.findByText(/只读历史修订 1/);
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await screen.findByLabelText("已确认回执");
  expect(vi.mocked(api.write).mock.calls[0][1].expectedRevision).toBe(1);
});
it("denies mutations to read-only viewers and does not fetch source without permission", async () => {
  view({ permissions: { read: true, manage: false, source: false } });
  await ready();
  expect(screen.getByRole("button", { name: "新增禁用草稿" })).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "读取本人评估记录列表" }),
  ).toBeDisabled();
  expect(api.evaluation).not.toHaveBeenCalled();
  expect(api.evaluations).not.toHaveBeenCalled();
});
it("clears displayed data on current authorization denial", async () => {
  view();
  await ready();
  vi.mocked(api.snapshot).mockRejectedValueOnce(new ApiError("forbidden", 403));
  fireEvent.click(screen.getByRole("button", { name: "重新读取当前配置" }));
  await screen.findByText(/已清除页面数据/);
  expect(
    screen.queryByRole("button", { name: /^exception-1 ·/ }),
  ).not.toBeInTheDocument();
});
it("suppresses a late old-identity result and uses current CSRF for next writes", async () => {
  let release!: (r: ExceptionRecord) => void;
  vi.mocked(api.snapshot).mockImplementationOnce(
    () => new Promise((r) => (release = r)),
  );
  const v = view();
  v.rerender(
    <Workspace
      profile={{ ...profile, ID: 10, csrfToken: "new-csrf" }}
      sessionChanged={() => {}}
      domainId="d"
      permissions={permissions}
    />,
  );
  await ready();
  await act(async () =>
    release({
      ...structuredClone(base),
      snapshot: { ...base.snapshot, entries: [{ ...entry, id: "old-only" }] },
    }),
  );
  expect(
    screen.queryByRole("button", { name: /old-only/ }),
  ).not.toBeInTheDocument();
  await createForm();
  vi.mocked(api.write).mockImplementation(async (a, w) => ({
    ...result(a, w),
    actorId: 10,
  }));
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await screen.findByLabelText("已确认回执");
  expect(vi.mocked(api.write).mock.calls[0][2].csrfToken).toBe("new-csrf");
});
it("validates strict field/operator/time/import contracts", () => {
  expect(validDeclaration(declaration)).toBe(true);
  for (const d of [
    {
      ...declaration,
      conditions: [{ field: "event.code", operator: "eq", values: ["4625"] }],
    },
    {
      ...declaration,
      conditions: [
        { field: "event.outcome", operator: "regex", values: [".*"] },
      ],
    },
    { ...declaration, expiresAt: declaration.notBefore },
    { ...declaration, ruleDigest: h },
    {
      ...declaration,
      conditions: [declaration.conditions[0], declaration.conditions[0]],
    },
  ])
    expect(validDeclaration(d)).toBe(false);
  expect(() =>
    parseDraftImport(JSON.stringify([declaration, declaration])),
  ).toThrow();
  expect(() => parseDraftImport(JSON.stringify(base))).toThrow();
});
it("rejects truncated or altered original matches in annotated responses", () => {
  expect(validEvaluation(evaluation)).toBe(true);
  expect(validEvaluation({ ...evaluation, matches: [] })).toBe(false);
  expect(
    validEvaluation({
      ...evaluation,
      matches: [
        { ...evaluation.matches[0], match: { ...match, event_id: "altered" } },
      ],
    }),
  ).toBe(false);
  expect(() =>
    parseResponse("evaluation", { ...evaluation, simulation: false }),
  ).toThrow();
});
it("checks every typed intent field and treats equivalent RFC3339 times consistently", () => {
  const w: Write = {
    domainId: "d",
    operationId: "op1",
    expectedRevision: 1,
    declaration: { ...declaration, notBefore: "2026-01-01T00:00:00.000Z" },
  };
  const r = result("create", w);
  r.intent.declaration = { ...declaration };
  expect(() => verifyReceipt(r, "create", w, 9)).not.toThrow();
  for (const changed of [
    { ...r, actorId: 10 },
    { ...r, action: "edit" },
    { ...r, intent: { ...r.intent, domainId: "other" } },
    {
      ...r,
      intent: {
        ...r.intent,
        declaration: {
          ...declaration,
          conditions: [
            { field: "event.outcome", operator: "eq", values: ["success"] },
          ],
        },
      },
    },
    { ...r, intent: { ...r.intent, expectedRevision: 2 } },
  ])
    expect(() => verifyReceipt(changed as Receipt, "create", w, 9)).toThrow();
});
it("downloads actual evidence JSON bytes and revokes the object URL", async () => {
  let blob!: Blob;
  vi.stubGlobal(
    "URL",
    Object.assign(URL, {
      createObjectURL: vi.fn((b: Blob) => {
        blob = b;
        return "blob:evidence";
      }),
      revokeObjectURL: vi.fn(),
    }),
  );
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  downloadExceptions(base);
  expect(click).toHaveBeenCalledTimes(1);
  const raw = await new Promise<string>((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = reject;
    r.readAsText(blob);
  });
  expect(JSON.parse(raw)).toEqual(base);
  await waitFor(() =>
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:evidence"),
  );
});
it("client rejects a successful response for another user and sends current CSRF", async () => {
  vi.mocked(api.snapshot).mockRestore();
  vi.mocked(api.write).mockRestore();
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(base), {
        status: 200,
        headers: { "X-ADTR-User-ID": "10" },
      }),
    ),
  );
  await expect(
    api.snapshot("d", profile, new AbortController().signal),
  ).rejects.toMatchObject({ status: 401 });
  const w = {
    domainId: "d",
    operationId: "op",
    expectedRevision: 1,
    id: entry.id,
  };
  vi.mocked(fetch).mockResolvedValueOnce(
    new Response(JSON.stringify(result("enable", w)), {
      status: 200,
      headers: { "X-ADTR-User-ID": "9" },
    }),
  );
  await api.write("enable", w, profile, new AbortController().signal);
  expect(vi.mocked(fetch).mock.calls[1][1]).toMatchObject({
    credentials: "same-origin",
    cache: "no-store",
    method: "POST",
    headers: { "X-CSRF-Token": "csrf-9" },
  });
});
it("previews from a server-verified real event, then requires explicit disabled save", async () => {
  view();
  await ready();
  await createForm();
  fireEvent.change(screen.getByLabelText("检测任务 UUID"), {
    target: { value: "task-1" },
  });
  fireEvent.change(screen.getByLabelText("来源事件 ID"), {
    target: { value: "event-1" },
  });
  fireEvent.change(screen.getByLabelText("来源规则 ID"), {
    target: { value: "r1" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "从真实命中预览禁用声明" }),
  );
  await screen.findByText(/预览未写入/);
  expect(api.write).not.toHaveBeenCalled();
  expect(screen.getByLabelText("规则来源任务 UUID")).toHaveValue("task-1");
  expect(screen.getByLabelText("执行规则 ID")).toHaveValue("r1");
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await screen.findByLabelText("已确认回执");
  expect(vi.mocked(api.write).mock.calls[0][1].declaration).toMatchObject({
    global: false,
    sourceTaskId: "task-1",
    ruleId: "r1",
  });
});
it("reports a CAS conflict without automatic reissue and retains draft for reinspection", async () => {
  vi.mocked(api.write).mockRejectedValueOnce(
    new ApiError("simulation_conflict", 409),
  );
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await screen.findByText(/配置修订冲突/);
  expect(api.write).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("region", { name: "未确认操作" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("理由")).toHaveValue("explicit domain config");
  fireEvent.click(screen.getByRole("button", { name: "重新读取当前配置" }));
  await waitFor(() => expect(api.snapshot).toHaveBeenCalledTimes(2));
  expect(api.write).toHaveBeenCalledTimes(1);
});
it("rejects timestamps whose normalization would discard precision or calendar errors", () => {
  for (const notBefore of [
    "2026-01-01T00:00:00.000000001Z",
    "2026-02-30T00:00:00Z",
    "2026-01-01T08:00:00+08:00",
  ])
    expect(validDeclaration({ ...declaration, notBefore })).toBe(false);
  expect(
    validDeclaration({ ...declaration, notBefore: "2026-01-01T00:00:00.01Z" }),
  ).toBe(true);
});
it("client rejects cross-domain records and history/evaluation identifier mismatches", async () => {
  vi.mocked(api.snapshot).mockRestore();
  vi.mocked(api.history).mockRestore();
  vi.mocked(api.evaluation).mockRestore();
  vi.stubGlobal("fetch", vi.fn());
  const reply = (x: unknown) =>
    new Response(JSON.stringify(x), {
      status: 200,
      headers: { "X-ADTR-User-ID": "9" },
    });
  vi.mocked(fetch).mockResolvedValueOnce(
    reply({
      ...base,
      snapshot: { ...base.snapshot, scope: { tenant: "t", domain: "other" } },
    }),
  );
  await expect(
    api.snapshot("d", profile, new AbortController().signal),
  ).rejects.toMatchObject({ code: "invalid_response" });
  vi.mocked(fetch).mockResolvedValueOnce(reply(base));
  await expect(
    api.history("d", 2, profile, new AbortController().signal),
  ).rejects.toMatchObject({ code: "invalid_response" });
  vi.mocked(fetch).mockResolvedValueOnce(reply(evaluation));
  await expect(
    api.evaluation(
      "d",
      "other-evaluation",
      profile,
      new AbortController().signal,
    ),
  ).rejects.toMatchObject({ code: "invalid_response" });
});
it("unlocks editing after receipt404 followed by a definitive retry409, retaining the draft", async () => {
  vi.mocked(api.write)
    .mockRejectedValueOnce(new ApiError("network"))
    .mockRejectedValueOnce(new ApiError("simulation_conflict", 409));
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "查无回执后重试原操作" }),
    ).toBeEnabled(),
  );
  const original = vi.mocked(api.write).mock.calls[0][1];
  fireEvent.click(screen.getByRole("button", { name: "查无回执后重试原操作" }));
  await screen.findByText(/配置修订冲突/);
  expect(
    screen.queryByRole("region", { name: "未确认操作" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "重新读取当前配置" }),
  ).toBeEnabled();
  expect(screen.getByLabelText("理由")).toHaveValue("explicit domain config");
  expect(vi.mocked(api.write).mock.calls[1][1]).toEqual(original);
  fireEvent.click(screen.getByRole("button", { name: "重新读取当前配置" }));
  await waitFor(() => expect(api.snapshot).toHaveBeenCalledTimes(2));
  expect(api.write).toHaveBeenCalledTimes(2);
});
it("retains uncertain retry operation on5xx and on read-only receipt404", async () => {
  vi.mocked(api.write)
    .mockRejectedValueOnce(new ApiError("network"))
    .mockRejectedValueOnce(new ApiError("internal", 500));
  view();
  await ready();
  await createForm();
  fireEvent.click(screen.getByRole("button", { name: "保存禁用草稿" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "读取原回执" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "读取原回执" }));
  await waitFor(() => expect(api.receipt).toHaveBeenCalledTimes(1));
  expect(api.write).toHaveBeenCalledTimes(1);
  expect(
    screen.getByRole("region", { name: "未确认操作" }),
  ).toBeInTheDocument();
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "查无回执后重试原操作" }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "查无回执后重试原操作" }));
  await waitFor(() => expect(api.write).toHaveBeenCalledTimes(2));
  expect(
    screen.getByRole("region", { name: "未确认操作" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "重新读取当前配置" }),
  ).toBeDisabled();
});
