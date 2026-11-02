// Synthetic DOM/HTTP tests only; no real AD or PostgreSQL claim.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SimulationLabelsDomainWorkspace as Workspace } from "./SimulationLabelsWorkspace";
import { ApiError, type Profile } from "./api";
import {
  parseLabelsResponse,
  simulationLabelsAPI,
  labelsContract,
} from "./simulation-labels-api";
const profile: Profile = {
  ID: 1,
  username: "reader",
  role: "admin",
  priv: 99,
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
const hash = "a".repeat(64);
const dataset = {
  id: "fixture",
  contract: labelsContract,
  producer: "synthetic-only",
  simulation: true,
  hash,
  observationCount: 2,
};
const observation = {
  id: "obs1",
  sequence: 1,
  complete: true,
  hash,
  assets: [
    {
      objectId: "group",
      name: "Sensitive group",
      description: "Synthetic group",
    },
    { objectId: "user", name: "Synthetic user", description: null },
  ],
  snapshot: {
    simulated: true,
    scope: { tenantId: "tenant", domainId: "domain" },
    observationId: "obs1",
    nodes: [
      { id: "group", kind: "group", membersKnown: true },
      { id: "user", kind: "user", membersKnown: true },
    ],
  },
};
const policy = {
  id: "policy",
  datasetId: "fixture",
  revision: 1,
  status: "active",
  spec: { name: "My policy", sensitiveGroupIds: ["group"], manualLabels: [] },
  hash,
  actor: "1",
  reason: "fixture",
  at: "2026-11-02T20:00:00Z",
};
const evaluation = {
  id: "eval1",
  dataset,
  observation,
  policy,
  result: {
    simulated: true,
    completeWithinSnapshot: true,
    scope: { tenantId: "tenant", domainId: "domain" },
    objects: [],
  },
  hash,
  actor: "1",
  at: policy.at,
};
const asset = {
  objectId: "user",
  name: "Synthetic user",
  description: null,
  kind: "user",
  state: "sensitive",
  labels: [
    {
      label: "sensitive_group",
      source: "transitive",
      groupId: "group",
      distance: 2,
    },
  ],
  isClick: true,
  isHoneypot: null,
  honeypotAvailability: "unavailable",
  reason: "synthetic evidence",
};
const comparison = {
  id: "cmp",
  datasetId: "fixture",
  beforeId: "eval1",
  afterId: "eval2",
  hash,
  actor: "1",
  at: policy.at,
  alerts: [],
};
const receipt = {
  simulation: true,
  operationId: "operation",
  actor: "1",
  action: "create",
  datasetId: "fixture",
  reason: "test",
  at: policy.at,
  policy,
};
const page = (items: unknown[], offset = 0) => ({
  items,
  count: items.length,
  offset,
  limit: 100,
});
const response = (v: unknown, status = 200, actor = "1") =>
  new Response(JSON.stringify(v), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
  });
let handler: (url: URL, init: RequestInit) => Promise<Response> | Response;
let postBodies: Record<string, unknown>[];
function standard(url: URL, init: RequestInit) {
  const route = url.pathname.split("/").at(-1)!;
  if (init.method === "POST") {
    const body = JSON.parse(String(init.body));
    postBodies.push(body);
    return response({
      ...receipt,
      operationId: body.operationId,
      action: route,
      reason: body.reason,
      policy: {
        ...policy,
        id: body.policyId,
        revision: body.expectedRevision + 1,
        spec: body.spec ?? policy.spec,
      },
    });
  }
  const values: Record<string, unknown> = {
    datasets: page([dataset]),
    observations: page([
      observation,
      {
        ...observation,
        id: "obs2",
        sequence: 2,
        snapshot: { ...observation.snapshot, observationId: "obs2" },
      },
    ]),
    policies: page([policy]),
    policy,
    history: page([policy]),
    evaluations: page([
      evaluation,
      {
        ...evaluation,
        id: "eval2",
        observation: {
          ...observation,
          id: "obs2",
          sequence: 2,
          snapshot: { ...observation.snapshot, observationId: "obs2" },
        },
      },
    ]),
    observation: {
      ...observation,
      id: url.searchParams.get("observationId") ?? "obs1",
      snapshot: {
        ...observation.snapshot,
        observationId: url.searchParams.get("observationId") ?? "obs1",
      },
    },
    evaluation: {
      ...evaluation,
      id: url.searchParams.get("evaluationId") ?? "eval1",
      observation:
        url.searchParams.get("evaluationId") === "eval2"
          ? {
              ...observation,
              id: "obs2",
              sequence: 2,
              snapshot: { ...observation.snapshot, observationId: "obs2" },
            }
          : observation,
    },
    comparison,
    comparisons: page([comparison]),
    assets: page([asset]),
    asset,
    operation: { ...receipt, operationId: url.searchParams.get("operationId") },
  };
  return response(values[route]);
}
beforeEach(() => {
  localStorage.clear();
  postBodies = [];
  handler = standard;
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init: RequestInit = {}) =>
      handler(new URL(url, "https://test"), init),
    ),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const props = {
  profile,
  domainId: "domain",
  permissions: { read: true, manage: true },
  sessionChanged: vi.fn(),
};
async function open() {
  render(<Workspace {...props} />);
  await screen.findByRole("option", { name: "fixture" });
  fireEvent.change(screen.getByLabelText("合成数据集"), {
    target: { value: "fixture" },
  });
  await screen.findByRole("option", { name: /My policy/ });
  fireEvent.change(screen.getByLabelText("固定观测"), {
    target: { value: "obs1" },
  });
  await screen.findByText(/观测 hash/);
}
async function fill() {
  await open();
  fireEvent.change(screen.getByLabelText("策略名称"), {
    target: { value: "New policy" },
  });
  fireEvent.change(screen.getByLabelText("操作原因"), {
    target: { value: "test" },
  });
}
describe("label workspace", () => {
  it("does not read without explicit read grant", () => {
    render(
      <Workspace {...props} permissions={{ read: false, manage: false }} />,
    );
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toContain("无读取权限");
  });
  it("shows fixture, pinned observations and truthful provenance/honeypot", async () => {
    await open();
    expect(screen.getByText(/生产者 synthetic-only/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("已保存评估"), {
      target: { value: "eval1" },
    });
    fireEvent.click(
      await screen.findByRole("button", { name: /Synthetic user \(user/ }),
    );
    await screen.findByRole("region", { name: "资产标签详情" });
    expect(screen.getByText(/蜜罐状态：不可用/)).toBeTruthy();
    expect(
      screen.getByText(/Sensitive group · Synthetic group · 距离 2/),
    ).toBeTruthy();
  });
  it("uses scoped CAS and no browser actor or tenant authority", async () => {
    await open();
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "policy" },
    });
    await screen.findByText(/当前 r1/);
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "edit" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存新修订" }));
    await screen.findByRole("region", { name: "历史操作回执" });
    expect(postBodies[0]).toMatchObject({
      domainId: "domain",
      datasetId: "fixture",
      policyId: "policy",
      expectedRevision: 1,
      reason: "edit",
    });
    expect(postBodies[0]).not.toHaveProperty("actor");
    expect(postBodies[0]).not.toHaveProperty("tenantId");
  });
  it("prevents rapid duplicate writes synchronously", async () => {
    let release!: (r: Response) => void;
    handler = (u, i) =>
      i.method === "POST"
        ? new Promise((r) => {
            postBodies.push(JSON.parse(String(i.body)));
            release = r;
          })
        : standard(u, i);
    await fill();
    const button = screen.getByRole("button", { name: "创建策略" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(postBodies).toHaveLength(1);
    await act(async () =>
      release(
        response({
          ...receipt,
          operationId: postBodies[0].operationId,
          policy: {
            ...policy,
            id: postBodies[0].policyId,
            spec: postBodies[0].spec,
          },
        }),
      ),
    );
  });
  it("retains stable operation on response loss and retries same payload", async () => {
    let attempts = 0;
    handler = (u, i) => {
      if (i.method === "POST") {
        postBodies.push(JSON.parse(String(i.body)));
        if (++attempts === 1) return Promise.reject(new TypeError("offline"));
        return response({
          ...receipt,
          operationId: postBodies.at(-1)!.operationId,
          policy: {
            ...policy,
            id: postBodies.at(-1)!.policyId,
            spec: postBodies.at(-1)!.spec,
          },
        });
      }
      return standard(u, i);
    };
    await fill();
    fireEvent.click(screen.getByRole("button", { name: "创建策略" }));
    await screen.findByText(/网络结果不确定/);
    fireEvent.click(
      screen.getByRole("button", { name: "使用相同操作 ID 重试" }),
    );
    await screen.findByRole("region", { name: "历史操作回执" });
    expect(postBodies).toHaveLength(2);
    expect(postBodies[1]).toEqual(postBodies[0]);
  });
  it("recovers persisted operation after reload without new POST", async () => {
    localStorage.setItem(
      "adtr.labels.intent.v1:1:domain",
      JSON.stringify({
        kind: "create",
        input: {
          domainId: "domain",
          datasetId: "fixture",
          operationId: "lost",
          reason: "test",
          policyId: "policy",
          expectedRevision: 0,
          spec: policy.spec,
        },
      }),
    );
    render(<Workspace {...props} />);
    fireEvent.click(
      await screen.findByRole("button", { name: "恢复原操作回执" }),
    );
    await screen.findByRole("region", { name: "历史操作回执" });
    expect(screen.getByText(/lost · create/)).toBeTruthy();
    expect(postBodies).toHaveLength(0);
    expect(localStorage.getItem("adtr.labels.intent.v1:1:domain")).toBeNull();
  });
  it("does not recover another actor storage", async () => {
    localStorage.setItem(
      "adtr.labels.intent.v1:2:domain",
      JSON.stringify({
        kind: "create",
        input: {
          domainId: "domain",
          datasetId: "fixture",
          operationId: "secret",
          reason: "test",
        },
      }),
    );
    await open();
    expect(screen.queryByRole("region", { name: "待恢复操作" })).toBeNull();
  });
  it("rejects actor mismatch and clears sensitive state", async () => {
    const changed = vi.fn();
    handler = (u, i) =>
      u.pathname.endsWith("/assets")
        ? response(page([asset]), 200, "2")
        : standard(u, i);
    render(<Workspace {...props} sessionChanged={changed} />);
    await screen.findByRole("option", { name: "fixture" });
    fireEvent.change(screen.getByLabelText("合成数据集"), {
      target: { value: "fixture" },
    });
    await screen.findByRole("option", { name: /eval1 · obs1/ });
    fireEvent.change(screen.getByLabelText("已保存评估"), {
      target: { value: "eval1" },
    });
    await screen.findByText(/授权已失效/);
    expect(screen.queryByLabelText("合成数据集")).toBeNull();
    expect(changed).toHaveBeenCalled();
  });
  it("clears all data on revocation response", async () => {
    await open();
    handler = (u, i) =>
      u.pathname.endsWith("/policy")
        ? response({ error: "forbidden" }, 403)
        : standard(u, i);
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "policy" },
    });
    await screen.findByText(/授权已失效/);
    expect(screen.queryByText(/生产者 synthetic-only/)).toBeNull();
  });
  it("ignores late dataset response after domain switch", async () => {
    let release!: (r: Response) => void;
    handler = (u, i) =>
      u.searchParams.get("domainId") === "domain"
        ? new Promise((r) => {
            release = r;
          })
        : response(page([]));
    const view = render(<Workspace {...props} />);
    view.rerender(<Workspace {...props} domainId="other" />);
    await act(async () => release(response(page([dataset]))));
    expect(screen.queryByRole("option", { name: "fixture" })).toBeNull();
  });
  it("clears state on logout invalidation and ignores late policy response", async () => {
    let release!: (r: Response) => void;
    await open();
    handler = (u, i) =>
      u.pathname.endsWith("/policy")
        ? new Promise((r) => {
            release = r;
          })
        : standard(u, i);
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "policy" },
    });
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "adtr.session-invalidation.v1",
          newValue: "a".repeat(32),
          storageArea: localStorage,
        }),
      ),
    );
    await act(async () => release(response(policy)));
    expect(screen.queryByLabelText("当前策略")).toBeNull();
  });
  it("shows zero changes distinctly from missing comparison", async () => {
    await open();
    fireEvent.change(screen.getByLabelText("已保存观测比较"), {
      target: { value: "cmp" },
    });
    expect(await screen.findByText(/变化为零，未生成告警/)).toBeTruthy();
    expect(screen.getByText(/策略编辑历史（不是资产变化告警）/)).toBeTruthy();
  });
  it("preserves incomplete observations as unknown not removals", async () => {
    handler = (u, i) =>
      u.pathname.endsWith("/evaluation")
        ? response({
            ...evaluation,
            result: { ...evaluation.result, completeWithinSnapshot: false },
          })
        : u.pathname.endsWith("/evaluations")
          ? response(
              page([
                {
                  ...evaluation,
                  result: {
                    ...evaluation.result,
                    completeWithinSnapshot: false,
                  },
                },
              ]),
            )
          : standard(u, i);
    await open();
    fireEvent.change(screen.getByLabelText("已保存评估"), {
      target: { value: "eval1" },
    });
    expect(await screen.findByText(/不完整／未知：不得推断/)).toBeTruthy();
  });
  it("filters server assets by pinned evaluation, type and name", async () => {
    await open();
    fireEvent.change(screen.getByLabelText("已保存评估"), {
      target: { value: "eval1" },
    });
    await screen.findByLabelText("资产类型");
    fireEvent.change(screen.getByLabelText("资产类型"), {
      target: { value: "user" },
    });
    fireEvent.change(screen.getByLabelText("资产名称"), {
      target: { value: "needle" },
    });
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining("evaluationId=eval1&kind=user&name=needle"),
        expect.anything(),
      ),
    );
  });
  it("read-only users cannot mutate", async () => {
    render(
      <Workspace {...props} permissions={{ read: true, manage: false }} />,
    );
    await screen.findByRole("option", { name: "fixture" });
    fireEvent.change(screen.getByLabelText("合成数据集"), {
      target: { value: "fixture" },
    });
    expect(
      (screen.getByRole("button", { name: "创建策略" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });
  it("CAS rejection is visible and can release only rejected intent", async () => {
    handler = (u, i) =>
      i.method === "POST"
        ? response({ error: "revision_conflict" }, 409)
        : standard(u, i);
    await fill();
    fireEvent.click(screen.getByRole("button", { name: "创建策略" }));
    await screen.findByText(/修订冲突/);
    fireEvent.click(
      screen.getByRole("button", { name: "放弃已被拒绝的请求并重载" }),
    );
    expect(screen.queryByRole("region", { name: "待恢复操作" })).toBeNull();
    expect(screen.queryByRole("region", { name: "历史操作回执" })).toBeNull();
  });
});
describe("label async and actions", () => {
  it("ignores late policy after returning to new-policy form", async () => {
    await open();
    let release!: (r: Response) => void;
    handler = (u, i) =>
      u.pathname.endsWith("/policy")
        ? new Promise((r) => {
            release = r;
          })
        : standard(u, i);
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "policy" },
    });
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "" },
    });
    await act(async () => release(response(policy)));
    expect(screen.queryByText(/当前 r1/)).toBeNull();
    expect((screen.getByLabelText("策略名称") as HTMLInputElement).value).toBe(
      "",
    );
  });
  it("ignores late asset detail after evaluation selection changes", async () => {
    await open();
    fireEvent.change(screen.getByLabelText("已保存评估"), {
      target: { value: "eval1" },
    });
    await screen.findByRole("button", { name: /Synthetic user \(user/ });
    let release!: (r: Response) => void;
    handler = (u, i) =>
      u.pathname.endsWith("/asset")
        ? new Promise((r) => {
            release = r;
          })
        : standard(u, i);
    fireEvent.click(
      screen.getByRole("button", { name: /Synthetic user \(user/ }),
    );
    fireEvent.change(screen.getByLabelText("已保存评估"), {
      target: { value: "eval2" },
    });
    await act(async () => release(response(asset)));
    expect(screen.queryByRole("region", { name: "资产标签详情" })).toBeNull();
  });
  it("sends archived policy CAS without a spec", async () => {
    await open();
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "policy" },
    });
    await screen.findByText(/当前 r1/);
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "archive" },
    });
    fireEvent.click(screen.getByRole("button", { name: "归档策略" }));
    await screen.findByRole("region", { name: "历史操作回执" });
    expect(postBodies[0]).toMatchObject({
      policyId: "policy",
      expectedRevision: 1,
    });
    expect(postBodies[0]).not.toHaveProperty("spec");
  });
  it("compares exact evaluation IDs and displays stored alert changes", async () => {
    const changed = {
      ...comparison,
      alerts: [
        {
          id: "alert",
          objectId: "user",
          simulation: true,
          delivery: "in_app_only",
          changes: [
            {
              objectId: "user",
              action: "removed",
              provenance: asset.labels[0],
            },
          ],
        },
      ],
    };
    handler = (u, i) => {
      if (i.method === "POST") {
        const b = JSON.parse(String(i.body));
        postBodies.push(b);
        return response({
          ...receipt,
          policy: undefined,
          action: "compare",
          reason: b.reason,
          operationId: b.operationId,
          comparison: changed,
        });
      }
      if (u.pathname.endsWith("/comparisons")) return response(page([changed]));
      if (u.pathname.endsWith("/comparison")) return response(changed);
      return standard(u, i);
    };
    await open();
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "compare" },
    });
    fireEvent.change(screen.getByLabelText("前置评估"), {
      target: { value: "eval1" },
    });
    fireEvent.change(screen.getByLabelText("后置评估"), {
      target: { value: "eval2" },
    });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "比较并生成站内告警",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole("button", { name: "比较并生成站内告警" }));
    await screen.findByRole("region", { name: "历史操作回执" });
    expect(postBodies[0]).toMatchObject({
      beforeId: "eval1",
      afterId: "eval2",
    });
    await screen.findByRole("option", { name: /cmp · 1/ });
    fireEvent.change(screen.getByLabelText("已保存观测比较"), {
      target: { value: "cmp" },
    });
    expect(await screen.findByText(/removed · sensitive_group/)).toBeTruthy();
    expect(screen.getByText(/移除一个来源不代表资产已不敏感/)).toBeTruthy();
  });
  it("displays installed empty dataset separately from loading", async () => {
    handler = () => response(page([]));
    render(<Workspace {...props} />);
    expect(screen.getByText("正在读取合成数据集…")).toBeTruthy();
    expect(screen.queryByText(/没有可用合成数据集/)).toBeNull();
    await screen.findByText(/没有可用合成数据集/);
  });
});
describe("recovery intent binding", () => {
  const input = {
    domainId: "domain",
    datasetId: "fixture",
    operationId: "lost-bound",
    reason: "original reason",
    policyId: "policy",
    expectedRevision: 0,
    spec: {
      name: "Original",
      sensitiveGroupIds: ["z", "a"],
      manualLabels: [
        { objectId: "z", label: "two" },
        { objectId: "a", label: "one" },
      ],
    },
  };
  const good = {
    ...receipt,
    operationId: input.operationId,
    reason: input.reason,
    policy: {
      ...policy,
      spec: {
        ...input.spec,
        sensitiveGroupIds: ["a", "z"],
        manualLabels: [...input.spec.manualLabels].reverse(),
      },
    },
  };
  const save = () =>
    localStorage.setItem(
      "adtr.labels.intent.v1:1:domain",
      JSON.stringify({ kind: "create", input }),
    );
  it.each(["name", "groups", "manualLabels", "reason"])(
    "keeps intent pending for mismatched %s",
    async (field) => {
      save();
      const bad = structuredClone(good);
      if (field === "name") bad.policy.spec.name = "Other";
      if (field === "groups") bad.policy.spec.sensitiveGroupIds = ["different"];
      if (field === "manualLabels")
        bad.policy.spec.manualLabels = [{ objectId: "a", label: "different" }];
      if (field === "reason") bad.reason = "Other reason";
      handler = (u, i) =>
        u.pathname.endsWith("/operation") ? response(bad) : standard(u, i);
      render(<Workspace {...props} />);
      fireEvent.click(
        await screen.findByRole("button", { name: "恢复原操作回执" }),
      );
      await screen.findByText("invalid_response");
      expect(screen.getByRole("region", { name: "待恢复操作" })).toBeTruthy();
      expect(screen.queryByRole("region", { name: "历史操作回执" })).toBeNull();
      expect(
        localStorage.getItem("adtr.labels.intent.v1:1:domain"),
      ).not.toBeNull();
      expect(postBodies).toHaveLength(0);
    },
  );
  it("accepts canonical ordering without changing labels or name", async () => {
    save();
    handler = (u, i) =>
      u.pathname.endsWith("/operation") ? response(good) : standard(u, i);
    render(<Workspace {...props} />);
    fireEvent.click(
      await screen.findByRole("button", { name: "恢复原操作回执" }),
    );
    await screen.findByRole("region", { name: "历史操作回执" });
    expect(localStorage.getItem("adtr.labels.intent.v1:1:domain")).toBeNull();
  });
});
describe("bounded windows and policy selection", () => {
  it("shrinks only oversized pages, retains bounded windows and cross-page comparison selections", async () => {
    const requested: number[][] = [];
    handler = (u, i) => {
      if (u.pathname.endsWith("/evaluations")) {
        const offset = Number(u.searchParams.get("offset")),
          limit = Number(u.searchParams.get("limit"));
        requested.push([offset, limit]);
        if (limit === 20)
          return response({ error: "simulation_page_too_large" }, 422);
        const e =
          offset === 0
            ? evaluation
            : {
                ...evaluation,
                id: "eval2",
                observation: {
                  ...observation,
                  id: "obs2",
                  sequence: 2,
                  snapshot: { ...observation.snapshot, observationId: "obs2" },
                },
              };
        return response({ items: [e], count: 5000, offset, limit });
      }
      return standard(u, i);
    };
    await open();
    await screen.findByRole("option", { name: /eval1 · obs1/ });
    expect(requested).toEqual([
      [0, 20],
      [0, 5],
    ]);
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "compare" },
    });
    fireEvent.change(screen.getByLabelText("前置评估"), {
      target: { value: "eval1" },
    });
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        expect.stringContaining(
          "/evaluation?domainId=domain&datasetId=fixture&evaluationId=eval1",
        ),
        expect.anything(),
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "下一页评估" }));
    await screen.findByRole("option", { name: /eval2 · obs2/ });
    expect(requested).toEqual([
      [0, 20],
      [0, 5],
      [1, 20],
      [1, 5],
    ]);
    expect(screen.queryByRole("option", { name: /eval1 · obs1/ })).toBeNull();
    expect((screen.getByLabelText("前置评估") as HTMLSelectElement).value).toBe(
      "eval1",
    );
    fireEvent.change(screen.getByLabelText("后置评估"), {
      target: { value: "eval2" },
    });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "比较并生成站内告警",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    expect(postBodies).toHaveLength(0);
  });
  it("does not retry unrelated limits or call them empty", async () => {
    let count = 0;
    handler = (u, i) => {
      if (u.pathname.endsWith("/datasets")) {
        count++;
        return response({ error: "simulation_limit_exceeded" }, 422);
      }
      return standard(u, i);
    };
    render(<Workspace {...props} />);
    await screen.findByText("simulation_limit_exceeded");
    expect(count).toBe(1);
    expect(screen.queryByText(/没有可用合成数据集/)).toBeNull();
  });
  it("stops oversized page retries at one and retains an explicit error", async () => {
    const limits: number[] = [];
    handler = (u, i) => {
      if (u.pathname.endsWith("/datasets")) {
        limits.push(Number(u.searchParams.get("limit")));
        return response({ error: "simulation_page_too_large" }, 422);
      }
      return standard(u, i);
    };
    render(<Workspace {...props} />);
    await screen.findByText("simulation_page_too_large");
    expect(limits).toEqual([20, 5, 1]);
    expect(screen.queryByText(/没有可用合成数据集/)).toBeNull();
  });
  it("never changes policy-loading into an enabled create action", async () => {
    let release!: (r: Response) => void;
    const second = {
      ...policy,
      id: "second",
      revision: 7,
      spec: { ...policy.spec, name: "Second policy" },
    };
    handler = (u, i) => {
      if (u.pathname.endsWith("/policies"))
        return response(page([policy, second]));
      if (
        u.pathname.endsWith("/policy") &&
        u.searchParams.get("policyId") === "second"
      )
        return new Promise((r) => {
          release = r;
        });
      return standard(u, i);
    };
    await open();
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "policy" },
    });
    await screen.findByText(/当前 r1/);
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: "edit second" },
    });
    fireEvent.change(screen.getByLabelText("当前策略"), {
      target: { value: "second" },
    });
    expect(
      (screen.getByRole("button", { name: "创建策略" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(
      (screen.getByLabelText("策略名称") as HTMLInputElement).closest(
        "fieldset",
      )?.disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "创建策略" }));
    expect(postBodies).toHaveLength(0);
    await act(async () => release(response(second)));
    fireEvent.click(screen.getByRole("button", { name: "保存新修订" }));
    await screen.findByRole("region", { name: "历史操作回执" });
    expect(postBodies[0]).toMatchObject({
      policyId: "second",
      expectedRevision: 7,
    });
  });
});
describe("label wire contract", () => {
  it.each([
    "dataset",
    "observation",
    "policy",
    "evaluation",
    "comparison",
    "asset",
    "receipt",
  ])("accepts %s frozen DTO", (kind) => {
    const v = (
      {
        dataset,
        observation,
        policy,
        evaluation,
        comparison,
        asset,
        receipt,
      } as Record<string, unknown>
    )[kind];
    expect(parseLabelsResponse(kind, v)).toBe(v);
  });
  it.each([false, true, undefined])(
    "never interprets honeypot %s as unknown",
    (value) => {
      expect(() =>
        parseLabelsResponse("asset", { ...asset, isHoneypot: value }),
      ).toThrow(ApiError);
    },
  );
  it("rejects malformed paging and non-simulation", () => {
    expect(() =>
      parseLabelsResponse("dataset", { ...dataset, simulation: false }),
    ).toThrow();
    expect(() =>
      parseLabelsResponse("dataset", { ...page([dataset]), count: 0 }, true),
    ).toThrow();
  });
  it("rejects wrong domain response", async () => {
    handler = () =>
      response({
        ...observation,
        snapshot: {
          ...observation.snapshot,
          scope: { tenantId: "tenant", domainId: "other" },
        },
      });
    await expect(
      simulationLabelsAPI.read(
        "observation",
        { domainId: "domain", datasetId: "fixture", observationId: "obs1" },
        profile,
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("rejects wrong receipt operation and actor", async () => {
    handler = () => response(receipt);
    await expect(
      simulationLabelsAPI.read(
        "operation",
        { domainId: "domain", datasetId: "fixture", operationId: "different" },
        profile,
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
});
