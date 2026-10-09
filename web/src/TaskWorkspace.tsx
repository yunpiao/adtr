import { isDomainConnectionTask } from "./task-api";
import { useEffect, useState } from "react";
import { ApiError, type Profile } from "./api";
import {
  accessRequest,
  queryString,
  type Permission,
  type Proof,
} from "./access-api";
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
  stateLabels,
  supportedKind,
  taskAPI,
  taskOperations,
  taskStates,
  terminal,
  type Task,
  type TaskKind,
} from "./task-api";
import { useTaskMutation, useTaskRead, type TaskContext } from "./task-common";
import {
  MaintenanceContent,
  RestoreTask,
  type MaintenanceView,
} from "./TaskMaintenance";
import { maintenanceIntent } from "./maintenance-intent";

const isDirectoryTask = (name: unknown) =>
  name === "domain.directory_read" || name === "domain.directory_read.v2";

interface Intent {
  action: "submit" | "recover";
  key: string;
  taskUUID?: string;
  attempted: boolean;
}
// Persist only a non-secret operation key and target in this tab, so refresh
// cannot silently turn an uncertain submission into a new task. Passwords, OTPs,
// CSRF/session tokens and payloads never enter browser storage.
const intentStorage = "adtr.pending-task";
const pending = new Map<string, Intent>();
type IntentActor = Pick<Profile, "ID" | "username">;
let activeIntentActor: IntentActor | undefined;
const intentOwnerKey = (p: IntentActor) =>
  `${intentStorage}:${p.ID}:${encodeURIComponent(p.username)}`;
export function forgetTaskSession() {
  for (const [key, intent] of pending)
    if (!intent.attempted) pending.delete(key);
  activeIntentActor = undefined;
}
export function discardTaskIntent(
  profile: IntentActor | undefined = activeIntentActor,
) {
  if (!profile) return;
  pending.delete(intentOwnerKey(profile));
  try {
    sessionStorage.removeItem(intentOwnerKey(profile));
    const legacy = JSON.parse(sessionStorage.getItem(intentStorage) ?? "null");
    if (legacy?.owner === profile.ID && legacy?.username === profile.username)
      sessionStorage.removeItem(intentStorage);
  } catch {
    /* Storage may be disabled. */
  }
}
function projectIntent(stored: unknown): Intent | undefined {
  if (!stored || typeof stored !== "object" || Array.isArray(stored)) return;
  const value = stored as Record<string, unknown>;
  if (
    typeof value.key !== "string" ||
    !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(value.key) ||
    !(
      value.action === "submit" ||
      (value.action === "recover" &&
        typeof value.taskUUID === "string" &&
        /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(value.taskUUID))
    )
  )
    return;
  return {
    action: value.action,
    key: value.key,
    ...(value.action === "recover"
      ? { taskUUID: value.taskUUID as string }
      : {}),
    attempted: true,
  };
}
function getIntent(profile: Profile) {
  activeIntentActor = { ID: profile.ID, username: profile.username };
  const key = intentOwnerKey(profile);
  if (!pending.has(key)) {
    try {
      const current = sessionStorage.getItem(key);
      const raw = current ?? sessionStorage.getItem(intentStorage);
      const stored = raw && raw.length <= 65536 ? JSON.parse(raw) : null;
      const intent =
        stored?.owner === profile.ID && stored?.username === profile.username
          ? projectIntent(stored)
          : undefined;
      if (intent) {
        pending.set(key, intent);
        if (current === null) {
          sessionStorage.setItem(
            key,
            JSON.stringify({
              owner: profile.ID,
              username: profile.username,
              ...intent,
            }),
          );
          sessionStorage.removeItem(intentStorage);
        }
      } else if (current !== null) sessionStorage.removeItem(key);
    } catch {
      /* The in-memory key still covers navigation if storage is unavailable. */
    }
  }
  return pending.get(key);
}
function markAttempted(profile: Profile, intent: Intent) {
  const clean = projectIntent(intent);
  if (!clean || pending.get(intentOwnerKey(profile)) !== intent)
    throw Error("Invalid task intent");
  for (const field of Object.keys(intent))
    if (!["action", "key", "taskUUID", "attempted"].includes(field))
      delete (intent as unknown as Record<string, unknown>)[field];
  intent.attempted = true;
  try {
    sessionStorage.setItem(
      intentOwnerKey(profile),
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        action: clean.action,
        key: clean.key,
        taskUUID: clean.taskUUID,
      }),
    );
  } catch {
    /* Keep the original in-memory key. */
  }
}
function intentFor(
  profile: Profile,
  action: Intent["action"],
  taskUUID?: string,
): Intent {
  const old = getIntent(profile);
  if (old?.action === action && old.taskUUID === taskUUID) return old;
  if (old?.attempted) throw new Error("Unresolved task intent");
  const clean = projectIntent({ action, taskUUID, key: crypto.randomUUID() });
  if (!clean) throw Error("Invalid task intent");
  const intent = { ...clean, attempted: false };
  pending.set(intentOwnerKey(profile), intent);
  return intent;
}
function clearIntent(intent: Intent) {
  for (const [key, current] of pending) {
    if (current !== intent) continue;
    pending.delete(key);
    try {
      sessionStorage.removeItem(key);
      const legacy = JSON.parse(
        sessionStorage.getItem(intentStorage) ?? "null",
      );
      if (
        typeof legacy?.owner === "number" &&
        typeof legacy?.username === "string" &&
        intentOwnerKey({ ID: legacy.owner, username: legacy.username }) ===
          key &&
        legacy.key === intent.key
      )
        sessionStorage.removeItem(intentStorage);
    } catch {
      /* Memory is cleared. */
    }
    return;
  }
}
export {
  getIntent as taskIntent,
  intentFor as beginTaskIntent,
  markAttempted as attemptTaskIntent,
  clearIntent as clearTaskIntent,
};

type View =
  | { type: "list" }
  | { type: "submit" }
  | { type: "detail"; id: string; archived?: boolean }
  | MaintenanceView;
export default function TaskWorkspace({
  profile,
  sessionChanged,
}: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  const [view, setView] = useState<View>({ type: "list" }),
    [notice, setNotice] = useState(""),
    [revision, setRevision] = useState(0);
  const gate = useTaskRead(
    profile.csrfToken,
    async (signal) => {
      const [menu, checks] = await Promise.all([
        accessRequest<{ menu: Permission[] }>("/menu", signal),
        accessRequest<{ results: boolean[] }>(
          "/check",
          signal,
          { paths: taskOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== taskOperations.length ||
        checks.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return {
        readable: menu.menu.some(
          (node) => node.mark === "tasks" && node.auth.readable,
        ),
        results: checks.results,
        menu: menu.menu,
      };
    },
    sessionChanged,
  );
  const context: TaskContext = {
    profile,
    sessionChanged,
    canArchiveView:
      !!gate.data?.menu.some((n) => n.mark === "tasks" && n.auth.writeable) &&
      !!gate.data?.menu.some(
        (n) => n.mark === "task_archive" && n.auth.readable,
      ),
    can: (path) =>
      gate.data?.results[taskOperations.indexOf(path)] === true &&
      (!path.includes("/schedules") ||
        gate.data.menu.some(
          (n) => n.mark === "schedules" && n.auth.readable,
        )) &&
      (![
        "GET /api/tasks/archive-candidates",
        "POST /api/tasks/archive",
        "POST /api/tasks/restore",
      ].includes(path) ||
        gate.data.menu.some(
          (n) => n.mark === "task_archive" && n.auth.readable,
        )),
  };
  const open = (next: View, message = "") => {
    setView(next);
    setNotice(message);
    setRevision((n) => n + 1);
    window.history.pushState(
      {},
      "",
      `#tasks/${next.type}${next.type === "detail" || next.type === "schedule-detail" ? `/${encodeURIComponent(next.id)}` : ""}`,
    );
  };
  const intent = getIntent(profile);
  const maintenance = maintenanceIntent(profile);
  return (
    <div className="task-workspace">
      <h2>后台任务</h2>
      <p className="warning">
        平台健康检查（infrastructure.health）可在此提交；审计导出请使用操作审计页面，域连接检测请使用域连接页面。平台健康成功不代表
        AD 业务验收通过。
      </p>
      <ErrorNotice error={gate.error} />
      {gate.busy && <p role="status">正在确认任务权限…</p>}
      {gate.error && (
        <button className="secondary" onClick={gate.refresh}>
          重新读取任务权限
        </button>
      )}
      {gate.data && (!gate.data.readable || !context.can("GET /api/tasks")) && (
        <p role="status">当前账户没有后台任务读取权限。</p>
      )}
      {gate.data?.readable && context.can("GET /api/tasks") && (
        <>
          <nav className="actions compact" aria-label="任务视图">
            <button
              className="secondary"
              onClick={() => open({ type: "list" })}
            >
              执行任务
            </button>
            {context.can("GET /api/tasks/schedules") && (
              <button
                className="secondary"
                onClick={() => open({ type: "schedules" })}
              >
                周期计划
              </button>
            )}
            {context.can("GET /api/tasks/archive-candidates") && (
              <button
                className="secondary"
                onClick={() => open({ type: "archive" })}
              >
                任务归档
              </button>
            )}
          </nav>
          {maintenance?.attempted && view.type !== "maintenance-confirm" && (
            <div className="warning" role="status">
              有一项维护操作尚未确认，请使用原幂等键核对。
              <button
                className="secondary"
                onClick={() => open({ type: "maintenance-confirm" })}
              >
                继续核对维护操作
              </button>
            </div>
          )}
          {notice && (
            <div role="status" className="task-notice">
              {notice}
            </div>
          )}
          {intent?.attempted && view.type === "list" && (
            <div className="warning" role="status">
              有一项提交结果尚未确认。请使用原幂等键和新的验证码核对，避免创建重复任务。
              <button
                className="secondary"
                onClick={() =>
                  open(
                    intent.action === "submit"
                      ? { type: "submit" }
                      : {
                          type: "detail",
                          id: intent.taskUUID!,
                          archived: context.canArchiveView,
                        },
                  )
                }
              >
                继续核对未确认的提交
              </button>
            </div>
          )}
          {view.type === "list" && (
            <TasksList
              key={revision}
              context={context}
              open={open}
              blocked={!!intent?.attempted}
            />
          )}
          {view.type === "submit" && (
            <SubmitTask context={context} done={open} />
          )}
          {!["list", "submit", "detail"].includes(view.type) && (
            <MaintenanceContent
              key={revision}
              context={context}
              view={view as MaintenanceView}
              open={open}
            />
          )}
          {view.type === "detail" && (
            <TaskDetails
              key={view.id}
              context={context}
              id={view.id}
              archived={view.archived}
              done={open}
            />
          )}
        </>
      )}
    </div>
  );
}
function TasksList({
  context,
  open,
  blocked,
}: {
  context: TaskContext;
  open: (view: View, notice?: string) => void;
  blocked: boolean;
}) {
  const [query, setQuery] = useState({
    pageIdx: 1,
    pageSize: 20,
    domainId: "",
    state: "",
    taskName: "",
    visibility: "",
  });
  const text = queryString(query);
  const read = useTaskRead(
    text,
    (signal) => taskAPI.list(text, signal),
    context.sessionChanged,
    (data) => data.tasks.some((task) => !terminal(task.state)),
  );
  return (
    <>
      <div className="actions compact">
        {context.can("POST /api/tasks/submit") &&
          context.can("GET /api/tasks/kinds") && (
            <button disabled={blocked} onClick={() => open({ type: "submit" })}>
              提交健康检查
            </button>
          )}
        <button className="secondary" onClick={read.refresh}>
          刷新任务列表
        </button>
      </div>
      <form
        className="filters"
        onSubmit={(event) => {
          const v = readValues(event);
          setQuery({
            pageIdx: 1,
            pageSize: Number(v.pageSize),
            domainId: v.domainId,
            state: v.state,
            taskName: v.taskName,
            visibility: v.visibility ?? "",
          });
        }}
      >
        <div className="form-grid">
          <Field label="任务种类筛选">
            <input
              name="taskName"
              defaultValue={query.taskName}
              maxLength={64}
            />
          </Field>
          <Field label="域 ID 筛选" help="平台健康检查的域 ID 为 platform">
            <input
              name="domainId"
              defaultValue={query.domainId}
              maxLength={128}
            />
          </Field>
          <Field label="任务状态筛选">
            <select name="state" defaultValue="">
              <option value="">所有状态</option>
              {taskStates.map((state) => (
                <option key={state} value={state}>
                  {stateLabels[state]}
                </option>
              ))}
            </select>
          </Field>
          {context.canArchiveView && (
            <Field label="任务可见性筛选">
              <select name="visibility" defaultValue="">
                <option value="">默认可见任务</option>
                <option value="archived">已归档任务</option>
                <option value="all">所有可见性</option>
              </select>
            </Field>
          )}
          <Field label="每页任务数">
            <select name="pageSize" defaultValue={20}>
              {[10, 20, 50, 100].map((size) => (
                <option key={size}>{size}</option>
              ))}
            </select>
          </Field>
        </div>
        <div className="actions compact">
          <button>筛选任务</button>
          <button
            type="reset"
            className="secondary"
            onClick={() =>
              setQuery({
                pageIdx: 1,
                pageSize: 20,
                domainId: "",
                state: "",
                taskName: "",
                visibility: "",
              })
            }
          >
            清除任务筛选
          </button>
        </div>
      </form>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取任务…</p>}
      {read.data && (
        <>
          <div className="table-scroll">
            <table>
              <caption>任务列表</caption>
              <thead>
                <tr>
                  <th>任务 / ID</th>
                  <th>范围</th>
                  <th>状态</th>
                  <th>进度 / 尝试</th>
                  <th>创建时间（UTC）</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {read.data.tasks.map((task) => (
                  <tr key={task.taskUUID}>
                    <th scope="row">
                      {task.taskName}
                      <small className="block">{task.taskUUID}</small>
                    </th>
                    <td>{task.domainId}</td>
                    <td>
                      <TaskStatus task={task} />
                      {task.archived && (
                        <strong className="block">已归档</strong>
                      )}
                    </td>
                    <td>
                      {task.progress}% · {task.attempt}/{task.maxAttempts}
                    </td>
                    <td>{task.createdAt}</td>
                    <td>
                      {context.can("GET /api/tasks/detail") && (
                        <button
                          className="secondary"
                          onClick={() =>
                            open({
                              type: "detail",
                              id: task.taskUUID,
                              archived: task.archived,
                            })
                          }
                        >
                          详情 {task.taskUUID}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!read.data.tasks.length && <p role="status">没有符合条件的任务。</p>}
          <Pagination
            page={read.data.page}
            exhausted={read.data.exhausted}
            busy={read.busy}
            change={(pageIdx) => setQuery({ ...query, pageIdx })}
          />
        </>
      )}
    </>
  );
}
export function TaskStatus({ task }: { task: Task }) {
  return (
    <span
      className={`task-state task-state-${task.state}`}
      data-state={task.state}
    >
      {stateLabels[task.state]}
      <small className="block">
        {task.state} · 来源状态 {task.sourceState}
      </small>
    </span>
  );
}
function SubmitTask({
  context,
  done,
}: {
  context: TaskContext;
  done: (view: View, notice?: string) => void;
}) {
  const registry = useTaskRead(
    "kinds",
    (signal) => taskAPI.kinds(signal),
    context.sessionChanged,
  );
  const existing = getIntent(context.profile);
  const [intent] = useState(() =>
    existing?.attempted && existing.action !== "submit"
      ? null
      : intentFor(context.profile, "submit"),
  );
  const mutation = useTaskMutation(context.sessionChanged),
    [proofVersion, setProofVersion] = useState(0),
    [reconcile, setReconcile] = useState(0);
  // An uncertain submit has no known task ID. Refresh actual list state and keep
  // the original key for an explicit authenticated replay; never auto-resubmit.
  const list = useTaskRead(
    `reconcile-${reconcile}`,
    (signal) =>
      taskAPI.list(
        "pageIdx=1&pageSize=10&taskName=infrastructure.health&domainId=platform",
        signal,
      ),
    context.sessionChanged,
  );
  const kind = registry.data?.find(supportedKind);
  return (
    <form
      onSubmit={(event) => {
        const v = readValues(event);
        if (mutation.busy || !kind || !intent) return;
        const proof = getProof(v);
        if (!proof) {
          mutation.setError("请输入有效的操作者密码和六位未使用验证码。");
          return;
        }
        markAttempted(context.profile, intent);
        void mutation.run(
          (signal) =>
            taskAPI.submit(
              {
                taskName: kind.taskName,
                domainId: "platform",
                payloadVersion: kind.payloadVersion,
                payload: {},
                idempotencyKey: intent.key,
              },
              proof,
              context.profile.csrfToken,
              signal,
            ),
          (result) => {
            clearIntent(intent);
            done(
              {
                type: "detail",
                id: result.task.taskUUID,
                archived: result.task.archived,
              },
              result.replayed
                ? `服务器确认幂等重放，返回同一个任务${result.task.archived ? "（已归档）" : ""}。未创建新的执行；请以实际任务状态为准。`
                : "任务已持久化提交，请以实际执行结果为准。",
            );
          },
          () => {
            setProofVersion((n) => n + 1);
            setReconcile((n) => n + 1);
          },
        );
      }}
    >
      <h3>提交平台健康检查</h3>
      <ErrorNotice error={registry.error || mutation.error} />
      {registry.busy && <p role="status">正在读取服务器任务登记表…</p>}
      {registry.error && (
        <button type="button" className="secondary" onClick={registry.refresh}>
          刷新任务种类
        </button>
      )}
      {registry.data && !kind && (
        <p className="warning">
          服务器未登记受支持的健康检查种类，当前不能提交。
        </p>
      )}
      {kind && <KindSummary kind={kind} />}
      {intent && (
        <Field
          label="提交幂等键"
          help="同一次提交及响应不确定后的重试使用此键；密码和验证码不会存储。"
        >
          <input readOnly value={intent.key} />
        </Field>
      )}
      {!intent && <p className="warning">请先核对原有未确认的恢复任务。</p>}
      {intent?.attempted && (
        <p className="warning">
          原提交可能已经生效。请使用此幂等键及新的验证码核对；返回列表或停止等待不能取消服务器任务。刷新页面可继续核对；退出登录或关闭标签页后请先核对任务列表。
        </p>
      )}
      {reconcile > 0 && (
        <>
          <ErrorNotice error={list.error} />
          {list.data && (
            <p role="status">
              已重新读取最近 {list.data.tasks.length}{" "}
              项真实任务；只有幂等响应能确认这次提交对应的任务 ID。
            </p>
          )}
        </>
      )}
      {kind && intent && context.can("POST /api/tasks/submit") && (
        <fieldset key={proofVersion} disabled={mutation.busy}>
          <ProofFields />
        </fieldset>
      )}
      <FormActions
        busy={mutation.busy}
        disabled={!kind || !intent || !context.can("POST /api/tasks/submit")}
        cancel={() => {
          if (intent && !intent.attempted) clearIntent(intent);
          done(
            { type: "list" },
            intent?.attempted
              ? "已停止等待；提交可能已生效，请核对真实任务列表或使用原幂等键继续确认。"
              : "",
          );
        }}
      >
        {intent?.attempted ? "使用原幂等键确认提交" : "确认提交健康检查"}
      </FormActions>
    </form>
  );
}
function KindSummary({ kind }: { kind: TaskKind }) {
  return (
    <dl>
      <div>
        <dt>任务种类 / 版本</dt>
        <dd>
          {kind.taskName} / {kind.payloadVersion}
        </dd>
      </div>
      <div>
        <dt>执行范围</dt>
        <dd>{kind.scope} · platform</dd>
      </div>
      <div>
        <dt>最多总尝试次数</dt>
        <dd>{kind.maxAttempts}（包含首次执行）</dd>
      </div>
      <div>
        <dt>单次超时</dt>
        <dd>{kind.timeoutSeconds} 秒</dd>
      </div>
      <div>
        <dt>任务输入</dt>
        <dd>{"{}（无凭据或 AD 目标）"}</dd>
      </div>
    </dl>
  );
}
function TaskDetails({
  context,
  id,
  archived = false,
  done,
}: {
  context: TaskContext;
  id: string;
  archived?: boolean;
  done: (view: View, notice?: string) => void;
}) {
  const read = useTaskRead(
    `${id}:${archived}`,
    (signal) => taskAPI.detail(id, signal, archived ? "all" : "active"),
    context.sessionChanged,
    (data) => !terminal(data.task.state),
  );
  const old = getIntent(context.profile);
  const [action, setAction] = useState<"cancel" | "recover" | null>(
    old?.action === "recover" && old.taskUUID === id && old.attempted
      ? "recover"
      : null,
  );
  const task = read.data?.task;
  useEffect(() => {
    // The dedicated audit route owns export creation and its protected ledger.
    // Reconcile an old saved generic recovery intent after the server identifies
    // its kind, instead of leaving the tab trapped in an impossible retry.
    if (
      task?.taskName === "audit.export" ||
      task?.taskName === "system.logs_bundle" ||
      isDomainConnectionTask(task?.taskName) ||
      isDirectoryTask(task?.taskName)
    ) {
      if (old?.action === "recover" && old.taskUUID === id) clearIntent(old);
      setAction((current) => (current === "recover" ? null : current));
    }
  }, [task?.taskName, id, old]);
  const blocked =
    !!old?.attempted && !(old.action === "recover" && old.taskUUID === id);
  return (
    <>
      <h3>任务详情</h3>
      <div className="actions compact">
        <button className="secondary" onClick={() => done({ type: "list" })}>
          返回任务列表
        </button>
        <button className="secondary" onClick={read.refresh}>
          刷新任务详情
        </button>
      </div>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取任务详情…</p>}
      {task && (
        <>
          <div role="status" aria-live="polite">
            <TaskStatus task={task} />
            {!terminal(task.state) && (
              <p>正在自动核对服务器状态；页面轮询不会增加业务尝试次数。</p>
            )}
          </div>
          {task.state === "cancel_requested" && (
            <p className="warning">
              取消请求已登记，执行器尚未确认停止。取消可能与完成竞争，请等待真实终态。
            </p>
          )}
          {task.state === "retry_wait" && (
            <p className="warning">
              尚未成功，正在等待下一次业务尝试。下一次尝试时间：
              {task.nextAttemptAt ?? "服务器未提供"}
            </p>
          )}
          {task.state === "partial_failed" && (
            <p className="warning">
              结果包含部分失败；不能整体自动重放。当前健康检查界面不支持选择失败对象重新提交。
            </p>
          )}
          <dl>
            {[
              ["任务 ID", task.taskUUID],
              ["任务种类", task.taskName],
              ["域 / 范围", task.domainId],
              ["输入版本", task.payloadVersion],
              ["业务尝试次数", `${task.attempt}/${task.maxAttempts}（含首次）`],
              ["结果版本", task.resultVersion],
              ["父任务 ID", task.parentTaskUUID || "无"],
              ["创建时间（UTC）", task.createdAt],
              ["更新时间（UTC）", task.updatedAt],
              [
                "终态时间（UTC）",
                task.terminalAt ??
                  (terminal(task.state) ? "终态时间未知" : "尚未结束"),
              ],
              ["可见性", task.archived ? "已归档" : "默认可见"],
              ["可见性版本", task.visibilityVersion],
              ["下一次尝试（UTC）", task.nextAttemptAt ?? "无"],
              ["错误代码", task.error || "无"],
            ].map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          <label className="task-progress">
            持久化进度：{task.progress}%
            <progress
              aria-label="任务持久化进度"
              max={100}
              value={task.progress}
            />
          </label>
          {task.taskName === "system.logs_bundle" ? (
            <p>
              运行日志诊断包的结果和文件请在「运行日志与诊断包」页面查看；提交、取消和下载均需该页面重新核验权限。
            </p>
          ) : isDirectoryTask(task.taskName) ? (
            <p>
              目录任务请在「
              {task.taskName === "domain.directory_read.v2"
                ? "补充目录资产"
                : "目录资产"}
              」页面查看；读取观察、查询回执、取消和原编号重试均需在对应页面核验权限。
            </p>
          ) : isDomainConnectionTask(task.taskName) ? (
            <p>
              域连接诊断请在「域连接」页面查看；重新检测须在那里明确提交保存版本，不能创建通用恢复任务。
            </p>
          ) : (
            <>
              <h4>实际结果</h4>
              <pre className="task-json" aria-label="任务实际结果">
                {JSON.stringify(task.result, null, 2) ?? "null"}
              </pre>
              <details>
                <summary>查看已提交的执行游标</summary>
                <pre className="task-json">
                  {JSON.stringify(task.cursor, null, 2) ?? "null"}
                </pre>
              </details>
            </>
          )}
          <div className="actions compact">
            {!action &&
              !task.archived &&
              !terminal(task.state) &&
              task.state !== "cancel_requested" &&
              task.taskName !== "system.logs_bundle" &&
              !isDirectoryTask(task.taskName) &&
              context.can("POST /api/tasks/cancel") && (
                <button
                  className="secondary danger"
                  onClick={() => setAction("cancel")}
                >
                  请求取消任务
                </button>
              )}
            {!action &&
              !task.archived &&
              !(
                task.taskName === "audit.export" ||
                task.taskName === "system.logs_bundle" ||
                isDomainConnectionTask(task.taskName) ||
                isDirectoryTask(task.taskName)
              ) &&
              ["failed", "dead_letter"].includes(task.state) &&
              context.can("POST /api/tasks/recover") && (
                <button disabled={blocked} onClick={() => setAction("recover")}>
                  创建恢复任务
                </button>
              )}
          </div>
          {task.archived && (
            <p className="warning">
              任务已归档，原执行结果和证据仍保留。请先恢复可见性，再进行允许的失败恢复。
            </p>
          )}
          {task.archived && context.can("POST /api/tasks/restore") && (
            <RestoreTask
              context={context}
              taskUUID={task.taskUUID}
              visibilityVersion={task.visibilityVersion}
              open={done}
            />
          )}
          {task.taskName === "audit.export" && (
            <p className="warning">
              审计导出文件请在「操作审计」使用此任务 ID
              查看。失败后通过该页面重新创建导出，生成新的受保护控制记录。
            </p>
          )}
          {action &&
            task.taskName !== "system.logs_bundle" &&
            !isDirectoryTask(task.taskName) &&
            !task.archived &&
            !(
              action === "recover" &&
              (task.taskName === "audit.export" ||
                task.taskName === "system.logs_bundle" ||
                isDomainConnectionTask(task.taskName) ||
                isDirectoryTask(task.taskName))
            ) && (
              <TaskAction
                key={`${id}-${action}`}
                context={context}
                action={action}
                task={task}
                closed={() => {
                  setAction(null);
                  read.refresh();
                }}
                done={done}
                reconcile={read.refresh}
              />
            )}
          <div className="table-scroll">
            <table>
              <caption>持久化任务事件</caption>
              <thead>
                <tr>
                  <th>事件</th>
                  <th>状态</th>
                  <th>尝试 / 结果版本</th>
                  <th>时间（UTC）</th>
                </tr>
              </thead>
              <tbody>
                {read.data!.events.map((event) => (
                  <tr key={event.id}>
                    <td>
                      {event.action}
                      {event.action === "completed-with-cancel-race" && (
                        <strong className="block">
                          取消与完成发生竞争，终态反映实际结果
                        </strong>
                      )}
                    </td>
                    <td>
                      {stateLabels[event.state]} · {event.state}
                    </td>
                    <td>
                      {event.attempt} / {event.resultVersion}
                    </td>
                    <td>{event.createdAt}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!read.data!.events.length && <p>服务器尚未返回事件。</p>}
        </>
      )}
    </>
  );
}
function TaskAction({
  context,
  action,
  task,
  closed,
  done,
  reconcile,
}: {
  context: TaskContext;
  action: "cancel" | "recover";
  task: Task;
  closed: () => void;
  done: (view: View, notice?: string) => void;
  reconcile: () => void;
}) {
  const [intent] = useState(() =>
      action === "recover"
        ? intentFor(context.profile, action, task.taskUUID)
        : null,
    ),
    [proofVersion, setProofVersion] = useState(0);
  const mutation = useTaskMutation(context.sessionChanged);
  const allowed =
    !task.archived &&
    task.taskName !== "system.logs_bundle" &&
    !isDirectoryTask(task.taskName) &&
    context.can(`POST /api/tasks/${action}`) &&
    (action === "cancel"
      ? !terminal(task.state)
      : !(
          task.taskName === "audit.export" ||
          task.taskName === "system.logs_bundle" ||
          isDomainConnectionTask(task.taskName) ||
          isDirectoryTask(task.taskName)
        ) && ["failed", "dead_letter"].includes(task.state));
  return (
    <form
      className="task-action"
      onSubmit={(event) => {
        const values = readValues(event);
        if (mutation.busy || !allowed) return;
        const proof: Proof | null = getProof(values);
        if (!proof) {
          mutation.setError("请输入有效的操作者密码和六位未使用验证码。");
          return;
        }
        if (intent) markAttempted(context.profile, intent);
        void mutation.run(
          async (signal) => {
            if (action === "cancel")
              return {
                task: await taskAPI.cancel(
                  task.taskUUID,
                  proof,
                  context.profile.csrfToken,
                  signal,
                ),
                replayed: false,
              };
            return taskAPI.recover(
              task.taskUUID,
              intent!.key,
              proof,
              context.profile.csrfToken,
              signal,
            );
          },
          (result) => {
            if (intent) clearIntent(intent);
            done(
              {
                type: "detail",
                id: result.task.taskUUID,
                archived: result.task.archived,
              },
              action === "cancel"
                ? `服务器返回：${stateLabels[result.task.state]}。${result.task.state === "cancel_requested" ? "执行器尚未确认停止。" : result.task.state === "cancelled" ? "任务已进入取消终态。" : "请以实际状态为准。"}`
                : result.replayed
                  ? "已找到同一个恢复任务；旧任务历史保持不变。"
                  : "已创建有父任务关联的新恢复任务，执行结果待确认。",
            );
            closed();
          },
          () => {
            setProofVersion((n) => n + 1);
            reconcile();
          },
        );
      }}
    >
      <h4>{action === "cancel" ? "确认请求取消" : "确认创建恢复任务"}</h4>
      <p className="warning">
        {action === "cancel"
          ? "取消不能撤销已发生的副作用。服务器可能返回取消请求中、已取消或竞争后的实际终态。"
          : "恢复将重新校验当前权限并创建新任务，不修改原任务或重置其历史；重复确认使用同一幂等键。"}
      </p>
      {intent && (
        <Field label="恢复幂等键">
          <input readOnly value={intent.key} />
        </Field>
      )}
      <ErrorNotice error={mutation.error} />
      {!allowed && (
        <p role="status">
          任务状态已改变，当前操作已停止。请关闭确认并查看最新状态。
        </p>
      )}
      <fieldset key={proofVersion} disabled={mutation.busy || !allowed}>
        <ProofFields />
      </fieldset>
      <FormActions
        busy={mutation.busy}
        disabled={!allowed}
        cancel={() => {
          if (intent && !intent.attempted) clearIntent(intent);
          closed();
        }}
      >
        {action === "cancel" ? "确认请求取消" : "确认恢复任务"}
      </FormActions>
    </form>
  );
}
