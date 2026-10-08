import { useEffect, useState, type FormEvent } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import {
  ErrorNotice,
  Field,
  ProofFields,
  getProof,
  readValues,
} from "./access-common";
import SavedSourcePicker from "./SavedSourcePicker";
import type { ResolvedSource, SourceChoice } from "./domain-selection-api";
import { useTaskMutation, useTaskRead } from "./task-common";
import { stateLabels, terminal, type Task } from "./task-api";
import {
  directoryAPI,
  directoryError,
  directoryOperations,
  uncertainDirectoryError,
  type DirectoryKind,
  type DirectoryOperation,
  type DirectoryQuery,
} from "./directory-api";
import {
  readDirectoryIntent,
  saveDirectoryIntent,
  discardDirectoryIntent,
  type DirectoryIntent,
} from "./directory-intent";

type Props = { profile: Profile; sessionChanged: () => void };
type Context = Props & { can: (operation: DirectoryOperation) => boolean };
const accessOperations = [
  ...directoryOperations,
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
];
export default function DirectoryWorkspace(props: Props) {
  return (
    <Workspace
      key={`${props.profile.ID}:${props.profile.csrfToken}`}
      {...props}
    />
  );
}
function Workspace({ profile, sessionChanged }: Props) {
  const [source, setSource] = useState<ResolvedSource | null>(null),
    [selectionVersion, setSelectionVersion] = useState(0),
    [pending, setPending] = useState(() => readDirectoryIntent(profile)),
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
          { paths: accessOperations },
          profile.csrfToken,
        ),
      ]);
      if (
        !Array.isArray(menu.menu) ||
        !Array.isArray(checks.results) ||
        checks.results.length !== accessOperations.length ||
        checks.results.some((v) => typeof v !== "boolean")
      )
        throw new ApiError("invalid_response");
      return { menu: menu.menu, checks: checks.results };
    },
    lost,
    () => false,
    directoryError,
  );
  const context: Context = {
    profile,
    sessionChanged: lost,
    can: (operation) => {
      const readable = (mark: string) =>
        gate.data?.menu.find((p) => p.mark === mark)?.auth?.readable === true;
      const writeable = (mark: string) =>
        gate.data?.menu.find((p) => p.mark === mark)?.auth?.writeable === true;
      return (
        !lostAccess &&
        gate.data?.checks[directoryOperations.indexOf(operation)] === true &&
        readable("domains") &&
        readable("directory_assets") &&
        (operation === "GET /api/directory/observation" || readable("tasks")) &&
        (!operation.startsWith("POST") ||
          (writeable("directory_assets") && writeable("tasks")))
      );
    },
  };
  const recover = (intent: DirectoryIntent) => {
    saveDirectoryIntent(profile, intent);
    setPending(intent);
  };
  return (
    <section aria-labelledby="directory-title">
      <h2 id="directory-title">目录资产</h2>
      <p>
        读取已完成的有界目录观察。结果不代表 AD
        的时间点快照；未观察到对象不表示删除。当前仅含用户、组和计算机的基础字段。
      </p>
      <ErrorNotice error={gate.error} />
      {gate.busy && <p role="status">正在核对目录访问权限…</p>}
      {gate.data && !context.can("GET /api/directory/observation") && (
        <p role="status">当前账户没有目录资产读取权限。</p>
      )}
      {context.can("GET /api/directory/observation") && (
        <>
          {gate.data?.checks[5] && gate.data.checks[6] ? (
            <SavedSourcePicker
              userID={profile.ID}
              sessionChanged={lost}
              onResolved={(value) => {
                setSource(value);
                setSelectionVersion((v) => v + 1);
                setPending(readDirectoryIntent(profile));
              }}
            />
          ) : (
            <p role="status">当前账户无权选择已配置域。</p>
          )}
          {source && (
            <>
              <p>
                已选择域：{source.selection.domain} · 配置版本{" "}
                {source.selection.revision} · 凭据代次{" "}
                {source.selection.credentialRevision}
              </p>
              <Observation
                key={selectionVersion}
                context={context}
                domainId={source.selection.domainId}
              />
            </>
          )}
          {pending ? (
            <section aria-label="目录同步恢复">
              <p>
                原同步域：{pending.domainId} · 操作编号：
                {pending.idempotencyKey}
              </p>
              {pending.taskUUID && context.can("GET /api/directory/task") ? (
                <TaskStatus
                  context={context}
                  intent={pending}
                  done={() => {
                    discardDirectoryIntent(profile);
                    setPending(null);
                  }}
                />
              ) : context.can("GET /api/directory/receipt") ? (
                <Recovery
                  context={context}
                  intent={pending}
                  recovered={recover}
                />
              ) : (
                <p role="status">
                  原操作编号已保留。当前无权核对任务回执，恢复权限后再查询。
                </p>
              )}
            </section>
          ) : source && context.can("POST /api/directory/sync") ? (
            <SyncForm
              key={selectionVersion}
              context={context}
              choice={source.selection}
              recovered={recover}
            />
          ) : (
            source && <p role="status">当前为目录只读访问，不能提交同步。</p>
          )}
        </>
      )}
    </section>
  );
}
function Observation({
  context,
  domainId,
}: {
  context: Context;
  domainId: string;
}) {
  const [query, setQuery] = useState<DirectoryQuery>({
      domainId,
      kind: "",
      pageIdx: 1,
      pageSize: 50,
    }),
    [revision, setRevision] = useState(0);
  const key = JSON.stringify(query) + revision;
  const read = useTaskRead(
    key,
    async (signal) => ({
      key,
      value: await directoryAPI.observation(query, context.profile.ID, signal),
    }),
    context.sessionChanged,
    () => false,
    directoryError,
  );
  const data = read.data?.key === key ? read.data.value : null;
  const reset = (next = query) => {
    setQuery({ ...next, pageIdx: 1, observationId: undefined });
    setRevision((v) => v + 1);
  };
  const kinds = { user: "用户", group: "组", computer: "计算机" };
  return (
    <section aria-label="目录观察">
      <div className="filters">
        <Field label="目录对象类型">
          <select
            value={query.kind}
            onChange={(e) =>
              reset({ ...query, kind: e.target.value as "" | DirectoryKind })
            }
          >
            <option value="">全部对象</option>
            {Object.entries(kinds).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="目录每页条数">
          <select
            value={query.pageSize}
            onChange={(e) =>
              reset({ ...query, pageSize: Number(e.target.value) })
            }
          >
            {[25, 50, 100].map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </Field>
        <button onClick={() => reset()}>刷新目录观察</button>
      </div>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在读取目录观察…</p>}
      {data && !data.available && (
        <p role="status">尚无当前配置的成功目录观察。此状态不表示目录为空。</p>
      )}
      {data?.available && (
        <>
          <p>观察编号：{data.observationId}</p>
          <p>服务器名称：{data.source!.server_name}</p>
          <p>
            来源域：{data.source!.domain} · 域控：{data.source!.dc_host_name} ·
            命名上下文：{data.source!.naming_context}
          </p>
          <p>
            观察时间（UTC）：{data.source!.started_at} 至{" "}
            {data.source!.completed_at} · 耗时{" "}
            {data.source!.elapsed_milliseconds} 毫秒 · LDAP 页数{" "}
            {data.source!.pages}
          </p>
          {data.list.length === 0 ? (
            <p role="status">已成功观察，当前筛选结果为空。</p>
          ) : (
            <div className="table-scroll">
              <table>
                <caption>目录对象观察</caption>
                <thead>
                  <tr>
                    <th>对象 GUID</th>
                    <th>类型</th>
                    <th>名称</th>
                    <th>可分辨名称</th>
                    <th>对象类</th>
                    <th>账户控制值</th>
                  </tr>
                </thead>
                <tbody>
                  {data.list.map((row) => (
                    <tr key={row.objectGUID}>
                      <td>{row.objectGUID}</td>
                      <td>{kinds[row.kind]}</td>
                      <td>{row.samAccountName ?? "未提供"}</td>
                      <td>{row.distinguishedName}</td>
                      <td>{row.objectClass.join(", ")}</td>
                      <td>{row.userAccountControl ?? "未提供"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <nav aria-label="目录观察分页">
            <button
              disabled={read.busy || query.pageIdx <= 1}
              onClick={() =>
                setQuery({
                  ...query,
                  pageIdx: query.pageIdx - 1,
                  observationId: data.observationId,
                })
              }
            >
              上一页
            </button>
            <span>
              第 {query.pageIdx} 页 · 共 {data.page.total} 个对象
            </span>
            <button
              disabled={read.busy || query.pageIdx >= data.page.totalPage}
              onClick={() =>
                setQuery({
                  ...query,
                  pageIdx: query.pageIdx + 1,
                  observationId: data.observationId,
                })
              }
            >
              下一页
            </button>
          </nav>
        </>
      )}
    </section>
  );
}
function SyncForm({
  context,
  choice,
  original,
  recovered,
}: {
  context: Context;
  choice?: SourceChoice;
  original?: DirectoryIntent;
  recovered: (intent: DirectoryIntent) => void;
}) {
  const [key] = useState(() => original?.idempotencyKey ?? crypto.randomUUID()),
    [proofVersion, setProofVersion] = useState(0),
    [review, setReview] = useState(false);
  const mutation = useTaskMutation(context.sessionChanged, directoryError);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    const values = readValues(event);
    if (mutation.busy || review || !context.can("POST /api/directory/sync"))
      return;
    const proof = getProof(values);
    if (!proof) {
      mutation.setError("请输入当前密码及六位未使用验证码。");
      return;
    }
    const existing = readDirectoryIntent(context.profile);
    if (!original && existing) {
      recovered(existing);
      return;
    }
    const intent: DirectoryIntent = original ?? {
      domainId: choice!.domainId,
      expectedRevision: choice!.revision,
      expectedCredentialGeneration: choice!.credentialRevision,
      idempotencyKey: key,
    };
    saveDirectoryIntent(context.profile, intent);
    setProofVersion((v) => v + 1);
    void mutation.run(
      (signal) =>
        directoryAPI.sync(
          intent,
          proof,
          context.profile.csrfToken,
          context.profile.ID,
          signal,
        ),
      (value) => recovered({ ...intent, taskUUID: value.task.taskUUID }),
      (error) => {
        if (
          original ||
          uncertainDirectoryError(error) ||
          (error instanceof ApiError && error.code === "idempotency_conflict")
        ) {
          recovered(intent);
          return;
        }
        discardDirectoryIntent(context.profile);
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
      <h3>{original ? "重试原目录同步" : "提交目录同步"}</h3>
      <p>
        同步将使用此域已绑定的操作账户，需独立的 domain.directory_read
        显式授权。服务器部署开关也必须已开启。
      </p>
      <p>
        固定配置版本：{original?.expectedRevision ?? choice?.revision} ·
        固定凭据代次：
        {original?.expectedCredentialGeneration ?? choice?.credentialRevision}
      </p>
      <ErrorNotice error={mutation.error} />
      {review && (
        <p role="status">此表单已停止提交。请重新选择并核对数据源后再提交。</p>
      )}
      <fieldset disabled={mutation.busy || review}>
        <fieldset key={proofVersion}>
          <ProofFields />
        </fieldset>
      </fieldset>
      <button disabled={mutation.busy || review}>
        {mutation.busy
          ? "正在提交…"
          : original
            ? "使用原编号和版本重试同步"
            : "提交目录同步"}
      </button>
    </form>
  );
}
function Recovery({
  context,
  intent,
  recovered,
}: {
  context: Context;
  intent: DirectoryIntent;
  recovered: (intent: DirectoryIntent) => void;
}) {
  const [revision, setRevision] = useState(0);
  const read = useTaskRead(
    `${intent.idempotencyKey}:${revision}`,
    async (signal) => {
      try {
        return {
          task: await directoryAPI.receipt(intent, context.profile.ID, signal),
        };
      } catch (error) {
        if (
          error instanceof ApiError &&
          error.status === 404 &&
          error.code === "not_found"
        )
          return { task: null };
        throw error;
      }
    },
    context.sessionChanged,
    () => false,
    directoryError,
  );
  useEffect(() => {
    if (read.data?.task)
      recovered({ ...intent, taskUUID: read.data.task.taskUUID });
  }, [read.data]);
  return (
    <section aria-label="核对目录同步回执">
      <h3>核对原目录同步回执</h3>
      <p>
        原操作可能已提交。先查询原回执；未找到回执不能证明提交失败。查询不会新建任务。
      </p>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在核对原同步回执…</p>}
      <button disabled={read.busy} onClick={() => setRevision((v) => v + 1)}>
        查询原同步回执
      </button>
      {!read.busy && read.data && !read.data.task && (
        <>
          <p role="status">
            未找到原同步回执。重试将保留原域、版本和操作编号。
          </p>
          {context.can("POST /api/directory/sync") && (
            <SyncForm
              key={`${intent.idempotencyKey}:${revision}`}
              context={context}
              original={intent}
              recovered={(value) => {
                recovered(value);
                setRevision((v) => v + 1);
              }}
            />
          )}
        </>
      )}
    </section>
  );
}
function TaskStatus({
  context,
  intent,
  done,
}: {
  context: Context;
  intent: DirectoryIntent;
  done: () => void;
}) {
  const [revision, setRevision] = useState(0);
  const key = `${intent.taskUUID}:${revision}`;
  const read = useTaskRead(
    key,
    async (signal) => ({
      key,
      task: await directoryAPI.task(
        intent.domainId,
        intent.taskUUID!,
        context.profile.ID,
        signal,
      ),
    }),
    context.sessionChanged,
    (value) => !terminal(value.task.state),
    directoryError,
  );
  const task = read.data?.key === key ? read.data.task : null;
  const refresh = () => setRevision((v) => v + 1);
  return (
    <section aria-label="目录同步任务">
      <h3>目录同步任务</h3>
      <p>任务编号：{intent.taskUUID}</p>
      <ErrorNotice error={read.error} />
      {read.busy && <p role="status">正在核对目录任务…</p>}
      <button onClick={refresh} disabled={read.busy}>
        刷新目录任务
      </button>
      {task && (
        <>
          <p role="status">
            服务器状态：{stateLabels[task.state]} · 进度 {task.progress}%
          </p>
          {task.error && <p>{directoryError(new ApiError(task.error))}</p>}
          <p>请求取消不代表执行器已停止；任务终态不代表凭据使用阻断已解除。</p>
          {terminal(task.state) ? (
            <>
              <p>任务已结束。可刷新目录观察以核对当前可见结果。</p>
              <button onClick={done}>结束此任务查看</button>
            </>
          ) : task.state === "cancel_requested" ? (
            <p>已请求取消，继续等待服务器确认实际终态。</p>
          ) : (
            context.can("POST /api/directory/cancel") && (
              <CancelForm
                key={task.taskUUID}
                context={context}
                task={task}
                refresh={refresh}
              />
            )
          )}
        </>
      )}
    </section>
  );
}
function CancelForm({
  context,
  task,
  refresh,
}: {
  context: Context;
  task: Task;
  refresh: () => void;
}) {
  const mutation = useTaskMutation(context.sessionChanged, directoryError),
    [proofVersion, setProofVersion] = useState(0);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    const proof = getProof(readValues(event));
    if (mutation.busy) return;
    if (!proof) {
      mutation.setError("请输入当前密码及六位未使用验证码。");
      return;
    }
    setProofVersion((v) => v + 1);
    void mutation.run(
      (signal) =>
        directoryAPI.cancel(
          task.domainId,
          task.taskUUID,
          proof,
          context.profile.csrfToken,
          context.profile.ID,
          signal,
        ),
      refresh,
      refresh,
    );
  };
  return (
    <form onSubmit={submit} autoComplete="off">
      <ErrorNotice error={mutation.error} />
      <fieldset disabled={mutation.busy} key={proofVersion}>
        <ProofFields />
      </fieldset>
      <button disabled={mutation.busy}>请求取消目录同步</button>
    </form>
  );
}
