import { useState } from "react";
import { ApiError } from "./api";
import { queryString } from "./access-api";
import {
  ErrorNotice,
  Field,
  FormActions,
  Pagination,
  ProofFields,
  getProof,
  readValues,
} from "./access-common";
import { stateLabels, supportedKind, taskAPI } from "./task-api";
import { useTaskMutation, useTaskRead, type TaskContext } from "./task-common";
import {
  maintenanceAPI,
  scheduleLabels,
  scheduleStates,
  utcSecond,
  validUTC,
  type Schedule,
  type ScheduleDefinition,
} from "./maintenance-api";
import {
  attemptMaintenanceIntent,
  beginMaintenanceIntent,
  discardMaintenanceIntent,
  maintenanceIntent,
  type MaintenanceIntent,
} from "./maintenance-intent";

export type MaintenanceView =
  | { type: "schedules" }
  | { type: "schedule-create" }
  | { type: "schedule-detail"; id: string }
  | { type: "archive" }
  | { type: "maintenance-confirm" };
type Destination =
  | MaintenanceView
  | { type: "list" }
  | { type: "detail"; id: string; archived?: boolean };
export type MaintenanceOpen = (view: Destination, message?: string) => void;
const localScope =
  "当前仅提供平台健康检查的本地周期计划与可逆归档。AD 检测、同步、关联分析及过期消息物理清理的字段和执行链路仍待冻结，不计为业务验收通过。";
const actionLabels = {
  create: "创建暂停计划",
  enable: "启用计划",
  pause: "暂停计划",
  archive: "归档所选任务",
  restore: "恢复任务可见性",
};

export function MaintenanceContent({
  view,
  context,
  open,
}: {
  view: MaintenanceView;
  context: TaskContext;
  open: MaintenanceOpen;
}) {
  return (
    <section>
      <p className="warning">{localScope}</p>
      {view.type === "schedules" && (
        <SchedulesList context={context} open={open} />
      )}
      {view.type === "schedule-create" && (
        <CreateSchedule context={context} open={open} />
      )}
      {view.type === "schedule-detail" && (
        <ScheduleDetails context={context} open={open} id={view.id} />
      )}
      {view.type === "archive" && (
        <ArchivePreview context={context} open={open} />
      )}
      {view.type === "maintenance-confirm" && (
        <MaintenanceConfirm context={context} open={open} />
      )}
    </section>
  );
}
function SchedulesList({
  context,
  open,
}: {
  context: TaskContext;
  open: MaintenanceOpen;
}) {
  const [query, setQuery] = useState({ pageIdx: 1, pageSize: 20, state: "" });
  const q = queryString(query),
    read = useTaskRead(
      q,
      (s) => maintenanceAPI.schedules(q, s),
      context.sessionChanged,
      (v) => v.schedules.some((s) => s.state === "enabled"),
    );
  return (
    <>
      <h3>周期计划</h3>
      <p>
        仅列出当前操作者创建的计划。创建后暂停，定义和授权版本不可修改；启用后按
        UTC 固定秒间隔运行。
      </p>
      <div className="actions compact">
        {context.can("POST /api/tasks/schedules/create") &&
          context.can("GET /api/tasks/kinds") && (
            <button
              disabled={!!maintenanceIntent(context.profile)?.attempted}
              onClick={() => open({ type: "schedule-create" })}
            >
              新建周期计划
            </button>
          )}
        <button className="secondary" onClick={read.refresh}>
          刷新周期计划
        </button>
      </div>
      <form
        className="filters"
        onSubmit={(e) => {
          const v = readValues(e);
          setQuery({
            pageIdx: 1,
            pageSize: Number(v.pageSize),
            state: v.state,
          });
        }}
      >
        <Field label="计划状态筛选">
          <select name="state">
            <option value="">所有状态</option>
            {scheduleStates.map((s) => (
              <option key={s} value={s}>
                {scheduleLabels[s]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="每页计划数">
          <select name="pageSize" defaultValue="20">
            {[10, 20, 50, 100].map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        </Field>
        <button>筛选周期计划</button>
      </form>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取周期计划…</p>}
      {read.data && (
        <>
          <div className="table-scroll">
            <table>
              <caption>周期计划列表</caption>
              <thead>
                <tr>
                  <th>计划 / ID</th>
                  <th>状态</th>
                  <th>UTC 锚点 / 间隔</th>
                  <th>下一次计划时间</th>
                  <th>控制版本</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {read.data.schedules.map((s) => (
                  <tr key={s.scheduleUUID}>
                    <th scope="row">
                      {s.label}
                      <small className="block">{s.scheduleUUID}</small>
                    </th>
                    <td>{scheduleLabels[s.state]}</td>
                    <td>
                      {s.startAt}
                      <small className="block">每 {s.intervalSeconds} 秒</small>
                    </td>
                    <td>{s.nextAt ?? "无"}</td>
                    <td>{s.controlVersion}</td>
                    <td>
                      {context.can("GET /api/tasks/schedules/detail") && (
                        <button
                          className="secondary"
                          onClick={() =>
                            open({
                              type: "schedule-detail",
                              id: s.scheduleUUID,
                            })
                          }
                        >
                          计划详情 {s.scheduleUUID}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!read.data.schedules.length && (
            <p role="status">没有符合条件的周期计划。</p>
          )}
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
function CreateSchedule({
  context,
  open,
}: {
  context: TaskContext;
  open: MaintenanceOpen;
}) {
  const registry = useTaskRead(
      "schedule-kinds",
      (s) => taskAPI.kinds(s),
      context.sessionChanged,
    ),
    [error, setError] = useState("");
  const [start] = useState(() => utcSecond(Date.now() + 120_000));
  const kind = registry.data?.find(supportedKind),
    blocked = !!maintenanceIntent(context.profile)?.attempted;
  return (
    <form
      onSubmit={(e) => {
        const v = readValues(e);
        if (
          !kind ||
          blocked ||
          !context.can("POST /api/tasks/schedules/create")
        )
          return;
        const interval = Number(v.intervalSeconds);
        if (
          v.label.trim() !== v.label ||
          !v.label ||
          Array.from(v.label).length > 80 ||
          /[\u0000-\u001f\u007f]/.test(v.label)
        ) {
          setError("计划名称须为 1–80 个字符，不能包含控制字符或首尾空格。");
          return;
        }
        if (
          !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(v.startAt) ||
          !validUTC(v.startAt) ||
          Date.parse(v.startAt) < Date.now() + 60_000
        ) {
          setError("首次时间须为 UTC 整秒，且至少比当前时间晚 60 秒。");
          return;
        }
        if (!Number.isInteger(interval) || interval < 60 || interval > 86400) {
          setError("间隔须为 60–86400 秒的整数。");
          return;
        }
        const input: ScheduleDefinition = {
          label: v.label,
          taskName: "infrastructure.health",
          domainId: "platform",
          payloadVersion: 1,
          payload: {},
          startAt: v.startAt,
          intervalSeconds: interval,
        };
        beginMaintenanceIntent(context.profile, { action: "create", input });
        open({ type: "maintenance-confirm" });
      }}
    >
      <h3>新建周期计划</h3>
      <p>
        固定 UTC 整秒锚点，无
        cron、时区或夏令时规则。漏过的时间点合并处理，不补跑全部历史；已有未结束任务时跳过重叠执行。
      </p>
      <ErrorNotice error={error || registry.error} />
      {registry.error && (
        <button type="button" className="secondary" onClick={registry.refresh}>
          重新读取任务登记表
        </button>
      )}
      {registry.data && !kind && (
        <p className="warning">
          服务器未登记受支持的健康检查种类，当前不能创建。
        </p>
      )}
      <fieldset disabled={blocked || !kind}>
        <Field label="计划名称">
          <input name="label" required maxLength={160} />
        </Field>
        <Field
          label="首次计划时间（UTC）"
          help="RFC3339 整秒，例如 2026-10-07T12:00:00Z；创建时至少晚于服务器当前时间 60 秒"
        >
          <input name="startAt" required defaultValue={start} />
        </Field>
        <Field label="执行间隔（秒）">
          <input
            name="intervalSeconds"
            type="number"
            min={60}
            max={86400}
            step={1}
            defaultValue={60}
            required
          />
        </Field>
        <p>任务：infrastructure.health / platform / 输入版本 1 / 空输入</p>
      </fieldset>
      <FormActions
        busy={false}
        disabled={
          !kind || blocked || !context.can("POST /api/tasks/schedules/create")
        }
        cancel={() => open({ type: "schedules" })}
      >
        核对暂停计划
      </FormActions>
    </form>
  );
}
function ScheduleSummary({ schedule: s }: { schedule: Schedule }) {
  return (
    <dl>
      {[
        ["计划 ID", s.scheduleUUID],
        ["计划名称", s.label],
        ["计划状态", scheduleLabels[s.state]],
        ["任务种类", s.taskName],
        ["范围", s.domainId],
        ["首次时间（UTC）", s.startAt],
        ["间隔（秒）", s.intervalSeconds],
        ["控制版本", s.controlVersion],
        ["下次时间（UTC）", s.nextAt ?? "无"],
        ["最近计划时间（UTC）", s.lastScheduledAt ?? "无"],
        ["最近任务 ID", s.lastTaskUUID || "无"],
        ["错误代码", s.error || "无"],
      ].map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}
const eventLabels: Record<string, string> = {
  created: "创建（暂停）",
  enabled: "启用",
  paused: "暂停",
  authorization_blocked: "授权失效",
  admitted: "已提交实际任务",
  skipped_paused: "跳过暂停时间点",
  skipped_misfire: "合并漏过时间点",
  skipped_overlap: "跳过重叠执行",
  schedule_time_overflow: "时间超出范围",
};
function ScheduleDetails({
  context,
  open,
  id,
}: {
  context: TaskContext;
  open: MaintenanceOpen;
  id: string;
}) {
  const [pageIdx, setPage] = useState(1),
    read = useTaskRead(
      `${id}:${pageIdx}`,
      (s) => maintenanceAPI.detail(id, pageIdx, s),
      context.sessionChanged,
      (v) => v.schedule.state === "enabled",
    ),
    plan = read.data?.schedule;
  const blocked = !!maintenanceIntent(context.profile)?.attempted;
  return (
    <>
      <h3>周期计划详情</h3>
      <div className="actions compact">
        <button
          className="secondary"
          onClick={() => open({ type: "schedules" })}
        >
          返回周期计划
        </button>
        <button className="secondary" onClick={read.refresh}>
          刷新计划详情
        </button>
      </div>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取计划详情…</p>}
      {plan && (
        <>
          <ScheduleSummary schedule={plan} />
          <p>
            控制版本仅在用户启用或暂停等控制变更时递增，与后台执行游标分开。暂停只阻止将来的提交，已生成任务仍按原状态继续。
          </p>
          {plan.state === "authorization_blocked" && (
            <p className="warning">
              授权版本已失效。此计划不能重新启用，须按当前权限创建新计划。
            </p>
          )}
          <div className="actions compact">
            {(["enable", "pause"] as const)
              .filter((a) =>
                a === "enable"
                  ? plan.state === "paused"
                  : plan.state === "enabled",
              )
              .map(
                (action) =>
                  context.can(`POST /api/tasks/schedules/${action}`) && (
                    <button
                      key={action}
                      disabled={blocked}
                      onClick={() => {
                        beginMaintenanceIntent(context.profile, {
                          action,
                          input: {
                            scheduleUUID: id,
                            expectedControlVersion: plan.controlVersion,
                          },
                        });
                        open({ type: "maintenance-confirm" });
                      }}
                    >
                      {actionLabels[action]}
                    </button>
                  ),
              )}
            {plan.lastTaskUUID && context.can("GET /api/tasks/detail") && (
              <button
                className="secondary"
                onClick={() =>
                  open({
                    type: "detail",
                    id: plan.lastTaskUUID,
                    archived: context.canArchiveView,
                  })
                }
              >
                查看最近实际任务
              </button>
            )}
          </div>
          <div className="table-scroll">
            <table>
              <caption>计划发生记录</caption>
              <thead>
                <tr>
                  <th>事件</th>
                  <th>发生次数 / 索引</th>
                  <th>UTC 范围</th>
                  <th>任务 ID</th>
                  <th>控制版本 / 时间</th>
                </tr>
              </thead>
              <tbody>
                {read.data!.events.map((e) => (
                  <tr key={e.id}>
                    <td>
                      {eventLabels[e.action] ?? e.action}
                      <small className="block">{e.action}</small>
                    </td>
                    <td>
                      {e.count ?? "—"} / {e.firstIndex ?? "—"} →{" "}
                      {e.lastIndex ?? "—"}
                    </td>
                    <td>
                      {e.firstAt ?? "—"}
                      <small className="block">{e.lastAt ?? "—"}</small>
                    </td>
                    <td>{e.taskUUID || "无"}</td>
                    <td>
                      {e.controlVersion}
                      <small className="block">{e.createdAt}</small>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!read.data!.events.length && <p>没有发生记录。</p>}
          <Pagination
            page={read.data!.page}
            exhausted={read.data!.exhausted}
            busy={read.busy}
            change={setPage}
          />
        </>
      )}
    </>
  );
}
function ArchivePreview({
  context,
  open,
}: {
  context: TaskContext;
  open: MaintenanceOpen;
}) {
  const [query, setQuery] = useState<{
      before: string;
      pageIdx: number;
      pageSize: number;
    } | null>(null),
    [selected, setSelected] = useState<string[]>([]),
    [error, setError] = useState("");
  const q = query ? queryString(query) : "";
  // The initial view performs no candidate read until the user supplies a cutoff.
  const read = useTaskRead(
    q,
    async (signal) => (query ? maintenanceAPI.candidates(q, signal) : null),
    context.sessionChanged,
  );
  return (
    <>
      <h3>预览任务归档</h3>
      <p>
        按任务实际终态时间筛选早于截止时间的健康检查任务。归档仅隐藏默认列表中的记录；任务、结果、幂等记录、审计和周期发生记录保留。不删除消息或释放存储。
      </p>
      <form
        onSubmit={(e) => {
          const v = readValues(e);
          if (!validUTC(v.before) || Date.parse(v.before) > Date.now()) {
            setError("请输入不晚于当前时间的 UTC 截止时间。");
            return;
          }
          setError("");
          setSelected([]);
          setQuery({
            before: v.before,
            pageIdx: 1,
            pageSize: Number(v.pageSize),
          });
          read.refresh();
        }}
      >
        <Field label="归档截止时间（UTC）">
          <input
            name="before"
            required
            defaultValue={utcSecond(Date.now() - 1000)}
          />
        </Field>
        <Field label="每页候选数">
          <select name="pageSize" defaultValue="20">
            {[10, 20, 50, 100].map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        </Field>
        <button disabled={read.busy}>预览归档候选</button>
      </form>
      <ErrorNotice error={error || read.error} />
      {read.busy && query && <p role="status">正在读取归档候选…</p>}
      {read.data && (
        <form
          onSubmit={(e) => {
            const v = readValues(e);
            if (
              !selected.length ||
              !context.can("POST /api/tasks/archive") ||
              maintenanceIntent(context.profile)?.attempted
            )
              return;
            const reason = v.reason;
            if (!validReason(reason)) {
              setError("原因须为 1–500 个字符，不能包含控制字符或首尾空格。");
              return;
            }
            beginMaintenanceIntent(context.profile, {
              action: "archive",
              input: {
                before: read.data!.before,
                reason,
                targets: read
                  .data!.tasks.filter((t) => selected.includes(t.taskUUID))
                  .map((t) => ({
                    taskUUID: t.taskUUID,
                    visibilityVersion: t.visibilityVersion,
                  })),
              },
            });
            open({ type: "maintenance-confirm" });
          }}
        >
          <p>
            本次预览截止时间：{read.data.before}
            。仅选择当前页的明确任务和版本；新的预览或翻页会清空选择。
          </p>
          <div className="table-scroll">
            <table>
              <caption>归档候选任务</caption>
              <thead>
                <tr>
                  <th>选择</th>
                  <th>任务 ID</th>
                  <th>状态</th>
                  <th>终态时间（UTC）</th>
                  <th>可见性版本</th>
                </tr>
              </thead>
              <tbody>
                {read.data.tasks.map((t) => (
                  <tr key={t.taskUUID}>
                    <td>
                      <input
                        type="checkbox"
                        aria-label={`选择归档 ${t.taskUUID}`}
                        checked={selected.includes(t.taskUUID)}
                        disabled={!context.can("POST /api/tasks/archive")}
                        onChange={(e) =>
                          setSelected(
                            e.target.checked
                              ? [...selected, t.taskUUID]
                              : selected.filter((id) => id !== t.taskUUID),
                          )
                        }
                      />
                    </td>
                    <th scope="row">{t.taskUUID}</th>
                    <td>{stateLabels[t.state]}</td>
                    <td>{t.terminalAt}</td>
                    <td>{t.visibilityVersion}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!read.data.tasks.length && (
            <p role="status">没有可归档的健康检查任务。</p>
          )}
          <Pagination
            page={read.data.page}
            exhausted={read.data.exhausted}
            busy={read.busy}
            change={(pageIdx) => {
              setSelected([]);
              setQuery({ ...query!, pageIdx });
            }}
          />
          {context.can("POST /api/tasks/archive") && (
            <>
              <Field label="归档原因">
                <input name="reason" required maxLength={1000} />
              </Field>
              <button
                disabled={
                  !selected.length ||
                  read.busy ||
                  !!maintenanceIntent(context.profile)?.attempted
                }
              >
                核对归档所选任务
              </button>
            </>
          )}
        </form>
      )}
    </>
  );
}
export const validReason = (reason: string) =>
  reason.length > 0 &&
  reason.trim() === reason &&
  Array.from(reason).length <= 500 &&
  !/[\u0000-\u001f\u007f]/.test(reason);
export function RestoreTask({
  context,
  taskUUID,
  visibilityVersion,
  open,
}: {
  context: TaskContext;
  taskUUID: string;
  visibilityVersion: number;
  open: MaintenanceOpen;
}) {
  const [error, setError] = useState("");
  return (
    <form
      onSubmit={(e) => {
        const v = readValues(e);
        if (
          !context.can("POST /api/tasks/restore") ||
          maintenanceIntent(context.profile)?.attempted
        )
          return;
        if (!validReason(v.reason)) {
          setError("原因须为 1–500 个字符，不能包含控制字符或首尾空格。");
          return;
        }
        beginMaintenanceIntent(context.profile, {
          action: "restore",
          input: {
            targets: [{ taskUUID, visibilityVersion }],
            reason: v.reason,
          },
        });
        open({ type: "maintenance-confirm" });
      }}
    >
      <h4>恢复任务可见性</h4>
      <p>
        恢复到默认任务列表；不会重新执行任务。需要重新执行时，请先恢复可见性，再使用允许的恢复任务操作。
      </p>
      <ErrorNotice error={error} />
      <Field label="恢复可见性原因">
        <input name="reason" required maxLength={1000} />
      </Field>
      <button disabled={!!maintenanceIntent(context.profile)?.attempted}>
        核对恢复可见性
      </button>
    </form>
  );
}
const intentPath = (intent: MaintenanceIntent) =>
  intent.action === "create" ||
  intent.action === "enable" ||
  intent.action === "pause"
    ? (`POST /api/tasks/schedules/${intent.action}` as const)
    : (`POST /api/tasks/${intent.action}` as const);
function MaintenanceConfirm({
  context,
  open,
}: {
  context: TaskContext;
  open: MaintenanceOpen;
}) {
  const [intent] = useState(() => maintenanceIntent(context.profile)),
    mutation = useTaskMutation(context.sessionChanged),
    [proofVersion, setProofVersion] = useState(0),
    [recheck, setRecheck] = useState(0),
    [rejected, setRejected] = useState(false);
  const read = useTaskRead(
    `confirm:${recheck}`,
    async (signal) => {
      if (!intent || !recheck) return null;
      if (intent.action === "enable" || intent.action === "pause")
        return maintenanceAPI
          .detail(intent.input.scheduleUUID, 1, signal)
          .then((v) => ({
            message: `当前计划：${scheduleLabels[v.schedule.state]}，控制版本 ${v.schedule.controlVersion}`,
          }));
      if (intent.action === "archive" || intent.action === "restore")
        return taskAPI
          .list("pageIdx=1&pageSize=20&visibility=all", signal)
          .then((v) => ({
            message: `已重新读取 ${v.tasks.length} 项真实任务；收据仅确认原操作，当前可见性以任务详情为准。`,
          }));
      return maintenanceAPI
        .schedules("pageIdx=1&pageSize=20", signal)
        .then((v) => ({
          message: `已重新读取 ${v.schedules.length} 项计划；请使用原幂等键确认这次创建对应的计划。`,
        }));
    },
    context.sessionChanged,
  );
  if (!intent)
    return (
      <>
        <p role="status">没有待核对的任务维护操作。</p>
        <button onClick={() => open({ type: "list" })}>返回任务列表</button>
      </>
    );
  const allowed = context.can(intentPath(intent));
  const destination: Destination =
    intent.action === "enable" || intent.action === "pause"
      ? { type: "schedule-detail", id: intent.input.scheduleUUID }
      : intent.action === "create"
        ? { type: "schedules" }
        : intent.action === "archive"
          ? { type: "archive" }
          : {
              type: "detail",
              id: intent.input.targets[0].taskUUID,
              archived: true,
            };
  return (
    <form
      onSubmit={(e) => {
        const v = readValues(e);
        if (mutation.busy || !allowed || rejected) return;
        const proof = getProof(v);
        if (!proof) {
          mutation.setError("请输入有效的操作者密码和六位未使用验证码。");
          return;
        }
        attemptMaintenanceIntent(context.profile, intent);
        void mutation.run(
          async (signal) => {
            if (intent.action === "create") {
              const result = await maintenanceAPI.create(
                intent.input,
                intent.key,
                proof,
                context.profile.csrfToken,
                signal,
              );
              return {
                view: {
                  type: "schedule-detail",
                  id: result.schedule.scheduleUUID,
                } as Destination,
                message: `${result.replayed ? "已确认同一计划的创建记录" : "已创建暂停计划"}；当前计划状态：${scheduleLabels[result.schedule.state]}。`,
              };
            }
            if (intent.action === "enable" || intent.action === "pause") {
              const result = await maintenanceAPI.control(
                intent.action,
                intent.input,
                intent.key,
                proof,
                context.profile.csrfToken,
                signal,
              );
              return {
                view: {
                  type: "schedule-detail",
                  id: result.schedule.scheduleUUID,
                } as Destination,
                message: `${result.replayed ? "已确认原控制操作收据" : "控制操作已登记"}；服务器当前状态：${scheduleLabels[result.schedule.state]}。暂停不会取消已经生成的任务。`,
              };
            }
            const result = await maintenanceAPI.archive(
              intent.action,
              intent.input,
              intent.key,
              proof,
              context.profile.csrfToken,
              signal,
            );
            return {
              view: {
                type: "detail",
                id: intent.input.targets[0].taskUUID,
                archived: true,
              } as Destination,
              message: `${result.replayed ? "已确认原操作收据，当前可见性可能已经变化" : "任务可见性操作已登记"}；共 ${result.receipt.targets.length} 项。下面重新读取首项任务的当前状态；没有删除或重新执行任务。`,
            };
          },
          (result) => {
            discardMaintenanceIntent(context.profile);
            open(result.view, result.message);
          },
          (error) => {
            setProofVersion((n) => n + 1);
            setRecheck((n) => n + 1);
            if (
              error instanceof ApiError &&
              [
                "visibility_conflict",
                "control_version_conflict",
                "invalid_start_at",
                "invalid_input",
                "invalid_before",
                "authorization_epoch_changed",
                "task_not_archivable",
              ].includes(error.code)
            )
              setRejected(true);
          },
        );
      }}
    >
      <h3>确认{actionLabels[intent.action]}</h3>
      <Field label="维护操作幂等键">
        <input readOnly value={intent.key} />
      </Field>
      <p className="warning">
        {intent.action === "create"
          ? "创建后暂停，首次时间、间隔、任务定义和授权版本不可修改。"
          : "请核对目标和版本。启用或暂停只影响未来提交；归档或恢复只改变可见性，不清理任务证据。"}
      </p>
      <pre className="task-json" aria-label="待确认的维护内容">
        {JSON.stringify(intent.input, null, 2)}
      </pre>
      {intent.attempted && (
        <p className="warning">
          原请求可能已生效。继续核对使用同一幂等键、相同内容和新的验证码；离开或停止等待不能撤销操作。刷新或重新登录同一账户后可继续核对；其他账户不能读取此待确认记录。
        </p>
      )}
      <ErrorNotice error={mutation.error || read.error} />
      {read.data && <p role="status">{read.data.message}</p>}
      {!allowed && <p role="status">当前权限不允许此维护操作。</p>}
      {rejected ? (
        <>
          <p role="status">
            服务器拒绝了本次内容或版本，需重新读取并重新核对。
          </p>
          <button
            type="button"
            onClick={() => {
              discardMaintenanceIntent(context.profile);
              open(destination);
            }}
          >
            重新读取并核对
          </button>
        </>
      ) : (
        <>
          <fieldset key={proofVersion} disabled={mutation.busy || !allowed}>
            <ProofFields />
          </fieldset>
          <FormActions
            busy={mutation.busy}
            disabled={!allowed}
            cancel={() => {
              if (!intent.attempted) discardMaintenanceIntent(context.profile);
              open(
                destination,
                intent.attempted
                  ? "已停止等待；请使用原维护幂等键继续核对结果。"
                  : "",
              );
            }}
          >
            {intent.attempted
              ? "使用原幂等键核对维护操作"
              : `确认${actionLabels[intent.action]}`}
          </FormActions>
        </>
      )}
    </form>
  );
}
