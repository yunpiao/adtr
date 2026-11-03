import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationAssetAssessmentAPI as api,
  prefix,
  emptyFilters,
  type Observation,
  type Report,
  type History,
  type RowsPage,
  type Filters,
  type CreateInput,
  type Summary,
} from "./simulation-asset-assessment-api";
type Props = { profile: Profile; sessionChanged: () => void };
const readPaths = [
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
  ...["observation", "history", "detail", "rows", "receipt"].map(
    (x) => `GET ${prefix}/${x}`,
  ),
];
export default function SimulationAssetAssessmentWorkspace(props: Props) {
  return <Entry key={JSON.stringify(props.profile)} {...props} />;
}
function Entry({ profile, sessionChanged }: Props) {
  const [gate, setGate] = useState<{ read: boolean; manage: boolean } | null>(
      null,
    ),
    [domain, setDomain] = useState(""),
    [error, setError] = useState(""),
    [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    const c = new AbortController();
    setGate(null);
    setDomain("");
    setError("");
    Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", c.signal),
      accessRequest<{ results: boolean[] }>(
        "/check",
        c.signal,
        { paths: [...readPaths, `POST ${prefix}/create`] },
        profile.csrfToken,
      ),
    ])
      .then(([m, r]) => {
        if (!live) return;
        if (
          !Array.isArray(r.results) ||
          r.results.length !== readPaths.length + 1 ||
          r.results.some((x) => typeof x !== "boolean")
        )
          throw new ApiError("invalid_response");
        const read =
          m.menu.some(
            (x) => x.mark === "simulation_asset_assessment" && x.auth?.readable,
          ) && r.results.slice(0, -1).every(Boolean);
        setGate({ read, manage: read && r.results.at(-1) === true });
      })
      .catch((e) => {
        if (!live) return;
        setGate({ read: false, manage: false });
        setError("权限核对失败，请重新核对。");
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
  }, [profile.csrfToken, sessionChanged, refresh]);
  return (
    <section aria-label="模拟资产分类入口">
      <h2>SIMULATED · 模拟资产分类</h2>
      <p>
        simulation assumptions
        v1（simulated-asset-v1）：仅服务器固定合成观测。同步生成不可变报告，不创建
        Worker 任务。
      </p>
      {error && <p role="alert">{error}</p>}
      {!gate ? (
        <p role="status">核对权限中…</p>
      ) : !gate.read ? (
        <>
          <p>没有模拟资产分类读取权限。</p>
          <button onClick={() => setRefresh((n) => n + 1)}>重新核对权限</button>
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
          <SimulationAssetAssessmentDomainWorkspace
            profile={profile}
            sessionChanged={sessionChanged}
            domainId={domain}
            manage={gate.manage}
          />
        </>
      )}
    </section>
  );
}
export function SimulationAssetAssessmentDomainWorkspace(
  props: Props & { domainId: string; manage?: boolean },
) {
  return (
    <Workspace
      key={JSON.stringify([props.profile, props.domainId, props.manage])}
      {...props}
    />
  );
}
const labels: Record<string, string> = {
  kind: "对象类别",
  accountStatus: "账户状态",
  activity: "登录活动",
  groupType: "组类型",
  groupScope: "组范围",
};
const choices: Record<string, string[]> = {
  kind: ["user", "computer", "group"],
  accountStatus: ["enabled", "disabled", "locked", "unknown", "not_applicable"],
  activity: [
    "recent",
    "inactive",
    "never_observed",
    "unknown",
    "not_applicable",
  ],
  groupType: ["security", "distribution", "unknown", "not_applicable"],
  groupScope: [
    "global",
    "domain_local",
    "universal",
    "unknown",
    "not_applicable",
  ],
};
function SummaryView({ summary }: { summary: Summary }) {
  return (
    <section aria-label="完整报告统计">
      <h3>SIMULATED · 完整报告统计（不随筛选或分页改变）</h3>
      <p>
        已观察对象总数：{summary.total}；
        {summary.complete ? "完整固定观测" : "不完整观测，不能代表全域总量"}
      </p>
      {Object.entries(labels).map(([k, label]) => (
        <p key={k}>
          {label}：
          {Object.entries(
            summary[k === "kind" ? "kinds" : (k as keyof Summary)] as Record<
              string,
              number
            >,
          )
            .map(([v, n]) => `${v}: ${n}`)
            .join("；")}
        </p>
      ))}
      <p>
        原因计数：
        {Object.entries(summary.reasons)
          .map(([v, n]) => `${v}: ${n}`)
          .join("；")}
      </p>
    </section>
  );
}
function Workspace({
  profile,
  domainId,
  sessionChanged,
  manage = false,
}: Props & { domainId: string; manage?: boolean }) {
  const [observation, setObservation] = useState<Observation | null>(null),
    [scenario, setScenario] = useState(""),
    [report, setReport] = useState<Report | null>(null),
    [history, setHistory] = useState<History | null>(null),
    [historyOffset, setHistoryOffset] = useState(0),
    [page, setPage] = useState<RowsPage | null>(null),
    [filters, setFilters] = useState<Filters>(emptyFilters),
    [pending, setPending] = useState<CreateInput | null>(null),
    [recovery, setRecovery] = useState(""),
    [busy, setBusy] = useState(false),
    [denied, setDenied] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [refresh, setRefresh] = useState(0);
  const live = useRef(false),
    controllers = useRef(new Set<AbortController>()),
    action = useRef(0);
  useLayoutEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
      action.current++;
      controllers.current.forEach((c) => c.abort());
      controllers.current.clear();
    };
  }, []);
  const controller = () => {
    const c = new AbortController();
    controllers.current.add(c);
    return c;
  };
  const fail = (e: unknown) => {
    if (!live.current) return;
    if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
      live.current = false;
      controllers.current.forEach((c) => c.abort());
      setDenied(true);
      setReport(null);
      setPage(null);
      setObservation(null);
      setHistory(null);
      setError("当前身份或权限已失效；停止读取与写入，请重新核对会话。");
      if (e.status === 401) sessionChanged();
    } else
      setError(
        e instanceof ApiError
          ? `请求未完成：${e.code}。`
          : "请求未完成，请重试读取。",
      );
  };
  useEffect(() => {
    const n = listenForSessionInvalidation(() => {
      live.current = false;
      controllers.current.forEach((c) => c.abort());
      setDenied(true);
      setReport(null);
      setPage(null);
      setObservation(null);
      setHistory(null);
      sessionChanged();
    });
    return () => n.close();
  }, [sessionChanged]);
  useEffect(() => {
    if (denied) return;
    const c = controller();
    let current = true;
    api
      .observation(domainId, profile, c.signal)
      .then((o) => {
        if (live.current && current) {
          setObservation(o);
          setScenario((old) =>
            o.scenarios.some((s) => s.id === old) ? old : o.scenarios[0].id,
          );
        }
      })
      .catch((e) => {
        if (current) fail(e);
      });
    return () => {
      current = false;
      c.abort();
      controllers.current.delete(c);
    };
  }, [domainId, profile, denied, refresh]);
  useEffect(() => {
    if (denied) return;
    let current = true;
    const c = controller();
    setHistory(null);
    api
      .history(domainId, historyOffset, profile, c.signal)
      .then((h) => {
        if (live.current && current) setHistory(h);
      })
      .catch((e) => {
        if (current) fail(e);
      });
    return () => {
      current = false;
      c.abort();
      controllers.current.delete(c);
    };
  }, [domainId, profile, historyOffset, denied, refresh]);
  useEffect(() => {
    setPage(null);
    if (!report || denied) return;
    let current = true;
    const c = controller();
    api
      .rows(domainId, report, filters, profile, c.signal)
      .then((p) => {
        if (live.current && current) setPage(p);
      })
      .catch((e) => {
        if (current) fail(e);
      });
    return () => {
      current = false;
      c.abort();
      controllers.current.delete(c);
    };
  }, [domainId, profile, report, filters, denied, refresh]);
  async function run(fn: (s: AbortSignal) => Promise<void>) {
    if (busy || denied) return;
    const seq = ++action.current,
      c = controller();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await fn(c.signal);
    } catch (e) {
      if (live.current && seq === action.current) fail(e);
    } finally {
      controllers.current.delete(c);
      if (live.current && seq === action.current) setBusy(false);
    }
  }
  const accept = (r: Report) => {
    if (!live.current) return;
    setReport(r);
    setFilters({ ...emptyFilters });
    setPending(null);
    setNotice("已确认不可变报告；历史内容不会按当前时间重新计算。");
    setRefresh((n) => n + 1);
  };
  async function submit(input: CreateInput, s: AbortSignal) {
    try {
      const r = await api.create(domainId, input, profile, s);
      accept(r.report);
    } catch (e) {
      if (!live.current) return;
      const definitive =
        e instanceof ApiError &&
        e.status >= 400 &&
        e.status < 500 &&
        e.code !== "invalid_response";
      if (definitive) {
        setPending(null);
        setNotice(
          "服务器明确拒绝本次提交，已解锁输入；请按错误修正后重新创建。",
        );
      } else
        setNotice(
          "提交结果不确定：保留原 operationId，请先查询回执，不能创建重复报告。",
        );
      throw e;
    }
  }
  function create() {
    if (!observation || !scenario || pending || !manage) return;
    const input: CreateInput = {
      id: `report-${crypto.randomUUID()}`,
      operationId: `op-${crypto.randomUUID()}`,
      observationId: observation.id,
      expectedSourceHash: observation.hash,
      scenarioId: scenario,
    };
    setPending(input);
    setRecovery(input.operationId);
    void run((s) => submit(input, s));
  }
  function recover() {
    const op = pending?.operationId ?? recovery.trim();
    if (!op) return;
    void run(async (s) => {
      try {
        const r = await api.receipt(
          domainId,
          op,
          profile,
          s,
          pending ?? undefined,
        );
        accept(r.report);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404 && live.current)
          setNotice(
            pending
              ? "回执尚不存在；可显式重试同一操作，服务器将再次核对权限和意图。"
              : "未找到此回执；请核对 operationId，未提交任何新报告。",
          );
        throw e;
      }
    });
  }
  function retry() {
    if (!pending) return;
    void run(async (s) => {
      try {
        const r = await api.receipt(
          domainId,
          pending.operationId,
          profile,
          s,
          pending,
        );
        accept(r.report);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) await submit(pending, s);
        else throw e;
      }
    });
  }
  return (
    <section aria-label="模拟资产分类工作区">
      <p>
        当前域：{domainId}。enabled
        仅表示已明确未禁用且未锁定，不等于原产品“活跃
        0”。unknown/missing_evidence、unknown/stale_observation 与
        not_applicable 分开显示；未提供风险、密码过期或信任关系指标。
      </p>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {denied ? (
        <p>请退出此域并重新核对当前权限。</p>
      ) : (
        <>
          <button
            disabled={busy || !!pending}
            onClick={() => setRefresh((n) => n + 1)}
          >
            重新读取
          </button>
          {observation && (
            <section aria-label="固定合成观测">
              <p>
                观测：{observation.id}；source hash：{observation.hash}
              </p>
              <p>
                {observation.complete ? "固定观测完整" : "固定观测不完整"}：
                {observation.completenessReason}
              </p>
              <label>
                固定评估场景
                <select
                  aria-label="固定评估场景"
                  disabled={busy || !!pending}
                  value={scenario}
                  onChange={(e) => {
                    setScenario(e.target.value);
                    setReport(null);
                    setPage(null);
                    setFilters({ ...emptyFilters });
                    setError("");
                    setNotice("");
                  }}
                >
                  {observation.scenarios.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.id} · asOf {s.asOf}
                    </option>
                  ))}
                </select>
              </label>
              <button disabled={!manage || busy || !!pending} onClick={create}>
                创建不可变模拟报告
              </button>
              {!manage && <p>当前仅有读取权限。</p>}
            </section>
          )}
          <section aria-label="报告回执恢复">
            <label>
              operationId
              <input
                aria-label="operationId"
                value={pending?.operationId ?? recovery}
                disabled={busy || !!pending}
                onChange={(e) => setRecovery(e.target.value)}
              />
            </label>
            <button
              disabled={busy || !(pending || recovery.trim())}
              onClick={recover}
            >
              查询原操作回执
            </button>
            {pending && (
              <>
                <p>
                  待确认操作：{pending.operationId}；报告：{pending.id}；场景：
                  {pending.scenarioId}；source hash：
                  {pending.expectedSourceHash}
                </p>
                <button disabled={busy || !manage} onClick={retry}>
                  先查回执，再重试同一操作
                </button>
              </>
            )}
          </section>
          <section aria-label="报告历史">
            <h3>SIMULATED · 不可变报告历史</h3>
            {history?.reports.map((h) => (
              <p key={h.id}>
                <button
                  disabled={busy || !!pending}
                  onClick={() =>
                    void run(async (s) =>
                      accept(await api.detail(domainId, h.id, profile, s)),
                    )
                  }
                >
                  {h.id} · {h.scenarioId} · {h.createdAt}
                </button>
              </p>
            ))}
            <p>
              历史共 {history?.total ?? "未读取"} 份；本页{" "}
              {history?.reports.length ?? 0} 份。
            </p>
            <button
              disabled={busy || historyOffset === 0}
              onClick={() => setHistoryOffset((n) => Math.max(0, n - 20))}
            >
              历史上一页
            </button>
            <button
              disabled={busy || !history || historyOffset + 20 >= history.total}
              onClick={() => setHistoryOffset((n) => n + 20)}
            >
              历史下一页
            </button>
          </section>
          {report && (
            <section aria-label="报告详情">
              <h3>{report.id}</h3>
              <p>
                不可变历史 · {report.version} · asOf {report.asOf} · 创建{" "}
                {report.createdAt}
              </p>
              <p>
                source hash：{report.sourceHash}；report hash：{report.hash}
                ；授权 epoch：{report.epoch}
              </p>
              <SummaryView summary={report.summary} />
              <section aria-label="分类筛选">
                {Object.entries(choices).map(([k, values]) => (
                  <label key={k}>
                    {labels[k]}
                    <select
                      aria-label={labels[k]}
                      value={filters[k as keyof Filters]}
                      onChange={(e) =>
                        setFilters((f) => ({
                          ...f,
                          [k]: e.target.value,
                          offset: 0,
                        }))
                      }
                    >
                      <option value="">全部</option>
                      {values.map((v) => (
                        <option key={v}>{v}</option>
                      ))}
                    </select>
                  </label>
                ))}
              </section>
              <p>
                筛选匹配 {page?.matched ?? "读取中"}；本页{" "}
                {page?.rows.length ?? 0} 条（不是统计总数）。
              </p>
              <table>
                <thead>
                  <tr>
                    <th>对象</th>
                    <th>类别</th>
                    {Object.values(labels)
                      .slice(1)
                      .map((l) => (
                        <th key={l}>{l} / 原因</th>
                      ))}
                  </tr>
                </thead>
                <tbody>
                  {page?.rows.map((r) => (
                    <tr key={r.objectId}>
                      <td>{r.objectId}</td>
                      <td>{r.kind}</td>
                      {(
                        [
                          "accountStatus",
                          "activity",
                          "groupType",
                          "groupScope",
                        ] as const
                      ).map((k) => (
                        <td key={k}>
                          {r[k].value} / {r[k].reason}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
              <button
                disabled={filters.offset === 0 || !page}
                onClick={() =>
                  setFilters((f) => ({
                    ...f,
                    offset: Math.max(0, f.offset - 20),
                  }))
                }
              >
                分类上一页
              </button>
              <button
                disabled={!page || filters.offset + 20 >= page.matched}
                onClick={() =>
                  setFilters((f) => ({ ...f, offset: f.offset + 20 }))
                }
              >
                分类下一页
              </button>
            </section>
          )}
        </>
      )}
    </section>
  );
}
