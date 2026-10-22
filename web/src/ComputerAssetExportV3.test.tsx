// Synthetic DOM contracts, not real API/Worker/LDAP/browser evidence.
import { webcrypto } from "node:crypto";
import * as computerAPI from "./computer-asset-export-v3-api";
import ComputerAssetExportTaskCancel from "./ComputerAssetExportTaskCancel";
import { cancelMarkerBytes, cancelStorageKey } from "./computer-export-cancel-intent";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ComputerAssetExportV3 from "./ComputerAssetExportV3";
import { ApiError } from "./api";
import { computerAssetExportAPI, exportColumns, exportKind } from "./computer-asset-export-v3-api";
import { clearExportRecovery, exportRecoveryStorage, readExportRecovery, writeExportRecovery } from "./computer-asset-export-v3-recovery";
import { exportJSON, exportProfile, exportReceipt, exportRequest, exportTask, readyExport } from "./computer-asset-export-v3-fixtures";
const query = { ...exportRequest, creatTm: undefined, pageIdx: 2, pageSize: 10 };
const pending = { ...exportReceipt, downloadReady: false, downloadStatus: "pending" as const };
const grants = (write = true) => ["domains", "directory_assets", "tasks", "computer_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: write } }));
function gate(write = true) {
  vi.stubGlobal("fetch", vi.fn(async (path: string, options?: RequestInit) => {
    if (path === "/api/access/menu") return exportJSON({ menu: grants(write) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(options?.body)).paths.map(() => true) });
    throw new Error(`Unexpected URL ${path}`);
  }));
}
function view(props = {}) {
  return render(<ComputerAssetExportV3 profile={exportProfile} query={query} disabled={false} failure={vi.fn()} {...props} />);
}
async function open() {
  fireEvent.click(screen.getByRole("button", { name: "导出匹配计算机 XLSX" }));
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
  vi.stubGlobal("navigator", Object.create(navigator)); vi.stubGlobal("crypto", webcrypto);
  Object.defineProperty(navigator, "locks", { configurable: true, value: { request: vi.fn(async (_name, _options, callback) => callback({ name: _name })) } });
  localStorage.clear(); clearExportRecovery(); gate();
  vi.spyOn(computerAssetExportAPI, "recheckCancelAuthority").mockResolvedValue(pending);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); clearExportRecovery(); localStorage.clear(); });
describe("computer export DOM", () => {
  it("submits all matching rows, four pins and ordered columns once, clears proof", async () => {
    const held = deferred<typeof exportReceipt>();
    const create = vi.spyOn(computerAssetExportAPI, "create").mockReturnValue(held.promise);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open();
    expect(screen.getByText(/全部匹配计算机，不限当前页/)).toBeVisible();
    fireEvent.click(screen.getByLabelText("导出列：guid")); fireEvent.click(screen.getByLabelText("导出列：guid"));
    proof(); const button = screen.getByRole("button", { name: "创建计算机导出" });
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
    const create = vi.spyOn(computerAssetExportAPI, "create").mockRejectedValue(new ApiError("network"));
    const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockRejectedValue(new ApiError("not_found", 404));
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
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
    const create = vi.spyOn(computerAssetExportAPI, "create").mockReturnValue(held.promise);
    const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    fireEvent.click(screen.getByRole("button", { name: "关闭导出面板" }));
    await act(async () => held.resolve(exportReceipt));
    expect(screen.queryByRole("button", { name: "下载计算机 XLSX" })).not.toBeInTheDocument();
    await open(); expect(receipt).toHaveBeenCalledTimes(1); expect(create).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "下载计算机 XLSX" })).toBeEnabled();
  });
  it("same actor relogin rehydrates only via receipt; wrong actor clears locator", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "original-key" });
    const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    const create = vi.spyOn(computerAssetExportAPI, "create");
    const rendered = view({ profile: { ...exportProfile, csrfToken: "new-session" } }); await open();
    expect(receipt).toHaveBeenCalledWith(query.domainId, "original-key", 1, expect.any(AbortSignal));
    expect(create).not.toHaveBeenCalled();
    rendered.rerender(<ComputerAssetExportV3 profile={{ ...exportProfile, ID: 2 }} query={query} disabled={false} failure={vi.fn()} />);
    expect(screen.queryByText(exportTask.taskUUID)).not.toBeInTheDocument();
    expect(sessionStorage.getItem(exportRecoveryStorage)).toBeNull();
  });
  it("refresh/source change invalidates old response before restoring explicitly", async () => {
    const held = deferred<typeof exportReceipt>();
    vi.spyOn(computerAssetExportAPI, "create").mockReturnValue(held.promise);
    const detail = vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    const rendered = view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    rendered.rerender(<ComputerAssetExportV3 profile={exportProfile} query={{ ...query, observationId: "new-observation" }} disabled={false} failure={vi.fn()} />);
    await act(async () => held.resolve(exportReceipt));
    expect(detail).not.toHaveBeenCalled(); expect(screen.queryByRole("button", { name: "下载计算机 XLSX" })).not.toBeInTheDocument();
  });
  it("read-only export access never exposes proof, create, or cancellation action", async () => {
    gate(false); view(); await open();
    expect(screen.getByRole("button", { name: "创建计算机导出" })).toBeDisabled();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  });
  it("shows cancel_requested as waiting, never as confirmed cancellation", async () => {
    vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValueOnce(pending).mockResolvedValue({ ...pending, task: { ...exportTask, state: "cancel_requested" } });
    vi.spyOn(computerAssetExportAPI, "cancel").mockResolvedValue({ ...exportTask, state: "cancel_requested" });
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    await screen.findByRole("button", { name: "取消计算机导出任务" }); proof();
    fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" }));
    await screen.findByText(/已请求取消，待确认/);
    expect(screen.queryByText("服务器已确认取消。")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "取消计算机导出任务" })).toBeDisabled();
  });
  it("revoked download never creates an object URL and clears private task data", async () => {
    vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    vi.spyOn(computerAssetExportAPI, "download").mockRejectedValue(new ApiError("forbidden", 403));
    const url = vi.fn();
    class MockURL extends URL { static createObjectURL = url; static revokeObjectURL = vi.fn(); }
    vi.stubGlobal("URL", MockURL);
    const failure = vi.fn(); view({ failure }); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    fireEvent.click(await screen.findByRole("button", { name: "下载计算机 XLSX" }));
    await waitFor(() => expect(failure).toHaveBeenCalled()); expect(url).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "下载计算机 XLSX" })).not.toBeInTheDocument();
  });
  it("restored 404 never exposes submit without original request", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "unknown-key" });
    vi.spyOn(computerAssetExportAPI, "receipt").mockRejectedValue(new ApiError("not_found", 404));
    const create = vi.spyOn(computerAssetExportAPI, "create"); view(); await open();
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
    vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    vi.spyOn(computerAssetExportAPI, "download").mockResolvedValue(new Blob(["verified"]));
    const make = vi.fn().mockReturnValue("blob:synthetic"), revoke = vi.fn();
    class MockURL extends URL { static createObjectURL = make; static revokeObjectURL = revoke; }
    vi.stubGlobal("URL", MockURL); const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    fireEvent.click(await screen.findByRole("button", { name: "下载计算机 XLSX" }));
    await screen.findByText("完整文件已验证，并交由浏览器下载。");
    expect(make).toHaveBeenCalledTimes(1); expect(click).toHaveBeenCalledTimes(1); expect(revoke).toHaveBeenCalledWith("blob:synthetic");
    expect(document.querySelector('a[href="blob:synthetic"]')).toBeNull();
  });
  it("closed old download cannot publish a Blob into a newer task", async () => {
    vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    const held = deferred<Blob>(); vi.spyOn(computerAssetExportAPI, "download").mockReturnValue(held.promise);
    const make = vi.fn(); class MockURL extends URL { static createObjectURL = make; static revokeObjectURL = vi.fn(); }
    vi.stubGlobal("URL", MockURL); view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    fireEvent.click(await screen.findByRole("button", { name: "下载计算机 XLSX" }));
    fireEvent.click(screen.getByRole("button", { name: "关闭导出面板" })); await open();
    fireEvent.click(screen.getByRole("button", { name: "按当前列表创建另一导出" }));
    await act(async () => held.resolve(new Blob(["old"])));
    expect(make).not.toHaveBeenCalled(); expect(screen.getByRole("button", { name: "创建计算机导出" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "下载计算机 XLSX" })).not.toBeInTheDocument();
  });
  it("restores old filters explicitly without changing the selected list source", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "original-key" });
    vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue({ ...readyExport, downloadReady: false, downloadStatus: "source_changed", artifact: undefined });
    view({ query: { ...query, observationId: "new-source", search: "new-filter" } }); await open();
    expect(screen.getByText(/正在恢复原操作/)).toBeVisible();
    expect(screen.getByText(/来源版本已变化/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "下载计算机 XLSX" })).not.toBeInTheDocument();
  });
});

describe("explicit recovery abandonment", () => {
  it("keeps unknown locator until explicit confirmation, never automatically creates or cancels", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "unknown-key" });
    vi.spyOn(computerAssetExportAPI, "receipt").mockRejectedValue(new ApiError("not_found", 404));
    const create = vi.spyOn(computerAssetExportAPI, "create"), cancel = vi.spyOn(computerAssetExportAPI, "cancel");
    view(); await open(); fireEvent.click(screen.getByRole("button", { name: "放弃原回执恢复" }));
    expect(screen.getByText(/另建导出可能重复/)).toBeVisible(); expect(readExportRecovery(1)?.idempotencyKey).toBe("unknown-key");
    fireEvent.click(screen.getByRole("button", { name: "保留原回执恢复" }));
    expect(readExportRecovery(1)?.idempotencyKey).toBe("unknown-key");
    fireEvent.click(screen.getByRole("button", { name: "放弃原回执恢复" })); fireEvent.click(screen.getByRole("button", { name: "确认放弃恢复记录" }));
    expect(readExportRecovery(1)).toBeNull(); expect(create).not.toHaveBeenCalled(); expect(cancel).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "创建计算机导出" })).toBeEnabled();
  });
  it("shows immutable original column order after receipt recovery", async () => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "old-key" });
    const original = { ...exportReceipt, request: { ...exportRequest, screenColumns: ["sid", "guid"] as (typeof exportColumns)[number][] } };
    vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(original);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue({ ...readyExport, request: original.request });
    view(); await open(); expect(screen.getByLabelText("固定导出列顺序")).toHaveTextContent("sid → guid");
  });
  it("automatically retries only receipt after network failure, not create", async () => {
    const create = vi.spyOn(computerAssetExportAPI, "create").mockRejectedValue(new ApiError("network"));
    const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open(); proof();
    vi.useFakeTimers();
    try {
      await act(async () => fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" })));
      await act(async () => vi.advanceTimersByTimeAsync(2000));
      expect(create).toHaveBeenCalledTimes(1); expect(receipt).toHaveBeenCalledTimes(1);
      expect(screen.getByRole("button", { name: "下载计算机 XLSX" })).toBeEnabled();
    } finally { vi.useRealTimers(); }
  });
});

it("retries a restored locator receipt after stream/network failure without any create", async () => {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "saved-original-key" });
  const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockRejectedValueOnce(new ApiError("network")).mockResolvedValue(exportReceipt);
  vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
  const create = vi.spyOn(computerAssetExportAPI, "create"); view();
  vi.useFakeTimers();
  try {
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "导出匹配计算机 XLSX" })));
    expect(receipt).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(receipt).toHaveBeenCalledTimes(2); expect(create).not.toHaveBeenCalled();
    expect(receipt.mock.calls[0][1]).toBe("saved-original-key"); expect(receipt.mock.calls[1][1]).toBe("saved-original-key");
    expect(screen.getByRole("button", { name: "下载计算机 XLSX" })).toBeEnabled();
  } finally { vi.useRealTimers(); }
});


describe("independent computer export authority and storage", () => {
  it("does not inherit user, group or audit export permission", async () => {
    vi.stubGlobal("fetch", vi.fn(async (path: string) => {
      if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "user_asset_exports", "group_asset_exports", "audit_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
      return exportJSON({ results: [true, true, true, true, true] });
    }));
    const create = vi.spyOn(computerAssetExportAPI, "create");
    view(); await open();
    expect(screen.getByRole("region", { name: "导出访问已失效" })).toBeVisible();
    expect(create).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  });
  it.each(["user", "group"])("does not read or remove %s recovery and rejects its kind in computer storage", (kind) => {
    const userKey = `adtr.${kind}-assets-v3.export-recovery`;
    const raw = JSON.stringify({ version: 1, kind: `${kind}_assets.v3.export`, actorID: 1, domainId: query.domainId, idempotencyKey: "foreign-key" });
    sessionStorage.setItem(userKey, raw);
    expect(readExportRecovery(1)).toBeNull();
    sessionStorage.setItem(exportRecoveryStorage, raw);
    expect(readExportRecovery(1)).toBeNull();
    expect(sessionStorage.getItem(userKey)).toBe(raw);
    sessionStorage.removeItem(userKey);
  });
  it("blocks creation when a draft or list operation disables the panel", async () => {
    const create = vi.spyOn(computerAssetExportAPI, "create");
    const rendered = view(); await open();
    rendered.rerender(<ComputerAssetExportV3 profile={exportProfile} query={query} disabled={true} failure={vi.fn()} />);
    expect(screen.getByRole("button", { name: "创建计算机导出" })).toBeDisabled();
    fireEvent.submit(screen.getByRole("button", { name: "创建计算机导出" }).closest("form")!);
    expect(create).not.toHaveBeenCalled();
  });
});


it("recovers across a different selected domain without repinning or overwriting the original locator", async () => {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "original-key" });
  const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
  const create = vi.spyOn(computerAssetExportAPI, "create");
  view({ query: { ...query, domainId: "another-domain", observationId: undefined }, disabled: true });
  fireEvent.click(screen.getByRole("button", { name: "恢复计算机导出回执" }));
  await screen.findByRole("button", { name: "下载计算机 XLSX" });
  expect(receipt).toHaveBeenCalledWith(query.domainId, "original-key", 1, expect.any(AbortSignal));
  expect(readExportRecovery(1)?.domainId).toBe(query.domainId);
  expect(create).not.toHaveBeenCalled();
});


describe("computer date and default-column intent", () => {
  it("uses the five defaults when all columns are deselected", async () => {
    const create = vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open();
    for (const column of exportColumns) fireEvent.click(screen.getByLabelText(`导出列：${column}`));
    expect(screen.getByLabelText("导出列顺序")).toHaveTextContent(exportColumns.join(" → "));
    proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0][0].screenColumns).toEqual(exportColumns);
  });
  it("keeps original date bounds on receipt recovery despite a changed current range", async () => {
    const dates = { startTm: "2026-01-01T08:00:00+08:00", endTm: "2026-01-02T08:00:00.000000001+08:00" };
    const original = { ...exportReceipt, request: { ...exportRequest, ...dates } };
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: query.domainId, idempotencyKey: "dated-key" });
    const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(original);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue({ ...readyExport, request: original.request });
    const create = vi.spyOn(computerAssetExportAPI, "create");
    view({ query: { ...query, startTm: "2026-02-01T00:00:00Z", endTm: "2026-03-01T00:00:00Z" } }); await open();
    expect(screen.getByText(`${dates.startTm} → ${dates.endTm}`)).toBeVisible();
    expect(screen.getByText(/正在恢复原操作/)).toBeVisible();
    expect(receipt).toHaveBeenCalledWith(query.domainId, "dated-key", 1, expect.any(AbortSignal));
    expect(create).not.toHaveBeenCalled();
  });
  it("date-filter navigation drops a late create response", async () => {
    const held = deferred<typeof exportReceipt>();
    vi.spyOn(computerAssetExportAPI, "create").mockReturnValue(held.promise);
    const detail = vi.spyOn(computerAssetExportAPI, "detail");
    const rendered = view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
    rendered.rerender(<ComputerAssetExportV3 profile={exportProfile} query={{ ...query, startTm: "2026-01-01T00:00:00Z", endTm: "2026-02-01T00:00:00Z" }} disabled={false} failure={vi.fn()} />);
    await act(async () => held.resolve(exportReceipt));
    expect(detail).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "下载计算机 XLSX" })).not.toBeInTheDocument();
    expect(readExportRecovery(1)?.idempotencyKey).toBeTruthy();
  });
});

const cancelled = { ...pending, task: { ...exportTask, state: "cancelled" as const }, downloadStatus: "cancelled" as const };
const running = { ...pending, task: { ...exportTask, state: "running" as const, attempt: 1 } };
const cancelRequested = { ...pending, task: { ...exportTask, state: "cancel_requested" as const } };
async function createPending() {
  vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
  vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(pending);
  vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  const rendered = view(); await open(); proof(); fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
  await screen.findByRole("button", { name: "取消计算机导出任务" });
  await waitFor(() => expect(screen.getByRole("button", { name: "取消计算机导出任务" })).toBeEnabled());
  return rendered;
}
function pendingLocator() {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: exportProfile.ID, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
}

describe("shared computer cancellation and legacy read compatibility", () => {
  it("retains the mounted MFA fieldset through an ordinary same-task queued-to-running receipt/detail read", async () => {
    await createPending(); proof();
    const password = screen.getByLabelText("操作者当前密码"), code = screen.getByLabelText("未使用的认证器验证码"), fieldset = password.closest("fieldset");
    vi.mocked(computerAssetExportAPI.detail).mockResolvedValue(running);
    vi.mocked(computerAssetExportAPI.recheckCancelAuthority).mockResolvedValue(running);
    vi.spyOn(computerAssetExportAPI, "cancel").mockImplementation(async () => {
      vi.mocked(computerAssetExportAPI.detail).mockResolvedValue(cancelRequested); return cancelRequested.task;
    });
    fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "取消计算机导出任务" })).toBeEnabled());
    expect(screen.getByLabelText("操作者当前密码")).toBe(password);
    expect(screen.getByLabelText("未使用的认证器验证码")).toBe(code);
    expect(password.closest("fieldset")).toBe(fieldset); expect(password).toHaveValue("SyntheticPassword123!"); expect(code).toHaveValue("123456");
    fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" }));
    await waitFor(() => expect(computerAssetExportAPI.cancel).toHaveBeenCalledOnce());
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    await screen.findByText(/已请求取消，待确认/);
    expect(screen.getByRole("button", { name: "取消计算机导出任务" })).toBeDisabled();
  });
  it("keeps the old exact cancel button natively disabled after cancel_requested and manual receipt/detail recovery", async () => {
    await createPending();
    const cancel = vi.spyOn(computerAssetExportAPI, "cancel").mockImplementation(async () => {
      vi.mocked(computerAssetExportAPI.detail).mockResolvedValue(cancelRequested); return cancelRequested.task;
    });
    proof(); fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" }));
    await screen.findByText(/已请求取消，待确认/);
    const button = screen.getByRole("button", { name: "取消计算机导出任务" });
    expect(button).toBeDisabled(); expect(button).toHaveAttribute("disabled"); expect(button.closest("form")).toBeNull();
    expect(screen.queryByLabelText("未使用的认证器验证码")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "核对原导出回执" })).toBeEnabled());
    expect(screen.getByRole("button", { name: "取消计算机导出任务" })).toHaveAttribute("disabled");
    expect(screen.queryByLabelText("未使用的认证器验证码")).toBeNull(); expect(cancel).toHaveBeenCalledOnce();
  });
  it.each(["focus", "storage", "visibility"])("clears old prepared MFA on %s, performs no automatic read, and requires explicit recovery", async (event) => {
    await createPending(); proof(); const detailCalls = vi.mocked(computerAssetExportAPI.detail).mock.calls.length;
    const cancel = vi.spyOn(computerAssetExportAPI, "cancel");
    if (event === "focus") fireEvent(window, new Event("focus"));
    if (event === "visibility") fireEvent(document, new Event("visibilitychange"));
    if (event === "storage") fireEvent(window, new StorageEvent("storage", { storageArea: localStorage, key: cancelStorageKey(exportProfile.ID, exportTask.taskUUID) }));
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull(); expect(screen.queryByLabelText("固定导出来源")).toBeNull();
    expect(computerAssetExportAPI.detail).toHaveBeenCalledTimes(detailCalls); expect(cancel).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
    await screen.findByLabelText("操作者当前密码");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue(""); expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue(""); expect(cancel).not.toHaveBeenCalled();
  });
  it("session rotation discards prepared old-panel MFA instead of reusing it", async () => {
    const rendered = await createPending(); proof(); const cancel = vi.spyOn(computerAssetExportAPI, "cancel");
    rendered.rerender(<ComputerAssetExportV3 profile={{ ...exportProfile, role: "rotated-role" }} query={query} disabled={false} failure={vi.fn()} />);
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull(); expect(cancel).not.toHaveBeenCalled();
  });
  it.each(["supported", "missing-lock", "storage-read-failure"])("preserves ordinary 1.5s pending-to-terminal GET polling with %s capability", async (mode) => {
    if (mode === "missing-lock") Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    if (mode === "storage-read-failure") {
      const original = Storage.prototype.getItem;
      vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (this: Storage, key) {
        if (this === localStorage) throw new Error("storage-unavailable-canary"); return original.call(this, key);
      });
    }
    vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
    const detail = vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValueOnce(pending).mockResolvedValue(readyExport);
    view(); await open(); proof(); vi.useFakeTimers();
    try {
      await act(async () => fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" })));
      expect(detail).toHaveBeenCalledOnce();
      if (mode !== "supported") expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
      await act(async () => vi.advanceTimersByTimeAsync(1500));
      expect(detail).toHaveBeenCalledTimes(2); expect(screen.getByRole("button", { name: "下载计算机 XLSX" })).toBeEnabled();
      expect(computerAssetExportAPI.create).toHaveBeenCalledOnce();
    } finally { vi.useRealTimers(); }
  });
  it("missing locking still allows read-only recovery after a creation network failure", async () => {
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    const create = vi.spyOn(computerAssetExportAPI, "create").mockRejectedValue(new ApiError("network"));
    const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
    view(); await open(); proof(); vi.useFakeTimers();
    try {
      await act(async () => fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" })));
      await act(async () => vi.advanceTimersByTimeAsync(2000));
      expect(receipt).toHaveBeenCalledOnce(); expect(create).toHaveBeenCalledOnce(); expect(screen.getByRole("button", { name: "下载计算机 XLSX" })).toBeEnabled();
    } finally { vi.useRealTimers(); }
  });
  it("unknown legacy cancellation blocks the original-task entry without changing the create locator", async () => {
    const old = await createPending(), locator = sessionStorage.getItem(exportRecoveryStorage);
    const cancel = vi.spyOn(computerAssetExportAPI, "cancel").mockRejectedValue(new ApiError("network"));
    proof(); fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" })); await screen.findByText(/可能已发出取消请求/); old.unmount();
    vi.spyOn(computerAssetExportAPI, "detailByTask").mockResolvedValue(pending);
    render(<ComputerAssetExportTaskCancel profile={exportProfile} task={exportTask} sessionChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "核对原计算机导出取消" })); await screen.findByText(/可能已发出取消请求/);
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull(); expect(cancel).toHaveBeenCalledOnce();
    expect(sessionStorage.getItem(exportRecoveryStorage)).toBe(locator);
  });
  it("unknown original-task cancellation blocks old receipt recovery without changing its locator", async () => {
    pendingLocator(); const locator = sessionStorage.getItem(exportRecoveryStorage);
    vi.spyOn(computerAssetExportAPI, "detailByTask").mockResolvedValue(pending); vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(pending);
    vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    const cancel = vi.spyOn(computerAssetExportAPI, "cancel").mockRejectedValue(new ApiError("network"));
    const fresh = render(<ComputerAssetExportTaskCancel profile={exportProfile} task={exportTask} sessionChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "核对原计算机导出取消" })); await screen.findByLabelText("操作者当前密码");
    proof(); fireEvent.click(screen.getByRole("button", { name: "取消原计算机导出任务" })); await screen.findByText(/可能已发出取消请求/); fresh.unmount();
    view(); await open(); expect(screen.queryByLabelText("操作者当前密码")).toBeNull(); expect(cancel).toHaveBeenCalledOnce();
    expect(sessionStorage.getItem(exportRecoveryStorage)).toBe(locator);
  });
  it("supported old cancellation uses exact proof rejection reauthorization and waits for a fresh explicit proof", async () => {
    await createPending(); vi.spyOn(computerAPI, "isComputerExportCancelProofRejected").mockReturnValue(true);
    const cancel = vi.spyOn(computerAssetExportAPI, "cancel").mockRejectedValue(new ApiError("invalid_credentials", 401));
    proof(); fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" })); await screen.findByText("密码或验证码未通过；请重新输入。");
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue(""); expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    expect(cancel).toHaveBeenCalledOnce(); expect(computerAssetExportAPI.recheckCancelAuthority).toHaveBeenCalledTimes(2); expect(localStorage.length).toBe(0);
  });
  it("explicit original receipt recovery clears only a verified terminal marker", async () => {
    pendingLocator();
    const binding = { profile: exportProfile, taskID: exportTask.taskUUID, request: exportRequest, signal: new AbortController().signal, assertCurrent: () => undefined };
    const key = cancelStorageKey(exportProfile.ID, exportTask.taskUUID); localStorage.setItem(key, await cancelMarkerBytes(binding));
    vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt); vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(cancelled);
    view(); await open(); expect(localStorage.getItem(key)).toBeNull(); expect(screen.getByText(/服务器已确认取消，没有可下载文件/)).toBeVisible();
  });
  it("abandoning the create locator cannot erase an unknown cancellation marker", async () => {
    await createPending(); vi.spyOn(computerAssetExportAPI, "cancel").mockRejectedValue(new ApiError("network"));
    proof(); fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" })); await screen.findByText(/可能已发出取消请求/);
    const key = cancelStorageKey(exportProfile.ID, exportTask.taskUUID), raw = localStorage.getItem(key);
    fireEvent.click(screen.getByRole("button", { name: "放弃原回执恢复" })); fireEvent.click(screen.getByRole("button", { name: "确认放弃恢复记录" }));
    expect(sessionStorage.getItem(exportRecoveryStorage)).toBeNull(); expect(localStorage.getItem(key)).toBe(raw); expect(raw).not.toBeNull();
  });
});

it("legacy close aborts a pending cancel response and reopen remains read-only under its marker", async () => {
  await createPending(); const held = deferred<typeof exportTask>();
  const cancel = vi.spyOn(computerAssetExportAPI, "cancel").mockReturnValue(held.promise);
  proof(); fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" }));
  await waitFor(() => expect(cancel).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "关闭导出面板" }));
  expect(cancel.mock.calls[0][5].aborted).toBe(true);
  await act(async () => held.resolve(cancelled.task));
  expect(localStorage.length).toBe(1); await open();
  expect(screen.queryByLabelText("操作者当前密码")).toBeNull(); expect(cancel).toHaveBeenCalledOnce();
  expect(screen.getByRole("button", { name: "核对原导出回执" })).toBeEnabled();
});

it("legacy known-task 404 clears private proof and capabilities, retaining its cancellation marker", async () => {
  await createPending();
  const key = cancelStorageKey(exportProfile.ID, exportTask.taskUUID);
  localStorage.setItem(key, await cancelMarkerBytes({ profile: exportProfile, taskID: exportTask.taskUUID,
    request: exportRequest, signal: new AbortController().signal, assertCurrent: () => undefined }));
  vi.mocked(computerAssetExportAPI.receipt).mockRejectedValue(new ApiError("not_found", 404));
  proof(); fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
  await screen.findByRole("region", { name: "导出访问已失效" });
  expect(screen.queryByLabelText("操作者当前密码")).toBeNull(); expect(screen.queryByLabelText("固定导出来源")).toBeNull(); expect(localStorage.getItem(key)).not.toBeNull();
});
