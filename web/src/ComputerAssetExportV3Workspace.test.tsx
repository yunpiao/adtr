import { webcrypto } from "node:crypto";
import { ApiError } from "./api";
import { computerAssetsV3API } from "./computer-assets-v3-api";
// Synthetic workspace integration: source-picker control is isolated, while the
// real workspace list availability gate and export recovery UI are exercised.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ComputerAssetsV3Workspace from "./ComputerAssetsV3Workspace";
import { exportProfile, exportJSON, exportRequest, exportReceipt, readyExport } from "./computer-asset-export-v3-fixtures";
import { computerAssetExportAPI, exportKind } from "./computer-asset-export-v3-api";
import { clearExportRecovery, writeExportRecovery } from "./computer-asset-export-v3-recovery";
import type { ResolvedSource } from "./domain-selection-api";
vi.mock("./SavedSourcePicker", () => ({ default: ({ onResolved }: { onResolved: (value: ResolvedSource) => void }) => <button onClick={() => onResolved({ selection: {
  domainId: "synthetic-domain", domain: "synthetic.invalid", revision: "9007199254740993", credentialRevision: "9007199254740995",
  dcHostName: "dc.synthetic.invalid", ldapAddr: "", port: "636", mode: "ldaps", credentialConfigured: true,
  connectionState: "unverified", source: "configured_connection", lastTest: null,
}, checkedAt: "2026-10-13T00:00:00Z" })}>Select synthetic source</button> }));
beforeEach(() => {
  clearExportRecovery(); localStorage.clear();
  vi.stubGlobal("crypto", webcrypto); vi.stubGlobal("navigator", Object.create(navigator));
  const held = new Set<string>();
  Object.defineProperty(navigator, "locks", { configurable: true, value: { request: vi.fn(async (name: string, options: { mode: string; ifAvailable: boolean }, callback: (lock: { name: string; mode: "exclusive" } | null) => unknown) => {
    expect(options).toEqual({ mode: "exclusive", ifAvailable: true });
    if (held.has(name)) return callback(null);
    held.add(name);
    try { return await callback({ name, mode: "exclusive" }); } finally { held.delete(name); }
  }) } });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); clearExportRecovery(); localStorage.clear(); });
describe("unavailable observation export recovery", () => {
  it.each([[false, false], [true, false], [false, true], [true, true]])("keeps recovery reachable for unavailable-error=%s and cancellable=%s", async (errorResponse, cancellable) => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
    vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/auth/me") return exportJSON(exportProfile);
      if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "computer_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
      if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
      if (path.startsWith("/api/computer-assets/v3?")) return errorResponse ? exportJSON({ error: "directory_observation_unavailable" }, 409) : exportJSON({ dictionaryVersion: 3, selection: { domainId: exportRequest.domainId, revision: exportRequest.expectedRevision, credentialRevision: exportRequest.expectedCredentialRevision }, available: false, observedComputerCount: null, list: [], page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 } });
      throw new Error(`Unexpected URL ${path}`);
    }));
    const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    const detail = vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(cancellable ? { ...exportReceipt, downloadReady: false, downloadStatus: "pending" } : { ...readyExport, downloadReady: false, downloadStatus: "source_unavailable", artifact: undefined });
    // Exercise the real current-authority preflight. The mocked business state
    // changes only after cancellation actually dispatches, not before its fresh read.
    const recheck = vi.spyOn(computerAssetExportAPI, "recheckCancelAuthority");
    const cancel = vi.spyOn(computerAssetExportAPI, "cancel").mockImplementation(async () => {
      detail.mockResolvedValue({ ...exportReceipt, task: { ...exportReceipt.task, state: "cancelled" }, downloadReady: false, downloadStatus: "cancelled" });
      return { ...exportReceipt.task, state: "cancelled" as const };
    });
    render(<ComputerAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Select synthetic source" }));
    const recover = await screen.findByRole("button", { name: "恢复计算机导出回执" });
    await waitFor(() => expect(screen.queryByText("正在读取计算机观测…")).not.toBeInTheDocument());
    fireEvent.click(recover);
    await waitFor(() => expect(receipt).toHaveBeenCalled());
    expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
    expect(screen.queryByRole("button", { name: "创建计算机导出" })).not.toBeInTheDocument();
    if (cancellable) {
      await screen.findByRole("button", { name: "取消计算机导出任务" });
      fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
      fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
      fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" }));
      await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
      await screen.findByText(/已取消 · 服务器已确认取消/);
      expect(recheck).toHaveBeenCalledTimes(1);
      expect(recheck).toHaveBeenCalledWith(exportReceipt.taskID, exportRequest, exportProfile, expect.any(AbortSignal));
      expect(vi.mocked(fetch).mock.calls.filter(([path]) => path === "/api/auth/me")).toHaveLength(2);
      expect(localStorage.length).toBe(0);
    } else {
      await screen.findByText(/固定观测不可用，原文件不可下载/);
      expect(cancel).not.toHaveBeenCalled(); expect(recheck).not.toHaveBeenCalled();
      expect(screen.queryByRole("button", { name: "取消计算机导出任务" })).not.toBeInTheDocument();
      expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("未使用的认证器验证码")).not.toBeInTheDocument();
    }
    expect(screen.queryByRole("button", { name: "下载计算机 XLSX" })).not.toBeInTheDocument();
  });
});


it("restores the original operation before selecting or resolving any fresh source", async () => {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "computer_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    throw new Error(`No fresh source request expected: ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue({ ...readyExport, downloadReady: false, downloadStatus: "source_unavailable", artifact: undefined });
  const create = vi.spyOn(computerAssetExportAPI, "create");
  render(<ComputerAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "恢复计算机导出回执" }));
  await screen.findByText(/固定观测不可用，原文件不可下载/);
  expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
  expect(create).not.toHaveBeenCalled();
  expect(fetcher.mock.calls.every(([path]) => path.startsWith("/api/access/"))).toBe(true);
  expect(screen.queryByRole("button", { name: "创建计算机导出" })).not.toBeInTheDocument();
});


it("recovers a newly created locator after source invalidation in the same workspace mount", async () => {
  vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "computer_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    throw new Error(`Unexpected URL ${path}`);
  }));
  const list = vi.spyOn(computerAssetsV3API, "list").mockResolvedValue({
    dictionaryVersion: 3, available: true, observedComputerCount: 0,
    selection: { domainId: exportRequest.domainId, revision: exportRequest.expectedRevision, credentialRevision: exportRequest.expectedCredentialRevision },
    observationId: exportRequest.observationId,
    source: { server_name: "synthetic", dc_host_name: "dc.synthetic.invalid", domain: "synthetic.invalid", naming_context: "DC=synthetic,DC=invalid", started_at: "2026-10-13T00:00:00Z", completed_at: "2026-10-13T00:00:01Z", elapsed_milliseconds: 1000, pages: 1 },
    list: [], page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 },
  });
  const create = vi.spyOn(computerAssetExportAPI, "create").mockResolvedValue(exportReceipt);
  vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue(readyExport);
  const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  render(<ComputerAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Select synthetic source" }));
  const open = await screen.findByRole("button", { name: "导出匹配计算机 XLSX" });
  await waitFor(() => expect(open).toBeEnabled());
  fireEvent.click(open);
  await waitFor(() => expect(screen.getByRole("button", { name: "创建计算机导出" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
  fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
  await screen.findByRole("button", { name: "下载计算机 XLSX" });
  expect(screen.getByRole("region", { name: "已选数据源计算机资产" })).toBeVisible();
  list.mockRejectedValue(new ApiError("selection_changed", 409));
  fireEvent.click(screen.getByRole("button", { name: "刷新计算机资产观测" }));
  // Refresh first removes the observation pin inside Assets. Its intermediate
  // recovery button is then unmounted when selection_changed clears the source.
  // Await that lifecycle boundary before clicking the root recovery entry.
  await waitFor(() => expect(screen.queryByRole("region", { name: "已选数据源计算机资产" })).not.toBeInTheDocument());
  expect(screen.getByText("数据源配置或凭据版本已改变，请重新选择数据源。")).toBeVisible();
  expect(screen.queryByRole("button", { name: "刷新计算机资产观测" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "恢复计算机导出回执" }));
  await waitFor(() => expect(receipt).toHaveBeenCalledTimes(1));
  expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, create.mock.calls[0][1], 1, expect.any(AbortSignal));
  expect(create).toHaveBeenCalledTimes(1);
});


it("exports the applied computer date range and search, not a dirty draft or pagination", async () => {
  vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "computer_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    throw new Error(`Unexpected URL ${path}`);
  }));
  const list = vi.spyOn(computerAssetsV3API, "list").mockResolvedValue({
    dictionaryVersion: 3, available: true, observedComputerCount: 0,
    selection: { domainId: exportRequest.domainId, revision: exportRequest.expectedRevision, credentialRevision: exportRequest.expectedCredentialRevision },
    observationId: exportRequest.observationId,
    source: { server_name: "synthetic", dc_host_name: "dc.synthetic.invalid", domain: "synthetic.invalid", naming_context: "DC=synthetic,DC=invalid", started_at: "2026-10-13T00:00:00Z", completed_at: "2026-10-13T00:00:01Z", elapsed_milliseconds: 1000, pages: 1 },
    list: [], page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 },
  });
  const create = vi.spyOn(computerAssetExportAPI, "create").mockImplementation(async (request) => ({ ...exportReceipt, request }));
  vi.spyOn(computerAssetExportAPI, "detail").mockImplementation(async (_id, request) => ({ ...readyExport, request }));
  render(<ComputerAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Select synthetic source" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "导出匹配计算机 XLSX" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("计算机资产关键词"), { target: { value: " device$ " } });
  fireEvent.change(screen.getByLabelText("创建时间开始（含）"), { target: { value: "2026-01-01T08:00:00+08:00" } });
  fireEvent.change(screen.getByLabelText("创建时间结束（不含）"), { target: { value: "2026-02-01T00:00:00Z" } });
  fireEvent.change(screen.getByLabelText("创建时间排序"), { target: { value: "desc" } });
  expect(screen.getByRole("button", { name: "导出匹配计算机 XLSX" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "应用计算机筛选" }));
  await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.getByRole("button", { name: "导出匹配计算机 XLSX" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "导出匹配计算机 XLSX" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "创建计算机导出" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
  fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "创建计算机导出" }));
  await screen.findByRole("button", { name: "下载计算机 XLSX" });
  expect(create.mock.calls[0][0]).toEqual({ ...exportRequest, search: " device$ ", startTm: "2026-01-01T08:00:00+08:00", endTm: "2026-02-01T00:00:00Z", creatTm: "desc" });
  expect(create.mock.calls[0][0]).not.toHaveProperty("pageIdx");
  expect(create.mock.calls[0][0]).not.toHaveProperty("pageSize");
});

// Cancellation capability belongs to the browser/intent protocol, not source
// availability. Lack of that capability must not hide original read recovery.
it("keeps original receipt/detail recovery usable without Web Locks but exposes no proof or cancel", async () => {
  Object.defineProperty(navigator, "locks", { configurable: true, value: undefined });
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "computer_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    throw new Error("Unexpected recovery-only transport");
  });
  vi.stubGlobal("fetch", fetcher);
  const receipt = vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  const detail = vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue({ ...exportReceipt, downloadReady: false, downloadStatus: "pending" });
  const cancel = vi.spyOn(computerAssetExportAPI, "cancel"), create = vi.spyOn(computerAssetExportAPI, "create");
  render(<ComputerAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "恢复计算机导出回执" }));
  await screen.findByText(/浏览器缺少可靠的本地存储或独占锁/);
  expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
  expect(detail).toHaveBeenCalledWith(exportReceipt.taskID, exportRequest, 1, expect.any(AbortSignal));
  expect(screen.getByRole("button", { name: "核对原导出回执" })).toBeEnabled();
  expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("未使用的认证器验证码")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "取消计算机导出任务" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "核对原导出回执" }));
  await waitFor(() => expect(receipt).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.getByRole("button", { name: "核对原导出回执" })).toBeEnabled());
  expect(detail).toHaveBeenCalledTimes(2);
  expect(cancel).not.toHaveBeenCalled(); expect(create).not.toHaveBeenCalled();
  expect(localStorage.length).toBe(0);
  expect(fetcher.mock.calls.every(([path]) => path.startsWith("/api/access/"))).toBe(true);
});

it("does not dispatch cancellation when real current-authority preflight observes an already terminal original task", async () => {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/auth/me") return exportJSON(exportProfile);
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "computer_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    throw new Error("Unexpected original-task transport");
  });
  vi.stubGlobal("fetch", fetcher);
  vi.spyOn(computerAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  const detail = vi.spyOn(computerAssetExportAPI, "detail").mockResolvedValue({ ...exportReceipt, downloadReady: false, downloadStatus: "pending" });
  const recheck = vi.spyOn(computerAssetExportAPI, "recheckCancelAuthority"), cancel = vi.spyOn(computerAssetExportAPI, "cancel");
  render(<ComputerAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "恢复计算机导出回执" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "取消计算机导出任务" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
  fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
  // A different cooperating entry already reached terminal after this form was
  // prepared. The mandatory fresh detail, not the stale form, decides eligibility.
  detail.mockResolvedValue({ ...exportReceipt, task: { ...exportReceipt.task, state: "cancelled" }, downloadReady: false, downloadStatus: "cancelled" });
  fireEvent.click(screen.getByRole("button", { name: "取消计算机导出任务" }));
  await screen.findByText(/已取消 · 服务器已确认取消/);
  expect(recheck).toHaveBeenCalledTimes(1);
  expect(recheck).toHaveBeenCalledWith(exportReceipt.taskID, exportRequest, exportProfile, expect.any(AbortSignal));
  expect(fetcher.mock.calls.filter(([path]) => path === "/api/auth/me")).toHaveLength(2);
  expect(cancel).not.toHaveBeenCalled(); expect(localStorage.length).toBe(0);
  expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "取消计算机导出任务" })).not.toBeInTheDocument();
});
