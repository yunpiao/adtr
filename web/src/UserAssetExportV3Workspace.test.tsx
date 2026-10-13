// Synthetic workspace integration: source-picker control is isolated, while the
// real workspace list availability gate and export recovery UI are exercised.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import UserAssetsV3Workspace from "./UserAssetsV3Workspace";
import { exportProfile, exportJSON, exportRequest, exportReceipt, readyExport } from "./user-asset-export-v3-fixtures";
import { userAssetExportAPI, exportKind } from "./user-asset-export-v3-api";
import { clearExportRecovery, writeExportRecovery } from "./user-asset-export-v3-recovery";
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
      if (path === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "tasks", "user_asset_exports"].map((mark) => ({ mark, auth: { readable: true, writeable: true } })) });
      if (path === "/api/access/check") return exportJSON({ results: JSON.parse(String(init?.body)).paths.map(() => true) });
      if (path.startsWith("/api/user-assets/v3?")) return errorResponse ? exportJSON({ error: "directory_observation_unavailable" }, 409) : exportJSON({ dictionaryVersion: 3, selection: { domainId: exportRequest.domainId, revision: exportRequest.expectedRevision, credentialRevision: exportRequest.expectedCredentialRevision }, available: false, list: [], page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 } });
      throw new Error(`Unexpected URL ${path}`);
    }));
    const receipt = vi.spyOn(userAssetExportAPI, "receipt").mockResolvedValue(exportReceipt);
    const detail = vi.spyOn(userAssetExportAPI, "detail").mockResolvedValue(cancellable ? { ...exportReceipt, downloadReady: false, downloadStatus: "pending" } : { ...readyExport, downloadReady: false, downloadStatus: "source_unavailable", artifact: undefined });
    const cancel = vi.spyOn(userAssetExportAPI, "cancel").mockResolvedValue({ ...exportReceipt.task, state: "cancelled" });
    render(<UserAssetsV3Workspace profile={exportProfile} sessionChanged={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Select synthetic source" }));
    const recover = await screen.findByRole("button", { name: "恢复用户导出回执" });
    await waitFor(() => expect(screen.queryByText("正在读取用户资产…")).not.toBeInTheDocument());
    fireEvent.click(recover);
    await waitFor(() => expect(receipt).toHaveBeenCalled());
    expect(receipt).toHaveBeenCalledWith(exportRequest.domainId, "original-key", 1, expect.any(AbortSignal));
    expect(screen.queryByRole("button", { name: "创建用户导出" })).not.toBeInTheDocument();
    if (cancellable) {
    await screen.findByRole("button", { name: "取消用户导出任务" });
    fireEvent.change(screen.getByLabelText("操作者当前密码"), { target: { value: "SyntheticPassword123!" } });
    fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), { target: { value: "123456" } });
    detail.mockResolvedValue({ ...exportReceipt, task: { ...exportReceipt.task, state: "cancelled" }, downloadReady: false, downloadStatus: "cancelled" });
    fireEvent.click(screen.getByRole("button", { name: "取消用户导出任务" }));
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
    await screen.findByText(/已取消 · 服务器已确认取消/);
    } else {
      await screen.findByText(/固定观测不可用，原文件不可下载/);
      expect(cancel).not.toHaveBeenCalled();
    }
    expect(screen.queryByRole("button", { name: "下载用户 XLSX" })).not.toBeInTheDocument();
  });
});
