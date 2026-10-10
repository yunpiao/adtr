import type { AuditRow } from "./audit-api";

// This view consumes only the already-authorized list row. Expanding a record
// never issues a separate request or invents missing historical fields.
export function AuditResult({ row }: { row: AuditRow }) {
  if (!row.availability.eventResult)
    return <span className="audit-result missing">未记录（历史数据缺失）</span>;
  return (
    <span className={`audit-result ${row.eventResult.toLowerCase()}`}>
      {row.eventResult}
    </span>
  );
}
export function AuditRecordDetails({
  row,
  close,
}: {
  row: AuditRow;
  close: () => void;
}) {
  const labels = {
    loginUser: "登录用户",
    sourceIp: "登录IP",
    path: "请求路径",
    requestId: "请求标识",
    eventResult: "事件结果",
  };
  const missing = (Object.keys(labels) as (keyof typeof labels)[])
    .filter((key) => !row.availability[key])
    .map((key) => labels[key]);
  return (
    <section
      id={`audit-record-${row.ID}`}
      className="audit-record-details"
      aria-label={`审计记录字段 ${row.ID}`}
    >
      <div className="audit-record-heading">
        <h4>记录字段 · {row.ID}</h4>
        <button
          type="button"
          className="secondary"
          onClick={close}
          aria-label={`收起记录字段 ${row.ID}`}
        >
          收起字段
        </button>
      </div>
      <dl>
        <div>
          <dt>记录来源</dt>
          <dd>{row.source}</dd>
        </div>
        <div>
          <dt>所属域</dt>
          <dd>{row.domainId ?? "未记录 / 平台操作"}</dd>
        </div>
        <div>
          <dt>用户 ID</dt>
          <dd>{row.userId ?? "未记录"}</dd>
        </div>
        <div>
          <dt>可见性版本</dt>
          <dd>{row.visibilityVersion}</dd>
        </div>
        <div>
          <dt>事件</dt>
          <dd>{row.event}</dd>
        </div>
        <div>
          <dt>记录保护</dt>
          <dd>
            {row.deletable ? "可按现有权限隐藏 / 恢复" : "受保护控制记录"}
          </dd>
        </div>
      </dl>
      <h4>服务器返回的事件参数</h4>
      <pre className="task-json">{row.eventArgs}</pre>
      <p className="audit-metadata-availability">
        {missing.length
          ? `未记录：${missing.join("、")}。历史缺失信息不会用当前账户信息补写。`
          : "当前记录已采集全部请求元数据。"}
      </p>
    </section>
  );
}
