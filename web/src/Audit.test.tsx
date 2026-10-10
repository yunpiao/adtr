import { revealNavigation } from "./test-navigation";
// Synthetic DOM/transport unit fixtures only. These tests do not replace a real
// browser -> authenticated API -> PostgreSQL -> worker -> XLSX acceptance run.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import AuditWorkspace, { discardAuditIntent } from "./AuditWorkspace";
import { ApiError, type Profile } from "./api";
import { labels, marks, type Permission } from "./access-api";
import {
  auditAPI,
  auditColumns,
  auditOperations,
  auditQuery,
  emptyFilter,
  type AuditFilter,
  type AuditRow,
  type ExportInput,
} from "./audit-api";
import { auditIntent } from "./audit-intent";
import { type Task, type TaskState } from "./task-api";

const taskUUID = "00000000-0000-4000-8000-000000000011";
const password = "Synthetic Actor Password 123";
const xlsxType =
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
const columnLabels = [
  "用户ID",
  "登录用户",
  "登录IP",
  "审计类型",
  "事件",
  "事件参数",
  "事件结果",
  "审计时间",
];
const catalogue = () => ({
  columns: auditColumns.map((prop, i) => ({ prop, label: columnLabels[i] })),
  defaultColumns: [...auditColumns],
  maxColumns: 8,
});
const row = (changes: Partial<AuditRow> = {}): AuditRow => ({
  ID: "auth.1",
  loginUser: "synthetic-user",
  sourceIp: "192.0.2.10",
  event: "login",
  eventArgs: '{"synthetic":true}',
  eventResult: "SUCCESS",
  CreateTm: "2026-10-07T00:00:00Z",
  logType: 9,
  logTypeName: "系统设置",
  userId: 3,
  source: "auth",
  domainId: null,
  availability: {
    loginUser: true,
    sourceIp: true,
    path: true,
    requestId: true,
    eventResult: true,
  },
  deleted: false,
  deletable: true,
  visibilityVersion: 0,
  ...changes,
});
const task = (
  state: TaskState = "queued",
  changes: Partial<Task> = {},
): Task => ({
  taskUUID,
  taskName: "audit.export",
  domainId: "platform",
  payloadVersion: 1,
  state,
  sourceState: state === "succeeded" ? "SUCCESS" : "PENDING",
  error: "",
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:01Z",
  attempt: 0,
  maxAttempts: 5,
  progress: state === "succeeded" ? 100 : 0,
  resultVersion: 0,
  result: null,
  cursor: null,
  parentTaskUUID: "",
  terminalAt: null,
  archived: false,
  visibilityVersion: 0,
  ...changes,
});
const list = (rows = [row()]) => ({
  page: {
    pageIdx: 1,
    pageSize: 20,
    total: rows.length,
    totalPage: rows.length ? 1 : 0,
  },
  List: rows,
  exhausted: true,
});
const detail = (
  state: TaskState = "succeeded",
  extra: Record<string, unknown> = {},
) => ({
  task: task(state),
  downloadReady: state === "succeeded",
  ...(state === "succeeded"
    ? {
        rowCount: 1,
        snapshotAt: "2026-10-07T00:00:01Z",
        downloadPath: "/api/audit/exports/download",
      }
    : {}),
  ...extra,
});
const metadata = (): Permission[] =>
  marks.map((mark) => ({
    mark,
    name: labels[mark],
    auth: { readable: true, writeable: true },
    allow_auth: { readable: true, writeable: true },
    paths: [],
    children: [],
    checked: true,
    icon: mark,
  }));
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "X-ADTR-User-ID": String(profile.ID) }),
    json: async () => body,
  } as Response);
const binary = (type = xlsxType, size = 16, length?: string) =>
  Promise.resolve({
    ok: true,
    status: 200,
    headers: new Headers({
      "X-ADTR-User-ID": String(profile.ID),
      "Content-Disposition": `attachment; filename="audit-${taskUUID}.xlsx"`,
      "Content-Type": type,
      ...(length ? { "Content-Length": length } : {}),
    }),
    blob: async () => ({ size, type }) as Blob,
  } as Response);
const input = (): ExportInput => ({
  ...emptyFilter(),
  selectColumn: ["event", "CreateTm"],
  idempotencyKey: taskUUID,
});
const signal = () => new AbortController().signal;
let profile: Profile;
let fetcher: ReturnType<typeof vi.fn>;
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
let counter = 0;
beforeEach(() => {
  discardAuditIntent({ ID: 1, username: "admin" });
  localStorage.clear();
  sessionStorage.clear();
  profile = {
    ID: 1,
    username: "admin",
    role: "platform_admin",
    priv: 1,
    mobile: "",
    email: "",
    remark: "",
    passStrength: "high",
    hasMfa: true,
    needChangePwd: false,
    isExpired: false,
    passwordNotUpdatedDays: 0,
    pwdUpdateTm: "2026-10-01T00:00:00Z",
    csrfToken: `audit-test-${counter++}`,
  };
  window.history.replaceState({}, "", "#account");
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu: metadata() });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/audit") return response(list());
    if (path === "/api/audit/types")
      return response({
        events: ["login", "resource.update"],
        List: [
          {
            logType: 7,
            logTypeName: "资产",
            eventNameList: ["resource.update"],
          },
          { logType: 9, logTypeName: "系统设置", eventNameList: ["login"] },
        ],
      });
    if (path === "/api/audit/columns") return response(catalogue());
    if (path === "/api/audit/exports")
      return response({ task: task(), taskUUID, replayed: false });
    if (path === "/api/audit/exports/history")
      return response({
        page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
        list: [],
        exhausted: true,
      });
    if (path === "/api/audit/exports/detail") return response(detail());
    if (path === "/api/audit/exports/download") return binary();
    if (path === "/api/audit/delete" || path === "/api/audit/restore")
      return response({ result: "success", changed: 1, visibilityRevision: 1 });
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  discardAuditIntent();
  vi.useRealTimers();
});
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const proof = (code = "123456") => {
  fill("操作者当前密码", password);
  fill("未使用的认证器验证码", code);
};
const calls = (path: string) =>
  fetcher.mock.calls.filter(
    ([url]) => url.split("?")[0] === `/api/audit${path}`,
  );
const body = (path: string, index = -1) =>
  JSON.parse(calls(path).at(index)![1].body as string);
const choose = (label: string, values: string[]) => {
  const select = screen.getByLabelText(label, {
    exact: true,
  }) as HTMLSelectElement;
  for (const option of select.options)
    option.selected = values.includes(option.value);
  fireEvent.change(select);
};
async function start(changed = vi.fn()) {
  let mounted!: ReturnType<typeof render>;
  // The permission read mounts the list/type/history readers in a subsequent
  // effect. Settle that immediate mocked promise chain before querying the
  // resulting table; a synchronous render only flushes the first mount.
  await act(async () => {
    mounted = render(
      <AuditWorkspace profile={profile} sessionChanged={changed} />,
    );
  });
  await screen.findByRole("table", { name: "操作审计记录" });
  await waitFor(() =>
    expect(
      screen.getByLabelText("审计类型筛选", { exact: true }),
    ).toBeEnabled(),
  );
  return mounted;
}
async function exportForm() {
  await start();
  click("当前筛选导出");
  await screen.findByLabelText("操作者当前密码");
}
async function exportDetail() {
  await start();
  fill("导出任务 ID", taskUUID);
  click("查看导出任务");
  await screen.findByLabelText("导出实际结果");
}
function selectRow(id = "auth.1") {
  fireEvent.click(screen.getByRole("checkbox", { name: `选择 ${id}` }));
}
const selectColumn = (label: string) =>
  fireEvent.click(screen.getByRole("checkbox", { name: `导出列：${label}` }));
function storedText() {
  return `${JSON.stringify({ ...localStorage })}${JSON.stringify({ ...sessionStorage })}`;
}

describe("audit transport unit contracts", () => {
  it("encodes repeated arrays and exact UTC half-open bounds without comma joining", () => {
    const filter: AuditFilter = {
      startTm: "2026-10-06T12:34:56.000Z",
      endTm: "2026-10-07T12:34:57.000Z",
      keyword: "alice + 192.0.2",
      filterEvent: ["login", "resource.update"],
      logTypeList: [7, 9],
      createSort: 1,
      visibility: "all",
    };
    expect(auditQuery(filter, 2, 50)).toBe(
      "pageIdx=2&pageSize=50&createSort=1&visibility=all&startTm=2026-10-06T12%3A34%3A56.000Z&endTm=2026-10-07T12%3A34%3A57.000Z&keyword=alice+%2B+192.0.2&filterEvent=login&filterEvent=resource.update&logTypeList=7&logTypeList=9",
    );
  });
  it("accepts explicit historic nulls but rejects malformed row and pagination schemas", async () => {
    override = () =>
      response(list([row({ loginUser: null, sourceIp: null, userId: null })]));
    expect((await auditAPI.list("", signal())).List[0].loginUser).toBeNull();
    for (const invalid of [
      { ...list(), List: [row({ ID: "audit.0" })] },
      { ...list(), List: [row({ source: "task" })] },
      { ...list(), List: [{ ...row(), loginUser: undefined }] },
      { ...list(), List: [{ ...row(), availability: {} }] },
      { ...list(), List: [row({ visibilityVersion: -1 })] },
      { ...list(), page: { ...list().page, pageSize: 101 } },
      { ...list(), exhausted: "yes" },
    ]) {
      override = () => response(invalid);
      await expect(auditAPI.list("", signal())).rejects.toMatchObject({
        code: "invalid_response",
      });
    }
  });
  it("parses domain-scoped operation-account audit IDs without admitting mismatched sources", async () => {
    override = () =>
      response(
        list([
          row({
            ID: "operation_account.7",
            source: "operation_account",
            domainId: "granted-domain",
            deletable: false,
          }),
        ]),
      );
    expect((await auditAPI.list("", signal())).List[0]).toMatchObject({
      source: "operation_account",
      domainId: "granted-domain",
      deletable: false,
    });
    override = () =>
      response(list([row({ ID: "operation_account.7", source: "domain" })]));
    await expect(auditAPI.list("", signal())).rejects.toMatchObject({
      code: "invalid_response",
    });
  });
  it.each(["credential_use", "operational_log"] as const)(
    "parses protected %s source and rejects an ID/source mismatch",
    async (source) => {
      override = () =>
        response(
          list([
            row({
              ID: `${source}.8`,
              source,
              domainId: "granted-domain",
              deletable: false,
            }),
          ]),
        );
      expect((await auditAPI.list("", signal())).List[0]).toMatchObject({
        source,
        domainId: "granted-domain",
        deletable: false,
      });
      override = () =>
        response(
          list([row({ ID: `${source}.8`, source: "operation_account" })]),
        );
      await expect(auditAPI.list("", signal())).rejects.toMatchObject({
        code: "invalid_response",
      });
    },
  );
  it("rejects unknown, duplicate, missing, or out-of-contract server columns", async () => {
    for (const invalid of [
      { ...catalogue(), maxColumns: 9 },
      { ...catalogue(), columns: catalogue().columns.slice(1) },
      {
        ...catalogue(),
        columns: catalogue().columns.map((c, i) =>
          i === 0 ? { ...c, prop: "password" } : c,
        ),
      },
      {
        ...catalogue(),
        columns: catalogue().columns.map((c, i, all) => (i === 0 ? all[1] : c)),
      },
      { ...catalogue(), defaultColumns: [] },
      { ...catalogue(), defaultColumns: ["event", "event"] },
      { ...catalogue(), defaultColumns: ["password"] },
    ]) {
      override = () => response(invalid);
      await expect(auditAPI.columns(signal())).rejects.toMatchObject({
        code: "invalid_response",
      });
    }
  });
  it("sends only frozen export body fields with same-origin cookie/CSRF, never client identity", async () => {
    const requestSignal = signal();
    await auditAPI.submit(
      {
        ...input(),
        actorId: 77,
        downloadPath: "https://example.invalid/leak",
        pageSize: -1,
      } as ExportInput,
      { actorPassword: password, totpCode: "123456" },
      profile.csrfToken,
      requestSignal,
    );
    expect(body("/exports")).toEqual({
      startTm: "",
      endTm: "",
      keyword: "",
      filterEvent: [],
      createSort: -1,
      logTypeList: [],
      selectColumn: ["event", "CreateTm"],
      idempotencyKey: taskUUID,
      actorPassword: password,
      totpCode: "123456",
    });
    expect(calls("/exports")[0][1]).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      redirect: "error",
      cache: "no-store",
      signal: requestSignal,
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": profile.csrfToken,
      },
    });
    expect(calls("/exports")[0][1].headers).not.toHaveProperty("Origin");
    expect(localStorage.length + sessionStorage.length).toBe(0);
    for (const visibility of ["hidden", "all"] as const)
      await expect(
        auditAPI.submit(
          { ...input(), visibility },
          { actorPassword: password, totpCode: "123456" },
          profile.csrfToken,
          signal(),
        ),
      ).rejects.toMatchObject({ code: "invalid_export_visibility" });
    expect(calls("/exports")).toHaveLength(1);
  });
  it("rejects false visibility success and mismatched or malformed export acknowledgements", async () => {
    for (const invalid of [
      { result: "success", changed: 0, visibilityRevision: 1 },
      { result: "success", changed: 2, visibilityRevision: 1 },
      { result: "success", changed: 1, visibilityRevision: 0 },
      { result: "failure", changed: 1, visibilityRevision: 1 },
    ]) {
      override = () => response(invalid);
      await expect(
        auditAPI.visibility(
          "delete",
          ["auth.1"],
          "synthetic reason",
          { actorPassword: password, totpCode: "123456" },
          "csrf",
          signal(),
        ),
      ).rejects.toBeInstanceOf(ApiError);
    }
    for (const invalid of [
      { task: task(), taskUUID: "wrong", replayed: false },
      {
        task: task("queued", { taskName: "infrastructure.health" }),
        taskUUID,
        replayed: false,
      },
      { task: task(), taskUUID, replayed: "true" },
      { task: task("queued", { progress: 101 }), taskUUID, replayed: false },
    ]) {
      override = () => response(invalid);
      await expect(
        auditAPI.submit(
          input(),
          { actorPassword: password, totpCode: "123456" },
          "csrf",
          signal(),
        ),
      ).rejects.toMatchObject({ code: "invalid_response" });
    }
  });
  it("rejects mismatched task identity, unknown states, impossible ready state, and invalid row counts", async () => {
    for (const invalid of [
      detail("succeeded", { task: task("succeeded", { taskUUID: "other" }) }),
      detail("succeeded", {
        task: task("succeeded", { taskName: "infrastructure.health" }),
      }),
      detail("running", { downloadReady: true, rowCount: 1 }),
      detail("succeeded", { rowCount: -1 }),
      detail("succeeded", { rowCount: 100001 }),
      detail("succeeded", { rowCount: undefined }),
      detail("succeeded", { downloadReady: "true" }),
      detail("succeeded", { task: { ...task(), state: "SUCCESS" } }),
    ]) {
      override = () => response(invalid);
      await expect(
        auditAPI.detail(taskUUID, signal(), profile.ID),
      ).rejects.toMatchObject({
        code: "invalid_response",
      });
    }
  });
  it("accepts only bounded nonempty XLSX downloads and preserves HTTP error codes", async () => {
    for (const bad of [
      () => binary("text/html"),
      () => binary("application/zip"),
      () => binary(xlsxType, 0),
      () => binary(xlsxType, 1, String(128 * 1024 * 1024 + 1)),
      () => binary(xlsxType, 128 * 1024 * 1024 + 1),
    ]) {
      override = bad;
      await expect(
        auditAPI.download(taskUUID, signal(), profile.ID),
      ).rejects.toMatchObject({ code: "invalid_response" });
    }
    override = () => response({ error: "export_snapshot_changed" }, 409);
    await expect(
      auditAPI.download(taskUUID, signal(), profile.ID),
    ).rejects.toMatchObject({
      code: "export_snapshot_changed",
      status: 409,
    });
    override = () => binary(`${xlsxType}; charset=binary`);
    expect((await auditAPI.download(taskUUID, signal(), profile.ID)).size).toBe(
      16,
    );
  });
});

describe("audit workspace DOM unit behavior", () => {
  it.each(["menu", "checks"])(
    "requires both server grants: denies %s despite platform_admin",
    async (denied) => {
      override = (url) =>
        url === "/api/access/menu" && denied === "menu"
          ? response({ menu: metadata().filter((p) => p.mark !== "audit") })
          : url === "/api/access/check" && denied === "checks"
            ? response({ results: auditOperations.map(() => false) })
            : undefined;
      render(<AuditWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await screen.findByText("当前账户没有操作审计读取权限。");
      expect(calls("")).toHaveLength(0);
      expect(
        screen.queryByRole("button", { name: "当前筛选导出" }),
      ).not.toBeInTheDocument();
    },
  );
  it("keeps readonly records readable while hiding writes and export without task-submit permission", async () => {
    override = (url) =>
      url === "/api/access/check"
        ? response({
            results: auditOperations.map((operation) =>
              operation.startsWith("GET "),
            ),
          })
        : undefined;
    await start();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    for (const name of ["当前筛选导出", "隐藏所选记录", "恢复所选记录"])
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    expect(
      JSON.parse(
        fetcher.mock.calls.find(([url]) => url === "/api/access/check")![1]
          .body as string,
      ).paths,
    ).toEqual(auditOperations);
  });
  it("uses repeated filter arrays and UTC controls, then resets every control and query", async () => {
    await start();
    fill("开始时间（UTC）", "2026-10-06T12:34:56");
    fill("结束时间（UTC）", "2026-10-07T12:34:57");
    fill("审计关键词", "alice + 192.0.2");
    fill("审计可见性", "all");
    fill("审计排序", "1");
    fill("每页审计数", "50");
    choose("审计事件筛选", ["login", "resource.update"]);
    choose("审计类型筛选", ["7", "9"]);
    click("筛选审计");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toBe(
        "/api/audit?pageIdx=1&pageSize=50&createSort=1&visibility=all&startTm=2026-10-06T12%3A34%3A56.000Z&endTm=2026-10-07T12%3A34%3A57.000Z&keyword=alice+%2B+192.0.2&filterEvent=login&filterEvent=resource.update&logTypeList=7&logTypeList=9",
      ),
    );
    expect(screen.getByRole("button", { name: "当前筛选导出" })).toBeDisabled();
    click("清除审计筛选");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toBe(
        "/api/audit?pageIdx=1&pageSize=20&createSort=-1&visibility=visible",
      ),
    );
    for (const name of ["开始时间（UTC）", "结束时间（UTC）", "审计关键词"])
      expect(screen.getByLabelText(name, { exact: true })).toHaveValue("");
    expect(screen.getByLabelText("审计事件筛选", { exact: true })).toHaveValue(
      [],
    );
    expect(screen.getByLabelText("审计类型筛选", { exact: true })).toHaveValue(
      [],
    );
    expect(screen.getByLabelText("审计可见性")).toHaveValue("visible");
    expect(screen.getByLabelText("审计排序")).toHaveValue("-1");
    expect(screen.getByLabelText("每页审计数")).toHaveValue("20");
  });
  it.each(["2026-10-07T01:00", "2026-10-06T01:00"])(
    "rejects equal or inverted UTC end %s before requesting",
    async (end) => {
      await start();
      const count = calls("").length;
      fill("开始时间（UTC）", "2026-10-07T01:00");
      fill("结束时间（UTC）", end);
      click("筛选审计");
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "开始时间必须早于结束时间",
      );
      expect(calls("")).toHaveLength(count);
    },
  );
  it("renders explicit historic metadata absence without filling in the current account", async () => {
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response(
            list([
              row({
                loginUser: null,
                sourceIp: null,
                userId: null,
                eventResult: "NONE",
                availability: {
                  loginUser: false,
                  sourceIp: false,
                  path: false,
                  requestId: false,
                  eventResult: false,
                },
              }),
            ]),
          )
        : undefined;
    await start();
    const table = screen.getByRole("table", { name: "操作审计记录" });
    expect(within(table).getAllByText("未记录（历史数据缺失）")).toHaveLength(
      3,
    );
    expect(within(table).getByText("用户 ID：未记录")).toBeInTheDocument();
    expect(within(table).queryByText(profile.username)).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("查看元数据 auth.1"));
    expect(
      screen.getByText(
        "未记录：登录用户、登录IP、请求路径、请求标识、事件结果。历史缺失信息不会用当前账户信息补写。",
      ),
    ).toBeVisible();
  });
  it("prevents selecting protected audit records and blocks mixed visible/hidden operations", async () => {
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response(
            list([
              row(),
              row({ ID: "resource.2", source: "resource", deleted: true }),
              row({ ID: "audit.3", source: "audit", deletable: true }),
              row({
                ID: "operational_log.9",
                source: "operational_log",
                deletable: true,
              }),
              row({ ID: "task.4", source: "task", deletable: false }),
              row({ ID: "domain.5", source: "domain", deletable: false }),
              row({
                ID: "credential_use.8",
                source: "credential_use",
                domainId: "granted-domain",
                deletable: true,
              }),
              row({
                ID: "operation_account.6",
                source: "operation_account",
                domainId: "granted-domain",
                deletable: false,
              }),
            ]),
          )
        : undefined;
    await start();
    expect(
      screen.getByRole("checkbox", { name: "选择 operational_log.9" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: "选择 audit.3" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: "选择 task.4" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: "选择 domain.5" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: "选择 operation_account.6" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: "选择 credential_use.8" }),
    ).toBeDisabled();
    selectRow();
    expect(screen.getByRole("button", { name: "隐藏所选记录" })).toBeEnabled();
    selectRow("resource.2");
    expect(screen.getByRole("button", { name: "隐藏所选记录" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "恢复所选记录" })).toBeDisabled();
    click("刷新审计列表");
    await waitFor(() =>
      expect(screen.getByText("已选择 0 条；每次最多 100 条")).toBeVisible(),
    );
  });
  it("hides and restores exact IDs with fresh proof and independently rereads current visibility", async () => {
    let deleted = false;
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response(list([row({ deleted })]))
        : url === "/api/audit/delete"
          ? ((deleted = true),
            response({ result: "success", changed: 1, visibilityRevision: 1 }))
          : url === "/api/audit/restore"
            ? ((deleted = false),
              response({
                result: "success",
                changed: 1,
                visibilityRevision: 2,
              }))
            : undefined;
    await start();
    selectRow();
    click("隐藏所选记录");
    expect(screen.getByText(/原始记录保留且可恢复/)).toBeVisible();
    fill("操作原因", "合成隐藏原因");
    proof();
    click("确认隐藏");
    await screen.findByRole("table", { name: "操作审计记录" });
    expect(screen.getByText("已隐藏", { exact: true })).toBeVisible();
    expect(body("/delete")).toEqual({
      id: ["auth.1"],
      reason: "合成隐藏原因",
      actorPassword: password,
      totpCode: "123456",
    });
    selectRow();
    click("恢复所选记录");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    fill("操作原因", "合成恢复原因");
    proof("654321");
    click("确认恢复");
    await screen.findByRole("table", { name: "操作审计记录" });
    expect(screen.getByText("可见", { exact: true })).toBeVisible();
    expect(body("/restore")).toEqual({
      id: ["auth.1"],
      reason: "合成恢复原因",
      actorPassword: password,
      totpCode: "654321",
    });
    expect(calls("").length).toBeGreaterThanOrEqual(3);
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });
  it("clears proof after uncertain hide, refreshes actual state, and never fabricates success", async () => {
    let deleted = false;
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response(list([row({ deleted })]))
        : url === "/api/audit/delete"
          ? ((deleted = true),
            Promise.reject(new TypeError("synthetic offline after commit")))
          : undefined;
    await start();
    selectRow();
    click("隐藏所选记录");
    fill("操作原因", "合成原因");
    proof();
    click("确认隐藏");
    await screen.findByRole("alert");
    await screen.findByText(/已从服务器重新读取最近/);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    expect(screen.queryByText(/服务器已确认隐藏/)).not.toBeInTheDocument();
    expect(calls("").some(([url]) => url.includes("visibility=all"))).toBe(
      true,
    );
    click("取消");
    await screen.findByRole("table", { name: "操作审计记录" });
    expect(screen.getByText("已隐藏", { exact: true })).toBeVisible();
    expect(screen.queryByText(/服务器已确认隐藏/)).not.toBeInTheDocument();
    expect(calls("/delete")).toHaveLength(1);
  });
  it("locks duplicate visibility submits and ignores delayed success after cancel", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/audit/delete"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    await start();
    selectRow();
    click("隐藏所选记录");
    fill("操作原因", "合成原因");
    proof();
    const form = screen.getByLabelText("操作原因").closest("form")!;
    click("确认隐藏");
    fireEvent.submit(form);
    expect(calls("/delete")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "正在提交…" })).toBeDisabled();
    click("取消");
    await screen.findByRole("table", { name: "操作审计记录" });
    await act(async () =>
      resolve(
        await response({
          result: "success",
          changed: 1,
          visibilityRevision: 1,
        }),
      ),
    );
    expect(screen.queryByText(/服务器已确认隐藏/)).not.toBeInTheDocument();
    expect(screen.getByText("可见", { exact: true })).toBeVisible();
  });
  it("browser Back unmounts audit requests and ignores their late response", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url.startsWith("/api/audit/exports/detail")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await revealNavigation("操作审计");
    click("操作审计");
    await screen.findByRole("table", { name: "操作审计记录" });
    fill("导出任务 ID", taskUUID);
    click("查看导出任务");
    await waitFor(() => expect(calls("/exports/detail")).toHaveLength(1));
    act(() => {
      window.history.replaceState({}, "", "#account");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("heading", { name: "账户概览" });
    expect(calls("/exports/detail")[0][1].signal?.aborted).toBe(true);
    await act(async () => resolve(await response(detail())));
    expect(screen.queryByLabelText("导出实际结果")).not.toBeInTheDocument();
  });
});

describe("audit export DOM unit behavior", () => {
  it("allows one through eight registered columns, disables empty selection, and sends chosen order", async () => {
    await exportForm();
    expect(
      screen.getByText("已选 8 / 8 列；文件按所选列顺序导出。"),
    ).toBeVisible();
    for (const label of columnLabels) selectColumn(label);
    expect(screen.getByRole("button", { name: "确认创建导出" })).toBeDisabled();
    proof();
    fireEvent.submit(screen.getByLabelText("操作者当前密码").closest("form")!);
    expect(calls("/exports")).toHaveLength(0);
    selectColumn("审计时间");
    expect(screen.getByRole("button", { name: "确认创建导出" })).toBeEnabled();
    selectColumn("登录用户");
    selectColumn("事件");
    click("确认创建导出");
    await screen.findByLabelText("导出实际结果");
    expect(body("/exports").selectColumn).toEqual([
      "CreateTm",
      "loginUser",
      "event",
    ]);
  });
  it("refuses to expose an export proof form for a malformed column catalogue", async () => {
    override = (url) =>
      url === "/api/audit/columns"
        ? response({ ...catalogue(), defaultColumns: ["password"] })
        : undefined;
    await start();
    click("当前筛选导出");
    await screen.findByRole("alert");
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(calls("/exports")).toHaveLength(0);
  });
  it("polls actual queued and running states to an empty successful result, then stops", async () => {
    let state: TaskState = "queued";
    override = (url) =>
      url.startsWith("/api/audit/exports/detail")
        ? response(
            detail(
              state,
              state === "succeeded"
                ? {
                    rowCount: 0,
                    task: task("succeeded", { result: { rowCount: 0 } }),
                  }
                : {
                    task: task(state, {
                      progress: state === "running" ? 45 : 0,
                    }),
                  },
            ),
          )
        : undefined;
    await exportDetail();
    expect(screen.getByText("等待执行", { exact: true })).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "下载 XLSX" }),
    ).not.toBeInTheDocument();
    state = "running";
    await waitFor(
      () => expect(screen.getByLabelText("导出持久化进度")).toHaveValue(45),
      { timeout: 2500 },
    );
    state = "succeeded";
    await screen.findByText(
      "筛选结果为空；XLSX 仅包含所选列的表头，没有数据行。",
      {},
      { timeout: 2500 },
    );
    expect(screen.getByRole("button", { name: "下载 XLSX" })).toBeEnabled();
    expect(screen.getByLabelText("导出实际结果")).toHaveTextContent(
      '"rowCount": 0',
    );
    const count = calls("/exports/detail").length;
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 1150));
    });
    expect(calls("/exports/detail")).toHaveLength(count);
  });
  it("shows failed export and real server error without offering a download", async () => {
    override = (url) =>
      url.startsWith("/api/audit/exports/detail")
        ? response(
            detail("failed", {
              task: task("failed", {
                error: "export_too_large",
                result: { rows: 100001 },
              }),
            }),
          )
        : undefined;
    await exportDetail();
    expect(screen.getByText("已失败", { exact: true })).toBeVisible();
    expect(screen.getByText("export_too_large", { exact: true })).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "下载 XLSX" }),
    ).not.toBeInTheDocument();
  });
  it("downloads by authenticated same-origin fetch and ignores a remote server downloadPath", async () => {
    override = (url) =>
      url.startsWith("/api/audit/exports/detail")
        ? response(
            detail("succeeded", {
              downloadPath: "https://example.invalid/private-export",
            }),
          )
        : undefined;
    const create = vi.fn(() => "blob:synthetic-audit");
    const revoke = vi.fn();
    vi.stubGlobal(
      "URL",
      class extends URL {
        static createObjectURL = create;
        static revokeObjectURL = revoke;
      },
    );
    const anchor = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    await exportDetail();
    click("下载 XLSX");
    await screen.findByText(
      "已收到通过当前权限验证的 XLSX 文件，并交由浏览器下载。",
    );
    expect(calls("/exports/download")[0][0]).toBe(
      `/api/audit/exports/download?taskUUID=${taskUUID}`,
    );
    expect(calls("/exports/download")[0][1]).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      redirect: "error",
      cache: "no-store",
    });
    expect(fetcher.mock.calls.every(([url]) => url.startsWith("/api/"))).toBe(
      true,
    );
    expect(create).toHaveBeenCalledTimes(1);
    expect(anchor).toHaveBeenCalledTimes(1);
    const downloadedAnchor = anchor.mock.instances[0] as HTMLAnchorElement;
    expect(downloadedAnchor.download).toBe(`audit-${taskUUID}.xlsx`);
    expect(downloadedAnchor.href).toBe("blob:synthetic-audit");
  });
  it("rejects HTML download responses without claiming or starting a file download", async () => {
    override = (url) =>
      url.startsWith("/api/audit/exports/download")
        ? binary("text/html")
        : undefined;
    const anchor = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    await exportDetail();
    click("下载 XLSX");
    await screen.findByRole("alert");
    expect(anchor).not.toHaveBeenCalled();
    expect(
      screen.queryByText(/已收到通过当前权限验证/),
    ).not.toBeInTheDocument();
  });
  it("removes download permanently for a stale 409 even if detail temporarily remains ready", async () => {
    override = (url) =>
      url.startsWith("/api/audit/exports/download")
        ? response({ error: "export_snapshot_changed" }, 409)
        : undefined;
    await exportDetail();
    click("下载 XLSX");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "导出快照已因隐藏或恢复操作失效",
    );
    await waitFor(() =>
      expect(calls("/exports/detail").length).toBeGreaterThanOrEqual(2),
    );
    expect(
      screen.queryByRole("button", { name: "下载 XLSX" }),
    ).not.toBeInTheDocument();
    click("刷新导出状态");
    await waitFor(() =>
      expect(calls("/exports/detail").length).toBeGreaterThanOrEqual(3),
    );
    expect(
      screen.queryByRole("button", { name: "下载 XLSX" }),
    ).not.toBeInTheDocument();
  });
  it("erases exported detail and download after authorization is revoked", async () => {
    let denied = false;
    override = (url) =>
      url.startsWith("/api/audit/exports/detail") && denied
        ? response({ error: "forbidden" }, 403)
        : undefined;
    await exportDetail();
    denied = true;
    click("刷新导出状态");
    await screen.findByRole("alert");
    expect(screen.queryByLabelText("导出实际结果")).not.toBeInTheDocument();
    expect(
      screen.queryByText(taskUUID, { exact: true }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "下载 XLSX" }),
    ).not.toBeInTheDocument();
  });
  it("locks double export submits, cancels only local waiting, and ignores late success", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/audit/exports"
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    await exportForm();
    const key = (screen.getByLabelText("导出幂等键") as HTMLInputElement).value;
    proof();
    const form = screen.getByLabelText("操作者当前密码").closest("form")!;
    click("确认创建导出");
    fireEvent.submit(form);
    expect(calls("/exports")).toHaveLength(1);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    click("取消");
    await screen.findByRole("table", { name: "操作审计记录" });
    await act(async () =>
      resolve(await response({ task: task(), taskUUID, replayed: false })),
    );
    expect(screen.queryByLabelText("导出实际结果")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "当前筛选导出" })).toBeDisabled();
    click("继续核对未确认的导出");
    await screen.findByLabelText("操作者当前密码");
    expect(screen.getByLabelText("导出幂等键")).toHaveValue(key);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
  });
  it("retains exact filter, column order and key across failed submission, cancel, remount and fresh OTP", async () => {
    let submissions = 0;
    override = (url) =>
      url === "/api/audit/exports"
        ? ++submissions === 1
          ? Promise.reject(new TypeError("synthetic uncertain result"))
          : response({ task: task(), taskUUID, replayed: true })
        : undefined;
    const mounted = await start();
    fill("审计关键词", "original");
    fill("开始时间（UTC）", "2026-10-06T01:02:03");
    choose("审计事件筛选", ["login", "resource.update"]);
    choose("审计类型筛选", ["7", "9"]);
    fill("审计排序", "1");
    click("筛选审计");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "当前筛选导出" }),
      ).toBeEnabled(),
    );
    click("当前筛选导出");
    await screen.findByLabelText("操作者当前密码");
    for (const label of columnLabels) selectColumn(label);
    selectColumn("审计时间");
    selectColumn("事件");
    const key = (screen.getByLabelText("导出幂等键") as HTMLInputElement).value;
    proof();
    click("确认创建导出");
    await screen.findByRole("alert");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    expect(
      screen.getByRole("checkbox", { name: "导出列：事件" }),
    ).toBeDisabled();
    const stored = sessionStorage.getItem(
      `adtr.pending-audit-export:${profile.ID}:${encodeURIComponent(profile.username)}`,
    )!;
    expect(JSON.parse(stored).input.idempotencyKey).toBe(key);
    for (const secret of [
      password,
      "123456",
      profile.csrfToken,
      "actorPassword",
      "totpCode",
      "csrfToken",
    ])
      expect(storedText()).not.toContain(secret);
    expect(localStorage.length).toBe(0);
    click("取消");
    await screen.findByRole("table", { name: "操作审计记录" });
    fill("审计关键词", "different");
    click("筛选审计");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toContain("keyword=different"),
    );
    mounted.unmount();
    await start();
    click("继续核对未确认的导出");
    await screen.findByLabelText("操作者当前密码");
    expect(screen.getByLabelText("导出幂等键")).toHaveValue(key);
    expect(screen.getByLabelText("导出筛选摘要")).toHaveTextContent("original");
    expect(screen.getByLabelText("导出筛选摘要")).not.toHaveTextContent(
      "different",
    );
    proof("654321");
    click("使用原幂等键确认导出");
    await screen.findByText("已确认原导出任务，未重复创建。");
    const first = body("/exports", 0);
    const second = body("/exports", 1);
    expect(second).toEqual({ ...first, totpCode: "654321" });
    expect(second.selectColumn).toEqual(["CreateTm", "event"]);
    expect(second.filterEvent).toEqual(["login", "resource.update"]);
    expect(second.logTypeList).toEqual([7, 9]);
    expect(second.startTm).toBe("2026-10-06T01:02:03.000Z");
    expect(second.idempotencyKey).toBe(key);
    expect(sessionStorage.length).toBe(0);
  });
  it("restores only whitelisted non-secret intent fields from reload storage and clears on logout", async () => {
    const saved = {
      ...input(),
      keyword: "saved synthetic filter",
      actorPassword: "polluted-password",
      totpCode: "999999",
      csrfToken: "polluted-token",
      actorId: 77,
    };
    sessionStorage.setItem(
      "adtr.pending-audit-export",
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        input: saved,
      }),
    );
    await start();
    click("继续核对未确认的导出");
    await screen.findByLabelText("操作者当前密码");
    expect(screen.getByLabelText("导出幂等键")).toHaveValue(taskUUID);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(auditIntent(profile)?.input).toEqual({
      ...input(),
      keyword: "saved synthetic filter",
    });
    proof("654321");
    click("使用原幂等键确认导出");
    await screen.findByLabelText("导出实际结果");
    expect(body("/exports")).toMatchObject({
      actorPassword: password,
      totpCode: "654321",
    });
    expect(body("/exports")).not.toHaveProperty("actorId");
    expect(body("/exports")).not.toHaveProperty("csrfToken");
    discardAuditIntent();
    expect(sessionStorage.length).toBe(0);
  });
});

describe("audit stale-data and pagination regressions", () => {
  it("clears all prior export metadata when a completed snapshot detail returns 409", async () => {
    let stale = false;
    override = (url) =>
      url.startsWith("/api/audit/exports/detail") && stale
        ? response({ error: "export_snapshot_changed" }, 409)
        : undefined;
    await exportDetail();
    expect(screen.getByLabelText("导出持久化进度")).toHaveValue(100);
    stale = true;
    click("刷新导出状态");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "请返回列表重新创建导出",
    );
    expect(screen.queryByLabelText("导出实际结果")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("导出持久化进度")).not.toBeInTheDocument();
    expect(
      screen.queryByText(taskUUID, { exact: true }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("实际导出行数", { exact: true }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "下载 XLSX" }),
    ).not.toBeInTheDocument();
  });
  it("paginates by real server metadata and resets page and selection on a new filter", async () => {
    override = (url) => {
      if (!url.startsWith("/api/audit?")) return;
      const query = new URLSearchParams(url.split("?")[1]);
      const pageIdx = Number(query.get("pageIdx"));
      const pageSize = Number(query.get("pageSize"));
      return response({
        ...list(),
        page: { pageIdx, pageSize, total: 11, totalPage: 2 },
        exhausted: pageIdx === 2,
      });
    };
    await start();
    fill("每页审计数", "10");
    click("筛选审计");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toContain("pageIdx=1&pageSize=10"),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled(),
    );
    selectRow();
    click("下一页");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toContain("pageIdx=2&pageSize=10"),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled(),
    );
    expect(screen.getByText("已选择 0 条；每次最多 100 条")).toBeVisible();
    fill("审计关键词", "narrowed");
    click("筛选审计");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toContain("pageIdx=1&pageSize=10"),
    );
    expect(calls("").at(-1)![0]).toContain("keyword=narrowed");
  });
  it("preserves an active event when refreshed visible-only event metadata no longer includes it", async () => {
    let eventAbsent = false;
    override = (url) =>
      url === "/api/audit/types" && eventAbsent
        ? response({
            events: ["resource.update"],
            List: [{ logType: 9, logTypeName: "系统设置", eventNameList: [] }],
          })
        : undefined;
    await start();
    choose("审计事件筛选", ["login"]);
    click("筛选审计");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toContain("filterEvent=login"),
    );
    eventAbsent = true;
    click("刷新审计列表");
    await waitFor(() => expect(calls("/types")).toHaveLength(2));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "筛选审计" })).toBeEnabled(),
    );
    expect(screen.getByLabelText("审计事件筛选", { exact: true })).toHaveValue([
      "login",
    ]);
    fill("审计可见性", "hidden");
    click("筛选审计");
    await waitFor(() =>
      expect(calls("").at(-1)![0]).toContain("visibility=hidden"),
    );
    expect(
      new URLSearchParams(calls("").at(-1)![0].split("?")[1]).getAll(
        "filterEvent",
      ),
    ).toEqual(["login"]);
  });
  it("clears rejected proof and requires a fresh proof before retrying a visibility change", async () => {
    let attempts = 0;
    override = (url) =>
      url === "/api/audit/delete"
        ? ++attempts === 1
          ? response({ error: "invalid_totp" }, 403)
          : response({ result: "success", changed: 1, visibilityRevision: 1 })
        : undefined;
    await start();
    selectRow();
    click("隐藏所选记录");
    fill("操作原因", "Synthetic reason");
    proof();
    click("确认隐藏");
    await screen.findByRole("alert");
    await screen.findByText(/已从服务器重新读取最近/);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    fireEvent.submit(screen.getByLabelText("操作原因").closest("form")!);
    expect(calls("/delete")).toHaveLength(1);
    proof("654321");
    click("确认隐藏");
    await screen.findByRole("table", { name: "操作审计记录" });
    expect(calls("/delete")).toHaveLength(2);
    expect(body("/delete")).toEqual({
      ...body("/delete", 0),
      totpCode: "654321",
    });
  });
  it("locks duplicate downloads and ignores a late file after leaving its detail", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url.startsWith("/api/audit/exports/download")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    const anchor = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    await exportDetail();
    const download = screen.getByRole("button", { name: "下载 XLSX" });
    fireEvent.click(download);
    fireEvent.click(download);
    expect(calls("/exports/download")).toHaveLength(1);
    click("返回审计列表");
    await screen.findByRole("table", { name: "操作审计记录" });
    expect(calls("/exports/download")[0][1].signal?.aborted).toBe(true);
    await act(async () => resolve(await binary()));
    expect(anchor).not.toHaveBeenCalled();
    expect(
      screen.queryByText(/已收到通过当前权限验证/),
    ).not.toBeInTheDocument();
  });
  it("isolates another user's saved intent and explicitly discards only the resolved actor", () => {
    sessionStorage.setItem(
      "adtr.pending-audit-export",
      JSON.stringify({ owner: 999, username: "other", input: input() }),
    );
    expect(auditIntent(profile)).toBeUndefined();
    expect(sessionStorage.length).toBe(1);
    sessionStorage.setItem(
      "adtr.pending-audit-export",
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        input: input(),
      }),
    );
    expect(auditIntent(profile)?.input.idempotencyKey).toBe(taskUUID);
    discardAuditIntent();
    expect(auditIntent(profile)).toBeUndefined();
    expect(sessionStorage.length).toBe(0);
  });
});

describe("SOC audit presentation and inline record lifecycle", () => {
  it("uses authorized page counts and expands only the list fields without another read", async () => {
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response({
            ...list(),
            page: { pageIdx: 1, pageSize: 20, total: 45, totalPage: 3 },
            exhausted: false,
          })
        : undefined;
    await start();
    expect(screen.getByLabelText("审计查询结果摘要")).toHaveTextContent(
      "匹配 45 条",
    );
    expect(screen.getByLabelText("审计查询结果摘要")).toHaveTextContent(
      "本页 1 条",
    );
    const requests = fetcher.mock.calls.length;
    const toggle = screen.getByRole("button", { name: "查看元数据 auth.1" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(
      screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
    ).not.toBeInTheDocument();
    fireEvent.click(toggle);
    const fields = screen.getByRole("region", { name: "审计记录字段 auth.1" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(toggle).toHaveAttribute("aria-controls", fields.id);
    expect(fields).toHaveTextContent('{"synthetic":true}');
    expect(fields).toHaveTextContent("未记录 / 平台操作");
    expect(fetcher.mock.calls).toHaveLength(requests);
    click("收起记录字段 auth.1");
    expect(toggle).toHaveFocus();
    expect(fields).not.toBeInTheDocument();
    expect(toggle).toHaveAttribute("aria-expanded", "false");
  });
  it("renders hostile field text inertly without inventing request metadata", async () => {
    const text =
      "<img src=x onerror=alert(1)> https://example.invalid/<script>boom</script>";
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response(
            list([
              row({
                loginUser: text,
                domainId: text,
                eventArgs: text,
                event: text,
                userId: 3,
              }),
            ]),
          )
        : undefined;
    await start();
    click("查看元数据 auth.1");
    const fields = screen.getByRole("region", { name: "审计记录字段 auth.1" });
    expect(fields).toHaveTextContent(text);
    expect(fields.querySelectorAll("img,script,a")).toHaveLength(0);
    expect(
      within(fields).getByText("0", { selector: "dd" }),
    ).toBeInTheDocument();
    expect(within(fields).queryByText("请求路径")).not.toBeInTheDocument();
  });
  it.each(["筛选审计", "清除审计筛选", "刷新审计列表"])(
    "clears expanded private fields on %s",
    async (action) => {
      await start();
      click("查看元数据 auth.1");
      if (action === "筛选审计") fill("审计关键词", "other");
      click(action);
      await screen.findByRole("table", { name: "操作审计记录" });
      expect(
        screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
      ).not.toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "查看元数据 auth.1" }),
      ).toHaveAttribute("aria-expanded", "false");
    },
  );
  it("clears expansion on paging even if the next page includes the same ID", async () => {
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response({
            ...list(),
            page: {
              pageIdx: Number(
                new URL(url, "http://localhost").searchParams.get("pageIdx"),
              ),
              pageSize: 20,
              total: 45,
              totalPage: 3,
            },
            exhausted: false,
          })
        : undefined;
    await start();
    click("查看元数据 auth.1");
    click("下一页");
    await screen.findByRole("table", { name: "操作审计记录" });
    expect(screen.getByLabelText("分页")).toHaveTextContent("第 2 / 3 页");
    expect(
      screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
    ).not.toBeInTheDocument();
  });
  it("removes fields during refresh and does not resurrect them after an ignored-abort old response", async () => {
    await start();
    click("查看元数据 auth.1");
    let old!: (value: Response) => void;
    override = (url) =>
      url.startsWith("/api/audit?")
        ? new Promise((resolve) => {
            old = resolve;
          })
        : undefined;
    click("刷新审计列表");
    expect(
      screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("table", { name: "操作审计记录" }),
    ).not.toBeInTheDocument();
    override = (url) =>
      url.startsWith("/api/audit?")
        ? response(list([row({ ID: "auth.2", eventArgs: "new query" })]))
        : undefined;
    fill("审计关键词", "new");
    click("筛选审计");
    await screen.findByRole("button", { name: "查看元数据 auth.2" });
    await act(async () =>
      old(await response(list([row({ eventArgs: "stale private fields" })]))),
    );
    expect(screen.queryByText("stale private fields")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "查看元数据 auth.1" }),
    ).not.toBeInTheDocument();
  });
  it.each([401, 403])(
    "clears records on a refreshed %s response without reporting zero matching records",
    async (status) => {
      const changed = vi.fn();
      await start(changed);
      click("查看元数据 auth.1");
      override = (url) =>
        url.startsWith("/api/audit?")
          ? response(
              { error: status === 401 ? "unauthenticated" : "forbidden" },
              status,
            )
          : undefined;
      click("刷新审计列表");
      await waitFor(() =>
        expect(screen.queryByText("正在读取审计记录…")).not.toBeInTheDocument(),
      );
      expect(
        screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByLabelText("审计查询结果摘要"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByText("没有符合筛选条件的审计记录。"),
      ).not.toBeInTheDocument();
      if (status === 401) expect(changed).toHaveBeenCalled();
    },
  );
  it("unmounts inline records on session change and navigation", async () => {
    const mounted = await start();
    click("查看元数据 auth.1");
    await act(async () =>
      mounted.rerender(
        <AuditWorkspace
          profile={{ ...profile, csrfToken: "new-session" }}
          sessionChanged={vi.fn()}
        />,
      ),
    );
    expect(
      screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
    ).not.toBeInTheDocument();
    click("查看元数据 auth.1");
    click("导出历史");
    expect(
      screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
    ).not.toBeInTheDocument();
    click("返回审计列表");
    await screen.findByRole("table", { name: "操作审计记录" });
    expect(
      screen.queryByRole("region", { name: "审计记录字段 auth.1" }),
    ).not.toBeInTheDocument();
  });
  it("retains read-only expansion with no mutation or export controls", async () => {
    override = (url, init) =>
      url === "/api/access/check"
        ? response({
            results: JSON.parse(init.body as string).paths.map(
              (operation: string) =>
                ["GET /api/audit", "GET /api/audit/types"].includes(operation),
            ),
          })
        : undefined;
    await start();
    click("查看元数据 auth.1");
    expect(
      screen.getByRole("region", { name: "审计记录字段 auth.1" }),
    ).toBeVisible();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    for (const name of [
      "当前筛选导出",
      "隐藏所选记录",
      "恢复所选记录",
      "查看导出任务",
      "导出历史",
    ])
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
  });
});
