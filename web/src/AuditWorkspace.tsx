import { useEffect, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, validText, type Permission } from "./access-api";
import {
  ErrorNotice,
  Field,
  FormActions,
  Pagination,
  ProofFields,
  getProof,
  readValues,
} from "./access-common";
import {
  auditAPI,
  auditOperations,
  auditQuery,
  emptyFilter,
  type AuditFilter,
  type AuditRow,
} from "./audit-api";
import {
  useAuditRead,
  useAuditMutation,
  type AuditContext,
} from "./audit-common";
import { auditIntent } from "./audit-intent";
import { AuditExportForm, AuditExportDetail } from "./AuditExports";
import AuditHistory, { AuditHistorySummary } from "./AuditHistory";
import { emptyHistoryQuery, validExportID } from "./audit-history";
export { discardAuditIntent } from "./audit-intent";

type View =
  | { type: "list" }
  | { type: "history" }
  | { type: "export" }
  | { type: "detail"; id: string }
  | { type: "visibility"; action: "delete" | "restore"; rows: AuditRow[] };
function readView(): View {
  if (window.location.hash === "#audit/history") return { type: "history" };
  const detail = /^#audit\/detail\/([^/?#]+)$/.exec(window.location.hash);
  if (detail && validExportID(detail[1]))
    return { type: "detail", id: detail[1] };
  if (window.location.hash === "#audit/export") return { type: "export" };
  return { type: "list" };
}
// A session change synchronously unmounts all metadata, polls and downloads,
// including when this workspace is rendered independently of App.
export default function AuditWorkspace(props: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  return (
    <AuditWorkspaceSession
      key={`${props.profile.ID}:${props.profile.csrfToken}`}
      {...props}
    />
  );
}
function AuditWorkspaceSession({
  profile,
  sessionChanged,
}: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  const [view, setView] = useState<View>(readView),
    [notice, setNotice] = useState(""),
    [revision, setRevision] = useState(0),
    [filter, setFilter] = useState<AuditFilter>(emptyFilter),
    [historyFilter, setHistoryFilter] = useState(emptyHistoryQuery);
  useEffect(() => {
    const navigate = () => {
      setView(readView());
      setNotice("");
      setRevision((n) => n + 1);
    };
    window.addEventListener("popstate", navigate);
    window.addEventListener("hashchange", navigate);
    return () => {
      window.removeEventListener("popstate", navigate);
      window.removeEventListener("hashchange", navigate);
    };
  }, []);
  const gate = useAuditRead(
    profile.csrfToken,
    async (signal) => {
      const [menu, checks] = await Promise.all([
        accessRequest<{ menu: Permission[] }>("/menu", signal),
        accessRequest<{ results: boolean[] }>(
          "/check",
          signal,
          { paths: auditOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== auditOperations.length ||
        checks.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return {
        readable: menu.menu.some((p) => p.mark === "audit" && p.auth.readable),
        results: checks.results,
      };
    },
    sessionChanged,
  );
  const context: AuditContext = {
    profile,
    sessionChanged,
    can: (operation) =>
      gate.data?.results[auditOperations.indexOf(operation)] === true,
  };
  const open = (next: View, message = "") => {
    setView(next);
    setNotice(message);
    setRevision((n) => n + 1);
    window.history.pushState(
      {},
      "",
      `#audit/${next.type}${next.type === "detail" ? `/${encodeURIComponent(next.id)}` : ""}`,
    );
  };
  const pending = auditIntent(profile);
  return (
    <div className="audit-workspace">
      <h2>操作审计</h2>
      <p className="muted">
        查询当前授权范围内的真实平台操作记录。历史未采集的用户、IP
        和请求信息会明确标为缺失。
      </p>
      <ErrorNotice error={gate.error} />
      {gate.busy && <p role="status">正在确认审计权限…</p>}
      {gate.error && (
        <button className="secondary" onClick={gate.refresh}>
          重新读取审计权限
        </button>
      )}
      {gate.data && (!gate.data.readable || !context.can("GET /api/audit")) && (
        <p role="status">当前账户没有操作审计读取权限。</p>
      )}
      {gate.data?.readable && context.can("GET /api/audit") && (
        <>
          {notice && (
            <div className="task-notice" role="status">
              {notice}
            </div>
          )}
          {pending?.attempted && view.type === "list" && (
            <div className="warning" role="status">
              有一项导出提交结果尚未确认。请保留原筛选、列和幂等键核对。
              {context.can("POST /api/audit/exports") &&
                context.can("POST /api/tasks/submit") && (
                  <button
                    className="secondary"
                    onClick={() => open({ type: "export" })}
                  >
                    继续核对未确认的导出
                  </button>
                )}
            </div>
          )}
          {view.type === "list" &&
            context.can("GET /api/audit/exports/history") && (
              <AuditHistorySummary
                key={`history-${revision}`}
                context={context}
                open={() => open({ type: "history" })}
              />
            )}
          {view.type === "history" &&
            (context.can("GET /api/audit/exports/history") ? (
              <AuditHistory
                key={revision}
                context={context}
                query={historyFilter}
                change={setHistoryFilter}
                open={(id) => open({ type: "detail", id })}
                done={() => open({ type: "list" })}
              />
            ) : (
              <>
                <p role="status">当前账户没有导出历史读取权限。</p>
                <button
                  className="secondary"
                  onClick={() => open({ type: "list" })}
                >
                  返回审计列表
                </button>
              </>
            ))}
          {view.type === "list" && (
            <AuditList
              key={revision}
              context={context}
              filter={filter}
              setFilter={setFilter}
              blocked={!!pending?.attempted}
              open={open}
            />
          )}
          {view.type === "visibility" && (
            <VisibilityForm
              key={revision}
              context={context}
              action={view.action}
              rows={view.rows}
              done={(message) => open({ type: "list" }, message)}
            />
          )}
          {view.type === "export" && (
            <AuditExportForm
              key={revision}
              context={context}
              filter={filter}
              done={(id, message) =>
                open(id ? { type: "detail", id } : { type: "list" }, message)
              }
            />
          )}
          {view.type === "detail" && (
            <AuditExportDetail
              key={`${revision}:${view.id}`}
              context={context}
              id={view.id}
              done={() => open({ type: "list" })}
              history={() => open({ type: "history" })}
            />
          )}
        </>
      )}
    </div>
  );
}
function AuditList({
  context,
  filter,
  setFilter,
  blocked,
  open,
}: {
  context: AuditContext;
  filter: AuditFilter;
  setFilter: (filter: AuditFilter) => void;
  blocked: boolean;
  open: (view: View, message?: string) => void;
}) {
  const [pageIdx, setPage] = useState(1),
    [pageSize, setSize] = useState(20),
    [selected, setSelected] = useState<string[]>([]),
    [formError, setError] = useState("");
  const query = auditQuery(filter, pageIdx, pageSize);
  const read = useAuditRead(
    query,
    (signal) => auditAPI.list(query, signal),
    context.sessionChanged,
  );
  const types = useAuditRead(
    "types",
    (signal) => auditAPI.types(signal),
    context.sessionChanged,
  );
  const targets =
    read.data?.List.filter(
      (row) =>
        selected.includes(row.ID) &&
        row.deletable &&
        !["audit", "credential_use", "operational_log"].includes(row.source),
    ) ?? [];
  const hidden = targets.filter((row) => row.deleted),
    visible = targets.filter((row) => !row.deleted);
  const clearSelection = () => setSelected([]);
  return (
    <>
      <div className="actions compact">
        {context.can("POST /api/audit/exports") &&
          context.can("POST /api/tasks/submit") &&
          context.can("GET /api/audit/columns") &&
          context.can("GET /api/audit/exports/detail") && (
            <button
              disabled={
                blocked ||
                read.busy ||
                !read.data ||
                filter.visibility !== "visible"
              }
              onClick={() => open({ type: "export" })}
            >
              当前筛选导出
            </button>
          )}
        <button
          className="secondary"
          onClick={() => {
            clearSelection();
            read.refresh();
            types.refresh();
          }}
        >
          刷新审计列表
        </button>
      </div>
      {filter.visibility !== "visible" && (
        <p className="warning">
          当前仅支持导出未隐藏记录。请切换为未隐藏记录并应用筛选后导出。
        </p>
      )}
      {context.can("GET /api/audit/exports/detail") && (
        <form
          className="filters"
          onSubmit={(event) => {
            const values = readValues(event);
            if (!validExportID(values.taskUUID)) {
              setError("请输入完整的导出任务 UUID。");
              return;
            }
            open({ type: "detail", id: values.taskUUID });
          }}
        >
          <Field
            label="导出任务 ID"
            help="输入已提交的任务 ID，读取持久化进度及可下载文件"
          >
            <input name="taskUUID" required pattern="[0-9a-f-]{36}" />
          </Field>
          <button className="secondary">查看导出任务</button>
        </form>
      )}
      <form
        key={JSON.stringify(filter)}
        className="filters"
        onSubmit={(event) => {
          event.preventDefault();
          const values = new FormData(event.currentTarget);
          const text = (key: string) => String(values.get(key) ?? "");
          const utc = (key: string) => {
            const value = text(key);
            return value ? new Date(`${value}Z`).toISOString() : "";
          };
          let startTm: string, endTm: string;
          try {
            startTm = utc("startTm");
            endTm = utc("endTm");
          } catch {
            setError("请输入有效的 UTC 时间。");
            return;
          }
          if (
            !validText(text("keyword"), 50) ||
            (startTm && endTm && startTm >= endTm)
          ) {
            setError(
              "关键词最多 50 个字符；开始时间必须早于结束时间，结束边界不包含在结果内。",
            );
            return;
          }
          setError("");
          clearSelection();
          setPage(1);
          setSize(Number(text("pageSize")));
          setFilter({
            startTm,
            endTm,
            keyword: text("keyword"),
            filterEvent: values.getAll("filterEvent").map(String),
            logTypeList: values.getAll("logTypeList").map(Number),
            createSort: Number(text("createSort")),
            visibility: text("visibility") as AuditFilter["visibility"],
          });
        }}
      >
        <div className="form-grid">
          <Field label="开始时间（UTC）" help="包含此时刻；留空表示不限起点">
            <input
              type="datetime-local"
              name="startTm"
              step="1"
              defaultValue={filter.startTm.slice(0, 19)}
            />
          </Field>
          <Field label="结束时间（UTC）" help="不包含此时刻；留空表示不限终点">
            <input
              type="datetime-local"
              name="endTm"
              step="1"
              defaultValue={filter.endTm.slice(0, 19)}
            />
          </Field>
          <Field
            label="审计关键词"
            help="搜索记录中实际采集的登录用户或 IP，最多 50 个字符"
          >
            <input
              name="keyword"
              maxLength={50}
              defaultValue={filter.keyword}
            />
          </Field>
          <Field label="审计可见性">
            <select name="visibility" defaultValue={filter.visibility}>
              <option value="visible">未隐藏记录</option>
              <option value="hidden">已隐藏记录</option>
              <option value="all">全部记录</option>
            </select>
          </Field>
          <Field label="审计排序">
            <select name="createSort" defaultValue={filter.createSort}>
              <option value={-1}>时间降序</option>
              <option value={1}>时间升序</option>
            </select>
          </Field>
          <Field label="每页审计数">
            <select name="pageSize" defaultValue={pageSize}>
              {[10, 20, 30, 40, 50, 100].map((n) => (
                <option key={n}>{n}</option>
              ))}
              <option value={-1}>全部（最多 1,000 条）</option>
            </select>
          </Field>
          <Field label="审计类型筛选" help="可多选；未选择表示所有类型">
            <select
              key={types.data ? "ready" : "loading"}
              name="logTypeList"
              multiple
              defaultValue={filter.logTypeList.map(String)}
              disabled={!types.data}
            >
              {types.data?.List.map((item) => (
                <option key={item.logType} value={item.logType}>
                  {item.logTypeName}
                </option>
              ))}
            </select>
          </Field>
          <Field
            label="审计事件筛选"
            help="可多选；候选值来自当前授权范围内的事件"
          >
            <select
              key={types.data ? "ready" : "loading"}
              name="filterEvent"
              multiple
              defaultValue={filter.filterEvent}
              disabled={!types.data}
            >
              {[
                ...new Set([
                  ...(types.data?.events ?? []),
                  ...filter.filterEvent,
                ]),
              ].map((event) => (
                <option key={event}>{event}</option>
              ))}
            </select>
          </Field>
        </div>
        <div className="actions compact">
          <button disabled={!types.data}>筛选审计</button>
          <button
            className="secondary"
            type="reset"
            onClick={() => {
              setError("");
              setPage(1);
              setSize(20);
              clearSelection();
              setFilter(emptyFilter());
            }}
          >
            清除审计筛选
          </button>
        </div>
      </form>
      <ErrorNotice error={formError || read.error || types.error} />
      {read.busy && <p role="status">正在读取审计记录…</p>}
      {read.data && (
        <>
          <div className="actions compact">
            {context.can("POST /api/audit/delete") && (
              <button
                disabled={
                  visible.length === 0 || hidden.length > 0 || read.busy
                }
                onClick={() =>
                  open({ type: "visibility", action: "delete", rows: visible })
                }
              >
                隐藏所选记录
              </button>
            )}
            {context.can("POST /api/audit/restore") && (
              <button
                className="secondary"
                disabled={
                  hidden.length === 0 || visible.length > 0 || read.busy
                }
                onClick={() =>
                  open({ type: "visibility", action: "restore", rows: hidden })
                }
              >
                恢复所选记录
              </button>
            )}
            <span>已选择 {targets.length} 条；每次最多 100 条</span>
          </div>
          <div className="table-scroll">
            <table>
              <caption>操作审计记录</caption>
              <thead>
                <tr>
                  <th>选择</th>
                  <th>ID / 可见性</th>
                  <th>登录用户 / 用户 ID</th>
                  <th>登录 IP</th>
                  <th>类型 / 事件</th>
                  <th>结果</th>
                  <th>审计时间（UTC）</th>
                  <th>事件与历史元数据</th>
                </tr>
              </thead>
              <tbody>
                {read.data.List.map((row) => (
                  <tr key={row.ID}>
                    <td>
                      {(context.can("POST /api/audit/delete") ||
                        context.can("POST /api/audit/restore")) && (
                        <input
                          type="checkbox"
                          aria-label={`选择 ${row.ID}`}
                          checked={selected.includes(row.ID)}
                          disabled={
                            !row.deletable ||
                            [
                              "audit",
                              "credential_use",
                              "operational_log",
                            ].includes(row.source) ||
                            (!selected.includes(row.ID) &&
                              selected.length >= 100)
                          }
                          onChange={(event) =>
                            setSelected(
                              event.target.checked
                                ? [...selected, row.ID]
                                : selected.filter((id) => id !== row.ID),
                            )
                          }
                        />
                      )}
                    </td>
                    <th scope="row">
                      {row.ID}
                      <small className="block">
                        {row.deleted ? "已隐藏" : "可见"}
                        {[
                          "audit",
                          "credential_use",
                          "operational_log",
                        ].includes(row.source) && " · 受保护控制记录"}
                      </small>
                    </th>
                    <td>
                      {row.loginUser ?? "未记录（历史数据缺失）"}
                      <small className="block">
                        用户 ID：{row.userId ?? "未记录"}
                      </small>
                    </td>
                    <td>{row.sourceIp ?? "未记录（历史数据缺失）"}</td>
                    <td>
                      {row.logTypeName}
                      <small className="block">{row.event}</small>
                    </td>
                    <td>
                      {row.availability.eventResult
                        ? row.eventResult
                        : "未记录（历史数据缺失）"}
                    </td>
                    <td>{row.CreateTm}</td>
                    <td>
                      <details>
                        <summary>查看元数据 {row.ID}</summary>
                        <p>
                          来源：{row.source} · 域：
                          {row.domainId ?? "未记录 / 平台操作"}
                        </p>
                        <pre className="task-json">{row.eventArgs}</pre>
                        <MetadataAvailability row={row} />
                      </details>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {read.data.List.length === 0 && (
            <p role="status">没有符合筛选条件的审计记录。</p>
          )}
          <Pagination
            page={read.data.page}
            exhausted={read.data.exhausted}
            busy={read.busy}
            change={(page) => {
              clearSelection();
              setPage(page);
            }}
          />
        </>
      )}
    </>
  );
}
function MetadataAvailability({ row }: { row: AuditRow }) {
  const labels = {
    loginUser: "登录用户",
    sourceIp: "登录IP",
    path: "请求路径",
    requestId: "请求标识",
    eventResult: "事件结果",
  };
  const missing = (Object.keys(labels) as (keyof typeof labels)[])
    .filter((key) => !row.availability[key])
    .map((key) => labels[key]);
  return (
    <p>
      {missing.length
        ? `未记录：${missing.join("、")}。历史缺失信息不会用当前账户信息补写。`
        : "当前记录已采集全部请求元数据。"}
    </p>
  );
}
function VisibilityForm({
  context,
  action,
  rows,
  done,
}: {
  context: AuditContext;
  action: "delete" | "restore";
  rows: AuditRow[];
  done: (message?: string) => void;
}) {
  const mutation = useAuditMutation(context.sessionChanged),
    [proofVersion, setProofVersion] = useState(0),
    [attempted, setAttempted] = useState(false),
    [reconcile, setReconcile] = useState(false);
  const read = useAuditRead(
    String(reconcile),
    (signal) =>
      reconcile
        ? auditAPI.list(
            auditQuery({ ...emptyFilter(), visibility: "all" }, 1, 100),
            signal,
          )
        : Promise.resolve(null),
    context.sessionChanged,
  );
  const label = action === "delete" ? "隐藏" : "恢复";
  return (
    <form
      onSubmit={(event) => {
        const values = readValues(event);
        if (mutation.busy) return;
        const proof = getProof(values);
        if (!proof || !values.reason.trim() || !validText(values.reason, 500)) {
          mutation.setError(
            "请输入有效的操作者密码、新验证码及 1 至 500 字的操作原因。",
          );
          return;
        }
        setAttempted(true);
        setProofVersion((v) => v + 1);
        void mutation.run(
          (signal) =>
            auditAPI.visibility(
              action,
              rows.map((row) => row.ID),
              values.reason,
              proof,
              context.profile.csrfToken,
              signal,
            ),
          (value) =>
            done(
              `服务器已确认${label} ${value.changed} 条记录。已重新读取审计列表。`,
            ),
          () => {
            setReconcile(true);
            read.refresh();
          },
        );
      }}
    >
      <h3>{label}审计记录</h3>
      <p className="warning">
        {label}所选 {rows.length}{" "}
        条记录。隐藏仅改变查询可见性，原始记录保留且可恢复；控制记录始终保留。此操作会使先前导出快照失效。
      </p>
      <ul>
        {rows.map((row) => (
          <li key={row.ID}>
            {row.ID} · {row.event}
          </li>
        ))}
      </ul>
      <ErrorNotice error={mutation.error || read.error} />
      {reconcile && read.data && (
        <p role="status">
          已从服务器重新读取最近 {read.data.List.length}{" "}
          条记录。请返回列表核对目标的当前可见性，不要根据连接错误推断操作失败。
        </p>
      )}
      <fieldset disabled={mutation.busy}>
        <Field label="操作原因">
          <textarea name="reason" maxLength={500} required />
        </Field>
      </fieldset>
      <fieldset key={proofVersion} disabled={mutation.busy}>
        <ProofFields />
      </fieldset>
      <FormActions
        busy={mutation.busy}
        cancel={() =>
          done(
            attempted
              ? "已停止等待；操作可能已生效，请在列表核对实际可见性。"
              : "",
          )
        }
      >
        确认{label}
      </FormActions>
    </form>
  );
}
