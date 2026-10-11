import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { StrictMode } from "react";
import MessagesWorkspace from "./MessagesWorkspace";
import { ApiError, type Profile } from "./api";
import {
  messageAPI,
  type MessageReadInput,
  type MessageReadReceipt,
} from "./message-api";
import {
  loadMessageReadIntents,
  persistMessageReadIntent,
  completeMessageReadIntent,
} from "./message-read-intent";
import { sampleSnapshot } from "./message-test-fixtures";
const profile: Profile = {
  ID: 1,
  username: "synthetic",
  role: "user",
  priv: 0,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: false,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 1,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "csrf",
};
const input: MessageReadInput = {
  resultId: "result-1",
  messageId: "health-1",
  operationId: "original-operation",
};
const receipt = (i: MessageReadInput = input): MessageReadReceipt => ({
  ...i,
  firstReadAt: "2026-10-11T00:01:00.123456Z",
  committedAt: "2026-10-11T00:01:00.123456Z",
  progress: {
    target: 1,
    processed: 1,
    newlyRead: 1,
    alreadyRead: 0,
    failed: 0,
    remaining: 0,
  },
});
const changed = vi.fn();
const json = (data: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(data), { status }));
let post: (i: MessageReadInput) => Promise<Response>,
  lookup: () => Promise<Response>,
  query: () => Promise<Response>;
let grants: boolean[];
const requests = (path: string) =>
  vi
    .mocked(fetch)
    .mock.calls.filter(
      ([url]) => String(url).split("?")[0] === `/api/messages/${path}`,
    );
beforeEach(() => {
  localStorage.clear();
  changed.mockReset();
  grants = [true, true, true, true, true, true, true, true];
  post = (i) => json(receipt(i));
  lookup = () => json(receipt());
  query = () => json(sampleSnapshot());
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init: RequestInit) => {
      if (url === "/api/access/menu")
        return json({
          menu: [{ mark: "tasks", auth: { readable: true, writeable: false } }],
        });
      if (url === "/api/access/check") return json({ results: grants });
      if (url === "/api/messages/query") return query();
      if (url === "/api/messages/read")
        return post(JSON.parse(String(init.body)));
      if (url.startsWith("/api/messages/read-operation?")) return lookup();
      if (url.startsWith("/api/messages/page?")) return json(sampleSnapshot());
      throw new Error(url);
    }),
  );
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
async function mount(p = profile, strict = false) {
  const ui = <MessagesWorkspace profile={p} sessionChanged={changed} />;
  const view = render(strict ? <StrictMode>{ui}</StrictMode> : ui);
  await screen.findByRole("button", { name: "确认查询" });
  if (loadMessageReadIntents(p).length && grants[3] && grants[4])
    await screen.findByRole("region", { name: "单条已读操作" });
  return view;
}
async function confirm() {
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await screen.findByRole("list", { name: "站内消息列表" });
}
const panel = () =>
  within(screen.getByRole("region", { name: "单条已读操作" }));
const begin = () =>
  fireEvent.click(screen.getByRole("button", { name: "标记为已读" }));
it("persists original IDs before the POST, uses only readable authority and preserves old membership/counts", async () => {
  post = (i) => {
    expect(loadMessageReadIntents(profile)).toEqual([i]);
    return json(receipt(i));
  };
  await mount();
  await confirm();
  begin();
  await screen.findByRole("heading", { name: "已确认单条已读" });
  expect(requests("read")).toHaveLength(1);
  expect(loadMessageReadIntents(profile)).toEqual([]);
  expect(
    screen.getByRole("article", { name: "消息 health-1" }),
  ).toHaveTextContent("查询时未读");
  expect(
    screen.getByRole("region", { name: "查询时消息统计" }),
  ).toHaveTextContent("查询时未读1查询时已读0");
  expect(screen.getByRole("button", { name: "本次已确认已读" })).toBeDisabled();
  expect(panel().getByText(/已处理 1 \/ 1/)).toHaveTextContent(
    "本次变为已读 1；此前已读 0；失败 0；剩余 0",
  );
  expect(requests("query")).toHaveLength(1);
});
it("an explicit fresh query alone updates counts and rows", async () => {
  await mount();
  await confirm();
  begin();
  await screen.findByRole("heading", { name: "已确认单条已读" });
  query = () =>
    json({
      ...sampleSnapshot(),
      resultId: "new-result",
      counts: { all: 1, unread: 0, read: 1 },
      items: [
        {
          ...sampleSnapshot().items[0],
          read: true,
          firstReadAt: receipt().firstReadAt,
        },
      ],
    });
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await screen.findByText("new-result");
  expect(
    screen.getByRole("article", { name: "消息 health-1" }),
  ).toHaveTextContent("查询时已读");
  expect(requests("read")).toHaveLength(1);
});
it("synchronously locks rapid duplicate clicks and preserves intent on unmount", async () => {
  let finish!: (value: Response) => void;
  post = () =>
    new Promise((r) => {
      finish = r;
    });
  const view = await mount();
  await confirm();
  const button = screen.getByRole("button", { name: "标记为已读" });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(requests("read")).toHaveLength(1);
  const saved = loadMessageReadIntents(profile);
  expect(saved).toHaveLength(1);
  view.unmount();
  await act(async () =>
    finish(new Response(JSON.stringify(receipt(saved[0])))),
  );
  expect(loadMessageReadIntents(profile)).toEqual(saved);
  await mount();
  expect(requests("read")).toHaveLength(1);
  expect(requests("read-operation")).toHaveLength(0);
  expect(
    screen.queryByRole("heading", { name: "已确认单条已读" }),
  ).not.toBeInTheDocument();
});
it.each(["network", "invalid_response", "internal"])(
  "%s response stays unknown until receipt confirmation; never recreates the operation",
  async (kind) => {
    post = (i) => {
      lookup = () => json(receipt(i));
      return kind === "network"
        ? Promise.reject(new Error("lost"))
        : kind === "invalid_response"
          ? Promise.resolve(new Response("broken"))
          : json({ error: "internal" }, 503);
    };
    await mount();
    await confirm();
    begin();
    await screen.findByText(/原操作结果尚未确认/);
    expect(
      screen.queryByRole("button", { name: "重放原操作" }),
    ).not.toBeInTheDocument();
    fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
    await screen.findByRole("heading", { name: "已确认单条已读" });
    expect(requests("read")).toHaveLength(1);
    expect(requests("read-operation")).toHaveLength(1);
  },
);
it("requires an actual not_found receipt before replay and rechecks receipt immediately before the write", async () => {
  persistMessageReadIntent(profile, input);
  lookup = () => json({ error: "not_found" }, 404);
  await mount(profile, true);
  await screen.findByRole("region", { name: "单条已读操作" });
  expect(requests("read")).toHaveLength(0);
  expect(requests("read-operation")).toHaveLength(0);
  expect(
    screen.queryByRole("button", { name: "重放原操作" }),
  ).not.toBeInTheDocument();
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  const replay = await screen.findByRole("button", { name: "重放原操作" });
  expect(panel().getByText(/404不能证明原操作未提交/)).toBeInTheDocument();
  fireEvent.click(replay);
  fireEvent.click(replay);
  await screen.findByRole("heading", { name: "已确认单条已读" });
  expect(requests("read")).toHaveLength(1);
  expect(JSON.parse(String(requests("read")[0][1]?.body))).toEqual(input);
  expect(requests("read-operation")).toHaveLength(2);
});
it("if the fresh replay lookup finds a receipt, no POST is sent", async () => {
  persistMessageReadIntent(profile, input);
  lookup = () => json({ error: "not_found" }, 404);
  await mount();
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  const replay = await screen.findByRole("button", { name: "重放原操作" });
  lookup = () => json(receipt());
  fireEvent.click(replay);
  await screen.findByRole("heading", { name: "已确认单条已读" });
  expect(requests("read")).toHaveLength(0);
});
it.each([401, 403])(
  "receipt %i clears private screen and cannot unlock replay",
  async (status) => {
    persistMessageReadIntent(profile, input);
    lookup = () =>
      json({ error: status === 401 ? "unauthenticated" : "forbidden" }, status);
    await mount();
    await confirm();
    fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("region", { name: "单条已读操作" }),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.queryByRole("list", { name: "站内消息列表" }),
    ).not.toBeInTheDocument();
    expect(requests("read")).toHaveLength(0);
    expect(loadMessageReadIntents(profile)).toEqual([input]);
    expect(changed).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
  },
);
it.each([401, 403])(
  "POST %i retains intent but clears authorization and private state",
  async (status) => {
    post = () =>
      json({ error: status === 401 ? "unauthenticated" : "forbidden" }, status);
    await mount();
    await confirm();
    begin();
    await waitFor(() =>
      expect(
        screen.queryByRole("list", { name: "站内消息列表" }),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.queryByRole("region", { name: "单条已读操作" }),
    ).not.toBeInTheDocument();
    expect(loadMessageReadIntents(profile)).toHaveLength(1);
  },
);
it.each([404, 410, 409])(
  "POST %i remains unconfirmed and offers receipt lookup",
  async (status) => {
    post = () => json({ error: "unavailable" }, status);
    await mount();
    await confirm();
    begin();
    await waitFor(() =>
      expect(
        panel().getByRole("button", { name: "查询原操作结果" }),
      ).toBeEnabled(),
    );
    expect(
      screen.queryByRole("heading", { name: "已确认单条已读" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "重放原操作" }),
    ).not.toBeInTheDocument();
    expect(loadMessageReadIntents(profile)).toHaveLength(1);
  },
);
it("snapshot expiration does not hide or block success receipt recovery", async () => {
  persistMessageReadIntent(profile, input);
  query = () => json({ error: "result_expired" }, 410);
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await screen.findByText(/固定结果已过期/);
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  await screen.findByRole("heading", { name: "已确认单条已读" });
  expect(requests("read")).toHaveLength(0);
});
it("same-user new CSRF can confirm old receipt without replaying old snapshot", async () => {
  persistMessageReadIntent(profile, input);
  const view = await mount();
  view.rerender(
    <MessagesWorkspace
      profile={{ ...profile, csrfToken: "new-session" }}
      sessionChanged={changed}
    />,
  );
  await screen.findByRole("button", { name: "确认查询" });
  await screen.findByRole("region", { name: "单条已读操作" });
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  await screen.findByRole("heading", { name: "已确认单条已读" });
  expect(requests("read")).toHaveLength(0);
});
it("switching users hides original IDs and late receipts cannot clear another identity's records", async () => {
  persistMessageReadIntent(profile, input);
  let done!: (r: Response) => void;
  lookup = () => new Promise((r) => (done = r));
  const view = await mount();
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  const other = {
    ...profile,
    ID: 2,
    username: "other",
    csrfToken: "other-csrf",
  };
  view.rerender(<MessagesWorkspace profile={other} sessionChanged={changed} />);
  await screen.findByRole("button", { name: "确认查询" });
  await act(async () => done(new Response(JSON.stringify(receipt()))));
  expect(screen.queryByText(input.operationId)).not.toBeInTheDocument();
  expect(loadMessageReadIntents(profile)).toEqual([input]);
  expect(loadMessageReadIntents(other)).toEqual([]);
});
it("new session resets a prior replayable state to receipt-first unknown", async () => {
  persistMessageReadIntent(profile, input);
  lookup = () => json({ error: "not_found" }, 404);
  const view = await mount();
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  await screen.findByRole("button", { name: "重放原操作" });
  view.rerender(
    <MessagesWorkspace
      profile={{ ...profile, csrfToken: "new-session" }}
      sessionChanged={changed}
    />,
  );
  await screen.findByRole("button", { name: "确认查询" });
  expect(
    screen.queryByRole("button", { name: "重放原操作" }),
  ).not.toBeInTheDocument();
  expect(requests("read")).toHaveLength(0);
});
it("storage failure prevents the initial write and can recover without clearing saved intents", async () => {
  await mount();
  await confirm();
  const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("quota");
  });
  begin();
  await screen.findByText(/未发出已读请求/);
  expect(requests("read")).toHaveLength(0);
  spy.mockRestore();
  fireEvent.click(screen.getByRole("button", { name: "重新读取原操作记录" }));
  expect(screen.getByRole("button", { name: "标记为已读" })).toBeEnabled();
});
it("a saved unknown blocks a replacement operation, including another-tab changes", async () => {
  await mount();
  await confirm();
  persistMessageReadIntent(profile, input);
  begin();
  await screen.findByText(input.operationId);
  expect(requests("read")).toHaveLength(0);
});
it("concurrent per-operation records are both retained and separately reconciled", async () => {
  persistMessageReadIntent(profile, input);
  const second = { ...input, operationId: "other-tab", messageId: "health-2" };
  persistMessageReadIntent(profile, second);
  await mount();
  expect(
    panel().getAllByRole("button", { name: "查询原操作结果" }),
  ).toHaveLength(2);
  lookup = () => json(receipt(input));
  const buttons = panel().getAllByRole("button", { name: "查询原操作结果" });
  fireEvent.click(buttons[0]);
  await screen.findByRole("heading", { name: "已确认单条已读" });
  expect(loadMessageReadIntents(profile)).toEqual([second]);
});
it.each([3, 4])(
  "missing operation grant %i prevents marking/recovery while query is still usable",
  async (i) => {
    grants[i] = false;
    persistMessageReadIntent(profile, input);
    await mount();
    await confirm();
    expect(screen.getByRole("button", { name: "标记为已读" })).toBeDisabled();
    expect(screen.queryByText(input.operationId)).not.toBeInTheDocument();
  },
);
it("malformed 404 or mismatched successful receipt never unlocks replay", async () => {
  persistMessageReadIntent(profile, input);
  lookup = () => json({ error: "not_found_but_wrong" }, 404);
  await mount();
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  await waitFor(() =>
    expect(
      panel().getByRole("button", { name: "查询原操作结果" }),
    ).toBeEnabled(),
  );
  expect(
    screen.queryByRole("button", { name: "重放原操作" }),
  ).not.toBeInTheDocument();
  lookup = () => json(receipt({ ...input, messageId: "wrong" }));
  fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
  await screen.findByText(/原操作结果尚未确认/);
  expect(
    screen.queryByRole("heading", { name: "已确认单条已读" }),
  ).not.toBeInTheDocument();
});
it("persisted input is exact, actor-bound, survives closure, and contains no credentials or receipt", () => {
  persistMessageReadIntent(profile, input);
  const raw = localStorage.getItem(Object.keys(localStorage)[0])!;
  expect(JSON.parse(raw)).toEqual({
    version: 1,
    owner: profile.ID,
    username: profile.username,
    input,
  });
  expect(raw).not.toContain(profile.csrfToken);
  expect(loadMessageReadIntents({ ...profile, ID: 2 })).toEqual([]);
  expect(loadMessageReadIntents({ ...profile, username: "other" })).toEqual([]);
  expect(
    completeMessageReadIntent(profile, { ...input, messageId: "wrong" }),
  ).toBe(false);
  expect(loadMessageReadIntents(profile)).toEqual([input]);
});
it("unreadable persisted intent fails closed rather than silently discarding unknown result", () => {
  persistMessageReadIntent(profile, input);
  const key = Object.keys(localStorage)[0];
  localStorage.setItem(key, "broken");
  expect(() => loadMessageReadIntents(profile)).toThrow(ApiError);
  expect(localStorage.getItem(key)).toBe("broken");
});
it.each(["resultId", "messageId", "operationId"] as const)(
  "API receipt rejects wrong %s binding",
  async (key) => {
    lookup = () => json(receipt({ ...input, [key]: "wrong" }));
    await expect(
      messageAPI.readOperation(input, new AbortController().signal),
    ).rejects.toMatchObject({ code: "invalid_response" });
  },
);
it.each([
  { target: 2 },
  { processed: 0 },
  { newlyRead: 2 },
  { newlyRead: 0, alreadyRead: 0 },
  { alreadyRead: 1 },
  { failed: 1 },
  { remaining: 1 },
  { extra: 0 },
])("API rejects impossible receipt progress %j", async (patch) => {
  lookup = () =>
    json({ ...receipt(), progress: { ...receipt().progress, ...patch } });
  await expect(
    messageAPI.readOperation(input, new AbortController().signal),
  ).rejects.toMatchObject({ code: "invalid_response" });
});
it("receipt reads use only operationId and no-store, writes use exact original three IDs and current CSRF", async () => {
  await messageAPI.readOperation(input, new AbortController().signal);
  expect(requests("read-operation")[0]).toEqual([
    "/api/messages/read-operation?operationId=original-operation",
    expect.objectContaining({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
    }),
  ]);
  await messageAPI.markRead(input, "new-csrf", new AbortController().signal);
  expect(requests("read")[0][1]).toMatchObject({
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": "new-csrf" },
    body: JSON.stringify(input),
  });
});

it.each([
  new ApiError("forbidden", 403),
  new ApiError("unauthenticated", 401),
  new ApiError("password_change_required", 428),
])(
  "late receipt after concurrent query %s preserves original intent synchronously",
  async (denied) => {
    let rejectQuery!: (e: unknown) => void;
    let finishRead!: (v: MessageReadReceipt) => void;
    await mount();
    await confirm();
    vi.spyOn(messageAPI, "markRead").mockImplementation(
      () =>
        new Promise((r) => {
          finishRead = r;
        }),
    );
    vi.spyOn(messageAPI, "query").mockImplementation(
      () =>
        new Promise((_, reject) => {
          rejectQuery = reject;
        }),
    );
    begin();
    const saved = loadMessageReadIntents(profile);
    fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
    await act(async () => {
      rejectQuery(denied);
      await Promise.resolve();
      finishRead(receipt(saved[0]));
      await Promise.resolve();
    });
    expect(loadMessageReadIntents(profile)).toEqual(saved);
    expect(
      screen.queryByRole("region", { name: "单条已读操作" }),
    ).not.toBeInTheDocument();
  },
);
it.each([
  new ApiError("forbidden", 403),
  new ApiError("unauthenticated", 401),
  new ApiError("password_change_required", 428),
])(
  "concurrent query %s suppresses replay after a late not_found receipt",
  async (denied) => {
    persistMessageReadIntent(profile, input);
    lookup = () => json({ error: "not_found" }, 404);
    await mount();
    fireEvent.click(panel().getByRole("button", { name: "查询原操作结果" }));
    const replay = await screen.findByRole("button", { name: "重放原操作" });
    let rejectQuery!: (e: unknown) => void,
      rejectReceipt!: (e: unknown) => void;
    vi.spyOn(messageAPI, "readOperation").mockImplementation(
      () =>
        new Promise((_, reject) => {
          rejectReceipt = reject;
        }),
    );
    vi.spyOn(messageAPI, "query").mockImplementation(
      () =>
        new Promise((_, reject) => {
          rejectQuery = reject;
        }),
    );
    fireEvent.click(replay);
    fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
    await act(async () => {
      rejectQuery(denied);
      await Promise.resolve();
      rejectReceipt(new ApiError("not_found", 404));
      await Promise.resolve();
    });
    expect(requests("read")).toHaveLength(0);
    expect(loadMessageReadIntents(profile)).toEqual([input]);
  },
);
