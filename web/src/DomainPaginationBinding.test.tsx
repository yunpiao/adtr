// Actual API/Workspace with synthetic responses, not live domain acceptance.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import DomainsWorkspace from "./DomainsWorkspace";
import { domainAPI, domainOperations, type Connection } from "./domain-api";
import { discardDomainIntent } from "./domain-intent";

const profile: Profile = {
  ID: 314,
  username: "synthetic-page-reader",
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
  pwdUpdateTm: "2026-11-09T00:00:00Z",
  csrfToken: "synthetic-session",
};
const connection = (n = 0, prefix = "row"): Connection => ({
  domainId: `${prefix}-${n}`,
  domain: `${prefix}-${n}.invalid`,
  dcHostName: "dc.synthetic.invalid",
  ldapAddr: "192.0.2.1",
  port: "389",
  mode: "starttls",
  revision: "3",
  credentialRevision: "2",
  credentialConfigured: true,
  createdAt: "2026-11-09T00:00:00Z",
  updatedAt: "2026-11-09T00:00:00Z",
  connectionState: "unverified",
  lastDiagnostic: null,
  latestTaskUUID: "",
});
const page = (pageIdx = 1, pageSize = 20, total = 21, prefix = "row") => {
  const size = pageSize === -1 ? 1000 : pageSize;
  const offset = (pageIdx - 1) * size;
  const length = Math.max(0, Math.min(size, total - offset));
  return {
    page: { pageIdx, pageSize, total, totalPage: Math.ceil(total / size) },
    List: Array.from({ length }, (_, n) => connection(offset + n, prefix)),
    exhausted: offset + length >= total,
  };
};
const signal = () => new AbortController().signal;
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
const lists = () =>
  vi
    .mocked(fetch)
    .mock.calls.filter(([url]) => String(url).startsWith("/api/domains?"));
async function settle() {
  await act(async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  });
}
let reply: (url: string, init: RequestInit) => Promise<Response>;
beforeEach(() => {
  sessionStorage.clear();
  discardDomainIntent(profile);
  window.history.replaceState({}, "", "#account");
  reply = async () => response(page());
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (url === "/api/access/menu")
        return response({
          menu: [
            { mark: "domains", auth: { readable: true, writeable: false } },
          ],
        });
      if (url === "/api/access/check")
        return response({
          results: domainOperations.map((op) => op.startsWith("GET")),
        });
      return reply(url, init);
    }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  discardDomainIntent(profile);
});

const matching: [string, number, number, number][] = [
  ["", 1, 20, 0],
  ["", 1, 20, 21],
  ["pageIdx=2", 2, 20, 21],
  ["pageSize=7", 1, 7, 8],
  ["filterKeyword=literal", 1, 20, 1],
  ["pageIdx=1000000&pageSize=100", 1000000, 100, 0],
  ["page%49dx=%32&page%53ize=%32%30", 2, 20, 21],
  ["filterKeyword=pageIdx%3D8%26pageSize%3D50&pageIdx=2", 2, 20, 21],
  ...[1, 2, 7, 10, 20, 30, 40, 50, 99, 100].flatMap(
    (size) =>
      [
        [`pageIdx=1&pageSize=${size}`, 1, size, size + 1],
        [`pageIdx=2&pageSize=${size}`, 2, size, size + 1],
      ] as [string, number, number, number][],
  ),
];
const wrongQueries = [
  "pageIdx=2",
  "pageSize=10",
  "pageIdx=2&pageSize=10",
  "pageIdx=1000001",
  "pageSize=101",
  "pageIdx=0",
  "pageSize=0",
  "pageIdx=-1",
  "pageSize=-2",
  "pageIdx=",
  "pageSize=",
  "pageIdx=01",
  "pageSize=020",
  "pageIdx=+1",
  "pageSize=%2B20",
  "pageIdx=1.0",
  "pageSize=2e1",
  "pageIdx=1%20",
  "pageSize=%2020",
  "pageIdx=1&pageIdx=1",
  "pageSize=20&pageSize=20",
  "pageIdx=1&page%49dx=1",
  "pageSize=20&page%53ize=20",
  "pageIdx=1&pageIdx=2",
  "pageSize=-1",
];
describe("requested domain pages are bound without rewriting the query", () => {
  it.each(matching)(
    "accepts matching query %j",
    async (query, index, size, total) => {
      const data = page(index, size, total);
      reply = async () => response(data);
      await expect(domainAPI.list(query, signal())).resolves.toEqual(data);
      expect(lists()).toHaveLength(1);
      expect(lists()[0][0]).toBe(`/api/domains?${query}`);
      expect(lists()[0][1]).toMatchObject({
        method: "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: {},
      });
      expect(lists()[0][1]!.body).toBeUndefined();
    },
  );
  it.each(wrongQueries)(
    "rejects success for mismatched/noncanonical query %j",
    async (query) => {
      reply = async () => response(page());
      await expect(domainAPI.list(query, signal())).rejects.toMatchObject({
        code: "invalid_response",
      });
      expect(lists()).toHaveLength(1);
      expect(lists()[0][0]).toBe(`/api/domains?${query}`);
    },
  );
  it("keeps repeated domain/status filters and encoded literal text unchanged", async () => {
    const query =
      "filterDomain=a.invalid&filterDomain=b.invalid&filterStatus=unverified&filterStatus=error&filterKeyword=pageIdx%3D2&pageIdx=1&pageSize=20";
    await expect(domainAPI.list(query, signal())).resolves.toEqual(page());
    expect(lists()[0][0]).toBe(`/api/domains?${query}`);
  });
  it.each([
    { page: { ...page().page, pageIdx: 0 } },
    { page: { ...page().page, pageSize: 101 } },
    { page: { ...page().page, total: -1 } },
    { page: { ...page().page, totalPage: -1 } },
    { List: [{ ...connection(), domainId: "bad id" }] },
    { exhausted: "true" },
  ])("retains original invalid DTO rejection %#", async (patch) => {
    reply = async () => response({ ...page(), ...patch });
    await expect(domainAPI.list("", signal())).rejects.toMatchObject({
      code: "invalid_response",
    });
  });
  it.each([
    [400, "invalid_input"],
    [401, "unauthenticated"],
    [403, "password_change_required"],
    [403, "forbidden"],
    [404, "not_found"],
    [422, "result_too_large"],
    [503, "schema_unavailable"],
  ])(
    "preserves server error %s/%s before pagination validation",
    async (status, code) => {
      reply = async () => response({ error: code }, Number(status));
      await expect(domainAPI.list("pageIdx=2", signal())).rejects.toMatchObject(
        { code, status },
      );
    },
  );
  it("does not change malformed-error classification in this pagination slice", async () => {
    reply = async () => new Response("html", { status: 401 });
    await expect(domainAPI.list("pageIdx=2", signal())).rejects.toMatchObject({
      code: "invalid_response",
      status: 401,
    });
  });
  it("does not reinterpret a network failure as a page mismatch", async () => {
    reply = async () => {
      throw Error("synthetic offline");
    };
    await expect(domainAPI.list("pageIdx=2", signal())).rejects.toMatchObject({
      code: "network",
    });
  });
});

describe("existing bounded-all sentinel is supported, not expanded", () => {
  for (const query of [
    "pageSize=-1",
    "pageIdx=1&pageSize=-1",
    "page%53ize=%2D1&pageIdx=%31",
  ]) {
    it.each([0, 1, 1000])(
      `${query} accepts complete %s results`,
      async (total) => {
        const data = page(1, -1, total);
        reply = async () => response(data);
        await expect(domainAPI.list(query, signal())).resolves.toEqual(data);
        expect(lists()[0][0]).toBe(`/api/domains?${query}`);
      },
    );
  }
  it.each([
    ["pageIdx=2&pageSize=-1", page(2, -1, 0)],
    ["pageSize=-1", page(1, -1, 1001)],
    ["pageSize=-1", { ...page(1, -1, 1), exhausted: false }],
    ["pageSize=-1", { ...page(1, -1, 1), List: [] }],
    ["pageSize=-1", { ...page(1, -1, 0), List: [connection()] }],
    [
      "pageSize=-1",
      { ...page(1, -1, 1), page: { ...page(1, -1, 1).page, totalPage: 2 } },
    ],
    [
      "pageSize=-1",
      { ...page(1, -1, 0), page: { ...page(1, -1, 0).page, totalPage: 1 } },
    ],
    ["pageSize=-01", page(1, -1, 1)],
    ["pageSize=-1&pageSize=-1", page(1, -1, 1)],
    ["", page(1, -1, 1)],
  ] as [string, unknown][])(
    "rejects an invalid all result/request %#",
    async (query, data) => {
      reply = async () => response(data);
      await expect(domainAPI.list(query, signal())).rejects.toMatchObject({
        code: "invalid_response",
      });
    },
  );
});

describe("actual domain pagination recovers instead of accepting a wrong page", () => {
  async function mount() {
    render(<DomainsWorkspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByRole("table", { name: "获授权的域连接" });
  }
  const click = (name: string) =>
    fireEvent.click(screen.getByRole("button", { name }));
  it("wrong page is hidden; explicit refresh retrieves the requested page, then previous works", async () => {
    reply = async () => response(page(1, 20, 21, "first"));
    await mount();
    click("下一页");
    await settle();
    expect(
      screen.queryByRole("table", { name: "获授权的域连接" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("服务器结果无法确认");
    expect(lists()).toHaveLength(2);
    expect(String(lists()[1][0])).toContain("pageIdx=2");
    reply = async (url) => {
      const q = new URL(url, "http://synthetic.invalid").searchParams;
      return response(page(Number(q.get("pageIdx")), 20, 21, "restored"));
    };
    click("刷新域连接");
    await settle();
    expect(screen.getByText("restored-20.invalid")).toBeInTheDocument();
    expect(screen.getByText(/第 2 \/ 2 页/)).toBeInTheDocument();
    expect(lists()).toHaveLength(3);
    click("上一页");
    await settle();
    expect(screen.getByText("restored-0.invalid")).toBeInTheDocument();
    expect(lists()).toHaveLength(4);
  });
  it("page-size mismatch is hidden until a matching explicit refresh", async () => {
    await mount();
    fireEvent.change(screen.getByLabelText("每页条数", { exact: true }), {
      target: { value: "10" },
    });
    click("查询域连接");
    await settle();
    expect(
      screen.queryByRole("table", { name: "获授权的域连接" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("服务器结果无法确认");
    expect(String(lists().at(-1)![0])).toContain("pageSize=10");
    reply = async () => response(page(1, 10, 21));
    click("刷新域连接");
    await settle();
    expect(screen.getByText(/第 1 \/ 3 页/)).toBeInTheDocument();
    expect(
      (
        screen.getByLabelText("每页条数", { exact: true }) as HTMLSelectElement
      ).querySelector('option[value="-1"]'),
    ).toBeNull();
  });
  it("new page-size query wins over an old page response that ignores abort", async () => {
    await mount();
    let finish!: (r: Response) => void;
    reply = () =>
      new Promise((r) => {
        finish = r;
      });
    click("下一页");
    await settle();
    const stale = lists().at(-1)!;
    reply = async () => response(page(1, 10, 21, "new"));
    fireEvent.change(screen.getByLabelText("每页条数", { exact: true }), {
      target: { value: "10" },
    });
    click("查询域连接");
    await settle();
    expect(stale[1]?.signal?.aborted).toBe(true);
    finish(response(page(2, 20, 21, "late")));
    await settle();
    expect(screen.getByText("new-0.invalid")).toBeInTheDocument();
    expect(screen.queryByText("late-20.invalid")).not.toBeInTheDocument();
  });
});
