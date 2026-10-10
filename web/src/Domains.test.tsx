import { revealNavigation } from "./test-navigation";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, afterEach, describe, it, expect, vi } from "vitest";
import DomainsWorkspace, { discardDomainIntent } from "./DomainsWorkspace";
import App from "./App";
import { marks, labels, type Permission } from "./access-api";
import { PermissionEditor, emptyPermissions } from "./access-common";
import {
  domainAPI,
  validConnection,
  validRevision,
  validADPair,
  validIP,
  domainOperations,
  type Connection,
  type Diagnostic,
} from "./domain-api";
import { saveDomainIntent, readDomainIntent } from "./domain-intent";
import type { Profile } from "./api";
import type { Task, TaskState } from "./task-api";
const id = "synthetic-domain-id",
  taskID = "00000000-0000-4000-8000-000000000001";
const connection = (extra: Partial<Connection> = {}): Connection => ({
  domainId: id,
  domain: "synthetic.invalid",
  dcHostName: "dc.synthetic.invalid",
  ldapAddr: "192.0.2.50",
  port: "389",
  mode: "starttls",
  revision: "9007199254740993",
  credentialRevision: "1",
  credentialConfigured: true,
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:01Z",
  connectionState: "unverified",
  lastDiagnostic: null,
  latestTaskUUID: "",
  ...extra,
});
const diagnostic = (extra: Partial<Diagnostic> = {}): Diagnostic => ({
  taskUUID: taskID,
  revision: connection().revision,
  credentialRevision: "1",
  stage: "complete",
  code: "ok",
  observedAt: "2026-10-07T01:00:00Z",
  elapsedMilliseconds: 12,
  dcHostName: "dc.synthetic.invalid",
  ...extra,
});
const task = (
  state: TaskState = "queued",
  extra: Partial<Task> = {},
): Task => ({
  taskUUID: taskID,
  taskName: "domain.connection_test",
  domainId: id,
  payloadVersion: 1,
  state,
  sourceState: "PENDING",
  error: "",
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:01Z",
  attempt: 0,
  maxAttempts: 1,
  progress: 0,
  resultVersion: 0,
  result: {},
  cursor: {},
  parentTaskUUID: "",
  terminalAt: null,
  archived: false,
  visibilityVersion: 0,
  ...extra,
});
const list = (items: Connection[] = [connection()]) => ({
  page: {
    pageIdx: 1,
    pageSize: 20,
    total: items.length,
    totalPage: items.length ? 1 : 0,
  },
  List: items,
  exhausted: true,
});
const metadata = (): Permission[] =>
  marks.map((mark) => ({
    mark,
    name: labels[mark],
    auth: { readable: true, writeable: true },
    allow_auth: { readable: true, writeable: true },
    children: [],
    paths: [],
    checked: true,
    icon: mark,
  }));
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status < 400,
    status,
    headers: new Headers({ "X-ADTR-User-ID": String(profile?.ID ?? 1) }),
    json: async () => body,
  } as Response);
let profile: Profile,
  fetcher: ReturnType<typeof vi.fn>,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined;
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const proof = () => {
  fill("操作者当前密码", "Synthetic Actor Password 123");
  fill("未使用的认证器验证码", "123456");
};
const calls = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const body = (path: string) =>
  JSON.parse(calls(path).at(-1)![1].body as string);
async function start() {
  render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
  await screen.findByRole("table", { name: "获授权的域连接" });
}
async function detail() {
  await start();
  click("查看 synthetic.invalid");
  await screen.findByRole("heading", { name: "域连接详情：synthetic.invalid" });
}
async function create() {
  await start();
  click("新增域连接");
  await screen.findByRole("heading", { name: "登记域连接" });
  fill("域 DNS 名称", "synthetic.invalid");
  fill("域控 DNS 名称", "dc.synthetic.invalid");
  fill("AD 用户名", "SYNTHETIC\\reader");
  fill("AD 密码", " Synthetic secret ");
  proof();
}
beforeEach(() => {
  discardDomainIntent({ ID: 1, username: "admin" });
  discardDomainIntent({ ID: 2, username: "another" });
  sessionStorage.clear();
  window.history.replaceState({}, "", "#account");
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
    csrfToken: "domain-test-session",
  };
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
    if (path === "/api/domains") return response(list());
    if (path === "/api/domains/detail")
      return response({ connection: connection() });
    if (path === "/api/domains/create")
      return response({
        result: "SUCCESS",
        domainId: id,
        revision: "1",
        requiresResourceAssignment: true,
        replayed: false,
      });
    if (path === "/api/domains/update")
      return response({
        result: "SUCCESS",
        domainId: id,
        revision: "9007199254740994",
      });
    if (path === "/api/domains/delete")
      return response({ result: "SUCCESS", domainId: id });
    if (path === "/api/domains/test")
      return response({ task: task(), replayed: false });
    if (path === "/api/domains/test-result")
      return response({ task: task(), diagnostic: null });
    if (path === "/api/domains/creation")
      return response({ error: "not_found" }, 404);
    throw Error(`Unexpected ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => vi.useRealTimers());
describe("domain safe DTO and validation", () => {
  it("preserves bigint revisions and rejects numeric/overflow/noncanonical revisions", () => {
    expect(validRevision("9007199254740993")).toBe(true);
    for (const v of [9007199254740993, "01", "0", "9223372036854775808"]) {
      expect(validRevision(v)).toBe(false);
    }
    expect(validConnection(connection())).toBe(true);
  });
  it("rejects credential-bearing, mismatched transport, invented success, and stale DTOs", () => {
    for (const bad of [
      { ...connection(), username: "secret" },
      { ...connection(), port: "636" },
      { ...connection(), connectionState: "verified" },
      { ...connection(), lastDiagnostic: diagnostic({ revision: "1" }) },
    ])
      expect(validConnection(bad)).toBe(false);
  });
  it("requires terminal success plus current safe diagnostic, never raw LDAP text", async () => {
    override = () =>
      response({ task: task("running"), diagnostic: diagnostic() });
    await expect(
      domainAPI.result(taskID, id, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () =>
      response({
        task: task("failed"),
        diagnostic: diagnostic({ code: "raw secret returned", stage: "bind" }),
      });
    await expect(
      domainAPI.result(taskID, id, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("accepts exact custom secret bytes and validates bounded credentials and literal IP", () => {
    expect(validADPair("SYNTHETIC\\reader", " x ")).toBe(true);
    expect(validADPair("reader@alternate.invalid", "short")).toBe(true);
    for (const v of ["", "x".repeat(51), "x\0y"])
      expect(validADPair("a@b", v)).toBe(false);
    expect(validADPair(" reader@a", "abc")).toBe(false);
    expect(validIP("2001:db8::1")).toBe(true);
    expect(validIP("http://host")).toBe(false);
  });
  it("round trips the domains mark with deny-by-default editor state", () => {
    expect(marks).toHaveLength(13);
    expect(emptyPermissions().find((p) => p.mark === "domains")?.auth).toEqual({
      readable: false,
      writeable: false,
    });
    const changed = vi.fn();
    render(
      <PermissionEditor
        value={emptyPermissions()}
        metadata={metadata()}
        onChange={changed}
      />,
    );
    fireEvent.click(screen.getByLabelText("域连接：写入"));
    expect(
      changed.mock.calls[0][0].find(
        (p: { mark: string }) => p.mark === "domains",
      ).auth,
    ).toEqual({ readable: true, writeable: true });
  });
});
describe("domain workspace security flows", () => {
  it("uses all exact route checks and hides denied UI without fetching domain data", async () => {
    override = (url) =>
      url === "/api/access/menu"
        ? response({ menu: metadata().filter((m) => m.mark !== "domains") })
        : undefined;
    render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("服务器未授予域连接读取权限。");
    expect(calls("/api/domains")).toHaveLength(0);
    expect(body("/api/access/check").paths).toEqual(domainOperations);
  });
  it("saves local unverified registration with write-only credentials, CSRF and explicit F47 assignment", async () => {
    await create();
    click("保存本地连接");
    await screen.findByLabelText("新域 ID");
    expect(screen.getByLabelText("新域 ID")).toHaveValue(id);
    expect(screen.getByText(/显式关联所需角色/)).toBeVisible();
    const request = body("/api/domains/create");
    expect(request).toMatchObject({
      port: "389",
      username: "SYNTHETIC\\reader",
      password: " Synthetic secret ",
      actorPassword: "Synthetic Actor Password 123",
      totpCode: "123456",
    });
    expect(calls("/api/domains/create")[0][1].headers["X-CSRF-Token"]).toBe(
      profile.csrfToken,
    );
    expect(calls("/api/domains/test")).toHaveLength(0);
    expect(sessionStorage.length + localStorage.length).toBe(0);
    expect(screen.queryByLabelText("AD 密码")).toBeNull();
  });
  it("locks repeated create clicks and clears secrets immediately, even before delayed response", async () => {
    let resolve!: (r: Response) => void;
    override = (url) =>
      url === "/api/domains/create"
        ? new Promise((r) => (resolve = r))
        : undefined;
    await create();
    click("保存本地连接");
    expect(screen.getByLabelText("AD 密码")).toHaveValue("");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    fireEvent.submit(screen.getByLabelText("域 DNS 名称").closest("form")!);
    expect(calls("/api/domains/create")).toHaveLength(1);
    expect(
      sessionStorage.getItem(
        `adtr.domain-intent.v1:${profile.ID}:${encodeURIComponent(profile.username)}`,
      ),
    ).not.toContain("Synthetic secret");
    click("返回域连接列表");
    await screen.findByRole("heading", { name: "核对未确认的登记" });
    await act(async () =>
      resolve(
        await response({
          result: "SUCCESS",
          domainId: "late-id",
          revision: "1",
          requiresResourceAssignment: true,
          replayed: false,
        }),
      ),
    );
    expect(screen.queryByDisplayValue("late-id")).toBeNull();
    expect(screen.getByRole("button", { name: "新增域连接" })).toBeDisabled();
  });
  it("recovers interrupted creation using original receipt key after remount", async () => {
    saveDomainIntent(profile, {
      kind: "create",
      key: "original-create-key",
      domain: "synthetic.invalid",
    });
    override = (url) =>
      url.startsWith("/api/domains/creation?")
        ? response({
            receipt: {
              domainId: id,
              domain: "synthetic.invalid",
              revision: "1",
              deleted: false,
              requiresResourceAssignment: true,
            },
          })
        : undefined;
    await start();
    await screen.findByLabelText("新域 ID");
    expect(calls("/api/domains/create")).toHaveLength(0);
    expect(calls("/api/domains/creation")[0][0]).toContain(
      "original-create-key",
    );
    expect(sessionStorage.length).toBe(0);
  });
  it.each(["immediate", "delayed"] as const)(
    "restores the creation route and original receipt with an %s response",
    async (timing) => {
      saveDomainIntent(profile, {
        kind: "create",
        key: "reload-create-key",
        domain: "synthetic.invalid",
      });
      window.history.replaceState({}, "", "#domains/create");
      let resolve!: (value: Response) => void;
      const receipt = {
        receipt: {
          domainId: id,
          domain: "synthetic.invalid",
          revision: "1",
          deleted: false,
          requiresResourceAssignment: true,
        },
      };
      override = (url) =>
        url.startsWith("/api/domains/creation?")
          ? timing === "immediate"
            ? response(receipt)
            : new Promise<Response>((done) => (resolve = done))
          : url.startsWith("/api/domains?")
            ? response(list([]))
            : undefined;
      render(<App />);
      if (timing === "immediate") {
        // Recovery may finish before the caller observes the restored route.
        await screen.findByLabelText("新域 ID");
      } else {
        await screen.findByRole("heading", { name: "核对未确认的登记" });
        expect(screen.queryByLabelText("新域 ID")).toBeNull();
        expect(readDomainIntent(profile)?.key).toBe("reload-create-key");
        expect(
          screen.getByRole("button", { name: "新增域连接" }),
        ).toBeDisabled();
      }
      expect(screen.getByRole("button", { name: "域连接" })).toHaveAttribute(
        "aria-current",
        "page",
      );
      expect(screen.getByRole("heading", { name: "域连接" })).toBeVisible();
      expect(window.location.hash).toBe("#domains/create");
      if (timing === "delayed")
        await act(async () => resolve(await response(receipt)));
      expect(await screen.findByLabelText("新域 ID")).toHaveValue(id);
      expect(calls("/api/auth/me")).toHaveLength(1);
      expect(calls("/api/domains/creation")).toHaveLength(1);
      expect(calls("/api/domains/creation")[0][0]).toBe(
        "/api/domains/creation?idempotencyKey=reload-create-key",
      );
      expect(calls("/api/domains/create")).toHaveLength(0);
      expect(calls("/api/domains/test")).toHaveLength(0);
      expect(readDomainIntent(profile)).toBeNull();
      expect(sessionStorage.length).toBe(0);
      expect(screen.queryByLabelText("AD 密码")).toBeNull();
      expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
      await screen.findByText(/当前筛选下没有获授权的域连接/);
    },
  );
  it("keeps unknown creation blocked after missing receipt and shows no optimistic save", async () => {
    await create();
    override = (url) =>
      url === "/api/domains/create"
        ? Promise.reject(Error("socket lost"))
        : undefined;
    click("保存本地连接");
    await screen.findByText(/请返回列表，重新读取配置或查询原登记回执/);
    click("返回域连接列表");
    await screen.findByRole("heading", { name: "核对未确认的登记" });
    expect(screen.queryByLabelText("新域 ID")).toBeNull();
    expect(screen.getByRole("button", { name: "新增域连接" })).toBeDisabled();
  });
  it("never prepopulates credentials, omits unchanged pair and keeps bigint CAS exact", async () => {
    await detail();
    click("编辑域连接");
    await screen.findByRole("heading", { name: "编辑域连接" });
    expect(screen.queryByLabelText("AD 用户名")).toBeNull();
    fill("域控 DNS 名称", "dc2.synthetic.invalid");
    proof();
    click("保存域修改");
    await waitFor(() => expect(calls("/api/domains/update")).toHaveLength(1));
    expect(body("/api/domains/update")).toMatchObject({
      expectedRevision: "9007199254740993",
      dcHostName: "dc2.synthetic.invalid",
    });
    expect(body("/api/domains/update")).not.toHaveProperty("username");
    expect(body("/api/domains/update")).not.toHaveProperty("password");
  });
  it("replacement requires both empty inputs and clears them on toggle off", async () => {
    await detail();
    click("编辑域连接");
    fireEvent.click(await screen.findByLabelText("替换凭据"));
    expect(screen.getByLabelText("AD 用户名")).toHaveValue("");
    expect(screen.getByLabelText("AD 密码")).toHaveValue("");
    fill("AD 用户名", "SYNTHETIC\\reader");
    proof();
    fireEvent.submit(screen.getByLabelText("AD 用户名").closest("form")!);
    expect(calls("/api/domains/update")).toHaveLength(0);
    fill("AD 密码", "new secret");
    fireEvent.click(screen.getByLabelText("替换凭据"));
    fireEvent.click(screen.getByLabelText("替换凭据"));
    expect(screen.getByLabelText("AD 密码")).toHaveValue("");
  });
  it("revision conflict requires fresh server review and never automatically retries", async () => {
    override = (url) =>
      url === "/api/domains/update"
        ? response({ error: "revision_conflict" }, 409)
        : undefined;
    await detail();
    click("编辑域连接");
    await screen.findByLabelText("域控 DNS 名称");
    fill("域控 DNS 名称", "dc2.synthetic.invalid");
    proof();
    click("保存域修改");
    await screen.findByText(/配置已被其他操作修改/);
    expect(screen.getByRole("button", { name: "保存域修改" })).toBeDisabled();
    expect(calls("/api/domains/update")).toHaveLength(1);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
  });
  it("test uses only saved ID/revision and fresh proof, then shows queue rather than success", async () => {
    await detail();
    click("检测已保存连接");
    await screen.findByRole("heading", {
      name: "检测已保存连接：synthetic.invalid",
    });
    proof();
    click("确认检测保存版本");
    await screen.findByText("等待执行");
    expect(Object.keys(body("/api/domains/test")).sort()).toEqual(
      [
        "actorPassword",
        "domainId",
        "expectedRevision",
        "idempotencyKey",
        "totpCode",
      ].sort(),
    );
    expect(
      screen.queryByText("TLS、凭据绑定及域命名上下文检测通过。"),
    ).toBeNull();
    expect(screen.queryByLabelText("AD 密码")).toBeNull();
  });
  it("retries uncertain test submission with original key after navigation", async () => {
    let attempts = 0;
    override = (url) =>
      url === "/api/domains/test"
        ? ++attempts === 1
          ? Promise.reject(Error("lost"))
          : response({ task: task(), replayed: true })
        : undefined;
    await detail();
    click("检测已保存连接");
    await screen.findByLabelText("操作者当前密码");
    proof();
    click("确认检测保存版本");
    await screen.findByRole("button", { name: "使用原幂等键核对检测" });
    const key = body("/api/domains/test").idempotencyKey;
    proof();
    click("使用原幂等键核对检测");
    await screen.findByText("等待执行");
    expect(body("/api/domains/test").idempotencyKey).toBe(key);
    expect(calls("/api/domains/test")).toHaveLength(2);
  });
  it("recovers the latest saved pending task on detail without resubmitting", async () => {
    override = (url) =>
      url.startsWith("/api/domains/detail")
        ? response({
            connection: connection({
              connectionState: "testing",
              latestTaskUUID: taskID,
            }),
          })
        : undefined;
    await detail();
    await screen.findByText("等待执行");
    expect(calls("/api/domains/test")).toHaveLength(0);
  });
  it("cancellation request stays pending until real terminal state and allows completed race", async () => {
    let state: TaskState = "running";
    override = (url) =>
      url.startsWith("/api/domains/detail")
        ? response({
            connection: connection({
              connectionState: "testing",
              latestTaskUUID: taskID,
            }),
          })
        : url.startsWith("/api/domains/test-result")
          ? response({
              task: task(state),
              diagnostic: state === "succeeded" ? diagnostic() : null,
            })
          : url === "/api/tasks/cancel"
            ? ((state = "cancel_requested"), response({ task: task(state) }))
            : undefined;
    await detail();
    await screen.findByRole("button", { name: "请求取消检测" });
    click("请求取消检测");
    proof();
    click("确认请求取消检测");
    await screen.findByText("已请求取消，待确认");
    expect(screen.queryByText("已取消")).toBeNull();
    state = "succeeded";
    click("刷新检测结果");
    await screen.findByText("TLS、凭据绑定及域命名上下文检测通过。");
    expect(calls("/api/tasks/cancel")).toHaveLength(1);
  });
  it("does not show connectivity success for succeeded task without safe evidence", async () => {
    override = (url) =>
      url.startsWith("/api/domains/detail")
        ? response({ connection: connection({ latestTaskUUID: taskID }) })
        : url.startsWith("/api/domains/test-result")
          ? response({ task: task("succeeded"), diagnostic: null })
          : undefined;
    await detail();
    await screen.findByText(/不能确认连接通过/);
    expect(
      screen.queryByText("TLS、凭据绑定及域命名上下文检测通过。"),
    ).toBeNull();
  });
  it("renders executor loss honestly with no automatic bind/recovery", async () => {
    override = (url) =>
      url.startsWith("/api/domains/detail")
        ? response({ connection: connection({ latestTaskUUID: taskID }) })
        : url.startsWith("/api/domains/test-result")
          ? response({
              task: task("failed", { error: "executor_lost" }),
              diagnostic: null,
            })
          : undefined;
    await detail();
    await screen.findByText(/执行器失联，外部检测结果不确定/);
    expect(calls("/api/domains/test")).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "创建恢复任务" })).toBeNull();
  });
  it("requires canonical typed deletion and lossless revision; unknown reply blocks repeats", async () => {
    await detail();
    click("删除域连接");
    await screen.findByRole("heading", { name: "删除本地域连接" });
    proof();
    fill("输入域名确认删除", "other.invalid");
    fireEvent.submit(
      screen.getByLabelText("输入域名确认删除").closest("form")!,
    );
    expect(calls("/api/domains/delete")).toHaveLength(0);
    fill("输入域名确认删除", "synthetic.invalid");
    override = (url) =>
      url === "/api/domains/delete" ? Promise.reject(Error("lost")) : undefined;
    click("确认删除本地连接");
    await screen.findByText(/删除结果尚未确认或配置已改变/);
    expect(body("/api/domains/delete").expectedRevision).toBe(
      "9007199254740993",
    );
    expect(
      screen.getByRole("button", { name: "确认删除本地连接" }),
    ).toBeDisabled();
  });
  it("clears proof and preserves isolated recovery on expired session during submission", async () => {
    const lost = vi.fn();
    override = (url) =>
      url === "/api/domains/test"
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    render(<DomainsWorkspace profile={profile} sessionChanged={lost} />);
    click(
      (await screen.findByRole("button", { name: "查看 synthetic.invalid" }))
        .textContent!,
    );
    await screen.findByRole("heading", {
      name: "域连接详情：synthetic.invalid",
    });
    click("检测已保存连接");
    await screen.findByLabelText("操作者当前密码");
    proof();
    click("确认检测保存版本");
    await waitFor(() => expect(lost).toHaveBeenCalledOnce());
    expect(sessionStorage.length).toBe(1);
    expect(readDomainIntent({ ...profile, ID: 2 })).toBeNull();
    expect(readDomainIntent({ ...profile, csrfToken: "renewed" })?.kind).toBe(
      "test",
    );
    expect(calls("/api/domains/test")).toHaveLength(1);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
  });
  it("Back removes domain secrets and ignores stale detail responses", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await revealNavigation("域连接");
    click("域连接");
    await screen.findByRole("table", { name: "获授权的域连接" });
    click("新增域连接");
    fill("AD 密码", "temporary secret");
    const sessionReads = calls("/api/auth/me").length;
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#domains"));
    await screen.findByRole("table", { name: "获授权的域连接" });
    expect(calls("/api/auth/me").length).toBeGreaterThan(sessionReads);
    expect(screen.queryByLabelText("AD 密码")).toBeNull();
    expect(window.location.href).not.toContain("temporary");
    act(() => window.history.forward());
    await waitFor(() => expect(window.location.hash).toBe("#domains/create"));
    await screen.findByRole("table", { name: "获授权的域连接" });
    expect(screen.queryByLabelText("AD 密码")).toBeNull();
    expect(screen.queryByDisplayValue("temporary secret")).toBeNull();
  });
  it("filters are literal, page changes reset correctly and empty scope is distinct from error", async () => {
    await start();
    fill("域或域控关键词", "%_");
    fill("域名筛选", "synthetic.invalid");
    click("查询域连接");
    await waitFor(() =>
      expect(calls("/api/domains").at(-1)![0]).toContain("filterKeyword=%25_"),
    );
    click("重置筛选");
    await waitFor(() =>
      expect(calls("/api/domains").at(-1)![0]).not.toContain("filterKeyword"),
    );
    override = (url) =>
      url.startsWith("/api/domains?") ? response(list([])) : undefined;
    click("刷新域连接");
    await screen.findByText(/当前筛选下没有获授权的域连接/);
  });
});

describe("domain navigation and revision isolation", () => {
  it("does not revive an earlier list response after changing the filter", async () => {
    let resolve!: (r: Response) => void;
    override = (url) =>
      url.startsWith("/api/domains?")
        ? url.includes("filterKeyword=current")
          ? response(list([connection({ domain: "current.invalid" })]))
          : new Promise((r) => (resolve = r))
        : undefined;
    render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByLabelText("域或域控关键词");
    fill("域或域控关键词", "current");
    click("查询域连接");
    await screen.findByRole("button", { name: "查看 current.invalid" });
    await act(async () =>
      resolve(await response(list([connection({ domain: "stale.invalid" })]))),
    );
    expect(screen.queryByText("stale.invalid")).toBeNull();
  });
  it("rekeys credential fields and isolates another account's recoverable intent", async () => {
    const view = render(
      <DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("table", { name: "获授权的域连接" });
    click("新增域连接");
    fill("AD 用户名", "SYNTHETIC\\reader");
    fill("AD 密码", "temporary secret");
    saveDomainIntent(profile, {
      kind: "create",
      key: "previous-account-key",
      domain: "synthetic.invalid",
    });
    view.rerender(
      <DomainsWorkspace
        profile={{
          ...profile,
          ID: 2,
          username: "another",
          csrfToken: "another-session",
        }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByRole("table", { name: "获授权的域连接" });
    expect(screen.queryByLabelText("AD 密码")).toBeNull();
    expect(screen.queryByDisplayValue("previous-account-key")).toBeNull();
    expect(sessionStorage.length).toBe(1);
    expect(
      readDomainIntent({ ...profile, ID: 2, username: "another" }),
    ).toBeNull();
    expect(readDomainIntent({ ...profile, csrfToken: "rotated" })?.key).toBe(
      "previous-account-key",
    );
    expect(calls("/api/domains/create")).toHaveLength(0);
  });
  it("lets an explicit current-version test replace stale intent without replaying old configuration", async () => {
    saveDomainIntent(profile, {
      kind: "test",
      domainId: id,
      expectedRevision: "1",
      idempotencyKey: "original-stale-key",
      key: "original-stale-key",
      taskUUID: taskID,
    });
    override = (url) =>
      url.startsWith("/api/domains/test-result")
        ? response({ error: "forbidden" }, 403)
        : undefined;
    await detail();
    click("检测已保存连接");
    await screen.findByRole("button", { name: "改为检测当前保存版本" });
    click("改为检测当前保存版本");
    proof();
    click("确认检测保存版本");
    await waitFor(() => expect(calls("/api/domains/test")).toHaveLength(1));
    expect(body("/api/domains/test").expectedRevision).toBe(
      connection().revision,
    );
    expect(body("/api/domains/test").idempotencyKey).not.toBe(
      "original-stale-key",
    );
  });
  it("uses the creator receipt for a replayed create and respects a deleted receipt", async () => {
    override = (url) =>
      url === "/api/domains/create"
        ? response({
            result: "SUCCESS",
            domainId: id,
            revision: "1",
            requiresResourceAssignment: true,
            replayed: true,
          })
        : url.startsWith("/api/domains/creation")
          ? response({
              receipt: {
                domainId: id,
                domain: "synthetic.invalid",
                revision: "1",
                deleted: true,
                requiresResourceAssignment: true,
              },
            })
          : undefined;
    await create();
    click("保存本地连接");
    await screen.findByText("此登记已删除；回执仅保留历史记录。");
    expect(calls("/api/domains/creation")).toHaveLength(1);
    expect(calls("/api/domains/test")).toHaveLength(0);
  });
});

describe("concurrent domain review invalidation", () => {
  it.each(["delete", "test"] as const)(
    "clears reviewed %s confirmation and proof when a newer configuration arrives",
    async (action) => {
      let current = connection({
        connectionState: "testing",
        latestTaskUUID: taskID,
      });
      override = (url) =>
        url.startsWith("/api/domains/detail")
          ? response({ connection: current })
          : undefined;
      await detail();
      click(action === "delete" ? "删除域连接" : "检测已保存连接");
      if (action === "test") {
        // A pending saved task has no new-test proof form; finish and explicitly
        // open a fresh review before another editor changes the configuration.
        override = (url) =>
          url.startsWith("/api/domains/detail")
            ? response({ connection: current })
            : url.startsWith("/api/domains/test-result")
              ? response({ task: task("failed"), diagnostic: null })
              : undefined;
        await screen.findByRole("button", { name: "刷新检测结果" });
        click("刷新检测结果");
        await screen.findByRole("button", { name: "开始新的手动检测" });
        click("开始新的手动检测");
      }
      await screen.findByLabelText("操作者当前密码");
      proof();
      if (action === "delete") fill("输入域名确认删除", "synthetic.invalid");
      current = connection({
        revision: "9007199254740994",
        dcHostName: "changed.synthetic.invalid",
      });
      click("重新读取域详情");
      await waitFor(() =>
        expect(screen.getByLabelText("操作者当前密码")).toHaveValue(""),
      );
      expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
      if (action === "delete")
        expect(screen.getByLabelText("输入域名确认删除")).toHaveValue("");
      expect(calls(`/api/domains/${action}`)).toHaveLength(0);
    },
  );
});
