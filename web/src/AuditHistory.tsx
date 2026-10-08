import { useState } from "react";
import { ErrorNotice, Field, Pagination } from "./access-common";
import { auditAPI } from "./audit-api";
import { useAuditRead, type AuditContext } from "./audit-common";
import {
  emptyHistoryQuery,
  historyInputUTC,
  historyQuery,
  type DownloadStatus,
  type ExportHistoryQuery,
} from "./audit-history";
import { stateLabels, taskStates, terminal, type TaskState } from "./task-api";

const downloadLabels: Record<DownloadStatus, string> = {
  eligible: "可申请下载；下载时重新核验权限和文件完整性",
  not_ready: "尚无已发布文件",
  snapshot_changed: "快照已失效，请重新创建导出",
  artifact_invalid: "文件元数据无效，请重新创建导出",
};
export function AuditHistorySummary({
  context,
  open,
}: {
  context: AuditContext;
  open: () => void;
}) {
  const read = useAuditRead(
    "history-summary",
    (signal) =>
      auditAPI.history(emptyHistoryQuery(), signal, context.profile.ID),
    context.sessionChanged,
  );
  return (
    <section aria-label="已有导出">
      <button className="secondary" onClick={open}>
        导出历史
      </button>
      {read.busy && <p role="status">正在读取已有导出…</p>}
      {read.data && (
        <p>
          当前可见的本人导出：{read.data.page.total}{" "}
          项。可在导出历史中查看持久化进度和文件，无需记住任务 ID。
        </p>
      )}
      <ErrorNotice error={read.error} />
      {read.error && (
        <button className="secondary" onClick={read.refresh}>
          重新读取已有导出
        </button>
      )}
    </section>
  );
}
export default function AuditHistory({
  context,
  query,
  change,
  open,
  done,
}: {
  context: AuditContext;
  query: ExportHistoryQuery;
  change: (query: ExportHistoryQuery) => void;
  open: (id: string) => void;
  done: () => void;
}) {
  const [formError, setError] = useState("");
  const read = useAuditRead(
    historyQuery(query),
    (signal) => auditAPI.history(query, signal, context.profile.ID),
    context.sessionChanged,
    (value) => value.list.some((row) => !terminal(row.state)),
  );
  return (
    <>
      <h3>导出历史</h3>
      <p className="muted">
        这里只显示当前授权范围内本人的 Audit 审计 XLSX
        导出。创建时间筛选与审计记录筛选相互独立；文件名是预期名称，不代表文件已经生成。
      </p>
      <div className="actions compact">
        <button className="secondary" onClick={done}>
          返回审计列表
        </button>
        <button className="secondary" onClick={read.refresh}>
          刷新导出历史
        </button>
      </div>
      <form
        key={JSON.stringify(query)}
        className="filters"
        onSubmit={(event) => {
          event.preventDefault();
          const values = new FormData(event.currentTarget);
          try {
            const next: ExportHistoryQuery = {
              pageIdx: 1,
              pageSize: Number(values.get("pageSize")),
              status: values.getAll("status").map(String) as TaskState[],
              startTm: historyInputUTC(String(values.get("startTm") ?? "")),
              endTm: historyInputUTC(String(values.get("endTm") ?? "")),
              sortTm: Number(values.get("sortTm")) as -1 | 1,
            };
            historyQuery(next);
            setError("");
            change(next);
          } catch {
            setError(
              "请输入有效的 UTC 创建时间；开始时间必须早于结束时间，结束边界不包含在结果内。",
            );
          }
        }}
      >
        <Field label="导出创建开始时间（UTC）">
          <input
            type="datetime-local"
            step="1"
            name="startTm"
            defaultValue={query.startTm.replace(/Z$/, "")}
          />
        </Field>
        <Field label="导出创建结束时间（UTC，不包含）">
          <input
            type="datetime-local"
            step="1"
            name="endTm"
            defaultValue={query.endTm.replace(/Z$/, "")}
          />
        </Field>
        <Field label="导出任务状态" help="可多选；不选表示所有状态">
          <select name="status" multiple defaultValue={query.status}>
            {taskStates.map((state) => (
              <option key={state} value={state}>
                {stateLabels[state]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="导出创建时间排序">
          <select name="sortTm" defaultValue={query.sortTm}>
            <option value="-1">最新在前</option>
            <option value="1">最早在前</option>
          </select>
        </Field>
        <Field label="每页导出数">
          <select name="pageSize" defaultValue={query.pageSize}>
            {[10, 20, 30, 40, 50].map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </Field>
        <button>筛选导出历史</button>
        <button
          type="button"
          className="secondary"
          onClick={() => {
            setError("");
            change(emptyHistoryQuery());
          }}
        >
          清空导出筛选
        </button>
      </form>
      <ErrorNotice error={formError || read.error} />
      {read.busy && <p role="status">正在读取导出历史…</p>}
      {read.data && (
        <>
          {!read.data.list.length && (
            <p role="status">当前页没有符合条件的可见导出。</p>
          )}
          <div className="table-scroll">
            <table aria-label="导出历史记录">
              <thead>
                <tr>
                  <th>预期文件名 / 任务 ID</th>
                  <th>模型 / 格式</th>
                  <th>任务状态 / 持久化进度</th>
                  <th>创建 / 更新时间（UTC）</th>
                  <th>尝试 / 错误</th>
                  <th>下载状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {read.data.list.map((row) => (
                  <tr key={row.taskUUID}>
                    <td>
                      {row.fileName}
                      <br />
                      <span className="muted">{row.taskUUID}</span>
                    </td>
                    <td>{row.modelType} / XLSX</td>
                    <td>
                      <span className="task-state" data-state={row.state}>
                        {stateLabels[row.state]}
                      </span>
                      <br />
                      <label>
                        {row.progress}%
                        <progress
                          aria-label={`导出进度 ${row.taskUUID}`}
                          max={100}
                          value={row.progress}
                        />
                      </label>
                    </td>
                    <td>
                      {row.createdAt}
                      <br />
                      {row.updatedAt}
                    </td>
                    <td>
                      {row.attempt} / {row.maxAttempts}
                      {row.nextAttemptAt && (
                        <p>下次重试（UTC）：{row.nextAttemptAt}</p>
                      )}
                      {row.error && <p>{row.error}</p>}
                    </td>
                    <td>
                      {downloadLabels[row.downloadStatus]}
                      {row.downloadReady && (
                        <p>
                          实际行数：{row.rowCount}；快照（UTC）：
                          {row.snapshotAt}
                        </p>
                      )}
                    </td>
                    <td>
                      {context.can("GET /api/audit/exports/detail") && (
                        <button
                          className="secondary"
                          onClick={() => open(row.taskUUID)}
                        >
                          查看导出详情
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <Pagination
            page={read.data.page}
            exhausted={read.data.exhausted}
            busy={read.busy}
            change={(pageIdx) => change({ ...query, pageIdx })}
          />
        </>
      )}
    </>
  );
}
