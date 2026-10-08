import { useEffect, useState } from "react";
import { ApiError, type Profile } from "./api";
import { queryString } from "./access-api";
import {
  ErrorNotice,
  Field,
  ProofFields,
  getProof,
  readValues,
  Pagination,
} from "./access-common";
import { useTaskRead, useTaskMutation } from "./task-common";
import {
  validADPair,
  type Connection,
  type DomainOperation,
} from "./domain-api";
import {
  operationAccountAPI,
  type OperationAccount,
} from "./operation-account-api";
import { credentialUseAPI } from "./credential-use-api";
import {
  sourceAPI,
  sourceError,
  sourceLabels,
  uncertainSourceError,
  type SourceDetail,
  type SourceInput,
  type SourceOperation,
  type SourceReceipt,
} from "./domain-source-api";
import {
  readSourceIntent,
  saveSourceIntent,
  discardSourceIntent,
  matchesSourceIntent,
  type SourceIntent,
} from "./domain-source-intent";
export interface SourceContext {
  profile: Profile;
  sessionChanged: () => void;
  can: (operation: DomainOperation) => boolean;
}
export function SourceRecovery({
  context,
  intent,
  done,
}: {
  context: SourceContext;
  intent: SourceIntent;
  done: (r: SourceReceipt) => void;
}) {
  const read = useTaskRead(
    intent.idempotencyKey,
    async (signal) => {
      const receipt = await sourceAPI.receipt(
        intent.idempotencyKey,
        context.profile.ID,
        signal,
      );
      if (!matchesSourceIntent(receipt, intent))
        throw new ApiError("invalid_response");
      return receipt;
    },
    context.sessionChanged,
    () => false,
    sourceError,
  );
  useEffect(() => {
    if (read.data) {
      discardSourceIntent(context.profile);
      done(read.data);
    }
  }, [read.data]);
  return (
    <section aria-label="核对凭据来源回执">
      <h3>核对未确认的凭据来源变更</h3>
      <p>
        原操作可能已保存。先查询原回执；不会再次提交、生成新操作编号或要求重新输入凭据。未找到回执不能证明原操作失败。
      </p>
      <p>待核对域：{intent.domainId}</p>
      <Field label="来源操作编号">
        <input readOnly value={intent.idempotencyKey} />
      </Field>
      <ErrorNotice error={read.error} />
      <button disabled={read.busy} onClick={read.refresh}>
        查询原来源回执
      </button>
    </section>
  );
}
export default function DomainCredentialSource(props: {
  context: SourceContext;
  connection: Pick<Connection, "domainId" | "domain">;
  done: (message?: string) => void;
}) {
  return (
    <SourceWorkspace
      key={`${props.context.profile.ID}:${props.context.profile.role}:${props.context.profile.csrfToken}:${props.connection.domainId}`}
      {...props}
    />
  );
}
function SourceWorkspace({
  context,
  connection,
  done,
}: {
  context: SourceContext;
  connection: Pick<Connection, "domainId" | "domain">;
  done: (message?: string) => void;
}) {
  const [pending, setPending] = useState(() =>
    readSourceIntent(context.profile),
  );
  const [mutationPending, setMutationPending] = useState(false);
  const read = useTaskRead(
    connection.domainId,
    (signal) =>
      sourceAPI.detail(connection.domainId, context.profile.ID, signal),
    context.sessionChanged,
    () => false,
    sourceError,
  );
  const reread = () => {
    if (mutationPending) return;
    const saved = readSourceIntent(context.profile);
    if (saved) {
      setPending(saved);
      return;
    }
    read.refresh();
  };
  if (pending)
    return (
      <SourceRecovery
        context={context}
        intent={pending}
        done={(r) =>
          done(
            `原凭据来源操作已确认；当前来源：${sourceLabels[r.currentCredentialSource]}。请重新读取当前版本。`,
          )
        }
      />
    );
  return (
    <section aria-label="凭据来源">
      <h3>凭据来源：{connection.domain || connection.domainId}</h3>
      <ErrorNotice error={read.error} />
      <button className="secondary" disabled={mutationPending} onClick={reread}>
        重新读取凭据来源
      </button>
      {read.busy && <p role="status">正在核对来源与当前使用权限…</p>}
      {read.data && !read.busy && (
        <SourceForm
          key={`${read.data.revision}:${read.data.connectionCredentialGeneration}`}
          context={context}
          connection={connection}
          source={read.data}
          reread={reread}
          mutationPending={setMutationPending}
          uncertain={() => setPending(readSourceIntent(context.profile))}
          done={done}
        />
      )}
    </section>
  );
}
function SourceForm({
  context,
  connection,
  source,
  reread,
  uncertain,
  mutationPending,
  done,
}: {
  context: SourceContext;
  connection: Pick<Connection, "domainId" | "domain">;
  source: SourceDetail;
  reread: () => void;
  uncertain: () => void;
  mutationPending: (pending: boolean) => void;
  done: (message?: string) => void;
}) {
  const [operation, setOperation] = useState<SourceOperation | "">("");
  const [selected, setSelected] = useState<OperationAccount | null>(null);
  const [selectionVersion, setSelectionVersion] = useState(0);
  const [secretVersion, setSecretVersion] = useState(0);
  const [stale, setStale] = useState(false);
  const mutation = useTaskMutation(context.sessionChanged, sourceError);
  const effective = useTaskRead(
    `${selected?.accountId ?? ""}:${selected?.revision ?? ""}:${selectionVersion}`,
    async (signal) => {
      if (!selected) return null;
      // Reread the account record and the actor's own grant together. Never infer
      // use authority from list membership, metadata rights, role name or binding.
      const [account, grant] = await Promise.all([
        operationAccountAPI.detail(
          selected.accountId,
          signal,
          context.profile.ID,
        ),
        credentialUseAPI.effective(
          selected.accountId,
          context.profile.ID,
          signal,
        ),
      ]);
      if (
        account.domainId !== connection.domainId ||
        grant.domainId !== connection.domainId ||
        account.credentialRevision !== grant.accountCredentialRevision
      )
        throw new ApiError("revision_conflict", 409);
      if (
        account.revision !== selected.revision ||
        account.credentialRevision !== selected.credentialRevision
      )
        throw new ApiError("revision_conflict", 409);
      return { account, grant, selectionVersion };
    },
    context.sessionChanged,
    () => false,
    sourceError,
  );
  const permittedReference =
    !effective.busy &&
    effective.data?.selectionVersion === selectionVersion &&
    effective.data?.account.accountId === selected?.accountId &&
    effective.data?.account.revision === selected?.revision &&
    !!effective.data?.grant.consumerEnabled &&
    !!effective.data.grant.eligible &&
    !!effective.data.grant.explicitlyGranted;
  const allowed =
    !!operation &&
    context.can(`POST /api/domains/credential-source/${operation}`) &&
    (operation !== "reference" || permittedReference) &&
    !(operation === "detach" && source.credentialSource === "unconfigured");
  const choose = (next: SourceOperation | "") => {
    setOperation(next);
    setSelected(null);
    setSecretVersion((n) => n + 1);
    mutation.setError("");
  };
  return (
    <>
      <dl>
        <div>
          <dt>当前凭据来源</dt>
          <dd>{sourceLabels[source.credentialSource]}</dd>
        </div>
        <div>
          <dt>连接凭据代次 / 配置版本</dt>
          <dd>
            {source.connectionCredentialGeneration} / {source.revision}
          </dd>
        </div>
        <div>
          <dt>凭据已配置</dt>
          <dd>{source.credentialConfigured ? "是" : "否"}</dd>
        </div>
        <div>
          <dt>当前可提交检测</dt>
          <dd>{source.testEligible ? "是" : "否"}</dd>
        </div>
      </dl>
      {source.reference ? (
        <p>
          已引用：{source.reference.label || source.reference.accountId}
          。本角色显式授权：
          {source.reference.explicitlyGranted ? "已授予" : "未授予（默认拒绝）"}
          ；当前满足使用条件：{source.reference.eligible ? "是" : "否"}。
        </p>
      ) : (
        source.credentialSource === "operation_account" && (
          <p>
            当前无法读取账户元数据；未展示账户引用。仍可按域权限解除引用或改用新提供的自定义凭据。
          </p>
        )
      )}
      <p>
        引用属于连接。每位检测操作者都需要其自身角色的显式使用授权，包括平台管理员。保存来源不会启动检测。
      </p>
      <form
        autoComplete="off"
        onSubmit={(event) => {
          const values = readValues(event);
          if (mutation.busy || stale || !allowed || !operation) return;
          // A source intent may outlive a form, an aborted read, or navigation.
          // Reconcile it before reading any fresh proof or allocating a new key.
          if (readSourceIntent(context.profile)) {
            uncertain();
            return;
          }
          const proof = getProof(values);
          if (!proof) {
            mutation.setError("请输入有效的操作者密码和六位未使用验证码。");
            return;
          }
          if (
            operation === "custom" &&
            !validADPair(values.username, values.password)
          ) {
            mutation.setError(
              "请重新输入有效的 AD 用户名和密码；不会从原来源提取凭据。",
            );
            return;
          }
          const base = {
            domainId: source.domainId,
            expectedRevision: source.revision,
            expectedConnectionCredentialGeneration:
              source.connectionCredentialGeneration,
            idempotencyKey: crypto.randomUUID(),
          };
          let input: SourceInput;
          if (operation === "reference") {
            if (!effective.data || !permittedReference) return;
            input = {
              ...base,
              operation,
              accountId: effective.data.account.accountId,
              expectedAccountRevision: effective.data.account.revision,
              expectedAccountCredentialRevision:
                effective.data.account.credentialRevision,
              expectedGrantRevision: effective.data.grant.grantRevision,
            };
          } else
            input =
              operation === "custom"
                ? {
                    ...base,
                    operation,
                    username: values.username,
                    password: values.password,
                  }
                : { ...base, operation };
          saveSourceIntent(context.profile, { ...base, operation });
          setSecretVersion((n) => n + 1);
          mutationPending(true);
          void mutation
            .run(
              (signal) =>
                sourceAPI.mutate(
                  input,
                  proof,
                  context.profile.csrfToken,
                  context.profile.ID,
                  signal,
                ),
              (receipt) => {
                discardSourceIntent(context.profile);
                done(
                  `凭据来源已保存：${sourceLabels[receipt.currentCredentialSource]}。尚未检测。`,
                );
              },
              (error) => {
                if (uncertainSourceError(error)) uncertain();
                else {
                  discardSourceIntent(context.profile);
                  setStale(true);
                  setSelected(null);
                }
              },
            )
            .finally(() => mutationPending(false));
        }}
      >
        <ErrorNotice error={mutation.error} />
        {stale && (
          <p role="alert">
            来源、账户或授权可能已改变，请重新读取并核对后重新选择。
          </p>
        )}
        <fieldset disabled={mutation.busy || stale}>
          <Field label="新的凭据来源">
            <select
              value={operation}
              onChange={(e) => choose(e.target.value as SourceOperation | "")}
            >
              <option value="">请选择明确操作</option>
              {context.can("POST /api/domains/credential-source/custom") && (
                <option value="custom">自定义凭据（重新输入）</option>
              )}
              {context.can("POST /api/domains/credential-source/reference") &&
                context.can("GET /api/operation-accounts") &&
                context.can("GET /api/credential-use/effective") && (
                  <option value="reference">引用同域已登记操作账户</option>
                )}
              {context.can("POST /api/domains/credential-source/detach") && (
                <option value="detach">解除来源，设为未配置</option>
              )}
            </select>
          </Field>
          {operation === "reference" && (
            <>
              <AccountPicker
                context={context}
                connection={connection}
                selected={selected}
                choose={(account) => {
                  setSelected(account);
                  setSelectionVersion((n) => n + 1);
                  setSecretVersion((n) => n + 1);
                }}
              />
              <ErrorNotice error={effective.error} />
              {selected && effective.busy && (
                <p role="status">正在核对所选账户与本角色授权…</p>
              )}
              {effective.data && (
                <div aria-label="所选账户使用权限">
                  <p>
                    所选账户：
                    {effective.data.account.label ||
                      effective.data.account.accountId}
                  </p>
                  <p>
                    本角色显式授权：
                    {effective.data.grant.explicitlyGranted
                      ? "已授予"
                      : "未授予（默认拒绝）"}
                  </p>
                  <p>
                    当前满足使用条件：
                    {effective.data.grant.eligible ? "是" : "否"}
                    ；账户检测功能：
                    {effective.data.grant.consumerEnabled ? "已支持" : "未支持"}
                  </p>
                  <p>
                    账户 / 凭据 / 授权版本：{effective.data.account.revision} /{" "}
                    {effective.data.account.credentialRevision} /{" "}
                    {effective.data.grant.grantRevision}
                  </p>
                  {!permittedReference && (
                    <p>
                      请先在「管理操作账户 →
                      凭据使用授权」核对本角色的显式授权。此页面不会自动授予权限。
                    </p>
                  )}
                </div>
              )}
            </>
          )}
          {operation === "detach" && (
            <p>
              解除后不能发起新检测。已打开的使用仍可能阻止账户删除或替换；解除来源不证明执行已停止。
            </p>
          )}
          <div key={secretVersion}>
            {operation === "custom" && (
              <>
                <Field label="AD 用户名">
                  <input name="username" autoComplete="off" required />
                </Field>
                <Field label="AD 密码">
                  <input
                    name="password"
                    type="password"
                    autoComplete="new-password"
                    required
                  />
                </Field>
                <p>必须重新提供整对凭据；已保存凭据不会回显。</p>
              </>
            )}
            {operation && allowed && <ProofFields />}
          </div>
        </fieldset>
        <div className="actions">
          <button disabled={!allowed || mutation.busy || stale}>
            保存凭据来源
          </button>
          <button
            type="button"
            className="secondary"
            onClick={() =>
              done("已关闭来源编辑；关闭不会撤销服务器已提交的变更。")
            }
          >
            关闭来源编辑
          </button>
          {stale && (
            <button type="button" onClick={reread}>
              重新核对来源版本
            </button>
          )}
        </div>
      </form>
    </>
  );
}
function AccountPicker({
  context,
  connection,
  selected,
  choose,
}: {
  context: SourceContext;
  connection: Pick<Connection, "domainId" | "domain">;
  selected: OperationAccount | null;
  choose: (a: OperationAccount) => void;
}) {
  const [page, setPage] = useState(1);
  const read = useTaskRead(
    `${connection.domainId}:${page}`,
    async (signal) => {
      const list = await operationAccountAPI.list(
        queryString({
          filterDomain: [connection.domain],
          pageIdx: page,
          pageSize: 20,
        }),
        signal,
        context.profile.ID,
      );
      if (list.List.some((a) => a.domainId !== connection.domainId))
        throw new ApiError("invalid_response");
      return list;
    },
    context.sessionChanged,
    () => false,
    sourceError,
  );
  return (
    <section aria-label="同域操作账户">
      <h4>同域操作账户：{connection.domain}</h4>
      <ErrorNotice error={read.error} />
      <button type="button" className="secondary" onClick={read.refresh}>
        刷新同域账户
      </button>
      {read.data && (
        <>
          <ul>
            {read.data.List.map((account) => (
              <li key={account.accountId}>
                <button
                  type="button"
                  aria-pressed={selected?.accountId === account.accountId}
                  onClick={() => choose(account)}
                >
                  选择账户 {account.label || account.accountId}
                </button>
              </li>
            ))}
          </ul>
          {read.data.List.length === 0 && (
            <p>
              此域暂无可读取的操作账户。请完成资源分配后，在管理操作账户中登记一次凭据并显式授权。
            </p>
          )}
          <Pagination
            page={read.data.page}
            exhausted={read.data.exhausted}
            busy={read.busy}
            change={setPage}
          />
        </>
      )}
    </section>
  );
}
export function SourceTestPermission({
  context,
  connection,
  children,
}: {
  context: SourceContext;
  connection: Connection;
  children: React.ReactNode;
}) {
  const read = useTaskRead(
    connection.domainId,
    (s) => sourceAPI.detail(connection.domainId, context.profile.ID, s),
    context.sessionChanged,
    () => false,
    sourceError,
  );
  return (
    <>
      <ErrorNotice error={read.error} />
      {read.data?.testEligible && read.data.revision === connection.revision ? (
        children
      ) : (
        <p role="status">
          当前不可提交检测。请核对凭据来源、本角色显式使用授权及部署条件。
        </p>
      )}
      <button onClick={read.refresh}>重新核对检测权限</button>
    </>
  );
}
