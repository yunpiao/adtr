import { useEffect, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, queryString, type Permission } from "./access-api";
import {
  ErrorNotice,
  Field,
  FormActions,
  Pagination,
  ProofFields,
  getProof,
  readValues,
} from "./access-common";
import { useTaskRead, useTaskMutation } from "./task-common";
import { stateLabels, taskAPI, terminal } from "./task-api";
import DomainCredentialSource, {
  SourceRecovery,
  SourceTestPermission,
} from "./DomainCredentialSource";
import { sourceAPI, sourceLabels } from "./domain-source-api";
import { readSourceIntent } from "./domain-source-intent";
import SavedSourcePicker from "./SavedSourcePicker";
import {
  domainAPI,
  domainError,
  domainOperations,
  connectionLabels,
  connectionStates,
  diagnosticMessages,
  validDNS,
  validIP,
  validADPair,
  uncertainDomainError,
  validDomainTask,
  type Connection,
  type Diagnostic,
  type DomainOperation,
  type Receipt,
} from "./domain-api";
import {
  readDomainIntent,
  saveDomainIntent,
  discardDomainIntent,
  forgetDomainSession,
  type DomainIntent,
} from "./domain-intent";
export { discardDomainIntent } from "./domain-intent";
type Context = {
  profile: Profile;
  sessionChanged: () => void;
  can: (operation: DomainOperation) => boolean;
};
type View =
  | { type: "list" }
  | { type: "create" }
  | { type: "source-picker" }
  | {
      type: "detail" | "edit" | "delete" | "test" | "credential-source";
      id: string;
      domain?: string;
    };
export default function DomainsWorkspace(props: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  return (
    <Workspace
      key={`${props.profile.ID}:${props.profile.csrfToken}`}
      {...props}
    />
  );
}
function Workspace({
  profile,
  sessionChanged,
}: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  const [view, setView] = useState<View>({ type: "list" }),
    [version, setVersion] = useState(0),
    [notice, setNotice] = useState(""),
    [receipt, setReceipt] = useState<Receipt | null>(null);
  const lost = () => {
    forgetDomainSession();
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
          { paths: domainOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== domainOperations.length ||
        checks.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return { menu: menu.menu, checks: checks.results };
    },
    lost,
    () => false,
    domainError,
  );
  const context: Context = {
    profile,
    sessionChanged: lost,
    can: (operation) => {
      const grant = gate.data?.menu.find((n) => n.mark === "domains")?.auth;
      return (
        !!grant?.readable &&
        (!operation.startsWith("POST") || !!grant.writeable) &&
        gate.data?.checks[domainOperations.indexOf(operation)] === true
      );
    },
  };
  const open = (next: View, message = "") => {
    setView(next);
    setNotice(message);
    setVersion((n) => n + 1);
    window.history.pushState({}, "", `#domains/${next.type}`);
  };
  useEffect(() => {
    const back = () => {
      setView({ type: "list" });
      setVersion((n) => n + 1);
      setNotice("");
    };
    window.addEventListener("popstate", back);
    return () => window.removeEventListener("popstate", back);
  }, []);
  const pending = readDomainIntent(profile);
  const pendingSource = readSourceIntent(profile);
  return (
    <div className="domain-workspace">
      <h2>域连接</h2>
      <p className="warning">
        保存仅登记本地配置，不验证 AD
        连通性，也不自动授予数据权限。平台管理员同样需要显式资源组授权。
      </p>
      <ErrorNotice error={gate.error} />
      {notice && <p role="status">{notice}</p>}
      {gate.busy && <p role="status">正在确认域连接权限…</p>}
      {gate.error && <button onClick={gate.refresh}>重新读取域权限</button>}
      {gate.data && !context.can("GET /api/domains") && (
        <p role="status">服务器未授予域连接读取权限。</p>
      )}
      {gate.data && context.can("GET /api/domains") && (
        <>
          {view.type !== "list" && (
            <button
              className="secondary"
              onClick={() =>
                open(
                  { type: "list" },
                  "已离开当前页面；关闭页面不会撤销已提交的操作，也不会取消服务器检测。",
                )
              }
            >
              返回域连接列表
            </button>
          )}
          {receipt && <ReceiptNotice receipt={receipt} />}
          {pendingSource &&
            view.type !== "credential-source" &&
            context.can("GET /api/domains/credential-source/mutation") && (
              <SourceRecovery
                key={pendingSource.idempotencyKey}
                context={context}
                intent={pendingSource}
                done={(r) => {
                  setNotice(
                    `原凭据来源操作已确认；当前来源：${sourceLabels[r.currentCredentialSource]}。`,
                  );
                  setVersion((n) => n + 1);
                }}
              />
            )}
          {pending?.kind === "create" &&
            view.type !== "create" &&
            context.can("GET /api/domains/creation") && (
              <CreationRecovery
                key={pending.key}
                context={context}
                intent={pending}
                done={(value) => {
                  discardDomainIntent(context.profile);
                  setReceipt(value);
                  setVersion((n) => n + 1);
                }}
              />
            )}
          {view.type === "list" && (
            <DomainList
              key={`list-${version}`}
              context={context}
              open={open}
              blocked={!!pending}
            />
          )}
          {view.type === "source-picker" &&
            context.can("GET /api/domain-selection") &&
            context.can("GET /api/domain-selection/resolve") &&
            context.can("GET /api/domains/detail") && (
              <SavedSourcePicker
                key={`source-picker-${version}`}
                userID={profile.ID}
                sessionChanged={context.sessionChanged}
                onResolved={({ selection, checkedAt }) =>
                  open(
                    { type: "detail", id: selection.domainId },
                    `数据源核对时间（UTC）：${checkedAt}。连接详情按当前权限重新读取；检测仍需另行确认。`,
                  )
                }
              />
            )}
          {view.type === "create" &&
            context.can("POST /api/domains/create") &&
            (pending?.kind === "create" ? (
              <CreationRecovery
                context={context}
                intent={pending}
                done={(value) => {
                  discardDomainIntent(context.profile);
                  setReceipt(value);
                  open({ type: "list" });
                }}
              />
            ) : pending ? (
              <p role="alert">请先核对未确认的检测提交。</p>
            ) : (
              <EndpointForm
                key={`create-${version}`}
                context={context}
                done={(value) => {
                  if (value) setReceipt(value);
                  open(
                    { type: "list" },
                    value
                      ? value.deleted
                        ? "原登记已删除；此回执仅供核对历史。"
                        : "本地连接已保存，尚未验证。请完成显式资源分配后再手动检测。"
                      : "已停止等待；请核对原提交结果。",
                  );
                }}
              />
            ))}
          {view.type === "credential-source" &&
            "id" in view &&
            context.can("GET /api/domains/credential-source") && (
              <DomainCredentialSource
                key={`source-${view.id}-${version}`}
                context={context}
                connection={{ domainId: view.id, domain: view.domain ?? "" }}
                done={(message) =>
                  open({ type: "detail", id: view.id }, message)
                }
              />
            )}
          {["detail", "edit", "delete", "test"].includes(view.type) &&
            "id" in view && (
              <DomainDetails
                key={`${view.type}-${view.id}-${version}`}
                context={context}
                view={view as Extract<View, { id: string }>}
                open={open}
              />
            )}
        </>
      )}
    </div>
  );
}
function ReceiptNotice({ receipt }: { receipt: Receipt }) {
  return (
    <section className="notice" aria-label="本地登记回执">
      <h3>本地登记回执</h3>
      <p>
        域：{receipt.domain} · 版本：{receipt.revision}
      </p>
      <Field label="新域 ID">
        <input readOnly value={receipt.domainId} />
      </Field>
      {receipt.deleted ? (
        <p>此登记已删除；回执仅保留历史记录。</p>
      ) : (
        <p>
          下一步：在「资源与租户 → 资源组」中添加此域
          ID，并显式关联所需角色（F47）。保存不会自动授予创建者权限。
        </p>
      )}
    </section>
  );
}
function CreationRecovery({
  context,
  intent,
  done,
}: {
  context: Context;
  intent: Extract<DomainIntent, { kind: "create" }>;
  done: (receipt: Receipt) => void;
}) {
  const read = useTaskRead(
    intent.key,
    (signal) => domainAPI.receipt(intent.key, signal, context.profile.ID),
    context.sessionChanged,
    () => false,
    domainError,
  );
  useEffect(() => {
    if (read.data) done(read.data);
  }, [read.data]);
  return (
    <section>
      <h3>核对未确认的登记</h3>
      <p className="warning">
        先前提交可能已保存。未找到回执也不能证明仍在处理的提交已失败；不会生成新幂等键或重复创建。
      </p>
      <Field label="登记幂等键">
        <input readOnly value={intent.key} />
      </Field>
      <p>待核对域：{intent.domain}</p>
      <ErrorNotice error={read.error} />
      <button disabled={read.busy} onClick={read.refresh}>
        查询原登记回执
      </button>
    </section>
  );
}
function DomainList({
  context,
  open,
  blocked,
}: {
  context: Context;
  open: (v: View, m?: string) => void;
  blocked: boolean;
}) {
  const [filters, setFilters] = useState({
      filterKeyword: "",
      filterDomain: "",
      filterStatus: "",
      pageSize: "20",
      tmSort: "-1",
    }),
    [applied, setApplied] = useState(filters),
    [page, setPage] = useState(1),
    [error, setError] = useState("");
  const query = queryString({
    ...applied,
    pageIdx: page,
    filterDomain: applied.filterDomain
      ? applied.filterDomain.split(/[\s,]+/u)
      : [],
  });
  const read = useTaskRead(
    query,
    (s) => domainAPI.list(query, s),
    context.sessionChanged,
    () => false,
    domainError,
  );
  return (
    <>
      <div className="actions">
        {context.can("GET /api/domain-selection") &&
          context.can("GET /api/domain-selection/resolve") &&
          context.can("GET /api/domains/detail") && (
            <button onClick={() => open({ type: "source-picker" })}>
              选择已授权数据源
            </button>
          )}
        <button className="secondary" onClick={read.refresh}>
          刷新域连接
        </button>
        {context.can("POST /api/domains/create") && (
          <button disabled={blocked} onClick={() => open({ type: "create" })}>
            新增域连接
          </button>
        )}
      </div>
      <form
        className="filters"
        onSubmit={(e) => {
          e.preventDefault();
          if (
            Array.from(filters.filterKeyword).length > 50 ||
            (filters.filterDomain &&
              filters.filterDomain.split(/[\s,]+/u).some((v) => !validDNS(v)))
          ) {
            setError("关键词最多 50 个字符；域筛选须为有效 DNS 域名。");
            return;
          }
          setError("");
          setApplied(filters);
          setPage(1);
        }}
      >
        <Field label="域或域控关键词">
          <input
            value={filters.filterKeyword}
            onChange={(e) =>
              setFilters({ ...filters, filterKeyword: e.target.value })
            }
          />
        </Field>
        <Field label="域名筛选" help="完整域名，多个以逗号分隔">
          <input
            value={filters.filterDomain}
            onChange={(e) =>
              setFilters({ ...filters, filterDomain: e.target.value })
            }
          />
        </Field>
        <Field label="连接状态">
          <select
            value={filters.filterStatus}
            onChange={(e) =>
              setFilters({ ...filters, filterStatus: e.target.value })
            }
          >
            <option value="">全部状态</option>
            {connectionStates.map((s) => (
              <option key={s} value={s}>
                {connectionLabels[s]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="每页条数">
          <select
            value={filters.pageSize}
            onChange={(e) =>
              setFilters({ ...filters, pageSize: e.target.value })
            }
          >
            {[10, 20, 30, 40, 50].map((n) => (
              <option key={n}>{n}</option>
            ))}
          </select>
        </Field>
        <Field label="创建时间排序">
          <select
            value={filters.tmSort}
            onChange={(e) => setFilters({ ...filters, tmSort: e.target.value })}
          >
            <option value="-1">最新优先</option>
            <option value="1">最早优先</option>
          </select>
        </Field>
        <button>查询域连接</button>
        <button
          type="button"
          className="secondary"
          onClick={() => {
            const reset = {
              filterKeyword: "",
              filterDomain: "",
              filterStatus: "",
              pageSize: "20",
              tmSort: "-1",
            };
            setFilters(reset);
            setApplied(reset);
            setPage(1);
            setError("");
          }}
        >
          重置筛选
        </button>
      </form>
      <ErrorNotice error={error || read.error} />
      {read.busy && <p role="status">正在读取域连接…</p>}
      {read.data && (
        <>
          {read.data.List.length === 0 ? (
            <p role="status">
              当前筛选下没有获授权的域连接。新登记需要显式资源组分配。
            </p>
          ) : (
            <div className="table-scroll">
              <table>
                <caption>获授权的域连接</caption>
                <thead>
                  <tr>
                    <th>域名</th>
                    <th>配置的域控</th>
                    <th>加密连接</th>
                    <th>状态</th>
                    <th>上次检测（UTC）</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {read.data.List.map((c) => (
                    <tr key={c.domainId}>
                      <td>{c.domain}</td>
                      <td>{c.dcHostName}</td>
                      <td>{mode(c)}</td>
                      <td>
                        {connectionLabels[c.connectionState]}
                        {c.lastDiagnostic && c.lastDiagnostic.code !== "ok" && (
                          <small>
                            {diagnosticMessages[c.lastDiagnostic.code]}
                          </small>
                        )}
                      </td>
                      <td>
                        {c.lastDiagnostic?.observedAt ?? "尚无当前版本终态结果"}
                      </td>
                      <td>
                        {context.can("GET /api/domains/detail") && (
                          <button
                            className="secondary"
                            onClick={() =>
                              open({ type: "detail", id: c.domainId })
                            }
                          >
                            查看 {c.domain}
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <Pagination
            page={read.data.page}
            exhausted={read.data.exhausted}
            busy={read.busy}
            change={setPage}
          />
        </>
      )}
    </>
  );
}
const mode = (c: Pick<Connection, "port">) =>
  c.port === "389" ? "LDAP + StartTLS · 389" : "LDAPS · 636";
function EndpointForm({
  context,
  connection,
  done,
}: {
  context: Context;
  connection?: Connection;
  done: (receipt?: Receipt) => void;
}) {
  const [createSource, setCreateSource] = useState<"custom" | "unconfigured">(
    "custom",
  );
  const [replace, setReplace] = useState(false),
    [sensitiveVersion, setSensitiveVersion] = useState(0),
    [mustReview, setMustReview] = useState(false);
  const mutation = useTaskMutation(context.sessionChanged, domainError);
  const [key] = useState(() => crypto.randomUUID());
  const submit = (e: FormEvent<HTMLFormElement>) => {
    const v = readValues(e);
    if (mutation.busy || mustReview) return;
    const proof = getProof(v);
    if (!proof) {
      mutation.setError("请输入有效的操作者密码和六位未使用验证码。");
      return;
    }
    if (
      (!connection && !validDNS(v.domain)) ||
      !validDNS(v.dcHostName) ||
      !validIP(v.ldapAddr) ||
      !["389", "636"].includes(v.port)
    ) {
      mutation.setError(
        "请输入完整 DNS 域名、域控 DNS 名称及可选的单个 IP；仅支持 389 StartTLS 或 636 LDAPS。",
      );
      return;
    }
    if (
      ((!connection && createSource === "custom") || replace) &&
      !validADPair(v.username, v.password)
    ) {
      mutation.setError(
        "AD 用户名须为 DOMAIN\\user 或 user@domain；用户名和密码各需 1–50 个字符。密码不会修剪。",
      );
      return;
    }
    setSensitiveVersion((n) => n + 1);
    const endpoint = {
      dcHostName: v.dcHostName,
      ldapAddr: v.ldapAddr,
      port: v.port,
    };
    if (connection) {
      void mutation.run(
        (signal) =>
          domainAPI.update(
            {
              ...endpoint,
              domainId: connection.domainId,
              expectedRevision: connection.revision,
              ...(replace
                ? { username: v.username, password: v.password }
                : {}),
            },
            proof,
            context.profile.csrfToken,
            signal,
          ),
        () => done(),
        (error) => {
          if (
            uncertainDomainError(error) ||
            (error instanceof ApiError &&
              ["revision_conflict", "credential_source_changed"].includes(
                error.code,
              ))
          )
            setMustReview(true);
        },
      );
    } else {
      saveDomainIntent(context.profile, {
        kind: "create",
        key,
        domain: v.domain,
      });
      void mutation.run(
        async (signal): Promise<Receipt> => {
          const result =
            createSource === "unconfigured"
              ? await sourceAPI.createUnconfigured(
                  { ...endpoint, domain: v.domain, idempotencyKey: key },
                  proof,
                  context.profile.csrfToken,
                  context.profile.ID,
                  signal,
                )
              : await domainAPI.create(
                  {
                    ...endpoint,
                    domain: v.domain,
                    username: v.username,
                    password: v.password,
                    idempotencyKey: key,
                  },
                  proof,
                  context.profile.csrfToken,
                  signal,
                );
          if (result.replayed)
            return domainAPI.receipt(key, signal, context.profile.ID);
          return {
            domainId: result.domainId,
            domain: v.domain.toLowerCase().replace(/\.$/u, ""),
            revision: result.revision,
            deleted: false,
            requiresResourceAssignment: true,
          };
        },
        (receipt) => {
          discardDomainIntent(context.profile);
          done(receipt);
        },
        (error) => {
          if (uncertainDomainError(error)) {
            setMustReview(true);
          } else discardDomainIntent(context.profile);
        },
      );
    }
  };
  return (
    <form onSubmit={submit} autoComplete="off">
      <h3>{connection ? "编辑域连接" : "登记域连接"}</h3>
      <p>
        证书始终按域控 DNS
        名称验证。可先登记未配置凭据的域，再显式分配资源、登记操作账户及授权，最后选择引用来源。
      </p>
      <ErrorNotice error={mutation.error} />
      {mustReview && (
        <p role="alert">请返回列表，重新读取配置或查询原登记回执后再操作。</p>
      )}
      <fieldset disabled={mutation.busy || mustReview}>
        {!connection &&
          context.can("POST /api/domains/create-unconfigured") && (
            <Field label="初始凭据来源">
              <select
                value={createSource}
                onChange={(e) => {
                  setCreateSource(e.target.value as "custom" | "unconfigured");
                  setSensitiveVersion((n) => n + 1);
                }}
              >
                <option value="custom">自定义凭据</option>
                <option value="unconfigured">
                  暂不配置凭据（稍后引用操作账户）
                </option>
              </select>
            </Field>
          )}
        <Field label="域 DNS 名称" help="创建后不可重命名">
          <input
            name="domain"
            defaultValue={connection?.domain ?? ""}
            readOnly={!!connection}
            required
            autoComplete="off"
          />
        </Field>
        <Field
          label="域控 DNS 名称"
          help="TLS 证书身份，即使填写 IP 也必须提供"
        >
          <input
            name="dcHostName"
            defaultValue={connection?.dcHostName ?? ""}
            required
            autoComplete="off"
          />
        </Field>
        <Field
          label="连接 IP（可选）"
          help="留空时按部署允许策略解析 DNS；不接受 URL 或端口"
        >
          <input
            name="ldapAddr"
            defaultValue={connection?.ldapAddr ?? ""}
            autoComplete="off"
          />
        </Field>
        <Field label="加密连接方式">
          <select name="port" defaultValue={connection?.port ?? "389"}>
            <option value="389">LDAP + StartTLS · 389</option>
            <option value="636">LDAPS · 636</option>
          </select>
        </Field>
        {connection && (
          <>
            <p>
              已保存凭据：{connection.credentialConfigured ? "是" : "否"} ·
              凭据版本：{connection.credentialRevision} · 配置版本：
              {connection.revision}
            </p>
            {(!connection.credentialSource ||
              connection.credentialSource === "custom") && (
              <label>
                <input
                  type="checkbox"
                  checked={replace}
                  onChange={(e) => {
                    setReplace(e.target.checked);
                    setSensitiveVersion((n) => n + 1);
                  }}
                />
                替换凭据
              </label>
            )}
            <p>
              {!connection.credentialSource ||
              connection.credentialSource === "custom"
                ? "不替换时保留已保存的用户名和密码。替换必须重新输入两项。"
                : "此处仅编辑连接端点。更换凭据须进入凭据来源页面明确选择。"}
            </p>
          </>
        )}
        <div key={sensitiveVersion}>
          {((!connection && createSource === "custom") || replace) && (
            <>
              <Field
                label="AD 用户名"
                help="DOMAIN\user 或 user@domain，最多 50 个字符"
              >
                <input name="username" autoComplete="off" required />
              </Field>
              <Field
                label="AD 密码"
                help="1–50 个字符，保留原始空格；不回显已保存值"
              >
                <input
                  name="password"
                  type="password"
                  autoComplete="new-password"
                  required
                />
              </Field>
            </>
          )}
          <ProofFields />
        </div>
      </fieldset>
      <FormActions
        busy={mutation.busy}
        disabled={mustReview}
        cancel={() => done()}
      >
        {connection ? "保存域修改" : "保存本地连接"}
      </FormActions>
    </form>
  );
}
function DomainDetails({
  context,
  view,
  open,
}: {
  context: Context;
  view: Extract<View, { id: string }>;
  open: (view: View, m?: string) => void;
}) {
  const read = useTaskRead(
    view.id,
    (s) => domainAPI.detail(view.id, s),
    context.sessionChanged,
    (c) => c.connectionState === "testing",
    domainError,
  );
  const c = read.data;
  return (
    <>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取域详情…</p>}
      <button className="secondary" onClick={read.refresh}>
        重新读取域详情
      </button>
      {c && (
        <>
          {view.type === "edit" && context.can("POST /api/domains/update") ? (
            <EndpointForm
              key={c.revision}
              context={context}
              connection={c}
              done={() =>
                open(
                  { type: "detail", id: c.domainId },
                  "请核对服务器配置；旧版本检测不再作为当前验证结果。",
                )
              }
            />
          ) : view.type === "delete" &&
            context.can("POST /api/domains/delete") ? (
            <DeleteConnection
              key={c.revision}
              context={context}
              connection={c}
              done={() =>
                open({ type: "list" }, "本地连接已删除；远程 AD 未被删除。")
              }
            />
          ) : view.type === "test" && context.can("POST /api/domains/test") ? (
            c.credentialSource ? (
              <SourceTestPermission context={context} connection={c}>
                <TestConnection
                  key={c.revision}
                  context={context}
                  connection={c}
                />
              </SourceTestPermission>
            ) : (
              <TestConnection
                key={c.revision}
                context={context}
                connection={c}
              />
            )
          ) : (
            <>
              <h3>域连接详情：{c.domain}</h3>
              <dl>
                {[
                  ["域 ID", c.domainId],
                  ["配置域控", c.dcHostName],
                  ["连接 IP", c.ldapAddr || "按部署允许策略解析 DNS"],
                  ["加密连接", mode(c)],
                  ["配置版本", c.revision],
                  ["凭据版本", c.credentialRevision],
                  ["已保存凭据", c.credentialConfigured ? "是" : "否"],
                  ["凭据来源", sourceLabels[c.credentialSource ?? "custom"]],
                  ["连接状态", connectionLabels[c.connectionState]],
                  ["创建时间（UTC）", c.createdAt],
                  ["更新时间（UTC）", c.updatedAt],
                ].map(([k, v]) => (
                  <div key={k}>
                    <dt>{k}</dt>
                    <dd>{v}</dd>
                  </div>
                ))}
              </dl>
              <DiagnosticSummary diagnostic={c.lastDiagnostic} />
              <p className="muted">
                通过仅表示该时间点的 TLS、绑定和 rootDSE
                观察；不表示持续在线、AD 管理权限或同步已就绪。
              </p>
              <div className="actions">
                {context.can("GET /api/domains/credential-source") && (
                  <button
                    onClick={() =>
                      open({
                        type: "credential-source",
                        id: c.domainId,
                        domain: c.domain,
                      })
                    }
                  >
                    管理凭据来源
                  </button>
                )}
                {context.can("POST /api/domains/update") && (
                  <button
                    onClick={() => open({ type: "edit", id: c.domainId })}
                  >
                    编辑域连接
                  </button>
                )}
                {context.can("POST /api/domains/test") && (
                  <button
                    onClick={() => open({ type: "test", id: c.domainId })}
                  >
                    检测已保存连接
                  </button>
                )}
                {context.can("POST /api/domains/delete") && (
                  <button
                    className="danger"
                    onClick={() => open({ type: "delete", id: c.domainId })}
                  >
                    删除域连接
                  </button>
                )}
              </div>
              {c.latestTaskUUID &&
                context.can("GET /api/domains/test-result") && (
                  <TestObservation
                    key={c.latestTaskUUID}
                    context={context}
                    connection={c}
                    id={c.latestTaskUUID}
                  />
                )}
            </>
          )}
        </>
      )}
    </>
  );
}
function DiagnosticSummary({ diagnostic }: { diagnostic: Diagnostic | null }) {
  return diagnostic ? (
    <section aria-label="安全诊断">
      <p>{diagnosticMessages[diagnostic.code]}</p>
      <p>
        检测时间（UTC）：{diagnostic.observedAt} · 总用时：
        {diagnostic.elapsedMilliseconds} 毫秒
      </p>
      <p>
        阶段：{diagnostic.stage} · 配置版本：{diagnostic.revision} · 凭据版本：
        {diagnostic.credentialRevision}
      </p>
      {diagnostic.dcHostName && <p>已验证域控：{diagnostic.dcHostName}</p>}
    </section>
  ) : (
    <p>尚无当前版本的终态诊断证据。</p>
  );
}
function TestConnection({
  context,
  connection: c,
}: {
  context: Context;
  connection: Connection;
}) {
  const [intent, setIntent] = useState(() => readDomainIntent(context.profile)),
    [secretVersion, setSecretVersion] = useState(0);
  const mutation = useTaskMutation(context.sessionChanged, domainError);
  const matching =
    intent?.kind === "test" && intent.domainId === c.domainId ? intent : null;
  const blocked = !!intent && !matching;
  const [id, setID] = useState(
    matching?.taskUUID ||
      (c.connectionState === "testing" ? c.latestTaskUUID : ""),
  );
  return (
    <>
      <h3>检测已保存连接：{c.domain}</h3>
      <p>
        仅检测已保存版本 {c.revision} · {mode(c)}
        。不会测试未保存的表单。每次手动检测最多绑定一次，可能影响 AD
        失败登录计数；失败或结果不确定均不会自动重试。
      </p>
      {blocked && <p role="alert">请先核对其他未确认的域操作。</p>}
      {matching && matching.expectedRevision !== c.revision && (
        <section className="warning">
          <p>
            原检测针对版本 {matching.expectedRevision}，当前已是版本{" "}
            {c.revision}。原结果不能验证当前配置；切换不会取消原服务器任务。
          </p>
          <button
            type="button"
            onClick={() => {
              discardDomainIntent(context.profile);
              setIntent(null);
              setID("");
              setSecretVersion((n) => n + 1);
            }}
          >
            改为检测当前保存版本
          </button>
        </section>
      )}
      {matching && !matching.taskUUID && (
        <p className="warning">
          原检测提交结果尚未确认；使用原幂等键和新验证码核对，不会自动创建新检测。
        </p>
      )}
      {!id && (
        <form
          onSubmit={(e) => {
            const v = readValues(e);
            if (mutation.busy || blocked) return;
            const proof = getProof(v);
            if (!proof) {
              mutation.setError("请输入有效的操作者密码和六位未使用验证码。");
              return;
            }
            const chosen = matching ?? {
              kind: "test" as const,
              key: crypto.randomUUID(),
              domainId: c.domainId,
              expectedRevision: c.revision,
              idempotencyKey: "",
            };
            chosen.idempotencyKey = chosen.key;
            saveDomainIntent(context.profile, chosen);
            setIntent({ ...chosen });
            setSecretVersion((n) => n + 1);
            void mutation.run(
              (signal) =>
                domainAPI.test(
                  {
                    domainId: chosen.domainId,
                    expectedRevision: chosen.expectedRevision,
                    idempotencyKey: chosen.key,
                  },
                  proof,
                  context.profile.csrfToken,
                  signal,
                ),
              (result) => {
                const next = { ...chosen, taskUUID: result.task.taskUUID };
                saveDomainIntent(context.profile, next);
                setIntent(next);
                setID(result.task.taskUUID);
              },
              (error) => {
                if (!uncertainDomainError(error)) {
                  discardDomainIntent(context.profile);
                  setIntent(null);
                }
              },
            );
          }}
        >
          <ErrorNotice error={mutation.error} />
          {matching && (
            <Field label="检测幂等键">
              <input readOnly value={matching.key} />
            </Field>
          )}
          <fieldset key={secretVersion} disabled={mutation.busy || blocked}>
            <ProofFields />
          </fieldset>
          <button disabled={mutation.busy || blocked}>
            {mutation.busy
              ? "正在提交…"
              : matching
                ? "使用原幂等键核对检测"
                : "确认检测保存版本"}
          </button>
        </form>
      )}
      {id && (
        <TestObservation
          key={id}
          context={context}
          connection={c}
          id={id}
          newTest={() => {
            discardDomainIntent(context.profile);
            setIntent(null);
            setID("");
          }}
        />
      )}
    </>
  );
}
function TestObservation({
  context,
  connection: c,
  id,
  newTest,
}: {
  context: Context;
  connection: Connection;
  id: string;
  newTest?: () => void;
}) {
  const read = useTaskRead(
    id,
    (s) => domainAPI.result(id, c.domainId, s),
    context.sessionChanged,
    (r) => !terminal(r.task.state),
    domainError,
  );
  const [cancel, setCancel] = useState(false),
    [secretVersion, setSecretVersion] = useState(0);
  const mutation = useTaskMutation(context.sessionChanged, domainError),
    result = read.data,
    task = result?.task;
  useEffect(() => {
    if (task && terminal(task.state)) {
      const intent = readDomainIntent(context.profile);
      if (intent?.kind === "test" && intent.taskUUID === id)
        discardDomainIntent(context.profile);
    }
  }, [task?.state, id]);
  return (
    <section aria-label="连接检测任务">
      <h4>连接检测任务</h4>
      <p>任务 ID：{id}</p>
      <ErrorNotice error={read.error || mutation.error} />
      <button className="secondary" onClick={read.refresh}>
        刷新检测结果
      </button>
      {task && (
        <>
          <p role="status">{stateLabels[task.state]}</p>
          {!terminal(task.state) && (
            <p>正在自动核对服务器终态。关闭页面仅停止等待，不会取消检测。</p>
          )}
          {task.state === "cancel_requested" && (
            <p className="warning">
              取消已请求，执行器尚未确认停止；请等待真实终态。
            </p>
          )}
          {task.error && (
            <p>
              {diagnosticMessages[task.error] ??
                "检测未得到可用结果，请核对服务器状态。"}
            </p>
          )}
          {task.state === "succeeded" && !result?.diagnostic && (
            <p className="warning">
              任务已结束，但当前版本的诊断证据不可用，不能确认连接通过。
            </p>
          )}
          {result?.diagnostic &&
          (result.diagnostic.revision !== c.revision ||
            result.diagnostic.credentialRevision !== c.credentialRevision) ? (
            <p className="warning">此检测属于旧版本，不能作为当前连接验证。</p>
          ) : (
            <DiagnosticSummary diagnostic={result?.diagnostic ?? null} />
          )}
          {terminal(task.state) && (
            <>
              <p>再次检测需要新的明确提交；请先修正凭据或证书错误。</p>
              {newTest && <button onClick={newTest}>开始新的手动检测</button>}
            </>
          )}
          {!terminal(task.state) &&
            task.state !== "cancel_requested" &&
            context.can("POST /api/tasks/cancel") &&
            !cancel && (
              <button
                className="secondary danger"
                onClick={() => setCancel(true)}
              >
                请求取消检测
              </button>
            )}
          {cancel &&
            !terminal(task.state) &&
            task.state !== "cancel_requested" && (
              <form
                onSubmit={(e) => {
                  const v = readValues(e);
                  if (mutation.busy) return;
                  const proof = getProof(v);
                  if (!proof) {
                    mutation.setError(
                      "请输入有效的操作者密码和六位未使用验证码。",
                    );
                    return;
                  }
                  setSecretVersion((n) => n + 1);
                  void mutation.run(
                    async (signal) => {
                      const value = await taskAPI.cancel(
                        id,
                        proof,
                        context.profile.csrfToken,
                        signal,
                      );
                      if (
                        !validDomainTask(value) ||
                        value.domainId !== c.domainId
                      )
                        throw new ApiError("invalid_response");
                      return value;
                    },
                    () => {
                      setCancel(false);
                      read.refresh();
                    },
                    () => read.refresh(),
                  );
                }}
              >
                <fieldset key={secretVersion} disabled={mutation.busy}>
                  <ProofFields />
                </fieldset>
                <FormActions
                  busy={mutation.busy}
                  cancel={() => setCancel(false)}
                >
                  确认请求取消检测
                </FormActions>
              </form>
            )}
        </>
      )}
    </section>
  );
}
function DeleteConnection({
  context,
  connection: c,
  done,
}: {
  context: Context;
  connection: Connection;
  done: () => void;
}) {
  const mutation = useTaskMutation(context.sessionChanged, domainError),
    [secretVersion, setSecretVersion] = useState(0),
    [uncertain, setUncertain] = useState(false);
  return (
    <form
      onSubmit={(e) => {
        const v = readValues(e);
        if (mutation.busy || uncertain) return;
        const proof = getProof(v);
        if (!proof || v.confirmDomain !== c.domain) {
          mutation.setError("请完整输入域名和有效的新鲜操作验证。");
          return;
        }
        setSecretVersion((n) => n + 1);
        void mutation.run(
          (signal) =>
            domainAPI.delete(
              {
                domainId: c.domainId,
                expectedRevision: c.revision,
                confirmDomain: v.confirmDomain,
              },
              proof,
              context.profile.csrfToken,
              signal,
            ),
          done,
          () => setUncertain(true),
        );
      }}
    >
      <h3>删除本地域连接</h3>
      <p className="warning">
        删除 {c.domain} 的本地配置、已保存凭据和域资源关联。远程 AD
        对象、账户、Agent 均不受此操作删除；正在执行的外部连接不保证立即停止。
      </p>
      <p>配置版本：{c.revision}</p>
      <ErrorNotice error={mutation.error} />
      {uncertain && (
        <p role="alert">
          删除结果尚未确认或配置已改变。请返回列表重新核对；此表单不会重复提交。
        </p>
      )}
      <fieldset disabled={mutation.busy || uncertain}>
        <Field label="输入域名确认删除">
          <input name="confirmDomain" required autoComplete="off" />
        </Field>
        <div key={secretVersion}>
          <ProofFields />
        </div>
      </fieldset>
      <button className="danger" disabled={mutation.busy || uncertain}>
        确认删除本地连接
      </button>
    </form>
  );
}
