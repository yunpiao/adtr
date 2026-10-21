// Synthetic workspace integration: source-picker control is isolated, while the
// real workspace list availability gate and export recovery UI are exercised.
import { webcrypto } from "node:crypto";
import { cancelMarkerBytes, cancelStorageKey } from "./user-export-cancel-intent";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import UserAssetsV3Workspace from "./UserAssetsV3Workspace";
import { exportProfile, exportJSON, exportRequest, exportReceipt, readyExport } from "./user-asset-export-v3-fixtures";
import { userAssetExportAPI, exportKind, type ExportDetail } from "./user-asset-export-v3-api";
import { clearExportRecovery, writeExportRecovery } from "./user-asset-export-v3-recovery";
import type { ResolvedSource } from "./domain-selection-api";
vi.mock("./SavedSourcePicker", () => ({ default: ({ onResolved }: { onResolved: (value: ResolvedSource) => void }) => <button onClick={() => onResolved({ selection: {
  domainId: "synthetic-domain", domain: "synthetic.invalid", revision: "9007199254740993", credentialRevision: "9007199254740995",
  dcHostName: "dc.synthetic.invalid", ldapAddr: "", port: "636", mode: "ldaps", credentialConfigured: true,
  connectionState: "unverified", source: "configured_connection", lastTest: null,
}, checkedAt: "2026-10-13T00:00:00Z" })}>Select synthetic source</button> }));
beforeEach(() => {
  clearExportRecovery(); localStorage.clear();
  vi.stubGlobal("crypto", webcrypto);
  vi.stubGlobal("navigator", Object.create(navigator));
  let held = false;
  Object.defineProperty(navigator, "locks", { configurable: true, value: {
    request: vi.fn(async (name: string, options: { mode: string; ifAvailable: boolean }, callback: (lock: { name: string } | null) => Promise<unknown>) => {
      expect(options).toEqual({ mode: "exclusive", ifAvailable: true });
      if (held) return callback(null);
      held = true;
      try { return await callback({ name }); } finally { held = false; }
    }),
  } });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); clearExportRecovery(); localStorage.clear(); });
function fixtures(errorResponse: boolean, cancellable: boolean) {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/auth/me") return exportJSON(exportProfile);
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "user_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    if (path.startsWith("/api/user-assets/v3?")) return errorResponse ? exportJSON({ error: "directory_observation_unavailable" }, 409) : exportJSON({ dictionaryVersion: 3, selection: { domainId: exportRequest.domainId, revision: exportRequest.expectedRevision, credentialRevision: exportRequest.expectedCredentialRevision }, available: false, list: [], page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 } });
    throw new Error(`Unexpected URL ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  let cancelled = false;
  const detail = vi.spyOn(userAssetExportAPI, "detail").mockImplementation(async (): Promise<ExportDetail> => !cancellable
    ? { ...readyExport, downloadReady: false, downloadStatus: "source_unavailable", artifact: undefined }
    : cancelled
      ? { ...exportReceipt, task: { ...exportReceipt.task, state: "cancelled" }, downloadReady: false, downloadStatus: "cancelled" }
      : { ...exportReceipt, downloadReady: false, downloadStatus: "pending" });
  // The real bounded authority method executes. Only the transport-level task
  // fixture changes after the explicit cancel, never before its fresh preflight.
  const authority = vi.spyOn(userAssetExportAPI, "recheckCancelAuthority");
  const cancel = vi.spyOn(userAssetExportAPI, "cancel").mockImplementation(async (taskID, request, _proof, actor, _csrf, signal) => {
    expect(detail.mock.results.length).toBeGreaterThanOrEqual(2);
    const marker = localStorage.getItem(cancelStorageKey(exportProfile.ID, exportReceipt.taskID));
    expect(marker).not.toBeNull();
    const expected = await cancelMarkerBytes({ taskID, request, profile: { ...exportProfile, ID: actor }, signal, assertCurrent: () => signal.throwIfAborted() });
    expect(marker === expected).toBe(true);
    cancelled = true;
    return { ...exportReceipt.task, state: "cancelled" };
  });
  const create = vi.spyOn(userAssetExportAPI, "create");
  return { receipt, detail, authority, cancel, create, fetcher };
}
async function recover() {
  render(<UserAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Select synthetic source" }));
  const button = await screen.findByRole("button", { name: "恢复用户导出回执" });
  await waitFor(() => expect(screen.queryByText("正在读取用户资产…")).not.toBeInTheDocument());
  fireEvent.click(button);
}
describe("unavailable observation export recovery", () => {
  it.each([[false, false], [true, false], [false, true], [true, true]])("keeps recovery reachable for unavailable-error=%s and cancellable=%s", async (errorResponse, cancellable) => {
    const { receipt, detail, authority, cancel, create, fetcher } = fixtures(errorResponse, cancellable);
    await recover();
    await waitFor(() => expect(receipt).toHaveBeenCalled());
    expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
    expect(screen.queryByRole("button", { name: "创建用户导出" })).not.toBeInTheDocument();
    if (cancellable) {
      await screen.findByRole("button", { name: "取消用户导出任务" });
      fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
      fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
      fireEvent.click(screen.getByRole("button", { name: "取消用户导出任务" }));
      await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
      await screen.findByText(/已取消 · 服务器已确认取消/);
      expect(authority).toHaveBeenCalledTimes(1);
      const [checkedID, checkedRequest, checkedProfile, checkedSignal] = authority.mock.calls[0];
      expect(checkedID === exportReceipt.taskID && JSON.stringify(checkedRequest) === JSON.stringify(exportRequest) && checkedProfile.ID === exportProfile.ID && checkedProfile.csrfToken === exportProfile.csrfToken && checkedSignal instanceof AbortSignal).toBe(true);
      const [id, request, , actor, csrf, signal] = cancel.mock.calls[0];
      expect(id === exportReceipt.taskID && JSON.stringify(request) === JSON.stringify(exportRequest) && actor === exportProfile.ID && csrf === exportProfile.csrfToken && signal instanceof AbortSignal).toBe(true);
      expect(fetcher.mock.calls.filter(([path]) => path === "/api/auth/me")).toHaveLength(2);
      expect(detail.mock.calls.every(([id, request]) => id === exportReceipt.taskID && JSON.stringify(request) === JSON.stringify(exportRequest))).toBe(true);
    } else {
      await screen.findByText(/固定观测不可用，原文件不可下载/);
      expect(cancel).not.toHaveBeenCalled(); expect(authority).not.toHaveBeenCalled();
    }
    expect(create).not.toHaveBeenCalled();
    expect(fetcher.mock.calls.some(([path]) => /\/api\/tasks\/(cancel|recover)/u.test(path))).toBe(false);
    expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
  });
  it.each([false, true])("missing locks retain read-only unavailable-error=%s recovery without a cancel action", async (errorResponse) => {
    Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
    const { receipt, detail, cancel, create, authority } = fixtures(errorResponse, true);
    await recover();
    await screen.findByText(/浏览器缺少可靠的本地存储或独占锁/);
    expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
    const readsBefore = detail.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
    await waitFor(() => expect(detail.mock.calls.length > readsBefore).toBe(true));
    await waitFor(() => expect(screen.getByRole("button", { name: "核对原导出回执" })).toBeEnabled());
    expect(screen.queryByRole("button", { name: "取消用户导出任务" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
    expect(cancel).not.toHaveBeenCalled(); expect(create).not.toHaveBeenCalled(); expect(authority).not.toHaveBeenCalled();
  });
});
