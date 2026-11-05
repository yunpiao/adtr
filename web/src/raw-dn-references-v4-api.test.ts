// Synthetic DTO evidence only; not an AD membership or real-chain claim.
import { describe, expect, it } from "vitest";
import {
  parseRawDNReferencesV4,
  validRawDNReferencesV4Query,
  type RawDNReferencesV4,
  type RawDNReferencesV4Query,
} from "./raw-dn-references-v4-api";
const q: RawDNReferencesV4Query = {
  domainId: "synthetic-domain",
  expectedRevision: "9007199254740993",
  expectedCredentialRevision: "9007199254740995",
  observationId: "synthetic-observation",
  objectGUID: "00000000-0000-0000-0000-000000000001",
  pageIdx: 1,
  pageSize: 25,
};
const fixture = (): RawDNReferencesV4 => {
  const target: RawDNReferencesV4["object"] = {
    objectGUID: q.objectGUID,
    distinguishedName: " CN=Target\\,Exact,DC=synthetic ",
    kind: "user",
    objectClass: ["top", "user"],
    samAccountName: null,
    userAccountControl: null,
    objectSid: null,
    mail: null,
    description: null,
    whenCreated: null,
    primaryGroupID: null,
    servicePrincipalName: null,
    rawMember: null,
    returnedMemberCount: null,
    memberReadState: "not_returned",
  };
  return {
    dictionaryVersion: 4,
    selection: {
      domainId: q.domainId,
      revision: q.expectedRevision,
      credentialRevision: q.expectedCredentialRevision,
    },
    observationId: q.observationId,
    source: {
      server_name: "dc.synthetic.invalid",
      dc_host_name: "dc.synthetic.invalid",
      domain: "synthetic.invalid",
      naming_context: "DC=synthetic",
      started_at: "2026-10-10T00:00:00Z",
      completed_at: "2026-10-10T00:00:01Z",
      elapsed_milliseconds: 1000,
      pages: 1,
    },
    object: target,
    list: [2, 3].map((n) => ({
      ...target,
      objectGUID: q.objectGUID.slice(0, -1) + n,
      kind: "group",
      objectClass: ["group", "top"],
      rawMember: [target.distinguishedName],
      returnedMemberCount: 1,
      memberReadState: "returned",
    })),
    page: { pageIdx: 1, pageSize: 25, total: 2, totalPage: 1 },
    coverage: {
      groupCount: 4,
      memberReturnedGroupCount: 3,
      memberNotReturnedGroupCount: 1,
    },
  };
};
const reject = (v: unknown, query = q) =>
  expect(() => parseRawDNReferencesV4(v, query)).toThrowError(
    expect.objectContaining({ code: "invalid_response" }),
  );
describe("raw DN references closed DTO", () => {
  it.each(["user", "group", "computer"] as const)(
    "accepts %s target and distinct GUIDs sharing the same exact DN",
    (kind) => {
      const v = fixture();
      v.object.kind = kind;
      v.object.objectClass = [kind, "top"].sort();
      expect(parseRawDNReferencesV4(v, q)).toBe(v);
    },
  );
  it("keeps empty matches, returned empty groups and unknown groups distinct", () => {
    const v = fixture();
    v.list = [];
    v.page.total = 0;
    v.page.totalPage = 0;
    expect(parseRawDNReferencesV4(v, q).coverage).toEqual({
      groupCount: 4,
      memberReturnedGroupCount: 3,
      memberNotReturnedGroupCount: 1,
    });
  });
  it("rejects every omitted/unknown envelope, source, selection, object, row, page and coverage field", () => {
    const v = fixture();
    for (const target of [
      v,
      v.source,
      v.selection,
      v.object,
      v.list[0],
      v.page,
      v.coverage,
    ]) {
      for (const key of Object.keys(target)) {
        const saved = Reflect.get(target, key);
        Reflect.deleteProperty(target, key);
        reject(v);
        Reflect.set(target, key, saved);
      }
      Reflect.set(target, "extra", true);
      reject(v);
      Reflect.deleteProperty(target, "extra");
    }
  });
  it("requires all seven noncoerced pins and bounded pagination", () => {
    expect(validRawDNReferencesV4Query(q)).toBe(true);
    for (const key of Object.keys(q))
      for (const value of [undefined, null, [], {}, ""]) {
        const wrong = { ...q, [key]: value };
        expect(validRawDNReferencesV4Query(wrong)).toBe(false);
        reject(fixture(), wrong as RawDNReferencesV4Query);
      }
    for (const patch of [
      { latest: true },
      { pageIdx: 0 },
      { pageIdx: 10001 },
      { pageIdx: 1.1 },
      { pageIdx: "1" },
      { pageSize: 26 },
      { pageSize: "25" },
      { expectedRevision: "01" },
      { expectedCredentialRevision: "9223372036854775808" },
      { objectGUID: q.objectGUID.toUpperCase() + "A" },
    ])
      expect(validRawDNReferencesV4Query({ ...q, ...patch })).toBe(false);
  });
  it.each([
    "trim",
    "case",
    "escape",
    "null",
    "empty",
    "user",
    "duplicate",
    "order",
    "count",
  ])("rejects %s result instead of interpreting membership", (mode) => {
    const v = fixture(),
      r = v.list[0];
    if (mode === "trim") r.rawMember = [v.object.distinguishedName.trim()];
    if (mode === "case")
      r.rawMember = [v.object.distinguishedName.toLowerCase()];
    if (mode === "escape")
      r.rawMember = [v.object.distinguishedName.replace("\\,", "\\2c")];
    if (mode === "null") {
      r.rawMember = null;
      r.returnedMemberCount = null;
      r.memberReadState = "not_returned";
    }
    if (mode === "empty") {
      r.rawMember = [];
      r.returnedMemberCount = 0;
    }
    if (mode === "user") {
      r.kind = "user";
      r.objectClass = ["top", "user"];
    }
    if (mode === "duplicate") v.list[1] = { ...r };
    if (mode === "order") v.list.reverse();
    if (mode === "count") r.returnedMemberCount = 2;
    reject(v);
  });
  it.each([
    "negative",
    "fraction",
    "sum",
    "total",
    "pages",
    "short",
    "long",
    "source",
    "observation",
    "revision",
    "guid",
    "version",
  ])("rejects %s inconsistency", (mode) => {
    const v = fixture();
    if (mode === "negative") v.coverage.memberNotReturnedGroupCount = -1;
    if (mode === "fraction") v.coverage.groupCount = 4.5;
    if (mode === "sum") v.coverage.groupCount++;
    if (mode === "total") {
      v.coverage.memberReturnedGroupCount = 1;
      v.coverage.groupCount = 2;
    }
    if (mode === "pages") v.page.totalPage++;
    if (mode === "short") v.list.pop();
    if (mode === "long") v.list.push(v.list[0]);
    if (mode === "source") v.source.completed_at = "bad";
    if (mode === "observation") v.observationId = "other";
    if (mode === "revision") v.selection.revision = "2";
    if (mode === "guid") v.object.objectGUID = v.list[0].objectGUID;
    if (mode === "version") Reflect.set(v, "dictionaryVersion", 3);
    reject(v);
  });
  it("strictly validates last and out-of-range page lengths", () => {
    const v = fixture();
    const next = { ...q, pageIdx: 2 };
    v.page = { pageIdx: 2, pageSize: 25, total: 27, totalPage: 2 };
    v.coverage = {
      groupCount: 30,
      memberReturnedGroupCount: 29,
      memberNotReturnedGroupCount: 1,
    };
    expect(parseRawDNReferencesV4(v, next)).toBe(v);
    reject(v, q);
    v.list = [];
    v.page.pageIdx = 10000;
    expect(parseRawDNReferencesV4(v, { ...q, pageIdx: 10000 })).toBe(v);
  });
});
