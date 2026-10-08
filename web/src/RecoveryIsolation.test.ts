// Storage and intent unit regressions; these do not substitute for a real
// browser session transition through the authenticated API and PostgreSQL.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import { emptyFilter } from "./audit-api";
import {
  auditIntent,
  beginAuditIntent,
  attemptedAuditIntent,
  discardAuditIntent,
  forgetAuditSession,
} from "./audit-intent";
import {
  maintenanceIntent,
  beginMaintenanceIntent,
  attemptMaintenanceIntent,
  discardMaintenanceIntent,
  forgetMaintenanceSession,
} from "./maintenance-intent";
import {
  readDomainIntent,
  saveDomainIntent,
  discardDomainIntent,
  forgetDomainSession,
} from "./domain-intent";
import {
  taskIntent,
  beginTaskIntent,
  attemptTaskIntent,
  clearTaskIntent,
  discardTaskIntent,
  forgetTaskSession,
} from "./TaskWorkspace";

const actor = {
  ID: 11,
  username: "synthetic:one",
  csrfToken: "secret-csrf-one",
} as Profile;
const other = {
  ...actor,
  ID: 12,
  username: "synthetic:two",
  csrfToken: "secret-csrf-two",
};
const renewed = { ...actor, csrfToken: "rotated-secret-csrf" };
const renamed = { ...actor, username: "different-name" };
const operationKey = "10000000-0000-4000-8000-000000000001";
const poison = {
  actorPassword: "synthetic-secret-password",
  totpCode: "654321",
  csrfToken: "stored-csrf",
  response: { private: "row" },
};
const auditInput = { ...emptyFilter(), selectColumn: ["event"] as const };
const cases = [
  {
    name: "audit export",
    prefix: "adtr.pending-audit-export",
    save: (p: Profile) => {
      const i = beginAuditIntent(p, {
        ...auditInput,
        selectColumn: ["event"],
        ...poison,
      });
      attemptedAuditIntent(p, i);
      return i.input.idempotencyKey;
    },
    read: (p: Profile) => auditIntent(p),
    key: (p: Profile) => auditIntent(p)?.input.idempotencyKey,
    forget: forgetAuditSession,
    discard: discardAuditIntent,
    legacy: (p: Profile) => ({
      owner: p.ID,
      username: p.username,
      input: { ...auditInput, idempotencyKey: operationKey, ...poison },
    }),
    corrupt: (v: any) => {
      v.input.filterEvent = ["x".repeat(129)];
    },
  },
  {
    name: "task maintenance",
    prefix: "adtr.pending-task-maintenance",
    save: (p: Profile) => {
      const i = beginMaintenanceIntent(p, {
        action: "pause",
        input: {
          scheduleUUID: operationKey,
          expectedControlVersion: 1,
          ...poison,
        },
      });
      attemptMaintenanceIntent(p, i);
      return i.key;
    },
    read: (p: Profile) => maintenanceIntent(p),
    key: (p: Profile) => maintenanceIntent(p)?.key,
    forget: forgetMaintenanceSession,
    discard: discardMaintenanceIntent,
    legacy: (p: Profile) => ({
      owner: p.ID,
      username: p.username,
      action: "pause",
      key: operationKey,
      input: {
        scheduleUUID: operationKey,
        expectedControlVersion: 1,
        ...poison,
      },
    }),
    corrupt: (v: any) => {
      v.input.expectedControlVersion = -1;
    },
  },
  {
    name: "domain operation",
    prefix: "adtr.domain-intent.v1",
    save: (p: Profile) => {
      const key = crypto.randomUUID();
      saveDomainIntent(p, {
        kind: "create",
        key,
        domain: "synthetic.example",
        ...poison,
      });
      return key;
    },
    read: (p: Profile) => readDomainIntent(p),
    key: (p: Profile) => readDomainIntent(p)?.key,
    forget: forgetDomainSession,
    discard: discardDomainIntent,
    legacy: (p: Profile) => ({
      owner: `${p.ID}:${p.username}`,
      intent: {
        kind: "create",
        key: operationKey,
        domain: "synthetic.example",
        ...poison,
      },
    }),
    corrupt: (v: any) => {
      v.intent.domain = "x".repeat(255);
    },
  },
  {
    name: "task submission",
    prefix: "adtr.pending-task",
    save: (p: Profile) => {
      const i = beginTaskIntent(p, "submit");
      Object.assign(i, poison);
      attemptTaskIntent(p, i);
      return i.key;
    },
    read: (p: Profile) => taskIntent(p),
    key: (p: Profile) => taskIntent(p)?.key,
    forget: forgetTaskSession,
    discard: discardTaskIntent,
    legacy: (p: Profile) => ({
      owner: p.ID,
      username: p.username,
      action: "submit",
      key: operationKey,
      ...poison,
    }),
    corrupt: (v: any) => {
      v.key = "x".repeat(1000);
    },
  },
];
const storageKey = (prefix: string, p: Profile) =>
  `${prefix}:${p.ID}:${encodeURIComponent(p.username)}`;
beforeEach(() => {
  for (const c of cases) {
    for (const p of [actor, other, renamed]) c.discard(p);
    c.forget();
  }
  sessionStorage.clear();
  vi.stubGlobal("fetch", vi.fn());
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe.each(cases)("$name recovery isolation", (c) => {
  it("keeps the original key across CSRF renewal and a different actor's visit", () => {
    const key = c.save(actor);
    expect(c.key(renewed)).toBe(key);
    expect(c.key(other)).toBeUndefined();
    expect(c.key(renamed)).toBeUndefined();
    expect(c.key(actor)).toBe(key);
    expect(fetch).not.toHaveBeenCalled();
  });
  it("restores each actor after session bindings are forgotten without replay", () => {
    const first = c.save(actor),
      second = c.save(other);
    c.forget();
    expect(c.key(renamed)).toBeUndefined();
    expect(c.key(renewed)).toBe(first);
    expect(c.key(other)).toBe(second);
    expect(fetch).not.toHaveBeenCalled();
  });
  it("discards only the explicitly resolved actor's record", () => {
    const first = c.save(actor);
    c.save(other);
    c.discard(other);
    c.forget();
    expect(c.key(other)).toBeUndefined();
    expect(c.key(actor)).toBe(first);
    c.discard();
    c.forget();
    expect(c.key(actor)).toBeUndefined();
  });
  it("a no-argument discard after forgetting cannot delete another actor's record", () => {
    const key = c.save(actor);
    c.forget();
    c.discard();
    expect(c.key(actor)).toBe(key);
  });
  it("preserves another actor's legacy slot and imports only for its owner", () => {
    sessionStorage.setItem(c.prefix, JSON.stringify(c.legacy(actor)));
    expect(c.key(other)).toBeUndefined();
    c.save(other);
    c.discard(other);
    expect(sessionStorage.getItem(c.prefix)).not.toBeNull();
    expect(c.key(renewed)).toBe(operationKey);
    expect(sessionStorage.getItem(c.prefix)).toBeNull();
    expect(sessionStorage.getItem(storageKey(c.prefix, actor))).toContain(
      operationKey,
    );
    c.forget();
    expect(c.key(actor)).toBe(operationKey);
    expect(JSON.stringify(c.read(actor))).not.toMatch(
      /actorPassword|totpCode|csrfToken|response|synthetic-secret/,
    );
    expect(JSON.stringify({ ...sessionStorage })).not.toMatch(
      /actorPassword|totpCode|csrfToken|response|synthetic-secret/,
    );
  });
  it("persists only the bounded non-secret whitelist", () => {
    const key = c.save(actor);
    expect(sessionStorage.getItem(c.prefix)).toBeNull();
    const raw = sessionStorage.getItem(storageKey(c.prefix, actor))!;
    expect(raw).not.toMatch(
      /actorPassword|totpCode|csrfToken|response|synthetic-secret/,
    );
    expect(raw).not.toContain(actor.csrfToken);
    expect(raw.length).toBeLessThan(65536);
    c.discard(actor);
    sessionStorage.setItem(storageKey(c.prefix, actor), raw);
    c.forget();
    expect(c.key(actor)).toBe(key);
    expect(JSON.stringify(c.read(actor))).not.toMatch(
      /actorPassword|totpCode|csrfToken|response|synthetic-secret/,
    );
  });
  it("rejects corrupt own records without deleting another actor's recovery", () => {
    const key = c.save(other),
      invalid = c.legacy(actor);
    c.corrupt(invalid);
    sessionStorage.setItem(
      storageKey(c.prefix, actor),
      JSON.stringify(invalid),
    );
    c.forget();
    expect(c.key(actor)).toBeUndefined();
    expect(sessionStorage.getItem(storageKey(c.prefix, actor))).toBeNull();
    expect(c.key(other)).toBe(key);
  });
  it("refuses oversized records and records with a mismatched embedded owner", () => {
    sessionStorage.setItem(storageKey(c.prefix, actor), " ".repeat(65537));
    expect(c.key(actor)).toBeUndefined();
    sessionStorage.setItem(
      storageKey(c.prefix, actor),
      JSON.stringify(c.legacy(other)),
    );
    expect(c.key(actor)).toBeUndefined();
    expect(fetch).not.toHaveBeenCalled();
  });
  it("retains in-memory actor isolation if session storage becomes unavailable", () => {
    const key = c.save(actor);
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("unavailable");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("unavailable");
    });
    expect(c.key(other)).toBeUndefined();
    c.save(other);
    c.forget();
    expect(c.key(renewed)).toBe(key);
    expect(c.key(renamed)).toBeUndefined();
    expect(c.key(actor)).toBe(key);
  });
  it("keeps uncertain memory recovery when persistence was denied before the write", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    const key = c.save(actor);
    expect(sessionStorage.length).toBe(0);
    c.forget();
    expect(c.key(other)).toBeUndefined();
    expect(c.key(renewed)).toBe(key);
    expect(fetch).not.toHaveBeenCalled();
  });
});

describe("task legacy migration resolution", () => {
  it("does not resurrect a resolved legacy intent after its migration write failed", () => {
    sessionStorage.setItem(
      "adtr.pending-task",
      JSON.stringify({
        owner: actor.ID,
        username: actor.username,
        action: "submit",
        key: operationKey,
      }),
    );
    const write = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("quota");
      });
    const intent = taskIntent(actor)!;
    expect(intent.key).toBe(operationKey);
    expect(sessionStorage.getItem("adtr.pending-task")).not.toBeNull();
    write.mockRestore();
    const otherIntent = beginTaskIntent(other, "submit");
    attemptTaskIntent(other, otherIntent);
    clearTaskIntent(intent);
    expect(taskIntent(actor)).toBeUndefined();
    expect(sessionStorage.getItem("adtr.pending-task")).toBeNull();
    expect(taskIntent(other)?.key).toBe(otherIntent.key);
  });
});
