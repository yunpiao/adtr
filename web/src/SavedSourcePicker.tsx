import { useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { ErrorNotice, Field, Pagination } from "./access-common";
import { connectionStates, diagnosticMessages } from "./domain-api";
import {
  sourceAPI,
  sourceError,
  validSourceKeyword,
  type ResolvedSource,
  type SourceChoice,
  type SourcePage,
  type SourceQuery,
} from "./domain-selection-api";
import { sessionError } from "./task-common";

export const observationLabels = {
  unverified: "无匹配版本的历史检测记录",
  testing: "已提交检测，尚无终态记录",
  verified: "历史检测通过",
  error: "历史检测未通过",
};
const defaults: SourceQuery = {
  pageIdx: 1,
  pageSize: 20,
  keyword: "",
  observationState: "",
};
// Mount only within a domains-readable consumer with exact list/resolve checks.
// Session identity changes must remount it; nothing is persisted in the browser.
export default function SavedSourcePicker({
  onResolved,
  sessionChanged,
  userID,
}: {
  onResolved: (value: ResolvedSource) => void;
  sessionChanged: () => void;
  userID: number;
}) {
  const [filters, setFilters] = useState(defaults),
    [query, setQuery] = useState(defaults),
    [refresh, setRefresh] = useState(0),
    [data, setData] = useState<SourcePage | null>(null),
    [selected, setSelected] = useState<SourceChoice | null>(null),
    [busy, setBusy] = useState(true),
    [resolving, setResolving] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const listSequence = useRef(0),
    resolveSequence = useRef(0),
    listController = useRef<AbortController | null>(null),
    resolveController = useRef<AbortController | null>(null),
    callbacks = useRef({ onResolved, sessionChanged });
  callbacks.current = { onResolved, sessionChanged };
  const cancelResolve = () => {
    resolveSequence.current++;
    resolveController.current?.abort();
    resolveController.current = null;
  };
  const clearChoice = () => {
    cancelResolve();
    setSelected(null);
    setResolving(false);
  };
  const reload = (next = query) => {
    clearChoice();
    listSequence.current++;
    listController.current?.abort();
    setData(null);
    setBusy(true);
    setQuery(next);
    setRefresh((n) => n + 1);
  };
  useEffect(() => {
    const sequence = ++listSequence.current,
      controller = new AbortController();
    listController.current = controller;
    clearChoice();
    setBusy(true);
    setError("");
    setData(null);
    void sourceAPI.list(query, controller.signal, userID).then(
      (value) => {
        if (sequence !== listSequence.current || controller.signal.aborted)
          return;
        setData(value);
        setBusy(false);
      },
      (failure: unknown) => {
        if (sequence !== listSequence.current || controller.signal.aborted)
          return;
        clearChoice();
        setData(null);
        setBusy(false);
        setNotice("");
        if (sessionError(failure)) callbacks.current.sessionChanged();
        else setError(sourceError(failure));
      },
    );
    return () => {
      listSequence.current++;
      controller.abort();
      cancelResolve();
    };
  }, [query, refresh, userID]);
  const resolve = async () => {
    if (!selected || resolving || busy || resolveController.current) return;
    const sequence = ++resolveSequence.current,
      controller = new AbortController(),
      choice = selected;
    resolveController.current = controller;
    setResolving(true);
    setError("");
    setNotice("");
    try {
      const value = await sourceAPI.resolve(choice, controller.signal, userID);
      if (sequence !== resolveSequence.current || controller.signal.aborted)
        return;
      // The consuming workflow reads its own current detail and still enforces
      // its existing permissions, revisions and fresh proof for any mutation.
      clearChoice();
      callbacks.current.onResolved(value);
    } catch (failure) {
      if (sequence !== resolveSequence.current || controller.signal.aborted)
        return;
      clearChoice();
      setData(null);
      if (sessionError(failure)) callbacks.current.sessionChanged();
      else if (
        failure instanceof ApiError &&
        failure.status === 409 &&
        failure.code === "selection_changed"
      ) {
        setNotice(sourceError(failure));
        reload();
      } else setError(sourceError(failure));
    }
  };
  const changeFilter = (next: SourceQuery) => {
    clearChoice();
    setNotice("");
    setError("");
    setFilters(next);
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    clearChoice();
    if (!validSourceKeyword(filters.keyword)) {
      setError("关键词最多 50 个字符，且不能包含控制字符。");
      return;
    }
    setNotice("");
    reload({ ...filters, pageIdx: 1 });
  };
  return (
    <section aria-labelledby="source-picker-title">
      <h3 id="source-picker-title">选择已授权数据源</h3>
      <p>
        这里只列出当前获授权的已保存连接配置；配置域控不是域控资产清单。
        检测记录仅表示所示时间点的历史观察，不代表当前在线、许可证或采集器状态。
      </p>
      <form className="filters" onSubmit={submit}>
        <Field
          label="数据源关键词"
          help="按配置的域名或域控名称搜索，最多 50 个字符"
        >
          <input
            value={filters.keyword}
            onChange={(e) =>
              changeFilter({ ...filters, keyword: e.target.value })
            }
          />
        </Field>
        <Field label="历史检测状态">
          <select
            value={filters.observationState}
            onChange={(e) =>
              changeFilter({
                ...filters,
                observationState: e.target
                  .value as SourceQuery["observationState"],
              })
            }
          >
            <option value="">全部历史状态</option>
            {connectionStates.map((state) => (
              <option key={state} value={state}>
                {observationLabels[state]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="数据源每页条数">
          <select
            value={filters.pageSize}
            onChange={(e) =>
              changeFilter({ ...filters, pageSize: Number(e.target.value) })
            }
          >
            {[10, 20, 30, 40, 50].map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </Field>
        <button>查询数据源</button>
        <button
          type="button"
          className="secondary"
          onClick={() => {
            setFilters(defaults);
            setNotice("");
            reload(defaults);
          }}
        >
          重置数据源筛选
        </button>
      </form>
      <ErrorNotice error={error} />
      {notice && <p role="status">{notice}</p>}
      <button
        className="secondary"
        onClick={() => {
          setNotice("");
          reload();
        }}
      >
        刷新已授权数据源
      </button>
      {busy && <p role="status">正在读取已授权数据源…</p>}
      {data && (
        <>
          {data.List.length === 0 ? (
            <p role="status">当前筛选下没有已授权数据源。</p>
          ) : (
            <div className="table-scroll">
              <table>
                <caption>已授权的保存连接配置</caption>
                <thead>
                  <tr>
                    <th>选择</th>
                    <th>域名</th>
                    <th>配置的域控与地址</th>
                    <th>配置版本 / 凭据版本</th>
                    <th>历史检测状态</th>
                    <th>历史检测时间（UTC）</th>
                  </tr>
                </thead>
                <tbody>
                  {data.List.map((choice) => (
                    <tr key={choice.domainId}>
                      <td>
                        <input
                          type="radio"
                          name="saved-source"
                          aria-label={`选择数据源 ${choice.domain}`}
                          checked={selected?.domainId === choice.domainId}
                          onChange={() => {
                            clearChoice();
                            setSelected(choice);
                            setError("");
                            setNotice("");
                          }}
                        />
                      </td>
                      <td>
                        {choice.domain}
                        <small>已保存连接配置</small>
                      </td>
                      <td>
                        {choice.dcHostName}
                        <small>配置 IP：{choice.ldapAddr || "未配置"}</small>
                        <small>
                          {choice.mode === "starttls"
                            ? "LDAP + StartTLS"
                            : "LDAPS"}{" "}
                          · {choice.port}
                        </small>
                        <small>
                          已保存凭据：
                          {choice.credentialConfigured ? "是" : "否"}
                        </small>
                      </td>
                      <td>
                        {choice.revision} / {choice.credentialRevision}
                      </td>
                      <td>
                        {observationLabels[choice.connectionState]}
                        {choice.lastTest && (
                          <small>
                            历史结果：{diagnosticMessages[choice.lastTest.code]}
                          </small>
                        )}
                      </td>
                      <td>
                        {choice.lastTest?.observedAt ??
                          "暂无匹配版本的终态记录"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <Pagination
            page={data.page}
            exhausted={data.exhausted || data.page.pageIdx >= 1000000}
            busy={busy}
            change={(pageIdx) => {
              setNotice("");
              reload({ ...query, pageIdx });
            }}
          />
        </>
      )}
      <div className="actions">
        <button
          disabled={!selected || busy || resolving}
          onClick={() => void resolve()}
        >
          {resolving ? "正在核对数据源…" : "核对并打开连接详情"}
        </button>
        {selected && (
          <button className="secondary" onClick={clearChoice}>
            清除数据源选择
          </button>
        )}
      </div>
    </section>
  );
}
