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
        {reason ? ` · ${reason}` : ""}
      </small>
    </div>
  );
}
