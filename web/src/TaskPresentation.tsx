import {
  requiresDedicatedTaskControls,
  stateLabels,
  type Task,
  type TaskEvent,
} from "./task-api";

const kindLabels: Record<string, string> = {
  "infrastructure.health": "平台健康检查",
  "audit.export": "审计导出",
  "system.logs_bundle": "运行日志诊断包",
  "domain.connection_test": "域连接检测",
  "domain.account_connection_test": "账户引用连接检测",
  "domain.directory_read": "目录读取",
  "domain.directory_read.v2": "补充目录读取",
  "domain.directory_read.v3": "字典 3 目录读取",
  "domain.directory_read.v4": "字典 4 组成员目录读取",
  "user_assets.v3.export": "用户资产导出",
  "group_assets.v3.export": "组资产导出",
  "computer_assets.v3.export": "计算机资产导出",
};
export const taskKindLabel = (kind: string) =>
  Object.hasOwn(kindLabels, kind) ? kindLabels[kind] : kind;

export function TaskStateExplanation({ task }: { task: Task }) {
  const dedicated = requiresDedicatedTaskControls(task.taskName);
  const descriptions: Record<Task["state"], string> = {
    queued: "任务已进入待执行队列。等待 Worker 获取本次执行条件。",
    running: "执行器已开始本次业务尝试，终态尚未确认。页面会自动核对进度。",
    retry_wait:
      "上一次尝试未成功，服务器已安排下一次业务尝试。请核对错误代码和下次尝试时间。",
    cancel_requested: "服务器已登记取消请求，当前处于等待执行器确认的阶段。",
    succeeded:
      task.taskName === "infrastructure.health"
        ? "平台健康执行器已成功完成，结果表示此次数据库和队列检查通过。"
        : dedicated
          ? "执行器已报告成功。请按下方专用入口核对可用结果；本页保留执行状态和事件。"
          : "对应执行器已报告成功。请核对实际结果；专用任务的完整结果由对应业务页面提供。",
    failed: dedicated
      ? "任务已进入失败终态。请核对错误代码，并按下方专用入口说明处理；本页只展示执行记录。"
      : "任务已进入失败终态。请核对错误代码；服务器允许且有权限的任务可创建新的恢复任务。",
    partial_failed: dedicated
      ? "执行器报告部分失败，任务已结束。请通过下方专用入口核对可用结果与错误；本页只展示执行记录。"
      : "执行结果包含部分失败，任务已结束。请核对逐对象结果。",
    dead_letter: dedicated
      ? "自动重试已停止，任务进入死信终态。请核对错误代码与尝试次数，再按下方专用入口说明处理。"
      : "自动重试已停止，任务进入死信终态。请核对错误代码与尝试次数，再决定是否创建允许的恢复任务。",
    cancelled: "服务器已确认任务取消。已发生的副作用仍须按原执行结果核对。",
  };
  return (
    <div className={`task-state-explanation task-explanation-${task.state}`}>
      <strong>当前执行状态</strong>
      <p>{descriptions[task.state]}</p>
      {task.error && (
        <p className="task-error-code">
          服务器错误代码：<span>{task.error}</span>
        </p>
      )}
    </div>
  );
}

type TaskDestination = { href: string; label: string };
const assetExportDestinations: Record<string, TaskDestination> = {
  "user_assets.v3.export": { href: "#user-assets-v3", label: "用户资产" },
  "group_assets.v3.export": { href: "#group-assets-v3", label: "组资产" },
  "computer_assets.v3.export": {
    href: "#computer-assets-v3",
    label: "计算机资产",
  },
};
export function TaskDedicatedGuidance({ taskName }: { taskName: string }) {
  if (taskName === "domain.directory_read.v3")
    return (
      <p>
        字典 3 目录任务的观察、回执及专用任务操作请在「
        <a href="#directory-v3">字典 3 目录采集</a>
        」页面核对；读取和取消均需在该页面重新核验当前权限。
      </p>
    );
  if (taskName === "domain.directory_read.v4")
    return (
      <p>
        打开「<a href="#directory-v4">字典 4 目录采集</a>」通用工作区，
        不会重新打开当前选中的历史任务。只有本标签页同一身份保存的原操作记录
        才能通过回执恢复，并重新核验当前权限；组原始成员页面仍保持只读。
      </p>
    );
  if (taskName === "user_assets.v3.export")
    return (
      <p>
        可用下方“查看原导出结果”按此任务 ID 只读核对原请求与文件，并重新核验专用权限。
        创建、取消及未知提交恢复仍在「<a href="#user-assets-v3">用户资产</a>」专用面板处理。
      </p>
    );
  if (taskName === "computer_assets.v3.export")
    return (
      <p>
        可用下方“查看原导出结果”按此任务 ID 只读核对原请求与文件，并重新核验专用权限。
        创建、取消及未知提交恢复仍在「<a href="#computer-assets-v3">计算机资产</a>」专用面板处理。
      </p>
    );
  if (taskName === "group_assets.v3.export")
    return (
      <p>
        可用下方“查看原导出结果”按此任务 ID 只读核对原请求与文件，并重新核验专用权限。
        创建、取消及未知提交恢复仍在「<a href="#group-assets-v3">组资产</a>」专用面板处理。
      </p>
    );
  if (Object.hasOwn(assetExportDestinations, taskName)) {
    const destination = assetExportDestinations[taskName];
    return (
      <p>
        此资产导出的详情、取消和文件下载由「
        <a href={destination.href}>{destination.label}</a>
        」页面的专用导出面板处理，并重新核验当前权限。该页面不会按此任务 ID
        自动恢复；请保留原导出操作记录，按专用面板的恢复提示核对。
      </p>
    );
  }
  if (taskName === "system.logs_bundle")
    return (
      <p>
        运行日志诊断包的结果和文件请在「运行日志与诊断包」页面查看；提交、取消和下载均需该页面重新核验权限。
      </p>
    );
  if (
    taskName === "domain.directory_read" ||
    taskName === "domain.directory_read.v2"
  )
    return (
      <p>
        目录任务请在「
        {taskName === "domain.directory_read.v2" ? "补充目录资产" : "目录资产"}
        」页面查看；读取观察、查询回执、取消和原编号重试均需在对应页面核验权限。
      </p>
    );
  return null;
}

const eventLabels: Record<string, string> = {
  submitted: "任务已提交",
  leased: "取得执行租约",
  started: "开始业务尝试",
  progress: "进度已持久化",
  finished: "本次尝试已结束",
  cancel_requested: "取消请求已登记",
  cancelled: "取消已确认",
  "completed-with-cancel-race": "取消与完成竞争后结束",
};
export function TaskTimeline({ events }: { events: TaskEvent[] }) {
  return (
    <section
      className="task-timeline-panel"
      aria-labelledby="task-timeline-title"
    >
      <div className="task-panel-heading">
        <h4 id="task-timeline-title">执行时间线</h4>
        <span>{events.length} 条持久化事件</span>
      </div>
      <p className="task-timeline-description">
        按服务器返回顺序展示事件；时间为 UTC，尝试和结果版本来自当时记录。
      </p>
      <ol className="task-timeline" aria-label="持久化任务事件">
        {events.map((event) => (
          <li key={event.id}>
            <div className="task-event-time">
              <time>{event.createdAt}</time>
              <span>事件 #{event.id}</span>
            </div>
            <div className="task-event-body">
              <strong>
                {Object.hasOwn(eventLabels, event.action)
                  ? eventLabels[event.action]
                  : event.action}
              </strong>
              <small className="block">{event.action}</small>
              <p>
                {stateLabels[event.state]} · {event.state}
              </p>
              {event.action === "completed-with-cancel-race" && (
                <p>取消与完成发生竞争，终态反映实际结果</p>
              )}
              <span className="task-event-version">
                尝试 {event.attempt} · 结果版本 {event.resultVersion}
              </span>
            </div>
          </li>
        ))}
      </ol>
      {!events.length && <p className="task-empty">服务器尚未返回事件。</p>}
    </section>
  );
}
