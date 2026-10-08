import { useState } from "react";
import { ApiError } from "./api";
import {
  ErrorNotice,
  Field,
  FormActions,
  ProofFields,
  getProof,
  readValues,
} from "./access-common";
import {
  auditAPI,
  auditColumns,
  type AuditColumn,
  type AuditFilter,
  type ColumnCatalogue,
} from "./audit-api";
import {
  useAuditMutation,
  useAuditRead,
  type AuditContext,
} from "./audit-common";
import {
  auditIntent,
  attemptedAuditIntent,
  beginAuditIntent,
  discardAuditIntent,
} from "./audit-intent";
import { exportFilename } from "./audit-history";
import { terminal, stateLabels } from "./task-api";

export function AuditExportForm({
  context,
  filter,
  done,
}: {
  context: AuditContext;
  filter: AuditFilter;
  done: (id?: string, notice?: string) => void;
}) {
  const catalogue = useAuditRead(
    "columns",
    (signal) => auditAPI.columns(signal),
    context.sessionChanged,
  );
  return (
    <>
      <h3>创建审计导出</h3>
      <p className="warning">
        XLSX 最多导出 100,000 条记录，按 10,000 条分批写入。选择 1 至 8
        列；空结果将生成只有表头的文件。文件在异步任务成功后才能下载。
      </p>
      <ErrorNotice error={catalogue.error} />
      {catalogue.busy && <p role="status">正在读取导出列…</p>}
      {catalogue.error && (
        <button className="secondary" onClick={catalogue.refresh}>
          重新读取导出列
        </button>
      )}
      {catalogue.data ? (
        <ExportForm
          context={context}
          filter={filter}
          catalogue={catalogue.data}
          done={done}
        />
      ) : (
        <button className="secondary" onClick={() => done()}>
          返回审计列表
        </button>
      )}
    </>
  );
}
function ExportForm({
  context,
  filter,
  catalogue,
  done,
}: {
  context: AuditContext;
  filter: AuditFilter;
  catalogue: ColumnCatalogue;
  done: (id?: string, notice?: string) => void;
}) {
  const [intent] = useState(
    () =>
      auditIntent(context.profile) ??
      beginAuditIntent(context.profile, {
        ...filter,
        selectColumn: catalogue.defaultColumns,
      }),
  );
  const [columns, setColumns] = useState<AuditColumn[]>(
      intent.input.selectColumn,
    ),
    [proofVersion, setProofVersion] = useState(0),
    [attempted, setAttempted] = useState(intent.attempted);
  const mutation = useAuditMutation(context.sessionChanged);
  const allowed =
    intent.input.visibility === "visible" &&
    context.can("POST /api/audit/exports") &&
    context.can("POST /api/tasks/submit");
  return (
    <form
      onSubmit={(event) => {
        const values = readValues(event);
        if (mutation.busy || !allowed) return;
        if (
          columns.length < 1 ||
          columns.length > 8 ||
          columns.some((c) => !auditColumns.includes(c))
        ) {
          mutation.setError("请选择 1 至 8 个导出列；空选择不能提交。");
          return;
        }
        const proof = getProof(values);
        if (!proof) {
          mutation.setError("请输入有效的操作者密码及六位未使用验证码。");
          return;
        }
        if (!intent.attempted)
          intent.input = { ...intent.input, selectColumn: [...columns] };
        attemptedAuditIntent(context.profile, intent);
        setAttempted(true);
        setProofVersion((v) => v + 1);
        void mutation.run(
          (signal) =>
            auditAPI.submit(
              intent.input,
              proof,
              context.profile.csrfToken,
              signal,
            ),
          (value) => {
            discardAuditIntent(context.profile);
            done(
              value.task.taskUUID,
              value.replayed
                ? "已确认原导出任务，未重复创建。"
                : "服务器已登记导出任务，正在核对执行状态。",
            );
          },
          () => {},
        );
      }}
    >
      <ErrorNotice error={mutation.error} />
      <dl aria-label="导出筛选摘要">
        <div>
          <dt>时间（UTC，起点包含 / 终点不包含）</dt>
          <dd>
            {intent.input.startTm || "不限"} 至 {intent.input.endTm || "不限"}
          </dd>
        </div>
        <div>
          <dt>关键词</dt>
          <dd>{intent.input.keyword || "不限"}</dd>
        </div>
        <div>
          <dt>事件</dt>
          <dd>{intent.input.filterEvent.join("、") || "所有事件"}</dd>
        </div>
        <div>
          <dt>类型</dt>
          <dd>{intent.input.logTypeList.join("、") || "所有类型"}</dd>
        </div>
        <div>
          <dt>可见性 / 排序</dt>
          <dd>
            {
              { visible: "未隐藏记录", hidden: "已隐藏记录", all: "全部记录" }[
                intent.input.visibility
              ]
            }{" "}
            / {intent.input.createSort === -1 ? "时间降序" : "时间升序"}
          </dd>
        </div>
      </dl>
      <fieldset disabled={mutation.busy || attempted} className="audit-columns">
        <legend>选择导出列（1 至 8 列）</legend>
        {catalogue.columns.map((column) => (
          <label key={column.prop}>
            <input
              type="checkbox"
              checked={columns.includes(column.prop)}
              onChange={(event) =>
                setColumns(
                  event.target.checked
                    ? [...columns, column.prop]
                    : columns.filter((c) => c !== column.prop),
                )
              }
            />
            导出列：{column.label}
          </label>
        ))}
      </fieldset>
      <p>已选 {columns.length} / 8 列；文件按所选列顺序导出。</p>
      <Field
        label="导出幂等键"
        help="本标签页保留原筛选、列与此键以核对不确定提交；密码、验证码和令牌不会保存。"
      >
        <input readOnly value={intent.input.idempotencyKey} />
      </Field>
      {attempted && (
        <p className="warning">
          原提交可能已经生效。重试会使用相同筛选、列和幂等键，并需要新的验证码；停止等待不能取消服务器任务。退出登录或关闭标签页后，请先核对已有任务。
        </p>
      )}
      {allowed && (
        <fieldset key={proofVersion} disabled={mutation.busy}>
          <ProofFields />
        </fieldset>
      )}
      <FormActions
        busy={mutation.busy}
        disabled={!allowed || columns.length === 0}
        cancel={() => {
          if (!intent.attempted) discardAuditIntent(context.profile);
          done(
            undefined,
            intent.attempted
              ? "已停止等待；请使用原幂等键继续核对未确认的导出。"
              : "",
          );
        }}
      >
        {attempted ? "使用原幂等键确认导出" : "确认创建导出"}
      </FormActions>
    </form>
  );
}
export function AuditExportDetail({
  context,
  id,
  done,
  history,
}: {
  context: AuditContext;
  id: string;
  done: () => void;
  history: () => void;
}) {
  const read = useAuditRead(
    id,
    (signal) => auditAPI.detail(id, signal, context.profile.ID),
    context.sessionChanged,
    (value) => !terminal(value.task.state),
  );
  const download = useAuditMutation(context.sessionChanged),
    [downloaded, setDownloaded] = useState(false),
    [stale, setStale] = useState(false);
  const task = read.data?.task;
  return (
    <>
      <h3>审计导出任务</h3>
      <div className="actions compact">
        <button className="secondary" onClick={done}>
          返回审计列表
        </button>
        <button className="secondary" onClick={history}>
          返回导出历史
        </button>
        <button className="secondary" onClick={read.refresh}>
          刷新导出状态
        </button>
      </div>
      <ErrorNotice error={read.error || download.error} />
      {read.busy && <p role="status">正在读取导出任务…</p>}
      {task && (
        <>
          <p className="task-state" data-state={task.state} role="status">
            {stateLabels[task.state]}
          </p>
          {!terminal(task.state) && (
            <p>
              正在自动核对实际执行状态。停止等待或离开页面不会撤销服务器任务。
            </p>
          )}
          <dl>
            <div>
              <dt>任务 ID</dt>
              <dd>{task.taskUUID}</dd>
            </div>
            <div>
              <dt>创建时间（UTC）</dt>
              <dd>{task.createdAt}</dd>
            </div>
            <div>
              <dt>尝试次数</dt>
              <dd>
                {task.attempt}/{task.maxAttempts}
              </dd>
            </div>
            <div>
              <dt>快照时间（UTC）</dt>
              <dd>{read.data?.snapshotAt || "尚未生成"}</dd>
            </div>
            <div>
              <dt>实际导出行数</dt>
              <dd>{read.data?.rowCount ?? "尚未确认"}</dd>
            </div>
            <div>
              <dt>错误代码</dt>
              <dd>{task.error || "无"}</dd>
            </div>
          </dl>
          <label className="task-progress">
            持久化进度：{task.progress}%
            <progress
              aria-label="导出持久化进度"
              max={100}
              value={task.progress}
            />
          </label>
          <pre className="task-json" aria-label="导出实际结果">
            {JSON.stringify(task.result, null, 2)}
          </pre>
          {read.data?.downloadReady && read.data.rowCount === 0 && (
            <p role="status">
              筛选结果为空；XLSX 仅包含所选列的表头，没有数据行。
            </p>
          )}
          {task.state === "succeeded" && !read.data?.downloadReady && (
            <p className="warning">
              任务已结束，当前没有可下载的有效文件。可见性或权限变化后需要重新创建导出。
            </p>
          )}
          {read.data?.downloadReady &&
            !stale &&
            context.can("GET /api/audit/exports/download") && (
              <button
                disabled={download.busy}
                onClick={() => {
                  setDownloaded(false);
                  void download.run(
                    (signal) =>
                      auditAPI.download(id, signal, context.profile.ID),
                    (blob) => {
                      const url = URL.createObjectURL(blob),
                        anchor = document.createElement("a");
                      anchor.href = url;
                      anchor.download = exportFilename(id);
                      anchor.hidden = true;
                      document.body.appendChild(anchor);
                      anchor.click();
                      anchor.remove();
                      setTimeout(() => URL.revokeObjectURL(url), 1000);
                      setDownloaded(true);
                    },
                    (error) => {
                      if (
                        error instanceof ApiError &&
                        error.code === "export_snapshot_changed"
                      )
                        setStale(true);
                      read.refresh();
                    },
                  );
                }}
              >
                {download.busy ? "正在下载…" : "下载 XLSX"}
              </button>
            )}
          {downloaded && (
            <p role="status">
              已收到通过当前权限验证的 XLSX 文件，并交由浏览器下载。
            </p>
          )}
          <p className="muted">
            下载时会重新验证登录会话、当前权限和记录可见性。隐藏或恢复记录后，旧快照会失效，请重新创建导出。
          </p>
        </>
      )}
    </>
  );
}
