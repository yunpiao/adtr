import {
  Children,
  cloneElement,
  isValidElement,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
  type ReactElement,
  type ReactNode,
} from "react";
import { ApiError, validCode, validPassword, type Profile } from "./api";
import {
  accessRequest,
  errorText,
  labels,
  marks,
  type Can,
  type Mutation,
  type PageInfo,
  type Permission,
  type PermissionInput,
  type Proof,
  type Role,
} from "./access-api";

export interface AccessContext {
  profile: Profile;
  can: Can;
  menu: Permission[];
  sessionChanged: () => void;
}
export function useAccessTask(sessionChanged: () => void) {
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const sequence = useRef(0),
    controller = useRef<AbortController | null>(null),
    locked = useRef(false);
  const invalidate = useCallback(() => {
    sequence.current++;
    controller.current?.abort();
    locked.current = false;
  }, []);
  useEffect(() => invalidate, [invalidate]);
  const run = async <T,>(
    work: (signal: AbortSignal) => Promise<T>,
    success: (data: T) => void,
    replace = false,
  ) => {
    if (locked.current && !replace) return;
    invalidate();
    locked.current = true;
    setBusy(true);
    setError("");
    const id = sequence.current,
      current = new AbortController();
    controller.current = current;
    try {
      const data = await work(current.signal);
      if (id === sequence.current) success(data);
    } catch (error) {
      if (id !== sequence.current) return;
      if (
        error instanceof ApiError &&
        ["unauthenticated", "password_change_required"].includes(error.code)
      )
        sessionChanged();
      else setError(errorText(error));
    } finally {
      if (id === sequence.current) {
        locked.current = false;
        setBusy(false);
      }
    }
  };
  return { busy, error, setError, run };
}
export function Field({
  label,
  children,
  help,
}: {
  label: string;
  children: ReactNode;
  help?: string;
}) {
  const id = useId();
  return (
    <label className="field">
      <span id={`${id}-label`}>{label}</span>
      {Children.map(children, (child) =>
        isValidElement(child) &&
        ["input", "select", "textarea"].includes(String(child.type))
          ? cloneElement(child as ReactElement<Record<string, unknown>>, {
              "aria-labelledby": `${id}-label`,
              ...(help ? { "aria-describedby": `${id}-help` } : {}),
            })
          : child,
      )}
      {help && <small id={`${id}-help`}>{help}</small>}
    </label>
  );
}
export function ErrorNotice({ error }: { error: string }) {
  return error ? (
    <div role="alert" className="alert">
      {error}
    </div>
  ) : null;
}
export function readValues(event: FormEvent<HTMLFormElement>) {
  event.preventDefault();
  return Object.fromEntries(new FormData(event.currentTarget)) as Record<
    string,
    string
  >;
}
export function ProofFields() {
  return (
    <fieldset className="proof">
      <legend>确认管理操作</legend>
      <p className="warning">
        每次修改均需操作者当前密码及新的、未使用的六位验证码。登录、启用 MFA
        或上一次修改使用过的验证码不能重复使用，请等待认证器显示下一个验证码。
      </p>
      <Field label="操作者当前密码">
        <input
          name="actorPassword"
          type="password"
          autoComplete="current-password"
          required
        />
      </Field>
      <Field label="未使用的认证器验证码">
        <input
          name="totpCode"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9]{6}"
          required
        />
      </Field>
    </fieldset>
  );
}
export function getProof(values: Record<string, string>): Proof | null {
  return validPassword(values.actorPassword) && validCode(values.totpCode)
    ? { actorPassword: values.actorPassword, totpCode: values.totpCode }
    : null;
}
export function FormActions({
  busy,
  cancel,
  children,
}: {
  busy: boolean;
  cancel: () => void;
  children: ReactNode;
}) {
  return (
    <div className="actions">
      <button disabled={busy} type="submit">
        {busy ? "正在提交…" : children}
      </button>
      <button type="button" className="secondary" onClick={cancel}>
        取消
      </button>
      {busy && (
        <small>
          取消仅停止等待，不能撤销已提交的修改。返回列表后请刷新核对，不要自动重试。
        </small>
      )}
    </div>
  );
}
export function useMutation(
  context: AccessContext,
  done: (notice: string) => void,
) {
  const task = useAccessTask(context.sessionChanged);
  const submit = (
    path: string,
    body: Record<string, unknown>,
    values: Record<string, string>,
    message: string,
  ) => {
    const proof = getProof(values);
    if (!proof) {
      task.setError("请输入有效的操作者密码和六位未使用验证码。");
      return;
    }
    void task.run(
      async (signal) => {
        const result = await accessRequest<Mutation>(
          path,
          signal,
          { ...body, ...proof },
          context.profile.csrfToken,
        );
        if (
          result.result !== "SUCCESS" ||
          typeof result.sessionRevoked !== "boolean"
        )
          throw new ApiError("invalid_response");
        return result;
      },
      (result) =>
        result.sessionRevoked ? context.sessionChanged() : done(message),
    );
  };
  return { ...task, submit };
}
export function Pagination({
  page,
  exhausted,
  busy,
  change,
}: {
  page: PageInfo;
  exhausted: boolean;
  busy: boolean;
  change: (page: number) => void;
}) {
  return (
    <div className="pagination" aria-label="分页">
      <span>
        共 {page.total} 条 · 第 {page.pageIdx} / {Math.max(page.totalPage, 1)}{" "}
        页
      </span>
      <button
        className="secondary"
        disabled={busy || page.pageIdx <= 1}
        onClick={() => change(page.pageIdx - 1)}
      >
        上一页
      </button>
      <button
        className="secondary"
        disabled={busy || exhausted || page.pageSize === -1}
        onClick={() => change(page.pageIdx + 1)}
      >
        下一页
      </button>
    </div>
  );
}
export function PageSize() {
  return (
    <Field label="每页条数">
      <select name="pageSize" defaultValue="20">
        {[10, 20, 30, 40, 50, 100].map((size) => (
          <option key={size} value={size}>
            {size}
          </option>
        ))}
        <option value="-1">全部（最多 1,000 条）</option>
      </select>
    </Field>
  );
}
export function RoleField({
  roles,
  value = "viewer",
  name = "roleID",
  label = "角色",
}: {
  roles: Role[];
  value?: string;
  name?: string;
  label?: string;
}) {
  // The registry remains usable with a known ID when this actor cannot list roles.
  return (
    <Field
      label={label}
      help="填写角色 ID，可从候选项选择；最终分配权限由服务器校验。"
    >
      <input name={name} list={`roles-${name}`} defaultValue={value} required />
      <datalist id={`roles-${name}`}>
        {roles.map((role) => (
          <option key={role.id} value={role.id}>
            {role.name}
          </option>
        ))}
      </datalist>
    </Field>
  );
}
export function PermissionEditor({
  value,
  metadata,
  onChange,
  readOnly = false,
}: {
  value: PermissionInput[];
  metadata: Permission[];
  onChange: (value: PermissionInput[]) => void;
  readOnly?: boolean;
}) {
  return (
    <fieldset className="permission-editor">
      <legend>功能授权</legend>
      <p className="muted">
        未授予的功能默认拒绝。写入必须同时允许读取；服务端限制可授予范围。
      </p>
      {marks.map((mark) => {
        const grant = value.find((p) => p.mark === mark)?.auth ?? {
          readable: false,
          writeable: false,
        };
        const meta = metadata.find((p) => p.mark === mark);
        const update = (kind: "readable" | "writeable", checked: boolean) =>
          onChange(
            marks.map((m) => {
              const auth = {
                ...(value.find((p) => p.mark === m)?.auth ?? {
                  readable: false,
                  writeable: false,
                }),
              };
              if (m === mark) {
                auth[kind] = checked;
                if (kind === "writeable" && checked) auth.readable = true;
                if (kind === "readable" && !checked) auth.writeable = false;
              }
              return { mark: m, auth };
            }),
          );
        return (
          <div className="permission-row" key={mark}>
            <strong>{meta?.name || labels[mark]}</strong>
            <label>
              <input
                type="checkbox"
                checked={grant.readable}
                disabled={
                  readOnly || (!meta?.allow_auth.readable && !grant.readable)
                }
                onChange={(e) => update("readable", e.target.checked)}
              />
              {labels[mark]}：读取
            </label>
            <label>
              <input
                type="checkbox"
                checked={grant.writeable}
                disabled={
                  readOnly || (!meta?.allow_auth.writeable && !grant.writeable)
                }
                onChange={(e) => update("writeable", e.target.checked)}
              />
              {labels[mark]}：写入
            </label>
            {meta && (
              <details>
                <summary>查看服务器登记的操作</summary>
                <ul>
                  {meta.paths.map((path) => (
                    <li key={path.url}>
                      {path.name} · {path.url} · {path.auth}
                    </li>
                  ))}
                </ul>
                <small>
                  标记：{meta.mark} · 图标：{meta.icon || "未提供"} · 子节点：
                  {meta.children.length} · 已勾选：{meta.checked ? "是" : "否"}
                </small>
              </details>
            )}
          </div>
        );
      })}
    </fieldset>
  );
}
export const emptyPermissions = (): PermissionInput[] =>
  marks.map((mark) => ({ mark, auth: { readable: false, writeable: false } }));
