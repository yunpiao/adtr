// Synthetic DOM/HTTP only; not browser, PostgreSQL or AD evidence.
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
  SimulationDetectionDomainWorkspace as Workspace,
} from "./SimulationDetectionWorkspace";
import { type Profile } from "./api";
import {
  parseDeclarations,
  parseDetectionResponse,
  detectionContract,
  detectionProvenance,
  detectionMapping,
} from "./simulation-detection-api";
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
const rule = {
  id: "rule-1",
  enabled: true,
  combine: "all",
  conditions: [{ field: "event.category", operator: "exists", values: [] }],
};
const template = {
  id: "template-a",
  name: "合成规则",
  version: 1,
  hash: "a".repeat(64),
  rules: [
    {
      ...rule,
      contract: detectionContract,
      provenance: detectionProvenance,
      version: 1,
      scope: { tenant: "tenant-a", domain: "domain-a" },
    },
  ],
  archived: false,
  simulation: true,
};
const dataset = {
  id: "dataset-a",
  fixture: "synthetic-a",
  fixtureHash: "b".repeat(64),
  contract: "adtr.securityevents.simulated.v1",
  simulation: true,
  eventCount: 1,
};
const task = {
  taskUUID: "task-a",
  taskName: "simulation.detection",
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
const result = {
  simulation: true,
  contract: detectionContract,
  mappingVersion: detectionMapping,
  templateId: template.id,
  templateVersion: 1,
  templateHash: template.hash,
  datasetId: dataset.id,
  fixtureHash: dataset.fixtureHash,
  eventCount: 1,
  matches: [],
};
const reply = (v: unknown, status = 200, actor = "1") =>
  new Response(JSON.stringify(v), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
  });
let fetcher: ReturnType<typeof vi.fn>;
function defaults(url: string) {
  if (url.includes("/datasets?"))
    return reply({ simulation: true, datasets: [dataset], total: 1 });
  if (url.includes("/templates?"))
    return reply({ simulation: true, templates: [template] });
  if (url.includes("/template?")) return reply({ simulation: true, template });
  throw Error(`Unexpected ${url}`);
}
const mount = (manage = true) =>
  render(
    <Workspace
      profile={profile}
      domainId="domain-a"
      sessionChanged={vi.fn()}
      permissions={{ read: true, manage }}
    />,
  );
async function open() {
  fireEvent.click(await screen.findByText("查看模板 合成规则"));
  await screen.findByText(/固定版本 1/);
}
async function selectRun() {
  await open();
  fireEvent.change(screen.getByLabelText("检测合成数据集"), {
    target: { value: dataset.id },
  });
  fireEvent.click(screen.getByText("手动运行模拟检测"));
}
beforeEach(() => {
  fetcher = vi.fn(async (url: string) => defaults(url));
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
describe("bounded declarations and provenance", () => {
  it("accepts supported declarations and rejects unbounded/extra/executable fields", () => {
    expect(parseDeclarations(JSON.stringify([rule]))).toHaveLength(1);
    for (const invalid of [
      [],
      [{ ...rule, script: "execute" }],
      [
        {
          ...rule,
          conditions: [{ field: "password", operator: "eq", values: ["x"] }],
        },
      ],
      [
        {
          ...rule,
          conditions: [
            { field: "actor.name", operator: "regex", values: [".*"] },
          ],
        },
      ],
      [rule, rule],
      [
        {
          ...rule,
          conditions: [
            { field: "actor.name", operator: "exists", values: ["x"] },
          ],
        },
      ],
    ])
      expect(() => parseDeclarations(JSON.stringify(invalid))).toThrow();
  });
  it("rejects wrong template contract and successful result provenance", () => {
    expect(() =>
      parseDetectionResponse("template", { simulation: true, template }),
    ).not.toThrow();
    expect(() =>
      parseDetectionResponse("template", {
        simulation: true,
        template: { ...template, simulation: false },
      }),
    ).toThrow();
    expect(() =>
      parseDetectionResponse("task", {
        simulation: true,
        task: { ...task, state: "succeeded", result },
      }),
    ).not.toThrow();
    expect(() =>
      parseDetectionResponse("task", {
        simulation: true,
        task: {
          ...task,
          state: "succeeded",
          result: { ...result, mappingVersion: "production" },
        },
      }),
    ).toThrow();
  });
});
describe("SIMULATED detection workspace", () => {
  it("does not infer permissions from admin profile", async () => {
    fetcher.mockImplementation(async (url: string) =>
      url.includes("/menu")
        ? reply({ menu: [{ mark: "domains", auth: { readable: true } }] })
        : reply({ results: Array(12).fill(true) }),
    );
    render(<Entry profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("当前账户未获得模拟检测与域选择读取权限。");
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).includes("/detection/datasets"),
      ),
    ).toBe(false);
  });
  it("loads explicit immutable dataset and readonly template without mutation controls", async () => {
    mount(false);
    await open();
    expect(screen.getByLabelText("检测规则 JSON")).toBeDisabled();
    expect(screen.getByText("手动运行模拟检测")).toBeDisabled();
    expect(screen.queryByText("新建模拟模板")).not.toBeInTheDocument();
  });
  it("pins selected version, exact query keys, CSRF, then displays server task result", async () => {
    fetcher.mockImplementation(async (url: string, init: RequestInit) => {
      if (url.endsWith("/run")) {
        const input = JSON.parse(String(init.body));
        expect(input.templateVersion).toBe(1);
        expect(input.datasetId).toBe(dataset.id);
        expect(init.headers).toEqual({
          "Content-Type": "application/json",
          "X-CSRF-Token": "csrf",
        });
        return reply({
          operationId: input.operationId,
          kind: "run",
          submission: { task, replayed: false },
        });
      }
      if (url.includes("/task?")) {
        expect([...new URL(url, "http://test").searchParams.keys()]).toEqual([
          "domainId",
          "taskUUID",
        ]);
        return reply({
          simulation: true,
          task: {
            ...task,
            state: "succeeded",
            progress: 100,
            resultVersion: 1,
            result,
          },
        });
      }
      return defaults(url);
    });
    mount();
    await selectRun();
    await screen.findByText("SIMULATED · 等待执行");
    fireEvent.click(screen.getByText("刷新检测任务"));
    expect(
      await screen.findByLabelText("SIMULATED 检测结果"),
    ).toHaveTextContent(detectionMapping);
  });
  it("retains original operation and immutable payload after unknown response, suppresses repeated clicks", async () => {
    let posted: unknown;
    let n = 0;
    fetcher.mockImplementation(async (url: string, init: RequestInit) => {
      if (url.endsWith("/run")) {
        const input = JSON.parse(String(init.body));
        if (n++ === 0) {
          posted = input;
          throw Error("lost response");
        }
        expect(input).toEqual(posted);
        return reply({
          operationId: input.operationId,
          kind: "run",
          submission: { task, replayed: true },
        });
      }
      return defaults(url);
    });
    mount();
    await selectRun();
    fireEvent.click(screen.getByText("手动运行模拟检测"));
    await screen.findByText(/请求未取得可确认结果/);
    expect(n).toBe(1);
    expect(screen.getByLabelText("检测合成数据集")).toBeDisabled();
    fireEvent.click(screen.getByText("使用原操作 ID 重试"));
    await screen.findByText("SIMULATED · 等待执行");
    expect(n).toBe(2);
  });
  it("recovers missing receipt with same ID instead of clearing domain", async () => {
    fetcher.mockImplementation(async (url: string) => {
      if (url.endsWith("/run")) throw Error("lost");
      if (url.includes("/receipt?"))
        return reply({ error: "operation_not_found" }, 404);
      return defaults(url);
    });
    mount();
    await selectRun();
    await screen.findByText(/请求未取得可确认结果/);
    fireEvent.click(screen.getByText("核验原操作"));
    await screen.findByText(/服务器尚无原操作回执/);
    expect(screen.getByText("使用原操作 ID 重试")).toBeEnabled();
  });
  it("clears sensitive state after scope404/revocation", async () => {
    fetcher.mockImplementation(async (url: string) =>
      url.includes("/template?")
        ? reply({ error: "not_found" }, 404)
        : defaults(url),
    );
    mount();
    fireEvent.click(await screen.findByText("查看模板 合成规则"));
    await screen.findByText(/已清除模板、任务和回执/);
    expect(screen.queryByText("查看模板 合成规则")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("检测合成数据集")).not.toBeInTheDocument();
  });
  it("rejects exact actor mismatch", async () => {
    fetcher.mockImplementation(async () =>
      reply({ simulation: true, datasets: [dataset], total: 1 }, 200, "01"),
    );
    mount();
    await screen.findByText(/已清除模板、任务和回执/);
    expect(screen.queryByText("查看模板 合成规则")).not.toBeInTheDocument();
  });
  it("ignores late old-route reads after domain remount", async () => {
    let resolve!: (r: Response) => void;
    fetcher.mockImplementation(async (url: string) =>
      url.includes("/templates?") && url.includes("domain-a")
        ? new Promise<Response>((r) => (resolve = r))
        : url.includes("domain-b")
          ? url.includes("/datasets?")
            ? reply({ simulation: true, datasets: [], total: 0 })
            : reply({ simulation: true, templates: [] })
          : defaults(url),
    );
    const view = mount();
    await waitFor(() => expect(resolve).toBeDefined());
    view.rerender(
      <Workspace
        profile={profile}
        domainId="domain-b"
        sessionChanged={vi.fn()}
        permissions={{ read: true, manage: true }}
      />,
    );
    await act(async () =>
      resolve(reply({ simulation: true, templates: [template] })),
    );
    expect(screen.queryByText("查看模板 合成规则")).not.toBeInTheDocument();
  });
  it("rejects client invalid JSON without posting", async () => {
    mount();
    await open();
    fireEvent.change(screen.getByLabelText("检测规则 JSON"), {
      target: { value: "bad" },
    });
    fireEvent.click(screen.getByText("保存模板新版本"));
    await screen.findByText("规则必须是有效 JSON 数组。");
    expect(
      fetcher.mock.calls.some(([url]) => String(url).endsWith("/update")),
    ).toBe(false);
  });
  it("restores run receipt after reload using explicit operation ID", async () => {
    fetcher.mockImplementation(async (url: string) =>
      url.includes("/receipt?")
        ? reply({
            operationId: "original-op",
            kind: "run",
            submission: { task, replayed: true },
          })
        : defaults(url),
    );
    mount();
    await screen.findByText("查看模板 合成规则");
    fireEvent.change(screen.getByLabelText("恢复操作 ID"), {
      target: { value: "original-op" },
    });
    fireEvent.click(screen.getByText("恢复服务器回执"));
    await screen.findByText("SIMULATED · 等待执行");
  });
});

describe("mutation lifecycle boundaries", () => {
  it("creates, versions, and archives only server-confirmed templates", async () => {
    fetcher.mockImplementation(async (url: string, init: RequestInit) => {
      for (const kind of ["create", "update", "archive"])
        if (url.endsWith(`/${kind}`)) {
          const input = JSON.parse(String(init.body));
          if (kind !== "create")
            expect(input.expectedVersion).toBe(kind === "update" ? 1 : 2);
          return reply({
            operationId: input.operationId,
            kind,
            template: {
              ...template,
              id: input.templateId,
              name: input.name ?? "new",
              version: kind === "create" ? 1 : 2,
              rules: template.rules.map((r) => ({
                ...r,
                version: kind === "create" ? 1 : 2,
              })),
              archived: kind === "archive",
            },
          });
        }
      return defaults(url);
    });
    mount();
    await screen.findByText("查看模板 合成规则");
    fireEvent.click(screen.getByText("新建模拟模板"));
    fireEvent.change(screen.getByLabelText("模板 ID"), {
      target: { value: "new-template" },
    });
    fireEvent.change(screen.getByLabelText("模板名称"), {
      target: { value: "new" },
    });
    fireEvent.click(screen.getByText("保存新模板"));
    await screen.findByText(/已确认 create/);
    fireEvent.click(screen.getByText("保存模板新版本"));
    await screen.findByText(/固定版本 2/);
    fireEvent.click(screen.getByText("归档模拟模板"));
    await screen.findByText(/已确认 archive/);
    expect(screen.getByText("手动运行模拟检测")).toBeDisabled();
  });
  it("cancels through server receipt without claiming immediate completion", async () => {
    fetcher.mockImplementation(async (url: string, init: RequestInit) => {
      if (url.endsWith("/run") || url.endsWith("/cancel")) {
        const input = JSON.parse(String(init.body));
        return reply(
          url.endsWith("/run")
            ? {
                operationId: input.operationId,
                kind: "run",
                submission: { task, replayed: false },
              }
            : {
                operationId: input.operationId,
                kind: "cancel",
                task: { ...task, state: "cancel_requested" },
              },
        );
      }
      return defaults(url);
    });
    mount();
    await selectRun();
    await screen.findByText("SIMULATED · 等待执行");
    fireEvent.click(screen.getByText("取消检测任务"));
    await screen.findByText("SIMULATED · 已请求取消，待确认");
    expect(
      screen.queryByLabelText("SIMULATED 检测结果"),
    ).not.toBeInTheDocument();
  });
  it("rejects terminal result from a different template version", async () => {
    fetcher.mockImplementation(async (url: string, init: RequestInit) => {
      if (url.endsWith("/run")) {
        const input = JSON.parse(String(init.body));
        return reply({
          operationId: input.operationId,
          kind: "run",
          submission: { task, replayed: false },
        });
      }
      if (url.includes("/task?"))
        return reply({
          simulation: true,
          task: {
            ...task,
            state: "succeeded",
            result: { ...result, templateVersion: 2 },
          },
        });
      return defaults(url);
    });
    mount();
    await selectRun();
    await screen.findByText("SIMULATED · 等待执行");
    fireEvent.click(screen.getByText("刷新检测任务"));
    await screen.findByText(/invalid_response/);
    expect(
      screen.queryByLabelText("SIMULATED 检测结果"),
    ).not.toBeInTheDocument();
  });
  it("clears data on session hint and rejects late write completion", async () => {
    let complete!: (r: Response) => void;
    let operationId = "";
    fetcher.mockImplementation(async (url: string, init: RequestInit) => {
      if (url.endsWith("/run")) {
        operationId = JSON.parse(String(init.body)).operationId;
        return new Promise<Response>((r) => (complete = r));
      }
      return defaults(url);
    });
    mount();
    await selectRun();
    await waitFor(() => expect(complete).toBeDefined());
    act(() =>
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "adtr.session-invalidation.v1",
          newValue: "a".repeat(32),
          storageArea: window.localStorage,
        }),
      ),
    );
    await act(async () =>
      complete(
        reply({
          operationId,
          kind: "run",
          submission: { task, replayed: false },
        }),
      ),
    );
    expect(screen.queryByText("SIMULATED · 等待执行")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("检测合成数据集")).not.toBeInTheDocument();
  });
});
