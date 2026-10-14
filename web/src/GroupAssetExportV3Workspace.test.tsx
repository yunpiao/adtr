import { ApiError } from "./api";
import { groupAssetsV3API } from "./group-assets-v3-api";
// Synthetic workspace integration: source-picker control is isolated, while the
// real workspace list availability gate and export recovery UI are exercised.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import GroupAssetsV3Workspace from "./GroupAssetsV3Workspace";
import { exportProfile, exportJSON, exportRequest, exportReceipt, readyExport } from "./group-asset-export-v3-fixtures";
import { groupAssetExportAPI, exportKind } from "./group-asset-export-v3-api";
import { clearExportRecovery, writeExportRecovery } from "./group-asset-export-v3-recovery";
import type { ResolvedSource } from "./domain-selection-api";
vi.mock("./SavedSourcePicker", () => ({ default: ({ onResolved }: { onResolved: (value: ResolvedSource) => void }) => <button onClick={() => onResolved({ selection: {
  domainId: "synthetic-domain", domain: "synthetic.invalid", revision: "9007199254740993", credentialRevision: "9007199254740995",
  dcHostName: "dc.synthetic.invalid", ldapAddr: "", port: "636", mode: "ldaps", credentialConfigured: true,
  connectionState: "unverified", source: "configured_connection", lastTest: null,
}, checkedAt: "2026-10-13T00:00:00Z" })}>Select synthetic source</button> }));
beforeEach(() => clearExportRecovery());
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); clearExportRecovery(); });
describe("unavailable observation export recovery", () => {
  it.each([[false, false], [true, false], [false, true], [true, true]])("keeps recovery reachable for unavailable-error=%s and cancellable=%s", async (errorResponse, cancellable) => {
    writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
    vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "group_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
      if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
      if (path.startsWith("/api/group-assets/v3?")) return errorResponse ? exportJSON({ error: "directory_observation_unavailable" }, 409) : exportJSON({ dictionaryVersion: 3, selection: { domainId: exportRequest.domainId, revision: exportRequest.expectedRevision, credentialRevision: exportRequest.expectedCredentialRevision }, available: false, observedGroupCount: null, list: [], page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 } });
      throw new Error(`Unexpected URL ${path}`);
    }));
    const receipt = vi.spyOn(groupAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    const detail = vi.spyOn(groupAssetExportAPI, "detail").mockResolvedValue(cancellable ? { ...exportReceipt, downloadReady: false, downloadStatus: "pending" } : { ...readyExport, downloadReady: false, downloadStatus: "source_unavailable", artifact: undefined });
    const cancel = vi.spyOn(groupAssetExportAPI, "cancel").mockResolvedValue({ ...exportReceipt.task, state: "cancelled" });
    render(<GroupAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Select synthetic source" }));
    const recover = await screen.findByRole("button", { name: "恢复组导出回执" });
    await waitFor(() => expect(screen.queryByText("正在读取组观测…")).not.toBeInTheDocument());
    fireEvent.click(recover);
    await waitFor(() => expect(receipt).toHaveBeenCalled());
    expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
    expect(screen.queryByRole("button", { name: "创建组导出" })).not.toBeInTheDocument();
    if (cancellable) {
    await screen.findByRole("button", { name: "取消组导出任务" });
    fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
    fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
    detail.mockResolvedValue({ ...exportReceipt, task: { ...exportReceipt.task, state: "cancelled" }, downloadReady: false, downloadStatus: "cancelled" });
    fireEvent.click(screen.getByRole("button", { name: "取消组导出任务" }));
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
    await screen.findByText(/已取消 · 服务器已确认取消/);
    } else {
      await screen.findByText(/固定观测不可用，原文件不可下载/);
      expect(cancel).not.toHaveBeenCalled();
    }
    expect(screen.queryByRole("button", { name: "下载组 XLSX" })).not.toBeInTheDocument();
  });
});


it("restores the original operation before selecting or resolving any fresh source", async () => {
  writeExportRecovery({ version: 1, kind: exportKind, actorID: 1, domainId: exportRequest.domainId, idempotencyKey: "original-key" });
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "group_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    throw new Error(`No fresh source request expected: ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const receipt = vi.spyOn(groupAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  vi.spyOn(groupAssetExportAPI, "detail").mockResolvedValue({ ...readyExport, downloadReady: false, downloadStatus: "source_unavailable", artifact: undefined });
  const create = vi.spyOn(groupAssetExportAPI, "create");
  render(<GroupAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "恢复组导出回执" }));
  await screen.findByText(/固定观测不可用，原文件不可下载/);
  expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
  expect(create).not.toHaveBeenCalled();
  expect(fetcher.mock.calls.every(([path]) => path.startsWith("/api/access/"))).toBe(true);
  expect(screen.queryByRole("button", { name: "创建组导出" })).not.toBeInTheDocument();
});


it("recovers a newly created locator after source invalidation in the same workspace mount", async () => {
  vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "group_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
    throw new Error(`Unexpected URL ${path}`);
  }));
  const list = vi.spyOn(groupAssetsV3API, "list").mockResolvedValue({
    dictionaryVersion: 3, available: true, observedGroupCount: 0,
    selection: { domainId: exportRequest.domainId, revision: exportRequest.expectedRevision, credentialRevision: exportRequest.expectedCredentialRevision },
    observationId: exportRequest.observationId,
    source: { server_name: "synthetic", dc_host_name: "dc.synthetic.invalid", domain: "synthetic.invalid", naming_context: "DC=synthetic,DC=invalid", started_at: "2026-10-13T00:00:00Z", completed_at: "2026-10-13T00:00:01Z", elapsed_milliseconds: 1000, pages: 1 },
    list: [], page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 },
  });
  const create = vi.spyOn(groupAssetExportAPI, "create").mockResolvedValue(exportReceipt);
  vi.spyOn(groupAssetExportAPI, "detail").mockResolvedValue(readyExport);
  const receipt = vi.spyOn(groupAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
  render(<GroupAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: "Select synthetic source" }));
  const open = await screen.findByRole("button", { name: "导出匹配组 XLSX" });
  await waitFor(() => expect(open).toBeEnabled());
  fireEvent.click(open);
  await waitFor(() => expect(screen.getByRole("button", { name: "创建组导出" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
  fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
  fireEvent.click(screen.getByRole("button", { name: "创建组导出" }));
  await screen.findByRole("button", { name: "下载组 XLSX" });
  expect(screen.getByRole("region", { name: "已选数据源组资产" })).toBeVisible();
  list.mockRejectedValue(new ApiError("selection_changed", 409));
  fireEvent.click(screen.getByRole("button", { name: "刷新组资产观测" }));
  // Refresh first removes the observation pin inside Assets. Its intermediate
  // recovery button is then unmounted when selection_changed clears the source.
  // Await that lifecycle boundary before clicking the root recovery entry.
  await waitFor(() => expect(screen.queryByRole("region", { name: "已选数据源组资产" })).not.toBeInTheDocument());
  expect(screen.getByText("数据源配置或凭据版本已改变，请重新选择数据源。")).toBeVisible();
  expect(screen.queryByRole("button", { name: "刷新组资产观测" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "恢复组导出回执" }));
  await waitFor(() => expect(receipt).toHaveBeenCalledTimes(1));
  expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, create.mock.calls[0][1], 1, expect.any(AbortSignal));
  expect(create).toHaveBeenCalledTimes(1);
});
