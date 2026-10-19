import ComputerAssetExportV3 from "./ComputerAssetExportV3";
import { readExportRecovery } from "./computer-asset-export-v3-recovery";
import { directoryV3DisplaySPN } from "./directory-v3-facts";
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
import "./user-assets-soc.css";
import type { ResolvedSource } from "./domain-selection-api";
import { directoryV2DisplayText as display } from "./directory-v2-json";
import type { DirectoryV2Source } from "./directory-v2-api";
import { sessionError, useTaskRead } from "./task-common";
import {
  validUserAssetsV2Filters,
  type UserAssetsV2Filters,
} from "./user-assets-v2-filters";
import {
  retryComputerAssetsV3Read,
  computerAssetsV3API,
  computerAssetsV3Error,
  computerAssetsV3Operations,
  computerAssetsV3PageSizes,
  validComputerAssetsV3Search,
  type ComputerAssetV3Object,
  type ComputerAssetsV3List,
  type ComputerAssetsV3Query,
} from "./computer-assets-v3-api";

type Props = { profile: Profile; sessionChanged: () => void };
const emptyFilters = {
  startTm: "",
  endTm: "",
  creatTm: "" as "" | "asc" | "desc",
};
const rawFilterLabels = {
  search: "计算机资产关键词",
  startTm: "创建时间开始（含）",
  endTm: "创建时间结束（不含）",
};
type RawFilterKey = keyof typeof rawFilterLabels;
const accessOperations = [
  ...computerAssetsV3Operations,
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
];
export default function ComputerAssetsV3Workspace(props: Props) {
  return (
    <Workspace
      key={`${props.profile.ID}:${props.profile.csrfToken}`}
      {...props}
    />
  );
}
function Workspace({ profile, sessionChanged }: Props) {
  const recovery = readExportRecovery(profile.ID);
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
    computerAssetsV3Error,
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
  const canExport = !!readable("computer_asset_exports") && !!readable("tasks");
  return (
    <section
      className="user-assets-workspace"
      aria-labelledby="computer-assets-v3-title"
    >
      <div className="asset-page-heading">
        <p className="asset-eyebrow">DIRECTORY &amp; IDENTITY</p>
        <h2 id="computer-assets-v3-title">计算机资产</h2>
        <p>检索字典 3 已保存的目录计算机，核对身份字段与观测来源。</p>
      </div>
      <details className="asset-data-help">
        <summary>如何理解这些数据</summary>
        <p>
          数据来自已完成并保存的目录观测。观测不是 AD
          时间点快照，未观察到对象不表示删除。“未返回”表示已请求但未返回，不推断原因。
        </p>
        <p id="asset-search-help">
          按 SAM、SID 或 DN 搜索；最多 50 个 UTF-16 单元，保留空白，按字面匹配。
        </p>
      </details>
      <ErrorNotice error={gate.error || notice} />
      {!source && (
        <p role="status">
          字典
          3：请选择并核对数据源。切换版本或返回页面后需重新选择，尚未查询计算机。
        </p>
      )}
      {gate.busy && <p role="status">正在核对计算机资产访问权限…</p>}
      {gate.data && !canRead && (
        <p role="status">当前账户没有计算机资产读取权限。</p>
      )}
      {canRead && (
        <>
          {gate.data?.checks[1] && gate.data.checks[2] ? (
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
          {!source && recovery && canExport && (
            <ComputerAssetExportV3
              profile={profile}
              query={{ domainId: recovery.domainId, expectedRevision: "", expectedCredentialRevision: "", search: "", pageIdx: 1, pageSize: 10 }}
              disabled={true}
              failure={lost}
            />
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
                canExport={canExport}
                failure={(error) => {
                  clearSource();
                  setNotice(computerAssetsV3Error(error));
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
function Facts({ object }: { object: ComputerAssetV3Object }) {
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
  failure,
  canExport,
}: {
  profile: Profile;
  source: ResolvedSource;
  canExport: boolean;
  failure: (error: unknown) => void;
}) {
  const initial: ComputerAssetsV3Query = {
    domainId: source.selection.domainId,
    expectedRevision: source.selection.revision,
    expectedCredentialRevision: source.selection.credentialRevision,
    search: "",
    pageIdx: 1,
    pageSize: 10,
  };
  const [query, setQuery] = useState(initial),
    queryRef = useRef(initial);
  const [draft, setDraft] = useState(""),
    [filters, setFilters] = useState(emptyFilters);
  const keywordInput = useRef<HTMLInputElement>(null);
  const [list, setList] = useState<ComputerAssetsV3List | null>(null),
    [expanded, setExpanded] = useState<string | null>(null);
  const [error, setError] = useState(""),
    [inputError, setInputError] = useState(""),
    [busy, setBusy] = useState(true),
    [stale, setStale] = useState(false);
  const [transfers, setTransfers] = useState<
    Partial<Record<RawFilterKey, string>>
  >({});
  const sequence = useRef(0),
    controller = useRef<AbortController | null>(null),
    timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const failureRef = useRef(failure);
  failureRef.current = failure;
  const cancel = () => {
    sequence.current++;
    controller.current?.abort();
    clearTimeout(timer.current);
  };
  const load = (next: ComputerAssetsV3Query) => {
    cancel();
    setExpanded(null);
    const current = sequence.current,
      abort = new AbortController();
    controller.current = abort;
    queryRef.current = next;
    setQuery(next);
    setList(null);
    setError("");
    setInputError("");
    setBusy(true);
    setStale(false);
    async function read() {
      try {
        const value = await computerAssetsV3API.list(
          next,
          profile.ID,
          abort.signal,
        );
        if (current !== sequence.current || abort.signal.aborted) return;
        const pinned = value.available
          ? { ...next, observationId: value.observationId }
          : next;
        queryRef.current = pinned;
        setQuery(pinned);
        setList(value);
        setBusy(false);
        setError("");
      } catch (reason) {
        if (current !== sequence.current || abort.signal.aborted) return;
        setList(null);
        setExpanded(null);
        setBusy(false);
        if (
          reason instanceof ApiError &&
          (sessionError(reason) ||
            [401, 403, 404].includes(reason.status) ||
            reason.code === "selection_changed")
        ) {
          cancel();
          failureRef.current(reason);
          return;
        }
        setError(computerAssetsV3Error(reason));
        if (
          reason instanceof ApiError &&
          reason.code === "directory_observation_unavailable"
        ) {
          cancel();
          setStale(true);
          return;
        }
        if (retryComputerAssetsV3Read(reason))
          timer.current = setTimeout(() => {
            setBusy(true);
            void read();
          }, 2000);
      }
    }
    void read();
  };
  useLayoutEffect(() => {
    load(initial);
    return cancel;
  }, []);
  const transferError = Object.values(transfers).join(" ");
  const dirty =
    !!transferError ||
    draft !== query.search ||
    (Object.keys(emptyFilters) as (keyof typeof emptyFilters)[]).some(
      (k) => filters[k] !== (query[k] ?? ""),
    );
  const guard = (key: RawFilterKey) => {
    const reject = (raw: string, event: { preventDefault: () => void }) => {
      if (!/[\p{Cc}]/u.test(raw)) return;
      event.preventDefault();
      setTransfers((x) => ({
        ...x,
        [key]: `${rawFilterLabels[key]}含控制字符，已拒绝粘贴或拖入，请重新编辑该字段。`,
      }));
      setExpanded(null);
    };
    return {
      "aria-invalid": !!transfers[key],
      onPaste: (e: ClipboardEvent<HTMLInputElement>) =>
        reject(e.clipboardData.getData("text/plain"), e),
      onDrop: (e: DragEvent<HTMLInputElement>) =>
        reject(e.dataTransfer.getData("text/plain"), e),
    };
  };
  const edit = (key: RawFilterKey, value: string) => {
    if (key === "search") setDraft(value);
    else setFilters((x) => ({ ...x, [key]: value }));
    setTransfers((x) => {
      const n = { ...x };
      delete n[key];
      return n;
    });
    setInputError("");
    setExpanded(null);
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (stale || transferError || (busy && !dirty)) return;
    const applied: Omit<UserAssetsV2Filters, "sAMAccountName"> = {
      startTm: filters.startTm || undefined,
      endTm: filters.endTm || undefined,
      creatTm: filters.creatTm || undefined,
    };
    if (
      !validComputerAssetsV3Search(draft) ||
      !validUserAssetsV2Filters(applied)
    ) {
      setInputError(
        "请核对搜索长度与成对 RFC3339 创建时间；开始须早于结束，输入不会被截断。",
      );
      return;
    }
    load({ ...queryRef.current, ...applied, search: draft, pageIdx: 1 });
  };
  return (
    <section className="asset-queue" aria-label="已选数据源计算机资产">
      <div className="asset-queue-heading">
        <h3>计算机目录</h3>
        <button
          type="button"
          className="secondary asset-refresh"
          aria-label="刷新计算机资产观测"
          onClick={() => {
            const { observationId: _pin, ...next } = queryRef.current;
            load({ ...next, pageIdx: 1 });
          }}
        >
          刷新观测
        </button>
      </div>
      <form className="asset-filter-form" onSubmit={submit}>
        <Field label="计算机资产关键词">
          <input
            ref={keywordInput}
            aria-label="计算机资产关键词"
            value={draft}
            onChange={(e) => edit("search", e.target.value)}
            {...guard("search")}
            placeholder="搜索 SAM、SID 或 DN"
          />
        </Field>
        <button
          type="button"
          className="secondary"
          aria-label="清除计算机资产关键词"
          disabled={stale || (draft === "" && !transfers.search)}
          onClick={() => {
            edit("search", "");
            keywordInput.current?.focus();
          }}
        >
          清除关键词
        </button>
        <details>
          <summary>创建时间筛选与排序</summary>
          <div className="form-grid">
            <Field label="创建时间开始（含）">
              <input
                aria-label="创建时间开始（含）"
                value={filters.startTm}
                onChange={(e) => edit("startTm", e.target.value)}
                {...guard("startTm")}
                placeholder="2026-01-01T00:00:00Z"
              />
            </Field>
            <Field label="创建时间结束（不含）">
              <input
                aria-label="创建时间结束（不含）"
                value={filters.endTm}
                onChange={(e) => edit("endTm", e.target.value)}
                {...guard("endTm")}
                placeholder="2026-02-01T00:00:00Z"
              />
            </Field>
            <Field label="创建时间排序">
              <select
                aria-label="创建时间排序"
                value={filters.creatTm}
                onChange={(e) => {
                  setFilters((x) => ({
                    ...x,
                    creatTm: e.target.value as "" | "asc" | "desc",
                  }));
                  setExpanded(null);
                }}
              >
                <option value="">GUID 升序</option>
                <option value="asc">创建时间升序</option>
                <option value="desc">创建时间降序</option>
              </select>
            </Field>
          </div>
        </details>
        <button disabled={stale || !!transferError || (busy && !dirty)}>
          应用计算机筛选
        </button>
        <button
          type="button"
          className="secondary"
          disabled={stale}
          onClick={() => {
            setDraft("");
            setFilters(emptyFilters);
            setTransfers({});
            load({
              ...queryRef.current,
              search: "",
              startTm: undefined,
              endTm: undefined,
              creatTm: undefined,
              pageIdx: 1,
            });
          }}
        >
          清除计算机筛选
        </button>
      </form>
      {dirty && <p role="status">筛选尚未应用；分页和事实展开已暂停。</p>}
      <ErrorNotice error={inputError || transferError || error} />
      {busy && <p role="status">正在读取计算机观测…</p>}
      {stale && <p role="status">原观测已不可用，请明确刷新观测。</p>}
      {list && !list.available && (
        <p role="status">暂无可读取的已完成观测，尚不能确定计算机数量。</p>
      )}
      {canExport && (
        <ComputerAssetExportV3 profile={profile} query={query} disabled={dirty || busy || stale || !list?.available} failure={failure} />
      )}
      {list?.available && (
        <>
          <p className="asset-count">
            仅该次观测中的计算机：{list.observedComputerCount} · 匹配：
            {list.page.total}
          </p>
          <details className="asset-data-help">
            <summary>观测来源 · {display(list.observationId!)}</summary>
            <Provenance
              source={list.source!}
              observationId={list.observationId!}
            />
          </details>
          {list.observedComputerCount === 0 ? (
            <p role="status">本次观测未返回计算机对象。</p>
          ) : list.page.total === 0 ? (
            <p role="status">本次观测有计算机，但没有符合当前筛选的结果。</p>
          ) : list.list.length === 0 ? (
            <p role="status">此页没有结果，请返回前一页。</p>
          ) : (
            <div className="table-scroll">
              <table aria-label="计算机资产列表">
                <thead>
                  <tr>
                    <th scope="col">本页序号</th>
                    <th>账户名（SAM）</th>
                    <th>安全标识（SID）</th>
                    <th>创建时间（UTC）</th>
                    <th>目录路径（DN）</th>
                    <th>事实</th>
                  </tr>
                </thead>
                <tbody>
                  {list.list.map((row, index) => (
                    <ComputerRow
                      key={row.objectGUID}
                      row={row}
                      pageRowNumber={index + 1}
                      expanded={expanded === row.objectGUID && !dirty}
                      disabled={dirty || busy}
                      toggle={() =>
                        setExpanded((x) =>
                          x === row.objectGUID ? null : row.objectGUID,
                        )
                      }
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className="pagination">
            <label>
              每页{" "}
              <select
                aria-label="计算机每页条数"
                disabled={dirty || busy}
                value={query.pageSize}
                onChange={(e) =>
                  load({
                    ...queryRef.current,
                    pageIdx: 1,
                    pageSize: Number(e.target.value),
                  })
                }
              >
                {computerAssetsV3PageSizes.map((n) => (
                  <option key={n}>{n}</option>
                ))}
              </select>
            </label>
            <button
              type="button"
              disabled={dirty || busy || query.pageIdx <= 1}
              onClick={() =>
                load({ ...queryRef.current, pageIdx: query.pageIdx - 1 })
              }
            >
              上一页
            </button>
            <span>
              第 {query.pageIdx} 页 · 共 {list.page.totalPage} 页
            </span>
            <button
              type="button"
              disabled={dirty || busy || query.pageIdx >= list.page.totalPage}
              onClick={() =>
                load({ ...queryRef.current, pageIdx: query.pageIdx + 1 })
              }
            >
              下一页
            </button>
          </div>
        </>
      )}
    </section>
  );
}
function ComputerRow({
  row,
  pageRowNumber,
  expanded,
  disabled,
  toggle,
}: {
  row: ComputerAssetV3Object;
  pageRowNumber: number;
  expanded: boolean;
  disabled: boolean;
  toggle: () => void;
}) {
  return (
    <>
      <tr>
        <td>{pageRowNumber}</td>
        <td>{nullable(row.samAccountName)}</td>
        <td>{nullable(row.objectSid)}</td>
        <td>{nullable(row.whenCreated)}</td>
        <td>{display(row.distinguishedName)}</td>
        <td>
          <button
            type="button"
            disabled={disabled}
            aria-expanded={expanded}
            aria-controls={`computer-facts-${row.objectGUID}`}
            onClick={toggle}
          >
            {expanded ? "收起事实" : "展开事实"}
          </button>
        </td>
      </tr>
      {expanded && (
        <tr id={`computer-facts-${row.objectGUID}`}>
          <td colSpan={6}>
            <Facts object={row} />
          </td>
        </tr>
      )}
    </>
  );
}
