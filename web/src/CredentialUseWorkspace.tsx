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
import { useTaskMutation, useTaskRead } from "./task-common";
import {
  credentialPurpose,
  credentialUseAPI,
  credentialUseError,
  credentialUseOperations,
  uncertainCredentialError,
  type CredentialUseOperation,
  type CredentialAccount,
  type CredentialGrant,
  type CredentialRole,
  type CredentialMutation,
  type CredentialReceipt,
} from "./credential-use-api";
import {
  discardCredentialIntent,
  matchesCredentialIntent,
  readCredentialIntent,
  saveCredentialIntent,
  type CredentialIntent,
} from "./credential-use-intent";

type Props = { profile: Profile; sessionChanged: () => void };
type Context = Props & { can: (operation: CredentialUseOperation) => boolean };
const saveNotice =
  "保存授权不会读取凭据、连接域或启动任务；域连接检测需要另行明确提交。";
function Gate({
  profile,
  sessionChanged,
  accountId,
}: Props & { accountId?: string }) {
  const [lostAccess, setLostAccess] = useState(false);
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
          { paths: credentialUseOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== credentialUseOperations.length ||
        checks.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return { menu: menu.menu, checks: checks.results };
    },
    lost,
    () => false,
    credentialUseError,
  );
  const context: Context = {
    profile,
    sessionChanged: lost,
    can: (operation) => {
      if (
        lostAccess ||
        gate.data?.checks[credentialUseOperations.indexOf(operation)] !== true
      )
        return false;
      if (operation === "GET /api/credential-use/effective")
        return gate.data.menu.some(
          (entry) =>
            entry.mark === "operation_accounts" &&
            entry.auth?.readable === true,
        );
      return profile.role === "platform_admin";
    },
  };
  return (
    <>
      <ErrorNotice error={gate.error} />
      {gate.busy && <p role="status">正在核对凭据授权权限…</p>}
      {gate.data &&
        !lostAccess &&
        (accountId ? (
          <AccountGovernance context={context} accountId={accountId} />
        ) : context.can("GET /api/credential-use/accounts") ? (
          <CleanupCatalogue context={context} />
        ) : (
          <p role="status">当前账户没有凭据授权清理权限。</p>
        ))}
    </>
  );
}
export default function CredentialUseWorkspace(props: Props) {
  return (
    <div className="credential-use-workspace">
      <h2>凭据授权清理</h2>
      <p>
        查看具有已保存授权的账户。租户过期或容量超限时，仍可在已有域管理范围内撤销授权。
      </p>
      <p>{saveNotice}</p>
      <Gate key={`${props.profile.ID}:${props.profile.csrfToken}`} {...props} />
    </div>
  );
}
export function CredentialUsePanel(props: Props & { accountId: string }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <section aria-label="凭据使用授权">
      <h3>凭据使用授权</h3>
      <p>
        账户登记和普通功能权限不会自动授予凭据使用权，平台管理员也默认无权使用。
      </p>
      <p>{saveNotice}</p>
      <button
        className="secondary"
        onClick={() => setExpanded((value) => !value)}
      >
        {expanded ? "收起凭据使用授权" : "查看凭据使用授权"}
      </button>
      {expanded && (
        <Gate
          key={`${props.profile.ID}:${props.profile.csrfToken}:${props.accountId}`}
          {...props}
        />
      )}
    </section>
  );
}
function CleanupCatalogue({ context }: { context: Context }) {
  const [accountId, setAccountId] = useState<string | null>(null),
    [keyword, setKeyword] = useState(""),
    [applied, setApplied] = useState(""),
    [page, setPage] = useState(1),
    [size, setSize] = useState(20),
    [error, setError] = useState(""),
    [version, setVersion] = useState(0),
    [receipt, setReceipt] = useState<CredentialReceipt | null>(null);
  const query = queryString({
    pageIdx: page,
    pageSize: size,
    keyword: applied,
  });
  const read = useTaskRead(
    `${query}:${version}`,
    (signal) => credentialUseAPI.accounts(query, context.profile.ID, signal),
    context.sessionChanged,
    () => false,
    credentialUseError,
  );
  const pending = readCredentialIntent(context.profile);
  return (
    <>
      {receipt && !accountId && <ReceiptNotice receipt={receipt} />}
      {pending && !accountId && (
        <Recovery
          context={context}
          intent={pending}
          done={(value) => {
            setReceipt(value);
            setVersion((v) => v + 1);
          }}
        />
      )}
      {accountId ? (
        <>
          <button
            className="secondary"
            onClick={() => {
              setAccountId(null);
              setVersion((v) => v + 1);
            }}
          >
            返回授权清理目录
          </button>
          <AccountGovernance
            key={`${accountId}:${version}`}
            context={context}
            accountId={accountId}
          />
        </>
      ) : (
        <>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (
                Array.from(keyword).length > 50 ||
                /[\p{Cc}\ud800-\udfff]/u.test(keyword)
              ) {
                setError("关键词最多 50 个字符，不能含控制字符。");
                return;
              }
              setError("");
              setApplied(keyword);
              setPage(1);
            }}
          >
            <Field label="授权清理关键词">
              <input
                value={keyword}
                onChange={(event) => setKeyword(event.target.value)}
              />
            </Field>
            <Field label="清理目录每页条数">
              <select
                value={size}
                onChange={(event) => {
                  setSize(Number(event.target.value));
                  setPage(1);
                }}
              >
                {[10, 20, 30, 40, 50].map((n) => (
                  <option key={n}>{n}</option>
                ))}
              </select>
            </Field>
            <button>查询授权清理目录</button>
          </form>
          <ErrorNotice error={error || read.error} />
          {read.busy && <p role="status">正在读取授权清理目录…</p>}
          {read.data && (
            <>
              {read.data.List.length ? (
                <table>
                  <caption>具有已保存使用授权的账户</caption>
                  <thead>
                    <tr>
                      <th>账户</th>
                      <th>所属域</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {read.data.List.map((account) => (
                      <tr key={account.accountId}>
                        <th scope="row">
                          {account.label || account.accountId}
                          <small className="block">{account.accountId}</small>
                        </th>
                        <td>{account.domain}</td>
                        <td>
                          <button
                            disabled={!!pending}
                            onClick={() => setAccountId(account.accountId)}
                          >
                            清理 {account.label || account.accountId}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : (
                <p role="status">当前范围内没有需要清理的已保存授权。</p>
              )}
              <Pagination
                page={read.data.page}
                exhausted={read.data.exhausted}
                busy={read.busy}
                change={setPage}
              />
            </>
          )}
          <button className="secondary" onClick={read.refresh}>
            刷新授权清理目录
          </button>
        </>
      )}
    </>
  );
}
function AccountGovernance({
  context,
  accountId,
}: {
  context: Context;
  accountId: string;
}) {
  const [version, setVersion] = useState(0),
    [receipt, setReceipt] = useState<CredentialReceipt | null>(null),
    [stale, setStale] = useState(false);
  const pending = readCredentialIntent(context.profile);
  const refresh = (value?: CredentialReceipt) => {
    if (value) setReceipt(value);
    setVersion((v) => v + 1);
  };
  return (
    <>
      {receipt && <ReceiptNotice receipt={receipt} />}
      {pending ? (
        <Recovery context={context} intent={pending} done={refresh} />
      ) : stale ? (
        <section>
          <p role="alert">
            账户、凭据或授权版本已改变。已清除旧表单，请重新读取授权状态后重新选择角色。
          </p>
          <button
            onClick={() => {
              setStale(false);
              refresh();
            }}
          >
            重新读取授权状态
          </button>
        </section>
      ) : (
        <>
          {context.can("GET /api/credential-use/grants") && (
            <GrantManagement
              key={`grants:${accountId}:${version}`}
              context={context}
              accountId={accountId}
              done={refresh}
              uncertain={() => refresh()}
              stale={() => setStale(true)}
            />
          )}
          {context.can("GET /api/credential-use/effective") && (
            <EffectiveView
              key={`effective:${accountId}:${version}`}
              context={context}
              accountId={accountId}
            />
          )}
          {!context.can("GET /api/credential-use/grants") &&
            !context.can("GET /api/credential-use/effective") && (
              <p role="status">当前账户没有读取此凭据使用授权的权限。</p>
            )}
        </>
      )}
    </>
  );
}
function EffectiveView({
  context,
  accountId,
}: {
  context: Context;
  accountId: string;
}) {
  const read = useTaskRead(
    accountId,
    (signal) =>
      credentialUseAPI.effective(accountId, context.profile.ID, signal),
    context.sessionChanged,
    () => false,
    credentialUseError,
  );
  return (
    <section aria-label="我的凭据使用权限">
      <h4>我的凭据使用权限</h4>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取我的使用权限…</p>}
      {read.data && (
        <dl>
          <div>
            <dt>本角色已保存显式授权</dt>
            <dd>{read.data.explicitlyGranted ? "是" : "否（默认拒绝）"}</dd>
          </div>
          <div>
            <dt>当前满足使用条件</dt>
            <dd>{read.data.eligible ? "是" : "否"}</dd>
          </div>
          <div>
            <dt>用途</dt>
            <dd>{read.data.purpose}</dd>
          </div>
          <div>
            <dt>授权版本 / 凭据版本</dt>
            <dd>
              {read.data.grantRevision} / {read.data.accountCredentialRevision}
            </dd>
          </div>
        </dl>
      )}
      <p>
        {read.data?.consumerEnabled
          ? "此部署支持使用已登记账户检测域连接；当前是否可检测仍取决于授权、配置与部署条件。"
          : "此部署尚不支持使用已登记账户检测域连接。"}
      </p>
      <button className="secondary" onClick={read.refresh}>
        刷新我的使用权限
      </button>
    </section>
  );
}
function GrantManagement({
  context,
  accountId,
  done,
  uncertain,
  stale,
}: {
  context: Context;
  accountId: string;
  done: (r: CredentialReceipt) => void;
  uncertain: () => void;
  stale: () => void;
}) {
  const [selected, setSelected] = useState(""),
    [choice, setChoice] = useState<{
      kind: CredentialMutation;
      roleId: string;
    } | null>(null);
  // Separate reads are essential: live role eligibility may fail during expiry,
  // while persisted scope still authorizes viewing and reducing saved grants.
  const grants = useTaskRead(
    accountId,
    (signal) => credentialUseAPI.grants(accountId, context.profile.ID, signal),
    context.sessionChanged,
    () => false,
    credentialUseError,
  );
  const roles = useTaskRead(
    accountId,
    (signal) =>
      context.can("GET /api/credential-use/roles")
        ? credentialUseAPI.roles(accountId, context.profile.ID, signal)
        : Promise.resolve([]),
    context.sessionChanged,
    () => false,
    credentialUseError,
  );
  const target =
    choice &&
    (roles.data?.find((r) => r.roleId === choice.roleId) ??
      grants.data?.grants.find((g) => g.roleId === choice.roleId));
  return (
    <section aria-label="角色使用授权管理">
      <h4>角色使用授权管理</h4>
      <p>
        授权覆盖目标角色的当前及未来全部成员，仅限
        domain.connection_test，不授予普通功能权限或域范围。
      </p>
      <ErrorNotice error={grants.error} />
      {grants.busy && <p role="status">正在读取已保存授权…</p>}
      {grants.data && !grants.busy && (
        <>
          <p>
            账户：{grants.data.account.label || grants.data.account.accountId} ·
            所属域：{grants.data.account.domain} · 当前凭据版本：
            {grants.data.account.credentialRevision}
          </p>
          {grants.data.grants.length ? (
            <table>
              <caption>已保存的角色使用授权</caption>
              <thead>
                <tr>
                  <th>角色</th>
                  <th>已保存授权</th>
                  <th>授权版本 / 凭据版本</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {grants.data.grants.map((grant) => (
                  <tr key={grant.roleId}>
                    <th scope="row">
                      {grant.roleName}
                      <small className="block">{grant.roleId}</small>
                    </th>
                    <td>{grant.allowed ? "允许" : "已撤销"}</td>
                    <td>
                      {grant.grantRevision} / {grant.accountCredentialRevision}
                    </td>
                    <td>
                      {grant.allowed &&
                        context.can("POST /api/credential-use/revoke") && (
                          <button
                            className="secondary danger"
                            disabled={!!choice}
                            onClick={() =>
                              setChoice({
                                kind: "revoke",
                                roleId: grant.roleId,
                              })
                            }
                          >
                            撤销 {grant.roleName}
                          </button>
                        )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p role="status">
              没有显式使用授权，所有角色（包括平台管理员）默认拒绝。
            </p>
          )}
          <ErrorNotice error={roles.error} />
          {roles.error && (
            <p>当前无法确认可新增授权的角色；已有授权仍可按上方列表撤销。</p>
          )}
          {roles.busy && <p role="status">正在核对可授权角色…</p>}
          {context.can("POST /api/credential-use/grant") && (
            <>
              <Field
                label="授权目标角色"
                help="覆盖当前及未来全部成员，人数仅代表本次读取时的成员数。"
              >
                <select
                  value={selected}
                  disabled={!roles.data || roles.busy || !!choice}
                  onChange={(event) => setSelected(event.target.value)}
                >
                  <option value="">请选择角色</option>
                  {roles.data?.map((role) => (
                    <option key={role.roleId} value={role.roleId}>
                      {role.roleName}（当前 {role.memberCount} 人）
                    </option>
                  ))}
                </select>
              </Field>
              <button
                disabled={
                  !selected ||
                  !roles.data?.some((r) => r.roleId === selected) ||
                  roles.busy ||
                  !!choice
                }
                onClick={() => setChoice({ kind: "grant", roleId: selected })}
              >
                审阅角色授权
              </button>
            </>
          )}
          {choice && target && (
            <GrantForm
              key={`${choice.kind}:${choice.roleId}`}
              context={context}
              account={grants.data.account}
              target={target}
              kind={choice.kind}
              existing={grants.data.grants.find(
                (g) => g.roleId === choice.roleId,
              )}
              done={done}
              uncertain={uncertain}
              stale={stale}
              cancel={() => setChoice(null)}
            />
          )}
        </>
      )}
      {!choice && (
        <button
          className="secondary"
          onClick={() => {
            grants.refresh();
            roles.refresh();
          }}
        >
          重新读取授权状态
        </button>
      )}
    </section>
  );
}
function GrantForm({
  context,
  account,
  target,
  kind,
  existing,
  done,
  uncertain,
  stale,
  cancel,
}: {
  context: Context;
  account: CredentialAccount;
  target: CredentialRole | CredentialGrant;
  kind: CredentialMutation;
  existing?: CredentialGrant;
  done: (r: CredentialReceipt) => void;
  uncertain: () => void;
  stale: () => void;
  cancel: () => void;
}) {
  const [proofVersion, setProofVersion] = useState(0),
    [review, setReview] = useState(false),
    [key] = useState(() => crypto.randomUUID());
  const mutation = useTaskMutation(context.sessionChanged, credentialUseError);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event);
    if (mutation.busy || review) return;
    if (readCredentialIntent(context.profile)) {
      uncertain();
      return;
    }
    const proof = getProof(values);
    if (!proof || values.confirmRole !== "on") {
      mutation.setError(
        "请确认角色全体成员范围，并输入当前密码和六位未使用验证码。",
      );
      return;
    }
    const intent: CredentialIntent = {
      kind,
      domainId: account.domainId,
      accountId: account.accountId,
      roleId: target.roleId,
      purpose: credentialPurpose,
      expectedAccountRevision: account.revision,
      expectedCredentialRevision: account.credentialRevision,
      expectedGrantRevision: existing?.grantRevision ?? "0",
      idempotencyKey: key,
    };
    saveCredentialIntent(context.profile, intent);
    setProofVersion((v) => v + 1);
    const { kind: _, domainId: __, ...input } = intent;
    void mutation.run(
      (signal) =>
        credentialUseAPI.mutate(
          kind,
          input,
          proof,
          context.profile.csrfToken,
          context.profile.ID,
          signal,
        ),
      (receipt) => {
        if (!matchesCredentialIntent(receipt, intent)) {
          uncertain();
          return;
        }
        discardCredentialIntent(context.profile);
        done(receipt);
      },
      (error) => {
        if (uncertainCredentialError(error)) {
          uncertain();
          return;
        }
        discardCredentialIntent(context.profile);
        if (error instanceof ApiError && error.code === "revision_conflict") {
          // Drop every stale form/selection and its proof before another
          // explicit read. The original POST is never replayed automatically.
          stale();
          return;
        }
        if (
          !(error instanceof ApiError) ||
          error.code !== "invalid_credentials"
        )
          setReview(true);
      },
    );
  };
  return (
    <form onSubmit={submit} autoComplete="off">
      <h4>{kind === "grant" ? "确认角色凭据授权" : "确认撤销角色授权"}</h4>
      <p>
        目标角色：{target.roleName}（{target.roleId}）
      </p>
      <p>
        {"memberCount" in target
          ? `当前成员数：${target.memberCount}。`
          : "当前成员数不可读取。"}
        此操作影响该角色当前及未来全部成员。
      </p>
      <p>
        用途：{credentialPurpose} · 凭据版本：{account.credentialRevision}
      </p>
      <p>{saveNotice}</p>
      <ErrorNotice error={mutation.error} />
      {review && (
        <p role="alert">此表单已停止提交，请取消后重新读取授权状态。</p>
      )}
      <fieldset disabled={mutation.busy || review}>
        <label>
          <input type="checkbox" name="confirmRole" required />
          我已核对目标角色，确认{kind === "grant" ? "授权" : "撤销"}
          覆盖当前及未来全部成员
        </label>
        <fieldset key={proofVersion}>
          <ProofFields />
        </fieldset>
      </fieldset>
      <FormActions busy={mutation.busy} disabled={review} cancel={cancel}>
        {kind === "grant" ? "确认授予角色使用权" : "确认撤销角色使用权"}
      </FormActions>
    </form>
  );
}
function Recovery({
  context,
  intent,
  done,
}: {
  context: Context;
  intent: CredentialIntent;
  done: (r: CredentialReceipt) => void;
}) {
  const allowed = context.can("GET /api/credential-use/mutation");
  const read = useTaskRead(
    intent.idempotencyKey,
    async (signal) => {
      if (!allowed) throw new ApiError("forbidden");
      const receipt = await credentialUseAPI.receipt(
        intent.idempotencyKey,
        context.profile.ID,
        signal,
      );
      if (!matchesCredentialIntent(receipt, intent))
        throw new ApiError("invalid_response");
      return receipt;
    },
    context.sessionChanged,
    () => false,
    credentialUseError,
  );
  useEffect(() => {
    if (read.data) {
      discardCredentialIntent(context.profile);
      done(read.data);
    }
  }, [read.data]);
  return (
    <section aria-label="核对未确认的凭据授权">
      <h3>核对未确认的凭据授权</h3>
      <p className="warning">
        原操作可能已提交。查询仅恢复原回执，不会再次授权、撤销或生成新操作编号。未找到回执不能证明操作失败。
      </p>
      <Field label="凭据授权操作编号">
        <input readOnly value={intent.idempotencyKey} />
      </Field>
      <ErrorNotice error={read.error} />
      <button disabled={read.busy || !allowed} onClick={read.refresh}>
        查询原授权回执
      </button>
    </section>
  );
}
function ReceiptNotice({ receipt }: { receipt: CredentialReceipt }) {
  return (
    <section aria-label="凭据授权回执">
      <h3>凭据授权回执</h3>
      <p>
        原操作已提交：{receipt.operation === "grant" ? "授予" : "撤销"}角色{" "}
        {receipt.roleId} 的使用权
      </p>
      <p>
        账户：{receipt.accountId} · 原授权版本：{receipt.grantRevision} ·
        当前授权版本：{receipt.currentGrantRevision}
      </p>
      <p>
        当前已保存授权：{receipt.currentAllowed ? "允许" : "已撤销或不存在"}
        {receipt.accountDeleted ? "；账户已删除" : ""}
      </p>
      <p>{saveNotice}</p>
    </section>
  );
}
