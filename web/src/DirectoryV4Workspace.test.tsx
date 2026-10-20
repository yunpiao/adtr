// Authored synthetic DOM/transport tests. NOT executed; not runtime acceptance.
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ActualDirectoryV4Workspace from "./DirectoryV4Workspace";
import { createDirectoryV4RecoveryContext, type DirectoryV4RecoveryContext } from "./directory-v4-controls-api";
import App from "./App";
import type { Profile } from "./api";
import type { Task } from "./task-api";
import type { SourceChoice } from "./domain-selection-api";
import { directoryV4IntentPrefix, directoryV4OwnerKey } from "./directory-v4-intent";
import { SESSION_INVALIDATION_STORAGE } from "./session-invalidation";
const profile: Profile = { ID: 7, username: "synthetic", role: "synthetic-role", priv: 1, csrfToken: "synthetic-session", needChangePwd: false, isExpired: false, mobile: "", email: "", remark: "", passStrength: "", hasMfa: true, passwordNotUpdatedDays: 0, pwdUpdateTm: "" };
const choice: SourceChoice = { domainId: "synthetic-domain", domain: "synthetic.invalid", revision: "9007199254740993", credentialRevision: "9007199254740995", dcHostName: "dc.synthetic.invalid", ldapAddr: "192.0.2.1", port: "636", mode: "ldaps", credentialConfigured: true, connectionState: "unverified", source: "configured_connection", lastTest: null };
const original = { domainId: choice.domainId, expectedRevision: choice.revision, expectedCredentialGeneration: choice.credentialRevision, idempotencyKey: "synthetic-original-key" };
const storageKey = directoryV4IntentPrefix + directoryV4OwnerKey(profile);
let recoveryContext: DirectoryV4RecoveryContext;
const stableStorage = { getItem: (key: string) => sessionStorage.getItem(key), setItem: (key: string, value: string) => sessionStorage.setItem(key, value), removeItem: (key: string) => sessionStorage.removeItem(key) };
function DirectoryV4Workspace(props: { profile: Profile; sessionChanged: () => void }) { return <ActualDirectoryV4Workspace {...props} recoveryContext={recoveryContext} storage={stableStorage} />; }
const queued = (): Task => ({ taskUUID: "original-task", taskName: "domain.directory_read.v4", domainId: choice.domainId, payloadVersion: 1, state: "queued", sourceState: "PENDING", error: "", createdAt: "2026-10-20T00:00:00Z", updatedAt: "2026-10-20T00:00:00Z", attempt: 0, maxAttempts: 1, progress: 0, resultVersion: 0, result: {}, cursor: {}, parentTaskUUID: "", terminalAt: null, archived: false, visibilityVersion: 0 });
function response(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-ADTR-User-ID": "7" } }); }
let current: Task, receiptFound: boolean, override: (path: string, init: RequestInit) => Response | Promise<Response> | undefined;
let fetcher: ReturnType<typeof vi.fn<(path: string, init?: RequestInit) => Promise<Response>>>;
const calls = (path: string) => fetcher.mock.calls.filter(([url]) => url.split("?")[0] === path);
const mutations = () => fetcher.mock.calls.filter(([url]) => ["/api/directory/v4/sync", "/api/directory/v4/cancel"].includes(url));
const click = (name: string) => fireEvent.click(screen.getByRole("button", { name }));
const seed = (patch: Record<string, unknown> = {}) => sessionStorage.setItem(storageKey, JSON.stringify({ ...original, storageVersion: 1, dictionaryVersion: 4, kind: "domain.directory_read.v4", payloadVersion: 1, owner: { ID: profile.ID, username: profile.username }, receiptOnly: false, ...patch }));
const deferred = () => { let resolve!: (value: Response) => void; const promise = new Promise<Response>(r => { resolve = r; }); return { promise, resolve }; };
beforeEach(() => {
  sessionStorage.clear(); recoveryContext = createDirectoryV4RecoveryContext(); current = queued(); receiptFound = true; override = () => undefined;
  window.history.replaceState({}, "", "#account");
  fetcher = vi.fn(async (path: string, init: RequestInit = {}) => {
    const special = override(path, init); if (special) return special;
    if (path === "/api/auth/me") return response(profile);
    if (path === "/api/access/menu") return response({ menu: ["domains", "directory_assets", "tasks"].map(mark => ({ mark, auth: { readable: true, writeable: true } })) });
    if (path === "/api/access/check") return response({ results: (JSON.parse(String(init.body)).paths as string[]).map(() => true) });
    if (path.startsWith("/api/domain-selection/resolve?")) return response({ selection: choice, checkedAt: "2026-10-20T00:00:00Z" });
    if (path.startsWith("/api/domain-selection?")) return response({ List: [choice], page: { pageIdx: 1, pageSize: 10, total: 1, totalPage: 1 }, exhausted: true });
    if (path.startsWith("/api/directory/v4/receipt?")) return receiptFound ? response({ task: current }) : response({ error: "not_found" }, 404);
    if (path.startsWith("/api/directory/v4/task?")) return response({ task: current });
    if (path === "/api/directory/v4/sync") { receiptFound = true; return response({ task: current, replayed: false }); }
    if (path === "/api/directory/v4/cancel") { current = { ...current, state: "cancelled", sourceState: "REVOKED", terminalAt: current.updatedAt, resultVersion: current.resultVersion + 1 }; return response({ task: current }); }
    throw new Error("unexpected synthetic route");
  }); vi.stubGlobal("fetch", fetcher);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); sessionStorage.clear(); window.history.replaceState({}, "", "#account"); });
async function openNew() {
  const view = render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
  fireEvent.click(await screen.findByRole("button", { name: /synthetic.invalid · 配置/ }));
  await screen.findByRole("button", { name: "准备采集字典 4" });
  return view;
}
function proof(action: "sync" | "cancel") {
  click(action === "sync" ? "准备采集字典 4" : "申请取消原任务");
  const form = screen.getByRole("form", { name: action === "sync" ? "字典 4 采集证明" : "原任务取消证明" });
  const password = within(form).getByLabelText("当前用户密码") as HTMLInputElement;
  const totp = within(form).getByLabelText("当前六位验证码") as HTMLInputElement;
  fireEvent.change(password, { target: { value: "synthetic-proof-only" } }); fireEvent.change(totp, { target: { value: "123456" } });
  return { form, password, totp };
}
describe("v4 original-operation DOM boundaries", () => {
  it("keeps direct entry private until path checks succeed", async () => {
    const held = deferred(); override = path => path === "/api/access/check" ? held.promise : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    await waitFor(() => expect(calls("/api/access/check")).toHaveLength(1));
    expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(screen.queryByLabelText("当前用户密码")).toBeNull();
    await act(async () => held.resolve(response({ results: [false, false] })));
    expect(mutations()).toHaveLength(0);
  });
  it("clears uncontrolled proof synchronously before async admission and double-submit sends once", async () => {
    await openNew(); const fields = proof("sync"); const held = deferred();
    expect(fields.form).toHaveAttribute("autocomplete", "off"); expect(fields.password).toHaveAttribute("autocomplete", "off"); expect(fields.totp).toHaveAttribute("autocomplete", "off");
    override = path => path === "/api/auth/me" ? held.promise : undefined;
    act(() => { fireEvent.submit(fields.form); expect(fields.password.value).toBe(""); expect(fields.totp.value).toBe(""); fireEvent.submit(fields.form); });
    expect(mutations()).toHaveLength(0);
    override = () => undefined; await act(async () => held.resolve(response(profile)));
    await screen.findByRole("heading", { name: "原任务 original-task" }); expect(calls("/api/directory/v4/sync")).toHaveLength(1);
    expect(sessionStorage.getItem(storageKey)).not.toContain("synthetic-proof-only"); expect(sessionStorage.getItem(storageKey)).not.toContain("123456");
  });
  it("failed durable write clears proof and sends zero business POSTs", async () => {
    await openNew(); receiptFound = false; const fields = proof("sync"); vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("synthetic storage unavailable"); });
    fireEvent.submit(fields.form); expect(fields.password.value).toBe(""); expect(fields.totp.value).toBe("");
    await screen.findByText(/无法可靠保存并读回原操作记录/); expect(mutations()).toHaveLength(0);
    expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull();
    click("重新核验并恢复"); await waitFor(() => expect(calls("/api/directory/v4/receipt").length).toBeGreaterThan(0));
    await waitFor(() => expect(screen.getByRole("button", { name: "重新核验并恢复" })).toBeEnabled());
    expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("keeps post-success memory-only recovery across workspace remount without another sync", async () => {
    const view = await openNew(); const originalSet = Storage.prototype.setItem; let writes = 0;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key: string, value: string) {
      if (key.startsWith(directoryV4IntentPrefix) && ++writes > 1) throw new Error("synthetic metadata persistence failure");
      originalSet.call(this, key, value);
    });
    fireEvent.submit(proof("sync").form); await screen.findByText(/无法可靠保存并读回原操作记录/);
    expect(calls("/api/directory/v4/sync")).toHaveLength(1); view.unmount();
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByRole("heading", { name: "原任务 original-task" }); await screen.findByText(/当前状态已核验，但恢复记录暂不能可靠保存/);
    expect(screen.queryByRole("button", { name: "申请取消原任务" })).toBeNull(); expect(screen.queryByRole("button", { name: "准备采集字典 4" })).toBeNull();
    expect(calls("/api/directory/v4/sync")).toHaveLength(1); expect(calls("/api/directory/v4/cancel")).toHaveLength(0);
  });
  it("closing the proof form and replacing source clear the actual input nodes", async () => {
    await openNew(); const first = proof("sync"); click("关闭证明输入"); expect(first.password.value).toBe(""); expect(first.totp.value).toBe("");
    const second = proof("sync"); fireEvent.click(screen.getByRole("button", { name: /synthetic.invalid · 配置/ }));
    expect(second.password.value).toBe(""); expect(second.totp.value).toBe(""); expect(mutations()).toHaveLength(0);
    await screen.findByRole("button", { name: "准备采集字典 4" });
  });
  it("hidden visibility clears proof and protected task facts", async () => {
    seed({ taskUUID: current.taskUUID }); render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "申请取消原任务" }); const fields = proof("cancel");
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden"); act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(fields.password.value).toBe(""); expect(fields.totp.value).toBe(""); expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull();
  });
  it("current task permission loss hides previously verified private facts", async () => {
    seed({ taskUUID: current.taskUUID }); render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "申请取消原任务" }); const fields = proof("cancel");
    override = path => path === "/api/access/menu" ? response({ menu: ["domains", "directory_assets"].map(mark => ({ mark, auth: { readable: true } })) }) : undefined;
    click("查询原操作回执与状态"); await screen.findByText(/当前权限不可用/);
    expect(fields.password.value).toBe(""); expect(screen.queryByText(/synthetic-original-key/)).toBeNull(); expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("committed sync response loss recovers receipt with no replay", async () => {
    await openNew(); override = path => path === "/api/directory/v4/sync" ? Promise.reject(new Error("synthetic loss")) : undefined;
    fireEvent.submit(proof("sync").form); await screen.findByText(/请点击“重新核验并恢复”/);
    override = () => undefined; click("重新核验并恢复"); await screen.findByRole("heading", { name: "原任务 original-task" });
    expect(calls("/api/directory/v4/sync")).toHaveLength(1); expect(screen.queryByText("使用原键和原版本显式重试")).toBeNull();
  });
  it("receipt 404 offers only explicit same-key and same-pins retry", async () => {
    seed(); receiptFound = false; render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "使用原键和原版本显式重试" }));
    const form = screen.getByRole("form", { name: "字典 4 采集证明" });
    fireEvent.change(within(form).getByLabelText("当前用户密码"), { target: { value: "synthetic-proof-only" } });
    fireEvent.change(within(form).getByLabelText("当前六位验证码"), { target: { value: "123456" } }); fireEvent.submit(form);
    await screen.findByRole("heading", { name: "原任务 original-task" });
    expect(JSON.parse(String(calls("/api/directory/v4/sync")[0][1]?.body))).toMatchObject(original);
  });
  it("a guarded retry with changed source pins sends zero POST and persists receipt-only", async () => {
    seed(); receiptFound = false; render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "使用原键和原版本显式重试" }));
    const form = screen.getByRole("form", { name: "字典 4 采集证明" });
    fireEvent.change(within(form).getByLabelText("当前用户密码"), { target: { value: "synthetic-proof-only" } });
    fireEvent.change(within(form).getByLabelText("当前六位验证码"), { target: { value: "123456" } });
    override = path => path.startsWith("/api/domain-selection/resolve?") ? response({ error: "revision_conflict" }, 409) : undefined;
    fireEvent.submit(form); await screen.findByText(/原始来源或观测与当前配置冲突/);
    expect(mutations()).toHaveLength(0); expect(JSON.parse(sessionStorage.getItem(storageKey)!).receiptOnly).toBe(true);
    override = () => undefined; click("重新核验并恢复"); await screen.findByText(/原回执暂未找到/);
    expect(screen.queryByRole("button", { name: "使用原键和原版本显式重试" })).toBeNull();
  });
  it("requires a fresh explicit receipt404 before another uncertain retry", async () => {
    seed(); receiptFound = false; render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "使用原键和原版本显式重试" }));
    const form = screen.getByRole("form", { name: "字典 4 采集证明" });
    fireEvent.change(within(form).getByLabelText("当前用户密码"), { target: { value: "synthetic-proof-only" } });
    fireEvent.change(within(form).getByLabelText("当前六位验证码"), { target: { value: "123456" } });
    override = path => path === "/api/directory/v4/sync" ? Promise.reject(new Error("synthetic pre-arrival loss")) : undefined;
    fireEvent.submit(form); await screen.findByText(/请点击“重新核验并恢复”/);
    const before = calls("/api/directory/v4/receipt").length;
    expect(screen.queryByRole("button", { name: "使用原键和原版本显式重试" })).toBeNull(); expect(calls("/api/directory/v4/sync")).toHaveLength(1);
    click("重新核验并恢复"); await screen.findByRole("button", { name: "使用原键和原版本显式重试" });
    expect(calls("/api/directory/v4/receipt").length).toBeGreaterThan(before); expect(calls("/api/directory/v4/sync")).toHaveLength(1);
  });
  it("cancel remains independent of source resolution and producer eligibility", async () => {
    seed({ taskUUID: current.taskUUID, receiptOnly: true });
    override = path => path.startsWith("/api/domain-selection/resolve?") ? response({ error: "revision_conflict" }, 409) : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByRole("button", { name: "申请取消原任务" }); fireEvent.submit(proof("cancel").form);
    await screen.findByText("任务已进入取消终态。 进度 0%");
    expect(calls("/api/domain-selection/resolve")).toHaveLength(0); expect(calls("/api/directory/v4/cancel")).toHaveLength(1);
    expect(JSON.parse(String(calls("/api/directory/v4/cancel")[0][1]?.body)).taskUUID).toBe("original-task");
  });
  it("lost cancel response stays query-only even if original reads still show running", async () => {
    current = { ...current, state: "running", sourceState: "STARTED", attempt: 1, progress: 41 }; seed({ taskUUID: current.taskUUID });
    override = path => path === "/api/directory/v4/cancel" ? Promise.reject(new Error("synthetic loss")) : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "申请取消原任务" });
    fireEvent.submit(proof("cancel").form); await screen.findByText(/请点击“重新核验并恢复”/);
    click("重新核验并恢复"); await screen.findByRole("heading", { name: "原任务 original-task" });
    expect(screen.queryByRole("button", { name: "申请取消原任务" })).toBeNull(); expect(calls("/api/directory/v4/cancel")).toHaveLength(1);
    expect(JSON.parse(sessionStorage.getItem(storageKey)!).cancelState).toBe("unknown");
  });
  it("same running heartbeat keeps proof DOM nodes and values stable", async () => {
    current = { ...current, state: "running", sourceState: "STARTED", attempt: 1, progress: 41 }; seed({ taskUUID: current.taskUUID });
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "申请取消原任务" });
    const fields = proof("cancel"); current = { ...current, updatedAt: "2026-10-20T00:00:01Z" };
    click("查询原操作回执与状态"); await waitFor(() => expect(screen.getByRole("button", { name: "查询原操作回执与状态" })).toBeEnabled());
    expect(screen.getByLabelText("当前用户密码")).toBe(fields.password); expect(fields.password.value).toBe("synthetic-proof-only"); expect(fields.totp.value).toBe("123456");
    expect(mutations()).toHaveLength(0);
  });
  it.each(["blur", "pagehide", "hashchange", "popstate", "session"])("%s clears proof immediately and keeps only recovery hint", async channel => {
    seed({ taskUUID: current.taskUUID }); render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "申请取消原任务" }); const fields = proof("cancel");
    act(() => { if (channel === "session") window.dispatchEvent(new StorageEvent("storage", { key: SESSION_INVALIDATION_STORAGE, storageArea: localStorage, newValue: "abcdef0123456789abcdef0123456789" })); else window.dispatchEvent(new Event(channel)); });
    expect(fields.password.value).toBe(""); expect(fields.totp.value).toBe(""); expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull(); expect(sessionStorage.getItem(storageKey)).not.toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("never mounts a source chooser while original receipt recovery is pending", async () => {
    seed({ taskUUID: current.taskUUID }); const held = deferred();
    override = path => path.startsWith("/api/directory/v4/receipt?") ? held.promise : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    await waitFor(() => expect(calls("/api/directory/v4/receipt")).toHaveLength(1));
    expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(calls("/api/domain-selection")).toHaveLength(0);
    expect(screen.queryByText(/synthetic-original-key/)).toBeNull();
    await act(async () => held.resolve(response({ task: current })));
    await screen.findByRole("heading", { name: "原任务 original-task" });
  });
  it("task-bound receipt404 hides saved pins and does not offer sync retry", async () => {
    seed({ taskUUID: current.taskUUID }); receiptFound = false;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText(/原回执暂未找到/);
    expect(screen.queryByText(/synthetic-original-key/)).toBeNull(); expect(screen.queryByText(/原任务 original-task/)).toBeNull();
    expect(screen.queryByRole("button", { name: "使用原键和原版本显式重试" })).toBeNull(); expect(calls("/api/domain-selection")).toHaveLength(0); expect(mutations()).toHaveLength(0);
  });
  it("hides established original identifiers when a later receipt/status refresh returns404", async () => {
    seed({ taskUUID: current.taskUUID }); render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByRole("heading", { name: "原任务 original-task" }); expect(screen.getByText(/synthetic-original-key/)).toBeVisible();
    receiptFound = false; click("查询原操作回执与状态"); await screen.findByText(/当前未找到原回执或对象/);
    expect(screen.queryByText(/synthetic-original-key/)).toBeNull(); expect(screen.queryByText(/原任务 original-task/)).toBeNull();
    expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(sessionStorage.getItem(storageKey)).not.toBeNull();
  });
  it("blocks unusable records without deleting or replacing them", async () => {
    sessionStorage.setItem(storageKey, '{"dictionaryVersion":3}'); render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    await screen.findByText(/原操作恢复记录不可用/); expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(sessionStorage.getItem(storageKey)).toBe('{"dictionaryVersion":3}'); expect(mutations()).toHaveLength(0);
  });
  it("does not expose saved identity when task read authority is denied", async () => {
    seed({ taskUUID: current.taskUUID }); override = path => path === "/api/access/menu" ? response({ menu: ["domains", "directory_assets"].map(mark => ({ mark, auth: { readable: true } })) }) : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByText(/原操作恢复当前不可用/);
    expect(screen.queryByText(/synthetic-original-key/)).toBeNull(); expect(screen.queryByText(/原任务 original-task/)).toBeNull(); expect(calls("/api/directory/v4/receipt")).toHaveLength(0);
  });
  it("rejects a late protected result after deactivation even if fetch ignores abort", async () => {
    seed({ taskUUID: current.taskUUID }); const held = deferred(); override = path => path.startsWith("/api/directory/v4/receipt?") ? held.promise : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await waitFor(() => expect(calls("/api/directory/v4/receipt")).toHaveLength(1));
    act(() => window.dispatchEvent(new Event("blur"))); await act(async () => held.resolve(response({ task: current })));
    expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull(); expect(screen.queryByLabelText("当前用户密码")).toBeNull();
  });
  it.each(["csrf", "actor"])("%s replacement rejects a held response from the old identity", async change => {
    seed({ taskUUID: current.taskUUID }); const held = deferred();
    override = path => path.startsWith("/api/directory/v4/receipt?") ? held.promise : undefined;
    const changed = vi.fn(); const view = render(<DirectoryV4Workspace profile={profile} sessionChanged={changed} />);
    await waitFor(() => expect(calls("/api/directory/v4/receipt")).toHaveLength(1));
    const replacement = change === "csrf" ? { ...profile, csrfToken: "replacement-session" } : { ...profile, ID: 8, username: "replacement-actor", csrfToken: "replacement-session" };
    override = path => path === "/api/auth/me" ? response(replacement) : path === "/api/access/menu" ? response({ menu: [] }) : undefined;
    view.rerender(<DirectoryV4Workspace profile={replacement} sessionChanged={changed} />);
    await act(async () => held.resolve(response({ task: current })));
    expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull(); expect(screen.queryByText(/synthetic-original-key/)).toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("same-actor task authority revocation after delayed response clears proof and facts", async () => {
    seed({ taskUUID: current.taskUUID }); render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "申请取消原任务" }); const fields = proof("cancel");
    const held = deferred(); let revoked = false;
    override = path => path.startsWith("/api/directory/v4/task?") ? held.promise : revoked && path === "/api/access/menu" ? response({ menu: [] }) : undefined;
    click("查询原操作回执与状态"); await waitFor(() => expect(calls("/api/directory/v4/task")).toHaveLength(1)); revoked = true;
    await act(async () => held.resolve(response({ task: current }))); await screen.findByText(/当前权限不可用/);
    expect(fields.password.value).toBe(""); expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull();
  });
  it("same-actor source authority revocation during a held list cannot publish sources", async () => {
    const held = deferred(); let revoked = false;
    override = path => path.startsWith("/api/domain-selection?") ? held.promise : revoked && path === "/api/access/menu" ? response({ menu: [] }) : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await waitFor(() => expect(calls("/api/domain-selection")).toHaveLength(1)); revoked = true;
    await act(async () => held.resolve(response({ List: [choice], page: { pageIdx: 1, pageSize: 10, total: 1, totalPage: 1 }, exhausted: true })));
    await screen.findByText(/当前权限不可用/); expect(screen.queryByRole("button", { name: /synthetic.invalid · 配置/ })).toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("same-actor reader grant loss during held original result prevents publication", async () => {
    current = { ...current, state: "succeeded", sourceState: "SUCCESS", attempt: 1, progress: 100, terminalAt: current.updatedAt }; seed({ taskUUID: current.taskUUID });
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "查看本任务的原始组成员结果" });
    const held = deferred(); let revoked = false;
    override = path => path.startsWith("/api/directory/v4/observation?") ? held.promise : revoked && path === "/api/access/menu" ? response({ menu: [] }) : undefined;
    click("查看本任务的原始组成员结果"); await waitFor(() => expect(calls("/api/directory/v4/observation")).toHaveLength(1));
    expect(new URL(calls("/api/directory/v4/observation")[0][0], "https://synthetic.invalid").searchParams.get("observationId")).toBe("original-task"); revoked = true;
    await act(async () => held.resolve(response({ dictionaryVersion: 4, available: true, observationId: "original-task", source: { server_name: choice.dcHostName, dc_host_name: choice.dcHostName, domain: choice.domain, naming_context: "DC=synthetic,DC=invalid", started_at: "2026-10-20T00:00:00Z", completed_at: "2026-10-20T00:00:01Z", elapsed_milliseconds: 1000, pages: 1 }, list: [], page: { pageIdx: 1, pageSize: 25, total: 0, totalPage: 0 } })));
    await screen.findByText(/当前权限不可用/); expect(screen.queryByRole("region", { name: "本任务的原始组成员结果" })).toBeNull(); expect(screen.queryByText(/本观测共有/)).toBeNull();
  });
  it("read-only actor can inspect authorized sources but has no proof or business mutation", async () => {
    override = path => path === "/api/access/menu" ? response({ menu: ["domains", "directory_assets"].map(mark => ({ mark, auth: { readable: true, writeable: false } })) }) : undefined;
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: /synthetic.invalid · 配置/ })); await screen.findByText(/已选来源：synthetic.invalid/);
    expect(screen.queryByLabelText("当前用户密码")).toBeNull(); expect(screen.queryByRole("button", { name: "准备采集字典 4" })).toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("hidden view restores only through a fresh explicit authority and original receipt check", async () => {
    seed({ taskUUID: current.taskUUID }); render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("heading", { name: "原任务 original-task" });
    const before = calls("/api/directory/v4/receipt").length;
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden"); act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull(); visibility.mockReturnValue("visible"); click("重新核验并恢复");
    await screen.findByRole("heading", { name: "原任务 original-task" }); expect(calls("/api/directory/v4/receipt").length).toBeGreaterThan(before); expect(mutations()).toHaveLength(0);
  });
  it("acknowledgement receipt404 hides the previously verified terminal and retains original storage", async () => {
    current = { ...current, state: "cancelled", sourceState: "REVOKED", terminalAt: current.updatedAt }; seed({ taskUUID: current.taskUUID });
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "确认结束原操作并清除恢复记录" });
    receiptFound = false; click("确认结束原操作并清除恢复记录"); await screen.findByText(/当前未找到原回执或对象/);
    expect(screen.queryByRole("region", { name: "原任务状态" })).toBeNull(); expect(screen.queryByText(/synthetic-original-key/)).toBeNull();
    expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(sessionStorage.getItem(storageKey)).not.toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("failed terminal acknowledgement never exposes a replacement operation", async () => {
    current = { ...current, state: "cancelled", sourceState: "REVOKED", terminalAt: current.updatedAt }; seed({ taskUUID: current.taskUUID });
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "确认结束原操作并清除恢复记录" });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("synthetic removal failure"); });
    click("确认结束原操作并清除恢复记录"); await screen.findByText(/无法可靠保存并读回原操作记录/);
    expect(screen.queryByRole("button", { name: "确认结束原操作并清除恢复记录" })).toBeNull();
    expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(sessionStorage.getItem(storageKey)).not.toBeNull(); expect(mutations()).toHaveLength(0);
  });
  it("does not read result automatically on success or fabricate historical URLs", async () => {
    current = { ...current, state: "succeeded", sourceState: "SUCCESS", attempt: 1, progress: 100, terminalAt: current.updatedAt }; seed({ taskUUID: current.taskUUID });
    render(<DirectoryV4Workspace profile={profile} sessionChanged={vi.fn()} />); await screen.findByRole("button", { name: "查看本任务的原始组成员结果" });
    expect(calls("/api/directory/v4/observation")).toHaveLength(0); expect(screen.queryByRole("link")).toBeNull();
  });
});
describe("additive v4 App route", () => {
  it("opens exact v4 collection hash behind current access checks", async () => {
    window.history.replaceState({}, "", "#directory-v4"); render(<App />);
    await screen.findByRole("heading", { name: "字典 4 目录采集" }); await screen.findByRole("region", { name: "字典 4 已保存来源" });
    expect(screen.getAllByRole("main")).toHaveLength(1);
    expect(calls("/api/access/check").length).toBeGreaterThan(0); expect(mutations()).toHaveLength(0);
  });
  it("App-owned context retains failed cancel marker across route unmount and prevents replay", async () => {
    seed({ taskUUID: current.taskUUID }); window.history.replaceState({}, "", "#directory-v4"); render(<App />);
    await screen.findByRole("button", { name: "申请取消原任务" });
    const originalSet = Storage.prototype.setItem;
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, key: string, value: string) {
      if (key.startsWith(directoryV4IntentPrefix) && JSON.parse(value).cancelState === "unknown") throw new Error("synthetic marker failure");
      originalSet.call(this, key, value);
    });
    fireEvent.submit(proof("cancel").form); await screen.findByText(/无法可靠保存并读回原操作记录/); expect(calls("/api/directory/v4/cancel")).toHaveLength(0);
    act(() => { window.history.pushState({}, "", "#account"); window.dispatchEvent(new PopStateEvent("popstate")); }); await screen.findByRole("heading", { name: "账户概览" });
    act(() => { window.history.pushState({}, "", "#directory-v4"); window.dispatchEvent(new PopStateEvent("popstate")); }); await screen.findByRole("heading", { name: "原任务 original-task" });
    await screen.findByText(/当前状态已核验，但恢复记录暂不能可靠保存/);
    expect(screen.queryByRole("button", { name: "申请取消原任务" })).toBeNull(); expect(screen.queryByRole("region", { name: "字典 4 已保存来源" })).toBeNull(); expect(calls("/api/directory/v4/cancel")).toHaveLength(0);
    vi.restoreAllMocks(); click("查询原操作回执与状态");
    await waitFor(() => expect(JSON.parse(sessionStorage.getItem(storageKey)!).cancelState).toBe("unknown"));
    expect(screen.queryByRole("button", { name: "申请取消原任务" })).toBeNull(); expect(calls("/api/directory/v4/cancel")).toHaveLength(0);
  });
  it.each(["#directory-v4?taskUUID=historical", "#directory-v4/historical"])("rejects unsupported historical route %s", async hash => {
    window.history.replaceState({}, "", hash); render(<App />); await screen.findByRole("heading", { name: "账户概览" });
    expect(screen.queryByRole("heading", { name: "字典 4 目录采集" })).toBeNull(); expect(calls("/api/directory/v4/receipt")).toHaveLength(0);
  });
});
