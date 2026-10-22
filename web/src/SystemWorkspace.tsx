import { useState } from "react";
import "./system-soc.css";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import {
  ErrorNotice,
  Field,
  FormActions,
  ProofFields,
  getProof,
  readValues,
} from "./access-common";
import {
  systemAPI,
  systemOperations,
  type Dependency,
  type Availability,
  type GraphType,
  type History,
  type HistoryInput,
  type Snapshot,
  type StorageItem,
  type SystemHealth,
  type SystemOperation,
} from "./system-api";
import {
  Freshness,
  availabilityLabel,
  byteText,
  percentText,
  observationReason,
  useSystemMutation,
  useSystemRead,
  utc,
  type SystemContext,
} from "./system-common";

type Tab = "overview" | "resources" | "storage" | "services" | "health";
const tabs: { id: Tab; label: string; operation: SystemOperation }[] = [
  { id: "overview", label: "系统信息", operation: "GET /api/system/info" },
  {
    id: "resources",
    label: "资源与历史",
    operation: "GET /api/system/resources/current",
  },
  { id: "storage", label: "存储管理", operation: "GET /api/system/storage" },
  {
    id: "services",
    label: "服务运行状态",
    operation: "GET /api/system/services",
  },
  { id: "health", label: "平台依赖健康", operation: "GET /api/system/health" },
];
export default function SystemWorkspace({
  profile,
  sessionChanged,
}: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  // Restore only exact read-only routes; editors and unknown suffixes start safe.
  const [tab, setTab] = useState<Tab>(
      () =>
        tabs.find((t) => window.location.hash === `#system/${t.id}`)?.id ??
        "overview",
    ),
    [revision, setRevision] = useState(0);
  const gate = useSystemRead(
    profile.csrfToken,
    async (signal) => {
      const [menu, checks] = await Promise.all([
        accessRequest<{ menu: Permission[] }>("/menu", signal),
        accessRequest<{ results: boolean[] }>(
          "/check",
          signal,
          { paths: systemOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== systemOperations.length ||
        checks.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return {
        readable: menu.menu.some((p) => p.mark === "system" && p.auth.readable),
        results: checks.results,
      };
    },
    sessionChanged,
  );
  const context: SystemContext = {
    profile,
    sessionChanged,
    can: (operation) =>
      gate.data?.results[systemOperations.indexOf(operation)] === true,
  };
  return (
    <div className="system-workspace system-soc">
      <header className="system-soc-heading">
        <div>
          <span className="system-kicker">SYSTEM OPERATIONS</span>
          <h2>系统健康</h2>
        </div>
        <span className="system-scope">local-api · 实际运行观测 · UTC</span>
      </header>
      <p className="muted">
        查看 API 运行环境的实际采样、存储和依赖状态。缺失或过期数据会明确标记。
      </p>
      <ErrorNotice error={gate.error} />
      {gate.busy && <p role="status">正在确认系统健康权限…</p>}
      {gate.error && (
        <button className="secondary" onClick={gate.refresh}>
          重新读取系统权限
        </button>
      )}
      {gate.data && !gate.data.readable && (
        <p role="status">当前账户没有系统健康读取权限。</p>
      )}
      {gate.data?.readable && (
        <>
          <nav className="access-tabs" aria-label="系统健康分类">
            {tabs
              .filter((t) => context.can(t.operation))
              .map((t) => (
                <button
                  key={t.id}
                  className={tab === t.id ? "selected" : ""}
                  aria-current={tab === t.id ? "page" : undefined}
                  onClick={() => {
                    setTab(t.id);
                    setRevision((n) => n + 1);
                    window.history.pushState({}, "", `#system/${t.id}`);
                  }}
                >
                  {t.label}
                </button>
              ))}
          </nav>
          {!context.can(tabs.find((t) => t.id === tab)!.operation) && (
            <p role="status">
              当前账户没有此项读取权限，请选择已获授权的分类。
            </p>
          )}
          <div className="system-soc-panel" key={`${tab}-${revision}`}>
            {tab === "overview" && context.can("GET /api/system/info") && (
              <Overview context={context} />
            )}
            {tab === "resources" &&
              context.can("GET /api/system/resources/current") && (
                <Resources context={context} />
              )}
            {tab === "storage" && context.can("GET /api/system/storage") && (
              <Storage context={context} />
            )}
            {(tab === "services" || tab === "health") &&
              context.can(
                tab === "health"
                  ? "GET /api/system/health"
                  : "GET /api/system/services",
              ) && <Services context={context} healthOnly={tab === "health"} />}
          </div>
        </>
      )}
    </div>
  );
}
function ReadStatus({
  busy,
  error,
  refresh,
  label,
}: {
  busy: boolean;
  error: string;
  refresh: () => void;
  label: string;
}) {
  return (
    <>
      <ErrorNotice error={error} />
      {busy && <p role="status">正在读取{label}…</p>}
      <button className="secondary" onClick={refresh} disabled={busy}>
        刷新{label}
      </button>
    </>
  );
}
function MetricSnapshot({
  snapshot,
  stale,
  availability,
  reason,
}: {
  snapshot: Snapshot | null;
  stale: boolean;
  availability: Availability;
  reason?: string;
}) {
  if (!snapshot)
    return (
      <p className="resource-empty" role="status">
        尚无真实采样。不会用零值替代缺失数据。
      </p>
    );
  const rows = [
    {
      name: "CPU 使用率",
      value: percentText(snapshot.cpu.percent),
      metric: snapshot.cpu,
    },
    {
      name: "内存使用率",
      value: percentText(snapshot.memory.percent),
      metric: snapshot.memory,
    },
    {
      name: "运行时长",
      value:
        snapshot.uptime.seconds === null
          ? "不可用"
          : `${snapshot.uptime.seconds} 秒`,
      metric: snapshot.uptime,
    },
    {
      name: "系统负载 1 / 5 / 15 分钟",
      value: snapshot.load.values
        ? `${snapshot.load.values.one} / ${snapshot.load.values.five} / ${snapshot.load.values.fifteen}`
        : "不可用",
      metric: snapshot.load,
    },
  ];
  return (
    <>
      <p className="warning">
        CPU、内存、运行时长与负载的范围为
        kernel_visible（内核可见资源），不等同于容器配额。
      </p>
      <div className="system-metrics">
        {rows.map((row) => (
          <article key={row.name}>
            <h3>{row.name}</h3>
            <strong className="system-value">{row.value}</strong>
            <small className="block">
              {availabilityLabel(row.metric.availability)}
              {row.metric.reason
                ? ` · ${observationReason(row.metric.reason)}`
                : ""}
            </small>
            <small className="block">
              {row.metric.source} · {row.metric.scope}
              <br />
              采样（UTC）：{utc(row.metric.observedAt)}
            </small>
          </article>
        ))}
      </div>
      <p>
        内存总量：{byteText(snapshot.memory.totalBytes)} · 已用：
        {byteText(snapshot.memory.usedBytes)} · 可用：
        {byteText(snapshot.memory.availableBytes)}
      </p>
      <ContainerMemory
        snapshot={snapshot}
        stale={stale}
        availability={availability}
        reason={reason}
      />
      {snapshot.cpu.intervalStart && (
        <p className="muted">
          CPU 采样区间（UTC）：{utc(snapshot.cpu.intervalStart)} 至{" "}
          {utc(snapshot.cpu.observedAt)}
        </p>
      )}
    </>
  );
}
function ContainerMemory({
  snapshot,
  stale,
  availability,
  reason,
}: {
  snapshot: Snapshot;
  stale: boolean;
  availability: Availability;
  reason?: string;
}) {
  const metric = snapshot.containerMemory;
  const trusted = !stale && availability === "available";
  const known = metric?.availability === "available" && trusted;
  return (
    <section className="system-container-memory" aria-label="容器内存观测">
      <div className="system-finding-title">
        <h3>容器内存使用率</h3>
        <span
          className="system-badge"
          data-state={known ? "observed" : "unknown"}
        >
          {stale
            ? "数据过期"
            : !trusted
              ? "不可用 · 观测未确认"
              : !metric
                ? "不支持 · 无历史观测"
                : availabilityLabel(metric.availability)}
        </span>
      </div>
      <strong className="system-value">
        {metric ? percentText(metric.percent) : "无观测"}
      </strong>
      <p>
        当前使用量：{byteText(metric?.usageBytes ?? null)} · 可见层级上限：
        {metric?.limitState === "unlimited"
          ? "无限制"
          : byteText(metric?.limitBytes ?? null)}
      </p>
      <p className="muted">
        范围：runtime_cgroup · 仅反映可见 cgroup
        层级；隐藏祖先约束与同组进程竞争仍可能进一步限制可用内存。
      </p>
      {!metric ? (
        <p className="warning">
          此记录没有容器内存观测字段，无法判断容器内存压力；等待升级后的实际采样。
        </p>
      ) : (
        <>
          <small className="block">
            {metric.source} · {metric.scope} · {metric.hierarchy} ·
            采样（UTC）：{utc(metric.observedAt)}
          </small>
          {stale ? (
            <p className="warning">
              此值属于过期观测，当前压力未知。检查采样与数据库写入，恢复后刷新确认。
            </p>
          ) : !trusted ? (
            <p className="warning">
              当前容器内存观测不可用，压力未知。
              {observationReason(reason || "unavailable_resource_observation")}
              （{reason || "原因未提供"}）
            </p>
          ) : metric.limitState === "unlimited" ? (
            <p className="warning">
              可见层级没有有限上限，使用量可读，但无法计算配额使用率；请核对运行环境的内存限制。
            </p>
          ) : !known ? (
            <p className="warning">
              容器内存压力未知（{metric.reason || "原因未提供"}）。
              {observationReason(
                metric.reason || "unavailable_resource_observation",
              )}
              检查 cgroup 挂载、内存控制器及读取权限，再等待实际采样。
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}
function Overview({ context }: { context: SystemContext }) {
  const read = useSystemRead(
    "info",
    async (signal) => ({
      info: await systemAPI.info(signal),
      nodes: context.can("GET /api/system/nodes")
        ? await systemAPI.nodes(signal)
        : null,
    }),
    context.sessionChanged,
    15000,
  );
  const info = read.data?.info;
  return (
    <>
      <h3>平台基本信息</h3>
      <ReadStatus {...read} label="系统信息" />
      {info && (
        <>
          <dl className="system-facts">
            {[
              ["系统名称", info.basic.systemName],
              ["系统当前时间（UTC）", utc(info.systemCurrentTime)],
              ["操作系统平台", info.version.OSPlatform],
              ["操作系统版本", info.version.OSVersion ?? "未提供"],
              ["Go 版本", info.version.goVersion],
              ["构建修订", info.version.revision ?? "未提供"],
              ["主版本", info.version.majorVersion ?? "未提供"],
              ["引擎版本", info.version.engineVersion ?? "未配置"],
              ["平台 IP", info.basic.ip ?? "未提供"],
              ["公司名称", info.basic.companyName ?? "未配置"],
              ["官方网站", info.basic.officialWebsite ?? "未配置"],
            ].map(([name, value]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          {read.data?.nodes && (
            <div className="table-scroll">
              <table>
                <caption>已登记节点</caption>
                <thead>
                  <tr>
                    <th scope="col">实例</th>
                    <th scope="col">主机名</th>
                    <th scope="col">IP</th>
                    <th scope="col">范围</th>
                    <th scope="col">采样可用性</th>
                  </tr>
                </thead>
                <tbody>
                  {read.data.nodes.nodeList.map((n) => (
                    <tr key={n.instance}>
                      <th scope="row">{n.instance}</th>
                      <td>{n.hostName ?? "未提供"}</td>
                      <td>{n.ip ?? "未提供"}</td>
                      <td>{n.scope}</td>
                      <td>{availabilityLabel(n.availability)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <Freshness
            {...info.current}
            observedAt={info.current.snapshot?.observedAt ?? null}
          />
          <MetricSnapshot
            snapshot={info.current.snapshot}
            stale={info.current.stale}
            availability={info.current.availability}
            reason={info.current.reason}
          />
          <h3>已登记能力</h3>
          <p>
            授权许可、升级、清理、外部引擎与公司元数据按服务器能力展示；未配置项没有可执行操作。
          </p>
          <dl className="system-facts">
            {Object.entries(info.capabilities).map(([name, value]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
    </>
  );
}
const graphLabels: Record<GraphType, string> = {
  cpu_basic: "CPU 使用率",
  ram_basic: "内存使用率",
  container_ram: "容器内存使用率",
  disk_usage: "API 文件系统使用率",
};
function Resources({ context }: { context: SystemContext }) {
  const [range, setRange] = useState(900),
    [graph, setGraph] = useState<GraphType>("cpu_basic"),
    [storageId, setStorageId] = useState("runtime-root");
  const read = useSystemRead(
    `${range}:${graph}:${storageId}`,
    async (signal) => {
      const [current, storage] = await Promise.all([
        systemAPI.current(signal),
        context.can("GET /api/system/storage")
          ? systemAPI.storage(1, 50, signal)
          : Promise.resolve(null),
      ]);
      const selectedStorage =
        storage?.storage.find((item) => item.id === storageId) ??
        storage?.storage[0];
      const missingStorage = graph === "disk_usage" && !selectedStorage;
      const endTime = Math.floor(Date.parse(current.checkedAt) / 1000),
        query: HistoryInput = {
          instance: "local-api",
          graphType: graph,
          startTime: endTime - range,
          endTime,
          ...(graph === "disk_usage" && selectedStorage
            ? { storageId: selectedStorage.id }
            : {}),
        };
      const history =
        context.can("GET /api/system/resources/history") && !missingStorage
          ? await systemAPI.history(query, signal)
          : null;
      return { current, storage, history, query, missingStorage };
    },
    context.sessionChanged,
    15000,
  );
  return (
    <>
      <h3>运行资源与历史</h3>
      <div className="form-grid">
        <Field label="历史时间范围">
          <select
            value={range}
            onChange={(e) => setRange(Number(e.target.value))}
          >
            <option value={900}>最近 15 分钟</option>
            <option value={3600}>最近 1 小时</option>
            <option value={21600}>最近 6 小时</option>
            <option value={86400}>最近 24 小时</option>
          </select>
        </Field>
        <Field label="历史指标">
          <select
            value={graph}
            onChange={(e) => setGraph(e.target.value as GraphType)}
          >
            {Object.entries(graphLabels).map(([id, label]) => (
              <option key={id} value={id}>
                {label}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {graph === "disk_usage" && (
        <Field label="已登记存储目标">
          <select
            value={read.data?.query.storageId ?? storageId}
            disabled={!read.data?.storage?.storage.length}
            onChange={(e) => setStorageId(e.target.value)}
          >
            {(read.data?.storage?.storage ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.id} · {s.mount}
              </option>
            ))}
          </select>
        </Field>
      )}
      <ReadStatus {...read} label="资源与历史" />
      {read.data && (
        <>
          <Freshness
            {...read.data.current}
            observedAt={read.data.current.snapshot?.observedAt ?? null}
          />
          <MetricSnapshot
            snapshot={read.data.current.snapshot}
            stale={read.data.current.stale}
            availability={read.data.current.availability}
            reason={read.data.current.reason}
          />
          {read.data.history ? (
            <HistoryView
              key={`${range}:${graph}:${storageId}`}
              history={read.data.history}
              query={read.data.query}
            />
          ) : (
            <p role="status">
              {read.data.missingStorage
                ? "没有可读取的已登记存储目标，无法查询文件系统历史。"
                : "当前账户没有资源历史读取权限。"}
            </p>
          )}
        </>
      )}
    </>
  );
}
function HistoryView({
  history,
  query,
}: {
  history: History;
  query: HistoryInput;
}) {
  const [page, setPage] = useState(1),
    series = history.info[0].data,
    stats = series.dataStatistics;
  const x = (time: number) =>
    40 + ((time - query.startTime) / (query.endTime - query.startTime)) * 700;
  const count = series.timestamp.length,
    pages = Math.max(1, Math.ceil(count / 20)),
    safePage = Math.min(page, pages),
    scaleMax =
      query.graphType === "container_ram"
        ? Math.max(100, ...series.value.map(Number))
        : 100;
  return (
    <section className="system-history">
      <h3>{graphLabels[query.graphType]}历史</h3>
      <p>
        范围（UTC）：{utc(new Date(query.startTime * 1000).toISOString())} 至{" "}
        {utc(new Date(query.endTime * 1000).toISOString())}（终点不包含）
      </p>
      <p>
        此指标最早可用时间（UTC）：{utc(history.availableSince)} · 采样间隔：
        {history.sampleIntervalSeconds} 秒 · 实际样本：{count}
      </p>
      <p className="muted">
        仅绘制真实观测点；灰色区域为缺测区间，不补零、不插值。历史最多查询 24
        小时。
      </p>
      {query.graphType === "container_ram" && (
        <p className="warning">
          仅使用已持久化的有限上限观测；无限制、未知与旧记录为缺测。可见层级上限不包含隐藏祖先约束与共享竞争。
        </p>
      )}
      {query.graphType === "container_ram" && (
        <p className="muted">
          {history.segments === undefined
            ? "此历史响应未提供观测分段信息。"
            : `观测分段：${history.segments.length} 段。`}
          来源、容器身份或可见上限变化会开始新段；缺少旧记录身份时单独分段。段边界表示计量依据变化，与缺测区间分别展示；各段仍仅绘制实际散点。
        </p>
      )}
      {query.graphType === "disk_usage" && (
        <p className="warning">
          此历史属于 API 运行环境文件系统，不代表 PostgreSQL
          数据盘或容器磁盘配额。
        </p>
      )}
      {count === 0 ? (
        <p className="resource-empty" role="status">
          此范围没有真实历史样本，统计值不可用。
        </p>
      ) : (
        <svg
          className="system-chart"
          viewBox="0 0 780 180"
          role="img"
          aria-label={`${graphLabels[query.graphType]}：${count} 个实际样本，${history.gaps.length} 个缺测区间`}
        >
          <title>实际资源采样点，缺测区间不连线</title>
          <line x1="40" x2="740" y1="145" y2="145" stroke="currentColor" />
          <text x="0" y="20">
            {scaleMax}%
          </text>
          <text x="10" y="149">
            0%
          </text>
          {history.gaps.map((g, i) => (
            <rect
              key={i}
              x={x(g.startTime)}
              y="15"
              width={Math.max(0, x(g.endTime) - x(g.startTime))}
              height="130"
              fill="#dce4e5"
            />
          ))}
          {series.timestamp.map((time, i) => (
            <circle
              key={`${time}:${i}`}
              cx={x(time)}
              cy={145 - (Number(series.value[i]) / scaleMax) * 130}
              r="2.5"
              fill="#075e59"
            >
              <title>
                {utc(new Date(time * 1000).toISOString())}：{series.value[i]}%
              </title>
            </circle>
          ))}
        </svg>
      )}
      <dl className="system-statistics">
        {[
          ["最大值", stats.max],
          ["平均值", stats.avg],
          ["最小值", stats.min],
          ["最近观测值", stats.current],
        ].map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{percentText(value as number | null)}</dd>
          </div>
        ))}
      </dl>
      {history.gaps.length > 0 && (
        <details>
          <summary>查看 {history.gaps.length} 个实际缺测区间</summary>
          <ul>
            {history.gaps.map((gap, i) => (
              <li key={i}>
                {utc(new Date(gap.startTime * 1000).toISOString())} 至{" "}
                {utc(new Date(gap.endTime * 1000).toISOString())}
                <p>
                  {history.availableSince &&
                  gap.endTime * 1000 <= Date.parse(history.availableSince)
                    ? "早于此指标最早可用记录，当前数据库没有该时段历史。"
                    : "该区间缺少此指标的可用记录；历史接口未提供根因。请对照采样状态、API 日志与数据库写入记录定位。"}
                </p>
              </li>
            ))}
          </ul>
        </details>
      )}
      {count > 0 && (
        <details>
          <summary>查看原始采样数据</summary>
          <div className="table-scroll">
            <table>
              <caption>实际历史采样</caption>
              <thead>
                <tr>
                  <th scope="col">采样时间（UTC）</th>
                  <th scope="col">百分比（原始十进制字符串）</th>
                </tr>
              </thead>
              <tbody>
                {series.timestamp
                  .slice((safePage - 1) * 20, safePage * 20)
                  .map((time, i) => (
                    <tr key={`${time}:${i}`}>
                      <th scope="row">
                        {utc(new Date(time * 1000).toISOString())}
                      </th>
                      <td>{series.value[(safePage - 1) * 20 + i]}%</td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
          <div className="pagination">
            <span>
              共 {count} 条 · 第 {safePage} / {pages} 页
            </span>
            <button
              className="secondary"
              disabled={safePage === 1}
              onClick={() => setPage(safePage - 1)}
            >
              上一页采样
            </button>
            <button
              className="secondary"
              disabled={safePage === pages}
              onClick={() => setPage(safePage + 1)}
            >
              下一页采样
            </button>
          </div>
        </details>
      )}
    </section>
  );
}
function Storage({ context }: { context: SystemContext }) {
  const [page, setPage] = useState(1),
    [pageSize, setPageSize] = useState(20),
    [editing, setEditing] = useState<StorageItem | null>(null),
    [revision, setRevision] = useState(0),
    [notice, setNotice] = useState("");
  const done = (message: string) => {
    setEditing(null);
    setNotice(message);
    setRevision((n) => n + 1);
  };
  if (editing)
    return (
      <AlarmForm
        key={`${editing.id}:${editing.revision}`}
        context={context}
        item={editing}
        done={done}
      />
    );
  return (
    <StorageList
      key={`${page}:${pageSize}:${revision}`}
      context={context}
      page={page}
      pageSize={pageSize}
      changePage={setPage}
      changeSize={(size) => {
        setPage(1);
        setPageSize(size);
      }}
      notice={notice}
      edit={(item) => {
        setEditing(item);
        setNotice("");
        window.history.pushState(
          {},
          "",
          `#system/storage/settings/${encodeURIComponent(item.id)}`,
        );
      }}
    />
  );
}
function StorageList({
  context,
  page,
  pageSize,
  changePage,
  changeSize,
  notice,
  edit,
}: {
  context: SystemContext;
  page: number;
  pageSize: number;
  changePage: (v: number) => void;
  changeSize: (v: number) => void;
  notice: string;
  edit: (v: StorageItem) => void;
}) {
  const read = useSystemRead(
    `${page}:${pageSize}`,
    (signal) => systemAPI.storage(page, pageSize, signal),
    context.sessionChanged,
    15000,
  );
  return (
    <>
      <h3>API 运行环境存储</h3>
      <p className="warning">
        测量范围为 runtime_filesystem：API 运行环境文件系统，不代表 PostgreSQL
        数据盘，也不是容器磁盘配额。可用空间不含文件系统保留空间。
      </p>
      <p>
        默认告警阈值 85% 是本地策略，可设置为 85–90
        的整数。当前不支持自动清理或日志保存天数设置。
      </p>
      {notice && (
        <p role="status" className="task-notice">
          {notice}
        </p>
      )}
      <Field label="存储每页条数">
        <select
          value={pageSize}
          onChange={(e) => changeSize(Number(e.target.value))}
        >
          {[10, 20, 30, 40, 50].map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </Field>
      <ReadStatus {...read} label="存储状态" />
      {read.data && (
        <>
          <Freshness {...read.data} />
          <div className="table-scroll">
            <table>
              <caption>实际文件系统存储</caption>
              <thead>
                <tr>
                  {[
                    "存储目标",
                    "文件系统",
                    "总容量",
                    "已用",
                    "可用",
                    "保留",
                    "已用百分比",
                    "挂载点",
                    "告警阈值",
                    "告警状态",
                    "观测状态",
                    "操作",
                  ].map((h) => (
                    <th scope="col" key={h}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {read.data.storage.map((item) => (
                  <tr key={item.id}>
                    <th scope="row">{item.id}</th>
                    <td>{item.fs || "未提供"}</td>
                    <td>{byteText(item.totalBytes)}</td>
                    <td>{byteText(item.usedBytes)}</td>
                    <td>{byteText(item.freeBytes)}</td>
                    <td>{byteText(item.reservedBytes)}</td>
                    <td>{percentText(item.percent)}</td>
                    <td>{item.mount}</td>
                    <td>
                      {item.alarmPercent}%
                      <small className="block">版本 {item.revision}</small>
                    </td>
                    <td>
                      {read.data!.stale
                        ? "数据过期，需重新确认"
                        : item.alarmExceeded === null
                          ? "不可用"
                          : item.alarmExceeded
                            ? "达到告警阈值"
                            : "未达到告警阈值"}
                    </td>
                    <td>
                      {availabilityLabel(item.availability)}
                      <small className="block">
                        {item.source} · {item.scope}
                        <br />
                        {utc(item.observedAt)}
                        {item.reason ? ` · ${item.reason}` : ""}
                      </small>
                    </td>
                    <td>
                      {context.can("POST /api/system/storage/settings") ? (
                        <button
                          className="secondary"
                          onClick={() => edit(item)}
                          aria-label={`调整阈值 ${item.id}`}
                        >
                          调整阈值
                        </button>
                      ) : (
                        "只读"
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {read.data.storage.length === 0 && (
            <p role="status">此页没有已登记存储目标。</p>
          )}
          <div className="pagination">
            <span>
              共 {read.data.total} 条 · 第 {read.data.page} 页
            </span>
            <button
              className="secondary"
              disabled={page <= 1}
              onClick={() => changePage(page - 1)}
            >
              上一页存储
            </button>
            <button
              className="secondary"
              disabled={page * pageSize >= read.data.total}
              onClick={() => changePage(page + 1)}
            >
              下一页存储
            </button>
          </div>
        </>
      )}
    </>
  );
}
function AlarmForm({
  context,
  item,
  done,
}: {
  context: SystemContext;
  item: StorageItem;
  done: (notice: string) => void;
}) {
  const mutation = useSystemMutation(context.sessionChanged),
    [reloadRequired, setReloadRequired] = useState(false);
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (mutation.isLocked() || reloadRequired) return;
        const values = readValues(event),
          proof = getProof(values),
          percent = Number(values.percent);
        if (
          !/^(8[5-9]|90)$/.test(values.percent) ||
          !Number.isInteger(percent) ||
          !proof
        ) {
          mutation.setError(
            "阈值需为 85–90 的整数，并提供有效密码和六位未使用验证码。",
          );
          return;
        }
        const form = event.currentTarget;
        void mutation.run(
          (signal) => {
            for (const name of ["actorPassword", "totpCode"]) {
              const field = form.elements.namedItem(name);
              if (field instanceof HTMLInputElement) field.value = "";
            }
            return systemAPI.settings(
              {
                instance: "local-api",
                storageId: item.id,
                setType: "alarm",
                percent,
                expectedRevision: item.revision,
              },
              proof,
              context.profile.csrfToken,
              signal,
            );
          },
          () => done("存储阈值已由服务器保存，以下为重新读取的状态。"),
          (error) => {
            if (
              error instanceof ApiError &&
              (error.status >= 500 ||
                ["revision_conflict", "network", "invalid_response"].includes(
                  error.code,
                ))
            )
              setReloadRequired(true);
          },
        );
      }}
    >
      <h3>调整存储告警阈值</h3>
      <p>
        目标：local-api / {item.id} · 挂载点：{item.mount} · 当前版本：
        {item.revision}
      </p>
      <p>当前已确认阈值：{item.alarmPercent}% 。保存只改变此目标的告警策略。</p>
      <ErrorNotice error={mutation.error} />
      <fieldset disabled={mutation.busy || reloadRequired}>
        <Field label="存储告警阈值（%）">
          <input
            name="percent"
            type="number"
            min={85}
            max={90}
            step={1}
            defaultValue={item.alarmPercent}
            required
          />
        </Field>
        <ProofFields />
      </fieldset>
      {reloadRequired && (
        <p className="warning" role="status">
          本次结果或版本尚未确认，已停止重试。重新读取服务器状态后，才能开始新的修改。
        </p>
      )}
      {reloadRequired && (
        <button
          type="button"
          className="secondary"
          onClick={() => done("请核对重新读取的阈值与版本，再决定是否修改。")}
        >
          重新读取存储设置
        </button>
      )}
      <FormActions
        busy={mutation.busy}
        disabled={reloadRequired}
        cancel={() =>
          done(
            "已停止等待并清空证明字段。取消不能撤销已提交修改，请核对服务器状态。",
          )
        }
      >
        保存告警阈值
      </FormActions>
    </form>
  );
}
const dependencyLabel: Record<string, string> = {
  healthy: "健康",
  unhealthy: "不健康",
  unknown: "未知",
  not_configured: "未配置",
};
function Services({
  context,
  healthOnly,
}: {
  context: SystemContext;
  healthOnly: boolean;
}) {
  const [type, setType] = useState<"all" | "service" | "port" | "engine">(
      "all",
    ),
    label = healthOnly ? "平台依赖健康" : "服务运行状态";
  const read = useSystemRead(
    type,
    (signal) =>
      healthOnly ? systemAPI.health(signal) : systemAPI.services(type, signal),
    context.sessionChanged,
    5000,
  );
  return (
    <>
      <h3>{label}</h3>
      {!healthOnly && (
        <Field label="诊断类型">
          <select
            value={type}
            onChange={(e) => setType(e.target.value as typeof type)}
          >
            <option value="all">所有</option>
            <option value="service">服务</option>
            <option value="port">端口与协议</option>
            <option value="engine">引擎</option>
          </select>
        </Field>
      )}
      <p>
        依赖状态来自已登记服务的实际检查。数据库检查核对 PostgreSQL
        协议及模式；缓存和分析引擎未配置时不会报告健康。
      </p>
      <ReadStatus {...read} label={label} />
      {read.data && <HealthTable health={read.data} />}
    </>
  );
}
function dependencyAction(dependency: Dependency): string {
  if (dependency.status === "not_configured")
    return "服务器未登记此能力，没有可用检查证据。需要此能力时先确认支持范围与接入配置。";
  if (dependency.id === "container-memory")
    return "打开资源与历史核对容器使用量、可见层级上限和采样时间；检查 cgroup 内存控制器、挂载及读取权限。达到上限时核对同组进程占用与配置，隐藏祖先仍可能施加额外约束。";
  if (dependency.id === "worker")
    return "核对下方各循环的最近活动与成功时间；检查 Worker 进程、队列与数据库连接。恢复后刷新，确认完整循环重新有活动。";
  if (dependency.id === "sampler")
    return "打开资源与历史核对采样时间、各指标原因及缺测区间；检查 API 采样日志和数据库写入，恢复后等待下一轮实际采样。";
  if (dependency.id === "postgresql")
    return "检查数据库连接、PostgreSQL 协议与 schema 兼容性；恢复连接后重新读取依赖检查。";
  return "根据检查依据核对服务日志与依赖连接，再刷新确认；当前接口没有提供更具体的修复证据。";
}
function HealthTable({ health }: { health: SystemHealth }) {
  const [anomaliesOnly, setAnomaliesOnly] = useState(false);
  const anomalies = health.dependencies.filter(
    (d) => d.status === "unhealthy" || d.status === "unknown",
  );
  const unconfigured = health.dependencies.filter(
    (d) => d.status === "not_configured",
  );
  const healthy = health.dependencies.filter((d) => d.status === "healthy");
  const displayed = anomaliesOnly ? anomalies : health.dependencies;
  return (
    <>
      <section className="system-diagnostics" aria-label="依赖诊断工作台">
        <div className="system-status-strip">
          <article>
            <span>已配置且检查通过</span>
            <strong>{healthy.length}</strong>
          </article>
          <article className={anomalies.length ? "system-attention" : ""}>
            <span>异常或未知</span>
            <strong>{anomalies.length}</strong>
          </article>
          <article>
            <span>未配置能力</span>
            <strong>{unconfigured.length}</strong>
          </article>
        </div>
        <h3>需要处理的异常</h3>
        {anomalies.length === 0 ? (
          <p className="system-empty">
            当前返回的依赖中没有异常或未知项。检查范围仅限服务器已登记依赖。
          </p>
        ) : (
          <ul className="system-findings">
            {anomalies.map((d) => (
              <li key={d.id}>
                <div className="system-finding-title">
                  <strong>{d.name}</strong>
                  <span className="system-badge" data-state={d.status}>
                    {dependencyLabel[d.status]}
                  </span>
                </div>
                <p>{observationReason(d.reason)}</p>
                <p>
                  <strong>处理建议：</strong>
                  {dependencyAction(d)}
                </p>
                <small>
                  依据：{d.reason || "服务器未提供原因"} · {d.source} ·{" "}
                  {d.scope}
                  <br />
                  最近观测（UTC）：{utc(d.observedAt)} · 检查：
                  {utc(d.checkedAt)}
                </small>
              </li>
            ))}
          </ul>
        )}
        {unconfigured.length > 0 && (
          <details className="system-unconfigured">
            <summary>未配置能力（{unconfigured.length}）</summary>
            <ul>
              {unconfigured.map((d) => (
                <li key={d.id}>
                  <strong>{d.name} · 未配置</strong>
                  <p>{dependencyAction(d)}</p>
                  <small>
                    {d.source} · {d.scope}
                  </small>
                </li>
              ))}
            </ul>
          </details>
        )}
      </section>
      <p
        role="status"
        className={
          health.result === "healthy" &&
          healthy.length > 0 &&
          anomalies.length === 0
            ? "success"
            : "warning"
        }
      >
        已配置依赖检查结果：
        {healthy.length + anomalies.length === 0
          ? "当前检查范围没有已配置依赖，健康无法判定"
          : health.result === "healthy"
            ? "健康"
            : "存在异常或未知状态"}{" "}
        · 检查时间（UTC）：{utc(health.checkedAt)}
      </p>
      <label className="system-anomaly-filter">
        <input
          type="checkbox"
          checked={anomaliesOnly}
          onChange={(event) => setAnomaliesOnly(event.target.checked)}
        />
        只看异常或未知依赖
      </label>
      {anomaliesOnly && displayed.length === 0 && (
        <p role="status">筛选范围内没有异常或未知依赖。</p>
      )}
      <div className="table-scroll">
        <table>
          <caption>实际依赖检查</caption>
          <thead>
            <tr>
              {[
                "依赖",
                "类型",
                "状态",
                "检查依据",
                "观测时间（UTC）",
                "检查时间（UTC）",
              ].map((h) => (
                <th scope="col" key={h}>
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {displayed.map((d) => (
              <tr key={d.id}>
                <th scope="row">
                  {d.name}
                  <small className="block">{d.id}</small>
                </th>
                <td>{d.type}</td>
                <td>
                  <span className="system-badge" data-state={d.status}>
                    {dependencyLabel[d.status]}
                  </span>
                </td>
                <td>
                  {d.reason}
                  <small className="block">
                    {d.source} · {d.scope}
                  </small>
                </td>
                <td>{utc(d.observedAt)}</td>
                <td>{utc(d.checkedAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <h3>Worker 实际活动</h3>
      <p>
        状态：{availabilityLabel(health.worker.availability)} · 过期界限：
        {health.worker.staleAfterSeconds} 秒
        {health.worker.reason
          ? ` · ${observationReason(health.worker.reason)}`
          : ""}
      </p>
      {health.worker.cycles.length === 0 ? (
        <p role="status">尚无 Worker 实际循环活动，状态未知。</p>
      ) : (
        <div className="table-scroll">
          <table>
            <caption>Worker 循环观测</caption>
            <thead>
              <tr>
                {[
                  "Worker",
                  "循环",
                  "状态",
                  "证据",
                  "最近活动（UTC）",
                  "最近成功（UTC）",
                  "可用性",
                ].map((h) => (
                  <th scope="col" key={h}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {health.worker.cycles.map((c) => (
                <tr key={`${c.workerId}:${c.cycle}`}>
                  <th scope="row">{c.workerId}</th>
                  <td>{c.cycle}</td>
                  <td>{c.status}</td>
                  <td>{c.evidence}</td>
                  <td>{utc(c.lastActivityAt)}</td>
                  <td>{utc(c.lastSuccessAt)}</td>
                  <td>
                    {availabilityLabel(c.availability)}
                    {c.reason ? ` · ${c.reason}` : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
