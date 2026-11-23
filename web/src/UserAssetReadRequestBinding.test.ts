import { afterEach, describe, expect, it, vi } from "vitest";

import {
  type UserAssetV3Object,
  type UserAssetsV3Query,
  userAssetsV3API,
} from "./user-assets-v3-api";

const actor = 1;
const original = {
  domainId: "synthetic-domain",
  expectedRevision: "9007199254740993",
  expectedCredentialRevision: "9007199254740995",
  search: "observed",
  pageIdx: 1,
  pageSize: 10,
  observationId: "observation-one",
  uacDisableBit: "clear",
} satisfies UserAssetsV3Query;
const source = {
  server_name: "dc.synthetic.invalid",
  dc_host_name: "dc.synthetic.invalid",
  domain: "synthetic.invalid",
  naming_context: "DC=synthetic,DC=invalid",
  started_at: "2026-10-10T00:00:00Z",
  completed_at: "2026-10-10T00:00:01Z",
  elapsed_milliseconds: 877,
  pages: 1,
};
const row = {
  objectGUID: "00000000-0000-0000-0000-000000000001",
  distinguishedName: "CN=Observed,DC=synthetic,DC=invalid",
  kind: "user",
  objectClass: ["top", "user"],
  samAccountName: "observed",
  userAccountControl: 0,
  objectSid: "S-1-5-21-1-2-3-500",
  mail: "observed@synthetic.invalid",
  description: null,
  whenCreated: "2026-10-10T00:00:00Z",
  primaryGroupID: 0,
  servicePrincipalName: null,
} satisfies UserAssetV3Object;
const envelope = {
  dictionaryVersion: 3,
  selection: {
    domainId: original.domainId,
    revision: original.expectedRevision,
    credentialRevision: original.expectedCredentialRevision,
  },
  observationId: original.observationId,
  source,
};

function deferFetch() {
  let resolve!: (response: Response) => void;
  const response = new Promise<Response>((settle) => {
    resolve = settle;
  });
  const fetch = vi.fn<typeof globalThis.fetch>().mockReturnValue(response);
  vi.stubGlobal("fetch", fetch);
  return {
    sent() {
      expect(fetch).toHaveBeenCalledTimes(1);
      const [input, options] = fetch.mock.calls[0];
      expect(options?.method).toBe("GET");
      return new URL(String(input), "https://fixtures.invalid");
    },
    respond(body: unknown) {
      resolve(
        new Response(JSON.stringify(body), {
          headers: { "X-ADTR-User-ID": String(actor) },
        }),
      );
    },
  };
}

afterEach(() => vi.unstubAllGlobals());

describe("user asset read request binding", () => {
  it("checks a list against the sent filters and page after draft edits", async () => {
    const remote = deferFetch();
    const draft: UserAssetsV3Query = { ...original };
    const pending = userAssetsV3API.list(
      draft,
      actor,
      new AbortController().signal,
    );
    const sent = remote.sent();
    expect(sent.pathname).toBe("/api/user-assets/v3");
    expect(Object.fromEntries(sent.searchParams)).toMatchObject({
      search: original.search,
      pageSize: "10",
      observationId: original.observationId,
      uacDisableBit: original.uacDisableBit,
    });
    Object.assign(draft, {
      search: "different",
      pageSize: 20,
      observationId: "observation-two",
      uacDisableBit: "set",
    });

    const page = { pageIdx: 1, pageSize: 10, total: 1, totalPage: 1 };
    remote.respond({ ...envelope, available: true, list: [row], page });
    await expect(pending).resolves.toMatchObject({
      observationId: original.observationId,
      list: [row],
      page,
    });
  });

  it("checks a detail against the requested asset after selection changes", async () => {
    const remote = deferFetch();
    const draft = {
      domainId: original.domainId,
      expectedRevision: original.expectedRevision,
      expectedCredentialRevision: original.expectedCredentialRevision,
      observationId: original.observationId,
      objectGUID: row.objectGUID,
    };
    const pending = userAssetsV3API.detail(
      draft,
      actor,
      new AbortController().signal,
    );
    const sent = remote.sent();
    expect(sent.pathname).toBe("/api/user-assets/v3/detail");
    expect(sent.searchParams.get("objectGUID")).toBe(row.objectGUID);
    expect(sent.searchParams.get("observationId")).toBe(original.observationId);
    draft.observationId = "observation-two";
    draft.objectGUID = "00000000-0000-0000-0000-000000000002";

    remote.respond({ ...envelope, object: row });
    await expect(pending).resolves.toMatchObject({ object: row });
  });
});
