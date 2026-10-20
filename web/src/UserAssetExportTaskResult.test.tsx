// Synthetic DOM/API contracts; real source and XLSX evidence belongs to CI.
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import UserAssetExportTaskResult from "./UserAssetExportTaskResult";
import TaskWorkspace from "./TaskWorkspace";
import { exportJSON, exportProfile, exportRequest, readyExport } from "./user-asset-export-v3-fixtures";
import { exportPrefix, userAssetExportAPI } from "./user-asset-export-v3-api";
import { exportRecoveryStorage } from "./user-asset-export-v3-recovery";
import { defaultTaskQuery, writeTaskNavigation } from "./task-view-state";
let results: boolean[], status: number, value: unknown;
let response: () => Promise<Response>;
let changed: ReturnType<typeof vi.fn<() => void>>;
const click = (name: string) => fireEvent.click(screen.getByRole("button", { name }));
const requests = () => vi.mocked(fetch).mock.calls.filter(([url]) => String(url).startsWith(exportPrefix));
function view(props = {}) {
  return render(<UserAssetExportTaskResult profile={exportProfile} task={readyExport.task} sessionChanged={changed} {...props} />);
}
async function open() {
  await waitFor(() => expect(screen.getByRole("button", { name: "查看原导出结果" })).toBeEnabled());
  click("查看原导出结果");
  await screen.findByLabelText("原导出规范请求");
}
beforeEach(() => {
  results = [true, true]; status = 200; value = readyExport; changed = vi.fn();
  response = async () => exportJSON(value, status);
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url === "/api/access/menu") return exportJSON({ menu: ["domains", "directory_assets", "user_asset_exports", "tasks"].map((mark) => ({ mark, auth: { readable: true, writeable: false } })) });
    if (url === "/api/access/check") {
      const paths: string[] = JSON.parse(String(init?.body)).paths;
      return exportJSON({ results: paths.length === 2 ? results : paths.map(() => true) });
    }
    if (url.startsWith(`${exportPrefix}/detail?`)) return response();
    if (url.startsWith("/api/tasks/detail?")) return exportJSON({ task: readyExport.task, events: [] });
    throw new Error(url);
  }));
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); sessionStorage.clear(); window.history.replaceState({}, "", "#account"); });
it("reads the exact original task without saved recovery, write grants, receipt, or POST and pins refresh source", async () => {
  sessionStorage.setItem(exportRecoveryStorage, "unchanged foreign locator");
  view(); await open();
  expect(JSON.parse(screen.getByLabelText("原导出规范请求").textContent!)).toEqual(exportRequest);
  expect(sessionStorage.getItem(exportRecoveryStorage)).toBe("unchanged foreign locator");
  expect(requests()).toHaveLength(1);
  expect(requests()[0][0]).toBe(`${exportPrefix}/detail?taskID=${readyExport.taskID}`);
  expect(requests()[0][1]?.method).toBe("GET");
  const checks = vi.mocked(fetch).mock.calls.find(([url]) => url === "/api/access/check")!;
  expect(JSON.parse(String(checks[1]?.body)).paths).toEqual([`GET ${exportPrefix}/detail`, `GET ${exportPrefix}/download`]);
  value = { ...readyExport, request: { ...exportRequest, observationId: "new-source" } };
  click("刷新原导出结果");
  await screen.findByRole("alert");
  expect(screen.queryByLabelText("原导出规范请求")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "下载原用户 XLSX" })).not.toBeInTheDocument();
  expect(requests().every(([, init]) => init?.method === "GET")).toBe(true);
});
it.each(["group_assets.v3.export", "computer_assets.v3.export", "user_assets.v3.export.extra"])("does not add a result entrance for %s", (taskName) => {
  view({ task: { ...readyExport.task, taskName } });
  expect(screen.queryByRole("button", { name: "查看原导出结果" })).not.toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});
it("denied dedicated detail leaves generic task viewing intact", async () => {
  results = [false, false];
  writeTaskNavigation(exportProfile, { view: { type: "detail", id: readyExport.taskID, archived: false }, query: defaultTaskQuery(), listReturn: { focusTaskId: "", scrollY: 0 } }, "replace");
  render(<TaskWorkspace profile={exportProfile} sessionChanged={changed} />);
  await screen.findByText("当前未获原导出专用读取权限；通用任务记录仍可查看。");
  expect(screen.getByRole("region", { name: "任务执行详情" })).toBeVisible();
  expect(screen.getByRole("button", { name: "查看原导出结果" })).toBeDisabled();
  expect(requests()).toHaveLength(0);
  expect(changed).not.toHaveBeenCalled();
});
it("generic task refresh invalidates an open result before the replacement read", async () => {
  writeTaskNavigation(exportProfile, { view: { type: "detail", id: readyExport.taskID, archived: false }, query: defaultTaskQuery(), listReturn: { focusTaskId: "", scrollY: 0 } }, "replace");
  render(<TaskWorkspace profile={exportProfile} sessionChanged={changed} />);
  await open();
  const fetcher = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation((url, init) => String(url).startsWith("/api/tasks/detail?") ? Promise.resolve(exportJSON({ error: "forbidden" }, 403)) : fetcher(url, init));
  click("刷新任务详情");
  await waitFor(() => expect(screen.queryByRole("region", { name: "原导出只读详情" })).not.toBeInTheDocument());
  expect(changed).not.toHaveBeenCalled();
});
it.each(["failed", "cancelled", "source_changed", "source_unavailable", "artifact_invalid"])("shows original %s without enabling download", async (downloadStatus) => {
  const { artifact: _artifact, ...base } = readyExport;
  value = { ...base, task: { ...base.task, state: downloadStatus === "failed" ? "failed" : downloadStatus === "cancelled" ? "cancelled" : "succeeded" }, downloadStatus, downloadReady: false };
  view(); await open();
  expect(screen.getByRole("button", { name: "下载原用户 XLSX" })).toBeDisabled();
  expect(JSON.parse(screen.getByLabelText("原导出规范请求").textContent!)).toEqual(exportRequest);
});
it.each([401, 403, 404, 409])("clears original metadata after authority/epoch failure %i", async (code) => {
  view(); await open();
  status = code; value = { error: code === 409 ? "authorization_epoch_changed" : code === 401 ? "unauthenticated" : code === 404 ? "not_found" : "forbidden" };
  click("刷新原导出结果");
  await screen.findByRole("alert");
  expect(screen.queryByLabelText("原导出规范请求")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "查看原导出结果" })).toBeDisabled();
  expect(changed).toHaveBeenCalledTimes(code === 401 ? 1 : 0);
});
it.each(["close", "session", "actor", "domain", "task", "unmount"])("discards late detail after %s", async (change) => {
  let resolve!: (r: Response) => void;
  response = () => new Promise((done) => { resolve = done; });
  const mounted = view();
  await waitFor(() => expect(screen.getByRole("button", { name: "查看原导出结果" })).toBeEnabled());
  click("查看原导出结果");
  const signal = requests()[0][1]!.signal!;
  if (change === "close") click("关闭原导出结果");
  else if (change === "unmount") mounted.unmount();
  else mounted.rerender(<UserAssetExportTaskResult profile={{ ...exportProfile, ...(change === "session" ? { csrfToken: "new" } : change === "actor" ? { ID: 2, username: "other" } : {}) }} task={{ ...readyExport.task, ...(change === "domain" ? { domainId: "another" } : change === "task" ? { taskUUID: "00000000-0000-0000-0000-000000000002" } : {}) }} sessionChanged={changed} />);
  expect(signal.aborted).toBe(true);
  await act(async () => resolve(exportJSON(readyExport)));
  expect(screen.queryByLabelText("原导出规范请求")).not.toBeInTheDocument();
});
it("downloads with both fresh metadata checks and complete byte digest, never mutating recovery", async () => {
  vi.stubGlobal("crypto", webcrypto);
  const create = vi.fn(() => "blob:verified"), revoke = vi.fn();
  vi.stubGlobal("URL", class extends URL { static createObjectURL = create; static revokeObjectURL = revoke; });
  const anchor = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  const fetcher = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation((url, init) => String(url).startsWith(`${exportPrefix}/download?`) ? Promise.resolve(new Response("abc", { headers: {
    "Content-Type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "Content-Disposition": `attachment; filename="${readyExport.artifact!.filename}"`,
    "Content-Length": "3", "X-ADTR-Artifact-SHA256": readyExport.artifact!.sha256, "X-ADTR-Row-Count": "0", "X-ADTR-User-ID": "1", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
  } })) : fetcher(url, init));
  view(); await open(); click("下载原用户 XLSX");
  await screen.findByText("原文件完整字节已验证，并交由浏览器下载。");
  expect(anchor).toHaveBeenCalledTimes(1); expect(create).toHaveBeenCalledTimes(1); expect(revoke).toHaveBeenCalledTimes(1);
  expect(requests().map(([url]) => String(url).split("?")[0])).toEqual([`${exportPrefix}/detail`, `${exportPrefix}/detail`, `${exportPrefix}/download`, `${exportPrefix}/detail`]);
  expect(requests().every(([, init]) => init?.method === "GET")).toBe(true);
  expect(sessionStorage.getItem(exportRecoveryStorage)).toBeNull();
});
it("allows dedicated detail without download or write authority", async () => {
  results = [true, false]; view(); await open();
  expect(screen.getByRole("button", { name: "下载原用户 XLSX" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "创建用户导出" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "取消用户导出任务" })).not.toBeInTheDocument();
});
it.each(["close", "session", "unmount"])("late verified bytes never create a download after %s", async (change) => {
  let resolve!: (blob: Blob) => void;
  const download = vi.spyOn(userAssetExportAPI, "download").mockImplementation(() => new Promise((done) => { resolve = done; }));
  const create = vi.fn(() => "blob:forbidden");
  vi.stubGlobal("URL", class extends URL { static createObjectURL = create; static revokeObjectURL = vi.fn(); });
  const mounted = view(); await open(); click("下载原用户 XLSX");
  const signal = download.mock.calls[0][2];
  if (change === "close") click("关闭原导出结果");
  else if (change === "unmount") mounted.unmount();
  else mounted.rerender(<UserAssetExportTaskResult profile={{ ...exportProfile, csrfToken: "new" }} task={readyExport.task} sessionChanged={changed} />);
  expect(signal.aborted).toBe(true);
  await act(async () => resolve(new Blob(["abc"])));
  expect(create).not.toHaveBeenCalled();
});
it("network failure has no automatic retry and never creates a submission intent", async () => {
  response = async () => { throw new TypeError("network"); };
  view();
  await waitFor(() => expect(screen.getByRole("button", { name: "查看原导出结果" })).toBeEnabled());
  click("查看原导出结果"); await screen.findByRole("alert");
  const before = requests().length;
  vi.useFakeTimers(); await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
  expect(requests()).toHaveLength(before);
  expect(sessionStorage.getItem(exportRecoveryStorage)).toBeNull();
});
function originalFile(body: BodyInit = "abc") {
  return new Response(body, { headers: {
    "Content-Type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    "Content-Disposition": `attachment; filename="${readyExport.artifact!.filename}"`,
    "Content-Length": "3", "X-ADTR-Artifact-SHA256": readyExport.artifact!.sha256,
    "X-ADTR-Row-Count": "0", "X-ADTR-User-ID": "1", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
  } });
}
it.each(["body-close", "body-actor", "body-session", "postcheck-close", "postcheck-actor", "postcheck-session"])("aborts actual protected download %s before a Blob or anchor can escape", async (scenario) => {
  vi.stubGlobal("crypto", webcrypto);
  const create = vi.fn(), anchor = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  vi.stubGlobal("URL", class extends URL { static createObjectURL = create; static revokeObjectURL = vi.fn(); });
  let finish!: (value: Response) => void;
  let detailReads = 0;
  response = () => ++detailReads === 3
    ? new Promise((resolve) => { finish = resolve; }) : Promise.resolve(exportJSON(readyExport));
  const cancelled = vi.fn();
  const body = scenario.startsWith("body") ? new ReadableStream<Uint8Array>({
    start(control) { control.enqueue(new TextEncoder().encode("abc")); }, cancel: cancelled,
  }) : "abc";
  const fetcher = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation((url, init) => String(url).startsWith(`${exportPrefix}/download?`) ? Promise.resolve(originalFile(body)) : fetcher(url, init));
  const mounted = view(); await open(); click("下载原用户 XLSX");
  await waitFor(() => expect(requests()).toHaveLength(scenario.startsWith("body") ? 3 : 4));
  const signal = requests()[2][1]!.signal!;
  if (scenario.endsWith("close")) click("关闭原导出结果");
  else mounted.rerender(<UserAssetExportTaskResult profile={{ ...exportProfile, ...(scenario.endsWith("actor") ? { ID: 2, username: "other" } : { csrfToken: "new" }) }} task={readyExport.task} sessionChanged={changed} />);
  expect(signal.aborted).toBe(true);
  if (scenario.startsWith("postcheck")) await act(async () => finish(exportJSON(readyExport)));
  else await waitFor(() => expect(cancelled).toHaveBeenCalled());
  expect(create).not.toHaveBeenCalled(); expect(anchor).not.toHaveBeenCalled();
});
it.each(["bytes", "metadata"])("rejects changed %s without creating a browser file", async (invalid) => {
  vi.stubGlobal("crypto", webcrypto);
  const create = vi.fn(), anchor = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  vi.stubGlobal("URL", class extends URL { static createObjectURL = create; static revokeObjectURL = vi.fn(); });
  let count = 0;
  response = async () => exportJSON(++count === 3 ? { ...readyExport, artifact: { ...readyExport.artifact, sha256: "0".repeat(64) } } : readyExport);
  const fetcher = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation((url, init) => String(url).startsWith(`${exportPrefix}/download?`) ? Promise.resolve(originalFile(invalid === "bytes" ? "abd" : "abc")) : fetcher(url, init));
  view(); await open(); click("下载原用户 XLSX"); await screen.findByRole("alert");
  expect(create).not.toHaveBeenCalled(); expect(anchor).not.toHaveBeenCalled();
  expect(screen.queryByLabelText("原导出规范请求")).not.toBeInTheDocument();
});
