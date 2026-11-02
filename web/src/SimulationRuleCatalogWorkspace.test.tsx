// Synthetic DOM/client contracts, not PostgreSQL/browser product acceptance.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import { afterEach, beforeEach, it, expect, vi } from "vitest";
import { SimulationRuleCatalogDomainWorkspace as Workspace } from "./SimulationRuleCatalogWorkspace";
import { ApiError, type Profile } from "./api";
import {
  simulationRuleCatalogAPI as api,
  catalogContract,
  copyAcknowledgement,
  downloadCatalog,
  intentFingerprint,
  parseCatalogResponse,
  validDeclaration,
  verifyReceipt,
  receiptIntent,
  type Write,
  type Fixture,
  type CatalogRecord,
  type Receipt,
  type Export,
} from "./simulation-rule-catalog-api";
const profile = { ID: 9, csrfToken: "csrf", username: "reviewer" } as Profile;
const h = "a".repeat(64),
  decl = {
    combine: "all" as const,
    conditions: [{ field: "event.code", operator: "eq", values: ["4625"] }],
  };
const rule = {
  ...decl,
  id: "r1",
  version: 1,
  enabled: false,
  scope: { tenant: "t", domain: "d" },
};
const report = {
  digest: h,
  passed: true,
  cases: [
    {
      event: { id: "positive", fields: { "event.code": "4625" } },
      want_match: true,
    },
    {
      event: { id: "negative", fields: { "event.code": "4624" } },
      want_match: false,
    },
  ],
};
const summary = {
  copySupported: true,
  copyUnsupportedFields: [],
  id: "r1",
  revision: 3,
  draftVersion: 1,
  latestPublishedVersion: 1,
  activeVersion: 1,
  archived: false,
  testPassed: true,
  lastEditorId: 2,
  updatedAt: "2026-11-02T00:00:00Z",
};
const record: CatalogRecord = {
  ...summary,
  contract: catalogContract,
  simulation: true,
  learningSupported: false,
  hash: h,
  caseResults: [
    { id: "positive", wantMatch: true, actualMatch: true },
    { id: "negative", wantMatch: false, actualMatch: false },
  ],
  snapshot: {
    scope: { tenant: "t", domain: "d" },
    revision: 3,
    entries: [
      {
        id: "r1",
        draft: rule,
        test: report,
        published: [{ rule, digest: h, test: report, reviewer: "9" }],
        active_version: 1,
        archived: false,
      },
    ],
  },
};
const permissions = { read: true, manage: true, publish: true, copy: true };
function show(p = permissions) {
  render(
    <Workspace
      profile={profile}
      sessionChanged={vi.fn()}
      domainId="d"
      permissions={p}
    />,
  );
}
async function select() {
  fireEvent.click(await screen.findByRole("button", { name: "r1" }));
  await screen.findByLabelText("生成声明 JSON");
}
beforeEach(() => {
  vi.spyOn(api, "list").mockResolvedValue({
    items: [summary],
    count: 1,
    offset: 0,
    limit: 20,
  });
  vi.spyOn(api, "detail").mockResolvedValue(structuredClone(record));
  vi.spyOn(api, "write").mockImplementation(async (action, input) => ({
    simulation: true,
    requestHash: h,
    operationId: input.operationId,
    action: action === "create-template" ? "copy-template" : action,
    intent: receiptIntent(action, input),
    actorId: 9,
    at: "now",
    rule: summary,
  }));
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it("loads only metadata then lazy detail and offers explicit read-only JSON and selects", async () => {
  show();
  await screen.findByRole("button", { name: "r1" });
  expect(api.detail).not.toHaveBeenCalled();
  await select();
  expect(screen.getByLabelText("组合")).toHaveAttribute("aria-label", "组合");
  expect(screen.getByLabelText("字段 1")).toHaveAttribute(
    "aria-label",
    "字段 1",
  );
  expect(screen.getByLabelText("生成声明 JSON")).toHaveAttribute("readonly");
  expect(screen.getByLabelText("实际测试结果")).toHaveTextContent(
    '"actualMatch": false',
  );
});
it("read-only reviewer may publish but cannot change drafts", async () => {
  show({ ...permissions, manage: false, copy: false });
  await select();
  expect(screen.getByRole("button", { name: "保存草稿修订" })).toBeDisabled();
  const publish = screen.getByRole("button", {
    name: "以当前身份审核并发布模拟版本",
  });
  expect(publish).toBeEnabled();
  fireEvent.click(publish);
  await waitFor(() =>
    expect(api.write).toHaveBeenCalledWith(
      "publish",
      expect.objectContaining({ id: "r1", expectedRevision: 3 }),
      profile,
      expect.any(AbortSignal),
    ),
  );
  expect(JSON.stringify(vi.mocked(api.write).mock.calls[0][1])).not.toContain(
    "reviewer",
  );
});
it("last editor cannot self-publish", async () => {
  vi.mocked(api.detail).mockResolvedValue({ ...record, lastEditorId: 9 });
  show();
  await select();
  expect(
    screen.getByRole("button", { name: "以当前身份审核并发布模拟版本" }),
  ).toBeDisabled();
});
it("loading a selected record never enables accidental new creation", async () => {
  let resolve!: (r: CatalogRecord) => void;
  vi.mocked(api.detail).mockReturnValue(new Promise((r) => (resolve = r)));
  show();
  fireEvent.click(await screen.findByRole("button", { name: "r1" }));
  expect(screen.getByRole("button", { name: "新建独立草稿" })).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: "保存新草稿" }),
  ).not.toBeInTheDocument();
  await act(async () => resolve(record));
  expect(screen.getByRole("button", { name: "保存草稿修订" })).toBeEnabled();
});
it("requires no-cascade acknowledgement and preserves source digest/copy identity", async () => {
  vi.mocked(api.write).mockImplementation(async (action, input) => ({
    simulation: true,
    requestHash: h,
    operationId: input.operationId,
    action: action === "create-template" ? "copy-template" : action,
    intent: receiptIntent(action, input),
    actorId: 9,
    at: "now",
    origin: {
      ruleId: "r1",
      publishedVersion: 1,
      publishedDigest: h,
      catalogRevision: 3,
      templateId: "copy-1",
      templateVersion: 1,
      templateHash: "b".repeat(64),
      acknowledgement: copyAcknowledgement,
    },
  }));
  show();
  await select();
  fireEvent.change(screen.getByLabelText("新模板 ID"), {
    target: { value: "copy-1" },
  });
  fireEvent.change(screen.getByLabelText("新模板名称"), {
    target: { value: "独立合成模板" },
  });
  const button = screen.getByRole("button", { name: "复制为独立手动模板" });
  expect(button).toBeDisabled();
  expect(screen.getByText(/以后停用或归档规则库不会改变/)).toBeInTheDocument();
  fireEvent.click(screen.getByLabelText("确认独立副本不级联"));
  fireEvent.click(button);
  await waitFor(() =>
    expect(api.write).toHaveBeenCalledWith(
      "create-template",
      expect.objectContaining({
        publishedDigest: h,
        publishedVersion: 1,
        templateId: "copy-1",
        acknowledgement: copyAcknowledgement,
      }),
      profile,
      expect.any(AbortSignal),
    ),
  );
  expect(await screen.findByLabelText("操作回执")).toHaveTextContent(
    "b".repeat(64),
  );
  expect(screen.getByLabelText("操作回执")).toHaveTextContent(h);
});
it("recovers ambiguous submission receipt before retry and freezes exact intent", async () => {
  vi.mocked(api.write).mockRejectedValueOnce(new ApiError("network"));
  const receiptSpy = vi
    .spyOn(api, "receipt")
    .mockImplementation(async (_, op) => ({
      simulation: true,
      requestHash: h,
      operationId: op,
      intent: receiptIntent("edit", vi.mocked(api.write).mock.calls[0][1]),
      action: "edit",
      actorId: 9,
      at: "now",
      rule: summary,
    }));
  show();
  await select();
  fireEvent.change(screen.getByLabelText("值 1"), {
    target: { value: "4626" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存草稿修订" }));
  const recovery = await screen.findByRole("button", {
    name: "查询回执并安全重试原操作",
  });
  expect(screen.getByLabelText("值 1")).toBeDisabled();
  fireEvent.click(recovery);
  await waitFor(() => expect(receiptSpy).toHaveBeenCalled());
  await screen.findByLabelText("操作回执");
  expect(api.write).toHaveBeenCalledTimes(1);
  expect(
    vi.mocked(api.write).mock.calls[0][1].declaration?.conditions[0].values,
  ).toEqual(["4626"]);
});
it("clears sensitive detail after permission denial", async () => {
  vi.mocked(api.write).mockRejectedValue(new ApiError("forbidden", 403));
  show();
  await select();
  fireEvent.click(screen.getByRole("button", { name: "保存草稿修订" }));
  await screen.findByText(/已清除规则与回执/);
  expect(screen.queryByLabelText("生成声明 JSON")).not.toBeInTheDocument();
});
it("validates aggregate reports and declared allowlists, never coerces bad arrays", () => {
  expect(validDeclaration(decl)).toBe(true);
  expect(
    validDeclaration({
      ...decl,
      conditions: [{ field: "arbitrary", operator: "eq", values: ["x"] }],
    }),
  ).toBe(false);
  expect(() => parseCatalogResponse("detail", record)).not.toThrow();
  expect(() =>
    parseCatalogResponse("detail", { ...record, simulation: false }),
  ).toThrow();
  expect(() =>
    parseCatalogResponse("list", {
      items: [record],
      count: 1,
      limit: 1000,
      offset: 0,
    }),
  ).toThrow();
});
it("fingerprints complete editable intent and rejects foreign actor receipt", () => {
  const input = {
    domainId: "d",
    id: "r1",
    operationId: "op",
    expectedRevision: 3,
    declaration: decl,
  };
  expect(intentFingerprint("edit", input)).not.toEqual(
    intentFingerprint("edit", {
      ...input,
      declaration: { ...decl, combine: "any" },
    }),
  );
  expect(() =>
    verifyReceipt(
      {
        simulation: true,
        requestHash: h,
        operationId: "op",
        intent: receiptIntent("edit", input),
        action: "edit",
        actorId: 2,
        at: "now",
        rule: summary,
      },
      "edit",
      input,
      9,
    ),
  ).toThrow();
});
it("exports actual parseable JSON bytes and attachment name, not a toast", async () => {
  let blob: Blob | undefined;
  vi.stubGlobal(
    "URL",
    Object.assign(URL, {
      createObjectURL: vi.fn((v: Blob) => {
        blob = v;
        return "blob:test";
      }),
      revokeObjectURL: vi.fn(),
    }),
  );
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  const value: Export = {
    simulation: true,
    contract: catalogContract,
    id: "r1",
    publications: [
      {
        rule: record.snapshot.entries[0].published[0],
        reviewedBy: 9,
        lastEditorId: 2,
        testedBy: 2,
        testRevision: 2,
        publishedAt: "now",
      },
    ],
  };
  downloadCatalog(value);
  expect(click).toHaveBeenCalledOnce();
  const content = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = reject;
    reader.readAsText(blob!);
  });
  expect(JSON.parse(content)).toEqual(value);
  expect(JSON.parse(content).publications[0].rule.digest).toBe(h);
});
it("disables unsupported copy and names unsupported fields", async () => {
  vi.mocked(api.detail).mockResolvedValue({
    ...record,
    copySupported: false,
    copyUnsupportedFields: ["event.code"],
  });
  show();
  await select();
  expect(screen.getByText(/不支持字段：event.code/)).toBeInTheDocument();
  expect(screen.getByLabelText("确认独立副本不级联")).toBeDisabled();
});
it("late response from previous domain cannot populate the new domain", async () => {
  let resolve!: (r: CatalogRecord) => void;
  vi.mocked(api.detail).mockReturnValueOnce(new Promise((r) => (resolve = r)));
  const view = render(
    <Workspace
      profile={profile}
      sessionChanged={vi.fn()}
      domainId="d"
      permissions={permissions}
    />,
  );
  fireEvent.click(await screen.findByRole("button", { name: "r1" }));
  view.rerender(
    <Workspace
      profile={profile}
      sessionChanged={vi.fn()}
      domainId="other"
      permissions={permissions}
    />,
  );
  await act(async () => resolve(record));
  expect(screen.queryByLabelText("生成声明 JSON")).not.toBeInTheDocument();
});
it("pagination requests bounded metadata offsets only", async () => {
  vi.mocked(api.list).mockResolvedValue({
    items: [summary],
    count: 2,
    offset: 0,
    limit: 20,
  });
  show();
  fireEvent.click(await screen.findByRole("button", { name: "下一页规则" }));
  await waitFor(() =>
    expect(api.list).toHaveBeenCalledWith(
      "d",
      "",
      1,
      profile,
      expect.any(AbortSignal),
    ),
  );
  expect(api.detail).not.toHaveBeenCalled();
});
it("accepts backend copy-template receipt and rejects every mismatched fixed intent field", () => {
  const input: Write = {
    domainId: "d",
    id: "r1",
    operationId: "op",
    expectedRevision: 5,
    publishedVersion: 1,
    publishedDigest: h,
    templateId: "copy-1",
    name: "name",
    acknowledgement: copyAcknowledgement,
  };
  const real: Receipt = {
    simulation: true,
    requestHash: h,
    operationId: "op",
    action: "copy-template",
    actorId: 9,
    at: "now",
    intent: {
      domainId: "d",
      id: "r1",
      action: "copy-template",
      operationId: "op",
      expectedRevision: 5,
      declaration: null,
      cases: null,
      publishedVersion: 1,
      publishedDigest: h,
      templateId: "copy-1",
      name: "name",
      acknowledgement: copyAcknowledgement,
    },
    origin: {
      ruleId: "r1",
      publishedVersion: 1,
      publishedDigest: h,
      catalogRevision: 5,
      templateId: "copy-1",
      templateVersion: 1,
      templateHash: "b".repeat(64),
      acknowledgement: copyAcknowledgement,
    },
  };
  expect(() => verifyReceipt(real, "create-template", input, 9)).not.toThrow();
  expect(() =>
    verifyReceipt(
      { ...real, action: "create-template" },
      "create-template",
      input,
      9,
    ),
  ).toThrow();
  for (const key of Object.keys(real.intent) as (keyof Receipt["intent"])[]) {
    const altered = structuredClone(real);
    const old = altered.intent[key];
    Object.assign(altered.intent, {
      [key]:
        typeof old === "number"
          ? old + 1
          : old === null
            ? []
            : String(old) + "-changed",
    });
    expect(
      () => verifyReceipt(altered, "create-template", input, 9),
      key,
    ).toThrow();
  }
  expect(() =>
    verifyReceipt(
      {
        ...real,
        intent: { ...real.intent, extra: "unexpected" } as Receipt["intent"],
      },
      "create-template",
      input,
      9,
    ),
  ).toThrow();
});
it("binds full declarations and case arrays, ignoring only object key order", () => {
  const cases: Fixture[] = [
    {
      id: "positive",
      fields: { "event.outcome": "failure", "actor.name": "alice" },
      wantMatch: true,
    },
    {
      id: "negative",
      fields: { "event.outcome": "success" },
      wantMatch: false,
    },
  ];
  const input: Write = {
    domainId: "d",
    id: "r1",
    operationId: "op",
    expectedRevision: 3,
    cases,
  };
  const real: Receipt = {
    simulation: true,
    requestHash: h,
    operationId: "op",
    action: "test",
    actorId: 9,
    at: "now",
    rule: summary,
    intent: {
      domainId: "d",
      id: "r1",
      action: "test",
      operationId: "op",
      expectedRevision: 3,
      declaration: null,
      cases: structuredClone(cases),
      publishedVersion: 0,
      publishedDigest: "",
      templateId: "",
      name: "",
      acknowledgement: "",
    },
  };
  const reordered = structuredClone(real);
  reordered.intent.cases![0].fields = {
    "actor.name": "alice",
    "event.outcome": "failure",
  };
  expect(() => verifyReceipt(reordered, "test", input, 9)).not.toThrow();
  for (const mutation of [
    (r: Receipt) => {
      r.intent.cases![0].fields["event.outcome"] = "success";
    },
    (r: Receipt) => {
      r.intent.cases![0].wantMatch = false;
    },
    (r: Receipt) => {
      r.intent.cases!.reverse();
    },
    (r: Receipt) => {
      r.intent.cases![0].id = "other";
    },
  ]) {
    const altered = structuredClone(real);
    mutation(altered);
    expect(() => verifyReceipt(altered, "test", input, 9)).toThrow();
  }
  const editInput: Write = { ...input, cases: undefined, declaration: decl };
  const edit: Receipt = {
    ...real,
    action: "edit",
    intent: {
      ...real.intent,
      action: "edit",
      cases: null,
      declaration: structuredClone(decl),
    },
  };
  expect(() => verifyReceipt(edit, "edit", editInput, 9)).not.toThrow();
  edit.intent.declaration!.conditions[0].values = ["altered"];
  expect(() => verifyReceipt(edit, "edit", editInput, 9)).toThrow();
});
it("selection published-only B replaces A declaration and testcase edits with B saved evidence", async () => {
  const b = structuredClone(record);
  b.id = "r2";
  b.snapshot.entries[0].id = "r2";
  b.snapshot.entries[0].draft = null;
  b.snapshot.entries[0].test = null;
  b.snapshot.entries[0].published[0].rule = {
    ...rule,
    id: "r2",
    conditions: [
      { field: "event.outcome", operator: "eq", values: ["failure"] },
    ],
  };
  b.snapshot.entries[0].published[0].test = {
    ...report,
    cases: [
      {
        event: { id: "b-positive", fields: { "event.outcome": "failure" } },
        want_match: true,
      },
      {
        event: { id: "b-negative", fields: { "event.outcome": "success" } },
        want_match: false,
      },
    ],
  };
  vi.mocked(api.list).mockResolvedValue({
    items: [summary, { ...summary, id: "r2" }],
    count: 2,
    offset: 0,
    limit: 20,
  });
  vi.mocked(api.detail).mockImplementation(async (_, id) =>
    structuredClone(id === "r2" ? b : record),
  );
  show();
  await select();
  fireEvent.change(screen.getByLabelText("值 1"), {
    target: { value: "A-unsaved" },
  });
  fireEvent.change(screen.getByLabelText("用例 ID 1"), {
    target: { value: "A-unsaved-case" },
  });
  fireEvent.click(screen.getByRole("button", { name: "r2" }));
  await waitFor(() =>
    expect(screen.getByLabelText("规则 ID")).toHaveValue("r2"),
  );
  expect(screen.getByLabelText("值 1")).toHaveValue("failure");
  expect(screen.getByLabelText("字段 1")).toHaveValue("event.outcome");
  expect(screen.getByLabelText("用例 ID 1")).toHaveValue("b-positive");
  expect(screen.getByLabelText("用例 1 event.outcome")).toHaveValue("failure");
  expect(screen.getByLabelText("用例 1 event.code")).toHaveValue("");
  fireEvent.click(screen.getByRole("button", { name: "保存草稿修订" }));
  await waitFor(() =>
    expect(api.write).toHaveBeenCalledWith(
      "edit",
      expect.objectContaining({
        id: "r2",
        declaration: {
          combine: "all",
          conditions: [
            { field: "event.outcome", operator: "eq", values: ["failure"] },
          ],
        },
      }),
      profile,
      expect.any(AbortSignal),
    ),
  );
});
