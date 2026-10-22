// Synthetic DOM and protocol tests, not live browser/Worker evidence.
import { webcrypto } from "node:crypto";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import GroupAssetExportTaskCancel from "./GroupAssetExportTaskCancel";
import { ApiError } from "./api";
import * as api from "./group-asset-export-v3-api";
import { cancelMarkerBytes, cancelStorageKey } from "./group-export-cancel-intent";
import { exportJSON, exportProfile, exportReceipt, exportTask, readyExport } from "./group-asset-export-v3-fixtures";
const pending = { ...exportReceipt, downloadReady: false, downloadStatus: "pending" as const };
const cancelled = { ...pending, task: { ...exportTask, state: "cancelled" as const }, downloadStatus: "cancelled" as const };
const changed = vi.fn();
function view(profile = exportProfile, task = exportTask) { return render(<GroupAssetExportTaskCancel profile={profile} task={task} sessionChanged={changed} />); }
async function open() { fireEvent.click(screen.getByRole("button", { name: "核对原组导出取消" })); await waitFor(() => expect(screen.queryByText("正在核对原组导出取消…")).toBeNull()); }
function proof() {
  fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
  fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
}
function gates(write = true) {
  vi.stubGlobal("fetch", vi.fn(async (path: string) => {
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "group_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: write } })) });
    if (path === "/api/access/check") return exportJSON({ results: [true, write] });
    throw new Error("unexpected-private-transport");
  }));
}
beforeEach(() => {
  vi.stubGlobal("navigator", Object.create(navigator));
  localStorage.clear(); sessionStorage.clear(); changed.mockClear(); vi.stubGlobal("crypto", webcrypto);
  Object.defineProperty(navigator, "locks", { configurable: true, value: { request: vi.fn(async (_name, _options, callback) => callback({ name: _name })) } });
  gates(); vi.spyOn(api.groupAssetExportAPI, "detailByTask").mockResolvedValue(pending);
  vi.spyOn(api.groupAssetExportAPI, "detail").mockResolvedValue(pending);
  vi.spyOn(api.groupAssetExportAPI, "recheckCancelAuthority").mockResolvedValue(pending);
  vi.spyOn(api.groupAssetExportAPI, "cancel").mockResolvedValue(exportTask);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear(); });
describe("original-task group export cancellation", () => {
  it("is collapsed, private-free and performs no business reads on mount", () => {
    view(); expect(fetch).not.toHaveBeenCalled(); expect(api.groupAssetExportAPI.detailByTask).not.toHaveBeenCalled();
    expect(document.querySelector("pre,input")).toBeNull(); expect(screen.getByRole("button", { name: "核对原组导出取消" })).toBeEnabled();
  });
  it("renders nothing for another producer", () => {
    view(exportProfile, { ...exportTask, taskName: "user_assets.v3.export" }); expect(document.querySelector("button")).toBeNull();
  });
  it("does not require or create a submission locator", async () => {
    view(); await open(); expect(screen.getByText(exportTask.taskUUID)).toBeVisible(); expect(sessionStorage.length).toBe(0);
    expect(api.groupAssetExportAPI.detailByTask).toHaveBeenCalledWith(exportTask.taskUUID, exportTask.domainId, exportProfile.ID, expect.any(AbortSignal));
  });
  it("separates dedicated read from cancellation permission", async () => {
    gates(false); view(); await open(); expect(screen.getByText(exportTask.taskUUID)).toBeVisible();
    expect(document.querySelector("input")).toBeNull(); expect(screen.getByText("当前仅有原导出读取权限，不能取消。")).toBeVisible();
  });
  it("clears proof synchronously and dispatches once with the original request", async () => {
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValueOnce(pending).mockResolvedValue(cancelled);
    view(); await open(); proof();
    const button = screen.getByRole("button", { name: "取消原组导出任务" }); fireEvent.click(button); fireEvent.click(button);
    expect(document.querySelector("input")).toBeNull();
    await screen.findByText("服务器已确认取消，没有可下载文件");
    expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
    expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledWith(exportTask.taskUUID, exportReceipt.request, expect.any(Object), exportProfile.ID, exportProfile.csrfToken, expect.any(AbortSignal), exportProfile);
    expect(vi.mocked(fetch).mock.calls.every(([url]) => String(url).startsWith("/api/access/"))).toBe(true);
  });
  it("unknown cancel stays query-only after manual running reads and remount", async () => {
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("network"));
    const first = view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "取消原组导出任务" }));
    await screen.findByText(/可能已发出取消请求/);
    fireEvent.click(screen.getByRole("button", { name: "读取原组导出当前状态" })); await screen.findByText(/可能已发出取消请求/);
    expect(document.querySelector("input")).toBeNull(); first.unmount(); view(); await open();
    expect(document.querySelector("input")).toBeNull(); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledTimes(1);
  });
  it("missing Web Locks disables only cancellation and explains the reason", async () => {
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    view(); await open(); expect(screen.getByText(/浏览器缺少可靠的本地存储或独占锁/)).toBeVisible();
    expect(screen.getByRole("button", { name: "读取原组导出当前状态" })).toBeEnabled(); expect(document.querySelector("input")).toBeNull();
  });
  it("another entry's marker suppresses the new form", async () => {
    const key = cancelStorageKey(exportProfile.ID, exportTask.taskUUID);
    localStorage.setItem(key, await cancelMarkerBytes({ profile: exportProfile, taskID: exportTask.taskUUID, request: exportReceipt.request, signal: new AbortController().signal, assertCurrent: () => undefined }));
    view(); await open(); expect(document.querySelector("input")).toBeNull(); expect(screen.getByText(/可能已发出取消请求/)).toBeVisible();
  });
  it.each(["focus", "visibility", "storage"])("invalidates private proof on %s without fetching", async (event) => {
    view(); await open(); proof(); const before = vi.mocked(fetch).mock.calls.length;
    if (event === "focus") fireEvent(window, new Event("focus"));
    if (event === "visibility") fireEvent(document, new Event("visibilitychange"));
    if (event === "storage") fireEvent(window, new StorageEvent("storage", { key: null, storageArea: localStorage }));
    expect(document.querySelector("input,pre")).toBeNull(); expect(vi.mocked(fetch).mock.calls.length).toBe(before);
  });
  it("sessionStorage events do not invalidate dedicated local marker preparation", async () => {
    view(); await open(); fireEvent(window, new StorageEvent("storage", { key: null, storageArea: sessionStorage }));
    expect(screen.getByLabelText("操作者当前密码")).toBeVisible();
  });
  it("unchanged generic polling preserves proof but changed task state invalidates it", async () => {
    const rendered = view(); await open(); proof();
    rendered.rerender(<GroupAssetExportTaskCancel profile={exportProfile} task={{ ...exportTask }} sessionChanged={changed} />);
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("SyntheticPassword123!");
    rendered.rerender(<GroupAssetExportTaskCancel profile={exportProfile} task={{ ...exportTask, state: "cancel_requested" }} sessionChanged={changed} />);
    expect(document.querySelector("input,pre")).toBeNull();
  });
  it("identity rotation destroys private state and fences late reads", async () => {
    let resolve!: (value: typeof pending) => void;
    vi.mocked(api.groupAssetExportAPI.detailByTask).mockReturnValue(new Promise((r) => { resolve = r; }));
    const rendered = view(); fireEvent.click(screen.getByRole("button", { name: "核对原组导出取消" }));
    await waitFor(() => expect(api.groupAssetExportAPI.detailByTask).toHaveBeenCalled());
    rendered.rerender(<GroupAssetExportTaskCancel profile={{ ...exportProfile, csrfToken: "new-session" }} task={exportTask} sessionChanged={changed} />);
    await act(async () => resolve(pending)); expect(document.querySelector("pre,input")).toBeNull();
  });
  it.each([401, 403, 404])("authority %s clears private pixels without echoing errors", async (status) => {
    const error = new ApiError("canary-body-header-csrf", status); error.stack = "canary-stack";
    vi.mocked(api.groupAssetExportAPI.detailByTask).mockRejectedValue(error);
    view(); await open(); expect(document.querySelector("input,pre")).toBeNull(); expect(document.body.textContent).not.toContain("canary");
    expect(changed).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
  });
  it.each(["malformed", "delete-failed"])("terminal state remains readable with %s local marker", async (mode) => {
    const key = cancelStorageKey(exportProfile.ID, exportTask.taskUUID);
    const bytes = await cancelMarkerBytes({ profile: exportProfile, taskID: exportTask.taskUUID, request: exportReceipt.request, signal: new AbortController().signal, assertCurrent: () => undefined });
    localStorage.setItem(key, mode === "malformed" ? "broken-marker" : bytes);
    if (mode === "delete-failed") vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("private-canary"); });
    vi.mocked(api.groupAssetExportAPI.detailByTask).mockResolvedValue(cancelled);
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(cancelled);
    view(); await open();
    expect(screen.getByText("服务器已确认取消，没有可下载文件")).toBeVisible();
    expect(screen.getByText(/不能确认记录是否仍保留/)).toBeVisible();
    expect(document.querySelector("input")).toBeNull(); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("shows success-winning terminal state without claiming cancellation won", async () => {
    vi.mocked(api.groupAssetExportAPI.detailByTask).mockResolvedValue(readyExport); vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(readyExport);
    view(); await open(); expect(screen.getByText(/不能断言取消获胜/)).toBeVisible(); expect(screen.queryByText("服务器已确认取消，没有可下载文件")).toBeNull();
  });
  it("verified rejected proof allows only a new explicit proof, without automatic POST", async () => {
    vi.spyOn(api, "isGroupExportCancelProofRejected").mockReturnValue(true);
    vi.mocked(api.groupAssetExportAPI.cancel).mockRejectedValue(new ApiError("invalid_credentials", 401));
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "取消原组导出任务" }));
    await screen.findByText("密码或验证码未通过；请重新输入。");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    expect(localStorage.length).toBe(0); expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce();
    expect(changed).not.toHaveBeenCalled();
  });
  it("a read-only timeout can be explicitly rechecked without a permanent cancel barrier", async () => {
    vi.mocked(api.groupAssetExportAPI.detailByTask).mockReturnValueOnce(new Promise(() => undefined));
    const nativeTimeout = globalThis.setTimeout;
    let expire: (() => void) | undefined;
    vi.spyOn(globalThis, "setTimeout").mockImplementation(((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      if (delay === 30000 && typeof handler === "function") expire = () => handler();
      return nativeTimeout(handler, delay, ...args);
    }) as typeof setTimeout);
    view(); fireEvent.click(screen.getByRole("button", { name: "核对原组导出取消" }));
    await waitFor(() => expect(api.groupAssetExportAPI.detailByTask).toHaveBeenCalledOnce());
    act(() => expire!()); expect(localStorage.length).toBe(0); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
    expect(screen.getByText(/读取或核验超时/)).toBeVisible();
    vi.mocked(api.groupAssetExportAPI.detailByTask).mockResolvedValue(pending);
    await open(); expect(screen.getByRole("button", { name: "取消原组导出任务" })).toBeEnabled();
  });
  it("the owned deadline aborts an in-flight POST and ignores late completion", async () => {
    let resolve!: (value: typeof exportTask) => void;
    vi.mocked(api.groupAssetExportAPI.cancel).mockReturnValue(new Promise((r) => { resolve = r; }));
    view(); await open();
    const nativeTimeout = globalThis.setTimeout;
    let expire: (() => void) | undefined;
    vi.spyOn(globalThis, "setTimeout").mockImplementation(((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      if (delay === 30000 && typeof handler === "function") expire = () => handler();
      return nativeTimeout(handler, delay, ...args);
    }) as typeof setTimeout);
    proof(); fireEvent.click(screen.getByRole("button", { name: "取消原组导出任务" }));
    await waitFor(() => expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce());
    act(() => expire!());
    expect(vi.mocked(api.groupAssetExportAPI.cancel).mock.calls[0][5].aborted).toBe(true);
    expect(document.querySelector("pre,input")).toBeNull();
    await act(async () => resolve(exportTask));
    expect(localStorage.length).toBe(1); expect(document.querySelector("pre,input")).toBeNull();
  });
  it("unmount aborts and fences late mutation responses without clearing the marker", async () => {
    let resolve!: (value: typeof exportTask) => void;
    vi.mocked(api.groupAssetExportAPI.cancel).mockReturnValue(new Promise((r) => { resolve = r; }));
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "取消原组导出任务" }));
    await waitFor(() => expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce());
    const signal = vi.mocked(api.groupAssetExportAPI.cancel).mock.calls[0][5];
    cleanup(); expect(signal.aborted).toBe(true);
    await act(async () => resolve(exportTask)); expect(localStorage.length).toBe(1); expect(document.querySelector("input,pre")).toBeNull();
  });
});

describe("group task identity, read gates and explicit lifecycle", () => {
  it.each([
    { taskName: "computer_assets.v3.export" }, { taskName: "group_assets.v3.exported" },
    { taskUUID: "../task" }, { taskUUID: "x".repeat(129) },
    { domainId: "platform" }, { domainId: "" }, { archived: true },
  ])("has no cancellation control for an ineligible generic task %j", (change) => {
    view(exportProfile, { ...exportTask, ...change });
    expect(document.querySelector("button, input, pre")).toBeNull(); expect(fetch).not.toHaveBeenCalled();
  });
  it.each(["missing", "duplicate", "wrong-type", "too-long", "wrong-results"])("rejects malformed current gates: %s", async (mode) => {
    vi.stubGlobal("fetch", vi.fn(async (path: string) => {
      const marks = ["domains", "directory_assets", "tasks", "group_asset_exports"];
      const menu: unknown[] = marks.map((mark) => ({ mark, auth: { readable: true, writeable: true } }));
      if (mode === "missing") menu.pop();
      if (mode === "duplicate") menu.push(menu[3]);
      if (mode === "wrong-type") menu[3] = { mark: marks[3], auth: { readable: true, writeable: "yes" } };
      if (mode === "too-long") menu.push(...Array.from({ length: 257 }, () => ({ mark: "other", auth: { readable: true, writeable: true } })));
      return exportJSON(path === "/api/access/menu" ? { menu } : { results: mode === "wrong-results" ? [true, "true"] : [true, true] });
    }));
    view(); await open(); expect(api.groupAssetExportAPI.detailByTask).not.toHaveBeenCalled();
    expect(document.querySelector("input,pre")).toBeNull(); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("read denial remains independent of the cancel operation check", async () => {
    vi.stubGlobal("fetch", vi.fn(async (path: string) => exportJSON(path === "/api/access/menu"
      ? { menu: ["domains", "directory_assets", "tasks", "group_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) }
      : { results: [false, true] })));
    view(); await open(); expect(api.groupAssetExportAPI.detailByTask).not.toHaveBeenCalled(); expect(document.querySelector("input,pre")).toBeNull();
  });
  it.each([{ hasMfa: false }, { needChangePwd: true }, { isExpired: true }])("keeps original state readable without eligible proof preparation %j", async (change) => {
    view({ ...exportProfile, ...change }); await open();
    expect(screen.getByText(exportTask.taskUUID)).toBeVisible(); expect(document.querySelector("input")).toBeNull();
  });
  it("pins all original request fields across close and explicit reopen", async () => {
    view(); await open(); const pinned = vi.mocked(api.groupAssetExportAPI.detail).mock.calls[0][1];
    fireEvent.click(screen.getByRole("button", { name: "关闭原组导出取消" }));
    vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue({ ...pending, request: { ...pending.request, observationId: "changed-observation" } });
    await open(); expect(api.groupAssetExportAPI.detailByTask).toHaveBeenCalledOnce();
    expect(vi.mocked(api.groupAssetExportAPI.detail).mock.calls.at(-1)?.[1]).toEqual(pinned);
    expect(document.querySelector("input,pre")).toBeNull(); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toContain("changed-observation");
  });
  it("closing cancels a late explicit read and only another click can read again", async () => {
    let finish!: (value: typeof pending) => void;
    vi.mocked(api.groupAssetExportAPI.detailByTask).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    view(); fireEvent.click(screen.getByRole("button", { name: "核对原组导出取消" }));
    await waitFor(() => expect(api.groupAssetExportAPI.detailByTask).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole("button", { name: "关闭原组导出取消" }));
    expect(vi.mocked(api.groupAssetExportAPI.detailByTask).mock.calls[0][3].aborted).toBe(true);
    await act(async () => finish(pending)); expect(document.querySelector("input,pre")).toBeNull();
    await open(); expect(screen.getByRole("button", { name: "取消原组导出任务" })).toBeEnabled();
  });
  it.each(["role", "priv", "username", "hasMfa", "needChangePwd", "isExpired"] as const)("remounts on actual Profile %s rotation", async (key) => {
    const rendered = view(); await open(); proof();
    const value = typeof exportProfile[key] === "boolean" ? !exportProfile[key] : typeof exportProfile[key] === "number" ? 1 : "changed";
    rendered.rerender(<GroupAssetExportTaskCancel profile={{ ...exportProfile, [key]: value }} task={exportTask} sessionChanged={changed} />);
    expect(document.querySelector("input,pre")).toBeNull(); expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("a visibility-version change clears proof without doing a business read", async () => {
    const rendered = view(); await open(); proof();
    const calls = vi.mocked(api.groupAssetExportAPI.detail).mock.calls.length;
    rendered.rerender(<GroupAssetExportTaskCancel profile={exportProfile} task={{ ...exportTask, visibilityVersion: 1 }} sessionChanged={changed} />);
    expect(document.querySelector("input,pre")).toBeNull(); expect(api.groupAssetExportAPI.detail).toHaveBeenCalledTimes(calls);
    fireEvent.click(screen.getByRole("button", { name: "读取原组导出当前状态" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "取消原组导出任务" })).toBeEnabled());
  });
  it("never calls creation, receipt, download or generic mutation while reading and cancelling", async () => {
    const create = vi.spyOn(api.groupAssetExportAPI, "create"), receipt = vi.spyOn(api.groupAssetExportAPI, "receipt"), download = vi.spyOn(api.groupAssetExportAPI, "download");
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "取消原组导出任务" }));
    await waitFor(() => expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce());
    expect(create).not.toHaveBeenCalled(); expect(receipt).not.toHaveBeenCalled(); expect(download).not.toHaveBeenCalled();
    expect(vi.mocked(fetch).mock.calls.every(([url]) => String(url).startsWith("/api/access/"))).toBe(true);
  });
  it("a same-task marker event discards the stale form without issuing an automatic read or cancel", async () => {
    view(); await open(); proof(); const key = cancelStorageKey(exportProfile.ID, exportTask.taskUUID);
    const calls = vi.mocked(api.groupAssetExportAPI.detail).mock.calls.length;
    fireEvent(window, new StorageEvent("storage", { storageArea: localStorage, key }));
    expect(document.querySelector("input,pre")).toBeNull(); expect(api.groupAssetExportAPI.detail).toHaveBeenCalledTimes(calls);
    expect(api.groupAssetExportAPI.cancel).not.toHaveBeenCalled();
  });
  it("an unrelated task marker event preserves the current proof", async () => {
    view(); await open(); proof();
    fireEvent(window, new StorageEvent("storage", { storageArea: localStorage, key: cancelStorageKey(1, "00000000-0000-0000-0000-000000000002") }));
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("123456");
  });
});

it.each(["queued", "running", "retry_wait"] as const)("allows one explicitly verified original %s cancellation", async (state) => {
  const current = { ...pending, task: { ...exportTask, state } };
  vi.mocked(api.groupAssetExportAPI.detailByTask).mockResolvedValue(current);
  vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(current);
  vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValue(current);
  view(exportProfile, current.task); await open(); proof();
  fireEvent.click(screen.getByRole("button", { name: "取消原组导出任务" }));
  await waitFor(() => expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce());
  expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
});
it("accepts the existing non-UUID group export ID vocabulary without inventing a stricter task schema", async () => {
  const task = { ...exportTask, taskUUID: "Group_Export.01" }, current = { ...pending, task, taskID: task.taskUUID };
  vi.mocked(api.groupAssetExportAPI.detailByTask).mockResolvedValue(current);
  vi.mocked(api.groupAssetExportAPI.detail).mockResolvedValue(current);
  vi.mocked(api.groupAssetExportAPI.recheckCancelAuthority).mockResolvedValue(current);
  vi.mocked(api.groupAssetExportAPI.cancel).mockResolvedValue({ ...task, state: "cancel_requested" });
  view(exportProfile, task); await open(); expect(screen.getByText(task.taskUUID)).toBeVisible();
  proof(); fireEvent.click(screen.getByRole("button", { name: "取消原组导出任务" }));
  await waitFor(() => expect(api.groupAssetExportAPI.cancel).toHaveBeenCalledOnce());
  expect(vi.mocked(api.groupAssetExportAPI.cancel).mock.calls[0][0]).toBe(task.taskUUID);
});
