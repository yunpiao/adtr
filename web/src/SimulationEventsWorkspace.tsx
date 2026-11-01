import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationEventAPI as api,
  ambiguousSimulationWrite,
  type SimulationDataset,
  type SimulationDetail,
  type SimulationPage,
  type SimulationTransition,
  type SimulationReceipt,
} from "./simulation-event-api";

type EntryProps = { profile: Profile; sessionChanged: () => void };
export default function SimulationEventsWorkspace(props: EntryProps) {
  return (
    <SimulationEntry
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
function SimulationEntry({ profile, sessionChanged }: EntryProps) {
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
      "GET /api/simulation/events/datasets",
      "POST /api/simulation/events/query",
      "GET /api/simulation/events/detail",
      "POST /api/simulation/events/transition",
      "GET /api/simulation/events/operation",
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
          readable("simulation_events") &&
          checks.results.slice(0, 5).every(Boolean);
        setGate({
          read,
          manage: read && checks.results[5] && checks.results[6],
        });
      })
      .catch((e: unknown) => {
        if (!live) return;
        setGate({ read: false, manage: false });
        setError("未能确认模拟事件及域选择权限，请重新核对。");
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
    <section aria-label="模拟事件入口">
      <h2>SIMULATED · 模拟事件</h2>
      <p>合成数据专用；选择已授权域，不连接真实 AD。</p>
      {error && <p role="alert">{error}</p>}
      {gate === null ? (
        <p role="status">正在核对模拟事件权限…</p>
      ) : !gate.read ? (
        <>
          <p>当前账户未获得模拟事件与域选择读取权限。</p>
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
          <SimulationEventsDomainWorkspace
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

export type SimulationEventsWorkspaceProps = {
  profile: Profile;
  sessionChanged: () => void;
  domainId: string;
  // Supply current server operation checks, never role-name/admin inference.
  // These controls are presentation only: every HTTP request reauthorizes.
  permissions?: { read: boolean; manage: boolean };
};
export function SimulationEventsDomainWorkspace(
  props: SimulationEventsWorkspaceProps,
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
}: SimulationEventsWorkspaceProps) {
  const [datasets, setDatasets] = useState<SimulationDataset[]>([]),
    [datasetId, setDatasetId] = useState("");
  const [page, setPage] = useState<SimulationPage | null>(null),
    [detail, setDetail] = useState<SimulationDetail | null>(null);
  const [actor, setActor] = useState(""),
    [kind, setKind] = useState(""),
    [caseId, setCaseId] = useState(""),
    [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false),
    [notice, setNotice] = useState(""),
    [denied, setDenied] = useState(false);
  const [stale, setStale] = useState(false);
  const [pending, setPending] = useState<SimulationTransition | null>(null);
  const generation = useRef(0),
    locked = useRef(false),
    controller = useRef<AbortController | null>(null),
    pendingRef = useRef<SimulationTransition | null>(null);
  const canRead =
    permissions?.read === true &&
    !!domainId &&
    !denied &&
    !profile.needChangePwd &&
    !profile.isExpired;
  const canManage = canRead && permissions?.manage === true;
  const changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  const invalidate = () => {
    generation.current++;
    controller.current?.abort();
    locked.current = false;
  };
  const clear = () => {
    invalidate();
    setDatasets([]);
    setDatasetId("");
    setPage(null);
    setDetail(null);
    setBusy(false);
  };
  const failure = (e: unknown) => {
    if (
      e instanceof ApiError &&
      (e.status === 401 ||
        e.status === 403 ||
        e.code === "password_change_required")
    ) {
      clear();
      setDenied(true);
      if (e.status === 401 || e.code === "password_change_required")
        changed.current();
      setNotice("当前会话或权限已失效，已清除结果；请重新验证权限。");
      return;
    }
    const code = e instanceof ApiError ? e.code : "network";
    if (code === "revision_conflict") setStale(true);
    if (code === "not_found") {
      setDatasets([]);
      setDatasetId("");
      setPage(null);
      setDetail(null);
    }
    setNotice(
      code === "simulation_disabled"
        ? "模拟事件功能未启用。"
        : code === "simulation_unavailable"
          ? "模拟事件存储暂不可用，未返回数据。"
          : code === "revision_conflict"
            ? "事件版本已变化。请刷新详情后重新决定操作。"
            : code === "not_found"
              ? "当前授权范围内未找到该模拟记录。"
              : "请求未取得可确认结果；可手动重试读取。",
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
  const load = () => {
    if (pendingRef.current) return;
    setPage(null);
    setDetail(null);
    setDatasets([]);
    setDatasetId("");
    void run(async (signal, current) => {
      const result = await api.datasets(domainId, profile, signal);
      if (current()) {
        setDatasets(result.datasets);
        setNotice(
          result.total > result.datasets.length
            ? "仅展示前 100 个模拟数据集。"
            : "",
        );
      }
    });
  };
  useEffect(() => {
    load();
    const hints = listenForSessionInvalidation(() => {
      clear();
      setDenied(true);
      changed.current();
    });
    const back = () => {
      invalidate();
      setBusy(false);
      setDetail(null);
      setReason("");
    };
    window.addEventListener("popstate", back);
    return () => {
      invalidate();
      hints.close();
      window.removeEventListener("popstate", back);
    };
    // Identity, domain and permission changes remount the entire session above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const query = (offset = 0) => {
    if (!datasetId || pendingRef.current) return;
    setPage(null);
    setDetail(null);
    void run(async (signal, current) => {
      const result = await api.query(
        {
          domainId,
          datasetId,
          offset,
          limit: 100,
          ...(actor ? { actor } : {}),
          ...(kind ? { kind } : {}),
        },
        profile,
        signal,
      );
      if (current()) {
        if (
          result.dataset.id !== datasetId ||
          result.page.Events.some((e) => e.Domain !== domainId)
        )
          throw new ApiError("invalid_response");
        setPage(result);
      }
    });
  };
  const readDetail = (id: string) => {
    if (!id || !datasetId || pendingRef.current) return;
    setDetail(null);
    setReason("");
    void run(async (signal, current) => {
      const result = await api.detail(domainId, datasetId, id, profile, signal);
      if (current()) {
        if (
          result.dataset.id !== datasetId ||
          result.case.ID !== id ||
          result.evidence.some((e) => e.Domain !== domainId)
        )
          throw new ApiError("invalid_response");
        setStale(false);
        setDetail(result);
        setCaseId(id);
      }
    });
  };
  const accept = (receipt: SimulationReceipt, q: SimulationTransition) => {
    if (
      receipt.operationId !== q.operationId ||
      receipt.datasetId !== q.datasetId ||
      receipt.case.ID !== q.caseId ||
      receipt.dataset.id !== q.datasetId ||
      receipt.case.Revision !== q.expectedRevision + 1 ||
      receipt.audit.CaseID !== q.caseId ||
      receipt.audit.Action !== q.action ||
      receipt.audit.Reason !== q.reason
    )
      throw new ApiError("invalid_response");
    pendingRef.current = null;
    setPending(null);
    setDetail((old) =>
      old?.case.ID === receipt.case.ID
        ? {
            ...old,
            case: receipt.case,
            history: [...old.history, receipt.audit],
            historyTotal: old.historyTotal + 1,
          }
        : old,
    );
    setNotice("模拟操作已由服务端确认。请刷新详情读取完整历史。");
    setReason("");
  };
  const recover = () => {
    const q = pendingRef.current;
    if (!q || !canManage) return;
    void run(async (signal, current) => {
      try {
        const result = await api.operation(q, profile, signal);
        if (current()) accept(result, q);
      } catch (e) {
        if (!current()) return;
        // Only not_found permits considering replay, and only after the
        // scoped detail probe below. The original operation ID is retained.
        if (
          e instanceof ApiError &&
          e.status === 404 &&
          e.code === "not_found"
        ) {
          let replayed = false;
          try {
            // A hidden domain revocation also returns not_found. Revalidate
            // scoped access before replaying the identical write.
            const verified = await api.detail(
              q.domainId,
              q.datasetId,
              q.caseId,
              profile,
              signal,
            );
            if (!current()) return;
            if (
              verified.dataset.id !== q.datasetId ||
              verified.case.ID !== q.caseId ||
              verified.evidence.some((e) => e.Domain !== q.domainId)
            )
              throw new ApiError("invalid_response");
            // The original POST may commit between lookup and this read.
            // Preserve its ID/payload: the server checks receipts before CAS.
            replayed = true;
            const result = await api.transition(q, profile, signal);
            if (current()) accept(result, q);
          } catch (retryError) {
            if (current()) {
              if (replayed && !ambiguousSimulationWrite(retryError)) {
                pendingRef.current = null;
                setPending(null);
              }
              throw retryError;
            }
          }
        } else throw e;
      }
    });
  };
  const transition = (action: SimulationTransition["action"]) => {
    if (
      !canManage ||
      !detail ||
      !reason.trim() ||
      locked.current ||
      pendingRef.current ||
      stale ||
      detail.case.State !== "open"
    )
      return;
    let operationId: string;
    try {
      operationId = crypto.randomUUID();
    } catch {
      setNotice("无法安全生成操作 ID，未提交变更。");
      return;
    }
    const q: SimulationTransition = {
      domainId,
      datasetId,
      caseId: detail.case.ID,
      operationId,
      action,
      reason: reason.trim(),
      expectedRevision: detail.case.Revision,
    };
    pendingRef.current = q;
    setPending(q);
    void run(async (signal, current) => {
      try {
        const result = await api.transition(q, profile, signal);
        if (current()) accept(result, q);
      } catch (e) {
        if (!current()) return;
        if (!ambiguousSimulationWrite(e)) {
          pendingRef.current = null;
          setPending(null);
          throw e;
        }
        // Never issue a second POST until this exact operation has been read.
        try {
          const result = await api.operation(q, profile, signal);
          if (current()) accept(result, q);
        } catch (lookupError) {
          if (current()) {
            if (
              lookupError instanceof ApiError &&
              [401, 403].includes(lookupError.status)
            )
              throw lookupError;
            if (
              lookupError instanceof ApiError &&
              lookupError.code === "not_found"
            ) {
              setDatasets([]);
              setDatasetId("");
              setPage(null);
              setDetail(null);
            }
            setNotice(
              "操作结果未确认。请核验原操作；核验未找到时才会使用同一操作 ID 重试。切换或取消不会撤销服务端操作。",
            );
          }
        }
      }
    });
  };
  const cancel = () => {
    invalidate();
    setBusy(false);
    setDetail(null);
    setReason("");
    setNotice(
      pendingRef.current
        ? "已关闭详情；操作结果仍待核验，关闭不会撤销变更。"
        : "",
    );
  };
  const selected = datasets.find((d) => d.id === datasetId);
  return (
    <section aria-label="模拟事件工作区">
      <h2>
        模拟事件 <span>SIMULATED · 合成数据</span>
      </h2>
      <p>仅使用已显式导入的合成数据集；不是实时 AD 数据或攻击判定。</p>
      {!canRead ? (
        <p>尚未获得当前域的模拟事件读取权限。</p>
      ) : (
        <>
          <p>
            域：{domainId} · 来源：
            {selected
              ? `${selected.fixture} / ${selected.contract} / ${selected.fixtureHash}`
              : "未选择模拟数据集"}
          </p>
          <button disabled={busy || !!pending} onClick={load}>
            刷新数据集
          </button>
          <label>
            模拟数据集
            <select
              value={datasetId}
              disabled={busy || !!pending}
              onChange={(e) => {
                invalidate();
                setDatasetId(e.target.value);
                setPage(null);
                setDetail(null);
                setCaseId("");
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
            <p>没有可显示的模拟数据集；不会自动生成样例。</p>
          )}
          <label>
            操作者筛选
            <input
              value={actor}
              disabled={busy || !!pending}
              onChange={(e) => {
                setActor(e.target.value);
                setPage(null);
              }}
            />
          </label>
          <label>
            事件类型
            <select
              value={kind}
              disabled={busy || !!pending}
              onChange={(e) => {
                setKind(e.target.value);
                setPage(null);
              }}
            >
              <option value="">全部</option>
              <option value="login_failure">登录失败</option>
              <option value="login_success">登录成功</option>
              <option value="service_access">服务访问</option>
            </select>
          </label>
          <button
            disabled={busy || !datasetId || !!pending}
            onClick={() => query()}
          >
            查询模拟事件
          </button>
          {page && (
            <>
              <p>
                匹配 {page.page.Total} 条；当前展示 {page.page.Events.length}{" "}
                条（最多 100 条）。
              </p>
              {page.page.Events.length === 0 ? (
                <p>没有匹配的模拟事件。</p>
              ) : (
                <table>
                  <thead>
                    <tr>
                      <th>事件 ID</th>
                      <th>类型</th>
                      <th>操作者</th>
                      <th>设备</th>
                      <th>时间 UTC</th>
                      <th>来源</th>
                    </tr>
                  </thead>
                  <tbody>
                    {page.page.Events.map((e) => (
                      <tr key={e.ID}>
                        <td>{e.ID}</td>
                        <td>{e.Kind}</td>
                        <td>{e.Actor}</td>
                        <td>{e.Device}</td>
                        <td>{e.OccurredAt}</td>
                        <td>SIMULATED · {e.Provenance.Fixture}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </>
          )}
          {page && <h3>数据集案例（不随事件筛选改变）</h3>}
          {page?.cases.map(({ ID: id }) => (
            <button
              key={id}
              disabled={busy || !!pending}
              onClick={() => readDetail(id)}
            >
              查看案例 {id}
            </button>
          ))}
          <label>
            模拟案例 ID
            <input
              value={caseId}
              disabled={busy || !!pending}
              onChange={(e) => setCaseId(e.target.value)}
            />
          </label>
          <button
            disabled={busy || !datasetId || !caseId || !!pending}
            onClick={() => readDetail(caseId)}
          >
            读取案例详情
          </button>
          {detail && (
            <article aria-label="模拟案例详情">
              <h3>SIMULATED · {detail.case.ID}</h3>
              <p>
                状态：{detail.case.State} · 标记：{detail.case.Mark || "未标记"}{" "}
                · 版本：{detail.case.Revision}
              </p>
              <p>
                证据来源：{detail.dataset.fixture} ·{" "}
                {detail.dataset.fixtureHash}
              </p>
              <ul>
                {detail.evidence.map((e) => (
                  <li key={e.ID}>
                    {e.ID} · {e.Kind} · {e.Actor} · {e.OccurredAt} · SIMULATED
                  </li>
                ))}
              </ul>
              <h4>
                操作历史（{detail.history.length}/{detail.historyTotal}）
              </h4>
              <ul>
                {detail.history.map((a) => (
                  <li key={a.Revision}>
                    {a.At} · {a.Action} · {a.Actor} · {a.Reason}
                  </li>
                ))}
              </ul>
              <label>
                操作原因
                <input
                  maxLength={128}
                  value={reason}
                  disabled={!canManage || busy || !!pending}
                  onChange={(e) => setReason(e.target.value)}
                />
              </label>
              {(
                [
                  ["mark_suspicious", "标记可疑"],
                  ["mark_benign", "标记正常"],
                  ["unmark", "清除标记"],
                  ["close", "关闭案例"],
                ] as const
              ).map(([action, label]) => (
                <button
                  key={action}
                  disabled={
                    !canManage ||
                    busy ||
                    !!pending ||
                    !reason.trim() ||
                    stale ||
                    detail.case.State !== "open"
                  }
                  onClick={() => transition(action)}
                >
                  {label}
                </button>
              ))}
              <button
                disabled={busy || !!pending}
                onClick={() => readDetail(detail.case.ID)}
              >
                刷新详情
              </button>
              <button onClick={cancel}>返回列表</button>
              {!canManage && <p>只读：尚未获得模拟事件管理权限。</p>}
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
              <p>
                待核验操作：{pending.operationId}；请勿把未确认结果当成成功。
              </p>
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
