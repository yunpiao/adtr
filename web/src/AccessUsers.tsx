import { useEffect, useState } from "react";
import { validPassword, validUsername } from "./api";
import {
  accessRequest,
  queryString,
  validUserFields,
  type AccessUser,
  type QueryValue,
  type Role,
  type UserList,
} from "./access-api";
import {
  ErrorNotice,
  Field,
  FormActions,
  PageSize,
  Pagination,
  ProofFields,
  RoleField,
  readValues,
  useAccessTask,
  useMutation,
  type AccessContext,
} from "./access-common";

const profileFields = [
  ["mobile", "手机号"],
  ["email", "邮箱"],
  ["remark", "备注"],
  ["address", "所在地"],
  ["realName", "真实姓名"],
  ["department", "部门"],
  ["post", "岗位"],
] as const;
type View =
  | { kind: "list" }
  | { kind: "create" }
  | { kind: "edit" | "delete"; user: AccessUser }
  | { kind: "assign"; users: AccessUser[] };
export default function AccessUsers({
  context,
  roles,
}: {
  context: AccessContext;
  roles: Role[];
}) {
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
    window.history.pushState({}, "", `#access/users/${next.kind}`);
  };
  return (
    <>
      <h2>用户管理</h2>
      {notice && (
        <div role="status" className="success">
          {notice}
        </div>
      )}
      {view.kind === "list" ? (
        <UsersList key={revision} context={context} roles={roles} open={open} />
      ) : view.kind === "assign" ? (
        <Assignments
          context={context}
          roles={roles}
          users={view.users}
          done={done}
        />
      ) : (
        <UserForm
          key={view.kind}
          context={context}
          roles={roles}
          mode={view.kind}
          user={"user" in view ? view.user : undefined}
          done={done}
        />
      )}
    </>
  );
}
function UsersList({
  context,
  roles,
  open,
}: {
  context: AccessContext;
  roles: Role[];
  open: (view: View) => void;
}) {
  const [query, setQuery] = useState<Record<string, QueryValue>>({
      pageIdx: 1,
      pageSize: 20,
      sort: -1,
    }),
    [data, setData] = useState<UserList | null>(null),
    [selected, setSelected] = useState<string[]>([]),
    [revision, setRevision] = useState(0);
  const task = useAccessTask(context.sessionChanged);
  const queryText = queryString(query);
  useEffect(() => {
    setData(null);
    setSelected([]);
    void task.run(
      (signal) => accessRequest<UserList>(`/users?${queryText}`, signal),
      setData,
      true,
    );
  }, [queryText, revision]);
  return (
    <>
      <div className="actions compact">
        {context.can("POST /api/access/users/create") && (
          <button onClick={() => open({ kind: "create" })}>新增用户</button>
        )}
        {context.can("POST /api/access/assignments") && (
          <button
            className="secondary"
            disabled={selected.length === 0}
            onClick={() =>
              open({
                kind: "assign",
                users: data!.List.filter((user) =>
                  selected.includes(user.username),
                ),
              })
            }
          >
            批量分配（{selected.length}）
          </button>
        )}
        <button className="secondary" onClick={() => setRevision((n) => n + 1)}>
          刷新用户列表
        </button>
      </div>
      <form
        className="filters"
        onSubmit={(event) => {
          event.preventDefault();
          const form = new FormData(event.currentTarget);
          const next: Record<string, QueryValue> = {
            pageIdx: 1,
            pageSize: Number(form.get("pageSize")),
            sort: Number(form.get("sort")),
            search: String(form.get("search") ?? ""),
            isSelf: form.has("isSelf"),
            roleID: String(form.get("roleID") ?? ""),
            filterRole: String(form.get("filterRole") ?? "")
              .split(",")
              .map((v) => v.trim())
              .filter(Boolean),
            filterMfaStatus: form.getAll("filterMfaStatus") as string[],
            filterPassStrength: form.getAll("filterPassStrength") as string[],
          };
          for (const key of [
            "filterStartCreateTm",
            "filterEndCreateTm",
            "filterStartPassTm",
            "filterEndPassTm",
          ]) {
            const raw = String(form.get(key) ?? "");
            if (raw) {
              const date = new Date(raw);
              if (!Number.isFinite(date.getTime())) {
                task.setError("请输入有效的日期时间。");
                return;
              }
              next[key] = date.toISOString();
            }
          }
          for (const pair of [
            ["filterStartCreateTm", "filterEndCreateTm"],
            ["filterStartPassTm", "filterEndPassTm"],
          ])
            if (
              next[pair[0]] &&
              next[pair[1]] &&
              String(next[pair[0]]) >= String(next[pair[1]])
            ) {
              task.setError("结束时间必须晚于开始时间。");
              return;
            }
          setQuery(next);
        }}
      >
        <div className="form-grid">
          <Field label="搜索用户名">
            <input name="search" maxLength={50} />
          </Field>
          <PageSize />
          <Field label="排序">
            <select name="sort" defaultValue="-1">
              <option value="-1">创建时间：新到旧</option>
              <option value="1">创建时间：旧到新</option>
              <option value="-2">密码更新时间：新到旧</option>
              <option value="2">密码更新时间：旧到新</option>
            </select>
          </Field>
        </div>
        <details>
          <summary>更多筛选条件</summary>
          <div className="form-grid">
            <Field
              label="筛选角色 ID"
              help="多个角色 ID 用英文逗号分隔，同字段内为任一匹配。"
            >
              <input name="filterRole" />
            </Field>
            <Field label="分配列表角色 ID">
              <input name="roleID" list="assignment-filter-roles" />
              <datalist id="assignment-filter-roles">
                {roles.map((role) => (
                  <option key={role.id} value={role.id}>
                    {role.name}
                  </option>
                ))}
              </datalist>
            </Field>
          </div>
          <label className="check">
            <input name="isSelf" type="checkbox" />
            仅当前账户
          </label>
          <fieldset className="filter-group">
            <legend>MFA 状态</legend>
            <label>
              <input type="checkbox" name="filterMfaStatus" value="enable" />
              已开启
            </label>
            <label>
              <input type="checkbox" name="filterMfaStatus" value="stop" />
              已关闭
            </label>
            <small>不支持独立的 MFA 禁用策略；账户停用状态另列。</small>
          </fieldset>
          <fieldset className="filter-group">
            <legend>密码强度</legend>
            {[
              ["high", "高"],
              ["middle", "中"],
              ["low", "低"],
            ].map(([value, label]) => (
              <label key={value}>
                <input
                  type="checkbox"
                  name="filterPassStrength"
                  value={value}
                />
                {label}
              </label>
            ))}
          </fieldset>
          <p className="muted">
            时间按浏览器本地时区输入，转换为 UTC
            查询；开始包含，结束不包含。不同筛选字段同时满足。
          </p>
          <div className="form-grid">
            {[
              ["filterStartCreateTm", "创建开始时间"],
              ["filterEndCreateTm", "创建结束时间"],
              ["filterStartPassTm", "密码更新开始时间"],
              ["filterEndPassTm", "密码更新结束时间"],
            ].map(([name, label]) => (
              <Field key={name} label={label}>
                <input type="datetime-local" name={name} />
              </Field>
            ))}
          </div>
        </details>
        <div className="actions compact">
          <button type="submit">应用筛选</button>
          <button
            type="reset"
            className="secondary"
            onClick={() => setQuery({ pageIdx: 1, pageSize: 20, sort: -1 })}
          >
            清除筛选
          </button>
        </div>
      </form>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在读取用户…</p>}
      {data && (
        <>
          <div className="table-scroll">
            <table>
              <caption>平台用户列表</caption>
              <thead>
                <tr>
                  {context.can("POST /api/access/assignments") && (
                    <th scope="col">选择</th>
                  )}
                  <th scope="col">用户名</th>
                  <th scope="col">角色</th>
                  <th scope="col">安全状态</th>
                  <th scope="col">资料</th>
                  <th scope="col">操作</th>
                </tr>
              </thead>
              <tbody>
                {data.List.map((user) => (
                  <tr key={user.ID}>
                    {context.can("POST /api/access/assignments") && (
                      <td>
                        <input
                          type="checkbox"
                          aria-label={`选择 ${user.username}`}
                          checked={selected.includes(user.username)}
                          disabled={
                            !selected.includes(user.username) &&
                            selected.length >= 100
                          }
                          onChange={(event) =>
                            setSelected((old) =>
                              event.target.checked
                                ? [...old, user.username]
                                : old.filter((name) => name !== user.username),
                            )
                          }
                        />
                      </td>
                    )}
                    <th scope="row">
                      {user.username}
                      <small className="block">ID {user.ID}</small>
                    </th>
                    <td>
                      {user.roleName}
                      <small className="block">{user.roleID}</small>
                    </td>
                    <td>
                      {user.disabled ? "账户已停用" : "账户已启用"}
                      <br />
                      MFA {user.hasMfa ? "已开启" : "已关闭"}
                      <br />
                      密码强度：{user.passStrength}
                    </td>
                    <td>
                      <details>
                        <summary>{user.email || "查看用户资料"}</summary>
                        <UserDetails user={user} />
                      </details>
                    </td>
                    <td>
                      <div className="row-actions">
                        {context.can("POST /api/access/users/update") && (
                          <button
                            className="secondary"
                            onClick={() => open({ kind: "edit", user })}
                          >
                            编辑 {user.username}
                          </button>
                        )}
                        {context.can("POST /api/access/assignments") && (
                          <button
                            className="secondary"
                            onClick={() =>
                              open({ kind: "assign", users: [user] })
                            }
                          >
                            分配 {user.username}
                          </button>
                        )}
                        {context.can("POST /api/access/users/delete") && (
                          <button
                            className="secondary danger"
                            onClick={() => open({ kind: "delete", user })}
                          >
                            删除 {user.username}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {data.List.length === 0 && <p role="status">没有符合条件的用户。</p>}
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
function UserDetails({ user }: { user: AccessUser }) {
  return (
    <dl>
      {[
        ["用户名", user.username],
        ["用户 ID", user.ID],
        ["兼容身份", user.role],
        ["权限类别", user.priv],
        ...profileFields.map(([key, label]) => [label, user[key]]),
        ["创建时间（UTC）", user.createTm],
        ["密码更新时间（UTC）", user.pwdUpdateTm],
        ["角色 ID", user.roleID],
        ["角色名称", user.roleName],
        ["头像", "未提供；暂不支持上传"],
      ].map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{value || "未提供"}</dd>
        </div>
      ))}
    </dl>
  );
}
function UserForm({
  context,
  roles,
  mode,
  user,
  done,
}: {
  context: AccessContext;
  roles: Role[];
  mode: "create" | "edit" | "delete";
  user?: AccessUser;
  done: (message?: string) => void;
}) {
  const task = useMutation(context, done),
    [exists, setExists] = useState("");
  return (
    <form
      onSubmit={(event) => {
        const values = readValues(event);
        if (task.busy) return;
        const username = user?.username ?? values.username.toLowerCase();
        if (!validUsername(username)) {
          task.setError(
            "用户名需为 1–64 位 ASCII 字母、数字、点、下划线或连字符。",
          );
          return;
        }
        if (mode === "delete") {
          task.submit("/users/delete", { username }, values, "用户已删除");
          return;
        }
        if (!validUserFields(values)) {
          task.setError(
            "请检查资料：手机号为 1[3-9] 开头的 11 位数字，邮箱为有效地址；资料最多 50 个字符，备注最多 150 个字符，不含控制字符。",
          );
          return;
        }
        if (
          mode === "create" &&
          (!validPassword(values.password) ||
            values.password !== values.confirmPassword)
        ) {
          task.setError("初始密码需为 12–64 个字符，两次输入必须一致。");
          return;
        }
        const body: Record<string, unknown> = {
          username,
          ...(context.can("POST /api/access/assignments")
            ? { roleID: values.roleID }
            : {}),
          ...Object.fromEntries(
            profileFields.map(([key]) => [key, values[key]]),
          ),
        };
        if (mode === "create") body.password = values.password;
        else
          body.disabled =
            user!.disabled && !context.can("POST /api/access/assignments")
              ? true
              : values.disabled === "on";
        task.submit(
          `/users/${mode === "create" ? "create" : "update"}`,
          body,
          values,
          mode === "create" ? "用户已创建" : "用户资料已保存",
        );
      }}
    >
      <h3>
        {mode === "create"
          ? "新增用户"
          : mode === "edit"
            ? `编辑用户：${user!.username}`
            : `删除用户：${user!.username}`}
      </h3>
      <ErrorNotice error={task.error} />
      {mode === "delete" ? (
        <p className="warning">
          删除 {user!.username}{" "}
          后，其登录会话会失效。请核对目标；当前账户及最后一个启用的管理员不能删除。
        </p>
      ) : (
        <fieldset disabled={task.busy}>
          <Field
            label="用户名"
            help="创建后不可修改；ASCII 字母、数字、点、下划线、连字符，最多 64 位。"
          >
            <input
              name="username"
              defaultValue={user?.username}
              disabled={mode === "edit"}
              required
              maxLength={64}
              autoComplete="off"
            />
          </Field>
          {mode === "create" && (
            <>
              {context.can("GET /api/access/users/exists") && (
                <button
                  type="button"
                  className="secondary"
                  onClick={(event) => {
                    const form = event.currentTarget.form!;
                    const username = String(
                      new FormData(form).get("username") ?? "",
                    ).toLowerCase();
                    if (!validUsername(username)) {
                      task.setError("请输入有效用户名后检查。");
                      return;
                    }
                    void task.run(
                      (signal) =>
                        accessRequest<{ result: boolean }>(
                          `/users/exists?${queryString({ username })}`,
                          signal,
                        ),
                      (result) =>
                        setExists(
                          result.result
                            ? "用户名已存在"
                            : "用户名当前可用，提交时仍由服务器检查",
                        ),
                    );
                  }}
                >
                  检查用户名
                </button>
              )}
              {exists && <p role="status">{exists}</p>}
              <Field
                label="初始密码"
                help="12–64 个 Unicode 字符；新用户首次登录必须修改。"
              >
                <input
                  name="password"
                  type="password"
                  required
                  autoComplete="new-password"
                />
              </Field>
              <Field label="确认初始密码">
                <input
                  name="confirmPassword"
                  type="password"
                  required
                  autoComplete="new-password"
                />
              </Field>
            </>
          )}
          {context.can("POST /api/access/assignments") ? (
            <RoleField roles={roles} value={user?.roleID ?? "viewer"} />
          ) : (
            <p>
              角色：{user?.roleName ?? "viewer"}（当前账户没有分配角色权限）
            </p>
          )}
          <div className="form-grid">
            {profileFields.map(([name, label]) => (
              <Field
                key={name}
                label={label}
                help={
                  name === "remark"
                    ? "最多 150 个字符；留空清除。"
                    : name === "mobile"
                      ? "可选，11 位中国大陆手机号。"
                      : "可选；留空清除。"
                }
              >
                {name === "remark" ? (
                  <textarea
                    name={name}
                    defaultValue={user?.[name] ?? ""}
                    rows={3}
                  />
                ) : (
                  <input
                    name={name}
                    defaultValue={user?.[name] ?? ""}
                    type={name === "email" ? "email" : "text"}
                    autoComplete="off"
                  />
                )}
              </Field>
            ))}
          </div>
          {mode === "edit" &&
            user?.disabled &&
            !context.can("POST /api/access/assignments") && (
              <p className="warning">
                重新启用账户需要角色分配权限。当前仅可编辑资料，账户将保持停用。
              </p>
            )}
          {mode === "edit" && (
            <label className="check">
              <input
                name="disabled"
                type="checkbox"
                defaultChecked={user?.disabled}
                disabled={
                  user?.disabled && !context.can("POST /api/access/assignments")
                }
              />
              停用账户（将撤销其会话；不能停用自己）
            </label>
          )}
        </fieldset>
      )}
      <fieldset disabled={task.busy}>
        <ProofFields />
      </fieldset>
      <FormActions busy={task.busy} cancel={() => done()}>
        {mode === "create"
          ? "创建用户"
          : mode === "edit"
            ? "保存用户"
            : "确认删除用户"}
      </FormActions>
    </form>
  );
}
function Assignments({
  context,
  roles,
  users,
  done,
}: {
  context: AccessContext;
  roles: Role[];
  users: AccessUser[];
  done: (message?: string) => void;
}) {
  const task = useMutation(context, done);
  return (
    <form
      onSubmit={(event) => {
        const values = readValues(event);
        if (task.busy) return;
        task.submit(
          "/assignments",
          {
            userRoles: users.map((user, i) => ({
              username: user.username,
              roleID: values[`role-${i}`],
            })),
          },
          values,
          "角色分配已保存，受影响用户的会话已撤销",
        );
      }}
    >
      <h3>{users.length === 1 ? "单个角色分配" : "批量角色分配"}</h3>
      <p>
        一次原子提交 {users.length}{" "}
        个用户，每个用户可选择不同角色。任意一项未获授权或无效时，不会部分保存。
      </p>
      <ErrorNotice error={task.error} />
      <fieldset disabled={task.busy}>
        {users.map((user, i) => (
          <RoleField
            key={user.username}
            roles={roles}
            value={user.roleID}
            name={`role-${i}`}
            label={`角色：${user.username}`}
          />
        ))}
        <ProofFields />
      </fieldset>
      <FormActions busy={task.busy} cancel={() => done()}>
        保存角色分配
      </FormActions>
    </form>
  );
}
