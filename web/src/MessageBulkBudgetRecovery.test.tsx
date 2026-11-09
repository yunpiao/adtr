import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { StrictMode } from "react";
import type { Profile } from "./api";
import { useMessageBulk } from "./message-bulk";
import { sampleSnapshot } from "./message-test-fixtures";
import { validMessageSnapshot } from "./message-api";
import { loadBulkIntents, persistBulkIntent } from "./message-bulk-intent";
import type { BulkInput } from "./message-bulk-api";
import MessagesWorkspace from "./MessagesWorkspace";
const profile: Profile = {
  ID: 606,
  username: "budget-synthetic",
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
  csrfToken: "synthetic-csrf",
};
const snapshot = (offset = 0, resultId = "budget-result") => ({
  ...sampleSnapshot(),
  resultId,
  offset,
  limit: 200,
  total: 1000,
  counts: { all: 1000, unread: 1000, read: 0 },
  items: Array.from({ length: 200 }, (_, index) => ({
    ...sampleSnapshot().items[0],
    messageId: `00000000-0000-4000-8000-${String(offset + index).padStart(12, "0")}`,
  })),
});
function Harness({
  offset = 0,
  resultId = "budget-result",
  allowed = true,
  workspaceBusy = false,
}) {
  const value = snapshot(offset, resultId);
  const bulk = useMessageBulk(profile, allowed, value, vi.fn(), workspaceBusy);
  return (
    <>
      {bulk.controls}
      {bulk.panel}
      <button
        disabled={bulk.selectionDisabled}
        onClick={() => bulk.toggle(value.items[0].messageId)}
      >
        切换第一条
      </button>
    </>
  );
}
const button = (name: string) => screen.getByRole("button", { name });
const click = (name: string) => fireEvent.click(button(name));
const selection = () => screen.getByRole("region", { name: "批量目标选择" });
function oversized(strict = false) {
  const ui = (offset: number) =>
    strict ? (
      <StrictMode>
        <Harness offset={offset} />
      </StrictMode>
    ) : (
      <Harness offset={offset} />
    );
  const view = render(ui(0));
  for (const offset of [0, 200, 400, 600, 800]) {
    expect(validMessageSnapshot(snapshot(offset))).toBe(true);
    view.rerender(ui(offset));
    click("选择本页");
  }
  click("预览批量目标");
  click("确认启动批量已读");
  expect(screen.getByRole("alert")).toHaveTextContent("32 KiB");
  expect(fetch).not.toHaveBeenCalled();
  expect(loadBulkIntents(profile)).toEqual([]);
  return view;
}
beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init?: RequestInit) => {
      const input = JSON.parse(String(init?.body)) as BulkInput;
      const target = input.selection === "all" ? 1000 : input.messageIds.length;
      return new Response(
        JSON.stringify({
          receipt: {
            resultId: input.resultId,
            operationId: input.operationId,
            selection: input.selection,
            acceptedAt: "2026-10-11T00:01:00Z",
            target,
          },
          state: "queued",
          updatedAt: "2026-10-11T00:01:00Z",
          progress: {
            target,
            processed: 0,
            newlyRead: 0,
            alreadyRead: 0,
            failed: 0,
            remaining: target,
          },
        }),
      );
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it.each([false, true])(
  "keeps all selected IDs while explicitly returning from byte-budget rejection to editing (strict=%s)",
  async (strict) => {
    oversized(strict);
    expect(button("取消选择本页")).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "重新读取批量记录" }),
    ).not.toBeInTheDocument();
    click("返回调整批量选择");
    expect(selection()).toHaveTextContent("已选 1000 / 1000");
    expect(
      screen.queryByRole("region", { name: "批量目标预览" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
    click("取消选择本页");
    expect(selection()).toHaveTextContent("已选 800 / 1000");
    click("预览批量目标");
    await act(async () => click("确认启动批量已读"));
    expect(fetch).toHaveBeenCalledTimes(1);
    const input = JSON.parse(String(vi.mocked(fetch).mock.calls[0][1]?.body));
    expect(input.selection).toBe("selected");
    expect(input.messageIds).toEqual(
      Array.from(
        { length: 800 },
        (_, n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
      ),
    );
    expect(
      new TextEncoder().encode(JSON.stringify(input)).length,
    ).toBeLessThanOrEqual(32768);
    expect(loadBulkIntents(profile)[0].input).toEqual(input);
  },
);
it("can switch to fixed-result all after rejection without creating a replacement query", async () => {
  oversized();
  click("返回调整批量选择");
  click("选择固定结果全部");
  click("预览批量目标");
  await act(async () => click("确认启动批量已读"));
  expect(fetch).toHaveBeenCalledTimes(1);
  const [url, init] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe("/api/messages/bulk-read");
  expect(JSON.parse(String(init?.body))).toEqual({
    resultId: "budget-result",
    operationId: expect.any(String),
    selection: "all",
  });
});
it("unchanged oversized selections are rejected again without persistence or POST", () => {
  oversized();
  click("返回调整批量选择");
  click("预览批量目标");
  click("确认启动批量已读");
  expect(screen.getByRole("alert")).toHaveTextContent("32 KiB");
  expect(fetch).not.toHaveBeenCalled();
  expect(loadBulkIntents(profile)).toEqual([]);
});
it("editing recovery performs no storage I/O; a later write failure still blocks POST", () => {
  oversized();
  const get = vi.spyOn(Storage.prototype, "getItem");
  const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new DOMException("synthetic", "QuotaExceededError");
  });
  click("返回调整批量选择");
  expect(get).not.toHaveBeenCalled();
  expect(set).not.toHaveBeenCalled();
  click("取消选择本页");
  click("预览批量目标");
  click("确认启动批量已读");
  expect(screen.getByRole("alert")).toHaveTextContent("无法可靠保存批量原操作");
  expect(button("重新读取批量记录")).toBeEnabled();
  expect(
    screen.queryByRole("button", { name: "返回调整批量选择" }),
  ).not.toBeInTheDocument();
  expect(button("选择固定结果全部")).toBeDisabled();
  expect(fetch).not.toHaveBeenCalled();
  expect(loadBulkIntents(profile)).toEqual([]);
});
it("focus/storage refresh cannot silently clear the byte-budget rejection", () => {
  oversized();
  fireEvent(window, new Event("focus"));
  fireEvent(window, new Event("storage"));
  expect(screen.getByRole("alert")).toHaveTextContent("32 KiB");
  expect(button("确认启动批量已读")).toBeDisabled();
  expect(button("返回调整批量选择")).toBeEnabled();
  expect(fetch).not.toHaveBeenCalled();
});
it("pending page reads keep the recovery action disabled until the confirmed page is usable", () => {
  const view = oversized();
  view.rerender(<Harness offset={800} workspaceBusy />);
  expect(button("返回调整批量选择")).toBeDisabled();
  click("返回调整批量选择");
  expect(
    screen.getByRole("region", { name: "批量目标预览" }),
  ).toBeInTheDocument();
  view.rerender(<Harness offset={800} />);
  click("返回调整批量选择");
  expect(selection()).toHaveTextContent("已选 1000 / 1000");
  expect(fetch).not.toHaveBeenCalled();
});
it("new fixed results do not inherit an old result's local byte-budget failure", () => {
  const view = oversized();
  view.rerender(<Harness resultId="new-result" />);
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("region", { name: "批量目标预览" }),
  ).not.toBeInTheDocument();
  expect(selection()).toHaveTextContent("已选 0 / 1000");
  expect(button("选择本页")).toBeEnabled();
  expect(fetch).not.toHaveBeenCalled();
});
it("revoked permission clears budget state and never restores the old selection", () => {
  const view = oversized();
  view.rerender(<Harness allowed={false} />);
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  view.rerender(<Harness />);
  expect(selection()).toHaveTextContent("已选 0 / 1000");
  expect(
    screen.queryByRole("button", { name: "返回调整批量选择" }),
  ).not.toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});
it("another tab's uncertain operation prevents using budget recovery to enable a new batch", () => {
  oversized();
  persistBulkIntent(profile, {
    input: {
      resultId: "other-result",
      operationId: "other-operation",
      selection: "all",
    },
    target: 1,
  });
  fireEvent(window, new Event("storage"));
  expect(button("返回调整批量选择")).toBeDisabled();
  expect(button("确认启动批量已读")).toBeDisabled();
  expect(loadBulkIntents(profile)).toHaveLength(1);
  expect(fetch).not.toHaveBeenCalled();
});
it("single-item edits retain other pages and demand a new preview", () => {
  oversized();
  click("返回调整批量选择");
  click("切换第一条");
  expect(selection()).toHaveTextContent("已选 999 / 1000");
  expect(
    screen.queryByRole("region", { name: "批量目标预览" }),
  ).not.toBeInTheDocument();
  click("预览批量目标");
  click("确认启动批量已读");
  expect(screen.getByRole("alert")).toHaveTextContent("32 KiB");
  expect(fetch).not.toHaveBeenCalled();
});
it("full workspace and real HTTP adapters recover by reducing the last page without losing the first page", async () => {
  const click = (name: string) =>
    fireEvent.click(
      screen.getByText(name, { selector: "button", exact: true }),
    );
  const workspaceSnapshot = (offset = 0) => ({
    ...snapshot(offset),
    total: 400,
    counts: { all: 400, unread: 400, read: 0 },
    items: snapshot(offset).items.map((row) => ({
      ...row,
      messageId: row.messageId + "x".repeat(72),
    })),
  });
  const post = vi.mocked(fetch).getMockImplementation()!;
  let accepted: { receipt: { operationId: string } } | undefined;
  vi.mocked(fetch).mockImplementation(async (url, init) => {
    const value = String(url);
    if (value === "/api/access/menu")
      return new Response(
        JSON.stringify({
          menu: [{ mark: "tasks", auth: { readable: true, writeable: false } }],
        }),
      );
    if (value === "/api/access/check")
      return new Response(JSON.stringify({ results: Array(8).fill(true) }));
    if (value === "/api/messages/query")
      return new Response(JSON.stringify(workspaceSnapshot()));
    if (value.startsWith("/api/messages/page?"))
      return new Response(
        JSON.stringify(
          workspaceSnapshot(
            Number(
              new URL(value, "http://synthetic").searchParams.get("offset"),
            ),
          ),
        ),
      );
    if (value === "/api/messages/bulk-read") {
      const response = await post(url, init);
      accepted = await response.clone().json();
      return response;
    }
    if (value.startsWith("/api/messages/bulk-read-operation?")) {
      expect(
        new URL(value, "http://synthetic").searchParams.get("operationId"),
      ).toBe(accepted?.receipt.operationId);
      return new Response(JSON.stringify(accepted));
    }
    throw Error("unexpected request");
  });
  render(<MessagesWorkspace profile={profile} sessionChanged={vi.fn()} />);
  await screen.findByRole("button", { name: "确认查询" });
  fireEvent.change(screen.getByLabelText("每页条数"), {
    target: { value: "200" },
  });
  await act(async () => click("确认查询"));
  for (let page = 0; page < 2; page++) {
    if (page) await act(async () => click("下一页"));
    await screen.findByLabelText(
      `选择消息 00000000-0000-4000-8000-${String(page * 200).padStart(12, "0")}${"x".repeat(72)}`,
    );
    click("选择本页");
  }
  click("预览批量目标");
  click("确认启动批量已读");
  expect(screen.getByRole("alert")).toHaveTextContent("32 KiB");
  const before = vi.mocked(fetch).mock.calls.length;
  click("返回调整批量选择");
  click("取消选择本页");
  click("预览批量目标");
  expect(fetch).toHaveBeenCalledTimes(before);
  await act(async () => click("确认启动批量已读"));
  await waitFor(() =>
    expect(
      screen.getByRole("heading", { name: "已接受，等待处理" }),
    ).toBeInTheDocument(),
  );
  await act(async () => click("查询原批次进度"));
  expect(
    screen.getByRole("heading", { name: "已接受，等待处理" }),
  ).toBeInTheDocument();
  expect(
    vi
      .mocked(fetch)
      .mock.calls.some(([u]) =>
        String(u).startsWith("/api/messages/bulk-read-operation?"),
      ),
  ).toBe(true);
  const writes = vi
    .mocked(fetch)
    .mock.calls.filter(([u]) => u === "/api/messages/bulk-read");
  expect(writes).toHaveLength(1);
  const input = JSON.parse(String(writes[0][1]?.body));
  expect(input.messageIds).toHaveLength(200);
  expect(input.messageIds[0]).toBe(workspaceSnapshot().items[0].messageId);
  expect(input.messageIds[199]).toBe(workspaceSnapshot().items[199].messageId);
  expect(
    vi.mocked(fetch).mock.calls.filter(([u]) => u === "/api/messages/query"),
  ).toHaveLength(1);
  expect(
    vi
      .mocked(fetch)
      .mock.calls.filter(([u]) => String(u).startsWith("/api/messages/page?")),
  ).toHaveLength(1);
});
