// Synthetic DOM contracts. Actual API/PostgreSQL/browser evidence is separate.
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AccessUsers from "./AccessUsers";
import AccessRoles from "./AccessRoles";
import type { AccessUser, Role } from "./access-api";
import type { AccessContext } from "./access-common";

const alice: AccessUser = {
  ID: 81,
  username: "alice",
  passStrength: "high",
  role: "viewer",
  priv: 3,
  mobile: "",
  email: "",
  remark: "",
  createTm: "2026-10-01T00:00:00Z",
  hasMfa: false,
  avatar: "",
  pwdUpdateTm: "2026-10-02T00:00:00Z",
  address: "",
  realName: "",
  department: "",
  post: "",
  roleID: "viewer",
  roleName: "Viewer",
  disabled: false,
};
const viewer: Role = {
  id: "viewer",
  name: "Viewer",
  userNum: 1,
  remark: "",
  allowDelete: false,
  allowEdit: false,
  created: alice.createTm,
  dataSrc: {},
};
const variants = [
  {
    kind: "users",
    label: "搜索用户名",
    clear: "清空用户名关键词",
    submit: "应用筛选",
    reset: "清除筛选",
    refresh: "刷新用户列表",
    table: "平台用户列表",
    sort: "排序",
  },
  {
    kind: "roles",
    label: "搜索角色名称",
    clear: "清空角色名关键词",
    submit: "筛选角色",
    reset: "清除角色筛选",
    refresh: "刷新角色列表",
    table: "角色列表",
    sort: "角色排序",
  },
] as const;
type Variant = (typeof variants)[number];
let context: AccessContext;
let fetcher: ReturnType<typeof vi.fn>;
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response);
function result(
  kind: string,
  url = `/api/access/${kind}?pageIdx=1&pageSize=20`,
) {
  const q = new URL(url, "http://localhost").searchParams;
  const pageIdx = Number(q.get("pageIdx") || 1),
    pageSize = Number(q.get("pageSize") || 20);
  return {
    ...(kind === "users" ? { List: [alice] } : { list: [viewer] }),
    page: { pageIdx, pageSize, total: 41, totalPage: Math.ceil(41 / pageSize) },
    exhausted: pageIdx * pageSize >= 41,
  };
}
beforeEach(() => {
  window.history.replaceState({}, "", "#access/users");
  context = {
    profile: {
      ...alice,
      needChangePwd: false,
      isExpired: false,
      passwordNotUpdatedDays: 0,
      csrfToken: "synthetic-session",
    },
    can: () => true,
    menu: [],
    sessionChanged: vi.fn(),
  };
  fetcher = vi.fn((url: string) =>
    response(result(url.includes("/users?") ? "users" : "roles", url)),
  );
  vi.stubGlobal("fetch", fetcher);
});
const click = (name: string) =>
  userEvent.click(screen.getByRole("button", { name }));
const query = () =>
  new URL(fetcher.mock.calls.at(-1)![0] as string, "http://localhost")
    .searchParams;
function start(v: Variant) {
  return render(
    v.kind === "users" ? (
      <AccessUsers context={context} roles={[viewer]} />
    ) : (
      <AccessRoles context={context} />
    ),
  );
}
function input(v: Variant) {
  return screen.getByLabelText(v.label) as HTMLInputElement;
}
function formValues(v: Variant) {
  return [...new FormData(input(v).form!).entries()].filter(
    ([key]) => key !== "search",
  );
}
function onlyReads(v: Variant) {
  for (const [url, init] of fetcher.mock.calls) {
    expect(url).toMatch(new RegExp(`^/api/access/${v.kind}\\?`));
    expect(init.method).toBe("GET");
    expect(init.body).toBeUndefined();
  }
}

for (const v of variants)
  describe(`${v.kind} keyword draft clearing`, () => {
    it("keeps the input label, limit, help and a separate non-submit accessible button", async () => {
      start(v);
      await screen.findByRole("table");
      expect(input(v)).toHaveAttribute("name", "search");
      expect(input(v)).toHaveAttribute("maxlength", "50");
      expect(input(v)).toHaveAccessibleDescription(/清空仅修改输入/);
      const button = screen.getByRole("button", { name: v.clear });
      expect(button).toHaveAttribute("type", "button");
      expect(button.closest("label")).toBeNull();
      expect(
        input(v)
          .closest("label")!
          .querySelectorAll("input,button,select,textarea"),
      ).toHaveLength(1);
      onlyReads(v);
    });

    it.each(["Enter", " "])(
      "clears via keyboard %s and returns focus without submitting",
      async (key) => {
        start(v);
        await screen.findByRole("table");
        await userEvent.type(input(v), "草稿%_keyword");
        const calls = fetcher.mock.calls.length;
        screen.getByRole("button", { name: v.clear }).focus();
        await userEvent.keyboard(key === "Enter" ? "{Enter}" : " ");
        expect(input(v)).toHaveValue("");
        expect(input(v)).toHaveFocus();
        expect(fetcher).toHaveBeenCalledTimes(calls);
        onlyReads(v);
      },
    );

    it("repeated nonempty and already-empty clicks preserve rows, other draft controls and user selections", async () => {
      start(v);
      const table = await screen.findByRole("table");
      const before = table.innerHTML;
      await userEvent.selectOptions(screen.getByLabelText(v.sort), "1");
      await userEvent.selectOptions(screen.getByLabelText("每页条数"), "10");
      if (v.kind === "users") {
        await userEvent.click(screen.getByText("更多筛选条件"));
        for (const [name, value] of [
          ["filterRole", "viewer,custom"],
          ["roleID", "viewer"],
          ["filterStartCreateTm", "2026-10-01T01:02"],
          ["filterEndCreateTm", "2026-10-02T01:02"],
          ["filterStartPassTm", "2026-10-03T01:02"],
          ["filterEndPassTm", "2026-10-04T01:02"],
        ]) {
          fireEvent.change(input(v).form!.querySelector(`[name="${name}"]`)!, {
            target: { value },
          });
        }
        await userEvent.click(screen.getByLabelText("仅当前账户"));
        await userEvent.click(screen.getByLabelText("已开启"));
        await userEvent.click(screen.getByLabelText("高", { exact: true }));
        await userEvent.click(
          screen.getByRole("checkbox", { name: "选择 alice" }),
        );
      }
      const fields = formValues(v),
        calls = fetcher.mock.calls.length;
      await userEvent.type(input(v), "draft");
      await click(v.clear);
      await click(v.clear);
      expect(input(v)).toHaveValue("");
      expect(input(v)).toHaveFocus();
      expect(formValues(v)).toEqual(fields);
      expect(fetcher).toHaveBeenCalledTimes(calls);
      expect(table.innerHTML).toBe(before);
      if (v.kind === "users")
        expect(
          screen.getByRole("checkbox", { name: "选择 alice" }),
        ).toBeChecked();
    });

    it("keeps applied search and page on clear/refresh/page navigation, then explicitly submits empty search", async () => {
      start(v);
      await screen.findByRole("table");
      await userEvent.type(input(v), "a");
      await userEvent.selectOptions(screen.getByLabelText(v.sort), "1");
      await userEvent.selectOptions(screen.getByLabelText("每页条数"), "10");
      await click(v.submit);
      await screen.findByRole("table");
      await click("下一页");
      const table = await screen.findByRole("table");
      expect(query().get("pageIdx")).toBe("2");
      expect(query().get("search")).toBe("a");
      const before = table.innerHTML,
        calls = fetcher.mock.calls.length;
      await click(v.clear);
      expect(fetcher).toHaveBeenCalledTimes(calls);
      expect(table.innerHTML).toBe(before);
      await click(v.refresh);
      await screen.findByRole("table");
      expect(query().get("pageIdx")).toBe("2");
      expect(query().get("search")).toBe("a");
      await click("下一页");
      await screen.findByRole("table");
      expect(query().get("pageIdx")).toBe("3");
      expect(query().get("search")).toBe("a");
      await click(v.submit);
      await screen.findByRole("table");
      expect(query().get("pageIdx")).toBe("1");
      expect(query().has("search")).toBe(false);
      expect(query().get("sort")).toBe("1");
      expect(query().get("pageSize")).toBe("10");
      onlyReads(v);
    });

    it("retains native whole-form reset after clearing and typing again", async () => {
      start(v);
      await screen.findByRole("table");
      await userEvent.type(input(v), "first");
      await click(v.clear);
      await userEvent.type(input(v), "second");
      await userEvent.selectOptions(screen.getByLabelText(v.sort), "1");
      await userEvent.selectOptions(screen.getByLabelText("每页条数"), "10");
      await click(v.submit);
      await screen.findByRole("table");
      await click(v.reset);
      await screen.findByRole("table");
      expect(input(v)).toHaveValue("");
      expect(screen.getByLabelText(v.sort)).toHaveValue("-1");
      expect(screen.getByLabelText("每页条数")).toHaveValue("20");
      expect(query().toString()).toBe("pageIdx=1&pageSize=20&sort=-1");
    });

    it("works for read-only users without adding write controls or writes", async () => {
      context.can = (operation) => operation.startsWith("GET ");
      start(v);
      await screen.findByRole("table");
      await userEvent.type(input(v), "draft");
      await click(v.clear);
      expect(input(v)).toHaveValue("");
      expect(
        screen.queryByRole("button", {
          name: v.kind === "users" ? "新增用户" : "新增角色",
        }),
      ).toBeNull();
      expect(screen.queryByRole("checkbox", { name: "选择 alice" })).toBeNull();
      onlyReads(v);
    });

    it("does not cancel a pending applied read; its result does not repopulate the cleared draft", async () => {
      start(v);
      await screen.findByRole("table");
      let settle!: (value: Response) => void;
      fetcher.mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            settle = resolve;
          }),
      );
      await userEvent.type(input(v), "pending");
      await click(v.submit);
      const signal = fetcher.mock.calls.at(-1)![1].signal as AbortSignal;
      const calls = fetcher.mock.calls.length;
      await click(v.clear);
      expect(signal.aborted).toBe(false);
      expect(fetcher).toHaveBeenCalledTimes(calls);
      expect(screen.queryByRole("table")).toBeNull();
      await act(async () => {
        settle(await response(result(v.kind)));
      });
      await screen.findByRole("table");
      expect(input(v)).toHaveValue("");
    });

    it("keeps failure state and submits cleared draft only when explicitly retried", async () => {
      start(v);
      await screen.findByRole("table");
      fetcher.mockImplementationOnce(() =>
        response({ error: "internal" }, 500),
      );
      await userEvent.type(input(v), "failing");
      await click(v.submit);
      const error = await screen.findByRole("alert"),
        text = error.textContent;
      const calls = fetcher.mock.calls.length;
      await click(v.clear);
      expect(error.textContent).toBe(text);
      expect(fetcher).toHaveBeenCalledTimes(calls);
      expect(screen.queryByRole("table")).toBeNull();
      await click(v.submit);
      await screen.findByRole("table");
      expect(query().has("search")).toBe(false);
    });

    it("retains newer-read and unmount rejection after clearing a pending old query", async () => {
      const view = start(v);
      await screen.findByRole("table");
      let settle!: (value: Response) => void;
      fetcher.mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            settle = resolve;
          }),
      );
      await userEvent.type(input(v), "old");
      await click(v.submit);
      const signal = fetcher.mock.calls.at(-1)![1].signal as AbortSignal;
      await click(v.clear);
      await click(v.submit);
      await screen.findByRole("table");
      expect(signal.aborted).toBe(true);
      const before = screen.getByRole("table").innerHTML;
      await act(async () => {
        settle(
          await response({
            ...result(v.kind),
            ...(v.kind === "users" ? { List: [] } : { list: [] }),
          }),
        );
      });
      expect(screen.getByRole("table").innerHTML).toBe(before);
      expect(input(v)).toHaveValue("");
      let finish!: (value: Response) => void;
      fetcher.mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          }),
      );
      await click(v.refresh);
      view.unmount();
      await act(async () => {
        finish(await response(result(v.kind)));
      });
      expect(screen.queryByRole("table")).toBeNull();
      expect(screen.queryByRole("button", { name: v.clear })).toBeNull();
    });
  });
