import { useLayoutEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import { sessionError } from "./task-common";
import { stateLabels, type Task } from "./task-api";
import { directoryV2DisplayText as display } from "./directory-v2-json";
import {
  exportKind, exportPrefix, computerAssetExportAPI,
  type ExportDetail, type ExportRequest,
} from "./computer-asset-export-v3-api";

const paths = [`GET ${exportPrefix}/detail`, `GET ${exportPrefix}/download`];
const statuses: Record<ExportDetail["downloadStatus"], string> = {
  pending: "任务尚未完成，文件不可下载；可手动刷新原结果。",
  ready: "原导出完整文件已就绪。",
  failed: "任务失败，没有可下载文件。",
  cancelled: "任务已取消，没有可下载文件。",
  source_changed: "来源版本已变化，原文件不可下载；本面板保留原来源，不切换到最新观测。",
  source_unavailable: "原固定观测不可用，文件不可下载。",
  artifact_invalid: "文件完整性无效，已禁止下载。",
};
type Props = { profile: Profile; task: Task; sessionChanged: () => void };
export default function ComputerAssetExportTaskResult(props: Props) {
  if (props.task.taskName !== exportKind) return null;
  return <Result key={JSON.stringify([props.profile.ID, props.profile.username, props.profile.csrfToken,
    props.task.taskUUID, props.task.domainId])} {...props} />;
}
function Result({ profile, task, sessionChanged }: Props) {
  const [allowed, setAllowed] = useState<boolean[]>([]);
  const [open, setOpen] = useState(false), [busy, setBusy] = useState(true);
  const [detail, setDetail] = useState<ExportDetail | null>(null);
  const [error, setError] = useState(""), [notice, setNotice] = useState("");
  const controller = useRef<AbortController | null>(null), generation = useRef(0), locked = useRef(false);
  const original = useRef<ExportRequest | undefined>(undefined);
  const session = useRef(sessionChanged); session.current = sessionChanged;
  const live = (signal: AbortSignal) => !signal.aborted && controller.current?.signal === signal;
  const invalidate = () => { generation.current++; controller.current?.abort(); locked.current = false; };
  const run = async (work: (signal: AbortSignal) => Promise<void>) => {
    if (locked.current) return;
    invalidate(); locked.current = true;
    const token = generation.current, control = new AbortController(); controller.current = control;
    setBusy(true); setError(""); setNotice("");
    try { await work(control.signal); }
    catch (reason) {
      if (!live(control.signal) || token !== generation.current) return;
      setDetail(null);
      const denied = reason instanceof ApiError &&
        ([401, 403, 404].includes(reason.status) || sessionError(reason) || reason.code === "authorization_epoch_changed");
      if (denied) setAllowed([]);
      setError(denied ? "原导出读取授权或会话已失效，结果已清空；请重新核验权限。"
        : "未取得可验证的原导出结果，已清空详情并停止下载；可手动重新读取同一任务。");
      if (reason instanceof ApiError && (reason.status === 401 || sessionError(reason))) session.current();
    } finally {
      if (live(control.signal) && token === generation.current) { locked.current = false; setBusy(false); }
    }
  };
  const check = async (signal: AbortSignal) => {
    setAllowed([]); setDetail(null);
    const [menu, checks] = await Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", signal),
      accessRequest<{ results: boolean[] }>("/check", signal, { paths }, profile.csrfToken),
    ]);
    if (!live(signal)) return;
    if (!Array.isArray(menu.menu) || !Array.isArray(checks.results) || checks.results.length !== paths.length ||
      checks.results.some((value) => typeof value !== "boolean")) throw new ApiError("invalid_response");
    const read = ["domains", "directory_assets", "computer_asset_exports", "tasks"].every((mark) =>
      menu.menu.some((node) => node?.mark === mark && node.auth?.readable === true));
    setAllowed(checks.results.map((value) => read && value));
  };
  useLayoutEffect(() => {
    void run(check);
    return invalidate;
    // Identity/task changes remount Result, invalidating metadata and byte reads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const read = () => {
    if (!allowed[0] || locked.current) return;
    setOpen(true); setDetail(null);
    void run(async (signal) => {
      const value = original.current
        ? await computerAssetExportAPI.detail(task.taskUUID, original.current, profile.ID, signal)
        : await computerAssetExportAPI.detailByTask(task.taskUUID, task.domainId, profile.ID, signal);
      if (!live(signal)) return;
      original.current ??= value.request;
      setDetail(value);
    });
  };
  const close = () => { invalidate(); setOpen(false); setDetail(null); setBusy(false); setError(""); setNotice(""); };
  return <section aria-label="任务原计算机导出结果">
    <button disabled={busy || !allowed[0]} onClick={read}>查看原导出结果</button>
    {!busy && !allowed[0] && <p>当前未获原导出专用读取权限；通用任务记录仍可查看。</p>}
    <button className="secondary" disabled={busy} onClick={() => { void run(check); }}>重新核验原导出权限</button>
    {busy && <><p role="status">正在核对原导出…</p><button className="secondary" onClick={close}>停止读取原导出</button></>}
    {error && <p role="alert">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {open && <section aria-label="原导出只读详情">
      <h4>原导出只读详情</h4>
      <p>仅读取此任务的原请求与文件；不会新建、取消或重放导出。</p>
      <button className="secondary" onClick={close}>关闭原导出结果</button>
      <button className="secondary" disabled={busy || !allowed[0]} onClick={read}>刷新原导出结果</button>
      {detail && <>
        <dl>
          <div><dt>原任务 ID</dt><dd>{detail.taskID}</dd></div>
          <div><dt>原域</dt><dd>{display(detail.request.domainId)}</dd></div>
          <div><dt>原域配置版本</dt><dd>{detail.request.expectedRevision}</dd></div>
          <div><dt>原目录凭据版本</dt><dd>{detail.request.expectedCredentialRevision}</dd></div>
          <div><dt>原观测 ID</dt><dd>{display(detail.request.observationId)}</dd></div>
          <div><dt>任务状态</dt><dd>{stateLabels[detail.task.state]}</dd></div>
          <div><dt>下载状态</dt><dd>{statuses[detail.downloadStatus]}</dd></div>
        </dl>
        <pre className="task-json" aria-label="原导出规范请求">{JSON.stringify(detail.request, null, 2)}</pre>
        {detail.task.error && <p>原任务错误：{display(detail.task.error)}</p>}
        {detail.artifact && <dl aria-label="原文件元数据">
          <div><dt>文件名</dt><dd>{detail.artifact.filename}</dd></div>
          <div><dt>实际行数</dt><dd>{detail.artifact.rowCount}</dd></div>
          <div><dt>完整字节</dt><dd>{detail.artifact.byteCount}</dd></div>
          <div><dt>SHA256</dt><dd>{detail.artifact.sha256}</dd></div>
        </dl>}
        <button disabled={busy || !allowed[0] || !allowed[1] || !detail.downloadReady} onClick={() => {
          if (!allowed[0] || !allowed[1] || !detail.downloadReady) return;
          const captured = detail;
          void run(async (signal) => {
            const blob = await computerAssetExportAPI.download(captured, profile.ID, signal);
            if (!live(signal)) return;
            const url = URL.createObjectURL(blob), anchor = document.createElement("a");
            try {
              anchor.href = url; anchor.download = captured.artifact!.filename; anchor.hidden = true;
              document.body.appendChild(anchor); anchor.click(); setNotice("原文件完整字节已验证，并交由浏览器下载。");
            } finally { anchor.remove(); URL.revokeObjectURL(url); }
          });
        }}>下载原计算机 XLSX</button>
      </>}
    </section>}
  </section>;
}
