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
import { useMessageBulk } from "./message-bulk";
import MessagesWorkspace from "./MessagesWorkspace";
import { type Profile } from "./api";
import { sampleSnapshot } from "./message-test-fixtures";
import {
  bulkAPI,
  validBulkInput,
  validBulkOperation,
  checkBulkProgress,
  type BulkInput,
  type BulkOperation,
} from "./message-bulk-api";
import {
  loadBulkIntents,
  persistBulkIntent,
  removeBulkIntent,
} from "./message-bulk-intent";
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
const input: BulkInput = {
  resultId: "result-1",
  operationId: "original-bulk",
  selection: "all",
};
const result = (
  i: BulkInput = input,
  target = 1,
  state: BulkOperation["state"] = "queued",
  processed = 0,
): BulkOperation => ({
  receipt: {
    operationId: i.operationId,
    resultId: i.resultId,
    selection: i.selection,
    acceptedAt: "2026-10-11T00:01:00Z",
    target,
  },
  state,
  updatedAt: "2026-10-11T00:01:00Z",
  progress: {
    target,
    processed,
    newlyRead: processed,
    alreadyRead: 0,
    failed: 0,
    remaining: target - processed,
  },
});
const json = (v: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(v), { status }));
const calls = (path: string) =>
  vi
    .mocked(fetch)
    .mock.calls.filter(
      ([u]) => String(u).split("?")[0] === `/api/messages/${path}`,
    );
const panel = () => screen.getByRole("region", { name: "批量已读操作" });
let post: (i: BulkInput) => Promise<Response>,
  lookup: () => Promise<Response>,
  cancel: () => Promise<Response>,
  query: () => Promise<Response>,
  page: (url: string) => Promise<Response>;
let grants: boolean[], changed: ReturnType<typeof vi.fn<() => void>>;
beforeEach(() => {
  localStorage.clear();
  changed = vi.fn();
  grants = Array(8).fill(true);
  post = (i) => json(result(i));
  lookup = () => json(result());
  cancel = () => json(result(input, 1, "cancelled"));
  query = () => json(sampleSnapshot());
  page = () => json(sampleSnapshot());
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init: RequestInit) => {
      if (url === "/api/access/menu")
        return json({
          menu: [{ mark: "tasks", auth: { readable: true, writeable: false } }],
        });
      if (url === "/api/access/check") return json({ results: grants });
      if (url === "/api/messages/query") return query();
      if (url.startsWith("/api/messages/page?")) return page(url);
      if (url === "/api/messages/bulk-read")
        return post(JSON.parse(String(init.body)));
      if (url.startsWith("/api/messages/bulk-read-operation?")) return lookup();
      if (url === "/api/messages/bulk-read-cancel") return cancel();
      throw new Error(url);
    }),
  );
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
async function mount(p = profile, strict = false) {
  const ui = <MessagesWorkspace profile={p} sessionChanged={changed} />;
  let view!: ReturnType<typeof render>;
  await act(async () => {
    view = render(strict ? <StrictMode>{ui}</StrictMode> : ui);
  });
  await screen.findByRole("button", { name: "确认查询" });
  return view;
}
async function confirm() {
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await screen.findByRole("list", { name: "站内消息列表" });
}
async function previewAll() {
  await confirm();
  fireEvent.click(screen.getByRole("button", { name: "选择固定结果全部" }));
  fireEvent.click(screen.getByRole("button", { name: "预览批量目标" }));
}
function saved() {
  return loadBulkIntents(profile);
}
it("requires explicit preview, persists original request before start, blocks repeated starts and preserves snapshot counts", async () => {
  let resolve!: (r: Response) => void;
  post = (i) => {
    expect(saved()).toEqual([{ input: i, target: 1 }]);
    return new Promise((r) => {
      resolve = r;
    });
  };
  await mount(profile, true);
  await previewAll();
  expect(calls("bulk-read")).toHaveLength(0);
  expect(
    screen.getByRole("region", { name: "批量目标预览" }),
  ).toHaveTextContent("确认冻结目标：1 条");
  fireEvent.click(screen.getByRole("button", { name: "确认启动批量已读" }));
  await waitFor(() => expect(calls("bulk-read")).toHaveLength(1));
  expect(
    screen.queryByRole("button", { name: "确认启动批量已读" }),
  ).not.toBeInTheDocument();
  const i = saved()[0].input;
  await act(async () => resolve(new Response(JSON.stringify(result(i)))));
  expect(panel()).toHaveTextContent("已接受，等待处理");
  expect(
    screen.getByRole("region", { name: "查询时消息统计" }),
  ).toHaveTextContent("查询时未读1");
  expect(screen.getByRole("button", { name: "标记为已读" })).toBeEnabled();
});
it("retains same-result selection across pages and draft edits, then freezes exact selected IDs", async () => {
  const full = {
    ...sampleSnapshot(),
    total: 21,
    counts: { all: 21, unread: 21, read: 0 },
    items: Array.from({ length: 20 }, (_, n) => ({
      ...sampleSnapshot().items[0],
      messageId: `health-${String(n + 1).padStart(2, "0")}`,
    })),
  };
  query = () => json(full);
  page = (u) =>
    json({
      ...full,
      offset: Number(new URL(u, "http://test").searchParams.get("offset")),
      items: [{ ...full.items[0], messageId: "health-21" }],
    });
  post = (i) => json(result(i, 2));
  await mount();
  await confirm();
  fireEvent.click(screen.getByRole("checkbox", { name: "选择消息 health-01" }));
  fireEvent.change(screen.getByLabelText("关键词"), {
    target: { value: "draft only" },
  });
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("checkbox", { name: "选择消息 health-21" });
  fireEvent.click(screen.getByRole("checkbox", { name: "选择消息 health-21" }));
  fireEvent.click(screen.getByRole("button", { name: "预览批量目标" }));
  expect(
    screen.getByRole("region", { name: "批量目标预览" }),
  ).toHaveTextContent("确认冻结目标：2 条");
  fireEvent.click(screen.getByRole("button", { name: "确认启动批量已读" }));
  await waitFor(() => expect(panel()).toHaveTextContent("已接受，等待处理"));
  expect(saved()[0].input).toMatchObject({
    resultId: "result-1",
    selection: "selected",
    messageIds: ["health-01", "health-21"],
  });
  expect(calls("query")).toHaveLength(1);
});
it("clears preview and selection when a new result replaces the old result", async () => {
  await mount();
  await previewAll();
  query = () => json({ ...sampleSnapshot(), resultId: "result-2" });
  await confirm();
  expect(
    screen.queryByRole("region", { name: "批量目标预览" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "预览批量目标" })).toBeDisabled();
});
it("unknown start first looks up original ID and replays only after another 404", async () => {
  post = () => Promise.reject(new TypeError("lost"));
  lookup = () => json({ error: "not_found" }, 404);
  await mount();
  await previewAll();
  fireEvent.click(screen.getByRole("button", { name: "确认启动批量已读" }));
  await waitFor(() => expect(panel()).toHaveTextContent("原批次结果尚未确认"));
  const i = saved()[0].input;
  expect(
    screen.queryByRole("button", { name: "重放原批量操作" }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    within(panel()).getByRole("button", { name: "查询原批次进度" }),
  );
  await screen.findByRole("button", { name: "重放原批量操作" });
  post = (v) => json(result(v));
  fireEvent.click(screen.getByRole("button", { name: "重放原批量操作" }));
  await waitFor(() => expect(panel()).toHaveTextContent("已接受，等待处理"));
  expect(calls("bulk-read")).toHaveLength(2);
  expect(calls("bulk-read-operation")).toHaveLength(2);
  for (const [, init] of calls("bulk-read"))
    expect(JSON.parse(String(init?.body))).toEqual(i);
});
it("a receipt found immediately before replay prevents a second write", async () => {
  persistBulkIntent(profile, { input, target: 1 });
  lookup = () => json({ error: "not_found" }, 404);
  await mount();
  fireEvent.click(
    within(panel()).getByRole("button", { name: "查询原批次进度" }),
  );
  await screen.findByRole("button", { name: "重放原批量操作" });
  lookup = () => json(result(input, 1, "completed", 1));
  fireEvent.click(screen.getByRole("button", { name: "重放原批量操作" }));
  await waitFor(() => expect(panel()).toHaveTextContent("批量已读完成"));
  expect(calls("bulk-read")).toHaveLength(0);
});
it("cancel only uses original operation, checks it first, and preserves committed counts", async () => {
  persistBulkIntent(profile, { input, target: 51 });
  lookup = () => json(result(input, 51, "running", 50));
  cancel = () => json(result(input, 51, "cancelled", 50));
  await mount();
  await screen.findByRole("button", { name: "取消剩余项目" });
  fireEvent.click(screen.getByRole("button", { name: "取消剩余项目" }));
  await waitFor(() => expect(panel()).toHaveTextContent("已取消剩余项目"));
  expect(calls("bulk-read-operation")).toHaveLength(2);
  expect(calls("bulk-read-cancel")).toHaveLength(1);
  expect(JSON.parse(String(calls("bulk-read-cancel")[0][1]?.body))).toEqual({
    operationId: input.operationId,
  });
  expect(panel()).toHaveTextContent("剩余项不会由本操作继续执行");
  expect(panel()).toHaveTextContent("已处理 Processed50");
  expect(saved()).toEqual([{ input, target: 51 }]);
  fireEvent.click(screen.getByRole("button", { name: "收起已结束批次" }));
  expect(saved()).toEqual([]);
});
it("unknown cancel restores by GET and never automatically repeats cancellation", async () => {
  persistBulkIntent(profile, { input, target: 51 });
  lookup = () => json(result(input, 51, "running", 50));
  cancel = () => Promise.reject(new TypeError("lost"));
  await mount();
  fireEvent.click(
    within(panel()).getByRole("button", { name: "查询原批次进度" }),
  );
  await screen.findByRole("button", { name: "取消剩余项目" });
  fireEvent.click(screen.getByRole("button", { name: "取消剩余项目" }));
  await waitFor(() => expect(panel()).toHaveTextContent("取消响应尚未确认"));
  lookup = () => json(result(input, 51, "cancelled", 50));
  fireEvent.click(
    within(panel()).getByRole("button", { name: "查询原批次进度" }),
  );
  await waitFor(() => expect(panel()).toHaveTextContent("已取消剩余项目"));
  expect(calls("bulk-read-cancel")).toHaveLength(1);
  expect(calls("bulk-read")).toHaveLength(0);
});
it("completion before cancellation returns completed without issuing cancel", async () => {
  persistBulkIntent(profile, { input, target: 1 });
  await mount();
  fireEvent.click(
    within(panel()).getByRole("button", { name: "查询原批次进度" }),
  );
  await screen.findByRole("button", { name: "取消剩余项目" });
  lookup = () => json(result(input, 1, "completed", 1));
  fireEvent.click(screen.getByRole("button", { name: "取消剩余项目" }));
  await waitFor(() => expect(panel()).toHaveTextContent("批量已读完成"));
  expect(calls("bulk-read-cancel")).toHaveLength(0);
});
it("restores same-user new session without old query, and hides other user's intents", async () => {
  persistBulkIntent(profile, { input, target: 1 });
  const view = await mount();
  view.rerender(
    <MessagesWorkspace
      profile={{ ...profile, csrfToken: "fresh" }}
      sessionChanged={changed}
    />,
  );
  await screen.findByRole("button", { name: "查询原批次进度" });
  fireEvent.click(screen.getByRole("button", { name: "查询原批次进度" }));
  await waitFor(() => expect(panel()).toHaveTextContent("已接受，等待处理"));
  expect(calls("query")).toHaveLength(0);
  expect(calls("bulk-read")).toHaveLength(0);
  view.rerender(
    <MessagesWorkspace
      profile={{ ...profile, ID: 2, username: "other", csrfToken: "other" }}
      sessionChanged={changed}
    />,
  );
  await screen.findByRole("button", { name: "确认查询" });
  expect(
    screen.queryByRole("region", { name: "批量已读操作" }),
  ).not.toBeInTheDocument();
});
it("late old-session progress cannot repopulate new actor UI", async () => {
  persistBulkIntent(profile, { input, target: 1 });
  let resolve!: (r: Response) => void;
  lookup = () =>
    new Promise((r) => {
      resolve = r;
    });
  const view = await mount();
  fireEvent.click(screen.getByRole("button", { name: "查询原批次进度" }));
  await waitFor(() => expect(calls("bulk-read-operation")).toHaveLength(1));
  view.rerender(
    <MessagesWorkspace
      profile={{ ...profile, ID: 2, username: "other" }}
      sessionChanged={changed}
    />,
  );
  await screen.findByRole("button", { name: "确认查询" });
  await act(async () => resolve(new Response(JSON.stringify(result()))));
  expect(screen.queryByText(input.operationId)).not.toBeInTheDocument();
});
it("permission denied suspends private progress and no old intent executes", async () => {
  persistBulkIntent(profile, { input, target: 1 });
  lookup = () => json({ error: "forbidden" }, 403);
  render(<MessagesWorkspace profile={profile} sessionChanged={changed} />);
  await screen.findByRole("button", { name: "重新核验权限" });
  expect(screen.queryByText(input.operationId)).not.toBeInTheDocument();
  expect(saved()).toHaveLength(1);
});
it("authorization epoch terminal keeps remaining stopped after permission returns", async () => {
  persistBulkIntent(profile, { input, target: 51 });
  lookup = () => json(result(input, 51, "authorization_blocked", 50));
  const view = await mount();
  fireEvent.click(screen.getByRole("button", { name: "查询原批次进度" }));
  await waitFor(() =>
    expect(panel()).toHaveTextContent("重新获得权限也不会恢复这个批次"),
  );
  expect(
    screen.queryByRole("button", { name: "取消剩余项目" }),
  ).not.toBeInTheDocument();
  view.unmount();
  await mount({ ...profile, csrfToken: "new-session" });
  fireEvent.click(screen.getByRole("button", { name: "查询原批次进度" }));
  await waitFor(() =>
    expect(panel()).toHaveTextContent("权限版本变化，剩余项目已停止"),
  );
  expect(calls("bulk-read")).toHaveLength(0);
});
it("failed local persistence prevents start", async () => {
  await mount();
  await previewAll();
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error();
  });
  fireEvent.click(screen.getByRole("button", { name: "确认启动批量已读" }));
  expect(calls("bulk-read")).toHaveLength(0);
  expect(screen.getByRole("alert")).toHaveTextContent("未发送新请求");
});
it("another tab's unresolved intent is discovered before assigning a new ID", async () => {
  await mount();
  await previewAll();
  persistBulkIntent(profile, { input, target: 1 });
  fireEvent.click(screen.getByRole("button", { name: "确认启动批量已读" }));
  expect(saved()).toHaveLength(1);
  expect(calls("bulk-read")).toHaveLength(0);
  expect(panel()).toHaveTextContent(input.operationId);
});
it("polling restores progress read-only", async () => {
  persistBulkIntent(profile, { input, target: 1 });
  lookup = () => json(result(input, 1, "completed", 1));
  await mount();
  await waitFor(() => expect(panel()).toHaveTextContent("批量已读完成"), {
    timeout: 3500,
  });
  expect(calls("bulk-read-operation").length).toBeGreaterThan(0);
  expect(calls("bulk-read")).toHaveLength(0);
  expect(calls("bulk-read-cancel")).toHaveLength(0);
});
for (const malformed of [
  { ...input, messageIds: [] },
  { ...input, selection: "selected", messageIds: [] },
  { ...input, selection: "selected", messageIds: ["same", "same"] },
  { ...input, selection: "selected", messageIds: ["bad space"] },
  { ...input, tenant: "other" },
])
  it(`rejects malformed bulk input ${JSON.stringify(malformed)}`, () =>
    expect(validBulkInput(malformed)).toBe(false));
for (const patch of [
  { progress: { ...result().progress, remaining: 0 } },
  { progress: { ...result().progress, failed: 1 } },
  { progress: { ...result().progress, processed: 0.5 } },
  { state: "completed" },
  { state: "running" },
  { state: "invented" },
  { state: ["queued"] },
  { state: ["completed"] },
  { state: ["cancelled"] },
  { receipt: { ...result().receipt, operationId: "other" } },
  { receipt: { ...result().receipt, target: 2 } },
  { updatedAt: "2026-10-10T00:00:00Z" },
  { extra: true },
])
  it(`rejects malformed progress ${JSON.stringify(patch)}`, () =>
    expect(validBulkOperation({ ...result(), ...patch }, input, 1)).toBe(
      false,
    ));
it("rejects regressed progress, changed receipt, and resurrected terminal", () => {
  expect(() =>
    checkBulkProgress(result(input, 51), result(input, 51, "running", 50)),
  ).toThrow();
  expect(() =>
    checkBulkProgress(
      result(input, 51, "running", 50),
      result(input, 51, "cancelled", 50),
    ),
  ).toThrow();
  expect(() =>
    checkBulkProgress(
      {
        ...result(),
        receipt: { ...result().receipt, acceptedAt: "2026-10-11T00:00:00Z" },
      },
      result(),
    ),
  ).toThrow();
});
it("API omits messageIds for all and checks response conservation", async () => {
  const ctrl = new AbortController();
  await bulkAPI.run(input, 1, "start", "csrf", ctrl.signal);
  expect(JSON.parse(String(calls("bulk-read")[0][1]?.body))).toEqual(input);
  expect(calls("bulk-read")[0][1]?.headers).toEqual({
    "Content-Type": "application/json",
    "X-CSRF-Token": "csrf",
  });
  post = () =>
    json({ ...result(), progress: { ...result().progress, remaining: 5 } });
  await expect(
    bulkAPI.run(input, 1, "start", "csrf", ctrl.signal),
  ).rejects.toThrow();
});
it("storage keeps namespaces distinct, rejects corruption and preserves another actor", () => {
  persistBulkIntent(profile, { input, target: 1 });
  localStorage.setItem(
    "adtr.message-read.v1.1.synthetic.single",
    "single remains",
  );
  expect(loadBulkIntents({ ...profile, ID: 2 })).toEqual([]);
  expect(removeBulkIntent(profile, { input, target: 1 })).toBe(true);
  expect(localStorage.getItem("adtr.message-read.v1.1.synthetic.single")).toBe(
    "single remains",
  );
  localStorage.setItem("adtr.message-bulk.v1.1.synthetic.broken", "{broken");
  expect(() => saved()).toThrow();
});
it("round-robin recovery queries later batches despite a failing first batch", async () => {
  const second = { ...input, operationId: "second-bulk" };
  persistBulkIntent(profile, { input, target: 1 });
  persistBulkIntent(profile, { input: second, target: 1 });
  vi.mocked(fetch).mockImplementation((url, init) => {
    const u = String(url);
    if (u === "/api/access/menu")
      return json({ menu: [{ mark: "tasks", auth: { readable: true } }] });
    if (u === "/api/access/check") return json({ results: grants });
    if (u.includes("bulk-read-operation"))
      return u.includes("second-bulk")
        ? json(result(second, 1, "completed", 1))
        : json({ error: "internal" }, 503);
    throw new Error(u + init?.method);
  });
  await mount();
  await waitFor(() => expect(panel()).toHaveTextContent("批量已读完成"), {
    timeout: 4800,
  });
  expect(calls("bulk-read-operation")).toHaveLength(2);
  expect(calls("bulk-read")).toHaveLength(0);
}, 8000);
it("a hanging response times out without losing the ID and rejects late progress", async () => {
  persistBulkIntent(profile, { input, target: 1 });
  let resolve!: (r: Response) => void;
  lookup = () =>
    new Promise((r) => {
      resolve = r;
    });
  vi.useFakeTimers();
  await act(async () => {
    render(<RecoveryHarness />);
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15001);
  });
  expect(panel()).toHaveTextContent("等待响应已超时");
  expect(screen.getByRole("button", { name: "查询原批次进度" })).toBeEnabled();
  await act(async () =>
    resolve(new Response(JSON.stringify(result(input, 1, "completed", 1)))),
  );
  expect(panel()).not.toHaveTextContent("批量已读完成");
  expect(saved()).toEqual([{ input, target: 1 }]);
  expect(calls("bulk-read")).toHaveLength(0);
});

function RecoveryHarness({
  p = profile,
  allowed = true,
}: {
  p?: Profile;
  allowed?: boolean;
}) {
  return useMessageBulk(p, allowed, undefined, changed).panel;
}

for (const epochID of ["a-bulk", "b-bulk", "c-bulk"]) {
  it(`recovers three terminal intents within two poll intervals with epoch ${epochID}`, async () => {
    vi.useFakeTimers();
    const inputs = ["c-bulk", "a-bulk", "b-bulk"].map((operationId) => ({
      ...input,
      operationId,
    }));
    for (const i of inputs) persistBulkIntent(profile, { input: i, target: 1 });
    const readIDs: string[] = [];
    vi.mocked(fetch).mockImplementation((url) => {
      const id = new URL(String(url), "http://localhost").searchParams.get(
        "operationId",
      )!;
      readIDs.push(id);
      const i = inputs.find((v) => v.operationId === id)!;
      return json(
        result(
          i,
          1,
          id === epochID ? "authorization_blocked" : "completed",
          id === epochID ? 0 : 1,
        ),
      );
    });
    await act(async () => {
      render(<RecoveryHarness />);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000);
    });
    expect(readIDs).toEqual(["a-bulk", "b-bulk", "c-bulk"]);
    expect(panel()).toHaveTextContent("权限版本变化，剩余项目已停止");
    expect(panel()).not.toHaveTextContent("批量操作结果待确认");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(readIDs).toHaveLength(3);
    expect(calls("bulk-read")).toHaveLength(0);
    expect(calls("bulk-read-cancel")).toHaveLength(0);
  });
}

it("keeps the ID cursor when a terminal is dismissed and another tab adds an earlier ID", async () => {
  vi.useFakeTimers();
  const intents = ["a-bulk", "b-bulk", "c-bulk", "aa-bulk"].map(
    (operationId) => ({ ...input, operationId }),
  );
  for (const i of intents.slice(0, 3))
    persistBulkIntent(profile, { input: i, target: 1 });
  const readIDs: string[] = [];
  vi.mocked(fetch).mockImplementation((url) => {
    const id = new URL(String(url), "http://localhost").searchParams.get(
      "operationId",
    )!;
    readIDs.push(id);
    return json(
      result(intents.find((i) => i.operationId === id)!, 1, "completed", 1),
    );
  });
  await act(async () => {
    render(<RecoveryHarness />);
  });
  expect(readIDs).toEqual(["a-bulk"]);
  fireEvent.click(screen.getByRole("button", { name: "收起已结束批次" }));
  persistBulkIntent(profile, { input: intents[3], target: 1 });
  fireEvent(window, new Event("storage"));
  fireEvent.focus(window);
  expect(readIDs).toEqual(["a-bulk"]);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(6000);
  });
  expect(readIDs).toEqual(["a-bulk", "aa-bulk", "b-bulk", "c-bulk"]);
  expect(panel()).not.toHaveTextContent("批量操作结果待确认");
  expect(calls("bulk-read")).toHaveLength(0);
});

it("skips in-flight ticks and rotates past a failed read without flooding retries", async () => {
  vi.useFakeTimers();
  const intents = ["a-bulk", "b-bulk", "c-bulk"].map((operationId) => ({
    ...input,
    operationId,
  }));
  for (const i of intents) persistBulkIntent(profile, { input: i, target: 1 });
  const readIDs: string[] = [];
  let resolve!: (r: Response) => void;
  vi.mocked(fetch).mockImplementation((url) => {
    const id = new URL(String(url), "http://localhost").searchParams.get(
      "operationId",
    )!;
    readIDs.push(id);
    return id === "a-bulk"
      ? new Promise((r) => {
          resolve = r;
        })
      : json(
          result(intents.find((i) => i.operationId === id)!, 1, "completed", 1),
        );
  });
  const view = render(<RecoveryHarness />);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(4000);
  });
  expect(readIDs).toEqual(["a-bulk"]);
  await act(async () => {
    resolve(
      new Response(JSON.stringify({ error: "internal" }), { status: 503 }),
    );
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5999);
  });
  expect(readIDs).toEqual(["a-bulk", "b-bulk", "c-bulk"]);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1);
  });
  expect(readIDs).toEqual(["a-bulk", "b-bulk", "c-bulk", "a-bulk"]);
  const signal = calls("bulk-read-operation").at(-1)![1]!.signal!;
  view.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => {
    resolve(
      new Response(JSON.stringify(result(intents[0], 1, "completed", 1))),
    );
    await vi.advanceTimersByTimeAsync(20000);
  });
  expect(readIDs).toHaveLength(4);
  expect(calls("bulk-read")).toHaveLength(0);
  expect(calls("bulk-read-cancel")).toHaveLength(0);
});

it("aborts immediate recovery on permission loss and rejects its late response after reauthorization", async () => {
  vi.useFakeTimers();
  persistBulkIntent(profile, { input, target: 1 });
  let resolve!: (r: Response) => void;
  lookup = () =>
    new Promise((r) => {
      resolve = r;
    });
  const view = render(<RecoveryHarness />);
  const signal = calls("bulk-read-operation")[0][1]!.signal!;
  view.rerender(<RecoveryHarness allowed={false} />);
  expect(signal.aborted).toBe(true);
  expect(
    screen.queryByRole("region", { name: "批量已读操作" }),
  ).not.toBeInTheDocument();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20000);
  });
  expect(calls("bulk-read-operation")).toHaveLength(1);
  lookup = () => json(result(input, 1, "authorization_blocked"));
  await act(async () => {
    view.rerender(<RecoveryHarness />);
  });
  await act(async () => {
    resolve(new Response(JSON.stringify(result(input, 1, "completed", 1))));
  });
  expect(panel()).toHaveTextContent("权限版本变化，剩余项目已停止");
  expect(panel()).not.toHaveTextContent("批量已读完成");
  expect(calls("bulk-read-operation")).toHaveLength(2);
});

it("StrictMode discards the first immediate read and only accepts the live recovery", async () => {
  vi.useFakeTimers();
  persistBulkIntent(profile, { input, target: 1 });
  const pending: ((r: Response) => void)[] = [];
  lookup = () =>
    new Promise((r) => {
      pending.push(r);
    });
  render(
    <StrictMode>
      <RecoveryHarness />
    </StrictMode>,
  );
  expect(calls("bulk-read-operation")).toHaveLength(2);
  expect(calls("bulk-read-operation")[0][1]!.signal!.aborted).toBe(true);
  await act(async () => {
    pending[1](
      new Response(JSON.stringify(result(input, 1, "authorization_blocked"))),
    );
  });
  await act(async () => {
    pending[0](new Response(JSON.stringify(result(input, 1, "completed", 1))));
    await vi.advanceTimersByTimeAsync(6000);
  });
  expect(panel()).toHaveTextContent("权限版本变化，剩余项目已停止");
  expect(panel()).not.toHaveTextContent("批量已读完成");
  expect(calls("bulk-read-operation")).toHaveLength(2);
  expect(calls("bulk-read")).toHaveLength(0);
});

// AD-F-158 current-page selection: transport fixtures, not real producer evidence.
const pageSelection = () => screen.getByRole("region", { name: "批量目标选择" });
const pageButton = (name: string) => screen.getByRole("button", { name });
const selectCurrentPage = () => fireEvent.click(pageButton("选择本页"));
const deselectCurrentPage = () => fireEvent.click(pageButton("取消选择本页"));
const pageSnapshot = (offset = 0) => ({
  ...sampleSnapshot(), total: 21, offset,
  counts: { all: 21, unread: 21, read: 0 },
  items: Array.from({ length: offset === 0 ? 20 : 1 }, (_, n) => ({
    ...sampleSnapshot().items[0],
    messageId: `health-${String(offset + n + 1).padStart(2, "0")}`,
  })),
});
function PageSelectionHarness({
  value = sampleSnapshot(), workspaceBusy = false, allowed = true,
}: {
  value?: ReturnType<typeof sampleSnapshot>;
  workspaceBusy?: boolean;
  allowed?: boolean;
}) {
  const bulk = useMessageBulk(profile, allowed, value, changed, workspaceBusy);
  return <>{bulk.controls}{bulk.panel}</>;
}
it("page union and difference preserve other pages, invalidate preview, and freeze only selected IDs", async () => {
  query = () => json(pageSnapshot());
  page = (u) => json(pageSnapshot(Number(new URL(u, "http://test").searchParams.get("offset"))));
  post = (i) => json(result(i, 1));
  await mount(profile, true);
  await confirm();
  expect(pageButton("取消选择本页")).toBeDisabled();
  fireEvent.click(screen.getByRole("checkbox", { name: "选择消息 health-01" }));
  fireEvent.click(pageButton("预览批量目标"));
  const before = vi.mocked(fetch).mock.calls.length;
  selectCurrentPage();
  expect(vi.mocked(fetch).mock.calls).toHaveLength(before);
  expect(screen.queryByRole("region", { name: "批量目标预览" })).not.toBeInTheDocument();
  expect(pageSelection()).toHaveTextContent("已选 20 / 21");
  expect(pageButton("选择本页")).toBeDisabled();
  selectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 20 / 21");
  expect(screen.getAllByRole("article")).toHaveLength(20);
  fireEvent.change(screen.getByLabelText("关键词"), { target: { value: "unapplied draft" } });
  fireEvent.click(pageButton("下一页"));
  await screen.findByRole("checkbox", { name: "选择消息 health-21" });
  selectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 21 / 21");
  fireEvent.click(pageButton("预览批量目标"));
  deselectCurrentPage();
  expect(screen.queryByRole("region", { name: "批量目标预览" })).not.toBeInTheDocument();
  expect(pageSelection()).toHaveTextContent("已选 20 / 21");
  selectCurrentPage();
  fireEvent.click(pageButton("上一页"));
  await screen.findByRole("checkbox", { name: "选择消息 health-01" });
  expect(screen.getByRole("checkbox", { name: "选择消息 health-01" })).toBeChecked();
  deselectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 1 / 21");
  expect(pageButton("取消选择本页")).toBeDisabled();
  fireEvent.click(pageButton("预览批量目标"));
  expect(screen.getByRole("region", { name: "批量目标预览" })).toHaveTextContent("health-21");
  fireEvent.click(pageButton("确认启动批量已读"));
  await waitFor(() => expect(panel()).toHaveTextContent("已接受，等待处理"));
  expect(saved()[0].input).toMatchObject({ selection: "selected", messageIds: ["health-21"] });
  expect(calls("query")).toHaveLength(1);
  expect(calls("bulk-read")).toHaveLength(1);
  expect(pageButton("选择本页")).toBeDisabled();
  expect(pageButton("取消选择本页")).toBeDisabled();
});
it("deduplicates current page IDs and disables both page buttons for an empty page", () => {
  const value = { ...sampleSnapshot(), total: 3, items: [sampleSnapshot().items[0], sampleSnapshot().items[0]] };
  const view = render(<PageSelectionHarness value={value} />);
  selectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 1 / 3");
  fireEvent.click(pageButton("预览批量目标"));
  expect(within(screen.getByRole("region", { name: "批量目标预览" })).getAllByRole("listitem", { hidden: true })).toHaveLength(1);
  view.rerender(<PageSelectionHarness value={{ ...value, items: [] }} />);
  expect(pageButton("选择本页")).toBeDisabled();
  expect(pageButton("取消选择本页")).toBeDisabled();
  deselectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 1 / 3");
  view.rerender(<PageSelectionHarness value={{ ...value, total: 0, items: [] }} />);
  expect(screen.queryByRole("button", { name: "选择本页" })).not.toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});
it("all-result mode blocks page exclusion and keeps the frozen all preview", async () => {
  await mount();
  await previewAll();
  expect(pageButton("选择本页")).toBeDisabled();
  expect(pageButton("取消选择本页")).toBeDisabled();
  selectCurrentPage();
  deselectCurrentPage();
  expect(screen.getByRole("region", { name: "批量目标预览" })).toHaveTextContent("全部成员（含全部页）");
  expect(calls("bulk-read")).toHaveLength(0);
});
it("workspace read lock blocks selecting the stale page until the accepted page arrives", async () => {
  query = () => json(pageSnapshot());
  let done!: (r: Response) => void;
  page = () => new Promise((resolve) => { done = resolve; });
  await mount();
  await confirm();
  selectCurrentPage();
  fireEvent.click(pageButton("下一页"));
  expect(pageButton("选择本页")).toBeDisabled();
  expect(pageButton("取消选择本页")).toBeDisabled();
  deselectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 20 / 21");
  await act(async () => done(new Response(JSON.stringify(pageSnapshot(20)))));
  selectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 21 / 21");
  expect(calls("bulk-read")).toHaveLength(0);
});
it("workspace busy flag and storage errors independently disable page changes", () => {
  const view = render(<PageSelectionHarness />);
  selectCurrentPage();
  view.rerender(<PageSelectionHarness workspaceBusy />);
  expect(pageButton("取消选择本页")).toBeDisabled();
  deselectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 1 / 1");
  view.rerender(<PageSelectionHarness />);
  expect(pageButton("取消选择本页")).toBeEnabled();
  localStorage.setItem("adtr.message-bulk.v1.1.synthetic.broken", "{broken");
  fireEvent(window, new Event("storage"));
  expect(pageButton("取消选择本页")).toBeDisabled();
  expect(panel()).toHaveTextContent("无法可靠读取");
});
it.each([401, 403, 404, 410])("page selection disappears on read status %i and does not create a new operation", async (status) => {
  query = () => json(pageSnapshot());
  page = () => json({ error: status === 403 ? "forbidden" : status === 410 ? "expired" : "not_found" }, status);
  await mount();
  await confirm();
  selectCurrentPage();
  fireEvent.click(pageButton("下一页"));
  await waitFor(() => expect(screen.queryByRole("button", { name: "选择本页" })).not.toBeInTheDocument());
  expect(calls("bulk-read")).toHaveLength(0);
  expect(saved()).toEqual([]);
});
it.each(["result", "session", "unmount"])("late cancelled page cannot restore selection after %s replacement", async (kind) => {
  query = () => json(pageSnapshot());
  let done!: (r: Response) => void;
  page = () => new Promise((resolve) => { done = resolve; });
  const view = await mount();
  await confirm();
  selectCurrentPage();
  fireEvent.click(pageButton("下一页"));
  if (kind === "result") {
    fireEvent.click(pageButton("停止等待"));
    query = () => json({ ...sampleSnapshot(), resultId: "new-result" });
    await confirm();
    expect(pageSelection()).toHaveTextContent("已选 0 / 1");
  } else if (kind === "session") {
    view.rerender(<MessagesWorkspace profile={{ ...profile, csrfToken: "next" }} sessionChanged={changed} />);
    await screen.findByRole("button", { name: "确认查询" });
  } else view.unmount();
  await act(async () => done(new Response(JSON.stringify(pageSnapshot(20)))));
  expect(screen.queryByRole("checkbox", { name: "选择消息 health-21" })).not.toBeInTheDocument();
  if (kind === "result") expect(pageSelection()).toHaveTextContent("已选 0 / 1");
  else expect(screen.queryByRole("button", { name: "选择本页" })).not.toBeInTheDocument();
  expect(calls("bulk-read")).toHaveLength(0);
});
it("revoking and restoring bulk authority never restores local page selection", () => {
  const view = render(<PageSelectionHarness />);
  selectCurrentPage();
  view.rerender(<PageSelectionHarness allowed={false} />);
  expect(screen.queryByRole("button", { name: "选择本页" })).not.toBeInTheDocument();
  view.rerender(<PageSelectionHarness />);
  expect(pageSelection()).toHaveTextContent("已选 0 / 1");
  expect(pageButton("取消选择本页")).toBeDisabled();
});
it("page unions retain the existing 32 KiB preflight and never persist or send an oversized batch", () => {
  const largePage = (offset: number) => ({
    ...sampleSnapshot(), total: 400, offset, limit: 200,
    counts: { all: 400, read: 0, unread: 400 },
    items: Array.from({ length: 200 }, (_, n) => ({
      ...sampleSnapshot().items[0], messageId: `id-${String(n + offset).padStart(4, "0")}-${"x".repeat(100)}`,
    })),
  });
  const view = render(<PageSelectionHarness value={largePage(0)} />);
  selectCurrentPage();
  view.rerender(<PageSelectionHarness value={largePage(200)} />);
  selectCurrentPage();
  expect(pageSelection()).toHaveTextContent("已选 400 / 400");
  fireEvent.click(pageButton("预览批量目标"));
  fireEvent.click(pageButton("确认启动批量已读"));
  expect(panel()).toHaveTextContent("32 KiB");
  expect(saved()).toEqual([]);
  expect(fetch).not.toHaveBeenCalled();
  expect(pageButton("取消选择本页")).toBeDisabled();
  fireEvent.click(pageButton("清空批量选择"));
  expect(pageSelection()).toHaveTextContent("已选 0 / 400");
  expect(pageButton("选择本页")).toBeEnabled();
});
it.each(["unknown", "missing"])("page actions cannot change the original target while a submitted operation is %s", async (phase) => {
  post = () => Promise.reject(new TypeError("lost"));
  lookup = () => json({ error: "not_found" }, 404);
  await mount();
  await confirm();
  selectCurrentPage();
  fireEvent.click(pageButton("预览批量目标"));
  fireEvent.click(pageButton("确认启动批量已读"));
  await waitFor(() => expect(panel()).toHaveTextContent("原批次结果尚未确认"));
  if (phase === "missing") {
    fireEvent.click(within(panel()).getByRole("button", { name: "查询原批次进度" }));
    await screen.findByRole("button", { name: "重放原批量操作" });
  }
  const original = saved();
  const before = vi.mocked(fetch).mock.calls.length;
  expect(pageButton("选择本页")).toBeDisabled();
  expect(pageButton("取消选择本页")).toBeDisabled();
  selectCurrentPage();
  deselectCurrentPage();
  expect(saved()).toEqual(original);
  expect(original[0].input).toMatchObject({ selection: "selected", messageIds: ["health-1"] });
  expect(vi.mocked(fetch).mock.calls).toHaveLength(before);
  expect(calls("bulk-read")).toHaveLength(1);
  expect(screen.queryByRole("region", { name: "批量目标预览" })).not.toBeInTheDocument();
});
