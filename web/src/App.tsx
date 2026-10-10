import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  ApiError,
  messages,
  request,
  validCode,
  validPassword,
  validUsername,
  type Profile,
} from "./api";

import AccessWorkspace from "./AccessWorkspace";
import ResourcesWorkspace from "./ResourcesWorkspace";
import TaskWorkspace, { forgetTaskSession } from "./TaskWorkspace";
import { forgetMaintenanceSession } from "./maintenance-intent";
import AuditWorkspace from "./AuditWorkspace";
import { forgetAuditSession } from "./audit-intent";
import SystemWorkspace from "./SystemWorkspace";
import OperationalLogsWorkspace from "./OperationalLogsWorkspace";
import { forgetBundleSession } from "./operational-log-intent";
import DomainsWorkspace from "./DomainsWorkspace";
import { forgetDomainSession } from "./domain-intent";
import OperationAccountsWorkspace from "./OperationAccountsWorkspace";
import ProfileWorkspace from "./ProfileWorkspace";
import CredentialUseWorkspace from "./CredentialUseWorkspace";
import DirectoryWorkspace from "./DirectoryWorkspace";
import DirectoryCredentialUseWorkspace from "./DirectoryCredentialUseWorkspace";
import DirectoryV2Workspace from "./DirectoryV2Workspace";
import UserAssetsV2Workspace from "./UserAssetsV2Workspace";
import DirectoryV2CredentialUseWorkspace from "./DirectoryV2CredentialUseWorkspace";
import {
  listenForSessionInvalidation,
  type SessionInvalidation,
} from "./session-invalidation";

type Page =
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
type Run = <T>(
  path: string,
  body: unknown | undefined,
  success: (value: T) => void,
) => void;
function pageFromHash(): Page {
  const hash = window.location.hash;
  // Restore only known workspaces. Each one rechecks current permissions and
  // owns its safe initial view; no draft state is read from browser history.
  const exact = (
    [
      "profile",
      "credential-use",
      "directory",
      "directory-credential-use",
      "directory-v2",
      "user-assets-v2",
      "directory-credential-use-v2",
    ] as const
  ).find((name) => hash === `#${name}`);
  if (exact) return exact;
  return (
    (
      [
        "access",
        "resources",
        "tasks",
        "audit",
        "system",
        "operational-logs",
        "domains",
        "operation-accounts",
      ] as const
    ).find((name) => hash === `#${name}` || hash.startsWith(`#${name}/`)) ??
    "account"
  );
}
function Field({
  label,
  name,
  type = "text",
  autoComplete,
  help,
  ...props
}: {
  label: string;
  name: string;
  type?: string;
  autoComplete?: string;
  help?: string;
  value?: string;
  onChange?: React.ChangeEventHandler<HTMLInputElement>;
  inputMode?: "numeric";
  maxLength?: number;
}) {
  return (
    <div className="field">
      <label htmlFor={name}>{label}</label>
      <input
        id={name}
        name={name}
        type={type}
        autoComplete={autoComplete ?? "off"}
        required
        aria-describedby={help ? `${name}-help` : undefined}
        {...props}
      />
      {help && <small id={`${name}-help`}>{help}</small>}
    </div>
  );
}
function Actions({
  busy,
  children,
  cancel,
}: {
  busy: boolean;
  children: ReactNode;
  cancel?: () => void;
}) {
  return (
    <div className="actions">
      <button type="submit" disabled={busy}>
        {busy ? "正在处理…" : children}
      </button>
      {busy && (
        <small className="pending-note">
          取消只停止等待，不能撤销已提交的操作。
        </small>
      )}
      {cancel && (
        <button type="button" className="secondary" onClick={cancel}>
          取消
        </button>
      )}
    </div>
  );
}
function values(event: FormEvent<HTMLFormElement>) {
  event.preventDefault();
  return Object.fromEntries(new FormData(event.currentTarget)) as Record<
    string,
    string
  >;
}
export default function App() {
  const [profile, setProfile] = useState<Profile | null>(null),
    [loading, setLoading] = useState(true),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [page, setPage] = useState<Page>(pageFromHash),
    [revision, setRevision] = useState(0);
  const [checkingSession, setCheckingSession] = useState(false);
  const sessionNotifications = useRef<SessionInvalidation | null>(null);
  const controller = useRef<AbortController | null>(null),
    sequence = useRef(0),
    locked = useRef(false),
    profileRef = useRef(profile);
  profileRef.current = profile;
  const invalidate = useCallback(() => {
    sequence.current++;
    controller.current?.abort();
    locked.current = false;
    setBusy(false);
  }, []);
  const refresh = useCallback(() => {
    invalidate();
    setCheckingSession(false);
    const id = sequence.current;
    const current = new AbortController();
    controller.current = current;
    setLoading(true);
    setError("");
    request<Profile>("/me", current.signal)
      .then((data) => {
        if (id === sequence.current) setProfile(data);
      })
      .catch((err) => {
        if (id !== sequence.current) return;
        setProfile(null);
        setRevision((n) => n + 1);
        if (err instanceof ApiError && err.status === 401) {
          forgetTaskSession();
          forgetMaintenanceSession();
          forgetAuditSession();
          forgetBundleSession();
          forgetDomainSession();
        } else setError(messages[err.code] ?? messages.internal);
      })
      .finally(() => {
        if (id === sequence.current) setLoading(false);
      });
  }, [invalidate]);
  useEffect(() => {
    const resetPrivateView = () => {
      setProfile(null);
      setRevision((n) => n + 1);
      setNotice("");
      setPage("account");
    };
    const notifications = listenForSessionInvalidation(() => {
      resetPrivateView();
      refresh();
    });
    sessionNotifications.current = notifications;
    const verify = () => {
      if (document.visibilityState !== "visible") return;
      invalidate();
      const id = sequence.current;
      const current = new AbortController();
      controller.current = current;
      setCheckingSession(true);
      request<Profile>("/me", current.signal)
        .then((data) => {
          if (id !== sequence.current) return;
          const previous = profileRef.current;
          if (
            !previous ||
            previous.ID !== data.ID ||
            previous.username !== data.username ||
            previous.role !== data.role ||
            previous.priv !== data.priv ||
            previous.csrfToken !== data.csrfToken ||
            previous.needChangePwd !== data.needChangePwd ||
            previous.isExpired !== data.isExpired
          ) {
            resetPrivateView();
            setProfile(data);
          }
        })
        .catch((err) => {
          if (id !== sequence.current) return;
          const unauthenticated = err instanceof ApiError && err.status === 401;
          // A verified absence of a session does not change a login draft.
          // Notices and transitions from a previously authenticated actor do.
          if (!unauthenticated || profileRef.current) resetPrivateView();
          setError(
            unauthenticated ? "" : (messages[err.code] ?? messages.internal),
          );
        })
        .finally(() => {
          if (id !== sequence.current) return;
          setCheckingSession(false);
          setLoading(false);
        });
    };
    const focused = (event: Event) => {
      if (!(event.target instanceof Element)) verify();
    };
    window.addEventListener("focus", focused);
    document.addEventListener("visibilitychange", verify);
    return () => {
      notifications.close();
      sessionNotifications.current = null;
      window.removeEventListener("focus", focused);
      document.removeEventListener("visibilitychange", verify);
      sequence.current++;
      controller.current?.abort();
    };
  }, [invalidate, refresh]);
  useEffect(() => {
    refresh();
    const back = () => {
      setPage(pageFromHash());
      setRevision((n) => n + 1);
      setNotice("");
      refresh();
    };
    window.addEventListener("popstate", back);
    window.addEventListener("hashchange", back);
    return () => {
      sequence.current++;
      controller.current?.abort();
      window.removeEventListener("popstate", back);
      window.removeEventListener("hashchange", back);
    };
  }, [refresh]);
  const run: Run = (path, body, success) => {
    if (locked.current) return;
    locked.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    const id = ++sequence.current;
    controller.current?.abort();
    const current = new AbortController();
    controller.current = current;
    request(path, current.signal, body, profileRef.current?.csrfToken)
      .then((data) => {
        if (id === sequence.current) success(data as never);
      })
      .catch((err) => {
        if (id !== sequence.current) return;
        if (err instanceof ApiError && err.code === "mfa_required") {
          setError("mfa_required");
          return;
        }
        if (err instanceof ApiError && err.code === "unauthenticated") {
          forgetTaskSession();
          forgetMaintenanceSession();
          forgetAuditSession();
          forgetBundleSession();
          forgetDomainSession();
          setProfile(null);
          setRevision((n) => n + 1);
        }
        if (
          err instanceof ApiError &&
          err.code === "password_change_required"
        ) {
          setPage("password");
          refresh();
        }
        setError(messages[err.code] ?? messages.internal);
      })
      .finally(() => {
        if (id === sequence.current) {
          locked.current = false;
          setBusy(false);
        }
      });
  };
  const navigate = (next: Page) => {
    const pending = locked.current || checkingSession || loading;
    invalidate();
    setPage(next);
    setRevision((n) => n + 1);
    setError("");
    setNotice("");
    window.history.pushState({}, "", `#${next}`);
    if (
      pending ||
      page === "access" ||
      page === "resources" ||
      page === "tasks" ||
      page === "audit" ||
      page === "system" ||
      page === "operational-logs" ||
      page === "domains" ||
      page === "operation-accounts" ||
      page === "credential-use" ||
      page === "directory" ||
      page === "directory-credential-use" ||
      page === "directory-v2" ||
      page === "user-assets-v2" ||
      page === "directory-credential-use-v2" ||
      page === "profile"
    )
      refresh();
  };
  const updated = (data: Profile, message: string) => {
    sessionNotifications.current?.publish();
    setProfile(data);
    setPage("account");
    setRevision((n) => n + 1);
    setNotice(message);
  };
  const forced = !!profile && (profile.needChangePwd || profile.isExpired);
  const active = forced ? "password" : page;
  return (
    <div className="shell">
      <header>
        <a
          className="brand"
          href="#account"
          onClick={(e) => {
            e.preventDefault();
            navigate("account");
          }}
        >
          <span className="brand-mark">A</span>ADTR
          <span className="brand-sub">身份安全平台</span>
        </a>
        <span className="environment">账户与访问</span>
      </header>
      {checkingSession && <p role="status">正在核验当前会话…</p>}
      <main hidden={checkingSession} inert={checkingSession}>
        <div className="intro">
          <p className="eyebrow">IDENTITY SECURITY</p>
          <h1>{profile ? "账户安全" : "安全登录"}</h1>
          <p>
            {profile
              ? "管理登录凭据与多因素认证，保护平台访问。"
              : "使用平台账户继续。您的登录状态由安全会话管理。"}
          </p>
        </div>
        {loading ? (
          <section className="card" role="status">
            正在确认登录状态…
          </section>
        ) : (
          <>
            {error && error !== "mfa_required" && (
              <div role="alert" className="alert">
                {error}
                <button
                  type="button"
                  className="text-button"
                  disabled={busy}
                  onClick={() => refresh()}
                >
                  刷新账户状态
                </button>
              </div>
            )}
            {notice && (
              <div role="status" className="success">
                {notice}
              </div>
            )}
            {!profile ? (
              <section className="card login">
                <Login
                  key={revision}
                  run={run}
                  busy={busy}
                  mfa={error === "mfa_required"}
                  onSuccess={(data) => updated(data, "登录成功")}
                  cancel={() => {
                    invalidate();
                    setRevision((n) => n + 1);
                    setError("");
                    refresh();
                  }}
                  invalid={setError}
                />
              </section>
            ) : (
              <div className="workspace">
                <aside>
                  <p className="signed-in">
                    已登录为<strong>{profile.username}</strong>
                  </p>
                  <nav aria-label="账户设置">
                    {(
                      [
                        "account",
                        "profile",
                        "password",
                        "mfa",
                        "reset",
                        "access",
                        "resources",
                        "tasks",
                        "audit",
                        "system",
                        "operational-logs",
                        "domains",
                        "operation-accounts",
                        "credential-use",
                        "directory",
                        "directory-credential-use",
                        "directory-v2",
                        "user-assets-v2",
                        "directory-credential-use-v2",
                      ] as Page[]
                    )
                      .filter(
                        (p) =>
                          (p !== "reset" &&
                            p !== "credential-use" &&
                            p !== "directory-credential-use" &&
                            p !== "directory-credential-use-v2") ||
                          profile.role === "platform_admin",
                      )
                      .map((p) => (
                        <button
                          key={p}
                          disabled={(forced && p !== "password") || busy}
                          className={active === p ? "selected" : ""}
                          aria-current={active === p ? "page" : undefined}
                          onClick={() => navigate(p)}
                        >
                          {
                            {
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
                            }[p]
                          }
                        </button>
                      ))}
                  </nav>
                  <button
                    className="secondary logout"
                    disabled={busy}
                    onClick={() =>
                      run<{ result: string }>("/logout", {}, (data) => {
                        if (data.result !== "SUCCESS") {
                          setError(messages.invalid_response);
                          return;
                        }
                        sessionNotifications.current?.publish();
                        forgetTaskSession();
                        forgetMaintenanceSession();
                        forgetAuditSession();
                        forgetBundleSession();
                        forgetDomainSession();
                        setProfile(null);
                        setPage("account");
                        setRevision((n) => n + 1);
                        setNotice("已安全退出");
                      })
                    }
                  >
                    退出登录
                  </button>
                </aside>
                <section
                  className="card"
                  key={`${active}-${revision}`}
                  aria-busy={busy}
                >
                  {forced && (
                    <div className="warning" role="status">
                      {profile.isExpired
                        ? "密码已过期"
                        : "首次登录或管理员重置后，需要修改密码"}
                      。完成修改后才能继续使用平台。
                    </div>
                  )}
                  {active === "access" && (
                    <AccessWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "resources" && (
                    <ResourcesWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "tasks" && (
                    <TaskWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "audit" && (
                    <AuditWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "domains" && (
                    <DomainsWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "operation-accounts" && (
                    <OperationAccountsWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "credential-use" && (
                    <CredentialUseWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "directory" && (
                    <DirectoryWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "user-assets-v2" && (
                    <UserAssetsV2Workspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "directory-v2" && (
                    <DirectoryV2Workspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "directory-credential-use-v2" &&
                    profile.role === "platform_admin" && (
                      <DirectoryV2CredentialUseWorkspace
                        profile={profile}
                        sessionChanged={() => {
                          setPage("account");
                          refresh();
                        }}
                      />
                    )}
                  {active === "directory-credential-use" &&
                    profile.role === "platform_admin" && (
                      <DirectoryCredentialUseWorkspace
                        profile={profile}
                        sessionChanged={() => {
                          setPage("account");
                          refresh();
                        }}
                      />
                    )}
                  {active === "operational-logs" && (
                    <OperationalLogsWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "system" && (
                    <SystemWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "account" && <Account profile={profile} />}{" "}
                  {active === "profile" && (
                    <ProfileWorkspace
                      profile={profile}
                      sessionChanged={() => {
                        setPage("account");
                        refresh();
                      }}
                    />
                  )}
                  {active === "password" && (
                    <Password
                      busy={busy}
                      run={run}
                      invalid={setError}
                      cancel={forced ? undefined : () => navigate("account")}
                      updated={(data) =>
                        updated(data, "密码已更新，当前会话已轮换")
                      }
                    />
                  )}{" "}
                  {active === "mfa" && (
                    <Mfa
                      profile={profile}
                      busy={busy}
                      run={run}
                      invalid={setError}
                      cancel={() => navigate("account")}
                      updated={(data) =>
                        updated(
                          data,
                          data.hasMfa ? "多因素认证已启用" : "多因素认证已停用",
                        )
                      }
                    />
                  )}{" "}
                  {active === "reset" && profile.role === "platform_admin" && (
                    <Reset
                      busy={busy}
                      run={run}
                      invalid={setError}
                      cancel={() => navigate("account")}
                      done={() => {
                        setRevision((n) => n + 1);
                        setNotice(
                          "用户密码已重置。该用户下次登录必须修改密码。",
                        );
                      }}
                    />
                  )}
                </section>
              </div>
            )}
          </>
        )}
        <footer>ADTR · 仅展示服务器确认的账户状态</footer>
      </main>
    </div>
  );
}
function Login({
  run,
  busy,
  mfa,
  onSuccess,
  cancel,
  invalid,
}: {
  run: Run;
  busy: boolean;
  mfa: boolean;
  onSuccess: (p: Profile) => void;
  cancel: () => void;
  invalid: (s: string) => void;
}) {
  const [challenge, setChallenge] = useState(false);
  useEffect(() => {
    if (mfa) setChallenge(true);
  }, [mfa]);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (busy) return;
        const v = values(e);
        if (
          !validUsername(v.username) ||
          !validPassword(v.password) ||
          (challenge && !validCode(v.totpCode))
        ) {
          invalid(
            "用户名需为 1–64 位字母、数字、点、下划线或连字符；密码需为 12–64 个字符，验证码为 6 位数字。",
          );
          return;
        }
        run(
          "/login",
          {
            username: v.username.toLowerCase(),
            password: v.password,
            ...(challenge ? { totpCode: v.totpCode } : {}),
          },
          onSuccess,
        );
      }}
    >
      <h2>登录账户</h2>
      <fieldset disabled={busy}>
        <Field
          label="用户名"
          name="username"
          autoComplete="username"
          maxLength={64}
        />
        <Field
          label="密码"
          name="password"
          type="password"
          autoComplete="current-password"
        />
        {challenge && (
          <>
            <p role="status">请打开认证器，输入当前的 6 位验证码</p>
            <Field
              label="二次认证验证码"
              name="totpCode"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
            />
          </>
        )}
      </fieldset>
      <Actions busy={busy} cancel={challenge || busy ? cancel : undefined}>
        登录
      </Actions>
    </form>
  );
}
function Account({ profile: p }: { profile: Profile }) {
  return (
    <>
      <h2>账户概览</h2>
      <p className="muted">以下信息来自当前服务器会话</p>
      <dl>
        {[
          ["用户 ID", p.ID],
          ["用户名", p.username],
          ["角色", p.role],
          ["权限值", p.priv],
          ["邮箱", p.email || "未设置"],
          ["手机号", p.mobile || "未设置"],
          ["备注", p.remark || "未设置"],
          ["密码强度", p.passStrength],
          ["密码未更新天数", p.passwordNotUpdatedDays],
          ["密码更新时间", p.pwdUpdateTm || "未提供"],
          ["多因素认证", p.hasMfa ? "已启用" : "未启用"],
        ].map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
    </>
  );
}
type FormProps = {
  run: Run;
  busy: boolean;
  invalid: (s: string) => void;
  cancel?: () => void;
};
function Password({
  run,
  busy,
  invalid,
  cancel,
  updated,
}: FormProps & { updated: (p: Profile) => void }) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (busy) return;
        const v = values(e);
        if (
          !validPassword(v.newPassword) ||
          v.newPassword !== v.confirmPassword ||
          v.newPassword === v.oldPassword
        ) {
          invalid(
            "新密码需为 12–64 个字符，与确认密码一致，且不能与旧密码相同。",
          );
          return;
        }
        run(
          "/password",
          { oldPassword: v.oldPassword, newPassword: v.newPassword },
          updated,
        );
      }}
    >
      <h2>修改密码</h2>
      <p className="muted">
        修改后其他会话将失效。请使用未在其他服务使用过的密码。
      </p>
      <fieldset disabled={busy}>
        <Field
          label="当前密码"
          name="oldPassword"
          type="password"
          autoComplete="current-password"
        />
        <Field
          label="新密码"
          name="newPassword"
          type="password"
          autoComplete="new-password"
          help="12–64 个字符，支持 Unicode"
        />
        <Field
          label="确认新密码"
          name="confirmPassword"
          type="password"
          autoComplete="new-password"
        />
      </fieldset>
      <Actions busy={busy} cancel={cancel}>
        更新密码
      </Actions>
    </form>
  );
}
function Mfa({
  profile,
  run,
  busy,
  invalid,
  cancel,
  updated,
}: FormProps & { profile: Profile; updated: (p: Profile) => void }) {
  const [enrollment, setEnrollment] = useState<{
    secret: string;
    otpauthUri: string;
  } | null>(null);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (busy) return;
        const v = values(e);
        if ((profile.hasMfa || enrollment) && !validCode(v.mfaCode)) {
          invalid("请输入 6 位数字验证码。");
          return;
        }
        if (profile.hasMfa) {
          run(
            "/mfa/disable",
            { password: v.password, mfaCode: v.mfaCode },
            updated,
          );
        } else if (enrollment) {
          run(
            "/mfa/confirm",
            {
              password: v.password,
              secret: enrollment.secret,
              mfaCode: v.mfaCode,
            },
            updated,
          );
        } else {
          run(
            "/mfa/enroll",
            { password: v.password },
            (data: { secret: string; otpauthUri: string }) => {
              setEnrollment(data);
            },
          );
        }
      }}
    >
      <h2>多因素认证</h2>
      <p className={profile.hasMfa ? "status-on" : "muted"}>
        {profile.hasMfa
          ? "已启用 · 登录时需要认证器验证码"
          : "未启用 · 为账户增加一层保护"}
      </p>
      {profile.hasMfa && (
        <p className="warning">
          停用后，登录将不再要求认证器验证码。此操作将使其他会话失效。
        </p>
      )}
      {enrollment && (
        <div className="enrollment">
          <h3>添加到您的认证器</h3>
          <p>
            在认证器中选择手动添加账户，类型选择基于时间，并输入以下密钥。密钥仅在本次设置中显示，请勿分享。
          </p>
          <label className="field" htmlFor="secret">
            设置密钥
            <input
              id="secret"
              value={enrollment.secret}
              readOnly
              spellCheck={false}
            />
          </label>
          <details>
            <summary>查看认证器 URI</summary>
            <p className="secret">{enrollment.otpauthUri}</p>
          </details>
          <p>添加后输入验证码完成启用。提交验证前取消不会启用 MFA。</p>
        </div>
      )}
      <fieldset disabled={busy}>
        <Field
          label="当前密码"
          name="password"
          type="password"
          autoComplete="current-password"
        />
        {(profile.hasMfa || enrollment) && (
          <Field
            label="认证器验证码"
            name="mfaCode"
            inputMode="numeric"
            maxLength={6}
            autoComplete="one-time-code"
          />
        )}
      </fieldset>
      <Actions busy={busy} cancel={cancel}>
        {profile.hasMfa ? "确认停用" : enrollment ? "验证并启用" : "开始设置"}
      </Actions>
    </form>
  );
}
function Reset({
  run,
  busy,
  invalid,
  cancel,
  done,
}: FormProps & { done: () => void }) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (busy) return;
        const v = values(e);
        if (
          !validUsername(v.username) ||
          !validPassword(v.newPassword) ||
          v.newPassword !== v.confirmPassword ||
          !validCode(v.totpCode)
        ) {
          invalid(
            "请检查目标用户名、12–64 字符的新密码、确认密码及 6 位验证码。",
          );
          return;
        }
        run<{ result: string }>(
          "/reset-password",
          {
            username: v.username.toLowerCase(),
            newPassword: v.newPassword,
            password: v.password,
            totpCode: v.totpCode,
          },
          (data) => {
            if (data.result === "SUCCESS") done();
            else invalid(messages.invalid_response);
          },
        );
      }}
    >
      <h2>重置用户密码</h2>
      <p className="warning">
        仅平台管理员可操作，且必须已启用
        MFA。将立即撤销目标用户的会话，并要求其下次登录修改密码。
      </p>
      <fieldset disabled={busy}>
        <Field label="目标用户名" name="username" maxLength={64} />
        <Field
          label="用户新密码"
          name="newPassword"
          type="password"
          autoComplete="new-password"
        />
        <Field
          label="确认用户新密码"
          name="confirmPassword"
          type="password"
          autoComplete="new-password"
        />
        <Field
          label="管理员当前密码"
          name="password"
          type="password"
          autoComplete="current-password"
        />
        <Field
          label="管理员认证器验证码"
          name="totpCode"
          inputMode="numeric"
          maxLength={6}
          autoComplete="one-time-code"
        />
      </fieldset>
      <Actions busy={busy} cancel={cancel}>
        确认重置密码
      </Actions>
    </form>
  );
}
