import { useEffect, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { CredentialUsePanel } from "./CredentialUseWorkspace";
import { DirectoryCredentialUsePanel } from "./DirectoryCredentialUseWorkspace";
import { DirectoryV2CredentialUsePanel } from "./DirectoryV2CredentialUseWorkspace";
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
import { validADPair, validDNS } from "./domain-api";
import { useTaskRead, useTaskMutation } from "./task-common";
import {
  operationAccountAPI,
  operationAccountError,
  operationAccountOperations,
  validAccountLabel,
  uncertainOperationError,
  type AccountDomain,
  type OperationAccount,
  type OperationAccountOperation,
  type OperationReceipt,
} from "./operation-account-api";
import {
  discardOperationAccountIntent,
  readOperationAccountIntent,
  saveOperationAccountIntent,
  type OperationAccountIntent,
} from "./operation-account-intent";
export { discardOperationAccountIntent } from "./operation-account-intent";
type Context = {
  profile: Profile;
  sessionChanged: () => void;
  can: (operation: OperationAccountOperation) => boolean;
};
type View =
  | { type: "list" }
  | { type: "create" }
  | { type: "detail" | "edit" | "delete"; id: string };
const accountName = (account: OperationAccount) =>
  account.label || account.accountId.slice(0, 12);
export default function OperationAccountsWorkspace(props: {
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
    [receipt, setReceipt] = useState<OperationReceipt | null>(null),
    [lostAccess, setLostAccess] = useState(false);
  const lost = () => {
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
          { paths: operationAccountOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== operationAccountOperations.length ||
        checks.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return { menu: menu.menu, checks: checks.results };
    },
    lost,
    () => false,
    operationAccountError,
  );
  const context: Context = {
    profile,
    sessionChanged: lost,
    can: (operation) => {
      const grant = gate.data?.menu.find(
        (entry) => entry.mark === "operation_accounts",
      )?.auth;
      return (
        !lostAccess &&
        !!grant?.readable &&
        (!operation.startsWith("POST") || !!grant.writeable) &&
        gate.data?.checks[operationAccountOperations.indexOf(operation)] ===
          true
      );
    },
  };
  const open = (next: View, message = "") => {
    setView(next);
    setNotice(message);
    setVersion((value) => value + 1);
    window.history.pushState({}, "", `#operation-accounts/${next.type}`);
  };
  useEffect(() => {
    const back = () => {
      setView({ type: "list" });
      setVersion((value) => value + 1);
      setNotice("");
    };
    window.addEventListener("popstate", back);
    return () => window.removeEventListener("popstate", back);
  }, []);
  const pending = readOperationAccountIntent(profile);
  const complete = (value: OperationReceipt) => {
    discardOperationAccountIntent(profile);
    setReceipt(value);
    open(
      value.deleted
        ? { type: "list" }
        : { type: "detail", id: value.accountId },
      value.deleted
        ? "已确认原操作回执；当前本地登记已删除。"
        : value.replayed
          ? "已确认原操作提交成功，正在重新读取当前记录。"
          : "本地登记已保存，凭据尚未验证。",
    );
  };
  return (
    <div className="operation-accounts-workspace">
      <h2>管理操作账户</h2>
      <p className="warning">
        登记已有域账户的凭据，供本地配置管理。保存后状态为「已保存，未验证」；此页面不创建、改密或删除远程
        AD 账户。
      </p>
      <ErrorNotice error={gate.error} />
      {notice && <p role="status">{notice}</p>}
      {gate.busy && <p role="status">正在确认操作账户权限…</p>}
      {gate.error && (
        <button onClick={gate.refresh}>重新读取操作账户权限</button>
      )}
      {gate.data && !context.can("GET /api/operation-accounts") && (
        <p role="status">服务器未授予操作账户读取权限。</p>
      )}
      {context.can("GET /api/operation-accounts") && (
        <>
          {view.type !== "list" && (
            <button
              className="secondary"
              onClick={() =>
                open({ type: "list" }, "已离开表单；已提交的操作仍需核对回执。")
              }
            >
              返回操作账户列表
            </button>
          )}
          {receipt && <ReceiptNotice receipt={receipt} />}
          {pending ? (
            context.can("GET /api/operation-accounts/mutation") ? (
              <Recovery
                key={pending.key}
                context={context}
                intent={pending}
                done={complete}
              />
            ) : (
              <p role="alert">
                存在尚未确认的操作，当前无权查询回执。请恢复权限后核对。
              </p>
            )
          ) : (
            <>
              {view.type === "create" &&
                context.can("POST /api/operation-accounts/create") &&
                context.can("GET /api/operation-accounts/domains") && (
                  <CreateAccount
                    key={`create-${version}`}
                    context={context}
                    done={complete}
                    cancel={() => open({ type: "list" })}
                    uncertain={() => setVersion((value) => value + 1)}
                  />
                )}
              {"id" in view &&
                context.can("GET /api/operation-accounts/detail") && (
                  <AccountDetails
                    key={`${view.type}-${view.id}-${version}`}
                    context={context}
                    view={view}
                    open={open}
                    done={complete}
                    uncertain={() => setVersion((value) => value + 1)}
                  />
                )}
            </>
          )}
          {view.type === "list" && (
            <AccountList
              key={`list-${version}`}
              context={context}
              open={open}
              blocked={!!pending}
            />
          )}
        </>
      )}
    </div>
  );
}
function ReceiptNotice({ receipt }: { receipt: OperationReceipt }) {
  return (
    <section className="notice" aria-label="本地操作回执">
      <h3>本地操作回执</h3>
      <Field label="账户 ID">
        <input readOnly value={receipt.accountId} />
      </Field>
      <p>
        已提交操作：
        {
          { create: "登记", update: "修改登记", delete: "删除本地登记" }[
            receipt.operation
          ]
        }{" "}
        · 提交版本：{receipt.revision} · 提交凭据版本：
        {receipt.credentialRevision}
      </p>
      <p>
        当前记录版本：{receipt.currentRevision} · 当前凭据版本：
        {receipt.currentCredentialRevision}
      </p>
      <p>
        {receipt.deleted
          ? "当前登记已删除；回执保留原操作的提交结果。"
          : "原操作已提交；当前状态以重新读取的记录为准。凭据尚未验证。"}
      </p>
    </section>
  );
}
function Recovery({
  context,
  intent,
  done,
}: {
  context: Context;
  intent: OperationAccountIntent;
  done: (receipt: OperationReceipt) => void;
}) {
  const read = useTaskRead(
    intent.key,
    async (signal) => {
      const receipt = await operationAccountAPI.receipt(intent.key, signal);
      if (
        receipt.operation !== intent.kind ||
        receipt.domainId !== intent.domainId ||
        (intent.accountId && receipt.accountId !== intent.accountId)
      )
        throw new ApiError("invalid_response");
      return receipt;
    },
    context.sessionChanged,
    () => false,
    operationAccountError,
  );
  useEffect(() => {
    if (read.data) done(read.data);
  }, [read.data]);
  return (
    <section>
      <h3>核对未确认的操作</h3>
      <p className="warning">
        原操作可能已提交。未找到回执不能证明删除成功或提交失败；请查询原回执，不要重新登记。
      </p>
      <Field label="操作编号">
        <input readOnly value={intent.key} />
      </Field>
      <ErrorNotice error={read.error} />
      <button disabled={read.busy} onClick={read.refresh}>
        查询原操作回执
      </button>
    </section>
  );
}
function AccountList({
  context,
  open,
  blocked,
}: {
  context: Context;
  open: (view: View) => void;
  blocked: boolean;
}) {
  const defaults = { filterKeyword: "", filterDomain: "", pageSize: "20" };
  const [filters, setFilters] = useState(defaults),
    [applied, setApplied] = useState(defaults),
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
    (signal) => operationAccountAPI.list(query, signal),
    context.sessionChanged,
    () => false,
    operationAccountError,
  );
  return (
    <>
      <div className="actions">
        <button className="secondary" onClick={read.refresh}>
          刷新操作账户
        </button>
        {context.can("POST /api/operation-accounts/create") &&
          context.can("GET /api/operation-accounts/domains") && (
            <button disabled={blocked} onClick={() => open({ type: "create" })}>
              新增操作账户
            </button>
          )}
      </div>
      <form
        className="filters"
        onSubmit={(event) => {
          event.preventDefault();
          const domains = filters.filterDomain
            ? filters.filterDomain.split(/[\s,]+/u)
            : [];
          if (
            Array.from(filters.filterKeyword).length > 50 ||
            domains.length > 100 ||
            domains.some(
              (value) =>
                !validDNS(value) ||
                value !== value.toLowerCase().replace(/\.$/u, ""),
            )
          ) {
            setError(
              "关键词最多 50 个字符；域名须为小写完整 DNS 名称，最多 100 项。",
            );
            return;
          }
          setError("");
          setApplied(filters);
          setPage(1);
        }}
      >
        <Field label="域名或登记标签关键词">
          <input
            value={filters.filterKeyword}
            onChange={(event) =>
              setFilters({ ...filters, filterKeyword: event.target.value })
            }
          />
        </Field>
        <Field label="所属域筛选" help="完整小写域名，多个以逗号分隔">
          <input
            value={filters.filterDomain}
            onChange={(event) =>
              setFilters({ ...filters, filterDomain: event.target.value })
            }
          />
        </Field>
        <Field label="每页条数">
          <select
            value={filters.pageSize}
            onChange={(event) =>
              setFilters({ ...filters, pageSize: event.target.value })
            }
          >
            {[10, 20, 50, 100].map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </Field>
        <button>查询操作账户</button>
        <button
          type="button"
          className="secondary"
          onClick={() => {
            setFilters(defaults);
            setApplied(defaults);
            setPage(1);
            setError("");
          }}
        >
          重置筛选
        </button>
      </form>
      <ErrorNotice error={error || read.error} />
      {read.busy && <p role="status">正在读取操作账户…</p>}
      {read.data && (
        <>
          {read.data.List.length === 0 ? (
            <p role="status">当前筛选下没有获授权的操作账户。</p>
          ) : (
            <div className="table-scroll">
              <table>
                <caption>获授权的操作账户</caption>
                <thead>
                  <tr>
                    <th>登记标签 / 账户 ID</th>
                    <th>所属域</th>
                    <th>保存状态</th>
                    <th>创建时间（UTC）</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {read.data.List.map((account) => (
                    <tr key={account.accountId}>
                      <th scope="row">
                        {accountName(account)}
                        <small className="block">{account.accountId}</small>
                      </th>
                      <td>{account.domain}</td>
                      <td>已保存，未验证</td>
                      <td>{account.createdAt}</td>
                      <td>
                        {context.can("GET /api/operation-accounts/detail") && (
                          <button
                            className="secondary"
                            disabled={blocked}
                            onClick={() =>
                              open({ type: "detail", id: account.accountId })
                            }
                          >
                            查看 {accountName(account)}
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
function CreateAccount({
  context,
  done,
  cancel,
  uncertain,
}: {
  context: Context;
  done: (receipt: OperationReceipt) => void;
  cancel: () => void;
  uncertain: () => void;
}) {
  const [selected, setSelected] = useState<AccountDomain | null>(null);
  return selected ? (
    <>
      <AccountForm
        key={`${selected.domainId}:${selected.revision}`}
        context={context}
        domain={selected}
        done={done}
        cancel={cancel}
        uncertain={uncertain}
      />
    </>
  ) : (
    <DomainPicker context={context} select={setSelected} cancel={cancel} />
  );
}
function DomainPicker({
  context,
  select,
  cancel,
}: {
  context: Context;
  select: (domain: AccountDomain) => void;
  cancel: () => void;
}) {
  const [keyword, setKeyword] = useState(""),
    [applied, setApplied] = useState(""),
    [page, setPage] = useState(1),
    [error, setError] = useState("");
  const query = queryString({
    pageIdx: page,
    pageSize: 20,
    filterKeyword: applied,
  });
  const read = useTaskRead(
    query,
    (signal) => operationAccountAPI.domains(query, signal),
    context.sessionChanged,
    () => false,
    operationAccountError,
  );
  return (
    <section>
      <h3>选择已授权的域</h3>
      <p>仅可选择已登记且具有显式资源权限的域；平台管理员也需要资源组授权。</p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (Array.from(keyword).length > 50) {
            setError("域关键词最多 50 个字符。");
            return;
          }
          setError("");
          setApplied(keyword);
          setPage(1);
        }}
      >
        <Field label="查找所属域">
          <input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
          />
        </Field>
        <button>查找域</button>
      </form>
      <ErrorNotice error={error || read.error} />
      {read.busy && <p role="status">正在读取可选域…</p>}
      {read.data && (
        <>
          {read.data.domains.length === 0 ? (
            <p role="status">
              没有可选的已授权域。请先完成域连接登记和显式资源分配。
            </p>
          ) : (
            <ul>
              {read.data.domains.map((domain) => (
                <li key={domain.domainId}>
                  {domain.domain}{" "}
                  <button onClick={() => select(domain)}>
                    选择 {domain.domain}
                  </button>
                </li>
              ))}
            </ul>
          )}
          <Pagination
            page={read.data.page}
            exhausted={read.data.exhausted}
            busy={read.busy}
            change={setPage}
          />
        </>
      )}
      <div className="actions">
        <button className="secondary" onClick={read.refresh}>
          刷新可选域
        </button>
        <button className="secondary" onClick={cancel}>
          取消
        </button>
      </div>
    </section>
  );
}
function AccountDetails({
  context,
  view,
  open,
  done,
  uncertain,
}: {
  context: Context;
  view: Extract<View, { id: string }>;
  open: (view: View) => void;
  done: (receipt: OperationReceipt) => void;
  uncertain: () => void;
}) {
  const read = useTaskRead(
    view.id,
    (signal) => operationAccountAPI.detail(view.id, signal),
    context.sessionChanged,
    () => false,
    operationAccountError,
  );
  return (
    <>
      <ErrorNotice error={read.error} />
      <button
        className="secondary"
        onClick={() => {
          if (readOperationAccountIntent(context.profile)) uncertain();
          else read.refresh();
        }}
      >
        重新读取当前登记
      </button>
      {read.busy && <p role="status">正在读取当前登记…</p>}
      {read.data && !read.busy && (
        <>
          <h3>操作账户详情：{accountName(read.data)}</h3>
          <dl>
            {[
              ["完整账户 ID", read.data.accountId],
              ["所属域", read.data.domain],
              ["域 ID", read.data.domainId],
              ["登记标签", read.data.label || "未设置"],
              ["记录版本", read.data.revision],
              ["凭据版本", read.data.credentialRevision],
              ["凭据状态", "已保存，未验证"],
              ["创建时间（UTC）", read.data.createdAt],
              ["更新时间（UTC）", read.data.updatedAt],
            ].map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          {view.type === "detail" && (
            <>
              <CredentialUsePanel
                profile={context.profile}
                sessionChanged={context.sessionChanged}
                accountId={read.data.accountId}
              />
              <DirectoryCredentialUsePanel
                profile={context.profile}
                sessionChanged={context.sessionChanged}
                accountId={read.data.accountId}
              />
              <DirectoryV2CredentialUsePanel
                profile={context.profile}
                sessionChanged={context.sessionChanged}
                accountId={read.data.accountId}
              />
            </>
          )}
          {view.type === "detail" && (
            <div className="actions">
              {context.can("POST /api/operation-accounts/update") && (
                <button onClick={() => open({ type: "edit", id: view.id })}>
                  编辑登记
                </button>
              )}
              {context.can("POST /api/operation-accounts/delete") && (
                <button
                  className="secondary danger"
                  onClick={() => open({ type: "delete", id: view.id })}
                >
                  删除本地登记
                </button>
              )}
            </div>
          )}
          {view.type === "edit" &&
            context.can("POST /api/operation-accounts/update") && (
              <AccountForm
                key={`${read.data.accountId}:${read.data.revision}`}
                context={context}
                account={read.data}
                done={done}
                cancel={() => open({ type: "detail", id: view.id })}
                uncertain={uncertain}
              />
            )}
          {view.type === "delete" &&
            context.can("POST /api/operation-accounts/delete") && (
              <DeleteAccount
                key={`${read.data.accountId}:${read.data.revision}`}
                context={context}
                account={read.data}
                done={done}
                cancel={() => open({ type: "detail", id: view.id })}
                uncertain={uncertain}
              />
            )}
        </>
      )}
    </>
  );
}
type MutationProps = {
  context: Context;
  done: (receipt: OperationReceipt) => void;
  cancel: () => void;
  uncertain: () => void;
};
function AccountForm({
  context,
  domain,
  account,
  done,
  cancel,
  uncertain,
}: MutationProps & { domain?: AccountDomain; account?: OperationAccount }) {
  const [replace, setReplace] = useState(false),
    [sensitiveVersion, setSensitiveVersion] = useState(0),
    [mustReview, setMustReview] = useState(false),
    [key] = useState(() => crypto.randomUUID());
  const mutation = useTaskMutation(() => {
    discardOperationAccountIntent(context.profile);
    context.sessionChanged();
  }, operationAccountError);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event);
    if (mutation.busy || mustReview) return;
    if (readOperationAccountIntent(context.profile)) {
      uncertain();
      return;
    }
    const proof = getProof(values);
    if (!proof) {
      mutation.setError("请输入有效的操作者密码和六位未使用验证码。");
      return;
    }
    if (!validAccountLabel(values.label)) {
      mutation.setError("标签最多 50 个字符，不能含控制字符或首尾空白。");
      return;
    }
    if (
      (!account || replace) &&
      !validADPair(values.username, values.password)
    ) {
      mutation.setError(
        "AD 用户名须为 DOMAIN\\user 或 user@domain；用户名和密码各需 1–50 个字符，密码原样保存。",
      );
      return;
    }
    if (account && !replace && values.label === account.label) {
      mutation.setError("未检测到修改，请调整标签或明确替换凭据后提交。");
      return;
    }
    setSensitiveVersion((value) => value + 1);
    const intent: OperationAccountIntent = {
      kind: account ? "update" : "create",
      key,
      domainId: account?.domainId ?? domain!.domainId,
      ...(account ? { accountId: account.accountId } : {}),
    };
    saveOperationAccountIntent(context.profile, intent);
    const pair = { username: values.username, password: values.password };
    void mutation.run(
      (signal) =>
        account
          ? operationAccountAPI.update(
              {
                accountId: account.accountId,
                expectedRevision: account.revision,
                idempotencyKey: key,
                ...(values.label === account.label
                  ? {}
                  : { label: values.label }),
                ...(replace ? pair : {}),
              },
              proof,
              context.profile.csrfToken,
              signal,
            )
          : operationAccountAPI.create(
              {
                domainId: domain!.domainId,
                expectedDomainRevision: domain!.revision,
                idempotencyKey: key,
                ...pair,
                ...(values.label ? { label: values.label } : {}),
              },
              proof,
              context.profile.csrfToken,
              signal,
            ),
      done,
      (error) => {
        if (
          uncertainOperationError(error) ||
          (error instanceof ApiError && error.code === "idempotency_conflict")
        ) {
          uncertain();
          return;
        }
        discardOperationAccountIntent(context.profile);
        if (
          error instanceof ApiError &&
          [
            "revision_conflict",
            "not_found",
            "forbidden",
            "tenant_expired",
            "tenant_domain_limit_exceeded",
          ].includes(error.code)
        )
          setMustReview(true);
      },
    );
  };
  return (
    <form onSubmit={submit} autoComplete="off">
      <h3>{account ? "编辑本地登记" : "登记操作账户"}</h3>
      <p>所属域：{account?.domain ?? domain!.domain}</p>
      <ErrorNotice error={mutation.error} />
      {mustReview && (
        <p role="alert">
          此表单已停止提交。请重新读取当前登记，或重新选择所属域后核对。
        </p>
      )}
      <fieldset disabled={mutation.busy || mustReview}>
        <Field
          label="登记标签（可选）"
          help="用于识别本地登记，不是 AD 用户名；清空会移除标签。"
        >
          <input
            name="label"
            defaultValue={account?.label ?? ""}
            autoComplete="off"
          />
        </Field>
        {account && (
          <label>
            <input
              type="checkbox"
              checked={replace}
              onChange={(event) => {
                setReplace(event.target.checked);
                setSensitiveVersion((value) => value + 1);
              }}
            />
            替换已保存的凭据
          </label>
        )}
        {account && (
          <p>
            不替换时保留原凭据；替换仅保存新提供的用户名和密码，不修改远程账户密码。
          </p>
        )}
        <fieldset key={sensitiveVersion}>
          {(!account || replace) && (
            <>
              <Field label="AD 用户名">
                <input name="username" required autoComplete="off" />
              </Field>
              <Field label="AD 密码">
                <input
                  name="password"
                  type="password"
                  required
                  autoComplete="new-password"
                />
              </Field>
            </>
          )}
          <ProofFields />
        </fieldset>
      </fieldset>
      <FormActions busy={mutation.busy} disabled={mustReview} cancel={cancel}>
        {account ? "保存登记修改" : "保存本地登记"}
      </FormActions>
    </form>
  );
}
function DeleteAccount({
  context,
  account,
  done,
  cancel,
  uncertain,
}: MutationProps & { account: OperationAccount }) {
  const [sensitiveVersion, setSensitiveVersion] = useState(0),
    [mustReview, setMustReview] = useState(false),
    [key] = useState(() => crypto.randomUUID());
  const mutation = useTaskMutation(() => {
    discardOperationAccountIntent(context.profile);
    context.sessionChanged();
  }, operationAccountError);
  return (
    <form
      onSubmit={(event) => {
        const values = readValues(event);
        if (mutation.busy || mustReview) return;
        if (readOperationAccountIntent(context.profile)) {
          uncertain();
          return;
        }
        const proof = getProof(values);
        if (!proof || values.confirmAccountId !== account.accountId) {
          mutation.setError(
            "请输入完整账户 ID、有效操作者密码和六位未使用验证码。",
          );
          return;
        }
        setSensitiveVersion((value) => value + 1);
        saveOperationAccountIntent(context.profile, {
          kind: "delete",
          key,
          domainId: account.domainId,
          accountId: account.accountId,
        });
        void mutation.run(
          (signal) =>
            operationAccountAPI.delete(
              {
                accountId: account.accountId,
                expectedRevision: account.revision,
                confirmAccountId: values.confirmAccountId,
                idempotencyKey: key,
              },
              proof,
              context.profile.csrfToken,
              signal,
            ),
          done,
          (error) => {
            if (
              uncertainOperationError(error) ||
              (error instanceof ApiError &&
                error.code === "idempotency_conflict")
            ) {
              uncertain();
              return;
            }
            discardOperationAccountIntent(context.profile);
            if (
              error instanceof ApiError &&
              [
                "revision_conflict",
                "not_found",
                "forbidden",
                "tenant_expired",
                "tenant_domain_limit_exceeded",
              ].includes(error.code)
            )
              setMustReview(true);
          },
        );
      }}
    >
      <h3>删除本地操作账户登记</h3>
      <p className="warning">
        确认移除此登记及当前保存的凭据？这不会删除远程 AD
        账户、修改其密码或撤销其域权限。本地登记不能恢复。
      </p>
      <ErrorNotice error={mutation.error} />
      {mustReview && (
        <p role="alert">此表单已停止提交，请重新读取当前登记后核对。</p>
      )}
      <fieldset disabled={mutation.busy || mustReview} key={sensitiveVersion}>
        <Field label="输入完整账户 ID 确认">
          <input name="confirmAccountId" required autoComplete="off" />
        </Field>
        <ProofFields />
      </fieldset>
      <FormActions busy={mutation.busy} disabled={mustReview} cancel={cancel}>
        确认删除本地登记
      </FormActions>
    </form>
  );
}
