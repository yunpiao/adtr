import {
  parseDirectoryV2Observation,
  parseDirectoryV2Task,
} from "./directory-v2-api";
import {
  saveDirectoryV2Intent,
  readDirectoryV2Intent,
  discardDirectoryV2Intent,
} from "./directory-v2-intent";
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
import DirectoryV3Workspace from "./DirectoryV3Workspace";
import { ApiError, type Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import type { Task } from "./task-api";
import {
  parseDirectoryObservation as parseLegacyObservation,
  parseDirectoryTask as parseLegacyTask,
} from "./directory-api";
import {
  readDirectoryIntent as readLegacyIntent,
  saveDirectoryIntent as saveLegacyIntent,
  discardDirectoryIntent as discardLegacyIntent,
} from "./directory-intent";
import {
  directoryV2BodyLimit,
  directoryV2DisplayText,
  readDirectoryV2JSON,
} from "./directory-v2-json";
import {
  directoryV3API,
  directoryV3Error,
  directoryV3Operations,
  parseDirectoryV3Observation,
  parseDirectoryV3Task,
  validDirectoryV3Input,
  uncertainDirectoryV3Error,
  type DirectoryV3Object,
  type DirectoryV3ObservedCounts,
  type DirectoryV3Query,
} from "./directory-v3-api";
import {
  discardDirectoryV3Intent,
  readDirectoryV3Intent,
  saveDirectoryV3Intent,
  type DirectoryV3Intent,
} from "./directory-v3-intent";

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
const query: DirectoryV3Query = {
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
  kind: DirectoryV3Object["kind"] = "user",
): DirectoryV3Object => ({
  objectGUID: `00000000-0000-0000-0000-${String(index).padStart(12, "0")}`,
  distinguishedName: `CN=Object ${index},DC=synthetic,DC=invalid`,
  kind,
  objectClass: ["top", kind].sort(),
  samAccountName: `observed-${index}`,
  userAccountControl: 512,
  objectSid: "S-1-5-21-1-2-3-500",
  mail: "Observed@Synthetic.invalid",
  description: ["Synthetic directory observation"],
  whenCreated: "0001-01-01T00:00:00Z",
  primaryGroupID: 0,
  servicePrincipalName: ["HOST/Z", "host/a\u0000\n😀<script>", "HOST/a"],
});
const observation = (
  rows = [row()],
  q = query,
  total = rows.length,
  observationId = "observation-one",
  observedCounts: DirectoryV3ObservedCounts = rows.reduce(
    (counts, value) => ({ ...counts, [value.kind]: counts[value.kind] + 1 }),
    { user: 0, group: 0, computer: 0 },
  ),
) => ({
  dictionaryVersion: 3 as const,
  available: true,
  observationId,
  source,
  observedCounts,
  list: rows,
  page: {
    pageIdx: q.pageIdx,
    pageSize: q.pageSize,
    total,
    totalPage: Math.ceil(total / q.pageSize),
  },
});
const absent = () => ({
  dictionaryVersion: 3 as const,
  available: false,
  observedCounts: null,
  list: [],
  page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
});
const intent = (extra: Partial<DirectoryV3Intent> = {}): DirectoryV3Intent => ({
  domainId: choice.domainId,
  expectedRevision: choice.revision,
  expectedCredentialGeneration: choice.credentialRevision,
  idempotencyKey: "original-directory-key",
  ...extra,
});
const task = (extra: Partial<Task> = {}): Task => ({
  taskUUID: "directory-task-one",
  taskName: "domain.directory_read.v3",
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
    <DirectoryV3Workspace profile={profile} sessionChanged={sessionChanged} />,
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
    discardDirectoryV3Intent(p);
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
    if (path === "/api/directory/v3/observation")
      return response(observation());
    if (path === "/api/directory/v3/sync")
      return response({ task: task(), replayed: false });
    if (
      path === "/api/directory/v3/receipt" ||
      path === "/api/directory/v3/task"
    )
      return response({ task: task() });
    if (path === "/api/directory/v3/cancel")
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
  it("requires dictionary 3 on unavailable and available observations without widening the legacy parser", () => {
    const {
      dictionaryVersion: _,
      observedCounts: __,
      ...legacyAbsent
    } = absent();
    expect(parseLegacyObservation(legacyAbsent, query).available).toBe(false);
    expect(() => parseDirectoryV3Observation(legacyAbsent, query)).toThrow(
      "invalid_response",
    );
    for (const value of [absent(), observation()]) {
      expect(() => parseLegacyObservation(value, query)).toThrow(
        "invalid_response",
      );
      for (const dictionaryVersion of [1, 2, 4])
        expect(() =>
          parseDirectoryV3Observation({ ...value, dictionaryVersion }, query),
        ).toThrow("unsupported_directory_profile");
      for (const dictionaryVersion of [null, "2", 2.5, 0])
        expect(() =>
          parseDirectoryV3Observation({ ...value, dictionaryVersion }, query),
        ).toThrow("invalid_response");
    }
    expect(() => parseLegacyTask(task(), choice.domainId)).toThrow(
      "invalid_response",
    );
    expect(() =>
      parseDirectoryV3Task(
        { ...task(), taskName: "domain.directory_read" },
        choice.domainId,
      ),
    ).toThrow("invalid_response");
  });
  it("preserves supplemental raw text, explicit nulls, canonical SID and year 0001 without aliases", () => {
    const values = {
      ...row(),
      mail: " Mixed@Example.test \u0000\n\t😀",
      description: ["Line 1\u0000\n\t😀\u202e"] as [string],
      whenCreated: "0001-01-01T00:00:00Z",
    };
    expect(
      parseDirectoryV3Observation(observation([values]), query).list,
    ).toEqual([values]);
    const missing = {
      ...row(),
      objectSid: null,
      mail: null,
      description: null,
      whenCreated: null,
    };
    expect(
      parseDirectoryV3Observation(observation([missing]), query).list,
    ).toEqual([missing]);
    for (const objectSid of [
      "S-1-0-0",
      "S-1-4294967295-4294967295",
      "S-1-0x000100000000-1",
      "S-1-0xffffffffffff-1-2-3-4-5",
    ])
      expect(
        parseDirectoryV3Observation(
          observation([{ ...row(), objectSid }]),
          query,
        ).list[0].objectSid,
      ).toBe(objectSid);
    expect(
      parseDirectoryV3Observation(
        observation([
          { ...row(), mail: "😀".repeat(128), description: ["😀".repeat(512)] },
        ]),
        query,
      ).list,
    ).toHaveLength(1);
  });
  it.each([
    { objectSid: "S-1-05-1" },
    { objectSid: "S-1-4294967296-1" },
    { objectSid: "S-1-0x000000000005-1" },
    { objectSid: "S-1-0xFFFFFFFFFFFF-1" },
    { objectSid: "S-1-5" },
    { objectSid: "S-1-5-1-2-3-4-5-6" },
    { objectSid: "S-1-5-4294967296" },
    { objectSid: "S-1-5-01" },
    { objectSid: "" },
    { mail: "" },
    { mail: "😀".repeat(129) },
    { mail: "x".repeat(257) },
    { mail: "\ud800" },
    { mail: 4 },
    { description: [] },
    { description: [""] },
    { description: [null] },
    { description: ["a", "b"] },
    { description: "scalar" },
    { description: ["x".repeat(1025)] },
    { description: ["\udfff"] },
    { whenCreated: "0000-01-01T00:00:00Z" },
    { whenCreated: "2026-02-30T00:00:00Z" },
    { whenCreated: "2026-01-01T00:00:60Z" },
    { whenCreated: "2026-01-01T00:00:00.0Z" },
    { whenCreated: "2026-01-01T00:00:00+00:00" },
    { whenCreated: "" },
    { mailBytes: "eA==" },
    { email: "inferred@example.test" },
  ])(
    "rejects malformed, widened or unsupported supplemental field %#",
    (extra) => {
      expect(() =>
        parseDirectoryV3Observation(
          { ...observation(), list: [{ ...row(), ...extra }] },
          query,
        ),
      ).toThrow("invalid_response");
    },
  );
  it("requires every supplemental key even when null", () => {
    for (const key of [
      "objectSid",
      "mail",
      "description",
      "whenCreated",
    ] as const) {
      const value: Partial<DirectoryV3Object> = { ...row() };
      delete value[key];
      expect(() =>
        parseDirectoryV3Observation({ ...observation(), list: [value] }, query),
      ).toThrow("invalid_response");
    }
  });
  it("distinguishes missing observations from observed empty lists and preserves source timestamps", () => {
    expect(parseDirectoryV3Observation(absent(), query).available).toBe(false);
    const empty = parseDirectoryV3Observation(observation([]), query);
    expect(empty.available).toBe(true);
    expect(empty.source?.started_at).toBe(source.started_at);
    expect(empty.list).toEqual([]);
    expect(parseDirectoryV3Observation(observation(), query).list).toEqual([
      row(),
    ]);
  });
  it("validates full-observation counts independently of kind and page", () => {
    const counts = { user: 51, group: 2, computer: 1 };
    for (const kind of ["", "user", "group", "computer"] as const) {
      const allRows = [
        ...Array.from({ length: 51 }, (_, i) => row(i + 1)),
        row(52, "group"),
        row(53, "group"),
        row(54, "computer"),
      ];
      const selected = allRows.filter((r) => !kind || r.kind === kind);
      for (const pageIdx of [1, 2, 3]) {
        const q = { ...query, kind, pageIdx, pageSize: 25 };
        const value = observation(
          selected.slice((pageIdx - 1) * 25, pageIdx * 25),
          q,
          selected.length,
          "observation-one",
          counts,
        );
        expect(parseDirectoryV3Observation(value, q).observedCounts).toEqual(
          counts,
        );
      }
    }
    expect(
      parseDirectoryV3Observation(absent(), query).observedCounts,
    ).toBeNull();
    expect(
      parseDirectoryV3Observation(observation([]), query).observedCounts,
    ).toEqual({ user: 0, group: 0, computer: 0 });
  });
  it.each([
    undefined,
    null,
    [],
    {},
    { user: 1, group: 0 },
    { user: 1, group: 0, computer: 0, password: "private" },
    { user: -1, group: 1, computer: 1 },
    { user: 0.5, group: 0.5, computer: 0 },
    { user: "1", group: 0, computer: 0 },
    { user: 10001, group: 0, computer: 0 },
    { user: 9999, group: 1, computer: 1 },
    { user: 0, group: 1, computer: 0 },
    { user: 2, group: 0, computer: 0 },
  ])(
    "rejects malformed or contradictory observed counts %#",
    (observedCounts) => {
      expect(() =>
        parseDirectoryV3Observation({ ...observation(), observedCounts }, query),
      ).toThrow("invalid_response");
    },
  );
  it("requires the count key, null for unavailable, and exact active-kind totals", () => {
    const { observedCounts: _, ...missing } = observation();
    const { observedCounts: __, ...missingAbsent } = absent();
    for (const value of [
      missing,
      missingAbsent,
      { ...absent(), observedCounts: { user: 0, group: 0, computer: 0 } },
    ])
      expect(() => parseDirectoryV3Observation(value, query)).toThrow(
        "invalid_response",
      );
    const q: DirectoryV3Query = { ...query, kind: "user" };
    expect(() =>
      parseDirectoryV3Observation(
        { ...observation(), observedCounts: { user: 2, group: 0, computer: 0 } },
        q,
      ),
    ).toThrow("invalid_response");
    // The full sum matches, but a partial page contradicts the per-kind counts.
    expect(() =>
      parseDirectoryV3Observation(
        observation(
          Array.from({ length: 50 }, (_, i) => row(i + 1)),
          query,
          51,
          "observation-one",
          { user: 49, group: 2, computer: 0 },
        ),
        query,
      ),
    ).toThrow("invalid_response");
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
      expect(() => parseDirectoryV3Observation(value, query)).toThrow(
        "invalid_response",
      );
    expect(() =>
      parseDirectoryV3Observation(observation(), { ...query, kind: "group" }),
    ).toThrow("invalid_response");
    expect(() =>
      parseDirectoryV3Observation(observation(), {
        ...query,
        observationId: "other-observation",
      }),
    ).toThrow("invalid_response");
    expect(() =>
      parseDirectoryV3Observation(absent(), {
        ...query,
        observationId: "observation-one",
      }),
    ).toThrow("invalid_response");
  });
  it("accepts only the pinned directory task kind, scope, single attempt and empty public result/cursor", () => {
    expect(
      parseDirectoryV3Task(task(), choice.domainId, task().taskUUID),
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
        parseDirectoryV3Task(
          { ...task(), ...extra },
          choice.domainId,
          task().taskUUID,
        ),
      ).toThrow("invalid_response");
  });
  it("preserves string revisions and rejects invalid requests before transport", async () => {
    expect(validDirectoryV3Input(intent())).toBe(true);
    for (const extra of [
      { expectedRevision: 9007199254740993 },
      { expectedRevision: "01" },
      { expectedCredentialGeneration: "0" },
      { expectedRevision: "9223372036854775808" },
      { idempotencyKey: "short" },
    ])
      expect(validDirectoryV3Input({ ...intent(), ...extra })).toBe(false);
    for (const extra of [
      { pageIdx: 2 },
      { pageSize: 20 },
      { domainId: "../other" },
    ])
      await expect(
        directoryV3API.observation(
          { ...query, ...extra },
          profile.ID,
          signal(),
        ),
      ).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each([null, "2", "01"])(
    "rejects every successful directory route with foreign/missing actor header %s",
    async (actor) => {
      override = (url) => {
        const path = url.split("?")[0];
        if (path === "/api/directory/v3/observation")
          return response(observation(), 200, actor);
        if (path === "/api/directory/v3/sync")
          return response({ task: task(), replayed: false }, 200, actor);
        return response({ task: task() }, 200, actor);
      };
      const freshProof = {
        actorPassword: "Synthetic actor password 482",
        totpCode: "123456",
      };
      for (const read of [
        () => directoryV3API.observation(query, profile.ID, signal()),
        () =>
          directoryV3API.sync(
            intent(),
            freshProof,
            profile.csrfToken,
            profile.ID,
            signal(),
          ),
        () => directoryV3API.receipt(intent(), profile.ID, signal()),
        () =>
          directoryV3API.task(
            choice.domainId,
            task().taskUUID,
            profile.ID,
            signal(),
          ),
        () =>
          directoryV3API.cancel(
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
      await directoryV3API.observation(query, profile.ID, signal());
    } catch (error) {
      expect(directoryV3Error(error)).not.toContain(privateText);
    }
    expect(directoryV3Error(new ApiError(privateText))).not.toContain(
      privateText,
    );
  });
});

describe("directory observations and access", () => {
  it("renders factual values as inert text with visible control/bidi escapes and no links", async () => {
    const mail = " Mixed@Example.test ";
    const description = "<img src=x onerror=alert(1)>\u0000\n\t😀\u202e";
    override = (url) =>
      url.startsWith("/api/directory/v3/observation")
        ? response(
            observation([
              { ...row(), mail, description: [description] },
              {
                ...row(2),
                samAccountName: null,
                userAccountControl: null,
                objectSid: null,
                mail: null,
                description: null,
                whenCreated: null,
              },
            ]),
          )
        : undefined;
    await start();
    const table = await screen.findByRole("table", { name: "目录对象观察" });
    expect(within(table).getByText("Mixed@Example.test").textContent).toBe(
      mail,
    );
    expect(
      within(table).getByText(directoryV2DisplayText(description)),
    ).toBeVisible();
    expect(within(table).getByText("0001-01-01T00:00:00Z")).toBeVisible();
    expect(within(table).getAllByText("未返回")).toHaveLength(6);
    expect(table.querySelector("a,img,script")).toBeNull();
    expect(table.textContent).not.toMatch(/[\u0000\u202e]/u);
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((header) => header.textContent),
    ).toEqual([
      "对象 GUID",
      "类型",
      "名称",
      "可分辨名称",
      "对象类",
      "账户控制值",
      "objectSid",
      "mail",
      "description",
      "whenCreated（UTC）",
      "primaryGroupID",
      "servicePrincipalName",
    ]);
    // Explicit scope keeps native column associations and browser role queries
    // consistent; Testing Library's implicit TH role accepts unscoped headers.
    for (const header of within(table).getAllByRole("columnheader"))
      expect(header).toHaveAttribute("scope", "col");
    expect(screen.queryByRole("button", { name: /导出/ })).toBeNull();
  });
  it.each(["available", "absent", "empty", "error"])(
    "renders %s without conflating unavailable, empty, or failed reads",
    async (state) => {
      override = (url) =>
        url.split("?")[0] === "/api/directory/v3/observation"
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
        expect(screen.getByText(/不是 AD 时间点快照/)).toBeVisible();
        expect(screen.getByText(/未观察到对象不能判断已删除/)).toBeVisible();
      } else if (state === "absent")
        await screen.findByText(
          "尚无当前配置的成功目录观察。此状态不表示目录为空。",
        );
      else if (state === "empty")
        await screen.findByText("已成功观察，当前筛选结果为空。");
      else await screen.findByRole("alert");
      if (state === "empty")
        expect(screen.getByLabelText("本次观测中的对象数量")).toHaveTextContent(
          "本次观测中的对象数量：用户 0 · 组 0 · 计算机 0",
        );
      if (state === "absent")
        expect(screen.getByLabelText("本次观测中的对象数量")).toHaveTextContent(
          "暂无可用观测",
        );
      if (state === "error")
        expect(screen.queryByLabelText("本次观测中的对象数量")).toBeNull();
      if (state !== "available")
        expect(
          screen.queryByRole("table", { name: "目录对象观察" }),
        ).toBeNull();
      if (state !== "empty")
        expect(screen.queryByText("已成功观察，当前筛选结果为空。")).toBeNull();
    },
  );
  it("shows the same mixed counts across filters and pages, then replaces them on a new observation", async () => {
    let refreshed = false;
    override = (url) => {
      if (url.split("?")[0] !== "/api/directory/v3/observation") return;
      const p = new URL(url, "https://local.invalid").searchParams;
      const q: DirectoryV3Query = {
        domainId: choice.domainId,
        kind: (p.get("kind") ?? "") as DirectoryV3Query["kind"],
        pageIdx: Number(p.get("pageIdx")),
        pageSize: Number(p.get("pageSize")),
      };
      const full = refreshed
        ? [row(1, "computer")]
        : [
            ...Array.from({ length: 51 }, (_, i) => row(i + 1)),
            row(52, "group"),
            row(53, "group"),
            row(54, "computer"),
          ];
      const selected = full.filter((r) => !q.kind || r.kind === q.kind);
      return response(
        observation(
          selected.slice((q.pageIdx - 1) * q.pageSize, q.pageIdx * q.pageSize),
          q,
          selected.length,
          refreshed ? "observation-two" : "observation-one",
          refreshed
            ? { user: 0, group: 0, computer: 1 }
            : { user: 51, group: 2, computer: 1 },
        ),
      );
    };
    await start();
    const expectCounts = async () => {
      await waitFor(() =>
        expect(screen.getByLabelText("本次观测中的对象数量")).toHaveTextContent(
          "本次观测中的对象数量：用户 51 · 组 2 · 计算机 1",
        ),
      );
    };
    await expectCounts();
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "目录观察分页" }),
      ).getByRole("button", { name: "下一页" }),
    );
    await screen.findByText("observed-54");
    await expectCounts();
    expect(params("/api/directory/v3/observation").observationId).toBe(
      "observation-one",
    );
    for (const kind of ["user", "group", "computer"]) {
      fill("目录对象类型", kind);
      await expectCounts();
    }
    fill("目录每页条数", "25");
    await expectCounts();
    refreshed = true;
    click("刷新目录观察");
    await screen.findByText(/观察编号：observation-two/);
    expect(screen.getByLabelText("本次观测中的对象数量")).toHaveTextContent(
      "本次观测中的对象数量：用户 0 · 组 0 · 计算机 1",
    );
  });
  it("uses pinned pages and resets the observation pin on kind, page size, and explicit refresh", async () => {
    override = (url) => {
      if (url.split("?")[0] !== "/api/directory/v3/observation") return;
      const p = new URL(url, "https://local.invalid").searchParams;
      const q: DirectoryV3Query = {
        domainId: choice.domainId,
        kind: (p.get("kind") ?? "") as DirectoryV3Query["kind"],
        pageIdx: Number(p.get("pageIdx")),
        pageSize: Number(p.get("pageSize")),
      };
      const full = [
        ...Array.from({ length: 51 }, (_, i) => row(i + 1)),
        ...Array.from({ length: 51 }, (_, i) => row(i + 52, "group")),
      ];
      const selected = full.filter((r) => !q.kind || r.kind === q.kind);
      return response(
        observation(
          selected.slice((q.pageIdx - 1) * q.pageSize, q.pageIdx * q.pageSize),
          q,
          selected.length,
          "observation-one",
          { user: 51, group: 51, computer: 0 },
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
    expect(params("/api/directory/v3/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "2",
      pageSize: "50",
      observationId: "observation-one",
    });
    fill("目录对象类型", "group");
    await screen.findByText("observed-52");
    expect(params("/api/directory/v3/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "1",
      pageSize: "50",
      kind: "group",
    });
    next();
    await screen.findByText("observed-102");
    fill("目录每页条数", "25");
    await screen.findByText("observed-52");
    expect(params("/api/directory/v3/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "1",
      pageSize: "25",
      kind: "group",
    });
    next();
    await screen.findByText("observed-77");
    click("刷新目录观察");
    await screen.findByText("observed-52");
    expect(params("/api/directory/v3/observation")).toEqual({
      domainId: choice.domainId,
      pageIdx: "1",
      pageSize: "25",
      kind: "group",
    });
  });
  it("stops on a stale observation 409 and requires an explicit unpinned refresh", async () => {
    override = (url) =>
      url.split("?")[0] === "/api/directory/v3/observation"
        ? url.includes("observationId=")
          ? response({ error: "directory_observation_unavailable" }, 409)
          : response(
              observation(
                Array.from({ length: 50 }, (_, i) => row(i + 1)),
                query,
                51,
                "observation-one",
                { user: 51, group: 0, computer: 0 },
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
    expect(screen.queryByLabelText("本次观测中的对象数量")).toBeNull();
    expect(calls("/api/directory/v3/observation")).toHaveLength(2);
    click("刷新目录观察");
    await screen.findByText("observed-1");
    expect(params("/api/directory/v3/observation")).toEqual({
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
              "GET /api/directory/v3/observation",
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
      render(
        <DirectoryV3Workspace profile={profile} sessionChanged={vi.fn()} />,
      );
      await screen.findByText("当前账户没有目录资产读取权限。");
      expect(calls("/api/domain-selection")).toHaveLength(0);
      expect(calls("/api/directory/v3/observation")).toHaveLength(0);
    },
  );
  it.each([
    "GET /api/domain-selection",
    "GET /api/domain-selection/resolve",
    "GET /api/directory/v3/observation",
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
      render(
        <DirectoryV3Workspace profile={profile} sessionChanged={vi.fn()} />,
      );
      await waitFor(() =>
        expect(screen.queryByText("正在核对目录访问权限…")).toBeNull(),
      );
      expect(calls("/api/domain-selection")).toHaveLength(0);
      expect(calls("/api/directory/v3/observation")).toHaveLength(0);
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
    const [, init] = calls("/api/directory/v3/sync")[0];
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
    expect(readDirectoryV3Intent(profile)).toEqual({
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
      if (url === "/api/directory/v3/sync")
        return Promise.reject(new TypeError("connection lost"));
      if (url.split("?")[0] === "/api/directory/v3/receipt")
        return receipt.promise;
    };
    await start();
    proof();
    click("提交目录同步");
    await screen.findByRole("heading", { name: "核对原目录同步回执" });
    await waitFor(() =>
      expect(calls("/api/directory/v3/receipt")).toHaveLength(1),
    );
    const original = JSON.parse(calls("/api/directory/v3/sync")[0][1].body);
    expect(params("/api/directory/v3/receipt")).toEqual({
      domainId: choice.domainId,
      idempotencyKey: original.idempotencyKey,
    });
    expect(
      screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
    ).toBeNull();
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
    await receipt.resolve({ error: "not_found" }, 404);
    await screen.findByRole("button", { name: "使用原编号和版本重试同步" });
    expect(
      screen.getByLabelText("操作者当前密码", { exact: true }),
    ).toHaveValue("");
    expect(
      screen.getByLabelText("未使用的认证器验证码", { exact: true }),
    ).toHaveValue("");
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
    override = () => undefined;
    proof("654321");
    click("使用原编号和版本重试同步");
    await screen.findByText(/服务器状态：已成功/);
    expect(calls("/api/directory/v3/sync")).toHaveLength(2);
    expect(JSON.parse(calls("/api/directory/v3/sync")[1][1].body)).toEqual({
      ...original,
      totpCode: "654321",
    });
    expect(
      fetcher.mock.calls.findIndex(([url]) =>
        url.startsWith("/api/directory/v3/receipt?"),
      ),
    ).toBeGreaterThan(
      fetcher.mock.calls.findIndex(([url]) => url === "/api/directory/v3/sync"),
    );
  });
  it("keeps a conflicted unknown original receipt-only across navigation and never offers stale revision retry", async () => {
    const original = intent();
    saveDirectoryV3Intent(profile, original);
    override = (url) =>
      url.split("?")[0] === "/api/directory/v3/receipt"
        ? response({ error: "not_found" }, 404)
        : url === "/api/directory/v3/sync"
          ? response({ error: "revision_conflict" }, 409)
          : undefined;
    await mount();
    await screen.findByRole("button", { name: "使用原编号和版本重试同步" });
    proof();
    click("使用原编号和版本重试同步");
    await screen.findByText(/原配置或凭据版本已冲突/);
    expect(readDirectoryV3Intent(profile)).toEqual({
      ...original,
      receiptOnly: true,
    });
    expect(
      screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
    ).toBeNull();
    expect(
      screen.queryByLabelText("操作者当前密码", { exact: true }),
    ).toBeNull();
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
    expect(
      JSON.parse(calls("/api/directory/v3/sync")[0][1].body).expectedRevision,
    ).toBe(original.expectedRevision);
    override = (url) =>
      url.split("?")[0] === "/api/directory/v3/receipt"
        ? response({ error: "not_found" }, 404)
        : url.split("?")[0] === "/api/domain-selection/resolve"
          ? response({
              selection: choice,
              checkedAt: "2026-10-07T00:20:00Z",
            })
          : undefined;
    await selectSource();
    expect(readDirectoryV3Intent(profile)).toEqual({
      ...original,
      receiptOnly: true,
    });
    expect(
      screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
    ).toBeNull();
    cleanup();
    // A fresh module has no in-memory recovery map: this read proves persisted
    // reload recovery keeps the receipt-only restriction and original pins.
    vi.resetModules();
    const fresh = await import("./directory-v3-intent");
    expect(fresh.readDirectoryV3Intent(profile)).toEqual({
      ...original,
      receiptOnly: true,
    });
    await mount();
    await screen.findByText(/原配置或凭据版本已冲突/);
    click("查询原同步回执");
    await screen.findByText(/原配置或凭据版本已冲突/);
    expect(
      screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
    ).toBeNull();
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
    expect(readDirectoryV3Intent(profile)).toEqual({
      ...original,
      receiptOnly: true,
    });
    override = () => undefined;
    click("查询原同步回执");
    await screen.findByText(/服务器状态：已成功/);
    expect(readDirectoryV3Intent(profile)).toEqual({
      ...original,
      receiptOnly: true,
      taskUUID: task().taskUUID,
    });
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
  });
  it("uses a recovered receipt directly and never submits a replacement synchronization", async () => {
    saveDirectoryV3Intent(profile, intent());
    await mount();
    await screen.findByText(/服务器状态：已成功/);
    expect(params("/api/directory/v3/receipt")).toEqual({
      domainId: choice.domainId,
      idempotencyKey: intent().idempotencyKey,
    });
    expect(calls("/api/directory/v3/sync")).toHaveLength(0);
    expect(readDirectoryV3Intent(profile)?.taskUUID).toBe(task().taskUUID);
  });
  it("stops a definitively disabled submission and reopens only after explicit source review", async () => {
    override = (url) =>
      url === "/api/directory/v3/sync"
        ? response({ error: "directory_read_disabled" }, 503)
        : undefined;
    await start();
    proof();
    click("提交目录同步");
    await screen.findByText(/当前部署未开启目录读取/);
    expect(screen.getByRole("button", { name: "提交目录同步" })).toBeDisabled();
    expect(readDirectoryV3Intent(profile)).toBeNull();
    expect(calls("/api/directory/v3/receipt")).toHaveLength(0);
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
    await selectSource();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "提交目录同步" }),
      ).toBeEnabled(),
    );
    expect(
      screen.getByLabelText("操作者当前密码", { exact: true }),
    ).toHaveValue("");
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
  });
  it("renders task failure without echoing arbitrary backend diagnostics", async () => {
    saveDirectoryV3Intent(profile, intent({ taskUUID: task().taskUUID }));
    override = (url) =>
      url.split("?")[0] === "/api/directory/v3/task"
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
      saveDirectoryV3Intent(profile, intent());
      override = (url) =>
        url.split("?")[0] === "/api/directory/v3/receipt"
          ? response({ error: code }, Number(status))
          : undefined;
      await mount();
      await screen.findByRole("alert");
      expect(
        screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
      ).toBeNull();
      expect(calls("/api/directory/v3/sync")).toHaveLength(0);
      expect(readDirectoryV3Intent(profile)).toEqual(intent());
    },
  );
  it("projects recovery storage to nonsecret pins and isolates it by actor and username", () => {
    saveDirectoryV3Intent(profile, {
      ...intent(),
      actorPassword: "private-proof",
      totpCode: "876543",
      csrfToken: "private-csrf",
      payload: { secret: "private-source" },
    } as DirectoryV3Intent);
    expect(readDirectoryV3Intent(profile)).toEqual(intent());
    expect(readDirectoryV3Intent(otherProfile)).toBeNull();
    expect(
      readDirectoryV3Intent({ ...profile, username: "renamed-reader" }),
    ).toBeNull();
    const stored = JSON.stringify(Object.values(sessionStorage));
    expect(stored).not.toMatch(
      /private|876543|actorPassword|totpCode|csrfToken|payload/u,
    );
    expect(localStorage.length).toBe(0);
  });
  it("keeps cancel_requested distinct from cancelled, polls to the actual terminal state, then stops", async () => {
    saveDirectoryV3Intent(profile, intent({ taskUUID: task().taskUUID }));
    let current = task({ state: "running", progress: 10, terminalAt: null });
    override = (url) => {
      if (url.split("?")[0] === "/api/directory/v3/task")
        return response({ task: current });
      if (url === "/api/directory/v3/cancel") {
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
    expect(JSON.parse(calls("/api/directory/v3/cancel")[0][1].body)).toEqual({
      taskUUID: task().taskUUID,
      actorPassword: "Synthetic actor password 482",
      totpCode: "123456",
    });
    const beforePoll = calls("/api/directory/v3/task").length;
    current = task({ state: "cancelled", progress: 10 });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(calls("/api/directory/v3/task")).toHaveLength(beforePoll + 1);
    expect(screen.getByText(/服务器状态：已取消/)).toBeVisible();
    expect(
      screen.getByRole("button", { name: "结束此任务查看" }),
    ).toBeVisible();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });
    expect(calls("/api/directory/v3/task")).toHaveLength(beforePoll + 1);
    click("结束此任务查看");
    expect(readDirectoryV3Intent(profile)).toBeNull();
  });
  it.each(["invalid_credentials", "network"])(
    "keeps the %s cancellation notice while reconciling actual status and clearing proof",
    async (failure) => {
      saveDirectoryV3Intent(profile, intent({ taskUUID: task().taskUUID }));
      override = (url) =>
        url.startsWith("/api/directory/v3/task")
          ? response({
              task: task({ state: "running", progress: 10, terminalAt: null }),
            })
          : url === "/api/directory/v3/cancel"
            ? failure === "network"
              ? Promise.reject(new TypeError("network"))
              : response({ error: failure }, 401)
            : undefined;
      await mount();
      await screen.findByRole("button", { name: "请求取消目录同步" });
      const before = calls("/api/directory/v3/task").length;
      proof();
      click("请求取消目录同步");
      await waitFor(() =>
        expect(calls("/api/directory/v3/task").length).toBeGreaterThan(before),
      );
      await screen.findByRole("button", { name: "请求取消目录同步" });
      expect(screen.getByRole("alert")).toHaveTextContent(
        failure === "network"
          ? "取消请求结果尚未确认。已重新查询任务状态，请以服务器状态为准。"
          : directoryV3Error(new ApiError(failure, 401)),
      );
      expect(
        screen.getByLabelText("操作者当前密码", { exact: true }),
      ).toHaveValue("");
      expect(
        screen.getByLabelText("未使用的认证器验证码", { exact: true }),
      ).toHaveValue("");
      expect(calls("/api/directory/v3/cancel")).toHaveLength(1);
      expect(readDirectoryV3Intent(profile)?.taskUUID).toBe(task().taskUUID);
    },
  );
  it("preserves cancel proof across running task heartbeats and clears it immediately on submit", async () => {
    saveDirectoryV3Intent(profile, intent({ taskUUID: task().taskUUID }));
    const cancelResponse = deferred();
    let current = task({ state: "running", progress: 10, terminalAt: null });
    override = (url) => {
      if (url.split("?")[0] === "/api/directory/v3/task")
        return response({ task: current });
      if (url === "/api/directory/v3/cancel") return cancelResponse.promise;
    };
    await mount();
    await screen.findByRole("button", { name: "请求取消目录同步" });
    vi.useFakeTimers();
    await act(async () => click("刷新目录任务"));
    const passwordField = screen.getByLabelText("操作者当前密码");
    const codeField = screen.getByLabelText("未使用的认证器验证码");
    fill("操作者当前密码", "Partially typed password");
    fill("未使用的认证器验证码", "123");
    const beforePoll = calls("/api/directory/v3/task").length;
    current = { ...current, updatedAt: "2026-10-07T00:00:02Z" };
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(calls("/api/directory/v3/task")).toHaveLength(beforePoll + 1);
    expect(screen.getByLabelText("操作者当前密码")).toBe(passwordField);
    expect(screen.getByLabelText("未使用的认证器验证码")).toBe(codeField);
    expect(passwordField).toHaveValue("Partially typed password");
    expect(codeField).toHaveValue("123");
    proof();
    await act(async () => click("请求取消目录同步"));
    expect(calls("/api/directory/v3/cancel")).toHaveLength(1);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    expect(
      JSON.parse(calls("/api/directory/v3/cancel")[0][1].body),
    ).toMatchObject({
      actorPassword: "Synthetic actor password 482",
      totpCode: "123456",
    });
    current = { ...current, state: "cancel_requested" };
    await cancelResponse.resolve({ task: current });
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
  });
  it.each(["session", "task"])(
    "clears cancel proof when the %s boundary changes",
    async (boundary) => {
      saveDirectoryV3Intent(profile, intent({ taskUUID: task().taskUUID }));
      let current = task({ state: "running", terminalAt: null });
      override = (url) =>
        url.split("?")[0] === "/api/directory/v3/task"
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
          url.split("?")[0] === "/api/directory/v3/task"
            ? response({ task: current })
            : url === "/api/directory/v3/sync"
              ? response({ task: current, replayed: false })
              : undefined;
        proof("654321");
        click("提交目录同步");
      } else {
        mounted.rerender(
          <DirectoryV3Workspace
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
  it("clears counts and source details when a refreshed read loses permission", async () => {
    await start();
    await screen.findByText("observed-1");
    expect(screen.getByLabelText("本次观测中的对象数量")).toBeVisible();
    override = (url) =>
      url.split("?")[0] === "/api/directory/v3/observation"
        ? response({ error: "forbidden" }, 403)
        : undefined;
    click("刷新目录观察");
    await screen.findByRole("alert");
    expect(screen.queryByLabelText("本次观测中的对象数量")).toBeNull();
    expect(screen.queryByText(/观察编号：/)).toBeNull();
    expect(screen.queryByText(/服务器名称：/)).toBeNull();
  });
  it("discards late counts when a newly resolved source replaces the observation", async () => {
    const pending = deferred();
    let reads = 0;
    override = (url) => {
      if (url.split("?")[0] !== "/api/directory/v3/observation") return;
      reads += 1;
      return reads === 1
        ? pending.promise
        : response(observation([row(2, "group")], query, 1, "observation-new"));
    };
    await start();
    await waitFor(() => expect(reads).toBe(1));
    const requestSignal = calls("/api/directory/v3/observation")[0][1]
      .signal as AbortSignal;
    await selectSource();
    await screen.findByText(/观察编号：observation-new/);
    expect(requestSignal.aborted).toBe(true);
    await pending.resolve(observation());
    expect(screen.getByLabelText("本次观测中的对象数量")).toHaveTextContent(
      "本次观测中的对象数量：用户 0 · 组 1 · 计算机 0",
    );
    expect(screen.queryByText(/观察编号：observation-one/)).toBeNull();
    expect(screen.queryByText("observed-1")).toBeNull();
  });
  it("removes protected data and notifies session expiry on a foreign-actor success", async () => {
    const lost = vi.fn();
    await start(lost);
    await screen.findByText("observed-1");
    override = (url) =>
      url.split("?")[0] === "/api/directory/v3/observation"
        ? response(observation(), 200, "2")
        : undefined;
    click("刷新目录观察");
    await waitFor(() => expect(lost).toHaveBeenCalledOnce());
    expect(screen.queryByRole("table", { name: "目录对象观察" })).toBeNull();
    expect(screen.queryByRole("radio")).toBeNull();
    expect(screen.queryByLabelText("本次观测中的对象数量")).toBeNull();
  });
  it.each(["actor", "csrf"])(
    "aborts late observation after %s switch even when the transport ignores AbortSignal",
    async (change) => {
      const pending = deferred();
      override = (url) =>
        url.split("?")[0] === "/api/directory/v3/observation"
          ? pending.promise
          : undefined;
      const mounted = await start();
      // The region mounts before its read effect necessarily dispatches.
      // Switch identity only after proving the delayed request is in flight.
      await waitFor(() =>
        expect(calls("/api/directory/v3/observation")).toHaveLength(1),
      );
      const requestSignal = calls("/api/directory/v3/observation")[0][1]
        .signal as AbortSignal;
      const nextProfile =
        change === "actor"
          ? otherProfile
          : { ...profile, csrfToken: "new-directory-session" };
      mounted.rerender(
        <DirectoryV3Workspace profile={nextProfile} sessionChanged={vi.fn()} />,
      );
      expect(requestSignal.aborted).toBe(true);
      await pending.resolve(observation());
      expect(screen.queryByRole("table", { name: "目录对象观察" })).toBeNull();
      expect(screen.queryByText("observed-1")).toBeNull();
      expect(screen.queryByLabelText("本次观测中的对象数量")).toBeNull();
    },
  );
  it("aborts late synchronization on actor switch and never attributes the old receipt to the new actor", async () => {
    const pending = deferred();
    const mounted = await start();
    override = (url) =>
      url === "/api/directory/v3/sync" ? pending.promise : undefined;
    proof();
    click("提交目录同步");
    await waitFor(() =>
      expect(calls("/api/directory/v3/sync")).toHaveLength(1),
    );
    const requestSignal = calls("/api/directory/v3/sync")[0][1]
      .signal as AbortSignal;
    mounted.rerender(
      <DirectoryV3Workspace profile={otherProfile} sessionChanged={vi.fn()} />,
    );
    expect(requestSignal.aborted).toBe(true);
    await pending.resolve({ task: task(), replayed: false });
    expect(readDirectoryV3Intent(otherProfile)).toBeNull();
    expect(readDirectoryV3Intent(profile)?.taskUUID).toBeUndefined();
    expect(screen.queryByRole("region", { name: "目录同步任务" })).toBeNull();
    expect(calls("/api/directory/v3/task")).toHaveLength(0);
  });
  it("requests the directory route catalogue without relying on generic task submit/recovery", async () => {
    await mount();
    const checked = calls("/api/access/check").flatMap(
      ([, init]) => JSON.parse(init.body as string).paths,
    );
    expect(checked).toEqual(expect.arrayContaining([...directoryV3Operations]));
    expect(checked).not.toContain("POST /api/tasks/submit");
    expect(checked).not.toContain("POST /api/tasks/recover");
  });
});

describe("bounded dictionary 2 transport and isolated recovery", () => {
  it("accepts an exactly 8 MiB UTF-8 body and rejects a chunked or lying-length oversize body", async () => {
    const json = JSON.stringify(absent());
    const padded = json + " ".repeat(directoryV2BodyLimit - json.length);
    await expect(
      readDirectoryV2JSON(new Response(padded), signal()),
    ).resolves.toEqual(absent());
    const cancelled = vi.fn();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(padded));
        controller.enqueue(new Uint8Array([32]));
      },
      cancel: cancelled,
    });
    await expect(
      readDirectoryV2JSON(
        new Response(stream, { headers: { "Content-Length": "1" } }),
        signal(),
      ),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
    expect(cancelled).toHaveBeenCalledOnce();
    const excessive = new Response("{}", {
      headers: { "Content-Length": String(directoryV2BodyLimit + 1) },
    });
    await expect(
      readDirectoryV2JSON(excessive, signal()),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
  });
  it("rejects malformed UTF-8 and JSON and interrupts a stalled body on abort", async () => {
    for (const body of [new Uint8Array([0xc0, 0xaf]), "{", "", "null trailing"])
      await expect(
        readDirectoryV2JSON(new Response(body), signal()),
      ).rejects.toMatchObject({ code: "invalid_response" });
    const controller = new AbortController();
    const cancel = vi.fn();
    const pending = readDirectoryV2JSON(
      new Response(new ReadableStream<Uint8Array>({ cancel })),
      controller.signal,
    );
    controller.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(cancel).toHaveBeenCalledOnce();
  });
  it("does not lose an original mutation intent when a successful response is too large", async () => {
    override = (url) =>
      url === "/api/directory/v3/sync"
        ? Promise.resolve(
            new Response("{}", {
              headers: {
                "X-ADTR-User-ID": "1",
                "Content-Length": String(directoryV2BodyLimit + 1),
              },
            }),
          )
        : url.startsWith("/api/directory/v3/receipt")
          ? response({ error: "not_found" }, 404)
          : undefined;
    await start();
    proof();
    click("提交目录同步");
    await screen.findByText(
      "未找到原同步回执。重试将保留原域、版本和操作编号。",
    );
    expect(calls("/api/directory/v3/sync")).toHaveLength(1);
    expect(readDirectoryV3Intent(profile)).toMatchObject({
      domainId: choice.domainId,
    });
    expect(
      uncertainDirectoryV3Error(new ApiError("directory_limit_exceeded", 200)),
    ).toBe(true);
    expect(
      uncertainDirectoryV3Error(new ApiError("directory_limit_exceeded", 422)),
    ).toBe(false);
  });
  it("keeps original v1 and v3 sync keys independent and never silently upgrades a task", () => {
    discardLegacyIntent(profile);
    const legacy = {
      ...intent(),
      idempotencyKey: "legacy-original-key",
      taskUUID: "legacy-task",
    };
    saveLegacyIntent(profile, legacy);
    expect(readDirectoryV3Intent(profile)).toBeNull();
    saveDirectoryV3Intent(profile, intent());
    expect(readLegacyIntent(profile)).toEqual(legacy);
    expect(readDirectoryV3Intent(profile)).toEqual(intent());
    discardDirectoryV3Intent(profile);
    expect(readLegacyIntent(profile)).toEqual(legacy);
    expect(
      sessionStorage.getItem(
        "adtr.directory-sync-intent.v1:1:directory-reader",
      ),
    ).toContain("legacy-original-key");
    expect(
      sessionStorage.getItem(
        "adtr.directory-sync-intent.v3:1:directory-reader",
      ),
    ).toBeNull();
    discardLegacyIntent(profile);
  });
  it("distinguishes unsupported profiles, malformed responses and safety limits without leaking values", () => {
    const errors = [
      "unsupported_directory_profile",
      "invalid_directory_response",
      "directory_limit_exceeded",
    ];
    expect(
      new Set(errors.map((code) => directoryV3Error(new ApiError(code)))).size,
    ).toBe(3);
    for (const code of errors)
      expect(directoryV3Error(new ApiError(code))).not.toContain(code);
  });
});

describe("dictionary3 exact-profile isolation", () => {
  it("leaves existing v2 objects/tasks strict and never converts their null history", () => {
    const { primaryGroupID: _, servicePrincipalName: __, ...base } = row();
    const { observedCounts: ___, ...v3 } = observation();
    const v2 = { ...v3, dictionaryVersion: 2, list: [base] };
    expect(parseDirectoryV2Observation(v2, query).dictionaryVersion).toBe(2);
    expect(() => parseDirectoryV3Observation(v2, query)).toThrow(
      "unsupported_directory_profile",
    );
    expect(() =>
      parseDirectoryV3Observation({ ...v2, dictionaryVersion: 3 }, query),
    ).toThrow("invalid_response");
    expect(() => parseDirectoryV2Observation(observation(), query)).toThrow(
      "unsupported_directory_profile",
    );
    expect(() =>
      parseDirectoryV3Task(
        { ...task(), taskName: "domain.directory_read.v2" },
        choice.domainId,
      ),
    ).toThrow("invalid_response");
    expect(() => parseDirectoryV2Task(task(), choice.domainId)).toThrow(
      "invalid_response",
    );
  });
  it("keeps a pre-existing v2 unknown operation in its original namespace", () => {
    discardDirectoryV2Intent(profile);
    const older = { ...intent(), idempotencyKey: "same-cross-profile-key" };
    saveDirectoryV2Intent(profile, older);
    expect(readDirectoryV3Intent(profile)).toBeNull();
    saveDirectoryV3Intent(profile, older);
    discardDirectoryV3Intent(profile);
    expect(readDirectoryV2Intent(profile)).toEqual(older);
    discardDirectoryV2Intent(profile);
  });
});
