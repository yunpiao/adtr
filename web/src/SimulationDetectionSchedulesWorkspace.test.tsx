// Synthetic DOM/HTTP only; not PostgreSQL, browser or real AD acceptance.
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
  SimulationDetectionSchedulesDomainWorkspace as Workspace,
} from "./SimulationDetectionSchedulesWorkspace";
import { type Profile } from "./api";
import {
  detectionContract,
  detectionProvenance,
  detectionMapping,
} from "./simulation-detection-api";
import {
  parseScheduleResponse,
  simulationDetectionSchedulesAPI as api,
} from "./simulation-detection-schedules-api";
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
const template = {
  id: "template-a",
  name: "合成规则",
  version: 1,
  hash: "a".repeat(64),
  rules: [
    {
      id: "rule-1",
      enabled: true,
      combine: "all",
      conditions: [{ field: "event.category", operator: "exists", values: [] }],
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
const schedule = {
  id: "schedule-a",
  domainId: "domain-a",
  revision: 1,
  controlVersion: 1,
  state: "paused",
  error: "",
  label: "每日合成",
  cycle: 0,
  templateId: template.id,
  templateVersion: 1,
  datasetId: dataset.id,
  pinned: {
    templateId: template.id,
    templateVersion: 1,
    templateHash: template.hash,
    datasetId: dataset.id,
    fixtureHash: dataset.fixtureHash,
    mappingVersion: detectionMapping,
  },
  anchor: "2026-01-31T12:00:00Z",
  nextIndex: 1,
  nextAt: "2026-02-01T12:00:00Z",
  createdAt: "2026-01-31T12:00:00Z",
  updatedAt: "2026-01-31T12:00:00Z",
  simulation: true,
};
const reply = (v: unknown, status = 200, actor = "1") =>
  new Response(JSON.stringify(v), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
  });
const page = { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 };
const change = (
  operationId: string,
  current = { ...schedule },
  replayed = false,
) => ({
  receipt: {
    operationId,
    action: "created",
    scheduleId: schedule.id,
    controlVersion: 1,
    revision: 1,
    state: "paused",
  },
  schedule: current,
  replayed,
});
let fetcher: ReturnType<typeof vi.fn>;
function defaults(url: string) {
  if (url.includes("/datasets?"))
    return reply({ simulation: true, datasets: [dataset], total: 1 });
  if (url.includes("/templates?"))
    return reply({ simulation: true, templates: [template] });
  if (url.includes("/list?")) return reply({ schedules: [schedule], page });
  if (url.includes("/detail?"))
    return reply({ schedule, events: [], hasMore: false });
  throw Error("unexpected " + url);
}
beforeEach(() => {
  fetcher = vi.fn((u: string) => Promise.resolve(defaults(u)));
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const mount = () =>
  render(
    <Workspace
      profile={profile}
      domainId="domain-a"
      permissions={{ read: true, manage: true }}
      sessionChanged={vi.fn()}
    />,
  );
async function ready() {
  await screen.findByRole("button", { name: schedule.label });
}
function fill() {
  fireEvent.change(screen.getByLabelText("计划名称"), {
    target: { value: "新计划" },
  });
  fireEvent.change(screen.getByLabelText("检测模板"), {
    target: { value: template.id },
  });
  fireEvent.change(screen.getByLabelText("合成数据集"), {
    target: { value: dataset.id },
  });
}
describe("scheduled simulation workspace", () => {
  it("defaults deny even privileged profile; no fetch without explicit scoped grants", () => {
    render(
      <Workspace
        profile={profile}
        domainId="domain-a"
        sessionChanged={vi.fn()}
      />,
    );
    expect(fetcher).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("权限未确认");
  });
  it("entry requires both grants and exact route checks", async () => {
    fetcher.mockImplementation((u: string) =>
      Promise.resolve(
        u.endsWith("/menu")
          ? reply({
              menu: [
                { mark: "domains", auth: { readable: true } },
                { mark: "simulation_detection", auth: { readable: true } },
              ],
            })
          : reply({ results: Array(13).fill(true) }),
      ),
    );
    render(<Entry profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("当前账户未获得模拟定时检测与域选择读取权限。");
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it.each([0, 1, 2])(
    "creates paused cycle %i and sends pinned version; no Rate field",
    async (cycle) => {
      fetcher.mockImplementation((u: string, o?: RequestInit) => {
        if (u.endsWith("/create"))
          return Promise.resolve(
            reply(change(JSON.parse(String(o?.body)).operationId)),
          );
        return Promise.resolve(defaults(u));
      });
      mount();
      await ready();
      fill();
      fireEvent.change(screen.getByLabelText("UTC 周期"), {
        target: { value: String(cycle) },
      });
      fireEvent.click(screen.getByRole("button", { name: "创建暂停计划" }));
      await screen.findByText("操作已保存；已重新读取当前状态。");
      const call = fetcher.mock.calls.find((c) =>
        String(c[0]).endsWith("/create"),
      )!;
      expect(JSON.parse(call[1].body)).toEqual({
        domainId: "domain-a",
        operationId: expect.any(String),
        label: "新计划",
        cycle,
        templateId: "template-a",
        templateVersion: 1,
        datasetId: "dataset-a",
      });
      expect(call[1].headers["X-CSRF-Token"]).toBe("csrf");
      expect(screen.queryByLabelText(/Rate/)).not.toBeInTheDocument();
    },
  );
  it("edits with current CAS version and explicit selected template", async () => {
    fetcher.mockImplementation((u: string, o?: RequestInit) =>
      u.endsWith("/update")
        ? Promise.resolve(
            reply({
              ...change(JSON.parse(String(o?.body)).operationId),
              receipt: {
                ...change("x").receipt,
                operationId: JSON.parse(String(o?.body)).operationId,
                action: "edited",
              },
            }),
          )
        : Promise.resolve(defaults(u)),
    );
    mount();
    await ready();
    fireEvent.click(screen.getByRole("button", { name: schedule.label }));
    await screen.findByRole("button", { name: "编辑计划" });
    fireEvent.click(screen.getByRole("button", { name: "编辑计划" }));
    fireEvent.change(screen.getByLabelText("UTC 周期"), {
      target: { value: "2" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存新修订并暂停" }));
    await screen.findByText("操作已保存；已重新读取当前状态。");
    const body = JSON.parse(
      fetcher.mock.calls.find((c) => String(c[0]).endsWith("/update"))![1].body,
    );
    expect(body).toMatchObject({
      id: "schedule-a",
      expectedControlVersion: 1,
      cycle: 2,
    });
  });
  it("lost response queries receipt then renders current truth rather than historical receipt", async () => {
    let op = "";
    fetcher.mockImplementation((u: string, o?: RequestInit) => {
      if (u.endsWith("/create")) {
        op = JSON.parse(String(o?.body)).operationId;
        return Promise.reject(Error("lost"));
      }
      if (u.includes("/receipt?"))
        return Promise.resolve(
          reply(
            change(
              op,
              { ...schedule, state: "archived", controlVersion: 4 },
              true,
            ),
          ),
        );
      if (u.includes("/detail?"))
        return Promise.resolve(
          reply({
            schedule: { ...schedule, state: "archived", controlVersion: 4 },
            events: [],
            hasMore: false,
          }),
        );
      return Promise.resolve(defaults(u));
    });
    mount();
    await ready();
    fill();
    fireEvent.click(screen.getByRole("button", { name: "创建暂停计划" }));
    await screen.findByRole("alert");
    expect(screen.getByRole("button", { name: "创建暂停计划" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "核对回执并恢复" }));
    await screen.findByText(
      "已恢复原操作；下方显示当前状态，不以历史回执覆盖。",
    );
    expect(
      screen.getByRole("heading", { name: "每日合成 · 已归档" }),
    ).toBeInTheDocument();
    expect(
      fetcher.mock.calls.filter((c) => String(c[0]).endsWith("/create")),
    ).toHaveLength(1);
  });
  it("receipt not found safely replays exact same operation once", async () => {
    let calls = 0;
    fetcher.mockImplementation((u: string, o?: RequestInit) => {
      if (u.endsWith("/create")) {
        calls++;
        return calls === 1
          ? Promise.reject(Error("lost"))
          : Promise.resolve(
              reply(change(JSON.parse(String(o?.body)).operationId)),
            );
      }
      if (u.includes("/receipt?"))
        return Promise.resolve(reply({ error: "operation_not_found" }, 404));
      return Promise.resolve(defaults(u));
    });
    mount();
    await ready();
    fill();
    fireEvent.click(screen.getByRole("button", { name: "创建暂停计划" }));
    await screen.findByRole("alert");
    fireEvent.click(screen.getByRole("button", { name: "核对回执并恢复" }));
    await screen.findByText("操作已保存；已重新读取当前状态。");
    const callsMade = fetcher.mock.calls.filter((c) =>
      String(c[0]).endsWith("/create"),
    );
    expect(callsMade).toHaveLength(2);
    expect(callsMade[0][1].body).toBe(callsMade[1][1].body);
  });
  it("clears scope immediately and ignores late old actor/domain reads", async () => {
    let resolve!: (r: Response) => void;
    fetcher.mockImplementation((u: string) =>
      u.includes("/list?")
        ? new Promise<Response>((r) => {
            resolve = r;
          })
        : Promise.resolve(defaults(u)),
    );
    const view = mount();
    view.rerender(
      <Workspace
        profile={{ ...profile, ID: 2 }}
        domainId="domain-b"
        permissions={{ read: false, manage: false }}
        sessionChanged={vi.fn()}
      />,
    );
    await act(async () => resolve(reply({ schedules: [schedule], page })));
    expect(screen.queryByText(schedule.label)).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("权限未确认");
  });
  it("fails closed on response actor mismatch", async () => {
    fetcher.mockImplementation((u: string) =>
      u.includes("/list?")
        ? Promise.resolve(reply({ schedules: [schedule], page }, 200, "2"))
        : Promise.resolve(defaults(u)),
    );
    const changed = vi.fn();
    render(
      <Workspace
        profile={profile}
        domainId="domain-a"
        permissions={{ read: true, manage: true }}
        sessionChanged={changed}
      />,
    );
    await waitFor(() => expect(changed).toHaveBeenCalled());
    expect(screen.queryByText(schedule.label)).not.toBeInTheDocument();
  });
  it("shows skipped and configuration blocked distinctly", async () => {
    fetcher.mockImplementation((u: string) =>
      u.includes("/detail?")
        ? Promise.resolve(
            reply({
              schedule: {
                ...schedule,
                state: "configuration_blocked",
                error: "configuration_changed",
              },
              events: [
                {
                  id: 1,
                  revision: 1,
                  action: "skipped_misfire",
                  firstIndex: 1,
                  lastIndex: 4,
                  createdAt: schedule.createdAt,
                },
              ],
              hasMore: false,
            }),
          )
        : Promise.resolve(defaults(u)),
    );
    mount();
    await ready();
    fireEvent.click(screen.getByRole("button", { name: schedule.label }));
    await screen.findByText(/固定模板版本已变化/);
    expect(screen.getByText(/错过时窗，已跳过/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "启用计划" })).toBeDisabled();
  });
});
describe("schedule controls and recovery boundaries", () => {
  it.each([
    ["enable", "启用计划", "paused"],
    ["pause", "暂停计划", "enabled"],
    ["archive", "归档计划", "paused"],
  ])("controls %s with CAS", async (kind, button, state) => {
    fetcher.mockImplementation((u: string, o?: RequestInit) => {
      if (u.endsWith("/" + kind)) {
        const b = JSON.parse(String(o?.body));
        return Promise.resolve(
          reply({
            ...change(b.operationId),
            receipt: {
              ...change(b.operationId).receipt,
              action:
                kind === "enable"
                  ? "enabled"
                  : kind === "pause"
                    ? "paused"
                    : "archived",
            },
          }),
        );
      }
      if (u.includes("/detail?"))
        return Promise.resolve(
          reply({
            schedule: { ...schedule, state },
            events: [],
            hasMore: false,
          }),
        );
      return Promise.resolve(defaults(u));
    });
    mount();
    await ready();
    fireEvent.click(screen.getByRole("button", { name: schedule.label }));
    await screen.findByRole("button", { name: button });
    fireEvent.click(screen.getByRole("button", { name: button }));
    await screen.findByText("操作已保存；已重新读取当前状态。");
    expect(
      JSON.parse(
        fetcher.mock.calls.find((c) => String(c[0]).endsWith("/" + kind))![1]
          .body,
      ),
    ).toEqual({
      domainId: "domain-a",
      id: "schedule-a",
      expectedControlVersion: 1,
      operationId: expect.any(String),
    });
  });
  it("CAS rejection does not replay writes and requires refresh", async () => {
    fetcher.mockImplementation((u: string) =>
      u.endsWith("/enable")
        ? Promise.resolve(reply({ error: "revision_conflict" }, 409))
        : Promise.resolve(defaults(u)),
    );
    mount();
    await ready();
    fireEvent.click(screen.getByRole("button", { name: schedule.label }));
    await screen.findByRole("button", { name: "启用计划" });
    fireEvent.click(screen.getByRole("button", { name: "启用计划" }));
    await screen.findByText("版本已变化：刷新当前状态后重新编辑。");
    expect(
      fetcher.mock.calls.filter((c) => String(c[0]).endsWith("/enable")),
    ).toHaveLength(1);
  });
  it("403 on refresh clears previous data", async () => {
    mount();
    await ready();
    fetcher.mockImplementation(() =>
      Promise.resolve(reply({ error: "forbidden" }, 403)),
    );
    fireEvent.click(screen.getByRole("button", { name: "刷新当前状态" }));
    await screen.findByText(
      "当前域的模拟检测及定时检测权限未确认或已失效；请重新进入并核对权限。",
    );
    expect(
      screen.queryByRole("button", { name: schedule.label }),
    ).not.toBeInTheDocument();
  });
  it("history task failure is explicit, not rendered as success", async () => {
    const task = {
      taskUUID: "task-a",
      taskName: "simulation.detection.scheduled",
      domainId: "domain-a",
      payloadVersion: 1,
      state: "failed",
      sourceState: "",
      error: "configuration_changed",
      createdAt: "",
      updatedAt: "",
      attempt: 1,
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
    fetcher.mockImplementation((u: string) =>
      u.includes("/detail?")
        ? Promise.resolve(
            reply({
              schedule,
              events: [
                {
                  id: 1,
                  revision: 1,
                  action: "admitted",
                  taskId: "task-a",
                  createdAt: schedule.createdAt,
                },
              ],
              hasMore: false,
            }),
          )
        : u.includes("/task?")
          ? Promise.resolve(reply({ simulation: true, task }))
          : Promise.resolve(defaults(u)),
    );
    mount();
    await ready();
    fireEvent.click(screen.getByRole("button", { name: schedule.label }));
    fireEvent.click(
      await screen.findByRole("button", { name: "查看执行 task-a" }),
    );
    await screen.findByText(
      "此发生执行失败，已消耗本次机会；未来周期是新的发生，不是重试。",
    );
    expect(
      screen.queryByText("合成评估成功，不代表真实 AD 产品验收。"),
    ).not.toBeInTheDocument();
  });
  it("readonly users cannot mutate", async () => {
    render(
      <Workspace
        profile={profile}
        domainId="domain-a"
        permissions={{ read: true, manage: false }}
        sessionChanged={vi.fn()}
      />,
    );
    await ready();
    expect(screen.getByRole("button", { name: "创建暂停计划" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: schedule.label }));
    expect(
      await screen.findByRole("button", { name: "启用计划" }),
    ).toBeDisabled();
  });
});
describe("schedule wire validation", () => {
  it.each(["enabled", "paused", "archived", "created", "edited"])(
    "accepts actual backend receipt action %s",
    (action) =>
      expect(() =>
        parseScheduleResponse("change", {
          ...change("op"),
          receipt: { ...change("op").receipt, action },
        }),
      ).not.toThrow(),
  );
  it.each([
    { cycle: 3 },
    { simulation: false },
    { controlVersion: 0 },
    { nextAt: "not-date" },
    { state: "unknown" },
    { pinned: { ...schedule.pinned, templateVersion: 2 } },
  ])("rejects malformed schedule %j", (patch) =>
    expect(() =>
      parseScheduleResponse("detail", {
        schedule: { ...schedule, ...patch },
        events: [],
        hasMore: false,
      }),
    ).toThrow(),
  );
  it("rejects cross-domain responses", async () => {
    fetcher.mockResolvedValue(reply({ schedules: [schedule], page }));
    await expect(
      api.list("domain-b", 1, profile, new AbortController().signal),
    ).rejects.toThrow();
  });
});
