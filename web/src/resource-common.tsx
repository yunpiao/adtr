import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { getProof } from "./access-common";
import {
  resourceErrorText,
  resourceRequest,
  type ResourceMutation,
  type ResourceOperation,
} from "./resource-api";
export interface ResourceContext {
  profile: Profile;
  can: (operation: ResourceOperation) => boolean;
  sessionChanged: () => void;
}
export function useResourceTask(sessionChanged: () => void) {
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const sequence = useRef(0),
    controller = useRef<AbortController | null>(null),
    locked = useRef(false);
  const invalidate = useCallback(() => {
    sequence.current++;
    controller.current?.abort();
    locked.current = false;
  }, []);
  useEffect(() => invalidate, [invalidate]);
  const run = async <T,>(
    work: (signal: AbortSignal) => Promise<T>,
    success: (value: T) => void,
    replace = false,
  ) => {
    if (locked.current && !replace) return;
    invalidate();
    locked.current = true;
    setBusy(true);
    setError("");
    const id = sequence.current,
      current = new AbortController();
    controller.current = current;
    try {
      const data = await work(current.signal);
      if (id === sequence.current) success(data);
    } catch (error) {
      if (id !== sequence.current) return;
      if (
        error instanceof ApiError &&
        ["unauthenticated", "password_change_required"].includes(error.code)
      )
        sessionChanged();
      else setError(resourceErrorText(error));
    } finally {
      if (id === sequence.current) {
        locked.current = false;
        setBusy(false);
      }
    }
  };
  return {
    busy,
    error,
    setError,
    run,
    cancel: () => {
      invalidate();
      setBusy(false);
      setError("");
    },
  };
}
export function useResourceMutation(
  context: ResourceContext,
  done: (notice: string) => void,
) {
  const task = useResourceTask(context.sessionChanged);
  const submit = (
    path: string,
    body: Record<string, unknown>,
    values: Record<string, string>,
    notice: string,
  ) => {
    const proof = getProof(values);
    if (!proof) {
      task.setError("请输入有效的操作者密码和六位未使用验证码。");
      return;
    }
    void task.run(
      async (signal) => {
        const data = await resourceRequest<ResourceMutation>(
          path,
          signal,
          { ...body, ...proof },
          context.profile.csrfToken,
        );
        if (
          data.result !== "SUCCESS" ||
          typeof data.sessionRevoked !== "boolean"
        )
          throw new ApiError("invalid_response");
        return data;
      },
      (data) => (data.sessionRevoked ? context.sessionChanged() : done(notice)),
    );
  };
  return { ...task, submit };
}
