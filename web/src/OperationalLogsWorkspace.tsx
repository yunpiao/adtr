import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest } from "./access-api";
import {
  ErrorNotice,
  Field,
  Pagination,
  ProofFields,
  getProof,
  readValues,
} from "./access-common";
import { historyInputUTC, validExportID } from "./audit-history";
import { stateLabels, taskStates, terminal, type TaskState } from "./task-api";
import {
  bundleFilename,
  bundleQuery,
  emptyBundleQuery,
  emptyLogQuery,
  logAPI,
  logOperations,
  logQuery,
  type BundleDetail,
  type BundleQuery,
  type LogModule,
  type LogQuery,
  type Selection,
} from "./operational-log-api";
import {
  CoverageNotice,
  SelectionSummary,
  useLogMutation,
  useLogRead,
  type LogContext,
} from "./operational-log-common";
import {
  beginBundleIntent,
  bundleIntent,
  discardBundleIntent,
  markBundleAttempted,
  type BundleIntent,
} from "./operational-log-intent";

type View =
  | { type: "list" | "sources" | "history" | "bundle" }
  | { type: "detail"; id: string };
function readView(): View {
  const id = /^#operational-logs\/detail\/([^/?#]+)$/.exec(
    window.location.hash,
  )?.[1];
  if (id && validExportID(id)) return { type: "detail", id };
  const type = window.location.hash.split("/")[1];
  return {
    type:
      type === "sources" || type === "history" || type === "bundle"
        ? type
        : "list",
  };
}
export default function OperationalLogsWorkspace(props: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  return (
    <LogSession
      key={`${props.profile.ID}:${props.profile.csrfToken}`}
      {...props}
    />
  );
}
function LogSession({
  profile,
  sessionChanged,
}: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  const [view, setView] = useState<View>(readView),
    [revision, setRevision] = useState(0),
    [query, setQuery] = useState<LogQuery>(emptyLogQuery),
    [history, setHistory] = useState<BundleQuery>(emptyBundleQuery),
    [intent, setIntent] = useState<BundleIntent | undefined>(() =>
      bundleIntent(profile),
    );
  const gate = useLogRead(
    profile.csrfToken,
    async (signal) => {
      const result = await accessRequest<{ results: boolean[] }>(
        "/check",
        signal,
        { paths: logOperations },
        profile.csrfToken,
      );
      if (
        !Array.isArray(result.results) ||
        result.results.length !== logOperations.length ||
        result.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return result.results;
    },
    sessionChanged,
  );
  const context: LogContext = {
    profile,
    sessionChanged,
    can: (operation) => gate.data?.[logOperations.indexOf(operation)] === true,
  };
  useEffect(() => {
    const back = () => {
      setView(readView());
      setRevision((n) => n + 1);
      setIntent(bundleIntent(profile));
    };
    window.addEventListener("popstate", back);
    window.addEventListener("hashchange", back);
    return () => {
      window.removeEventListener("popstate", back);
      window.removeEventListener("hashchange", back);
    };
  }, [profile]);
  function open(next: View) {
    setView(next);
    setRevision((n) => n + 1);
    setIntent(bundleIntent(profile));
    window.history.pushState(
      {},
      "",
      `#operational-logs/${next.type}${next.type === "detail" ? `/${next.id}` : ""}`,
    );
  }
  const allowed =
    view.type === "detail"
      ? context.can("GET /api/system/logs/bundles/detail")
      : view.type === "history"
        ? context.can("GET /api/system/logs/bundles/history")
        : view.type === "sources"
          ? context.can("GET /api/system/logs/sources")
          : view.type === "bundle"
            ? context.can("POST /api/system/logs/bundles")
            : context.can("GET /api/system/logs");
  return (
    <div className="operational-logs-workspace">
      <h2>运行日志与诊断包</h2>
      <p className="warning">
        仅查询部署后新增的 api / worker
        应用事件。尽力记录，可能存在缺口；不提供历史
        stdout、完整系统日志或网络诊断。Worker
        周期完成可能只是空轮询，不表示业务任务完成。
      </p>
      <ErrorNotice error={gate.error} />
      {gate.busy && <p role="status">正在确认运行日志权限…</p>}
      {gate.error && (
        <button onClick={gate.refresh}>重新读取运行日志权限</button>
      )}
      {gate.data && (
        <>
          <nav className="access-tabs" aria-label="运行日志分类">
            {context.can("GET /api/system/logs") && (
              <button
                onClick={() => open({ type: "list" })}
                aria-current={view.type === "list" ? "page" : undefined}
              >
                事件查询
              </button>
            )}
            {context.can("GET /api/system/logs/sources") && (
              <button
                onClick={() => open({ type: "sources" })}
                aria-current={view.type === "sources" ? "page" : undefined}
              >
                记录来源
              </button>
            )}
            {context.can("GET /api/system/logs/bundles/history") && (
              <button
                onClick={() => open({ type: "history" })}
                aria-current={view.type === "history" ? "page" : undefined}
              >
                打包历史
              </button>
            )}
          </nav>
          {intent?.attempted && view.type !== "bundle" && (
            <div className="warning" role="status">
              有一次打包提交尚未确认。先核对打包历史和详情，或用原幂等键及新验证码重试。
              {context.can("POST /api/system/logs/bundles") && (
                <button onClick={() => open({ type: "bundle" })}>
                  核对未确认打包
                </button>
              )}
            </div>
          )}
          {!allowed && (
            <p role="status">
              当前账户没有此运行日志操作权限。读取需要安装所有者租户的 system 和
              system_logs 读取授权；打包和取消另需写入及任务权限。
            </p>
          )}
          {allowed && (
            <div
              key={`${view.type}:${view.type === "detail" ? view.id : ""}:${revision}`}
            >
              {view.type === "list" && (
                <EventList
                  context={context}
                  query={query}
                  change={setQuery}
                  bundle={(selection) => {
                    const next = beginBundleIntent(profile, selection);
                    setIntent(next);
                    open({ type: "bundle" });
                  }}
                />
              )}
              {view.type === "sources" && <Sources context={context} />}
              {view.type === "history" && (
                <History
                  context={context}
                  query={history}
                  change={setHistory}
                  open={(id) => open({ type: "detail", id })}
                />
              )}
              {view.type === "bundle" &&
                (intent ? (
                  <BundleForm
                    context={context}
                    intent={intent}
                    open={(id) => open({ type: "detail", id })}
                    history={() => open({ type: "history" })}
                  />
                ) : (
                  <p role="status">
                    请返回事件查询，确认服务器解析后的时间范围和模块，再创建诊断包。
                  </p>
                ))}
              {view.type === "detail" && (
                <Bundle context={context} id={view.id} />
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}
function EventList({
  context,
  query,
  change,
  bundle,
}: {
  context: LogContext;
  query: LogQuery;
  change: (q: LogQuery) => void;
  bundle: (s: Selection) => void;
}) {
  const [formError, setFormError] = useState("");
  const read = useLogRead(
    logQuery(query),
    (signal) => logAPI.list(query, signal, context.profile.ID),
    context.sessionChanged,
  );
  return (
    <>
      <h3>已记录应用事件</h3>
      <form
        className="filters"
        key={JSON.stringify(query)}
        onSubmit={(e) => {
          e.preventDefault();
          const fields = new FormData(e.currentTarget);
          try {
            const next = {
              pageIdx: 1,
              pageSize: Number(fields.get("pageSize")),
              startTm: historyInputUTC(String(fields.get("startTm") ?? "")),
              endTm: historyInputUTC(String(fields.get("endTm") ?? "")),
              systemType: fields
                .getAll("systemType")
                .map(String) as LogModule[],
            };
            logQuery(next);
            setFormError("");
            change(next);
          } catch {
            setFormError(
              "请选择 api 或 worker；时间须同时填写或同时留空，开始早于结束，最长 24 小时。",
            );
          }
        }}
      >
        <Field label="入库开始时间（UTC）">
          <input
            name="startTm"
            type="datetime-local"
            step="0.000001"
            defaultValue={query.startTm.replace(/Z$/, "")}
          />
        </Field>
        <Field label="入库结束时间（UTC，不包含）">
          <input
            name="endTm"
            type="datetime-local"
            step="0.000001"
            defaultValue={query.endTm.replace(/Z$/, "")}
          />
        </Field>
        <Field label="事件模块">
          <select name="systemType" multiple defaultValue={query.systemType}>
            <option value="api">api</option>
            <option value="worker">worker</option>
          </select>
        </Field>
        <Field label="每页事件数">
          <select name="pageSize" defaultValue={query.pageSize}>
            {[10, 20, 50, 100].map((size) => (
              <option key={size}>{size}</option>
            ))}
          </select>
        </Field>
        <button>查询事件</button>
        <button
          type="button"
          className="secondary"
          onClick={() => {
            setFormError("");
            change(emptyLogQuery());
            read.refresh();
          }}
        >
          查询最近 24 小时
        </button>
      </form>
      <p className="muted">
        时间留空时，由数据库解析最近 24
        小时。按入库时间筛选和倒序显示，观测时间与入库时间分别展示；入库时间不是精确事务提交时刻。
      </p>
      <ErrorNotice error={formError || read.error} />
      {read.busy && <p role="status">正在读取已记录事件…</p>}
      <button className="secondary" disabled={read.busy} onClick={read.refresh}>
        刷新事件
      </button>
      {read.data && (
        <>
          <SelectionSummary selection={read.data.selection} />
          <CoverageNotice coverage={read.data.coverage} />
          {context.can("POST /api/system/logs/bundles") && (
            <button onClick={() => bundle(read.data!.selection)}>
              确认当前筛选并打包
            </button>
          )}
          {!read.data.List.length && (
            <p role="status">
              该范围没有已记录事件，不能据此判断服务是否活动或日志是否完整。
            </p>
          )}
          <div className="table-scroll">
            <table aria-label="运行事件记录">
              <thead>
                <tr>
                  <th>模块 / 事件</th>
                  <th>结果 / 级别</th>
                  <th>观测时间（UTC）</th>
                  <th>入库时间（UTC）</th>
                  <th>安全事件详情</th>
                </tr>
              </thead>
              <tbody>
                {read.data.List.map(({ event, log }) => (
                  <tr key={event.eventId}>
                    <td>
                      {event.module}
                      <br />
                      {event.code}
                    </td>
                    <td>
                      {event.outcome} / {event.severity}
                      {event.reason && (
                        <small className="block">{event.reason}</small>
                      )}
                    </td>
                    <td>{event.observedAt}</td>
                    <td>{event.recordedAt}</td>
                    <td>
                      <details>
                        <summary>查看事件 {event.eventId}</summary>
                        <pre className="task-json">{log}</pre>
                      </details>
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
            change={(pageIdx) =>
              change({ ...query, ...read.data!.selection, pageIdx })
            }
          />
        </>
      )}
    </>
  );
}
function Sources({ context }: { context: LogContext }) {
  const read = useLogRead(
    "sources",
    (signal) => logAPI.sources(signal, context.profile.ID),
    context.sessionChanged,
  );
  return (
    <>
      <h3>应用事件来源与记录观察</h3>
      <button onClick={read.refresh} disabled={read.busy}>
        刷新记录来源
      </button>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取来源观察…</p>}
      {read.data && (
        <>
          <CoverageNotice coverage={read.data.coverage} />
          <p className="warning">
            已登记表示存在应用记录入口，不证明当前服务健康。以下进程计数是可能过期的不完整观察，只随部分事件入库；不能用于完整丢失统计，进程崩溃仍可能留下未知缺口。
          </p>
          <table aria-label="日志模块来源">
            <thead>
              <tr>
                <th>模块</th>
                <th>记录入口</th>
                <th>已记录数</th>
                <th>最早 / 最近入库（UTC）</th>
              </tr>
            </thead>
            <tbody>
              {read.data.modules.map((m) => (
                <tr key={m.module}>
                  <td>{m.module}</td>
                  <td>已登记</td>
                  <td>{m.recordedCount}</td>
                  <td>
                    {m.firstRecordedAt ?? "尚无记录"}
                    <br />
                    {m.lastRecordedAt ?? "尚无记录"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {read.data.reportsTruncated && (
            <p className="warning">
              仅展示最近 100 个进程观察，其余未在此页列出。
            </p>
          )}
          {!read.data.reports.length && (
            <p role="status">尚无已记录的进程观察；这不是服务健康结论。</p>
          )}
          {read.data.reports.map((report) => (
            <details key={report.processId}>
              <summary>
                {report.module} · 进程 {report.processId} · 报告入库{" "}
                {report.reportedAt}
              </summary>
              <dl>
                {[
                  ["进程开始（UTC）", report.startedAt],
                  ["计数观测（UTC）", report.observedAt],
                  ["报告入库（UTC）", report.reportedAt],
                  ["已接受", report.accepted],
                  ["已获写入确认", report.acknowledged],
                  ["已拒绝", report.rejected],
                  ["队列满", report.queueFull],
                  ["写入失败尝试", report.writeFailures],
                  ["未获确认", report.unacknowledged],
                  ["停止时放弃等待", report.abandoned],
                  ["观察到停止接受", report.acceptanceStopped ? "是" : "否"],
                  ["证据类型", report.evidence],
                ].map(([name, value]) => (
                  <div key={name}>
                    <dt>{name}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
            </details>
          ))}
        </>
      )}
    </>
  );
}
function BundleForm({
  context,
  intent,
  open,
  history,
}: {
  context: LogContext;
  intent: BundleIntent;
  open: (id: string) => void;
  history: () => void;
}) {
  const mutation = useLogMutation(context.sessionChanged),
    [proofVersion, setProofVersion] = useState(0),
    [attempted, setAttempted] = useState(intent.attempted);
  return (
    <>
      <h3>确认运行日志诊断包</h3>
      <SelectionSummary selection={intent.input} />
      <p className="warning">
        Worker
        执行时才捕获此筛选的事件快照，可能包含稍后入库且位于时间范围内的记录；不是当前页面行的精确副本。ZIP
        包含 manifest.json 和 events.jsonl，最多 10,000 条、16
        MiB，超限失败且不截断；范围内零条记录会生成明确的空快照。
      </p>
      <p>幂等键：{intent.input.idempotencyKey}</p>
      {attempted && (
        <p role="status">
          原提交结果尚未确认。条件和幂等键已保留；先核对历史和详情，再用新验证码重试原提交。
        </p>
      )}
      {context.can("GET /api/system/logs/bundles/history") && (
        <button className="secondary" onClick={history}>
          核对打包历史
        </button>
      )}
      <form
        onSubmit={(event) => {
          const values = readValues(event);
          if (mutation.busy) return;
          const proof = getProof(values);
          if (!proof) {
            mutation.setError("请输入操作者当前密码及新的六位验证码。");
            return;
          }
          setProofVersion((n) => n + 1);
          void mutation.run(
            async (signal) => {
              markBundleAttempted(context.profile, intent);
              setAttempted(true);
              return logAPI.submit(
                intent.input,
                proof,
                context.profile.csrfToken,
                signal,
                context.profile.ID,
              );
            },
            (result) => {
              discardBundleIntent(context.profile);
              open(result.task.taskUUID);
            },
          );
        }}
      >
        <fieldset disabled={mutation.busy}>
          <ProofFields key={proofVersion} />
          <button>
            {mutation.busy
              ? "正在确认…"
              : attempted
                ? "用原幂等键重试打包"
                : "提交诊断包任务"}
          </button>
        </fieldset>
        <ErrorNotice error={mutation.error} />
      </form>
    </>
  );
}
const artifactLabel = (status: BundleDetail["artifactStatus"]) =>
  ({
    eligible: "可申请下载，下载时重新核验权限与完整性",
    not_ready: "尚无已发布 ZIP",
    artifact_invalid: "文件元数据无效，不能下载",
  })[status];
function History({
  context,
  query,
  change,
  open,
}: {
  context: LogContext;
  query: BundleQuery;
  change: (q: BundleQuery) => void;
  open: (id: string) => void;
}) {
  const read = useLogRead(
    bundleQuery(query),
    (signal) => logAPI.history(query, signal, context.profile.ID),
    context.sessionChanged,
    (v) => v.list.some((row) => !terminal(row.task.state)),
  );
  return (
    <>
      <h3>本人的诊断包历史</h3>
      <p>
        仅显示当前授权版本内的本人任务。任务成功和文件可下载分别核验；历史读取不要求通用任务页面权限。
      </p>
      <form
        className="filters"
        onSubmit={(e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          change({
            pageIdx: 1,
            pageSize: Number(f.get("pageSize")),
            status: f.getAll("status").map(String) as TaskState[],
          });
        }}
      >
        <Field label="诊断包任务状态">
          <select name="status" multiple defaultValue={query.status}>
            {taskStates.map((s) => (
              <option key={s} value={s}>
                {stateLabels[s]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="每页诊断包数">
          <select name="pageSize" defaultValue={query.pageSize}>
            {[10, 20, 50, 100].map((size) => (
              <option key={size}>{size}</option>
            ))}
          </select>
        </Field>
        <button>筛选打包历史</button>
      </form>
      <button className="secondary" onClick={read.refresh} disabled={read.busy}>
        刷新打包历史
      </button>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取诊断包历史…</p>}
      {read.data && (
        <>
          {!read.data.list.length && (
            <p role="status">当前页没有可见诊断包任务。</p>
          )}
          <div className="table-scroll">
            <table aria-label="诊断包历史记录">
              <thead>
                <tr>
                  <th>预期文件名 / 任务 ID</th>
                  <th>任务状态</th>
                  <th>文件状态</th>
                  <th>创建 / 更新（UTC）</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {read.data.list.map((row) => (
                  <tr key={row.task.taskUUID}>
                    <td>
                      {bundleFilename(row.task.taskUUID)}
                      <small className="block">{row.task.taskUUID}</small>
                    </td>
                    <td>
                      {stateLabels[row.task.state]} · {row.task.progress}%
                      <small className="block">
                        {row.task.error || "无错误代码"}
                      </small>
                    </td>
                    <td>
                      {artifactLabel(row.artifactStatus)}
                      {row.downloadReady && (
                        <small className="block">
                          快照 {row.snapshotAt} · {row.rowCount} 条
                        </small>
                      )}
                    </td>
                    <td>
                      {row.task.createdAt}
                      <br />
                      {row.task.updatedAt}
                    </td>
                    <td>
                      {context.can("GET /api/system/logs/bundles/detail") && (
                        <button onClick={() => open(row.task.taskUUID)}>
                          查看诊断包 {row.task.taskUUID}
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
function Bundle({ context, id }: { context: LogContext; id: string }) {
  const read = useLogRead(
    id,
    (signal) => logAPI.detail(id, signal, context.profile.ID),
    context.sessionChanged,
    (v) => !terminal(v.task.state),
  );
  const download = useLogMutation(context.sessionChanged),
    cancel = useLogMutation(context.sessionChanged),
    [cancelOpen, setCancelOpen] = useState(false),
    [proofVersion, setProofVersion] = useState(0),
    [downloaded, setDownloaded] = useState(false),
    urls = useRef(new Set<string>());
  useEffect(
    () => () => {
      for (const url of urls.current) URL.revokeObjectURL(url);
      urls.current.clear();
    },
    [],
  );
  const detail = read.data;
  return (
    <>
      <h3>诊断包详情</h3>
      <button
        className="secondary"
        onClick={() => {
          setDownloaded(false);
          read.refresh();
        }}
        disabled={read.busy}
      >
        刷新诊断包详情
      </button>
      <ErrorNotice error={read.error || download.error || cancel.error} />
      {read.busy && <p role="status">正在核对任务与 ZIP 状态…</p>}
      {detail && (
        <>
          <dl>
            {[
              ["任务 ID", detail.task.taskUUID],
              ["任务状态", stateLabels[detail.task.state]],
              ["文件状态", artifactLabel(detail.artifactStatus)],
              [
                "业务尝试次数",
                `${detail.task.attempt}/${detail.task.maxAttempts}`,
              ],
              ["错误代码", detail.task.error || "无"],
              ["实际快照时间（UTC）", detail.snapshotAt ?? "尚未确认"],
              ["实际事件数", detail.rowCount ?? "尚未确认"],
            ].map(([name, value]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          <label className="task-progress">
            打包持久化进度：{detail.task.progress}%
            <progress
              aria-label="诊断包持久化进度"
              max={100}
              value={detail.task.progress}
            />
          </label>
          {detail.coverage && <CoverageNotice coverage={detail.coverage} />}
          {detail.downloadReady && detail.rowCount === 0 && (
            <p role="status">
              此快照没有已记录事件；ZIP 仍包含清单和空
              events.jsonl，不代表覆盖完整。
            </p>
          )}
          {detail.task.state === "succeeded" && !detail.downloadReady && (
            <p className="warning">
              任务已成功，但当前没有可下载的有效 ZIP。请重新核对文件状态。
            </p>
          )}
          {detail.task.state === "cancel_requested" && (
            <p className="warning">
              已请求取消，尚未确认停止。请等待真实终态，暂不可下载。
            </p>
          )}
          {detail.downloadReady &&
            context.can("GET /api/system/logs/bundles/download") && (
              <button
                disabled={download.busy}
                onClick={() => {
                  setDownloaded(false);
                  void download.run(
                    (signal) => logAPI.download(id, signal, context.profile.ID),
                    (blob) => {
                      const url = URL.createObjectURL(blob);
                      urls.current.add(url);
                      const a = document.createElement("a");
                      a.href = url;
                      a.download = bundleFilename(id);
                      a.hidden = true;
                      document.body.appendChild(a);
                      a.click();
                      a.remove();
                      setTimeout(() => {
                        if (urls.current.delete(url)) URL.revokeObjectURL(url);
                      }, 1000);
                      setDownloaded(true);
                    },
                    read.refresh,
                  );
                }}
              >
                {download.busy ? "正在下载 ZIP…" : "下载 ZIP"}
              </button>
            )}
          {downloaded && (
            <p role="status">
              已收到经当前权限和文件完整性验证的 ZIP，并交由浏览器下载。
            </p>
          )}
          <p className="muted">
            每次下载重新核验当前账户、授权版本和全部文件字节。浏览器离开详情或切换账户后丢弃未完成下载。
          </p>
          {!terminal(detail.task.state) &&
            detail.task.state !== "cancel_requested" &&
            context.can("POST /api/system/logs/bundles/cancel") &&
            !cancelOpen && (
              <button
                className="secondary danger"
                onClick={() => setCancelOpen(true)}
              >
                请求取消诊断包
              </button>
            )}
          {cancelOpen &&
            !terminal(detail.task.state) &&
            detail.task.state !== "cancel_requested" && (
              <form
                onSubmit={(event) => {
                  const values = readValues(event);
                  const proof = getProof(values);
                  if (!proof) {
                    cancel.setError("请输入操作者当前密码及新的六位验证码。");
                    return;
                  }
                  setProofVersion((n) => n + 1);
                  void cancel.run(
                    (signal) =>
                      logAPI.cancel(
                        id,
                        proof,
                        context.profile.csrfToken,
                        signal,
                        context.profile.ID,
                      ),
                    () => {
                      setCancelOpen(false);
                      read.refresh();
                    },
                    read.refresh,
                  );
                }}
              >
                <fieldset disabled={cancel.busy}>
                  <ProofFields key={proofVersion} />
                  <button>确认取消诊断包</button>
                  <button
                    type="button"
                    className="secondary"
                    onClick={() => setCancelOpen(false)}
                  >
                    关闭取消表单
                  </button>
                </fieldset>
              </form>
            )}
        </>
      )}
    </>
  );
}
