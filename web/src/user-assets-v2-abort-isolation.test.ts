// Test-only transport regression. These synthetic Fetch/Response cases do not
// substitute for the CI browser/API/worker/LDAP acceptance suites.
import { webcrypto } from "node:crypto";
import { installDirectoryV2ResponseObserver } from "../e2e/directory-v2-response-observer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { installUserAssetAbortIsolation } from "../e2e/user-assets-v2-abort-isolation";
import { readDirectoryV2JSON, directoryV2BodyLimit } from "./directory-v2-json";
import { userAssetsV2API } from "./user-assets-v2-api";

const nativeAbort = AbortController.prototype.abort;
const key = "__adtrUserAssetAbortIsolation";
const state = () => (window as any)[key];
const path = "/api/user-assets/v2/detail";
const request = {
  domainId: "synthetic-domain",
  expectedRevision: "1",
  expectedCredentialRevision: "2",
  observationId: "synthetic-observation",
  objectGUID: "01010101-0101-0101-0101-010101010101",
};
let nativeFetch: ReturnType<typeof vi.fn>;
beforeEach(() => {
  nativeFetch = vi.fn();
  vi.stubGlobal("fetch", nativeFetch);
  vi.stubGlobal("crypto", webcrypto);
  (window as any).__adtrDirectoryV2Document = async () => {};
  installDirectoryV2ResponseObserver();
});
afterEach(() => {
  AbortController.prototype.abort = nativeAbort;
  delete (window as any)[key];
  delete (window as any).__adtrDirectoryV2ResponseObserver;
  delete (window as any).__adtrDirectoryV2Document;
  vi.unstubAllGlobals();
});

describe("armed user-asset cancellation isolation", () => {
  it("reproduces fetch-only suppression leaving the pre-aborted parser body unread", async () => {
    const controller = new AbortController();
    const bytes = new Uint8Array([123, 125]);
    let reads = 0;
    const body = new ReadableStream<Uint8Array>(
      {
        pull(target) {
          reads++;
          target.enqueue(bytes);
          target.close();
        },
      },
      { highWaterMark: 0 },
    );
    const response = new Response(body);
    controller.abort();
    await expect(
      readDirectoryV2JSON(response, controller.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(reads).toBe(0);
    expect(body.locked).toBe(false);
    expect(response.bodyUsed).toBe(false);
  });

  it("keeps native arguments/promise/response and allows the bounded reader to consume late genuine bytes", async () => {
    let resolve!: (response: Response) => void;
    const pending = new Promise<Response>((ready) => {
      resolve = ready;
    });
    nativeFetch.mockReturnValue(pending);
    installUserAssetAbortIsolation();
    state().arm(path, "detail");
    const controller = new AbortController();
    const init = {
      signal: controller.signal,
      credentials: "same-origin" as const,
      cache: "no-store" as const,
    };
    const fetch = window.fetch(path, init);
    expect(fetch).toBe(pending);
    expect(nativeFetch).toHaveBeenCalledExactlyOnceWith(path, init);
    controller.abort();
    expect(controller.signal.aborted).toBe(false);
    const response = new Response('{"sequence":17}', {
      headers: { "Cache-Control": "no-store", "X-ADTR-User-ID": "7" },
    });
    resolve(response);
    expect(await fetch).toBe(response);
    expect(await readDirectoryV2JSON(response, controller.signal)).toEqual({
      sequence: 17,
    });
    expect(response.bodyUsed).toBe(true);
    expect(state().witness("detail")).toMatchObject({
      path,
      requestURL: new URL(path, location.href).href,
      suppressed: 1,
      aborted: false,
      identityValid: true,
      requestIdentity: {
        epoch: expect.any(String),
        method: "GET",
        url: new URL(path, location.href).href,
        ordinal: 0,
      },
    });
  });

  it("does not suppress unarmed, other-route, other-origin or non-GET cancellation", async () => {
    nativeFetch.mockResolvedValue(new Response("{}"));
    installUserAssetAbortIsolation();
    const unarmed = new AbortController();
    await window.fetch(path, { signal: unarmed.signal });
    unarmed.abort();
    expect(unarmed.signal.aborted).toBe(true);
    state().arm(path, "armed");
    for (const [url, method] of [
      ["/api/auth/me", "GET"],
      ["https://outside.invalid" + path, "GET"],
      [path, "POST"],
    ]) {
      const controller = new AbortController();
      await window.fetch(url, { method, signal: controller.signal });
      controller.abort();
      expect(controller.signal.aborted).toBe(true);
    }
    expect(state().witness("armed")).toMatchObject({
      requestURL: null,
      suppressed: 0,
      aborted: null,
    });
    expect(nativeFetch).toHaveBeenCalledTimes(4);
  });

  it("preserves pre-aborted rejection and refuses reuse of a protected signal", async () => {
    nativeFetch.mockImplementation((_url, init) =>
      init.signal.aborted
        ? Promise.reject(init.signal.reason)
        : Promise.resolve(new Response("{}")),
    );
    installUserAssetAbortIsolation();
    const before = new AbortController();
    before.abort();
    state().arm(path, "before");
    await expect(
      window.fetch(path, { signal: before.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(state().witness("before")).toMatchObject({
      suppressed: 0,
      aborted: true,
    });
    const protectedController = new AbortController();
    state().arm(path, "after");
    await window.fetch(path, { signal: protectedController.signal });
    expect(() =>
      window.fetch("/api/auth/me", { signal: protectedController.signal }),
    ).toThrow("reused");
    expect(nativeFetch).toHaveBeenCalledTimes(2);
    protectedController.abort();
    expect(protectedController.signal.aborted).toBe(true);
    expect(state().witness("after").identityValid).toBe(false);
  });

  it.each(["observer first", "isolation first"])(
    "binds exact document/method/full URL/ordinal regardless of init-script order: %s",
    async (order) => {
      window.fetch = nativeFetch as typeof fetch;
      delete (window as any).__adtrDirectoryV2ResponseObserver;
      nativeFetch.mockImplementation(() => Promise.resolve(new Response("{}")));
      if (order === "observer first") installDirectoryV2ResponseObserver();
      installUserAssetAbortIsolation();
      if (order === "isolation first") installDirectoryV2ResponseObserver();
      const url = path + "?observationId=synthetic&objectGUID=synthetic";
      const first = new AbortController();
      await window.fetch(url, { signal: first.signal });
      state().arm(path, "selected");
      const selected = new AbortController();
      await window.fetch(url, { signal: selected.signal });
      const observer = (window as any).__adtrDirectoryV2ResponseObserver;
      const requestIdentity = observer.peek(
        `GET ${new URL(url, location.href).href}`,
        1,
      ).identity;
      expect(state().witness("selected")).toMatchObject({
        identityValid: true,
        requestIdentity: { ...requestIdentity, ordinal: 1 },
      });
      first.abort();
      selected.abort();
      expect(first.signal.aborted).toBe(true);
      expect(selected.signal.aborted).toBe(false);
      expect(state().witness("selected").suppressed).toBe(1);
    },
  );

  it("fails closed when the observer is missing at arm time", () => {
    delete (window as any).__adtrDirectoryV2ResponseObserver;
    installUserAssetAbortIsolation();
    expect(() => state().arm(path, "missing")).toThrow(
      "requires the response observer",
    );
    const controller = new AbortController();
    controller.abort();
    expect(controller.signal.aborted).toBe(true);
  });

  it.each(["epoch", "method", "url", "ordinal"])(
    "does not suppress when the first observer binding has an invalid %s",
    async (field) => {
      nativeFetch.mockImplementation(() => Promise.resolve(new Response("{}")));
      installUserAssetAbortIsolation();
      state().arm(path, "mismatch");
      const bind = state().bindRequest;
      state().bindRequest = (
        signal: AbortSignal,
        identity: Record<string, unknown>,
      ) =>
        bind(signal, {
          ...identity,
          [field]: field === "ordinal" ? -1 : "mismatch",
        });
      const controller = new AbortController();
      await window.fetch(path, { signal: controller.signal });
      controller.abort();
      expect(controller.signal.aborted).toBe(true);
      expect(state().witness("mismatch")).toMatchObject({
        identityValid: false,
        requestIdentity: null,
        suppressed: 0,
      });
    },
  );

  it("invalidates a duplicated binding and restores native abort", async () => {
    nativeFetch.mockResolvedValue(new Response("{}"));
    installUserAssetAbortIsolation();
    state().arm(path, "duplicate");
    const controller = new AbortController();
    await window.fetch(path, { signal: controller.signal });
    const witness = state().witness("duplicate");
    expect(() =>
      state().bindRequest(controller.signal, witness.requestIdentity),
    ).toThrow("identity did not match");
    controller.abort();
    expect(controller.signal.aborted).toBe(true);
    expect(state().witness("duplicate")).toMatchObject({
      identityValid: false,
      suppressed: 0,
    });
  });

  it.each(["before fetch", "after fetch"])(
    "rejects document epoch changes %s",
    async (when) => {
      nativeFetch.mockResolvedValue(new Response("{}"));
      installUserAssetAbortIsolation();
      state().arm(path, "old-document");
      const controller = new AbortController();
      if (when === "before fetch")
        (window as any).__adtrDirectoryV2ResponseObserver.epoch =
          "new-document";
      await window.fetch(path, { signal: controller.signal });
      if (when === "after fetch")
        (window as any).__adtrDirectoryV2ResponseObserver.epoch =
          "new-document";
      controller.abort();
      expect(controller.signal.aborted).toBe(true);
      expect(state().witness("old-document")).toMatchObject({
        identityValid: false,
        suppressed: 0,
      });
    },
  );

  it("restores native abort after disarm and never protects later unrelated traffic", async () => {
    nativeFetch.mockImplementation(() => Promise.resolve(new Response("{}")));
    installUserAssetAbortIsolation();
    state().arm(path, "held");
    const held = new AbortController();
    await window.fetch(path, { signal: held.signal });
    held.abort();
    expect(held.signal.aborted).toBe(false);
    state().disarm("held");
    held.abort();
    expect(held.signal.aborted).toBe(true);
    expect(state().witness("held")).toMatchObject({
      identityValid: false,
      suppressed: 1,
      aborted: null,
    });
    const next = new AbortController();
    await window.fetch(path, { signal: next.signal });
    next.abort();
    expect(next.signal.aborted).toBe(true);
    expect(() => state().arm(path, "held")).toThrow("Invalid held");
  });

  it("disarms an unconsumed arm and caps audit tombstones without token reuse", () => {
    installUserAssetAbortIsolation();
    for (let index = 0; index < 2048; index++) {
      state().arm(path, `token-${index}`);
      state().disarm(`token-${index}`);
    }
    expect(() => state().arm(path, "overflow")).toThrow("Invalid held");
    expect(() => state().arm(path, "token-0")).toThrow("Invalid held");
    const controller = new AbortController();
    controller.abort();
    expect(controller.signal.aborted).toBe(true);
  });

  it("preserves a synchronous native fetch failure and never suppresses its unbound signal", () => {
    const error = new TypeError("synthetic native fetch failure");
    nativeFetch.mockImplementation(() => {
      throw error;
    });
    installUserAssetAbortIsolation();
    state().arm(path, "undispatched");
    const controller = new AbortController();
    expect(() => window.fetch(path, { signal: controller.signal })).toThrow(
      error,
    );
    controller.abort();
    expect(controller.signal.aborted).toBe(true);
    expect(state().witness("undispatched")).toMatchObject({
      requestIdentity: null,
      identityValid: false,
      suppressed: 0,
    });
  });

  it("leaves the production response cap and actor-header rejection active", async () => {
    installUserAssetAbortIsolation();
    let resolve!: (response: Response) => void;
    nativeFetch.mockImplementation(
      () =>
        new Promise<Response>((ready) => {
          resolve = ready;
        }),
    );
    state().arm(path, "oversized");
    const oversized = new AbortController();
    const first = window.fetch(path, { signal: oversized.signal });
    oversized.abort();
    resolve(
      new Response("{}", {
        headers: { "Content-Length": String(directoryV2BodyLimit + 1) },
      }),
    );
    await expect(
      readDirectoryV2JSON(await first, oversized.signal),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
    state().arm(path, "wrong-actor");
    const wrong = new AbortController();
    const second = userAssetsV2API.detail(request, 7, wrong.signal);
    wrong.abort();
    resolve(new Response("{}", { headers: { "X-ADTR-User-ID": "8" } }));
    await expect(second).rejects.toMatchObject({
      code: "unauthenticated",
      status: 401,
    });
  });
});
