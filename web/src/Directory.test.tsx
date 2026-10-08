// Synthetic DOM and transport contracts; backend/database/browser evidence is separate.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DirectoryWorkspace from "./DirectoryWorkspace";
import { ApiError, type Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import type { Task } from "./task-api";
import {
  directoryAPI,
  directoryError,
  directoryOperations,
  parseDirectoryObservation,
  parseDirectoryTask,
  validDirectoryInput,
  type DirectoryObject,
  type DirectoryQuery,
} from "./directory-api";
import {
  discardDirectoryIntent,
  readDirectoryIntent,
  saveDirectoryIntent,
  type DirectoryIntent,
} from "./directory-intent";

const profile: Profile = {
  ID: 1,
  username: "directory-reader",
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
  pwdUpdateTm: "2026-10-07T00:00:00Z",
  csrfToken: "directory-session",
};
const otherProfile = {
  ...profile,
  ID: 2,
  username: "other-reader",
  csrfToken: "other-session",
};
const choice: SourceChoice = {
  domainId: "synthetic-domain",
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
};
const query: DirectoryQuery = {
  domainId: choice.domainId,
  kind: "",
  pageIdx: 1,
  pageSize: 50,
};
const source = {
  server_name: "dc.synthetic.invalid",
  dc_host_name: "dc.synthetic.invalid",
  domain: "synthetic.invalid",
  naming_context: "DC=synthetic,DC=invalid",
  started_at: "2026-10-07T00:00:00.123456789Z",
  completed_at: "2026-10-07T00:00:01Z",
  elapsed_milliseconds: 877,
  pages: 1,
};
const row = (
  index = 1,
  kind: DirectoryObject["kind"] = "user",
): DirectoryObject => ({
  objectGUID: `00000000-0000-0000-0000-${String(index).padStart(12, "0")}`,
  distinguishedName: `CN=Object ${index},DC=synthetic,DC=invalid`,
  kind,
  objectClass: ["top", kind].sort(),
  samAccountName: `observed-${index}`,
  userAccountControl: 512,
});
const observation = (
  rows = [row()],
  q = query,
  total = rows.length,
  observationId = "observation-one",
) => ({
  available: true,
  observationId,
  source,
  list: rows,
  page: {
    pageIdx: q.pageIdx,
    pageSize: q.pageSize,
    total,
    totalPage: Math.ceil(total / q.pageSize),
  },
});
const absent = () => ({
  available: false,
  list: [],
  page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
});
const intent = (extra: Partial<DirectoryIntent> = {}): DirectoryIntent => ({
  domainId: choice.domainId,
  expectedRevision: choice.revision,
  expectedCredentialGeneration: choice.credentialRevision,
  idempotencyKey: "original-directory-key",
  ...extra,
});
const task = (extra: Partial<Task> = {}): Task => ({
  taskUUID: "directory-task-one",
  taskName: "domain.directory_read",
  domainId: choice.domainId,
  payloadVersion: 1,
  state: "succeeded",
  sourceState: "",
  error: "",
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:01Z",
  attempt: 1,
  maxAttempts: 1,
  progress: 100,
  resultVersion: 1,
  result: {},
  cursor: {},
  parentTaskUUID: "",
  terminalAt: "2026-10-07T00:00:01Z",
  archived: false,
  visibilityVersion: 0,
  ...extra,
});
const response = (value: unknown, status = 200, actor: string | null = "1") =>
  Promise.resolve(
    new Response(JSON.stringify(value), {
      status,
      headers: {
        "Content-Type": "application/json",
        ...(actor === null ? {} : { "X-ADTR-User-ID": actor }),
      },
    }),
  );
const deferred = () => {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((r) => {
    resolve = r;
  });
  return {
    promise,
    resolve: async (
      value: unknown,
      status = 200,
      actor: string | null = "1",
    ) => {
      await act(async () => resolve(await response(value, status, actor)));
    },
  };
};
const signal = () => new AbortController().signal;
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const proof = (code = "123456") => {
  fill("操作者当前密码", "Synthetic actor password 482");
  fill("未使用的认证器验证码", code);
};
let fetcher: ReturnType<typeof vi.fn>;
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
const calls = (path: string) =>
  fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const params = (path: string) =>
  Object.fromEntries(
    new URL(calls(path).at(-1)![0], "https://local.invalid").searchParams,
  );
const defaultMenu = () =>
  ["domains", "directory_assets", "tasks"].map((mark) => ({
    mark,
    auth: { readable: true, writeable: true },
  }));
async function mount(sessionChanged = vi.fn()) {
  const view = render(
    <DirectoryWorkspace profile={profile} sessionChanged={sessionChanged} />,
  );
  await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" });
  return view;
}
async function selectSource() {
  fireEvent.click(
    screen.getByRole("radio", { name: "选择数据源 synthetic.invalid" }),
  );
  click("核对并打开连接详情");
  await screen.findByRole("region", { name: "目录观察" });
}
async function start(sessionChanged = vi.fn()) {
  const view = await mount(sessionChanged);
  await selectSource();
  return view;
}
beforeEach(() => {
  for (const p of [
    profile,
    otherProfile,
    { ...profile, username: "renamed-reader" },
  ])
    discardDirectoryIntent(p);
  sessionStorage.clear();
  localStorage.clear();
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit = {}) => {
    const custom = override(url, init);
    if (custom) return custom;
    const path = url.split("?")[0];
    if (path === "/api/access/menu") return response({ menu: defaultMenu() });
    if (path === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (path === "/api/domain-selection")
      return response({
        List: [choice],
        page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
        exhausted: true,
      });
    if (path === "/api/domain-selection/resolve")
      return response({ selection: choice, checkedAt: "2026-10-07T00:20:00Z" });
    if (path === "/api/directory/observation") return response(observation());
    if (path === "/api/directory/sync")
      return response({ task: task(), replayed: false });
    if (path === "/api/directory/receipt" || path === "/api/directory/task")
      return response({ task: task() });
    if (path === "/api/directory/cancel")
      return response({
        task: task({ state: "cancel_requested", terminalAt: null }),
      });
    throw Error(`Unexpected ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("strict directory contracts", () => {
  it("distinguishes missing observations from observed empty lists and preserves source timestamps", () => {
    expect(parseDirectoryObservation(absent(), query).available).toBe(false);
    const empty = parseDirectoryObservation(observation([]), query);
    expect(empty.available).toBe(true);
    expect(empty.source?.started_at).toBe(source.started_at);
    expect(empty.list).toEqual([]);
    expect(parseDirectoryObservation(observation(), query).list).toEqual([
      row(),
    ]);
  });
  it("rejects widened records, inconsistent page metadata, wrong pins, kinds, GUID order and malformed dates", () => {
    const bad = [
      null,
      { ...observation(), password: "private" },
      { ...observation(), list: null },
      { ...observation(), list: [{ ...row(), password: "private" }] },
      { ...observation(), list: [{ ...row(), objectGUID: "not-a-guid" }] },
      { ...observation(), list: [{ ...row(), userAccountControl: -1 }] },
      { ...observation(), list: [{ ...row(), objectClass: [] }] },
      { ...observation(), list: [{ ...row(), distinguishedName: "x\u0000y" }] },
      { ...observation(), source: { ...source, password: "private" } },
      { ...observation(), source: { ...source, pages: 0 } },
      {
        ...observation(),
        source: { ...source, completed_at: "2026-02-30T00:00:00Z" },
      },
      {
        ...observation(),
        source: { ...source, completed_at: "2026-10-06T00:00:00Z" },
      },
      { ...observation(), page: { ...observation().page, totalPage: 2 } },
      { ...observation(), page: { ...observation().page, pageIdx: 2 } },
      { ...observation(), page: { ...observation().page, total: 2 } },
      { ...absent(), observationId: "observation-one" },
      { ...absent(), list: [row()] },
      observation([row(), row()]),
      observation([row(2), row(1)]),
    ];
    for (const value of bad)
      expect(() => parseDirectoryObservation(value, query)).toThrow(
        "invalid_response",
      );
    expect(() =>
      parseDirectoryObservation(observation(), { ...query, kind: "group" }),
    ).toThrow("invalid_response");
    expect(() =>
      parseDirectoryObservation(observation(), {
        ...query,
        observationId: "other-observation",
      }),
    ).toThrow("invalid_response");
    expect(() =>
      parseDirectoryObservation(absent(), {
        ...query,
        observationId: "observation-one",
      }),
    ).toThrow("invalid_response");
  });
  it("accepts only the pinned directory task kind, scope, single attempt and empty public result/cursor", () => {
    expect(
      parseDirectoryTask(task(), choice.domainId, task().taskUUID),
    ).toEqual(task());
    for (const extra of [
      { taskName: "domain.connection_test" },
      { domainId: "other-domain" },
      { taskUUID: "other-task" },
      { payloadVersion: 2 },
      { maxAttempts: 2 },
      { parentTaskUUID: "parent-task" },
      { result: { password: "private" } },
      { cursor: { ldapCookie: "private" } },
      { result: null },
      { cursor: [] },
      { createdAt: "2026-02-30T00:00:00Z" },
      { terminalAt: "2026-10-07" },
      { nextAttemptAt: "2026-10-07T00:00:00+00:00" },
      { password: "private" },
    ])
      expect(() =>
        parseDirectoryTask(
          { ...task(), ...extra },
          choice.domainId,
          task().taskUUID,
        ),
      ).toThrow("invalid_response");
  });
  it("preserves string revisions and rejects invalid requests before transport", async () => {
    expect(validDirectoryInput(intent())).toBe(true);
    for (const extra of [
      { expectedRevision: 9007199254740993 },
      { expectedRevision: "01" },
      { expectedCredentialGeneration: "0" },
      { expectedRevision: "9223372036854775808" },
      { idempotencyKey: "short" },
    ])
      expect(validDirectoryInput({ ...intent(), ...extra })).toBe(false);
    for (const extra of [
      { pageIdx: 2 },
      { pageSize: 20 },
      { domainId: "../other" },
    ])
      await expect(
        directoryAPI.observation({ ...query, ...extra }, profile.ID, signal()),
      ).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each([null, "2", "01"])(
    "rejects every successful directory route with foreign/missing actor header %s",
    async (actor) => {
      override = (url) => {
        const path = url.split("?")[0];
        if (path === "/api/directory/observation")
          return response(observation(), 200, actor);
        if (path === "/api/directory/sync")
          return response({ task: task(), replayed: false }, 200, actor);
        return response({ task: task() }, 200, actor);
      };
      const freshProof = {
        actorPassword: "Synthetic actor password 482",
        totpCode: "123456",
      };
      for (const read of [
        () => directoryAPI.observation(query, profile.ID, signal()),
        () =>
          directoryAPI.sync(
            intent(),
            freshProof,
            profile.csrfToken,
            profile.ID,
            signal(),
          ),
        () => directoryAPI.receipt(intent(), profile.ID, signal()),
        () =>
          directoryAPI.task(
            choice.domainId,
            task().taskUUID,
            profile.ID,
            signal(),
          ),
        () =>
          directoryAPI.cancel(
            choice.domainId,
            task().taskUUID,
            freshProof,
            profile.csrfToken,
            profile.ID,
            signal(),
          ),
      ])
        await expect(read()).rejects.toMatchObject({
          code: "unauthenticated",
          status: 401,
        });
    },
  );
  it("never displays an unknown server error as private diagnostic text", async () => {
    const privateText = "private LDAP/password detail";
    override = () => response({ error: privateText }, 403);
    try {
      await directoryAPI.observation(query, profile.ID, signal());
    } catch (error) {
      expect(directoryError(error)).not.toContain(privateText);
    }
    expect(directoryError(new ApiError(privateText))).not.toContain(
      privateText,
    );
  });
});

describe("directory observations and access", () => {
  it.each(["available", "absent", "empty", "error"])(
    "renders %s without conflating unavailable, empty, or failed reads",
    async (state) => {
      override = (url) =>
        url.split("?")[0] === "/api/directory/observation"
          ? state === "absent"
            ? response(absent())
            : state === "empty"
              ? response(observation([]))
              : state === "error"
                ? response({ error: "forbidden" }, 403)
                : response(observation())
          : undefined;
      await start();
      if (state === "available") {
        const table = await screen.findByRole("table", {
          name: "目录对象观察",
        });
        expect(within(table).getByText("observed-1")).toBeVisible();
        expect(screen.getByText(/结果不代表 AD 的时间点快照/)).toBeVisible();
        expect(screen.getByText(/未观察到对象不表示删除/)).toBeVisible();
      } else if (state === "absent")
        await screen.findByText(
          "尚无当前配置的成功目录观察。此状态不表示目录为空。",
        );
      else if (state === "empty")
        await screen.findByText("已成功观察，当前筛选结果为空。");
      else await screen.findByRole("alert");
      if (state !== "available")
        expect(
          screen.queryByRole("table", { name: "目录对象观察" }),
        ).toBeNull();
      if (state !== "empty")
        expect(screen.queryByText("已成功观察，当前筛选结果为空。")).toBeNull();
    },
  );
  it("uses pinned pages and resets the observation pin on kind, page size, and explicit refresh", async () => {
    override = (url) => {
      if (url.split("?")[0] !== "/api/directory/observation") return;
      const p = new URL(url, "https://local.invalid").searchParams;
      const q: DirectoryQuery = {
        domainId: choice.domainId,
        kind: (p.get("kind") ?? "") as DirectoryQuery["kind"],
        pageIdx: Number(p.get("pageIdx")),
        pageSize: Number(p.get("pageSize")),
      };
      const start = (q.pageIdx - 1) * q.pageSize + 1;
      return response(
        observation(
          Array.from({ length: Math.min(q.pageSize, 51 - start + 1) }, (_, i) =>
            row(start + i, q.kind || "user"),
          ),
          q,
          51,
        ),
      );
    };
    await start();
    await screen.findByText("observed-1");
    const next = () =>
      fireEvent.click(
        within(
          screen.getByRole("navigation", { name: "目录观察分页" }),
        ).getByRole("button", { name: "下一页" }),
      );
    next();
    await screen.findByText("observed-51");
    expect(params("/api/directory/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "2",
      pageSize: "50",
      observationId: "observation-one",
    });
    fill("目录对象类型", "group");
    await screen.findByText("observed-1");
    expect(params("/api/directory/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "1",
      pageSize: "50",
      kind: "group",
    });
    next();
    await screen.findByText("observed-51");
    fill("目录每页条数", "25");
    await screen.findByText("observed-1");
    expect(params("/api/directory/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "1",
      pageSize: "25",
      kind: "group",
    });
    next();
    await screen.findByText("observed-26");
    click("刷新目录观察");
    await screen.findByText("observed-1");
    expect(params("/api/directory/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "1",
      pageSize: "25",
      kind: "group",
    });
  });
  it("stops on a stale observation 409 and requires an explicit unpinned refresh", async () => {
    override = (url) =>
      url.split("?")[0] === "/api/directory/observation"
        ? url.includes("observationId=")
          ? response({ error: "directory_observation_unavailable" }, 409)
          : response(
              observation(
                Array.from({ length: 50 }, (_, i) => row(i + 1)),
                query,
                51,
              ),
            )
        : undefined;
    await start();
    await screen.findByText("observed-1");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "目录观察分页" }),
      ).getByRole("button", { name: "下一页" }),
    );
    await screen.findByText(/原目录观察已不可用/);
    expect(screen.queryByText("observed-1")).toBeNull();
    expect(calls("/api/directory/observation")).toHaveLength(2);
    click("刷新目录观察");
    await screen.findByText("observed-1");
    expect(params("/api/directory/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "1",
      pageSize: "50",
    });
  });
  it("allows read-only observations independently of tasks and credential metadata permission", async () => {
    override = (url, init) => {
      if (url === "/api/access/menu")
        return response({
          menu: defaultMenu()
            .filter((p) => p.mark !== "tasks")
            .map((p) => ({ ...p, auth: { readable: true, writeable: false } })),
        });
      if (url === "/api/access/check")
        return response({
          results: JSON.parse(init.body as string).paths.map((path: string) =>
            [
              "GET /api/directory/observation",
              "GET /api/domain-selection",
              "GET /api/domain-selection/resolve",
            ].includes(path),
          ),
        });
    };
    await start();
    await screen.findByText("observed-1");
    expect(
      screen.getByText("当前为目录只读访问，不能提交同步。"),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: "提交目录同步" })).toBeNull();
    expect(
      fetcher.mock.calls.some(([url]) =>
        /credential-use|operation-accounts/u.test(url),
      ),
    ).toBe(false);
    expect(sessionStorage.length + localStorage.length).toBe(0);
  });
  it.each(["domains", "directory_assets"])(
    "requires readable %s metadata before fetching sources",
    async (denied) => {
      override = (url) =>
        url === "/api/access/menu"
          ? response({
              menu: defaultMenu().map((p) =>
                p.mark === denied
                  ? { ...p, auth: { readable: false, writeable: true } }
                  : p,
              ),
            })
          : undefined;
      render(<DirectoryWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await screen.findByText("当前账户没有目录资产读取权限。");
      expect(calls("/api/domain-selection")).toHaveLength(0);
      expect(calls("/api/directory/observation")).toHaveLength(0);
    },
  );
  it.each([
    "GET /api/domain-selection",
    "GET /api/domain-selection/resolve",
    "GET /api/directory/observation",
  ])(
    "requires exact route grant %s before fetching the picker",
    async (denied) => {
      override = (url, init) =>
        url === "/api/access/check"
          ? response({
              results: JSON.parse(init.body as string).paths.map(
                (path: string) => path !== denied,
              ),
            })
          : undefined;
      render(<DirectoryWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await waitFor(() =>
        expect(screen.queryByText("正在核对目录访问权限…")).toBeNull(),
      );
      expect(calls("/api/domain-selection")).toHaveLength(0);
      expect(calls("/api/directory/observation")).toHaveLength(0);
    },
  );
});

describe("directory synchronization and recovery", () => {
  it("sends only exact string pins, operation key and fresh proof with CSRF and no-store", async () => {
    await start();
    await screen.findByRole("button", { name: "提交目录同步" });
    proof();
    click("提交目录同步");
    await screen.findByText(/服务器状态：已成功/);
    const [, init] = calls("/api/directory/sync")[0];
    const body = JSON.parse(init.body as string);
    expect(body).toEqual({
      domainId: choice.domainId,
      expectedRevision: choice.revision,
      expectedCredentialGeneration: choice.credentialRevision,
      idempotencyKey: expect.stringMatching(/^[A-Za-z0-9_-]{8,128}$/u),
      actorPassword: "Synthetic actor password 482",
      totpCode: "123456",
    });
    expect(init).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": profile.csrfToken,
      },
    });
    expect(readDirectoryIntent(profile)).toEqual({
      domainId: choice.domainId,
      expectedRevision: choice.revision,
      expectedCredentialGeneration: choice.credentialRevision,
      idempotencyKey: body.idempotencyKey,
      taskUUID: task().taskUUID,
    });
    const stored = JSON.stringify(Object.values(sessionStorage));
    expect(stored).not.toMatch(
      /Synthetic actor password|123456|actorPassword|totpCode|directory-session/u,
    );
    expect(localStorage.length).toBe(0);
    expect(calls("/api/tasks/submit")).toHaveLength(0);
  });
  it("checks the original receipt before retrying a network-uncertain submission with the same key and pins", async () => {
    const receipt = deferred();
    override = (url) => {
      if (url === "/api/directory/sync")
        return Promise.reject(new TypeError("connection lost"));
      if (url.split("?")[0] === "/api/directory/receipt")
        return receipt.promise;
    };
    await start();
    proof();
    click("提交目录同步");
    await screen.findByRole("heading", { name: "核对原目录同步回执" });
    await waitFor(() =>
      expect(calls("/api/directory/receipt")).toHaveLength(1),
    );
    const original = JSON.parse(calls("/api/directory/sync")[0][1].body);
    expect(params("/api/directory/receipt")).toEqual({
      domainId: choice.domainId,
      idempotencyKey: original.idempotencyKey,
    });
    expect(
      screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
    ).toBeNull();
    expect(calls("/api/directory/sync")).toHaveLength(1);
    await receipt.resolve({ error: "not_found" }, 404);
    await screen.findByRole("button", { name: "使用原编号和版本重试同步" });
    expect(
      screen.getByLabelText("操作者当前密码", { exact: true }),
    ).toHaveValue("");
    expect(
      screen.getByLabelText("未使用的认证器验证码", { exact: true }),
    ).toHaveValue("");
    expect(calls("/api/directory/sync")).toHaveLength(1);
    override = () => undefined;
    proof("654321");
    click("使用原编号和版本重试同步");
    await screen.findByText(/服务器状态：已成功/);
    expect(calls("/api/directory/sync")).toHaveLength(2);
    expect(JSON.parse(calls("/api/directory/sync")[1][1].body)).toEqual({
      ...original,
      totpCode: "654321",
    });
    expect(
      fetcher.mock.calls.findIndex(([url]) =>
        url.startsWith("/api/directory/receipt?"),
      ),
    ).toBeGreaterThan(
      fetcher.mock.calls.findIndex(([url]) => url === "/api/directory/sync"),
    );
  });
  it("uses a recovered receipt directly and never submits a replacement synchronization", async () => {
    saveDirectoryIntent(profile, intent());
    await mount();
    await screen.findByText(/服务器状态：已成功/);
    expect(params("/api/directory/receipt")).toEqual({
      domainId: choice.domainId,
      idempotencyKey: intent().idempotencyKey,
    });
    expect(calls("/api/directory/sync")).toHaveLength(0);
    expect(readDirectoryIntent(profile)?.taskUUID).toBe(task().taskUUID);
  });
  it("stops a definitively disabled submission and reopens only after explicit source review", async () => {
    override = (url) =>
      url === "/api/directory/sync"
        ? response({ error: "directory_read_disabled" }, 503)
        : undefined;
    await start();
    proof();
    click("提交目录同步");
    await screen.findByText(/当前部署未开启目录读取/);
    expect(screen.getByRole("button", { name: "提交目录同步" })).toBeDisabled();
    expect(readDirectoryIntent(profile)).toBeNull();
    expect(calls("/api/directory/receipt")).toHaveLength(0);
    expect(calls("/api/directory/sync")).toHaveLength(1);
    await selectSource();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "提交目录同步" }),
      ).toBeEnabled(),
    );
    expect(
      screen.getByLabelText("操作者当前密码", { exact: true }),
    ).toHaveValue("");
    expect(calls("/api/directory/sync")).toHaveLength(1);
  });
  it("renders task failure without echoing arbitrary backend diagnostics", async () => {
    saveDirectoryIntent(profile, intent({ taskUUID: task().taskUUID }));
    override = (url) =>
      url.split("?")[0] === "/api/directory/task"
        ? response({
            task: task({
              state: "failed",
              error: "private LDAP/password diagnostic",
            }),
          })
        : undefined;
    await mount();
    await screen.findByText(/服务器状态：已失败/);
    expect(screen.queryByText(/private LDAP\/password/u)).toBeNull();
    expect(
      screen.getByRole("button", { name: "结束此任务查看" }),
    ).toBeVisible();
  });
  it.each([
    [403, "not_found"],
    [404, "forbidden"],
    [500, "not_found"],
    [409, "idempotency_conflict"],
  ])(
    "does not unlock a retry for non-exact missing receipt %s %s",
    async (status, code) => {
      saveDirectoryIntent(profile, intent());
      override = (url) =>
        url.split("?")[0] === "/api/directory/receipt"
          ? response({ error: code }, Number(status))
          : undefined;
      await mount();
      await screen.findByRole("alert");
      expect(
        screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
      ).toBeNull();
      expect(calls("/api/directory/sync")).toHaveLength(0);
      expect(readDirectoryIntent(profile)).toEqual(intent());
    },
  );
  it("projects recovery storage to nonsecret pins and isolates it by actor and username", () => {
    saveDirectoryIntent(profile, {
      ...intent(),
      actorPassword: "private-proof",
      totpCode: "876543",
      csrfToken: "private-csrf",
      payload: { secret: "private-source" },
    } as DirectoryIntent);
    expect(readDirectoryIntent(profile)).toEqual(intent());
    expect(readDirectoryIntent(otherProfile)).toBeNull();
    expect(
      readDirectoryIntent({ ...profile, username: "renamed-reader" }),
    ).toBeNull();
    const stored = JSON.stringify(Object.values(sessionStorage));
    expect(stored).not.toMatch(
      /private|876543|actorPassword|totpCode|csrfToken|payload/u,
    );
    expect(localStorage.length).toBe(0);
  });
  it("keeps cancel_requested distinct from cancelled, polls to the actual terminal state, then stops", async () => {
    saveDirectoryIntent(profile, intent({ taskUUID: task().taskUUID }));
    let current = task({ state: "running", progress: 10, terminalAt: null });
    override = (url) => {
      if (url.split("?")[0] === "/api/directory/task")
        return response({ task: current });
      if (url === "/api/directory/cancel") {
        current = task({
          state: "cancel_requested",
          progress: 10,
          terminalAt: null,
        });
        return response({ task: current });
      }
    };
    await mount();
    await screen.findByRole("button", { name: "请求取消目录同步" });
    vi.useFakeTimers();
    proof();
    await act(async () => click("请求取消目录同步"));
    expect(screen.getByText(/服务器状态：已请求取消，待确认/)).toBeVisible();
    expect(
      screen.getByText("已请求取消，继续等待服务器确认实际终态。"),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: "结束此任务查看" })).toBeNull();
    expect(JSON.parse(calls("/api/directory/cancel")[0][1].body)).toEqual({
      taskUUID: task().taskUUID,
      actorPassword: "Synthetic actor password 482",
      totpCode: "123456",
    });
    const beforePoll = calls("/api/directory/task").length;
    current = task({ state: "cancelled", progress: 10 });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(calls("/api/directory/task")).toHaveLength(beforePoll + 1);
    expect(screen.getByText(/服务器状态：已取消/)).toBeVisible();
    expect(
      screen.getByRole("button", { name: "结束此任务查看" }),
    ).toBeVisible();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });
    expect(calls("/api/directory/task")).toHaveLength(beforePoll + 1);
    click("结束此任务查看");
    expect(readDirectoryIntent(profile)).toBeNull();
  });
  it("preserves cancel proof across running task heartbeats and clears it immediately on submit", async () => {
    saveDirectoryIntent(profile, intent({ taskUUID: task().taskUUID }));
    const cancelResponse = deferred();
    let current = task({ state: "running", progress: 10, terminalAt: null });
    override = (url) => {
      if (url.split("?")[0] === "/api/directory/task")
        return response({ task: current });
      if (url === "/api/directory/cancel") return cancelResponse.promise;
    };
    await mount();
    await screen.findByRole("button", { name: "请求取消目录同步" });
    vi.useFakeTimers();
    await act(async () => click("刷新目录任务"));
    const passwordField = screen.getByLabelText("操作者当前密码");
    const codeField = screen.getByLabelText("未使用的认证器验证码");
    fill("操作者当前密码", "Partially typed password");
    fill("未使用的认证器验证码", "123");
    const beforePoll = calls("/api/directory/task").length;
    current = { ...current, updatedAt: "2026-10-07T00:00:02Z" };
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(calls("/api/directory/task")).toHaveLength(beforePoll + 1);
    expect(screen.getByLabelText("操作者当前密码")).toBe(passwordField);
    expect(screen.getByLabelText("未使用的认证器验证码")).toBe(codeField);
    expect(passwordField).toHaveValue("Partially typed password");
    expect(codeField).toHaveValue("123");
    proof();
    await act(async () => click("请求取消目录同步"));
    expect(calls("/api/directory/cancel")).toHaveLength(1);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    expect(JSON.parse(calls("/api/directory/cancel")[0][1].body)).toMatchObject(
      { actorPassword: "Synthetic actor password 482", totpCode: "123456" },
    );
    current = { ...current, state: "cancel_requested" };
    await cancelResponse.resolve({ task: current });
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
  });
  it.each(["session", "task"])(
    "clears cancel proof when the %s boundary changes",
    async (boundary) => {
      saveDirectoryIntent(profile, intent({ taskUUID: task().taskUUID }));
      let current = task({ state: "running", terminalAt: null });
      override = (url) =>
        url.split("?")[0] === "/api/directory/task"
          ? response({ task: current })
          : undefined;
      const mounted = await mount();
      await screen.findByRole("button", { name: "请求取消目录同步" });
      const previousPassword = screen.getByLabelText("操作者当前密码");
      proof();
      if (boundary === "task") {
        current = task({ state: "succeeded" });
        click("刷新目录任务");
        await screen.findByRole("button", { name: "结束此任务查看" });
        expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
        click("结束此任务查看");
        await selectSource();
        await screen.findByRole("button", { name: "提交目录同步" });
        current = task({
          taskUUID: "directory-task-two",
          state: "running",
          terminalAt: null,
        });
        override = (url) =>
          url.split("?")[0] === "/api/directory/task"
            ? response({ task: current })
            : url === "/api/directory/sync"
              ? response({ task: current, replayed: false })
              : undefined;
        proof("654321");
        click("提交目录同步");
      } else {
        mounted.rerender(
          <DirectoryWorkspace
            profile={{ ...profile, csrfToken: "rotated-directory-session" }}
            sessionChanged={vi.fn()}
          />,
        );
      }
      await screen.findByRole("button", { name: "请求取消目录同步" });
      expect(previousPassword.isConnected).toBe(false);
      expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
      expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    },
  );
});

describe("directory identity and request lifecycle", () => {
  it("removes protected data and notifies session expiry on a foreign-actor success", async () => {
    const lost = vi.fn();
    await start(lost);
    await screen.findByText("observed-1");
    override = (url) =>
      url.split("?")[0] === "/api/directory/observation"
        ? response(observation(), 200, "2")
        : undefined;
    click("刷新目录观察");
    await waitFor(() => expect(lost).toHaveBeenCalledOnce());
    expect(screen.queryByRole("table", { name: "目录对象观察" })).toBeNull();
    expect(screen.queryByRole("radio")).toBeNull();
  });
  it.each(["actor", "csrf"])(
    "aborts late observation after %s switch even when the transport ignores AbortSignal",
    async (change) => {
      const pending = deferred();
      override = (url) =>
        url.split("?")[0] === "/api/directory/observation"
          ? pending.promise
          : undefined;
      const mounted = await start();
      // The region mounts before its read effect necessarily dispatches.
      // Switch identity only after proving the delayed request is in flight.
      await waitFor(() =>
        expect(calls("/api/directory/observation")).toHaveLength(1),
      );
      const requestSignal = calls("/api/directory/observation")[0][1]
        .signal as AbortSignal;
      const nextProfile =
        change === "actor"
          ? otherProfile
          : { ...profile, csrfToken: "new-directory-session" };
      mounted.rerender(
        <DirectoryWorkspace profile={nextProfile} sessionChanged={vi.fn()} />,
      );
      expect(requestSignal.aborted).toBe(true);
      await pending.resolve(observation());
      expect(screen.queryByRole("table", { name: "目录对象观察" })).toBeNull();
      expect(screen.queryByText("observed-1")).toBeNull();
    },
  );
  it("aborts late synchronization on actor switch and never attributes the old receipt to the new actor", async () => {
    const pending = deferred();
    const mounted = await start();
    override = (url) =>
      url === "/api/directory/sync" ? pending.promise : undefined;
    proof();
    click("提交目录同步");
    await waitFor(() => expect(calls("/api/directory/sync")).toHaveLength(1));
    const requestSignal = calls("/api/directory/sync")[0][1]
      .signal as AbortSignal;
    mounted.rerender(
      <DirectoryWorkspace profile={otherProfile} sessionChanged={vi.fn()} />,
    );
    expect(requestSignal.aborted).toBe(true);
    await pending.resolve({ task: task(), replayed: false });
    expect(readDirectoryIntent(otherProfile)).toBeNull();
    expect(readDirectoryIntent(profile)?.taskUUID).toBeUndefined();
    expect(screen.queryByRole("region", { name: "目录同步任务" })).toBeNull();
    expect(calls("/api/directory/task")).toHaveLength(0);
  });
  it("requests the directory route catalogue without relying on generic task submit/recovery", async () => {
    await mount();
    const checked = calls("/api/access/check").flatMap(
      ([, init]) => JSON.parse(init.body as string).paths,
    );
    expect(checked).toEqual(expect.arrayContaining([...directoryOperations]));
    expect(checked).not.toContain("POST /api/tasks/submit");
    expect(checked).not.toContain("POST /api/tasks/recover");
  });
});
