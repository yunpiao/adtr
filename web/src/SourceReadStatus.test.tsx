// Actual API/React component with synthetic transport; no directory or DB access.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import SavedSourcePicker from "./SavedSourcePicker";
import {
  sourceAPI,
  type SourceChoice,
  type SourceQuery,
} from "./domain-selection-api";

const user = 310;
const choice = (revision = "9"): SourceChoice => ({
  domainId: "synthetic-source",
  domain: "synthetic.invalid",
  revision,
  credentialRevision: "12",
  dcHostName: "dc.synthetic.invalid",
  ldapAddr: "",
  port: "636",
  mode: "ldaps",
  credentialConfigured: true,
  connectionState: "unverified",
  source: "configured_connection",
  lastTest: null,
});
const query: SourceQuery = {
  pageIdx: 1,
  pageSize: 20,
  keyword: "",
  observationState: "",
};
const list = (revision = "9") => ({
  page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
  List: [choice(revision)],
  exhausted: true,
});
const resolved = () => ({
  selection: choice(),
  checkedAt: "2026-11-09T00:00:00Z",
});
const response = (value: unknown, status = 200, actor = String(user)) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
  });
const signal = () => new AbortController().signal;
const failures = [
  [401, "unauthenticated"],
  [403, "forbidden"],
  [404, "not_found"],
] as const;
const reads = [
  ["list", () => sourceAPI.list(query, signal(), user)],
  ["resolve", () => sourceAPI.resolve(choice(), signal(), user)],
] as const;
const bodies = [
  "",
  "<html>synthetic proxy failure</html>",
  '{"error":',
  "null",
  "[]",
  "{}",
  '{"error":14}',
  '{"error":"private_unrecognized"}',
  '{"error":"forbidden","extra":"not trusted"}',
];
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("configured source reads preserve known denial semantics", () => {
  for (const [name, read] of reads) {
    for (const [status, code] of failures) {
      it.each(bodies)(
        `${name}: ${status} unreadable or unrecognized body %j`,
        async (body) => {
          const fetcher = vi.fn(
            async (_url: string, _init: RequestInit) =>
              new Response(body, { status }),
          );
          vi.stubGlobal("fetch", fetcher);
          await expect(read()).rejects.toMatchObject({
            code:
              body === "" || body.startsWith("<html>") || body === '{"error":'
                ? code
                : "internal",
            status,
          });
          expect(fetcher).toHaveBeenCalledTimes(1);
          expect(fetcher.mock.calls[0][1]).toMatchObject({
            method: "GET",
            credentials: "same-origin",
            cache: "no-store",
          });
          expect(fetcher.mock.calls[0][1].body).toBeUndefined();
          expect(fetcher.mock.calls[0][1].headers).toBeUndefined();
        },
      );
    }
    it.each([
      [401, "unauthenticated"],
      [403, "password_change_required"],
      [403, "forbidden"],
      [403, "tenant_not_configured"],
      [403, "tenant_expired"],
      [403, "tenant_domain_limit_exceeded"],
      [404, "not_found"],
      [409, "selection_changed"],
      [422, "unsupported_source_filter"],
      [503, "schema_unavailable"],
    ])(`${name}: preserves recognized %s/%s`, async (status, code) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response({ error: code }, Number(status))),
      );
      await expect(read()).rejects.toMatchObject({ code, status });
    });
    it.each(failures)(
      `${name}: body read rejection retains %s`,
      async (status, code) => {
        const json = vi.fn(async () => {
          throw Error("synthetic stream interruption");
        });
        vi.stubGlobal(
          "fetch",
          vi.fn(
            async () => ({ ok: false, status, json }) as unknown as Response,
          ),
        );
        await expect(read()).rejects.toMatchObject({ code, status });
        expect(json).toHaveBeenCalledTimes(1);
      },
    );
    it.each([400, 409, 422, 429, 500, 503])(
      `${name}: unrelated malformed %s remains invalid`,
      async (status) => {
        vi.stubGlobal(
          "fetch",
          vi.fn(async () => new Response("<html>unknown</html>", { status })),
        );
        await expect(read()).rejects.toMatchObject({
          code: "invalid_response",
          status: 0,
        });
      },
    );
    it(`${name}: malformed successful data remains invalid`, async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async () =>
            new Response("{", {
              status: 200,
              headers: { "X-ADTR-User-ID": String(user) },
            }),
        ),
      );
      await expect(read()).rejects.toMatchObject({
        code: "invalid_response",
        status: 0,
      });
    });
    it.each([null, "999"])(
      `${name}: successful actor header %s is checked before reading`,
      async (actor) => {
        const json = vi.fn();
        vi.stubGlobal(
          "fetch",
          vi.fn(
            async () =>
              ({
                ok: true,
                status: 200,
                headers: new Headers(
                  actor === null ? {} : { "X-ADTR-User-ID": actor },
                ),
                json,
              }) as unknown as Response,
          ),
        );
        await expect(read()).rejects.toMatchObject({
          code: "unauthenticated",
          status: 401,
        });
        expect(json).not.toHaveBeenCalled();
      },
    );
  }
  it("both valid success paths still return the exact validated content", async () => {
    const fetcher = vi.fn(async (url: string) =>
      response(url.includes("/resolve?") ? resolved() : list()),
    );
    vi.stubGlobal("fetch", fetcher);
    await expect(sourceAPI.list(query, signal(), user)).resolves.toEqual(
      list(),
    );
    await expect(sourceAPI.resolve(choice(), signal(), user)).resolves.toEqual(
      resolved(),
    );
    const u = new URL(fetcher.mock.calls[1][0], "https://synthetic.invalid");
    expect([...u.searchParams.keys()].sort()).toEqual([
      "domainId",
      "expectedCredentialRevision",
      "expectedRevision",
    ]);
    expect(u.searchParams.get("expectedRevision")).toBe("9");
  });
  it("transport network and abort classifications are unchanged", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw Error("synthetic network");
      }),
    );
    await expect(sourceAPI.list(query, signal(), user)).rejects.toMatchObject({
      code: "network",
      status: 0,
    });
    const c = new AbortController(),
      e = new DOMException("synthetic", "AbortError");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        c.abort();
        throw e;
      }),
    );
    await expect(sourceAPI.list(query, c.signal, user)).rejects.toBe(e);
  });
});

const selectAndResolve = async () => {
  fireEvent.click(
    await screen.findByRole("radio", { name: "选择数据源 synthetic.invalid" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "核对并打开连接详情" }));
};
const noSelection = () => {
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  expect(screen.queryByRole("radio")).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "核对并打开连接详情" }),
  ).toBeDisabled();
};
describe("real saved-source picker failure recovery", () => {
  for (const phase of ["list", "resolve"] as const) {
    for (const [status, code] of failures) {
      it.each(["", "<html>synthetic denial</html>"])(
        `${phase}: ${status} body %j reaches existing recovery`,
        async (body) => {
          const changed = vi.fn(),
            opened = vi.fn();
          const fetcher = vi.fn(async (url: string) =>
            phase === "resolve" && !url.includes("/resolve?")
              ? response(list())
              : new Response(body, { status }),
          );
          vi.stubGlobal("fetch", fetcher);
          render(
            <SavedSourcePicker
              userID={user}
              sessionChanged={changed}
              onResolved={opened}
            />,
          );
          if (phase === "resolve") await selectAndResolve();
          if (code === "unauthenticated")
            await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
          else {
            const notice = await screen.findByRole("alert");
            expect(notice).toHaveTextContent(
              code === "forbidden"
                ? "当前账户已无数据源访问权限"
                : "此数据源已不可用或不在当前授权范围内",
            );
            expect(changed).not.toHaveBeenCalled();
          }
          noSelection();
          expect(opened).not.toHaveBeenCalled();
          expect(fetcher).toHaveBeenCalledTimes(phase === "list" ? 1 : 2);
        },
      );
    }
    it(`${phase}: recognized password change still requests session renewal`, async () => {
      const changed = vi.fn(),
        opened = vi.fn();
      vi.stubGlobal(
        "fetch",
        vi.fn(async (url: string) =>
          phase === "resolve" && !url.includes("/resolve?")
            ? response(list())
            : response({ error: "password_change_required" }, 403),
        ),
      );
      render(
        <SavedSourcePicker
          userID={user}
          sessionChanged={changed}
          onResolved={opened}
        />,
      );
      if (phase === "resolve") await selectAndResolve();
      await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
      noSelection();
      expect(opened).not.toHaveBeenCalled();
    });
  }
  it("a refreshed list clears its previously selected choice before a denial", async () => {
    let denied = false;
    const changed = vi.fn(),
      opened = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        denied ? new Response("", { status: 401 }) : response(list()),
      ),
    );
    render(
      <SavedSourcePicker
        userID={user}
        sessionChanged={changed}
        onResolved={opened}
      />,
    );
    fireEvent.click(await screen.findByRole("radio"));
    expect(
      screen.getByRole("button", { name: "核对并打开连接详情" }),
    ).toBeEnabled();
    denied = true;
    fireEvent.click(screen.getByRole("button", { name: "刷新已授权数据源" }));
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
    noSelection();
    expect(opened).not.toHaveBeenCalled();
  });
  it("recognized 409 still clears and refreshes without resolving automatically", async () => {
    let lists = 0;
    const changed = vi.fn(),
      opened = vi.fn();
    const fetcher = vi.fn(async (url: string) =>
      url.includes("/resolve?")
        ? response({ error: "selection_changed" }, 409)
        : response(list(++lists === 1 ? "9" : "10")),
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <SavedSourcePicker
        userID={user}
        sessionChanged={changed}
        onResolved={opened}
      />,
    );
    await selectAndResolve();
    await screen.findByText(/配置或凭据已改变/);
    await waitFor(() => expect(lists).toBe(2));
    expect(await screen.findByRole("radio")).not.toBeChecked();
    expect(
      screen.getByRole("button", { name: "核对并打开连接详情" }),
    ).toBeDisabled();
    expect(
      fetcher.mock.calls.filter(([u]) => u.includes("/resolve?")),
    ).toHaveLength(1);
    expect(opened).not.toHaveBeenCalled();
    expect(changed).not.toHaveBeenCalled();
  });
  it("malformed 409 is not invented as selection_changed or auto-refreshed", async () => {
    const fetcher = vi.fn(async (url: string) =>
      url.includes("/resolve?")
        ? new Response("<html>unknown conflict</html>", { status: 409 })
        : response(list()),
    );
    vi.stubGlobal("fetch", fetcher);
    const changed = vi.fn(),
      opened = vi.fn();
    render(
      <SavedSourcePicker
        userID={user}
        sessionChanged={changed}
        onResolved={opened}
      />,
    );
    await selectAndResolve();
    await screen.findByText("数据源响应无法安全确认，请重新读取。");
    noSelection();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(changed).not.toHaveBeenCalled();
    expect(opened).not.toHaveBeenCalled();
  });
  it("successful explicit resolve opens exactly once without a business POST", async () => {
    const fetcher = vi.fn(async (url: string, _init: RequestInit) =>
      response(url.includes("/resolve?") ? resolved() : list()),
    );
    vi.stubGlobal("fetch", fetcher);
    const changed = vi.fn(),
      opened = vi.fn();
    render(
      <SavedSourcePicker
        userID={user}
        sessionChanged={changed}
        onResolved={opened}
      />,
    );
    await selectAndResolve();
    await waitFor(() =>
      expect(opened).toHaveBeenCalledExactlyOnceWith(resolved()),
    );
    expect(
      fetcher.mock.calls.every(
        ([, init]) => init.method === "GET" && !init.body,
      ),
    ).toBe(true);
    expect(changed).not.toHaveBeenCalled();
  });
  it("clearing a pending resolve suppresses its late malformed 401", async () => {
    let finish!: (r: Response) => void;
    const pending = new Promise<Response>((r) => {
      finish = r;
    });
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string) =>
        url.includes("/resolve?") ? pending : Promise.resolve(response(list())),
      ),
    );
    const changed = vi.fn(),
      opened = vi.fn();
    render(
      <SavedSourcePicker
        userID={user}
        sessionChanged={changed}
        onResolved={opened}
      />,
    );
    await selectAndResolve();
    fireEvent.click(screen.getByRole("button", { name: "清除数据源选择" }));
    await act(async () => {
      finish(new Response("<html>late</html>", { status: 401 }));
      await pending;
    });
    expect(changed).not.toHaveBeenCalled();
    expect(opened).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "核对并打开连接详情" }),
    ).toBeDisabled();
  });
  it("a superseded user's list denial cannot invalidate the new user's result", async () => {
    let finish!: (r: Response) => void;
    const pending = new Promise<Response>((r) => {
      finish = r;
    });
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockImplementationOnce(() => pending)
        .mockResolvedValue(response(list(), 200, String(user + 1))),
    );
    const changed = vi.fn(),
      opened = vi.fn();
    const view = render(
      <SavedSourcePicker
        userID={user}
        sessionChanged={changed}
        onResolved={opened}
      />,
    );
    view.rerender(
      <SavedSourcePicker
        userID={user + 1}
        sessionChanged={changed}
        onResolved={opened}
      />,
    );
    await screen.findByRole("radio");
    await act(async () => {
      finish(new Response("<html>old identity</html>", { status: 401 }));
      await pending;
    });
    expect(changed).not.toHaveBeenCalled();
    expect(opened).not.toHaveBeenCalled();
    expect(screen.getByRole("radio")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
