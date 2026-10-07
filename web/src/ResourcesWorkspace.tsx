import { useEffect, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import { ErrorNotice } from "./access-common";
import { resourceOperations } from "./resource-api";
import { useResourceTask, type ResourceContext } from "./resource-common";
import ResourceGroups from "./ResourceGroups";
import ResourceTenant from "./ResourceTenant";
import ResourceScope from "./ResourceScope";

type Tab = "scope" | "groups" | "tenant";
export default function ResourcesWorkspace({
  profile,
  sessionChanged,
}: {
  profile: Profile;
  sessionChanged: () => void;
}) {
  const [capabilities, setCapabilities] = useState<{
      menu: Permission[];
      results: boolean[];
    } | null>(null),
    [active, setActive] = useState<Tab>("scope"),
    [revision, setRevision] = useState(0);
  const task = useResourceTask(sessionChanged);
  useEffect(() => {
    setCapabilities(null);
    void task.run(
      async (signal) => {
        const [menu, checks] = await Promise.all([
          accessRequest<{ menu: Permission[] }>("/menu", signal),
          accessRequest<{ results: boolean[] }>(
            "/check",
            signal,
            { paths: resourceOperations },
            profile.csrfToken,
          ),
        ]);
        if (
          !Array.isArray(menu.menu) ||
          !Array.isArray(checks.results) ||
          checks.results.length !== resourceOperations.length ||
          checks.results.some((value) => typeof value !== "boolean")
        )
          throw new ApiError("invalid_response");
        return { menu: menu.menu, results: checks.results };
      },
      setCapabilities,
      true,
    );
  }, [profile.csrfToken, revision]);
  const context: ResourceContext = {
    profile,
    sessionChanged,
    can: (operation) => {
      if (capabilities?.results[resourceOperations.indexOf(operation)] !== true)
        return false;
      if (!operation.includes("/groups")) return true;
      const grant = capabilities.menu.find(
        (node) => node.mark === "roles",
      )?.auth;
      return (
        !!grant?.readable &&
        (!operation.startsWith("POST") || !!grant?.writeable)
      );
    },
  };
  const tabs: { id: Tab; label: string; visible: boolean }[] = [
    {
      id: "scope",
      label: "当前数据权限",
      visible:
        context.can("GET /api/resources/grants") ||
        context.can("POST /api/resources/check"),
    },
    {
      id: "groups",
      label: "资源组",
      visible: context.can("GET /api/resources/groups"),
    },
    {
      id: "tenant",
      label: "租户配置",
      visible: context.can("GET /api/resources/tenant"),
    },
  ];
  return (
    <div className="resource-workspace">
      <h2>资源与租户</h2>
      <p className="warning">
        功能管理权限不自动授予 AD
        数据权限，包括平台管理员。只有显式资源组关联才可能授予域范围。
      </p>
      <p className="muted">
        这里管理本地授权范围，不连接或查询 AD
        目录。空范围即无可访问域；没有虚构的示例域。服务器逐次校验租户、角色及当前域状态。
      </p>
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在确认资源功能权限…</p>}
      <button className="secondary" onClick={() => setRevision((n) => n + 1)}>
        重新读取资源权限
      </button>
      {capabilities && (
        <>
          <nav className="access-tabs" aria-label="资源与租户功能">
            {tabs
              .filter((tab) => tab.visible)
              .map((tab) => (
                <button
                  key={tab.id}
                  className={active === tab.id ? "selected" : ""}
                  aria-current={active === tab.id ? "page" : undefined}
                  onClick={() => {
                    setActive(tab.id);
                    window.history.pushState({}, "", `#resources/${tab.id}`);
                  }}
                >
                  {tab.label}
                </button>
              ))}
          </nav>
          {!tabs.some((tab) => tab.visible) && (
            <p role="status">服务器没有授予可用的资源功能权限。</p>
          )}
          {active === "scope" && tabs[0].visible && (
            <ResourceScope key={`scope-${revision}`} context={context} />
          )}
          {active === "groups" && tabs[1].visible && (
            <ResourceGroups key={`groups-${revision}`} context={context} />
          )}
          {active === "tenant" && tabs[2].visible && (
            <ResourceTenant key={`tenant-${revision}`} context={context} />
          )}
        </>
      )}
    </div>
  );
}
