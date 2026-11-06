import type { Current } from "./system-api";
import {
  availabilityLabel,
  observationReason,
  percentText,
  utc,
} from "./system-common";

export default function SystemDiskOverview({ current }: { current: Current }) {
  const roots =
    current.snapshot?.storage.filter((item) => item.id === "runtime-root") ??
    [];
  const metric = roots.length === 1 ? roots[0] : undefined;
  const trusted = !current.stale && current.availability === "available";
  const measured =
    metric?.availability === "available" && metric.percent !== null;
  const known = trusted && measured;
  let explanation = "";
  if (!current.snapshot) {
    explanation = observationReason(current.reason || "no_samples");
  } else if (roots.length === 0) {
    explanation =
      "当前响应没有 runtime-root 观测，无法判断根文件系统使用率。请核对存储登记和采样，再刷新系统信息。";
  } else if (roots.length > 1) {
    explanation =
      "当前响应包含重复的 runtime-root 观测，无法确认唯一目标。请核对存储登记和采样，再刷新系统信息。";
  } else if (current.stale) {
    explanation = observationReason("stale_sample");
  } else if (!trusted) {
    explanation = observationReason(
      current.reason || "unavailable_resource_observation",
    );
  } else if (!measured) {
    explanation = observationReason(
      metric?.reason || "unavailable_resource_observation",
    );
  }
  return (
    <section
      className="system-container-memory"
      aria-label="API 根文件系统观测"
    >
      <div className="system-finding-title">
        <h3>API 根文件系统观测</h3>
        <span
          className="system-badge"
          data-state={known ? "observed" : "unknown"}
        >
          {known
            ? "当前观测可用"
            : current.stale
              ? "数据过期"
              : "当前观测未确认"}
        </span>
      </div>
      <strong className="system-value">
        {known ? percentText(metric.percent) : "当前使用率未知"}
      </strong>
      {!known && measured && (
        <p>已有观测：{percentText(metric.percent)}，不代表当前使用率。</p>
      )}
      <p className="muted">
        范围：API
        进程看到的根文件系统（runtime_filesystem），不是宿主机全部磁盘、PostgreSQL
        数据卷或容器磁盘配额。
      </p>
      {metric && (
        <small className="block">
          {metric.id} · {metric.source} · {metric.scope} · 指标状态：
          {availabilityLabel(metric.availability)}
          <br />
          采样（UTC）：{utc(metric.observedAt)}
        </small>
      )}
      {!known && <p className="warning">{explanation}</p>}
    </section>
  );
}
