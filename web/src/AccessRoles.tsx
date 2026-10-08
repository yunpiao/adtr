import { useEffect, useState } from "react";
import {
  accessRequest,
  permissionInputs,
  queryString,
  validText,
  type Permission,
  type PermissionInput,
  type QueryValue,
  type Role,
  type RoleList,
} from "./access-api";
import {
  ErrorNotice,
  Field,
  FormActions,
  PageSize,
  Pagination,
  PermissionEditor,
  ProofFields,
  RoleField,
  emptyPermissions,
  readValues,
  useAccessTask,
  useMutation,
  type AccessContext,
} from "./access-common";

export function grantMetadata(
  metadata: Permission[],
  context: AccessContext,
): Permission[] {
  return metadata.map((node) => {
    const actor = context.menu.find((item) => item.mark === node.mark)?.auth;
    return {
      ...node,
      allow_auth: {
        readable: !!actor?.readable && node.allow_auth.readable,
        writeable: !!actor?.writeable && node.allow_auth.writeable,
      },
    };
  });
}
type View =
  | { kind: "list" }
  | { kind: "create" }
  | { kind: "detail" | "delete"; role: Role };
export default function AccessRoles({ context }: { context: AccessContext }) {
  const [view, setView] = useState<View>({ kind: "list" }),
    [notice, setNotice] = useState(""),
    [revision, setRevision] = useState(0);
  const done = (message = "已返回列表，请核对最新服务器状态。") => {
    setNotice(message);
    setRevision((n) => n + 1);
    setView({ kind: "list" });
  };
  const open = (next: View) => {
    setNotice("");
    setView(next);
    window.history.pushState({}, "", `#access/roles/${next.kind}`);
  };
  return (
    <>
      <h2>角色管理</h2>
      {notice && (
        <div className="success" role="status">
          {notice}
        </div>
      )}
      {view.kind === "list" ? (
        <RolesList key={revision} context={context} open={open} />
      ) : (
        <RoleForm
          context={context}
          mode={view.kind}
          role={"role" in view ? view.role : undefined}
          done={done}
        />
      )}
    </>
  );
}
function RolesList({
  context,
  open,
}: {
  context: AccessContext;
  open: (view: View) => void;
}) {
  const [query, setQuery] = useState<Record<string, QueryValue>>({
      pageIdx: 1,
      pageSize: 20,
      sort: -1,
    }),
    [data, setData] = useState<RoleList | null>(null),
    [revision, setRevision] = useState(0);
  const task = useAccessTask(context.sessionChanged),
    queryText = queryString(query);
  useEffect(() => {
    setData(null);
    void task.run(
      (signal) => accessRequest<RoleList>(`/roles?${queryText}`, signal),
      setData,
      true,
    );
  }, [queryText, revision]);
  return (
    <>
      <div className="actions compact">
        {context.can("POST /api/access/roles/save") && (
          <button onClick={() => open({ kind: "create" })}>新增角色</button>
        )}
        <button className="secondary" onClick={() => setRevision((n) => n + 1)}>
          刷新角色列表
        </button>
      </div>
      <form
        className="filters"
        onSubmit={(event) => {
          const values = readValues(event);
          setQuery({
            pageIdx: 1,
            pageSize: Number(values.pageSize),
            search: values.search,
            sort: Number(values.sort),
          });
        }}
      >
        <div className="form-grid">
          <Field label="搜索角色名称">
            <input name="search" maxLength={50} />
          </Field>
          <PageSize />
          <Field label="角色排序">
            <select name="sort" defaultValue="-1">
              <option value="-1">创建时间：新到旧</option>
              <option value="1">创建时间：旧到新</option>
            </select>
          </Field>
        </div>
        <div className="actions compact">
          <button>筛选角色</button>
          <button
            type="reset"
            className="secondary"
            onClick={() => setQuery({ pageIdx: 1, pageSize: 20, sort: -1 })}
          >
            清除角色筛选
          </button>
        </div>
      </form>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在读取角色…</p>}
      {data && (
        <>
          <div className="table-scroll">
            <table>
              <caption>角色列表</caption>
              <thead>
                <tr>
                  <th scope="col">角色名称</th>
                  <th scope="col">用户数</th>
                  <th scope="col">备注</th>
                  <th scope="col">创建时间（UTC）</th>
                  <th scope="col">操作</th>
                </tr>
              </thead>
              <tbody>
                {data.list.map((role) => (
                  <tr key={role.id}>
                    <th scope="row">
                      {role.name}
                      <small className="block">{role.id}</small>
                    </th>
                    <td>{role.userNum}</td>
                    <td>{role.remark || "未提供"}</td>
                    <td>{role.created}</td>
                    <td>
                      <div className="row-actions">
                        {context.can("GET /api/access/roles/detail") && (
                          <button
                            className="secondary"
                            onClick={() => open({ kind: "detail", role })}
                          >
                            详情 {role.name}
                          </button>
                        )}
                        {context.can("POST /api/access/roles/delete") && (
                          <button
                            className="secondary danger"
                            disabled={!role.allowDelete}
                            title={
                              !role.allowDelete
                                ? "内置角色或仍被使用的角色不能删除"
                                : undefined
                            }
                            onClick={() => open({ kind: "delete", role })}
                          >
                            删除 {role.name}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {data.list.length === 0 && <p role="status">没有符合条件的角色。</p>}
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
function RoleForm({
  context,
  mode,
  role,
  done,
}: {
  context: AccessContext;
  mode: "create" | "detail" | "delete";
  role?: Role;
  done: (message?: string) => void;
}) {
  const task = useMutation(context, done),
    [detail, setDetail] = useState<{
      role: Role;
      permissions: Permission[];
    } | null>(null),
    [permissions, setPermissions] =
      useState<PermissionInput[]>(emptyPermissions),
    [exists, setExists] = useState("");
  useEffect(() => {
    if (mode !== "detail") return;
    void task.run(
      (signal) =>
        accessRequest<{ role: Role; permissions: Permission[] }>(
          `/roles/detail?${queryString({ roleID: role!.id })}`,
          signal,
        ),
      (value) => {
        setDetail(value);
        setPermissions(permissionInputs(value.permissions));
      },
    );
  }, []);
  const editable =
    mode === "create" ||
    (!!detail?.role.allowEdit && context.can("POST /api/access/roles/save"));
  const currentRole = detail?.role ?? role;
  if (mode === "detail" && !detail)
    return (
      <>
        <ErrorNotice error={task.error} />
        {task.busy && <p role="status">正在读取角色详情…</p>}
        <button className="secondary" onClick={() => done()}>
          返回角色列表
        </button>
      </>
    );
  return (
    <form
      onSubmit={(event) => {
        const values = readValues(event);
        if (task.busy) return;
        if (mode === "delete") {
          task.submit(
            "/roles/delete",
            { roleID: role!.id },
            values,
            "角色已删除",
          );
          return;
        }
        const name = currentRole?.name ?? values.roleName;
        if (
          !name.trim() ||
          !validText(name, 50) ||
          !validText(values.remark, 150)
        ) {
          task.setError(
            "角色名称需为 1–50 个字符，备注最多 150 个字符，不能含控制字符。",
          );
          return;
        }
        task.submit(
          "/roles/save",
          {
            ...(currentRole ? { roleID: currentRole.id } : {}),
            roleName: name,
            remark: values.remark,
            permissions: permissionInputs(permissions),
          },
          values,
          "角色已保存",
        );
      }}
    >
      <h3>
        {mode === "create"
          ? "新增角色"
          : mode === "delete"
            ? `删除角色：${role!.name}`
            : `角色详情：${currentRole!.name}`}
      </h3>
      <ErrorNotice error={task.error} />
      {mode === "delete" ? (
        <p className="warning">
          确认删除角色 {role!.name}？内置角色及仍被分配的角色不能删除。
        </p>
      ) : (
        <>
          {currentRole && (
            <dl>
              <div>
                <dt>角色 ID</dt>
                <dd>{currentRole.id}</dd>
              </div>
              <div>
                <dt>分配用户数</dt>
                <dd>{currentRole.userNum}</dd>
              </div>
              <div>
                <dt>创建时间（UTC）</dt>
                <dd>{currentRole.created}</dd>
              </div>
              <div>
                <dt>允许编辑 / 删除</dt>
                <dd>
                  {currentRole.allowEdit ? "是" : "否"} /{" "}
                  {currentRole.allowDelete ? "是" : "否"}
                </dd>
              </div>
            </dl>
          )}
          <fieldset disabled={task.busy || !editable}>
            <Field
              label="角色名称"
              help="1–50 个 Unicode 字符，不区分大小写唯一；创建后不可更名。"
            >
              <input
                name="roleName"
                defaultValue={currentRole?.name ?? ""}
                readOnly={mode === "detail"}
                required
              />
            </Field>
            {mode === "create" &&
              context.can("GET /api/access/roles/exists") && (
                <button
                  type="button"
                  className="secondary"
                  onClick={(event) => {
                    const name = String(
                      new FormData(event.currentTarget.form!).get("roleName") ??
                        "",
                    );
                    if (!name.trim() || !validText(name, 50)) {
                      task.setError("请输入有效角色名称后检查。");
                      return;
                    }
                    void task.run(
                      (signal) =>
                        accessRequest<{ exists: boolean }>(
                          `/roles/exists?${queryString({ name })}`,
                          signal,
                        ),
                      (data) =>
                        setExists(
                          data.exists
                            ? "角色名称已存在"
                            : "角色名称当前可用，提交时仍由服务器检查",
                        ),
                    );
                  }}
                >
                  检查角色名称
                </button>
              )}
            {exists && <p role="status">{exists}</p>}
            <Field label="角色备注">
              <textarea
                name="remark"
                rows={3}
                defaultValue={currentRole?.remark ?? ""}
              />
            </Field>
            <PermissionEditor
              value={permissions}
              metadata={grantMetadata(
                detail?.permissions ?? context.menu,
                context,
              )}
              onChange={setPermissions}
              readOnly={!editable}
            />
          </fieldset>
          <p className="warning">
            数据源范围当前为空；此功能不提供 AD
            资源隔离或数据源授权。内置角色权限不可修改。修改角色权限会撤销受影响用户的会话。
          </p>
        </>
      )}
      {editable || mode === "delete" ? (
        <>
          <fieldset disabled={task.busy}>
            <ProofFields />
          </fieldset>
          <FormActions busy={task.busy} cancel={() => done()}>
            {mode === "delete" ? "确认删除角色" : "保存角色"}
          </FormActions>
        </>
      ) : (
        <button className="secondary" type="button" onClick={() => done()}>
          返回角色列表
        </button>
      )}
    </form>
  );
}
export function AccessPermissions({
  context,
  roles,
}: {
  context: AccessContext;
  roles: Role[];
}) {
  const [target, setTarget] = useState(""),
    [revision, setRevision] = useState(0),
    [notice, setNotice] = useState("");
  return (
    <>
      <h2>功能权限</h2>
      {notice && (
        <div className="success" role="status">
          {notice}
        </div>
      )}
      <form
        onSubmit={(event) => {
          const values = readValues(event);
          setTarget(values.roleID);
          setRevision((n) => n + 1);
          setNotice("");
        }}
      >
        <RoleField roles={roles} label="权限角色 ID" />
        <button>读取权限</button>
      </form>
      {target && (
        <PermissionsForm
          key={`${target}-${revision}`}
          context={context}
          roleID={target}
          done={(message) => {
            setNotice(message ?? "已重新读取服务器权限。");
            setRevision((n) => n + 1);
          }}
        />
      )}
    </>
  );
}
function PermissionsForm({
  context,
  roleID,
  done,
}: {
  context: AccessContext;
  roleID: string;
  done: (message?: string) => void;
}) {
  const task = useMutation(context, done),
    [metadata, setMetadata] = useState<Permission[] | null>(null),
    [permissions, setPermissions] = useState<PermissionInput[]>([]);
  useEffect(() => {
    void task.run(
      (signal) =>
        accessRequest<{ permissions: Permission[] }>(
          `/permissions?${queryString({ roleID })}`,
          signal,
        ),
      (value) => {
        setMetadata(value.permissions);
        setPermissions(permissionInputs(value.permissions));
      },
    );
  }, []);
  const editable =
    context.can("POST /api/access/permissions/save") &&
    !["platform_admin", "viewer"].includes(roleID);
  return (
    <form
      onSubmit={(event) => {
        const values = readValues(event);
        if (task.busy) return;
        task.submit(
          "/permissions/save",
          { roleID, permissions: permissionInputs(permissions) },
          values,
          "权限已保存，受影响用户的会话已撤销",
        );
      }}
    >
      <h3>角色权限：{roleID}</h3>
      <ErrorNotice error={task.error} />
      {task.busy && !metadata && <p role="status">正在读取功能权限…</p>}
      {metadata && (
        <>
          <fieldset disabled={task.busy}>
            <PermissionEditor
              value={permissions}
              metadata={grantMetadata(metadata, context)}
              onChange={setPermissions}
              readOnly={!editable}
            />
          </fieldset>
          <p className="warning">
            只能授予自己拥有的功能权限。内置角色及自己所属角色不能由自定义角色修改；服务器是最终授权依据。
          </p>
          {editable && (
            <>
              <fieldset disabled={task.busy}>
                <ProofFields />
              </fieldset>
              <FormActions busy={task.busy} cancel={() => done()}>
                保存功能权限
              </FormActions>
            </>
          )}
        </>
      )}
    </form>
  );
}
