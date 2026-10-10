import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import { ErrorNotice, Field } from "./access-common";
import SavedSourcePicker from "./SavedSourcePicker";
import type { ResolvedSource } from "./domain-selection-api";
import { directoryV2DisplayText as display } from "./directory-v2-json";
import type { DirectoryV2Source } from "./directory-v2-api";
import { sessionError, useTaskRead } from "./task-common";
import {
  retryUserAssetsV2Read,
  userAssetsV2API,
  userAssetsV2Error,
  userAssetsV2Operations,
  userAssetsV2PageSizes,
  validUserAssetsV2Search,
  type UserAssetV2Detail,
  type UserAssetV2Object,
  type UserAssetV2Query,
  type UserAssetsV2List,
  type UserAssetsV2Query,
} from "./user-assets-v2-api";

type Props = { profile: Profile; sessionChanged: () => void };
const accessOperations = [
  ...userAssetsV2Operations,
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
];
export default function UserAssetsV2Workspace(props: Props) {
  return (
    <Workspace
      key={`${props.profile.ID}:${props.profile.csrfToken}`}
      {...props}
    />
  );
}
function Workspace({ profile, sessionChanged }: Props) {
  const [source, setSource] = useState<ResolvedSource | null>(null);
  const [sourceGeneration, setSourceGeneration] = useState(0);
  const [pickerGeneration, setPickerGeneration] = useState(0);
  const [lostAccess, setLostAccess] = useState(false);
  const [notice, setNotice] = useState("");
  const clearSource = () => {
    setSource(null);
    setSourceGeneration((value) => value + 1);
  };
  const lost = () => {
    clearSource();
    setLostAccess(true);
    sessionChanged();
  };
  const gate = useTaskRead(
    profile.csrfToken,
    async (signal) => {
      const [menu, checks] = await Promise.all([
        accessRequest<{ menu: Permission[] }>("/menu", signal),
        accessRequest<{ results: boolean[] }>(
          "/check",
          signal,
          { paths: accessOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== accessOperations.length ||
        checks.results.some((value) => typeof value !== "boolean")
      )
        throw new ApiError("invalid_response");
      return { menu: menu.menu, checks: checks.results };
    },
    lost,
    () => false,
    userAssetsV2Error,
  );
  const readable = (mark: string) =>
    gate.data?.menu.some(
      (permission) =>
        permission?.mark === mark && permission.auth?.readable === true,
    );
  const canRead =
    !lostAccess &&
    readable("domains") &&
    readable("directory_assets") &&
    gate.data?.checks[0] === true;
  return (
    <section aria-labelledby="user-assets-v2-title">
      <h2 id="user-assets-v2-title">用户资产</h2>
      <p>
        只读展示已完成、已保存的字典 2 用户观测；不发起采集。观测不是 AD
        时间点快照，未观察到对象不表示删除。未返回表示已请求但未返回，不推断原因。
      </p>
      <ErrorNotice error={gate.error || notice} />
      {gate.busy && <p role="status">正在核对用户资产访问权限…</p>}
      {gate.data && !canRead && (
        <p role="status">当前账户没有用户资产读取权限。</p>
      )}
      {canRead && (
        <>
          {gate.data?.checks[2] && gate.data.checks[3] ? (
            <div
              onChangeCapture={() => {
                if (source) clearSource();
              }}
            >
              <SavedSourcePicker
                key={pickerGeneration}
                userID={profile.ID}
                sessionChanged={lost}
                onResolved={(value) => {
                  clearSource();
                  setSource(value);
                  setNotice("");
                }}
              />
            </div>
          ) : (
            <p role="status">当前账户无权选择已配置域。</p>
          )}
          {source && (
            <>
              <p>
                已选择域：{display(source.selection.domain)} · 配置版本{" "}
                {source.selection.revision} · 凭据版本{" "}
                {source.selection.credentialRevision}
              </p>
              <Assets
                key={sourceGeneration}
                profile={profile}
                source={source}
                canDetail={gate.data?.checks[1] === true}
                failure={(error) => {
                  clearSource();
                  setNotice(userAssetsV2Error(error));
                  if (
                    sessionError(error) ||
                    (error instanceof ApiError && error.status === 401)
                  )
                    lost();
                  else if (error instanceof ApiError && error.status === 403)
                    setLostAccess(true);
                  else setPickerGeneration((value) => value + 1);
                }}
              />
            </>
          )}
        </>
      )}
    </section>
  );
}

function Provenance({
  source,
  observationId,
}: {
  source: DirectoryV2Source;
  observationId: string;
}) {
  return (
    <dl
      className="facts"
      style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
    >
      <dt>观测编号</dt>
      <dd>{display(observationId)}</dd>
      <dt>观测服务器</dt>
      <dd>{display(source.server_name)}</dd>
      <dt>观测域控</dt>
      <dd>{display(source.dc_host_name)}</dd>
      <dt>观测域</dt>
      <dd>{display(source.domain)}</dd>
      <dt>命名上下文</dt>
      <dd>{display(source.naming_context)}</dd>
      <dt>观测开始时间（UTC）</dt>
      <dd>{display(source.started_at)}</dd>
      <dt>观测完成时间（UTC）</dt>
      <dd>{display(source.completed_at)}</dd>
      <dt>观测耗时（毫秒）</dt>
      <dd>{source.elapsed_milliseconds}</dd>
      <dt>观测读取页数</dt>
      <dd>{source.pages}</dd>
    </dl>
  );
}
const nullable = (value: string | number | null) =>
  value === null ? "未返回" : display(String(value));
function Facts({ object }: { object: UserAssetV2Object }) {
  return (
    <dl
      className="facts"
      style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
    >
      <dt>objectGUID</dt>
      <dd>{display(object.objectGUID)}</dd>
      <dt>distinguishedName</dt>
      <dd>{display(object.distinguishedName)}</dd>
      <dt>kind</dt>
      <dd>{display(object.kind)}</dd>
      <dt>objectClass</dt>
      <dd>
        <ul>
          {object.objectClass.map((value) => (
            <li key={value}>{display(value)}</li>
          ))}
        </ul>
      </dd>
      <dt>samAccountName</dt>
      <dd>{nullable(object.samAccountName)}</dd>
      <dt>userAccountControl（原始整数）</dt>
      <dd>{nullable(object.userAccountControl)}</dd>
      <dt>objectSid</dt>
      <dd>{nullable(object.objectSid)}</dd>
      <dt>mail</dt>
      <dd>{nullable(object.mail)}</dd>
      <dt>description</dt>
      <dd>
        {object.description === null ? (
          "未返回"
        ) : (
          <ul>
            <li>{display(object.description[0])}</li>
          </ul>
        )}
      </dd>
      <dt>whenCreated（UTC）</dt>
      <dd>{nullable(object.whenCreated)}</dd>
    </dl>
  );
}
function Assets({
  profile,
  source,
  canDetail,
  failure,
}: {
  profile: Profile;
  source: ResolvedSource;
  canDetail: boolean;
  failure: (error: unknown) => void;
}) {
  const initial: UserAssetsV2Query = {
    domainId: source.selection.domainId,
    expectedRevision: source.selection.revision,
    expectedCredentialRevision: source.selection.credentialRevision,
    search: "",
    pageIdx: 1,
    pageSize: 10,
  };
  const [query, setQuery] = useState(initial);
  const queryRef = useRef(initial);
  const [draft, setDraft] = useState("");
  const [list, setList] = useState<UserAssetsV2List | null>(null);
  const [error, setError] = useState("");
  const [inputError, setInputError] = useState("");
  const [busy, setBusy] = useState(true);
  const [stale, setStale] = useState(false);
  const [selected, setSelected] = useState<UserAssetV2Query | null>(null);
  const [detail, setDetail] = useState<UserAssetV2Detail | null>(null);
  const [detailBusy, setDetailBusy] = useState(false);
  const [detailError, setDetailError] = useState("");
  const listSequence = useRef(0),
    detailSequence = useRef(0);
  const listController = useRef<AbortController | null>(null),
    detailController = useRef<AbortController | null>(null);
  const listTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
      undefined,
    ),
    detailTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const origin = useRef<HTMLButtonElement | null>(null),
    panel = useRef<HTMLElement | null>(null);
  const failureRef = useRef(failure);
  failureRef.current = failure;
  const cancelDetail = () => {
    detailSequence.current++;
    detailController.current?.abort();
    clearTimeout(detailTimer.current);
  };
  const clearDetail = () => {
    cancelDetail();
    setSelected(null);
    setDetail(null);
    setDetailError("");
    setDetailBusy(false);
  };
  const cancelList = () => {
    listSequence.current++;
    listController.current?.abort();
    clearTimeout(listTimer.current);
  };
  const close = () => {
    clearDetail();
    if (origin.current?.isConnected) origin.current.focus();
  };
  // Fatal read failures invalidate BOTH independent channels before handing
  // control to the source/session owner. No aborted transport can restore data.
  const handleFailure = (reason: unknown) => {
    if (
      reason instanceof ApiError &&
      (sessionError(reason) ||
        [401, 403, 404].includes(reason.status) ||
        reason.code === "selection_changed")
    ) {
      cancelList();
      clearDetail();
      setList(null);
      failureRef.current(reason);
      return true;
    }
    if (
      reason instanceof ApiError &&
      reason.code === "directory_observation_unavailable"
    ) {
      cancelList();
      clearDetail();
      setList(null);
      setBusy(false);
      setStale(true);
      setError(userAssetsV2Error(reason));
      return true;
    }
    return false;
  };
  const loadList = (next: UserAssetsV2Query) => {
    cancelList();
    clearDetail();
    const sequence = listSequence.current;
    const controller = new AbortController();
    listController.current = controller;
    queryRef.current = next;
    setQuery(next);
    setList(null);
    setError("");
    setInputError("");
    setStale(false);
    setBusy(true);
    async function read() {
      try {
        const value = await userAssetsV2API.list(
          next,
          profile.ID,
          controller.signal,
        );
        if (sequence !== listSequence.current || controller.signal.aborted)
          return;
        // Capture a pin without re-fetching or retaining an unpinned retry.
        const pinned = value.available
          ? { ...next, observationId: value.observationId }
          : next;
        queryRef.current = pinned;
        setQuery(pinned);
        setList(value);
        setBusy(false);
        setError("");
      } catch (reason) {
        if (sequence !== listSequence.current || controller.signal.aborted)
          return;
        if (handleFailure(reason)) return;
        setList(null);
        setBusy(false);
        setError(userAssetsV2Error(reason));
        if (retryUserAssetsV2Read(reason))
          listTimer.current = setTimeout(() => {
            setBusy(true);
            void read();
          }, 2000);
      }
    }
    void read();
  };
  const loadDetail = (next: UserAssetV2Query) => {
    clearDetail();
    const sequence = detailSequence.current;
    const controller = new AbortController();
    detailController.current = controller;
    setSelected(next);
    setDetailBusy(true);
    async function read() {
      try {
        const value = await userAssetsV2API.detail(
          next,
          profile.ID,
          controller.signal,
        );
        if (sequence !== detailSequence.current || controller.signal.aborted)
          return;
        setDetail(value);
        setDetailBusy(false);
        setDetailError("");
      } catch (reason) {
        if (sequence !== detailSequence.current || controller.signal.aborted)
          return;
        if (handleFailure(reason)) return;
        setDetail(null);
        setDetailBusy(false);
        setDetailError(userAssetsV2Error(reason));
        if (retryUserAssetsV2Read(reason))
          detailTimer.current = setTimeout(() => {
            setDetailBusy(true);
            void read();
          }, 2000);
      }
    }
    void read();
  };
  useLayoutEffect(() => {
    loadList(initial);
    return () => {
      cancelList();
      cancelDetail();
    };
    // Source/actor/session changes remount Assets; effects never update its identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (selected) panel.current?.focus();
  }, [selected]);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (stale || (busy && draft === query.search)) return;
    if (!validUserAssetsV2Search(draft)) {
      setInputError(
        "搜索最多 50 个 UTF-16 单元，且不能包含无效字符；输入不会被截断。",
      );
      return;
    }
    loadList({ ...queryRef.current, search: draft, pageIdx: 1 });
  };
  const dirty = draft !== query.search;
  return (
    <section aria-label="已选数据源用户资产">
      <form className="filters" onSubmit={submit}>
        <Field
          label="用户资产关键词"
          help="按 SAM、SID、mail 或 DN 搜索；最多 50 个 UTF-16 单元，保留空白，按字面匹配"
        >
          <input
            placeholder="按 SAM、SID、mail 或 DN 搜索"
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
              setInputError("");
              clearDetail();
            }}
          />
        </Field>
        <Field label="用户资产每页条数">
          <select
            value={query.pageSize}
            disabled={busy || stale}
            onChange={(event) =>
              loadList({
                ...queryRef.current,
                pageIdx: 1,
                pageSize: Number(event.target.value),
              })
            }
          >
            {userAssetsV2PageSizes.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </Field>
        <button disabled={stale || (busy && !dirty)}>搜索用户</button>
        <button
          type="button"
          className="secondary"
          disabled={busy || stale}
          onClick={() => {
            setDraft("");
            loadList({ ...queryRef.current, search: "", pageIdx: 1 });
          }}
        >
          清除用户搜索
        </button>
      </form>
      <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
        已应用搜索：
        {query.search === "" ? "无文本筛选" : `“${display(query.search)}”`}
      </p>
      {dirty && (
        <p role="status">搜索已编辑，尚未应用；请提交搜索后查看详情或翻页。</p>
      )}
      <ErrorNotice error={inputError || error} />
      <button
        className="secondary"
        onClick={() => {
          const { observationId: _pin, ...latest } = queryRef.current;
          loadList({ ...latest, pageIdx: 1 });
        }}
      >
        刷新用户资产观测
      </button>
      {error && !stale && (
        <button
          className="secondary"
          onClick={() => loadList(queryRef.current)}
        >
          重试用户资产读取
        </button>
      )}
      {busy && <p role="status">正在读取用户资产…</p>}
      {list && !list.available && (
        <p role="status">
          当前数据源没有可用的字典 2 观测；尚不能确认用户数量。
        </p>
      )}
      {list?.available && (
        <>
          <Provenance
            source={list.source!}
            observationId={list.observationId!}
          />
          {list.list.length === 0 ? (
            <p role="status">此观测在当前筛选下没有匹配用户。</p>
          ) : (
            <div className="table-scroll">
              <table style={{ overflowWrap: "anywhere" }}>
                <caption>用户资产列表</caption>
                <thead>
                  <tr>
                    <th scope="col">SAM</th>
                    <th scope="col">GUID</th>
                    <th scope="col">DN</th>
                    <th scope="col">SID</th>
                    <th scope="col">mail</th>
                    <th scope="col">详情</th>
                  </tr>
                </thead>
                <tbody>
                  {list.list.map((row) => (
                    <tr key={row.objectGUID}>
                      <td>{nullable(row.samAccountName)}</td>
                      <td>{display(row.objectGUID)}</td>
                      <td>{display(row.distinguishedName)}</td>
                      <td>{nullable(row.objectSid)}</td>
                      <td style={{ whiteSpace: "pre-wrap" }}>
                        {nullable(row.mail)}
                      </td>
                      <td>
                        <button
                          disabled={!canDetail || dirty || busy}
                          aria-label={`查看用户 ${display(row.samAccountName ?? row.objectGUID)}`}
                          onClick={(event) => {
                            origin.current = event.currentTarget;
                            const q = queryRef.current;
                            if (!q.observationId) return;
                            loadDetail({
                              domainId: q.domainId,
                              expectedRevision: q.expectedRevision,
                              expectedCredentialRevision:
                                q.expectedCredentialRevision,
                              observationId: q.observationId,
                              objectGUID: row.objectGUID,
                            });
                          }}
                        >
                          查看详情
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <p>
            第 {list.page.pageIdx} 页 · 共 {list.page.totalPage} 页 · 匹配用户{" "}
            {list.page.total} 个
          </p>
          <div className="actions">
            <button
              aria-label="用户资产上一页"
              disabled={busy || dirty || list.page.pageIdx <= 1}
              onClick={() =>
                loadList({ ...queryRef.current, pageIdx: query.pageIdx - 1 })
              }
            >
              上一页
            </button>
            <button
              aria-label="用户资产下一页"
              disabled={
                busy ||
                dirty ||
                list.page.pageIdx >= list.page.totalPage ||
                list.page.pageIdx >= 10000
              }
              onClick={() =>
                loadList({ ...queryRef.current, pageIdx: query.pageIdx + 1 })
              }
            >
              下一页
            </button>
          </div>
        </>
      )}
      {selected && (
        <section
          aria-label="用户详情"
          ref={panel}
          tabIndex={-1}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              close();
            }
          }}
        >
          <h3>用户详情</h3>
          <button className="secondary" onClick={close}>
            关闭用户详情
          </button>
          <p>按观测与 GUID 读取；null 显示为未返回，原始整数不推导账户状态。</p>
          {detailBusy && <p role="status">正在读取用户详情…</p>}
          <ErrorNotice error={detailError} />
          {detailError && (
            <button onClick={() => loadDetail(selected)}>
              重试用户详情读取
            </button>
          )}
          {detail && (
            <>
              <Facts object={detail.object} />
              <Provenance
                source={detail.source}
                observationId={detail.observationId}
              />
            </>
          )}
        </section>
      )}
    </section>
  );
}
