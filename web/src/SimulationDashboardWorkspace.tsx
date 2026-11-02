import { useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import { type SimulationDataset } from "./simulation-event-api";
import {
  simulationDashboardAPI as api,
  type DashboardSummary,
  type DashboardWindow,
} from "./simulation-dashboard-api";

type EntryProps = { profile: Profile; sessionChanged: () => void };
const identity = (p: Profile) =>
  JSON.stringify([
    p.ID,
    p.username,
    p.csrfToken,
    p.role,
    p.priv,
    p.needChangePwd,
    p.isExpired,
  ]);
export default function SimulationDashboardWorkspace(props: EntryProps) {
  return <Entry key={identity(props.profile)} {...props} />;
}
function Entry({ profile, sessionChanged }: EntryProps) {
  const [allowed, setAllowed] = useState<boolean | null>(null),
    [domainId, setDomainId] = useState(""),
    [revision, setRevision] = useState(0);
  const changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  useEffect(() => {
    let live = true;
    const ctrl = new AbortController();
    setAllowed(null);
    setDomainId("");
    void Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", ctrl.signal),
      accessRequest<{ results: boolean[] }>(
        "/check",
        ctrl.signal,
        {
          paths: [
            "GET /api/domain-selection",
            "GET /api/domain-selection/resolve",
            "GET /api/simulation/events/datasets",
          ],
        },
        profile.csrfToken,
      ),
    ])
      .then(([menu, checks]) => {
        if (!live) return;
        if (
          !Array.isArray(menu.menu) ||
          !Array.isArray(checks.results) ||
          checks.results.length !== 3 ||
          checks.results.some((x) => typeof x !== "boolean")
        )
          throw new ApiError("invalid_response");
        setAllowed(
          !profile.needChangePwd &&
            !profile.isExpired &&
            checks.results.every(Boolean) &&
            [
              "domains",
              "simulation_events",
              "simulation_risk",
              "simulation_detection",
            ].every((mark) =>
              menu.menu.some(
                (p) => p.mark === mark && p.auth?.readable === true,
              ),
            ),
        );
      })
      .catch((e: unknown) => {
        if (!live) return;
        setAllowed(false);
        if (
          e instanceof ApiError &&
          (e.status === 401 || e.code === "password_change_required")
        )
          changed.current();
      });
    const hints = listenForSessionInvalidation(() => {
      live = false;
      ctrl.abort();
      setAllowed(false);
      setDomainId("");
      changed.current();
    });
    return () => {
      live = false;
      ctrl.abort();
      hints.close();
    };
  }, [profile.csrfToken, profile.needChangePwd, profile.isExpired, revision]);
  return (
    <section aria-label="合成态势入口">
      <h2>SIMULATED · 合成态势</h2>
      <p>
        仅汇总已保存的合成证据，不连接真实
        AD。真实系统健康请到独立的系统健康页面查看。
      </p>
      {allowed === null ? (
        <p role="status">正在核对事件、风险、检测与域选择权限…</p>
      ) : !allowed ? (
        <>
          <p role="alert">当前无法确认全部必需读取权限，未加载任何态势数据。</p>
          <button onClick={() => setRevision((n) => n + 1)}>
            重新核对权限
          </button>
        </>
      ) : !domainId ? (
        <SavedSourcePicker
          userID={profile.ID}
          sessionChanged={sessionChanged}
          onResolved={(v) => setDomainId(v.selection.domainId)}
        />
      ) : (
        <>
          <button onClick={() => setDomainId("")}>切换模拟域</button>
          <SimulationDashboardDomainWorkspace
            profile={profile}
            sessionChanged={sessionChanged}
            domainId={domainId}
            permissions={{ read: true }}
          />
        </>
      )}
    </section>
  );
}
export type SimulationDashboardWorkspaceProps = EntryProps & {
  domainId: string;
  permissions?: { read: boolean };
};
export function SimulationDashboardDomainWorkspace(
  props: SimulationDashboardWorkspaceProps,
) {
  return (
    <Workspace
      key={JSON.stringify([
        identity(props.profile),
        props.domainId,
        props.permissions?.read,
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
}: SimulationDashboardWorkspaceProps) {
  const [datasets, setDatasets] = useState<SimulationDataset[]>([]),
    [datasetId, setDatasetId] = useState("");
  const [subject, setSubject] = useState(""),
    [windowPreset, setWindowPreset] = useState<DashboardWindow>("24h");
  const [summary, setSummary] = useState<DashboardSummary | null>(null),
    [busy, setBusy] = useState(false),
    [listing, setListing] = useState(false);
  const [notice, setNotice] = useState(""),
    [stale, setStale] = useState(false),
    [denied, setDenied] = useState(false),
    [listRevision, setListRevision] = useState(0);
  const generation = useRef(0),
    request = useRef<AbortController | null>(null),
    changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  const canRead =
    permissions?.read === true &&
    !!domainId &&
    !denied &&
    !profile.needChangePwd &&
    !profile.isExpired;
  const cancel = () => {
    generation.current++;
    request.current?.abort();
    request.current = null;
    setBusy(false);
  };
  const clearScope = () => {
    cancel();
    setSummary(null);
    setStale(false);
    setNotice("");
  };
  const fail = (e: unknown, previous: boolean) => {
    if (e instanceof ApiError && [401, 403, 404].includes(e.status)) {
      setSummary(null);
      setDatasets([]);
      setDatasetId("");
      setStale(false);
      setDenied(true);
      setNotice(
        "当前身份、权限或数据范围已失效，已清除原快照。请返回重新核对。",
      );
      if (e.status === 401) changed.current();
    } else {
      setStale(previous);
      setNotice(
        e instanceof ApiError && e.status === 422
          ? "来源超过有界汇总范围，未截断为不完整总数。"
          : "汇总暂时不可用，请重试。现有快照如仍展示，仅为旧快照。",
      );
    }
  };
  useEffect(() => {
    if (!canRead) return;
    let live = true;
    const ctrl = new AbortController();
    setListing(true);
    setNotice("");
    void api
      .datasets(domainId, profile, ctrl.signal)
      .then((value) => {
        if (!live) return;
        if (value.total !== value.datasets.length)
          throw new ApiError("dataset_list_incomplete", 422);
        setDatasets(value.datasets);
        setListing(false);
      })
      .catch((e: unknown) => {
        if (!live) return;
        setListing(false);
        fail(e, false);
      });
    return () => {
      live = false;
      ctrl.abort();
    };
  }, [canRead, domainId, profile, listRevision]);
  useEffect(() => {
    const hints = listenForSessionInvalidation(() => {
      cancel();
      setSummary(null);
      setDatasets([]);
      setDatasetId("");
      setDenied(true);
      changed.current();
    });
    // Browser navigation cannot revive a remembered snapshot for a previous scope.
    const historyChanged = () => {
      clearScope();
      setDatasetId("");
      setSubject("");
      setWindowPreset("24h");
    };
    window.addEventListener("popstate", historyChanged);
    window.addEventListener("hashchange", historyChanged);
    return () => {
      generation.current++;
      request.current?.abort();
      hints.close();
      window.removeEventListener("popstate", historyChanged);
      window.removeEventListener("hashchange", historyChanged);
    };
  }, []);
  const load = async (event?: FormEvent) => {
    event?.preventDefault();
    if (!canRead || !datasetId || request.current || subject.length > 128)
      return;
    const current = ++generation.current,
      ctrl = new AbortController();
    request.current = ctrl;
    setBusy(true);
    setNotice("");
    const previous = summary !== null;
    if (previous) setStale(true);
    try {
      const result = await api.summary(
        {
          domainId,
          datasetId,
          ...(subject ? { subject } : {}),
          window: windowPreset,
        },
        profile,
        ctrl.signal,
      );
      if (ctrl.signal.aborted || current !== generation.current) return;
      setSummary(result);
      setStale(false);
    } catch (e) {
      if (!ctrl.signal.aborted && current === generation.current)
        fail(e, previous);
    } finally {
      if (current === generation.current) {
        request.current = null;
        setBusy(false);
      }
    }
  };
  return (
    <section aria-label="合成态势工作台">
      <p>SIMULATED · 当前域：{domainId}</p>
      {notice && <p role="alert">{notice}</p>}
      {!canRead ? (
        <p>未获当前读取权限，已停止请求。</p>
      ) : (
        <>
          {listing ? (
            <p role="status">正在加载合成数据集…</p>
          ) : datasets.length === 0 ? (
            <p>没有可用的合成数据集。未知来源不显示为零。</p>
          ) : null}
          <button
            disabled={listing || busy}
            onClick={() => {
              clearScope();
              setDatasetId("");
              setListRevision((n) => n + 1);
            }}
          >
            重新加载数据集
          </button>
          <form onSubmit={load} aria-label="合成态势筛选">
            <label>
              合成态势数据集
              <select
                aria-label="合成态势数据集"
                value={datasetId}
                disabled={listing}
                onChange={(e) => {
                  clearScope();
                  setDatasetId(e.target.value);
                }}
              >
                <option value="">请选择数据集</option>
                {datasets.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.id} · {d.fixture}
                  </option>
                ))}
              </select>
            </label>
            <label>
              风险主体（可选）
              <input
                aria-label="风险主体（可选）"
                value={subject}
                maxLength={128}
                onChange={(e) => {
                  clearScope();
                  setSubject(e.target.value);
                }}
              />
            </label>
            <label>
              统计窗口
              <select
                aria-label="统计窗口"
                value={windowPreset}
                onChange={(e) => {
                  clearScope();
                  setWindowPreset(e.target.value as DashboardWindow);
                }}
              >
                <option value="1h">最近 1 小时</option>
                <option value="24h">最近 24 小时</option>
                <option value="7d">最近 7 天</option>
              </select>
            </label>
            <button disabled={!datasetId || busy || listing} type="submit">
              {summary ? "刷新快照" : "加载快照"}
            </button>
          </form>
          {busy && <p role="status">正在读取同一来源快照…</p>}
          {summary && <Summary data={summary} stale={stale} />}
        </>
      )}
    </section>
  );
}
const reasons: Record<string, string> = {
  no_inventory_producer: "尚无资产清单来源",
  no_connection_health_producer: "尚无连接健康来源",
  no_collector_producer: "尚无采集器来源",
  no_request_counter: "尚无请求计数来源",
  no_case_creation_clock: "未记录案例创建时刻",
  no_baseline_producer: "尚无基线检测来源",
  no_vulnerability_producer: "尚无漏洞检测来源",
  no_weak_password_producer: "尚无弱口令检测来源",
  no_response_producer: "尚无阻断执行来源",
  no_attack_classification: "尚无攻击分类来源",
};
function Counts({
  title,
  values,
}: {
  title: string;
  values: { name: string; count: number }[];
}) {
  return (
    <section>
      <h4>{title}（TOP 5）</h4>
      {values.length ? (
        <table>
          <caption>{title}频次，不表示失陷或受害</caption>
          <thead>
            <tr>
              <th>观察值</th>
              <th>事件次数</th>
            </tr>
          </thead>
          <tbody>
            {values.map((v) => (
              <tr key={v.name}>
                <td>{v.name}</td>
                <td>{v.count}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p>已完整读取的窗口内无该类观察值。</p>
      )}
    </section>
  );
}
function Summary({
  data: s,
  stale,
}: {
  data: DashboardSummary;
  stale: boolean;
}) {
  const activeCloses = s.caseCloseThroughput.buckets.filter((b) => b.count > 0);
  return (
    <article aria-label="SIMULATED 合成态势快照">
      <h3>SIMULATED · 合成证据快照{stale ? "（旧快照，尚未重新验证）" : ""}</h3>
      <p>快照标识：{s.snapshotId}</p>
      <p>读取时间：{s.asOf} · 内容指纹不是持久重放凭证。</p>
      <p>
        数据集：{s.dataset.id} · 夹具：{s.dataset.fixture} · SHA256：
        {s.dataset.fixtureHash}
      </p>
      <p>来源契约：{s.dataset.contract}；产品验收：未完成。</p>
      <p>
        UTC 窗口 [{s.window.start}, {s.window.end})，活动桶宽{" "}
        {s.window.bucketSeconds} 秒。
      </p>
      <p>
        来源完整读取：{s.coverage.eventCount} 条事件、{s.coverage.caseCount}{" "}
        个案例；排除未来事件 {s.coverage.excludedFutureEvents} 条。
      </p>
      <p>
        来源时间范围：{s.coverage.sourceFirstAt ?? "无事件"} 至{" "}
        {s.coverage.sourceLastAt ?? "无事件"}
      </p>
      <section>
        <h4>案例当前状态（不按历史窗口回溯）</h4>
        <p>
          总数 {s.cases.total}；待处理 {s.cases.open}；已关闭 {s.cases.closed}。
        </p>
        <p>
          未标记 {s.cases.marks.unmarked}；良性标记 {s.cases.marks.benign}
          ；可疑标记 {s.cases.marks.suspicious}。
        </p>
      </section>
      <section>
        <h4>窗口内合成事件活动</h4>
        <p>
          分母：窗口内 {s.eventActivity.total} 条事件；观察到{" "}
          {s.eventActivity.distinctActors} 个主体、
          {s.eventActivity.distinctDevices} 个设备值。这不是资产数量或攻击判定。
        </p>
        <table>
          <caption>按事件类型的完整窗口计数</caption>
          <thead>
            <tr>
              <th>类型</th>
              <th>次数</th>
            </tr>
          </thead>
          <tbody>
            {s.eventActivity.byKind.map((k) => (
              <tr key={k.kind}>
                <td>{k.kind}</td>
                <td>{k.count}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <details>
          <summary>查看 UTC 活动时间桶</summary>
          <table>
            <caption>窗口活动分布，零值仅表示完整来源的该桶无事件</caption>
            <thead>
              <tr>
                <th>UTC 桶开始</th>
                <th>登录失败</th>
                <th>登录成功</th>
                <th>服务访问</th>
              </tr>
            </thead>
            <tbody>
              {s.eventActivity.buckets.map((b) => (
                <tr key={b.start}>
                  <td>{b.start}</td>
                  <td>{b.countsByKind.login_failure ?? 0}</td>
                  <td>{b.countsByKind.login_success ?? 0}</td>
                  <td>{b.countsByKind.service_access ?? 0}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
        <Counts title="观察主体" values={s.eventActivity.topActors} />
        <Counts title="观察设备" values={s.eventActivity.topDevices} />
        <Counts title="观察来源 IP" values={s.eventActivity.topSourceIPs} />
      </section>
      <section>
        <h4>案例关闭吞吐（不是请求速率）</h4>
        <p>
          窗口关闭次数 {s.caseCloseThroughput.windowTotal}；最近 60 秒关闭次数{" "}
          {s.caseCloseThroughput.last60Seconds}；单位：案例关闭次数/分钟。
        </p>
        <details>
          <summary>
            查看非零分钟桶（{activeCloses.length}/
            {s.caseCloseThroughput.buckets.length}）
          </summary>
          <p>仅列非零桶；其余已完整统计的分钟桶为零。</p>
          <table>
            <caption>非零案例关闭分钟桶</caption>
            <thead>
              <tr>
                <th>UTC 分钟</th>
                <th>关闭次数</th>
              </tr>
            </thead>
            <tbody>
              {activeCloses.map((b) => (
                <tr key={b.start}>
                  <td>{b.start}</td>
                  <td>{b.count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      </section>
      <section>
        <h4>单主体示例风险趋势</h4>
        {s.risk.status === "available" ? (
          <>
            <p>
              主体 {s.risk.subject}；示例分数 {s.risk.score}；修订{" "}
              {s.risk.revision}；映射 {s.risk.mappingVersion}。
            </p>
            <p>重置边界：{s.risk.resetBoundary ?? "未重置"}</p>
            <details>
              <summary>查看风险趋势</summary>
              <table>
                <caption>合成风险趋势，不代表真实威胁效力</caption>
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>分数</th>
                  </tr>
                </thead>
                <tbody>
                  {s.risk.trend?.map((p, i) => (
                    <tr key={`${p.at}-${i}`}>
                      <td>{p.at}</td>
                      <td>{p.score}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          </>
        ) : (
          <p>
            {s.risk.status === "not_selected"
              ? "未选择风险主体，未计算分数。"
              : "所选主体在此来源中不存在，未计算分数。"}
          </p>
        )}
      </section>
      <section>
        <h4>当前用户最近成功的通用合成检测（不受统计窗口限制）</h4>
        {s.latestManualDetection.status === "available" ? (
          <>
            <p>
              任务：{s.latestManualDetection.taskUUID}；完成：
              {s.latestManualDetection.completedAt}
            </p>
            <p>
              模板 {s.latestManualDetection.templateId} v
              {s.latestManualDetection.templateVersion}；摘要{" "}
              {s.latestManualDetection.templateHash}
            </p>
            <p>
              事件数 {s.latestManualDetection.eventCount}；匹配数{" "}
              {s.latestManualDetection.matchCount}
              。这不是基线、漏洞或弱口令检测覆盖。
            </p>
          </>
        ) : (
          <p>当前用户、授权版本与数据集下没有可见的成功检测结果。</p>
        )}
      </section>
      <section>
        <h4>未具备来源的原始指标</h4>
        <ul>
          {s.unavailable.map((v) => (
            <li key={v.metric}>
              {v.metric}：不可用（{reasons[v.reason] ?? "缺少对应事实来源"}）。
            </li>
          ))}
        </ul>
      </section>
    </article>
  );
}
