import { useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import { ErrorNotice, ProofFields, getProof, readValues } from "./access-common";
import { stateLabels, terminal } from "./task-api";
import { sessionError } from "./task-common";
import { directoryV2DisplayText as display } from "./directory-v2-json";
import {
  exportColumns, exportKind, exportOperations, requestFromQuery, sameExportRequest,
  userAssetExportAPI, userAssetExportError,
  type ExportColumn, type ExportDetail, type ExportReceipt, type ExportRequest,
} from "./user-asset-export-v3-api";
import type { UserAssetsV3Query } from "./user-assets-v3-api";

type Props = { profile: Profile; query: UserAssetsV3Query; disabled: boolean; failure: (error: unknown) => void };
import { clearExportRecovery, readExportRecovery, writeExportRecovery, type ExportRecovery } from "./user-asset-export-v3-recovery";
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
export default function UserAssetExportV3(props: Props) {
  const request = props.query.observationId ? requestFromQuery(props.query) : null;
  return <Export key={`${props.profile.ID}:${props.profile.csrfToken}:${JSON.stringify([props.query.domainId, props.query.expectedRevision, props.query.expectedCredentialRevision, request])}`} {...props} request={request} />;
}
function Export({ profile, query, disabled, failure, request }: Props & { request: ExportRequest | null }) {
  const [abandoning, setAbandoning] = useState(false);
  const [open, setOpen] = useState(false), [columns, setColumns] = useState<ExportColumn[]>([...exportColumns]);
  const [recovery, setRecovery] = useState(() => readExportRecovery(profile.ID));
  const [intent, setIntent] = useState<Intent | null>(null), [detail, setDetail] = useState<ExportDetail | null>(null);
  const [error, setError] = useState(""), [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false), [allowed, setAllowed] = useState<boolean[]>([]);
  const [proofVersion, setProofVersion] = useState(0), [revoked, setRevoked] = useState(false);
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
    if (reason instanceof ApiError && (sessionError(reason) || [401, 403].includes(reason.status) || reason.code === "authorization_epoch_changed")) {
      resetTransport(); setDetail(null); intentRef.current = null; setIntent(null); setRevoked(true); setAllowed([]); failureRef.current(reason);
      return true;
    }
    return false;
  };
  const operation = async (work: (signal: AbortSignal) => Promise<void>) => {
    if (locked.current || revoked) return;
    locked.current = true; clearTimeout(timer.current); controller.current?.abort();
    const current = ++sequence.current, control = new AbortController(); controller.current = control;
    setBusy(true); setError(""); setNotice("");
    try { await work(control.signal); }
    catch (reason) {
      if (current !== sequence.current || control.signal.aborted) return;
      setDetail(null); setError(userAssetExportError(reason));
      if (!denied(reason) && reason instanceof ApiError && (reason.code === "network" || reason.status >= 500) && (intentRef.current?.attempted || (recovery?.actorID === profile.ID && recovery.domainId === query.domainId))) {
        // Retry read-only recovery, never the mutation or a newly generated key.
        timer.current = setTimeout(() => { void operation(recoverSaved); }, 2000);
      }
    } finally {
      if (current === sequence.current && !control.signal.aborted) { locked.current = false; setBusy(false); }
    }
  };
  const active = (signal: AbortSignal) => !signal.aborted && controller.current?.signal === signal;
  const refreshDetail = async (value: ExportReceipt, signal: AbortSignal) => {
    const next = await userAssetExportAPI.detail(value.taskID, value.request, profile.ID, signal);
    if (!active(signal)) return;
    setDetail(next);
    if (!terminal(next.task.state)) timer.current = setTimeout(() => { void operation((s) => refreshDetail(next, s)); }, 1500);
  };
  const accept = async (value: ExportReceipt, current: Intent, signal: AbortSignal) => {
    if (!active(signal) || intentRef.current !== current || !sameExportRequest(value.request, current.request)) return;
    current.taskID = value.taskID; setIntent({ ...current });
    await refreshDetail(value, signal);
  };
  const recoverSaved = async (signal: AbortSignal) => {
    const current = intentRef.current;
    if (current?.attempted) {
      const value = await userAssetExportAPI.receipt(current.request.domainId, current.key, profile.ID, signal, current.request);
      await accept(value, current, signal);
    } else if (recovery && recovery.domainId === query.domainId) {
      const value = await userAssetExportAPI.receipt(recovery.domainId, recovery.idempotencyKey, profile.ID, signal);
      if (!active(signal) || intentRef.current) return;
      const restored: Intent = { request: value.request, key: recovery.idempotencyKey, attempted: true, taskID: value.taskID };
      intentRef.current = restored; setIntent(restored);
      await accept(value, restored, signal);
    }
  };
  const recover = () => { void operation(recoverSaved); };
  const show = () => {
    setOpen(true);
    void operation(async (signal) => {
      const [menu, checks] = await Promise.all([
        accessRequest<{ menu: Permission[] }>("/menu", signal),
        accessRequest<{ results: boolean[] }>("/check", signal, { paths: exportOperations }, profile.csrfToken),
      ]);
      if (!active(signal)) return;
      if (!Array.isArray(menu.menu) || !Array.isArray(checks.results) || checks.results.length !== exportOperations.length || checks.results.some((v) => typeof v !== "boolean")) throw new ApiError("invalid_response");
      const can = (mark: string, mode: "readable" | "writeable") => menu.menu.some((p) => p?.mark === mark && p.auth?.[mode] === true);
      const read = ["domains", "directory_assets", "user_asset_exports", "tasks"].every((m) => can(m, "readable")) && checks.results[1] && checks.results[2];
      const write = read && can("user_asset_exports", "writeable") && can("tasks", "writeable");
      setAllowed(checks.results.map((v, i) => v && (i === 0 || i === 3 ? write : read)));
      if (!read) throw new ApiError("forbidden", 403);
      if (checks.results[1]) await recoverSaved(signal);
    });
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event);
    if (locked.current || abandoning || !request || !allowed[0] || disabled || (recovery && !intentRef.current) || columns.length === 0) return;
    const proof = getProof(values);
    if (!proof) { setError("请输入有效的操作者密码及六位未使用验证码。"); return; }
    const current: Intent = intentRef.current ?? { request: { ...request, screenColumns: [...columns] }, key: crypto.randomUUID(), attempted: true };
    const locator: ExportRecovery = { version: 1, kind: exportKind, actorID: profile.ID, domainId: request.domainId, idempotencyKey: current.key };
    writeExportRecovery(locator); setRecovery(locator);
    intentRef.current = current; setIntent(current); setProofVersion((v) => v + 1);
    void operation(async (signal) => {
      const value = await userAssetExportAPI.create(current.request, current.key, proof, profile.ID, profile.csrfToken, signal);
      await accept(value, current, signal);
    });
  };
  const cancel = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event), current = intentRef.current;
    if (locked.current || !allowed[3] || !current?.taskID || !detail || terminal(detail.task.state)) return;
    const proof = getProof(values);
    if (!proof) { setError("请输入有效的操作者密码及六位未使用验证码。"); return; }
    setProofVersion((v) => v + 1);
    void operation(async (signal) => {
      const task = await userAssetExportAPI.cancel(current.taskID!, current.request, proof, profile.ID, profile.csrfToken, signal);
      if (!active(signal)) return;
      setNotice(task.state === "cancelled" ? "服务器已确认取消。" : "取消请求已登记，仍需等待服务器确认。");
      await refreshDetail({ taskID: current.taskID!, request: current.request, task }, signal);
    });
  };
  const capturedRequest = intent?.request ?? request;
  const abandonControls = (intent?.attempted || recovery) && <div>
    {!abandoning ? <button type="button" className="secondary" onClick={() => { resetTransport(); setAbandoning(true); }}>放弃原回执恢复</button> : <fieldset aria-label="确认放弃原导出恢复">
      <legend>确认放弃原导出恢复</legend>
      <p role="alert">这只删除本标签页的恢复记录，不会取消服务器任务。原任务可能已创建或正在执行；另建导出可能重复。请保留原幂等键与已知任务 ID 供核对。</p>
      <button type="button" onClick={() => {
        resetTransport(); clearExportRecovery(); setRecovery(null); intentRef.current = null;
        setIntent(null); setDetail(null); setError(""); setAbandoning(false); setProofVersion((v) => v + 1);
        setNotice("已按你的确认放弃恢复记录，未发送新建或取消请求。");
      }}>确认放弃恢复记录</button>
      <button type="button" className="secondary" onClick={() => setAbandoning(false)}>保留原回执恢复</button>
    </fieldset>}
  </div>;
  if (revoked) return <section aria-label="导出访问已失效"><p role="alert">当前登录或导出权限已变化，私有导出数据已清除。请重新核对登录与授权。</p>{abandonControls}</section>;
  if (!open) return <button type="button" disabled={(!request || disabled) && !recovery} onClick={show}>{!request ? "恢复用户导出回执" : "导出匹配用户 XLSX"}</button>;
  return <section aria-label="用户资产导出" className="asset-data-help">
    <h3>用户资产 XLSX 导出</h3>
    <button type="button" className="secondary" onClick={() => { resetTransport(); setOpen(false); setDetail(null); setProofVersion((v) => v + 1); }}>关闭导出面板</button>
    <p>导出固定观测中全部匹配用户，不限当前页。最多 10,000 行，六个已采集标量列；缺失值写为空单元格，空结果仍生成表头。</p>
    {recovery && !intent && <p role="status">原导出请求尚未确认；下方仅显示当前列表来源，不能用于重发原任务。</p>}
    {capturedRequest && <dl aria-label="固定导出来源">
      <dt>域</dt><dd>{display(capturedRequest.domainId)}</dd>
      <dt>配置版本 / 凭据版本</dt><dd>{capturedRequest.expectedRevision} / {capturedRequest.expectedCredentialRevision}</dd>
      <dt>固定观测</dt><dd>{display(capturedRequest.observationId)}</dd>
      <dt>关键词 / 精确 SAM</dt><dd>{display(capturedRequest.search) || "不限"} / {display(capturedRequest.sAMAccountName) || "不限"}</dd>
      <dt>创建时间范围 / 排序</dt><dd>{display(capturedRequest.startTm) || "不限"} 至 {display(capturedRequest.endTm) || "不限"} / {capturedRequest.creatTm || "GUID 升序"}</dd>
    </dl>}
    {!request && <p role="status">当前没有可用固定观测，不能新建导出；仍可核对原回执和取消原任务。</p>}
    <p aria-label="固定导出列顺序">原有序选择列：{intent ? intent.request.screenColumns.join(" → ") : recovery ? "尚待原回执确认" : columns.join(" → ")}</p>
    <ErrorNotice error={error} />{notice && <p role="status">{notice}</p>}
    {busy && <p role="status">正在核对导出…</p>}
    {request && !intent?.attempted && !recovery && <form onSubmit={submit}>
      <fieldset disabled={busy || !allowed[0] || disabled}>
        <legend>选择导出列（按所选顺序）</legend>
        {exportColumns.map((column) => <label key={column}><input type="checkbox" checked={columns.includes(column)} onChange={(e) => setColumns(e.target.checked ? [...columns, column] : columns.filter((c) => c !== column))} />导出列：{column}</label>)}
        <p aria-label="导出列顺序">{columns.join(" → ")}</p>
      </fieldset>
      {allowed[0] && <fieldset key={proofVersion} disabled={busy || disabled}><ProofFields /></fieldset>}
      <button disabled={busy || abandoning || !allowed[0] || disabled || columns.length === 0}>创建用户导出</button>
      {!busy && !allowed[0] && <p role="status">当前权限仅允许读取已有导出，不能创建或取消。</p>}
    </form>}
    {(intent?.attempted || recovery) && <>
      {recovery && recovery.domainId !== query.domainId && <p role="status">原导出属于域 {display(recovery.domainId)}，请切回该域核对；不能用另一域的新请求覆盖原回执。</p>}
      {intent && (!request || !sameExportRequest(intent.request, { ...request, screenColumns: intent.request.screenColumns })) && <p role="status">正在恢复原操作，以下来源和筛选属于原导出；当前用户列表未改变。</p>}
      <p>原操作幂等键：<span>{intent?.key ?? recovery?.idempotencyKey}</span></p>
      <p>停止等待不会取消服务器任务。提交结果未知时只核对原回执，不自动创建另一任务。</p>
      <button disabled={busy || !allowed[1] || (!!recovery && recovery.domainId !== query.domainId)} onClick={recover}>核对原导出回执</button>
    </>}
    {abandonControls}
    {request && intent?.attempted && !detail && allowed[0] && <form onSubmit={submit}>
      <p>主动重试保持原请求与原幂等键，需要新的验证码；重新登录后尚未取到原请求时不能重发。</p>
      <fieldset key={proofVersion} disabled={busy || disabled}><ProofFields /></fieldset>
      <button disabled={busy || abandoning || disabled}>使用原请求和幂等键重试</button>
    </form>}
    {detail && <>
      <p>任务 ID：{detail.taskID}</p>
      <p role="status">{stateLabels[detail.task.state]} · {statusText[detail.downloadStatus]}</p>
      <label>导出持久化进度：{detail.task.progress}%<progress aria-label="用户导出持久化进度" value={detail.task.progress} max={100} /></label>
      {detail.task.error && <p>任务错误代码：{display(detail.task.error)}。{terminalErrors[detail.task.error] ?? "具体原因尚待服务端任务记录核对；请保留任务 ID。"}</p>}
      {detail.artifact && <p>实际导出 {detail.artifact.rowCount} 行；{detail.artifact.byteCount} 字节{detail.artifact.rowCount === 0 ? "，文件仅含表头" : ""}</p>}
      {!terminal(detail.task.state) && allowed[3] && <form onSubmit={cancel}><fieldset key={proofVersion} disabled={busy}><ProofFields /></fieldset><button disabled={busy || detail.task.state === "cancel_requested"}>取消用户导出任务</button></form>}
      {terminal(detail.task.state) && allowed[0] && <button disabled={busy || disabled || !request} onClick={() => {
        resetTransport(); clearExportRecovery(); setRecovery(null); intentRef.current = null;
        setIntent(null); setDetail(null); setError(""); setNotice(""); setProofVersion((v) => v + 1);
      }}>按当前列表创建另一导出</button>}
      {detail.downloadReady && allowed[4] && <button disabled={busy} onClick={() => {
        const captured = detail;
        void operation(async (signal) => {
          const blob = await userAssetExportAPI.download(captured, profile.ID, signal);
          if (!active(signal)) return;
          const url = URL.createObjectURL(blob), anchor = document.createElement("a");
          try {
            anchor.href = url; anchor.download = captured.artifact!.filename; anchor.hidden = true;
            document.body.appendChild(anchor); anchor.click(); setNotice("完整文件已验证，并交由浏览器下载。");
          } finally { anchor.remove(); URL.revokeObjectURL(url); }
        });
      }}>下载用户 XLSX</button>}
    </>}
  </section>;
}
