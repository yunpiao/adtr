import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import { stateLabels, terminal, type Task } from "./task-api";
import {
  simulationDNSIPAPI as api,
  dnsIPColumns,
  dnsIPContract,
  validFilter,
  validManifest,
  verifyReceipt,
  verifyTask,
  saveDNSIPFile,
  ambiguousDNSIPWrite,
  type Observation,
  type Query,
  type DNSIPPage,
  type Receipt,
  type Write,
  type Action,
  type Payload,
} from "./simulation-dns-ip-api";
type Props = { profile: Profile; sessionChanged: () => void };
type Permissions = { read: boolean; manage: boolean };
const prefix = "/api/simulation/dns-ip";
const readPaths = [
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
  ...["observations", "records", "task", "receipt", "download"].map(
    (x) => `GET ${prefix}/${x}`,
  ),
];
const writePaths = ["export", "cancel"].map((x) => `POST ${prefix}/${x}`);
export default function SimulationDNSIPWorkspace(props: Props) {
  return <Entry key={JSON.stringify(props.profile)} {...props} />;
}
function Entry({ profile, sessionChanged }: Props) {
  const [gate, setGate] = useState<Permissions | null>(null),
    [domain, setDomain] = useState(""),
    [error, setError] = useState(""),
    [retry, setRetry] = useState(0);
  useEffect(() => {
    let live = true;
    const c = new AbortController(),
      paths = [...readPaths, ...writePaths];
    setGate(null);
    setDomain("");
    setError("");
    Promise.all([
      accessRequest<{
        menu: { mark: string; auth?: { readable?: boolean } }[];
      }>("/menu", c.signal),
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
          !Array.isArray(m.menu) ||
          !Array.isArray(r.results) ||
          r.results.length !== paths.length ||
          r.results.some((x) => typeof x !== "boolean")
        )
          throw new ApiError("invalid_response");
        const read =
          m.menu.some(
            (x) => x.mark === "simulation_dns_ip" && x.auth?.readable === true,
          ) && r.results.slice(0, readPaths.length).every(Boolean);
        setGate({
          read,
          manage: read && r.results.slice(readPaths.length).every(Boolean),
        });
      })
      .catch((e) => {
        if (!live) return;
        setGate({ read: false, manage: false });
        setError("未能确认 DNS/IP 与域选择权限。");
        if (
          e instanceof ApiError &&
          (e.status === 401 || e.code === "password_change_required")
        )
          sessionChanged();
      });
    const hints = listenForSessionInvalidation(() => {
      live = false;
      c.abort();
      setGate(null);
      setDomain("");
      sessionChanged();
    });
    return () => {
      live = false;
      c.abort();
      hints.close();
    };
  }, [profile, sessionChanged, retry]);
  return (
    <section aria-label="模拟 DNS/IP 入口">
      <h2>SIMULATED · DNS/IP 固定观测与 XLSX</h2>
      <p>
        仅合成固定观测；不查询 DNS、不连接
        AD，不代表实时设备同步或生产容量。当前没有日期筛选。
      </p>
      {error && <p role="alert">{error}</p>}
      {!gate ? (
        <p role="status">核对权限中…</p>
      ) : !gate.read ? (
        <>
          <p>没有模拟 DNS/IP 与域选择读取权限。</p>
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
          <SimulationDNSIPDomainWorkspace
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
export type DNSIPWorkspaceProps = Props & {
  domainId: string;
  permissions?: Permissions;
};
export function SimulationDNSIPDomainWorkspace(props: DNSIPWorkspaceProps) {
  return (
    <Workspace
      key={JSON.stringify([props.profile, props.domainId, props.permissions])}
      {...props}
    />
  );
}
type Pending = { action: Action; input: Write };
function message(e: unknown) {
  if (e instanceof ApiError) {
    if (e.status === 409) return "版本或任务状态冲突，请重新读取后再操作。";
    if (e.status === 422) return "超过合成记录或导出预算，未发布工作簿。";
    if (e.status === 404) return "当前授权下未找到该操作或任务。";
    if (e.status === 403) return "当前权限不足。";
    if (e.status === 400) return "输入无效，请检查观测及筛选条件。";
    if (e.code === "invalid_response")
      return "服务器结果与当前观测、操作或文件证据不一致，已拒绝使用。";
  }
  return "请求结果未确认，请使用原操作 ID 恢复，不要重复创建。";
}
function Workspace({
  profile,
  sessionChanged,
  domainId,
  permissions = { read: false, manage: false },
}: DNSIPWorkspaceProps) {
  const [observations, setObservations] = useState<Observation[]>([]),
    [selected, setSelected] = useState(""),
    [keyword, setKeyword] = useState(""),
    [family, setFamily] = useState(""),
    [query, setQuery] = useState<Query | null>(null),
    [page, setPage] = useState<DNSIPPage | null>(null),
    [reading, setReading] = useState(false),
    [reload, setReload] = useState(0);
  const [task, setTask] = useState<Task | null>(null),
    [pin, setPin] = useState<Payload | null>(null),
    [receipt, setReceipt] = useState<Receipt | null>(null),
    [pending, setPending] = useState<Pending | null>(null),
    [restoreID, setRestoreID] = useState(""),
    [busy, setBusy] = useState(false),
    [denied, setDenied] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const live = useRef(true),
    controllers = useRef(new Set<AbortController>()),
    writeLock = useRef(false),
    taskRead = useRef(0);
  const begin = () => {
    const c = new AbortController();
    controllers.current.add(c);
    return c;
  };
  const end = (c: AbortController) => controllers.current.delete(c);
  const fail = (e: unknown) => {
    if (!live.current) return;
    setError(message(e));
    if (
      e instanceof ApiError &&
      (e.status === 401 || e.code === "password_change_required")
    ) {
      setDenied(true);
      setObservations([]);
      setPage(null);
      setTask(null);
      setReceipt(null);
      sessionChanged();
    } else if (e instanceof ApiError && e.status === 403) {
      setDenied(true);
      setPage(null);
      setTask(null);
      setReceipt(null);
    }
  };
  useLayoutEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
      controllers.current.forEach((c) => c.abort());
      controllers.current.clear();
    };
  }, []);
  useEffect(() => {
    const hints = listenForSessionInvalidation(() => {
      live.current = false;
      controllers.current.forEach((c) => c.abort());
      setDenied(true);
      setPage(null);
      setTask(null);
      setReceipt(null);
      sessionChanged();
    });
    return () => hints.close();
  }, [sessionChanged]);
  useEffect(() => {
    if (!permissions.read || denied) return;
    let current = true;
    const c = begin();
    setReading(true);
    api
      .observations(domainId, profile, c.signal)
      .then((v) => {
        if (!current || !live.current) return;
        setObservations(v);
        setSelected("");
        setQuery(null);
        setPage(null);
      })
      .catch((e) => {
        if (current && !c.signal.aborted) fail(e);
      })
      .finally(() => {
        end(c);
        if (current && live.current) setReading(false);
      });
    return () => {
      current = false;
      c.abort();
    };
  }, [domainId, profile, reload, permissions.read, denied]);
  useEffect(() => {
    if (!query || !permissions.read || denied) return;
    let current = true;
    const c = begin(),
      o = observations.find((x) => x.id === query.observationId);
    setPage(null);
    setReading(true);
    api
      .records(domainId, query, profile, c.signal, o)
      .then((v) => {
        if (current && live.current) setPage(v);
      })
      .catch((e) => {
        if (current && !c.signal.aborted) fail(e);
      })
      .finally(() => {
        end(c);
        if (current && live.current) setReading(false);
      });
    return () => {
      current = false;
      c.abort();
    };
  }, [query, domainId, profile, permissions.read, denied, observations]);
  // A cancel receipt binds a task, not its original export filter. Non-success
  // tasks can be followed without an export pin; successful races need a fresh
  // trusted source association before exposing any download affordance.
  const recoveredSuccessPin = async (
    t: Task,
    c: AbortController,
  ): Promise<Payload | null> => {
    if (t.state !== "succeeded") return null;
    if (!validManifest(t.result)) throw new ApiError("invalid_response");
    const payload = t.result.payload;
    const current = await api.observations(domainId, profile, c.signal);
    if (
      !current.some(
        (o) =>
          o.id === payload.observationId &&
          o.hash === payload.observationHash &&
          o.dnsDomain === payload.dnsDomain,
      )
    )
      throw new ApiError("invalid_response");
    return payload;
  };
  const refreshTask = async () => {
    if (!task || denied || writeLock.current) return;
    const generation = ++taskRead.current;
    const c = begin();
    try {
      const v = await api.task(
        domainId,
        task.taskUUID,
        profile,
        c.signal,
        pin ?? undefined,
      );
      verifyTask(v, domainId, task.taskUUID, pin ?? undefined);
      const nextPin = pin ?? (await recoveredSuccessPin(v, c));
      if (
        live.current &&
        !c.signal.aborted &&
        generation === taskRead.current
      ) {
        setTask(v);
        setPin(nextPin);
      }
    } catch (e) {
      if (!c.signal.aborted) fail(e);
    } finally {
      end(c);
    }
  };
  useEffect(() => {
    if (!task || terminal(task.state) || denied) return;
    const timer = setInterval(() => void refreshTask(), 2000);
    return () => clearInterval(timer);
  }, [task?.taskUUID, task?.state, pin, denied]);
  const accept = async (r: Receipt, c: AbortController, expected?: Pending) => {
    const generation = ++taskRead.current;
    verifyReceipt(r, profile, domainId, r.operationId, expected);
    let payload = task?.taskUUID === r.task?.taskUUID ? pin : null;
    if (r.action === "export") {
      const e = r.intent.export!,
        o = observations.find(
          (x) => x.id === e.observationId && x.hash === e.observationHash,
        );
      if (!o) throw new ApiError("invalid_response");
      payload = {
        observationId: e.observationId,
        observationHash: e.observationHash,
        dnsDomain: o.dnsDomain,
        keyword: e.keyword,
        family: e.family,
        contract: dnsIPContract,
      };
    }
    const initial = r.action === "export" ? r.submission!.task : r.task!;
    const t = await api.task(
      domainId,
      initial.taskUUID,
      profile,
      c.signal,
      payload ?? undefined,
    );
    if (!payload) payload = await recoveredSuccessPin(t, c);
    verifyTask(t, domainId, initial.taskUUID, payload ?? undefined);
    if (!live.current || c.signal.aborted || generation !== taskRead.current)
      return;
    setReceipt(r);
    setTask(t);
    setPin(payload);
    setPending(null);
    setRestoreID(r.operationId);
    setNotice(
      r.action === "export"
        ? "服务器已确认导出任务；实际文件需 Worker 成功后才可下载。"
        : "取消请求已由服务器确认，仍以任务最终状态为准。",
    );
  };
  const write = async (action: Action, input: Write) => {
    if (writeLock.current || busy || pending || denied || !permissions.manage)
      return;
    writeLock.current = true;
    taskRead.current++;
    setBusy(true);
    setError("");
    setNotice("");
    const operation = { action, input };
    setPending(operation);
    setRestoreID(input.operationId);
    const c = begin();
    try {
      const r = await api.write(action, input, profile, c.signal);
      await accept(r, c, operation);
    } catch (e) {
      if (live.current && !c.signal.aborted) {
        if (!ambiguousDNSIPWrite(e)) setPending(null);
        fail(e);
      }
    } finally {
      end(c);
      writeLock.current = false;
      if (live.current) setBusy(false);
    }
  };
  const recover = async () => {
    if (writeLock.current || busy || denied || !restoreID) return;
    writeLock.current = true;
    taskRead.current++;
    setBusy(true);
    setError("");
    const c = begin();
    let retried = false;
    try {
      let r: Receipt;
      try {
        r = await api.receipt(
          domainId,
          restoreID,
          profile,
          c.signal,
          pending ?? undefined,
        );
      } catch (e) {
        if (
          pending &&
          e instanceof ApiError &&
          e.status === 404 &&
          e.code === "operation_not_found"
        ) {
          retried = true;
          r = await api.write(pending.action, pending.input, profile, c.signal);
        } else throw e;
      }
      await accept(r, c, pending ?? undefined);
    } catch (e) {
      if (live.current && !c.signal.aborted) {
        if (retried && !ambiguousDNSIPWrite(e)) setPending(null);
        fail(e);
      }
    } finally {
      end(c);
      writeLock.current = false;
      if (live.current) setBusy(false);
    }
  };
  const download = async () => {
    if (!task || !pin || busy || denied) return;
    setBusy(true);
    setError("");
    const c = begin();
    try {
      const f = await api.download(domainId, task, profile, c.signal, pin);
      if (live.current && !c.signal.aborted) {
        saveDNSIPFile(f);
        setNotice("已核对实际字节数、SHA-256 和清单后下载 XLSX。");
      }
    } catch (e) {
      if (!c.signal.aborted) fail(e);
    } finally {
      end(c);
      if (live.current) setBusy(false);
    }
  };
  const locked = busy || !!pending || denied;
  const selectedObservation = observations.find((x) => x.id === selected);
  const draftChanged =
    !!query &&
    (query.observationId !== selected ||
      query.keyword !== keyword ||
      query.family !== family);
  return (
    <section aria-label="模拟 DNS/IP 工作区">
      <h3>SIMULATED · 固定观测</h3>
      <p>授权域 ID：{domainId}。DNS 域名称只是观测字段，不授予权限。</p>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {!permissions.read || denied ? (
        <p>当前不可读取 DNS/IP 数据，请重新核对权限。</p>
      ) : (
        <>
          <button
            disabled={locked || reading}
            onClick={() => setReload((x) => x + 1)}
          >
            刷新固定观测
          </button>
          <label>
            固定观测
            <select
              aria-label="固定观测"
              disabled={locked || reading}
              value={selected}
              onChange={(e) => {
                setSelected(e.target.value);
                setPage(null);
                setQuery(null);
                setKeyword("");
                setFamily("");
              }}
            >
              <option value="">请选择观测</option>
              {observations.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.id} · {o.dnsDomain} · {o.recordCount} 条
                </option>
              ))}
            </select>
          </label>
          {!reading && observations.length === 0 && (
            <p>没有可信合成观测，未填充演示记录。</p>
          )}
          {selectedObservation && (
            <p>
              来源 {selectedObservation.source} · 完整观测{" "}
              {selectedObservation.recordCount} 条 · SHA-256{" "}
              {selectedObservation.hash}
            </p>
          )}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!selectedObservation || locked) return;
              if (!validFilter(keyword, family)) {
                setError(
                  "关键词仅支持最多 253 个 ASCII 字符；不是正则表达式。",
                );
                return;
              }
              setError("");
              setQuery({
                observationId: selected,
                keyword,
                family,
                offset: 0,
                limit: 20,
              });
            }}
          >
            <label>
              DNS/IP 字面关键词
              <input
                aria-label="DNS/IP 字面关键词"
                maxLength={253}
                value={keyword}
                disabled={locked}
                onChange={(e) => setKeyword(e.target.value)}
              />
            </label>
            <label>
              地址类型
              <select
                aria-label="地址类型"
                value={family}
                disabled={locked}
                onChange={(e) => setFamily(e.target.value)}
              >
                <option value="">全部地址</option>
                <option value="ipv4">IPv4</option>
                <option value="ipv6">IPv6</option>
              </select>
            </label>
            <button disabled={locked || !selectedObservation}>应用筛选</button>
          </form>
          {reading && <p role="status">正在读取固定观测…</p>}
          {draftChanged && <p>筛选已修改，请先应用筛选后再导出。</p>}
          {page && (
            <>
              <p data-testid="dns-ip-page-summary">
                完整匹配 {page.total} 条，本页 {page.rows.length} 条；按 DNS /
                IP / ID 排序。
              </p>
              <div style={{ overflowX: "auto" }}>
                <table>
                  <caption>SIMULATED DNS/IP 固定记录</caption>
                  <thead>
                    <tr>
                      {dnsIPColumns.map((c) => (
                        <th key={c}>{c}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {page.rows.map((r) => (
                      <tr key={r.id}>
                        {[
                          r.id,
                          r.domain,
                          r.dnsName,
                          r.ip,
                          r.observation,
                          r.source,
                          r.version,
                          String(r.simulation),
                        ].map((v, i) => (
                          <td key={i}>{v}</td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {page.total === 0 && (
                <p>没有匹配记录；导出仍生成八列表头工作簿。</p>
              )}
              <button
                disabled={
                  locked || reading || page.query.offset === 0 || draftChanged
                }
                onClick={() =>
                  setQuery({
                    ...page.query,
                    offset: Math.max(0, page.query.offset - page.query.limit),
                  })
                }
              >
                上一页
              </button>
              <button
                disabled={locked || reading || page.exhausted || draftChanged}
                onClick={() =>
                  setQuery({
                    ...page.query,
                    offset: page.query.offset + page.query.limit,
                  })
                }
              >
                下一页
              </button>
              <button
                disabled={
                  locked || reading || draftChanged || !permissions.manage
                }
                onClick={() =>
                  void write("export", {
                    domainId,
                    operationId: crypto.randomUUID(),
                    observationId: page.observation.id,
                    observationHash: page.observation.hash,
                    keyword: page.query.keyword,
                    family: page.query.family,
                  })
                }
              >
                导出全部 {page.total} 条匹配记录 XLSX
              </button>
              <p>
                导出固定当前观测与筛选，包含全部匹配行，不仅当前页。没有日期区间或实时查询。
              </p>
            </>
          )}
          <label>
            恢复操作 ID
            <input
              aria-label="恢复操作 ID"
              value={restoreID}
              disabled={busy || !!pending}
              onChange={(e) => setRestoreID(e.target.value)}
            />
          </label>
          <button disabled={busy || !restoreID} onClick={() => void recover()}>
            恢复原操作回执
          </button>
          {pending && (
            <p role="status">
              操作结果待确认；保留原操作 ID {pending.input.operationId}
              ，不要另建重复任务。
            </p>
          )}
          {task && (
            <section aria-label="DNS/IP 导出任务">
              <h4>SIMULATED · {stateLabels[task.state]}</h4>
              <p>
                任务 {task.taskUUID} · 进度 {task.progress}% · 尝试{" "}
                {task.attempt}/{task.maxAttempts}
              </p>
              {pin && (
                <p>
                  固定观测 {pin.observationId} · SHA-256 {pin.observationHash} ·
                  关键词「{pin.keyword}」· 类型 {pin.family || "全部"}
                </p>
              )}
              <button disabled={busy} onClick={() => void refreshTask()}>
                刷新导出任务
              </button>
              <button
                disabled={
                  locked ||
                  !permissions.manage ||
                  terminal(task.state) ||
                  task.state === "cancel_requested"
                }
                onClick={() =>
                  void write("cancel", {
                    domainId,
                    operationId: crypto.randomUUID(),
                    taskId: task.taskUUID,
                  })
                }
              >
                取消导出任务
              </button>
              {task.error && <p>服务器错误：{task.error}</p>}
              {task.state === "succeeded" &&
              pin &&
              validManifest(task.result) ? (
                <>
                  <div role="region" aria-label="SIMULATED DNS/IP 导出结果">
                    <p>
                      完整工作簿：{task.result.rowCount} 条数据行 ·{" "}
                      {task.result.bytes} 字节
                    </p>
                    <p>文件 SHA-256：{task.result.sha256}</p>
                    <p>八列：{task.result.columns.join(" / ")}</p>
                  </div>
                  <button disabled={busy} onClick={() => void download()}>
                    下载已验证 XLSX
                  </button>
                </>
              ) : (
                <p>未成功的任务没有可下载文件；候选字节不代表已发布工作簿。</p>
              )}
            </section>
          )}
          {receipt && (
            <details>
              <summary>历史操作回执（不是当前任务状态）</summary>
              <pre>{JSON.stringify(receipt, null, 2)}</pre>
            </details>
          )}
        </>
      )}
    </section>
  );
}
