// Actual directory-v1 API and Workspace, synthetic transport only.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import DirectoryWorkspace from "./DirectoryWorkspace";
import { directoryAPI, uncertainDirectoryError } from "./directory-api";
import {
  discardDirectoryIntent,
  readDirectoryIntent,
  saveDirectoryIntent,
  type DirectoryIntent,
} from "./directory-intent";
import type { SourceChoice } from "./domain-selection-api";
import type { Task } from "./task-api";

const profile: Profile = {
  ID: 315,
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
const intent: DirectoryIntent = {
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
  available: false,
  list: [],
  page: { pageIdx: 1, pageSize: 50, total: 0, totalPage: 0 },
});
const task = (): Task => ({
  taskUUID: "synthetic-task",
  taskName: "domain.directory_read",
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
const response = (data: unknown, status = 200, actor = String(profile.ID)) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "X-ADTR-User-ID": actor, "Content-Type": "application/json" },
  });
const signal = () => new AbortController().signal;
const reads = [
  [
    "observation",
    (s = signal()) => directoryAPI.observation(query, profile.ID, s),
    absent,
  ],
  [
    "receipt",
    (s = signal()) => directoryAPI.receipt(intent, profile.ID, s),
    () => ({ task: task() }),
  ],
  [
    "task",
    (s = signal()) =>
      directoryAPI.task(choice.domainId, "synthetic-task", profile.ID, s),
    () => ({ task: task() }),
  ],
] as const;
const proof = { actorPassword: "synthetic-proof", totpCode: "123456" };
const writes = [
  [
    "sync",
    () =>
      directoryAPI.sync(intent, proof, profile.csrfToken, profile.ID, signal()),
  ],
  [
    "cancel",
    () =>
      directoryAPI.cancel(
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
      ([url]) => String(url).split("?")[0] === `/api/directory/${kind}`,
    );
const businessPosts = () =>
  vi
    .mocked(fetch)
    .mock.calls.filter(
      ([url, init]) =>
        String(url).startsWith("/api/directory/") && init?.method === "POST",
    );
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
async function settle() {
  await act(async () => {
    for (let i = 0; i < 25; i++) await Promise.resolve();
  });
}
async function mount(
  kind: "observation" | "receipt" | "task",
  changed = vi.fn(),
) {
  if (kind !== "observation")
    saveDirectoryIntent(profile, {
      ...intent,
      ...(kind === "task" ? { taskUUID: "synthetic-task" } : {}),
    });
  const root = render(
    <DirectoryWorkspace profile={profile} sessionChanged={changed} />,
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
  discardDirectoryIntent(profile);
  sessionStorage.clear();
  localStorage.clear();
  reply = async (url) =>
    response(
      url.startsWith("/api/directory/observation")
        ? absent()
        : { task: task() },
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
  discardDirectoryIntent(profile);
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
          ({
            ok: false,
            status,
            json: async () => {
              throw Error("synthetic body interruption");
            },
          }) as unknown as Response;
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
      `${kind}: malformed %s remains original invalid_response/status0`,
      async (status) => {
        reply = async () => new Response("html", { status });
        await expect(read()).rejects.toMatchObject({
          code: "invalid_response",
          status: 0,
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
      reply = async () => response(valid(), 200, "316");
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
          expect(error).toMatchObject({ code: "invalid_response", status: 0 });
          expect(uncertainDirectoryError(error)).toBe(true);
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
      expect(uncertainDirectoryError(error)).toBe(false);
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
          expect(readDirectoryIntent(profile)).toEqual({
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
          url.startsWith("/api/directory/observation")
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
        expect(readDirectoryIntent(profile)?.taskUUID).toBe("synthetic-task");
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
          screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
        ).not.toBeInTheDocument();
      expect(readDirectoryIntent(profile)).toEqual(intent);
      expect(businessPosts()).toHaveLength(0);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });
      expect(calls("receipt")).toHaveLength(structured ? 1 : 2);
      expect(readDirectoryIntent(profile)).toEqual(intent);
      expect(businessPosts()).toHaveLength(0);
    },
  );
  it("unknown sync response preserves one original intent and only performs receipt reads", async () => {
    reply = async (url, init) =>
      init.method === "POST"
        ? new Response("html", { status: 401 })
        : url.startsWith("/api/directory/receipt")
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
      saved = readDirectoryIntent(profile);
    expect(saved).toEqual({ ...intent, idempotencyKey: posted.idempotencyKey });
    expect(
      screen.queryByRole("button", { name: "使用原编号和版本重试同步" }),
    ).not.toBeInTheDocument();
    click("查询原同步回执");
    await settle();
    expect(businessPosts()).toHaveLength(1);
    expect(readDirectoryIntent(profile)).toEqual(saved);
    for (const [url] of calls("receipt"))
      expect(
        new URL(String(url), "http://synthetic.invalid").searchParams.get(
          "idempotencyKey",
        ),
      ).toBe(posted.idempotencyKey);
    expect(JSON.stringify(saved)).not.toContain(proof.actorPassword);
  });
});
