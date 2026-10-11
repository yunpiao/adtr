import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import type { MessageSnapshot } from "./message-api";
import {
  bulkAPI,
  bulkTerminal,
  checkBulkProgress,
  type BulkOperation,
} from "./message-bulk-api";
import {
  loadBulkIntents,
  persistBulkIntent,
  removeBulkIntent,
  type BulkIntent,
} from "./message-bulk-intent";

type Operation = BulkIntent & {
  phase: "unknown" | "missing" | "known";
  value?: BulkOperation;
  notice?: string;
};
const labels: Record<BulkOperation["state"], string> = {
  queued: "已接受，等待处理",
  running: "正在处理",
  completed: "批量已读完成",
  cancelled: "已取消剩余项目",
  authorization_blocked: "权限版本变化，剩余项目已停止",
};
const explanation: Record<BulkOperation["state"], string> = {
  queued: "服务器已冻结目标，尚未提交第一批。离开页面后仍会处理。",
  running: "已有项目提交，服务器正分批处理余项。可以取消尚未提交的余项。",
  completed:
    "所有冻结目标已处理，已提交结果保留。请确认新查询查看最新阅读状态。",
  cancelled:
    "取消已确认。已提交的首次已读时间保留，剩余项不会由本操作继续执行。",
  authorization_blocked:
    "当前账户、权限或授权版本不再满足接受时的条件。已提交结果保留，剩余项不会执行；重新获得权限也不会恢复这个批次。",
};
export function useMessageBulk(
  profile: Profile,
  allowed: boolean,
  snapshot: MessageSnapshot | undefined,
  authorityLost: (e: ApiError) => void,
) {
  const [operations, setOperations] = useState<Operation[]>([]),
    [storageError, setStorageError] = useState("");
  const [busy, setBusy] = useState(false);
  const [selection, setSelection] = useState<{
    resultId: string;
    ids: string[];
    all: boolean;
  }>({ resultId: "", ids: [], all: false });
  const [preview, setPreview] = useState<{
    resultId: string;
    ids: string[];
    all: boolean;
    target: number;
  }>();
  const state = useRef(operations),
    permitted = useRef(allowed),
    lost = useRef(authorityLost),
    generation = useRef(0),
    active = useRef<AbortController | null>(null),
    locked = useRef(false);
  state.current = operations;
  permitted.current = allowed;
  lost.current = authorityLost;
  const apply = (next: Operation[]) => {
    state.current = next;
    setOperations(next);
  };
  const update = (id: string, patch: Partial<Operation>) =>
    apply(
      state.current.map((o) =>
        o.input.operationId === id ? { ...o, ...patch } : o,
      ),
    );
  const suspend = () => {
    permitted.current = false;
    generation.current++;
    active.current?.abort();
    active.current = null;
    locked.current = false;
    apply([]);
    setBusy(false);
    setPreview(undefined);
    setSelection({ resultId: "", ids: [], all: false });
  };
  const restore = () => {
    try {
      const saved = loadBulkIntents(profile),
        next = [...state.current];
      for (const i of saved)
        if (!next.some((o) => o.input.operationId === i.input.operationId))
          next.push({ ...i, phase: "unknown" });
      apply(next);
      setStorageError("");
    } catch {
      setStorageError(
        "本机批量操作记录无法可靠读取，已暂停新提交。请恢复浏览器存储后重试，保留未知操作记录。",
      );
    }
  };
  const execute = async (
    supplied: BulkIntent,
    mode: "start" | "lookup" | "replay" | "cancel",
  ) => {
    if (locked.current || !permitted.current) return;
    const intent: BulkIntent = {
      input: supplied.input,
      target: supplied.target,
    };
    const original = state.current.find(
      (o) => o.input.operationId === intent.input.operationId,
    );
    if (
      !original ||
      (mode === "replay" && original.phase !== "missing") ||
      (mode === "cancel" &&
        (!original.value || bulkTerminal(original.value.state)))
    )
      return;
    locked.current = true;
    setBusy(true);
    const token = generation.current,
      ctrl = new AbortController();
    active.current = ctrl;
    const live = () =>
      generation.current === token && !ctrl.signal.aborted && permitted.current;
    const timeout = window.setTimeout(() => {
      if (!live()) return;
      update(intent.input.operationId, {
        phase: "unknown",
        notice:
          "等待响应已超时，原批次结果仍待确认。已停止本次等待，将只查询原操作恢复，不自动重放启动或取消。",
      });
      generation.current++;
      ctrl.abort();
      active.current = null;
      locked.current = false;
      setBusy(false);
    }, 15000);
    try {
      let value: BulkOperation;
      if (mode === "start")
        value = await bulkAPI.run(
          intent.input,
          intent.target,
          "start",
          profile.csrfToken,
          ctrl.signal,
        );
      else {
        try {
          value = await bulkAPI.run(
            intent.input,
            intent.target,
            "lookup",
            profile.csrfToken,
            ctrl.signal,
          );
        } catch (e) {
          if (!live()) return;
          if (
            !(e instanceof ApiError) ||
            e.status !== 404 ||
            e.code !== "not_found"
          )
            throw e;
          if (mode !== "replay") {
            update(intent.input.operationId, {
              phase: "missing",
              notice:
                "暂未查到原操作。404 不能证明原提交未生效；可再次查询，或显式重放原 ID 和原目标。",
            });
            return;
          }
          persistBulkIntent(profile, intent);
          value = await bulkAPI.run(
            intent.input,
            intent.target,
            "start",
            profile.csrfToken,
            ctrl.signal,
          );
        }
        if (!live()) return;
        checkBulkProgress(value, original.value);
        if (mode === "cancel" && !bulkTerminal(value.state)) {
          // Always read the original operation under current authority before
          // repeating cancellation. A running read is not proof of a lost cancel.
          persistBulkIntent(profile, intent);
          const cancelled = await bulkAPI.run(
            intent.input,
            intent.target,
            "cancel",
            profile.csrfToken,
            ctrl.signal,
          );
          checkBulkProgress(cancelled, value);
          value = cancelled;
        }
      }
      if (!live()) return;
      checkBulkProgress(value, original.value);
      update(intent.input.operationId, { phase: "known", value, notice: "" });
    } catch (e) {
      if (!live()) return;
      if (
        e instanceof ApiError &&
        (e.status === 401 ||
          e.status === 403 ||
          e.code === "password_change_required")
      ) {
        suspend();
        lost.current(e);
        return;
      }
      let notice =
        mode === "cancel"
          ? "取消响应尚未确认。已提交结果不会撤回；先查询原批次，仍运行时可对同一批次再次请求取消。"
          : "原批次结果尚未确认。先查询原操作，不生成替代批次；读取恢复会自动重试。";
      if (e instanceof ApiError && e.code === "invalid_response")
        notice =
          "返回的原目标、接受回执或持久计数未通过校验，当前结果无法确认。保留原 ID，继续查询原批次，不创建替代操作。";
      if (e instanceof ApiError && e.status === 410)
        notice =
          "原查询已过期或会话已变化，不能重新接受目标。仍可查询此前已接受的原操作；不会替换操作 ID。";
      if (e instanceof ApiError && e.status === 409)
        notice =
          "原 ID 与服务端目标冲突，未取得匹配进度。保留原记录继续核对，不能新建替代操作。";
      if (e instanceof ApiError && e.status === 429)
        notice =
          "活动批次配额已满，本次未取得接受确认。先查询原操作；查无记录后可稍后显式重放原 ID。";
      if (e instanceof ApiError && e.code === "bulk_request_too_large")
        notice =
          "所选 ID 超过 32 KiB 请求限制，本次未发送。原操作记录保留供核对，请勿把它当作已接受。";
      update(intent.input.operationId, { phase: "unknown", notice });
    } finally {
      clearTimeout(timeout);
      if (token === generation.current) {
        locked.current = false;
        active.current = null;
        setBusy(false);
      }
    }
  };
  const runner = useRef(execute);
  runner.current = execute;
  useEffect(() => {
    if (!allowed) {
      suspend();
      return;
    }
    permitted.current = true;
    restore();
    const reload = () => restore();
    // Safe read-only recovery, including reload and another tab's original intent.
    // Never automatically repeat start or cancel writes.
    let cursor = 0;
    const timer = window.setInterval(() => {
      if (locked.current) return;
      const candidates = state.current.filter(
        (o) =>
          o.phase !== "missing" &&
          (o.phase === "unknown" || !o.value || !bulkTerminal(o.value.state)),
      );
      const next = candidates.length
        ? candidates[cursor++ % candidates.length]
        : undefined;
      if (next) void runner.current(next, "lookup");
    }, 2000);
    window.addEventListener("storage", reload);
    window.addEventListener("focus", reload);
    return () => {
      clearInterval(timer);
      window.removeEventListener("storage", reload);
      window.removeEventListener("focus", reload);
      generation.current++;
      active.current?.abort();
      locked.current = false;
    };
    // Session identity changes remount MessageSession before private state renders.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allowed, profile.ID, profile.username, profile.csrfToken]);
  const currentSelection =
    selection.resultId === snapshot?.resultId
      ? selection
      : { resultId: snapshot?.resultId ?? "", ids: [], all: false };
  const unresolved = operations.some(
    (o) => o.phase !== "known" || !o.value || !bulkTerminal(o.value.state),
  );
  const blocked = !allowed || busy || unresolved || !!storageError;
  const target = currentSelection.all
    ? (snapshot?.total ?? 0)
    : currentSelection.ids.length;
  const begin = () => {
    if (
      blocked ||
      locked.current ||
      !preview ||
      preview.resultId !== snapshot?.resultId
    )
      return;
    try {
      const saved = loadBulkIntents(profile);
      if (
        saved.some(
          (i) =>
            !state.current.some(
              (o) =>
                o.input.operationId === i.input.operationId &&
                o.phase === "known" &&
                o.value &&
                bulkTerminal(o.value.state),
            ),
        )
      ) {
        restore();
        return;
      }
      const intent: BulkIntent = {
        target: preview.target,
        input: {
          resultId: preview.resultId,
          operationId: crypto.randomUUID(),
          ...(preview.all
            ? { selection: "all" as const }
            : {
                selection: "selected" as const,
                messageIds: [...preview.ids].sort(),
              }),
        },
      };
      // Verify transport budget before persisting or sending a new operation.
      if (
        new TextEncoder().encode(JSON.stringify(intent.input)).length > 32768
      ) {
        setStorageError(
          "所选 ID 超过 32 KiB 请求限制，尚未创建批次。请减少选择，或选择固定结果全部。",
        );
        return;
      }
      persistBulkIntent(profile, intent);
      apply([...state.current, { ...intent, phase: "unknown" }]);
      setPreview(undefined);
      setSelection({ resultId: "", ids: [], all: false });
      void execute(intent, "start");
    } catch {
      setStorageError(
        "无法可靠保存批量原操作，未发送新请求。请恢复存储后重试。",
      );
    }
  };
  return {
    suspend,
    selected: (id: string) =>
      currentSelection.all || currentSelection.ids.includes(id),
    selectionDisabled: blocked || currentSelection.all,
    toggle: (id: string) => {
      if (blocked || !snapshot || currentSelection.all) return;
      setPreview(undefined);
      setSelection({
        ...currentSelection,
        ids: currentSelection.ids.includes(id)
          ? currentSelection.ids.filter((v) => v !== id)
          : [...currentSelection.ids, id],
      });
    },
    controls:
      allowed && snapshot && snapshot.total > 0 ? (
        <section className="message-bulk-select" aria-label="批量目标选择">
          <h3>批量标记本人已读</h3>
          <p>
            跨页选择只属于当前固定结果。已选 {target} / {snapshot.total}{" "}
            条；已读项也会计入目标。
          </p>
          <div className="message-read-actions">
            <button
              className="secondary"
              disabled={blocked}
              onClick={() => {
                setPreview(undefined);
                setSelection({ ...currentSelection, all: true });
              }}
            >
              选择固定结果全部
            </button>
            <button
              className="secondary"
              disabled={busy}
              onClick={() => {
                setStorageError("");
                setPreview(undefined);
                setSelection({
                  resultId: snapshot.resultId,
                  ids: [],
                  all: false,
                });
              }}
            >
              清空批量选择
            </button>
            <button
              disabled={blocked || !target}
              onClick={() => setPreview({ ...currentSelection, target })}
            >
              预览批量目标
            </button>
          </div>
          {preview && preview.resultId === snapshot.resultId && (
            <section className="message-bulk-preview" aria-label="批量目标预览">
              <h4>确认冻结目标：{preview.target} 条</h4>
              <p>
                范围：
                {preview.all
                  ? "当前固定结果的全部成员（含全部页）"
                  : "当前固定结果中跨页勾选的成员"}
                。固定结果：{preview.resultId}
              </p>
              {!preview.all && (
                <details>
                  <summary>查看全部已选消息 ID</summary>
                  <ul>
                    {preview.ids.map((id) => (
                      <li key={id}>{id}</li>
                    ))}
                  </ul>
                </details>
              )}
              <p>
                仅更新本人阅读状态。接受后目标不再变化；关闭页面不取消后台处理。取消只停止尚未提交的余项。
              </p>
              <div className="message-read-actions">
                <button disabled={blocked} onClick={begin}>
                  确认启动批量已读
                </button>
                <button
                  className="secondary"
                  onClick={() => setPreview(undefined)}
                >
                  返回选择
                </button>
              </div>
            </section>
          )}
        </section>
      ) : null,
    panel:
      allowed && (operations.length > 0 || storageError) ? (
        <section
          className="message-read-panel message-bulk-panel"
          aria-label="批量已读操作"
        >
          {storageError && (
            <>
              <p role="alert">{storageError}</p>
              <button className="secondary" disabled={busy} onClick={restore}>
                重新读取批量记录
              </button>
            </>
          )}
          {operations.map((op) => (
            <article
              className="message-read-operation"
              key={op.input.operationId}
              data-bulk-operation={op.input.operationId}
            >
              <h3>
                {op.phase !== "known"
                  ? "批量操作结果待确认"
                  : labels[op.value!.state]}
              </h3>
              <dl>
                <div>
                  <dt>原操作 ID</dt>
                  <dd>{op.input.operationId}</dd>
                </div>
                <div>
                  <dt>原固定结果</dt>
                  <dd>{op.input.resultId}</dd>
                </div>
                <div>
                  <dt>冻结范围</dt>
                  <dd>
                    {op.input.selection === "all" ? "固定结果全部" : "跨页勾选"}{" "}
                    · {op.target} 条
                  </dd>
                </div>
              </dl>
              {op.value && (
                <>
                  <p>
                    {op.phase !== "known"
                      ? "以下为上次核实进度，当前状态待查询："
                      : "服务端持久进度："}
                  </p>
                  <dl className="message-bulk-counts">
                    {(
                      [
                        ["目标 Target", op.value.progress.target],
                        ["已处理 Processed", op.value.progress.processed],
                        ["新已读 NewlyRead", op.value.progress.newlyRead],
                        ["此前已读 AlreadyRead", op.value.progress.alreadyRead],
                        ["失败 Failed", op.value.progress.failed],
                        ["剩余 Remaining", op.value.progress.remaining],
                      ] as const
                    ).map(([label, count]) => (
                      <div key={label}>
                        <dt>{label}</dt>
                        <dd>{count}</dd>
                      </div>
                    ))}
                  </dl>
                  <p>
                    {op.phase === "known"
                      ? explanation[op.value.state]
                      : `上次核实状态：${labels[op.value.state]}。当前可能已变化，需查询确认。`}
                  </p>
                  <p>
                    数据库故障会回滚该批并重试，因此失败为
                    0，未提交项仍计入剩余。
                  </p>
                </>
              )}
              <p>
                原标识保存在本浏览器；刷新、重新登录或重新打开标签后只查询本人原批次，不自动提交。旧固定结果的成员与计数保持查询时状态，需点击“确认查询”刷新。
              </p>
              {op.notice && <p role="status">{op.notice}</p>}
              <div className="message-read-actions">
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => void execute(op, "lookup")}
                >
                  查询原批次进度
                </button>
                {op.phase === "missing" && (
                  <button
                    disabled={busy}
                    onClick={() => void execute(op, "replay")}
                  >
                    重放原批量操作
                  </button>
                )}
                {op.value && !bulkTerminal(op.value.state) && (
                  <button
                    className="secondary"
                    disabled={busy}
                    onClick={() => void execute(op, "cancel")}
                  >
                    取消剩余项目
                  </button>
                )}
                {op.phase === "known" &&
                  op.value &&
                  bulkTerminal(op.value.state) && (
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={() => {
                        if (
                          removeBulkIntent(profile, {
                            input: op.input,
                            target: op.target,
                          })
                        )
                          apply(
                            state.current.filter(
                              (o) =>
                                o.input.operationId !== op.input.operationId,
                            ),
                          );
                        else
                          setStorageError(
                            "本机记录清理未确认，保留此批次供再次查询。",
                          );
                      }}
                    >
                      收起已结束批次
                    </button>
                  )}
              </div>
            </article>
          ))}
        </section>
      ) : null,
  };
}
