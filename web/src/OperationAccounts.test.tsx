import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, afterEach, describe, it, expect, vi } from "vitest";
import OperationAccountsWorkspace from "./OperationAccountsWorkspace";
import App from "./App";
import { marks, labels, type Permission } from "./access-api";
import { PermissionEditor, emptyPermissions } from "./access-common";
import {
  operationAccountAPI,
  operationAccountOperations,
  validOperationAccount,
  validOperationReceipt,
  validAccountLabel,
  type OperationAccount,
  type OperationReceipt,
} from "./operation-account-api";
import {
  discardOperationAccountIntent,
  readOperationAccountIntent,
  saveOperationAccountIntent,
} from "./operation-account-intent";
import type { Profile } from "./api";
const domainId = "synthetic-domain",
  accountId = "synthetic-operation-account",
  revision = "9007199254740993",
  base = "/api/operation-accounts";
const account = (extra: Partial<OperationAccount> = {}): OperationAccount => ({
  accountId,
  domainId,
  domain: "synthetic.invalid",
  label: "部署凭据",
  revision,
  credentialRevision: "1",
  credentialConfigured: true,
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:01Z",
  storageState: "saved",
  verificationState: "unverified",
  ...extra,
});
const receipt = (extra: Partial<OperationReceipt> = {}): OperationReceipt => ({
  result: "SUCCESS",
  operation: "create",
  accountId,
  domainId,
  revision: "1",
  credentialRevision: "1",
  currentRevision: "1",
  currentCredentialRevision: "1",
  replayed: false,
  deleted: false,
  verificationState: "unverified",
  ...extra,
});
const page = (length: number) => ({
  pageIdx: 1,
  pageSize: 20,
  total: length,
  totalPage: length ? 1 : 0,
});
const list = (items: OperationAccount[] = [account()]) => ({
  page: page(items.length),
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
const response = (value: unknown, status = 200) =>
  Promise.resolve({
    ok: status < 400,
    status,
    json: async () => value,
  } as Response);
let profile: Profile,
  current: OperationAccount,
  fetcher: ReturnType<typeof vi.fn>,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined;
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (name: string, value: string) =>
  fireEvent.change(screen.getByLabelText(name, { exact: true }), {
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
  render(
    <OperationAccountsWorkspace profile={profile} sessionChanged={vi.fn()} />,
  );
  await screen.findByRole("table", { name: "获授权的操作账户" });
}
async function detail() {
  await start();
  click("查看 部署凭据");
  await screen.findByRole("heading", { name: "操作账户详情：部署凭据" });
}
async function edit() {
  await detail();
  click("编辑登记");
  await screen.findByRole("heading", { name: "编辑本地登记" });
}
async function create() {
  await start();
  click("新增操作账户");
  await screen.findByRole("heading", { name: "选择已授权的域" });
  await screen.findByRole("button", { name: "选择 synthetic.invalid" });
  click("选择 synthetic.invalid");
  await screen.findByRole("heading", { name: "登记操作账户" });
}
function pair() {
  fill("AD 用户名", "SYNTHETIC\\provided");
  fill("AD 密码", " Synthetic operation secret ");
}
beforeEach(() => {
  discardOperationAccountIntent();
  sessionStorage.clear();
  localStorage.clear();
  window.history.replaceState({}, "", "#account");
  current = account();
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
    csrfToken: "operation-test-session",
  };
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const result = override(url, init);
    if (result) return result;
    const path = url.split("?")[0];
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu: metadata() });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === base) return response(list([current]));
    if (path === `${base}/domains`)
      return response({
        page: page(1),
        domains: [{ domainId, domain: current.domain, revision }],
        exhausted: true,
      });
    if (path === `${base}/detail`) return response({ account: current });
    if (path === `${base}/mutation`)
      return response({ receipt: receipt({ replayed: true }) });
    if (path === `${base}/create`) {
      const sent = JSON.parse(init.body as string);
      current = account({ revision: "1", label: sent.label ?? "" });
      return response(receipt());
    }
    if (path === `${base}/update`) {
      const sent = JSON.parse(init.body as string);
      current = account({
        ...current,
        label: sent.label ?? current.label,
        revision: String(BigInt(current.revision) + 1n),
        credentialRevision: sent.username
          ? String(BigInt(current.credentialRevision) + 1n)
          : current.credentialRevision,
      });
      return response(
        receipt({
          operation: "update",
          revision: current.revision,
          credentialRevision: current.credentialRevision,
          currentRevision: current.revision,
          currentCredentialRevision: current.credentialRevision,
        }),
      );
    }
    if (path === `${base}/delete`)
      return response(
        receipt({
          operation: "delete",
          revision: String(BigInt(current.revision) + 1n),
          currentRevision: String(BigInt(current.revision) + 1n),
          credentialRevision: "2",
          currentCredentialRevision: "2",
          deleted: true,
        }),
      );
    throw new Error(`Unexpected request ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  vi.unstubAllGlobals();
  discardOperationAccountIntent();
});
describe("operation account strict safe projections", () => {
  it("accepts only saved/unverified metadata and exact canonical revisions", () => {
    expect(validOperationAccount(account())).toBe(true);
    for (const extra of [
      { username: "SECRET" },
      { password: "SECRET" },
      { userInfo: {} },
      { ciphertext: "x" },
      { credentialConfigured: false },
      { label: null },
      { storageState: "success" },
      { verificationState: "verified" },
      { revision: 1 },
      { revision: "01" },
      { revision: "9223372036854775808" },
      { credentialRevision: "9007199254740994" },
      { domain: "UPPER.invalid" },
      { updatedAt: "2026-10-06T00:00:00Z" },
    ])
      expect(validOperationAccount({ ...account(), ...extra })).toBe(false);
  });
  it("pins old and current receipt revisions without mistaking a replay for current state", () => {
    expect(
      validOperationReceipt(
        receipt({
          replayed: true,
          currentRevision: "3",
          currentCredentialRevision: "2",
          deleted: true,
        }),
      ),
    ).toBe(true);
    for (const extra of [
      { username: "SECRET" },
      { revision: "0" },
      { currentRevision: "0" },
      { currentCredentialRevision: "2" },
      { operation: "delete", deleted: false },
      { verificationState: "verified" },
      { replayed: 1 },
    ])
      expect(validOperationReceipt({ ...receipt(), ...extra })).toBe(false);
  });
  it("validates label bounds, Unicode, no control characters and explicit clearing", () => {
    for (const value of ["", "常".repeat(50), "😀".repeat(50)])
      expect(validAccountLabel(value)).toBe(true);
    for (const value of [" x", "x ", "\u0085", "\ud800", "x".repeat(51), null])
      expect(validAccountLabel(value)).toBe(false);
  });
  it("rejects additional secret fields at wrappers and malformed page totals", async () => {
    for (const value of [
      { ...list(), password: "SECRET" },
      { ...list(), page: { ...page(1), total: 2 } },
      { ...list(), exhausted: false },
      { ...list(), List: [account(), account()] },
    ]) {
      override = () => response(value);
      await expect(
        operationAccountAPI.list("pageIdx=1", new AbortController().signal),
      ).rejects.toMatchObject({ code: "invalid_response" });
    }
    override = () => response({ account: account(), username: "SECRET" });
    await expect(
      operationAccountAPI.detail(accountId, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("rejects foreign detail, receipt replay flag and mutation identity mismatches", async () => {
    override = () => response({ account: account({ accountId: "foreign" }) });
    await expect(
      operationAccountAPI.detail(accountId, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () => response({ receipt: receipt() });
    await expect(
      operationAccountAPI.receipt("original-key", new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
    override = () => response(receipt({ domainId: "foreign" }));
    await expect(
      operationAccountAPI.create(
        {
          domainId,
          expectedDomainRevision: revision,
          username: "X\\a",
          password: "x",
          idempotencyKey: "original-key",
        },
        { actorPassword: "x", totpCode: "123456" },
        "csrf",
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("accepts empty and out-of-range pages and bounded all results", async () => {
    for (const value of [
      list([]),
      {
        ...list([]),
        page: { pageIdx: 2, pageSize: 20, total: 1, totalPage: 1 },
      },
      { ...list(), page: { ...page(1), pageSize: -1 } },
    ]) {
      override = () => response(value);
      await expect(
        operationAccountAPI.list("", new AbortController().signal),
      ).resolves.toEqual(value);
    }
  });
  it("reconstructs only safe intent IDs and keys and isolates actors", () => {
    sessionStorage.setItem(
      "adtr.operation-account-intent.v1:1:admin",
      JSON.stringify({
        owner: "1:admin",
        intent: {
          kind: "update",
          key: "original-key",
          domainId,
          accountId,
          username: "SECRET",
          password: "SECRET",
          label: "SECRET",
        },
      }),
    );
    expect(readOperationAccountIntent(profile)).toEqual({
      kind: "update",
      key: "original-key",
      domainId,
      accountId,
    });
    saveOperationAccountIntent(profile, {
      kind: "delete",
      key: "original-key",
      domainId,
      accountId,
      password: "SECRET",
    } as never);
    expect(
      sessionStorage.getItem("adtr.operation-account-intent.v1:1:admin"),
    ).not.toContain("SECRET");
    const other = { ...profile, ID: 2 };
    expect(readOperationAccountIntent(other)).toBeNull();
    saveOperationAccountIntent(other, {
      kind: "create",
      key: "other-actor-key",
      domainId,
    });
    expect(readOperationAccountIntent(profile)?.key).toBe("original-key");
    expect(readOperationAccountIntent(other)?.key).toBe("other-actor-key");
    expect(sessionStorage.length).toBe(2);
  });
});
describe("operation account permissions and lifecycle", () => {
  it("round trips all thirteen marks with operation accounts denied by default", () => {
    expect(marks).toHaveLength(13);
    expect(
      emptyPermissions().find((value) => value.mark === "operation_accounts")
        ?.auth,
    ).toEqual({ readable: false, writeable: false });
    const change = vi.fn();
    render(
      <PermissionEditor
        value={emptyPermissions()}
        metadata={metadata()}
        onChange={change}
      />,
    );
    fireEvent.click(screen.getByLabelText("管理操作账户：读取"));
    expect(
      change.mock.calls[0][0].find(
        (value: { mark: string }) => value.mark === "operation_accounts",
      ).auth.readable,
    ).toBe(true);
  });
  it("waits for explicit navigation and menu/path grants before calling account APIs", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    expect(calls(base)).toHaveLength(0);
    override = (url) =>
      url === "/api/access/menu"
        ? response({
            menu: metadata().filter(
              (value) => value.mark !== "operation_accounts",
            ),
          })
        : undefined;
    click("管理操作账户");
    await screen.findByText("服务器未授予操作账户读取权限。");
    expect(calls(base)).toHaveLength(0);
  });
  it("does not call the broad domain route for operation-only readers", async () => {
    override = (url) =>
      url === "/api/access/menu"
        ? response({
            menu: metadata().filter(
              (value) => value.mark === "operation_accounts",
            ),
          })
        : undefined;
    await create();
    expect(calls("/api/domains")).toHaveLength(0);
    expect(body("/api/access/check").paths).toEqual(operationAccountOperations);
  });
  it("registers provided credentials using selected parent revision and fresh proof", async () => {
    await create();
    fill("登记标签（可选）", "登记标签");
    pair();
    proof();
    click("保存本地登记");
    await screen.findByRole("heading", { name: "操作账户详情：登记标签" });
    expect(body(`${base}/create`)).toEqual({
      domainId,
      expectedDomainRevision: revision,
      idempotencyKey: expect.any(String),
      label: "登记标签",
      username: "SYNTHETIC\\provided",
      password: " Synthetic operation secret ",
      actorPassword: "Synthetic Actor Password 123",
      totpCode: "123456",
    });
    expect(calls(`${base}/create`)[0][1].headers["X-CSRF-Token"]).toBe(
      profile.csrfToken,
    );
    expect(sessionStorage.length).toBe(0);
    expect(localStorage.length).toBe(0);
    expect(document.body.textContent).not.toContain(
      "Synthetic operation secret",
    );
    expect(screen.queryByLabelText("AD 密码")).not.toBeInTheDocument();
  });
  it("preserves credentials for label-only edit and sends an explicit empty label to clear", async () => {
    await edit();
    expect(screen.queryByLabelText("AD 用户名")).not.toBeInTheDocument();
    fill("登记标签（可选）", "");
    proof();
    click("保存登记修改");
    await waitFor(() => expect(calls(`${base}/update`)).toHaveLength(1));
    expect(body(`${base}/update`)).toMatchObject({
      accountId,
      expectedRevision: revision,
      label: "",
    });
    expect(body(`${base}/update`)).not.toHaveProperty("username");
    expect(body(`${base}/update`)).not.toHaveProperty("password");
  });
  it("requires paired replacement and omits unchanged label", async () => {
    await edit();
    fireEvent.click(screen.getByLabelText("替换已保存的凭据"));
    expect(screen.getByLabelText("AD 用户名")).toHaveValue("");
    expect(screen.getByLabelText("AD 密码")).toHaveValue("");
    pair();
    proof();
    click("保存登记修改");
    await waitFor(() => expect(calls(`${base}/update`)).toHaveLength(1));
    expect(body(`${base}/update`)).toMatchObject({
      username: "SYNTHETIC\\provided",
      password: " Synthetic operation secret ",
    });
    expect(body(`${base}/update`)).not.toHaveProperty("label");
  });
  it("rejects malformed pairs, label bounds and unchanged edit before POST", async () => {
    await create();
    fill("AD 用户名", "plainuser");
    fill("AD 密码", "x");
    proof();
    click("保存本地登记");
    await screen.findByText(/AD 用户名须为/);
    expect(calls(`${base}/create`)).toHaveLength(0);
    fill("登记标签（可选）", " bad ");
    pair();
    click("保存本地登记");
    await screen.findByText(/标签最多/);
    expect(calls(`${base}/create`)).toHaveLength(0);
  });
  it("clears entered secrets on cancellation, replacement toggle and back navigation", async () => {
    await edit();
    fireEvent.click(screen.getByLabelText("替换已保存的凭据"));
    pair();
    proof();
    fireEvent.click(screen.getByLabelText("替换已保存的凭据"));
    fireEvent.click(screen.getByLabelText("替换已保存的凭据"));
    expect(screen.getByLabelText("AD 密码")).toHaveValue("");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    pair();
    click("取消");
    await screen.findByRole("button", { name: "编辑登记" });
    click("编辑登记");
    await screen.findByRole("heading", { name: "编辑本地登记" });
    expect(screen.queryByLabelText("AD 密码")).not.toBeInTheDocument();
    proof();
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    await screen.findByRole("table", { name: "获授权的操作账户" });
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  });
  it("pins CAS revision, blocks stale forms, and clears all proof fields on refresh", async () => {
    await edit();
    fill("登记标签（可选）", "新标签");
    proof();
    override = (url) =>
      url === `${base}/update`
        ? response({ error: "revision_conflict" }, 409)
        : undefined;
    click("保存登记修改");
    await screen.findByText(/此表单已停止提交/);
    expect(screen.getByRole("button", { name: "保存登记修改" })).toBeDisabled();
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    current = account({ revision: "9007199254740994", label: "另一编辑者" });
    click("重新读取当前登记");
    await screen.findByRole("heading", { name: "操作账户详情：另一编辑者" });
    expect(screen.getByLabelText("登记标签（可选）")).toHaveValue("另一编辑者");
    proof();
    fill("登记标签（可选）", "更新");
    override = () => undefined;
    click("保存登记修改");
    await waitFor(() => expect(calls(`${base}/update`)).toHaveLength(2));
    expect(body(`${base}/update`).expectedRevision).toBe("9007199254740994");
  });
  it("retains one safe key after uncertain create and resolves receipt before fetching current state", async () => {
    await create();
    pair();
    proof();
    override = (url) =>
      url === `${base}/create`
        ? Promise.reject(Error("lost"))
        : url.startsWith(`${base}/mutation?`)
          ? response({ error: "not_found" }, 404)
          : undefined;
    click("保存本地登记");
    await screen.findByRole("heading", { name: "核对未确认的操作" });
    const key = body(`${base}/create`).idempotencyKey;
    expect(readOperationAccountIntent(profile)).toEqual({
      kind: "create",
      key,
      domainId,
    });
    expect(
      sessionStorage.getItem("adtr.operation-account-intent.v1:1:admin"),
    ).not.toMatch(/provided|secret|actorPassword|totpCode|label/);
    expect(screen.queryByLabelText("AD 密码")).not.toBeInTheDocument();
    expect(calls(`${base}/create`)).toHaveLength(1);
    await screen.findByText(/不能据此确认删除/);
    override = () => undefined;
    click("查询原操作回执");
    await screen.findByRole("heading", { name: "操作账户详情：部署凭据" });
    expect(screen.getByText(/提交版本：1/)).toBeInTheDocument();
    expect(calls(`${base}/detail`)).toHaveLength(1);
    expect(readOperationAccountIntent(profile)).toBeNull();
  });
  it("does not infer deletion from 404 or replace the original mutation key", async () => {
    await detail();
    click("删除本地登记");
    await screen.findByRole("heading", { name: "删除本地操作账户登记" });
    fill("输入完整账户 ID 确认", accountId);
    proof();
    override = (url) =>
      url === `${base}/delete`
        ? Promise.reject(Error("lost"))
        : url.startsWith(`${base}/mutation?`)
          ? response({ error: "not_found" }, 404)
          : undefined;
    click("确认删除本地登记");
    await screen.findByRole("heading", { name: "核对未确认的操作" });
    await screen.findByText(/不能据此确认删除/);
    const key = body(`${base}/delete`).idempotencyKey;
    click("查询原操作回执");
    await waitFor(() => expect(calls(`${base}/mutation`)).toHaveLength(2));
    expect(readOperationAccountIntent(profile)?.key).toBe(key);
    expect(calls(`${base}/delete`)).toHaveLength(1);
    expect(screen.queryByText(/当前登记已删除/)).not.toBeInTheDocument();
  });
  it.each(["immediate", "delayed"] as const)(
    "restores the creation route and original receipt with an %s response",
    async (timing) => {
      saveOperationAccountIntent(profile, {
        kind: "create",
        key: "reload-operation-key",
        domainId,
      });
      window.history.replaceState({}, "", "#operation-accounts/create");
      let resolve!: (value: Response) => void;
      const recovered = { receipt: receipt({ replayed: true }) };
      override = (url) =>
        url.startsWith(`${base}/mutation?`)
          ? timing === "immediate"
            ? response(recovered)
            : new Promise<Response>((done) => (resolve = done))
          : undefined;
      render(<App />);
      if (timing === "immediate") {
        // Recovery may finish before the caller observes the restored route.
        await screen.findByLabelText("账户 ID");
      } else {
        await screen.findByRole("heading", { name: "核对未确认的操作" });
        expect(screen.queryByLabelText("账户 ID")).toBeNull();
        expect(readOperationAccountIntent(profile)?.key).toBe(
          "reload-operation-key",
        );
        expect(
          screen.getByRole("button", { name: "新增操作账户" }),
        ).toBeDisabled();
        expect(window.location.hash).toBe("#operation-accounts/create");
      }
      expect(
        screen.getByRole("button", { name: "管理操作账户" }),
      ).toHaveAttribute("aria-current", "page");
      expect(
        screen.getByRole("heading", { name: "管理操作账户" }),
      ).toBeVisible();
      if (timing === "delayed")
        await act(async () => resolve(await response(recovered)));
      expect(await screen.findByLabelText("账户 ID")).toHaveValue(accountId);
      await screen.findByRole("heading", { name: "操作账户详情：部署凭据" });
      expect(window.location.hash).toBe("#operation-accounts/detail");
      expect(calls("/api/auth/me")).toHaveLength(1);
      expect(calls(`${base}/mutation`)).toHaveLength(1);
      expect(calls(`${base}/mutation`)[0][0]).toBe(
        `${base}/mutation?idempotencyKey=reload-operation-key`,
      );
      expect(calls(`${base}/create`)).toHaveLength(0);
      expect(readOperationAccountIntent(profile)).toBeNull();
      expect(sessionStorage.length).toBe(0);
      expect(screen.queryByLabelText("AD 密码")).toBeNull();
      expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    },
  );
  it("ignores late mutations after cancel and keeps receipt recovery active", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === `${base}/create`
        ? new Promise((done) => {
            resolve = done;
          })
        : url.startsWith(`${base}/mutation?`)
          ? response({ error: "not_found" }, 404)
          : undefined;
    await create();
    pair();
    proof();
    click("保存本地登记");
    click("取消");
    await screen.findByRole("heading", { name: "核对未确认的操作" });
    await act(async () => resolve(await response(receipt())));
    expect(
      screen.getByRole("heading", { name: "核对未确认的操作" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: /操作账户详情/ }),
    ).not.toBeInTheDocument();
    expect(readOperationAccountIntent(profile)).not.toBeNull();
  });
  it("recovers a persisted update receipt with later deletion without mounting an editor", async () => {
    saveOperationAccountIntent(profile, {
      kind: "update",
      key: "original-update-key",
      domainId,
      accountId,
    });
    override = (url) =>
      url.startsWith(`${base}/mutation?`)
        ? response({
            receipt: receipt({
              operation: "update",
              replayed: true,
              revision: "2",
              currentRevision: "4",
              credentialRevision: "1",
              currentCredentialRevision: "3",
              deleted: true,
            }),
          })
        : undefined;
    await start();
    await screen.findByText(/当前登记已删除/);
    expect(calls(`${base}/detail`)).toHaveLength(0);
    expect(calls(`${base}/update`)).toHaveLength(0);
    expect(readOperationAccountIntent(profile)).toBeNull();
  });
  it("rejects mismatched recovery identity without discarding the saved intent", async () => {
    saveOperationAccountIntent(profile, {
      kind: "delete",
      key: "original-delete-key",
      domainId,
      accountId,
    });
    override = (url) =>
      url.startsWith(`${base}/mutation?`)
        ? response({ receipt: receipt({ replayed: true }) })
        : undefined;
    await start();
    await screen.findByText(/服务器结果无法安全确认/);
    expect(readOperationAccountIntent(profile)?.kind).toBe("delete");
    expect(screen.queryByText(/当前登记已删除/)).not.toBeInTheDocument();
  });
  it("clears proof, credential fields and saved intent on session loss", async () => {
    const lost = vi.fn();
    render(
      <OperationAccountsWorkspace profile={profile} sessionChanged={lost} />,
    );
    await screen.findByRole("button", { name: "新增操作账户" });
    click("新增操作账户");
    await screen.findByRole("button", { name: "选择 synthetic.invalid" });
    click("选择 synthetic.invalid");
    pair();
    proof();
    override = (url) =>
      url === `${base}/create`
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    click("保存本地登记");
    await waitFor(() => expect(lost).toHaveBeenCalledOnce());
    expect(screen.queryByLabelText("AD 密码")).not.toBeInTheDocument();
    expect(readOperationAccountIntent(profile)).toBeNull();
  });
  it("supports literal keyword/domain filters, reset and authorized empty results", async () => {
    await start();
    fill("域名或登记标签关键词", "%_");
    fill("所属域筛选", "synthetic.invalid,other.invalid");
    click("查询操作账户");
    await waitFor(() =>
      expect(calls(base).at(-1)![0]).toContain("filterKeyword=%25_"),
    );
    expect(calls(base).at(-1)![0]).toContain(
      "filterDomain=synthetic.invalid&filterDomain=other.invalid",
    );
    override = (url) =>
      url.startsWith(`${base}?`) ? response(list([])) : undefined;
    click("重置筛选");
    await screen.findByText("当前筛选下没有获授权的操作账户。");
    expect(calls(base).at(-1)![0]).not.toContain("filterKeyword");
    expect(screen.queryByLabelText("连接状态")).not.toBeInTheDocument();
    expect(screen.queryByText("导出")).not.toBeInTheDocument();
  });
  it("shows tenant expiry and picker errors instead of fabricating empty results", async () => {
    override = (url) =>
      url.startsWith(`${base}?`)
        ? response({ error: "tenant_expired" }, 403)
        : undefined;
    render(
      <OperationAccountsWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByText("租户已过期，暂不能管理操作账户。");
    expect(
      screen.queryByText("当前筛选下没有获授权的操作账户。"),
    ).not.toBeInTheDocument();
  });
  it("keeps the original intent across receipt authentication loss for the same actor", async () => {
    const lost = vi.fn();
    const first = render(
      <OperationAccountsWorkspace profile={profile} sessionChanged={lost} />,
    );
    await screen.findByRole("button", { name: "新增操作账户" });
    click("新增操作账户");
    await screen.findByRole("button", { name: "选择 synthetic.invalid" });
    click("选择 synthetic.invalid");
    pair();
    proof();
    override = (url) =>
      url === `${base}/create`
        ? Promise.reject(Error("lost committed response"))
        : url.startsWith(`${base}/mutation?`)
          ? response({ error: "unauthenticated" }, 401)
          : undefined;
    click("保存本地登记");
    await waitFor(() => expect(lost).toHaveBeenCalledOnce());
    const key = body(`${base}/create`).idempotencyKey;
    expect(readOperationAccountIntent(profile)?.key).toBe(key);
    expect(screen.queryByLabelText("AD 密码")).not.toBeInTheDocument();
    first.unmount();
    override = () => undefined;
    render(
      <OperationAccountsWorkspace
        profile={{ ...profile, csrfToken: "new-login" }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByRole("heading", { name: "操作账户详情：部署凭据" });
    expect(calls(`${base}/create`)).toHaveLength(1);
    expect(calls(`${base}/mutation`).at(-1)![0]).toContain(key);
    expect(readOperationAccountIntent(profile)).toBeNull();
  });
  it("paginates scoped rows and resets page on a new filter", async () => {
    override = (url) => {
      if (!url.startsWith(`${base}?`)) return;
      const query = new URL(url, "https://example.invalid").searchParams;
      const index = Number(query.get("pageIdx"));
      return response({
        page: { pageIdx: index, pageSize: 20, total: 21, totalPage: 2 },
        List: Array.from({ length: index === 1 ? 20 : 1 }, (_, offset) =>
          account({
            accountId: `account-${index}-${offset}`,
            label: `Row ${index}-${offset}`,
          }),
        ),
        exhausted: index === 2,
      });
    };
    await start();
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    click("下一页");
    await screen.findByRole("button", { name: "查看 Row 2-0" });
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    fill("域名或登记标签关键词", "Row");
    click("查询操作账户");
    await screen.findByRole("button", { name: "查看 Row 1-0" });
    expect(calls(base).at(-1)![0]).toContain("pageIdx=1");
  });
  it("shows a scoped empty picker and safe errors without enabling a free-form parent ID", async () => {
    await start();
    override = (url) =>
      url.startsWith(`${base}/domains?`)
        ? response({ page: page(0), domains: [], exhausted: true })
        : undefined;
    click("新增操作账户");
    await screen.findByText(/没有可选的已授权域/);
    expect(
      screen.queryByRole("button", { name: /^选择 / }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("域 ID")).not.toBeInTheDocument();
    override = (url) =>
      url.startsWith(`${base}/domains?`)
        ? response({ error: "forbidden", message: "SECRET" }, 403)
        : undefined;
    click("刷新可选域");
    await screen.findByRole("alert");
    expect(document.body.textContent).not.toContain("SECRET");
    expect(screen.queryByText(/没有可选的已授权域/)).not.toBeInTheDocument();
  });
  it("clears rejected proof and credential inputs before a same-key explicit retry", async () => {
    await create();
    pair();
    proof();
    override = (url) =>
      url === `${base}/create`
        ? response({ error: "invalid_credentials" }, 401)
        : undefined;
    click("保存本地登记");
    await screen.findByRole("alert");
    const key = body(`${base}/create`).idempotencyKey;
    expect(screen.getByLabelText("AD 密码")).toHaveValue("");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(readOperationAccountIntent(profile)).toBeNull();
    override = () => undefined;
    pair();
    proof();
    click("保存本地登记");
    await waitFor(() => expect(calls(`${base}/create`)).toHaveLength(2));
    expect(body(`${base}/create`).idempotencyKey).toBe(key);
  });
  it.each(["edit", "delete"])(
    "routes refresh of pending %s to the original receipt without a new form",
    async (kind) => {
      await detail();
      click(kind === "edit" ? "编辑登记" : "删除本地登记");
      await screen.findByRole("heading", {
        name: kind === "edit" ? "编辑本地登记" : "删除本地操作账户登记",
      });
      if (kind === "edit") fill("登记标签（可选）", "Updated");
      else fill("输入完整账户 ID 确认", accountId);
      proof();
      const path = `${base}/${kind === "edit" ? "update" : "delete"}`;
      override = (url) =>
        url === path
          ? new Promise<Response>(() => {})
          : url.startsWith(`${base}/mutation?`)
            ? response({ error: "not_found" }, 404)
            : undefined;
      click(kind === "edit" ? "保存登记修改" : "确认删除本地登记");
      const key = body(path).idempotencyKey;
      click("重新读取当前登记");
      await screen.findByRole("heading", { name: "核对未确认的操作" });
      expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
      expect(readOperationAccountIntent(profile)?.key).toBe(key);
      expect(calls(path)).toHaveLength(1);
    },
  );
});
