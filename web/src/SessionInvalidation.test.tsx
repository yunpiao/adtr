import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { Profile } from "./api";
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
