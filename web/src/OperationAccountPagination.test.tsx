import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import OperationAccountsWorkspace from "./OperationAccountsWorkspace";
import { labels, marks } from "./access-api";
import type { Profile } from "./api";
import {
  operationAccountAPI,
  type OperationAccount,
} from "./operation-account-api";
import { discardOperationAccountIntent } from "./operation-account-intent";

const base = "/api/operation-accounts";
const account = (index: number): OperationAccount => ({
  accountId: `synthetic-account-${index}`,
  domainId: `synthetic-domain-${index}`,
  domain: `synthetic-${index}.invalid`,
  label: `Synthetic account ${index}`,
  revision: "1",
  credentialRevision: "1",
  credentialConfigured: true,
  createdAt: "2026-10-07T00:00:00Z",
  updatedAt: "2026-10-07T00:00:00Z",
  storageState: "saved",
  verificationState: "unverified",
});
const result = (
  kind: "list" | "domains",
  pageIdx = 1,
  pageSize = 20,
  total = 1,
) => {
  const size = pageSize === -1 ? Math.max(total, 1) : pageSize;
  const offset = (pageIdx - 1) * size;
  const items = Array.from(
    { length: Math.min(size, Math.max(0, total - offset)) },
    (_, index) => account(offset + index),
  );
  const totalPage = Math.ceil(total / size);
  return {
    page: { pageIdx, pageSize, total, totalPage },
    ...(kind === "list"
      ? { List: items }
      : {
          domains: items.map(({ domainId, domain, revision }) => ({
            domainId,
            domain,
            revision,
          })),
        }),
    exhausted: pageIdx >= totalPage,
  };
};
const response = (value: unknown, status = 200) =>
  Promise.resolve({
    ok: status < 400,
    status,
    json: async () => value,
  } as Response);
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
  csrfToken: "synthetic-pagination-session",
};
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  discardOperationAccountIntent();
  sessionStorage.clear();
  localStorage.clear();
  window.history.replaceState({}, "", "#account");
  fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => vi.unstubAllGlobals());

describe.each(["list", "domains"] as const)(
  "operation-account %s page binding",
  (kind) => {
    const read = (query: string) =>
      operationAccountAPI[kind](query, new AbortController().signal);
    it.each([
      ["", 1, 20, 0],
      [
        kind === "list"
          ? "filterKeyword=synthetic&filterDomain=a.invalid&filterDomain=b.invalid"
          : "filterKeyword=synthetic",
        1,
        20,
        1,
      ],
      ["pageIdx=2", 2, 20, 21],
      ["pageSize=10", 1, 10, 11],
      ["pageIdx=1000000&pageSize=100", 1000000, 100, 1],
      ["pageIdx=1&pageSize=-1", 1, -1, 1000],
      ["pageSize=-1", 1, -1, 0],
      ["page%49dx=%32&pageSize=%31", 2, 1, 2],
    ])(
      "accepts matching request %s",
      async (query, pageIdx, pageSize, total) => {
        const value = result(kind, pageIdx, pageSize, total);
        fetcher.mockImplementation(() => response(value));
        await expect(read(query)).resolves.toEqual(value);
        expect(fetcher).toHaveBeenCalledWith(
          `${base}${kind === "domains" ? "/domains" : ""}?${query}`,
          expect.objectContaining({
            method: "GET",
            credentials: "same-origin",
            cache: "no-store",
          }),
        );
      },
    );
    it.each([
      ["pageIdx=2", 1, 20],
      ["pageIdx=1", 2, 20],
      ["pageSize=10", 1, 20],
      ["pageSize=-1", 1, 20],
      ["pageSize=20", 1, -1],
      ["", 2, 20],
      ["", 1, 10],
      ["pageSize=10", 2, 10],
      ["pageIdx=2", 2, 10],
    ])(
      "rejects self-consistent wrong page for %s (%i/%i)",
      async (query, pageIdx, pageSize) => {
        fetcher.mockImplementation(() =>
          response(result(kind, pageIdx, pageSize)),
        );
        await expect(read(query)).rejects.toMatchObject({
          code: "invalid_response",
        });
      },
    );
    it.each([
      "pageIdx=",
      "pageSize=",
      "pageIdx=01",
      "pageSize=020",
      "pageIdx=%2B1",
      "pageSize=%2B20",
      "pageIdx=1.0",
      "pageSize=2e1",
      "pageIdx=0",
      "pageIdx=-1",
      "pageIdx=1000001",
      "pageSize=0",
      "pageSize=101",
      "pageIdx=9007199254740993",
      "pageIdx=NaN",
      "pageSize=Infinity",
      "pageIdx=1&pageIdx=1",
      "pageIdx=1&page%49dx=2",
      "pageSize=20&pageSize=20",
    ])(
      "does not accept success for noncanonical/ambiguous query %s",
      async (query) => {
        fetcher.mockImplementation(() => response(result(kind)));
        await expect(read(query)).rejects.toMatchObject({
          code: "invalid_response",
        });
      },
    );
    it.each([
      [400, "invalid_argument", "pageIdx=bad"],
      [401, "unauthorized", ""],
      [403, "forbidden", "pageIdx=2"],
      [422, "result_too_large", "pageSize=-1"],
    ])("preserves server error %i/%s", async (status, code, query) => {
      fetcher.mockImplementation(() => response({ error: code }, status));
      await expect(read(query)).rejects.toMatchObject({ code, status });
    });
    it("retains row-count, total-page, exhaustion and bounded-all checks", async () => {
      const valid = result(kind);
      for (const value of [
        {
          ...valid,
          page: { ...valid.page, total: 21, totalPage: 2 },
          exhausted: false,
        },
        { ...valid, page: { ...valid.page, totalPage: 2 } },
        { ...valid, exhausted: false },
      ]) {
        fetcher.mockImplementation(() => response(value));
        await expect(read("")).rejects.toMatchObject({
          code: "invalid_response",
        });
      }
      fetcher.mockImplementation(() => response(result(kind, 1, -1, 1001)));
      await expect(read("pageSize=-1")).rejects.toMatchObject({
        code: "invalid_response",
      });
      fetcher.mockImplementation(() => response(result(kind, 2, -1, 1)));
      await expect(read("pageIdx=2&pageSize=-1")).rejects.toMatchObject({
        code: "invalid_response",
      });
    });
  },
);

describe.each(["list", "domains"] as const)(
  "operation-account %s page recovery UI",
  (kind) => {
    it("hides a wrong-page result and retries the requested page without mutation", async () => {
      let correctSecondPage = false;
      const readPath = `${base}${kind === "domains" ? "/domains" : ""}`;
      fetcher.mockImplementation((url: string, init: RequestInit) => {
        const parsed = new URL(url, "http://localhost");
        if (parsed.pathname === "/api/access/menu")
          return response({
            menu: marks.map((mark) => ({
              mark,
              name: labels[mark],
              auth: { readable: true, writeable: true },
              allow_auth: { readable: true, writeable: true },
              children: [],
              paths: [],
              checked: true,
              icon: mark,
            })),
          });
        if (parsed.pathname === "/api/access/check")
          return response({
            results: JSON.parse(init.body as string).paths.map(() => true),
          });
        if (parsed.pathname === readPath) {
          const requested = Number(parsed.searchParams.get("pageIdx") ?? "1");
          return response(
            result(kind, requested === 2 && correctSecondPage ? 2 : 1, 20, 21),
          );
        }
        if (parsed.pathname === base) return response(result("list"));
        throw new Error(`Unexpected test request ${parsed.pathname}`);
      });
      render(
        <OperationAccountsWorkspace
          profile={profile}
          sessionChanged={vi.fn()}
        />,
      );
      await screen.findByRole("table", { name: "获授权的操作账户" });
      if (kind === "domains") {
        fireEvent.click(screen.getByRole("button", { name: "新增操作账户" }));
        await screen.findByRole("button", { name: "选择 synthetic-0.invalid" });
      }
      fireEvent.click(screen.getByRole("button", { name: "下一页" }));
      await waitFor(() =>
        expect(screen.getByRole("alert")).toHaveTextContent(
          "服务器结果无法安全确认",
        ),
      );
      expect(
        screen.queryByRole("button", {
          name:
            kind === "list"
              ? "查看 Synthetic account 0"
              : "选择 synthetic-0.invalid",
        }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "下一页" }),
      ).not.toBeInTheDocument();
      correctSecondPage = true;
      fireEvent.click(
        screen.getByRole("button", {
          name: kind === "list" ? "刷新操作账户" : "刷新可选域",
        }),
      );
      await screen.findByRole("button", {
        name:
          kind === "list"
            ? "查看 Synthetic account 20"
            : "选择 synthetic-20.invalid",
      });
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "上一页" })).toBeEnabled();
      const reads = fetcher.mock.calls.filter(
        ([url]) => url.split("?")[0] === readPath,
      );
      expect(
        reads.map(([url]) =>
          new URL(url, "http://localhost").searchParams.get("pageIdx"),
        ),
      ).toEqual(["1", "2", "2"]);
      expect(
        fetcher.mock.calls.filter(
          ([url, init]) => url.startsWith(base) && init.method !== "GET",
        ),
      ).toHaveLength(0);
    });
  },
);
