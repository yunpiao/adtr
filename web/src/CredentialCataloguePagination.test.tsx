// Synthetic response/DOM evidence for all four existing governance purposes.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import {
  credentialUseAPI,
  credentialUseOperations,
} from "./credential-use-api";
import {
  directoryCredentialUseAPI,
  directoryCredentialUseOperations,
} from "./directory-credential-use-api";
import {
  directoryV2CredentialUseAPI,
  directoryV2CredentialUseOperations,
} from "./directory-v2-credential-use-api";
import {
  directoryV3CredentialUseAPI,
  directoryV3CredentialUseOperations,
} from "./directory-v3-credential-use-api";
import CredentialUseWorkspace from "./CredentialUseWorkspace";
import DirectoryCredentialUseWorkspace from "./DirectoryCredentialUseWorkspace";
import DirectoryV2CredentialUseWorkspace from "./DirectoryV2CredentialUseWorkspace";
import DirectoryV3CredentialUseWorkspace from "./DirectoryV3CredentialUseWorkspace";

const variants = [
  {
    name: "connection",
    api: credentialUseAPI,
    operations: credentialUseOperations,
    Workspace: CredentialUseWorkspace,
    base: "/api/credential-use",
    refresh: "刷新授权清理目录",
  },
  {
    name: "directory-v1",
    api: directoryCredentialUseAPI,
    operations: directoryCredentialUseOperations,
    Workspace: DirectoryCredentialUseWorkspace,
    base: "/api/directory-credential-use",
    refresh: "刷新目录读取授权清理目录",
  },
  {
    name: "directory-v2",
    api: directoryV2CredentialUseAPI,
    operations: directoryV2CredentialUseOperations,
    Workspace: DirectoryV2CredentialUseWorkspace,
    base: "/api/directory-credential-use/v2",
    refresh: "刷新目录读取授权清理目录",
  },
  {
    name: "directory-v3",
    api: directoryV3CredentialUseAPI,
    operations: directoryV3CredentialUseOperations,
    Workspace: DirectoryV3CredentialUseWorkspace,
    base: "/api/directory-credential-use/v3",
    refresh: "刷新目录读取授权清理目录",
  },
] as const;
const profile: Profile = {
  ID: 311,
  username: "synthetic-page-admin",
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
  pwdUpdateTm: "2026-11-01T00:00:00Z",
  csrfToken: "synthetic-page-csrf",
};
const signal = () => new AbortController().signal;
const account = (n: number, label: string) => ({
  accountId: `synthetic-account-${n}`,
  domainId: "synthetic-domain",
  domain: "synthetic.invalid",
  label,
  revision: "9007199254740993",
  credentialRevision: "2",
});
const page = (pageIdx = 1, pageSize = 20, total = 21, label = "PageOne") => ({
  page: { pageIdx, pageSize, total, totalPage: Math.ceil(total / pageSize) },
  List: Array.from(
    {
      length: Math.min(pageSize, Math.max(0, total - (pageIdx - 1) * pageSize)),
    },
    (_, n) => account(n, `${label}-${n}`),
  ),
  exhausted: pageIdx >= Math.ceil(total / pageSize),
  consumerEnabled: false,
});
const response = (data: unknown, status = 200, actor = String(profile.ID)) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
  });
beforeEach(() => {
  sessionStorage.clear();
  window.history.replaceState({}, "", "#credential-use");
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

for (const variant of variants) {
  const { api, name, Workspace, base, operations, refresh } = variant;
  describe(`${name} cleanup pagination binding`, () => {
    const matching: [string, number, number, number][] = [
      ["", 1, 20, 0],
      ["", 1, 20, 21],
      ["keyword=synthetic", 1, 20, 1],
      ["pageIdx=2", 2, 20, 21],
      ["pageSize=30", 1, 30, 31],
      ["pageIdx=1000000&pageSize=50", 1000000, 50, 0],
      ["pageIdx=1000000&pageSize=20", 1000000, 20, 20000000],
      ["page%49dx=%32&page%53ize=%32%30", 2, 20, 21],
      ["pageSize=20&keyword=pageIdx%3D9%26pageSize%3D50&pageIdx=2", 2, 20, 21],
      ...[10, 20, 30, 40, 50].flatMap(
        (size) =>
          [
            [`pageIdx=1&pageSize=${size}`, 1, size, size + 1],
            [`pageIdx=2&pageSize=${size}`, 2, size, size + 1],
          ] as [string, number, number, number][],
      ),
    ];
    it.each(matching)(
      "accepts exact query %s",
      async (query, index, size, total) => {
        const value = page(index, size, total);
        const fetcher = vi.fn(async (_url: string, _init: RequestInit) =>
          response(value),
        );
        vi.stubGlobal("fetch", fetcher);
        await expect(
          api.accounts(query, profile.ID, signal()),
        ).resolves.toEqual(value);
        expect(fetcher).toHaveBeenCalledTimes(1);
        expect(fetcher.mock.calls[0][0]).toBe(`${base}/accounts?${query}`);
        expect(fetcher.mock.calls[0][1]).toMatchObject({
          method: "GET",
          credentials: "same-origin",
          cache: "no-store",
          redirect: "error",
        });
        expect(fetcher.mock.calls[0][1].body).toBeUndefined();
      },
    );
    const mismatches: [string, number, number, number][] = [
      ["pageIdx=2&pageSize=20", 1, 20, 21],
      ["pageIdx=1&pageSize=20", 2, 20, 21],
      ["pageIdx=1&pageSize=10", 1, 20, 1],
      ["pageIdx=1&pageSize=50", 1, 40, 41],
      ["", 2, 20, 0],
      ["", 1, 30, 1],
      ["pageIdx=1000000&pageSize=50", 999999, 50, 0],
    ];
    it.each(mismatches)(
      "rejects self-consistent response not belonging to %s",
      async (query, index, size, total) => {
        const fetcher = vi.fn(async () => response(page(index, size, total)));
        vi.stubGlobal("fetch", fetcher);
        await expect(
          api.accounts(query, profile.ID, signal()),
        ).rejects.toMatchObject({ code: "invalid_response" });
        expect(fetcher).toHaveBeenCalledTimes(1);
      },
    );
    it.each([
      "pageIdx=1&pageIdx=1",
      "pageIdx=1&page%49dx=1",
      "pageSize=20&pageSize=20",
      "pageIdx=1&pageIdx=2",
      "pageSize=20&pageSize=50",
      "pageIdx=01",
      "pageIdx=+1",
      "pageIdx=%2B1",
      "pageIdx=1.0",
      "pageIdx=1e0",
      "pageIdx=",
      "pageIdx",
      "pageIdx=0",
      "pageIdx=-1",
      "pageIdx=1000001",
      "pageSize=020",
      "pageSize=20.0",
      "pageSize=2e1",
      "pageSize=",
      "pageSize=0",
      "pageSize=-1",
      "pageSize=100",
    ])(
      "rejects forged success for ambiguous/noncanonical page input %s",
      async (query) => {
        vi.stubGlobal(
          "fetch",
          vi.fn(async () => response(page(1, 20, 1))),
        );
        await expect(
          api.accounts(query, profile.ID, signal()),
        ).rejects.toMatchObject({ code: "invalid_response" });
      },
    );
    it.each([
      [
        "wrong total pages",
        (v: ReturnType<typeof page>) => {
          v.page.totalPage = 2;
        },
      ],
      [
        "wrong exhausted",
        (v: ReturnType<typeof page>) => {
          v.exhausted = false;
        },
      ],
      [
        "wrong row count",
        (v: ReturnType<typeof page>) => {
          v.List = [];
        },
      ],
      [
        "duplicate account",
        (v: ReturnType<typeof page>) => {
          v.page.total = 2;
          v.List.push(v.List[0]);
        },
      ],
      [
        "invalid account revision",
        (v: ReturnType<typeof page>) => {
          v.List[0].credentialRevision = "9007199254740994";
        },
      ],
    ] as const)("keeps existing %s rejection", async (_name, corrupt) => {
      const value = page(1, 20, 1);
      corrupt(value);
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response(value)),
      );
      await expect(
        api.accounts("", profile.ID, signal()),
      ).rejects.toMatchObject({ code: "invalid_response" });
    });
    it("retains actor and HTTP errors rather than treating them as page mismatch", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response(page(), 200, "312")),
      );
      await expect(
        api.accounts("pageIdx=2", profile.ID, signal()),
      ).rejects.toMatchObject({
        code: "credential_use_identity_changed",
        status: 401,
      });
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response({ error: "forbidden" }, 403)),
      );
      await expect(
        api.accounts("pageIdx=2", profile.ID, signal()),
      ).rejects.toMatchObject({ code: "forbidden", status: 403 });
    });

    function mount(
      respond: (url: string, init: RequestInit) => Response | Promise<Response>,
    ) {
      const changed = vi.fn();
      const fetcher = vi.fn(async (url: string, init: RequestInit) => {
        if (url === "/api/access/menu")
          return response({
            menu: [
              { mark: "operation_accounts", auth: { readable: true } },
              { mark: "domains", auth: { readable: true } },
              { mark: "directory_assets", auth: { readable: true } },
            ],
          });
        if (url === "/api/access/check")
          return response({ results: operations.map(() => true) });
        if (url.startsWith(`${base}/accounts?`)) return respond(url, init);
        throw Error("Unexpected synthetic request");
      });
      vi.stubGlobal("fetch", fetcher);
      render(<Workspace profile={profile} sessionChanged={changed} />);
      return { fetcher, changed };
    }
    const catalogueCalls = (fetcher: ReturnType<typeof vi.fn>) =>
      fetcher.mock.calls.filter(([u]) =>
        String(u).startsWith(`${base}/accounts?`),
      );
    const noWrites = (fetcher: ReturnType<typeof vi.fn>) =>
      expect(
        fetcher.mock.calls.filter(
          ([u, init]) => init.method !== "GET" && u !== "/api/access/check",
        ),
      ).toHaveLength(0);

    it("wrong page is hidden and explicit refresh recovers page two without stuck pagination", async () => {
      let wrong = true;
      const { fetcher, changed } = mount((url) => {
        const p = Number(
          new URL(url, "https://synthetic.invalid").searchParams.get("pageIdx"),
        );
        return response(
          page(
            p === 2 && !wrong ? 2 : 1,
            20,
            21,
            p === 2 && !wrong ? "PageTwo" : "PageOne",
          ),
        );
      });
      await screen.findByText("PageOne-0");
      fireEvent.click(screen.getByRole("button", { name: "下一页" }));
      await screen.findByRole("alert");
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
      expect(screen.queryByText("PageOne-0")).not.toBeInTheDocument();
      wrong = false;
      fireEvent.click(screen.getByRole("button", { name: refresh }));
      await screen.findByText("PageTwo-0");
      expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
      expect(screen.getByText(/第 2 \/ 2/)).toBeVisible();
      fireEvent.click(screen.getByRole("button", { name: "上一页" }));
      await screen.findByText("PageOne-0");
      expect(
        catalogueCalls(fetcher).map(([u]) =>
          new URL(String(u), "https://synthetic.invalid").searchParams.get(
            "pageIdx",
          ),
        ),
      ).toEqual(["1", "2", "2", "1"]);
      noWrites(fetcher);
      expect(changed).not.toHaveBeenCalled();
    });
    it("changed page size rejects old-size data and recovers with the requested size", async () => {
      let wrong = true;
      const { fetcher } = mount((url) => {
        const size = Number(
          new URL(url, "https://synthetic.invalid").searchParams.get(
            "pageSize",
          ),
        );
        return response(page(1, size === 30 && wrong ? 20 : size, 1, "Size"));
      });
      await screen.findByText("Size-0");
      fireEvent.change(screen.getByLabelText("清理目录每页条数"), {
        target: { value: "30" },
      });
      await screen.findByRole("alert");
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
      wrong = false;
      fireEvent.click(screen.getByRole("button", { name: refresh }));
      await screen.findByText("Size-0");
      expect(screen.getByLabelText("清理目录每页条数")).toHaveValue("30");
      expect(
        catalogueCalls(fetcher).map(([u]) =>
          new URL(String(u), "https://synthetic.invalid").searchParams.get(
            "pageSize",
          ),
        ),
      ).toEqual(["20", "30", "30"]);
      noWrites(fetcher);
    });
    it("late valid page two cannot replace a newer size-reset result", async () => {
      let finish!: (r: Response) => void;
      const pending = new Promise<Response>((r) => {
        finish = r;
      });
      const { fetcher, changed } = mount((url) => {
        const q = new URL(url, "https://synthetic.invalid").searchParams;
        return q.get("pageIdx") === "2"
          ? pending
          : response(
              page(
                1,
                Number(q.get("pageSize")),
                21,
                q.get("pageSize") === "10" ? "Current" : "Original",
              ),
            );
      });
      await screen.findByText("Original-0");
      fireEvent.click(screen.getByRole("button", { name: "下一页" }));
      fireEvent.change(screen.getByLabelText("清理目录每页条数"), {
        target: { value: "10" },
      });
      await screen.findByText("Current-0");
      await act(async () => {
        finish(response(page(2, 20, 21, "Late")));
        await pending;
      });
      expect(screen.queryByText("Late-0")).not.toBeInTheDocument();
      expect(screen.getByText("Current-0")).toBeVisible();
      noWrites(fetcher);
      expect(changed).not.toHaveBeenCalled();
    });
  });
}
