// Actual versioned APIs/Workspaces; bounded synthetic streams only.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import type { SourceChoice } from "./domain-selection-api";
import type { Task } from "./task-api";
import DirectoryV2Workspace from "./DirectoryV2Workspace";
import DirectoryV3Workspace from "./DirectoryV3Workspace";
import { directoryV2API, uncertainDirectoryV2Error } from "./directory-v2-api";
import { directoryV3API, uncertainDirectoryV3Error } from "./directory-v3-api";
import { directoryV2BodyLimit } from "./directory-v2-json";
import {
  saveDirectoryV2Intent,
  readDirectoryV2Intent,
  discardDirectoryV2Intent,
  type DirectoryV2Intent,
} from "./directory-v2-intent";
import {
  saveDirectoryV3Intent,
  readDirectoryV3Intent,
  discardDirectoryV3Intent,
} from "./directory-v3-intent";

for (const {
  version,
  api,
  Workspace,
  uncertain,
  saveIntent,
  readIntent,
  discardIntent,
} of [
  {
    version: 2,
    api: directoryV2API,
    Workspace: DirectoryV2Workspace,
    uncertain: uncertainDirectoryV2Error,
    saveIntent: saveDirectoryV2Intent,
    readIntent: readDirectoryV2Intent,
    discardIntent: discardDirectoryV2Intent,
  },
  {
    version: 3,
    api: directoryV3API,
    Workspace: DirectoryV3Workspace,
    uncertain: uncertainDirectoryV3Error,
    saveIntent: saveDirectoryV3Intent,
    readIntent: readDirectoryV3Intent,
    discardIntent: discardDirectoryV3Intent,
  },
] as const)
  describe(`directory v${version}`, () => {
    const prefix = `/api/directory/v${version}`;
    const profile: Profile = {
      ID: 316,
      username: "synthetic-directory-reader",
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
    const choice: SourceChoice = {
      domainId: "synthetic-domain",
      domain: "synthetic.invalid",
      revision: "3",
      credentialRevision: "2",
      dcHostName: "dc.synthetic.invalid",
      ldapAddr: "",
      port: "636",
      mode: "ldaps",
      credentialConfigured: true,
      connectionState: "unverified",
      source: "configured_connection",
      lastTest: null,
    };
    const intent: DirectoryV2Intent = {
      domainId: choice.domainId,
      expectedRevision: "3",
      expectedCredentialGeneration: "2",
      idempotencyKey: "synthetic-original-key",
    };
    const query = {
      domainId: choice.domainId,
      kind: "" as const,
      pageIdx: 1,
      pageSize: 50,
    };
    const absent = () => ({
      dictionaryVersion: version,
      ...(version === 3 ? { observedCounts: null } : {}),
      available: false,
      list: [],
      page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
    });
    const task = (): Task => ({
      taskUUID: "synthetic-task",
      taskName: `domain.directory_read.v${version}`,
      domainId: choice.domainId,
      payloadVersion: 1,
      state: "succeeded",
      sourceState: "",
      error: "",
      createdAt: "2026-11-09T00:00:00Z",
      updatedAt: "2026-11-09T00:00:01Z",
      attempt: 1,
      maxAttempts: 1,
      progress: 100,
      resultVersion: 1,
      result: {},
      cursor: {},
      parentTaskUUID: "",
      terminalAt: "2026-11-09T00:00:01Z",
      archived: false,
      visibilityVersion: 0,
    });
    const response = (
      data: unknown,
      status = 200,
      actor = String(profile.ID),
    ) =>
      new Response(JSON.stringify(data), {
        status,
        headers: {
          "X-ADTR-User-ID": actor,
          "Content-Type": "application/json",
        },
      });
    const signal = () => new AbortController().signal;
    const reads = [
      [
        "observation",
        (s = signal()) => api.observation(query, profile.ID, s),
        absent,
      ],
      [
        "receipt",
        (s = signal()) => api.receipt(intent, profile.ID, s),
        () => ({ task: task() }),
      ],
      [
        "task",
        (s = signal()) =>
          api.task(choice.domainId, "synthetic-task", profile.ID, s),
        () => ({ task: task() }),
      ],
    ] as const;
    const proof = { actorPassword: "synthetic-proof", totpCode: "123456" };
    const writes = [
      [
        "sync",
        () => api.sync(intent, proof, profile.csrfToken, profile.ID, signal()),
      ],
      [
        "cancel",
        () =>
          api.cancel(
            choice.domainId,
            "synthetic-task",
            proof,
            profile.csrfToken,
            profile.ID,
            signal(),
          ),
      ],
    ] as const;
    let reply: (url: string, init: RequestInit) => Promise<Response>;
    const calls = (kind: string) =>
      vi
        .mocked(fetch)
        .mock.calls.filter(
          ([url]) => String(url).split("?")[0] === `${prefix}/${kind}`,
        );
    const businessPosts = () =>
      vi
        .mocked(fetch)
        .mock.calls.filter(
          ([url, init]) =>
            String(url).startsWith(`${prefix}/`) && init?.method === "POST",
        );
    const click = (name: string) =>
      fireEvent.click(screen.getByRole("button", { name }));
    async function settle() {
      await act(async () => {
        for (let i = 0; i < 100; i++) await Promise.resolve();
      });
    }
    async function mount(
      kind: "observation" | "receipt" | "task",
      changed = vi.fn(),
    ) {
      if (kind !== "observation")
        saveIntent(profile, {
          ...intent,
          ...(kind === "task" ? { taskUUID: "synthetic-task" } : {}),
        });
      const root = render(
        <Workspace profile={profile} sessionChanged={changed} />,
      );
      await settle();
      if (kind === "observation") {
        fireEvent.click(
          screen.getByRole("radio", { name: "选择数据源 synthetic.invalid" }),
        );
        click("核对并打开连接详情");
        await settle();
      }
      return root;
    }
    beforeEach(() => {
      discardIntent(profile);
      sessionStorage.clear();
      localStorage.clear();
      reply = async (url) =>
        response(
          url.startsWith(`${prefix}/observation`) ? absent() : { task: task() },
        );
      vi.stubGlobal(
        "fetch",
        vi.fn(async (url: string, init: RequestInit) => {
          const path = url.split("?")[0];
          if (path === "/api/access/menu")
            return response({
              menu: ["domains", "directory_assets", "tasks"].map((mark) => ({
                mark,
                auth: { readable: true, writeable: true },
              })),
            });
          if (path === "/api/access/check")
            return response({
              results: JSON.parse(init.body as string).paths.map(() => true),
            });
          if (path === "/api/domain-selection")
            return response({
              List: [choice],
              page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
              exhausted: true,
            });
          if (path === "/api/domain-selection/resolve")
            return response({
              selection: choice,
              checkedAt: "2026-11-09T00:00:00Z",
            });
          return reply(url, init);
        }),
      );
    });
    afterEach(() => {
      vi.useRealTimers();
      vi.unstubAllGlobals();
      discardIntent(profile);
    });

    describe("GET authority semantics survive only failed JSON decoding", () => {
      for (const [kind, read, valid] of reads) {
        for (const [status, code] of [
          [401, "unauthenticated"],
          [403, "forbidden"],
        ] as const) {
          it.each(["", "<html>synthetic</html>", '{"error":'])(
            `${kind}: ${status} malformed %j`,
            async (body) => {
              reply = async () => new Response(body, { status });
              await expect(read()).rejects.toMatchObject({ code, status });
              expect(calls(kind)).toHaveLength(1);
              expect(calls(kind)[0][1]).toMatchObject({
                method: "GET",
                credentials: "same-origin",
                cache: "no-store",
              });
              expect(calls(kind)[0][1]!.body).toBeUndefined();
            },
          );
          it(`${kind}: ${status} stream decode rejection`, async () => {
            reply = async () =>
              new Response(
                new ReadableStream({
                  start(controller) {
                    controller.error(new Error("synthetic stream failure"));
                  },
                }),
                { status },
              );
            await expect(read()).rejects.toMatchObject({ code, status });
          });
          it.each([
            null,
            {},
            { error: 12 },
            { error: "forbidden", extra: "synthetic" },
          ])(
            `${kind}: parsed malformed JSON at ${status} keeps original strict error policy %#`,
            async (data) => {
              reply = async () => response(data, status);
              await expect(read()).rejects.toMatchObject({
                code: "internal",
                status,
              });
            },
          );
        }
        it.each([
          [401, "unauthenticated"],
          [403, "password_change_required"],
          [403, "forbidden"],
          [403, "tenant_expired"],
          [404, "not_found"],
          [409, "directory_observation_unavailable"],
          [503, "directory_read_disabled"],
          [500, "private_unrecognized"],
        ])(`${kind}: preserves JSON %s/%s`, async (status, code) => {
          reply = async () => response({ error: code }, Number(status));
          await expect(read()).rejects.toMatchObject({ code, status });
        });
        it.each([400, 404, 409, 422, 429, 500, 503])(
          `${kind}: malformed %s retains its original HTTP status`,
          async (status) => {
            reply = async () => new Response("html", { status });
            await expect(read()).rejects.toMatchObject({
              code: "invalid_response",
              status,
            });
          },
        );
        it(`${kind}: valid success still passes`, async () => {
          reply = async () => response(valid());
          await expect(read()).resolves.toEqual(
            kind === "observation" ? absent() : task(),
          );
        });
        it(`${kind}: successful wrong actor still changes session`, async () => {
          reply = async () => response(valid(), 200, "999");
          await expect(read()).rejects.toMatchObject({
            code: "unauthenticated",
            status: 401,
          });
        });
        it(`${kind}: malformed success remains invalid`, async () => {
          reply = async () => response({ wrong: true });
          await expect(read()).rejects.toMatchObject({
            code: "invalid_response",
            status: 0,
          });
        });
        it(`${kind}: network and aborted fetch behavior is unchanged`, async () => {
          reply = async () => {
            throw Error("synthetic offline");
          };
          await expect(read()).rejects.toMatchObject({ code: "network" });
          const controller = new AbortController(),
            error = new DOMException("synthetic abort", "AbortError");
          reply = async () => {
            controller.abort();
            throw error;
          };
          await expect(read(controller.signal)).rejects.toBe(error);
        });
      }
    });

    describe("POST uncertainty remains original for sync and cancellation", () => {
      for (const [kind, write] of writes) {
        for (const status of [401, 403, 404]) {
          it.each(["", "<html>synthetic</html>", '{"error":'])(
            `${kind}: malformed ${status} is not proof of rejection %j`,
            async (body) => {
              reply = async () => new Response(body, { status });
              const error = await write().catch((e: unknown) => e);
              expect(error).toMatchObject({ code: "invalid_response", status });
              expect(uncertain(error)).toBe(true);
              expect(calls(kind)).toHaveLength(1);
              expect(calls(kind)[0][1]).toMatchObject({
                method: "POST",
                headers: { "X-CSRF-Token": profile.csrfToken },
              });
            },
          );
        }
        it(`${kind}: structured permission denial is still definitive`, async () => {
          reply = async () => response({ error: "forbidden" }, 403);
          const error = await write().catch((e: unknown) => e);
          expect(error).toMatchObject({ code: "forbidden", status: 403 });
          expect(uncertain(error)).toBe(false);
        });
      }
    });

    describe("actual Workspace authority, receipt and interruption flows", () => {
      for (const [kind] of reads) {
        for (const status of [401, 403]) {
          it(`${kind}: HTML${status} stops erroneous retry and retains recovery intent`, async () => {
            vi.useFakeTimers();
            reply = async () => new Response("html", { status });
            const changed = vi.fn();
            await mount(kind, changed);
            expect(changed).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
            if (status === 403)
              expect(screen.getByRole("alert")).toHaveTextContent(
                "当前账户没有执行此操作的权限",
              );
            await act(async () => {
              await vi.advanceTimersByTimeAsync(6000);
            });
            expect(calls(kind)).toHaveLength(1);
            expect(businessPosts()).toHaveLength(0);
            if (kind !== "observation")
              expect(readIntent(profile)).toEqual({
                ...intent,
                ...(kind === "task" ? { taskUUID: "synthetic-task" } : {}),
              });
          });
        }
        it(`${kind}: explicit refresh after 403 recovers with no write`, async () => {
          reply = async () => new Response("html", { status: 403 });
          await mount(kind);
          reply = async (url) =>
            response(
              url.startsWith(`${prefix}/observation`)
                ? absent()
                : { task: task() },
            );
          click(
            kind === "observation"
              ? "刷新目录观察"
              : kind === "receipt"
                ? "查询原同步回执"
                : "刷新目录任务",
          );
          await settle();
          expect(screen.queryByRole("alert")).not.toBeInTheDocument();
          expect(calls(kind)).toHaveLength(2);
          expect(businessPosts()).toHaveLength(0);
          if (kind !== "observation")
            expect(readIntent(profile)?.taskUUID).toBe("synthetic-task");
        });
        it(`${kind}: late401 after unmount cannot change session or reschedule`, async () => {
          vi.useFakeTimers();
          let finish!: (r: Response) => void;
          reply = () =>
            new Promise((r) => {
              finish = r;
            });
          const changed = vi.fn();
          const root = await mount(kind, changed);
          root.unmount();
          finish(new Response("html", { status: 401 }));
          await settle();
          await act(async () => {
            await vi.advanceTimersByTimeAsync(6000);
          });
          expect(changed).not.toHaveBeenCalled();
          expect(calls(kind)).toHaveLength(1);
          expect(calls(kind)[0][1]?.signal?.aborted).toBe(true);
        });
      }
      it.each([false, true])(
        "receipt404 structured=%s preserves the existing same-key retry gate",
        async (structured) => {
          vi.useFakeTimers();
          reply = async () =>
            structured
              ? response({ error: "not_found" }, 404)
              : new Response("html", { status: 404 });
          await mount("receipt");
          if (structured)
            expect(
              screen.getByRole("button", { name: "使用原编号和版本重试同步" }),
            ).toBeInTheDocument();
          else
            expect(
              screen.queryByRole("button", {
                name: "使用原编号和版本重试同步",
              }),
            ).not.toBeInTheDocument();
          expect(readIntent(profile)).toEqual(intent);
          expect(businessPosts()).toHaveLength(0);
          await act(async () => {
            await vi.advanceTimersByTimeAsync(2000);
          });
          expect(calls("receipt")).toHaveLength(structured ? 1 : 2);
          expect(readIntent(profile)).toEqual(intent);
          expect(businessPosts()).toHaveLength(0);
        },
      );
      it("unknown sync response preserves one original intent and only performs receipt reads", async () => {
        reply = async (url, init) =>
          init.method === "POST"
            ? new Response("html", { status: 401 })
            : url.startsWith(`${prefix}/receipt`)
              ? new Response("html", { status: 404 })
              : response(absent());
        const changed = vi.fn();
        await mount("observation", changed);
        fireEvent.change(screen.getByLabelText("操作者当前密码"), {
          target: { value: proof.actorPassword },
        });
        fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), {
          target: { value: proof.totpCode },
        });
        click("提交目录同步");
        await settle();
        expect(businessPosts()).toHaveLength(1);
        expect(changed).not.toHaveBeenCalled();
        const posted = JSON.parse(businessPosts()[0][1]!.body as string),
          saved = readIntent(profile);
        expect(saved).toEqual({
          ...intent,
          idempotencyKey: posted.idempotencyKey,
        });
        expect(
          screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
        ).not.toBeInTheDocument();
        click("查询原同步回执");
        await settle();
        expect(businessPosts()).toHaveLength(1);
        expect(readIntent(profile)).toEqual(saved);
        for (const [url] of calls("receipt"))
          expect(
            new URL(String(url), "http://synthetic.invalid").searchParams.get(
              "idempotencyKey",
            ),
          ).toBe(posted.idempotencyKey);
        expect(JSON.stringify(saved)).not.toContain(proof.actorPassword);
      });
    });

    describe("bounded decoding, cleanup and interruption remain stronger than fallback", () => {
      for (const [kind, read] of reads) {
        for (const status of [401, 403]) {
          it(`${kind}: invalid UTF8 ${status} is classified without unbounded decoding`, async () => {
            const body = new Response(new Uint8Array([0xc3, 0x28]), { status });
            const json = vi.spyOn(body, "json"),
              text = vi.spyOn(body, "text");
            reply = async () => body;
            await expect(read()).rejects.toMatchObject({
              code: status === 401 ? "unauthenticated" : "forbidden",
              status,
            });
            expect(json).not.toHaveBeenCalled();
            expect(text).not.toHaveBeenCalled();
            expect(body.body!.locked).toBe(false);
          });
          it(`${kind}: absent body ${status} is classified`, async () => {
            reply = async () => new Response(null, { status });
            await expect(read()).rejects.toMatchObject({
              code: status === 401 ? "unauthenticated" : "forbidden",
              status,
            });
          });
          it.each(["invalid", String(directoryV2BodyLimit + 1)])(
            `${kind}: ${status} invalid advertised limit %s is not authority fallback`,
            async (length) => {
              const cancel = vi.fn();
              const body = new Response(new ReadableStream({ cancel }), {
                status,
                headers: { "Content-Length": length },
              });
              reply = async () => body;
              await expect(read()).rejects.toMatchObject({
                code: "directory_limit_exceeded",
                status,
              });
              expect(cancel).toHaveBeenCalledTimes(1);
              expect(body.body!.locked).toBe(false);
            },
          );
          it(`${kind}: streamed ${status} over budget cancels and keeps limit error`, async () => {
            const cancel = vi.fn();
            let pulls = 0;
            const body = new Response(
              new ReadableStream({
                pull(c) {
                  pulls++;
                  c.enqueue(new Uint8Array(directoryV2BodyLimit + 1));
                },
                cancel,
              }),
              { status, headers: { "Content-Length": "0" } },
            );
            reply = async () => body;
            await expect(read()).rejects.toMatchObject({
              code: "directory_limit_exceeded",
              status,
            });
            expect(cancel).toHaveBeenCalledTimes(1);
            expect(pulls).toBeLessThanOrEqual(2);
            expect(body.body!.locked).toBe(false);
          });
          it(`${kind}: ${status} abort while reading remains AbortError and releases reader`, async () => {
            const controller = new AbortController(),
              cancel = vi.fn();
            const body = new Response(
              new ReadableStream({
                start(c) {
                  c.enqueue(new TextEncoder().encode("{"));
                },
                cancel,
              }),
              { status },
            );
            reply = async () => body;
            const pending = read(controller.signal),
              check = expect(pending).rejects.toMatchObject({
                name: "AbortError",
              });
            await settle();
            controller.abort();
            await check;
            expect(cancel).toHaveBeenCalledTimes(1);
            expect(body.body!.locked).toBe(false);
          });
          it(`${kind}: ${status} a reader-acquisition error is not reclassified`, async () => {
            const body = new Response("synthetic", { status }),
              reader = body.body!.getReader();
            reply = async () => body;
            try {
              await expect(read()).rejects.toBeInstanceOf(TypeError);
            } finally {
              reader.releaseLock();
              await body.body!.cancel();
            }
          });
        }
      }
      it.each([401, 403])(
        "exact body cap %s keeps the structured code, not fallback",
        async (status) => {
          const json = JSON.stringify({ error: "password_change_required" }),
            text = json + " ".repeat(directoryV2BodyLimit - json.length);
          reply = async () =>
            new Response(text, {
              status,
              headers: { "Content-Length": String(directoryV2BodyLimit) },
            });
          await expect(
            api.observation(query, profile.ID, signal()),
          ).rejects.toMatchObject({ code: "password_change_required", status });
        },
      );
      for (const [kind, write] of writes)
        it.each([401, 403])(
          `${kind}: unreadable over-budget %s keeps original non-authority classification`,
          async (status) => {
            reply = async () =>
              new Response("body", {
                status,
                headers: { "Content-Length": String(directoryV2BodyLimit + 1) },
              });
            const error = await write().catch((e) => e);
            expect(error).toMatchObject({
              code: "directory_limit_exceeded",
              status,
            });
            expect(uncertain(error)).toBe(false);
          },
        );
    });
  });
