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
        return json({ results: [true, true, true] });
      if (url === "/api/messages/query") return query(init);
      if (url.startsWith("/api/messages/page?")) return page(url);
      if (url.startsWith("/api/messages/locate?")) return locate();
      throw new Error(url);
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
it("requires explicit confirmation even in StrictMode, shows true query counts and UTC", async () => {
  await mount(true);
  expect(postCount()).toBe(0);
  expect(screen.getByLabelText("阅读状态")).toHaveValue("all");
  await confirm();
  expect(postCount()).toBe(1);
  expect(screen.getByText("result-1")).toBeInTheDocument();
  expect(
    screen.getByText("2026-10-10 23:59:59.123456789 UTC"),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /标记/ }),
  ).not.toBeInTheDocument();
});
it("keeps draft separate and pages only the original result", async () => {
  query = () => json(full());
  page = (url) =>
    json({
      ...full(),
      offset: Number(new URL(url, "http://test").searchParams.get("offset")),
      items: [{ ...sampleSnapshot().items[0], messageId: "health-21" }],
    });
  await mount();
  await confirm();
  fireEvent.change(screen.getByLabelText("关键词"), {
    target: { value: "new draft" },
  });
  fireEvent.change(screen.getByLabelText("每页条数"), {
    target: { value: "50" },
  });
  expect(screen.getByText(/筛选草稿已改变/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("article", { name: "消息 health-21" });
  expect(postCount()).toBe(1);
  expect(
    vi
      .mocked(fetch)
      .mock.calls.some(
        ([url]) =>
          url === "/api/messages/page?resultId=result-1&offset=20&limit=20",
      ),
  ).toBe(true);
});
it("locks rapid duplicate confirms", async () => {
  let done!: (value: Response) => void;
  query = () => new Promise((r) => (done = r));
  await mount();
  const button = screen.getByRole("button", { name: "确认查询" });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(postCount()).toBe(1);
  await act(async () => done(new Response(JSON.stringify(sampleSnapshot()))));
});
it("unknown query response warns of quota use and never retries", async () => {
  query = () => Promise.reject(new TypeError("lost"));
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "可能已创建快照并占用配额",
  );
  expect(postCount()).toBe(1);
  expect(
    screen.queryByRole("button", { name: "重试原结果读取" }),
  ).not.toBeInTheDocument();
});
it("cancelled query cannot resurrect a result when response ignores abort", async () => {
  let done!: (value: Response) => void;
  query = () => new Promise((r) => (done = r));
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  fireEvent.click(screen.getByRole("button", { name: "停止等待" }));
  await act(async () => done(new Response(JSON.stringify(sampleSnapshot()))));
  expect(screen.queryByText("result-1")).not.toBeInTheDocument();
  expect(screen.getByText(/服务器可能已创建快照/)).toBeInTheDocument();
});
it("new confirmation wins over cancelled late JSON", async () => {
  let done!: (value: unknown) => void;
  query = () =>
    Promise.resolve({
      ok: true,
      status: 200,
      json: () => new Promise((r) => (done = r)),
    } as Response);
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await waitFor(() => expect(done).toBeTypeOf("function"));
  fireEvent.click(screen.getByRole("button", { name: "停止等待" }));
  query = () => json({ ...sampleSnapshot(), resultId: "result-new" });
  await confirm();
  await act(async () => done(sampleSnapshot()));
  expect(screen.getByText("result-new")).toBeInTheDocument();
  expect(screen.queryByText("result-1")).not.toBeInTheDocument();
});
it.each([
  [410, "result_expired", "旧结果已清除"],
  [403, "forbidden", "权限"],
  [404, "not_found", "当前无法取得"],
])("clears result on GET %s", async (status, code, text) => {
  query = () => json(full());
  page = () => json({ error: code }, Number(status));
  await mount();
  await confirm();
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(String(text));
  expect(screen.queryByText("result-1")).not.toBeInTheDocument();
  expect(postCount()).toBe(1);
});
it.each([
  [429, "snapshot_quota_exceeded", "没有提供可用时间"],
  [422, "query_scan_budget_exceeded", "缩小开始与结束时间范围"],
])("explains query %s without guessing", async (status, code, text) => {
  query = () => json({ error: code }, Number(status));
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(String(text));
  expect(postCount()).toBe(1);
});
it("retries original GET after draft changes without new POST", async () => {
  query = () => json(full());
  page = () => Promise.reject(new TypeError("lost"));
  await mount();
  await confirm();
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await screen.findByRole("button", { name: "重试原结果读取" });
  fireEvent.change(screen.getByLabelText("每页条数"), {
    target: { value: "100" },
  });
  page = () =>
    json({
      ...full(),
      offset: 20,
      items: [{ ...sampleSnapshot().items[0], messageId: "health-21" }],
    });
  fireEvent.click(screen.getByRole("button", { name: "重试原结果读取" }));
  await screen.findByRole("article", { name: "消息 health-21" });
  expect(postCount()).toBe(1);
  expect(
    vi
      .mocked(fetch)
      .mock.calls.filter(
        ([url]) =>
          url === "/api/messages/page?resultId=result-1&offset=20&limit=20",
      ),
  ).toHaveLength(2);
});
it("locates on the fixed result and focuses the message", async () => {
  await mount();
  await confirm();
  fireEvent.change(screen.getByLabelText("消息 ID 定位"), {
    target: { value: "health-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "定位" }));
  await waitFor(() =>
    expect(
      screen.getByRole("article", { name: "消息 health-1" }),
    ).toHaveFocus(),
  );
  expect(screen.getByRole("article", { name: "消息 health-1" })).toHaveClass(
    "message-highlight",
  );
  expect(postCount()).toBe(1);
});
it("reports locate miss without changing page or creating query", async () => {
  locate = () =>
    json({ resultId: "result-1", found: false, index: -1, page: 0 });
  await mount();
  await confirm();
  fireEvent.change(screen.getByLabelText("消息 ID 定位"), {
    target: { value: "health-2" },
  });
  fireEvent.click(screen.getByRole("button", { name: "定位" }));
  await screen.findByText(/该消息不在当前固定结果/);
  expect(postCount()).toBe(1);
  expect(
    vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes("/page?")),
  ).toBe(false);
});
it("does not show another session result during same-user CSRF rotation", async () => {
  const view = await mount();
  await confirm();
  view.rerender(
    <MessagesWorkspace
      profile={{ ...profile, csrfToken: "new-session" }}
      sessionChanged={changed}
    />,
  );
  expect(screen.queryByText("result-1")).not.toBeInTheDocument();
  await screen.findByRole("button", { name: "确认查询" });
  expect(postCount()).toBe(1);
});
it("routes unauthenticated query to App session verification", async () => {
  query = () => json({ error: "unauthenticated" }, 401);
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
  expect(
    screen.queryByRole("list", { name: "站内消息列表" }),
  ).not.toBeInTheDocument();
});
it("empty results have zero counts rather than a waiting placeholder", async () => {
  query = () =>
    json({
      ...sampleSnapshot(),
      items: [],
      total: 0,
      counts: { all: 0, read: 0, unread: 0 },
    });
  await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  await screen.findByText("没有符合筛选的消息");
  expect(
    within(screen.getByRole("region", { name: "查询时消息统计" })).getAllByText(
      "0",
    ),
  ).toHaveLength(4);
});
it("invalid UTC pair cannot create a query", async () => {
  await mount();
  fireEvent.change(screen.getByLabelText("开始时间（UTC）"), {
    target: { value: "2026-10-01T00:00:00Z" },
  });
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("同时填写");
  expect(postCount()).toBe(0);
});
it("unmount aborts active request and discards its late response", async () => {
  let done!: (value: Response) => void;
  query = () => new Promise((r) => (done = r));
  const view = await mount();
  fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
  const signal = vi
    .mocked(fetch)
    .mock.calls.find(([url]) => url === "/api/messages/query")![1]!.signal!;
  view.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => done(new Response(JSON.stringify(sampleSnapshot()))));
  expect(changed).not.toHaveBeenCalled();
});
it("denies a missing tasks grant even when path checks are true", async () => {
  const delegate = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation((url, init) =>
    url === "/api/access/menu"
      ? json({
          menu: [{ mark: "tasks", auth: { readable: false, writeable: true } }],
        })
      : delegate(url, init),
  );
  render(<MessagesWorkspace profile={profile} sessionChanged={changed} />);
  await screen.findByText("消息读取尚未获准");
  expect(
    screen.queryByRole("button", { name: "确认查询" }),
  ).not.toBeInTheDocument();
  expect(postCount()).toBe(0);
});
it("requires exact route checks and disables unauthorized locate", async () => {
  const delegate = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation((url, init) =>
    url === "/api/access/check"
      ? json({ results: [true, true, false] })
      : delegate(url, init),
  );
  await mount();
  await confirm();
  expect(screen.getByRole("button", { name: "定位" })).toBeDisabled();
});
it("rejects malformed grants rather than allowing query", async () => {
  const delegate = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation((url, init) =>
    url === "/api/access/check"
      ? json({ results: [true] })
      : delegate(url, init),
  );
  render(<MessagesWorkspace profile={profile} sessionChanged={changed} />);
  await screen.findByRole("alert");
  expect(
    screen.queryByRole("button", { name: "确认查询" }),
  ).not.toBeInTheDocument();
  expect(postCount()).toBe(0);
});
it("late page response cannot overwrite a newer confirmed snapshot", async () => {
  query = () => json(full());
  let done!: (value: Response) => void;
  page = () => new Promise((r) => (done = r));
  await mount();
  await confirm();
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  fireEvent.click(screen.getByRole("button", { name: "停止等待" }));
  query = () => json({ ...sampleSnapshot(), resultId: "new-result" });
  await confirm();
  await act(async () =>
    done(
      new Response(
        JSON.stringify({
          ...full(),
          offset: 20,
          items: [{ ...sampleSnapshot().items[0], messageId: "health-21" }],
        }),
      ),
    ),
  );
  expect(screen.getByText("new-result")).toBeInTheDocument();
  expect(
    screen.queryByRole("article", { name: "消息 health-21" }),
  ).not.toBeInTheDocument();
});
it("navigation remount never restores a private result or repeats a POST", async () => {
  const view = await mount();
  await confirm();
  view.unmount();
  await mount();
  expect(postCount()).toBe(1);
  expect(screen.queryByText("result-1")).not.toBeInTheDocument();
  expect(screen.getByText("等待确认查询")).toBeInTheDocument();
});
it("rejects a locate index that contradicts the actual fixed row", async () => {
  const two = {
    ...sampleSnapshot(),
    total: 2,
    counts: { all: 2, unread: 2, read: 0 },
    items: [
      sampleSnapshot().items[0],
      { ...sampleSnapshot().items[0], messageId: "health-2" },
    ],
  };
  query = () => json(two);
  page = () => json(two);
  locate = () => json({ resultId: "result-1", found: true, index: 1, page: 1 });
  await mount();
  await confirm();
  fireEvent.change(screen.getByLabelText("消息 ID 定位"), {
    target: { value: "health-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "定位" }));
  await screen.findByRole("alert");
  expect(
    screen.getByRole("article", { name: "消息 health-1" }),
  ).not.toHaveClass("message-highlight");
});
it("retries locate then page under the same fixed result after page fails", async () => {
  await mount();
  await confirm();
  page = () => Promise.reject(new TypeError("lost"));
  fireEvent.change(screen.getByLabelText("消息 ID 定位"), {
    target: { value: "health-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "定位" }));
  await screen.findByRole("button", { name: "重试原结果读取" });
  fireEvent.change(screen.getByLabelText("消息 ID 定位"), {
    target: { value: "health-OTHER-DRAFT" },
  });
  page = () => json(sampleSnapshot());
  fireEvent.click(screen.getByRole("button", { name: "重试原结果读取" }));
  await waitFor(() =>
    expect(
      screen.getByRole("article", { name: "消息 health-1" }),
    ).toHaveFocus(),
  );
  expect(
    vi
      .mocked(fetch)
      .mock.calls.filter(
        ([url]) =>
          url ===
          "/api/messages/locate?resultId=result-1&messageId=health-1&limit=20",
      ),
  ).toHaveLength(2);
  expect(postCount()).toBe(1);
});
it("cancelled locate never initiates its following page", async () => {
  let done!: (value: Response) => void;
  locate = () => new Promise((r) => (done = r));
  await mount();
  await confirm();
  fireEvent.change(screen.getByLabelText("消息 ID 定位"), {
    target: { value: "health-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "定位" }));
  fireEvent.click(screen.getByRole("button", { name: "停止等待" }));
  await act(async () =>
    done(
      new Response(
        JSON.stringify({
          resultId: "result-1",
          found: true,
          index: 0,
          page: 1,
        }),
      ),
    ),
  );
  expect(
    vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes("/page?")),
  ).toBe(false);
});
it("binds exact isolated labels independently of select option text", async () => {
  await mount();
  for (const label of ["任务结果", "阅读状态", "排序", "每页条数"]) {
    const select = screen.getByLabelText(label);
    const labelID = select.getAttribute("aria-labelledby");
    expect(labelID).toBeTruthy();
    expect(document.getElementById(labelID!)?.textContent).toBe(label);
    expect(screen.getByRole("combobox", { name: label })).toBe(select);
  }
});
