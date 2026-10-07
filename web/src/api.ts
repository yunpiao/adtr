export interface Profile {
  ID: number;
  username: string;
  role: string;
  priv: number;
  mobile: string;
  email: string;
  remark: string;
  passStrength: string;
  hasMfa: boolean;
  needChangePwd: boolean;
  isExpired: boolean;
  passwordNotUpdatedDays: number;
  pwdUpdateTm: string;
  csrfToken: string;
}
export class ApiError extends Error {
  constructor(
    public code: string,
    public status = 0,
  ) {
    super(code);
  }
}
export async function request<T>(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  csrfToken?: string,
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/auth${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      signal,
      headers:
        body === undefined
          ? {}
          : {
              "Content-Type": "application/json",
              ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}),
            },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  const data = await response.json().catch(() => {
    throw new ApiError("invalid_response", response.status);
  });
  if (!response.ok)
    throw new ApiError(
      typeof data.error === "string" ? data.error : "internal",
      response.status,
    );
  return data as T;
}
export const messages: Record<string, string> = {
  invalid_input: "请检查输入格式后重试。",
  unauthenticated: "登录已失效，请重新登录。",
  forbidden: "当前账户没有执行此操作的权限。",
  password_change_required: "请先修改密码，再继续使用。",
  invalid_credentials: "凭据或验证码不正确，请重试。",
  rate_limited: "尝试次数过多，请稍后再试。",
  internal: "服务暂时不可用，请稍后重试。",
  network: "连接中断，结果尚未确认。请刷新账户状态后再决定是否重试。",
  invalid_response: "服务返回了无法识别的结果，请刷新状态。",
};
export const validUsername = (value: string) =>
  typeof value === "string" && /^[A-Za-z0-9._-]{1,64}$/.test(value);
export const validPassword = (value: string) =>
  typeof value === "string" &&
  Array.from(value).length >= 12 &&
  Array.from(value).length <= 64;
export const validCode = (value: string) =>
  typeof value === "string" && /^[0-9]{6}$/.test(value);
