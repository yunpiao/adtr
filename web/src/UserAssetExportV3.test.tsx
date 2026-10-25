// Synthetic DOM contracts, not real API/Worker/LDAP/browser evidence.
import { webcrypto } from "node:crypto";
import { cancelMarkerBytes, cancelStorageKey } from "./user-export-cancel-intent";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import UserAssetExportV3 from "./UserAssetExportV3";
import { ApiError } from "./api";
import { userAssetExportAPI, exportColumns, exportKind, type ExportDetail } from "./user-asset-export-v3-api";
import { clearExportRecovery, exportRecoveryStorage, readExportRecovery, writeExportRecovery } from "./user-asset-export-v3-recovery";
import { exportJSON, exportProfile, exportReceipt, exportRequest, exportTask, readyExport } from "./user-asset-export-v3-fixtures";
const query = { ...exportRequest, creatTm: undefined, pageIdx: 2, pageSize: 10 };
const pending = { ...exportReceipt, downloadReady: false, downloadStatus: "pending" as const };
const grants = (write = true) => ["domains", "directory_assets", "tasks", "user_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: write } }));
function gate(write = true) {
  vi.stubGlobal("fetch", vi.fn(async (path: string) => {
    if (path === "/api/access/menu") return exportJSON({ menu: grants(write) });
    if (path === "/api/access/check") return exportJSON({ results: [true, true, true, true, true] });
    throw new Error(`Unexpected URL ${path}`);
  }));
}
function view(props = {}) {
  return render(<UserAssetExportV3 profile={exportProfile} query={query} disabled={false} failure={vi.fn()} {...props} />);
}
async function open() {
  fireEvent.click(screen.getByRole("button", { name: "导出匹配用户 XLSX" }));
  await waitFor(() => expect(screen.queryByText("正在核对导出…")).not.toBeInTheDocument());
}
function proof() {
  fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
  fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => { resolve = r; }); return { promise, resolve };
}
beforeEach(() => {
  vi.stubGlobal("navigator", Object.create(navigator));
  clearExportRecovery(); localStorage.clear(); gate(); vi.stubGlobal("crypto", webcrypto);
  Object.defineProperty(navigator, "locks", { configurable: true, value: { request: vi.fn(async (_name, _options, callback) => callback({ name: _name })) } });
  vi.spyOn(userAssetExportAPI, "recheckCancelAuthority").mockResolvedValue(pending);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); clearExportRecovery(); localStorage.clear(); });
describe("user export DOM", () => {
  it("submits all matching rows, four pins and ordered columns once, clears proof", async () => {
    const held = deferred<typeof exportReceipt>();
    const create = vi.spyOn(userAssetExportAPI, "create").mockReturnValue(held.promise);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open();
    expect(screen.getByText(/全部匹配用户，不限当前页/)).toBeVisible();
    fireEvent.click(screen.getByLabelText("导出列：guid")); fireEvent.click(screen.getByLabelText("导出列：guid"));
    proof(); const button = screen.getByRole("button", { name: "创建用户导出" });
    fireEvent.click(button); fireEvent.click(button);
    expect(create).toHaveBeenCalledTimes(1);
    expect(create.mock.calls[0][0]).toEqual({ ...exportRequest, screenColumns: [...exportColumns.slice(1), "guid"] });
    expect(create.mock.calls[0][0]).not.toHaveProperty("pageIdx");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    const stored = JSON.parse(sessionStorage.getItem(exportRecoveryStorage)!);
    expect(Object.keys(stored).sort()).toEqual(["actorID", "domainId", "idempotencyKey", "kind", "version"]);
    expect(JSON.stringify(stored)).not.toMatch(/SyntheticPassword|123456|csrf|screenColumns|search/);
    await act(async () => held.resolve({ ...exportReceipt, request: create.mock.calls[0][0] }));
  });
  it("keeps original key after ambiguous failure and receipt 404, never auto-resubmits", async () => {
    const create = vi.spyOn(userAssetExportAPI, "create").mockRejectedValue(new ApiError("network"));
    const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockRejectedValue(new ApiError("not_found", 404));
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    await screen.findByText(/网络中断/); const key = create.mock.calls[0][1];
    fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
    await screen.findByText(/不能证明原提交未生效/);
    expect(receipt.mock.calls[0][1]).toBe(key); expect(create).toHaveBeenCalledTimes(1);
    proof(); fireEvent.click(screen.getByRole("button", { name: "使用原请求和幂等键重试" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
    expect(create.mock.calls[1][0]).toEqual(create.mock.calls[0][0]); expect(create.mock.calls[1][1]).toBe(key);
  });
  it("close aborts late submission; reopen only reads the original receipt", async () => {
    const held = deferred<typeof exportReceipt>();
    const create = vi.spyOn(userAssetExportAPI, "create").mockReturnValue(held.promise);
    const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    fireEvent.click(screen.getByRole("button", { name: "关闭导出面板" }));
    await act(async () => held.resolve(exportReceipt));
    expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
    await open(); expect(receipt).toHaveBeenCalledTimes(1); expect(create).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "下载用户 XLSX" })).toBeEnabled();
  });
  it("same actor relogin rehydrates only via receipt; wrong actor clears locator", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "original-key" });
    const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    const create = vi.spyOn(userAssetExportAPI, "create");
    const rendered = view({ profile: { ...exportProfile, csrfToken: "new-session" } }); await open();
    expect(receipt).toHaveBeenCalledWith(query.domainId, "original-key", 1, expect.any(AbortSignal));
    expect(create).not.toHaveBeenCalled();
    rendered.rerender(<UserAssetExportV3 profile={{ ...exportProfile, ID: 2 }} query={query} disabled={false} failure={vi.fn()} />);
    expect(screen.queryByText(exportTask.taskUUID)).not.toBeInTheDocument();
    expect(sessionStorage.getItem(exportRecoveryStorage)).toBeNull();
  });
  it("refresh/source change invalidates old response before restoring explicitly", async () => {
    const held = deferred<typeof exportReceipt>();
    vi.spyOn(userAssetExportAPI, "create").mockReturnValue(held.promise);
    const detail = vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    const rendered = view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    rendered.rerender(<UserAssetExportV3 profile={exportProfile} query={{ ...query, observationId: "new-observation" }} disabled={false} failure={vi.fn()} />);
    await act(async () => held.resolve(exportReceipt));
    expect(detail).not.toHaveBeenCalled(); expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
  });
  it("read-only export access never exposes proof, create, or cancellation action", async () => {
    gate(false); view(); await open();
    expect(screen.getByRole("button", { name: "创建用户导出" })).toBeDisabled();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  });
  it("shows cancel_requested as waiting, never as confirmed cancellation", async () => {
    vi.spyOn(userAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValueOnce(pending).mockResolvedValue({ ...pending, task: { ...exportTask, state: "cancel_requested" } });
    vi.spyOn(userAssetExportAPI, "cancel").mockResolvedValue({ ...exportTask, state: "cancel_requested" });
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    await screen.findByRole("button", { name: "取消用户导出任务" }); proof();
    fireEvent.click(screen.getByRole("button", { name: "取消用户导出任务" }));
    await screen.findByText(/已请求取消，待确认/);
    expect(screen.queryByText("服务器已确认取消。")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "取消用户导出任务" })).toBeDisabled();
  });
  it("revoked download never creates an object URL and clears private task data", async () => {
    vi.spyOn(userAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    vi.spyOn(userAssetExportAPI, "download").mockRejectedValue(new ApiError("forbidden", 403));
    const url = vi.fn();
    class MockURL extends URL { static createObjectURL = url; static revokeObjectURL = vi.fn(); }
    vi.stubGlobal("URL", MockURL);
    const failure = vi.fn(); view({ failure }); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    fireEvent.click(await screen.findByRole("button", { name: "下载用户 XLSX" }));
    await waitFor(() => expect(failure).toHaveBeenCalled()); expect(url).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
  });
  it("restored 404 never exposes submit without original request", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "unknown-key" });
    vi.spyOn(userAssetExportAPI, "receipt").mockRejectedValue(new ApiError("not_found", 404));
    const create = vi.spyOn(userAssetExportAPI, "create"); view(); await open();
    expect(screen.getByText(/不能证明原提交未生效/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "使用原请求和幂等键重试" })).not.toBeInTheDocument();
    expect(create).not.toHaveBeenCalled(); expect(readExportRecovery(1)?.idempotencyKey).toBe("unknown-key");
  });
  it.each(["{", "x".repeat(1025), JSON.stringify({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "key", actorPassword: "secret" })])("rejects corrupt or expanded storage", (raw) => {
    sessionStorage.setItem(exportRecoveryStorage, raw); expect(readExportRecovery(1)).toBeNull(); expect(sessionStorage.getItem(exportRecoveryStorage)).toBeNull();
  });
});

describe("download lifecycle and restored-source isolation", () => {
  it("revokes the object URL immediately after dispatching a verified download", async () => {
    vi.spyOn(userAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    vi.spyOn(userAssetExportAPI, "download").mockResolvedValue(new Blob(["verified"]));
    const make = vi.fn().mockReturnValue("blob:synthetic"), revoke = vi.fn();
    class MockURL extends URL { static createObjectURL = make; static revokeObjectURL = revoke; }
    vi.stubGlobal("URL", MockURL); const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    fireEvent.click(await screen.findByRole("button", { name: "下载用户 XLSX" }));
    await screen.findByText("完整文件已验证，并交由浏览器下载。");
    expect(make).toHaveBeenCalledTimes(1); expect(click).toHaveBeenCalledTimes(1); expect(revoke).toHaveBeenCalledWith("blob:synthetic");
    expect(document.querySelector('a[href="blob:synthetic"]')).toBeNull();
  });
  it("closed old download cannot publish a Blob into a newer task", async () => {
    vi.spyOn(userAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    const held = deferred<Blob>(); vi.spyOn(userAssetExportAPI, "download").mockReturnValue(held.promise);
    const make = vi.fn(); class MockURL extends URL { static createObjectURL = make; static revokeObjectURL = vi.fn(); }
    vi.stubGlobal("URL", MockURL); view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    fireEvent.click(await screen.findByRole("button", { name: "下载用户 XLSX" }));
    fireEvent.click(screen.getByRole("button", { name: "关闭导出面板" })); await open();
    fireEvent.click(screen.getByRole("button", { name: "按当前列表创建另一导出" }));
    await act(async () => held.resolve(new Blob(["old"])));
    expect(make).not.toHaveBeenCalled(); expect(screen.getByRole("button", { name: "创建用户导出" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
  });
  it("restores old filters explicitly without changing the selected list source", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "original-key" });
    vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue({ ...readyExport, downloadReady: false, downloadStatus: "source_changed", artifact: undefined });
    view({ query: { ...query, observationId: "new-source", search: "new-filter" } }); await open();
    expect(screen.getByText(/正在恢复原操作/)).toBeVisible();
    expect(screen.getByText(/来源版本已变化/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
  });
});

describe("explicit recovery abandonment", () => {
  it("keeps unknown locator until explicit confirmation, never automatically creates or cancels", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "unknown-key" });
    vi.spyOn(userAssetExportAPI, "receipt").mockRejectedValue(new ApiError("not_found", 404));
    const create = vi.spyOn(userAssetExportAPI, "create"), cancel = vi.spyOn(userAssetExportAPI, "cancel");
    view(); await open(); fireEvent.click(screen.getByRole("button", { name: "放弃原回执恢复" }));
    expect(screen.getByText(/另建导出可能重复/)).toBeVisible(); expect(readExportRecovery(1)?.idempotencyKey).toBe("unknown-key");
    fireEvent.click(screen.getByRole("button", { name: "保留原回执恢复" }));
    expect(readExportRecovery(1)?.idempotencyKey).toBe("unknown-key");
    fireEvent.click(screen.getByRole("button", { name: "放弃原回执恢复" })); fireEvent.click(screen.getByRole("button", { name: "确认放弃恢复记录" }));
    expect(readExportRecovery(1)).toBeNull(); expect(create).not.toHaveBeenCalled(); expect(cancel).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "创建用户导出" })).toBeEnabled();
  });
  it("shows immutable original column order after receipt recovery", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "old-key" });
    const original = { ...exportReceipt, request: { ...exportRequest, screenColumns: ["mail", "guid"] as (typeof exportColumns)[number][] } };
    vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(original);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue({ ...readyExport, request: original.request });
    view(); await open(); expect(screen.getByLabelText("固定导出列顺序")).toHaveTextContent("mail → guid");
  });
  it("automatically retries only receipt after network failure, not create", async () => {
    const create = vi.spyOn(userAssetExportAPI, "create").mockRejectedValue(new ApiError("network"));
    const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open(); proof();
    vi.useFakeTimers();
    try {
      await act(async () => fireEvent.click(screen.getByRole("button", { name: "创建用户导出" })));
      await act(async () => vi.advanceTimersByTimeAsync(2000));
      expect(create).toHaveBeenCalledTimes(1); expect(receipt).toHaveBeenCalledTimes(1);
      expect(screen.getByRole("button", { name: "下载用户 XLSX" })).toBeEnabled();
    } finally { vi.useRealTimers(); }
  });
});

it("retries a restored locator receipt after stream/network failure without any create", async () => {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "saved-original-key" });
  const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockRejectedValueOnce(new ApiError("network")).mockResolvedValue(exportReceipt);
  vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
  const create = vi.spyOn(userAssetExportAPI, "create"); view();
  vi.useFakeTimers();
  try {
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "导出匹配用户 XLSX" })));
    expect(receipt).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(receipt).toHaveBeenCalledTimes(2); expect(create).not.toHaveBeenCalled();
    expect(receipt.mock.calls[0][1]).toBe("saved-original-key"); expect(receipt.mock.calls[1][1]).toBe("saved-original-key");
    expect(screen.getByRole("button", { name: "下载用户 XLSX" })).toBeEnabled();
  } finally { vi.useRealTimers(); }
});

describe("shared old-panel cancellation barrier", () => {
  it("a marker from the task entry prevents another cancellation but preserves receipt reads", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: exportProfile.ID, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
    const locator = JSON.stringify(readExportRecovery(exportProfile.ID));
    localStorage.setItem(cancelStorageKey(exportProfile.ID, exportTask.taskUUID), await cancelMarkerBytes({ profile: exportProfile, taskID: exportTask.taskUUID, request: exportRequest, signal: new AbortController().signal, assertCurrent: () => undefined }));
    vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt); vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(pending);
    const cancel = vi.spyOn(userAssetExportAPI, "cancel"); view(); await open();
    expect(screen.queryByRole("button", { name: "取消用户导出任务" })).toBeNull();
    expect(screen.getByRole("button", { name: "核对原导出回执" })).toBeEnabled();
    expect(JSON.stringify(readExportRecovery(exportProfile.ID))).toBe(locator); expect(cancel).not.toHaveBeenCalled();
  });
  it("unknown cancellation does not run create/receipt automatic recovery or expose a second cancel form", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: exportProfile.ID, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
    const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(pending);
    const cancel = vi.spyOn(userAssetExportAPI, "cancel").mockRejectedValue(new ApiError("network"));
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "取消用户导出任务" }));
    await screen.findByText(/可能已发出取消请求/);
    fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
    await waitFor(() => expect(receipt).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("button", { name: "取消用户导出任务" })).toBeNull(); expect(cancel).toHaveBeenCalledOnce();
  });
  it("missing locks preserve normal queued-to-terminal automatic reads", async () => {
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    writeExportRecovery({ version: 1, kind: exportKind, actorID: exportProfile.ID, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
    vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    const read = vi.spyOn(userAssetExportAPI, "detail").mockResolvedValueOnce(pending).mockResolvedValue(readyExport);
    view(); await open();
    expect(screen.getByText(/浏览器缺少可靠的本地存储或独占锁/)).toBeVisible();
    await waitFor(() => expect(screen.getByRole("button", { name: "下载用户 XLSX" })).toBeEnabled(), { timeout: 3000 });
    expect(read).toHaveBeenCalledTimes(2);
  });
  it("missing locks preserve original read-only network recovery after the capability notice", async () => {
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    writeExportRecovery({ version: 1, kind: exportKind, actorID: exportProfile.ID, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
    const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValueOnce(pending).mockRejectedValueOnce(new ApiError("network")).mockResolvedValue(readyExport);
    const create = vi.spyOn(userAssetExportAPI, "create"), cancel = vi.spyOn(userAssetExportAPI, "cancel");
    view(); await open(); expect(screen.getByText(/浏览器缺少可靠的本地存储或独占锁/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "下载用户 XLSX" })).toBeEnabled(), { timeout: 4000 });
    expect(receipt).toHaveBeenCalledTimes(3); expect(create).not.toHaveBeenCalled(); expect(cancel).not.toHaveBeenCalled();
    expect(readExportRecovery(exportProfile.ID)?.idempotencyKey).toBe("original-key");
  });
  it("unsupported locks do not disable existing creation and receipt capability", async () => {
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    view(); await open(); expect(screen.getByRole("button", { name: "创建用户导出" })).toBeEnabled();
  });
});

describe("frozen UAC export summary", () => {
  it.each(["set", "clear", "not_returned"] as const)("shows and submits the original %s filter, not a later list query", async (uacDisableBit) => {
    const request = { ...exportRequest, uacDisableBit };
    const receipt = { ...exportReceipt, request }, ready = { ...readyExport, request };
    const create = vi.spyOn(userAssetExportAPI, "create").mockResolvedValue(receipt);
    const read = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(receipt);
    const detail = vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(ready);
    const rendered = view({ query: { ...query, uacDisableBit } }); await open();
    const label = uacDisableBit === "set" ? "禁用位已设置" : uacDisableBit === "clear" ? "禁用位未设置" : "UAC未返回";
    expect(screen.getByText(label)).toBeVisible();
    proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    await screen.findByRole("button", { name: "下载用户 XLSX" });
    expect(create.mock.calls[0][0]).toEqual(request);
    rendered.rerender(<UserAssetExportV3 profile={exportProfile} query={query} disabled={false} failure={vi.fn()} />);
    await open();
    await screen.findByText(/正在恢复原操作/);
    expect(read).toHaveBeenCalledWith(query.domainId, create.mock.calls[0][1], 1, expect.any(AbortSignal));
    expect(screen.getByText(label)).toBeVisible();
    expect(detail.mock.calls.every(([, expected]) => expected?.uacDisableBit === uacDisableBit)).toBe(true);
    expect(create).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "按当前列表创建另一导出" }));
    expect(screen.queryByText(label)).not.toBeInTheDocument();
    proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    expect(create).toHaveBeenCalledTimes(2);
    expect(create.mock.calls[1][0]).not.toHaveProperty("uacDisableBit");
    expect(create.mock.calls[1][1]).not.toBe(create.mock.calls[0][1]);
  });
});


describe("terminal export outcome and next action grouping", () => {
  it.each(["cancelled", "failed", "succeeded"] as const)("groups %s identity/status/restart while preserving visible progress and evidence", async (state) => {
    const terminalDetail: ExportDetail = {
      ...readyExport,
      task: { ...readyExport.task, state, error: state === "cancelled" ? "cancelled" : state === "failed" ? "export_invalid_text" : "" },
      downloadReady: state === "succeeded", downloadStatus: state === "succeeded" ? "ready" : state,
      artifact: state === "succeeded" ? readyExport.artifact : undefined,
    };
    const create = vi.spyOn(userAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(terminalDetail);
    const cancel = vi.spyOn(userAssetExportAPI, "cancel");
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    const group = await screen.findByRole("group", { name: "导出终态与后续操作" });
    const status = within(group).getByRole("status");
    expect(within(group).getByText(`任务 ID：${exportTask.taskUUID}`, { exact: true })).toBeVisible();
    const restart = within(group).getByRole("button", { name: "按当前列表创建另一导出" });
    expect(restart).toBeVisible(); expect(restart).toBeEnabled();
    expect(group).toHaveStyle({ display: "flex", flexWrap: "wrap", alignItems: "flex-end" });
    expect(status).toHaveStyle({ margin: "8px 0 0" });
    expect(status).not.toContainElement(restart);
    expect(screen.getByLabelText("用户导出持久化进度")).toBeVisible();
    expect(group).not.toContainElement(screen.getByLabelText("用户导出持久化进度"));
    if (state !== "succeeded") expect(screen.getByText(/^任务错误代码：/)).toBeVisible();
    else expect(screen.getByText(/实际导出 0 行/)).toBeVisible();
    expect(create).toHaveBeenCalledTimes(1); expect(cancel).not.toHaveBeenCalled();
  });
  it.each(["queued", "cancel_requested"] as const)("keeps %s status unchanged and does not offer a terminal restart", async (state) => {
    vi.spyOn(userAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue({ ...pending, task: { ...exportTask, state } });
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    await screen.findByText(`任务 ID：${exportTask.taskUUID}`, { exact: true });
    expect(screen.queryByRole("group", { name: "导出终态与后续操作" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "按当前列表创建另一导出" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("用户导出持久化进度")).toBeVisible();
  });
  it("keeps terminal identity/status readable without granting the restart action", async () => {
    gate(false);
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "original-key" });
    vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(readyExport);
    const create = vi.spyOn(userAssetExportAPI, "create");
    view(); await open();
    const group = await screen.findByRole("group", { name: "导出终态与后续操作" });
    expect(within(group).getByText(`任务 ID：${exportTask.taskUUID}`, { exact: true })).toBeVisible();
    expect(within(group).getByRole("status")).toHaveTextContent("完整文件已就绪");
    expect(screen.queryByRole("button", { name: "按当前列表创建另一导出" })).not.toBeInTheDocument();
    expect(create).not.toHaveBeenCalled();
  });
});
