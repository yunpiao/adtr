import { useState } from "react";
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
let pending: { session: string; intent: Intent } | undefined;
export function discardTaskIntent() {
  pending = undefined;
  try {
    sessionStorage.removeItem(intentStorage);
  } catch {
    /* Storage may be disabled. */
  }
}
function getIntent(profile: Profile) {
  const session = `${profile.ID}:${profile.csrfToken}`;
  if (pending && pending.session !== session) discardTaskIntent();
  if (!pending) {
    try {
      const stored = JSON.parse(
        sessionStorage.getItem(intentStorage) ?? "null",
      );
      const keyValid =
        typeof stored?.key === "string" && /^[0-9a-f-]{36}$/.test(stored.key);
      if (
        stored?.owner === profile.ID &&
        stored?.username === profile.username &&
        keyValid &&
        (stored.action === "submit" ||
          (stored.action === "recover" &&
            typeof stored.taskUUID === "string" &&
            /^[0-9a-f-]{36}$/.test(stored.taskUUID)))
      ) {
        pending = {
          session,
          intent: {
            action: stored.action,
            key: stored.key,
            ...(stored.action === "recover"
              ? { taskUUID: stored.taskUUID }
              : {}),
            attempted: true,
          },
        };
      } else sessionStorage.removeItem(intentStorage);
    } catch {
      /* The in-memory key still covers navigation if storage is unavailable. */
    }
  }
  return pending?.intent;
}
function markAttempted(profile: Profile, intent: Intent) {
  intent.attempted = true;
  try {
    sessionStorage.setItem(
      intentStorage,
      JSON.stringify({
        owner: profile.ID,
        username: profile.username,
        action: intent.action,
        key: intent.key,
        taskUUID: intent.taskUUID,
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
  const intent: Intent = {
    action,
    taskUUID,
    key: crypto.randomUUID(),
    attempted: false,
  };
  pending = { session: `${profile.ID}:${profile.csrfToken}`, intent };
  return intent;
}
function clearIntent(intent: Intent) {
  if (pending?.intent === intent) discardTaskIntent();
}

type View =
  | { type: "list" }
  | { type: "submit" }
  | { type: "detail"; id: string };
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
      };
    },
    sessionChanged,
  );
  const context: TaskContext = {
    profile,
    sessionChanged,
    can: (path) => gate.data?.results[taskOperations.indexOf(path)] === true,
  };
  const open = (next: View, message = "") => {
    setView(next);
    setNotice(message);
    setRevision((n) => n + 1);
    window.history.pushState(
      {},
      "",
      `#tasks/${next.type}${next.type === "detail" ? `/${encodeURIComponent(next.id)}` : ""}`,
    );
  };
  const intent = getIntent(profile);
  return (
    <div className="task-workspace">
      <h2>后台任务</h2>
      <p className="warning">
        当前仅提供平台数据库与队列健康检查（infrastructure.health）。没有 AD
        检测、导出、远程响应或通知执行器；平台健康成功不代表 AD 业务验收通过。
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
                      : { type: "detail", id: intent.taskUUID! },
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
          {view.type === "detail" && (
            <TaskDetails
              key={view.id}
              context={context}
              id={view.id}
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
                            open({ type: "detail", id: task.taskUUID })
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
              { type: "detail", id: result.task.taskUUID },
              result.replayed
                ? "服务器确认幂等重放，返回同一个任务。请以实际任务状态为准。"
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
        busy={
          mutation.busy ||
          !kind ||
          !intent ||
          !context.can("POST /api/tasks/submit")
        }
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
  done,
}: {
  context: TaskContext;
  id: string;
  done: (view: View, notice?: string) => void;
}) {
  const read = useTaskRead(
    id,
    (signal) => taskAPI.detail(id, signal),
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
          <div className="actions compact">
            {!action &&
              !terminal(task.state) &&
              task.state !== "cancel_requested" &&
              context.can("POST /api/tasks/cancel") && (
                <button
                  className="secondary danger"
                  onClick={() => setAction("cancel")}
                >
                  请求取消任务
                </button>
              )}
            {!action &&
              ["failed", "dead_letter"].includes(task.state) &&
              context.can("POST /api/tasks/recover") && (
                <button disabled={blocked} onClick={() => setAction("recover")}>
                  创建恢复任务
                </button>
              )}
          </div>
          {action && (
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
    context.can(`POST /api/tasks/${action}`) &&
    (action === "cancel"
      ? !terminal(task.state)
      : ["failed", "dead_letter"].includes(task.state));
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
              { type: "detail", id: result.task.taskUUID },
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
        busy={mutation.busy || !allowed}
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
