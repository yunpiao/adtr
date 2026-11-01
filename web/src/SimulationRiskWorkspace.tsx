import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationRiskAPI as api,
  ambiguousRiskWrite,
  type RiskResult,
  type RiskReset,
  type RiskReceipt,
} from "./simulation-risk-api";
import { type SimulationDataset } from "./simulation-event-api";

type EntryProps = { profile: Profile; sessionChanged: () => void };
export default function SimulationRiskWorkspace(props: EntryProps) {
  return (
    <SimulationRiskEntry
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
function SimulationRiskEntry({ profile, sessionChanged }: EntryProps) {
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
      "GET /api/simulation/risk/datasets",
      "POST /api/simulation/risk/query",
      "POST /api/simulation/risk/reset",
      "GET /api/simulation/risk/operation",
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
          readable("simulation_risk") &&
          checks.results.slice(0, 4).every(Boolean);
        setGate({
          read,
          manage: read && checks.results[4] && checks.results[5],
        });
      })
      .catch((e: unknown) => {
        if (!live) return;
        setGate({ read: false, manage: false });
        setError("未能确认模拟风险及域选择权限，请重新核对。");
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
    <section aria-label="模拟风险入口">
      <h2>SIMULATED · 模拟风险</h2>
      <p>合成数据专用；选择已授权域，不连接真实 AD。</p>
      {error && <p role="alert">{error}</p>}
      {gate === null ? (
        <p role="status">正在核对模拟风险权限…</p>
      ) : !gate.read ? (
        <>
          <p>当前账户未获得模拟风险与域选择读取权限。</p>
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
          <SimulationRiskDomainWorkspace
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

export type SimulationRiskWorkspaceProps = {
  profile: Profile;
  sessionChanged: () => void;
  domainId: string;
  // Supply current server operation checks, never role-name/admin inference.
  // These controls are presentation only: every HTTP request reauthorizes.
  permissions?: { read: boolean; manage: boolean };
};
export function SimulationRiskDomainWorkspace(
  props: SimulationRiskWorkspaceProps,
) {
  return (
    <Workspace
      key={JSON.stringify([
        props.profile.ID,
        props.profile.username,
        props.profile.csrfToken,
        props.profile.role,
        props.profile.priv,
        props.profile.needChangePwd,
        props.profile.isExpired,
        props.domainId,
        props.permissions,
      ])}
      {...props}
    />
  );
}
function Workspace({
  profile,
  domainId,
  permissions,
  sessionChanged,
}: SimulationRiskWorkspaceProps) {
  const [datasets, setDatasets] = useState<SimulationDataset[]>([]),
    [datasetId, setDatasetId] = useState("");
  const [subject, setSubject] = useState(""),
    [reason, setReason] = useState("");
  const [result, setResult] = useState<RiskResult | null>(null),
    [pending, setPending] = useState<RiskReset | null>(null);
  const [busy, setBusy] = useState(false),
    [notice, setNotice] = useState(""),
    [denied, setDenied] = useState(false);
  const generation = useRef(0),
    locked = useRef(false),
    controller = useRef<AbortController | null>(null),
    pendingRef = useRef<RiskReset | null>(null);
  const changed = useRef(sessionChanged);
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
    setResult(null);
    setReason("");
  };
  const failure = (e: unknown) => {
    if (
      e instanceof ApiError &&
      ([401, 403, 404].includes(e.status) ||
        e.code === "password_change_required")
    ) {
      clear();
      if (e.status !== 404) {
        setDenied(true);
        if (e.status === 401 || e.code === "password_change_required")
          changed.current();
      }
      setNotice(
        e.code === "simulation_disabled"
          ? "模拟风险功能未启用。"
          : "当前会话、权限或记录范围已失效，已清除结果；请重新核验。",
      );
      return;
    }
    const code = e instanceof ApiError ? e.code : "network";
    if (code === "revision_conflict") setResult(null);
    setNotice(
      code === "simulation_disabled"
        ? "模拟风险功能未启用。"
        : code === "simulation_unavailable"
          ? "模拟风险存储暂不可用；没有可确认分数。"
          : code === "revision_conflict"
            ? "风险版本已变化，请重新查询后决定是否重置。"
            : "请求未取得可确认结果，请重试读取或核验原操作。",
    );
  };
  const run = async (
    work: (signal: AbortSignal, current: () => boolean) => Promise<void>,
  ) => {
    if (locked.current || !canRead) return;
    locked.current = true;
    setBusy(true);
    setNotice("");
    const n = ++generation.current,
      ctrl = new AbortController();
    controller.current?.abort();
    controller.current = ctrl;
    const current = () => n === generation.current && !ctrl.signal.aborted;
    try {
      await work(ctrl.signal, current);
    } catch (e) {
      if (current()) failure(e);
    } finally {
      if (current()) {
        locked.current = false;
        setBusy(false);
      }
    }
  };
  const validate = (
    r: RiskResult,
    q: { datasetId: string; domainId: string; subject: string },
  ) => {
    if (
      r.dataset.id !== q.datasetId ||
      r.scope.domain_id !== q.domainId ||
      r.scope.user_id !== q.subject
    )
      throw new ApiError("invalid_response");
  };
  const load = () => {
    if (pendingRef.current || locked.current) return;
    clear();
    void run(async (signal, current) => {
      const r = await api.datasets(domainId, profile, signal);
      if (current()) {
        setDatasets(r.datasets);
        if (r.total > r.datasets.length) setNotice("仅展示前 100 个数据集。");
      }
    });
  };
  const cancel = () => {
    invalidate();
    setBusy(false);
    setResult(null);
    setReason("");
    setNotice(
      pendingRef.current
        ? "已取消等待；原操作仍待核验，取消不会撤销服务端重置。"
        : "已取消读取。",
    );
  };
  useEffect(() => {
    load();
    const hints = listenForSessionInvalidation(() => {
      invalidate();
      clear();
      setBusy(false);
      setDenied(true);
      changed.current();
    });
    window.addEventListener("popstate", cancel);
    return () => {
      invalidate();
      hints.close();
      window.removeEventListener("popstate", cancel);
    };
    // Identity, domain and permission changes remount this scoped workspace.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const query = () => {
    if (!datasetId || !subject.trim() || pendingRef.current || locked.current)
      return;
    setResult(null);
    setReason("");
    const q = { domainId, datasetId, subject: subject.trim() };
    void run(async (signal, current) => {
      const r = await api.query(q, profile, signal);
      if (current()) {
        validate(r, q);
        setResult(r);
      }
    });
  };
  const accept = (r: RiskReceipt, q: RiskReset) => {
    validate(r.result, q);
    if (
      r.operationId !== q.operationId ||
      r.reason !== q.reason ||
      r.actor !== String(profile.ID) ||
      r.result.revision !== q.expectedRevision + 1
    )
      throw new ApiError("invalid_response");
    pendingRef.current = null;
    setPending(null);
    setResult(r.result);
    setReason("");
    setNotice("模拟风险重置已由服务端确认；重置记录保留在账本中。");
  };
  const recover = () => {
    const q = pendingRef.current;
    if (!q || !canManage) return;
    void run(async (signal, current) => {
      try {
        const r = await api.operation(q, profile, signal);
        if (current()) accept(r, q);
      } catch (e) {
        if (!current()) return;
        if (
          !(e instanceof ApiError && e.status === 404 && e.code === "not_found")
        )
          throw e;
        // A hidden scope revocation also looks like an absent receipt. Only
        // live scoped query success allows identical POST replay. Do not veto
        // on revision: the first POST can commit between lookup and preflight.
        clear();
        const verified = await api.query(q, profile, signal);
        if (!current()) return;
        validate(verified, q);
        try {
          const r = await api.reset(q, profile, signal);
          if (current()) accept(r, q);
        } catch (retryError) {
          if (current()) {
            if (!ambiguousRiskWrite(retryError)) {
              pendingRef.current = null;
              setPending(null);
            }
            throw retryError;
          }
        }
      }
    });
  };
  const reset = () => {
    if (
      !canManage ||
      !result ||
      !reason.trim() ||
      locked.current ||
      pendingRef.current
    )
      return;
    let operationId: string;
    try {
      operationId = crypto.randomUUID();
    } catch {
      setNotice("无法安全生成操作 ID，未提交重置。");
      return;
    }
    const q: RiskReset = {
      domainId,
      datasetId: result.dataset.id,
      subject: result.scope.user_id,
      expectedRevision: result.revision,
      reason: reason.trim(),
      operationId,
    };
    pendingRef.current = q;
    setPending(q);
    void run(async (signal, current) => {
      try {
        const r = await api.reset(q, profile, signal);
        if (current()) accept(r, q);
      } catch (e) {
        if (!current()) return;
        if (!ambiguousRiskWrite(e)) {
          pendingRef.current = null;
          setPending(null);
          throw e;
        }
        // Receipt-first: never blindly repeat an interrupted reset.
        try {
          const r = await api.operation(q, profile, signal);
          if (current()) accept(r, q);
        } catch (lookupError) {
          if (current()) {
            if (
              lookupError instanceof ApiError &&
              [401, 403, 404].includes(lookupError.status)
            )
              failure(lookupError);
            if (
              !(
                lookupError instanceof ApiError &&
                [401, 403].includes(lookupError.status)
              )
            )
              setNotice(
                "重置结果未确认。请核验原操作；未找到回执且范围仍有效时才以同一 ID 重试。",
              );
          }
        }
      }
    });
  };
  const selected = datasets.find((d) => d.id === datasetId);
  return (
    <section aria-label="模拟风险工作区">
      <h2>SIMULATED · 模拟风险</h2>
      <p>
        仅重放已导入的合成证据；不是实时 AD
        风险或经过验证的检测模型。无结果时不显示零分。
      </p>
      <p>
        示例映射 illustrative-event-risk-v1：login_failure → low
        (5)，service_access → medium (15)，login_success 不计风险；分数上限
        100。每个数据集最多 4096 条来源事件，每个操作者最多 1024
        次重置。重置仅改变合成风险账本，不修改 AD 或删除证据。
      </p>
      {!canRead ? (
        <p>尚未获得当前域的模拟风险读取权限。</p>
      ) : (
        <>
          <p>
            域：{domainId} · 来源：
            {selected
              ? `${selected.fixture} / ${selected.fixtureHash}`
              : "未选择模拟数据集"}
          </p>
          <button disabled={busy || !!pending} onClick={load}>
            刷新风险数据集
          </button>
          <label>
            风险数据集
            <select
              aria-label="风险数据集"
              value={datasetId}
              disabled={busy || !!pending}
              onChange={(e) => {
                invalidate();
                setDatasetId(e.target.value);
                setResult(null);
                setReason("");
                setNotice("");
              }}
            >
              <option value="">请选择</option>
              {datasets.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.fixture} ({d.id})
                </option>
              ))}
            </select>
          </label>
          {!busy && datasets.length === 0 && (
            <p>没有可显示的模拟数据集；不会自动生成分数。</p>
          )}
          <label>
            证据操作者
            <input
              value={subject}
              maxLength={128}
              disabled={busy || !!pending}
              onChange={(e) => {
                setSubject(e.target.value);
                setResult(null);
                setReason("");
              }}
            />
          </label>
          <button
            disabled={busy || !!pending || !datasetId || !subject.trim()}
            onClick={query}
          >
            查询模拟风险
          </button>
          {result && (
            <article aria-label="模拟风险结果">
              <h3>模拟分数：{result.score}</h3>
              <p aria-label="结果证据来源">
                SIMULATED · 数据集：{result.dataset.id} · fixture：
                {result.dataset.fixture} · SHA-256：{result.dataset.fixtureHash}{" "}
                · 来源契约：{result.dataset.contract}
              </p>
              <p>
                操作者：{result.scope.user_id} · 版本：{result.revision} · 截至
                UTC：{result.as_of}
              </p>
              <p>
                {result.contract} · {result.mappingVersion} · {result.version} ·
                SIMULATED
              </p>
              <p>
                来源事件 {result.sourceEventCount}；非风险映射{" "}
                {result.noRiskEventCount}；当前风险证据 {result.evidence_count}
                ；重复 {result.duplicates}；未来证据忽略 {result.ignored_future}
              </p>
              <h4>风险分布（服务端权重）</h4>
              <ul>
                {result.distribution.map((b) => (
                  <li key={b.kind}>
                    {b.kind}：{b.count} 条 × {b.weight}；原始点数 {b.raw_points}
                  </li>
                ))}
              </ul>
              <h4>趋势 UTC</h4>
              <ul>
                {result.trend.map((p) => (
                  <li key={p.at}>
                    {p.at} · {p.score}
                  </li>
                ))}
              </ul>
              <h4>风险记录</h4>
              <ul>
                {result.records
                  .filter((r) => r.kind !== "reset")
                  .map((r) => (
                    <li key={r.id}>
                      {r.id} · {r.at} · {r.kind} · {r.before} → {r.after} (
                      {r.delta})
                    </li>
                  ))}
              </ul>
              <h4>重置账本</h4>
              <p>最近重置：{result.reset_boundary || "无"}</p>
              <ul>
                {result.records
                  .filter((r) => r.kind === "reset")
                  .map((r) => (
                    <li key={r.id}>
                      {r.id} · {r.at} · {r.reason} · {r.before} → {r.after}
                    </li>
                  ))}
              </ul>
              <label>
                重置原因
                <input
                  value={reason}
                  maxLength={128}
                  disabled={busy || !!pending || !canManage}
                  onChange={(e) => setReason(e.target.value)}
                />
              </label>
              <button
                disabled={busy || !!pending || !canManage || !reason.trim()}
                onClick={reset}
              >
                重置模拟风险
              </button>
              {!canManage && <p>只读：尚未获得模拟风险管理权限。</p>}
            </article>
          )}
          {busy && (
            <>
              <p role="status">正在读取或确认服务端结果…</p>
              <button onClick={cancel}>取消等待</button>
            </>
          )}
          {pending && (
            <aside>
              <p>待核验操作：{pending.operationId}；尚未确认成功。</p>
              <button disabled={busy || !canManage} onClick={recover}>
                核验原操作并安全重试
              </button>
            </aside>
          )}
        </>
      )}
      {notice && <p role="status">{notice}</p>}
    </section>
  );
}
