import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import {
  logError,
  type Coverage,
  type LogOperation,
  type Selection,
} from "./operational-log-api";

export interface LogContext {
  profile: Profile;
  sessionChanged: () => void;
  can: (operation: LogOperation) => boolean;
}
const sessionError = (e: unknown) =>
  e instanceof ApiError &&
  ["unauthenticated", "password_change_required"].includes(e.code);
export function useLogRead<T>(
  key: string,
  load: (signal: AbortSignal) => Promise<T>,
  sessionChanged: () => void,
  poll?: (data: T) => boolean,
) {
  const [stored, setStored] = useState<{ stamp: string; value: T } | null>(
      null,
    ),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(true),
    [revision, setRevision] = useState(0);
  const stamp = `${key}:${revision}`;
  const callbacks = useRef({ load, sessionChanged, poll });
  callbacks.current = { load, sessionChanged, poll };
  useEffect(() => {
    let alive = true,
      timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    setStored(null);
    setError("");
    setBusy(true);
    async function read() {
      try {
        const v = await callbacks.current.load(controller.signal);
        if (!alive) return;
        setStored({ stamp, value: v });
        setError("");
        setBusy(false);
        if (callbacks.current.poll?.(v)) timer = setTimeout(read, 2000);
      } catch (e) {
        if (!alive) return;
        setStored(null);
        setBusy(false);
        if (sessionError(e)) callbacks.current.sessionChanged();
        else setError(logError(e));
      }
    }
    void read();
    return () => {
      alive = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [stamp]);
  return {
    data: stored?.stamp === stamp ? stored.value : null,
    error,
    busy,
    refresh: () => setRevision((n) => n + 1),
  };
}
export function useLogMutation(sessionChanged: () => void) {
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
    success: (v: T) => void,
    failed?: () => void,
  ) {
    if (locked.current) return;
    locked.current = true;
    setBusy(true);
    setError("");
    const id = ++sequence.current,
      current = new AbortController();
    controller.current = current;
    try {
      const v = await work(current.signal);
      if (id === sequence.current) success(v);
    } catch (e) {
      if (id !== sequence.current) return;
      if (sessionError(e)) sessionChanged();
      else {
        setError(logError(e));
        failed?.();
      }
    } finally {
      if (id === sequence.current) {
        locked.current = false;
        setBusy(false);
      }
    }
  }
  return { busy, error, setError, run };
}
export function CoverageNotice({ coverage }: { coverage: Coverage }) {
  return (
    <p className="warning">
      尽力记录（best_effort），可能存在缺口。日志库建立时间（UTC）：
      {coverage.journalCreatedAt}；首条入库时间（UTC）：
      {coverage.firstRecordedAt ?? "尚无记录"}。没有匹配记录不代表期间没有活动。
    </p>
  );
}
export function SelectionSummary({ selection }: { selection: Selection }) {
  return (
    <p>
      已确认入库时间范围（UTC）：{selection.startTm} 至 {selection.endTm}
      （终点不包含）；模块：{selection.systemType.join("、")}
    </p>
  );
}
