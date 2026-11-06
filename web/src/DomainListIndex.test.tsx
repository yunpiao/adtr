import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DomainsWorkspace from "./DomainsWorkspace";
import {
  domainOperations,
  type Connection,
  type DomainList,
} from "./domain-api";
import type { Profile } from "./api";

const profile: Profile = {
  ID: 1,
  username: "admin",
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
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "index-session",
};
const connection = (name: string): Connection => ({
  domainId: `opaque-${name}`,
  domain: `${name}.invalid`,
  dcHostName: `dc.${name}.invalid`,
  ldapAddr: "192.0.2.50",
  port: "389",
  mode: "starttls",
  revision: "1",
  credentialRevision: "1",
  credentialConfigured: true,
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:01Z",
  connectionState: "unverified",
  lastDiagnostic: null,
  latestTaskUUID: "",
});
const records = [connection("zeta"), connection("alpha")];
const list = (pageIdx = 1, pageSize = 20, items = records): DomainList => ({
  page: { pageIdx, pageSize, total: pageSize * 3, totalPage: 3 },
  List: items,
  exhausted: pageIdx >= 3,
});
const response = (body: unknown, status = 200) =>
  Promise.resolve({
    ok: status < 400,
    status,
    headers: new Headers(),
    json: async () => body,
  } as Response);
let fetcher: ReturnType<typeof vi.fn>;
let handler: (params: URLSearchParams, init: RequestInit) => Promise<Response>;
let allowed: boolean;
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const params = () =>
  new URL(
    fetcher.mock.calls
      .filter(([url]) => url.split("?")[0] === "/api/domains")
      .at(-1)![0],
    "http://localhost",
  ).searchParams;
const positions = () =>
  Array.from(document.querySelectorAll('td[data-label="序号"]')).map(
    (cell) => cell.textContent,
  );
const expectPositions = async (values: string[]) =>
  waitFor(() => expect(positions()).toEqual(values));
const start = () =>
  render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
function deferred() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
beforeEach(() => {
  sessionStorage.clear();
  window.history.replaceState({}, "", "#domains");
  allowed = true;
  handler = (p) =>
    response(list(Number(p.get("pageIdx")), Number(p.get("pageSize"))));
  fetcher = vi.fn((url: string, init: RequestInit) => {
    const parsed = new URL(url, "http://localhost");
    if (parsed.pathname === "/api/access/menu")
      return response({
        menu: [
          { mark: "domains", auth: { readable: allowed, writeable: false } },
        ],
      });
    if (parsed.pathname === "/api/access/check")
      return response({ results: domainOperations.map(() => allowed) });
    if (parsed.pathname === "/api/domains")
      return handler(parsed.searchParams, init);
    if (parsed.pathname === "/api/domains/detail")
      return response({
        connection: records.find(
          (c) => c.domainId === parsed.searchParams.get("domainId"),
        ),
      });
    throw new Error(`Unexpected ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
});

describe("domain list response page positions", () => {
  it("adds a scoped column immediately after domain and preserves response order and domain identity", async () => {
    start();
    await expectPositions(["1", "2"]);
    const headers = screen.getAllByRole("columnheader");
    expect(headers.slice(0, 3).map((h) => h.textContent)).toEqual([
      "域名",
      "序号",
      "配置的域控",
    ]);
    expect(headers[1]).toHaveAttribute("scope", "col");
    const rows = within(screen.getByRole("table", { name: "获授权的域连接" }))
      .getAllByRole("row")
      .slice(1);
    records.forEach((record, index) => {
      expect(
        within(rows[index]).getByText(record.domain, { exact: true }),
      ).toBeVisible();
      expect(
        within(rows[index]).getByText(`域 ID：${record.domainId}`),
      ).toBeVisible();
      expect(rows[index].children[1]).toHaveTextContent(String(index + 1));
      expect(within(rows[index]).getByRole("button")).toHaveAttribute(
        "data-domain-id",
        record.domainId,
      );
    });
  });
  it.each([10, 20, 30, 40, 50])(
    "uses applied size %i across next and previous pages, not draft size",
    async (size) => {
      start();
      await expectPositions(["1", "2"]);
      fill("每页条数", String(size));
      click("查询域连接");
      await waitFor(() => expect(params().get("pageSize")).toBe(String(size)));
      await expectPositions(["1", "2"]);
      click("下一页");
      await expectPositions([String(size + 1), String(size + 2)]);
      const count = fetcher.mock.calls.length;
      fill("每页条数", size === 50 ? "10" : "50");
      fill("域或域控关键词", "draft");
      expect(positions()).toEqual([String(size + 1), String(size + 2)]);
      expect(fetcher.mock.calls).toHaveLength(count);
      click("上一页");
      await expectPositions(["1", "2"]);
      expect(params().get("pageSize")).toBe(String(size));
      expect(params().get("filterKeyword")).toBeNull();
    },
  );
  it("uses actual response page and size even when different from requested and draft metadata", async () => {
    handler = () => response(list(3, 30));
    start();
    await expectPositions(["61", "62"]);
    expect(params().get("pageIdx")).toBe("1");
    expect(params().get("pageSize")).toBe("20");
    fill("每页条数", "50");
    expect(positions()).toEqual(["61", "62"]);
  });
  it("restarts positions on submitted filters, sort and reset, without locally sorting the response", async () => {
    handler = (p) =>
      response(
        list(
          Number(p.get("pageIdx")),
          Number(p.get("pageSize")),
          p.get("tmSort") === "1" ? [...records].reverse() : records,
        ),
      );
    start();
    await expectPositions(["1", "2"]);
    click("下一页");
    await expectPositions(["21", "22"]);
    fill("域或域控关键词", "alpha");
    fill("域名筛选", "alpha.invalid");
    fireEvent.click(screen.getByRole("checkbox", { name: "已保存，未验证" }));
    fill("创建时间排序", "1");
    click("查询域连接");
    await expectPositions(["1", "2"]);
    expect(params().get("filterKeyword")).toBe("alpha");
    expect(params().getAll("filterDomain")).toEqual(["alpha.invalid"]);
    expect(params().getAll("filterStatus")).toEqual(["unverified"]);
    expect(params().get("tmSort")).toBe("1");
    expect(
      screen.getAllByRole("button", { name: /^查看 / })[0],
    ).toHaveAttribute("data-domain-id", records[1].domainId);
    click("下一页");
    await expectPositions(["21", "22"]);
    click("重置筛选");
    await expectPositions(["1", "2"]);
    expect(params().get("tmSort")).toBe("-1");
    expect(params().get("filterKeyword")).toBeNull();
  });
  it("opens details and restores focus by domain ID when a refresh reorders positions", async () => {
    start();
    await expectPositions(["1", "2"]);
    click("下一页");
    await expectPositions(["21", "22"]);
    click("查看 alpha.invalid");
    await screen.findByRole("heading", { name: "域连接详情：alpha.invalid" });
    const call = fetcher.mock.calls.find(([url]) =>
      url.startsWith("/api/domains/detail?"),
    )!;
    expect(
      new URL(call[0], "http://localhost").searchParams.get("domainId"),
    ).toBe(records[1].domainId);
    handler = (p) =>
      response(
        list(
          Number(p.get("pageIdx")),
          Number(p.get("pageSize")),
          [...records].reverse(),
        ),
      );
    click("返回域连接列表");
    await expectPositions(["21", "22"]);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "查看 alpha.invalid" }),
      ).toHaveFocus(),
    );
    expect(
      screen
        .getByRole("button", { name: "查看 alpha.invalid" })
        .closest("tr")
        ?.querySelector('[data-label="序号"]'),
    ).toHaveTextContent("21");
  });
  it("shows no index for empty results", async () => {
    handler = () =>
      response({
        page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
        List: [],
        exhausted: true,
      });
    start();
    await screen.findByText(/当前筛选下没有获授权/);
    expect(positions()).toEqual([]);
    expect(screen.queryByRole("columnheader", { name: "序号" })).toBeNull();
  });
  it.each([403, 500])(
    "clears indexed rows after a %i list failure",
    async (status) => {
      start();
      await expectPositions(["1", "2"]);
      handler = () =>
        response(
          { error: status === 403 ? "forbidden" : "internal_error" },
          status,
        );
      click("刷新域连接");
      await screen.findByRole("alert");
      expect(positions()).toEqual([]);
    },
  );
  it("does not request or number domains without list permission", async () => {
    allowed = false;
    start();
    await screen.findByText("服务器未授予域连接读取权限。");
    expect(positions()).toEqual([]);
    expect(
      fetcher.mock.calls.some(([url]) => url.startsWith("/api/domains?")),
    ).toBe(false);
  });
  it("ignores an older page response after a newer query completed", async () => {
    start();
    await expectPositions(["1", "2"]);
    const old = deferred();
    handler = (p) =>
      p.get("pageIdx") === "2"
        ? old.promise
        : response(list(1, 20, [records[1]]));
    click("下一页");
    await waitFor(() => expect(params().get("pageIdx")).toBe("2"));
    expect(positions()).toEqual([]);
    fill("域或域控关键词", "alpha");
    click("查询域连接");
    await expectPositions(["1"]);
    await act(async () => old.resolve(await response(list(2, 20))));
    expect(positions()).toEqual(["1"]);
    expect(
      screen.queryByRole("button", { name: "查看 zeta.invalid" }),
    ).toBeNull();
  });
  it.each(["identity", "unmount"])(
    "cannot restore pending indexed rows after %s changes",
    async (change) => {
      const old = deferred();
      let signal: AbortSignal | undefined;
      handler = (_p, init) => {
        signal = init.signal as AbortSignal;
        return old.promise;
      };
      const view = start();
      await waitFor(() => expect(signal).toBeDefined());
      if (change === "unmount") view.unmount();
      else {
        handler = () => response(list(1, 20, [records[1]]));
        view.rerender(
          <DomainsWorkspace
            profile={{ ...profile, ID: 2, csrfToken: "new-session" }}
            sessionChanged={vi.fn()}
          />,
        );
        await expectPositions(["1"]);
      }
      expect(signal!.aborted).toBe(true);
      await act(async () => old.resolve(await response(list(3, 50))));
      expect(positions()).toEqual(change === "unmount" ? [] : ["1"]);
      expect(
        screen.queryByRole("button", { name: "查看 zeta.invalid" }),
      ).toBeNull();
    },
  );
});
