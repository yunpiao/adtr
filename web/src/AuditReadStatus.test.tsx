import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AuditWorkspace from "./AuditWorkspace";
import { ApiError, type Profile } from "./api";
import {
  auditAPI,
  auditOperations,
  emptyFilter,
  type AuditRow,
} from "./audit-api";
import { emptyHistoryQuery } from "./audit-history";
import { useAuditMutation, useAuditRead } from "./audit-common";

const id = "00000000-0000-4000-8000-000000000305";
const profile: Profile = {
  ID: 305,
  username: "audit-synthetic-reader",
  role: "test",
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
  csrfToken: "synthetic-audit-csrf",
};
const signal = () => new AbortController().signal;
const proof = { actorPassword: "synthetic-only", totpCode: "123456" };
const input = () => ({
  ...emptyFilter(),
  selectColumn: ["event" as const],
  idempotencyKey: id,
});
const row: AuditRow = {
  ID: "auth.305",
  loginUser: "synthetic-visible-row",
  sourceIp: null,
  event: "login",
  eventArgs: "synthetic",
  eventResult: "SUCCESS",
  CreateTm: "2026-11-01T00:00:00Z",
  logType: 9,
  logTypeName: "system",
  userId: 305,
  source: "auth",
  domainId: null,
  deleted: false,
  deletable: true,
  visibilityVersion: 0,
  availability: {
    loginUser: true,
    sourceIp: false,
    path: false,
    requestId: false,
    eventResult: true,
  },
};
const list = () => ({
  page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
  List: [row],
  exhausted: true,
});
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: {
      "Content-Type": "application/json",
      "X-ADTR-User-ID": String(profile.ID),
    },
  });
const failures = [
  [401, "unauthenticated"],
  [403, "forbidden"],
  [404, "not_found"],
] as const;
const bodies = [
  "",
  "<html>synthetic proxy error</html>",
  '{"error":',
  "null",
  "{}",
  '{"error":17}',
];
const reads = [
  ["list", () => auditAPI.list("", signal())],
  ["types", () => auditAPI.types(signal())],
  ["columns", () => auditAPI.columns(signal())],
  [
    "history",
    () => auditAPI.history(emptyHistoryQuery(), signal(), profile.ID),
  ],
  ["detail", () => auditAPI.detail(id, signal(), profile.ID)],
  ["download", () => auditAPI.download(id, signal(), profile.ID)],
] as const;

beforeEach(() => {
  window.history.replaceState({}, "", "#audit/list");
  sessionStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("audit read status survives unusable error bodies", () => {
  for (const [name, read] of reads) {
    for (const [status, code] of failures) {
      it.each(bodies)(`${name}: ${status} with body %j`, async (body) => {
        const fetcher = vi.fn(
          async (_url: string, _init: RequestInit) =>
            new Response(body, { status }),
        );
        vi.stubGlobal("fetch", fetcher);
        await expect(read()).rejects.toMatchObject({ code, status });
        expect(fetcher).toHaveBeenCalledTimes(1);
        expect(fetcher.mock.calls[0][1]).toMatchObject({
          method: "GET",
          credentials: "same-origin",
          cache: "no-store",
          redirect: "error",
        });
      });
    }
  }
  it.each(failures)(
    "body read failure retains HTTP %s",
    async (status, code) => {
      const json = vi.fn(async () => {
        throw Error("synthetic body read failure");
      });
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => ({ ok: false, status, json }) as unknown as Response),
      );
      await expect(auditAPI.list("", signal())).rejects.toMatchObject({
        code,
        status,
      });
      expect(json).toHaveBeenCalledTimes(1);
    },
  );
  it.each([
    [401, "unauthenticated"],
    [403, "password_change_required"],
    [403, "authorization_revoked"],
    [403, "forbidden"],
    [404, "not_found"],
  ])("preserves valid GET %s/%s", async (status, code) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => response({ error: code }, status as number)),
    );
    await expect(auditAPI.list("", signal())).rejects.toMatchObject({
      code,
      status,
    });
  });
  it.each([200, 500, 503])(
    "retains invalid_response for malformed %s",
    async (status) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response("broken", { status })),
      );
      await expect(auditAPI.list("", signal())).rejects.toMatchObject({
        code: "invalid_response",
        status,
      });
    },
  );
  it("retains non-authority business errors and successful validation", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(
        response({ error: "export_snapshot_changed" }, 409),
      )
      .mockResolvedValueOnce(response(list()));
    vi.stubGlobal("fetch", fetcher);
    await expect(
      auditAPI.download(id, signal(), profile.ID),
    ).rejects.toMatchObject({ code: "export_snapshot_changed", status: 409 });
    await expect(auditAPI.list("", signal())).resolves.toEqual(list());
  });
});

function ReadProbe({
  changed,
  requestKey = "same",
}: {
  changed: () => void;
  requestKey?: string;
}) {
  const read = useAuditRead(
    requestKey,
    (s) => auditAPI.list(requestKey, s),
    changed,
  );
  return (
    <>
      <button onClick={read.refresh}>manual refresh</button>
      <output>{read.data ? "accepted row" : "no data"}</output>
      {read.error && <p role="alert">{read.error}</p>}
    </>
  );
}

describe("real reader and workspace recovery", () => {
  it.each(failures)(
    "%s clears previous data, stops retries and supports explicit refresh",
    async (status) => {
      vi.useFakeTimers();
      const changed = vi.fn();
      const fetcher = vi
        .fn()
        .mockResolvedValueOnce(response(list()))
        .mockImplementation(
          async () => new Response("<html>denied</html>", { status }),
        );
      vi.stubGlobal("fetch", fetcher);
      await act(async () => {
        render(<ReadProbe changed={changed} />);
      });
      expect(screen.getByText("accepted row")).toBeInTheDocument();
      await act(async () => {
        fireEvent.click(screen.getByText("manual refresh"));
      });
      expect(screen.getByText("no data")).toBeInTheDocument();
      expect(changed).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(fetcher).toHaveBeenCalledTimes(2);
      fetcher.mockResolvedValueOnce(response(list()));
      await act(async () => {
        fireEvent.click(screen.getByText("manual refresh"));
      });
      expect(screen.getByText("accepted row")).toBeInTheDocument();
      expect(fetcher).toHaveBeenCalledTimes(3);
    },
  );
  it.each(failures)(
    "AuditWorkspace handles a malformed %s without showing an empty successful result",
    async (status) => {
      vi.useFakeTimers();
      let denied = false,
        listReads = 0;
      const changed = vi.fn();
      vi.stubGlobal(
        "fetch",
        vi.fn(async (url: string, init: RequestInit) => {
          const path = url.split("?")[0];
          if (path === "/api/access/menu")
            return response({
              menu: [
                { mark: "audit", auth: { readable: true, writeable: false } },
              ],
            });
          if (path === "/api/access/check")
            return response({
              results: auditOperations.map((p) =>
                ["GET /api/audit", "GET /api/audit/types"].includes(p),
              ),
            });
          if (path === "/api/audit/types")
            return response({ events: [], List: [] });
          if (path === "/api/audit") {
            listReads++;
            return denied ? new Response("", { status }) : response(list());
          }
          throw Error(`Unexpected synthetic request ${url} ${init.method}`);
        }),
      );
      await act(async () => {
        render(<AuditWorkspace profile={profile} sessionChanged={changed} />);
      });
      expect(screen.getByText("synthetic-visible-row")).toBeInTheDocument();
      denied = true;
      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "刷新审计列表" }));
      });
      expect(
        screen.queryByText("synthetic-visible-row"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByLabelText("审计查询结果摘要"),
      ).not.toBeInTheDocument();
      expect(screen.queryByText(/匹配 0 条/)).not.toBeInTheDocument();
      expect(changed).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(listReads).toBe(2);
    },
  );
  it("valid GET 403/password_change_required revalidates the session without retries", async () => {
    vi.useFakeTimers();
    const changed = vi.fn();
    const fetcher = vi.fn(async () =>
      response({ error: "password_change_required" }, 403),
    );
    vi.stubGlobal("fetch", fetcher);
    await act(async () => {
      render(<ReadProbe changed={changed} />);
    });
    expect(changed).toHaveBeenCalledTimes(1);
    expect(screen.getByText("no data")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it.each(["network", "malformed-500"])(
    "still retries transient %s reads",
    async (kind) => {
      vi.useFakeTimers();
      const changed = vi.fn();
      const fetcher = vi
        .fn()
        .mockImplementationOnce(async () => {
          if (kind === "network") throw Error("synthetic offline");
          return new Response("broken", { status: 500 });
        })
        .mockResolvedValue(response(list()));
      vi.stubGlobal("fetch", fetcher);
      await act(async () => {
        render(<ReadProbe changed={changed} />);
      });
      expect(screen.getByText("no data")).toBeInTheDocument();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });
      expect(screen.getByText("accepted row")).toBeInTheDocument();
      expect(fetcher).toHaveBeenCalledTimes(2);
      expect(changed).not.toHaveBeenCalled();
    },
  );
  it("ignores a late unauthorized response from a replaced read generation", async () => {
    let finish!: (r: Response) => void;
    const changed = vi.fn();
    const fetcher = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          }),
      )
      .mockResolvedValue(response(list()));
    vi.stubGlobal("fetch", fetcher);
    const { rerender } = render(
      <ReadProbe changed={changed} requestKey="old" />,
    );
    await act(async () => {
      rerender(<ReadProbe changed={changed} requestKey="new" />);
    });
    await act(async () => {
      finish(new Response("", { status: 401 }));
    });
    expect(screen.getByText("accepted row")).toBeInTheDocument();
    expect(changed).not.toHaveBeenCalled();
    expect((fetcher.mock.calls[0][1] as RequestInit).signal?.aborted).toBe(
      true,
    );
  });
});

describe("proof-bearing writes keep their original semantics", () => {
  const writes = [
    [
      "submit",
      () => auditAPI.submit(input(), proof, profile.csrfToken, signal()),
    ],
    [
      "hide",
      () =>
        auditAPI.visibility(
          "delete",
          [row.ID],
          "synthetic",
          proof,
          profile.csrfToken,
          signal(),
        ),
    ],
    [
      "restore",
      () =>
        auditAPI.visibility(
          "restore",
          [row.ID],
          "synthetic",
          proof,
          profile.csrfToken,
          signal(),
        ),
    ],
  ] as const;
  for (const [name, write] of writes) {
    it.each([
      [401, "invalid_credentials"],
      [403, "invalid_totp"],
      [401, "unauthenticated"],
      [404, "not_found"],
    ])(`${name}: preserves %s/%s`, async (status, code) => {
      const fetcher = vi.fn(async (_url: string, _init: RequestInit) =>
        response({ error: code }, status as number),
      );
      vi.stubGlobal("fetch", fetcher);
      await expect(write()).rejects.toMatchObject({ code, status });
      expect(fetcher).toHaveBeenCalledTimes(1);
    });
    it.each([401, 403, 404])(
      `${name}: malformed %s is still uncertain`,
      async (status) => {
        vi.stubGlobal(
          "fetch",
          vi.fn(async () => new Response("broken", { status })),
        );
        await expect(write()).rejects.toMatchObject({
          code: "invalid_response",
          status,
        });
      },
    );
  }
  it.each([
    [401, "invalid_credentials"],
    [403, "invalid_totp"],
  ])(
    "mutation proof rejection %s/%s does not log out or replay",
    async (status, code) => {
      vi.useFakeTimers();
      const changed = vi.fn(),
        failed = vi.fn();
      const fetcher = vi.fn(async (_url: string, _init: RequestInit) =>
        response({ error: code }, status as number),
      );
      vi.stubGlobal("fetch", fetcher);
      function MutationProbe() {
        const mutation = useAuditMutation(changed);
        return (
          <button
            onClick={() =>
              void mutation.run(
                (s) => auditAPI.submit(input(), proof, profile.csrfToken, s),
                () => {},
                failed,
              )
            }
          >
            submit once
          </button>
        );
      }
      render(<MutationProbe />);
      await act(async () => {
        fireEvent.click(screen.getByText("submit once"));
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(changed).not.toHaveBeenCalled();
      expect(failed).toHaveBeenCalledWith(
        expect.objectContaining({ code, status }),
      );
      expect(fetcher).toHaveBeenCalledTimes(1);
      expect(
        JSON.parse((fetcher.mock.calls[0][1] as RequestInit).body as string)
          .idempotencyKey,
      ).toBe(id);
    },
  );
});
