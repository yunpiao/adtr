import {
  fireEvent,
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import {
  request,
  validCode,
  validPassword,
  validUsername,
  type Profile,
} from "./api";
const profile: Profile = {
  ID: 1,
  username: "admin",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "admin@example.test",
  remark: "",
  passStrength: "high",
  hasMfa: false,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 4,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "test-csrf",
};
const reply = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as Response);
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  window.history.replaceState({}, "", "#account");
});
async function start(p: Profile | null = profile) {
  fetcher.mockImplementationOnce(() =>
    p ? reply(p) : reply({ error: "unauthenticated" }, 401),
  );
  render(<App />);
  await screen.findByRole("heading", { name: p ? "账户概览" : "登录账户" });
}
async function fill(label: string, value: string) {
  await userEvent.type(screen.getByLabelText(label), value);
}
async function click(name: string) {
  await userEvent.click(screen.getByRole("button", { name }));
}
describe("validation", () => {
  it("enforces ASCII username, Unicode codepoint passwords and exact six digit codes", () => {
    expect(validUsername("User.Name_1-2")).toBe(true);
    for (const name of ["", "x".repeat(65), "帐号", "a b"])
      expect(validUsername(name)).toBe(false);
    expect(validPassword("😀".repeat(12))).toBe(true);
    expect(validPassword("😀".repeat(64))).toBe(true);
    expect(validPassword("😀".repeat(65))).toBe(false);
    expect(validPassword("x".repeat(11))).toBe(false);
    for (const code of ["12345", "1234567", "abcdef", "１２３４５６"])
      expect(validCode(code)).toBe(false);
    expect(validCode("012345")).toBe(true);
  });
  it("uses same-origin cookies, CSRF header, no-store and abort signal", async () => {
    fetcher.mockImplementationOnce(() => reply({ result: "SUCCESS" }));
    const controller = new AbortController();
    await request("/logout", controller.signal, {}, "csrf");
    expect(fetcher).toHaveBeenCalledWith(
      "/api/auth/logout",
      expect.objectContaining({
        credentials: "same-origin",
        cache: "no-store",
        signal: controller.signal,
        headers: { "Content-Type": "application/json", "X-CSRF-Token": "csrf" },
      }),
    );
  });
});
describe("authentication", () => {
  it("logs in with normalized username and only server-backed profile", async () => {
    await start(null);
    fetcher.mockImplementationOnce(() => reply(profile));
    await fill("用户名", "Admin");
    await fill("密码", "sample-password");
    await click("登录");
    expect(
      await screen.findByRole("heading", { name: "账户概览" }),
    ).toBeInTheDocument();
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({
      username: "admin",
      password: "sample-password",
    });
    expect(localStorage.length).toBe(0);
  });
  it("prompts for MFA only after server challenge and retains challenge on invalid code", async () => {
    await start(null);
    fetcher.mockImplementationOnce(() => reply({ error: "mfa_required" }, 401));
    await fill("用户名", "admin");
    await fill("密码", "sample-password");
    await click("登录");
    await screen.findByLabelText("二次认证验证码");
    fetcher.mockImplementationOnce(() =>
      reply({ error: "invalid_credentials" }, 401),
    );
    await fill("二次认证验证码", "123456");
    await click("登录");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "凭据或验证码不正确",
    );
    expect(screen.getByLabelText("二次认证验证码")).toBeInTheDocument();
    fetcher.mockImplementationOnce(() => reply({ ...profile, hasMfa: true }));
    await click("登录");
    expect(
      await screen.findByRole("heading", { name: "账户概览" }),
    ).toBeInTheDocument();
  });
  it("rejects empty and illegal login input without a request", async () => {
    await start(null);
    fireEvent.submit(
      screen.getByRole("button", { name: "登录" }).closest("form")!,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("用户名需为");
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("shows rate limiting without fake login success", async () => {
    await start(null);
    fetcher.mockImplementationOnce(() => reply({ error: "rate_limited" }, 429));
    await fill("用户名", "admin");
    await fill("密码", "sample-password");
    await click("登录");
    expect(await screen.findByRole("alert")).toHaveTextContent("尝试次数过多");
    expect(
      screen.getByRole("heading", { name: "登录账户" }),
    ).toBeInTheDocument();
  });
  it("blocks duplicate submit and discards canceled stale login result", async () => {
    await start(null);
    let resolve!: (r: Response) => void;
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          resolve = r;
        }),
    );
    await fill("用户名", "admin");
    await fill("密码", "sample-password");
    const form = screen.getByRole("button", { name: "登录" }).closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(fetcher).toHaveBeenCalledTimes(2);
    fetcher.mockImplementationOnce(() =>
      reply({ error: "unauthenticated" }, 401),
    );
    await click("取消");
    await screen.findByRole("heading", { name: "登录账户" });
    await act(async () => {
      resolve(await reply(profile));
    });
    expect(
      screen.queryByRole("heading", { name: "账户概览" }),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText("密码")).toHaveValue("");
  });
  it("logs out only after a successful server response", async () => {
    await start();
    fetcher.mockImplementationOnce(() => reply({ error: "internal" }, 500));
    await click("退出登录");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "服务暂时不可用",
    );
    expect(
      screen.getByRole("heading", { name: "账户概览" }),
    ).toBeInTheDocument();
    fetcher.mockImplementationOnce(() => reply({ result: "SUCCESS" }));
    await click("退出登录");
    expect(
      await screen.findByRole("heading", { name: "登录账户" }),
    ).toBeInTheDocument();
  });
  it("allows retry after network failure and never claims success", async () => {
    await start(null);
    fetcher.mockRejectedValueOnce(new TypeError("network"));
    await fill("用户名", "admin");
    await fill("密码", "sample-password");
    await click("登录");
    expect(await screen.findByRole("alert")).toHaveTextContent("结果尚未确认");
    expect(screen.getByRole("button", { name: "登录" })).toBeEnabled();
  });
});
describe("workspace history routing", () => {
  it.each([
    ["#profile", "个人资料"],
    ["#access/users/create", "访问管理"],
    ["#resources/groups/edit", "资源与租户"],
    ["#tasks/detail", "后台任务"],
    ["#audit/history", "操作审计"],
    ["#system/resources", "系统健康"],
    ["#operational-logs/history", "运行日志与诊断包"],
    ["#domains/credential-source", "域连接"],
    ["#operation-accounts/detail", "管理操作账户"],
    ["#credential-use", "凭据授权清理"],
    ["#directory", "目录资产"],
    ["#directory-credential-use", "目录读取授权"],
  ])(
    "restores %s only after verifying the current session",
    async (hash, name) => {
      window.history.replaceState({}, "", hash);
      let resolve!: (value: Response) => void;
      fetcher.mockImplementation(() => new Promise<Response>(() => {}));
      fetcher.mockImplementationOnce(
        () =>
          new Promise<Response>((done) => {
            resolve = done;
          }),
      );
      render(<App />);
      expect(screen.queryByRole("navigation", { name: "账户设置" })).toBeNull();
      expect(fetcher).toHaveBeenCalledTimes(1);
      expect(fetcher.mock.calls[0][0]).toBe("/api/auth/me");
      await act(async () => resolve(await reply(profile)));
      expect(screen.getByRole("button", { name })).toHaveAttribute(
        "aria-current",
        "page",
      );
    },
  );
  it.each([
    "#domains-extra/detail",
    "#operation-accounts-extra",
    "#credential-use-extra",
    "#directory-extra",
    "#unknown/domains",
    "#profile/edit",
    "#password",
    "#mfa",
    "#reset",
  ])(
    "keeps unknown and proof-only route %s at account overview",
    async (hash) => {
      window.history.replaceState({}, "", hash);
      fetcher.mockImplementation(() => reply(profile));
      render(<App />);
      await screen.findByRole("heading", { name: "账户概览" });
      expect(fetcher).toHaveBeenCalledTimes(1);
      expect(screen.getByRole("button", { name: "账户概览" })).toHaveAttribute(
        "aria-current",
        "page",
      );
    },
  );
});
describe("password lifecycle and access boundaries", () => {
  it.each(["needChangePwd", "isExpired"] as const)(
    "gates %s accounts and rotates profile after password change",
    async (flag) => {
      fetcher.mockImplementationOnce(() => reply({ ...profile, [flag]: true }));
      render(<App />);
      await screen.findByRole("heading", { name: "修改密码" });
      expect(screen.getByRole("button", { name: "多因素认证" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "账户概览" })).toBeDisabled();
      expect(
        screen.queryByRole("button", { name: "取消" }),
      ).not.toBeInTheDocument();
      fetcher.mockImplementationOnce(() =>
        reply({ ...profile, csrfToken: "rotated" }),
      );
      await fill("当前密码", "old-password");
      await fill("新密码", "new-password-12");
      await fill("确认新密码", "new-password-12");
      await click("更新密码");
      expect(
        await screen.findByRole("heading", { name: "账户概览" }),
      ).toBeInTheDocument();
      fetcher.mockImplementationOnce(() => reply({ result: "SUCCESS" }));
      await click("退出登录");
      expect(fetcher.mock.calls.at(-1)![1].headers["X-CSRF-Token"]).toBe(
        "rotated",
      );
    },
  );
  it("rejects mismatched passwords without a mutation", async () => {
    await start();
    await click("修改密码");
    await fill("当前密码", "old-password");
    await fill("新密码", "new-password-12");
    await fill("确认新密码", "different-pass");
    await click("更新密码");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "与确认密码一致",
    );
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("hides administrative reset from non-admin roles", async () => {
    await start({ ...profile, role: "viewer" });
    expect(
      screen.queryByRole("button", { name: "重置用户密码" }),
    ).not.toBeInTheDocument();
  });
  it("sends fresh admin proof and target credentials for reset", async () => {
    await start({ ...profile, hasMfa: true });
    await click("重置用户密码");
    fetcher.mockImplementationOnce(() => reply({ result: "SUCCESS" }));
    await fill("目标用户名", "Target");
    await fill("用户新密码", "new-password-12");
    await fill("确认用户新密码", "new-password-12");
    await fill("管理员当前密码", "admin-password");
    await fill("管理员认证器验证码", "123456");
    await click("确认重置密码");
    expect(await screen.findByRole("status")).toHaveTextContent(
      "用户密码已重置",
    );
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({
      username: "target",
      newPassword: "new-password-12",
      password: "admin-password",
      totpCode: "123456",
    });
    expect(screen.getByLabelText("管理员当前密码")).toHaveValue("");
  });
  it("clears sensitive fields on Back and verifies session again", async () => {
    await start();
    await click("修改密码");
    await fill("当前密码", "private-password");
    fetcher.mockImplementationOnce(() => reply(profile));
    await act(async () => {
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await screen.findByRole("heading", { name: "账户概览" });
    await click("修改密码");
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
  });
  it("returns to login when a protected operation has expired", async () => {
    await start();
    await click("修改密码");
    fetcher.mockImplementationOnce(() =>
      reply({ error: "unauthenticated" }, 401),
    );
    await fill("当前密码", "old-password");
    await fill("新密码", "new-password-12");
    await fill("确认新密码", "new-password-12");
    await click("更新密码");
    expect(
      await screen.findByRole("heading", { name: "登录账户" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("登录已失效");
  });
});
describe("MFA lifecycle", () => {
  it("enrolls and confirms before reporting enabled status", async () => {
    await start();
    await click("多因素认证");
    fetcher.mockImplementationOnce(() =>
      reply({
        secret: "SYNTHETICSECRET",
        otpauthUri: "otpauth://totp/ADTR:test?secret=SYNTHETICSECRET",
      }),
    );
    await fill("当前密码", "sample-password");
    await click("开始设置");
    expect(await screen.findByLabelText("设置密钥")).toHaveValue(
      "SYNTHETICSECRET",
    );
    expect(screen.getByText(/未启用 ·/)).toBeInTheDocument();
    fetcher.mockImplementationOnce(() =>
      reply({ ...profile, hasMfa: true, csrfToken: "rotated" }),
    );
    await fill("认证器验证码", "123456");
    await click("验证并启用");
    await screen.findByRole("heading", { name: "账户概览" });
    expect(screen.getByRole("status")).toHaveTextContent("多因素认证已启用");
    expect(
      screen.queryByDisplayValue("SYNTHETICSECRET"),
    ).not.toBeInTheDocument();
    expect(JSON.parse(fetcher.mock.calls[2][1].body)).toEqual({
      password: "sample-password",
      secret: "SYNTHETICSECRET",
      mfaCode: "123456",
    });
  });
  it("cancel discards setup secret and does not enable MFA", async () => {
    await start();
    await click("多因素认证");
    fetcher.mockImplementationOnce(() =>
      reply({ secret: "SYNTHETICSECRET", otpauthUri: "otpauth://totp/test" }),
    );
    await fill("当前密码", "sample-password");
    await click("开始设置");
    await screen.findByLabelText("设置密钥");
    await click("取消");
    await click("多因素认证");
    expect(screen.queryByLabelText("设置密钥")).not.toBeInTheDocument();
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it("requires fresh password and TOTP to disable MFA", async () => {
    await start({ ...profile, hasMfa: true });
    await click("多因素认证");
    fetcher.mockImplementationOnce(() => reply(profile));
    await fill("当前密码", "sample-password");
    await fill("认证器验证码", "012345");
    await click("确认停用");
    await screen.findByRole("heading", { name: "账户概览" });
    expect(screen.getByRole("status")).toHaveTextContent("多因素认证已停用");
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({
      password: "sample-password",
      mfaCode: "012345",
    });
  });
  it("does not render a stale enrollment secret after cancellation", async () => {
    await start();
    await click("多因素认证");
    let resolve!: (r: Response) => void;
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          resolve = r;
        }),
    );
    await fill("当前密码", "sample-password");
    await click("开始设置");
    fetcher.mockImplementationOnce(() => reply(profile));
    await click("取消");
    await screen.findByRole("heading", { name: "账户概览" });
    await act(async () =>
      resolve(
        await reply({
          secret: "STALESECRET",
          otpauthUri: "otpauth://totp/stale",
        }),
      ),
    );
    expect(screen.queryByDisplayValue("STALESECRET")).not.toBeInTheDocument();
  });
});
