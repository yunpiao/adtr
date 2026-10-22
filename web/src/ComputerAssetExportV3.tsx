import { useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import { ErrorNotice, ProofFields, getProof, readValues } from "./access-common";
import { stateLabels, terminal } from "./task-api";
import { sessionError } from "./task-common";
import { directoryV2DisplayText as display } from "./directory-v2-json";
import {
  exportColumns, exportKind, exportOperations, requestFromQuery, sameExportRequest,
  computerAssetExportAPI, computerAssetExportError,
  type ExportColumn, type ExportDetail, type ExportReceipt, type ExportRequest,
} from "./computer-asset-export-v3-api";
import type { ComputerAssetsV3Query } from "./computer-assets-v3-api";

import { cancelUnknown, cancelUnavailable, cancelCapability, cancelStorageKey, inspectCancelMarker,
  observeComputerExportCancel, submitComputerExportCancel } from "./computer-export-cancel-intent";

type Props = { profile: Profile; query: ComputerAssetsV3Query; disabled: boolean; failure: (error: unknown) => void };
import { clearExportRecovery, readExportRecovery, writeExportRecovery, type ExportRecovery } from "./computer-asset-export-v3-recovery";
type Intent = { request: ExportRequest; key: string; attempted: boolean; taskID?: string };
const statusText = {
  pending: "正在等待持久化任务完成", ready: "完整文件已就绪",
  failed: "任务失败，没有可下载文件", cancelled: "服务器已确认取消，没有可下载文件",
  source_changed: "来源版本已变化，原文件不可下载，请重新选择来源创建导出",
  source_unavailable: "固定观测不可用，原文件不可下载",
  artifact_invalid: "文件完整性校验失败，已禁止下载",
};
const terminalErrors: Record<string, string> = {
  authorization_revoked: "任务执行时当前授权失效，未发布文件；重新核对授权后创建新导出。",
  export_invalid_text: "观测中存在 XLSX 无法原样表示的文本，整个导出已停止；核对源数据后重新采集。",
  export_cell_too_long: "至少一个单元格超过 32,767 个 UTF-16 单元，未截断数据；调整所选列或源数据后新建导出。",
  artifact_too_large: "完整文件超过 32 MiB 上限，未发布部分文件；缩小筛选范围或减少列后新建导出。",
  directory_observation_unavailable: "固定观测已不可用，未改用其他观测；重新选择并核对来源后新建导出。",
  export_timeout: "任务超过执行时间上限，未发布文件；核对服务状态后重新创建导出。",
};
export default function ComputerAssetExportV3(props: Props) {
  const request = props.query.observationId ? requestFromQuery(props.query) : null;
  return <Export key={`${JSON.stringify([props.profile.ID, props.profile.username, props.profile.role, props.profile.priv, props.profile.hasMfa, props.profile.needChangePwd, props.profile.isExpired, props.profile.csrfToken])}:${JSON.stringify([props.query.domainId, props.query.expectedRevision, props.query.expectedCredentialRevision, request])}`} {...props} request={request} />;
}
function Export({ profile, query, disabled, failure, request }: Props & { request: ExportRequest | null }) {
  const [abandoning, setAbandoning] = useState(false);
  const [open, setOpen] = useState(false), [columns, setColumns] = useState<ExportColumn[]>([...exportColumns]);
  const [recovery, setRecovery] = useState(() => readExportRecovery(profile.ID));
  const [intent, setIntent] = useState<Intent | null>(null), [detail, setDetail] = useState<ExportDetail | null>(null);
  const [error, setError] = useState(""), [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false), [allowed, setAllowed] = useState<boolean[]>([]);
  const [proofVersion, setProofVersion] = useState(0), [revoked, setRevoked] = useState(false);
  const [cancelBlocked, setCancelBlocked] = useState("");
  const [privateCancel, setPrivateCancel] = useState(false);
  const cancelSticky = useRef(false), cancelling = useRef(false), cancelPrepared = useRef(true), cancelTask = useRef("");
  const sequence = useRef(0), locked = useRef(false), controller = useRef<AbortController | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const intentRef = useRef<Intent | null>(null), failureRef = useRef(failure);
  failureRef.current = failure;
  const resetTransport = () => {
    sequence.current++; controller.current?.abort(); clearTimeout(timer.current); locked.current = false; setBusy(false);
  };
  useLayoutEffect(() => () => {
    sequence.current++; controller.current?.abort(); clearTimeout(timer.current); locked.current = false;
  }, []);
  const denied = (reason: unknown) => {
    if (reason instanceof ApiError && (sessionError(reason) || [401, 403].includes(reason.status) || (reason.status === 404 && !!intentRef.current?.taskID) || reason.code === "authorization_epoch_changed")) {
      resetTransport(); setDetail(null); setProofVersion((v) => v + 1); intentRef.current = null; setIntent(null); setRevoked(true); setAllowed([]); failureRef.current(reason);
      return true;
    }
    return false;
  };
  const operation = async (work: (signal: AbortSignal) => Promise<void>, cancelAction = false) => {
    if (locked.current || revoked) return;
    locked.current = true; clearTimeout(timer.current); controller.current?.abort();
    const current = ++sequence.current, control = new AbortController(); controller.current = control;
    setBusy(true); setError(""); setNotice("");
    cancelling.current = cancelAction;
    if (cancelAction) timer.current = setTimeout(() => {
      if (current !== sequence.current) return;
      resetTransport(); setDetail(null); setPrivateCancel(true); setProofVersion((v) => v + 1);
      setCancelBlocked(cancelUnknown); setError(cancelUnknown);
    }, 30000);
    try { await work(control.signal); }
    catch (reason) {
      if (current !== sequence.current || control.signal.aborted) return;
      setDetail(null); setError(cancelAction ? cancelUnknown : computerAssetExportError(reason));
      if (cancelAction) {
        setCancelBlocked(cancelUnknown); setPrivateCancel(true); setProofVersion((v) => v + 1);
        if (reason instanceof ApiError && reason.status === 404) {
          intentRef.current = null; setIntent(null); setAllowed([]); setRevoked(true);
        } else denied(reason);
        return;
      }
      if (!denied(reason) && cancelPrepared.current && !cancelSticky.current && (!cancelBlocked || cancelBlocked === cancelUnavailable) && reason instanceof ApiError && (reason.code === "network" || reason.status >= 500) && (intentRef.current?.attempted || (recovery?.actorID === profile.ID))) {
        // Retry read-only recovery, never the mutation or a newly generated key.
        timer.current = setTimeout(() => { void operation(recoverSaved); }, 2000);
      }
    } finally {
      if (current === sequence.current && !control.signal.aborted) {
        if (cancelAction) clearTimeout(timer.current);
        cancelling.current = false; locked.current = false; setBusy(false);
      }
    }
  };
  const active = (signal: AbortSignal) => !signal.aborted && controller.current?.signal === signal;
  const refreshDetail = async (value: ExportReceipt, signal: AbortSignal) => {
    const next = await computerAssetExportAPI.detail(value.taskID, value.request, profile.ID, signal);
    if (!active(signal)) return;
    if (cancelTask.current !== next.taskID) { cancelTask.current = next.taskID; cancelSticky.current = false; }
    const binding = { profile, taskID: next.taskID, request: value.request, signal,
      assertCurrent: () => { if (!active(signal)) throw new DOMException("Stale operation", "AbortError"); } };
    let marker = await inspectCancelMarker(binding);
    if (!active(signal)) return;
    let observed = next;
    if (terminal(next.task.state) && marker && marker !== cancelUnavailable) {
      const result = await observeComputerExportCancel(binding);
      if (!active(signal)) return;
      observed = result.detail ?? next; marker = result.message;
    }
    // Ordinary same-original receipt/detail polling, including queued -> running,
    // preserves the mounted fieldset and already prepared MFA. Only lifecycle
    // invalidation or an explicit cancellation attempt destroys that preparation.
    setCancelBlocked(cancelSticky.current ? cancelUnknown : marker || (!cancelPrepared.current ? "准备状态已清除，请显式核对原导出回执。" : ""));
    setDetail(observed); setPrivateCancel(false);
    if (!terminal(observed.task.state) && (!marker || marker === cancelUnavailable) && !cancelSticky.current && cancelPrepared.current) {
      timer.current = setTimeout(() => { void operation((s) => refreshDetail(observed, s)); }, 1500);
    }
  };
  const accept = async (value: ExportReceipt, current: Intent, signal: AbortSignal) => {
    if (!active(signal) || intentRef.current !== current || !sameExportRequest(value.request, current.request)) return;
    current.taskID = value.taskID; setIntent({ ...current });
    await refreshDetail(value, signal);
  };
  const recoverSaved = async (signal: AbortSignal) => {
    const current = intentRef.current;
    if (current?.attempted) {
      const value = await computerAssetExportAPI.receipt(current.request.domainId, current.key, profile.ID, signal, current.request);
      await accept(value, current, signal);
    } else if (recovery) {
      const value = await computerAssetExportAPI.receipt(recovery.domainId, recovery.idempotencyKey, profile.ID, signal);
      if (!active(signal) || intentRef.current) return;
      const restored: Intent = { request: value.request, key: recovery.idempotencyKey, attempted: true, taskID: value.taskID };
      intentRef.current = restored; setIntent(restored);
      await accept(value, restored, signal);
    }
  };
  const recover = () => { if (locked.current) return; cancelPrepared.current = true; void operation(recoverSaved); };
  const show = () => {
    cancelPrepared.current = true; setOpen(true);
    void operation(async (signal) => {
      const [menu, checks] = await Promise.all([
        accessRequest<{ menu: Permission[] }>("/menu", signal),
        accessRequest<{ results: boolean[] }>("/check", signal, { paths: exportOperations }, profile.csrfToken),
      ]);
      if (!active(signal)) return;
      if (!Array.isArray(menu.menu) || !Array.isArray(checks.results) || checks.results.length !== exportOperations.length || checks.results.some((v) => typeof v !== "boolean")) throw new ApiError("invalid_response");
      const can = (mark: string, mode: "readable" | "writeable") => menu.menu.some((p) => p?.mark === mark && p.auth?.[mode] === true);
      const read = ["domains", "directory_assets", "computer_asset_exports", "tasks"].every((m) => can(m, "readable")) && checks.results[1] && checks.results[2];
      const write = read && can("computer_asset_exports", "writeable") && can("tasks", "writeable");
      setAllowed(checks.results.map((v, i) => v && (i === 0 || i === 3 ? write : read)));
      if (!read) throw new ApiError("forbidden", 403);
      if (checks.results[1]) await recoverSaved(signal);
    });
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event);
    if (locked.current || abandoning || !request || !allowed[0] || disabled || (recovery && !intentRef.current)) return;
    const proof = getProof(values);
    if (!proof) { setError("请输入有效的操作者密码及六位未使用验证码。"); return; }
    const current: Intent = intentRef.current ?? { request: { ...request, screenColumns: columns.length ? [...columns] : [...exportColumns] }, key: crypto.randomUUID(), attempted: true };
    const locator: ExportRecovery = { version: 1, kind: exportKind, actorID: profile.ID, domainId: current.request.domainId, idempotencyKey: current.key };
    writeExportRecovery(locator); setRecovery(locator);
    cancelPrepared.current = true;
    intentRef.current = current; setIntent(current); setProofVersion((v) => v + 1);
    void operation(async (signal) => {
      const value = await computerAssetExportAPI.create(current.request, current.key, proof, profile.ID, profile.csrfToken, signal);
      await accept(value, current, signal);
    });
  };
  useLayoutEffect(() => {
    const invalidateCancel = () => {
      cancelPrepared.current = false;
      setProofVersion((v) => v + 1); setCancelBlocked("准备状态已清除，请显式核对原导出回执。");
      if (cancelling.current || intentRef.current?.taskID) { resetTransport(); setDetail(null); setPrivateCancel(true); }
    };
    const storage = (event: StorageEvent) => {
      const id = intentRef.current?.taskID;
      try { if (event.storageArea === localStorage && (event.key === null || (id && event.key === cancelStorageKey(profile.ID, id)))) invalidateCancel(); }
      catch { invalidateCancel(); }
    };
    window.addEventListener("storage", storage); window.addEventListener("focus", invalidateCancel);
    document.addEventListener("visibilitychange", invalidateCancel);
    return () => { window.removeEventListener("storage", storage); window.removeEventListener("focus", invalidateCancel); document.removeEventListener("visibilitychange", invalidateCancel); };
    // Events invalidate only; explicit recovery performs the authorized reads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const cancel = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event), current = intentRef.current;
    event.currentTarget.reset(); setProofVersion((v) => v + 1);
    if (locked.current || cancelSticky.current || cancelBlocked || !cancelPrepared.current || !allowed[3] || !current?.taskID ||
      !detail || !["queued", "running", "retry_wait"].includes(detail.task.state)) return;
    const proof = getProof(values);
    if (!proof) { setError("请输入有效的操作者密码及六位未使用验证码。"); return; }
    setCancelBlocked(cancelUnknown); setDetail(null); setPrivateCancel(true);
    void operation(async (signal) => {
      const result = await submitComputerExportCancel({ profile, taskID: current.taskID!, request: current.request, signal,
        assertCurrent: () => { if (!active(signal) || intentRef.current !== current) throw new DOMException("Stale operation", "AbortError"); } }, proof,
        () => { setDetail(null); setProofVersion((v) => v + 1); }, () => { cancelSticky.current = true; });
      if (!active(signal)) return;
      if (result.state === "proof-rejected") cancelSticky.current = false;
      setCancelBlocked(result.queryOnly ? result.message || cancelUnknown : ""); setNotice(result.message);
      setDetail(result.detail ?? null); setPrivateCancel(!result.detail);
    }, true);
  };
  const capturedRequest = intent?.request ?? request;
  const abandonControls = (intent?.attempted || recovery) && <div>
    {!abandoning ? <button type="button" className="secondary" onClick={() => { resetTransport(); setAbandoning(true); }}>放弃原回执恢复</button> : <fieldset aria-label="确认放弃原导出恢复">
      <legend>确认放弃原导出恢复</legend>
      <p role="alert">这只删除本标签页的恢复记录，不会取消服务器任务。原任务可能已创建或正在执行；另建导出可能重复。请保留原幂等键与已知任务 ID 供核对。</p>
      <button type="button" onClick={() => {
        resetTransport(); clearExportRecovery(); setRecovery(null); intentRef.current = null;
        cancelSticky.current = false; cancelPrepared.current = true; cancelTask.current = ""; setCancelBlocked(""); setPrivateCancel(false);
        setIntent(null); setDetail(null); setError(""); setAbandoning(false); setProofVersion((v) => v + 1);
        setNotice("已按你的确认放弃恢复记录，未发送新建或取消请求。");
      }}>确认放弃恢复记录</button>
      <button type="button" className="secondary" onClick={() => setAbandoning(false)}>保留原回执恢复</button>
    </fieldset>}
  </div>;
  if (revoked) return <section aria-label="导出访问已失效"><p role="alert">当前登录或导出权限已变化，私有导出数据已清除。请重新核对登录与授权。</p>{abandonControls}</section>;
  if (!open) return <button type="button" disabled={(!request || disabled) && !recovery} onClick={show}>{!request ? "恢复计算机导出回执" : "导出匹配计算机 XLSX"}</button>;
  return <section aria-label="计算机资产导出" className="asset-data-help">
    <h3>计算机资产 XLSX 导出</h3>
    <button type="button" className="secondary" onClick={() => { resetTransport(); cancelPrepared.current = false; setOpen(false); setDetail(null); setProofVersion((v) => v + 1); }}>关闭导出面板</button>
    <p>导出固定观测中全部匹配计算机，不限当前页。最多 10,000 行，五个已采集标量列；缺失值写为空单元格，空结果仍生成表头。</p>
    {recovery && !intent && <p role="status">原导出请求尚未确认；下方仅显示当前列表来源，不能用于重发原任务。</p>}
    {!privateCancel && capturedRequest && <dl aria-label="固定导出来源">
      <dt>域</dt><dd>{display(capturedRequest.domainId)}</dd>
      <dt>配置版本 / 凭据版本</dt><dd>{capturedRequest.expectedRevision} / {capturedRequest.expectedCredentialRevision}</dd>
      <dt>固定观测</dt><dd>{display(capturedRequest.observationId)}</dd>
      <dt>关键词</dt><dd>{display(capturedRequest.search) || "不限"}</dd>
      <dt>创建时间范围（含起点、不含终点）</dt><dd>{capturedRequest.startTm ? `${capturedRequest.startTm} → ${capturedRequest.endTm}` : "不限"}</dd>
      <dt>创建时间排序</dt><dd>{capturedRequest.creatTm || "GUID 升序"}</dd>
    </dl>}
    {!request && <p role="status">当前没有可用固定观测，不能新建导出；仍可核对原回执和取消原任务。</p>}
    {!privateCancel && <p aria-label="固定导出列顺序">原有序选择列：{intent ? intent.request.screenColumns.join(" → ") : recovery ? "尚待原回执确认" : (columns.length ? columns : exportColumns).join(" → ")}</p>}
    <ErrorNotice error={error} />{notice && <p role="status">{notice}</p>}
    {busy && <p role="status">正在核对导出…</p>}
    {request && !intent?.attempted && !recovery && <form onSubmit={submit}>
      <fieldset disabled={busy || !allowed[0] || disabled}>
        <legend>选择导出列（按所选顺序；空选使用五个默认列）</legend>
        {exportColumns.map((column) => <label key={column}><input type="checkbox" checked={columns.includes(column)} onChange={(e) => setColumns(e.target.checked ? [...columns, column] : columns.filter((c) => c !== column))} />导出列：{column}</label>)}
        <p aria-label="导出列顺序">{(columns.length ? columns : exportColumns).join(" → ")}</p>
      </fieldset>
      {allowed[0] && <fieldset key={proofVersion} disabled={busy || disabled}><ProofFields /></fieldset>}
      <button disabled={busy || abandoning || !allowed[0] || disabled}>创建计算机导出</button>
      {!busy && !allowed[0] && <p role="status">当前权限仅允许读取已有导出，不能创建或取消。</p>}
    </form>}
    {(intent?.attempted || recovery) && <>
      {recovery && recovery.domainId !== query.domainId && <p role="status">正在核对原导出所属域 {display(recovery.domainId)}；原回执仍绑定原域，当前列表来源不变。</p>}
      {intent && (!request || !sameExportRequest(intent.request, { ...request, screenColumns: intent.request.screenColumns })) && <p role="status">正在恢复原操作，以下来源和筛选属于原导出；当前计算机列表未改变。</p>}
      {!privateCancel && <p>原操作幂等键：<span>{intent?.key ?? recovery?.idempotencyKey}</span></p>}
      <p>停止等待不会取消服务器任务。提交结果未知时只核对原回执，不自动创建另一任务。</p>
      <button disabled={busy || !allowed[1]} onClick={recover}>核对原导出回执</button>
    </>}
    {abandonControls}
    {request && intent?.attempted && !detail && allowed[0] && !privateCancel && !cancelSticky.current && <form onSubmit={submit}>
      <p>主动重试保持原请求与原幂等键，需要新的验证码；重新登录后尚未取到原请求时不能重发。</p>
      <fieldset key={proofVersion} disabled={busy || disabled}><ProofFields /></fieldset>
      <button disabled={busy || abandoning || disabled}>使用原请求和幂等键重试</button>
    </form>}
    {detail && <>
      <p>任务 ID：{detail.taskID}</p>
      <p role="status">{stateLabels[detail.task.state]} · {statusText[detail.downloadStatus]}</p>
      <label>导出持久化进度：{detail.task.progress}%<progress aria-label="计算机导出持久化进度" value={detail.task.progress} max={100} /></label>
      {detail.task.error && <p>任务错误代码：{display(detail.task.error)}。{terminalErrors[detail.task.error] ?? "具体原因尚待服务端任务记录核对；请保留任务 ID。"}</p>}
      {detail.artifact && <p>实际导出 {detail.artifact.rowCount} 行；{detail.artifact.byteCount} 字节{detail.artifact.rowCount === 0 ? "，文件仅含表头" : ""}</p>}
      {cancelBlocked && !terminal(detail.task.state) && <p role="status">{cancelBlocked}</p>}
      {!terminal(detail.task.state) && detail.task.state !== "cancel_requested" && allowed[3] && profile.hasMfa && !profile.needChangePwd && !profile.isExpired && !cancelBlocked && !cancelCapability() && <form onSubmit={cancel}><fieldset key={proofVersion} disabled={busy}><ProofFields /></fieldset><button disabled={busy}>取消计算机导出任务</button></form>}
      {detail.task.state === "cancel_requested" && allowed[3] && <button disabled>取消计算机导出任务</button>}
      {terminal(detail.task.state) && allowed[0] && <button disabled={busy || disabled || !request} onClick={() => {
        resetTransport(); clearExportRecovery(); setRecovery(null); intentRef.current = null;
        cancelSticky.current = false; cancelPrepared.current = true; cancelTask.current = ""; setCancelBlocked(""); setPrivateCancel(false);
        setIntent(null); setDetail(null); setError(""); setNotice(""); setProofVersion((v) => v + 1);
      }}>按当前列表创建另一导出</button>}
      {detail.downloadReady && allowed[4] && <button disabled={busy} onClick={() => {
        const captured = detail;
        void operation(async (signal) => {
          const blob = await computerAssetExportAPI.download(captured, profile.ID, signal);
          if (!active(signal)) return;
          const url = URL.createObjectURL(blob), anchor = document.createElement("a");
          try {
            anchor.href = url; anchor.download = captured.artifact!.filename; anchor.hidden = true;
            document.body.appendChild(anchor); anchor.click(); setNotice("完整文件已验证，并交由浏览器下载。");
          } finally { anchor.remove(); URL.revokeObjectURL(url); }
        });
      }}>下载计算机 XLSX</button>}
    </>}
  </section>;
}
