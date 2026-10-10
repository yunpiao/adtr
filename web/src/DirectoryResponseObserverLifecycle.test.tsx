// Synthetic React/transport regression. The real API/database/browser suites
// remain separate. Exercise the unchanged mutation API and actual hook cleanup.
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { webcrypto } from "node:crypto";
import type {
  BrowserContext,
  Response as BrowserResponse,
} from "@playwright/test";
import { useTaskMutation } from "./task-common";
import {
  directoryV2CredentialPurpose,
  directoryV2CredentialUseAPI,
  type DirectoryV2CredentialReceipt,
} from "./directory-v2-credential-use-api";
import {
  observeDirectoryV2Responses,
  installDirectoryV2ResponseObserver,
  directoryV2ResponseJSON,
  directoryV2ResponseWitness,
} from "../e2e/directory-v2-response-observer";

const path = "/api/directory-credential-use/v2/grant";
const receipt: DirectoryV2CredentialReceipt = {
  result: "SUCCESS",
  operation: "grant",
  accountId: "synthetic-account",
  domainId: "synthetic-domain",
  roleId: "platform_admin",
  purpose: directoryV2CredentialPurpose,
  allowed: true,
  grantRevision: "3",
  accountCredentialRevision: "2",
  replayed: false,
  accountDeleted: false,
  currentGrantRevision: "3",
  currentAllowed: true,
};
const body = { ...receipt, consumerEnabled: true };

afterEach(() => {
  delete (window as any).__adtrDirectoryV2ResponseObserver;
  delete (window as any).__adtrDirectoryV2Document;
  vi.unstubAllGlobals();
});

describe("ordinary directory mutation response observation", () => {
  it.each(["JSON first", "strict first"])(
    "retains completed grant JSON after real success/unmount cleanup with a delayed digest: %s",
    async (order) => {
      let releaseHash!: () => void;
      vi.stubGlobal("crypto", {
        randomUUID: () => webcrypto.randomUUID(),
        subtle: {
          digest: (algorithm: string, bytes: Uint8Array<ArrayBuffer>) =>
            new Promise<ArrayBuffer>((resolve, reject) => {
              releaseHash = () => {
                void webcrypto.subtle
                  .digest(algorithm, bytes)
                  .then(resolve, reject);
              };
            }),
        },
      });
      const context = new EventEmitter() as EventEmitter & {
        exposeBinding: ReturnType<typeof vi.fn>;
        addInitScript: ReturnType<typeof vi.fn>;
      };
      const frame = { page: () => page, url: () => location.href };
      const page = {
        mainFrame: () => frame,
        waitForFunction: vi.fn(async (fn, argument) => {
          await waitFor(() => expect(fn(argument)).toBeTruthy());
        }),
        evaluate: vi.fn(async (fn, argument) => fn(argument)),
      };
      context.exposeBinding = vi.fn(async (_name, callback) => {
        (window as any).__adtrDirectoryV2Document = async (epoch: string) =>
          callback({ page, frame }, epoch);
      });
      context.addInitScript = vi.fn(async () => {});
      await observeDirectoryV2Responses(context as unknown as BrowserContext);
      let signal!: AbortSignal;
      let captured!: BrowserResponse;
      const nativeFetch = vi.fn((url: string, init: RequestInit) => {
        signal = init.signal!;
        const request = {
          url: () => new URL(url, location.href).href,
          method: () => init.method!,
          frame: () => frame,
          resourceType: () => "fetch",
        };
        context.emit("request", request);
        captured = {
          url: request.url,
          request: () => request,
          status: () => 200,
          headers: () => ({
            "cache-control": "no-store",
            "x-adtr-user-id": "1",
          }),
        } as unknown as BrowserResponse;
        return Promise.resolve(
          new Response(JSON.stringify(body), {
            headers: { "Cache-Control": "no-store", "X-ADTR-User-ID": "1" },
          }),
        );
      });
      vi.stubGlobal("fetch", nativeFetch);
      installDirectoryV2ResponseObserver();
      const success = vi.fn(),
        uncertain = vi.fn(),
        sessionChanged = vi.fn();
      function Mutation({ done }: { done: () => void }) {
        const mutation = useTaskMutation(sessionChanged);
        return (
          <button
            onClick={() =>
              void mutation.run(
                (signal) =>
                  directoryV2CredentialUseAPI.mutate(
                    "grant",
                    {
                      accountId: receipt.accountId,
                      roleId: receipt.roleId,
                      purpose: directoryV2CredentialPurpose,
                      expectedAccountRevision: "1",
                      expectedCredentialRevision: "2",
                      expectedGrantRevision: "0",
                      idempotencyKey: "synthetic-grant-key",
                    },
                    { actorPassword: "synthetic-proof", totpCode: "123456" },
                    "synthetic-csrf",
                    1,
                    signal,
                  ),
                (value) => {
                  success(value);
                  done();
                },
                uncertain,
              )
            }
          >
            Commit synthetic grant
          </button>
        );
      }
      function Owner() {
        const [version, setVersion] = useState(0);
        return (
          <Mutation
            key={version}
            done={() => setVersion((value) => value + 1)}
          />
        );
      }
      render(<Owner />);
      fireEvent.click(
        screen.getByRole("button", { name: "Commit synthetic grant" }),
      );
      await waitFor(() =>
        expect(success).toHaveBeenCalledExactlyOnceWith(receipt),
      );
      await waitFor(() => expect(signal.aborted).toBe(true));
      expect(uncertain).not.toHaveBeenCalled();
      expect(sessionChanged).not.toHaveBeenCalled();
      expect(nativeFetch).toHaveBeenCalledTimes(1);
      expect(nativeFetch.mock.calls[0][0]).toBe(path);
      expect(nativeFetch.mock.calls[0][1].method).toBe("POST");
      const observer = (window as any).__adtrDirectoryV2ResponseObserver;
      const id = `POST ${new URL(path, location.href).href}`;
      const beforeHash = observer.peek(id, 0);
      expect(beforeHash).toMatchObject({
        eof: true,
        released: 1,
        readers: 1,
        cancelCalls: 0,
        signalAborted: true,
      });
      await act(async () => {
        releaseHash();
      });
      await waitFor(() => expect(observer.buffers().hashing).toBe(0));
      const take = vi.spyOn(observer, "take");
      if (order === "strict first")
        await expect(directoryV2ResponseWitness(captured)).rejects.toThrow(
          "body observation failed",
        );
      await expect(directoryV2ResponseJSON(captured)).resolves.toEqual(body);
      await expect(directoryV2ResponseWitness(captured)).rejects.toThrow(
        "body observation failed",
      );
      await expect(directoryV2ResponseJSON(captured)).resolves.toEqual(body);
      expect(take).toHaveBeenCalledOnce();
      expect(observer.buffers()).toMatchObject({
        chunks: 0,
        hashing: 0,
        text: 0,
      });
    },
  );
});
