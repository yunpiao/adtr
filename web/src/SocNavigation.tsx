import { useEffect, useRef, useState } from "react";
import type { Profile } from "./api";

export type SocPage =
  | "account"
  | "profile"
  | "password"
  | "mfa"
  | "reset"
  | "access"
  | "resources"
  | "tasks"
  | "audit"
  | "system"
  | "operational-logs"
  | "domains"
  | "operation-accounts"
  | "credential-use"
  | "directory"
  | "directory-credential-use"
  | "directory-v2"
  | "user-assets-v2"
  | "directory-credential-use-v2";

type NavigationGroup = "management" | "collection" | "identity";
export const SOC_NAVIGATION_GROUPS: ReadonlyArray<{
  id: NavigationGroup;
  label: string;
  pages: readonly SocPage[];
}> = [
  {
    id: "management",
    label: "平台管理",
    pages: ["access", "resources", "system", "operational-logs"],
  },
  {
    id: "collection",
    label: "采集与接入",
    pages: [
      "domains",
      "operation-accounts",
      "credential-use",
      "directory",
      "directory-credential-use",
      "directory-v2",
      "directory-credential-use-v2",
    ],
  },
  {
    id: "identity",
    label: "账户与身份",
    pages: ["account", "profile", "password", "mfa", "reset"],
  },
];

export const SOC_WORKSPACE_LABELS: Record<SocPage, string> = {
  account: "账户概览",
  profile: "个人资料",
  password: "修改密码",
  mfa: "多因素认证",
  reset: "重置用户密码",
  access: "访问管理",
  resources: "资源与租户",
  tasks: "后台任务",
  audit: "操作审计",
  system: "系统健康",
  "operational-logs": "运行日志与诊断包",
  domains: "域连接",
  "operation-accounts": "管理操作账户",
  "credential-use": "凭据授权清理",
  directory: "目录资产",
  "directory-credential-use": "目录读取授权",
  "directory-v2": "补充目录资产",
  "user-assets-v2": "用户资产",
  "directory-credential-use-v2": "补充目录凭据授权",
};

export function socWorkspaceSection(page: SocPage): string {
  return (
    SOC_NAVIGATION_GROUPS.find((group) => group.pages.includes(page))?.label ??
    "安全运营"
  );
}

function initialGroups(page: SocPage): Record<NavigationGroup, boolean> {
  return {
    management: SOC_NAVIGATION_GROUPS[0].pages.includes(page),
    collection: SOC_NAVIGATION_GROUPS[1].pages.includes(page),
    identity: SOC_NAVIGATION_GROUPS[2].pages.includes(page),
  };
}

function NavIcon({ kind }: { kind: "assets" | "tasks" | "audit" | "menu" }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" aria-hidden="true" focusable="false">
      {kind === "assets" && (
        <>
          <path d="m12 3 8 4.5v9L12 21l-8-4.5v-9L12 3Z" />
          <path d="m4 7.5 8 4.5 8-4.5M12 12v9" />
        </>
      )}
      {kind === "tasks" && (
        <>
          <path d="M8 5H5v16h14V5h-3M9 3h6v5H9zM9 12h6M9 16h6" />
        </>
      )}
      {kind === "audit" && (
        <>
          <path d="M6 3h8l4 4v14H6V3Z" />
          <path d="M14 3v5h4M9 12h6M9 16h6" />
        </>
      )}
      {kind === "menu" && <path d="M5 6h14M5 12h14M5 18h14" />}
    </svg>
  );
}

const compactQuery = "(max-width: 760px)";

export default function SocNavigation({
  profile,
  active,
  forced,
  busy,
  gated,
  blocked,
  mobileOpen,
  onMobileOpenChange,
  onNavigate,
  onLogout,
}: {
  profile: Profile;
  active: SocPage;
  forced: boolean;
  busy: boolean;
  gated: boolean;
  blocked: boolean;
  mobileOpen: boolean;
  onMobileOpenChange: (open: boolean) => void;
  onNavigate: (page: SocPage) => void;
  onLogout: () => void;
}) {
  const [compact, setCompact] = useState(
    () => window.matchMedia?.(compactQuery).matches ?? false,
  );
  const [expanded, setExpanded] = useState(() => initialGroups(active));
  const toggle = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const wasOpen = useRef(false);
  const restoreToggleFocus = useRef(false);
  const activeRef = useRef(active);
  activeRef.current = active;
  const close = () => onMobileOpenChange(false);

  useEffect(() => {
    const query = window.matchMedia?.(compactQuery);
    if (!query) return;
    const change = () => setCompact(query.matches);
    change();
    query.addEventListener("change", change);
    return () => query.removeEventListener("change", change);
  }, []);

  useEffect(() => {
    setExpanded((previous) => ({
      ...previous,
      ...Object.fromEntries(
        SOC_NAVIGATION_GROUPS.filter((group) =>
          group.pages.includes(active),
        ).map((group) => [group.id, true]),
      ),
    }));
    onMobileOpenChange(false);
  }, [active, onMobileOpenChange]);

  useEffect(() => {
    // A new actor or a rotated session must not inherit navigation state.
    setExpanded(initialGroups(activeRef.current));
    onMobileOpenChange(false);
  }, [profile.ID, profile.csrfToken, onMobileOpenChange]);

  useEffect(() => {
    if (gated || blocked || !compact) onMobileOpenChange(false);
  }, [gated, blocked, compact, onMobileOpenChange]);

  useEffect(() => {
    if (wasOpen.current && !mobileOpen) restoreToggleFocus.current = true;
    if (!compact) restoreToggleFocus.current = false;
    if (
      !mobileOpen &&
      !gated &&
      !blocked &&
      compact &&
      restoreToggleFocus.current
    ) {
      toggle.current?.focus();
      restoreToggleFocus.current = false;
    }
    wasOpen.current = mobileOpen;
    if (!mobileOpen || gated || blocked || !compact) return;
    closeButton.current?.focus();
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const keyboard = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onMobileOpenChange(false);
      }
      if (event.key !== "Tab") return;
      const targets = Array.from(
        panel.current?.querySelectorAll<HTMLElement>(
          "button:not(:disabled), summary, a[href]",
        ) ?? [],
      ).filter((element) => {
        if (element.closest("[hidden], [inert]")) return false;
        const details = element.closest("details");
        return !details || details.open || element.tagName === "SUMMARY";
      });
      const first = targets[0];
      const last = targets.at(-1);
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", keyboard);
    return () => {
      document.body.style.overflow = overflow;
      document.removeEventListener("keydown", keyboard);
    };
  }, [mobileOpen, gated, blocked, compact, onMobileOpenChange]);

  const visible = (page: SocPage) =>
    ![
      "reset",
      "credential-use",
      "directory-credential-use",
      "directory-credential-use-v2",
    ].includes(page) || profile.role === "platform_admin";
  const button = (page: SocPage, icon?: "assets" | "tasks" | "audit") => (
    <button
      type="button"
      key={page}
      disabled={(forced && page !== "password") || busy}
      className={`soc-nav-item${active === page ? " selected" : ""}`}
      aria-current={active === page ? "page" : undefined}
      onClick={() => {
        close();
        onNavigate(page);
      }}
    >
      {icon && <NavIcon kind={icon} />}
      <span>{SOC_WORKSPACE_LABELS[page]}</span>
    </button>
  );

  return (
    <>
      <button
        type="button"
        className="soc-mobile-toggle"
        data-workspace-modal-owned
        aria-label="打开导航"
        aria-controls="soc-navigation-panel"
        aria-expanded={mobileOpen}
        ref={toggle}
        hidden={!compact || mobileOpen}
        disabled={gated || blocked}
        inert={blocked}
        onClick={() => onMobileOpenChange(true)}
      >
        <NavIcon kind="menu" />
      </button>
      {compact && mobileOpen && !gated && (
        <div className="soc-nav-backdrop" onClick={close} aria-hidden="true" />
      )}
      <aside
        className="soc-sidebar"
        data-workspace-modal-owned
        id="soc-navigation-panel"
        ref={panel}
        role={compact && mobileOpen ? "dialog" : undefined}
        aria-modal={compact && mobileOpen ? true : undefined}
        aria-label="工作区导航"
        hidden={gated || (compact && !mobileOpen)}
        inert={gated || blocked || (compact && !mobileOpen)}
      >
        <div className="soc-sidebar-brand" aria-label="ADTR 身份安全平台">
          <span className="soc-brand-mark">A</span>
          <span>
            ADTR<small>IDENTITY SECURITY</small>
          </span>
          <button
            type="button"
            className="soc-close-navigation"
            aria-label="关闭导航"
            ref={closeButton}
            hidden={!compact}
            onClick={close}
          >
            ×
          </button>
        </div>
        <nav aria-label="账户设置" className="soc-navigation">
          <p className="soc-nav-label">安全运营</p>
          <div className="soc-primary-navigation">
            {button("user-assets-v2", "assets")}
            {button("tasks", "tasks")}
            {button("audit", "audit")}
          </div>
          <div className="soc-management-navigation">
            {SOC_NAVIGATION_GROUPS.map((group) => (
              <details
                key={group.id}
                data-navigation-group={group.id}
                open={expanded[group.id]}
                onToggle={(event) => {
                  const open = event.currentTarget.open;
                  setExpanded((previous) =>
                    previous[group.id] === open
                      ? previous
                      : { ...previous, [group.id]: open },
                  );
                }}
              >
                <summary>
                  {group.label}
                  <span aria-hidden="true">›</span>
                </summary>
                <div
                  className="soc-navigation-group-items"
                  hidden={!expanded[group.id]}
                >
                  {group.pages.filter(visible).map((page) => button(page))}
                </div>
              </details>
            ))}
          </div>
        </nav>
        <div className="soc-sidebar-footer">
          <p className="signed-in">
            已登录为<strong>{profile.username}</strong>
          </p>
          <button
            type="button"
            className="secondary logout"
            disabled={busy}
            onClick={() => {
              close();
              onLogout();
            }}
          >
            退出登录
          </button>
        </div>
      </aside>
    </>
  );
}
