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
import type { Profile } from "./api";
import { sampleSnapshot } from "./message-test-fixtures";
import type { MessageSnapshot } from "./message-api";
const profile: Profile = {
  ID: 1,
  username: "synthetic",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 1,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "csrf",
};
const changed = vi.fn();
let mutation: (url: string, init: RequestInit) => Promise<Response>;
let query: (init: RequestInit) => Promise<Response>,
  page: (url: string) => Promise<Response>,
  locate: () => Promise<Response>;
const json = (data: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(data), { status }));
const postCount = () =>
  vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/messages/query")
    .length;
const full = (): MessageSnapshot => ({
  ...sampleSnapshot(),
  total: 21,
  counts: { all: 21, unread: 21, read: 0 },
  items: Array.from({ length: 20 }, (_, n) => ({
    ...sampleSnapshot().items[0],
    messageId: `health-${String(n + 1).padStart(2, "0")}`,
  })),
});
beforeEach(() => {
  changed.mockReset();
  localStorage.clear();
  mutation = (url) => { throw new Error(url); };
  query = () => json(sampleSnapshot());
  page = () => json(sampleSnapshot());
  locate = () => json({ resultId: "result-1", found: true, index: 0, page: 1 });
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init: RequestInit) => {
      if (url === "/api/access/menu")
        return json({
          menu: [{ mark: "tasks", auth: { readable: true, writeable: false } }],
        });
      if (url === "/api/access/check")
        return json({
          results: [true, true, true, true, true, true, true, true],
        });
      if (url === "/api/messages/query") return query(init);
      if (url.startsWith("/api/messages/page?")) return page(url);
      if (url.startsWith("/api/messages/locate?")) return locate();
      return mutation(url, init);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());
async function mount(strict = false) {
  const ui = <MessagesWorkspace profile={profile} sessionChanged={changed} />;
  const view = render(strict ? <StrictMode>{ui}</StrictMode> : ui);
  await screen.findByRole("button", { name: "确认查询" });
  return view;
}
async function confirm() {
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await screen.findByRole("list", { name: "站内消息列表" });
}
// Synthetic accepted-response DOM contracts, not real cross-day producer evidence.
const multipleDays = (): MessageSnapshot => ({
  ...sampleSnapshot(), total: 3, counts: { all: 3, unread: 3, read: 0 },
  items: ["2026-10-10T00:00:00.000000001Z", "2026-10-10T00:00:00Z", "2026-10-09T23:59:59.999999999Z"].map((occurredAt, index) => ({
    ...sampleSnapshot().items[0], messageId: `health-${index + 1}`, occurredAt,
  })),
});
const headings = () => screen.queryAllByRole("heading", { name: /^发生日期（UTC）/ }).map((node) => node.textContent);
const click = (name: string) => fireEvent.click(screen.getByRole("button", { name, exact: true }));

it("renders adjacent accepted UTC dates without changing IDs, order, absolute numbers or nanosecond fields", async () => {
  const value = multipleDays();
  query = () => json(value);
  await mount(true);
  expect(headings()).toEqual([]);
  expect(postCount()).toBe(0);
  await confirm();
  expect(screen.getByRole("region", { name: "当前页时间线" })).toHaveTextContent("不代表全天消息汇总");
  expect(headings()).toEqual(["发生日期（UTC） · 2026-10-10", "发生日期（UTC） · 2026-10-09"]);
  const articles = screen.getAllByRole("article");
  expect(articles.map((node) => node.dataset.messageId)).toEqual(value.items.map((row) => row.messageId));
  articles.forEach((node, i) => expect(node.querySelector(".message-row-number")).toHaveTextContent(`0${i + 1}`));
  expect(within(screen.getByRole("list", { name: "站内消息列表" })).getAllByRole("listitem")).toHaveLength(3);
  expect(screen.getByText("2026-10-09 23:59:59.999999999 UTC")).toBeVisible();
  expect(screen.getByText("2026-10-10 00:00:00.000000001 UTC")).toBeVisible();
  expect(within(articles[2]).getByText("2026-10-11 00:00:00 UTC")).toBeVisible();
  const before = vi.mocked(fetch).mock.calls.length;
  fireEvent.change(screen.getByLabelText("排序"), { target: { value: "asc" } });
  fireEvent.change(screen.getByLabelText("关键词"), { target: { value: "draft only" } });
  expect(headings()).toEqual(["发生日期（UTC） · 2026-10-10", "发生日期（UTC） · 2026-10-09"]);
  expect(screen.getAllByRole("article")).toEqual(articles);
  expect(vi.mocked(fetch).mock.calls).toHaveLength(before);
});

it("uses ascending accepted order rather than a fixed newest-date-first timeline", async () => {
  const value = multipleDays();
  value.items.reverse();
  query = () => json(value);
  await mount();
  fireEvent.change(screen.getByLabelText("排序"), { target: { value: "asc" } });
  await confirm();
  expect(headings()).toEqual(["发生日期（UTC） · 2026-10-09", "发生日期（UTC） · 2026-10-10"]);
  expect(screen.getAllByRole("article").map((node) => node.dataset.messageId)).toEqual(["health-3", "health-2", "health-1"]);
});

it("repeats a continuing date on page two and keeps absolute locate number and focus", async () => {
  query = () => json(full());
  page = () => json({ ...full(), offset: 20, items: [{ ...sampleSnapshot().items[0], messageId: "health-21" }] });
  locate = () => json({ resultId: "result-1", found: true, index: 20, page: 2 });
  await mount();
  await confirm();
  expect(headings()).toEqual(["发生日期（UTC） · 2026-10-10"]);
  click("下一页");
  await screen.findByRole("article", { name: "消息 health-21" });
  expect(headings()).toEqual(["发生日期（UTC） · 2026-10-10"]);
  fireEvent.change(screen.getByLabelText("消息 ID 定位"), { target: { value: "health-21" } });
  click("定位");
  const row = screen.getByRole("article", { name: "消息 health-21" });
  await waitFor(() => expect(row).toHaveFocus());
  expect(row).toHaveClass("message-highlight");
  expect(row.querySelector(".message-row-number")).toHaveTextContent("21");
  expect(postCount()).toBe(1);
});

it.each([410, 403, 404, 401])("removes timeline dates with the protected snapshot on %s", async (status) => {
  query = () => json(full());
  page = () => json({ error: status === 410 ? "result_expired" : status === 403 ? "forbidden" : status === 401 ? "unauthenticated" : "not_found" }, status);
  await mount();
  await confirm();
  click("下一页");
  await waitFor(() => expect(screen.queryByRole("region", { name: "当前页时间线" })).toBeNull());
  expect(headings()).toEqual([]);
  expect(postCount()).toBe(1);
});

it("empty accepted pages do not invent a timeline date or count", async () => {
  query = () => json({ ...sampleSnapshot(), items: [], total: 0, counts: { all: 0, read: 0, unread: 0 } });
  await mount();
  click("确认查询");
  await screen.findByText("没有符合筛选的消息");
  expect(headings()).toEqual([]);
  expect(screen.queryByRole("region", { name: "当前页时间线" })).toBeNull();
});

it.each(["session", "unmount"])("late ignored-abort pages cannot restore timeline after %s", async (reason) => {
  query = () => json(full());
  let resolve!: (response: Response) => void;
  page = () => new Promise((done) => { resolve = done; });
  const view = await mount();
  await confirm();
  click("下一页");
  if (reason === "session")
    view.rerender(<MessagesWorkspace profile={{ ...profile, csrfToken: "replacement" }} sessionChanged={changed} />);
  else view.unmount();
  await act(async () => resolve(await json({ ...full(), offset: 20, items: [{ ...sampleSnapshot().items[0], messageId: "health-21" }] })));
  expect(headings()).toEqual([]);
  expect(screen.queryByRole("article")).toBeNull();
  view.unmount();
  expect(headings()).toEqual([]);
});

it("date headings are not selectable targets and selected bulk IDs remain exact", async () => {
  query = () => json(multipleDays());
  let submitted: Record<string, unknown> | undefined;
  mutation = (url, init) => {
    if (url !== "/api/messages/bulk-read") throw new Error(url);
    submitted = JSON.parse(String(init.body));
    return json({
      receipt: { operationId: submitted!.operationId, resultId: "result-1", selection: "selected", acceptedAt: "2026-10-11T00:01:00Z", target: 2 },
      state: "queued", updatedAt: "2026-10-11T00:01:00Z",
      progress: { target: 2, processed: 0, newlyRead: 0, alreadyRead: 0, failed: 0, remaining: 2 },
    });
  };
  await mount();
  await confirm();
  fireEvent.click(screen.getByRole("checkbox", { name: "选择消息 health-1" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "选择消息 health-3" }));
  expect(within(screen.getByRole("list", { name: "站内消息列表" })).getAllByRole("checkbox")).toHaveLength(3);
  click("预览批量目标");
  expect(screen.getByRole("region", { name: "批量目标预览" })).toHaveTextContent("确认冻结目标：2 条");
  click("确认启动批量已读");
  await waitFor(() => expect(submitted).toMatchObject({ resultId: "result-1", selection: "selected", messageIds: ["health-1", "health-3"] }));
  await waitFor(() => expect(screen.getByRole("region", { name: "批量已读操作" })).toHaveTextContent("已接受，等待处理"));
  expect(headings()).toEqual(["发生日期（UTC） · 2026-10-10", "发生日期（UTC） · 2026-10-09"]);
  expect(postCount()).toBe(1);
});

it("single read uses the original result/message IDs without regrouping snapshot rows", async () => {
  query = () => json(multipleDays());
  let submitted: Record<string, unknown> | undefined;
  mutation = (url, init) => {
    if (url !== "/api/messages/read") throw new Error(url);
    submitted = JSON.parse(String(init.body));
    return json({ ...submitted, firstReadAt: "2026-10-11T00:01:00Z", committedAt: "2026-10-11T00:01:00Z", progress: { target: 1, processed: 1, newlyRead: 1, alreadyRead: 0, failed: 0, remaining: 0 } });
  };
  await mount();
  await confirm();
  const row = screen.getByRole("article", { name: "消息 health-3" });
  fireEvent.click(within(row).getByRole("button", { name: "标记为已读" }));
  await within(row).findByRole("button", { name: "本次已确认已读" });
  expect(submitted).toMatchObject({ resultId: "result-1", messageId: "health-3" });
  expect(headings()).toEqual(["发生日期（UTC） · 2026-10-10", "发生日期（UTC） · 2026-10-09"]);
  expect(screen.getByRole("article", { name: "消息 health-3" })).toBe(row);
  expect(postCount()).toBe(1);
});

it("cancelled late query cannot create orphan timeline headings", async () => {
  let resolve!: (response: Response) => void;
  query = () => new Promise((done) => { resolve = done; });
  await mount();
  click("确认查询");
  click("停止等待");
  await act(async () => resolve(await json(multipleDays())));
  expect(headings()).toEqual([]);
  expect(screen.queryByRole("region", { name: "当前页时间线" })).toBeNull();
  expect(postCount()).toBe(1);
});
