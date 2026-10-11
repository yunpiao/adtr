import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import {
  messageAPI,
  type MessageReadInput,
  type MessageReadReceipt,
} from "./message-api";
import {
  loadMessageReadIntents,
  persistMessageReadIntent,
  completeMessageReadIntent,
} from "./message-read-intent";

type Operation = {
  input: MessageReadInput;
  phase: "unknown" | "replayable" | "busy" | "confirmed";
  receipt?: MessageReadReceipt;
  notice?: string;
};
const unresolved = (ops: Operation[]) =>
  ops.some((op) => op.phase !== "confirmed");
const utc = (v: string) => `${v.replace("T", " ").replace(/Z$/, "")} UTC`;
export function useMessageRead(
  profile: Profile,
  allowed: boolean,
  authorityLost: (e: ApiError) => void,
) {
  const [operations, setOperations] = useState<Operation[]>([]);
  const [storageError, setStorageError] = useState("");
  const state = useRef(operations),
    permitted = useRef(allowed),
    lost = useRef(authorityLost);
  state.current = operations;
  permitted.current = allowed;
  lost.current = authorityLost;
  const active = useRef<AbortController | null>(null),
    generation = useRef(0),
    locked = useRef(false);
  const apply = (next: Operation[]) => {
    state.current = next;
    setOperations(next);
  };
  const update = (input: MessageReadInput, patch: Partial<Operation>) =>
    apply(
      state.current.map((op) =>
        op.input.operationId === input.operationId ? { ...op, ...patch } : op,
      ),
    );
  const restore = () => {
    try {
      const saved = loadMessageReadIntents(profile);
      // Keep successful in-session receipts. Restored or other-tab records always
      // start unknown, even when storage includes forged state or a past session.
      const next = [...state.current];
      for (const input of saved)
        if (!next.some((op) => op.input.operationId === input.operationId))
          next.push({ input, phase: "unknown" });
      apply(next);
      setStorageError("");
    } catch {
      setStorageError(
        "无法可靠读取本机保存的原操作，新的已读提交已停用。请恢复浏览器存储后重新核验；不要清除未知操作记录。",
      );
    }
  };
  useEffect(() => {
    if (!allowed) {
      generation.current++;
      active.current?.abort();
      active.current = null;
      locked.current = false;
      apply([]);
      return;
    }
    restore();
    const reload = () => restore();
    window.addEventListener("storage", reload);
    window.addEventListener("focus", reload);
    return () => {
      window.removeEventListener("storage", reload);
      window.removeEventListener("focus", reload);
      generation.current++;
      active.current?.abort();
      active.current = null;
      locked.current = false;
    };
    // Identity/session changes remount the owning MessageSession.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allowed, profile.ID, profile.username, profile.csrfToken]);
  const execute = async (
    input: MessageReadInput,
    mode: "new" | "lookup" | "replay",
  ) => {
    if (locked.current || !permitted.current) return;
    const current = state.current.find(
      (op) => op.input.operationId === input.operationId,
    );
    if (
      !current ||
      current.phase === "confirmed" ||
      (mode === "replay" && current.phase !== "replayable")
    )
      return;
    locked.current = true;
    const token = generation.current,
      ctrl = new AbortController();
    active.current = ctrl;
    const live = () =>
      token === generation.current && !ctrl.signal.aborted && permitted.current;
    update(input, {
      phase: "busy",
      notice: mode === "new" ? "正在提交原操作…" : "正在核对原操作收据…",
    });
    try {
      let receipt: MessageReadReceipt;
      if (mode === "new")
        receipt = await messageAPI.markRead(
          input,
          profile.csrfToken,
          ctrl.signal,
        );
      else {
        try {
          receipt = await messageAPI.readOperation(input, ctrl.signal);
        } catch (e) {
          if (!live()) return;
          if (
            !(e instanceof ApiError) ||
            e.status !== 404 ||
            e.code !== "not_found"
          )
            throw e;
          if (mode === "lookup") {
            update(input, {
              phase: "replayable",
              notice:
                "暂未查到成功收据，404不能证明原操作未提交。可再次核对，或用原标识重放；不会新建操作。",
            });
            return;
          }
          // Every replay is preceded by a fresh current-authority receipt read.
          // Persist/read-back original input again before any network write.
          persistMessageReadIntent(profile, input);
          receipt = await messageAPI.markRead(
            input,
            profile.csrfToken,
            ctrl.signal,
          );
        }
      }
      if (!live()) return;
      const removed = completeMessageReadIntent(profile, input);
      update(input, {
        phase: "confirmed",
        receipt,
        notice: removed
          ? ""
          : "成功收据已核实，但本机恢复记录清理未确认；再次进入可查询同一收据。",
      });
    } catch (e) {
      if (!live()) return;
      if (
        e instanceof ApiError &&
        (e.status === 401 ||
          e.status === 403 ||
          e.code === "password_change_required")
      ) {
        apply([]);
        lost.current(e);
        return;
      }
      let notice =
        "原操作结果尚未确认。连接中断或响应异常不能证明失败，也不代表取消；请先查询原操作结果。";
      if (e instanceof ApiError && [404, 410].includes(e.status))
        notice =
          "原结果当前不可用或已过期，本次重放未取得成功确认。原操作仍需查收据，查询收据不依赖旧快照有效性。";
      if (e instanceof ApiError && e.status === 409)
        notice =
          "原操作标识与服务端目标冲突，未取得可匹配的成功收据。保留原标识并继续核对，不生成替代操作。";
      if (e instanceof ApiError && e.code === "storage_unavailable")
        notice =
          "本机无法可靠保存原标识，未发出这次重放。此前提交结果仍需查询收据。";
      update(input, { phase: "unknown", notice });
    } finally {
      if (token === generation.current) {
        locked.current = false;
        active.current = null;
      }
    }
  };
  const mark = (resultId: string, messageId: string) => {
    if (
      !permitted.current ||
      locked.current ||
      unresolved(state.current) ||
      state.current.some((op) => op.receipt?.messageId === messageId)
    )
      return;
    let input: MessageReadInput;
    try {
      // Check disk afresh, including another tab's intents, before assigning IDs.
      if (loadMessageReadIntents(profile).length) {
        restore();
        return;
      }
      input = { resultId, messageId, operationId: crypto.randomUUID() };
      persistMessageReadIntent(profile, input);
    } catch {
      setStorageError(
        "无法可靠保存原操作标识，未发出已读请求。请恢复浏览器存储后重试。",
      );
      return;
    }
    setStorageError("");
    apply([...state.current, { input, phase: "unknown" }]);
    void execute(input, "new");
  };
  const busy = operations.some((op) => op.phase === "busy");
  return {
    // Query and mutation requests run independently. Revoke this generation
    // synchronously when either discovers lost authority, before React commits.
    suspend: () => {
      permitted.current = false;
      generation.current++;
      active.current?.abort();
      active.current = null;
      locked.current = false;
      apply([]);
    },
    mark,
    blocked: !allowed || busy || unresolved(operations) || !!storageError,
    isConfirmed: (id: string) =>
      operations.some((op) => op.receipt?.messageId === id),
    panel:
      allowed && (operations.length > 0 || storageError) ? (
        <section className="message-read-panel" aria-label="单条已读操作">
          {storageError && <p role="alert">{storageError}</p>}
          {storageError && (
            <button className="secondary" onClick={restore} disabled={busy}>
              重新读取原操作记录
            </button>
          )}
          {operations.map((op) => (
            <div key={op.input.operationId} className="message-read-operation">
              <h3>
                {op.phase === "confirmed"
                  ? "已确认单条已读"
                  : "单条已读结果待确认"}
              </h3>
              <dl>
                <div>
                  <dt>消息 ID</dt>
                  <dd>{op.input.messageId}</dd>
                </div>
                <div>
                  <dt>原操作 ID</dt>
                  <dd>{op.input.operationId}</dd>
                </div>
                <div>
                  <dt>原固定结果</dt>
                  <dd>{op.input.resultId}</dd>
                </div>
              </dl>
              {op.receipt ? (
                <>
                  <p role="status">
                    已处理 {op.receipt.progress.processed} /{" "}
                    {op.receipt.progress.target}；本次变为已读{" "}
                    {op.receipt.progress.newlyRead}；此前已读{" "}
                    {op.receipt.progress.alreadyRead}；失败{" "}
                    {op.receipt.progress.failed}；剩余{" "}
                    {op.receipt.progress.remaining}
                  </p>
                  <p>
                    首次已读：{utc(op.receipt.firstReadAt)}；提交确认：
                    {utc(op.receipt.committedAt)}
                  </p>
                  <p>
                    旧固定结果的成员、序号和计数仍是查询时快照。请明确点击“确认查询”创建新结果，查看更新后的阅读状态和数量。
                  </p>
                </>
              ) : (
                <p>
                  原标识已保存在本浏览器，离开或关闭页面不等于取消。重新登录后仅在本人当前权限下核对；不会自动重放。
                </p>
              )}
              {op.notice && <p role="status">{op.notice}</p>}
              {op.phase !== "confirmed" && (
                <div className="message-read-actions">
                  <button
                    className="secondary"
                    disabled={busy}
                    onClick={() => void execute(op.input, "lookup")}
                  >
                    查询原操作结果
                  </button>
                  {op.phase === "replayable" && (
                    <button
                      disabled={busy}
                      onClick={() => void execute(op.input, "replay")}
                    >
                      重放原操作
                    </button>
                  )}
                </div>
              )}
            </div>
          ))}
        </section>
      ) : null,
  };
}
