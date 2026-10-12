import { directoryV3DisplaySPN } from "./directory-v3-facts";
import AssetDictionaryNavigation from "./AssetDictionaryNavigation";
import {
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
  type ClipboardEvent,
  type DragEvent,
} from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import { ErrorNotice, Field } from "./access-common";
import SavedSourcePicker from "./SavedSourcePicker";
import AssetDetailDrawer from "./AssetDetailDrawer";
import "./user-assets-soc.css";
import type { ResolvedSource } from "./domain-selection-api";
import { directoryV2DisplayText as display } from "./directory-v2-json";
import type { DirectoryV2Source } from "./directory-v2-api";
import { sessionError, useTaskRead } from "./task-common";
import {
  validUserAssetsV2SAM,
  validUserAssetsV2Filters,
  type UserAssetsV2Filters,
} from "./user-assets-v2-filters";
import {
  retryUserAssetsV3Read,
  userAssetsV3API,
  userAssetsV3Error,
  userAssetsV3Operations,
  userAssetsV3PageSizes,
  validUserAssetsV3Search,
  type UserAssetV3Detail,
  type UserAssetV3Object,
  type UserAssetV3Query,
  type UserAssetsV3List,
  type UserAssetsV3Query,
} from "./user-assets-v3-api";

type Props = { profile: Profile; sessionChanged: () => void };
const emptyFilters = {
  sAMAccountName: "",
  startTm: "",
  endTm: "",
  creatTm: "" as "" | "asc" | "desc",
};
const rawFilterLabels = {
  search: "用户资产关键词",
  sAMAccountName: "精确账户名（SAM）",
  startTm: "创建时间开始（含）",
  endTm: "创建时间结束（不含）",
};
type RawFilterKey = keyof typeof rawFilterLabels;
const accessOperations = [
  ...userAssetsV3Operations,
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
];
export default function UserAssetsV3Workspace(props: Props) {
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
  const [sourcePickerOpen, setSourcePickerOpen] = useState(true);
  const clearSource = () => {
    setSource(null);
    setSourcePickerOpen(true);
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
    userAssetsV3Error,
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
    <section
      className="user-assets-workspace"
      aria-labelledby="user-assets-v3-title"
    >
      <div className="asset-page-heading">
        <p className="asset-eyebrow">DIRECTORY &amp; IDENTITY</p>
        <h2 id="user-assets-v3-title">用户资产</h2>
        <p>检索字典 3 已保存的目录用户，核对身份字段与观测来源。</p>
        <AssetDictionaryNavigation version={3} />
      </div>
      <details className="asset-data-help">
        <summary>如何理解这些数据</summary>
        <p>
          数据来自已完成并保存的目录观测。观测不是 AD
          时间点快照，未观察到对象不表示删除。“未返回”表示已请求但未返回，不推断原因。
        </p>
        <p id="asset-search-help">
          按 SAM、SID、mail 或 DN 搜索；最多 50 个 UTF-16
          单元，保留空白，按字面匹配。
        </p>
      </details>
      <ErrorNotice error={gate.error || notice} />
      {!source && (
        <p role="status">
          字典
          3：请选择并核对数据源。切换版本或返回页面后需重新选择，尚未查询用户。
        </p>
      )}
      {gate.busy && <p role="status">正在核对用户资产访问权限…</p>}
      {gate.data && !canRead && (
        <p role="status">当前账户没有用户资产读取权限。</p>
      )}
      {canRead && (
        <>
          {gate.data?.checks[2] && gate.data.checks[3] ? (
            <div className="asset-source-card">
              <div className="asset-source-heading">
                <div>
                  <span className="asset-eyebrow">数据源</span>
                  <strong>
                    {source ? display(source.selection.domain) : "选择已授权域"}
                  </strong>
                </div>
                {source && (
                  <button
                    type="button"
                    className="secondary"
                    aria-expanded={sourcePickerOpen}
                    aria-controls="asset-source-picker"
                    onClick={() => setSourcePickerOpen(!sourcePickerOpen)}
                  >
                    {sourcePickerOpen ? "收起数据源选择" : "更换数据源"}
                  </button>
                )}
              </div>
              <div
                id="asset-source-picker"
                hidden={!sourcePickerOpen}
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
                    setSourcePickerOpen(false);
                    setNotice("");
                  }}
                />
              </div>
            </div>
          ) : (
            <p role="status">当前账户无权选择已配置域。</p>
          )}
          {source && (
            <>
              <p className="asset-source-revisions">
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
                  setNotice(userAssetsV3Error(error));
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
      <dt>采集字典版本</dt>
      <dd>3</dd>
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
function Facts({ object }: { object: UserAssetV3Object }) {
  return (
    <dl
      className="facts"
      style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
    >
      <dt>对象 GUID</dt>
      <dd>{display(object.objectGUID)}</dd>
      <dt>目录路径（DN）</dt>
      <dd>{display(object.distinguishedName)}</dd>
      <dt>对象类型</dt>
      <dd>{display(object.kind)}</dd>
      <dt>目录对象类</dt>
      <dd>
        <ul>
          {object.objectClass.map((value) => (
            <li key={value}>{display(value)}</li>
          ))}
        </ul>
      </dd>
      <dt>账户名（SAM）</dt>
      <dd>{nullable(object.samAccountName)}</dd>
      <dt>账户控制值（原始整数）</dt>
      <dd>{nullable(object.userAccountControl)}</dd>
      <dt>安全标识（SID）</dt>
      <dd>{nullable(object.objectSid)}</dd>
      <dt>电子邮箱</dt>
      <dd>{nullable(object.mail)}</dd>
      <dt>描述</dt>
      <dd>
        {object.description === null ? (
          "未返回"
        ) : (
          <ul>
            <li>{display(object.description[0])}</li>
          </ul>
        )}
      </dd>
      <dt>创建时间（UTC）</dt>
      <dd>{nullable(object.whenCreated)}</dd>
      <dt>主组 RID（primaryGroupID）</dt>
      <dd>{nullable(object.primaryGroupID)}</dd>
      <dt>服务主体名称（SPN）</dt>
      <dd>
        {object.servicePrincipalName === null ? (
          "未返回"
        ) : (
          <div>
            <p>
              共 {object.servicePrincipalName.length}{" "}
              项，按接收顺序展示；顺序不表示目录优先级。
            </p>
            <p>
              控制字符和双向文本控制符仅在展示时转义为 Unicode
              编号；原反斜线显示为双反斜线，原值保持不变。
            </p>
            <ol
              className="asset-spn-values"
              aria-label="完整 SPN 原始值（展示转义）"
              tabIndex={0}
            >
              {object.servicePrincipalName.map((value, index) => (
                <li key={index}>{directoryV3DisplaySPN(value)}</li>
              ))}
            </ol>
          </div>
        )}
      </dd>
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
  const initial: UserAssetsV3Query = {
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
  const [filterDraft, setFilterDraft] = useState(emptyFilters);
  const [filterTransferErrors, setFilterTransferErrors] = useState<
    Partial<Record<RawFilterKey, string>>
  >({});
  const [list, setList] = useState<UserAssetsV3List | null>(null);
  const [error, setError] = useState("");
  const [inputError, setInputError] = useState("");
  const [busy, setBusy] = useState(true);
  const [stale, setStale] = useState(false);
  const [selected, setSelected] = useState<UserAssetV3Query | null>(null);
  const [detail, setDetail] = useState<UserAssetV3Detail | null>(null);
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
  const origin = useRef<HTMLButtonElement | null>(null);
  const searchInput = useRef<HTMLInputElement | null>(null);
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
      setError(userAssetsV3Error(reason));
      return true;
    }
    return false;
  };
  const loadList = (next: UserAssetsV3Query) => {
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
        const value = await userAssetsV3API.list(
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
        setError(userAssetsV3Error(reason));
        if (retryUserAssetsV3Read(reason))
          listTimer.current = setTimeout(() => {
            setBusy(true);
            void read();
          }, 2000);
      }
    }
    void read();
  };
  const loadDetail = (next: UserAssetV3Query) => {
    clearDetail();
    const sequence = detailSequence.current;
    const controller = new AbortController();
    detailController.current = controller;
    setSelected(next);
    setDetailBusy(true);
    async function read() {
      try {
        const value = await userAssetsV3API.detail(
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
        setDetailError(userAssetsV3Error(reason));
        if (retryUserAssetsV3Read(reason))
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
    searchInput.current?.focus();
    return () => {
      cancelList();
      cancelDetail();
    };
    // Source/actor/session changes remount Assets; effects never update its identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (stale || transferError || (busy && !dirty)) return;
    if (!validUserAssetsV3Search(draft)) {
      setInputError(
        "搜索最多 50 个 UTF-16 单元，且不能包含无效字符；输入不会被截断。",
      );
      return;
    }
    if (!validUserAssetsV2SAM(filterDraft.sAMAccountName)) {
      setInputError(
        "精确 SAM 最多 256 个 UTF-16 单元、1024 个 UTF-8 字节，且不能包含无效字符；输入不会被截断。",
      );
      return;
    }
    const filters: UserAssetsV2Filters = {
      sAMAccountName: filterDraft.sAMAccountName || undefined,
      startTm: filterDraft.startTm || undefined,
      endTm: filterDraft.endTm || undefined,
      creatTm: filterDraft.creatTm || undefined,
    };
    if (!validUserAssetsV2Filters(filters)) {
      setInputError(
        "创建时间须同时填写起止值，使用含 T 和 Z 或时区偏移的 RFC3339 时间（可含 1–9 位小数秒），年份及换算后 UTC 年份为 0001–9999，且开始必须早于结束；不填写则不限制时间。",
      );
      return;
    }
    loadList({ ...queryRef.current, ...filters, search: draft, pageIdx: 1 });
  };
  const transferError = Object.values(filterTransferErrors).join(" ");
  const dirty =
    !!transferError ||
    draft !== query.search ||
    (Object.keys(emptyFilters) as (keyof typeof emptyFilters)[]).some(
      (key) => filterDraft[key] !== (query[key] ?? ""),
    );
  const changeFilter = (key: keyof typeof emptyFilters, value: string) => {
    setFilterDraft((current) => ({ ...current, [key]: value }));
    setFilterTransferErrors((current) => {
      const next = { ...current };
      delete next[key as RawFilterKey];
      return next;
    });
    setInputError("");
    clearDetail();
  };
  // Native single-line inputs strip CR/LF before onChange. Refuse the transfer
  // before insertion so a raw keyword/account can never silently become another query
  // (or an empty predicate). Preserve the block until this field is edited.
  const rejectControlTransfer = (
    key: RawFilterKey,
    raw: string,
    event: { preventDefault: () => void },
  ) => {
    if (!/[\p{Cc}]/u.test(raw)) return;
    event.preventDefault();
    setFilterTransferErrors((current) => ({
      ...current,
      [key]: `${rawFilterLabels[key]}含控制字符，已拒绝粘贴或拖入，原输入未改变；请重新编辑该字段或清除用户筛选。`,
    }));
    clearDetail();
  };
  const rawInputGuards = (key: RawFilterKey) => ({
    "aria-invalid": !!filterTransferErrors[key],
    onPaste: (event: ClipboardEvent<HTMLInputElement>) =>
      rejectControlTransfer(
        key,
        event.clipboardData.getData("text/plain"),
        event,
      ),
    onDrop: (event: DragEvent<HTMLInputElement>) =>
      rejectControlTransfer(
        key,
        event.dataTransfer.getData("text/plain"),
        event,
      ),
  });
  return (
    <section className="asset-queue" aria-label="已选数据源用户资产">
      <div className="asset-queue-heading">
        <h3>用户目录</h3>
        {list?.available && (
          <span className="asset-count">{list.page.total} 个匹配用户</span>
        )}
        <button
          type="button"
          className="secondary asset-refresh"
          aria-label="刷新用户资产观测"
          onClick={() => {
            const { observationId: _pin, ...latest } = queryRef.current;
            loadList({ ...latest, pageIdx: 1 });
          }}
        >
          刷新观测
        </button>
      </div>
      <form className="filters asset-search-toolbar" onSubmit={submit}>
        <Field label="用户资产关键词">
          <input
            ref={searchInput}
            {...rawInputGuards("search")}
            aria-describedby="asset-search-help"
            placeholder="按 SAM、SID、mail 或 DN 搜索"
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
              setFilterTransferErrors((current) => {
                const next = { ...current };
                delete next.search;
                return next;
              });
              setInputError("");
              clearDetail();
            }}
          />
        </Field>
        <label className="field asset-page-size">
          <span>每页条数</span>
          <select
            aria-label="用户资产每页条数"
            value={query.pageSize}
            disabled={busy || stale || dirty}
            onChange={(event) =>
              loadList({
                ...queryRef.current,
                pageIdx: 1,
                pageSize: Number(event.target.value),
              })
            }
          >
            {userAssetsV3PageSizes.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </label>
        <button disabled={stale || !!transferError || (busy && !dirty)}>
          搜索用户
        </button>
        <button
          type="button"
          className="secondary"
          disabled={busy || stale}
          onClick={() => {
            setDraft("");
            setFilterDraft(emptyFilters);
            setFilterTransferErrors({});
            loadList({
              ...queryRef.current,
              search: "",
              pageIdx: 1,
              sAMAccountName: undefined,
              startTm: undefined,
              endTm: undefined,
              creatTm: undefined,
            });
          }}
        >
          清除用户筛选
        </button>
        <details
          className="asset-data-help"
          style={{ flexBasis: "100%", margin: 0, minWidth: 0 }}
        >
          <summary>精确账户与创建时间筛选</summary>
          <p id="asset-exact-sam-help">
            SAM 区分大小写，按原始 UTF-8 字节精确匹配，保留空白，不做 Unicode
            规范化；空值不限制。最多 256 个 UTF-16 单元、1024 个 UTF-8 字节。
          </p>
          <Field label="精确账户名（SAM）">
            <input
              {...rawInputGuards("sAMAccountName")}
              aria-describedby="asset-exact-sam-help"
              value={filterDraft.sAMAccountName}
              onChange={(event) =>
                changeFilter("sAMAccountName", event.target.value)
              }
            />
          </Field>
          <p id="asset-created-range-help">
            起止时间须成对填写，按 UTC
            比较，包含开始、不含结束；只筛选目录创建时间（whenCreated），缺失时间不匹配。使用
            RFC3339，例如 2026-10-01T00:00:00Z 或
            2026-10-01T08:00:00+08:00，可含 1–9 位小数秒。
          </p>
          <Field label="创建时间开始（含）">
            <input
              {...rawInputGuards("startTm")}
              aria-describedby="asset-created-range-help"
              placeholder="2026-10-01T00:00:00Z"
              value={filterDraft.startTm}
              onChange={(event) => changeFilter("startTm", event.target.value)}
            />
          </Field>
          <Field label="创建时间结束（不含）">
            <input
              {...rawInputGuards("endTm")}
              aria-describedby="asset-created-range-help"
              placeholder="2026-10-02T00:00:00Z"
              value={filterDraft.endTm}
              onChange={(event) => changeFilter("endTm", event.target.value)}
            />
          </Field>
          <Field label="创建时间排序">
            <select
              aria-describedby="asset-created-sort-help"
              value={filterDraft.creatTm}
              onChange={(event) => changeFilter("creatTm", event.target.value)}
            >
              <option value="">默认 GUID 升序</option>
              <option value="asc">创建时间升序</option>
              <option value="desc">创建时间降序</option>
            </select>
          </Field>
          <p id="asset-created-sort-help">
            时间排序始终将未返回的时间放在最后，同一时间按 GUID
            升序。所有筛选同时生效；点击“搜索用户”应用，保留当前观测。
          </p>
        </details>
      </form>
      <p
        className="asset-applied-search"
        style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
      >
        已应用搜索：
        {query.search === "" ? "无文本筛选" : `“${display(query.search)}”`}
      </p>
      {(query.sAMAccountName || query.startTm || query.creatTm) && (
        <p
          className="asset-applied-search"
          style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
        >
          已应用筛选：SAM{" "}
          {query.sAMAccountName ? `“${display(query.sAMAccountName)}”` : "不限"}
          {" · 创建时间 "}
          {query.startTm
            ? `[${display(query.startTm)}, ${display(query.endTm!)})`
            : "不限"}
          {" · 排序 "}
          {query.creatTm === "asc"
            ? "创建时间升序"
            : query.creatTm === "desc"
              ? "创建时间降序"
              : "GUID 升序"}
        </p>
      )}
      {dirty && (
        <p role="status">筛选已编辑，尚未应用；请提交搜索后查看详情或翻页。</p>
      )}
      <ErrorNotice error={transferError || inputError || error} />
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
          当前数据源没有可用的字典 3 观测；尚不能确认用户数量。
        </p>
      )}
      {list?.available && (
        <>
          <div className="asset-observation-bar">
            <span>观测完成（UTC）：{display(list.source!.completed_at)}</span>
            <details className="asset-provenance">
              <summary>观测来源 · {display(list.observationId!)}</summary>
              <Provenance
                source={list.source!}
                observationId={list.observationId!}
              />
            </details>
          </div>
          {list.list.length === 0 ? (
            <p role="status">此观测在当前筛选下没有匹配用户。</p>
          ) : (
            <div className="table-scroll asset-table-scroll">
              <table
                className="asset-table"
                style={{ overflowWrap: "anywhere" }}
              >
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
                      <td data-label="SAM" className="asset-name">
                        {nullable(row.samAccountName)}
                      </td>
                      <td data-label="GUID" className="asset-identifier">
                        {display(row.objectGUID)}
                      </td>
                      <td data-label="DN">{display(row.distinguishedName)}</td>
                      <td data-label="SID" className="asset-identifier">
                        {nullable(row.objectSid)}
                      </td>
                      <td data-label="mail" style={{ whiteSpace: "pre-wrap" }}>
                        {nullable(row.mail)}
                      </td>
                      <td data-label="操作">
                        <button
                          className="asset-detail-button"
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
          <p className="asset-page-count">
            第 {list.page.pageIdx} 页 · 共 {list.page.totalPage} 页 · 匹配用户{" "}
            {list.page.total} 个
          </p>
          <div className="actions asset-pagination">
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
        <AssetDetailDrawer
          close={close}
          returnFocus={() => {
            if (
              origin.current?.isConnected &&
              !origin.current.closest("[hidden], [inert]")
            )
              origin.current.focus();
          }}
        >
          <section
            className="asset-detail-content"
            aria-label="用户详情"
            tabIndex={-1}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                close();
              }
            }}
          >
            <div className="asset-detail-header">
              <div
                className="asset-detail-identity"
                tabIndex={0}
                role="region"
                aria-label="用户身份"
              >
                <p className="asset-eyebrow">DIRECTORY OBJECT / 用户</p>
                <h3>
                  {detail
                    ? display(
                        detail.object.samAccountName ??
                          detail.object.objectGUID,
                      )
                    : "用户详情"}
                </h3>
                <small>{display(source.selection.domain)}</small>
              </div>
              <button
                className="secondary"
                aria-label="关闭用户详情"
                onClick={close}
              >
                关闭
              </button>
            </div>
            <div className="asset-detail-body">
              <p className="asset-fact-note">
                固定观测内的原始身份字段。“未返回”与空值、零值分别保留；账户控制值不推导当前账户状态。
              </p>
              {detailBusy && <p role="status">正在读取用户详情…</p>}
              <ErrorNotice error={detailError} />
              {detailError && (
                <button onClick={() => loadDetail(selected)}>
                  重试用户详情读取
                </button>
              )}
              {detail && (
                <>
                  <h4>身份字段</h4>
                  <Facts object={detail.object} />
                  <h4>观测来源</h4>
                  <Provenance
                    source={detail.source}
                    observationId={detail.observationId}
                  />
                </>
              )}
            </div>
            <div className="asset-detail-footer">
              <span>只读观测</span>
              <button className="secondary" onClick={close}>
                返回用户列表
              </button>
            </div>
          </section>
        </AssetDetailDrawer>
      )}
    </section>
  );
}
