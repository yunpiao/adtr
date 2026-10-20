// Synthetic contract coverage only; authored, NOT executed locally.
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, type Profile } from "./api";
import { createDirectoryV4Controls, createDirectoryV4RecoveryContext, directoryV4SourceInput, directoryV4TaskClass, parseDirectoryV4Task } from "./directory-v4-controls-api";
import { directoryV4IntentPrefix, directoryV4OwnerKey, type DirectoryV4Intent, type DirectoryV4Storage } from "./directory-v4-intent";
import type { SourceChoice } from "./domain-selection-api";
import type { Task } from "./task-api";
const input = { domainId: "synthetic-domain", expectedRevision: "9007199254740993", expectedCredentialGeneration: "9007199254740995", idempotencyKey: "synthetic-key" };
const profile: Profile = { ID: 7, username: "synthetic", role: "synthetic-role", priv: 1, csrfToken: "synthetic-csrf", needChangePwd: false, isExpired: false,
  mobile: "", email: "", remark: "", passStrength: "", hasMfa: true, passwordNotUpdatedDays: 0, pwdUpdateTm: "" };
const proof = { actorPassword: "synthetic-password", totpCode: "123456" };
const choice: SourceChoice = { domainId: input.domainId, domain: "synthetic.invalid", revision: input.expectedRevision, credentialRevision: input.expectedCredentialGeneration,
  dcHostName: "dc.synthetic.invalid", ldapAddr: "192.0.2.1", port: "636", mode: "ldaps", credentialConfigured: true, connectionState: "unverified", source: "configured_connection", lastTest: null };
function queued(): Task { return { taskUUID: "original-task", taskName: "domain.directory_read.v4", domainId: input.domainId, payloadVersion: 1,
  state: "queued", sourceState: "PENDING", error: "", createdAt: "2026-10-20T00:00:00Z", updatedAt: "2026-10-20T00:00:00Z", attempt: 0,
  maxAttempts: 1, progress: 0, resultVersion: 0, result: {}, cursor: {}, parentTaskUUID: "", terminalAt: null, archived: false, visibilityVersion: 0 }; }
function response(value: unknown, status = 200, extra: Record<string,string> = {}) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-ADTR-User-ID": "7", ...extra } });
}
afterEach(() => vi.unstubAllGlobals());
describe("closed v4 single-attempt task grammar", () => {
  it.each([
    ["queued",0,"PENDING",0,null], ["running",1,"STARTED",40,null], ["cancel_requested",1,"STARTED",42,null],
    ["succeeded",1,"SUCCESS",100,"2026-10-20T00:00:00Z"], ["failed",0,"FAILURE",0,"2026-10-20T00:00:00Z"],
    ["failed",1,"FAILURE",40,"2026-10-20T00:00:00Z"], ["cancelled",0,"REVOKED",0,"2026-10-20T00:00:00Z"], ["cancelled",1,"REVOKED",42,"2026-10-20T00:00:00Z"],
  ])("accepts %s attempt %s", (state, attempt, sourceState, progress, terminalAt) => {
    expect(parseDirectoryV4Task({ ...queued(), state, attempt, sourceState, progress, terminalAt }, input.domainId).state).toBe(state);
  });
  it.each([
    { taskName: "domain.directory_read.v3" }, { domainId: "other-domain" }, { payloadVersion: 4 }, { maxAttempts: 2 },
    { nextAttemptAt: "2026-10-20T00:00:00Z" }, { state: "retry_wait", sourceState: "RETRY" }, { attempt: 1 },
    { state: "running", sourceState: "STARTED", attempt: 0 }, { result: { observationId: "wrong" } }, { cursor: { key: 1 } },
    { extra: true }, { createdAt: "2026-02-30T00:00:00Z" }, { error: "unsafe\ntext" }, { expectedRevision: 1 },
    { state: "succeeded", attempt: 1, sourceState: "SUCCESS", progress: 99, terminalAt: "2026-10-20T00:00:00Z" },
  ])("rejects invalid task shape %j", patch => { expect(() => parseDirectoryV4Task({ ...queued(), ...patch }, input.domainId)).toThrow(); });
  it.each(["partial_failed", "dead_letter"])("retains %s only as diagnostic", state => {
    const task = parseDirectoryV4Task({ ...queued(), state, attempt: 1, sourceState: "FAILURE", error: "attempts_exhausted", terminalAt: "2026-10-20T00:00:00Z" }, input.domainId);
    expect(directoryV4TaskClass(task)).toBe("diagnostic");
  });
  it("rejects contradictory UUID and explicitly maps source credential generation", () => {
    expect(() => parseDirectoryV4Task(queued(), input.domainId, "other-task")).toThrow();
    expect(directoryV4SourceInput(choice, input.idempotencyKey)).toEqual(input);
  });
});
function setup() {
  const values = new Map<string,string>();
  const storage: DirectoryV4Storage = { getItem: k => values.get(k) ?? null, setItem: (k,v) => { values.set(k,v); }, removeItem: k => { values.delete(k); } };
  const key = directoryV4IntentPrefix + directoryV4OwnerKey(profile);
  const seed = (patch: Partial<DirectoryV4Intent> = {}) => { values.set(key, JSON.stringify({ ...input, storageVersion:1,dictionaryVersion:4,kind:"domain.directory_read.v4",payloadVersion:1,owner:{ID:profile.ID,username:profile.username},receiptOnly:false,...patch })); };
  let current = true;
  let task = queued();
  let receiptFound = true;
  const calls: { path: string; init: RequestInit }[] = [];
  let special: ((path: string, init: RequestInit) => Response | Promise<Response> | undefined) | undefined;
  vi.stubGlobal("fetch", vi.fn(async (path: string, init: RequestInit) => {
    calls.push({ path, init });
    const overridden = special?.(path, init); if (overridden) return overridden;
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu: ["domains","directory_assets","tasks"].map(mark => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return response({ results: (JSON.parse(String(init.body)).paths as string[]).map(() => true) });
    if (path.startsWith("/api/domain-selection/resolve?")) return response({ selection: choice, checkedAt: "2026-10-20T00:00:00Z" });
    if (path.startsWith("/api/directory/v4/receipt?")) return receiptFound ? response({ task }) : response({ error: "not_found" },404);
    if (path.startsWith("/api/directory/v4/task?")) return response({ task });
    if (path === "/api/directory/v4/sync") return response({ task, replayed: false });
    if (path === "/api/directory/v4/cancel") {
      task = { ...task, state: "cancelled", sourceState: "REVOKED", terminalAt: task.updatedAt, resultVersion: task.resultVersion + 1 };
      return response({ task });
    }
    throw new Error("unexpected synthetic route");
  }));
  const recoveryContext = createDirectoryV4RecoveryContext();
  const api = createDirectoryV4Controls(profile, storage, { assertCurrent() { if (!current) throw new ApiError("stale_epoch"); } }, recoveryContext);
  const store = { key, read: api.readIntent, recovery: api.recoveryIntent };
  return { api, store, seed, storage, recoveryContext, values, calls, setCurrent: (v: boolean) => { current = v; }, setTask: (v: Task) => { task = v; },
    setReceipt: (v: boolean) => { receiptFound = v; }, setSpecial: (v: typeof special) => { special = v; },
    mutations: () => calls.filter(c => c.path === "/api/directory/v4/sync" || c.path === "/api/directory/v4/cancel") };
}
const signal = () => new AbortController().signal;
describe("guarded original operation transport", () => {
  it("writes durable intent before one sync POST and prevents duplicate submission", async () => {
    const h = setup();
    const first = h.api.sync(input, proof, signal());
    await expect(h.api.sync(input, proof, signal())).rejects.toMatchObject({ code: "mutation_in_progress" });
    await expect(first).resolves.toMatchObject({ task: { taskUUID: "original-task" } });
    expect(h.mutations()).toHaveLength(1);
    await expect(h.api.sync(input, proof, signal())).rejects.toMatchObject({ code: "original_receipt_required" });
    expect(h.mutations()).toHaveLength(1);
    for (const c of h.calls) expect(c.init).toMatchObject({ credentials: "same-origin", cache: "no-store", redirect: "error" });
    expect([...h.values.values()].join()).not.toContain(proof.actorPassword);
    expect([...h.values.values()].join()).not.toContain(proof.totpCode);
  });
  it("sends zero business POSTs on failed intent persistence", async () => {
    const h = setup(); h.storage.setItem = () => { throw Error(); };
    await expect(h.api.sync(input, proof, signal())).rejects.toMatchObject({ code: "recovery_storage_failed" });
    expect(h.mutations()).toHaveLength(0);
  });
  it("cannot dispatch after ignored abort/stale epoch during prechecks", async () => {
    for (const kind of ["abort", "epoch"]) {
      const h = setup(); const controller = new AbortController();
      h.setSpecial(path => { if (path.startsWith("/api/domain-selection/resolve?")) {
        if (kind === "abort") controller.abort(); else h.setCurrent(false);
        return response({ selection: choice, checkedAt: "2026-10-20T00:00:00Z" });
      } });
      await expect(h.api.sync(input, proof, controller.signal)).rejects.toThrow(); expect(h.mutations()).toHaveLength(0);
    }
  });
  it("recovers committed response loss with original receipt and no sync replay", async () => {
    const h = setup(); h.setSpecial(path => { if (path === "/api/directory/v4/sync") return Promise.reject(Error("synthetic response loss")); });
    await expect(h.api.sync(input, proof, signal())).rejects.toMatchObject({ code: "network" });
    await expect(h.api.receipt(signal())).resolves.toMatchObject({ taskUUID: "original-task" });
    await expect(h.api.sync(input, proof, signal())).rejects.toThrow(); expect(h.mutations()).toHaveLength(1);
  });
  it("requires fresh explicit receipt-404 for each same-key retry", async () => {
    const h = setup(); h.seed(); h.setReceipt(false);
    await expect(h.api.sync(input, proof, signal())).rejects.toThrow(); expect(h.mutations()).toHaveLength(0);
    await expect(h.api.receipt(signal())).rejects.toMatchObject({ status: 404 });
    h.setReceipt(true); await h.api.sync(input, proof, signal()); expect(h.mutations()).toHaveLength(1);
  });
  it("does not rebind a stored UUID to a contradictory receipt", async () => {
    const h = setup(); h.seed({taskUUID:"different-task"});
    await expect(h.api.task(signal())).rejects.toMatchObject({ code: "invalid_response" }); expect(h.mutations()).toHaveLength(0);
  });
  it("cancel persists unknown before POST; lost response remains query-only across a new controller", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"});
    h.setSpecial(path => { if (path === "/api/directory/v4/cancel") {
      expect(h.store.recovery()?.cancelState).toBe("unknown"); return Promise.reject(Error("synthetic loss"));
    } });
    await expect(h.api.cancel(proof, signal())).rejects.toMatchObject({ code: "network" });
    const reload = createDirectoryV4Controls(profile, h.storage, { assertCurrent() {} });
    await reload.task(signal()); await expect(reload.cancel(proof, signal())).rejects.toMatchObject({ code: "cancel_query_only" });
    expect(h.mutations()).toHaveLength(1);
  });
  it("failed cancel-marker write sends zero cancel POSTs", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"});
    const set = h.storage.setItem; h.storage.setItem = (k,v) => { if (JSON.parse(v).cancelState) throw Error(); set(k,v); };
    await expect(h.api.cancel(proof, signal())).rejects.toMatchObject({ code: "recovery_storage_failed" }); expect(h.mutations()).toHaveLength(0);
  });
  it.each(["wrong_metadata","extra_key","duplicate_key","malformed","forbidden","network"])("keeps marker for %s", async variant => {
    const h = setup(); h.seed({taskUUID:"original-task"});
    h.setSpecial(path => { if (path !== "/api/directory/v4/cancel") return;
      if (variant === "network") return Promise.reject(Error());
      if (variant === "wrong_metadata") return response({ error: "invalid_credentials" },401,{ "Cache-Control": "private" });
      if (variant === "extra_key") return response({ error: "invalid_credentials", extra: true },401);
      if (variant === "forbidden") return response({ error: "forbidden" },403);
      return new Response(variant === "duplicate_key" ? '{"error":"invalid_credentials","error":"invalid_credentials"}' : 'broken', { status: 401, headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" } });
    });
    await expect(h.api.cancel(proof, signal())).rejects.toThrow(); expect(h.store.recovery()?.cancelState).toBe("unknown");
  });
  it("only exact proof rejection plus revalidation removes its marker", async () => {
    const h = setup(); h.seed({taskUUID:"original-task",receiptOnly:true});
    h.setSpecial(path => path === "/api/directory/v4/cancel" ? response({ error: "invalid_credentials" },401) : undefined);
    await expect(h.api.cancel(proof, signal())).rejects.toMatchObject({ code: "invalid_credentials" });
    expect(h.store.recovery()?.cancelState).toBeUndefined(); expect(h.store.recovery()?.receiptOnly).toBe(true);
    expect(h.calls.filter(c => c.path.startsWith("/api/domain-selection"))).toHaveLength(0);
  });
  it("stale proof-rejection response cannot clear marker", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"});
    h.setSpecial(path => { if (path === "/api/directory/v4/cancel") { h.setCurrent(false); return response({ error: "invalid_credentials" },401); } });
    await expect(h.api.cancel(proof, signal())).rejects.toThrow();
    expect(JSON.parse(h.values.get(h.store.key)!).cancelState).toBe("unknown");
    expect(() => h.api.recoveryIntent()).toThrow("stale_epoch");
  });
  it("cancel does not source-resolve or use producer eligibility", async () => {
    const h = setup(); h.seed({taskUUID:"original-task",receiptOnly:true});
    await expect(h.api.cancel(proof, signal())).resolves.toMatchObject({ state: "cancelled" });
    expect(h.calls.some(c => c.path.startsWith("/api/domain-selection"))).toBe(false);
    expect(h.calls.some(c => c.path.startsWith("/api/tasks/"))).toBe(false);
  });
});
const source = { server_name: choice.dcHostName, dc_host_name: "observed.synthetic.invalid", domain: choice.domain,
  naming_context: "DC=synthetic,DC=invalid", started_at: "2026-10-20T00:00:00Z", completed_at: "2026-10-20T00:00:01Z", elapsed_milliseconds: 1000, pages: 1 };
const row = { objectGUID: "00000000-0000-0000-0000-000000000001", distinguishedName: "CN=Group,DC=synthetic,DC=invalid", kind: "group",
  objectClass: ["group","top"], samAccountName: null, userAccountControl: null, objectSid: null, mail: null, description: null, whenCreated: null,
  primaryGroupID: null, servicePrincipalName: null, rawMember: [" z ","A"], returnedMemberCount: 2, memberReadState: "returned" };
const pageBody = () => ({ dictionaryVersion: 4, available: true, observationId: "original-task", source, list: [row], page: { pageIdx: 1, pageSize: 25, total: 1, totalPage: 1 } });
async function successful() {
  const h = setup(); h.seed({taskUUID:"original-task"});
  h.setTask({ ...queued(), state: "succeeded", sourceState: "SUCCESS", attempt: 1, progress: 100, terminalAt: "2026-10-20T00:00:00Z" });
  await h.api.receipt(signal()); return h;
}
describe("pinned v4 result and late authority", () => {
  it("accepts v4 page without observedCounts and detail with all five original pins", async () => {
    const h = await successful();
    h.setSpecial(path => path.startsWith("/api/directory/v4/observation?") ? response(pageBody()) :
      path.startsWith("/api/group-assets/v4/members?") ? response({ dictionaryVersion: 4, selection: { domainId: input.domainId, revision: input.expectedRevision, credentialRevision: input.expectedCredentialGeneration }, observationId: "original-task", source, object: row }) : undefined);
    await expect(h.api.page(1,25,signal())).resolves.toMatchObject({ dictionaryVersion: 4, list: [row] });
    await expect(h.api.detail(row.objectGUID,signal())).resolves.toMatchObject({ object: { rawMember: [" z ","A"] } });
    const path = h.calls.find(c => c.path.startsWith("/api/group-assets/v4/members?"))!.path;
    const query = new URL(path, "https://synthetic.invalid").searchParams;
    expect(Object.fromEntries(query)).toEqual({ domainId: input.domainId, expectedRevision: input.expectedRevision, expectedCredentialRevision: input.expectedCredentialGeneration, observationId: "original-task", objectGUID: row.objectGUID });
    expect(h.mutations()).toHaveLength(0);
  });
  it.each(["v3", "wrong_observation", "wrong_server", "unavailable", "wrong_actor"])("rejects %s without latest fallback", async variant => {
    const h = await successful();
    h.setSpecial(path => { if (!path.startsWith("/api/directory/v4/observation?")) return;
      if (variant === "unavailable") return response({ dictionaryVersion:4,available:false,list:[],page:{pageIdx:1,pageSize:25,total:0,totalPage:0} });
      const body = pageBody();
      return response(variant === "v3" ? { ...body, observedCounts: { group:1,user:0,computer:0 } } :
        variant === "wrong_observation" ? { ...body, observationId: "latest-task" } :
        variant === "wrong_server" ? { ...body, source: { ...source, server_name: source.dc_host_name } } : body,
        200, variant === "wrong_actor" ? { "X-ADTR-User-ID": "8" } : {});
    });
    await expect(h.api.page(1,25,signal())).rejects.toThrow();
    expect(h.calls.filter(c => c.path.startsWith("/api/directory/v4/observation?")).every(c => c.path.includes("observationId=original-task"))).toBe(true);
  });
  it("post-response function revocation rejects otherwise valid private facts", async () => {
    const h = await successful(); let received = false;
    h.setSpecial(path => {
      if (path.startsWith("/api/directory/v4/observation?")) { received = true; return response(pageBody()); }
      if (received && path === "/api/access/menu") return response({ menu: ["domains","directory_assets"].map(mark => ({ mark, auth:{ readable:false,writeable:false } })) });
    });
    await expect(h.api.page(1,25,signal())).rejects.toMatchObject({ status:403 });
    await expect(h.api.detail(row.objectGUID,signal())).rejects.toMatchObject({ code:"original_page_required" });
  });
  it("detail requires a group on a freshly accepted original page", async () => {
    const h = await successful();
    await expect(h.api.detail(row.objectGUID,signal())).rejects.toMatchObject({ code:"original_page_required" });
    expect(h.calls.some(c => c.path.startsWith("/api/group-assets/v4/members?"))).toBe(false);
  });
  it("source conflict stays receipt-only through reload and cannot be retried", async () => {
    const h = setup();
    h.setSpecial(path => path === "/api/directory/v4/sync" ? response({error:"revision_conflict"},409) : undefined);
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({ status:409 });
    expect(h.api.recoveryIntent()?.receiptOnly).toBe(true);
    h.setReceipt(false); await expect(h.api.receipt(signal())).rejects.toMatchObject({status:404});
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"original_receipt_required"});
    expect(h.mutations()).toHaveLength(1);
  });
  it("post-success metadata persistence failure keeps original key and blocks new sync", async () => {
    const h = setup(); const set = h.storage.setItem;
    h.storage.setItem = (k,v) => { if (JSON.parse(v).taskUUID) throw Error(); set(k,v); };
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    expect(JSON.parse(h.values.get(h.store.key)!).idempotencyKey).toBe(input.idempotencyKey);
    await expect(h.api.sync(input,proof,signal())).rejects.toThrow(); expect(h.mutations()).toHaveLength(1);
  });
  it("diagnostic terminals cannot clear the complete original intent", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"});
    h.setTask({...queued(),state:"dead_letter",sourceState:"FAILURE",attempt:1,error:"attempts_exhausted",terminalAt:"2026-10-20T00:00:00Z"});
    await expect(h.api.acknowledge(signal())).rejects.toMatchObject({code:"original_terminal_required"});
    expect(h.store.read().state).toBe("valid");
  });
});
describe("reviewed encapsulation and delayed boundaries", () => {
  it("exposes no marker/binding/terminal mutation primitives", () => {
    const h = setup();
    for (const key of ["begin","bind","restrict","markCancel","clearRejectedCancel","store"]) expect(key in h.api).toBe(false);
    expect(h.api.acknowledge.length).toBe(1);
  });
  it("does not persist a delayed first receipt after stale epoch", async () => {
    const h = setup(); h.seed();
    h.setSpecial(path => { if (path.startsWith("/api/directory/v4/receipt?")) { h.setCurrent(false); return response({task:queued()}); } });
    await expect(h.api.task(signal())).rejects.toMatchObject({code:"stale_epoch"});
    expect(JSON.parse(h.values.get(h.store.key)!).taskUUID).toBeUndefined();
  });
  it("same-actor session rotation during source resolve prevents dispatch", async () => {
    const h = setup(); let resolved = false;
    h.setSpecial(path => {
      if (path.startsWith("/api/domain-selection/resolve?")) { resolved = true; return response({selection:choice,checkedAt:"2026-10-20T00:00:00Z"}); }
      if (resolved && path === "/api/auth/me") return response({...profile,csrfToken:"rotated-session"});
    });
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({status:401}); expect(h.mutations()).toHaveLength(0);
  });
  it("serializes receipt/status reads even while transport is delayed", async () => {
    const h = setup(); h.seed(); let release!: (value:Response) => void;
    const held = new Promise<Response>(resolve => { release = resolve; });
    h.setSpecial(path => path === "/api/auth/me" ? held : undefined);
    const first = h.api.receipt(signal());
    await expect(h.api.task(signal())).rejects.toMatchObject({code:"read_in_progress"});
    await expect(h.api.receipt(signal())).rejects.toMatchObject({code:"read_in_progress"});
    h.setSpecial(undefined); release(response(profile)); await first;
  });
  it("rejects later status with same-millisecond older nanoseconds", async () => {
    const h = setup(); h.seed();
    h.setTask({...queued(),updatedAt:"2026-10-20T00:00:00.000000009Z"}); await h.api.receipt(signal());
    h.setTask({...queued(),updatedAt:"2026-10-20T00:00:00.000000008Z"});
    await expect(h.api.receipt(signal())).rejects.toMatchObject({code:"stale_result"});
  });
  it("snapshot input cannot be changed while authority check waits", async () => {
    const h = setup(); const mutable = {...input};
    const pending = h.api.sync(mutable,proof,signal()); mutable.expectedRevision = "1";
    await pending;
    expect(JSON.parse(String(h.mutations()[0].init.body)).expectedRevision).toBe(input.expectedRevision);
  });
  it("retains conservative marker when exact-proof removal readback fails", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"}); let removing = false;
    const set = h.storage.setItem; const get = h.storage.getItem;
    h.storage.setItem = (key,value) => {
      const before = get(key); set(key,value);
      if (before && JSON.parse(before).cancelState === "unknown" && !JSON.parse(value).cancelState) removing = true;
    };
    h.storage.getItem = key => { if (removing) { removing = false; return "{}"; } return get(key); };
    h.setSpecial(path => path === "/api/directory/v4/cancel" ? response({error:"invalid_credentials"},401) : undefined);
    await expect(h.api.cancel(proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    expect(h.api.recoveryIntent()?.cancelState).toBe("unknown");
    const reload = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}});
    expect(reload.recoveryIntent()?.cancelState).toBe("unknown");
    await expect(reload.cancel(proof,signal())).rejects.toMatchObject({code:"cancel_query_only"}); expect(h.mutations()).toHaveLength(1);
  });
  it("malformed, cross-owner and unavailable hints cannot dispatch", async () => {
    for (const raw of ["{broken",'{"owner":{"ID":8,"username":"other"}}', '{"storageVersion":1,"storageVersion":1}']) {
      const h = setup(); h.values.set(h.store.key,raw);
      expect(h.api.readIntent().state).toBe("unusable");
      await expect(h.api.sync(input,proof,signal())).rejects.toThrow(); expect(h.mutations()).toHaveLength(0);
      expect(h.values.get(h.store.key)).toBe(raw);
    }
    const h = setup(); h.storage.getItem = () => {throw Error();};
    expect(h.api.readIntent().state).toBe("storage_unavailable"); await expect(h.api.sync(input,proof,signal())).rejects.toThrow();
    expect(h.mutations()).toHaveLength(0);
  });
});
describe("delayed result generations", () => {
  it("postresolve access revocation prevents sync dispatch", async () => {
    const h = setup(); let resolved = false;
    h.setSpecial(path => {
      if (path.startsWith("/api/domain-selection/resolve?")) { resolved = true; return response({selection:choice,checkedAt:"2026-10-20T00:00:00Z"}); }
      if (resolved && path === "/api/access/menu") return response({menu:["domains","directory_assets","tasks"].map(mark=>({mark,auth:{readable:true,writeable:false}}))});
    });
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({status:403}); expect(h.mutations()).toHaveLength(0);
  });
  it("a newer page prevents held old page publication", async () => {
    const h = await successful(); let release!: (r:Response)=>void; let reached!: ()=>void; let pages = 0;
    const waiting = new Promise<void>(r=>{reached=r;}); const held = new Promise<Response>(r=>{release=r;});
    h.setSpecial(path => { if (path.startsWith("/api/directory/v4/observation?")) { if (++pages === 1) {reached();return held;} return response(pageBody()); } });
    const old = h.api.page(1,25,signal()); await waiting;
    await h.api.page(1,25,signal()); release(response(pageBody()));
    await expect(old).rejects.toMatchObject({code:"stale_result"});
  });
  it("a page replacement prevents held old detail publication even for same GUID", async () => {
    const h = await successful(); let release!: (r:Response)=>void; let reached!: ()=>void;
    const waiting = new Promise<void>(r=>{reached=r;}); const held = new Promise<Response>(r=>{release=r;});
    h.setSpecial(path => {
      if (path.startsWith("/api/directory/v4/observation?")) return response(pageBody());
      if (path.startsWith("/api/group-assets/v4/members?")) { reached(); return held; }
    });
    await h.api.page(1,25,signal()); const old = h.api.detail(row.objectGUID,signal()); await waiting;
    await h.api.page(1,25,signal());
    release(response({dictionaryVersion:4,selection:{domainId:input.domainId,revision:input.expectedRevision,credentialRevision:input.expectedCredentialGeneration},observationId:"original-task",source,object:row}));
    await expect(old).rejects.toMatchObject({code:"original_page_required"});
  });
});
describe("proof rejection and durable repair regressions", () => {
  it("a held proof-rejection receipt cannot clear marker after newer terminal receipt", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"}); let count = 0;
    let release!: (r:Response)=>void; let reached!: ()=>void;
    const waiting = new Promise<void>(r=>{reached=r;}); const held = new Promise<Response>(r=>{release=r;});
    h.setSpecial(path => {
      if (path === "/api/directory/v4/cancel") return response({error:"invalid_credentials"},401);
      if (path.startsWith("/api/directory/v4/receipt?") && ++count === 3) {reached();return held;}
    });
    const cancellation = h.api.cancel(proof,signal()); await waiting;
    h.setTask({...queued(),state:"cancelled",sourceState:"REVOKED",terminalAt:"2026-10-20T00:00:01Z",updatedAt:"2026-10-20T00:00:01Z",resultVersion:1});
    await h.api.receipt(signal()); release(response({task:queued()}));
    await expect(cancellation).rejects.toMatchObject({code:"stale_result"});
    expect(h.api.recoveryIntent()?.cancelState).toBe("unknown"); expect(h.mutations()).toHaveLength(1);
  });
  it("original receipt repairs failed UUID persistence without repeating sync", async () => {
    const h = setup(); const set = h.storage.setItem;
    h.storage.setItem = (key,value) => {if (JSON.parse(value).taskUUID) throw Error();set(key,value);};
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    expect(JSON.parse(h.values.get(h.store.key)!).taskUUID).toBeUndefined();
    h.storage.setItem = set;
    await expect(h.api.receipt(signal())).resolves.toMatchObject({taskUUID:"original-task"});
    expect(JSON.parse(h.values.get(h.store.key)!).taskUUID).toBe("original-task");
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"original_receipt_required"}); expect(h.mutations()).toHaveLength(1);
  });
});
describe("cumulative uncertainty and whole-intent completion", () => {
  it("later proof rejection never reopens uncertain sync without a new receipt check", async () => {
    const h = setup();
    h.setSpecial(path => path === "/api/directory/v4/sync" ? Promise.reject(Error("synthetic uncertain dispatch")) : undefined);
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"network"});
    h.setReceipt(false); await expect(h.api.receipt(signal())).rejects.toMatchObject({status:404});
    h.setSpecial(path => path === "/api/directory/v4/sync" ? response({error:"invalid_credentials"},401) : undefined);
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"invalid_credentials"});
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"original_receipt_required"});
    expect(h.mutations()).toHaveLength(2); expect(h.api.recoveryIntent()?.idempotencyKey).toBe(input.idempotencyKey);
  });
  it.each(["cancel_requested","succeeded","failed","cancelled"] as const)("reconciles unknown cancel to %s without another POST", async state => {
    const h = setup(); h.seed({taskUUID:"original-task",cancelState:"unknown"});
    h.setTask({...queued(),state,sourceState:state === "cancel_requested" ? "STARTED" : state === "succeeded" ? "SUCCESS" : state === "failed" ? "FAILURE" : "REVOKED",
      attempt:1,progress:state === "succeeded" ? 100 : 42,resultVersion:1,terminalAt:state === "cancel_requested" ? null : "2026-10-20T00:00:00Z"});
    await expect(h.api.task(signal())).resolves.toMatchObject({state});
    expect(h.api.recoveryIntent()?.cancelState).toBe(state === "cancel_requested" ? "accepted" : "terminal");
    await expect(h.api.cancel(proof,signal())).rejects.toMatchObject({code:"cancel_query_only"}); expect(h.mutations()).toHaveLength(0);
  });
  it("normal terminal acknowledgement removes the entire matching intent only", async () => {
    const h = await successful(); h.values.set("adtr.directory-sync-intent.v3:7:synthetic","legacy");
    await h.api.acknowledge(signal()); expect(h.api.readIntent().state).toBe("empty");
    expect(h.api.recoveryIntent()).toBeUndefined(); expect(h.values.get("adtr.directory-sync-intent.v3:7:synthetic")).toBe("legacy");
  });
  it("failed whole-intent removal stays recoverable and can be explicitly acknowledged later", async () => {
    const h = await successful(); const remove = h.storage.removeItem;
    h.storage.removeItem = () => {throw Error();};
    await expect(h.api.acknowledge(signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    expect(h.api.recoveryIntent()?.taskUUID).toBe("original-task");
    h.storage.removeItem = remove;
    await h.api.acknowledge(signal()); expect(h.api.readIntent().state).toBe("empty"); expect(h.mutations()).toHaveLength(0);
  });
  it("failed removal readback retains the whole hint, never just drops its cancel marker", async () => {
    const h = setup(); h.seed({taskUUID:"original-task",cancelState:"unknown"});
    h.setTask({...queued(),state:"cancelled",sourceState:"REVOKED",resultVersion:1,terminalAt:"2026-10-20T00:00:00Z"});
    h.storage.removeItem = () => {};
    await expect(h.api.acknowledge(signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    expect(h.api.recoveryIntent()).toMatchObject({taskUUID:"original-task",cancelState:"terminal"});
    expect(h.api.readIntent().state).toBe("valid"); expect(h.mutations()).toHaveLength(0);
  });
});
describe("opaque same-tab recovery context across security epochs", () => {
  it("retains failed UUID persistence across recreation and repairs only through original receipt", async () => {
    const h = setup(); const set = h.storage.setItem;
    h.storage.setItem = (key,value) => {if (JSON.parse(value).taskUUID) throw Error();set(key,value);};
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    const next = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    expect(next.recoveryIntent()?.taskUUID).toBe("original-task");
    await expect(next.sync(input,proof,signal())).rejects.toMatchObject({code:"original_receipt_required"});
    expect(() => h.api.recoveryIntent()).toThrow("stale_epoch");
    h.storage.setItem = set; await next.receipt(signal());
    expect(next.readIntent()).toMatchObject({state:"valid",intent:{taskUUID:"original-task"}}); expect(h.mutations()).toHaveLength(1);
  });
  it("failed sticky receiptOnly persistence survives epoch and cannot unlock on receipt404", async () => {
    const h = setup(); const set = h.storage.setItem;
    h.storage.setItem = (key,value) => {if (JSON.parse(value).receiptOnly) throw Error();set(key,value);};
    h.setSpecial(path => path === "/api/directory/v4/sync" ? response({error:"idempotency_conflict"},409) : undefined);
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    const next = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    expect(next.recoveryIntent()?.receiptOnly).toBe(true);
    h.setReceipt(false); await expect(next.receipt(signal())).rejects.toMatchObject({status:404});
    await expect(next.sync(input,proof,signal())).rejects.toMatchObject({code:"original_receipt_required"});
    h.storage.setItem = set; h.setReceipt(true); await next.receipt(signal());
    expect(next.readIntent()).toMatchObject({state:"valid",intent:{receiptOnly:true}}); expect(h.mutations()).toHaveLength(1);
  });
  it("failed unknown marker persistence remains query-only in the next epoch", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"}); const set = h.storage.setItem;
    h.storage.setItem = (key,value) => {if (JSON.parse(value).cancelState) throw Error();set(key,value);};
    await expect(h.api.cancel(proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"}); expect(h.mutations()).toHaveLength(0);
    const next = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    expect(next.recoveryIntent()?.cancelState).toBe("unknown");
    h.storage.setItem = set; await next.task(signal());
    await expect(next.cancel(proof,signal())).rejects.toMatchObject({code:"cancel_query_only"}); expect(h.mutations()).toHaveLength(0);
  });
  it("different owner cannot read old hint and returning owner starts with fresh receipt authority", async () => {
    const h = setup(); h.seed({taskUUID:"original-task",cancelState:"unknown"});
    const other = createDirectoryV4Controls({...profile,ID:8,username:"other"},h.storage,{assertCurrent(){}},h.recoveryContext);
    expect(other.readIntent().state).toBe("empty"); expect(other.recoveryIntent()).toBeUndefined();
    await expect(other.receipt(signal())).rejects.toMatchObject({code:"original_intent_required"});
    await expect(h.api.authorize("receipt",signal())).rejects.toMatchObject({code:"stale_epoch"});
    const returned = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    expect(returned.recoveryIntent()?.cancelState).toBe("unknown");
    await expect(returned.page(1,25,signal())).rejects.toMatchObject({code:"original_success_required"});
    await returned.receipt(signal()); expect(h.mutations()).toHaveLength(0);
    expect(() => other.readIntent()).toThrow("stale_epoch");
  });
  it("new context generation blocks an old pending predispatch response even if caller epoch callback stays valid", async () => {
    const h = setup(); let release!: (r:Response)=>void; let reached!: ()=>void;
    const waiting = new Promise<void>(r=>{reached=r;}); const held = new Promise<Response>(r=>{release=r;});
    h.setSpecial(path => {if(path.startsWith("/api/domain-selection/resolve?")){reached();return held;}});
    const pending = h.api.sync(input,proof,signal()); await waiting;
    createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    release(response({selection:choice,checkedAt:"2026-10-20T00:00:00Z"}));
    await expect(pending).rejects.toMatchObject({code:"stale_epoch"}); expect(h.mutations()).toHaveLength(0);
  });
  it("context has no data/mutators and rejects storage replacement", () => {
    const h = setup(); expect(Object.keys(h.recoveryContext)).toEqual([]); expect(Object.isFrozen(h.recoveryContext)).toBe(true);
    expect(() => createDirectoryV4Controls(profile,{...h.storage},{assertCurrent(){}},h.recoveryContext)).toThrow("invalid_recovery_context");
  });
});
describe("verified read-only recovery with unavailable persistence", () => {
  it("returns current receipt/task using private fallback, reports memory-only, blocks cancel, then repairs", async () => {
    const h = setup(); const set = h.storage.setItem; const get = h.storage.getItem;
    h.storage.setItem = (key,value) => {if(JSON.parse(value).taskUUID) throw Error();set(key,value);};
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    const next = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    h.storage.getItem = () => {throw Error();}; h.storage.setItem = () => {throw Error();};
    await expect(next.receipt(signal())).resolves.toMatchObject({taskUUID:"original-task"});
    await expect(next.task(signal())).resolves.toMatchObject({taskUUID:"original-task",state:"queued"});
    expect(next.recoveryDurability()).toBe("memory_only");
    await expect(next.cancel(proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"}); expect(h.mutations()).toHaveLength(1);
    h.storage.getItem = get; h.storage.setItem = set; await next.receipt(signal());
    expect(next.recoveryDurability()).toBe("durable"); expect(next.readIntent()).toMatchObject({state:"valid",intent:{taskUUID:"original-task"}});
  });
  it("read-only fallback never swallows denied authority or contradictory task identity", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"}); await h.api.receipt(signal());
    h.storage.getItem = () => {throw Error();};
    h.setSpecial(path => path.startsWith("/api/directory/v4/receipt?") ? response({error:"forbidden"},403) : undefined);
    await expect(h.api.receipt(signal())).rejects.toMatchObject({status:403});
    h.setSpecial(path => path.startsWith("/api/directory/v4/receipt?") ? response({task:{...queued(),taskUUID:"other-task"}}) : undefined);
    await expect(h.api.receipt(signal())).rejects.toMatchObject({code:"invalid_response"}); expect(h.mutations()).toHaveLength(0);
  });
  it("unknown cancel reconciliation can publish terminal reads despite marker-write failure", async () => {
    const h = setup(); h.seed({taskUUID:"original-task",cancelState:"unknown"});
    h.setTask({...queued(),state:"cancelled",sourceState:"REVOKED",resultVersion:1,terminalAt:"2026-10-20T00:00:00Z"});
    const set = h.storage.setItem; h.storage.setItem = (key,value) => {if(JSON.parse(value).cancelState === "terminal") throw Error();set(key,value);};
    await expect(h.api.task(signal())).resolves.toMatchObject({state:"cancelled"});
    expect(h.api.recoveryDurability()).toBe("memory_only"); expect(h.api.recoveryIntent()?.cancelState).toBe("terminal");
    await expect(h.api.acknowledge(signal())).rejects.toMatchObject({code:"recovery_storage_failed"}); expect(h.mutations()).toHaveLength(0);
  });
});
describe("conservative prewrite-read failures", () => {
  it("409 followed by unavailable getItem retains sticky restriction across epoch restoration", async () => {
    const h = setup(); const get = h.storage.getItem;
    h.setSpecial(path => {
      if(path === "/api/directory/v4/sync") {
        h.storage.getItem = () => {throw Error();};
        return response({error:"idempotency_conflict"},409);
      }
    });
    await expect(h.api.sync(input,proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    expect(h.api.recoveryIntent()?.receiptOnly).toBe(true); expect(h.api.recoveryDurability()).toBe("memory_only");
    h.storage.getItem = get;
    const next = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    expect(next.recoveryIntent()?.receiptOnly).toBe(true);
    h.setReceipt(false); await expect(next.receipt(signal())).rejects.toMatchObject({status:404});
    await expect(next.sync(input,proof,signal())).rejects.toMatchObject({code:"original_receipt_required"}); expect(h.mutations()).toHaveLength(1);
  });
  it("terminal marker transition survives unavailable prewrite read and remains query-only", async () => {
    const h = setup(); h.seed({taskUUID:"original-task",cancelState:"unknown"}); const get = h.storage.getItem;
    h.setTask({...queued(),state:"cancelled",sourceState:"REVOKED",resultVersion:1,terminalAt:"2026-10-20T00:00:00Z"});
    let reads = 0;
    h.setSpecial(path => {
      if(path.startsWith("/api/directory/v4/receipt?") && ++reads === 2) h.storage.getItem = () => {throw Error();};
      return undefined;
    });
    await expect(h.api.task(signal())).resolves.toMatchObject({state:"cancelled"});
    expect(h.api.recoveryIntent()?.cancelState).toBe("terminal");
    h.storage.getItem = get;
    const next = createDirectoryV4Controls(profile,h.storage,{assertCurrent(){}},h.recoveryContext);
    expect(next.recoveryIntent()?.cancelState).toBe("terminal");
    await expect(next.cancel(proof,signal())).rejects.toMatchObject({code:"cancel_query_only"}); expect(h.mutations()).toHaveLength(0);
  });
});
describe("proof-clear compensation identity boundary", () => {
  it("does not overwrite a replacement durable original during marker-clear readback failure", async () => {
    const h = setup(); h.seed({taskUUID:"original-task"}); const set = h.storage.setItem; const get = h.storage.getItem;
    let replacement = "";
    h.storage.setItem = (key,value) => {
      const before = get(key);
      if (before && JSON.parse(before).cancelState === "unknown" && !JSON.parse(value).cancelState) {
        replacement = JSON.stringify({...JSON.parse(value),idempotencyKey:"different-original-key",taskUUID:"different-task",receiptOnly:true,cancelState:"accepted"});
        set(key,replacement); return;
      }
      set(key,value);
    };
    h.setSpecial(path => path === "/api/directory/v4/cancel" ? response({error:"invalid_credentials"},401) : undefined);
    await expect(h.api.cancel(proof,signal())).rejects.toMatchObject({code:"recovery_storage_failed"});
    expect(h.values.get(h.store.key)).toBe(replacement);
    expect(h.api.recoveryIntent()).toMatchObject({idempotencyKey:input.idempotencyKey,taskUUID:"original-task",cancelState:"unknown"});
    expect(h.api.recoveryDurability()).toBe("memory_only"); expect(h.mutations()).toHaveLength(1);
  });
});

describe("source selection wire query contract", () => {
  it.each(["", "unverified", "testing", "verified", "error"] as const)(
    "omits empty observationState and preserves the exact nonempty enum: %s",
    async observationState => {
      const h = setup();
      h.setSpecial(path => {
        if (!path.startsWith("/api/domain-selection?")) return;
        const query = new URL(path, "https://synthetic.invalid").searchParams;
        // Match ParseSelectionFilter: absence means no filter; a PRESENT empty
        // value is invalid. Do not return success for the formerly broken URL.
        const state = query.get("observationState");
        if (state !== null && !["unverified", "testing", "verified", "error"].includes(state))
          return response({ error: "invalid_input" }, 400);
        return response({ page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 }, List: [], exhausted: true });
      });
      await expect(h.api.listSources({ pageIdx: 1, pageSize: 20, keyword: "", observationState }, signal()))
        .resolves.toMatchObject({ List: [], exhausted: true });
      const requests = h.calls.filter(call => call.path.startsWith("/api/domain-selection?"));
      expect(requests).toHaveLength(1);
      const query = new URL(requests[0].path, "https://synthetic.invalid").searchParams;
      expect(Object.fromEntries(query)).toEqual({ pageIdx: "1", pageSize: "20", keyword: "",
        ...(observationState ? { observationState } : {}) });
      expect(query.has("observationState")).toBe(observationState !== "");
      expect(query.getAll("observationState")).toEqual(observationState ? [observationState] : []);
      expect(h.mutations()).toHaveLength(0);
    },
  );
  it("still rejects unknown nonempty observationState before transport", async () => {
    const h = setup();
    await expect(h.api.listSources({ pageIdx: 1, pageSize: 20, keyword: "",
      observationState: "unexpected" as SourceChoice["connectionState"] }, signal()))
      .rejects.toMatchObject({ code: "invalid_input" });
    expect(h.calls).toHaveLength(0);
  });
});
