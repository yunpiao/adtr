import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { auditError, type AuditOperation } from "./audit-api";
export interface AuditContext {
  profile: Profile;
  can: (operation: AuditOperation) => boolean;
  sessionChanged: () => void;
}
export const sessionError = (error: unknown) =>
  error instanceof ApiError &&
  ["unauthenticated", "password_change_required"].includes(error.code);
const retryRead = (error: unknown) =>
  error instanceof ApiError &&
  (error.status >= 500 || ["network", "invalid_response"].includes(error.code));
// Each route/query owns a request generation. Cleanup also invalidates promises
// from transports that ignore AbortSignal, so old views cannot reappear.
export function useAuditRead<T>(
  key: string,
  load: (signal: AbortSignal) => Promise<T>,
  sessionChanged: () => void,
  poll: (value: T) => boolean = () => false,
) {
  const [data, setData] = useState<T | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(true),
    [revision, setRevision] = useState(0);
  const currentKey = useRef(key);
  const callbacks = useRef({ load, sessionChanged, poll });
  callbacks.current = { load, sessionChanged, poll };
  useEffect(() => {
    let alive = true,
      timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    if (currentKey.current !== key) setData(null);
    currentKey.current = key;
    setError("");
    setBusy(true);
    async function read() {
      try {
        const value = await callbacks.current.load(controller.signal);
        if (!alive) return;
        setData(value);
        setError("");
        setBusy(false);
        if (callbacks.current.poll(value)) timer = setTimeout(read, 1000);
      } catch (error) {
        if (!alive) return;
        setData(null);
        setBusy(false);
        if (sessionError(error)) callbacks.current.sessionChanged();
        else setError(auditError(error));
        if (retryRead(error)) timer = setTimeout(read, 2000);
      }
    }
    void read();
    return () => {
      alive = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [key, revision]);
  return { data, error, busy, refresh: () => setRevision((n) => n + 1) };
}
export function useAuditMutation(sessionChanged: () => void) {
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
  const run = async <T,>(
    work: (signal: AbortSignal) => Promise<T>,
    success: (value: T) => void,
    uncertain: (error: unknown) => void,
  ) => {
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
        setError(auditError(error));
        uncertain(error);
      }
    } finally {
      if (id === sequence.current) {
        locked.current = false;
        setBusy(false);
      }
    }
  };
  return { busy, error, setError, run };
}
