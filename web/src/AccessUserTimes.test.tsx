import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AccessUsers from "./AccessUsers";
import type { AccessUser, UserList } from "./access-api";
import type { AccessContext } from "./access-common";

const alice: AccessUser = {
  ID: 2,
  username: "alice",
  passStrength: "high",
  role: "viewer",
  priv: 3,
  mobile: "",
  email: "alice@example.test",
  remark: "Synthetic timestamp fixture",
  createTm: "2026-10-01T01:02:03.123456Z",
  hasMfa: false,
  avatar: "",
  pwdUpdateTm: "2026-10-02T04:05:06.654321Z",
  address: "",
  realName: "Alice",
  department: "",
  post: "",
  roleID: "viewer",
  roleName: "Viewer",
  disabled: false,
};
const bob: AccessUser = {
  ...alice,
  ID: 3,
  username: "bob",
  email: "bob@example.test",
  createTm: "2026-09-01T01:02:03.111111Z",
  pwdUpdateTm: "2026-11-01T04:05:06.999999Z",
};
const result = (
  users = [alice],
  pageIdx = 1,
  total = users.length,
): UserList => ({
  List: users,
  page: {
    pageIdx,
    pageSize: 20,
    total,
    totalPage: Math.max(1, Math.ceil(total / 20)),
  },
  exhausted: pageIdx * 20 >= total,
});
const response = (value: unknown, status = 200) =>
  Promise.resolve({
    ok: status === 200,
    status,
    json: async () => value,
  } as Response);
let context: AccessContext;
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  window.history.replaceState({}, "", "#access/users");
  context = {
    profile: {
      ...alice,
      needChangePwd: false,
      isExpired: false,
      passwordNotUpdatedDays: 0,
      csrfToken: "synthetic-csrf",
    },
    can: () => true,
    menu: [],
    sessionChanged: vi.fn(),
  };
  fetcher = vi.fn(() => response(result()));
  vi.stubGlobal("fetch", fetcher);
});
const start = () => render(<AccessUsers context={context} roles={[]} />);
const click = (name: string) =>
  userEvent.click(screen.getByRole("button", { name }));
function row(username: string) {
  return screen
    .getByRole("rowheader", { name: new RegExp(`^${username} ID `) })
    .closest("tr")!;
}
function expectTimes(user: AccessUser) {
  const current = row(user.username);
  for (const field of ["createTm", "pwdUpdateTm"] as const) {
    const cell = current.querySelector(`[data-field="${field}"]`)!;
    expect(cell.textContent).toBe(user[field] || "未提供");
    expect(cell.closest("details")).toBeNull();
    expect(cell).toBeVisible();
  }
}
function expectOnlyReads() {
  for (const [url, init] of fetcher.mock.calls) {
    expect(url).toMatch(/^\/api\/access\/users\?/);
    expect(init.method).toBe("GET");
  }
}

describe("platform user timestamp columns", () => {
  it("shows independent UTC columns with unmodified microseconds before opening details", async () => {
    start();
    const table = await screen.findByRole("table", { name: "平台用户列表" });
    expect(
      within(table).getByRole("columnheader", { name: "创建时间（UTC）" }),
    ).toHaveAttribute("scope", "col");
    expect(
      within(table).getByRole("columnheader", { name: "密码更新时间（UTC）" }),
    ).toHaveAttribute("scope", "col");
    expectTimes(alice);
    const details = row("alice").querySelector("details")!;
    expect(details.open).toBe(false);
    await userEvent.click(
      within(details).getByText(alice.email, { selector: "summary" }),
    );
    for (const value of [alice.createTm, alice.pwdUpdateTm]) {
      expect(within(details).getByText(value)).toBeVisible();
    }
    expectOnlyReads();
  });

  it("keeps equal timestamps as two separate cells", async () => {
    const user = { ...alice, pwdUpdateTm: alice.createTm };
    fetcher.mockImplementation(() => response(result([user])));
    start();
    await screen.findByRole("table");
    expectTimes(user);
    expect(
      within(row("alice")).getAllByRole("cell", { name: alice.createTm }),
    ).toHaveLength(2);
  });

  it.each([
    { ...alice, createTm: "" },
    { ...alice, pwdUpdateTm: "" },
    { ...alice, createTm: "", pwdUpdateTm: "" },
  ])(
    "uses 未提供 only for the empty field ($createTm / $pwdUpdateTm)",
    async (user) => {
      fetcher.mockImplementation(() => response(result([user])));
      start();
      await screen.findByRole("table");
      expectTimes(user);
    },
  );

  it("keeps times bound to the server row through every existing sort, filtering and paging", async () => {
    fetcher.mockImplementation((url: string) => {
      const query = new URL(url, "http://localhost").searchParams;
      const page = Number(query.get("pageIdx"));
      return response(
        result(
          page === 2
            ? [bob]
            : query.get("search")
              ? [alice]
              : Number(query.get("sort")) > 0
                ? [bob, alice]
                : [alice, bob],
          page,
          21,
        ),
      );
    });
    start();
    await screen.findByRole("table");
    for (const sort of ["1", "-1", "2", "-2"]) {
      await userEvent.selectOptions(screen.getByLabelText("排序"), sort);
      await click("应用筛选");
      await screen.findByRole("table");
      expect(
        new URL(
          fetcher.mock.calls.at(-1)![0],
          "http://localhost",
        ).searchParams.get("sort"),
      ).toBe(sort);
      expectTimes(alice);
      expectTimes(bob);
      const names = screen
        .getAllByRole("rowheader")
        .map((header) => header.textContent);
      expect(names).toEqual(
        Number(sort) > 0 ? ["bobID 3", "aliceID 2"] : ["aliceID 2", "bobID 3"],
      );
    }
    await click("下一页");
    await screen.findByRole("table");
    expectTimes(bob);
    expect(
      screen.queryByRole("rowheader", { name: /alice/ }),
    ).not.toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("搜索用户名"), "alice");
    await userEvent.click(screen.getByText("更多筛选条件"));
    fireEvent.change(screen.getByLabelText("创建开始时间"), {
      target: { value: "2026-09-01T00:00" },
    });
    fireEvent.change(screen.getByLabelText("密码更新结束时间"), {
      target: { value: "2026-11-01T00:00" },
    });
    await click("应用筛选");
    await screen.findByRole("table");
    const query = new URL(fetcher.mock.calls.at(-1)![0], "http://localhost")
      .searchParams;
    expect(query.get("pageIdx")).toBe("1");
    expect(query.get("search")).toBe("alice");
    expect(query.get("filterStartCreateTm")).toBe(
      new Date("2026-09-01T00:00").toISOString(),
    );
    expect(query.get("filterEndPassTm")).toBe(
      new Date("2026-11-01T00:00").toISOString(),
    );
    expectTimes(alice);
    expect(
      screen.queryByRole("rowheader", { name: /bob/ }),
    ).not.toBeInTheDocument();
    expectOnlyReads();
  });

  it("shows both columns to a read-only reader without mutation or proof controls", async () => {
    context.can = (operation) => operation === "GET /api/access/users";
    start();
    await screen.findByRole("table");
    expectTimes(alice);
    expect(
      screen.queryByRole("checkbox", { name: "选择 alice" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: /新增用户|编辑 alice|分配 alice|删除 alice|批量分配/,
      }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    expectOnlyReads();
  });

  it("retains headers but no timestamp rows for an empty result and removes old rows on failure", async () => {
    start();
    await screen.findByRole("table");
    expectTimes(alice);
    fetcher.mockImplementation(() => response({ error: "forbidden" }, 403));
    await click("刷新用户列表");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "没有执行此操作的权限",
    );
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.queryByText(alice.createTm)).not.toBeInTheDocument();
    fetcher.mockImplementation(() => response(result([])));
    await click("刷新用户列表");
    await screen.findByText("没有符合条件的用户。");
    expect(
      screen.getByRole("columnheader", { name: "创建时间（UTC）" }),
    ).toBeVisible();
    expect(
      screen.getByRole("columnheader", { name: "密码更新时间（UTC）" }),
    ).toBeVisible();
    expect(screen.queryAllByRole("rowheader")).toHaveLength(0);
    expect(screen.queryByText("未提供")).not.toBeInTheDocument();
    expectOnlyReads();
  });

  it("ignores a delayed old page and aborts pending reads on unmount", async () => {
    let resolveOld!: (value: Response) => void;
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          resolveOld = resolve;
        }),
    );
    const mounted = start();
    const initialSignal = fetcher.mock.calls[0][1].signal as AbortSignal;
    await click("刷新用户列表");
    await screen.findByRole("table");
    expect(initialSignal.aborted).toBe(true);
    await act(async () => resolveOld(await response(result([bob]))));
    expectTimes(alice);
    expect(
      screen.queryByRole("rowheader", { name: /bob/ }),
    ).not.toBeInTheDocument();
    let resolveLate!: (value: Response) => void;
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          resolveLate = resolve;
        }),
    );
    await click("刷新用户列表");
    const pendingSignal = fetcher.mock.calls.at(-1)![1].signal as AbortSignal;
    mounted.unmount();
    expect(pendingSignal.aborted).toBe(true);
    await act(async () => resolveLate(await response(result([bob]))));
    expect(mounted.container).toBeEmptyDOMElement();
    expect(context.sessionChanged).not.toHaveBeenCalled();
    expectOnlyReads();
  });
});
