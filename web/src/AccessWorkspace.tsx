import { useEffect, useState } from "react";
import { ApiError, type Profile } from "./api";
import {
  accessRequest,
  labels,
  marks,
  operations,
  type Mark,
  type Permission,
  type Role,
  type RoleList,
} from "./access-api";
import {
  ErrorNotice,
  useAccessTask,
  type AccessContext,
} from "./access-common";
import AccessUsers from "./AccessUsers";
import AccessRoles, { AccessPermissions } from "./AccessRoles";
export default function AccessWorkspace({
  profile,
  sessionChanged,
}: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  const [access, setAccess] = useState<{
      menu: Permission[];
      results: boolean[];
    } | null>(null),
    [active, setActive] = useState<Mark | null>(null),
    [revision, setRevision] = useState(0),
    [roles, setRoles] = useState<Role[]>([]);
  const task = useAccessTask(sessionChanged),
    catalog = useAccessTask(sessionChanged);
  useEffect(() => {
    setAccess(null);
    void task.run(
      async (signal) => {
        const [menu, checks] = await Promise.all([
          accessRequest<{ menu: Permission[] }>("/menu", signal),
          accessRequest<{ results: boolean[] }>(
            "/check",
            signal,
            { paths: operations },
            profile.csrfToken,
          ),
        ]);
        if (
          !Array.isArray(menu.menu) ||
          !Array.isArray(checks.results) ||
          checks.results.length !== operations.length ||
          checks.results.some((value) => typeof value !== "boolean")
        )
          throw new ApiError("invalid_response");
        return { menu: menu.menu, results: checks.results };
      },
      (data) => {
        setAccess(data);
        setActive(
          marks.find(
            (mark) =>
              data.menu.some(
                (node) => node.mark === mark && node.auth.readable,
              ) && data.results[operations.indexOf(`GET /api/access/${mark}`)],
          ) ?? null,
        );
      },
      true,
    );
  }, [revision, profile.csrfToken]);
  const context: AccessContext = {
    profile,
    menu: access?.menu ?? [],
    can: (operation) => access?.results[operations.indexOf(operation)] === true,
    sessionChanged,
  };
  useEffect(() => {
    setRoles([]);
    if (!access || !context.can("GET /api/access/roles") || active === "roles")
      return;
    void catalog.run(
      (signal) =>
        accessRequest<RoleList>("/roles?pageIdx=1&pageSize=-1", signal),
      (data) => setRoles(data.list),
      true,
    );
  }, [active, access]);
  return (
    <div className="access-workspace">
      <h2>访问管理</h2>
      <p className="muted">
        菜单及按钮根据服务器实时权限展示。所有修改需再次验证，服务器逐次校验权限。
      </p>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在确认管理权限…</p>}
      {task.error && (
        <button className="secondary" onClick={() => setRevision((n) => n + 1)}>
          重新读取权限
        </button>
      )}
      {access && (
        <>
          <nav className="access-tabs" aria-label="访问管理功能">
            {marks
              .filter(
                (mark) =>
                  access.menu.some(
                    (node) => node.mark === mark && node.auth.readable,
                  ) && context.can(`GET /api/access/${mark}`),
              )
              .map((mark) => (
                <button
                  key={mark}
                  className={active === mark ? "selected" : ""}
                  aria-current={active === mark ? "page" : undefined}
                  onClick={() => {
                    setActive(mark);
                    window.history.pushState({}, "", `#access/${mark}`);
                  }}
                >
                  {labels[mark]}
                </button>
              ))}
          </nav>
          {!active && <p role="status">当前账户没有访问管理功能权限。</p>}
          <ErrorNotice error={catalog.error} />
          {catalog.error && (
            <p>
              角色候选项未加载；仍可填写已知角色 ID。可以切换页面后重试读取。
            </p>
          )}
          {active === "users" && (
            <AccessUsers context={context} roles={roles} />
          )}
          {active === "roles" && <AccessRoles context={context} />}
          {active === "permissions" && (
            <AccessPermissions context={context} roles={roles} />
          )}
        </>
      )}
    </div>
  );
}
