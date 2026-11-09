// Synthetic transport/DOM regressions, not PostgreSQL or real browser evidence.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import OperationalLogsWorkspace from "./OperationalLogsWorkspace";
import { type Profile } from "./api";
import {
  emptyBundleQuery,
  emptyLogQuery,
  logAPI,
  logOperations,
  type BundleDetail,
  type Selection,
} from "./operational-log-api";
import { useLogRead, useLogMutation } from "./operational-log-common";
import {
  beginBundleIntent,
  bundleIntent,
  discardBundleIntent,
  markBundleAttempted,
} from "./operational-log-intent";

const id = "00000000-0000-4000-8000-000000000309";
const profile: Profile = {
  ID: 309,
  username: "synthetic-log-reader",
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
  csrfToken: "synthetic-log-csrf",
};
const selection: Selection = {
  startTm: "2026-11-08T00:00:00Z",
  endTm: "2026-11-09T00:00:00Z",
  systemType: ["api", "worker"],
};
const proof = { actorPassword: "synthetic-only", totpCode: "123456" };
const signal = () => new AbortController().signal;
const input = () => ({ ...selection, idempotencyKey: id });
const response = (value: unknown, status = 200, actor = String(profile.ID)) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", "X-ADTR-User-ID": actor },
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
  '{"error":""}',
  '{"error":"raw\\nvalue"}',
];
const reads = [
  ["list", () => logAPI.list(emptyLogQuery(), signal(), profile.ID)],
  ["sources", () => logAPI.sources(signal(), profile.ID)],
  ["history", () => logAPI.history(emptyBundleQuery(), signal(), profile.ID)],
  ["detail", () => logAPI.detail(id, signal(), profile.ID)],
  ["download", () => logAPI.download(id, signal(), profile.ID)],
] as const;
const writes = [
  [
    "submit",
    () =>
      logAPI.submit(input(), proof, profile.csrfToken, signal(), profile.ID),
  ],
  [
    "cancel",
    () => logAPI.cancel(id, proof, profile.csrfToken, signal(), profile.ID),
  ],
] as const;
const coverage = {
  mode: "best_effort" as const,
  gapsPossible: true as const,
  journalCreatedAt: "2026-11-08T00:00:00Z",
  firstRecordedAt: null,
};
const detail = (): BundleDetail => ({
  task: {
    taskUUID: id,
    taskName: "system.logs_bundle",
    domainId: "platform",
    payloadVersion: 1,
    state: "succeeded",
    sourceState: "SUCCESS",
    error: "",
    createdAt: "2026-11-08T00:00:00Z",
    updatedAt: "2026-11-08T00:01:00Z",
    attempt: 1,
    maxAttempts: 1,
    progress: 100,
    resultVersion: 1,
    result: {},
    cursor: {},
    parentTaskUUID: "",
    terminalAt: "2026-11-08T00:01:00Z",
    archived: false,
    visibilityVersion: 0,
  },
  downloadReady: true,
  artifactStatus: "eligible",
  rowCount: 0,
  snapshotAt: "2026-11-08T00:00:30Z",
  downloadPath: `/api/system/logs/bundles/download?taskUUID=${id}`,
  coverage,
});
beforeEach(() => {
  discardBundleIntent();
  sessionStorage.clear();
  window.history.replaceState({}, "", "#operational-logs/list");
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("operational-log GET error classification", () => {
  for (const [name, read] of reads) {
    for (const [status, code] of failures) {
      it.each(bodies)(
        `${name}: ${status} unusable error body %j`,
        async (body) => {
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
            redirect: "error",
            cache: "no-store",
          });
          expect(fetcher.mock.calls[0][1].body).toBeUndefined();
        },
      );
    }
    it.each([
      [401, "unauthenticated"],
      [403, "password_change_required"],
      [403, "forbidden"],
      [403, "authorization_revoked"],
      [404, "not_found"],
      [500, "schema_unavailable"],
    ])(`${name}: preserves valid %s/%s`, async (status, code) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response({ error: code }, Number(status))),
      );
      await expect(read()).rejects.toMatchObject({ code, status });
    });
    it(`${name}: malformed success is still invalid`, async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async () =>
            new Response("{", {
              status: 200,
              headers: { "X-ADTR-User-ID": String(profile.ID) },
            }),
        ),
      );
      await expect(read()).rejects.toMatchObject({
        code: "invalid_response",
        status: 0,
      });
    });
    it(`${name}: success cannot bypass actor binding`, async () => {
      const json = vi.fn();
      vi.stubGlobal(
        "fetch",
        vi.fn(
          async () =>
            ({
              ok: true,
              status: 200,
              headers: new Headers({ "X-ADTR-User-ID": "310" }),
              json,
            }) as unknown as Response,
        ),
      );
      await expect(read()).rejects.toMatchObject({
        code: "unauthenticated",
        status: 401,
      });
      expect(json).not.toHaveBeenCalled();
    });
    it.each([429, 500, 503])(
      `${name}: unclassified malformed %s stays invalid`,
      async (status) => {
        vi.stubGlobal(
          "fetch",
          vi.fn(async () => new Response("<html>not JSON</html>", { status })),
        );
        await expect(read()).rejects.toMatchObject({
          code: "invalid_response",
          status: 0,
        });
      },
    );
    it.each(failures)(
      `${name}: body reader failure preserves %s`,
      async (status, code) => {
        const json = vi.fn(async () => {
          throw Error("synthetic stream interrupted");
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
  }
});

describe("POST ambiguity and proof semantics remain unchanged", () => {
  for (const [name, write] of writes) {
    for (const status of [401, 403, 404, 500]) {
      it.each(["", "<html>interrupted</html>", '{"error":'])(
        `${name}: malformed ${status} is not a receipt or new authority code`,
        async (body) => {
          const fetcher = vi.fn(
            async (_url: string, _init: RequestInit) =>
              new Response(body, { status }),
          );
          vi.stubGlobal("fetch", fetcher);
          await expect(write()).rejects.toMatchObject({
            code: "invalid_response",
            status: 0,
          });
          expect(fetcher).toHaveBeenCalledTimes(1);
          expect(fetcher.mock.calls[0][1].method).toBe("POST");
        },
      );
    }
    it.each([
      [401, "unauthenticated"],
      [403, "password_change_required"],
      [403, "mfa_invalid"],
      [403, "forbidden"],
      [409, "idempotency_conflict"],
    ])(`${name}: preserves proof/server %s/%s`, async (status, code) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response({ error: code }, Number(status))),
      );
      await expect(write()).rejects.toMatchObject({ code, status });
    });
    it(`${name}: unusable JSON error code stays internal`, async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response({}, 401)),
      );
      await expect(write()).rejects.toMatchObject({
        code: "internal",
        status: 401,
      });
    });
  }
  it("unknown submission keeps the original attempted key and failure callback", async () => {
    const intent = beginBundleIntent(profile, selection);
    markBundleAttempted(profile, intent);
    const before = JSON.stringify(bundleIntent(profile)),
      stored = JSON.stringify(sessionStorage);
    const changed = vi.fn(),
      success = vi.fn(),
      failed = vi.fn();
    const fetcher = vi.fn(
      async () => new Response("<html>unknown</html>", { status: 403 }),
    );
    vi.stubGlobal("fetch", fetcher);
    function Harness() {
      const write = useLogMutation(changed);
      return (
        <>
          <button
            onClick={() =>
              void write.run(
                (s) =>
                  logAPI.submit(
                    intent.input,
                    proof,
                    profile.csrfToken,
                    s,
                    profile.ID,
                  ),
                success,
                failed,
              )
            }
          >
            submit
          </button>
          <p>{write.error}</p>
        </>
      );
    }
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "submit" }));
    await screen.findByText(/服务器结果无法确认/);
    expect(failed).toHaveBeenCalledTimes(1);
    expect(changed).not.toHaveBeenCalled();
    expect(success).not.toHaveBeenCalled();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(JSON.stringify(bundleIntent(profile))).toBe(before);
    expect(JSON.stringify(sessionStorage)).toBe(stored);
  });
});

describe("actual workspace and request-generation recovery", () => {
  it.each([401, 403, 404])(
    "refresh HTML %s removes a previously rendered row",
    async (status) => {
      const event = {
        schemaVersion: 1,
        eventId: id,
        processId: id,
        module: "api",
        code: "service_start_requested",
        outcome: "attempted",
        severity: "info",
        reason: "",
        observedAt: "2026-11-08T01:00:00Z",
        recordedAt: "2026-11-08T01:00:01Z",
      };
      let deny = false;
      const changed = vi.fn();
      vi.stubGlobal(
        "fetch",
        vi.fn(async (url: string) => {
          if (url === "/api/access/check")
            return response({ results: logOperations.map(() => true) });
          return deny
            ? new Response("<html>expired</html>", { status })
            : response({
                page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
                List: [{ event, log: JSON.stringify(event) }],
                exhausted: true,
                selection,
                coverage,
              });
        }),
      );
      render(
        <OperationalLogsWorkspace profile={profile} sessionChanged={changed} />,
      );
      await screen.findByRole("table");
      deny = true;
      fireEvent.click(screen.getByRole("button", { name: "刷新事件" }));
      if (status === 401)
        await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
      else {
        await screen.findByRole("alert");
        expect(changed).not.toHaveBeenCalled();
      }
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
    },
  );
  it.each([
    [401, "unauthenticated"],
    [403, "forbidden"],
    [404, "not_found"],
  ] as const)(
    "list HTML %s reaches its existing UI path without writes",
    async (status, code) => {
      const intent = beginBundleIntent(profile, selection);
      markBundleAttempted(profile, intent);
      const saved = JSON.stringify(sessionStorage),
        changed = vi.fn();
      const fetcher = vi.fn(async (url: string) =>
        url === "/api/access/check"
          ? response({ results: logOperations.map(() => true) })
          : new Response("<html>denied</html>", { status }),
      );
      vi.stubGlobal("fetch", fetcher);
      render(
        <OperationalLogsWorkspace profile={profile} sessionChanged={changed} />,
      );
      if (code === "unauthenticated")
        await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
      else {
        await screen.findByRole("alert");
        expect(changed).not.toHaveBeenCalled();
      }
      if (code === "forbidden")
        expect(screen.getByRole("alert")).toHaveTextContent(
          "当前账户没有此运行日志操作权限",
        );
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
      expect(screen.queryByText(/服务器结果无法确认/)).not.toBeInTheDocument();
      expect(JSON.stringify(sessionStorage)).toBe(saved);
      expect(
        fetcher.mock.calls.every(
          ([url]) =>
            url === "/api/access/check" || url.startsWith("/api/system/logs?"),
        ),
      ).toBe(true);
    },
  );
  it("valid password-change code keeps its session callback", async () => {
    const changed = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url === "/api/access/check"
          ? response({ results: logOperations.map(() => true) })
          : response({ error: "password_change_required" }, 403),
      ),
    );
    render(
      <OperationalLogsWorkspace profile={profile} sessionChanged={changed} />,
    );
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
  });
  it.each([401, 403])(
    "download HTML %s creates no browser artifact or business POST",
    async (status) => {
      window.history.replaceState({}, "", `#operational-logs/detail/${id}`);
      const changed = vi.fn(),
        create = vi.fn();
      Object.defineProperty(URL, "createObjectURL", {
        configurable: true,
        writable: true,
        value: create,
      });
      let denied = false;
      const fetcher = vi.fn(async (url: string, init: RequestInit) => {
        if (url === "/api/access/check")
          return response({ results: logOperations.map(() => true) });
        if (url.includes("/download?")) {
          denied = true;
          return new Response("<html>no download</html>", { status });
        }
        if (url.includes("/detail?"))
          return denied
            ? new Response("<html>no detail</html>", { status })
            : response(detail());
        throw Error(`unexpected ${init.method}`);
      });
      vi.stubGlobal("fetch", fetcher);
      render(
        <OperationalLogsWorkspace profile={profile} sessionChanged={changed} />,
      );
      fireEvent.click(await screen.findByRole("button", { name: "下载 ZIP" }));
      if (status === 401)
        await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
      else {
        await screen.findByText(/当前账户没有此运行日志操作权限/);
        expect(changed).not.toHaveBeenCalled();
      }
      expect(create).not.toHaveBeenCalled();
      expect(screen.queryByText(/已收到经当前权限/)).not.toBeInTheDocument();
      expect(
        fetcher.mock.calls.filter(
          ([url, init]) =>
            init.method === "POST" && url !== "/api/access/check",
        ),
      ).toHaveLength(0);
    },
  );
  it("late malformed denial from a superseded read cannot invalidate the current generation", async () => {
    let resolve!: (r: Response) => void;
    const old = new Promise<Response>((r) => {
      resolve = r;
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(() => old),
    );
    const changed = vi.fn();
    function Harness({ version }: { version: string }) {
      const r = useLogRead<unknown>(
        version,
        (s) =>
          version === "old"
            ? logAPI.sources(s, profile.ID)
            : Promise.resolve("current"),
        changed,
      );
      return <p>{typeof r.data === "string" ? r.data : r.error}</p>;
    }
    const view = render(<Harness version="old" />);
    view.rerender(<Harness version="new" />);
    await screen.findByText("current");
    await act(async () => {
      resolve(new Response("<html>late</html>", { status: 401 }));
      await old;
    });
    expect(changed).not.toHaveBeenCalled();
    expect(screen.getByText("current")).toBeVisible();
  });
  it("transport abort remains the original abort rather than manufactured status", async () => {
    const c = new AbortController(),
      aborted = new DOMException("synthetic abort", "AbortError");
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        c.abort();
        throw aborted;
      }),
    );
    await expect(logAPI.sources(c.signal, profile.ID)).rejects.toBe(aborted);
  });
});
