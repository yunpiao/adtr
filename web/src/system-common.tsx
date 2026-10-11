import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import {
  systemError,
  type Availability,
  type SystemOperation,
} from "./system-api";
export interface SystemContext {
  profile: Profile;
  can: (operation: SystemOperation) => boolean;
  sessionChanged: () => void;
}
const sessionError = (error: unknown) =>
  error instanceof ApiError &&
  ["unauthenticated", "password_change_required"].includes(error.code);
export function useSystemRead<T>(
  key: string,
  load: (signal: AbortSignal) => Promise<T>,
  sessionChanged: () => void,
  pollMs = 0,
) {
  const [data, setData] = useState<T | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(true),
    [revision, setRevision] = useState(0);
  const callbacks = useRef({ load, sessionChanged });
  callbacks.current = { load, sessionChanged };
  useEffect(() => {
    let alive = true,
      timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    setData(null);
    setError("");
    setBusy(true);
    async function read() {
      try {
        const value = await callbacks.current.load(controller.signal);
        if (!alive) return;
        setData(value);
        setError("");
        setBusy(false);
        if (pollMs) timer = setTimeout(read, pollMs);
      } catch (error) {
        if (!alive) return;
        setData(null);
        setBusy(false);
        if (sessionError(error)) callbacks.current.sessionChanged();
        else setError(systemError(error));
        if (
          pollMs &&
          error instanceof ApiError &&
          (error.status >= 500 ||
            ["network", "invalid_response"].includes(error.code))
        )
          timer = setTimeout(read, Math.max(pollMs, 5000));
      }
    }
    void read();
    return () => {
      alive = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [key, revision, pollMs]);
  return { data, error, busy, refresh: () => setRevision((n) => n + 1) };
}
export function useSystemMutation(sessionChanged: () => void) {
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const sequence = useRef(0),
    locked = useRef(false),
    controller = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      sequence.current++;
      controller.current?.abort();
      locked.current = false;
    },
    [],
  );
  async function run<T>(
    work: (signal: AbortSignal) => Promise<T>,
    success: (value: T) => void,
    failed: (error: unknown) => void,
  ) {
    if (locked.current) return;
    locked.current = true;
    setBusy(true);
    setError("");
    const id = ++sequence.current,
      current = new AbortController();
    controller.current = current;
    try {
      const value = await work(current.signal);
      if (id === sequence.current) success(value);
    } catch (error) {
      if (id !== sequence.current) return;
      if (sessionError(error)) sessionChanged();
      else {
        setError(systemError(error));
        failed(error);
      }
    } finally {
      if (id === sequence.current) {
        locked.current = false;
        setBusy(false);
      }
    }
  }
  return { busy, error, setError, run, isLocked: () => locked.current };
}
export const availabilityLabel = (v: Availability) =>
  ({
    available: "可用",
    unavailable: "不可用",
    warming_up: "等待采样",
    unsupported: "不支持",
  })[v];
export const utc = (value: string | null | undefined) =>
  value && !value.startsWith("0001-")
    ? new Date(value).toISOString()
    : "未提供";
export const percentText = (value: number | null) =>
  value === null ? "不可用" : `${value.toFixed(2)}%`;
export const byteText = (value: string | null) =>
  value === null ? "不可用" : `${BigInt(value).toLocaleString("en-US")} B`;
// Translate evidence codes without inferring an unobserved root cause.
export function observationReason(reason: string): string {
  const descriptions: Record<string, string> = {
    no_samples:
      "尚无持久化采样，无法判断当前资源；请检查采样与写入并等待首轮记录。",
    stale_sample:
      "最近采样已超过有效期；请检查采样进程和数据库写入，等待新样本后刷新。",
    clock_skew:
      "观测时间晚于检查时间，时钟证据不一致；请核对服务与数据库时钟。",
    no_worker_activity:
      "尚未观察到 Worker 活动；请检查进程及数据库连接并等待实际循环。",
    stale_worker_activity:
      "Worker 最近活动已超过有效期；请核对进程及循环日志，恢复后等待新活动。",
    stale_or_incomplete_worker_activity:
      "Worker 活动已过期或必需循环不完整；请核对下方每个循环的时间与状态。",
    worker_cycle_failed:
      "Worker 循环报告失败；请检查对应循环日志并核验下一次执行结果。",
    partial_resource_observation:
      "部分资源指标缺少可用采样；请在资源与历史中检查每项指标的具体原因。",
    unavailable_resource_observation:
      "资源指标暂无可用观测；请核对采样来源与服务日志。",
    unsupported_platform:
      "当前平台不支持这些采样来源；请核对运行平台与采样能力。",
    not_configured: "服务器未配置此能力，尚无检查证据。",
    read_failed: "采样来源读取失败；请核对读取权限和采样日志。",
    invalid_data: "采样来源格式无法验证；请核对来源内容与采样日志。",
    clock_reset: "采样时钟未向前推进；请核对时钟并等待新的有效采样区间。",
    counter_reset: "CPU 计数器回退或计数字段数量变化；请等待新的有效采样区间。",
    zero_delta:
      "CPU 计数在采样区间没有增长，无法计算使用率；请等待下一轮采样并核对来源。",
    first_observation: "CPU 需要两次计数计算使用率，请等待下一轮采样。",
  };
  return (
    (Object.hasOwn(descriptions, reason) ? descriptions[reason] : undefined) ??
    (reason
      ? `服务器原因：${reason}；具体根因需结合对应服务日志核查。`
      : "服务器未提供具体原因，请结合观测时间与服务日志核查。")
  );
}
export function Freshness({
  availability,
  stale,
  observedAt,
  checkedAt,
  reason,
}: {
  availability: Availability;
  stale: boolean;
  observedAt: string | null;
  checkedAt: string;
  reason?: string;
}) {
  return (
    <div
      className={
        stale || availability !== "available" ? "warning" : "system-freshness"
      }
      role="status"
    >
      <strong>
        {availabilityLabel(availability)}
        {stale ? " · 数据已过期，不代表当前状态" : ""}
      </strong>
      <small className="block">
        采样时间（UTC）：{utc(observedAt)} · 检查时间（UTC）：{utc(checkedAt)}
        {reason ? ` · ${observationReason(reason)}（${reason}）` : ""}
      </small>
    </div>
  );
}
