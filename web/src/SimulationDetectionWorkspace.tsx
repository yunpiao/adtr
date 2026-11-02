import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationDetectionAPI as api,
  ambiguousDetectionWrite,
  parseDeclarations,
  editableRules,
  detectionContract,
  detectionProvenance,
  detectionMapping,
  type DetectionTemplate,
  type DetectionReceipt,
  type DetectionWrite,
  type WriteKind,
} from "./simulation-detection-api";
import { type SimulationDataset } from "./simulation-event-api";
import { type Task, terminal, stateLabels } from "./task-api";
type EntryProps = { profile: Profile; sessionChanged: () => void };
export default function SimulationDetectionWorkspace(props: EntryProps) {
  return (
    <SimulationDetectionEntry
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
function SimulationDetectionEntry({ profile, sessionChanged }: EntryProps) {
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
      "GET /api/simulation/detection/template",
      "GET /api/simulation/detection/task",
      "GET /api/simulation/detection/receipt",
      "POST /api/simulation/detection/create",
      "POST /api/simulation/detection/update",
      "POST /api/simulation/detection/archive",
      "POST /api/simulation/detection/run",
      "POST /api/simulation/detection/cancel",
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
          checks.results.slice(0, 7).every(Boolean);
        setGate({
          read,
          manage: read && checks.results.slice(7).every(Boolean),
        });
      })
      .catch((e: unknown) => {
        if (!live) return;
        setGate({ read: false, manage: false });
        setError("未能确认模拟检测及域选择权限，请重新核对。");
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
    <section aria-label="模拟检测入口">
      <h2>SIMULATED · 模拟检测</h2>
      <p>合成数据专用；选择已授权域，不连接真实 AD。</p>
      {error && <p role="alert">{error}</p>}
      {gate === null ? (
        <p role="status">正在核对模拟检测权限…</p>
      ) : !gate.read ? (
        <>
          <p>当前账户未获得模拟检测与域选择读取权限。</p>
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
          <SimulationDetectionDomainWorkspace
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

export type SimulationDetectionWorkspaceProps = EntryProps & {
  domainId: string;
  permissions?: { read: boolean; manage: boolean };
};
export function SimulationDetectionDomainWorkspace(
  props: SimulationDetectionWorkspaceProps,
) {
  return (
    <Workspace
      key={JSON.stringify([props.profile, props.domainId, props.permissions])}
      {...props}
    />
  );
}
const initialRules = JSON.stringify(
  [
    {
      id: "rule-1",
      enabled: true,
      combine: "all",
      conditions: [{ field: "event.category", operator: "exists", values: [] }],
    },
  ],
  null,
  2,
);
type Pending = { kind: WriteKind; input: DetectionWrite };
function Workspace({
  profile,
  sessionChanged,
  domainId,
  permissions,
}: SimulationDetectionWorkspaceProps) {
  const [datasets, setDatasets] = useState<SimulationDataset[]>([]),
    [datasetId, setDatasetId] = useState("");
  const [templates, setTemplates] = useState<DetectionTemplate[]>([]),
    [selected, setSelected] = useState<DetectionTemplate | null>(null);
  const [editing, setEditing] = useState(false),
    [id, setID] = useState(""),
    [name, setName] = useState(""),
    [rules, setRules] = useState(initialRules);
  const [task, setTask] = useState<Task | null>(null),
    [pending, setPending] = useState<Pending | null>(null),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState(""),
    [denied, setDenied] = useState(false);
  const [recoveryID, setRecoveryID] = useState("");
  const taskPin = useRef<{
    templateId: string;
    templateVersion: number;
    templateHash: string;
    datasetId: string;
    fixtureHash: string;
  } | null>(null);
  const generation = useRef(0),
    controller = useRef<AbortController | null>(null),
    locked = useRef(false),
    pendingRef = useRef<Pending | null>(null),
    changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  const canRead =
    permissions?.read === true &&
    !!domainId &&
    !denied &&
    !profile.needChangePwd &&
    !profile.isExpired;
  const canManage = canRead && permissions?.manage === true;
  const invalidate = () => {
    generation.current++;
    controller.current?.abort();
    locked.current = false;
  };
  const clear = () => {
    setDatasets([]);
    setDatasetId("");
    setTemplates([]);
    setSelected(null);
    setEditing(false);
    setID("");
    setName("");
    setRules(initialRules);
    setTask(null);
    taskPin.current = null;
    setPending(null);
    setRecoveryID("");
    pendingRef.current = null;
  };
  const failure = (e: unknown) => {
    if (
      e instanceof ApiError &&
      ([401, 403, 404].includes(e.status) ||
        e.code === "password_change_required")
    ) {
      clear();
      setDenied(true);
      setNotice(
        "当前会话、权限或记录范围已失效，已清除模板、任务和回执；请重新选择域并核验权限。",
      );
      if (e.status === 401 || e.code === "password_change_required")
        changed.current();
      return;
    }
    const code = e instanceof ApiError ? e.code : "network";
    setNotice(
      code === "version_conflict" ||
        code === "revision_conflict" ||
        code === "conflict"
        ? "模板版本已变化，请重新读取模板后再决定。"
        : `请求未取得可确认结果（${code}）。读取可重试；写入结果未知时请核验原操作。`,
    );
  };
  const run = async (work: (signal: AbortSignal) => Promise<void>) => {
    if (locked.current || !canRead) return;
    locked.current = true;
    setBusy(true);
    setNotice("");
    const seq = ++generation.current,
      c = new AbortController();
    controller.current = c;
    // Every continuation must use this signal; abort prevents stale application even with mocked late replies.
    try {
      await work(c.signal);
    } catch (e) {
      if (seq === generation.current && !c.signal.aborted) failure(e);
    } finally {
      if (seq === generation.current && !c.signal.aborted) {
        locked.current = false;
        setBusy(false);
      }
    }
  };
  const live = (s: AbortSignal) => !s.aborted;
  const load = async (s: AbortSignal) => {
    const [d, t] = await Promise.all([
      api.datasets(domainId, profile, s),
      api.templates(domainId, profile, s),
    ]);
    if (!live(s)) return;
    setDatasets(d.datasets);
    setTemplates(t.templates);
  };
  useEffect(() => {
    if (canRead) void run(load);
    const hints = listenForSessionInvalidation(() => {
      invalidate();
      clear();
      setDenied(true);
      changed.current();
    });
    return () => {
      invalidate();
      hints.close();
    };
  }, []);
  const open = (t: DetectionTemplate) =>
    void run(async (s) => {
      setSelected(null);
      setEditing(false);
      const r = await api.template(domainId, t.id, profile, s);
      if (!live(s)) return;
      if (
        r.template.id !== t.id ||
        r.template.rules.some((r) => r.scope.domain !== domainId)
      )
        throw new ApiError("invalid_response");
      setSelected(r.template);
      setID(r.template.id);
      setName(r.template.name);
      setRules(JSON.stringify(editableRules(r.template), null, 2));
      setEditing(true);
    });
  const accept = (r: DetectionReceipt, p: Pending) => {
    if (r.operationId !== p.input.operationId || r.kind !== p.kind)
      throw new ApiError("invalid_response");
    const next = r.submission?.task ?? r.task;
    if (
      next &&
      (next.domainId !== domainId ||
        (p.kind === "cancel" && next.taskUUID !== p.input.taskUUID))
    )
      throw new ApiError("invalid_response");
    if (
      r.template &&
      (r.template.id !== p.input.templateId ||
        r.template.rules.some((x) => x.scope.domain !== domainId))
    )
      throw new ApiError("invalid_response");
    if (next?.state === "succeeded" && p.kind === "run") {
      const value = next.result as Record<string, unknown>;
      if (
        value.templateId !== p.input.templateId ||
        value.templateVersion !== p.input.templateVersion ||
        value.datasetId !== p.input.datasetId
      )
        throw new ApiError("invalid_response");
    }
    if (r.template) {
      setSelected(r.template);
      setTemplates((list) => [
        ...list.filter((t) => t.id !== r.template!.id),
        r.template!,
      ]);
      setID(r.template.id);
      setName(r.template.name);
      setRules(JSON.stringify(editableRules(r.template), null, 2));
      setEditing(!r.template.archived);
    }
    if (next) {
      if (p.kind === "run") {
        const dataset = datasets.find((d) => d.id === p.input.datasetId);
        taskPin.current =
          selected && dataset
            ? {
                templateId: selected.id,
                templateVersion: p.input.templateVersion!,
                templateHash: selected.hash,
                datasetId: dataset.id,
                fixtureHash: dataset.fixtureHash,
              }
            : null;
      }
      setTask(next);
    }
    setPending(null);
    pendingRef.current = null;
    setNotice(`SIMULATED · 已确认 ${r.kind} 回执，操作 ID：${r.operationId}`);
  };
  const send = (p: Pending) =>
    void run(async (s) => {
      try {
        const r = await api.write(p.kind, p.input, profile, s);
        if (live(s)) accept(r, p);
      } catch (e) {
        if (!live(s)) return;
        if (!ambiguousDetectionWrite(e)) {
          setPending(null);
          pendingRef.current = null;
        }
        throw e;
      }
    });
  const write = (kind: WriteKind) => {
    if (!canManage || locked.current || pendingRef.current) return;
    let input: DetectionWrite = { domainId, operationId: crypto.randomUUID() };
    if (kind === "create" || kind === "update") {
      if (
        !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(id) ||
        !name.trim() ||
        new TextEncoder().encode(name).length > 128
      ) {
        setNotice(
          "模板 ID 需要 1–128 个字母、数字、点、下划线或连字符；名称需要 1–128 字节。",
        );
        return;
      }
      try {
        input = {
          ...input,
          templateId: id,
          name,
          rules: parseDeclarations(rules),
          ...(kind === "update" ? { expectedVersion: selected?.version } : {}),
        };
      } catch (e) {
        setNotice((e as Error).message);
        return;
      }
    } else if (kind === "archive") {
      if (!selected) return;
      input = {
        ...input,
        templateId: selected.id,
        expectedVersion: selected.version,
      };
    } else if (kind === "run") {
      if (!selected || selected.archived || !datasetId) return;
      input = {
        ...input,
        templateId: selected.id,
        templateVersion: selected.version,
        datasetId,
      };
    } else {
      if (!task || terminal(task.state)) return;
      input = { ...input, taskUUID: task.taskUUID };
    }
    const p = { kind, input };
    pendingRef.current = p;
    setPending(p);
    send(p);
  };
  const recover = () => {
    const p = pendingRef.current;
    if (!p || !canManage) return;
    void run(async (s) => {
      try {
        const r = await api.receipt(domainId, p.input.operationId, profile, s);
        if (live(s)) accept(r, p);
      } catch (e) {
        if (!live(s)) return;
        if (
          e instanceof ApiError &&
          e.status === 404 &&
          e.code === "operation_not_found"
        ) {
          setNotice(
            "服务器尚无原操作回执；保留原操作 ID 和原始内容，可安全重试相同请求。",
          );
          return;
        }
        throw e;
      }
    });
  };
  const restoreReceipt = () => {
    if (!recoveryID.trim() || pendingRef.current) return;
    const operationId = recoveryID.trim();
    void run(async (signal) => {
      const receipt = await api.receipt(domainId, operationId, profile, signal);
      if (!live(signal)) return;
      const recoveredTask = receipt.submission?.task ?? receipt.task;
      if (
        receipt.operationId !== operationId ||
        (recoveredTask && recoveredTask.domainId !== domainId) ||
        receipt.template?.rules.some((rule) => rule.scope.domain !== domainId)
      )
        throw new ApiError("invalid_response");
      if (recoveredTask) {
        taskPin.current = null;
        setTask(recoveredTask);
      }
      if (receipt.template) {
        setSelected(receipt.template);
        setEditing(false);
      }
      setNotice(
        `SIMULATED · 已恢复服务器回执 ${operationId}，操作 ${receipt.kind}。`,
      );
    });
  };
  const refreshTask = () => {
    if (!task) return;
    const previous = task;
    void run(async (s) => {
      const r = await api.task(domainId, previous.taskUUID, profile, s);
      if (!live(s)) return;
      if (r.task.taskUUID !== previous.taskUUID || r.task.domainId !== domainId)
        throw new ApiError("invalid_response");
      if (r.task.state === "succeeded" && taskPin.current) {
        const result = r.task.result as Record<string, unknown>;
        if (
          Object.entries(taskPin.current).some(
            ([key, value]) => result[key] !== value,
          )
        )
          throw new ApiError("invalid_response");
      }
      setTask(r.task);
    });
  };
  return (
    <section aria-label="模拟检测模板">
      <h3>SIMULATED · 检测模板与手动运行</h3>
      <p>
        仅合成数据；无 AD
        连接、阻断、脚本或外部通知。版本固定运行，不执行本地模拟成功。
      </p>
      <p>
        声明契约：{detectionContract} · 来源：{detectionProvenance} · 映射：
        {detectionMapping}
      </p>
      {notice && <p role="status">{notice}</p>}
      {!canRead ? (
        <p>当前无法读取模拟检测，请重新核对授权。</p>
      ) : (
        <>
          <button disabled={busy || !!pending} onClick={() => void run(load)}>
            刷新检测模板
          </button>
          {canManage && (
            <button
              disabled={busy || !!pending}
              onClick={() => {
                setSelected(null);
                setEditing(true);
                setID("");
                setName("");
                setRules(initialRules);
              }}
            >
              新建模拟模板
            </button>
          )}
          <ul>
            {templates.map((t) => (
              <li key={t.id}>
                <button disabled={busy || !!pending} onClick={() => open(t)}>
                  查看模板 {t.name}
                </button>{" "}
                · v{t.version} · {t.archived ? "已归档" : "可运行"}
              </li>
            ))}
          </ul>
          {selected && (
            <p>
              模板 {selected.id} · 固定版本 {selected.version} · SHA-256{" "}
              {selected.hash} · {selected.archived ? "已归档" : "活动"}
            </p>
          )}
          {editing && (
            <fieldset disabled={busy || !!pending || !canManage}>
              <legend>
                {selected ? "编辑模板（保存生成新版本）" : "创建有界声明模板"}
              </legend>
              <label>
                模板 ID
                <input
                  aria-label="模板 ID"
                  value={id}
                  disabled={!!selected}
                  onChange={(e) => setID(e.target.value)}
                />
              </label>
              <label>
                模板名称
                <input
                  aria-label="模板名称"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label>
                检测规则 JSON
                <textarea
                  aria-label="检测规则 JSON"
                  rows={12}
                  value={rules}
                  onChange={(e) => setRules(e.target.value)}
                />
              </label>
              <p>
                最多 128 条规则，每条最多 32
                个条件。字段：event.category、event.outcome、actor.name、target.name、source.ip；event.code/object.kind
                缺失时不匹配（包括
                neq）。运算符：exists、eq、neq、contains、prefix、in；不支持正则或脚本。scope、version、contract、provenance
                由服务器固定。
              </p>
              <button onClick={() => write(selected ? "update" : "create")}>
                {selected ? "保存模板新版本" : "保存新模板"}
              </button>
            </fieldset>
          )}
          {selected && !selected.archived && canManage && (
            <button
              disabled={busy || !!pending}
              onClick={() => write("archive")}
            >
              归档模拟模板
            </button>
          )}
          <label>
            检测合成数据集
            <select
              aria-label="检测合成数据集"
              disabled={busy || !!pending}
              value={datasetId}
              onChange={(e) => setDatasetId(e.target.value)}
            >
              <option value="">请选择不可变合成数据集</option>
              {datasets.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.fixture} · {d.id}
                </option>
              ))}
            </select>
          </label>
          {datasets
            .filter((d) => d.id === datasetId)
            .map((d) => (
              <p key={d.id}>
                SIMULATED · {d.contract} · SHA-256 {d.fixtureHash} ·{" "}
                {d.eventCount} 个事件
              </p>
            ))}
          <button
            disabled={
              !canManage ||
              busy ||
              !!pending ||
              !selected ||
              selected.archived ||
              !datasetId
            }
            onClick={() => write("run")}
          >
            手动运行模拟检测
          </button>
          <label>
            恢复操作 ID
            <input
              aria-label="恢复操作 ID"
              value={recoveryID}
              disabled={busy || !!pending}
              onChange={(event) => setRecoveryID(event.target.value)}
            />
          </label>
          <button
            disabled={busy || !!pending || !recoveryID.trim()}
            onClick={restoreReceipt}
          >
            恢复服务器回执
          </button>
          {pending && (
            <aside>
              <p>
                原操作 ID：{pending.input.operationId}
                。结果未确认期间不能新建写操作。
              </p>
              <button disabled={busy || !canManage} onClick={recover}>
                核验原操作
              </button>
              <button
                disabled={busy || !canManage}
                onClick={() => send(pending)}
              >
                使用原操作 ID 重试
              </button>
            </aside>
          )}
          {task && (
            <section aria-label="模拟检测任务">
              <h4>SIMULATED · {stateLabels[task.state]}</h4>
              <p>
                任务 {task.taskUUID} · 进度 {task.progress}% · 尝试{" "}
                {task.attempt}/{task.maxAttempts} · 结果版本{" "}
                {task.resultVersion}
              </p>
              <p>
                以服务器状态为准；取消请求须等待终态确认，失败后不会宣称检测成功。
              </p>
              {task.error && <p>{task.error}</p>}
              <button disabled={busy} onClick={refreshTask}>
                刷新检测任务
              </button>
              {!terminal(task.state) && (
                <button
                  disabled={!canManage || busy || !!pending}
                  onClick={() => write("cancel")}
                >
                  取消检测任务
                </button>
              )}
              {task.state === "succeeded" && (
                <pre aria-label="SIMULATED 检测结果">
                  {JSON.stringify(task.result, null, 2)}
                </pre>
              )}
            </section>
          )}
        </>
      )}
    </section>
  );
}
