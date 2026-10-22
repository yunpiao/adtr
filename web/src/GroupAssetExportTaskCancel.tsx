import { useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import { ProofFields, getProof, readValues } from "./access-common";
import { validDomainID } from "./domain-api";
import { stateLabels, terminal, type Task } from "./task-api";
import { sessionError } from "./task-common";
import { exportKind, exportPrefix, groupAssetExportAPI, sameExportRequest, validExportRequest, type ExportDetail, type ExportRequest } from "./group-asset-export-v3-api";
import { cancelCapability, cancelStorageKey, cancelUnknown, copyCancelRequest, validCancelTaskID,
  observeGroupExportCancel, submitGroupExportCancel, type CancelBinding } from "./group-export-cancel-intent";

const paths = [`GET ${exportPrefix}/detail`, `POST ${exportPrefix}/cancel`];
type Props = { profile: Profile; task: Task; sessionChanged: () => void };
export default function GroupAssetExportTaskCancel(props: Props) {
  if (props.task.taskName !== exportKind || !validCancelTaskID(props.task.taskUUID) ||
    !validDomainID(props.task.domainId) || props.task.domainId === "platform" || props.task.archived) return null;
  return <Control key={JSON.stringify([props.profile.ID, props.profile.username, props.profile.csrfToken,
    props.profile.role, props.profile.priv, props.profile.hasMfa, props.profile.needChangePwd, props.profile.isExpired, props.task.taskUUID, props.task.domainId])} {...props} />;
}
function Control({ profile, task, sessionChanged }: Props) {
  const [open, setOpen] = useState(false), [busy, setBusy] = useState(false);
  const [detail, setDetail] = useState<ExportDetail | null>(null), [write, setWrite] = useState(false);
  const [message, setMessage] = useState(""), [proofVersion, setProofVersion] = useState(0);
  const [queryOnly, setQueryOnly] = useState(false);
  const original = useRef<ExportRequest | null>(null);
  const sticky = useRef(false), generation = useRef(0), locked = useRef(false);
  const controller = useRef<AbortController | null>(null), timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const session = useRef(sessionChanged); session.current = sessionChanged;
  const stop = () => { generation.current++; controller.current?.abort(); clearTimeout(timer.current); locked.current = false; };
  const clearPrivate = () => { setDetail(null); setWrite(false); setProofVersion((v) => v + 1); };
  const invalidate = () => { stop(); clearPrivate(); setBusy(false); setMessage("原准备状态已清除，请显式重新核对同一任务。"); };
  useLayoutEffect(() => {
    invalidate();
    const storage = (event: StorageEvent) => {
      try { if (event.storageArea === localStorage && (event.key === null || event.key === cancelStorageKey(profile.ID, task.taskUUID))) invalidate(); }
      catch { invalidate(); }
    };
    const visibility = () => invalidate();
    window.addEventListener("storage", storage); window.addEventListener("focus", visibility);
    document.addEventListener("visibilitychange", visibility);
    return () => { stop(); window.removeEventListener("storage", storage); window.removeEventListener("focus", visibility); document.removeEventListener("visibilitychange", visibility); };
    // Explicit parent refresh unmounts this control while read.busy. Unchanged
    // one-second generic polling must not destroy fresh proof preparation.
    // Meaningful state/visibility changes invalidate, never authorize a mutation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task.state, task.archived, task.visibilityVersion]);
  const run = async (work: (signal: AbortSignal, assertCurrent: () => void) => Promise<void>) => {
    if (locked.current) return;
    stop(); locked.current = true;
    const token = generation.current, control = new AbortController(); controller.current = control;
    const assertCurrent = () => { control.signal.throwIfAborted(); if (generation.current !== token) throw new DOMException("Stale operation", "AbortError"); };
    setBusy(true); setMessage("");
    timer.current = setTimeout(() => {
      if (generation.current !== token) return;
      invalidate(); setQueryOnly(sticky.current); setMessage(sticky.current ? cancelUnknown : "读取或核验超时，未取得当前结果；请显式重新核对同一任务。");
    }, 30000);
    try { await work(control.signal, assertCurrent); }
    catch (error) {
      if (generation.current !== token || control.signal.aborted) return;
      clearPrivate();
      const authority = error instanceof ApiError && ([401, 403, 404].includes(error.status) || sessionError(error) || error.code === "authorization_epoch_changed");
      setMessage(authority ? "原导出授权或会话已失效，私有详情已清除；请重新核对。" : sticky.current ? cancelUnknown : "未取得可验证的原任务状态；请手动核对，不能据此重发取消。");
      if (error instanceof ApiError && (error.status === 401 || sessionError(error))) session.current();
    } finally {
      if (generation.current === token && !control.signal.aborted) { clearTimeout(timer.current); locked.current = false; setBusy(false); }
    }
  };
  const read = () => {
    if (locked.current) return;
    setOpen(true); clearPrivate();
    void run(async (signal, assertCurrent) => {
      const menu = await accessRequest<{ menu: Permission[] }>("/menu", signal); assertCurrent();
      const check = await accessRequest<{ results: boolean[] }>("/check", signal, { paths }, profile.csrfToken); assertCurrent();
      if (!Array.isArray(menu.menu) || menu.menu.length > 256 || !Array.isArray(check.results) || check.results.length !== 2 || check.results.some((v) => typeof v !== "boolean")) throw new ApiError("invalid_response");
      const marks = ["domains", "directory_assets", "tasks", "group_asset_exports"];
      for (const mark of marks) {
        const entries = menu.menu.filter((p) => p?.mark === mark);
        if (entries.length !== 1 || typeof entries[0].auth?.readable !== "boolean" || typeof entries[0].auth?.writeable !== "boolean") throw new ApiError("invalid_response");
      }
      const can = (mark: string, mode: "readable" | "writeable") => menu.menu.find((p) => p.mark === mark)!.auth[mode];
      if (!check.results[0] || !["domains", "directory_assets", "tasks", "group_asset_exports"].every((m) => can(m, "readable"))) throw new ApiError("forbidden", 403);
      const value = original.current
        ? await groupAssetExportAPI.detail(task.taskUUID, original.current, profile.ID, signal)
        : await groupAssetExportAPI.detailByTask(task.taskUUID, task.domainId, profile.ID, signal);
      assertCurrent();
      if (value.taskID !== task.taskUUID || value.task.taskUUID !== task.taskUUID || value.task.taskName !== exportKind || value.task.archived ||
        value.task.domainId !== task.domainId || !validExportRequest(value.request) || value.request.domainId !== task.domainId ||
        (original.current && !sameExportRequest(value.request, original.current))) throw new ApiError("invalid_response");
      original.current ??= copyCancelRequest(value.request);
      const binding: CancelBinding = { profile, taskID: task.taskUUID, request: original.current, signal, assertCurrent };
      const result = await observeGroupExportCancel(binding); assertCurrent();
      setDetail(result.detail ?? null); setQueryOnly(sticky.current || result.queryOnly);
      const allowed = profile.hasMfa && !profile.needChangePwd && !profile.isExpired && check.results[1] && can("tasks", "writeable") && can("group_asset_exports", "writeable");
      setWrite(allowed);
      setMessage(result.message || (!allowed ? "当前仅有原导出读取权限，不能取消。" : ""));
    });
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event); event.currentTarget.reset(); setProofVersion((v) => v + 1);
    if (locked.current || queryOnly || sticky.current || !detail || !write || !["queued", "running", "retry_wait"].includes(detail.task.state)) return;
    const proof = getProof(values);
    if (!proof) { setMessage("请输入有效的操作者密码及六位未使用验证码。"); return; }
    const captured = detail;
    setQueryOnly(true); clearPrivate();
    void run(async (signal, assertCurrent) => {
      const result = await submitGroupExportCancel({ profile, taskID: captured.taskID, request: captured.request, signal, assertCurrent }, proof, clearPrivate, () => { sticky.current = true; });
      assertCurrent();
      if (result.state === "proof-rejected") sticky.current = false;
      setQueryOnly(sticky.current || result.queryOnly); setDetail(result.detail ?? null); setWrite(write); setMessage(result.message);
    });
  };
  return <section aria-label="任务原组导出取消">
    <button disabled={busy} onClick={read}>核对原组导出取消</button>
    {open && <>
      <button className="secondary" onClick={() => { stop(); clearPrivate(); setOpen(false); setBusy(false); setMessage(""); }}>关闭原组导出取消</button>
      <button disabled={busy} onClick={read}>读取原组导出当前状态</button>
      {busy && <p role="status">正在核对原组导出取消…</p>}
      {message && <p role="status">{message}</p>}
      {detail && <>
        <dl><dt>原任务 ID</dt><dd>{detail.taskID}</dd><dt>原域</dt><dd>{detail.request.domainId}</dd><dt>专用读取任务状态</dt><dd>{stateLabels[detail.task.state]}</dd></dl>
        <pre aria-label="原取消任务规范请求">{JSON.stringify(detail.request, null, 2)}</pre>
        {detail.task.state === "cancelled" ? <p role="status">服务器已确认取消，没有可下载文件</p>
          : detail.task.state === "cancel_requested" ? <p role="status">取消请求已登记，任务尚未停止，等待服务器确认。</p>
          : terminal(detail.task.state) ? <p role="status">任务已进入终态；以当前专用读取结果为准，不能断言取消获胜。</p>
          : <p>取消仅针对这个原任务，不重新采集或创建导出；停止等待不能保证服务器已停止。</p>}
        {!queryOnly && write && !terminal(detail.task.state) && detail.task.state !== "cancel_requested" && !cancelCapability() && <form onSubmit={submit}>
          <fieldset key={proofVersion} disabled={busy}><ProofFields /></fieldset><button disabled={busy}>取消原组导出任务</button>
        </form>}
      </>}
    </>}
  </section>;
}
