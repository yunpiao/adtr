import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationLabelsAPI as api,
  validateReceiptIntent,
  type Dataset,
  type Observation,
  type Policy,
  type Evaluation,
  type Comparison,
  type Receipt,
  type AssetLabels,
  type Spec,
  type Page,
  type Write,
  type WriteKind,
} from "./simulation-labels-api";
type EntryProps = { profile: Profile; sessionChanged: () => void };
const reads = [
  "datasets",
  "observations",
  "observation",
  "policies",
  "policy",
  "history",
  "evaluations",
  "evaluation",
  "assets",
  "asset",
  "comparisons",
  "comparison",
  "operation",
];
const writes: WriteKind[] = [
  "create",
  "update",
  "archive",
  "restore",
  "evaluate",
  "compare",
];
export default function SimulationLabelsWorkspace(props: EntryProps) {
  return <Entry key={JSON.stringify(props.profile)} {...props} />;
}
function Entry({ profile, sessionChanged }: EntryProps) {
  const changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  const [gate, setGate] = useState<{ read: boolean; manage: boolean } | null>(
      null,
    ),
    [domain, setDomain] = useState(""),
    [error, setError] = useState(""),
    [retry, setRetry] = useState(0);
  useEffect(() => {
    let live = true;
    const c = new AbortController();
    setGate(null);
    setDomain("");
    const paths = [
      "GET /api/domain-selection",
      "GET /api/domain-selection/resolve",
      ...reads.map((x) => "GET /api/simulation/labels/" + x),
      ...writes.map((x) => "POST /api/simulation/labels/" + x),
    ];
    Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", c.signal),
      accessRequest<{ results: boolean[] }>(
        "/check",
        c.signal,
        { paths },
        profile.csrfToken,
      ),
    ])
      .then(([menu, checks]) => {
        if (!live) return;
        if (
          !Array.isArray(checks.results) ||
          checks.results.length !== paths.length ||
          checks.results.some((x) => typeof x !== "boolean")
        )
          throw new ApiError("invalid_response");
        const readable = (m: string) =>
          menu.menu.some((p) => p.mark === m && p.auth?.readable === true);
        const read =
          readable("domains") &&
          readable("simulation_labels") &&
          checks.results.slice(0, 2 + reads.length).every(Boolean);
        setGate({
          read,
          manage: read && checks.results.slice(2 + reads.length).every(Boolean),
        });
      })
      .catch((e) => {
        if (!live) return;
        setGate({ read: false, manage: false });
        setError("未能确认模拟敏感资产权限。");
        if (e instanceof ApiError && e.status === 401) changed.current();
      });
    const notice = listenForSessionInvalidation(() => {
      live = false;
      c.abort();
      setGate(null);
      setDomain("");
      changed.current();
    });
    return () => {
      live = false;
      c.abort();
      notice.close();
    };
  }, [profile.csrfToken, retry]);
  return (
    <section aria-label="模拟敏感资产入口">
      <h2>SIMULATED · 敏感资产标签</h2>
      <p>仅可信合成观测；不连接真实 AD，不发送外部通知。</p>
      {error && <p role="alert">{error}</p>}
      {gate === null ? (
        <p role="status">核对权限中…</p>
      ) : !gate.read ? (
        <>
          <p>当前账户没有模拟敏感资产和域读取权限。</p>
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
          <SimulationLabelsDomainWorkspace
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
export type WorkspaceProps = EntryProps & {
  domainId: string;
  permissions?: { read: boolean; manage: boolean };
};
export function SimulationLabelsDomainWorkspace(props: WorkspaceProps) {
  return (
    <Workspace
      key={JSON.stringify([props.profile, props.domainId, props.permissions])}
      {...props}
    />
  );
}
type Intent = { kind: WriteKind; input: Write };
function Workspace({
  profile,
  domainId,
  sessionChanged,
  permissions = { read: false, manage: false },
}: WorkspaceProps) {
  const [denied, setDenied] = useState(false),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [refresh, setRefresh] = useState(0);
  const [windows, setWindows] = useState<Record<string, number>>({
    datasets: 0,
  });
  const [pageInfo, setPageInfo] = useState<
    Record<
      string,
      { offset: number; limit: number; count: number; shown: number }
    >
  >({});
  const [policyLoading, setPolicyLoading] = useState(false),
    [selectedPolicyId, setSelectedPolicyId] = useState("");
  const [selectedObservation, setSelectedObservation] =
    useState<Observation | null>(null);
  const [beforeSelection, setBeforeSelection] = useState<Evaluation | null>(
      null,
    ),
    [afterSelection, setAfterSelection] = useState<Evaluation | null>(null);
  const selectionRequests = useRef<Record<string, number>>({});
  const [datasetsLoaded, setDatasetsLoaded] = useState(false);
  const [datasets, setDatasets] = useState<Dataset[]>([]),
    [datasetId, setDatasetId] = useState("");
  const [observations, setObservations] = useState<Observation[]>([]),
    [observationId, setObservationId] = useState("");
  const [policies, setPolicies] = useState<Policy[]>([]),
    [policy, setPolicy] = useState<Policy | null>(null),
    [history, setHistory] = useState<Policy[]>([]);
  const [evaluations, setEvaluations] = useState<Evaluation[]>([]),
    [evaluation, setEvaluation] = useState<Evaluation | null>(null),
    [comparisons, setComparisons] = useState<Comparison[]>([]),
    [comparison, setComparison] = useState<Comparison | null>(null);
  const [name, setName] = useState(""),
    [groups, setGroups] = useState<string[]>([]),
    [manual, setManual] = useState<Spec["manualLabels"]>([]),
    [manualObject, setManualObject] = useState(""),
    [manualLabel, setManualLabel] = useState(""),
    [reason, setReason] = useState("");
  const [beforeId, setBeforeId] = useState(""),
    [afterId, setAfterId] = useState(""),
    [kind, setKind] = useState(""),
    [search, setSearch] = useState(""),
    [assets, setAssets] = useState<Page<AssetLabels> | null>(null),
    [asset, setAsset] = useState<AssetLabels | null>(null),
    [offset, setOffset] = useState(0);
  const [receipt, setReceipt] = useState<Receipt | null>(null),
    [pending, setPending] = useState<Intent | null>(null),
    [rejected, setRejected] = useState(false);
  const policyRequest = useRef(0),
    assetRequest = useRef(0);
  const alive = useRef(true),
    locked = useRef(false),
    scopeVersion = useRef(0),
    controllers = useRef(new Set<AbortController>());
  const storageKey = "adtr.labels.intent.v1:" + profile.ID + ":" + domainId;
  const fresh = () => {
    const c = new AbortController();
    controllers.current.add(c);
    return c;
  };
  const clear = () => {
    setDatasets([]);
    setObservations([]);
    setPolicies([]);
    setPolicy(null);
    setHistory([]);
    setEvaluations([]);
    setEvaluation(null);
    setComparisons([]);
    setComparison(null);
    setAssets(null);
    setAsset(null);
    setReceipt(null);
  };
  const fail = (e: unknown) => {
    if (!alive.current) return;
    if (
      e instanceof ApiError &&
      (e.status === 401 ||
        e.status === 403 ||
        e.code === "password_change_required")
    ) {
      scopeVersion.current++;
      controllers.current.forEach((c) => c.abort());
      setDenied(true);
      clear();
      setError("授权已失效，数据已清除；重新核对权限后才能继续。");
      sessionChanged();
      return;
    }
    setError(
      e instanceof ApiError
        ? ({
            revision_conflict:
              "修订冲突：请重新加载当前策略后编辑；没有覆盖他人修改。",
            observation_incomplete: "观测不完整，不能生成资产变化告警。",
            observations_incompatible: "观测不兼容，未生成告警。",
            not_found: "未找到记录；不能认定写入失败。",
            operation_not_found:
              "服务端尚无此操作回执；可以使用相同操作 ID 和原始请求安全重试。",
            network: "网络结果不确定；请恢复原操作回执，不要创建重复操作。",
          }[e.code] ?? e.code)
        : "请求失败，请重试。",
    );
  };
  useEffect(() => {
    alive.current = true;
    const invalidate = () => {
      alive.current = false;
      scopeVersion.current++;
      controllers.current.forEach((c) => c.abort());
      setDenied(true);
      clear();
      sessionChanged();
    };
    const notice = listenForSessionInvalidation(invalidate);
    return () => {
      alive.current = false;
      scopeVersion.current++;
      controllers.current.forEach((c) => c.abort());
      notice.close();
    };
  }, []);
  const read = <T,>(
    route: string,
    extra: Record<string, string | number> = {},
    c = fresh(),
  ) =>
    api.read<T>(
      route,
      { domainId, ...(route === "datasets" ? {} : { datasetId }), ...extra },
      profile,
      c.signal,
    );
  // One bounded page per list; never retain the full domain's evaluation payloads.
  async function windowPage<T>(
    route: string,
    c: AbortController,
    extra: Record<string, string | number> = {},
  ): Promise<Page<T>> {
    const offset =
      typeof extra.offset === "number" ? extra.offset : (windows[route] ?? 0);
    for (const limit of [20, 5, 1]) {
      try {
        return await read<Page<T>>(route, { ...extra, offset, limit }, c);
      } catch (e) {
        if (
          c.signal.aborted ||
          !(e instanceof ApiError) ||
          e.code !== "simulation_page_too_large" ||
          limit === 1
        )
          throw e;
      }
    }
    throw new ApiError("simulation_limit_exceeded");
  }
  function rememberPage<T>(route: string, p: Page<T>) {
    setPageInfo((v) => ({
      ...v,
      [route]: {
        offset: p.offset,
        limit: p.limit,
        count: p.count,
        shown: p.items.length,
      },
    }));
  }
  function pager(route: string, label: string) {
    const p = pageInfo[route];
    return (
      <nav aria-label={label + "分页"}>
        <span>
          {p
            ? `当前窗口 ${p.offset + 1}–${p.offset + p.shown} / ${p.count}`
            : "读取分页中…"}
        </span>
        <button
          disabled={!p || p.offset === 0 || busy}
          onClick={() =>
            setWindows((v) => ({
              ...v,
              [route]: Math.max(0, p.offset - p.limit),
            }))
          }
        >
          上一页{label}
        </button>
        <button
          disabled={
            !p || p.shown === 0 || p.offset + p.shown >= p.count || busy
          }
          onClick={() =>
            setWindows((v) => ({ ...v, [route]: p.offset + p.shown }))
          }
        >
          下一页{label}
        </button>
      </nav>
    );
  }
  useEffect(() => {
    if (!permissions.read || denied) return;
    const c = fresh();
    let live = true;
    setDatasetsLoaded(false);
    windowPage<Dataset>("datasets", c)
      .then((p) => {
        if (live && alive.current) {
          setDatasets(p.items);
          setDatasetsLoaded(true);
          rememberPage("datasets", p);
        }
      })
      .catch((e) => {
        if (live && !c.signal.aborted) fail(e);
      });
    return () => {
      live = false;
      c.abort();
    };
  }, [refresh, denied, windows.datasets]);
  useEffect(() => {
    clearDataset();
    setWindows((v) => ({ datasets: v.datasets ?? 0 }));
    setPageInfo((v): typeof v => (v.datasets ? { datasets: v.datasets } : {}));
  }, [datasetId, refresh]);
  useEffect(() => {
    if (!datasetId || denied || !permissions.read) return;
    const c = fresh();
    let live = true;
    const load = <T,>(route: string, set: (v: T[]) => void) => {
      set([]);
      windowPage<T>(route, c)
        .then((p) => {
          if (live && alive.current) {
            set(p.items);
            rememberPage(route, p);
          }
        })
        .catch((e) => {
          if (live && !c.signal.aborted) fail(e);
        });
    };
    load<Observation>("observations", setObservations);
    load<Policy>("policies", setPolicies);
    load<Evaluation>("evaluations", setEvaluations);
    load<Comparison>("comparisons", setComparisons);
    return () => {
      live = false;
      c.abort();
    };
  }, [
    datasetId,
    refresh,
    denied,
    windows.observations,
    windows.policies,
    windows.evaluations,
    windows.comparisons,
  ]);
  useEffect(() => {
    setHistory([]);
    if (!policy || denied) return;
    const c = fresh();
    let live = true;
    windowPage<Policy>("history", c, { policyId: policy.id })
      .then((p) => {
        if (live && alive.current) {
          setHistory(p.items);
          rememberPage("history", p);
        }
      })
      .catch((e) => {
        if (live && !c.signal.aborted) fail(e);
      });
    return () => {
      live = false;
      c.abort();
    };
  }, [policy, windows.history, denied]);
  function clearDataset() {
    scopeVersion.current++;
    setSelectedPolicyId("");
    setPolicyLoading(false);
    setSelectedObservation(null);
    setObservationId("");
    setBeforeSelection(null);
    setAfterSelection(null);
    policyRequest.current++;
    assetRequest.current++;
    setObservations([]);
    setPolicies([]);
    setPolicy(null);
    setHistory([]);
    setEvaluations([]);
    setEvaluation(null);
    setComparisons([]);
    setComparison(null);
    setAssets(null);
    setAsset(null);
    setBeforeId("");
    setAfterId("");
  }
  useEffect(() => {
    assetRequest.current++;
    setAssets(null);
    setAsset(null);
    if (!evaluation || denied) return;
    let live = true;
    const c = fresh();
    windowPage<AssetLabels>("assets", c, {
      evaluationId: evaluation.id,
      kind,
      name: search,
      offset,
    })
      .then((v) => {
        if (live && alive.current) setAssets(v);
      })
      .catch((e) => {
        if (live && !c.signal.aborted) fail(e);
      });
    return () => {
      live = false;
      c.abort();
    };
  }, [evaluation, kind, search, offset, denied]);
  useEffect(() => {
    try {
      const raw = localStorage.getItem(storageKey);
      if (!raw) return;
      const x = JSON.parse(raw) as Intent;
      if (
        writes.includes(x.kind) &&
        x.input?.domainId === domainId &&
        typeof x.input.datasetId === "string" &&
        typeof x.input.operationId === "string" &&
        typeof x.input.reason === "string"
      ) {
        setPending(x);
        setDatasetId(x.input.datasetId);
      } else setError("恢复记录格式无效，未提交任何写入。");
    } catch {
      setError("恢复存储不可用；写操作保持关闭以避免无法恢复的重复提交。");
      setDenied(true);
    }
  }, []);
  async function selectPolicy(id: string) {
    const request = ++policyRequest.current,
      token = scopeVersion.current,
      c = fresh();
    setSelectedPolicyId(id);
    setPolicyLoading(!!id);
    setPolicy(null);
    setName("");
    setGroups([]);
    setManual([]);
    setHistory([]);
    setWindows((v) => ({ ...v, history: 0 }));
    if (!id) {
      setName("");
      setGroups([]);
      setManual([]);
      return;
    }
    try {
      const p = await read<Policy>("policy", { policyId: id }, c);
      if (
        !alive.current ||
        token !== scopeVersion.current ||
        request !== policyRequest.current
      )
        return;
      setPolicy(p);
      setName(p.spec.name);
      setGroups(p.spec.sensitiveGroupIds);
      setManual(p.spec.manualLabels);
      setPolicyLoading(false);
    } catch (e) {
      if (!c.signal.aborted) fail(e);
    }
  }
  async function chooseDetail(
    type: "observation" | "evaluation" | "comparison",
    id: string,
    target = "main",
  ) {
    const key = type + target,
      request = (selectionRequests.current[key] ?? 0) + 1;
    selectionRequests.current[key] = request;
    const token = scopeVersion.current,
      c = fresh();
    if (type === "observation") {
      setObservationId(id);
      setSelectedObservation(null);
    } else if (type === "comparison") setComparison(null);
    else if (target === "before") {
      setBeforeId(id);
      setBeforeSelection(null);
    } else if (target === "after") {
      setAfterId(id);
      setAfterSelection(null);
    } else {
      assetRequest.current++;
      setEvaluation(null);
      setAssets(null);
      setAsset(null);
      setOffset(0);
    }
    if (!id) return;
    try {
      const v = await read<Observation | Evaluation | Comparison>(
        type,
        { [type + "Id"]: id },
        c,
      );
      if (
        !alive.current ||
        token !== scopeVersion.current ||
        selectionRequests.current[key] !== request
      )
        return;
      if (type === "observation") setSelectedObservation(v as Observation);
      else if (type === "comparison") setComparison(v as Comparison);
      else if (target === "before") setBeforeSelection(v as Evaluation);
      else if (target === "after") setAfterSelection(v as Evaluation);
      else setEvaluation(v as Evaluation);
    } catch (e) {
      if (!c.signal.aborted) fail(e);
    }
  }
  async function recover(intent: Intent) {
    if (locked.current || denied) return;
    locked.current = true;
    setBusy(true);
    const c = fresh(),
      token = scopeVersion.current;
    try {
      const r = await api.read<Receipt>(
        "operation",
        {
          domainId,
          datasetId: intent.input.datasetId,
          operationId: intent.input.operationId,
        },
        profile,
        c.signal,
      );
      if (!alive.current || token !== scopeVersion.current) return;
      validateReceiptIntent(intent.kind, intent.input, r);
      setReceipt(r);
      setPending(null);
      localStorage.removeItem(storageKey);
      setRefresh((x) => x + 1);
      setError("");
    } catch (e) {
      if (!c.signal.aborted) fail(e);
    } finally {
      locked.current = false;
      if (alive.current) setBusy(false);
    }
  }
  async function submit(action: WriteKind, retryIntent?: Intent) {
    if (
      locked.current ||
      policyLoading ||
      !permissions.manage ||
      denied ||
      (!retryIntent && pending)
    )
      return;
    locked.current = true;
    setBusy(true);
    setError("");
    setRejected(false);
    const c = fresh(),
      token = scopeVersion.current;
    try {
      const intent = retryIntent ?? {
        kind: action,
        input: {
          domainId,
          datasetId,
          operationId: crypto.randomUUID(),
          reason,
          ...(action === "compare"
            ? { beforeId, afterId }
            : {
                policyId: policy?.id ?? crypto.randomUUID(),
                expectedRevision: policy?.revision ?? 0,
                ...(["create", "update"].includes(action)
                  ? {
                      spec: {
                        name,
                        sensitiveGroupIds: groups,
                        manualLabels: manual,
                      },
                    }
                  : {}),
                ...(action === "evaluate" ? { observationId } : {}),
              }),
        },
      };
      localStorage.setItem(storageKey, JSON.stringify(intent));
      setPending(intent);
      const r = await api.write(intent.kind, intent.input, profile, c.signal);
      if (!alive.current || token !== scopeVersion.current) return;
      setReceipt(r);
      setPending(null);
      localStorage.removeItem(storageKey);
      setRefresh((x) => x + 1);
    } catch (e) {
      if (!c.signal.aborted) {
        if (e instanceof ApiError && [400, 409, 422].includes(e.status)) {
          setRejected(true);
        }
        fail(e);
      }
    } finally {
      locked.current = false;
      if (alive.current) setBusy(false);
    }
  }
  async function assetDetail(id: string) {
    const request = ++assetRequest.current;
    setAsset(null);
    const c = fresh(),
      token = scopeVersion.current;
    try {
      const a = await read<AssetLabels>(
        "asset",
        { evaluationId: evaluation!.id, objectId: id },
        c,
      );
      if (
        alive.current &&
        token === scopeVersion.current &&
        request === assetRequest.current
      )
        setAsset(a);
    } catch (e) {
      if (!c.signal.aborted) fail(e);
    }
  }
  const observation = selectedObservation;
  const meta = (id: string) =>
    observation?.assets.find((a) => a.objectId === id);
  const before = beforeSelection,
    after = afterSelection;
  const coverage = (e: Evaluation) =>
    JSON.stringify(
      e.observation.snapshot.nodes
        .map((n) => [n.id, n.kind])
        .sort((a, b) => a[0].localeCompare(b[0])),
    );
  const compatible =
    !!before &&
    !!after &&
    before.id !== after.id &&
    before.policy.id === after.policy.id &&
    before.policy.revision === after.policy.revision &&
    before.policy.hash === after.policy.hash &&
    before.dataset.hash === after.dataset.hash &&
    before.result.completeWithinSnapshot &&
    after.result.completeWithinSnapshot &&
    before.observation.sequence < after.observation.sequence &&
    coverage(before) === coverage(after);
  const canWrite =
    permissions.manage &&
    !policyLoading &&
    !busy &&
    !pending &&
    !!reason.trim() &&
    !!datasetId;
  if (denied || !permissions.read)
    return (
      <section>
        <p role="alert">{error || "无读取权限。"}</p>
      </section>
    );
  return (
    <section aria-label="模拟敏感资产工作区">
      <h3>SIMULATED · 固定观测与敏感策略</h3>
      {error && <p role="alert">{error}</p>}
      {busy && <p role="status">操作处理中；离开页面不会撤销服务端事务。</p>}
      <label>
        合成数据集
        <select
          aria-label="合成数据集"
          value={datasetId}
          disabled={busy || !!pending}
          onChange={(e) => {
            scopeVersion.current++;
            setDatasetId(e.target.value);
            setReceipt(null);
            setObservationId("");
          }}
        >
          <option value="">请选择</option>
          {datasetId && !datasets.some((d) => d.id === datasetId) && (
            <option value={datasetId}>{datasetId}（已选择，另一页）</option>
          )}
          {datasets.map((d) => (
            <option key={d.id} value={d.id}>
              {d.id}
            </option>
          ))}
        </select>
      </label>
      {pager("datasets", "数据集")}
      {!datasetsLoaded && !error && <p role="status">正在读取合成数据集…</p>}
      {datasetsLoaded && datasets.length === 0 && (
        <p>没有可用合成数据集；需隔离测试环境显式安装可信 fixture。</p>
      )}
      {datasets
        .filter((d) => d.id === datasetId)
        .map((d) => (
          <p key={d.id}>
            契约 {d.contract} · 生产者 {d.producer} · hash {d.hash} ·{" "}
            {d.observationCount} 个观测
          </p>
        ))}
      {pending && (
        <section aria-label="待恢复操作">
          <p>
            待确认操作 {pending.input.operationId}（{pending.kind}
            ）；这是恢复标识，不是成功证明。
          </p>
          <button disabled={busy} onClick={() => void recover(pending)}>
            恢复原操作回执
          </button>
          <button
            disabled={busy || !permissions.manage}
            onClick={() => void submit(pending.kind, pending)}
          >
            使用相同操作 ID 重试
          </button>
          {rejected && (
            <button
              disabled={busy}
              onClick={() => {
                localStorage.removeItem(storageKey);
                setPending(null);
                setRejected(false);
                setRefresh((x) => x + 1);
              }}
            >
              放弃已被拒绝的请求并重载
            </button>
          )}
        </section>
      )}
      {receipt && (
        <section aria-label="历史操作回执">
          <h4>历史操作回执</h4>
          <p>
            {receipt.operationId} · {receipt.action} · {receipt.at}
          </p>
          <p>
            回执是历史提交事实；下方列表重新读取当前状态，不将历史修订冒充当前策略。
          </p>
        </section>
      )}
      <button disabled={busy} onClick={() => setRefresh((x) => x + 1)}>
        重新加载当前状态
      </button>
      {datasetId && (
        <>
          <label>
            固定观测
            <select
              aria-label="固定观测"
              value={observationId}
              onChange={(e) => void chooseDetail("observation", e.target.value)}
            >
              <option value="">请选择</option>
              {selectedObservation &&
                !observations.some((o) => o.id === observationId) && (
                  <option value={observationId}>
                    {observationId}（已固定，另一页）
                  </option>
                )}
              {observations.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.id} · 序号 {o.sequence} · {o.complete ? "完整" : "不完整"}
                </option>
              ))}
            </select>
          </label>
          {pager("observations", "观测")}
          {observation && (
            <p>
              观测 hash {observation.hash}；
              {observation.complete
                ? "fixture 声明完整"
                : "不完整：缺失不能解释为标签移除"}
            </p>
          )}
          <label>
            当前策略
            <select
              aria-label="当前策略"
              disabled={busy}
              value={selectedPolicyId}
              onChange={(e) => void selectPolicy(e.target.value)}
            >
              <option value="">新建策略</option>
              {selectedPolicyId &&
                !policies.some((p) => p.id === selectedPolicyId) && (
                  <option value={selectedPolicyId}>
                    {selectedPolicyId}（所选策略）
                  </option>
                )}
              {policies.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.spec.name} · r{p.revision} · {p.status}
                </option>
              ))}
            </select>
          </label>
          {pager("policies", "策略")}
          {policyLoading && (
            <p role="status">正在加载所选策略；编辑与写入暂不可用。</p>
          )}
          <fieldset
            disabled={!permissions.manage || policyLoading || busy || !!pending}
          >
            <legend>策略定义（手工组标签不会传播至成员）</legend>
            <label>
              策略名称
              <input
                aria-label="策略名称"
                maxLength={256}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <fieldset>
              <legend>敏感组</legend>
              {observation?.snapshot.nodes
                .filter((n) => n.kind === "group")
                .map((n) => (
                  <label key={n.id}>
                    <input
                      type="checkbox"
                      checked={groups.includes(n.id)}
                      onChange={(e) =>
                        setGroups((g) =>
                          e.target.checked
                            ? [...g, n.id]
                            : g.filter((x) => x !== n.id),
                        )
                      }
                    />
                    {meta(n.id)?.name ?? n.id} ({n.id})
                  </label>
                ))}
            </fieldset>
            <label>
              手工标签对象
              <select
                aria-label="手工标签对象"
                value={manualObject}
                onChange={(e) => setManualObject(e.target.value)}
              >
                <option value="">请选择</option>
                {observation?.snapshot.nodes.map((n) => (
                  <option key={n.id} value={n.id}>
                    {meta(n.id)?.name ?? n.id} ({n.kind} · {n.id})
                  </option>
                ))}
              </select>
            </label>
            <label>
              手工标签
              <input
                aria-label="手工标签"
                value={manualLabel}
                maxLength={128}
                onChange={(e) => setManualLabel(e.target.value)}
              />
            </label>
            <button
              disabled={
                !manualObject || !manualLabel.trim() || manual.length >= 256
              }
              onClick={() => {
                setManual((m) =>
                  m.some(
                    (x) =>
                      x.objectId === manualObject && x.label === manualLabel,
                  )
                    ? m
                    : [...m, { objectId: manualObject, label: manualLabel }],
                );
                setManualLabel("");
              }}
            >
              添加手工标签
            </button>
            <ul>
              {manual.map((m, i) => (
                <li key={m.objectId + ":" + m.label}>
                  {m.objectId} · {m.label}
                  <button
                    onClick={() =>
                      setManual((v) => v.filter((_, n) => n !== i))
                    }
                  >
                    移除 {m.label}
                  </button>
                </li>
              ))}
            </ul>
            <label>
              操作原因
              <input
                aria-label="操作原因"
                value={reason}
                maxLength={512}
                onChange={(e) => setReason(e.target.value)}
              />
            </label>
          </fieldset>
          <button
            disabled={
              !canWrite ||
              !name.trim() ||
              !observation ||
              policy?.status === "archived"
            }
            onClick={() => void submit(policy ? "update" : "create")}
          >
            {policy ? "保存新修订" : "创建策略"}
          </button>
          {policy && (
            <>
              <p>
                当前 r{policy.revision} · {policy.status} · hash {policy.hash}
                。保存使用此修订作 CAS 前提。
              </p>
              <button
                disabled={!canWrite}
                onClick={() =>
                  void submit(
                    policy.status === "archived" ? "restore" : "archive",
                  )
                }
              >
                {policy.status === "archived" ? "还原策略" : "归档策略"}
              </button>
              <button
                disabled={
                  !canWrite || !observation || policy.status !== "active"
                }
                onClick={() => void submit("evaluate")}
              >
                评估固定修订
              </button>
            </>
          )}
          <section aria-label="策略编辑历史">
            <h4>策略编辑历史（不是资产变化告警）</h4>
            {pager("history", "修订历史")}
            {history.map((p) => (
              <p key={p.revision}>
                r{p.revision} · {p.status} · {p.actor} · {p.reason} · {p.at}
              </p>
            ))}
          </section>
          <label>
            已保存评估
            <select
              aria-label="已保存评估"
              disabled={busy}
              value={evaluation?.id ?? ""}
              onChange={(e) => void chooseDetail("evaluation", e.target.value)}
            >
              <option value="">请选择</option>
              {evaluation &&
                !evaluations.some((e) => e.id === evaluation.id) && (
                  <option value={evaluation.id}>
                    {evaluation.id}（已固定，另一页）
                  </option>
                )}
              {evaluations.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.id} · {e.observation.id} · r{e.policy.revision}
                </option>
              ))}
            </select>
          </label>
          {pager("evaluations", "评估")}
          {evaluation && (
            <>
              <p>
                固定策略 {evaluation.policy.id} r{evaluation.policy.revision} ·
                观测 {evaluation.observation.id} · hash {evaluation.hash}
              </p>
              <p>
                {evaluation.result.completeWithinSnapshot
                  ? "在固定观测范围内完整"
                  : "不完整／未知：不得推断缺席对象或标签已移除"}
              </p>
              <label>
                资产类型
                <select
                  aria-label="资产类型"
                  value={kind}
                  onChange={(e) => {
                    setKind(e.target.value);
                    setOffset(0);
                  }}
                >
                  <option value="">全部</option>
                  {["user", "group", "computer"].map((k) => (
                    <option key={k}>{k}</option>
                  ))}
                </select>
              </label>
              <label>
                资产名称
                <input
                  aria-label="资产名称"
                  value={search}
                  maxLength={256}
                  onChange={(e) => {
                    setSearch(e.target.value);
                    setOffset(0);
                  }}
                />
              </label>
              {assets ? (
                <>
                  <p>匹配资产 {assets.count} 个</p>
                  {assets.items.map((a) => (
                    <button
                      key={a.objectId}
                      disabled={!a.isClick}
                      onClick={() => void assetDetail(a.objectId)}
                    >
                      {a.name} ({a.kind} · {a.objectId}) · {a.state}
                    </button>
                  ))}
                  <button
                    disabled={offset === 0}
                    onClick={() =>
                      setOffset((n) => Math.max(0, n - assets.limit))
                    }
                  >
                    上一页资产
                  </button>
                  <button
                    disabled={offset + assets.items.length >= assets.count}
                    onClick={() => setOffset((n) => n + assets.items.length)}
                  >
                    下一页资产
                  </button>
                </>
              ) : (
                <p role="status">读取固定评估资产…</p>
              )}
            </>
          )}
          {asset && (
            <section aria-label="资产标签详情">
              <h4>
                {asset.name} · {asset.objectId}
              </h4>
              <p>{asset.description ?? "描述不可用"}</p>
              <p>
                蜜罐状态：不可用（{asset.honeypotAvailability}）；{asset.reason}
              </p>
              <p>isClick 只表示可打开详情，不表示 AD 权限。</p>
              {asset.labels.map((p, i) => {
                const group = evaluation?.observation.assets.find(
                  (a) => a.objectId === p.groupId,
                );
                return (
                  <p key={i}>
                    {p.label} · {p.source} ·{" "}
                    {group?.name ?? p.groupId ?? "手工标签"} ·{" "}
                    {group?.description ?? "描述不可用"} · 距离 {p.distance}
                  </p>
                );
              })}
              {asset.labels.length === 0 && (
                <p>没有已知标签；状态 {asset.state}，未知不等于非敏感。</p>
              )}
            </section>
          )}
          <section aria-label="观测比较">
            <h4>同一策略修订的兼容观测比较</h4>
            {(["before", "after"] as const).map((side) => (
              <label key={side}>
                {side === "before" ? "前置评估" : "后置评估"}
                <select
                  aria-label={side === "before" ? "前置评估" : "后置评估"}
                  value={side === "before" ? beforeId : afterId}
                  onChange={(e) =>
                    void chooseDetail("evaluation", e.target.value, side)
                  }
                >
                  <option value="">请选择</option>
                  {(side === "before" ? beforeSelection : afterSelection) &&
                    !evaluations.some(
                      (v) => v.id === (side === "before" ? beforeId : afterId),
                    ) && (
                      <option value={side === "before" ? beforeId : afterId}>
                        {side === "before" ? beforeId : afterId}
                        （已固定，另一页）
                      </option>
                    )}
                  {evaluations.map((v) => (
                    <option key={v.id} value={v.id}>
                      {v.id} · {v.observation.sequence} · r{v.policy.revision}
                    </option>
                  ))}
                </select>
              </label>
            ))}
            <p>
              必须同策略修订、同覆盖身份且完整，后置观测序号更大。服务端重新验证兼容性。
            </p>
            <button
              disabled={!canWrite || !compatible}
              onClick={() => void submit("compare")}
            >
              比较并生成站内告警
            </button>
          </section>
          <label>
            已保存观测比较
            <select
              aria-label="已保存观测比较"
              value={comparison?.id ?? ""}
              onChange={(e) => void chooseDetail("comparison", e.target.value)}
            >
              <option value="">请选择</option>
              {comparison &&
                !comparisons.some((c) => c.id === comparison.id) && (
                  <option value={comparison.id}>
                    {comparison.id}（已固定，另一页）
                  </option>
                )}
              {comparisons.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.id} · {c.alerts.length} 条站内告警
                </option>
              ))}
            </select>
          </label>
          {pager("comparisons", "观测比较")}
          {comparison && (
            <section aria-label="资产变化告警">
              <h4>SIMULATED · 站内资产变化告警</h4>
              <p>
                {comparison.beforeId} → {comparison.afterId} · hash{" "}
                {comparison.hash}
              </p>
              {comparison.alerts.length === 0 ? (
                <p>兼容比较已完成，变化为零，未生成告警。</p>
              ) : (
                comparison.alerts.map((a) => (
                  <article key={a.id}>
                    <h5>
                      {a.objectId} · {a.id}
                    </h5>
                    <p>交付：{a.delivery}</p>
                    {a.changes.map((c, i) => (
                      <p key={i}>
                        {c.action} · {c.provenance.label} ·{" "}
                        {c.provenance.source} · {c.provenance.groupId ?? "手工"}{" "}
                        · 距离 {c.provenance.distance}
                      </p>
                    ))}
                  </article>
                ))
              )}
              <p>移除一个来源不代表资产已不敏感；需查看后置评估全部来源。</p>
            </section>
          )}
        </>
      )}
    </section>
  );
}
