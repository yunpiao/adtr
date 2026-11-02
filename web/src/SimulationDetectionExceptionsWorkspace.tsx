import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationDetectionExceptionsAPI as api,
  exceptionFields,
  validDeclaration,
  parseDraftImport,
  verifyReceipt,
  downloadExceptions,
  type Declaration,
  type ExceptionRecord,
  type ExceptionEntry,
  type Evaluation,
  type EvaluationSummary,
  type Receipt,
  type Write,
  type Action,
} from "./simulation-detection-exceptions-api";
type Props = { profile: Profile; sessionChanged: () => void };
export type ExceptionPermissions = {
  read: boolean;
  manage: boolean;
  source: boolean;
};
const prefix = "/api/simulation/detection-exceptions";
const readPaths = [
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
  ...["snapshot", "history", "export", "receipt"].map(
    (x) => `GET ${prefix}/${x}`,
  ),
];
const managePaths = [
  "create",
  "edit",
  "enable",
  "disable",
  "archive",
  "import",
].map((x) => `POST ${prefix}/${x}`);
const sourcePaths = [
  `POST ${prefix}/preview`,
  `POST ${prefix}/evaluate`,
  `GET ${prefix}/evaluation`,
];
export default function SimulationDetectionExceptionsWorkspace(props: Props) {
  return <Entry key={JSON.stringify(props.profile)} {...props} />;
}
function Entry({ profile, sessionChanged }: Props) {
  const [gate, setGate] = useState<ExceptionPermissions | null>(null),
    [domain, setDomain] = useState(""),
    [error, setError] = useState(""),
    [retry, setRetry] = useState(0);
  useEffect(() => {
    let live = true;
    const c = new AbortController(),
      paths = [...readPaths, ...managePaths, ...sourcePaths];
    setGate(null);
    setDomain("");
    Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", c.signal),
      accessRequest<{ results: boolean[] }>(
        "/check",
        c.signal,
        { paths },
        profile.csrfToken,
      ),
    ])
      .then(([m, r]) => {
        if (!live) return;
        if (
          !Array.isArray(r.results) ||
          r.results.length !== paths.length ||
          r.results.some((x) => typeof x !== "boolean")
        )
          throw new ApiError("invalid_response");
        const read =
          m.menu.some(
            (x) =>
              x.mark === "simulation_detection_exceptions" && x.auth?.readable,
          ) && r.results.slice(0, readPaths.length).every(Boolean);
        setGate({
          read,
          manage:
            read &&
            r.results
              .slice(readPaths.length, readPaths.length + managePaths.length)
              .every(Boolean),
          source: read && r.results.at(-1) === true,
        });
      })
      .catch((e) => {
        if (!live) return;
        setGate({ read: false, manage: false, source: false });
        setError("权限核对失败。");
        if (e instanceof ApiError && e.status === 401) sessionChanged();
      });
    const notice = listenForSessionInvalidation(() => {
      live = false;
      c.abort();
      setGate(null);
      setDomain("");
      sessionChanged();
    });
    return () => {
      live = false;
      c.abort();
      notice.close();
    };
  }, [profile.csrfToken, retry, sessionChanged]);
  return (
    <section aria-label="模拟检测例外入口">
      <h2>SIMULATED · 检测例外解释</h2>
      <p>
        保留原始命中，只增加例外注释；无自动抑制、无追溯改写。“全局”仅指当前租户当前域。
      </p>
      {error && <p role="alert">{error}</p>}
      {!gate ? (
        <p role="status">核对权限中…</p>
      ) : !gate.read ? (
        <>
          <p>没有模拟检测例外读取权限。</p>
          <button onClick={() => setRetry((x) => x + 1)}>重新核对权限</button>
        </>
      ) : !domain ? (
        <SavedSourcePicker
          userID={profile.ID}
          sessionChanged={sessionChanged}
          onResolved={(v) => setDomain(v.selection.domainId)}
        />
      ) : (
        <>
          <button onClick={() => setDomain("")}>切换模拟域</button>
          <SimulationDetectionExceptionsDomainWorkspace
            profile={profile}
            sessionChanged={sessionChanged}
            domainId={domain}
            permissions={gate}
          />
        </>
      )}
    </section>
  );
}
export type WorkspaceProps = Props & {
  domainId: string;
  permissions?: ExceptionPermissions;
};
export function SimulationDetectionExceptionsDomainWorkspace(
  props: WorkspaceProps,
) {
  return (
    <Workspace
      key={JSON.stringify([props.profile, props.domainId, props.permissions])}
      {...props}
    />
  );
}
function fresh(): Declaration {
  return {
    id: "",
    global: true,
    sourceTaskId: "",
    ruleId: "",
    reason: "",
    notBefore: new Date().toISOString(),
    expiresAt: new Date(Date.now() + 86400000).toISOString(),
    conditions: [
      { field: "event.outcome", operator: "eq", values: ["failure"] },
    ],
  };
}
type Pending = { action: Action; input: Write };
function Workspace({
  profile,
  domainId,
  sessionChanged,
  permissions = { read: false, manage: false, source: false },
}: WorkspaceProps) {
  const [savedEvaluations, setSavedEvaluations] = useState<EvaluationSummary[]>(
    [],
  );
  const [record, setRecord] = useState<ExceptionRecord | null>(null),
    [historical, setHistorical] = useState<ExceptionRecord | null>(null),
    [historyRevision, setHistoryRevision] = useState("1"),
    [selected, setSelected] = useState(""),
    [draft, setDraft] = useState<Declaration>(fresh),
    [newMode, setNewMode] = useState(false),
    [query, setQuery] = useState("");
  const [sourceTask, setSourceTask] = useState(""),
    [eventID, setEventID] = useState(""),
    [ruleID, setRuleID] = useState(""),
    [evaluationID, setEvaluationID] = useState(""),
    [evaluation, setEvaluation] = useState<Evaluation | null>(null),
    [receipt, setReceipt] = useState<Receipt | null>(null),
    [pending, setPending] = useState<Pending | null>(null),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [busy, setBusy] = useState(false),
    [denied, setDenied] = useState(false),
    [refresh, setRefresh] = useState(0),
    [importText, setImportText] = useState("");
  const live = useRef(true),
    op = useRef(false),
    seq = useRef(0),
    controllers = useRef(new Set<AbortController>()),
    changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  const controller = () => {
    const c = new AbortController();
    controllers.current.add(c);
    return c;
  };
  const clear = () => {
    live.current = false;
    controllers.current.forEach((c) => c.abort());
    setDenied(true);
    setRecord(null);
    setHistorical(null);
    setEvaluation(null);
    setSavedEvaluations([]);
    setReceipt(null);
    setPending(null);
    setDraft(fresh());
    setSelected("");
    setSourceTask("");
    setImportText("");
  };
  function fail(e: unknown) {
    if (!live.current) return;
    if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
      clear();
      if (e.status === 401) changed.current();
      return;
    }
    setError(
      e instanceof ApiError && e.status === 409
        ? "配置修订冲突：另一操作已更新配置。重新读取当前配置后，核对草稿再提交。"
        : e instanceof Error
          ? e.message
          : "请求失败，结果尚未确认。",
    );
  }
  useLayoutEffect(() => {
    live.current = true;
    const n = listenForSessionInvalidation(() => {
      clear();
      changed.current();
    });
    return () => {
      live.current = false;
      controllers.current.forEach((c) => c.abort());
      n.close();
    };
  }, []);
  useEffect(() => {
    if (!permissions.read) return;
    const c = controller(),
      n = ++seq.current;
    api
      .snapshot(domainId, profile, c.signal)
      .then((r) => {
        if (live.current && n === seq.current) {
          setRecord(r);
          setError("");
        }
      })
      .catch(fail);
    return () => c.abort();
  }, [refresh, domainId, profile.csrfToken, permissions.read]);
  const available = permissions.read && !denied,
    locked = busy || !!pending;
  function choose(e: ExceptionEntry) {
    if (locked) return;
    setSelected(e.id);
    setNewMode(false);
    setDraft({
      id: e.id,
      global: e.global,
      sourceTaskId: record?.sourceTasks[e.id] ?? "",
      ruleId: e.rule_id ?? "",
      reason: e.reason,
      notBefore: e.not_before,
      expiresAt: e.expires_at,
      conditions: structuredClone(e.conditions),
    });
    setNotice("");
  }
  function accept(r: Receipt, p: Pending) {
    verifyReceipt(r, p.action, p.input, profile.ID);
    setReceipt(r);
    if (r.evaluation) setEvaluation(r.evaluation);
    setPending(null);
    setNotice("操作已确认；配置修改仅影响后续显式评估。");
    setRefresh((x) => x + 1);
  }
  async function write(action: Action, extra: Partial<Write> = {}) {
    if (op.current || pending || !record || !available) return;
    if (
      (action !== "evaluate" && !permissions.manage) ||
      (action === "evaluate" && (!permissions.source || !permissions.manage))
    )
      return;
    const input: Write = {
      domainId,
      operationId: crypto.randomUUID(),
      expectedRevision: record.snapshot.revision,
      ...extra,
    };
    const p = { action, input };
    op.current = true;
    setBusy(true);
    setError("");
    setPending(p);
    const c = controller();
    try {
      const r = await api.write(action, input, profile, c.signal);
      if (live.current) accept(r, p);
    } catch (e) {
      if (!live.current) return;
      const uncertain =
        !(e instanceof ApiError) ||
        e.status >= 500 ||
        e.status < 400 ||
        e.status === 408 ||
        ["network", "invalid_response"].includes(e.code);
      if (!uncertain) setPending(null);
      fail(e);
    } finally {
      op.current = false;
      if (live.current) setBusy(false);
    }
  }
  async function recover(retry = false) {
    if (!pending || op.current) return;
    const p = pending;
    op.current = true;
    setBusy(true);
    setError("");
    const c = controller();
    let retryStarted = false;
    try {
      let r: Receipt;
      try {
        r = await api.receipt(domainId, p.input.operationId, profile, c.signal);
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 404 || !retry) throw e;
        retryStarted = true;
        r = await api.write(p.action, p.input, profile, c.signal);
      }
      if (live.current) accept(r, p);
    } catch (e) {
      if (
        live.current &&
        retryStarted &&
        e instanceof ApiError &&
        e.status >= 400 &&
        e.status < 500 &&
        e.status !== 408 &&
        !["network", "invalid_response"].includes(e.code)
      )
        setPending(null);
      fail(e);
    } finally {
      op.current = false;
      if (live.current) setBusy(false);
    }
  }
  async function read<T>(
    fn: (s: AbortSignal) => Promise<T>,
    apply: (v: T) => void,
  ) {
    if (op.current || locked || !available) return;
    op.current = true;
    setBusy(true);
    setError("");
    const c = controller();
    try {
      const v = await fn(c.signal);
      if (live.current) apply(v);
    } catch (e) {
      fail(e);
    } finally {
      op.current = false;
      if (live.current) setBusy(false);
    }
  }
  function save() {
    if (!validDeclaration(draft)) {
      setError("请填写合法声明：1–5 个不重复字段、eq/in、UTC 起止时间及理由。");
      return;
    }
    write(newMode ? "create" : "edit", { declaration: draft });
  }
  const entries =
    record?.snapshot.entries.filter(
      (e) => e.id.includes(query) || e.reason.includes(query),
    ) ?? [];
  const current = record?.snapshot.entries.find((e) => e.id === selected);
  if (!available)
    return <p role="alert">当前没有检测例外读取权限；已清除页面数据。</p>;
  return (
    <section aria-label="检测例外工作区">
      <p>
        SIMULATED · 域 {domainId} · 当前修订{" "}
        {record?.snapshot.revision ?? "读取中"}
      </p>
      <p>
        配置由本域例外读者共享。事件预览、评估和结果读取还要求当前手动检测与事件权限；规则摘要来自执行副本。编辑会停用该项，须再次显式启用。
      </p>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {pending && (
        <section aria-label="未确认操作">
          <p>
            结果未确认：{pending.action} · {pending.input.operationId}
            。先读取原回执，确认未写入后才重发同一操作。
          </p>
          <button disabled={busy} onClick={() => recover()}>
            读取原回执
          </button>
          <button disabled={busy} onClick={() => recover(true)}>
            查无回执后重试原操作
          </button>
        </section>
      )}
      <button disabled={locked} onClick={() => setRefresh((x) => x + 1)}>
        重新读取当前配置
      </button>
      <button
        disabled={locked || !record}
        onClick={() =>
          read((s) => api.export(domainId, profile, s), downloadExceptions)
        }
      >
        下载配置证据 JSON
      </button>
      <p>
        下载是只读证据（含修订），不能直接作为导入。导入只接受不带身份、摘要、历史或启用状态的声明数组。
      </p>
      <label>
        查找例外
        <input
          aria-label="查找例外"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </label>
      <p>当前域最多 32 条，包含已归档条目；不是无限分页。</p>
      <ul>
        {entries.map((e) => (
          <li key={e.id}>
            <button disabled={locked} onClick={() => choose(e)}>
              {e.id} · {e.global ? "本域全局" : `规则 ${e.rule_id}`} ·{" "}
              {e.archived
                ? "已归档"
                : !e.enabled
                  ? "已停用"
                  : Date.parse(e.expires_at) <= Date.now()
                    ? "已到期"
                    : Date.parse(e.not_before) > Date.now()
                      ? "尚未生效"
                      : "当前时间内启用"}
            </button>
          </li>
        ))}
      </ul>
      {record && !entries.length && <p>没有符合条件的例外。</p>}
      <button
        disabled={locked || !permissions.manage || !record}
        onClick={() => {
          setDraft(fresh());
          setSelected("");
          setNewMode(true);
          setNotice("");
        }}
      >
        新增禁用草稿
      </button>
      {(newMode || selected) && (
        <fieldset
          disabled={locked || !permissions.manage || !!current?.archived}
        >
          <legend>{newMode ? "新建例外声明" : `编辑 ${selected}`}</legend>
          <label>
            例外 ID
            <input
              aria-label="例外 ID"
              disabled={!newMode}
              value={draft.id}
              onChange={(e) => setDraft({ ...draft, id: e.target.value })}
            />
          </label>
          <label>
            <input
              type="checkbox"
              checked={draft.global}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  global: e.target.checked,
                  sourceTaskId: "",
                  ruleId: "",
                })
              }
            />
            本域全局
          </label>
          {!draft.global && (
            <>
              <label>
                规则来源任务 UUID
                <input
                  aria-label="规则来源任务 UUID"
                  value={draft.sourceTaskId}
                  onChange={(e) =>
                    setDraft({ ...draft, sourceTaskId: e.target.value })
                  }
                />
              </label>
              <label>
                执行规则 ID
                <input
                  aria-label="执行规则 ID"
                  value={draft.ruleId}
                  onChange={(e) =>
                    setDraft({ ...draft, ruleId: e.target.value })
                  }
                />
              </label>
            </>
          )}
          <label>
            理由
            <input
              aria-label="理由"
              value={draft.reason}
              onChange={(e) => setDraft({ ...draft, reason: e.target.value })}
            />
          </label>
          <label>
            生效时间 UTC
            <input
              aria-label="生效时间 UTC"
              value={draft.notBefore}
              onChange={(e) =>
                setDraft({ ...draft, notBefore: e.target.value })
              }
            />
          </label>
          <label>
            到期时间 UTC
            <input
              aria-label="到期时间 UTC"
              value={draft.expiresAt}
              onChange={(e) =>
                setDraft({ ...draft, expiresAt: e.target.value })
              }
            />
          </label>
          <p>
            UTC 时间用 RFC3339 的 Z
            格式，最多三位小数。生效起点包含，到期点排除。时间以服务端评估时刻为准；旧评估不随时间改写。
          </p>
          {draft.conditions.map((c, i) => (
            <div key={i}>
              <label>
                条件字段
                <select
                  aria-label={`条件字段 ${i + 1}`}
                  value={c.field}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      conditions: draft.conditions.map((x, j) =>
                        j === i ? { ...x, field: e.target.value } : x,
                      ),
                    })
                  }
                >
                  {exceptionFields.map((f) => (
                    <option key={f}>{f}</option>
                  ))}
                </select>
              </label>
              <label>
                匹配方式
                <select
                  aria-label={`匹配方式 ${i + 1}`}
                  value={c.operator}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      conditions: draft.conditions.map((x, j) =>
                        j === i
                          ? { ...x, operator: e.target.value as "eq" | "in" }
                          : x,
                      ),
                    })
                  }
                >
                  <option>eq</option>
                  <option>in</option>
                </select>
              </label>
              <label>
                匹配值（每行一个）
                <textarea
                  aria-label={`匹配值 ${i + 1}`}
                  value={c.values.join("\n")}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      conditions: draft.conditions.map((x, j) =>
                        j === i
                          ? { ...x, values: e.target.value.split("\n") }
                          : x,
                      ),
                    })
                  }
                />
              </label>
              <button
                disabled={draft.conditions.length === 1}
                onClick={() =>
                  setDraft({
                    ...draft,
                    conditions: draft.conditions.filter((_, j) => j !== i),
                  })
                }
              >
                移除条件 {i + 1}
              </button>
            </div>
          ))}
          <button
            disabled={draft.conditions.length >= 5}
            onClick={() =>
              setDraft({
                ...draft,
                conditions: [
                  ...draft.conditions,
                  {
                    field:
                      exceptionFields.find(
                        (f) => !draft.conditions.some((c) => c.field === f),
                      ) ?? "event.outcome",
                    operator: "eq",
                    values: [""],
                  },
                ],
              })
            }
          >
            添加条件
          </button>
          <button onClick={save}>
            {newMode ? "保存禁用草稿" : "保存编辑并停用"}
          </button>
        </fieldset>
      )}
      {current && (
        <section aria-label="例外状态操作">
          <p>
            执行摘要：{current.rule_digest ?? "本域全局，不绑定单个规则摘要"}
          </p>
          <button
            disabled={
              locked ||
              !permissions.manage ||
              current.archived ||
              current.enabled
            }
            onClick={() => write("enable", { id: current.id })}
          >
            启用所选例外
          </button>
          <button
            disabled={
              locked ||
              !permissions.manage ||
              current.archived ||
              !current.enabled
            }
            onClick={() => write("disable", { id: current.id })}
          >
            停用所选例外
          </button>
          <button
            disabled={locked || !permissions.manage || current.archived}
            onClick={() => write("archive", { id: current.id })}
          >
            归档所选例外
          </button>
        </section>
      )}
      <section aria-label="导入禁用声明">
        <h3>原子导入禁用声明</h3>
        <input
          aria-label="选择声明 JSON 文件"
          type="file"
          accept="application/json,.json"
          disabled={locked || !permissions.manage}
          onChange={async (e) => {
            const f = e.target.files?.[0];
            if (!f) return;
            if (f.size > 256 * 1024) {
              setError("导入最多 256 KiB。");
              return;
            }
            try {
              const t = await f.text();
              if (live.current) setImportText(t);
            } catch (x) {
              fail(x);
            }
          }}
        />
        <textarea
          aria-label="声明 JSON 数组"
          value={importText}
          disabled={locked || !permissions.manage}
          onChange={(e) => setImportText(e.target.value)}
        />
        <button
          disabled={locked || !permissions.manage || !record}
          onClick={() => {
            try {
              write("import", { entries: parseDraftImport(importText) });
            } catch (e) {
              fail(e);
            }
          }}
        >
          原子导入（全部禁用）
        </button>
      </section>
      <section aria-label="检测来源与评估">
        <h3>本人成功检测任务</h3>
        <button
          disabled={locked || !permissions.source}
          onClick={() =>
            read(
              (s) => api.evaluations(domainId, profile, s),
              setSavedEvaluations,
            )
          }
        >
          读取本人评估记录列表
        </button>
        <ul>
          {savedEvaluations.map((x) => (
            <li key={x.id}>
              <button
                disabled={locked}
                onClick={() => {
                  setEvaluationID(x.id);
                  setSourceTask(x.sourceTaskId);
                  setEvaluation(null);
                }}
              >
                {x.id} · 修订 {x.revision} · {x.evaluatedAt}
              </button>
            </li>
          ))}
        </ul>
        <p>
          最多保留 64
          条显式评估；选择记录后读取保存结果，旧结果不会套用当前例外。
        </p>
        <p>
          输入既有手动检测任务 UUID。服务端核验当前所有者、授权
          epoch、不可变规则／数据集和原结果；不接受上传命中或摘要。
        </p>
        <label>
          检测任务 UUID
          <input
            aria-label="检测任务 UUID"
            value={sourceTask}
            disabled={locked || !permissions.source}
            onChange={(e) => {
              setSourceTask(e.target.value);
              setEvaluation(null);
            }}
          />
        </label>
        <label>
          来源事件 ID
          <input
            aria-label="来源事件 ID"
            value={eventID}
            disabled={locked || !permissions.source}
            onChange={(e) => setEventID(e.target.value)}
          />
        </label>
        <label>
          来源规则 ID
          <input
            aria-label="来源规则 ID"
            value={ruleID}
            disabled={locked || !permissions.source}
            onChange={(e) => setRuleID(e.target.value)}
          />
        </label>
        <button
          disabled={
            locked ||
            !permissions.source ||
            !permissions.manage ||
            !sourceTask ||
            !eventID ||
            !ruleID ||
            !draft.id ||
            !draft.reason
          }
          onClick={() =>
            read(
              (s) =>
                api.preview(
                  {
                    domainId,
                    sourceTaskId: sourceTask,
                    eventId: eventID,
                    ruleId: ruleID,
                    id: draft.id,
                    reason: draft.reason,
                    notBefore: draft.notBefore,
                    expiresAt: draft.expiresAt,
                  },
                  profile,
                  s,
                ),
              (d) => {
                setDraft(d);
                setSelected("");
                setNewMode(true);
                setNotice(
                  "预览未写入。核对条件后显式保存禁用草稿，配置将对本域例外读者可见。",
                );
              },
            )
          }
        >
          从真实命中预览禁用声明
        </button>
        <label>
          评估记录 ID
          <input
            aria-label="评估记录 ID"
            value={evaluationID}
            disabled={locked || !permissions.source}
            onChange={(e) => setEvaluationID(e.target.value)}
          />
        </label>
        <button
          disabled={
            locked ||
            !permissions.source ||
            !permissions.manage ||
            !sourceTask ||
            !evaluationID ||
            !record
          }
          onClick={() =>
            write("evaluate", { id: evaluationID, sourceTaskId: sourceTask })
          }
        >
          按当前修订显式评估
        </button>
        <button
          disabled={locked || !permissions.source || !evaluationID}
          onClick={() =>
            read(
              (s) => api.evaluation(domainId, evaluationID, profile, s),
              setEvaluation,
            )
          }
        >
          读取已保存评估
        </button>
        {!permissions.source && (
          <p>当前缺少来源读取／评估权限或功能开关，配置读取仍可用。</p>
        )}
        {evaluation && (
          <div aria-label="评估结果">
            <h4>SIMULATED · 固定修订例外解释</h4>
            <p>
              评估 {evaluation.id} · 固定修订 {evaluation.revision} · 服务端时间{" "}
              {evaluation.evaluatedAt}
            </p>
            <p>
              原始命中 {evaluation.source.matches.length} 条，解释结果{" "}
              {evaluation.matches.length} 条；未减少命中。
            </p>
            <ul>
              {evaluation.matches.map((m, i) => (
                <li key={i}>
                  {m.match.rule_id} / {m.match.event_id} ·{" "}
                  {m.exceptions.length
                    ? m.exceptions
                        .map((x) => `${x.exception_id}: ${x.reason}`)
                        .join("；")
                    : "无例外注释"}
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>
      <section aria-label="修订历史">
        <label>
          历史修订
          <input
            aria-label="历史修订"
            type="number"
            min="1"
            max={record?.snapshot.revision ?? 1}
            value={historyRevision}
            onChange={(e) => setHistoryRevision(e.target.value)}
          />
        </label>
        <button
          disabled={locked || !record}
          onClick={() =>
            read(
              (s) => api.history(domainId, Number(historyRevision), profile, s),
              setHistorical,
            )
          }
        >
          读取历史修订
        </button>
        {historical && (
          <>
            <p>
              只读历史修订 {historical.snapshot.revision} · {historical.at} ·{" "}
              {historical.hash}
            </p>
            <pre>{JSON.stringify(historical.snapshot.entries, null, 2)}</pre>
          </>
        )}
      </section>
      {receipt && (
        <p aria-label="已确认回执">
          已确认 {receipt.action} · 修订 {receipt.revision} · 操作{" "}
          {receipt.operationId}
        </p>
      )}
    </section>
  );
}
