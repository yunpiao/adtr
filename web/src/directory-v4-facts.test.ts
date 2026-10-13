// Synthetic unit contracts only; no API/Worker/LDAP or product acceptance.
import { describe, expect, it } from "vitest";
import { validDirectoryV3Object } from "./directory-v3-facts";
import {
  directoryV4DisplayMember,
  validDirectoryV4Object,
  type DirectoryV4Object,
} from "./directory-v4-facts";

const row = (rawMember: string[] | null = null): DirectoryV4Object => ({
  objectGUID: "00000000-0000-0000-0000-000000000001",
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
  rawMember,
  returnedMemberCount: rawMember === null ? null : rawMember.length,
  memberReadState: rawMember === null ? "not_returned" : "returned",
});

describe("dictionary4 facts", () => {
  it("distinguishes missing, returned empty and returned values without mutation", () => {
    for (const members of [
      null,
      [],
      [" z ", "A", "a", "é", "e\u0301", "\0\n😀<script>", "\\u0000"],
    ]) {
      const value = row(members);
      const before = structuredClone(value);
      expect(validDirectoryV4Object(value)).toBe(true);
      expect(value).toEqual(before);
      expect(value.rawMember).toBe(members);
    }
  });
  it.each([
    { rawMember: null, returnedMemberCount: 0 },
    { rawMember: null, memberReadState: "returned" },
    { rawMember: [], returnedMemberCount: null, memberReadState: "returned" },
    { rawMember: [], returnedMemberCount: 0, memberReadState: "not_returned" },
    { rawMember: ["a"], returnedMemberCount: 0, memberReadState: "returned" },
    { memberReadState: "denied" },
    { returnedMemberCount: -1 },
    { returnedMemberCount: 0.5 },
    { rawMember: "a" },
    { memberCount: 0 },
    { memberOf: [] },
  ])("rejects inconsistent or unknown fields %j", (patch) => {
    expect(validDirectoryV4Object({ ...row(), ...patch })).toBe(false);
  });
  it("requires every membership and unchanged v3 field and rejects v4 in v3", () => {
    const value = row();
    for (const key of Object.keys(value)) {
      const missing = { ...value } as Record<string, unknown>;
      delete missing[key];
      expect(validDirectoryV4Object(missing), key).toBe(false);
    }
    expect(validDirectoryV3Object(value)).toBe(false);
    expect(validDirectoryV4Object({ ...value, objectGUID: "BAD" })).toBe(false);
    expect(validDirectoryV4Object({ ...value, primaryGroupID: -1 })).toBe(
      false,
    );
    expect(validDirectoryV4Object({ ...value, servicePrincipalName: [] })).toBe(
      false,
    );
  });
  it.each(["user", "computer"] as const)(
    "allows only absent members on %s",
    (kind) => {
      for (const members of [null, [], ["a"]]) {
        expect(
          validDirectoryV4Object({
            ...row(members),
            kind,
            objectClass: [kind],
          }),
        ).toBe(members === null);
      }
    },
  );
  it.each(
    [
      [""],
      ["a", "a"],
      ["\ud800"],
      ["\udfff"],
      ["a\ud800b"],
      [1],
      [null],
      [undefined],
    ].map((members) => ({ members })),
  )("rejects malformed members %j", ({ members }) => {
    expect(
      validDirectoryV4Object({
        ...row(),
        rawMember: members,
        returnedMemberCount: members.length,
        memberReadState: "returned",
      }),
    ).toBe(false);
  });
  it("enforces exact 256-value, 4096-byte and 65536-byte limits", () => {
    expect(
      validDirectoryV4Object(
        row(Array.from({ length: 256 }, (_, i) => String(i))),
      ),
    ).toBe(true);
    expect(
      validDirectoryV4Object(
        row(Array.from({ length: 257 }, (_, i) => String(i))),
      ),
    ).toBe(false);
    for (const value of ["a".repeat(4096), "😀".repeat(1024)]) {
      expect(validDirectoryV4Object(row([value]))).toBe(true);
      expect(validDirectoryV4Object(row([value + "a"]))).toBe(false);
    }
    const exact = Array.from({ length: 16 }, (_, i) =>
      String.fromCharCode(65 + i).repeat(4096),
    );
    expect(validDirectoryV4Object(row(exact))).toBe(true);
    expect(validDirectoryV4Object(row([...exact, "a"]))).toBe(false);
    expect(validDirectoryV4Object(row(new Array(1)))).toBe(false);
  });
  it("escapes presentation reversibly without treating raw text as valid DN", () => {
    expect(directoryV4DisplayMember("\\u0000\0\n\u202e😀<script>")).toBe(
      "\\\\u0000\\u0000\\u000a\\u202e😀<script>",
    );
    expect(directoryV4DisplayMember("  not a DN  ")).toBe("  not a DN  ");
  });
});
