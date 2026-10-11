import {
  connectionLabels,
  diagnosticMessages,
  type Connection,
} from "./domain-api";

export function DomainStatus({ connection }: { connection: Connection }) {
  return (
    <span className={`domain-state domain-state-${connection.connectionState}`}>
      {connectionLabels[connection.connectionState]}
    </span>
  );
}

export function DomainGuidance({ connection: c }: { connection: Connection }) {
  const code = c.lastDiagnostic?.code;
  const meaning =
    c.connectionState === "testing"
      ? "服务器正在处理已保存版本的单次连接检测。"
      : c.connectionState === "verified"
        ? "最近一次检测已完成 TLS、凭据绑定与域命名上下文校验。"
        : c.connectionState === "error"
          ? code
            ? diagnosticMessages[code]
            : "最近一次检测未能确认连接可用。"
          : "当前保存版本还没有通过连接检测。";
  let next = "核对端点与凭据来源后，手动检测已保存版本。";
  if (c.connectionState === "testing")
    next = "在下方查看原检测进度，等待终态或核对取消结果；不要重复提交。";
  else if (c.connectionState === "verified")
    next = "结合下方检测时间与版本使用这份证据；配置变更后重新检测。";
  else if (code?.startsWith("tls_") || code === "starttls_required")
    next = "核对域控 DNS、证书有效期和部署 CA，修复信任配置后再手动检测。";
  else if (code === "credentials_rejected" || code?.startsWith("credential_"))
    next =
      "核对凭据来源与当前使用授权；凭据问题修复后再手动检测，避免连续失败登录。";
  else if (code === "executor_lost" || code === "checkpoint_failed")
    next = "先核对原任务与执行器状态。结果尚未确认时不要把重新提交当作恢复。";
  else if (
    code === "dns_failed" ||
    code === "connect_failed" ||
    code === "destination_denied"
  )
    next = "核对域控名称、连接地址和部署出口策略，再手动检测。";
  return (
    <section className="domain-guidance" aria-label="诊断与下一步">
      <div>
        <h4>当前证据</h4>
        <p>{meaning}</p>
      </div>
      <div>
        <h4>下一步</h4>
        <p>{next}</p>
      </div>
    </section>
  );
}
