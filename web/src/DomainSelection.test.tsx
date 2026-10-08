import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, expect, it, vi, describe } from "vitest";
import DomainsWorkspace from "./DomainsWorkspace";
import { ApiError, type Profile } from "./api";
import { domainOperations } from "./domain-api";
import {
  parseResolvedSource,
  parseSourcePage,
  sourceAPI,
  sourceError,
  validSourceChoice,
  validSourceKeyword,
  type SourceChoice,
  type SourceQuery,
} from "./domain-selection-api";

const choice = (extra: Partial<SourceChoice> = {}): SourceChoice => ({
  domainId: "source-one",
  domain: "synthetic.invalid",
  revision: "9007199254740993",
  credentialRevision: "9007199254740995",
  dcHostName: "dc.synthetic.invalid",
  ldapAddr: "",
  port: "636",
  mode: "ldaps",
  credentialConfigured: true,
  connectionState: "unverified",
  source: "configured_connection",
  lastTest: null,
  ...extra,
});
const query: SourceQuery = {
  pageIdx: 1,
  pageSize: 20,
  keyword: "",
  observationState: "",
};
const list = (
  items = [choice()],
  pageIdx = 1,
  total = items.length,
  pageSize = 20,
) => ({
  page: { pageIdx, pageSize, total, totalPage: Math.ceil(total / pageSize) },
  List: items,
  exhausted: (pageIdx - 1) * pageSize + items.length >= total,
});
const checkedAt = "2026-10-07T00:20:00.123456789Z";
const resolved = (selection = choice()) => ({ selection, checkedAt });
const observed = {
  observedAt: "2026-10-07T00:01:00Z",
  dcHostName: "dc.synthetic.invalid",
  code: "ok",
};
const detail = () => {
  const { source: _source, lastTest: _test, ...value } = choice();
  return {
    ...value,
    createdAt: "2026-10-07T00:00:00Z",
    updatedAt: "2026-10-07T00:00:01Z",
    lastDiagnostic: null,
    latestTaskUUID: "",
  };
};
const response = (
  value: unknown,
  status = 200,
  userID: string | null = "1",
): Promise<Response> =>
  Promise.resolve({
    ok: status < 400,
    status,
    headers: new Headers(userID === null ? {} : { "X-ADTR-User-ID": userID }),
    json: async () => value,
  } as Response);
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
let fetcher: ReturnType<typeof vi.fn>,
  profile: Profile,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined;
const calls = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const deferred = () => {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((r) => {
    resolve = r;
  });
  return {
    promise,
    resolve: async (value: unknown, status = 200) =>
      act(async () => resolve(await response(value, status))),
  };
};
async function start(sessionChanged = vi.fn()) {
  const mounted = render(
    <DomainsWorkspace profile={profile} sessionChanged={sessionChanged} />,
  );
  await screen.findByRole("button", { name: "选择已授权数据源" });
  click("选择已授权数据源");
  await screen.findByRole("table", { name: "已授权的保存连接配置" });
  return mounted;
}
function choose() {
  fireEvent.click(
    screen.getByRole("radio", { name: "选择数据源 synthetic.invalid" }),
  );
}
beforeEach(() => {
  window.history.replaceState({}, "", "#account");
  profile = {
    ID: 1,
    username: "reader",
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
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/access/menu")
      return response({
        menu: [{ mark: "domains", auth: { readable: true, writeable: true } }],
      });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/domains")
      return response({ ...list(), List: [detail()] });
    if (path === "/api/domain-selection") return response(list());
    if (path === "/api/domain-selection/resolve") return response(resolved());
    if (path === "/api/domains/detail")
      return response({ connection: detail() });
    throw Error(`Unexpected ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});

describe("strict scoped source DTO", () => {
  it("preserves bounded bigint revisions, empty configured addresses and null observation", () => {
    expect(validSourceChoice(choice())).toBe(true);
    expect(parseSourcePage(list(), query).List[0].revision).toBe(
      "9007199254740993",
    );
    expect(parseResolvedSource(resolved(), choice()).checkedAt).toBe(checkedAt);
    expect(validSourceKeyword("😀".repeat(50))).toBe(true);
    expect(validSourceKeyword("😀".repeat(51))).toBe(false);
    expect(validSourceKeyword("x\u0085y")).toBe(false);
    expect(validSourceKeyword("x\ud800")).toBe(false);
  });
  it.each([
    { domainId: "platform" },
    { domainId: "x".repeat(129) },
    { domainId: "../../other" },
    { revision: 9007199254740993 },
    { revision: "01" },
    { revision: "9223372036854775808" },
    { credentialRevision: "0" },
    { credentialRevision: 1 },
    { domain: "SYNTHETIC.invalid" },
    { source: "dc_inventory" },
    { source: "configured_connection", username: "secret" },
    { port: "389" },
    { ldapAddr: "https://192.0.2.1" },
    { dcHostName: "invalid host" },
    { lastTest: undefined },
    { lastTest: {} },
    { connectionState: "verified" },
    { lastTest: { ...observed, code: "secret LDAP text" } },
    { lastTest: { ...observed, credential: "secret" } },
    { lastTest: { ...observed, code: "tls_failed" } },
  ])("rejects malformed or widened source fields: %j", (extra) => {
    expect(validSourceChoice({ ...choice(), ...extra })).toBe(false);
  });
  it.each([
    "2026-02-30T00:00:00Z",
    "2026-10-07T25:00:00Z",
    "2026-10-07",
    "2026-10-07T00:00:00+00:00",
    "2026-10-07T00:00:00.1234567890Z",
    "0000-10-07T00:00:00Z",
    null,
  ])("rejects invalid or unbounded observation/resolve dates: %s", (date) => {
    expect(
      validSourceChoice({
        ...choice(),
        connectionState: "verified",
        lastTest: { ...observed, observedAt: date },
      }),
    ).toBe(false);
    expect(() =>
      parseResolvedSource({ ...resolved(), checkedAt: date }, choice()),
    ).toThrow("invalid_response");
  });
  it("rejects missing/null pages, duplicate IDs, page drift, counts and extra fields", () => {
    for (const bad of [
      null,
      { ...list(), token: "secret" },
      { ...list(), List: null },
      list([choice(), choice()]),
      list([], 1, 1),
      { ...list(), exhausted: false },
      { ...list(), page: { ...list().page, totalPage: 2 } },
      list([choice()], 2, 21),
    ])
      expect(() => parseSourcePage(bad, query)).toThrow("invalid_response");
    expect(() =>
      parseSourcePage(list(), { ...query, observationState: "verified" }),
    ).toThrow("invalid_response");
  });
  it("binds resolve to both exact revisions and ID while accepting a new historical observation", () => {
    for (const extra of [
      { domainId: "different" },
      { revision: "9007199254740994" },
      { credentialRevision: "9007199254740996" },
    ])
      expect(() =>
        parseResolvedSource(resolved(choice(extra)), choice()),
      ).toThrow("invalid_response");
    const result = resolved(
      choice({ connectionState: "verified", lastTest: observed }),
    );
    expect(parseResolvedSource(result, choice()).selection.lastTest).toEqual(
      observed,
    );
    expect(() =>
      parseResolvedSource({ ...result, authorized: true }, choice()),
    ).toThrow("invalid_response");
  });
  it("uses GET, same-origin and no-store, sends only exact string resolve fields and maps errors safely", async () => {
    await sourceAPI.resolve(choice(), new AbortController().signal, profile.ID);
    const [url, init] = calls("/api/domain-selection/resolve")[0];
    expect(
      Object.fromEntries(new URL(url, "https://local.invalid").searchParams),
    ).toEqual({
      domainId: "source-one",
      expectedRevision: "9007199254740993",
      expectedCredentialRevision: "9007199254740995",
    });
    expect(init).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
    });
    expect(init.body).toBeUndefined();
    override = () => response({ error: "private LDAP/password text" }, 403);
    await expect(
      sourceAPI.list(query, new AbortController().signal, profile.ID),
    ).rejects.toMatchObject({ code: "internal", status: 403 });
    expect(
      sourceError(new ApiError("private LDAP/password text")),
    ).not.toContain("private");
  });
});

describe("saved source consumer and lifecycle", () => {
  it.each([
    "GET /api/domain-selection",
    "GET /api/domain-selection/resolve",
    "GET /api/domains/detail",
  ])(
    "requires exact route grant %s before showing the picker",
    async (denied) => {
      override = (url, init) =>
        url === "/api/access/check"
          ? response({
              results: JSON.parse(init.body as string).paths.map(
                (p: string) => p !== denied,
              ),
            })
          : undefined;
      render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await screen.findByRole("table", { name: "获授权的域连接" });
      expect(
        screen.queryByRole("button", { name: "选择已授权数据源" }),
      ).toBeNull();
      expect(calls("/api/domain-selection")).toHaveLength(0);
      expect(JSON.parse(calls("/api/access/check")[0][1].body).paths).toEqual(
        domainOperations,
      );
    },
  );
  it("does not accept operation_accounts read grant as domains metadata authorization", async () => {
    override = (url) =>
      url === "/api/access/menu"
        ? response({
            menu: [
              {
                mark: "operation_accounts",
                auth: { readable: true, writeable: true },
              },
            ],
          })
        : undefined;
    render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText("服务器未授予域连接读取权限。");
    expect(calls("/api/domain-selection")).toHaveLength(0);
    expect(calls("/api/domains")).toHaveLength(0);
  });
  it("consumes a resolved choice through the real detail/test form without submitting a test", async () => {
    override = (url) =>
      url.startsWith("/api/domain-selection/resolve?")
        ? response(
            resolved(
              choice({ connectionState: "verified", lastTest: observed }),
            ),
          )
        : undefined;
    await start();
    expect(
      screen.getByRole("button", { name: "核对并打开连接详情" }),
    ).toBeDisabled();
    expect(
      screen.getByText(/不代表当前在线、许可证或采集器状态/),
    ).toBeVisible();
    choose();
    click("核对并打开连接详情");
    await screen.findByRole("heading", {
      name: "域连接详情：synthetic.invalid",
    });
    expect(calls("/api/domains/detail")).toHaveLength(1);
    expect(
      screen.getByText(new RegExp(checkedAt.replaceAll(".", "\\."))),
    ).toBeVisible();
    click("检测已保存连接");
    await screen.findByLabelText("操作者当前密码");
    expect(screen.getByLabelText("未使用的认证器验证码")).toBeVisible();
    expect(calls("/api/domains/test")).toHaveLength(0);
    expect(calls("/api/tasks")).toHaveLength(0);
    expect(sessionStorage.length + localStorage.length).toBe(0);
  });
  it("labels the historical observation with its UTC timestamp and leaves an empty address uninferred", async () => {
    override = (url) =>
      url.split("?")[0] === "/api/domain-selection"
        ? response(
            list([choice({ connectionState: "verified", lastTest: observed })]),
          )
        : undefined;
    await start();
    const table = screen.getByRole("table", { name: "已授权的保存连接配置" });
    expect(within(table).getByText("历史检测通过")).toBeVisible();
    expect(within(table).getByText(observed.observedAt)).toBeVisible();
    expect(within(table).getByText("配置 IP：未配置")).toBeVisible();
  });
  it("applies literal search/state/page size, paginates, and resets to the first page", async () => {
    await start();
    override = (url) => {
      if (url.split("?")[0] !== "/api/domain-selection") return;
      const p = new URL(url, "https://local.invalid").searchParams,
        page = Number(p.get("pageIdx")),
        size = Number(p.get("pageSize"));
      return response(
        list(
          page === 1
            ? Array.from({ length: 10 }, (_, i) =>
                choice({ domainId: `id-${i}`, domain: `domain${i}.invalid` }),
              )
            : [choice()],
          page,
          11,
          size,
        ),
      );
    };
    fill("数据源关键词", "%_ Example");
    fill("历史检测状态", "unverified");
    fill("数据源每页条数", "10");
    click("查询数据源");
    await screen.findByRole("radio", { name: "选择数据源 domain0.invalid" });
    expect(
      Object.fromEntries(
        new URL(
          calls("/api/domain-selection").at(-1)![0],
          "https://local.invalid",
        ).searchParams,
      ),
    ).toEqual({
      keyword: "%_ Example",
      observationState: "unverified",
      pageIdx: "1",
      pageSize: "10",
    });
    click("下一页");
    await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    expect(calls("/api/domain-selection").at(-1)![0]).toContain("pageIdx=2");
    override = () => undefined;
    click("重置数据源筛选");
    await waitFor(() =>
      expect(calls("/api/domain-selection").at(-1)![0]).toBe(
        "/api/domain-selection?pageIdx=1&pageSize=20",
      ),
    );
    expect(screen.getByLabelText("数据源关键词", { exact: true })).toHaveValue(
      "",
    );
  });
  it("clears stale choice and reloads after 409 without automatically resolving or mutating again", async () => {
    await start();
    override = (url) =>
      url.startsWith("/api/domain-selection/resolve?")
        ? response({ error: "selection_changed" }, 409)
        : undefined;
    choose();
    click("核对并打开连接详情");
    await screen.findByText(/配置或凭据已改变，已清除选择并刷新列表/);
    await waitFor(() => expect(calls("/api/domain-selection")).toHaveLength(2));
    expect(screen.getByRole("radio")).not.toBeChecked();
    expect(
      screen.getByRole("button", { name: "核对并打开连接详情" }),
    ).toBeDisabled();
    expect(calls("/api/domain-selection/resolve")).toHaveLength(1);
    expect(calls("/api/domains/detail")).toHaveLength(0);
  });
  it.each([
    [403, "forbidden"],
    [404, "not_found"],
    [403, "tenant_expired"],
    [403, "tenant_domain_limit_exceeded"],
    [409, "tenant_not_configured"],
    [503, "schema_incompatible"],
  ])(
    "clears visible data and selected scope on resolve failure %s %s",
    async (status, code) => {
      await start();
      override = (url) =>
        url.startsWith("/api/domain-selection/resolve?")
          ? response({ error: code }, Number(status))
          : undefined;
      choose();
      click("核对并打开连接详情");
      await screen.findByRole("alert");
      expect(screen.queryByRole("radio")).toBeNull();
      expect(
        screen.getByRole("button", { name: "核对并打开连接详情" }),
      ).toBeDisabled();
      expect(calls("/api/domains/detail")).toHaveLength(0);
    },
  );
  it("reports session expiry and removes the choice without opening details", async () => {
    const lost = vi.fn();
    await start(lost);
    override = (url) =>
      url.startsWith("/api/domain-selection/resolve?")
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    choose();
    click("核对并打开连接详情");
    await waitFor(() => expect(lost).toHaveBeenCalledOnce());
    expect(screen.queryByRole("radio")).toBeNull();
    expect(calls("/api/domains/detail")).toHaveLength(0);
  });
  it.each([
    ["list", null],
    ["list", "2"],
    ["list", "01"],
    ["resolve", null],
    ["resolve", "2"],
    ["resolve", "01"],
  ])(
    "rejects %s success with absent or foreign actor header %s",
    async (route, actor) => {
      const lost = vi.fn();
      await start(lost);
      choose();
      override = (url) => {
        if (route === "list" && url.split("?")[0] === "/api/domain-selection")
          return response(list(), 200, actor);
        if (
          route === "resolve" &&
          url.startsWith("/api/domain-selection/resolve?")
        )
          return response(resolved(), 200, actor);
      };
      click(route === "list" ? "刷新已授权数据源" : "核对并打开连接详情");
      await waitFor(() => expect(lost).toHaveBeenCalledOnce());
      expect(screen.queryByRole("radio")).toBeNull();
      expect(
        screen.getByRole("button", { name: "核对并打开连接详情" }),
      ).toBeDisabled();
      expect(calls("/api/domains/detail")).toHaveLength(0);
    },
  );
  it("clears saved choices on list revocation and rejects invalid search before sending", async () => {
    await start();
    choose();
    fill("数据源关键词", "x".repeat(51));
    click("查询数据源");
    await screen.findByText("关键词最多 50 个字符，且不能包含控制字符。");
    expect(calls("/api/domain-selection")).toHaveLength(1);
    expect(screen.getByRole("radio")).not.toBeChecked();
    override = (url) =>
      url.split("?")[0] === "/api/domain-selection"
        ? response({ error: "forbidden" }, 403)
        : undefined;
    click("刷新已授权数据源");
    await screen.findByText("当前账户已无数据源访问权限，已清除选择。");
    expect(screen.queryByRole("radio")).toBeNull();
    expect(calls("/api/domains/detail")).toHaveLength(0);
  });
  it("discards a late list load after browser Back", async () => {
    await start();
    const pending = deferred();
    override = (url) =>
      url.split("?")[0] === "/api/domain-selection"
        ? pending.promise
        : undefined;
    click("刷新已授权数据源");
    const signal = calls("/api/domain-selection").at(-1)![1]
      .signal as AbortSignal;
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    expect(signal.aborted).toBe(true);
    await pending.resolve(list());
    expect(screen.queryByRole("radio")).toBeNull();
    expect(
      screen.queryByRole("heading", { name: "选择已授权数据源" }),
    ).toBeNull();
  });
  it.each(["Back", "return", "clear", "filter", "refresh", "session"])(
    "ignores a late resolver after %s even when transport ignores AbortSignal",
    async (action) => {
      const pending = deferred();
      const mounted = await start();
      override = (url) =>
        url.startsWith("/api/domain-selection/resolve?")
          ? pending.promise
          : undefined;
      choose();
      click("核对并打开连接详情");
      const signal = calls("/api/domain-selection/resolve")[0][1]
        .signal as AbortSignal;
      if (action === "Back")
        act(() => window.dispatchEvent(new PopStateEvent("popstate")));
      if (action === "return") click("返回域连接列表");
      if (action === "clear") click("清除数据源选择");
      if (action === "filter") fill("数据源关键词", "new search");
      if (action === "refresh") click("刷新已授权数据源");
      if (action === "session")
        mounted.rerender(
          <DomainsWorkspace
            profile={{ ...profile, ID: 2, csrfToken: "new-session" }}
            sessionChanged={vi.fn()}
          />,
        );
      expect(signal.aborted).toBe(true);
      await pending.resolve(resolved());
      expect(calls("/api/domains/detail")).toHaveLength(0);
      expect(
        screen.queryByRole("heading", {
          name: "域连接详情：synthetic.invalid",
        }),
      ).toBeNull();
    },
  );
  it("locks repeated confirmation and invalidates a late result when another source is selected", async () => {
    override = (url) =>
      url.split("?")[0] === "/api/domain-selection"
        ? response(
            list([
              choice(),
              choice({ domainId: "other", domain: "other.invalid" }),
            ]),
          )
        : undefined;
    await start();
    const pending = deferred();
    override = (url) =>
      url.startsWith("/api/domain-selection/resolve?")
        ? pending.promise
        : undefined;
    choose();
    click("核对并打开连接详情");
    click("正在核对数据源…");
    expect(calls("/api/domain-selection/resolve")).toHaveLength(1);
    fireEvent.click(
      screen.getByRole("radio", { name: "选择数据源 other.invalid" }),
    );
    await pending.resolve(resolved());
    expect(calls("/api/domains/detail")).toHaveLength(0);
    expect(
      screen.getByRole("radio", { name: "选择数据源 other.invalid" }),
    ).toBeChecked();
  });
  it("never restores stale list results after a newer query or leaving the picker", async () => {
    await start();
    const pending = deferred();
    override = (url) =>
      url.split("?")[0] === "/api/domain-selection"
        ? pending.promise
        : undefined;
    fill("数据源关键词", "old");
    click("查询数据源");
    override = (url) =>
      url.split("?")[0] === "/api/domain-selection"
        ? response(list([]))
        : undefined;
    fill("数据源关键词", "new");
    click("查询数据源");
    await screen.findByText("当前筛选下没有已授权数据源。");
    await pending.resolve(list());
    expect(screen.queryByRole("radio")).toBeNull();
    click("返回域连接列表");
    expect(
      screen.queryByRole("heading", { name: "选择已授权数据源" }),
    ).toBeNull();
  });
});
