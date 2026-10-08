// Synthetic DOM and transport regression evidence only. The real worker/XLSX
// browser flow remains in e2e/audit.spec.ts and requires PostgreSQL.
import { StrictMode } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import AuditWorkspace from "./AuditWorkspace";
import { type Profile } from "./api";
import { auditAPI } from "./audit-api";
import {
  emptyHistoryQuery,
  exportDownloadPath,
  exportFilename,
  historyInputUTC,
  historyQuery,
  parseHistory,
  type ExportHistoryRow,
} from "./audit-history";
import { taskStates, type TaskState } from "./task-api";

const id = "00000000-0000-4000-8000-000000000011";
const other = "00000000-0000-4000-8000-000000000012";
const profile: Profile = {
  ID: 1,
  username: "synthetic-admin",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "synthetic-token",
};
const row = (
  state: TaskState = "succeeded",
  changes: Partial<ExportHistoryRow> = {},
): ExportHistoryRow => ({
  taskUUID: id,
  fileName: exportFilename(id),
  modelType: "Audit",
  fileType: "xlsx",
  state,
  progress: 37,
  error: "",
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:01:00Z",
  attempt: 1,
  maxAttempts: 3,
  downloadReady: state === "succeeded",
  downloadStatus: state === "succeeded" ? "eligible" : "not_ready",
  ...(state === "succeeded"
    ? {
        rowCount: 0,
        snapshotAt: "2026-10-07T00:00:30Z",
        downloadPath: exportDownloadPath(id),
      }
    : {}),
  ...changes,
});
const history = (
  rows = [row()],
  pageIdx = 1,
  pageSize = 20,
  total = rows.length,
) => ({
  page: {
    pageIdx,
    pageSize,
    total,
    totalPage: total
      ? Math.ceil(total / (pageSize === -1 ? total : pageSize))
      : 0,
  },
  list: rows,
  exhausted: (pageIdx - 1) * pageSize + rows.length >= total,
});
const response = (data: unknown, actor: string | null = "1", status = 200) =>
  ({
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers(actor === null ? {} : { "X-ADTR-User-ID": actor }),
    json: vi.fn(async () => data),
  }) as unknown as Response;
const detail = (state: TaskState = "succeeded") => ({
  task: {
    taskUUID: id,
    taskName: "audit.export",
    domainId: "platform",
    payloadVersion: 1,
    state,
    sourceState: "SUCCESS",
    error: "",
    createdAt: row().createdAt,
    updatedAt: row().updatedAt,
    attempt: 1,
    maxAttempts: 3,
    progress: 37,
    resultVersion: 1,
    result: null,
    cursor: null,
    parentTaskUUID: "",
    terminalAt: state === "succeeded" ? row().updatedAt : null,
    archived: false,
    visibilityVersion: 0,
  },
  downloadReady: state === "succeeded",
  ...(state === "succeeded"
    ? {
        rowCount: 0,
        snapshotAt: row().snapshotAt,
        downloadPath: exportDownloadPath(id),
      }
    : {}),
});
let fetcher: ReturnType<typeof vi.fn>;
let override: (
  url: string,
  init: RequestInit,
) => Response | Promise<Response> | undefined;
const clicks = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const requests = (path: string) =>
  fetcher.mock.calls.filter(
    ([url]) => url.split("?")[0] === `/api/audit/exports/${path}`,
  );
beforeEach(() => {
  window.history.replaceState({}, "", "#audit/history");
  override = () => undefined;
  fetcher = vi.fn(async (url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    if (url === "/api/auth/me") return response(profile);
    if (url === "/api/access/menu")
      return response({ menu: [{ mark: "audit", auth: { readable: true } }] });
    if (url === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map(() => true),
      });
    if (url.startsWith("/api/audit/exports/history"))
      return response(history());
    if (url.startsWith("/api/audit/exports/detail")) return response(detail());
    if (url === "/api/audit/types") return response({ events: [], List: [] });
    if (url.startsWith("/api/audit?"))
      return response({
        page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
        List: [],
        exhausted: true,
      });
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
async function start(changed = vi.fn()) {
  const mounted = render(
    <AuditWorkspace profile={profile} sessionChanged={changed} />,
  );
  await screen.findByRole("table", { name: "导出历史记录" });
  return mounted;
}

describe("export history transport contracts", () => {
  it("encodes distinct canonical statuses and explicit UTC creation bounds", () => {
    const query = historyQuery({
      ...emptyHistoryQuery(),
      status: ["retry_wait", "cancel_requested", "cancelled"],
      startTm: "2026-10-06T12:00:00Z",
      endTm: "2026-10-07T12:00:00Z",
      sortTm: 1,
      pageIdx: 2,
      pageSize: 10,
    });
    const params = new URLSearchParams(query);
    expect(params.getAll("status")).toEqual([
      "retry_wait",
      "cancel_requested",
      "cancelled",
    ]);
    expect(params.get("startTm")).toBe("2026-10-06T12:00:00Z");
    expect(params.has("modelType")).toBe(false);
    expect(params.has("appType")).toBe(false);
    expect(historyInputUTC("2026-10-06T12:00")).toBe(
      "2026-10-06T12:00:00.000Z",
    );
    expect(historyInputUTC("2026-10-06T12:00:00.123456")).toBe(
      "2026-10-06T12:00:00.123456Z",
    );
    expect(() =>
      historyQuery({
        ...emptyHistoryQuery(),
        startTm: "2026-10-06T12:00:00.123456Z",
        endTm: "2026-10-06T12:00:00.123457Z",
      }),
    ).not.toThrow();
    expect(() => historyInputUTC("2026-02-30T12:00:00")).toThrow();
    expect(() =>
      historyQuery({ ...emptyHistoryQuery(), status: ["queued", "queued"] }),
    ).toThrow();
  });
  it("preserves exact persisted state/progress and accepts a zero-row eligible workbook", () => {
    for (const state of taskStates) {
      const parsed = parseHistory(history([row(state)]), emptyHistoryQuery());
      expect(parsed.list[0]).toMatchObject({
        state,
        progress: 37,
        downloadReady: state === "succeeded",
      });
    }
    expect(parseHistory(history(), emptyHistoryQuery()).list[0].rowCount).toBe(
      0,
    );
  });
  it("rejects impossible metadata, leaks, duplicate rows and inconsistent page totals", () => {
    const eligible = row();
    const invalid = [
      history([{ ...eligible, rowCount: -1 }]),
      history([{ ...eligible, rowCount: 100001 }]),
      history([{ ...eligible, rowCount: undefined }]),
      history([{ ...eligible, fileName: "export.xlsx" }]),
      history([{ ...eligible, downloadPath: "https://example.invalid/file" }]),
      history([
        { ...eligible, modelType: "Alert" } as unknown as ExportHistoryRow,
      ]),
      history([{ ...eligible, state: "running" }]),
      history([{ ...eligible, progress: 101 }]),
      history([{ ...eligible, error: "database secret details" }]),
      history([{ ...eligible, createdAt: "2026-02-30T00:00:00Z" }]),
      history([{ ...eligible, snapshotAt: "2026-10-07 00:00:00" }]),
      history([
        {
          ...eligible,
          downloadStatus: "snapshot_changed",
          downloadReady: false,
        },
      ]),
      history([{ ...eligible, actorId: 1 } as ExportHistoryRow]),
      history([eligible, eligible]),
      { ...history(), list: null },
      { ...history(), page: { ...history().page, total: 2 } },
      { ...history(), exhausted: false },
    ];
    for (const value of invalid)
      expect(() => parseHistory(value, emptyHistoryQuery())).toThrow();
    expect(
      parseHistory(history([], 3, 20, 1), {
        ...emptyHistoryQuery(),
        pageIdx: 3,
      }).page.total,
    ).toBe(1);
    expect(() =>
      parseHistory(history([], 1, -1, 1001), {
        ...emptyHistoryQuery(),
        pageSize: -1,
      }),
    ).toThrow();
  });
  it.each(["history", "detail", "download"])(
    "rejects mismatched or missing actor before reading %s payload",
    async (path) => {
      for (const actor of ["2", null, "01"]) {
        const value = response(history(), actor);
        const bytes = vi.fn();
        value.blob = bytes;
        override = () => value;
        const read =
          path === "history"
            ? auditAPI.history(
                emptyHistoryQuery(),
                new AbortController().signal,
                profile.ID,
              )
            : path === "detail"
              ? auditAPI.detail(id, new AbortController().signal, profile.ID)
              : auditAPI.download(id, new AbortController().signal, profile.ID);
        await expect(read).rejects.toMatchObject({
          code: "unauthenticated",
          status: 401,
        });
        expect(value.json).not.toHaveBeenCalled();
        expect(bytes).not.toHaveBeenCalled();
      }
    },
  );
  it("rejects wrong download filenames before consuming bytes", async () => {
    const value = response({});
    value.headers.set(
      "Content-Type",
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    );
    value.headers.set(
      "Content-Disposition",
      'attachment; filename="different.xlsx"',
    );
    value.blob = vi.fn();
    override = () => value;
    await expect(
      auditAPI.download(id, new AbortController().signal, profile.ID),
    ).rejects.toMatchObject({ code: "invalid_response" });
    expect(value.blob).not.toHaveBeenCalled();
  });
});

describe("persisted export history DOM", () => {
  it("automatically discovers own exports on audit reopen and opens detail without UUID entry", async () => {
    window.history.replaceState({}, "", "#audit");
    const mounted = render(
      <AuditWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    expect(await screen.findByText(/当前可见的本人导出：1 项/)).toBeVisible();
    clicks("导出历史");
    await screen.findByRole("table", { name: "导出历史记录" });
    expect(screen.getByText(exportFilename(id))).toBeVisible();
    expect(screen.getByLabelText(`导出进度 ${id}`)).toHaveValue(37);
    expect(screen.getByText(/实际行数：0/)).toBeVisible();
    expect(screen.queryByLabelText("应用类型")).not.toBeInTheDocument();
    clicks("查看导出详情");
    await screen.findByLabelText("导出实际结果");
    expect(requests("detail")[0][0]).toBe(
      `/api/audit/exports/detail?taskUUID=${id}`,
    );
    clicks("返回导出历史");
    await screen.findByRole("table", { name: "导出历史记录" });
    mounted.unmount();
    render(<AuditWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByRole("table", { name: "导出历史记录" });
    expect(requests("history").length).toBeGreaterThanOrEqual(4);
  });
  it("applies and clears independent creation/status/sort filters and uses real pagination", async () => {
    override = (url) => {
      if (!url.startsWith("/api/audit/exports/history")) return;
      const q = new URLSearchParams(url.split("?")[1]);
      const pageIdx = Number(q.get("pageIdx")),
        pageSize = Number(q.get("pageSize"));
      const rows = Array.from(
        { length: pageIdx === 1 ? pageSize : 1 },
        (_, i) => {
          const taskUUID = `00000000-0000-4000-8000-${String(i + pageIdx * 100).padStart(12, "0")}`;
          return row("cancelled", {
            taskUUID,
            fileName: exportFilename(taskUUID),
          });
        },
      );
      return response(history(rows, pageIdx, pageSize, pageSize + 1));
    };
    await start();
    fill("每页导出数", "10");
    fill("导出创建开始时间（UTC）", "2026-10-06T01:02:03");
    fill("导出创建结束时间（UTC，不包含）", "2026-10-08T01:02:03");
    fill("导出创建时间排序", "1");
    const select = screen.getByLabelText("导出任务状态", {
      exact: true,
    }) as HTMLSelectElement;
    for (const option of select.options)
      option.selected = ["cancel_requested", "cancelled"].includes(
        option.value,
      );
    fireEvent.change(select);
    clicks("筛选导出历史");
    await waitFor(() =>
      expect(requests("history").at(-1)![0]).toContain(
        "pageIdx=1&pageSize=10&sortTm=1",
      ),
    );
    let params = new URLSearchParams(
      requests("history").at(-1)![0].split("?")[1],
    );
    expect(params.getAll("status")).toEqual(["cancel_requested", "cancelled"]);
    expect(params.get("startTm")).toBe("2026-10-06T01:02:03.000Z");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "下一页" })).toBeEnabled(),
    );
    clicks("下一页");
    await waitFor(() =>
      expect(requests("history").at(-1)![0]).toContain("pageIdx=2"),
    );
    await screen.findByText("共 11 条 · 第 2 / 2 页");
    clicks("清空导出筛选");
    await waitFor(() =>
      expect(requests("history").at(-1)![0]).toContain(
        "pageIdx=1&pageSize=20&sortTm=-1",
      ),
    );
    params = new URLSearchParams(requests("history").at(-1)![0].split("?")[1]);
    expect(params.has("status")).toBe(false);
    expect(params.has("startTm")).toBe(false);
    expect(params.has("keyword")).toBe(false);
  });
  it("rejects equal creation bounds without fetching and preserves the current result", async () => {
    await start();
    fill("导出创建开始时间（UTC）", "2026-10-07T01:02:03");
    fill("导出创建结束时间（UTC，不包含）", "2026-10-07T01:02:03");
    clicks("筛选导出历史");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "开始时间必须早于结束时间",
    );
    expect(requests("history")).toHaveLength(1);
  });
  it("keeps task success separate from snapshot/file eligibility", async () => {
    const changed = row("succeeded", {
      downloadReady: false,
      downloadStatus: "snapshot_changed",
    });
    delete changed.rowCount;
    delete changed.snapshotAt;
    delete changed.downloadPath;
    const invalid = {
      ...changed,
      taskUUID: other,
      fileName: exportFilename(other),
      downloadStatus: "artifact_invalid" as const,
    };
    override = (url) =>
      url.startsWith("/api/audit/exports/history")
        ? response(history([changed, invalid]))
        : undefined;
    await start();
    expect(
      within(screen.getByRole("table", { name: "导出历史记录" })).getAllByText(
        "已成功",
        { exact: true },
      ),
    ).toHaveLength(2);
    expect(screen.getByText("快照已失效，请重新创建导出")).toBeVisible();
    expect(screen.getByText("文件元数据无效，请重新创建导出")).toBeVisible();
    expect(screen.queryByText(/实际行数/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "下载 XLSX" }),
    ).not.toBeInTheDocument();
  });
  it("distinguishes a genuine empty page from failed reads and erases rows on denial", async () => {
    let current = response(history());
    override = (url) =>
      url.startsWith("/api/audit/exports/history") ? current : undefined;
    await start();
    current = response({ error: "forbidden" }, null, 403);
    clicks("刷新导出历史");
    await screen.findByRole("alert");
    expect(screen.queryByText(exportFilename(id))).not.toBeInTheDocument();
    expect(
      screen.queryByText("当前页没有符合条件的可见导出。"),
    ).not.toBeInTheDocument();
    current = response(history([]));
    clicks("刷新导出历史");
    expect(
      await screen.findByText("当前页没有符合条件的可见导出。"),
    ).toBeVisible();
  });
  it("clears old account rows and aborts ignored late history responses on actor switch", async () => {
    let resolve!: (value: Response) => void;
    const mounted = await start();
    override = (url) =>
      url.startsWith("/api/audit/exports/history")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    clicks("刷新导出历史");
    const oldResolve = resolve,
      oldRequest = requests("history").at(-1)!;
    override = (url) =>
      url.startsWith("/api/audit/exports/history")
        ? response(history([]), "2")
        : undefined;
    mounted.rerender(
      <AuditWorkspace
        profile={{ ...profile, ID: 2, csrfToken: "another-session" }}
        sessionChanged={vi.fn()}
      />,
    );
    expect(screen.queryByText(exportFilename(id))).not.toBeInTheDocument();
    expect(oldRequest[1].signal.aborted).toBe(true);
    await screen.findByText("当前页没有符合条件的可见导出。");
    await act(async () => oldResolve(response(history())));
    expect(screen.queryByText(exportFilename(id))).not.toBeInTheDocument();
  });
  it("rejects cross-tab actor changes and expired sessions without showing an empty success", async () => {
    const changed = vi.fn();
    const mounted = await start(changed);
    override = (url) =>
      url.startsWith("/api/audit/exports/history")
        ? response(history(), "2")
        : undefined;
    clicks("刷新导出历史");
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
    expect(screen.queryByText(exportFilename(id))).not.toBeInTheDocument();
    mounted.unmount();
    override = (url) =>
      url.startsWith("/api/audit/exports/history")
        ? response({ error: "unauthenticated" }, null, 401)
        : undefined;
    render(<AuditWorkspace profile={profile} sessionChanged={changed} />);
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(2));
    expect(
      screen.queryByText("当前页没有符合条件的可见导出。"),
    ).not.toBeInTheDocument();
  });
  it("polls nonterminal persisted history but rejects late results after detail navigation", async () => {
    let resolve!: (value: Response) => void,
      pending = false;
    override = (url) =>
      url.startsWith("/api/audit/exports/history")
        ? pending
          ? new Promise((r) => {
              resolve = r;
            })
          : response(
              history([
                row("retry_wait", { nextAttemptAt: "2026-10-07T00:02:00Z" }),
              ]),
            )
        : undefined;
    await start();
    expect(
      within(screen.getByRole("table", { name: "导出历史记录" })).getByText(
        "等待业务重试",
        { exact: true },
      ),
    ).toBeVisible();
    expect(screen.getByText(/下次重试（UTC）/)).toBeVisible();
    pending = true;
    await waitFor(() => expect(requests("history")).toHaveLength(2), {
      timeout: 2500,
    });
    clicks("查看导出详情");
    await screen.findByLabelText("导出实际结果");
    expect(requests("history")[1][1].signal.aborted).toBe(true);
    await act(async () => resolve(response(history([row("cancelled")]))));
    expect(
      screen.queryByRole("table", { name: "导出历史记录" }),
    ).not.toBeInTheDocument();
  });
  it("restores history and exact detail routes on reload and Back/Forward under StrictMode", async () => {
    const mounted = render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
    await screen.findByRole("table", { name: "导出历史记录" });
    clicks("查看导出详情");
    await screen.findByLabelText("导出实际结果");
    expect(window.location.hash).toBe(`#audit/detail/${id}`);
    mounted.unmount();
    render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
    await screen.findByLabelText("导出实际结果");
    act(() => {
      window.history.replaceState({}, "", "#audit/history");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("table", { name: "导出历史记录" });
    act(() => {
      window.history.replaceState({}, "", `#audit/detail/${id}`);
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByLabelText("导出实际结果");
    expect(
      screen.queryByRole("table", { name: "导出历史记录" }),
    ).not.toBeInTheDocument();
  });
  it("ignores late bytes after switching actors before starting the browser download", async () => {
    window.history.replaceState({}, "", `#audit/detail/${id}`);
    let resolve!: (value: Response) => void;
    override = (url) =>
      url.startsWith("/api/audit/exports/download")
        ? new Promise((r) => {
            resolve = r;
          })
        : undefined;
    const anchor = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    const mounted = render(
      <AuditWorkspace profile={profile} sessionChanged={vi.fn()} />,
    );
    await screen.findByRole("button", { name: "下载 XLSX" });
    clicks("下载 XLSX");
    mounted.rerender(
      <AuditWorkspace
        profile={{ ...profile, ID: 2 }}
        sessionChanged={vi.fn()}
      />,
    );
    expect(requests("download")[0][1].signal.aborted).toBe(true);
    const bytes = response({});
    bytes.headers.set(
      "Content-Type",
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    );
    bytes.headers.set(
      "Content-Disposition",
      `attachment; filename="${exportFilename(id)}"`,
    );
    bytes.blob = async () => ({ size: 20 }) as Blob;
    await act(async () => resolve(bytes));
    expect(anchor).not.toHaveBeenCalled();
  });
});
