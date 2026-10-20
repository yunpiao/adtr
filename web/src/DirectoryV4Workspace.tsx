import { useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { createDirectoryV4Controls, directoryV4SourceInput, directoryV4TaskClass, type DirectoryV4Controls, type DirectoryV4Input, type DirectoryV4Intent, type DirectoryV4RecoveryContext } from "./directory-v4-controls-api";
import { directoryV4Input, type DirectoryV4Storage } from "./directory-v4-intent";
import type { SourceChoice } from "./domain-selection-api";
import type { Task } from "./task-api";
import { directoryV4DisplayMember as display } from "./directory-v4-facts";
import { listenForSessionInvalidation } from "./session-invalidation";
import DirectoryV4SourcePicker from "./DirectoryV4SourcePicker";
import DirectoryV4TaskResult from "./DirectoryV4TaskResult";
import "./directory-v4-workspace.css";

type Props = { profile: Profile; sessionChanged: () => void; recoveryContext: DirectoryV4RecoveryContext; storage: DirectoryV4Storage };
const stateText: Record<string, string> = {
  queued: "已排队，尚未开始执行。", running: "执行中，正在读取目录。",
  cancel_requested: "已接受取消请求，等待任务终态；尚不能确认执行器已返回。",
  succeeded: "采集成功，可显式查看本任务绑定的原始观测。",
  failed: "任务失败，请依据安全错误码排查；不会自动重新采集。",
  cancelled: "任务已进入取消终态。", partial_failed: "出现本生产者非预期的诊断状态，只能查询原任务。",
  dead_letter: "出现本生产者非预期的诊断状态，只能查询原任务。",
};
export function directoryV4WorkspaceError(error: unknown): string {
  if (!(error instanceof ApiError)) return "请求结果未确认。请查询原操作回执；不会自动重发业务请求。";
  if (error.code === "directory_read_disabled") return "服务端报告目录采集开关未开启。本次采集不可用；原任务仍可独立查询或申请取消。";
  if (error.code === "directory_account_required") return "服务端要求当前来源绑定有效操作账户。请核对绑定；原任务查询与取消独立处理。";
  if (error.code === "invalid_credentials") return "本次证明未通过。重新核验会话及原回执后，才可按当前状态输入新证明。";
  if (error.status === 401) return "当前身份无法确认，已隐藏私有数据和证明输入。请重新验证会话。";
  if (error.status === 403) return "当前权限不可用，已隐藏私有数据。需核对功能权限、域范围及精确 v4 用途授权；此响应未说明具体缺失哪一项。";
  if (error.status === 409) return "原始来源或观测与当前配置冲突，已清除旧显示。请查询原回执；不会改用最新观测或新操作标识。";
  if (error.status === 404) return "当前未找到原回执或对象，不能据此断定请求未提交或 AD 对象已删除。";
  if (error.code === "recovery_storage_failed") return "无法可靠保存并读回原操作记录。新业务请求被阻止；保留可用的原操作查询，不清除记录。";
  return "结果尚未确认或响应未通过校验。请查询原操作；不会自动重新采集或取消。";
}
export default function DirectoryV4Workspace(props: Props) {
  return <Workspace key={`${props.profile.ID}:${props.profile.username}:${props.profile.csrfToken}:${props.profile.role}:${props.profile.priv}:${props.profile.needChangePwd}:${props.profile.isExpired}`} {...props} />;
}
function Workspace({ profile, sessionChanged, recoveryContext, storage }: Props) {
  const [api, setAPI] = useState<DirectoryV4Controls | null>(null), [ready, setReady] = useState(false), [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("正在核验当前身份和读取权限…"), [source, setSource] = useState<SourceChoice | null>(null);
  const [intent, setIntent] = useState<DirectoryV4Intent | undefined>(), [task, setTask] = useState<Task | null>(null);
  const [syncAllowed, setSyncAllowed] = useState(false), [cancelAllowed, setCancelAllowed] = useState(false), [retry, setRetry] = useState(false);
  const [durability, setDurability] = useState<ReturnType<DirectoryV4Controls["recoveryDurability"]>>("none");
  const [blocked, setBlocked] = useState(false), [form, setForm] = useState<"sync" | "cancel" | null>(null), [result, setResult] = useState(false);
  const password = useRef<HTMLInputElement | null>(null), totp = useRef<HTMLInputElement | null>(null);
  const generation = useRef(0), controller = useRef<AbortController | null>(null), lock = useRef(false), mounted = useRef(false);
  const taskBoundary = useRef(""), currentAPI = useRef<DirectoryV4Controls | null>(null);
  const clearProof = () => { if (password.current) password.current.value = ""; if (totp.current) totp.current.value = ""; };
  const closeProof = () => { clearProof(); setForm(null); };
  const deactivate = () => {
    // Synchronous DOM clearing precedes abort, React updates and all awaits.
    generation.current++; clearProof(); controller.current?.abort(); currentAPI.current = null; lock.current = false;
    taskBoundary.current = ""; setAPI(null); setReady(false); setBusy(false); setSource(null); setTask(null); setIntent(undefined);
    setSyncAllowed(false); setCancelAllowed(false); setRetry(false); setForm(null); setResult(false); setBlocked(false); setDurability("none");
  };
  const isCurrent = (epoch: number) => mounted.current && generation.current === epoch && !controller.current?.signal.aborted;
  const failure = (error: unknown) => {
    clearProof(); setNotice(directoryV4WorkspaceError(error));
    if (error instanceof ApiError && [401,403,409].includes(error.status)) {
      deactivate();
      if (error.status === 401 && error.code !== "invalid_credentials") sessionChanged();
    }
  };
  const refreshIntent = (client: DirectoryV4Controls) => {
    const record = client.readIntent();
    const persistence = client.recoveryDurability(); setDurability(persistence);
    setBlocked(record.state === "unusable" || record.state === "storage_unavailable" || persistence === "memory_only");
    // Only call after current receipt authority is established.
    const saved = client.recoveryIntent(); setIntent(saved); return saved;
  };
  const accept = async (client: DirectoryV4Controls, value: Task, signal: AbortSignal, epoch: number) => {
    const boundary = `${value.taskUUID}:${value.state}:${value.archived}`;
    if (taskBoundary.current !== boundary) { closeProof(); setResult(false); }
    taskBoundary.current = boundary;
    let canCancel = false;
    if (!value.archived && ["queued", "running"].includes(value.state)) {
      try { await client.authorize("cancel", signal); canCancel = true; }
      catch (error) {
        if (!(error instanceof ApiError) || error.status !== 403) throw error;
        // Cancel denial does not revoke an independently rechecked task read.
        await client.authorize("task", signal);
      }
    }
    if (!isCurrent(epoch)) return;
    const saved = refreshIntent(client);
    const mayCancel = canCancel && !saved?.cancelState && client.recoveryDurability() === "durable";
    if (!mayCancel) closeProof();
    setTask(value); setRetry(false); setCancelAllowed(mayCancel);
  };
  const queryOriginal = async (client: DirectoryV4Controls, signal: AbortSignal, epoch: number, receiptFirst = false) => {
    setRetry(false);
    try { await accept(client, receiptFirst ? await client.receipt(signal) : await client.task(signal), signal, epoch); }
    catch (error) {
      if (!isCurrent(epoch)) return;
      if (receiptFirst && error instanceof ApiError && error.status === 404) {
        // receipt() alone establishes the guarded one-use same-key retry token.
        const saved = client.recoveryIntent(); setIntent(undefined); setBlocked(true); setTask(null); setCancelAllowed(false); closeProof();
        let canRetry = false;
        if (saved && !saved.receiptOnly && !saved.taskUUID && client.recoveryDurability() === "durable") {
          try { await client.authorize("sync", signal); canRetry = true; }
          catch (checkError) { if (isCurrent(epoch)) failure(checkError); return; }
        }
        if (isCurrent(epoch)) { setRetry(canRetry); setSyncAllowed(canRetry); setNotice(canRetry ? "原回执暂未找到，仍不能确认先前请求未提交。只能显式使用原键和原版本重试，并提供新证明。" : "原回执暂未找到，已隐藏私有事实并保留原记录。只能重新核验并查询，不能重新采集或取消。"); }
      } else { setTask(null); setIntent(undefined); setBlocked(true); setResult(false); setCancelAllowed(false); closeProof(); failure(error); }
    }
  };
  const activate = async (selected?: SourceChoice) => {
    deactivate(); const epoch = generation.current, abort = new AbortController(); controller.current = abort; setBusy(true);
    setNotice("正在核验当前身份和读取权限…");
    try {
      const client = createDirectoryV4Controls(profile, storage, { assertCurrent() { if (!isCurrent(epoch)) throw new DOMException("Inactive view", "AbortError"); } }, recoveryContext);
      currentAPI.current = client;
      await client.authorize("page", abort.signal);
      if (!isCurrent(epoch)) return;
      const record = client.readIntent(), recovering = client.recoveryIntent();
      // Publish a blocked recovery shell before any asynchronous receipt check.
      // A stored hint must never transiently mount an unrelated source chooser.
      setBlocked(record.state !== "empty" || !!recovering); setDurability(client.recoveryDurability());
      setAPI(client); setReady(true); setNotice("");
      if (record.state !== "empty" || recovering) {
        if (!recovering) { setBlocked(true); setNotice("原操作恢复记录不可用或存储无法访问。为防止重复提交，已阻止新业务操作；不会删除旧记录。"); }
        else {
          // No stored domain/key/UUID is displayed before receipt authorization.
          try { await client.authorize("receipt", abort.signal); }
          catch (error) { if (isCurrent(epoch)) { deactivate(); setNotice("原操作恢复当前不可用。需重新核验任务读取权限；已保留同一身份的恢复记录。"); } return; }
          if (isCurrent(epoch)) await queryOriginal(client, abort.signal, epoch, true);
        }
      } else {
        await client.authorize("source", abort.signal);
        let allowed = false;
        try { await client.authorize("sync", abort.signal); allowed = true; }
        catch (error) { if (!(error instanceof ApiError) || error.status !== 403) throw error; await client.authorize("page", abort.signal); }
        const resolved = selected ? await client.resolveSource(directoryV4SourceInput(selected, crypto.randomUUID()), abort.signal) : null;
        if (isCurrent(epoch)) { setSyncAllowed(allowed); setSource(resolved); }
      }
    } catch (error) { if (isCurrent(epoch)) failure(error); }
    finally { if (isCurrent(epoch)) setBusy(false); }
  };
  useLayoutEffect(() => {
    mounted.current = true; void activate();
    const hide = () => { deactivate(); setNotice("视图已暂停并清除私有显示。重新进入前需核验身份及原回执。"); };
    const focus = (event: Event) => { if (event.isTrusted && document.visibilityState === "visible") void activate(); };
    const visibility = (event: Event) => { if (document.visibilityState === "hidden") hide(); else focus(event); };
    const session = listenForSessionInvalidation(hide);
    window.addEventListener("blur", hide); window.addEventListener("focus", focus); window.addEventListener("pagehide", hide);
    window.addEventListener("hashchange", hide); window.addEventListener("popstate", hide); document.addEventListener("visibilitychange", visibility);
    return () => { mounted.current = false; generation.current++; clearProof(); controller.current?.abort(); session.close(); window.removeEventListener("blur", hide); window.removeEventListener("focus", focus); window.removeEventListener("pagehide", hide); window.removeEventListener("hashchange", hide); window.removeEventListener("popstate", hide); document.removeEventListener("visibilitychange", visibility); };
  }, []);
  const refresh = async (receiptFirst = false) => {
    const client = currentAPI.current, abort = controller.current, epoch = generation.current;
    if (!client || !abort || lock.current) return;
    lock.current = true; setBusy(true);
    try { await queryOriginal(client, abort.signal, epoch, receiptFirst); }
    finally { if (isCurrent(epoch)) { lock.current = false; setBusy(false); } }
  };
  useLayoutEffect(() => {
    if (!ready || !task || directoryV4TaskClass(task) !== "active") return;
    const timer = window.setInterval(() => { void refresh(); }, 3000);
    return () => window.clearInterval(timer);
  }, [ready, api, task?.taskUUID, task?.state]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    // Uncontrolled inputs have no proof-bearing React state. Copy for one call,
    // clear DOM synchronously, then check locks and invoke the guarded API.
    const proof = { actorPassword: password.current?.value ?? "", totpCode: totp.current?.value ?? "" }; clearProof();
    const client = currentAPI.current, abort = controller.current, epoch = generation.current, action = form;
    if (!client || !abort || lock.current || !action) return;
    lock.current = true; setBusy(true); setForm(null); setNotice(""); setRetry(false);
    try {
      let value: Task;
      if (action === "cancel") value = await client.cancel(proof, abort.signal);
      else {
        let input: DirectoryV4Input;
        if (retry) { const saved = client.recoveryIntent(); if (!saved) throw new ApiError("original_receipt_required"); input = directoryV4Input(saved); }
        else if (!intent && source) input = directoryV4SourceInput(source, crypto.randomUUID());
        else throw new ApiError("original_receipt_required");
        value = (await client.sync(input, proof, abort.signal)).task;
      }
      if (isCurrent(epoch)) await accept(client, value, abort.signal, epoch);
    } catch (error) {
      if (isCurrent(epoch)) {
        setTask(null); setResult(false); setCancelAllowed(false); setSyncAllowed(false);
        // Keep identity internally, but expose it only after explicit receipt read.
        const record = client.readIntent(), recovering = client.recoveryIntent();
        setBlocked(record.state !== "empty" || !!recovering); setDurability(client.recoveryDurability()); setIntent(undefined);
        failure(error);
        if (!(error instanceof ApiError && [401,403,409].includes(error.status))) setNotice(`${directoryV4WorkspaceError(error)} 请点击“重新核验并恢复”查询原回执。`);
      }
    } finally { proof.actorPassword = ""; proof.totpCode = ""; if (isCurrent(epoch)) { lock.current = false; setBusy(false); } }
  };
  const acknowledge = async () => {
    const client = currentAPI.current, abort = controller.current, epoch = generation.current;
    if (!client || !abort || lock.current) return;
    closeProof(); setResult(false); lock.current = true; setBusy(true);
    try { await client.acknowledge(abort.signal); if (isCurrent(epoch)) void activate(); }
    catch (error) { if (isCurrent(epoch)) {
      setDurability(client.recoveryDurability()); setBlocked(true); setTask(null); setIntent(undefined);
      setResult(false); setCancelAllowed(false); setRetry(false); failure(error);
    } }
    finally { if (isCurrent(epoch)) { lock.current = false; setBusy(false); } }
  };
  return <section className="directory-v4-workspace" aria-label="字典 4 采集工作区">
    <h2>字典 4 目录采集</h2>
    <p>此处打开通用采集工作区，不会重新打开任务中心选中的历史任务。只有本标签页同一身份保存的原操作可以恢复。</p>
    <p>采集要求：已保存域绑定活跃操作账户、当前凭据与依赖有效，当前角色对该账户和域具有明确的 domain.directory_read.v4 使用授权。连接测试及 v1/v2/v3 授权不能替代。</p>
    <p>ADTR_DIRECTORY_READ_ENABLED 与 ADTR_DIRECTORY_READ_V4_ENABLED 均默认关闭且采集要求两者开启。部署开关状态未由接口提供；能力注册不代表已开启。原任务取消与这些采集前提独立。</p>
    {notice && <p role="status">{notice}</p>}
    {ready && durability === "memory_only" && <p role="status">{task ? "当前状态已核验，但恢复记录暂不能可靠保存，仅可继续查询。" : "恢复记录含仅保留在内存中的部分，仍须核验当前权限和原回执。"}离开当前应用或刷新页面可能丢失未保存部分；不会开放采集、取消或清除记录。</p>}
    <button disabled={busy} onClick={() => void activate()}>重新核验并恢复</button>
    {ready && api && <>
      {!intent && !blocked && <><DirectoryV4SourcePicker api={api} failure={failure} choose={choice => { void activate(choice); }} />
        {source && <p>已选来源：{display(source.domain)} · 配置版本 {source.revision} · 凭据版本 {source.credentialRevision}</p>}
        {!syncAllowed && <p>当前只读权限未开放采集表单。可使用独立组原始成员页面。</p>}
      </>}
      {intent && <section aria-label="原操作恢复"><p>原域：{display(intent.domainId)} · 原操作键：{display(intent.idempotencyKey)}</p>
        <button disabled={busy} onClick={() => void refresh(!task)}>查询原操作回执与状态</button>
        {intent.receiptOnly && <p>该操作仅允许回执恢复；不能替换来源或重新采集。原任务取消仍独立核验。</p>}
        {intent.cancelState && <p>{intent.cancelState === "unknown" ? "取消请求的结果尚未确认，可能已提交；只查询原任务，不会重复发送取消。" : intent.cancelState === "accepted" ? "原任务已接受取消请求；继续查询实际任务终态，不会重复发送取消。" : "原任务已取得终态；取消记录保留至明确确认结束原操作。"}</p>}
      </section>}
      {task && <section aria-label="原任务状态"><h3>原任务 {display(task.taskUUID)}</h3><p>{stateText[task.state]} 进度 {task.progress}%</p>{task.error && <p>安全错误码：{display(task.error)}</p>}
        {directoryV4TaskClass(task) === "terminal" && <p>任务终态接口不提供执行器返回见证或依赖释放状态；需独立证据确认。</p>}
        {cancelAllowed && <button disabled={busy} onClick={() => { clearProof(); setForm("cancel"); }}>申请取消原任务</button>}
        {task.state === "succeeded" && <button onClick={() => { closeProof(); setResult(true); }}>查看本任务的原始组成员结果</button>}
        {directoryV4TaskClass(task) === "terminal" && <button disabled={busy || durability !== "durable"} onClick={() => void acknowledge()}>确认结束原操作并清除恢复记录</button>}
      </section>}
      {syncAllowed && ((!intent && !blocked && source) || retry) && <button disabled={busy} onClick={() => { clearProof(); setForm("sync"); }}>{retry ? "使用原键和原版本显式重试" : "准备采集字典 4"}</button>}
      {form && <form aria-label={form === "cancel" ? "原任务取消证明" : "字典 4 采集证明"} onSubmit={submit} autoComplete="off">
        <label>当前用户密码<input ref={password} type="password" autoComplete="off" required /></label>
        <label>当前六位验证码<input ref={totp} inputMode="numeric" pattern="[0-9]{6}" maxLength={6} autoComplete="off" required /></label>
        <button disabled={busy}>{form === "cancel" ? "确认取消原任务" : "确认提交采集"}</button><button type="button" onClick={closeProof}>关闭证明输入</button>
      </form>}
      {result && task?.state === "succeeded" && <DirectoryV4TaskResult api={api} taskUUID={task.taskUUID} close={() => { clearProof(); setResult(false); }} failure={error => { setResult(false); failure(error); }} />}
    </>}
  </section>;
}
