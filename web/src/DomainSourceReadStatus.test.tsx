// Actual APIs and React components, synthetic responses only; no database/AD.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import DomainCredentialSource, {
  SourceRecovery,
} from "./DomainCredentialSource";
import {
  sourceAPI,
  uncertainSourceError,
  type SourceDetail,
  type SourceInput,
  type SourceReceipt,
} from "./domain-source-api";
import {
  discardSourceIntent,
  readSourceIntent,
  saveSourceIntent,
  type SourceIntent,
} from "./domain-source-intent";

const actor = 312;
const domainId = "synthetic-source-domain";
const base = "/api/domains/credential-source";
const profile: Profile = {
  ID: actor,
  username: "synthetic-source-reader",
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
const intent: SourceIntent = {
  operation: "detach",
  domainId,
  expectedRevision: "3",
  expectedConnectionCredentialGeneration: "2",
  idempotencyKey: "synthetic-original-key",
};
const detail = (): SourceDetail => ({
  domainId,
  revision: "3",
  connectionCredentialGeneration: "2",
  credentialSource: "custom",
  credentialConfigured: true,
  testEligible: false,
  reference: null,
});
const receipt = (): SourceReceipt => ({
  result: "SUCCESS",
  operation: "detach",
  domainId,
  revision: "4",
  connectionCredentialGeneration: "3",
  credentialSource: "unconfigured",
  replayed: true,
  deleted: false,
  currentRevision: "4",
  currentConnectionCredentialGeneration: "3",
  currentCredentialSource: "unconfigured",
});
const response = (data: unknown, status = 200, user = String(actor)) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "X-ADTR-User-ID": user, "Content-Type": "application/json" },
  });
const signal = () => new AbortController().signal;
const reads = [
  ["detail", (s = signal()) => sourceAPI.detail(domainId, actor, s), detail],
  [
    "receipt",
    (s = signal()) => sourceAPI.receipt(intent.idempotencyKey, actor, s),
    () => ({ receipt: receipt() }),
  ],
] as const;
const denials = [
  [401, "unauthenticated"],
  [403, "forbidden"],
  [404, "not_found"],
] as const;
const unreadable = ["", "<html>synthetic failure</html>", '{"error":'];
const proof = { actorPassword: "synthetic-proof", totpCode: "123456" };
const mutations = ["reference", "custom", "detach"].map(
  (operation) =>
    [
      operation,
      () =>
        sourceAPI.mutate(
          {
            ...intent,
            operation,
            ...(operation === "reference"
              ? {
                  accountId: "synthetic-account",
                  expectedAccountRevision: "2",
                  expectedAccountCredentialRevision: "1",
                  expectedGrantRevision: "4",
                }
              : {}),
            ...(operation === "custom"
              ? { username: "SYNTHETIC\\reader", password: "synthetic-pair" }
              : {}),
          } as SourceInput,
          proof,
          profile.csrfToken,
          actor,
          signal(),
        ),
    ] as const,
);
const writes = [
  ...mutations,
  [
    "create-unconfigured",
    () =>
      sourceAPI.createUnconfigured(
        {
          domain: "synthetic.invalid",
          dcHostName: "dc.synthetic.invalid",
          ldapAddr: "",
          port: "636",
          idempotencyKey: intent.idempotencyKey,
        },
        proof,
        profile.csrfToken,
        actor,
        signal(),
      ),
  ] as const,
];
async function settle() {
  await act(async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  });
}
const context = (sessionChanged = vi.fn()) => ({
  profile,
  sessionChanged,
  can: () => true,
});
const mount = (
  kind: "detail" | "receipt",
  sessionChanged = vi.fn(),
  done = vi.fn(),
) => {
  const ctx = context(sessionChanged);
  if (kind === "receipt") {
    saveSourceIntent(profile, intent);
    return render(<SourceRecovery context={ctx} intent={intent} done={done} />);
  }
  return render(
    <DomainCredentialSource
      context={ctx}
      connection={{ domainId, domain: "synthetic.invalid" }}
      done={done}
    />,
  );
};
beforeEach(() => {
  discardSourceIntent(profile);
  sessionStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  discardSourceIntent(profile);
});

describe("source reads retain status only when JSON decoding fails", () => {
  for (const [name, read, valid] of reads) {
    for (const [status, code] of denials) {
      it.each(unreadable)(`${name}: ${status} unreadable %j`, async (body) => {
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
          headers: {},
        });
        expect(fetcher.mock.calls[0][1].body).toBeUndefined();
      });
      it.each([
        null,
        {},
        { error: 12 },
        { error: "private_unrecognized" },
        { error: "forbidden", extra: "synthetic" },
      ])(
        `${name}: ${status} parsed JSON keeps old error policy %j`,
        async (data) => {
          vi.stubGlobal(
            "fetch",
            vi.fn(async () => response(data, status)),
          );
          const code =
            data && typeof data.error === "string" ? data.error : "internal";
          await expect(read()).rejects.toMatchObject({ code, status });
        },
      );
      it(`${name}: ${status} rejected body stream`, async () => {
        vi.stubGlobal(
          "fetch",
          vi.fn(
            async () =>
              ({
                ok: false,
                status,
                json: async () => {
                  throw Error("synthetic stream interruption");
                },
              }) as unknown as Response,
          ),
        );
        await expect(read()).rejects.toMatchObject({ code, status });
      });
    }
    it.each([
      [401, "unauthenticated"],
      [403, "password_change_required"],
      [403, "tenant_not_configured"],
      [403, "forbidden"],
      [404, "not_found"],
      [409, "revision_conflict"],
      [409, "idempotency_conflict"],
      [503, "schema_unavailable"],
    ])(`${name}: preserves server %s/%s`, async (status, code) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response({ error: code }, Number(status))),
      );
      await expect(read()).rejects.toMatchObject({ code, status });
    });
    it.each([400, 409, 422, 429, 500, 503])(
      `${name}: no invented meaning for malformed %s`,
      async (status) => {
        vi.stubGlobal(
          "fetch",
          vi.fn(async () => new Response("html", { status })),
        );
        await expect(read()).rejects.toMatchObject({
          code: "invalid_response",
          status,
        });
      },
    );
    it(`${name}: successful exact DTO remains valid`, async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response(valid())),
      );
      await expect(read()).resolves.toEqual(
        name === "detail" ? detail() : receipt(),
      );
    });
    it.each(["", '{"unexpected":true}'])(
      `${name}: malformed success stays invalid %j`,
      async (body) => {
        vi.stubGlobal(
          "fetch",
          vi.fn(
            async () =>
              new Response(body, {
                headers: { "X-ADTR-User-ID": String(actor) },
              }),
          ),
        );
        await expect(read()).rejects.toMatchObject({
          code: "invalid_response",
        });
      },
    );
    it.each(["", "313"])(
      `${name}: actor mismatch %s remains session-bound`,
      async (user) => {
        vi.stubGlobal(
          "fetch",
          vi.fn(async () => response(valid(), 200, user)),
        );
        await expect(read()).rejects.toMatchObject({
          code: "domain_source_identity_changed",
          status: 401,
        });
      },
    );
    it(`${name}: network failure remains retryable uncertainty`, async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => {
          throw Error("synthetic offline");
        }),
      );
      await expect(read()).rejects.toMatchObject({ code: "network" });
    });
    it(`${name}: aborted fetch preserves the abort`, async () => {
      const controller = new AbortController();
      const error = new DOMException("synthetic abort", "AbortError");
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => {
          controller.abort();
          throw error;
        }),
      );
      await expect(read(controller.signal)).rejects.toBe(error);
    });
  }
});

describe("all shared POST routes retain uncertain-response recovery", () => {
  for (const [name, write] of writes) {
    for (const [status] of denials) {
      it.each(unreadable)(
        `${name}: malformed ${status} is not a definitive rejection %j`,
        async (body) => {
          const fetcher = vi.fn(
            async (_url: string, _init: RequestInit) =>
              new Response(body, { status }),
          );
          vi.stubGlobal("fetch", fetcher);
          const error = await write().catch((e: unknown) => e);
          expect(error).toMatchObject({ code: "invalid_response", status });
          expect(uncertainSourceError(error)).toBe(true);
          expect(fetcher).toHaveBeenCalledTimes(1);
          expect(fetcher.mock.calls[0][1]).toMatchObject({
            method: "POST",
            headers: { "X-CSRF-Token": profile.csrfToken },
          });
          expect(
            JSON.parse(fetcher.mock.calls[0][1].body as string).idempotencyKey,
          ).toBe(intent.idempotencyKey);
        },
      );
    }
    it(`${name}: definitive JSON denial stays definitive`, async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => response({ error: "forbidden" }, 403)),
      );
      const error = await write().catch((e: unknown) => e);
      expect(error).toMatchObject({ code: "forbidden", status: 403 });
      expect(uncertainSourceError(error)).toBe(false);
    });
  }
});

describe("actual source detail and recovery UI", () => {
  for (const kind of ["detail", "receipt"] as const) {
    for (const [status, code] of denials) {
      it.each([false, true])(
        `${kind}: ${status} JSON=%s handles denial without automatic retries`,
        async (json) => {
          vi.useFakeTimers();
          const fetcher = vi.fn(async () =>
            json
              ? response({ error: code }, status)
              : new Response("<html>synthetic</html>", { status }),
          );
          vi.stubGlobal("fetch", fetcher);
          const sessionChanged = vi.fn(),
            done = vi.fn();
          mount(kind, sessionChanged, done);
          await settle();
          expect(sessionChanged).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
          if (status === 403)
            expect(screen.getByRole("alert")).toHaveTextContent(
              "当前账户没有执行此操作的权限",
            );
          if (status === 404)
            expect(screen.getByRole("alert")).toHaveTextContent(
              "未找到回执不能证明原操作未提交",
            );
          expect(
            screen.queryByLabelText("新的凭据来源"),
          ).not.toBeInTheDocument();
          expect(done).not.toHaveBeenCalled();
          await act(async () => {
            await vi.advanceTimersByTimeAsync(6000);
          });
          expect(fetcher).toHaveBeenCalledTimes(1);
          if (kind === "receipt")
            expect(readSourceIntent(profile)).toEqual(intent);
        },
      );
    }
    it(`${kind}: malformed 503 still retries and an explicit refresh recovers`, async () => {
      vi.useFakeTimers();
      const fetcher = vi.fn(async () => new Response("html", { status: 503 }));
      vi.stubGlobal("fetch", fetcher);
      const done = vi.fn();
      mount(kind, vi.fn(), done);
      await settle();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });
      expect(fetcher).toHaveBeenCalledTimes(2);
      fetcher.mockImplementation(async () =>
        response(kind === "detail" ? detail() : { receipt: receipt() }),
      );
      fireEvent.click(
        screen.getByRole("button", {
          name: kind === "detail" ? "重新读取凭据来源" : "查询原来源回执",
        }),
      );
      await settle();
      expect(fetcher).toHaveBeenCalledTimes(3);
      if (kind === "detail")
        expect(screen.getByLabelText("新的凭据来源")).toBeInTheDocument();
      else {
        expect(done).toHaveBeenCalledWith(receipt());
        expect(readSourceIntent(profile)).toBeNull();
      }
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(fetcher).toHaveBeenCalledTimes(3);
    });
    it(`${kind}: late denial after unmount cannot change session or retry`, async () => {
      vi.useFakeTimers();
      let finish!: (r: Response) => void;
      const fetcher = vi.fn(
        (_url: string, _init: RequestInit) =>
          new Promise<Response>((r) => {
            finish = r;
          }),
      );
      vi.stubGlobal("fetch", fetcher);
      const sessionChanged = vi.fn(),
        done = vi.fn();
      const root = mount(kind, sessionChanged, done);
      await settle();
      root.unmount();
      expect(fetcher.mock.calls[0][1].signal?.aborted).toBe(true);
      finish(new Response("html", { status: 401 }));
      await settle();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(sessionChanged).not.toHaveBeenCalled();
      expect(done).not.toHaveBeenCalled();
      expect(fetcher).toHaveBeenCalledTimes(1);
    });
  }
  it("detail clears previous editable source on denied refresh", async () => {
    const fetcher = vi.fn(async () => response(detail()));
    vi.stubGlobal("fetch", fetcher);
    mount("detail");
    await settle();
    expect(screen.getByLabelText("新的凭据来源")).toBeInTheDocument();
    fetcher.mockImplementation(
      async () => new Response("html", { status: 403 }),
    );
    fireEvent.click(screen.getByRole("button", { name: "重新读取凭据来源" }));
    await settle();
    expect(screen.queryByLabelText("新的凭据来源")).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "当前账户没有执行此操作的权限",
    );
  });
  it.each([401, 403, 404])(
    "uncertain POST %s keeps the original intent and only recovers with GET",
    async (status) => {
      const fetcher = vi.fn(async (url: string, init: RequestInit) => {
        if (init.method === "POST") return new Response("html", { status });
        if (url.startsWith(`${base}/mutation?`))
          return new Response("html", { status: 404 });
        return response(detail());
      });
      vi.stubGlobal("fetch", fetcher);
      const sessionChanged = vi.fn(),
        done = vi.fn();
      mount("detail", sessionChanged, done);
      await settle();
      fireEvent.change(screen.getByLabelText("新的凭据来源"), {
        target: { value: "detach" },
      });
      fireEvent.change(screen.getByLabelText("操作者当前密码"), {
        target: { value: proof.actorPassword },
      });
      fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), {
        target: { value: proof.totpCode },
      });
      fireEvent.click(screen.getByRole("button", { name: "保存凭据来源" }));
      await settle();
      const posts = () =>
        fetcher.mock.calls.filter(([, init]) => init.method === "POST");
      expect(posts()).toHaveLength(1);
      const posted = JSON.parse(posts()[0][1].body as string);
      const saved = readSourceIntent(profile);
      expect(saved).toEqual({
        ...intent,
        idempotencyKey: posted.idempotencyKey,
      });
      expect(screen.getByRole("alert")).toHaveTextContent(
        "未找到回执不能证明原操作未提交",
      );
      expect(sessionChanged).not.toHaveBeenCalled();
      expect(done).not.toHaveBeenCalled();
      const recovery = screen.getByRole("button", { name: "查询原来源回执" });
      fireEvent.click(recovery);
      fireEvent.click(recovery);
      await settle();
      expect(posts()).toHaveLength(1);
      expect(readSourceIntent(profile)).toEqual(saved);
      const receiptGets = fetcher.mock.calls.filter(([url]) =>
        url.startsWith(`${base}/mutation?`),
      );
      expect(receiptGets.length).toBeGreaterThanOrEqual(2);
      for (const [url, init] of receiptGets) {
        expect(
          new URL(url, "http://synthetic.invalid").searchParams.get(
            "idempotencyKey",
          ),
        ).toBe(posted.idempotencyKey);
        expect(init.method).toBe("GET");
        expect(init.body).toBeUndefined();
      }
      expect(
        sessionStorage.getItem(
          `adtr.domain-source-intent.v1:${actor}:${profile.username}`,
        ),
      ).not.toContain(proof.actorPassword);
      fetcher.mockImplementation(async () => response({ receipt: receipt() }));
      fireEvent.click(screen.getByRole("button", { name: "查询原来源回执" }));
      await settle();
      expect(posts()).toHaveLength(1);
      expect(readSourceIntent(profile)).toBeNull();
      expect(done).toHaveBeenCalledTimes(1);
      expect(sessionChanged).not.toHaveBeenCalled();
    },
  );
  it("new detail generation ignores an old response even if transport ignores abort", async () => {
    const pending: ((r: Response) => void)[] = [];
    const fetcher = vi.fn(
      (_url: string, _init: RequestInit) =>
        new Promise<Response>((r) => {
          pending.push(r);
        }),
    );
    vi.stubGlobal("fetch", fetcher);
    const sessionChanged = vi.fn();
    mount("detail", sessionChanged);
    await settle();
    fireEvent.click(screen.getByRole("button", { name: "重新读取凭据来源" }));
    await settle();
    expect(pending).toHaveLength(2);
    expect(fetcher.mock.calls[0][1].signal?.aborted).toBe(true);
    pending[1](response(detail()));
    await settle();
    pending[0](new Response("html", { status: 401 }));
    await settle();
    expect(sessionChanged).not.toHaveBeenCalled();
    expect(screen.getByLabelText("新的凭据来源")).toBeInTheDocument();
  });
  it("switching the recovery key suppresses an old failure and preserves the new intent", async () => {
    const pending: ((r: Response) => void)[] = [];
    const fetcher = vi.fn(
      (_url: string, _init: RequestInit) =>
        new Promise<Response>((r) => {
          pending.push(r);
        }),
    );
    vi.stubGlobal("fetch", fetcher);
    const sessionChanged = vi.fn(),
      done = vi.fn(),
      ctx = context(sessionChanged);
    const root = mount("receipt", sessionChanged, done);
    await settle();
    const next = { ...intent, idempotencyKey: "synthetic-next-key" };
    saveSourceIntent(profile, next);
    root.rerender(<SourceRecovery context={ctx} intent={next} done={done} />);
    await settle();
    expect(fetcher.mock.calls[0][1].signal?.aborted).toBe(true);
    pending[1](new Response("html", { status: 404 }));
    await settle();
    pending[0](new Response("html", { status: 401 }));
    await settle();
    expect(sessionChanged).not.toHaveBeenCalled();
    expect(done).not.toHaveBeenCalled();
    expect(readSourceIntent(profile)).toEqual(next);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "未找到回执不能证明原操作未提交",
    );
  });
  it("known password-change error keeps its session meaning", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => response({ error: "password_change_required" }, 403)),
    );
    const sessionChanged = vi.fn();
    mount("detail", sessionChanged);
    await settle();
    expect(sessionChanged).toHaveBeenCalledTimes(1);
  });
  it("malformed receipt success does not clear the original intent", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({ receipt: { ...receipt(), domainId: "another-domain" } }),
      ),
    );
    const done = vi.fn();
    mount("receipt", vi.fn(), done);
    await settle();
    expect(done).not.toHaveBeenCalled();
    expect(readSourceIntent(profile)).toEqual(intent);
    expect(screen.getByRole("alert")).toHaveTextContent("服务器结果无法确认");
  });
});
