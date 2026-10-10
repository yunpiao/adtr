import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import App from "./App";
import type { Profile } from "./api";
import SocNavigation, {
  SOC_NAVIGATION_GROUPS,
  SOC_WORKSPACE_LABELS,
  socWorkspaceSection,
  type SocPage,
} from "./SocNavigation";

const profile: Profile = {
  ID: 1,
  username: "synthetic-operator",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 1,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "synthetic-csrf",
};
let compact = false;
let mediaChanged: (() => void) | undefined;
const navigate = vi.fn();
const logout = vi.fn();

beforeEach(() => {
  compact = false;
  mediaChanged = undefined;
  navigate.mockReset();
  logout.mockReset();
  vi.stubGlobal("matchMedia", () => ({
    get matches() {
      return compact;
    },
    addEventListener: (_event: string, listener: () => void) => {
      mediaChanged = listener;
    },
    removeEventListener: vi.fn(),
  }));
  document.body.style.overflow = "";
  window.history.replaceState({}, "", "#account");
});
afterEach(() => {
  vi.unstubAllGlobals();
});

function Navigation({
  active = "user-assets-v2",
  actor = profile,
  forced = false,
  busy = false,
  gated = false,
  blocked = false,
}: {
  active?: SocPage;
  actor?: Profile;
  forced?: boolean;
  busy?: boolean;
  gated?: boolean;
  blocked?: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <SocNavigation
      profile={actor}
      active={active}
      forced={forced}
      busy={busy}
      gated={gated}
      blocked={blocked}
      mobileOpen={open}
      onMobileOpenChange={setOpen}
      onNavigate={navigate}
      onLogout={logout}
    />
  );
}

function group(label: string) {
  return screen.getByText(label, { selector: "summary" }).closest("details")!;
}

async function openMobile() {
  const trigger = screen.getByRole("button", { name: "打开导航" });
  await userEvent.click(trigger);
  const dialog = screen.getByRole("dialog", { name: "工作区导航" });
  expect(dialog).toHaveAttribute("aria-modal", "true");
  expect(screen.getByRole("button", { name: "关闭导航" })).toHaveFocus();
  return { trigger, dialog };
}

describe("SOC workspace navigation", () => {
  it("prioritizes real assets, tasks and audit with management initially collapsed", () => {
    render(<Navigation />);
    const nav = screen.getByRole("navigation", { name: "账户设置" });
    expect(
      within(nav)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["用户资产", "后台任务", "操作审计"]);
    for (const entry of SOC_NAVIGATION_GROUPS)
      expect(group(entry.label)).not.toHaveAttribute("open");
    expect(screen.getByRole("button", { name: "用户资产" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(nav).not.toHaveTextContent("告警");
    expect(nav).not.toHaveTextContent("响应处置");
    expect(socWorkspaceSection("user-assets-v2")).toBe("安全运营");
    expect(socWorkspaceSection("domains")).toBe("采集与接入");
  });

  it("retains every existing administrator workspace under a discoverable summary", async () => {
    render(<Navigation />);
    for (const entry of SOC_NAVIGATION_GROUPS) {
      await userEvent.click(
        screen.getByText(entry.label, { selector: "summary" }),
      );
      for (const page of entry.pages)
        expect(
          screen.getByRole("button", {
            name: SOC_WORKSPACE_LABELS[page],
          }),
        ).toBeVisible();
    }
    expect(screen.getByRole("button", { name: "退出登录" })).toBeEnabled();
  });

  it("opens the active group and respects a user's manual collapse until the route changes", async () => {
    const view = render(<Navigation active="account" />);
    expect(group("账户与身份")).toHaveAttribute("open");
    await userEvent.click(
      screen.getByText("账户与身份", { selector: "summary" }),
    );
    await waitFor(() =>
      expect(group("账户与身份")).not.toHaveAttribute("open"),
    );
    view.rerender(<Navigation active="account" busy />);
    expect(group("账户与身份")).not.toHaveAttribute("open");
    view.rerender(<Navigation active="directory-v2" />);
    expect(group("采集与接入")).toHaveAttribute("open");
    expect(
      screen.getByRole("button", { name: "补充目录资产" }),
    ).toHaveAttribute("aria-current", "page");
  });

  it("retains workspace-modal inert ownership across same-session revalidation", () => {
    const view = render(<Navigation blocked />);
    const sidebar = document.getElementById("soc-navigation-panel")!;
    expect(sidebar).toHaveAttribute("inert");
    expect(sidebar).not.toHaveAttribute("hidden");
    view.rerender(<Navigation blocked gated />);
    expect(sidebar).toHaveAttribute("hidden");
    expect(sidebar).toHaveAttribute("inert");
    view.rerender(<Navigation blocked />);
    expect(sidebar).not.toHaveAttribute("hidden");
    expect(sidebar).toHaveAttribute("inert");
    view.rerender(<Navigation />);
    expect(sidebar).not.toHaveAttribute("inert");
  });

  it("keeps administrator-only entrances absent for a viewer", async () => {
    render(<Navigation actor={{ ...profile, role: "viewer", priv: 0 }} />);
    for (const entry of SOC_NAVIGATION_GROUPS)
      await userEvent.click(
        screen.getByText(entry.label, { selector: "summary" }),
      );
    for (const label of [
      "重置用户密码",
      "凭据授权清理",
      "目录读取授权",
      "补充目录凭据授权",
    ]) {
      expect(
        screen.queryByRole("button", {
          name: label,
          hidden: true,
        }),
      ).toBeNull();
    }
    expect(screen.getByRole("button", { name: "用户资产" })).toBeEnabled();
  });

  it("preserves forced-password and busy gates without granting navigation", async () => {
    const view = render(<Navigation active="password" forced />);
    expect(screen.getByRole("button", { name: "修改密码" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "账户概览" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "用户资产" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "退出登录" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "用户资产" }));
    expect(navigate).not.toHaveBeenCalled();
    view.rerender(<Navigation active="password" forced busy />);
    expect(screen.getByRole("button", { name: "修改密码" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "退出登录" })).toBeDisabled();
  });
});

describe("compact SOC navigation lifecycle", () => {
  beforeEach(() => {
    compact = true;
  });

  it("opens accessibly, traps keyboard focus and restores the toggle on Escape", async () => {
    document.body.style.overflow = "auto";
    render(<Navigation />);
    expect(screen.queryByRole("navigation")).toBeNull();
    const { trigger } = await openMobile();
    expect(document.body.style.overflow).toBe("hidden");
    await userEvent.tab({ shift: true });
    expect(screen.getByRole("button", { name: "退出登录" })).toHaveFocus();
    await userEvent.tab();
    expect(screen.getByRole("button", { name: "关闭导航" })).toHaveFocus();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(trigger).toHaveFocus();
    expect(document.body.style.overflow).toBe("auto");
  });

  it("closes on workspace selection, close button and backdrop without duplicate actions", async () => {
    const { container } = render(<Navigation />);
    const { trigger } = await openMobile();
    await userEvent.click(screen.getByRole("button", { name: "后台任务" }));
    expect(navigate).toHaveBeenCalledExactlyOnceWith("tasks");
    expect(trigger).toHaveFocus();
    expect(screen.queryByRole("dialog")).toBeNull();
    await openMobile();
    await userEvent.click(screen.getByRole("button", { name: "关闭导航" }));
    expect(trigger).toHaveFocus();
    await openMobile();
    fireEvent.click(container.querySelector(".soc-nav-backdrop")!);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(trigger).toHaveFocus();
    expect(navigate).toHaveBeenCalledTimes(1);
  });

  it("closes on history-driven active changes and opens that workspace's group next time", async () => {
    const view = render(<Navigation />);
    const { trigger } = await openMobile();
    view.rerender(<Navigation active="domains" />);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(trigger).toHaveFocus();
    await openMobile();
    expect(group("采集与接入")).toHaveAttribute("open");
    expect(screen.getByRole("button", { name: "域连接" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("hides and clears the open drawer during revalidation without restoring it afterward", async () => {
    const view = render(<Navigation />);
    await openMobile();
    view.rerender(<Navigation gated />);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(
      screen.getByText(profile.username, { exact: true }),
    ).not.toBeVisible();
    expect(screen.getByRole("button", { name: "打开导航" })).toBeDisabled();
    expect(document.body.style.overflow).toBe("");
    view.rerender(<Navigation />);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("button", { name: "打开导航" })).toHaveFocus();
    expect(screen.getByRole("button", { name: "打开导航" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });

  it("prevents a second mobile modal while a workspace detail is open", async () => {
    const view = render(<Navigation />);
    await openMobile();
    view.rerender(<Navigation blocked />);
    expect(screen.queryByRole("dialog")).toBeNull();
    const trigger = screen.getByRole("button", { name: "打开导航" });
    expect(trigger).toBeDisabled();
    expect(trigger).toHaveAttribute("inert");
    await userEvent.click(trigger);
    expect(screen.queryByRole("dialog")).toBeNull();
    view.rerender(<Navigation />);
    expect(trigger).toBeEnabled();
    expect(trigger).not.toHaveAttribute("inert");
    expect(trigger).toHaveFocus();
  });

  it("clears expanded navigation on actor or session changes", async () => {
    const view = render(<Navigation />);
    await openMobile();
    await userEvent.click(
      screen.getByText("平台管理", { selector: "summary" }),
    );
    expect(group("平台管理")).toHaveAttribute("open");
    view.rerender(
      <Navigation
        actor={{
          ...profile,
          ID: 2,
          username: "next-actor",
          csrfToken: "next-csrf",
        }}
      />,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    await openMobile();
    expect(group("平台管理")).not.toHaveAttribute("open");
    expect(screen.queryByText(profile.username, { exact: true })).toBeNull();
    expect(screen.getByText("next-actor", { exact: true })).toBeVisible();
  });

  it("releases the mobile modal and scroll lock when changing to desktop or unmounting", async () => {
    const view = render(<Navigation />);
    await openMobile();
    act(() => {
      compact = false;
      mediaChanged?.();
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("navigation", { name: "账户设置" })).toBeVisible();
    expect(document.body.style.overflow).toBe("");
    act(() => {
      compact = true;
      mediaChanged?.();
    });
    await openMobile();
    view.unmount();
    expect(document.body.style.overflow).toBe("");
  });
});

const reply = (body: unknown, status = 200) =>
  ({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  }) as Response;

describe("SOC shell integration", () => {
  it("shows current workspace context while retaining the public ADTR link during session checks", async () => {
    let resolve!: (value: Response) => void;
    const fetcher = vi.fn(async () => reply(profile));
    vi.stubGlobal("fetch", fetcher);
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    expect(screen.getByLabelText("当前工作区")).toHaveTextContent(
      "账户与身份/账户概览",
    );
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((done) => {
          resolve = done;
        }),
    );
    fireEvent.focus(window);
    expect(
      screen.getByRole("link", { name: "ADTR 身份安全平台" }),
    ).toBeVisible();
    expect(screen.getByLabelText("当前工作区")).toHaveTextContent("会话核验");
    expect(screen.queryByRole("navigation")).toBeNull();
    await act(async () => resolve(reply(profile)));
    expect(screen.getByLabelText("当前工作区")).toHaveTextContent("账户概览");
  });

  it.each(["popstate", "hashchange"])(
    "closes mobile navigation for %s and revalidates before restoring workspace controls",
    async (event) => {
      compact = true;
      let resolve!: (value: Response) => void;
      const fetcher = vi.fn(async () => reply(profile));
      vi.stubGlobal("fetch", fetcher);
      render(<App />);
      await screen.findByRole("heading", { name: "账户概览" });
      await openMobile();
      expect(screen.getByRole("main", { hidden: true })).toHaveAttribute(
        "inert",
      );
      fetcher.mockImplementationOnce(
        () =>
          new Promise<Response>((done) => {
            resolve = done;
          }),
      );
      act(() => {
        window.history.replaceState({}, "", "#profile");
        window.dispatchEvent(new Event(event));
      });
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(screen.queryByRole("navigation")).toBeNull();
      await act(async () => resolve(reply(profile)));
      expect(screen.getByLabelText("当前工作区")).toHaveTextContent("个人资料");
      expect(screen.getByRole("button", { name: "打开导航" })).toHaveFocus();
      expect(screen.getByRole("button", { name: "打开导航" })).toHaveAttribute(
        "aria-expanded",
        "false",
      );
    },
  );

  it("drops navigation and protected content after a mobile focus check confirms no session", async () => {
    compact = true;
    const fetcher = vi.fn(async () => reply(profile));
    vi.stubGlobal("fetch", fetcher);
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await openMobile();
    fetcher.mockImplementationOnce(async () =>
      reply({ error: "unauthenticated" }, 401),
    );
    fireEvent.focus(window);
    await screen.findByRole("heading", { name: "登录账户" });
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("button", { name: "打开导航" })).toBeNull();
    expect(screen.queryByText(profile.username, { exact: true })).toBeNull();
    expect(document.body.style.overflow).toBe("");
  });
});
