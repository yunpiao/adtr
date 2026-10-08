import { useEffect, useState } from "react";
import { ApiError } from "./api";
import { queryString, type QueryValue } from "./access-api";
import {
  ErrorNotice,
  Field,
  FormActions,
  PageSize,
  Pagination,
  ProofFields,
  readValues,
} from "./access-common";
import {
  groupDomainIDs,
  parseResourceIDs,
  parseCustomRoleIDs,
  resourceRequest,
  validGroupName,
  validResourceMark,
  type AssociatedRoles,
  type ResourceInput,
  type ResourceList,
  type ResourceMeta,
} from "./resource-api";
import {
  useResourceMutation,
  useResourceTask,
  type ResourceContext,
} from "./resource-common";

type View =
  | { kind: "list" }
  | { kind: "create" }
  | { kind: "detail" | "edit" | "delete" | "assign"; id: string };
export default function ResourceGroups({
  context,
}: {
  context: ResourceContext;
}) {
  const [view, setView] = useState<View>({ kind: "list" }),
    [revision, setRevision] = useState(0),
    [notice, setNotice] = useState("");
  const [query, setQuery] = useState<Record<string, QueryValue>>({
    pageIdx: 1,
    pageSize: 20,
    sort: false,
  });
  const done = (
    message = "已返回资源组列表，请核对最新服务器状态。取消不能撤销已提交的修改。",
  ) => {
    setView({ kind: "list" });
    setNotice(message);
    setRevision((n) => n + 1);
  };
  const open = (next: View) => {
    setNotice("");
    setView(next);
    window.history.pushState({}, "", `#resources/groups/${next.kind}`);
  };
  return (
    <>
      <h3>资源组</h3>
      {notice && (
        <p className="success" role="status">
          {notice}
        </p>
      )}
      {view.kind === "list" ? (
        <GroupList
          key={revision}
          context={context}
          query={query}
          setQuery={setQuery}
          open={open}
        />
      ) : (
        <GroupEditor
          key={`${view.kind}-${"id" in view ? view.id : "new"}`}
          context={context}
          view={view}
          done={done}
          open={open}
        />
      )}
    </>
  );
}
function GroupList({
  context,
  query,
  setQuery,
  open,
}: {
  context: ResourceContext;
  query: Record<string, QueryValue>;
  setQuery: (query: Record<string, QueryValue>) => void;
  open: (view: View) => void;
}) {
  const [data, setData] = useState<ResourceList | null>(null),
    [revision, setRevision] = useState(0);
  const task = useResourceTask(context.sessionChanged),
    queryText = queryString(query);
  useEffect(() => {
    setData(null);
    void task.run(
      (signal) => resourceRequest<ResourceList>(`/groups?${queryText}`, signal),
      setData,
      true,
    );
  }, [queryText, revision]);
  return (
    <>
      <div className="actions compact">
        {context.can("POST /api/resources/groups/create") && (
          <button onClick={() => open({ kind: "create" })}>新增资源组</button>
        )}
        <button className="secondary" onClick={() => setRevision((n) => n + 1)}>
          刷新资源组列表
        </button>
      </div>
      <form
        className="filters"
        onSubmit={(event) => {
          const values = readValues(event);
          if (Array.from(values.name).length > 50) {
            task.setError("搜索名称最多 50 个字符。");
            return;
          }
          setQuery({
            pageIdx: 1,
            pageSize: Number(values.pageSize),
            name: values.name,
            sort: values.sort === "true",
          });
        }}
      >
        <div className="form-grid">
          <Field
            label="搜索资源组名称"
            help="按字面内容匹配，不区分大小写；最多 50 个字符"
          >
            <input name="name" defaultValue={String(query.name ?? "")} />
          </Field>
          <Field label="每页条数">
            <select name="pageSize" defaultValue={String(query.pageSize)}>
              {[10, 20, 50, 100].map((size) => (
                <option key={size} value={size}>
                  {size}
                </option>
              ))}
              <option value="-1">全部（最多 1,000 条）</option>
            </select>
          </Field>
          <Field label="资源组排序">
            <select name="sort" defaultValue={String(query.sort)}>
              <option value="false">创建时间：新到旧</option>
              <option value="true">创建时间：旧到新</option>
            </select>
          </Field>
        </div>
        <button>筛选资源组</button>
      </form>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在读取资源组…</p>}
      {data && (
        <>
          {data.metas.length === 0 ? (
            <p role="status">没有匹配的资源组。</p>
          ) : (
            <div className="table-scroll">
              <table>
                <caption>当前租户资源组</caption>
                <thead>
                  <tr>
                    <th>名称与 ID</th>
                    <th>域数量</th>
                    <th>关联角色数</th>
                    <th>备注</th>
                    <th>创建时间（UTC）</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {data.metas.map((group) => (
                    <tr key={group.id}>
                      <th scope="row">
                        {group.name}
                        <small className="resource-id">{group.id}</small>
                      </th>
                      <td>{groupDomainIDs(group).length}</td>
                      <td>{group.applyRoleCount}</td>
                      <td>{group.mark || "无"}</td>
                      <td>{group.createTime}</td>
                      <td>
                        {context.can("GET /api/resources/groups/detail") && (
                          <button
                            className="secondary"
                            onClick={() =>
                              open({ kind: "detail", id: group.id })
                            }
                          >
                            查看 {group.name}
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
            page={data.page}
            exhausted={data.exhausted}
            busy={task.busy}
            change={(pageIdx) => setQuery({ ...query, pageIdx })}
          />
        </>
      )}
    </>
  );
}
function GroupEditor({
  context,
  view,
  done,
  open,
}: {
  context: ResourceContext;
  view: Exclude<View, { kind: "list" }>;
  done: (notice?: string) => void;
  open: (view: View) => void;
}) {
  const [group, setGroup] = useState<ResourceMeta | null>(null),
    [revision, setRevision] = useState(0);
  const task = useResourceTask(context.sessionChanged),
    id = "id" in view ? view.id : undefined;
  useEffect(() => {
    if (!id) return;
    setGroup(null);
    void task.run(
      (signal) =>
        resourceRequest<{ meta: ResourceMeta }>(
          `/groups/detail?${queryString({ id })}`,
          signal,
        ),
      (data) => setGroup(data.meta),
      true,
    );
  }, [id, revision]);
  if (view.kind === "create")
    return <GroupForm context={context} done={done} />;
  return (
    <>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在读取资源组详情…</p>}
      {!group && (
        <div className="actions">
          <button
            className="secondary"
            onClick={() => setRevision((n) => n + 1)}
          >
            重新读取详情
          </button>
          <button className="secondary" onClick={() => done()}>
            取消
          </button>
        </div>
      )}
      {group &&
        (view.kind === "edit" ? (
          <GroupForm context={context} group={group} done={done} />
        ) : view.kind === "assign" ? (
          <AssociationForm context={context} group={group} done={done} />
        ) : view.kind === "delete" ? (
          <DeleteGroup context={context} group={group} done={done} />
        ) : (
          <>
            <h4>资源组详情：{group.name}</h4>
            <dl>
              {[
                ["资源组 ID", group.id],
                ["资源组名称", group.name],
                ["资源组备注", group.mark || "无"],
                ["关联角色数", group.applyRoleCount],
                ["创建时间（UTC）", group.createTime],
              ].map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>
            <h4>已配置域 ID</h4>
            {groupDomainIDs(group).length ? (
              <ul>
                {groupDomainIDs(group).map((domain) => (
                  <li key={domain}>{domain}</li>
                ))}
              </ul>
            ) : (
              <p>空资源组，不授予任何域访问权限。</p>
            )}
            <div className="actions">
              {context.can("POST /api/resources/groups/update") && (
                <button onClick={() => open({ kind: "edit", id: group.id })}>
                  编辑资源组
                </button>
              )}
              {context.can("POST /api/resources/groups/assign") &&
                context.can("GET /api/resources/groups/roles") && (
                  <button
                    onClick={() => open({ kind: "assign", id: group.id })}
                  >
                    调整关联角色
                  </button>
                )}
              {context.can("POST /api/resources/groups/delete") && (
                <button
                  className="danger"
                  onClick={() => open({ kind: "delete", id: group.id })}
                >
                  删除资源组
                </button>
              )}
              <button className="secondary" onClick={() => done()}>
                返回资源组列表
              </button>
            </div>
            {context.can("GET /api/resources/groups/roles") && (
              <AssociationList context={context} id={group.id} />
            )}
          </>
        ))}
    </>
  );
}
function GroupForm({
  context,
  group,
  done,
}: {
  context: ResourceContext;
  group?: ResourceMeta;
  done: (notice?: string) => void;
}) {
  const mutation = useResourceMutation(context, done),
    exists = useResourceTask(context.sessionChanged);
  const [name, setName] = useState(group?.name ?? ""),
    [availability, setAvailability] = useState("");
  return (
    <form
      onSubmit={(event) => {
        if (mutation.busy) {
          event.preventDefault();
          return;
        }
        const values = readValues(event),
          ids = parseResourceIDs(values.domains);
        if (
          !validGroupName(values.name) ||
          !validResourceMark(values.mark) ||
          !ids
        ) {
          mutation.setError(
            "请检查名称（无空白或控制字符，最多 256 字节）、备注（最多 500 字符）及不重复的域 ID（最多 1,000 个）。",
          );
          return;
        }
        const meta: ResourceInput = {
          name: values.name,
          mark: values.mark,
          datas: ids.length ? [{ appName: "ad", resources: ids }] : [],
        };
        mutation.submit(
          group ? "/groups/update" : "/groups/create",
          { ...(group ? { id: group.id } : {}), meta },
          values,
          group ? "资源组已更新。" : "资源组已创建。",
        );
      }}
    >
      <h4>{group ? "编辑资源组" : "新增资源组"}</h4>
      <p className="muted">
        填写已知的当前租户活动域
        ID；这里没有域目录查询或新增功能。留空创建空资源组。提交将完整替换域成员，相关角色的会话可能被撤销。
      </p>
      <ErrorNotice error={mutation.error} />
      <ErrorNotice error={exists.error} />
      <fieldset disabled={mutation.busy}>
        <Field
          label="资源组名称"
          help="1–256 UTF-8 字节，不允许任何 Unicode 空白或控制字符；名称可修改"
        >
          <input
            name="name"
            value={name}
            onChange={(event) => {
              setName(event.target.value);
              setAvailability("");
              exists.cancel();
            }}
            required
          />
        </Field>
        {context.can("GET /api/resources/groups/exists") && (
          <button
            type="button"
            className="secondary"
            disabled={exists.busy}
            onClick={() => {
              if (!validGroupName(name)) {
                exists.setError("请输入有效资源组名称。");
                return;
              }
              const checkedName = name;
              void exists.run(
                async (signal) => {
                  const result = await resourceRequest<{ isExist: boolean }>(
                    `/groups/exists?${queryString({ name: checkedName })}`,
                    signal,
                  );
                  if (typeof result.isExist !== "boolean")
                    throw new ApiError("invalid_response");
                  return result;
                },
                (result) =>
                  setAvailability(
                    `名称「${checkedName}」${result.isExist ? "已存在；编辑原名称时此结果也会为是" : "当前未占用"}。提交时服务器仍会核对。`,
                  ),
              );
            }}
          >
            检查资源组名称
          </button>
        )}
        {availability && <p role="status">{availability}</p>}
        <Field label="资源组备注" help="最多 500 个 Unicode 字符，无控制字符">
          <textarea name="mark" defaultValue={group?.mark ?? ""} />
        </Field>
        <Field
          label="域 ID"
          help="逗号、空格或换行分隔；每项 1–128 位 ASCII 字母、数字、点、下划线或连字符；不允许重复"
        >
          <textarea
            name="domains"
            rows={5}
            defaultValue={group ? groupDomainIDs(group).join("\n") : ""}
          />
        </Field>
        <ProofFields />
      </fieldset>
      <FormActions busy={mutation.busy} cancel={() => done()}>
        保存资源组
      </FormActions>
    </form>
  );
}
function AssociationList({
  context,
  id,
}: {
  context: ResourceContext;
  id: string;
}) {
  const [query, setQuery] = useState({ pageIdx: 1, pageSize: 20 }),
    [data, setData] = useState<AssociatedRoles | null>(null),
    [revision, setRevision] = useState(0);
  const task = useResourceTask(context.sessionChanged),
    queryText = queryString({ id, ...query });
  useEffect(() => {
    setData(null);
    void task.run(
      (signal) =>
        resourceRequest<AssociatedRoles>(`/groups/roles?${queryText}`, signal),
      setData,
      true,
    );
  }, [queryText, revision]);
  return (
    <section className="resource-associations">
      <h4>关联角色</h4>
      <p className="muted">
        内置 platform_admin 和 viewer 同样需要显式关联，均无隐含的全部域权限。
      </p>
      <form
        onSubmit={(event) => {
          const values = readValues(event);
          setQuery({ pageIdx: 1, pageSize: Number(values.pageSize) });
        }}
      >
        <PageSize />
        <button>读取关联角色</button>
        <button
          className="secondary"
          type="button"
          onClick={() => setRevision((n) => n + 1)}
        >
          刷新关联角色
        </button>
      </form>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在读取关联角色…</p>}
      {data && (
        <>
          {data.details.length ? (
            <ul>
              {data.details.map((role) => (
                <li key={role.id}>
                  {role.name} · {role.id} · {role.mark || "无备注"}
                </li>
              ))}
            </ul>
          ) : (
            <p>没有关联角色。</p>
          )}
          <Pagination
            page={data.page}
            exhausted={data.exhausted}
            busy={task.busy}
            change={(pageIdx) => setQuery({ ...query, pageIdx })}
          />
        </>
      )}
    </section>
  );
}
function AssociationForm({
  context,
  group,
  done,
}: {
  context: ResourceContext;
  group: ResourceMeta;
  done: (notice?: string) => void;
}) {
  const [roles, setRoles] = useState<string[] | null>(null),
    [revision, setRevision] = useState(0);
  const task = useResourceTask(context.sessionChanged),
    mutation = useResourceMutation(context, done);
  useEffect(() => {
    setRoles(null);
    void task.run(
      async (signal) => {
        const data = await resourceRequest<AssociatedRoles>(
          `/groups/roles?${queryString({ id: group.id, pageIdx: 1, pageSize: -1 })}`,
          signal,
        );
        if (!data.exhausted || data.details.length !== data.page.total)
          throw new ApiError("invalid_response");
        return data;
      },
      (data) => setRoles(data.details.map((role) => role.id)),
      true,
    );
  }, [group.id, revision]);
  return (
    <>
      <h4>调整关联角色：{group.name}</h4>
      <p className="warning">
        保存会替换完整关联集合。全部清空即移除所有关联；受影响角色会话将立即失效。非管理员不能改变自己或平台管理员的关联，只能委派自己已有的域范围。
      </p>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在读取完整关联集合…</p>}
      {!roles && (
        <div className="actions">
          <button
            className="secondary"
            onClick={() => setRevision((n) => n + 1)}
          >
            重新读取关联
          </button>
          <button className="secondary" onClick={() => done()}>
            取消
          </button>
        </div>
      )}
      {roles && (
        <form
          onSubmit={(event) => {
            if (mutation.busy) {
              event.preventDefault();
              return;
            }
            const values = readValues(event),
              custom = parseCustomRoleIDs(values.roleIds);
            if (
              !custom ||
              custom.some((id) => ["platform_admin", "viewer"].includes(id))
            ) {
              mutation.setError(
                "自定义角色 ID 必须为 24 位 ASCII 字母、数字、下划线或连字符，不得重复；内置角色请使用勾选项。",
              );
              return;
            }
            const roleIds = [
              ...(values.platform_admin ? ["platform_admin"] : []),
              ...(values.viewer ? ["viewer"] : []),
              ...custom,
            ];
            if (roleIds.length > 100) {
              mutation.setError("最多关联 100 个不同角色。");
              return;
            }
            mutation.submit(
              "/groups/assign",
              { id: group.id, roleIds },
              values,
              "资源组关联已保存。",
            );
          }}
        >
          <ErrorNotice error={mutation.error} />
          <fieldset disabled={mutation.busy}>
            <label className="resource-check">
              <input
                type="checkbox"
                name="platform_admin"
                defaultChecked={roles.includes("platform_admin")}
              />
              显式关联 platform_admin
            </label>
            <label className="resource-check">
              <input
                type="checkbox"
                name="viewer"
                defaultChecked={roles.includes("viewer")}
              />
              显式关联 viewer
            </label>
            <Field
              label="自定义角色 ID"
              help="填写已有角色的 24 位完整 ID，逗号、空格或换行分隔。整个集合最多 100 个角色"
            >
              <textarea
                name="roleIds"
                defaultValue={roles
                  .filter((id) => !["platform_admin", "viewer"].includes(id))
                  .join("\n")}
              />
            </Field>
            <ProofFields />
          </fieldset>
          <FormActions busy={mutation.busy} cancel={() => done()}>
            保存角色关联
          </FormActions>
        </form>
      )}
    </>
  );
}
function DeleteGroup({
  context,
  group,
  done,
}: {
  context: ResourceContext;
  group: ResourceMeta;
  done: (notice?: string) => void;
}) {
  const mutation = useResourceMutation(context, done);
  return (
    <form
      onSubmit={(event) => {
        if (mutation.busy) {
          event.preventDefault();
          return;
        }
        const values = readValues(event);
        mutation.submit(
          "/groups/delete",
          { id: group.id },
          values,
          "资源组已删除。",
        );
      }}
    >
      <h4>删除资源组：{group.name}</h4>
      <p className="warning">
        将删除资源组及其域成员、{group.applyRoleCount}{" "}
        个角色关联，并撤销受影响角色会话；不会删除域目录记录。请确认目标 ID：
        {group.id}。
      </p>
      <ErrorNotice error={mutation.error} />
      <fieldset disabled={mutation.busy}>
        <ProofFields />
      </fieldset>
      <FormActions busy={mutation.busy} cancel={() => done()}>
        确认删除资源组
      </FormActions>
    </form>
  );
}
