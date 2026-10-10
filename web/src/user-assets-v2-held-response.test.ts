import { describe, expect, it, vi } from "vitest";
import type { APIResponse, Route } from "@playwright/test";
import { fetchHeldResponse } from "../e2e/user-assets-v2-held-response";
describe("genuine held-response upstream transport", () => {
  it("preserves the exact successful APIResponse without retry or redirect", async () => {
    const response = { marker: "original response" } as unknown as APIResponse;
    const fetch = vi.fn().mockResolvedValue(response);
    expect(await fetchHeldResponse({ fetch } as Pick<Route, "fetch">)).toBe(
      response,
    );
    expect(fetch).toHaveBeenCalledExactlyOnceWith({
      maxRedirects: 0,
      maxRetries: 0,
      timeout: 15_000,
    });
  });
  it("fails once without retaining arbitrary call-log headers or credentials", async () => {
    const fetch = vi
      .fn()
      .mockRejectedValue(
        new Error(
          "socket failure; Cookie: synthetic-secret; Authorization: synthetic-token",
        ),
      );
    const error = await fetchHeldResponse({ fetch } as Pick<
      Route,
      "fetch"
    >).catch((error) => error);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(error.message).toBe(
      "Held genuine upstream fetch failed; transport details withheld",
    );
    expect(error.cause).toBeUndefined();
    expect(String(error.stack)).not.toMatch(
      /Cookie|Authorization|synthetic-secret|synthetic-token/u,
    );
  });
});
