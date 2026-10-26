import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import DirectoryV4Workspace from "./DirectoryV4Workspace";
import { createDirectoryV4RecoveryContext } from "./directory-v4-controls-api";
import { ApiError, request as apiRequest, type Profile } from "./api";
import { readFileSync, readdirSync } from "node:fs";
import { readSourceIntent, saveSourceIntent } from "./domain-source-intent";
import {
  listenForSessionInvalidation,
  SESSION_INVALIDATION_STORAGE,
} from "./session-invalidation";

class Channel {
  static instances: Channel[] = [];
  listeners = new Set<(e: MessageEvent) => void>();
  postMessage = vi.fn();
  close = vi.fn();
  constructor(public name: string) {
    Channel.instances.push(this);
  }
  addEventListener(_name: string, fn: (e: MessageEvent) => void) {
    this.listeners.add(fn);
  }
  removeEventListener(_name: string, fn: (e: MessageEvent) => void) {
    this.listeners.delete(fn);
  }
  emit(data: unknown) {
    for (const fn of this.listeners) fn({ data } as MessageEvent);
  }
}
const profile: Profile = {
  ID: 1,
  username: "first-actor",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: false,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 1,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "private-first-csrf",
};
const response = (body: unknown, status = 200) =>
  ({ ok: status < 400, status, json: async () => body }) as Response;
function deferred() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
function deferredValue<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
// Return headers immediately, but keep the original Response.json pending.
// Ignoring abort deliberately models a late body that still needs epoch fencing.
function heldBody(status = 200, abortAware = false) {
  const body = deferredValue<unknown>();
  const json = vi.fn(() => body.promise);
  let signal!: AbortSignal;
  const fetch = async (_url: unknown, init: RequestInit) => {
    signal = init.signal!;
    if (abortAware)
      signal.addEventListener(
        "abort",
        () => body.reject(new DOMException("aborted", "AbortError")),
        { once: true },
      );
    return { ok: status < 400, status, json } as unknown as Response;
  };
  return { body, json, fetch, signal: () => signal };
}
type PassiveEvent = "focus" | "visibilitychange";
function passive(event: PassiveEvent) {
  fireEvent(event === "focus" ? window : document, new Event(event));
}
function activationBoundary(event: "hidden" | "blur" | "pagehide") {
  if (event === "hidden") {
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "hidden",
    });
    fireEvent(document, new Event("visibilitychange"));
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
  } else fireEvent(window, new Event(event));
}
const secondProfile: Profile = {
  ...profile,
  ID: 2,
  username: "second-actor",
  csrfToken: "second-csrf",
};
function expectGated() {
  const main = document.querySelector("main")!;
  expect(main).toHaveAttribute("hidden");
  expect(main).toHaveAttribute("inert");
  expect(main).not.toBeVisible();
}
function expectActor(username: string) {
  for (const node of screen.getAllByText(username, { exact: true }))
    expect(node).toBeVisible();
}
function expectPurpose(signal: AbortSignal, marked: boolean) {
  const calls = fetcher.mock.calls.filter(([, init]) => init.signal === signal);
  expect(calls).toHaveLength(1);
  expect(calls[0][0]).toBe("/api/auth/me");
  expect(calls[0][1]).toEqual({
    method: "GET",
    credentials: "same-origin",
    cache: "no-store",
    signal,
    headers: marked ? { "x-adtr-identity-purpose": "app-passive-session" } : {},
  });
}
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  Channel.instances = [];
  vi.stubGlobal("BroadcastChannel", Channel);
  fetcher = vi.fn(async () => response(profile));
  vi.stubGlobal("fetch", fetcher);
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    value: "visible",
  });
  localStorage.clear();
  window.history.replaceState({}, "", "#account");
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
const nonce = (n: number) => n.toString(16).padStart(32, "0");
async function start() {
  const mounted = render(<App />);
  await screen.findByRole("heading", { name: "账户概览" });
  return mounted;
}
async function passwordDraft() {
  fireEvent.click(screen.getByRole("button", { name: "修改密码" }));
  await screen.findByLabelText("当前密码", { exact: true });
  fireEvent.change(screen.getByLabelText("当前密码", { exact: true }), {
    target: { value: "private-draft" },
  });
}

describe("nonce-only session notifications", () => {
  it("deduplicates channels, rejects structured identity and removes listeners", () => {
    const invalidated = vi.fn();
    const subscription = listenForSessionInvalidation(invalidated);
    const channel = Channel.instances[0];
    channel.emit({ userID: 1, token: "secret" });
    channel.emit("bad");
    channel.emit(nonce(1));
    window.dispatchEvent(
      new StorageEvent("storage", {
        key: SESSION_INVALIDATION_STORAGE,
        newValue: nonce(1),
        storageArea: localStorage,
      }),
    );
    expect(invalidated).toHaveBeenCalledTimes(1);
    subscription.publish();
    expect(channel.postMessage).toHaveBeenCalledWith(
      expect.stringMatching(/^[a-f0-9]{32}$/),
    );
    expect(localStorage.length).toBe(0);
    subscription.close();
    subscription.close();
    channel.emit(nonce(2));
    expect(channel.close).toHaveBeenCalledTimes(1);
    expect(invalidated).toHaveBeenCalledTimes(1);
  });
  it("uses storage when BroadcastChannel is unavailable and tolerates write denial", () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    const invalidated = vi.fn();
    const subscription = listenForSessionInvalidation(invalidated);
    window.dispatchEvent(
      new StorageEvent("storage", {
        key: SESSION_INVALIDATION_STORAGE,
        newValue: nonce(2),
        storageArea: localStorage,
      }),
    );
    expect(invalidated).toHaveBeenCalledTimes(1);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("unavailable");
    });
    expect(() => subscription.publish()).not.toThrow();
    subscription.close();
  });
});

describe("live session UI boundaries", () => {
  it("preserves an unauthenticated login draft when focus confirms no session", async () => {
    fetcher.mockImplementation(async () =>
      response({ error: "unauthenticated" }, 401),
    );
    render(<App />);
    await screen.findByRole("heading", { name: "登录账户" });
    fireEvent.change(screen.getByLabelText("用户名", { exact: true }), {
      target: { value: "draft-user" },
    });
    fireEvent.change(screen.getByLabelText("密码", { exact: true }), {
      target: { value: "unsubmitted-login-password" },
    });
    fireEvent.focus(window);
    await waitFor(() =>
      expect(screen.getByLabelText("密码", { exact: true })).toBeVisible(),
    );
    expect(screen.getByLabelText("用户名", { exact: true })).toHaveValue(
      "draft-user",
    );
    expect(screen.getByLabelText("密码", { exact: true })).toHaveValue(
      "unsubmitted-login-password",
    );
  });
  it("keeps old identity hidden when header navigation supersedes a failing focus verification", async () => {
    await start();
    const earlier = deferred();
    fetcher.mockImplementationOnce(() => earlier.promise);
    fireEvent.focus(window);
    const signal = fetcher.mock.calls.at(-1)![1].signal;
    fetcher.mockImplementationOnce(async () =>
      response({ error: "internal" }, 500),
    );
    fireEvent.click(screen.getByRole("link", { name: /ADTR/ }));
    expect(signal.aborted).toBe(true);
    await screen.findByRole("heading", { name: "登录账户" });
    await act(async () => earlier.resolve(response(profile)));
    expect(
      screen.queryByText("first-actor", { exact: true }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "登录账户" })).toBeVisible();
  });
  it("ignores hidden visibility events and resets drafts after same-actor session rotation", async () => {
    await start();
    await passwordDraft();
    const before = fetcher.mock.calls.length;
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "hidden",
    });
    fireEvent(document, new Event("visibilitychange"));
    expect(fetcher).toHaveBeenCalledTimes(before);
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
    fetcher.mockImplementationOnce(async () =>
      response({ ...profile, csrfToken: "rotated-csrf" }),
    );
    fireEvent(document, new Event("visibilitychange"));
    await screen.findByRole("heading", { name: "账户概览" });
    expect(
      screen.queryByLabelText("当前密码", { exact: true }),
    ).not.toBeInTheDocument();
    expect(Channel.instances[0].postMessage).not.toHaveBeenCalled();
  });
  it("hides old proofs before notice revalidation and never restores them on failure", async () => {
    await start();
    await passwordDraft();
    const held = deferred();
    fetcher.mockImplementationOnce(() => held.promise);
    act(() => Channel.instances[0].emit(nonce(3)));
    expect(
      screen.queryByLabelText("当前密码", { exact: true }),
    ).not.toBeInTheDocument();
    await act(async () => held.resolve(response({ error: "internal" }, 500)));
    expect(
      await screen.findByRole("heading", { name: "登录账户" }),
    ).toBeVisible();
    expect(
      screen.queryByText("first-actor", { exact: true }),
    ).not.toBeInTheDocument();
  });
  it("gates focus verification while preserving the same-session draft", async () => {
    await start();
    await passwordDraft();
    const input = screen.getByLabelText("当前密码", { exact: true });
    const count = fetcher.mock.calls.length;
    fireEvent.focus(input);
    expect(fetcher).toHaveBeenCalledTimes(count);
    const held = deferred();
    fetcher.mockImplementationOnce(() => held.promise);
    fireEvent.focus(window);
    expect(input).not.toBeVisible();
    expect(input.closest("main")).toHaveAttribute("inert");
    await act(async () => held.resolve(response(profile)));
    expect(input).toBeVisible();
    expect(input).toHaveValue("private-draft");
  });
  it("changes actor on visible-tab fallback even without either notification transport", async () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("unavailable");
    });
    await start();
    await passwordDraft();
    fetcher.mockImplementationOnce(async () =>
      response({
        ...profile,
        ID: 2,
        username: "second-actor",
        csrfToken: "second-csrf",
      }),
    );
    fireEvent(document, new Event("visibilitychange"));
    const currentActor = await screen.findAllByText("second-actor", {
      exact: true,
    });
    for (const node of currentActor) expect(node).toBeVisible();
    expect(
      screen.queryByLabelText("当前密码", { exact: true }),
    ).not.toBeInTheDocument();
  });
  it("ignores a delayed earlier identity response after a newer notification", async () => {
    await start();
    const old = deferred();
    const next = deferred();
    fetcher
      .mockImplementationOnce(() => old.promise)
      .mockImplementationOnce(() => next.promise);
    act(() => Channel.instances[0].emit(nonce(4)));
    const oldSignal = fetcher.mock.calls.at(-1)![1].signal;
    act(() => Channel.instances[0].emit(nonce(5)));
    expect(oldSignal.aborted).toBe(true);
    await act(async () =>
      next.resolve(
        response({
          ...profile,
          ID: 2,
          username: "second-actor",
          csrfToken: "second-csrf",
        }),
      ),
    );
    await act(async () => old.resolve(response(profile)));
    expect(
      screen.getAllByText("second-actor", { exact: true }).length,
    ).toBeGreaterThan(0);
    expect(
      screen.queryByText("first-actor", { exact: true }),
    ).not.toBeInTheDocument();
  });
  it("fails closed on focus401 without deleting uncertain actor-bound recovery data", async () => {
    await start();
    const intent = {
      operation: "detach" as const,
      domainId: "a".repeat(24),
      expectedRevision: "1",
      expectedConnectionCredentialGeneration: "1",
      idempotencyKey: "synthetic-intent-key",
    };
    saveSourceIntent(profile, intent);
    fetcher.mockImplementationOnce(async () =>
      response({ error: "unauthenticated" }, 401),
    );
    fireEvent.focus(window);
    await screen.findByRole("heading", { name: "登录账户" });
    expect(readSourceIntent({ ...profile, csrfToken: "renewed-csrf" })).toEqual(
      intent,
    );
    expect(
      readSourceIntent({ ...profile, ID: 2, username: "second-actor" }),
    ).toBeNull();
  });
  it("broadcasts confirmed logout without credentials and closes pending verification on unmount", async () => {
    const mounted = await start();
    const channel = Channel.instances[0];
    fetcher.mockImplementationOnce(async () => response({ result: "SUCCESS" }));
    fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
    await waitFor(() => expect(channel.postMessage).toHaveBeenCalledTimes(1));
    expect(JSON.stringify(channel.postMessage.mock.calls)).not.toContain(
      "private-first-csrf",
    );
    const held = deferred();
    fetcher.mockImplementationOnce(() => held.promise);
    fireEvent.focus(window);
    const signal = fetcher.mock.calls.at(-1)![1].signal;
    mounted.unmount();
    expect(signal.aborted).toBe(true);
    const count = fetcher.mock.calls.length;
    fireEvent.focus(window);
    channel.emit(nonce(6));
    expect(fetcher).toHaveBeenCalledTimes(count);
    await act(async () => held.resolve(response(profile)));
  });
});

describe("passive identity checks share only their current activation", () => {
  it.each<[PassiveEvent, PassiveEvent]>([
    ["focus", "visibilitychange"],
    ["visibilitychange", "focus"],
    ["focus", "focus"],
    ["visibilitychange", "visibilitychange"],
  ])(
    "keeps the original 200 body for overlapping %s then %s",
    async (first, next) => {
      await start();
      await passwordDraft();
      const input = screen.getByLabelText("当前密码", { exact: true });
      const held = heldBody(200, true);
      const count = fetcher.mock.calls.length;
      fetcher.mockImplementationOnce(held.fetch);
      passive(first);
      await waitFor(() => expect(held.json).toHaveBeenCalledTimes(1));
      expectGated();
      passive(next);
      expect(fetcher).toHaveBeenCalledTimes(count + 1);
      expect(held.signal().aborted).toBe(false);
      expectPurpose(held.signal(), true);
      expect(held.json).toHaveBeenCalledTimes(1);
      expectGated();
      await act(async () => held.body.resolve(profile));
      expect(input).toBeVisible();
      expect(input).toHaveValue("private-draft");
      expect(document.querySelector("main")).not.toHaveAttribute("inert");
      // A settled check is never an authorization cache for the next event.
      fetcher.mockImplementationOnce(async () => response(secondProfile));
      passive(next);
      await screen.findAllByText("second-actor", { exact: true });
      expect(fetcher).toHaveBeenCalledTimes(count + 2);
      expectActor("second-actor");
      expect(
        screen.queryByLabelText("当前密码", { exact: true }),
      ).not.toBeInTheDocument();
    },
  );

  it.each<PassiveEvent>(["focus", "visibilitychange"])(
    "retains the single %s request through its body",
    async (event) => {
      await start();
      const held = heldBody(200, true);
      const count = fetcher.mock.calls.length;
      fetcher.mockImplementationOnce(held.fetch);
      passive(event);
      await waitFor(() => expect(held.json).toHaveBeenCalledTimes(1));
      expect(held.signal().aborted).toBe(false);
      expectPurpose(held.signal(), true);
      expect(fetcher).toHaveBeenCalledTimes(count + 1);
      expectGated();
      await act(async () => held.body.resolve(secondProfile));
      expectActor("second-actor");
    },
  );

  it.each<"hidden" | "blur" | "pagehide">(["hidden", "blur", "pagehide"])(
    "requires fresh identity after %s without a session notification",
    async (boundary) => {
      await start();
      await passwordDraft();
      const old = heldBody();
      const current = heldBody();
      const count = fetcher.mock.calls.length;
      fetcher
        .mockImplementationOnce(old.fetch)
        .mockImplementationOnce(current.fetch);
      passive("focus");
      await waitFor(() => expect(old.json).toHaveBeenCalledTimes(1));
      activationBoundary(boundary);
      // Losing activation only revokes joining; it adds no abort policy.
      expect(old.signal().aborted).toBe(false);
      expect(fetcher).toHaveBeenCalledTimes(count + 1);
      passive(boundary === "hidden" ? "visibilitychange" : "focus");
      await waitFor(() => expect(current.json).toHaveBeenCalledTimes(1));
      expect(old.signal().aborted).toBe(true);
      expect(current.signal().aborted).toBe(false);
      expect(fetcher).toHaveBeenCalledTimes(count + 2);
      expectGated();
      await act(async () => current.body.resolve(secondProfile));
      await act(async () => old.body.resolve(profile));
      expectActor("second-actor");
      expect(
        screen.queryByText("first-actor", { exact: true }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByLabelText("当前密码", { exact: true }),
      ).not.toBeInTheDocument();
      expect(Channel.instances[0].postMessage).not.toHaveBeenCalled();
    },
  );

  it.each(["resolve", "reject"] as const)(
    "does not let an old %s/finally clear a newer passive slot or gate",
    async (settlement) => {
      await start();
      const old = heldBody();
      const current = heldBody();
      const count = fetcher.mock.calls.length;
      fetcher
        .mockImplementationOnce(old.fetch)
        .mockImplementationOnce(current.fetch);
      passive("focus");
      await waitFor(() => expect(old.json).toHaveBeenCalledTimes(1));
      activationBoundary("blur");
      passive("focus");
      await waitFor(() => expect(current.json).toHaveBeenCalledTimes(1));
      await act(async () => {
        if (settlement === "resolve") old.body.resolve(secondProfile);
        else old.body.reject(new Error("late old body failure"));
      });
      expectGated();
      passive("visibilitychange");
      expect(fetcher).toHaveBeenCalledTimes(count + 2);
      expect(current.signal().aborted).toBe(false);
      expectGated();
      await act(async () => current.body.resolve(profile));
      expectActor("first-actor");
      expect(
        screen.queryByText("second-actor", { exact: true }),
      ).not.toBeInTheDocument();
    },
  );

  it("does not treat Element focus or blur as activation changes", async () => {
    await start();
    await passwordDraft();
    const input = screen.getByLabelText("当前密码", { exact: true });
    const held = heldBody();
    const count = fetcher.mock.calls.length;
    fetcher.mockImplementationOnce(held.fetch);
    passive("focus");
    await waitFor(() => expect(held.json).toHaveBeenCalledTimes(1));
    fireEvent(input, new FocusEvent("blur", { bubbles: true }));
    fireEvent(input, new FocusEvent("focus", { bubbles: true }));
    passive("visibilitychange");
    expect(fetcher).toHaveBeenCalledTimes(count + 1);
    expect(held.signal().aborted).toBe(false);
    expectPurpose(held.signal(), true);
    await act(async () => held.body.resolve(profile));
    expect(input).toHaveValue("private-draft");
    expect(input).toBeVisible();
  });

  it.each(["401", "body failure"] as const)(
    "fails closed after a joined %s and permits the next fresh check",
    async (failure) => {
      await start();
      await passwordDraft();
      const held = heldBody(failure === "401" ? 401 : 200);
      const count = fetcher.mock.calls.length;
      fetcher.mockImplementationOnce(held.fetch);
      passive("focus");
      await waitFor(() => expect(held.json).toHaveBeenCalledTimes(1));
      passive("visibilitychange");
      expect(fetcher).toHaveBeenCalledTimes(count + 1);
      await act(async () => {
        if (failure === "401") held.body.resolve({ error: "unauthenticated" });
        else held.body.reject(new Error("invalid JSON body"));
      });
      expect(screen.getByRole("heading", { name: "登录账户" })).toBeVisible();
      expect(
        screen.queryByText("first-actor", { exact: true }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByLabelText("当前密码", { exact: true }),
      ).not.toBeInTheDocument();
      expect(document.querySelector("main")).not.toHaveAttribute("hidden");
      fetcher.mockImplementationOnce(async () => response(secondProfile));
      passive("focus");
      await screen.findAllByText("second-actor", { exact: true });
      expect(fetcher).toHaveBeenCalledTimes(count + 2);
      expectActor("second-actor");
    },
  );

  it("shares a pending network request but does not retain its failure", async () => {
    await start();
    const pending = deferredValue<Response>();
    const count = fetcher.mock.calls.length;
    fetcher.mockImplementationOnce(() => pending.promise);
    passive("visibilitychange");
    const signal = fetcher.mock.calls.at(-1)![1].signal as AbortSignal;
    passive("focus");
    expect(fetcher).toHaveBeenCalledTimes(count + 1);
    expect(signal.aborted).toBe(false);
    expectGated();
    await act(async () =>
      pending.reject(new TypeError("synthetic network failure")),
    );
    expect(screen.getByRole("heading", { name: "登录账户" })).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("连接中断");
    fetcher.mockImplementationOnce(async () => response(secondProfile));
    passive("focus");
    await screen.findAllByText("second-actor", { exact: true });
    expect(fetcher).toHaveBeenCalledTimes(count + 2);
    expectActor("second-actor");
  });
});

describe("authoritative identity changes still preempt passive work", () => {
  it.each([
    ["changed actor", secondProfile],
    [
      "same actor privilege and CSRF rotation",
      { ...profile, priv: 2, csrfToken: "rotated-csrf" },
    ],
  ] as const)(
    "lets a real notification preempt the pending body for %s",
    async (_label, updated) => {
      await start();
      await passwordDraft();
      const old = heldBody();
      const fresh = heldBody();
      const count = fetcher.mock.calls.length;
      fetcher
        .mockImplementationOnce(old.fetch)
        .mockImplementationOnce(fresh.fetch);
      passive("focus");
      await waitFor(() => expect(old.json).toHaveBeenCalledTimes(1));
      act(() => Channel.instances[0].emit(nonce(20)));
      await waitFor(() => expect(fresh.json).toHaveBeenCalledTimes(1));
      expect(old.signal().aborted).toBe(true);
      expect(fetcher).toHaveBeenCalledTimes(count + 2);
      expect(
        screen.queryByLabelText("当前密码", { exact: true }),
      ).not.toBeInTheDocument();
      await act(async () => fresh.body.resolve(updated));
      await act(async () => old.body.resolve(profile));
      expectActor(updated.username);
      expect(
        screen.queryByLabelText("当前密码", { exact: true }),
      ).not.toBeInTheDocument();
      expect(Channel.instances[0].postMessage).not.toHaveBeenCalled();
    },
  );

  it("does not make the initial refresh joinable by passive events", async () => {
    const initial = heldBody();
    const current = heldBody();
    fetcher
      .mockImplementationOnce(initial.fetch)
      .mockImplementationOnce(current.fetch);
    render(<App />);
    await waitFor(() => expect(initial.json).toHaveBeenCalledTimes(1));
    passive("focus");
    await waitFor(() => expect(current.json).toHaveBeenCalledTimes(1));
    expect(initial.signal().aborted).toBe(true);
    expectPurpose(initial.signal(), false);
    expectPurpose(current.signal(), true);
    passive("visibilitychange");
    expect(fetcher).toHaveBeenCalledTimes(2);
    await act(async () => initial.body.resolve(profile));
    expectGated();
    expect(
      screen.queryByText("first-actor", { exact: true }),
    ).not.toBeInTheDocument();
    await act(async () => current.body.resolve(secondProfile));
    expectActor("second-actor");
  });

  it.each(["navigation", "notification"] as const)(
    "keeps %s refresh fresh and not joinable by a subsequent passive event",
    async (forced) => {
      await start();
      const old = heldBody();
      const fresh = heldBody();
      const current = heldBody();
      const count = fetcher.mock.calls.length;
      fetcher
        .mockImplementationOnce(old.fetch)
        .mockImplementationOnce(fresh.fetch)
        .mockImplementationOnce(current.fetch);
      passive("focus");
      await waitFor(() => expect(old.json).toHaveBeenCalledTimes(1));
      if (forced === "navigation")
        fireEvent.click(screen.getByRole("link", { name: /ADTR/ }));
      else act(() => Channel.instances[0].emit(nonce(21)));
      await waitFor(() => expect(fresh.json).toHaveBeenCalledTimes(1));
      expect(old.signal().aborted).toBe(true);
      passive("visibilitychange");
      await waitFor(() => expect(current.json).toHaveBeenCalledTimes(1));
      expect(fresh.signal().aborted).toBe(true);
      expectPurpose(fresh.signal(), false);
      expectPurpose(current.signal(), true);
      await act(async () => {
        old.body.resolve(profile);
        fresh.body.resolve(profile);
      });
      expectGated();
      passive("focus");
      expect(fetcher).toHaveBeenCalledTimes(count + 3);
      expect(current.signal().aborted).toBe(false);
      await act(async () => current.body.resolve(secondProfile));
      expectActor("second-actor");
    },
  );

  it("keeps explicit domain-history verification fresh rather than sharing passive or forced bodies", async () => {
    const queue: ReturnType<typeof heldBody>[] = [];
    fetcher.mockImplementation(async (url: string, init: RequestInit) => {
      if (url === "/api/auth/me")
        return queue.shift()?.fetch(url, init) ?? response(profile);
      return response({ error: "forbidden" }, 403);
    });
    window.history.replaceState({}, "", "#domains");
    render(<App />);
    await screen.findAllByText("first-actor", { exact: true });
    const meCalls = () =>
      fetcher.mock.calls.filter(([url]) => url === "/api/auth/me").length;
    const count = meCalls();
    const old = heldBody();
    const history = heldBody();
    const nextHistory = heldBody();
    const current = heldBody();
    queue.push(old, history, nextHistory, current);
    passive("focus");
    await waitFor(() => expect(old.json).toHaveBeenCalledTimes(1));
    fireEvent(window, new PopStateEvent("popstate"));
    await waitFor(() => expect(history.json).toHaveBeenCalledTimes(1));
    expect(old.signal().aborted).toBe(true);
    fireEvent(window, new HashChangeEvent("hashchange"));
    await waitFor(() => expect(nextHistory.json).toHaveBeenCalledTimes(1));
    expect(history.signal().aborted).toBe(true);
    passive("visibilitychange");
    await waitFor(() => expect(current.json).toHaveBeenCalledTimes(1));
    expect(nextHistory.signal().aborted).toBe(true);
    expectPurpose(old.signal(), true);
    expectPurpose(history.signal(), false);
    expectPurpose(nextHistory.signal(), false);
    expectPurpose(current.signal(), true);
    await act(async () => {
      old.body.resolve(profile);
      history.body.resolve(profile);
      nextHistory.body.resolve(profile);
    });
    expectGated();
    passive("focus");
    expect(meCalls()).toBe(count + 4);
    expect(current.signal().aborted).toBe(false);
    await act(async () => current.body.resolve(secondProfile));
    expectActor("second-actor");
  });

  it("lets logout preempt a passive body without changing mutation request or busy semantics", async () => {
    await start();
    const logoutButton = screen.getByRole("button", { name: "退出登录" });
    const old = heldBody();
    const logout = heldBody();
    fetcher
      .mockImplementationOnce(old.fetch)
      .mockImplementationOnce(logout.fetch);
    passive("focus");
    await waitFor(() => expect(old.json).toHaveBeenCalledTimes(1));
    // Dispatch the existing callback through a retained node to test run's
    // epoch boundary, not to claim that a gated navigation is user-interactive.
    fireEvent.click(logoutButton);
    await waitFor(() => expect(logout.json).toHaveBeenCalledTimes(1));
    expect(old.signal().aborted).toBe(true);
    expect(fetcher.mock.calls.at(-1)).toEqual([
      "/api/auth/logout",
      expect.objectContaining({
        method: "POST",
        body: "{}",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": profile.csrfToken,
        },
      }),
    ]);
    expect(logoutButton).toBeDisabled();
    await act(async () => old.body.resolve(profile));
    expectGated();
    await act(async () => logout.body.resolve({ result: "SUCCESS" }));
    expect(Channel.instances[0].postMessage).toHaveBeenCalledTimes(1);
    expect(
      screen.queryByText("first-actor", { exact: true }),
    ).not.toBeInTheDocument();
    // Preserve the original policy: a later passive event can revalidate after logout.
    fetcher.mockImplementationOnce(async () =>
      response({ error: "unauthenticated" }, 401),
    );
    passive("focus");
    await screen.findByRole("heading", { name: "登录账户" });
  });

  it("retains passive preemption of an in-flight mutation rather than adding a scheduler", async () => {
    await start();
    const mutation = heldBody();
    const current = heldBody();
    const count = fetcher.mock.calls.length;
    fetcher
      .mockImplementationOnce(mutation.fetch)
      .mockImplementationOnce(current.fetch);
    fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
    await waitFor(() => expect(mutation.json).toHaveBeenCalledTimes(1));
    passive("focus");
    await waitFor(() => expect(current.json).toHaveBeenCalledTimes(1));
    expect(mutation.signal().aborted).toBe(true);
    await act(async () => mutation.body.resolve({ result: "SUCCESS" }));
    expect(Channel.instances[0].postMessage).not.toHaveBeenCalled();
    expectGated();
    passive("visibilitychange");
    expect(fetcher).toHaveBeenCalledTimes(count + 2);
    await act(async () => current.body.resolve(secondProfile));
    expectActor("second-actor");
    expect(screen.getByRole("button", { name: "退出登录" })).not.toBeDisabled();
  });

  it("removes every passive listener and fences a pending body on unmount", async () => {
    const windowAdded = vi.spyOn(window, "addEventListener");
    const windowRemoved = vi.spyOn(window, "removeEventListener");
    const documentAdded = vi.spyOn(document, "addEventListener");
    const documentRemoved = vi.spyOn(document, "removeEventListener");
    const mounted = await start();
    const held = heldBody();
    fetcher.mockImplementationOnce(held.fetch);
    passive("focus");
    await waitFor(() => expect(held.json).toHaveBeenCalledTimes(1));
    mounted.unmount();
    expect(held.signal().aborted).toBe(true);
    for (const name of ["focus", "blur", "pagehide"]) {
      const listener = windowAdded.mock.calls.find(
        ([event]) => event === name,
      )![1];
      expect(windowRemoved).toHaveBeenCalledWith(name, listener);
    }
    const listener = documentAdded.mock.calls.find(
      ([event]) => event === "visibilitychange",
    )![1];
    expect(documentRemoved).toHaveBeenCalledWith("visibilitychange", listener);
    const count = fetcher.mock.calls.length;
    activationBoundary("hidden");
    activationBoundary("blur");
    activationBoundary("pagehide");
    passive("focus");
    passive("visibilitychange");
    act(() => Channel.instances[0].emit(nonce(22)));
    await act(async () => held.body.resolve(secondProfile));
    expect(fetcher).toHaveBeenCalledTimes(count);
    expect(document.querySelector("main")).toBeNull();
  });
});

describe("closed App-passive identity request purpose", () => {
  const purpose = "app-passive-session" as const;
  it("preserves every old option when purpose is omitted, including other GETs and CSRF mutations", async () => {
    const signal = new AbortController().signal;
    await apiRequest("/me", signal);
    await apiRequest("/profile", signal);
    await apiRequest("/logout", signal, {}, "synthetic-csrf");
    expect(fetcher.mock.calls).toEqual([
      [
        "/api/auth/me",
        {
          method: "GET",
          credentials: "same-origin",
          cache: "no-store",
          signal,
          headers: {},
        },
      ],
      [
        "/api/auth/profile",
        {
          method: "GET",
          credentials: "same-origin",
          cache: "no-store",
          signal,
          headers: {},
        },
      ],
      [
        "/api/auth/logout",
        {
          method: "POST",
          credentials: "same-origin",
          cache: "no-store",
          signal,
          headers: {
            "Content-Type": "application/json",
            "X-CSRF-Token": "synthetic-csrf",
          },
          body: "{}",
        },
      ],
    ]);
  });
  it("adds only the fixed purpose on a bodyless same-origin no-store GET/me and consumes its original JSON once", async () => {
    const signal = new AbortController().signal;
    const json = vi.fn(async () => profile);
    fetcher.mockImplementationOnce(async () => ({
      ok: true,
      status: 200,
      json,
    }));
    await expect(
      apiRequest("/me", signal, undefined, undefined, purpose),
    ).resolves.toBe(profile);
    expectPurpose(signal, true);
    expect(json).toHaveBeenCalledTimes(1);
  });
  it.each([
    null,
    true,
    false,
    1,
    {},
    [],
    "",
    "APP-PASSIVE-SESSION",
    " app-passive-session",
    "app-passive-session ",
    "app-passive-session, app-passive-session",
    "workspace-session",
  ])("rejects invalid runtime purpose %j before fetch", async (value) => {
    await expect(
      apiRequest(
        "/me",
        new AbortController().signal,
        undefined,
        undefined,
        value as Parameters<typeof apiRequest>[4],
      ),
    ).rejects.toMatchObject({ code: "invalid_input" });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each([
    "/profile",
    "/logout",
    "/me?purpose=passive",
    "/me#passive",
    "https://other.invalid/me",
    "/ME",
  ])("rejects purpose on wrong path %s before fetch", async (path) => {
    await expect(
      apiRequest(
        path,
        new AbortController().signal,
        undefined,
        undefined,
        purpose,
      ),
    ).rejects.toBeInstanceOf(ApiError);
    expect(fetcher).not.toHaveBeenCalled();
  });
  it.each([null, {}, "", false, 0])(
    "rejects a defined body %j before fetch rather than turning a POST into an identity probe",
    async (body) => {
      await expect(
        apiRequest(
          "/me",
          new AbortController().signal,
          body,
          "synthetic-csrf",
          purpose,
        ),
      ).rejects.toMatchObject({ code: "invalid_input" });
      expect(fetcher).not.toHaveBeenCalled();
    },
  );
  it.each(["401", "network", "body"] as const)(
    "preserves original marked-request %s failure semantics without retry",
    async (failure) => {
      if (failure === "401")
        fetcher.mockImplementationOnce(async () =>
          response({ error: "unauthenticated" }, 401),
        );
      else if (failure === "network")
        fetcher.mockRejectedValueOnce(new TypeError("private-network-canary"));
      else
        fetcher.mockImplementationOnce(async () => ({
          ok: true,
          status: 200,
          json: async () => {
            throw new Error("private-body-canary");
          },
        }));
      const signal = new AbortController().signal;
      await expect(
        apiRequest("/me", signal, undefined, undefined, purpose),
      ).rejects.toMatchObject({
        code:
          failure === "401"
            ? "unauthenticated"
            : failure === "network"
              ? "network"
              : "invalid_response",
      });
      expectPurpose(signal, true);
    },
  );
  it("binds the sole production purpose caller to App's explicit passive mode without changing Workspace authority", () => {
    const app = readFileSync("src/App.tsx", "utf8");
    const api = readFileSync("src/api.ts", "utf8");
    const workspace = readFileSync("src/DirectoryV4Workspace.tsx", "utf8");
    expect(app.match(/"app-passive-session"/g)).toHaveLength(1);
    expect(app).toContain(
      'mode === "passive" ? "app-passive-session" : undefined',
    );
    expect(api).toContain('path !== "/me"');
    expect(api).toContain("body !== undefined");
    expect(api).toContain('"x-adtr-identity-purpose": identityPurpose');
    expect(workspace).not.toContain("app-passive-session");
    expect(workspace).not.toContain("x-adtr-identity-purpose");
    expect(workspace).toContain("createDirectoryV4Controls");
    const workspaceAPI = readFileSync(
      "src/directory-v4-controls-api.ts",
      "utf8",
    );
    expect(workspaceAPI).toContain('json("/api/auth/me", signal)');
    expect(workspaceAPI).not.toContain("app-passive-session");
    expect(workspaceAPI).not.toContain("x-adtr-identity-purpose");
    const production = readdirSync("src", { recursive: true })
      .filter(
        (name): name is string =>
          typeof name === "string" &&
          /\.tsx?$/.test(name) &&
          !name.includes(".test."),
      )
      .map((name) => ({ name, source: readFileSync(`src/${name}`, "utf8") }));
    expect(
      production
        .filter(({ source }) => source.includes("app-passive-session"))
        .map(({ name }) => name)
        .sort(),
    ).toEqual(["App.tsx", "api.ts"]);
    expect(
      production
        .filter(({ source }) =>
          source.toLowerCase().includes("x-adtr-identity-purpose"),
        )
        .map(({ name }) => name),
    ).toEqual(["api.ts"]);
  });
});

describe("unmarked Workspace authorization keeps its own cancellation boundary", () => {
  function streamedResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), {
      status,
      headers: {
        "Content-Type": "application/json",
        "Cache-Control": "no-store",
        "X-Content-Type-Options": "nosniff",
        "X-ADTR-User-ID": String(profile.ID),
      },
    });
  }
  function heldStream() {
    let controller!: ReadableStreamDefaultController<Uint8Array>;
    let signal!: AbortSignal;
    const read = vi.fn();
    const abort = vi.fn();
    const fetch = async (url: string, init: RequestInit) => {
      expect(url).toBe("/api/auth/me");
      signal = init.signal as AbortSignal;
      const stream = new ReadableStream<Uint8Array>(
        {
          start(value) {
            controller = value;
          },
          pull() {
            read();
          },
        },
        { highWaterMark: 0 },
      );
      signal.addEventListener(
        "abort",
        () => {
          abort();
          try {
            controller.error(new DOMException("synthetic abort", "AbortError"));
          } catch {
            /* A settled stream cannot be restored by a late abort. */
          }
        },
        { once: true },
      );
      return new Response(stream, {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Cache-Control": "no-store",
          "X-Content-Type-Options": "nosniff",
          "X-ADTR-User-ID": String(profile.ID),
        },
      });
    };
    const resolve = (value: unknown) => {
      controller.enqueue(new TextEncoder().encode(JSON.stringify(value)));
      controller.close();
    };
    return { fetch, read, abort, resolve, signal: () => signal };
  }
  beforeEach(() => {
    vi.stubGlobal("BroadcastChannel", undefined);
    sessionStorage.clear();
    fetcher.mockImplementation(async () =>
      streamedResponse({ error: "forbidden" }, 403),
    );
  });
  it.each([
    ["focus", "visibilitychange"],
    ["visibilitychange", "focus"],
  ] as const)(
    "preserves independent Workspace cancellation for trusted-shaped %s then %s callbacks",
    async (first, second) => {
      const w = vi.spyOn(window, "addEventListener"),
        d = vi.spyOn(document, "addEventListener");
      const view = render(
        <DirectoryV4Workspace
          profile={profile}
          sessionChanged={vi.fn()}
          recoveryContext={createDirectoryV4RecoveryContext()}
          storage={{
            getItem: () => null,
            setItem: () => {},
            removeItem: () => {},
          }}
        />,
      );
      await screen.findByText(/当前权限不可用/);
      const callbacks = {
        focus: w.mock.calls.find(
          ([name]) => name === "focus",
        )![1] as EventListener,
        visibilitychange: d.mock.calls.find(
          ([name]) => name === "visibilitychange",
        )![1] as EventListener,
      };
      const old = heldStream(),
        current = heldStream();
      fetcher
        .mockImplementationOnce(old.fetch)
        .mockImplementationOnce(current.fetch);
      // Invoke actual callbacks with a fixture-shaped trusted event. jsdom does
      // not create a native trusted event; native authority remains a CI gate.
      act(() =>
        callbacks[first]({
          isTrusted: true,
          target: window,
        } as unknown as Event),
      );
      await waitFor(() => expect(old.read).toHaveBeenCalledTimes(1));
      act(() =>
        callbacks[second]({
          isTrusted: true,
          target: window,
        } as unknown as Event),
      );
      await waitFor(() => expect(current.read).toHaveBeenCalledTimes(1));
      expect(old.signal().aborted).toBe(true);
      expect(old.abort).toHaveBeenCalledTimes(1);
      expect(current.signal().aborted).toBe(false);
      for (const [, init] of fetcher.mock.calls)
        expect(new Headers(init.headers).has("x-adtr-identity-purpose")).toBe(
          false,
        );
      view.unmount();
      await act(async () => {
        await Promise.resolve();
      });
      expect(current.signal().aborted).toBe(true);
      expect(current.abort).toHaveBeenCalledTimes(1);
    },
  );
  it.each([true, false])(
    "keeps changed-actor=%s App transition and legitimate unmarked Workspace unmount abort",
    async (changed) => {
      const workspace = heldStream(),
        app = heldStream();
      fetcher
        .mockImplementationOnce(async () => streamedResponse(profile))
        .mockImplementationOnce(workspace.fetch)
        .mockImplementationOnce(app.fetch);
      window.history.replaceState({}, "", "#directory-v4");
      const view = render(<App />);
      await waitFor(() => expect(workspace.read).toHaveBeenCalledTimes(1));
      expect(document.querySelector(".directory-v4-workspace")).not.toBeNull();
      // The untrusted fixture event reaches App but not Workspace's trusted-only
      // branch, isolating legitimate identity-change unmount from dual activation.
      passive("focus");
      await waitFor(() => expect(app.read).toHaveBeenCalledTimes(1));
      expectPurpose(app.signal(), true);
      const workspaceCall = fetcher.mock.calls.find(
        ([, init]) => init.signal === workspace.signal(),
      )!;
      expect(
        new Headers(workspaceCall[1].headers).has("x-adtr-identity-purpose"),
      ).toBe(false);
      expect(workspaceCall[1]).toEqual(
        expect.objectContaining({
          credentials: "same-origin",
          cache: "no-store",
          redirect: "error",
        }),
      );
      expect(workspace.signal()).not.toBe(app.signal());
      expect(workspace.signal().aborted).toBe(false);
      expectGated();
      await act(async () => app.resolve(changed ? secondProfile : profile));
      expect(app.signal().aborted).toBe(false);
      if (changed) {
        expect(document.querySelector(".directory-v4-workspace")).toBeNull();
        expect(workspace.signal().aborted).toBe(true);
        expect(workspace.abort).toHaveBeenCalledTimes(1);
        expectActor("second-actor");
      } else {
        expect(
          document.querySelector(".directory-v4-workspace"),
        ).not.toBeNull();
        expect(workspace.signal().aborted).toBe(false);
        await act(async () => workspace.resolve(profile));
        await screen.findByText(/当前权限不可用/);
      }
      view.unmount();
      await act(async () => {
        await Promise.resolve();
      });
    },
  );
});
