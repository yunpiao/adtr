import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationDetectionAPI,
  ambiguousDetectionWrite,
  type DetectionTemplate,
} from "./simulation-detection-api";
import { type SimulationDataset } from "./simulation-event-api";
import { type Task, stateLabels } from "./task-api";
import {
  simulationDetectionSchedulesAPI as api,
  type Schedule,
  type Detail,
  type List,
  type Write,
  type WriteKind,
  type Change,
} from "./simulation-detection-schedules-api";
type EntryProps = { profile: Profile; sessionChanged: () => void };
export default function SimulationDetectionSchedulesWorkspace(
  props: EntryProps,
) {
  return (
    <SimulationDetectionSchedulesEntry
      key={JSON.stringify([
        props.profile.ID,
        props.profile.username,
        props.profile.csrfToken,
        props.profile.role,
        props.profile.priv,
        props.profile.needChangePwd,
        props.profile.isExpired,
      ])}
      {...props}
    />
  );
}
function SimulationDetectionSchedulesEntry({
  profile,
  sessionChanged,
}: EntryProps) {
  const [gate, setGate] = useState<{ read: boolean; manage: boolean } | null>(
    null,
  );
  const [domainId, setDomainId] = useState("");
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  useEffect(() => {
    let live = true;
    const ctrl = new AbortController();
    setGate(null);
    setDomainId("");
    setError("");
    const paths = [
      "GET /api/domain-selection",
      "GET /api/domain-selection/resolve",
      "GET /api/simulation/detection/datasets",
      "GET /api/simulation/detection/templates",
      ...["list", "detail", "receipt", "task"].map(
        (p) => `GET /api/simulation/detection-schedules/${p}`,
      ),
      ...["create", "update", "enable", "pause", "archive"].map(
        (p) => `POST /api/simulation/detection-schedules/${p}`,
      ),
    ];
    void Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", ctrl.signal),
      accessRequest<{ results: boolean[] }>(
        "/check",
        ctrl.signal,
        { paths },
        profile.csrfToken,
      ),
    ])
      .then(([menu, checks]) => {
        if (!live) return;
        if (
          !Array.isArray(menu.menu) ||
          !Array.isArray(checks.results) ||
          checks.results.length !== paths.length ||
          checks.results.some((v) => typeof v !== "boolean")
        )
          throw new ApiError("invalid_response");
        const readable = (mark: string) =>
          menu.menu.some((p) => p.mark === mark && p.auth?.readable === true);
        const read =
          readable("domains") &&
          readable("simulation_detection") &&
          readable("simulation_detection_schedules") &&
          checks.results.slice(0, 8).every(Boolean);
        setGate({
          read,
          manage: read && checks.results.slice(8).every(Boolean),
        });
      })
      .catch((e: unknown) => {
        if (!live) return;
        setGate({ read: false, manage: false });
        setError("未能确认模拟定时检测及域选择权限，请重新核对。");
        if (
          e instanceof ApiError &&
          (e.status === 401 || e.code === "password_change_required")
        )
          changed.current();
      });
    const hints = listenForSessionInvalidation(() => {
      live = false;
      ctrl.abort();
      setGate(null);
      setDomainId("");
      changed.current();
    });
    return () => {
      live = false;
      ctrl.abort();
      hints.close();
    };
  }, [profile.csrfToken, revision]);
  return (
    <section aria-label="模拟定时检测入口">
      <h2>SIMULATED · 模拟定时检测</h2>
      <p>合成数据专用；选择已授权域，不连接真实 AD。</p>
      {error && <p role="alert">{error}</p>}
      {gate === null ? (
        <p role="status">正在核对模拟定时检测权限…</p>
      ) : !gate.read ? (
        <>
          <p>当前账户未获得模拟定时检测与域选择读取权限。</p>
          <button onClick={() => setRevision((n) => n + 1)}>
            重新核对权限
          </button>
        </>
      ) : !domainId ? (
        <SavedSourcePicker
          userID={profile.ID}
          sessionChanged={sessionChanged}
          onResolved={(value) => setDomainId(value.selection.domainId)}
        />
      ) : (
        <>
          <button onClick={() => setDomainId("")}>切换模拟域</button>
          <SimulationDetectionSchedulesDomainWorkspace
            profile={profile}
            sessionChanged={sessionChanged}
            domainId={domainId}
            permissions={gate}
          />
        </>
      )}
    </section>
  );
}

export type WorkspaceProps = EntryProps & {
  domainId: string;
  permissions?: { read: boolean; manage: boolean };
};
export function SimulationDetectionSchedulesDomainWorkspace(
  props: WorkspaceProps,
) {
  return (
    <Workspace
      key={JSON.stringify([props.profile, props.domainId, props.permissions])}
      {...props}
    />
  );
}
const labels: Record<Schedule["state"], string> = {
  paused: "已暂停",
  enabled: "已启用",
  archived: "已归档",
  authorization_blocked: "授权阻断",
  configuration_blocked: "配置阻断",
};
const events: Record<string, string> = {
  created: "创建（暂停）",
  edited: "编辑新修订（暂停）",
  enabled: "启用",
  paused: "暂停",
  archived: "归档",
  admitted: "已提交执行",
  skipped_misfire: "错过时窗，已跳过",
  skipped_overlap: "上一执行未结束，已跳过",
  skipped_paused: "暂停期间，已跳过",
  authorization_blocked: "授权阻断",
  configuration_blocked: "配置阻断",
};
function Workspace({
  profile,
  sessionChanged,
  domainId,
  permissions,
}: WorkspaceProps) {
  const [list, setList] = useState<List | null>(null),
    [detail, setDetail] = useState<Detail | null>(null),
    [task, setTask] = useState<Task | null>(null);
  const [templates, setTemplates] = useState<DetectionTemplate[]>([]),
    [datasets, setDatasets] = useState<SimulationDataset[]>([]);
  const [label, setLabel] = useState(""),
    [cycle, setCycle] = useState<0 | 1 | 2>(0),
    [templateId, setTemplateId] = useState(""),
    [datasetId, setDatasetId] = useState("");
  const [editing, setEditing] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [denied, setDenied] = useState(false);
  const [pending, setPending] = useState<{
      kind: WriteKind;
      input: Write;
    } | null>(null),
    [recovery, setRecovery] = useState(""),
    [page, setPage] = useState(1),
    [historyPage, setHistoryPage] = useState(1);
  const alive = useRef(true),
    ctrl = useRef<AbortController | null>(null),
    lock = useRef(false);
  const clear = () => {
    setList(null);
    setDetail(null);
    setTask(null);
    setTemplates([]);
    setDatasets([]);
    setEditing(false);
  };
  const failure = (e: unknown) => {
    if (
      e instanceof ApiError &&
      [401, 403, 404].includes(e.status) &&
      e.code !== "operation_not_found"
    ) {
      clear();
      setDenied(true);
      if (e.status === 401) sessionChanged();
    }
    setError(
      e instanceof ApiError && e.code === "revision_conflict"
        ? "版本已变化：刷新当前状态后重新编辑。"
        : e instanceof ApiError && e.code === "configuration_changed"
          ? "模板配置已变化：刷新后选择当前模板，保存新修订。"
          : "读取或操作未完成，请刷新核对；结果不确定时先恢复原操作。",
    );
  };
  async function run(fn: (s: AbortSignal) => Promise<void>) {
    if (lock.current || denied) return;
    lock.current = true;
    setBusy(true);
    setError("");
    const c = new AbortController();
    ctrl.current = c;
    try {
      await fn(c.signal);
    } catch (e) {
      if (alive.current && !c.signal.aborted) failure(e);
    } finally {
      if (alive.current && !c.signal.aborted) {
        lock.current = false;
        setBusy(false);
      }
    }
  }
  async function reload(
    s: AbortSignal,
    index = page,
    id = detail?.schedule.id,
    hp = historyPage,
  ) {
    const [rows, ts, ds, dt] = await Promise.all([
      api.list(domainId, index, profile, s),
      simulationDetectionAPI.templates(domainId, profile, s),
      simulationDetectionAPI.datasets(domainId, profile, s),
      id ? api.detail(domainId, id, hp, profile, s) : Promise.resolve(null),
    ]);
    if (!alive.current || s.aborted) return;
    setList(rows);
    setTemplates(ts.templates);
    setDatasets(ds.datasets);
    setDetail(dt);
    setTask(null);
    setPage(index);
    setHistoryPage(hp);
    setEditing(false);
  }
  useEffect(() => {
    alive.current = true;
    if (permissions?.read) void run((s) => reload(s));
    const listener = listenForSessionInvalidation(() => {
      alive.current = false;
      ctrl.current?.abort();
      clear();
      setDenied(true);
      sessionChanged();
    });
    return () => {
      alive.current = false;
      ctrl.current?.abort();
      listener.close();
    };
    // The keyed wrapper remounts for every actor, domain, credential and gate change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const choose = (v: Schedule) => {
    setLabel(v.label);
    setCycle(v.cycle);
    setTemplateId(v.templateId);
    setDatasetId(v.datasetId);
    setEditing(true);
  };
  async function accept(c: Change, s: AbortSignal, operationId: string) {
    if (c.receipt.operationId !== operationId)
      throw new ApiError("invalid_response");
    if (!alive.current || s.aborted) return;
    setPending(null);
    setRecovery("");
    setNotice(
      c.replayed
        ? "已恢复原操作；下方显示当前状态，不以历史回执覆盖。"
        : "操作已保存；已重新读取当前状态。",
    );
    await reload(s, page, c.schedule.id, 1);
  }
  function write(kind: WriteKind) {
    if (!permissions?.manage || pending || busy || lock.current) return;
    const input: Write = { domainId, operationId: crypto.randomUUID() };
    if (kind !== "create") {
      if (!detail) return;
      input.id = detail.schedule.id;
      input.expectedControlVersion = detail.schedule.controlVersion;
    }
    if (kind === "create" || kind === "update") {
      const t = templates.find((t) => t.id === templateId && !t.archived);
      if (
        !label.trim() ||
        [...label.trim()].length > 80 ||
        !t ||
        !datasets.some((d) => d.id === datasetId)
      ) {
        setError("填写 1–80 字名称并选择有效模板与合成数据集。");
        return;
      }
      Object.assign(input, {
        label: label.trim(),
        cycle,
        templateId,
        templateVersion: t.version,
        datasetId,
      });
    }
    const p = { kind, input };
    setPending(p);
    setRecovery(input.operationId);
    void run(async (s) => {
      try {
        await accept(
          await api.write(kind, input, profile, s),
          s,
          input.operationId,
        );
      } catch (e) {
        if (alive.current && !s.aborted && !ambiguousDetectionWrite(e))
          setPending(null);
        throw e;
      }
    });
  }
  const recover = () =>
    void run(async (s) => {
      const id = pending?.input.operationId ?? recovery.trim();
      if (!id) return;
      try {
        await accept(await api.receipt(domainId, id, profile, s), s, id);
      } catch (e) {
        if (
          e instanceof ApiError &&
          e.code === "operation_not_found" &&
          pending
        ) {
          await accept(
            await api.write(pending.kind, pending.input, profile, s),
            s,
            id,
          );
          return;
        }
        throw e;
      }
    });
  if (!permissions?.read || denied)
    return (
      <p role="alert">
        当前域的模拟检测及定时检测权限未确认或已失效；请重新进入并核对权限。
      </p>
    );
  const selected = detail?.schedule;
  return (
    <section aria-label="模拟定时检测工作区">
      <h3>SIMULATED · 定时检测 · {domainId}</h3>
      <p>
        UTC 每日 / 每周 /
        每月；以服务端创建或编辑时间为锚点，月末按原始日期钳制。创建和编辑后均暂停，需显式启用。
      </p>
      <p>
        仅合成数据；无真实 AD、包速率控制。超过 60
        秒的到期记录跳过、不补跑；重叠跳过；失败不自动重试。暂停/归档只停止未来提交，不取消已运行任务。
      </p>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {busy && <p role="status">正在读取或保存…</p>}
      <button disabled={busy} onClick={() => void run((s) => reload(s))}>
        刷新当前状态
      </button>
      <fieldset disabled={busy}>
        <legend>恢复操作</legend>
        <label>
          操作 ID
          <input
            value={recovery}
            onChange={(e) => setRecovery(e.target.value)}
            readOnly={!!pending}
          />
        </label>
        <button disabled={!recovery.trim()} onClick={recover}>
          核对回执并恢复
        </button>
        <p>
          结果不确定时保留相同操作 ID；先查回执，确认不存在后才重发同一请求。
        </p>
      </fieldset>
      <h4>计划列表</h4>
      {!list ? (
        <p>尚未读取计划。</p>
      ) : list.schedules.length === 0 ? (
        <p>当前页没有计划。</p>
      ) : (
        <ul>
          {list.schedules.map((v) => (
            <li key={v.id}>
              <button
                disabled={busy || !!pending}
                onClick={() => void run((s) => reload(s, page, v.id, 1))}
              >
                {v.label}
              </button>{" "}
              — {labels[v.state]} · 修订 {v.revision}
            </li>
          ))}
        </ul>
      )}
      <button
        disabled={busy || !!pending || page <= 1}
        onClick={() => void run((s) => reload(s, page - 1))}
      >
        上一页计划
      </button>
      <span>第 {page} 页</span>
      <button
        disabled={busy || !!pending || !list || page >= list.page.totalPage}
        onClick={() => void run((s) => reload(s, page + 1))}
      >
        下一页计划
      </button>
      {selected && (
        <section aria-label="计划详情">
          <h4>
            {selected.label} · {labels[selected.state]}
          </h4>
          <p>
            修订 {selected.revision} · 控制版本 {selected.controlVersion} · 模板{" "}
            {selected.templateId} v{selected.templateVersion}
          </p>
          <p>
            UTC 锚点 {selected.anchor} · 下一时间点 {selected.nextAt}
            （只有启用状态才可能提交）
          </p>
          <p>
            模板摘要 {selected.pinned.templateHash} · 数据集摘要{" "}
            {selected.pinned.fixtureHash}
          </p>
          {selected.state === "authorization_blocked" && (
            <p role="alert">
              授权已失效，此计划不能恢复；重新确认权限后创建替代计划。
            </p>
          )}
          {selected.state === "configuration_blocked" && (
            <p role="alert">
              固定模板版本已变化或归档；选择当前有效模板并保存新修订。
            </p>
          )}
          {selected.error && <p>原因：{selected.error}</p>}
          <button
            disabled={
              busy ||
              !!pending ||
              !permissions.manage ||
              ["archived", "authorization_blocked"].includes(selected.state)
            }
            onClick={() => choose(selected)}
          >
            编辑计划
          </button>
          {(["enable", "pause", "archive"] as const).map((k) => (
            <button
              key={k}
              disabled={
                busy ||
                !!pending ||
                !permissions.manage ||
                ["archived", "authorization_blocked"].includes(
                  selected.state,
                ) ||
                (k === "enable" && !["paused"].includes(selected.state)) ||
                (k === "pause" && selected.state !== "enabled")
              }
              onClick={() => write(k)}
            >
              {
                { enable: "启用计划", pause: "暂停计划", archive: "归档计划" }[
                  k
                ]
              }
            </button>
          ))}
          <h5>修订与发生记录</h5>
          {detail.events.length === 0 ? (
            <p>暂无发生记录。</p>
          ) : (
            <ul>
              {detail.events.map((e) => (
                <li key={e.id}>
                  {events[e.action] ?? e.action} · 修订 {e.revision} ·{" "}
                  {e.createdAt}
                  {e.firstIndex !== undefined &&
                    ` · 发生序号 ${e.firstIndex}–${e.lastIndex ?? e.firstIndex}`}
                  {e.taskId && (
                    <button
                      disabled={busy}
                      onClick={() =>
                        void run(async (s) => {
                          setTask(null);
                          const r = await api.task(
                            domainId,
                            e.taskId!,
                            profile,
                            s,
                          );
                          if (alive.current && !s.aborted) setTask(r.task);
                        })
                      }
                    >
                      查看执行 {e.taskId}
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
          <button
            disabled={busy || historyPage <= 1}
            onClick={() =>
              void run((s) => reload(s, page, selected.id, historyPage - 1))
            }
          >
            上一页记录
          </button>
          <span>记录第 {historyPage} 页</span>
          <button
            disabled={busy || !detail.hasMore}
            onClick={() =>
              void run((s) => reload(s, page, selected.id, historyPage + 1))
            }
          >
            下一页记录
          </button>
        </section>
      )}
      {task && (
        <section aria-label="发生执行结果">
          <h4>{stateLabels[task.state]}</h4>
          <p>
            任务 {task.taskUUID} · {task.error || "无错误码"} · 尝试{" "}
            {task.attempt}/{task.maxAttempts}
          </p>
          {task.state === "failed" && (
            <p role="alert">
              此发生执行失败，已消耗本次机会；未来周期是新的发生，不是重试。
            </p>
          )}
          {task.state === "succeeded" && (
            <>
              <p>合成评估成功，不代表真实 AD 产品验收。</p>
              <pre>{JSON.stringify(task.result, null, 2)}</pre>
            </>
          )}
          <button
            disabled={busy}
            onClick={() =>
              void run(async (s) => {
                setTask(null);
                const r = await api.task(domainId, task.taskUUID, profile, s);
                if (alive.current && !s.aborted) setTask(r.task);
              })
            }
          >
            刷新执行结果
          </button>
        </section>
      )}
      <h4>{editing ? "编辑为新修订" : "创建暂停计划"}</h4>
      <fieldset disabled={busy || !!pending || !permissions.manage}>
        <legend>计划定义</legend>
        <label>
          计划名称
          <input
            value={label}
            maxLength={80}
            onChange={(e) => setLabel(e.target.value)}
          />
        </label>
        <label>
          UTC 周期
          <select
            value={cycle}
            onChange={(e) => setCycle(Number(e.target.value) as 0 | 1 | 2)}
          >
            <option value={0}>每日</option>
            <option value={1}>每周</option>
            <option value={2}>每月</option>
          </select>
        </label>
        <label>
          检测模板
          <select
            value={templateId}
            onChange={(e) => setTemplateId(e.target.value)}
          >
            <option value="">选择当前有效模板</option>
            {templates
              .filter((t) => !t.archived)
              .map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name} · v{t.version}
                </option>
              ))}
          </select>
        </label>
        <label>
          合成数据集
          <select
            value={datasetId}
            onChange={(e) => setDatasetId(e.target.value)}
          >
            <option value="">选择数据集</option>
            {datasets.map((d) => (
              <option key={d.id} value={d.id}>
                {d.id}
              </option>
            ))}
          </select>
        </label>
        <button onClick={() => write(editing ? "update" : "create")}>
          {editing ? "保存新修订并暂停" : "创建暂停计划"}
        </button>
        {editing && <button onClick={() => setEditing(false)}>取消编辑</button>}
      </fieldset>
      {!permissions.manage && (
        <p>只读：管理计划需要同时获得模拟检测及定时检测写入权限。</p>
      )}
    </section>
  );
}
