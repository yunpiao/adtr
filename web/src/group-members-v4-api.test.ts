// Synthetic parser tests; transport, UI and real-chain gates remain separate.
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";
import { directoryV2BodyLimit } from "./directory-v2-json";
import {
  parseGroupMembersV4,
  readGroupMembersV4,
  validGroupMembersV4Query,
  type GroupMembersV4,
  type GroupMembersV4Query,
} from "./group-members-v4-api";

const query: GroupMembersV4Query = {
  domainId: "synthetic-domain",
  expectedRevision: "9007199254740993",
  expectedCredentialRevision: "9007199254740995",
  observationId: "synthetic-observation",
  objectGUID: "00000000-0000-0000-0000-000000000001",
};
const response = (): GroupMembersV4 => ({
  dictionaryVersion: 4,
  selection: {
    domainId: query.domainId,
    revision: query.expectedRevision,
    credentialRevision: query.expectedCredentialRevision,
  },
  observationId: query.observationId,
  source: {
    server_name: "dc.synthetic.invalid",
    dc_host_name: "dc.synthetic.invalid",
    domain: "synthetic.invalid",
    naming_context: "DC=synthetic,DC=invalid",
    started_at: "2026-10-10T00:00:00Z",
    completed_at: "2026-10-10T00:00:01Z",
    elapsed_milliseconds: 1000,
    pages: 1,
  },
  object: {
    objectGUID: query.objectGUID,
    distinguishedName: "CN=Group,DC=synthetic,DC=invalid",
    kind: "group",
    objectClass: ["group", "top"],
    samAccountName: null,
    userAccountControl: null,
    objectSid: null,
    mail: null,
    description: null,
    whenCreated: null,
    primaryGroupID: null,
    servicePrincipalName: null,
    rawMember: [" z ", "A", "\0😀", "\\u0000"],
    returnedMemberCount: 4,
    memberReadState: "returned",
  },
});
const invalid = (value: unknown, q: GroupMembersV4Query = query) => {
  expect(() => parseGroupMembersV4(value, q)).toThrowError(
    expect.objectContaining({ code: "invalid_response" }),
  );
};

describe("pinned dictionary4 group members parser", () => {
  it("preserves every byte-equivalent string and order; null differs from empty", () => {
    for (const members of [null, [], response().object.rawMember]) {
      const value = response();
      value.object.rawMember = members;
      value.object.returnedMemberCount =
        members === null ? null : members.length;
      value.object.memberReadState =
        members === null ? "not_returned" : "returned";
      expect(parseGroupMembersV4(value, query)).toBe(value);
      expect(value.object.rawMember).toBe(members);
    }
  });
  it("requires closed request pins without optional latest or coercion", () => {
    expect(validGroupMembersV4Query(query)).toBe(true);
    for (const key of Object.keys(query)) {
      for (const wrong of [undefined, null, "", 1]) {
        const q = { ...query, [key]: wrong };
        expect(validGroupMembersV4Query(q)).toBe(false);
        invalid(response(), q as GroupMembersV4Query);
      }
    }
    for (const patch of [
      { latest: true },
      { expectedRevision: "01" },
      { expectedRevision: "9223372036854775808" },
      { domainId: "platform" },
      { objectGUID: "00000000-0000-0000-0000-00000000000A" },
    ]) {
      expect(validGroupMembersV4Query({ ...query, ...patch })).toBe(false);
    }
  });
  it("rejects every missing or unknown envelope, selection, source and object field", () => {
    const value = response();
    for (const target of [value, value.selection, value.source, value.object]) {
      for (const key of Object.keys(target)) {
        const previous = Reflect.get(target, key);
        Reflect.deleteProperty(target, key);
        invalid(value);
        Reflect.set(target, key, previous);
      }
      Reflect.set(target, "unexpected", true);
      invalid(value);
      Reflect.deleteProperty(target, "unexpected");
    }
  });
  it("rejects version, observation, GUID, kind and selection substitutions", () => {
    for (const dictionaryVersion of [1, 2, 3, 5, "4", null])
      invalid({ ...response(), dictionaryVersion });
    invalid({ ...response(), observationId: "other-observation" });
    for (const key of ["domainId", "revision", "credentialRevision"] as const) {
      const value = response();
      value.selection[key] = "2";
      invalid(value);
    }
    const value = response();
    value.object.objectGUID = "00000000-0000-0000-0000-000000000002";
    invalid(value);
    invalid({
      ...response(),
      object: {
        ...response().object,
        kind: "user",
        objectClass: ["user"],
        rawMember: null,
        returnedMemberCount: null,
        memberReadState: "not_returned",
      },
    });
  });
  it.each([
    { pages: 0 },
    { pages: 101 },
    { domain: "bad domain" },
    { completed_at: "2026-10-09T00:00:00Z" },
    { elapsed_milliseconds: -1 },
    { naming_context: "\0" },
  ])("validates unchanged source contract %j", (patch) => {
    invalid({ ...response(), source: { ...response().source, ...patch } });
  });
  it("rejects invalid membership through the response boundary", () => {
    invalid({
      ...response(),
      object: { ...response().object, returnedMemberCount: 0 },
    });
    invalid({
      ...response(),
      object: {
        ...response().object,
        rawMember: ["\ud800"],
        returnedMemberCount: 1,
      },
    });
  });
  it("reads fatal UTF-8 across chunks and preserves raw fields", async () => {
    const bytes = new TextEncoder().encode(JSON.stringify(response()));
    const streamed = new Response(
      new ReadableStream({
        start(controller) {
          for (const byte of bytes) controller.enqueue(new Uint8Array([byte]));
          controller.close();
        },
      }),
    );
    expect(
      await readGroupMembersV4(streamed, query, new AbortController().signal),
    ).toEqual(response());
  });
  it.each([
    new Uint8Array([0xff]),
    new Uint8Array([0xe2, 0x82]),
    new TextEncoder().encode("{bad}"),
  ])("rejects malformed wire encoding or JSON", async (bytes) => {
    await expect(
      readGroupMembersV4(
        new Response(bytes),
        query,
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("rejects escaped unpaired surrogates after JSON decoding", async () => {
    const value = response();
    value.object.rawMember = ["\ud800"];
    value.object.returnedMemberCount = 1;
    await expect(
      readGroupMembersV4(
        new Response(JSON.stringify(value)),
        query,
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "invalid_response" });
  });
  it("enforces declared and actual whole-response limits and cancels the body", async () => {
    const cancel = vi.fn();
    const declared = new Response(new ReadableStream({ cancel }), {
      headers: { "Content-Length": String(directoryV2BodyLimit + 1) },
    });
    await expect(
      readGroupMembersV4(declared, query, new AbortController().signal),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
    expect(cancel).toHaveBeenCalledOnce();
    const actual = new Response(new Uint8Array(directoryV2BodyLimit + 1), {
      headers: { "Content-Length": "1" },
    });
    await expect(
      readGroupMembersV4(actual, query, new AbortController().signal),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
  });
  it("accepts exactly the body budget but rejects the next byte", async () => {
    const json = JSON.stringify(response());
    const padding =
      directoryV2BodyLimit - new TextEncoder().encode(json).length;
    expect(
      await readGroupMembersV4(
        new Response(json + " ".repeat(padding)),
        query,
        new AbortController().signal,
      ),
    ).toEqual(response());
    await expect(
      readGroupMembersV4(
        new Response(json + " ".repeat(padding + 1)),
        query,
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ code: "directory_limit_exceeded" });
  });
  it("never interprets failed HTTP or a cancelled read as success", async () => {
    await expect(
      readGroupMembersV4(
        new Response(JSON.stringify(response()), { status: 409 }),
        query,
        new AbortController().signal,
      ),
    ).rejects.toBeInstanceOf(ApiError);
    const controller = new AbortController();
    controller.abort();
    await expect(
      readGroupMembersV4(
        new Response(JSON.stringify(response())),
        query,
        controller.signal,
      ),
    ).rejects.toMatchObject({ name: "AbortError" });
    const cancel = vi.fn();
    const during = new AbortController();
    const pending = readGroupMembersV4(
      new Response(new ReadableStream({ cancel })),
      query,
      during.signal,
    );
    during.abort();
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(cancel).toHaveBeenCalledOnce();
  });
});
