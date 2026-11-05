import { useLayoutEffect, useRef, useState } from "react";
import { ApiError } from "./api";
import { errorText, type Role, type UserList } from "./access-api";
import {
  ErrorNotice,
  Field,
  Pagination,
  type AccessContext,
} from "./access-common";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  canReadRoleUsers,
  readRoleUsers,
  roleUsersIdentity,
  roleUsersPageSizes,
  validRoleUsersTarget,
  validRoleUsersSearch,
  type RoleUsersQuery,
} from "./role-users-read";

type Props = {
  context: AccessContext;
  role: Role;
  close: () => void;
  returnToList: () => void;
  returnToRole?: () => void;
  accessDenied?: () => void;
};
export default function RoleUsersReadPanel({
  context,
  role,
  close,
  returnToList,
  returnToRole,
  accessDenied,
}: Props) {
  const binding = useRef({
    identity: roleUsersIdentity(context.profile),
    roleID: role.id,
  });
  const [query, setQuery] = useState<RoleUsersQuery>({
    roleID: role.id,
    pageIdx: 1,
    pageSize: 20,
    search: "",
  });
  const [search, setSearch] = useState(""),
    [size, setSize] = useState(20);
  const [data, setData] = useState<UserList | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [closed, setClosed] = useState(false);
  const generation = useRef(0),
    abort = useRef<AbortController | null>(null),
    locked = useRef(false);
  const scope =
    binding.current.identity === roleUsersIdentity(context.profile) &&
    binding.current.roleID === role.id &&
    canReadRoleUsers(context) &&
    validRoleUsersTarget(role) &&
    !context.profile.needChangePwd &&
    !context.profile.isExpired;
  const current = useRef({ context, scope, closed, close, accessDenied });
  current.current = { context, scope, closed, close, accessDenied };
  const invalidate = () => {
    generation.current++;
    abort.current?.abort();
    locked.current = false;
  };
  const dismiss = (done: () => void) => {
    current.current.closed = true;
    invalidate();
    setData(null);
    setClosed(true);
    done();
  };
  const endAccess = (message: string) => {
    current.current.closed = true;
    invalidate();
    setData(null);
    setBusy(false);
    setClosed(true);
    setError(message);
  };
  const read = async (next: RoleUsersQuery) => {
    if (!current.current.scope || current.current.closed || locked.current)
      return;
    invalidate();
    locked.current = true;
    setData(null);
    setBusy(true);
    setError("");
    setQuery(next);
    const id = generation.current,
      controller = new AbortController();
    abort.current = controller;
    const live = () =>
      !controller.signal.aborted &&
      id === generation.current &&
      current.current.scope &&
      !current.current.closed;
    try {
      const result = await readRoleUsers(
        next,
        current.current.context.profile,
        controller.signal,
      );
      if (live()) setData(result);
    } catch (failure) {
      if (!live()) return;
      if (
        failure instanceof ApiError &&
        (failure.status === 401 ||
          ["unauthenticated", "password_change_required"].includes(
            failure.code,
          ))
      ) {
        endAccess("登录身份或会话已失效，已清除关联用户。请重新核对登录状态。");
        current.current.context.sessionChanged();
      } else if (failure instanceof ApiError && failure.status === 403) {
        endAccess(
          "关联用户读取权限已不可用，已清除数据并关闭读取入口。请重新核对授权。",
        );
        current.current.accessDenied?.();
      } else setError(`关联用户读取失败，未展示旧数据。${errorText(failure)}`);
    } finally {
      if (live()) {
        locked.current = false;
        setBusy(false);
      }
    }
  };
  useLayoutEffect(() => {
    if (!scope)
      endAccess(
        "身份、会话、目标角色或读取权限已改变，已清除关联用户。请返回角色列表重新选择。",
      );
  }, [scope]);
  useLayoutEffect(() => {
    void read(query);
    const history = () =>
      endAccess("已离开关联用户页面，数据已清除。请返回角色列表重新选择。");
    const invalidation = listenForSessionInvalidation(() => {
      endAccess("登录状态已变化，已清除关联用户。请重新核对登录状态。");
      current.current.context.sessionChanged();
    });
    window.addEventListener("popstate", history);
    window.addEventListener("hashchange", history);
    return () => {
      invalidate();
      invalidation.close();
      window.removeEventListener("popstate", history);
      window.removeEventListener("hashchange", history);
    };
  }, []);
  const applySearch = () => {
    if (locked.current || !current.current.scope || current.current.closed)
      return;
    if (!validRoleUsersSearch(search)) {
      invalidate();
      setData(null);
      setError("用户名搜索最多 50 个 Unicode 字符，不能含控制字符。");
      return;
    }
    void read({ roleID: role.id, pageIdx: 1, pageSize: size, search });
  };
  const active = scope && !closed;
  return (
    <section
      className="role-users-read"
      aria-label="角色关联用户工作区"
      style={{ overflowWrap: "anywhere" }}
    >
      <style>{`
        @media (max-width: 600px) {
          .role-users-read .table-scroll { overflow: visible; }
          .role-users-read table, .role-users-read tbody, .role-users-read tr, .role-users-read th, .role-users-read td { display: block; width: auto; min-width: 0; }
          .role-users-read caption { display: block; }
          .role-users-read thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
          .role-users-read tbody tr { margin-bottom: 1rem; border: 1px solid currentColor; border-radius: .5rem; padding: .5rem; }
          .role-users-read tbody th, .role-users-read tbody td { padding: .35rem; border: 0; }
          .role-users-read tbody td::before { content: attr(data-label) "："; font-weight: 600; }
        }
      `}</style>
      <h3>
        {active ? `角色关联用户：${role.name}` : "角色关联用户读取已关闭"}
      </h3>
      {active && (
        <>
          <p>固定角色 ID：{role.id}</p>
          <p className="muted">
            角色列表或详情读取时的用户数：{role.userNum}
            。下方总数来自本次用户查询，两者不是同一时点快照。空结果不能证明角色仍存在。
          </p>
          <div className="filters">
            <div className="form-grid">
              <Field
                label="关联用户名搜索"
                help="按用户名进行不区分大小写的字面子串搜索，最多 50 个 Unicode 字符；% 和 _ 不作为通配符。"
              >
                <input
                  value={search}
                  autoComplete="off"
                  onChange={(event) => setSearch(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      applySearch();
                    }
                  }}
                />
              </Field>
              <Field label="关联用户每页条数">
                <select
                  value={size}
                  onChange={(event) => setSize(Number(event.target.value))}
                >
                  {roleUsersPageSizes.map((value) => (
                    <option key={value} value={value}>
                      {value}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
            <div className="actions compact">
              <button type="button" disabled={busy} onClick={applySearch}>
                搜索关联用户
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => {
                  setSearch("");
                  setSize(20);
                  void read({
                    roleID: role.id,
                    pageIdx: 1,
                    pageSize: 20,
                    search: "",
                  });
                }}
              >
                清除关联用户搜索
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => void read(query)}
              >
                刷新关联用户
              </button>
            </div>
          </div>
        </>
      )}
      <ErrorNotice error={error} />
      {active && busy && <p role="status">正在读取关联用户…</p>}
      {active && data && (
        <>
          <div className="table-scroll">
            <table>
              <caption>角色关联用户列表</caption>
              <thead>
                <tr>
                  <th scope="col">用户名 / 用户 ID</th>
                  <th scope="col">当前角色 / 角色 ID</th>
                  <th scope="col">手机号</th>
                  <th scope="col">邮箱</th>
                  <th scope="col">账户状态</th>
                  <th scope="col">MFA 状态</th>
                </tr>
              </thead>
              <tbody>
                {data.List.map((user) => (
                  <tr key={user.ID}>
                    <th scope="row">
                      {user.username}
                      <small className="block">ID {user.ID}</small>
                    </th>
                    <td data-label="当前角色">
                      {user.roleName}
                      <small className="block">{user.roleID}</small>
                    </td>
                    <td data-label="手机号">{user.mobile || "未提供"}</td>
                    <td data-label="邮箱">{user.email || "未提供"}</td>
                    <td data-label="账户状态">
                      {user.disabled ? "账户已停用" : "账户已启用"}
                    </td>
                    <td data-label="MFA 状态">
                      {user.hasMfa ? "已开启" : "已关闭"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {data.List.length === 0 && (
            <p role="status">
              当前页没有符合条件的关联用户。空结果不证明角色仍存在。
            </p>
          )}
          {data.page.pageIdx > 1 && (
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => void read({ ...query, pageIdx: 1 })}
            >
              返回关联用户第一页
            </button>
          )}
          <Pagination
            page={data.page}
            exhausted={data.exhausted || data.page.pageIdx >= 1_000_000}
            busy={busy}
            change={(pageIdx) => void read({ ...query, pageIdx })}
          />
        </>
      )}
      <div className="actions compact">
        <button
          type="button"
          className="secondary"
          onClick={() => dismiss(close)}
        >
          关闭关联用户
        </button>
        {active && returnToRole && (
          <button
            type="button"
            className="secondary"
            onClick={() => dismiss(returnToRole)}
          >
            返回角色详情
          </button>
        )}
        <button
          type="button"
          className="secondary"
          onClick={() => dismiss(returnToList)}
        >
          返回角色列表
        </button>
      </div>
    </section>
  );
}
