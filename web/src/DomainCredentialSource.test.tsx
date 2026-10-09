import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DomainsWorkspace from "./DomainsWorkspace";
import App from "./App";
import type { Profile } from "./api";
import { labels, marks } from "./access-api";
import {
  sourceAPI,
  validSourceDetail,
  validSourceReceipt,
  type SourceDetail,
  type SourceReceipt,
} from "./domain-source-api";
import {
  discardSourceIntent,
  readSourceIntent,
  saveSourceIntent,
} from "./domain-source-intent";
import { discardDomainIntent } from "./domain-intent";
const domainId = "synthetic-domain",
  accountId = "synthetic-account",
  base = "/api/domains/credential-source";
const profile: Profile = {
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
  csrfToken: "source-session",
};
const detail = (extra: Partial<SourceDetail> = {}): SourceDetail => ({
  domainId,
  revision: "3",
  connectionCredentialGeneration: "2",
  credentialSource: "unconfigured",
  credentialConfigured: false,
  testEligible: false,
  reference: null,
  ...extra,
});
const receipt = (extra: Partial<SourceReceipt> = {}): SourceReceipt => ({
  result: "SUCCESS",
  operation: "reference",
  domainId,
  revision: "4",
  connectionCredentialGeneration: "3",
  credentialSource: "operation_account",
  replayed: false,
  deleted: false,
  currentRevision: "4",
  currentConnectionCredentialGeneration: "3",
  currentCredentialSource: "operation_account",
  ...extra,
});
const connection = () => ({
  domainId,
  domain: "synthetic.invalid",
  dcHostName: "dc.synthetic.invalid",
  ldapAddr: "",
  port: "389",
  mode: "starttls",
  revision: current.revision,
  credentialRevision: current.connectionCredentialGeneration,
  connectionCredentialGeneration: current.connectionCredentialGeneration,
  credentialSource: current.credentialSource,
  credentialConfigured: current.credentialConfigured,
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:00Z",
  connectionState: "unverified",
  lastDiagnostic: null,
  latestTaskUUID: "",
});
const account = {
  accountId,
  domainId,
  domain: "synthetic.invalid",
  label: "同域测试账户",
  revision: "2",
  credentialRevision: "1",
  credentialConfigured: true,
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:00Z",
  storageState: "saved",
  verificationState: "unverified",
};
const grant = {
  accountId,
  domainId,
  purpose: "domain.connection_test",
  explicitlyGranted: true,
  eligible: true,
  grantRevision: "7",
  accountCredentialRevision: "1",
  consumerEnabled: true,
};
const list = (items: unknown[]) => ({
  page: {
    pageIdx: 1,
    pageSize: 20,
    total: items.length,
    totalPage: items.length ? 1 : 0,
  },
  List: items,
  exhausted: true,
});
const response = (data: unknown, status = 200, actor: string | null = "1") =>
  Promise.resolve({
    ok: status < 400,
    status,
    headers: new Headers(actor === null ? {} : { "X-ADTR-User-ID": actor }),
    json: async () => data,
  } as Response);
let current: SourceDetail,
  fetcher: ReturnType<typeof vi.fn>,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined;
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (name: string, value: string) =>
  fireEvent.change(screen.getByLabelText(name, { exact: true }), {
    target: { value },
  });
const proof = () => {
  fill("操作者当前密码", "Synthetic Actor Secret");
  fill("未使用的认证器验证码", "123456");
};
const calls = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
async function start() {
  const root = render(
    <DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />,
  );
  await screen.findByRole("button", { name: "查看 synthetic.invalid" });
  click("查看 synthetic.invalid");
  await screen.findByRole("button", { name: "管理凭据来源" });
  click("管理凭据来源");
  await screen.findByLabelText("新的凭据来源");
  return root;
}
async function selectReference() {
  fill("新的凭据来源", "reference");
  await screen.findByRole("button", { name: "选择账户 同域测试账户" });
  click("选择账户 同域测试账户");
  await screen.findByLabelText("操作者当前密码");
}
beforeEach(() => {
  discardSourceIntent(profile);
  discardDomainIntent();
  sessionStorage.clear();
  localStorage.clear();
  current = detail();
  override = () => undefined;
  window.history.replaceState({}, "", "#account");
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const out = override(url, init);
    if (out) return out;
    const path = url.split("?")[0];
    if (path === "/api/access/menu")
      return response({
        menu: marks.map((mark) => ({
          mark,
          name: labels[mark],
          auth: { readable: true, writeable: true },
        })),
      });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/domains") return response(list([connection()]));
    if (path === "/api/domains/detail")
      return response({ connection: connection() });
    if (path === base) return response(current);
    if (path === "/api/operation-accounts") return response(list([account]));
    if (path === "/api/operation-accounts/detail") return response({ account });
    if (path === "/api/credential-use/effective") return response(grant);
    if (path === `${base}/reference`) return response(receipt());
    if (path === `${base}/custom`)
      return response(
        receipt({
          operation: "custom",
          credentialSource: "custom",
          currentCredentialSource: "custom",
        }),
      );
    if (path === `${base}/detach`)
      return response(
        receipt({
          operation: "detach",
          credentialSource: "unconfigured",
          currentCredentialSource: "unconfigured",
        }),
      );
    if (path === `${base}/mutation`)
      return response({ error: "not_found" }, 404);
    if (path === "/api/domains/create-unconfigured")
      return response({
        result: "SUCCESS",
        domainId,
        revision: "1",
        requiresResourceAssignment: true,
        replayed: false,
      });
    throw Error(`Unexpected ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
describe("source navigation through the app", () => {
  it("aborts a delayed source read on real Back/Forward and restores only the domain list", async () => {
    override = (url) =>
      url === "/api/auth/me" ? response(profile) : undefined;
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    click("域连接");
    await screen.findByRole("button", { name: "查看 synthetic.invalid" });
    click("查看 synthetic.invalid");
    await screen.findByRole("button", { name: "管理凭据来源" });
    click("管理凭据来源");
    await screen.findByLabelText("新的凭据来源");
    await selectReference();
    proof();
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === "/api/auth/me"
        ? response(profile)
        : url.split("?")[0] === base
          ? new Promise<Response>((done) => {
              resolve = done;
            })
          : undefined;
    click("重新读取凭据来源");
    await waitFor(() => expect(calls(base)).toHaveLength(2));
    const delayedSignal = calls(base).at(-1)![1].signal as AbortSignal;
    expect(delayedSignal.aborted).toBe(false);
    act(() => window.history.back());
    await waitFor(() => expect(window.location.hash).toBe("#domains/detail"));
    await screen.findByRole("button", { name: "查看 synthetic.invalid" });
    expect(delayedSignal.aborted).toBe(true);
    expect(screen.queryByLabelText("新的凭据来源")).toBeNull();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    await act(async () => resolve(await response(detail())));
    act(() => window.history.forward());
    await waitFor(() =>
      expect(window.location.hash).toBe("#domains/credential-source"),
    );
    await screen.findByRole("button", { name: "查看 synthetic.invalid" });
    expect(screen.queryByLabelText("新的凭据来源")).toBeNull();
    expect(screen.queryByText(account.label)).toBeNull();
    expect(screen.queryByDisplayValue("Synthetic Actor Secret")).toBeNull();
    expect(calls(base)).toHaveLength(2);
    expect(calls(`${base}/reference`)).toHaveLength(0);
  });
});
describe("source contracts and persistence", () => {
  it("accepts only exact metadata-only receipts and safe source detail", () => {
    expect(validSourceDetail(detail())).toBe(true);
    expect(validSourceReceipt(receipt())).toBe(true);
    for (const extra of [
      { accountId },
      { username: "secret" },
      { grantRevision: "7" },
      { revision: "01" },
      { connectionCredentialGeneration: "5" },
    ])
      expect(validSourceReceipt({ ...receipt(), ...extra })).toBe(false);
    expect(validSourceDetail(detail({ testEligible: true }))).toBe(false);
    expect(validSourceDetail({ ...detail(), accountId })).toBe(false);
  });
  it.each([null, "2"])(
    "rejects a successful source response with actor header %s",
    async (actor) => {
      override = () => response(detail(), 200, actor);
      await expect(
        sourceAPI.detail(domainId, 1, new AbortController().signal),
      ).rejects.toMatchObject({ code: "domain_source_identity_changed" });
    },
  );
  it("rejects picker metadata from another cookie actor before rendering it", async () => {
    override = (url) =>
      url.split("?")[0] === "/api/operation-accounts"
        ? response(list([{ ...account, label: "OTHER_ACTOR_LABEL" }]), 200, "2")
        : undefined;
    await start();
    fill("新的凭据来源", "reference");
    await waitFor(() =>
      expect(calls("/api/operation-accounts")).toHaveLength(1),
    );
    await act(async () => {});
    expect(screen.queryByText(/OTHER_ACTOR_LABEL/)).toBeNull();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
  });
  it("projects only nonsecret source intent and isolates other actors", () => {
    saveSourceIntent(profile, {
      operation: "reference",
      domainId,
      expectedRevision: "3",
      expectedConnectionCredentialGeneration: "2",
      idempotencyKey: "test-source-intent",
      accountId,
      username: "secret",
      password: "secret",
      actorPassword: "secret",
      totpCode: "123456",
    } as never);
    expect(JSON.stringify({ ...sessionStorage })).not.toMatch(
      /synthetic-account|username|password|actorPassword|totpCode|secret/u,
    );
    expect(readSourceIntent({ ...profile, ID: 2 })).toBeNull();
    expect(readSourceIntent(profile)?.idempotencyKey).toBe(
      "test-source-intent",
    );
  });
});
describe("source selection", () => {
  it("uses only same-domain metadata and exact own-role pins with one explicit POST", async () => {
    await start();
    await selectReference();
    expect(calls("/api/operation-accounts")[0][0]).toContain(
      "filterDomain=synthetic.invalid",
    );
    expect(screen.getByText("本角色显式授权：已授予")).toBeVisible();
    proof();
    click("保存凭据来源");
    click("保存凭据来源");
    await screen.findByText(/凭据来源已保存：已登记操作账户/);
    expect(calls(`${base}/reference`)).toHaveLength(1);
    expect(JSON.parse(calls(`${base}/reference`)[0][1].body)).toEqual({
      domainId,
      expectedRevision: "3",
      expectedConnectionCredentialGeneration: "2",
      idempotencyKey: expect.any(String),
      accountId,
      expectedAccountRevision: "2",
      expectedAccountCredentialRevision: "1",
      expectedGrantRevision: "7",
      actorPassword: "Synthetic Actor Secret",
      totpCode: "123456",
    });
    expect(sessionStorage.length).toBe(0);
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
  });
  it("does not infer grants for admin or enabled capability", async () => {
    override = (url) =>
      url.startsWith("/api/credential-use/effective")
        ? response({
            ...grant,
            explicitlyGranted: false,
            eligible: false,
            grantRevision: "0",
          })
        : undefined;
    await start();
    fill("新的凭据来源", "reference");
    await screen.findByRole("button", { name: "选择账户 同域测试账户" });
    click("选择账户 同域测试账户");
    await screen.findByText("本角色显式授权：未授予（默认拒绝）");
    expect(screen.getByRole("button", { name: "保存凭据来源" })).toBeDisabled();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
  });
  it("rereads own-role grants when explicitly reselecting the same account revision", async () => {
    let revoked = false;
    override = (url) =>
      revoked && url.startsWith("/api/credential-use/effective")
        ? response({
            ...grant,
            explicitlyGranted: false,
            eligible: false,
            grantRevision: "8",
          })
        : undefined;
    await start();
    await selectReference();
    proof();
    revoked = true;
    click("选择账户 同域测试账户");
    await screen.findByText("本角色显式授权：未授予（默认拒绝）");
    expect(calls("/api/credential-use/effective")).toHaveLength(2);
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(screen.getByRole("button", { name: "保存凭据来源" })).toBeDisabled();
  });
  it("refuses foreign-domain picker metadata", async () => {
    override = (url) =>
      url.split("?")[0] === "/api/operation-accounts"
        ? response(
            list([
              {
                ...account,
                domainId: "foreign-domain",
                label: "FOREIGN_LABEL",
              },
            ]),
          )
        : undefined;
    await start();
    fill("新的凭据来源", "reference");
    await screen.findByRole("alert");
    expect(screen.queryByText(/FOREIGN_LABEL/)).toBeNull();
  });
  it("requires rereading an account after a label-only revision changed", async () => {
    override = (url) =>
      url.startsWith("/api/operation-accounts/detail")
        ? response({
            account: { ...account, revision: "3", label: "new label" },
          })
        : undefined;
    await start();
    fill("新的凭据来源", "reference");
    await screen.findByRole("button", { name: "选择账户 同域测试账户" });
    click("选择账户 同域测试账户");
    await screen.findByRole("alert");
    expect(screen.getByRole("button", { name: "保存凭据来源" })).toBeDisabled();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
  });
  it("clears pair and proof when changing source and requires a fresh custom pair", async () => {
    await start();
    fill("新的凭据来源", "custom");
    fill("AD 用户名", "SYNTHETIC\\reader");
    fill("AD 密码", "Synthetic secret");
    proof();
    fill("新的凭据来源", "reference");
    expect(screen.queryByLabelText("AD 密码")).toBeNull();
    fill("新的凭据来源", "custom");
    expect(screen.getByLabelText("AD 密码")).toHaveValue("");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(JSON.stringify({ ...sessionStorage })).not.toMatch(
      /Synthetic|reader/,
    );
  });
  it("detaches without account metadata access or a live grant", async () => {
    current = detail({
      credentialSource: "operation_account",
      credentialConfigured: true,
    });
    await start();
    expect(screen.getByText(/当前无法读取账户元数据/)).toBeVisible();
    fill("新的凭据来源", "detach");
    proof();
    click("保存凭据来源");
    await screen.findByText(/凭据来源已保存：未配置凭据/);
    expect(calls("/api/operation-accounts")).toHaveLength(0);
    expect(calls("/api/credential-use/effective")).toHaveLength(0);
    expect(
      Object.keys(JSON.parse(calls(`${base}/detach`)[0][1].body)).sort(),
    ).toEqual(
      [
        "domainId",
        "expectedRevision",
        "expectedConnectionCredentialGeneration",
        "idempotencyKey",
        "actorPassword",
        "totpCode",
      ].sort(),
    );
  });
  it("blocks source/account/grant conflicts until explicit reread", async () => {
    override = (url) =>
      url === `${base}/reference`
        ? response({ error: "revision_conflict" }, 409)
        : undefined;
    await start();
    await selectReference();
    proof();
    click("保存凭据来源");
    await screen.findByRole("button", { name: "重新核对来源版本" });
    expect(screen.getByRole("button", { name: "保存凭据来源" })).toBeDisabled();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(readSourceIntent(profile)).toBeNull();
    current = detail({ revision: "4", connectionCredentialGeneration: "3" });
    click("重新核对来源版本");
    await waitFor(() =>
      expect(screen.getByLabelText("新的凭据来源")).toHaveValue(""),
    );
    expect(calls(`${base}/reference`)).toHaveLength(1);
  });
  it("creates unconfigured without entering or submitting a duplicate pair", async () => {
    render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByRole("button", { name: "新增域连接" });
    click("新增域连接");
    fill("初始凭据来源", "unconfigured");
    fill("域 DNS 名称", "synthetic.invalid");
    fill("域控 DNS 名称", "dc.synthetic.invalid");
    proof();
    expect(screen.queryByLabelText("AD 用户名")).toBeNull();
    click("保存本地连接");
    await screen.findByRole("heading", { name: "本地登记回执" });
    expect(
      Object.keys(
        JSON.parse(calls("/api/domains/create-unconfigured")[0][1].body),
      ).sort(),
    ).toEqual(
      [
        "domain",
        "dcHostName",
        "ldapAddr",
        "port",
        "idempotencyKey",
        "actorPassword",
        "totpCode",
      ].sort(),
    );
    expect(calls("/api/domains/create")).toHaveLength(0);
  });
});
describe("uncertain source recovery and navigation", () => {
  it.each(["immediate", "delayed"] as const)(
    "restores the source route and original receipt with an %s response",
    async (timing) => {
      saveSourceIntent(profile, {
        operation: "reference",
        domainId,
        expectedRevision: "3",
        expectedConnectionCredentialGeneration: "2",
        idempotencyKey: "reload-source-key",
      });
      window.history.replaceState({}, "", "#domains/credential-source");
      let resolve!: (value: Response) => void;
      const recovered = { receipt: receipt({ replayed: true }) };
      override = (url) =>
        url === "/api/auth/me"
          ? response(profile)
          : url.startsWith(`${base}/mutation?`)
            ? timing === "immediate"
              ? response(recovered)
              : new Promise<Response>((done) => (resolve = done))
            : undefined;
      render(<App />);
      if (timing === "immediate") {
        // Recovery may finish before the caller observes the restored route.
        await screen.findByText(/原凭据来源操作已确认/);
      } else {
        await screen.findByRole("heading", {
          name: "核对未确认的凭据来源变更",
        });
        expect(screen.queryByText(/原凭据来源操作已确认/)).toBeNull();
        expect(readSourceIntent(profile)?.idempotencyKey).toBe(
          "reload-source-key",
        );
      }
      expect(screen.getByRole("button", { name: "域连接" })).toHaveAttribute(
        "aria-current",
        "page",
      );
      expect(screen.getByRole("heading", { name: "域连接" })).toBeVisible();
      expect(window.location.hash).toBe("#domains/credential-source");
      if (timing === "delayed")
        await act(async () => resolve(await response(recovered)));
      await screen.findByText(/原凭据来源操作已确认/);
      expect(calls("/api/auth/me")).toHaveLength(1);
      expect(calls(`${base}/mutation`)).toHaveLength(1);
      expect(calls(`${base}/mutation`)[0][0]).toBe(
        `${base}/mutation?idempotencyKey=reload-source-key`,
      );
      expect(calls(`${base}/reference`)).toHaveLength(0);
      expect(calls("/api/domains/test")).toHaveLength(0);
      expect(readSourceIntent(profile)).toBeNull();
      expect(sessionStorage.length).toBe(0);
      expect(screen.queryByLabelText("AD 密码")).toBeNull();
      expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    },
  );
  it("blocks source refresh during an unresolved POST and preserves its original key", async () => {
    let reject!: (error: Error) => void;
    override = (url) =>
      url === `${base}/reference`
        ? new Promise<Response>((_, r) => {
            reject = r;
          })
        : undefined;
    await start();
    await selectReference();
    proof();
    click("保存凭据来源");
    const intent = readSourceIntent(profile);
    expect(intent).not.toBeNull();
    expect(
      screen.getByRole("button", { name: "重新读取凭据来源" }),
    ).toBeDisabled();
    click("重新读取凭据来源");
    click("保存凭据来源");
    expect(calls(base)).toHaveLength(1);
    expect(calls(`${base}/reference`)).toHaveLength(1);
    expect(readSourceIntent(profile)).toEqual(intent);
    await act(async () => {
      reject(Error("response lost"));
    });
    await screen.findByRole("heading", { name: "核对未确认的凭据来源变更" });
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(readSourceIntent(profile)).toEqual(intent);
    expect(calls(`${base}/reference`)).toHaveLength(1);
  });
  it("routes a fresh submit to recovery if an unresolved intent appeared since the form mounted", async () => {
    await start();
    await selectReference();
    const intent = {
      operation: "reference" as const,
      domainId,
      expectedRevision: "3",
      expectedConnectionCredentialGeneration: "2",
      idempotencyKey: "prior-unresolved-intent",
    };
    saveSourceIntent(profile, intent);
    // Submit the form directly without proof: the guard must precede proof/key creation.
    fireEvent.submit(
      screen.getByRole("button", { name: "保存凭据来源" }).closest("form")!,
    );
    await screen.findByRole("heading", { name: "核对未确认的凭据来源变更" });
    expect(readSourceIntent(profile)).toEqual(intent);
    expect(calls(`${base}/reference`)).toHaveLength(0);
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
  });
  it("recovers a lost commit before asking for proof, without rePOST", async () => {
    let recover = false;
    override = (url) =>
      url === `${base}/reference`
        ? Promise.reject(Error("lost committed response"))
        : url.startsWith(`${base}/mutation?`) && recover
          ? response({ receipt: receipt({ replayed: true }) })
          : undefined;
    await start();
    await selectReference();
    proof();
    click("保存凭据来源");
    await screen.findByRole("heading", { name: "核对未确认的凭据来源变更" });
    await screen.findByRole("alert");
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(JSON.stringify({ ...sessionStorage })).not.toMatch(
      /synthetic-account|Synthetic|123456|accountId|grantRevision/,
    );
    expect(readSourceIntent(profile)).not.toBeNull();
    recover = true;
    click("查询原来源回执");
    await screen.findByText(/原凭据来源操作已确认/);
    expect(calls(`${base}/reference`)).toHaveLength(1);
    expect(readSourceIntent(profile)).toBeNull();
  });
  it.each(["Back", "close"])(
    "ignores a late save after %s and retains receipt recovery",
    async (action) => {
      let resolve!: (value: Response) => void;
      override = (url) =>
        url === `${base}/reference`
          ? new Promise<Response>((r) => {
              resolve = r;
            })
          : undefined;
      await start();
      await selectReference();
      proof();
      click("保存凭据来源");
      if (action === "Back")
        act(() => window.dispatchEvent(new PopStateEvent("popstate")));
      else click("关闭来源编辑");
      await act(async () => {
        resolve(await response(receipt()));
      });
      expect(screen.queryByText(/凭据来源已保存/)).toBeNull();
      expect(readSourceIntent(profile)).not.toBeNull();
      expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
      expect(calls(`${base}/reference`)).toHaveLength(1);
    },
  );
  it("isolates actor switches and ignores the previous actor's late response", async () => {
    let resolve!: (value: Response) => void;
    override = (url) =>
      url === `${base}/reference`
        ? new Promise<Response>((r) => {
            resolve = r;
          })
        : undefined;
    const root = await start();
    await selectReference();
    proof();
    click("保存凭据来源");
    root.rerender(
      <DomainsWorkspace
        profile={{ ...profile, ID: 2, csrfToken: "new-session" }}
        sessionChanged={vi.fn()}
      />,
    );
    await act(async () => {
      resolve(await response(receipt()));
    });
    expect(screen.queryByText(/凭据来源已保存/)).toBeNull();
    expect(readSourceIntent({ ...profile, ID: 2 })).toBeNull();
    expect(readSourceIntent(profile)).not.toBeNull();
    expect(calls(`${base}/reference`)).toHaveLength(1);
  });
});
