// Synthetic DOM contracts; native API/PostgreSQL/browser evidence is separate.
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AccessUsers from "./AccessUsers";
import type { AccessUser, UserList } from "./access-api";
import type { AccessContext } from "./access-common";

const user = (ID: number, username: string): AccessUser => ({
  ID,
  username,
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
});
const alice = user(81, "alice"),
  bob = user(7, "bob"),
  carol = user(305, "carol");
function result(
  users = [alice, bob],
  pageIdx = 1,
  pageSize = 20,
  total = users.length,
): UserList {
  return {
    List: users,
    page: {
      pageIdx,
      pageSize,
      total,
      totalPage: pageSize === -1 ? 1 : Math.ceil(total / pageSize),
    },
    exhausted: pageSize === -1 || pageIdx * pageSize >= total,
  };
}
const response = (value: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
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
      csrfToken: "synthetic-session",
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
function numbers(users: AccessUser[]) {
  const table = screen.getByRole("table", { name: "平台用户列表" });
  const rows = within(table).queryAllByRole("rowheader");
  expect(rows.map((row) => row.textContent)).toEqual(
    users.map((row) => `${row.username}ID ${row.ID}`),
  );
  expect(table.querySelectorAll('td[data-field="rowNumber"]')).toHaveLength(
    users.length,
  );
  rows.forEach((header, index) => {
    const row = header.closest("tr")!;
    const cell = row.querySelector('td[data-field="rowNumber"]')!;
    expect(cell).toBeVisible();
    expect(cell.textContent).toBe(String(index + 1));
    expect(cell).not.toHaveAttribute("tabindex");
    expect(cell.querySelector("button, input, a, select")).toBeNull();
    expect(header).toHaveAttribute("scope", "row");
    // Existing role-cell position and identity-based selection remain intact.
    if (context.can("POST /api/access/assignments")) {
      expect(within(row).getAllByRole("cell")[1]).toHaveTextContent(
        users[index].roleID,
      );
      expect(
        within(row).getByRole("checkbox", {
          name: `选择 ${users[index].username}`,
        }),
      ).toBeVisible();
    }
  });
}
function onlyReads() {
  for (const [url, init] of fetcher.mock.calls) {
    expect(url).toMatch(/^\/api\/access\/users\?/);
    expect(init.method).toBe("GET");
    expect(init.body).toBeUndefined();
  }
}

describe("platform user page-local row numbers", () => {
  it("labels the display ordinal separately from the unchanged persistent user ID", async () => {
    start();
    await screen.findByRole("table");
    expect(
      screen.getByRole("columnheader", { name: "本页序号" }),
    ).toHaveAttribute("scope", "col");
    numbers([alice, bob]);
    onlyReads();
  });

  it.each(["-1", "1", "-2", "2"])(
    "follows server row order for sort %s without sorting locally",
    async (sort) => {
      fetcher.mockImplementation((url: string) => {
        const query = new URL(url, "http://localhost").searchParams;
        return response(
          result(
            query.get("sort") === sort ? [bob, carol, alice] : [alice, bob],
          ),
        );
      });
      start();
      await screen.findByRole("table");
      await userEvent.selectOptions(screen.getByLabelText("排序"), sort);
      await click("应用筛选");
      await screen.findByRole("table");
      numbers([bob, carol, alice]);
      onlyReads();
    },
  );

  it("restarts at one on later pages and preserves selected-user identity", async () => {
    fetcher.mockImplementation((url: string) => {
      const query = new URL(url, "http://localhost").searchParams;
      const page = Number(query.get("pageIdx"));
      return response(
        result(page === 1 ? [alice, bob] : [carol], page, 20, 21),
      );
    });
    start();
    await screen.findByRole("table");
    await userEvent.click(screen.getByRole("checkbox", { name: "选择 bob" }));
    expect(screen.getByRole("button", { name: "批量分配（1）" })).toBeEnabled();
    await click("下一页");
    await screen.findByRole("rowheader", { name: /carol/ });
    numbers([carol]);
    expect(
      screen.getByRole("button", { name: "批量分配（0）" }),
    ).toBeDisabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "选择 carol" }));
    await click("批量分配（1）");
    expect(screen.getByLabelText("角色：carol")).toBeVisible();
    expect(screen.queryByLabelText("角色：alice")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("角色：bob")).not.toBeInTheDocument();
    onlyReads();
  });

  it.each([10, 20, 30, 40, 50, 100, -1])(
    "numbers actual returned rows for existing page size %s",
    async (size) => {
      const users = Array.from(
        { length: size === -1 ? 103 : size },
        (_, index) => user(1000 + index * 3, `fixture-${index}`),
      );
      fetcher.mockImplementation((url: string) => {
        const query = new URL(url, "http://localhost").searchParams;
        return response(
          result(
            users,
            Number(query.get("pageIdx")),
            Number(query.get("pageSize")),
          ),
        );
      });
      start();
      await screen.findByRole("table");
      await userEvent.selectOptions(
        screen.getByLabelText("每页条数"),
        String(size),
      );
      await click("应用筛选");
      await screen.findByRole("table");
      numbers(users);
      onlyReads();
    },
  );

  it("keeps applied rows while editing a draft and renumbers the applied filter result", async () => {
    fetcher.mockImplementation((url: string) =>
      response(
        result(
          new URL(url, "http://localhost").searchParams.get("search")
            ? [bob]
            : [alice, bob],
        ),
      ),
    );
    start();
    await screen.findByRole("table");
    const calls = fetcher.mock.calls.length;
    await userEvent.type(screen.getByLabelText("搜索用户名"), "bob");
    numbers([alice, bob]);
    expect(fetcher).toHaveBeenCalledTimes(calls);
    await click("应用筛选");
    await screen.findByRole("table");
    numbers([bob]);
    await click("清除筛选");
    await screen.findByRole("table");
    numbers([alice, bob]);
    onlyReads();
  });

  it("keeps original time-filter semantics and numbers the actual matching subset", async () => {
    fetcher.mockImplementation((url: string) =>
      response(
        result(
          new URL(url, "http://localhost").searchParams.has(
            "filterStartCreateTm",
          )
            ? [carol]
            : [alice, bob],
        ),
      ),
    );
    start();
    await screen.findByRole("table");
    await userEvent.click(screen.getByText("更多筛选条件"));
    fireEvent.change(screen.getByLabelText("创建开始时间"), {
      target: { value: "2026-09-01T00:00" },
    });
    await click("应用筛选");
    await screen.findByRole("table");
    numbers([carol]);
    const query = new URL(fetcher.mock.calls.at(-1)![0], "http://localhost")
      .searchParams;
    expect(query.get("filterStartCreateTm")).toBe(
      new Date("2026-09-01T00:00").toISOString(),
    );
    expect(query.get("pageIdx")).toBe("1");
    onlyReads();
  });

  it("is read-only for readers and does not create selection or proof controls", async () => {
    context.can = (operation) => operation === "GET /api/access/users";
    start();
    await screen.findByRole("table");
    numbers([alice, bob]);
    expect(
      screen.queryByRole("checkbox", { name: /选择 / }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: /新增用户|批量分配|编辑 alice|分配 alice|删除 alice/,
      }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("操作者当前密码")).not.toBeInTheDocument();
    onlyReads();
  });

  it("shows an empty table header with no invented ordinal", async () => {
    fetcher.mockImplementation(() => response(result([])));
    start();
    await screen.findByText("没有符合条件的用户。");
    expect(
      screen.getByRole("columnheader", { name: "本页序号" }),
    ).toBeVisible();
    numbers([]);
    onlyReads();
  });

  it.each([403, 500])(
    "clears old numbered rows on HTTP %s then recovers from a fresh response",
    async (status) => {
      start();
      await screen.findByRole("table");
      numbers([alice, bob]);
      fetcher.mockImplementation(() =>
        response({ error: status === 403 ? "forbidden" : "internal" }, status),
      );
      await click("刷新用户列表");
      await screen.findByRole("alert");
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
      fetcher.mockImplementation(() => response(result([carol])));
      await click("刷新用户列表");
      await screen.findByRole("table");
      numbers([carol]);
      onlyReads();
    },
  );

  it("never restores numbered rows from a superseded or unmounted read", async () => {
    start();
    await screen.findByRole("table");
    numbers([alice, bob]);
    let resolveOld!: (value: Response) => void;
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          resolveOld = resolve;
        }),
    );
    await click("刷新用户列表");
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    const signal = fetcher.mock.calls.at(-1)![1].signal as AbortSignal;
    fetcher.mockImplementation(() => response(result([carol])));
    await click("刷新用户列表");
    await screen.findByRole("table");
    expect(signal.aborted).toBe(true);
    await act(async () => resolveOld(await response(result([alice, bob]))));
    numbers([carol]);
    onlyReads();
  });

  it("aborts pending reads and cannot render an ordinal after unmount", async () => {
    let resolveLate!: (value: Response) => void;
    fetcher.mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          resolveLate = resolve;
        }),
    );
    const mounted = start(),
      signal = fetcher.mock.calls[0][1].signal as AbortSignal;
    mounted.unmount();
    expect(signal.aborted).toBe(true);
    await act(async () => resolveLate(await response(result([alice, bob]))));
    expect(mounted.container).toBeEmptyDOMElement();
    onlyReads();
  });
});
