import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, errorText, type Permission } from "./access-api";
import {
  messageAPI,
  messageOperations,
  type MessageSnapshot,
} from "./message-api";
import {
  defaultMessageQuery,
  messageQueryBody,
  messageSubTypes,
  messageSubTypeLabels,
  type MessageQuery,
} from "./message-query";
import "./message-soc.css";

type Props = { profile: Profile; sessionChanged: () => void };
type ReadIntent =
  | { kind: "page"; offset: number }
  | { kind: "locate"; id: string };
const utc = (value: string) =>
  `${value.replace("T", " ").replace(/Z$/, "")} UTC`;
const readLabels = { all: "全部", unread: "未读", read: "已读" };
function messageError(error: unknown, query = false) {
  if (error instanceof ApiError) {
    if (error.status === 410)
      return "固定结果已过期或权限版本已变化，旧结果已清除。请重新确认筛选创建结果。";
    if (error.status === 404)
      return "当前无法取得这份固定结果，旧结果已清除。请重新核验权限并确认查询。";
    if (error.status === 429)
      return "活动查询快照已达到服务端配额，暂时不能创建新结果。服务端没有提供可用时间，请稍后手动确认查询。";
    if (error.status === 422 || error.code === "query_scan_budget_exceeded")
      return "本次查询超出扫描预算，未返回部分结果。请缩小开始与结束时间范围；关键词不能减少扫描预算。";
    if (error.code === "invalid_message_time")
      return "请同时填写有效的 UTC 开始与结束时间，且开始早于结束；也可同时留空查询确认前 168 小时。";
    if (error.code === "invalid_message_id")
      return "请填写有效消息 ID：1–128 个英文字母、数字、下划线或连字符。";
    if (
      query &&
      (error.status >= 500 ||
        ["network", "invalid_response", "internal"].includes(error.code))
    )
      return "未取得可确认的查询结果；服务器可能已创建快照并占用配额。不会自动重试，再次确认查询会创建新快照。";
    if (error.code === "network" || error.code === "invalid_response")
      return "未能读取这份固定结果。可重试原结果的同一次分页或定位；不会重新创建查询。";
    if (error.code === "budget_exceeded")
      return "本次查询超出处理预算，未返回部分结果。请缩小时间范围后重新确认。";
  }
  return errorText(error);
}

export default function MessagesWorkspace(props: Props) {
  // A changed authenticated identity or session never renders the previous private result.
  return (
    <MessageSession
      key={JSON.stringify([
        props.profile.ID,
        props.profile.username,
        props.profile.csrfToken,
      ])}
      {...props}
    />
  );
}
function MessageSession({ profile, sessionChanged }: Props) {
  const [draft, setDraft] = useState(defaultMessageQuery);
  const [applied, setApplied] = useState<MessageQuery>();
  const [snapshot, setSnapshot] = useState<MessageSnapshot>();
  const [gate, setGate] = useState<boolean[] | null>(null);
  const [gateBusy, setGateBusy] = useState(true);
  const [gateRevision, setGateRevision] = useState(0);
  const [busy, setBusy] = useState<"query" | "read" | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [locateId, setLocateId] = useState("");
  const [highlight, setHighlight] = useState("");
  const [retry, setRetry] = useState<ReadIntent>();
  const generation = useRef(0),
    controller = useRef<AbortController | null>(null),
    locked = useRef(false);
  const rows = useRef(new Map<string, HTMLElement>());
  const changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  const invalidate = () => {
    generation.current++;
    controller.current?.abort();
    controller.current = null;
    locked.current = false;
  };
  useEffect(() => {
    const ctrl = new AbortController();
    let live = true;
    setGateBusy(true);
    setGate(null);
    Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", ctrl.signal),
      accessRequest<{ results: boolean[] }>(
        "/check",
        ctrl.signal,
        { paths: messageOperations },
        profile.csrfToken,
      ),
    ])
      .then(([menu, checks]) => {
        if (!live) return;
        if (
          !Array.isArray(menu.menu) ||
          !Array.isArray(checks.results) ||
          checks.results.length !== messageOperations.length ||
          checks.results.some((v) => typeof v !== "boolean")
        )
          throw new ApiError("invalid_response");
        const allowed = menu.menu.some(
          (p) => p.mark === "tasks" && p.auth?.readable === true,
        );
        setGate(checks.results.map((v) => allowed && v));
      })
      .catch((e) => {
        if (!live) return;
        setError(errorText(e));
        if (
          e instanceof ApiError &&
          (e.status === 401 || e.code === "password_change_required")
        )
          changed.current();
      })
      .finally(() => {
        if (live) setGateBusy(false);
      });
    return () => {
      live = false;
      ctrl.abort();
      invalidate();
    };
  }, [profile.csrfToken, gateRevision]);
  const resetAuthority = () => {
    invalidate();
    setSnapshot(undefined);
    setApplied(undefined);
    setHighlight("");
    setRetry(undefined);
    setBusy(null);
    setGate(null);
  };
  const handleError = (e: unknown, isQuery: boolean, intent?: ReadIntent) => {
    setError(messageError(e, isQuery));
    if (
      e instanceof ApiError &&
      (e.status === 401 || e.code === "password_change_required")
    ) {
      resetAuthority();
      changed.current();
      return;
    }
    if (e instanceof ApiError && e.status === 403) {
      resetAuthority();
      return;
    }
    if (e instanceof ApiError && [410, 404].includes(e.status)) {
      setSnapshot(undefined);
      setApplied(undefined);
      setHighlight("");
      setRetry(undefined);
      return;
    }
    if (!isQuery && intent) setRetry(intent);
  };
  const confirm = async () => {
    if (locked.current || !gate?.[0]) return;
    try {
      messageQueryBody(draft);
    } catch (e) {
      setError(messageError(e));
      return;
    }
    invalidate();
    const token = generation.current,
      ctrl = new AbortController();
    controller.current = ctrl;
    locked.current = true;
    const query = { ...draft };
    setBusy("query");
    setSnapshot(undefined);
    setApplied(undefined);
    setHighlight("");
    setRetry(undefined);
    setError("");
    setNotice("");
    try {
      const value = await messageAPI.query(
        query,
        profile.csrfToken,
        ctrl.signal,
      );
      if (token !== generation.current || ctrl.signal.aborted) return;
      setSnapshot(value);
      setApplied(query);
      setNotice("查询已确认，分页和定位将使用这份固定结果。");
    } catch (e) {
      if (token === generation.current && !ctrl.signal.aborted)
        handleError(e, true);
    } finally {
      if (token === generation.current) {
        locked.current = false;
        setBusy(null);
      }
    }
  };
  const read = async (intent: ReadIntent) => {
    if (
      locked.current ||
      !snapshot ||
      !gate?.[intent.kind === "page" ? 1 : 2] ||
      (intent.kind === "locate" && !gate?.[1])
    )
      return;
    invalidate();
    const token = generation.current,
      ctrl = new AbortController();
    controller.current = ctrl;
    locked.current = true;
    const fixed = snapshot;
    setBusy("read");
    setError("");
    setNotice("");
    setRetry(undefined);
    setHighlight("");
    try {
      let value: MessageSnapshot,
        focus = "";
      if (intent.kind === "page")
        value = await messageAPI.page(
          fixed,
          intent.offset,
          ctrl.signal,
          applied,
        );
      else {
        const location = await messageAPI.locate(fixed, intent.id, ctrl.signal);
        if (token !== generation.current || ctrl.signal.aborted) return;
        if (!location.found) {
          setNotice("该消息不在当前固定结果中。请核对消息 ID 和已确认的筛选。");
          return;
        }
        value = await messageAPI.page(
          fixed,
          (location.page - 1) * fixed.limit,
          ctrl.signal,
          applied,
        );
        if (value.items[location.index - value.offset]?.messageId !== intent.id)
          throw new ApiError("invalid_response");
        focus = intent.id;
      }
      if (token !== generation.current || ctrl.signal.aborted) return;
      setSnapshot(value);
      setHighlight(focus);
      if (focus)
        setNotice(
          `已定位消息 ${focus}，第 ${value.offset + value.items.findIndex((row) => row.messageId === focus) + 1} 条。`,
        );
    } catch (e) {
      if (token === generation.current && !ctrl.signal.aborted)
        handleError(e, false, intent);
    } finally {
      if (token === generation.current) {
        locked.current = false;
        setBusy(null);
      }
    }
  };
  useEffect(() => {
    if (highlight) {
      rows.current.get(highlight)?.focus();
      rows.current.get(highlight)?.scrollIntoView?.({ block: "nearest" });
    }
  }, [highlight, snapshot]);
  const cancel = () => {
    const wasQuery = busy === "query";
    invalidate();
    setBusy(null);
    setRetry(undefined);
    setNotice(
      wasQuery
        ? "已停止等待。服务器可能已创建快照并占用配额；再次确认查询会创建新快照。"
        : "已停止等待，保留当前固定结果。可再次执行所需分页或定位。",
    );
  };
  const edit = <K extends keyof MessageQuery>(key: K, value: MessageQuery[K]) =>
    setDraft((previous) => ({ ...previous, [key]: value }));
  const dirty = applied && JSON.stringify(draft) !== JSON.stringify(applied);
  return (
    <section className="message-workspace" aria-labelledby="message-title">
      <header className="message-heading">
        <div>
          <p className="message-eyebrow">SECURITY OPERATIONS / INBOX</p>
          <h2 id="message-title">站内消息</h2>
          <p className="muted">
            本人收到的基础设施健康任务终态 · 真实 Worker 来源
          </p>
        </div>
        <span className="message-source">infrastructure.health</span>
      </header>
      <p className="message-boundary">
        当前来源：平台范围健康任务。已读状态与数量保留查询时快照，本页只读。
      </p>
      {error && (
        <div role="alert" className="error">
          {error}
        </div>
      )}
      {notice && (
        <p role="status" className="message-notice">
          {notice}
        </p>
      )}
      {gateBusy && <p role="status">正在核验消息读取权限…</p>}
      {!gateBusy && (!gate || !gate[0]) && (
        <div className="message-empty">
          <h3>消息读取尚未获准</h3>
          <p>需要当前账户的后台任务读取权限及消息查询授权。</p>
          <button
            onClick={() => {
              setError("");
              setGateRevision((n) => n + 1);
            }}
          >
            重新核验权限
          </button>
        </div>
      )}
      {gate?.[0] && (
        <>
          <form
            className="message-filters"
            onSubmit={(e) => {
              e.preventDefault();
              void confirm();
            }}
          >
            <div className="message-filter-title">
              <h3>确认筛选</h3>
              <span>时间统一使用 UTC，结束时间不包含</span>
            </div>
            <div className="message-filter-grid">
              <label>
                关键词
                <input
                  value={draft.keyword}
                  maxLength={4000}
                  onChange={(e) => edit("keyword", e.target.value)}
                  placeholder="标题字面匹配，区分大小写"
                />
              </label>
              <label>
                任务结果
                <select
                  value={draft.subType}
                  onChange={(e) =>
                    edit("subType", e.target.value as MessageQuery["subType"])
                  }
                >
                  <option value="">全部结果</option>
                  {messageSubTypes.map((type) => (
                    <option key={type} value={type}>
                      {messageSubTypeLabels[type]}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                阅读状态
                <select
                  value={draft.readFilter}
                  onChange={(e) =>
                    edit(
                      "readFilter",
                      e.target.value as MessageQuery["readFilter"],
                    )
                  }
                >
                  {Object.entries(readLabels).map(([id, label]) => (
                    <option key={id} value={id}>
                      {label}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                排序
                <select
                  value={draft.sort}
                  onChange={(e) =>
                    edit("sort", e.target.value as MessageQuery["sort"])
                  }
                >
                  <option value="desc">发生时间倒序</option>
                  <option value="asc">发生时间正序</option>
                </select>
              </label>
              <label>
                开始时间（UTC）
                <input
                  type="text"
                  value={draft.start}
                  onChange={(e) => edit("start", e.target.value)}
                  placeholder="2026-10-01T00:00:00Z"
                />
              </label>
              <label>
                结束时间（UTC）
                <input
                  type="text"
                  value={draft.end}
                  onChange={(e) => edit("end", e.target.value)}
                  placeholder="2026-10-08T00:00:00Z"
                />
              </label>
              <label>
                每页条数
                <select
                  value={draft.limit}
                  onChange={(e) => edit("limit", Number(e.target.value))}
                >
                  {[20, 50, 100, 200].map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </label>
              <div className="message-filter-action">
                <button type="submit" disabled={!!busy}>
                  确认查询
                </button>
              </div>
            </div>
            <p className="muted">
              时间同时留空：冻结为服务端确认前 168
              小时。每次确认创建一份新快照，10 分钟有效，每用户最多 8
              份活动快照。
            </p>
            {dirty && (
              <p className="message-draft" role="status">
                筛选草稿已改变，当前结果仍使用上次确认的筛选。
              </p>
            )}
          </form>
          {busy && (
            <div className="message-progress">
              <span role="status">
                {busy === "query" ? "正在确认查询…" : "正在读取固定结果…"}
              </span>
              <button type="button" className="secondary" onClick={cancel}>
                停止等待
              </button>
            </div>
          )}
          {retry && snapshot && !busy && (
            <button className="secondary" onClick={() => void read(retry)}>
              重试原结果读取
            </button>
          )}
          {!snapshot && !busy && (
            <div className="message-empty">
              <span className="message-empty-icon" aria-hidden="true">
                ◇
              </span>
              <h3>等待确认查询</h3>
              <p>选择筛选后确认，读取属于你的真实健康任务消息。</p>
            </div>
          )}
          {snapshot && applied && (
            <>
              <section className="message-summary" aria-label="查询时消息统计">
                <div>
                  <span>全部消息</span>
                  <strong>{snapshot.counts.all}</strong>
                </div>
                <div>
                  <span>查询时未读</span>
                  <strong>{snapshot.counts.unread}</strong>
                </div>
                <div>
                  <span>查询时已读</span>
                  <strong>{snapshot.counts.read}</strong>
                </div>
                <div>
                  <span>当前筛选总数 · {readLabels[applied.readFilter]}</span>
                  <strong>{snapshot.total}</strong>
                </div>
              </section>
              <div className="message-snapshot">
                <p>
                  固定结果 <code>{snapshot.resultId}</code>
                </p>
                <p>
                  确认：{utc(snapshot.confirmedAt)}
                  <br />
                  有效至：{utc(snapshot.expiresAt)}
                </p>
                <p>
                  计数遵循类型、标题和时间筛选；列表总数另含阅读状态筛选。分页不会刷新计数。
                </p>
              </div>
              <form
                className="message-locate"
                onSubmit={(e) => {
                  e.preventDefault();
                  void read({ kind: "locate", id: locateId });
                }}
              >
                <label>
                  消息 ID 定位
                  <input
                    value={locateId}
                    maxLength={128}
                    onChange={(e) => setLocateId(e.target.value)}
                    placeholder="粘贴完整消息 ID"
                  />
                </label>
                <button disabled={!!busy || !gate[1] || !gate[2]}>定位</button>
                <span>仅定位当前固定结果</span>
              </form>
              {!snapshot.items.length ? (
                <div className="message-empty">
                  <h3>
                    {snapshot.total === 0
                      ? "没有符合筛选的消息"
                      : "本页没有消息"}
                  </h3>
                  <p>这是已确认查询的实际空结果，可调整筛选后重新确认。</p>
                </div>
              ) : (
                <ul className="message-list" aria-label="站内消息列表">
                  {snapshot.items.map((row, index) => (
                    <li key={row.messageId}>
                      <article
                        tabIndex={-1}
                        ref={(node) => {
                          if (node) rows.current.set(row.messageId, node);
                          else rows.current.delete(row.messageId);
                        }}
                        data-message-id={row.messageId}
                        className={`message-row ${highlight === row.messageId ? "message-highlight" : ""}`}
                        aria-label={`消息 ${row.messageId}`}
                      >
                        <div className="message-row-number">
                          {String(snapshot.offset + index + 1).padStart(2, "0")}
                        </div>
                        <div className="message-row-content">
                          <div className="message-row-title">
                            <h3>{row.title || "（标题为空）"}</h3>
                            <span
                              className={`message-status message-status-${row.subType}`}
                            >
                              {messageSubTypeLabels[row.subType]}
                            </span>
                            <span
                              className={
                                row.read ? "message-read" : "message-unread"
                              }
                            >
                              {row.read ? "查询时已读" : "查询时未读"}
                            </span>
                          </div>
                          <p>{row.description || "（说明为空）"}</p>
                          <dl>
                            <div>
                              <dt>消息 ID</dt>
                              <dd>{row.messageId}</dd>
                            </div>
                            <div>
                              <dt>来源任务</dt>
                              <dd>{row.taskUUID}</dd>
                            </div>
                            <div>
                              <dt>发生时间</dt>
                              <dd>{utc(row.occurredAt)}</dd>
                            </div>
                            <div>
                              <dt>入库时间</dt>
                              <dd>{utc(row.ingestedAt)}</dd>
                            </div>
                            <div>
                              <dt>类型 / 范围</dt>
                              <dd>任务结果 / 平台</dd>
                            </div>
                            <div>
                              <dt>首次已读</dt>
                              <dd>
                                {row.firstReadAt
                                  ? utc(row.firstReadAt)
                                  : "未读"}
                              </dd>
                            </div>
                          </dl>
                        </div>
                      </article>
                    </li>
                  ))}
                </ul>
              )}
              <nav className="message-pagination" aria-label="消息分页">
                <span>
                  第 {Math.floor(snapshot.offset / snapshot.limit) + 1} /{" "}
                  {Math.max(1, Math.ceil(snapshot.total / snapshot.limit))} 页 ·
                  共 {snapshot.total} 条 · 每页 {snapshot.limit} 条
                </span>
                <div>
                  <button
                    className="secondary"
                    disabled={!!busy || !gate[1] || snapshot.offset === 0}
                    onClick={() =>
                      void read({
                        kind: "page",
                        offset: Math.max(0, snapshot.offset - snapshot.limit),
                      })
                    }
                  >
                    上一页
                  </button>
                  <button
                    className="secondary"
                    disabled={
                      !!busy ||
                      !gate[1] ||
                      snapshot.offset + snapshot.limit >= snapshot.total
                    }
                    onClick={() =>
                      void read({
                        kind: "page",
                        offset: snapshot.offset + snapshot.limit,
                      })
                    }
                  >
                    下一页
                  </button>
                </div>
              </nav>
            </>
          )}
        </>
      )}
    </section>
  );
}
