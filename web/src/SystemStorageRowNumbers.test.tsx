import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import SystemWorkspace from "./SystemWorkspace";
import type { Profile } from "./api";
import type { Permission } from "./access-api";
import type { StorageItem, StorageView } from "./system-api";

const now = "2026-11-06T08:00:00Z";
const profile: Profile = {
  ID: 1,
  username: "synthetic-storage-reader",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: now,
  csrfToken: "synthetic-storage-csrf",
};
// Multiple targets exercise the presentation contract only. The real backend
// currently registers just runtime-root; these are not real mounted resources.
const disk = (id: string, revision = 0): StorageItem => ({
  id,
  mount: `/synthetic/${id}`,
  fs: "synthetic-fs",
  totalBytes: "1000",
  usedBytes: "200",
  freeBytes: "750",
  reservedBytes: "50",
  percent: 21.0526,
  alarmPercent: 85,
  revision,
  alarmExceeded: false,
  observedAt: now,
  source: "linux_statfs",
  scope: "runtime_filesystem",
  availability: "available",
});
const view = (
  storage: StorageItem[],
  page = 1,
  pageSize = 20,
  total = storage.length,
): StorageView => ({
  instance: "local-api",
  storage,
  total,
  page,
  pageSize,
  availability: "available",
  stale: false,
  observedAt: now,
  checkedAt: now,
});
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response);
function deferred() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
let items: StorageItem[],
  menuReadable: boolean,
  storageReadable: boolean,
  writeable: boolean,
  fetcher: ReturnType<typeof vi.fn>,
  override: (url: string, init: RequestInit) => Promise<Response> | undefined;
beforeEach(() => {
  window.history.replaceState({}, "", "#system/storage");
  items = [disk("synthetic-alpha", 4), disk("synthetic-beta", 37)];
  menuReadable = true;
  storageReadable = true;
  writeable = true;
  override = () => undefined;
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const custom = override(url, init);
    if (custom) return custom;
    const parsed = new URL(url, window.location.origin);
    if (parsed.pathname === "/api/access/menu") {
      const permission: Permission = {
        mark: "system",
        name: "系统健康",
        auth: { readable: menuReadable, writeable },
        allow_auth: { readable: true, writeable: true },
        paths: [],
        children: [],
        checked: menuReadable,
        icon: "system",
      };
      return response({ menu: [permission] });
    }
    if (parsed.pathname === "/api/access/check")
      return response({
        results: JSON.parse(init.body as string).paths.map((path: string) =>
          path === "GET /api/system/storage"
            ? storageReadable
            : path === "POST /api/system/storage/settings"
              ? writeable
              : true,
        ),
      });
    if (parsed.pathname === "/api/system/storage") {
      const page = Number(parsed.searchParams.get("page"));
      const pageSize = Number(parsed.searchParams.get("pageSize"));
      return response(
        view(
          items.slice((page - 1) * pageSize, page * pageSize),
          page,
          pageSize,
          items.length,
        ),
      );
    }
    if (parsed.pathname === "/api/system/storage/settings") {
      const body = JSON.parse(init.body as string);
      return response({
        result: 1,
        setting: {
          instance: "local-api",
          storageId: body.storageId,
          alarmPercent: body.percent,
          revision: body.expectedRevision + 1,
          updatedAt: now,
        },
      });
    }
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
const calls = (path = "/storage") =>
  fetcher.mock.calls.filter(
    ([url]) => url.split("?")[0] === `/api/system${path}`,
  );
const table = () => screen.getByRole("table", { name: "实际文件系统存储" });
const rows = () => within(table()).getAllByRole("row").slice(1);
function expectRows(ids: string[]) {
  expect(rows()).toHaveLength(ids.length);
  rows().forEach((row, index) => {
    expect(row.children[0]).toHaveTextContent(new RegExp(`^${index + 1}$`));
    expect(row.children[0]).toHaveRole("cell");
    expect(within(row).getByRole("rowheader")).toHaveTextContent(
      new RegExp(`^${ids[index]}$`),
    );
  });
}
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
async function start(sessionChanged = vi.fn()) {
  const mounted = render(
    <SystemWorkspace profile={profile} sessionChanged={sessionChanged} />,
  );
  await screen.findByRole("table", { name: "实际文件系统存储" });
  return mounted;
}
async function refresh() {
  click("刷新存储状态");
  await screen.findByRole("table", { name: "实际文件系统存储" });
}
function submit() {
  fill("存储告警阈值（%）", "89");
  fill("操作者当前密码", "Synthetic Actor Password 123");
  fill("未使用的认证器验证码", "123456");
  const form = screen
    .getByRole("button", { name: "保存告警阈值" })
    .closest("form")!;
  fireEvent.submit(form);
  return form;
}

describe("storage page-local row numbers (UI-0646)", () => {
  it("puts the ordinal first while preserving resource row headers and read requests", async () => {
    await start();
    const headers = within(table()).getAllByRole("columnheader");
    expect(headers[0]).toHaveTextContent(/^本页序号$/);
    expect(headers[0]).toHaveAttribute("scope", "col");
    expect(headers[1]).toHaveTextContent(/^存储目标$/);
    expectRows(["synthetic-alpha", "synthetic-beta"]);
    expect(calls()).toHaveLength(1);
    expect(calls()[0][0]).toBe(
      "/api/system/storage?instance=local-api&page=1&pageSize=20",
    );
    expect(fetcher).toHaveBeenCalledTimes(3); // Two existing permission reads.
    expect(calls("/storage/settings")).toHaveLength(0);
    expect(window.location.hash).toBe("#system/storage");
  });

  it("restarts at 1 on page two instead of adding the page offset", async () => {
    items = Array.from({ length: 22 }, (_, index) =>
      disk(`synthetic-target-${index + 1}`),
    );
    await start();
    expectRows(items.slice(0, 20).map((item) => item.id));
    click("下一页存储");
    await screen.findByText("共 22 条 · 第 2 页");
    expectRows(["synthetic-target-21", "synthetic-target-22"]);
    expect(rows().map((row) => row.children[0].textContent)).toEqual([
      "1",
      "2",
    ]);
    expect(calls().map(([url]) => url)).toEqual([
      "/api/system/storage?instance=local-api&page=1&pageSize=20",
      "/api/system/storage?instance=local-api&page=2&pageSize=20",
    ]);
    expect(screen.getByRole("button", { name: "下一页存储" })).toBeDisabled();
    click("上一页存储");
    await screen.findByText("共 22 条 · 第 1 页");
    expectRows(items.slice(0, 20).map((item) => item.id));
    expect(calls()).toHaveLength(3);
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it("resets the existing page state on page-size change and uses only existing query keys", async () => {
    items = Array.from({ length: 22 }, (_, index) =>
      disk(`synthetic-${index}`),
    );
    await start();
    click("下一页存储");
    await screen.findByText("共 22 条 · 第 2 页");
    fill("存储每页条数", "10");
    await screen.findByText("共 22 条 · 第 1 页");
    expectRows(items.slice(0, 10).map((item) => item.id));
    expect(calls().map(([url]) => url)).toEqual([
      "/api/system/storage?instance=local-api&page=1&pageSize=20",
      "/api/system/storage?instance=local-api&page=2&pageSize=20",
      "/api/system/storage?instance=local-api&page=1&pageSize=10",
    ]);
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it("renumbers a reordered poll response but preserves the React row identity by resource ID", async () => {
    const mounted = await start();
    vi.useFakeTimers();
    await act(async () => click("刷新存储状态"));
    const original = rows();
    items = [items[1], items[0]];
    await act(async () => vi.advanceTimersByTimeAsync(14999));
    expect(calls()).toHaveLength(2);
    expectRows(["synthetic-alpha", "synthetic-beta"]);
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expectRows(["synthetic-beta", "synthetic-alpha"]);
    expect(rows()[0]).toBe(original[1]);
    expect(rows()[1]).toBe(original[0]);
    expect(calls()).toHaveLength(3);
    expect(calls("/storage/settings")).toHaveLength(0);
    mounted.unmount();
    await act(async () => vi.advanceTimersByTimeAsync(30000));
    expect(calls()).toHaveLength(3);
  });

  it("renumbers a reduced server response without adding a client filter or changing IDs", async () => {
    await start();
    items = [items[1]];
    await refresh();
    expectRows(["synthetic-beta"]);
    expect(
      screen.queryByRole("rowheader", { name: "synthetic-alpha" }),
    ).toBeNull();
    expect(calls()).toHaveLength(2);
    expect(calls()[1][0]).toBe(calls()[0][0]);
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it("keeps an empty page empty rather than manufacturing ordinal zero or a storage row", async () => {
    items = [];
    await start();
    expectRows([]);
    expect(screen.getByText("此页没有已登记存储目标。")).toBeVisible();
    expect(within(table()).getAllByRole("columnheader")[0]).toHaveTextContent(
      "本页序号",
    );
    expect(calls()).toHaveLength(1);
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it("numbers an unavailable registered resource without converting unknown metrics into zero", async () => {
    items = [
      {
        ...disk("synthetic-unavailable"),
        totalBytes: null,
        usedBytes: null,
        freeBytes: null,
        reservedBytes: null,
        percent: null,
        alarmExceeded: null,
        observedAt: "0001-01-01T00:00:00Z",
        availability: "unavailable",
        reason: "no_samples",
      },
    ];
    override = (url) =>
      url.startsWith("/api/system/storage?")
        ? response({
            ...view(items),
            availability: "unavailable",
            observedAt: null,
            reason: "no_samples",
          })
        : undefined;
    await start();
    expectRows(["synthetic-unavailable"]);
    for (const column of [3, 4, 5, 6, 7, 10])
      expect(rows()[0].children[column]).toHaveTextContent(/^不可用$/);
    expect(within(rows()[0]).queryByText(/^0 B$/)).toBeNull();
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it("retains stale-data and alarm warnings alongside the ordinal", async () => {
    override = (url) =>
      url.startsWith("/api/system/storage?")
        ? response({
            ...view(items),
            stale: true,
            availability: "unavailable",
            reason: "stale_sample",
          })
        : undefined;
    await start();
    expectRows(["synthetic-alpha", "synthetic-beta"]);
    expect(screen.getByText(/数据已过期，不代表当前状态/)).toBeVisible();
    rows().forEach((row) => {
      expect(within(row).getByText("数据过期，需重新确认")).toBeVisible();
      expect(within(row).queryByText("未达到告警阈值")).toBeNull();
    });
  });

  it.each(["menu", "operation"])(
    "does not read or reveal storage ordinals without %s permission",
    async (permission) => {
      menuReadable = permission !== "menu";
      storageReadable = permission !== "operation";
      render(<SystemWorkspace profile={profile} sessionChanged={vi.fn()} />);
      await screen.findByText(
        permission === "menu"
          ? "当前账户没有系统健康读取权限。"
          : "当前账户没有此项读取权限，请选择已获授权的分类。",
      );
      expect(screen.queryByRole("table")).toBeNull();
      expect(screen.queryByText("本页序号")).toBeNull();
      expect(calls()).toHaveLength(0);
      expect(calls("/storage/settings")).toHaveLength(0);
    },
  );

  it("keeps ordinal and identity visible to read-only users without adding an edit action", async () => {
    writeable = false;
    await start();
    expectRows(["synthetic-alpha", "synthetic-beta"]);
    rows().forEach((row) =>
      expect(within(row).getByText("只读")).toBeVisible(),
    );
    expect(screen.queryByRole("button", { name: /调整阈值/ })).toBeNull();
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it.each(["malformed", "forbidden", "unavailable", "network"])(
    "clears rows and ordinals on a %s refresh response",
    async (kind) => {
      await start();
      override = (url) => {
        if (!url.startsWith("/api/system/storage?")) return;
        if (kind === "network") return Promise.reject(new TypeError("offline"));
        if (kind === "malformed")
          return response({
            ...view(items),
            storage: [{ ...items[0], id: null }],
          });
        return response(
          {
            error: kind === "forbidden" ? "forbidden" : "database_unavailable",
          },
          kind === "forbidden" ? 403 : 503,
        );
      };
      click("刷新存储状态");
      await screen.findByRole("alert");
      expect(screen.queryByRole("table")).toBeNull();
      expect(screen.queryByText("本页序号")).toBeNull();
      expect(calls()).toHaveLength(2);
      expect(calls("/storage/settings")).toHaveLength(0);
    },
  );

  it("clears prior ordinals and invokes session revalidation on an expired session", async () => {
    const changed = vi.fn();
    await start(changed);
    override = (url) =>
      url.startsWith("/api/system/storage?")
        ? response({ error: "unauthenticated" }, 401)
        : undefined;
    click("刷新存储状态");
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("table")).toBeNull();
    expect(calls()).toHaveLength(2);
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it("aborts a superseded page read and ignores its late rows and ordinals", async () => {
    const pending = deferred();
    override = (url) =>
      url === "/api/system/storage?instance=local-api&page=1&pageSize=20"
        ? pending.promise
        : undefined;
    render(<SystemWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByLabelText("存储每页条数");
    await waitFor(() => expect(calls()).toHaveLength(1));
    const oldSignal = calls()[0][1].signal as AbortSignal;
    fill("存储每页条数", "10");
    await screen.findByRole("table", { name: "实际文件系统存储" });
    expectRows(["synthetic-alpha", "synthetic-beta"]);
    expect(oldSignal.aborted).toBe(true);
    await act(async () =>
      pending.resolve(await response(view([disk("synthetic-late")]))),
    );
    expectRows(["synthetic-alpha", "synthetic-beta"]);
    expect(screen.queryByText("synthetic-late")).toBeNull();
    expect(calls()).toHaveLength(2);
    expect(calls("/storage/settings")).toHaveLength(0);
  });

  it("submits the selected page-two resource ID and revision, never its ordinal", async () => {
    items = Array.from({ length: 22 }, (_, index) =>
      disk(`synthetic-target-${index + 1}`, index + 10),
    );
    const pending = deferred();
    override = (url) =>
      url === "/api/system/storage/settings" ? pending.promise : undefined;
    await start();
    click("下一页存储");
    await screen.findByText("共 22 条 · 第 2 页");
    expectRows(["synthetic-target-21", "synthetic-target-22"]);
    click("调整阈值 synthetic-target-22");
    expect(window.location.hash).toBe(
      "#system/storage/settings/synthetic-target-22",
    );
    expect(screen.getByText(/当前版本：31/)).toBeVisible();
    const form = submit();
    fireEvent.submit(form);
    expect(calls("/storage/settings")).toHaveLength(1);
    const [, init] = calls("/storage/settings")[0];
    expect(JSON.parse(init.body as string)).toEqual({
      instance: "local-api",
      storageId: "synthetic-target-22",
      setType: "alarm",
      percent: 89,
      expectedRevision: 31,
      actorPassword: "Synthetic Actor Password 123",
      totpCode: "123456",
    });
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(init.cache).toBe("no-store");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe(
      profile.csrfToken,
    );
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(screen.getByLabelText("未使用的认证器验证码")).toHaveValue("");
    expect(calls()).toHaveLength(2);
    items[21] = { ...items[21], alarmPercent: 89, revision: 32 };
    await act(async () =>
      pending.resolve(
        await response({
          result: 1,
          setting: {
            instance: "local-api",
            storageId: "synthetic-target-22",
            alarmPercent: 89,
            revision: 32,
            updatedAt: now,
          },
        }),
      ),
    );
    await screen.findByText(/存储阈值已由服务器保存/);
    expectRows(["synthetic-target-21", "synthetic-target-22"]);
    expect(within(rows()[1]).getByText("版本 32")).toBeVisible();
    expect(calls()).toHaveLength(3);
    expect(calls()[2][0]).toBe(calls()[1][0]);
    expect(calls("/storage/settings")).toHaveLength(1);
  });

  it("preserves CAS conflict handling and requires a reread before another edit", async () => {
    override = (url) =>
      url === "/api/system/storage/settings"
        ? response({ error: "revision_conflict" }, 409)
        : undefined;
    await start();
    click("调整阈值 synthetic-beta");
    const form = submit();
    await screen.findByText(/本次结果或版本尚未确认/);
    expect(screen.getByRole("button", { name: "保存告警阈值" })).toBeDisabled();
    fireEvent.submit(form);
    expect(calls("/storage/settings")).toHaveLength(1);
    expect(calls()).toHaveLength(1);
    items = [{ ...items[1], revision: 38 }, items[0]];
    click("重新读取存储设置");
    await screen.findByRole("table", { name: "实际文件系统存储" });
    expectRows(["synthetic-beta", "synthetic-alpha"]);
    click("调整阈值 synthetic-beta");
    expect(screen.getByText(/当前版本：38/)).toBeVisible();
    expect(screen.getByLabelText("操作者当前密码")).toHaveValue("");
    expect(calls()).toHaveLength(2);
    expect(calls("/storage/settings")).toHaveLength(1);
  });

  it("keeps the reread order after cancel and discards a late selected-row mutation", async () => {
    const pending = deferred();
    override = (url) =>
      url === "/api/system/storage/settings" ? pending.promise : undefined;
    await start();
    click("调整阈值 synthetic-beta");
    submit();
    const signal = calls("/storage/settings")[0][1].signal as AbortSignal;
    items = [items[1], items[0]];
    click("取消");
    await screen.findByRole("table", { name: "实际文件系统存储" });
    expectRows(["synthetic-beta", "synthetic-alpha"]);
    expect(signal.aborted).toBe(true);
    await act(async () =>
      pending.resolve(
        await response({
          result: 1,
          setting: {
            instance: "local-api",
            storageId: "synthetic-beta",
            alarmPercent: 89,
            revision: 38,
            updatedAt: now,
          },
        }),
      ),
    );
    expectRows(["synthetic-beta", "synthetic-alpha"]);
    expect(screen.queryByText(/存储阈值已由服务器保存/)).toBeNull();
    expect(screen.queryByLabelText("操作者当前密码")).toBeNull();
    expect(calls()).toHaveLength(2);
    expect(calls("/storage/settings")).toHaveLength(1);
  });
});
