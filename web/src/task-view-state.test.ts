import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import { taskStates } from "./task-api";
import {
  defaultTaskListReturn,
  defaultTaskQuery,
  readTaskNavigation,
  validTaskListQuery,
  writeTaskNavigation,
  type TaskListQuery,
  type TaskNavigationSnapshot,
  type TaskNavigationView,
} from "./task-view-state";

const namespace = "adtr.task-navigation";
const taskId = "10000000-0000-4000-8000-000000000001";
const secondId = "20000000-0000-4000-8000-000000000002";
const actor = {
  ID: 11,
  username: "synthetic-one",
  csrfToken: "synthetic-secret-csrf",
  email: "synthetic@example.invalid",
} as Profile;
const filtered: TaskListQuery = {
  pageIdx: 3,
  pageSize: 50,
  domainId: "synthetic-domain",
  state: "retry_wait",
  taskName: "infrastructure.health",
  visibility: "all",
};
const listReturn = { focusTaskId: taskId, scrollY: 1234 };
const defaults = (): TaskNavigationSnapshot => ({
  view: { type: "list" },
  query: defaultTaskQuery(),
  listReturn: defaultTaskListReturn(),
});
const snapshot = (
  view: TaskNavigationView = { type: "detail", id: taskId, archived: true },
): TaskNavigationSnapshot => ({
  view,
  query: { ...filtered },
  listReturn: { ...listReturn },
});
const hashFor = (view: TaskNavigationView) =>
  `#tasks/${view.type}${view.type === "detail" || view.type === "schedule-detail" ? `/${view.id}` : ""}`;
function stored(
  view: TaskNavigationView = { type: "detail", id: taskId, archived: true },
) {
  return {
    version: 1,
    owner: { ID: actor.ID, username: actor.username },
    hash: hashFor(view),
    ...snapshot(view),
  };
}
function install(value: unknown, hash = `#tasks/detail/${taskId}`) {
  window.history.replaceState({ [namespace]: value }, "", hash);
}
function travel(direction: "back" | "forward") {
  return new Promise<void>((resolve) => {
    window.addEventListener("popstate", () => resolve(), { once: true });
    window.history[direction]();
  });
}

beforeEach(() => {
  window.history.replaceState({}, "", "/#tasks");
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/#tasks");
});

describe("task navigation's per-entry account-bound read state", () => {
  it("returns fresh defaults without trusting an unowned URL", () => {
    window.history.replaceState({}, "", `#tasks/detail/${taskId}`);
    const first = readTaskNavigation(actor);
    expect(first).toEqual(defaults());
    first.query.domainId = "changed";
    first.listReturn.focusTaskId = taskId;
    expect(readTaskNavigation(actor)).toEqual(defaults());
    const query = defaultTaskQuery();
    query.pageIdx = 7;
    const location = defaultTaskListReturn();
    location.scrollY = 9;
    expect(defaultTaskQuery().pageIdx).toBe(1);
    expect(defaultTaskListReturn().scrollY).toBe(0);
  });

  it("persists only whitelisted fields and restores after a module reload", async () => {
    const requested = snapshot();
    expect(writeTaskNavigation(actor, requested, "replace")).toEqual(requested);
    expect(window.location.hash).toBe(`#tasks/detail/${taskId}`);
    expect(window.history.state).toEqual({ [namespace]: stored() });
    const serialized = JSON.stringify(window.history.state);
    expect(serialized).not.toContain(actor.csrfToken);
    expect(serialized).not.toContain(actor.email);
    expect(serialized).not.toContain("actorPassword");
    expect(serialized).not.toContain("totpCode");
    vi.resetModules();
    const reloaded = await import("./task-view-state");
    const renewed = { ...actor, csrfToken: "rotated" };
    expect(reloaded.readTaskNavigation(renewed)).toEqual(requested);
  });

  it("isolates a different ID, renamed user, and same name on another account", () => {
    install(stored());
    for (const profile of [
      { ...actor, ID: 12, username: "synthetic-two" },
      { ...actor, username: "renamed-user" },
      { ...actor, ID: 12 },
    ]) {
      expect(readTaskNavigation(profile)).toEqual(defaults());
    }
    expect(readTaskNavigation(actor)).toEqual(snapshot());
  });

  it("keeps each query, page, selected detail, and return position through actual back/forward", async () => {
    const first = snapshot({ type: "list" });
    writeTaskNavigation(actor, first, "replace");
    const second = {
      view: { type: "list" } as const,
      query: { ...filtered, pageIdx: 4 },
      listReturn: { focusTaskId: secondId, scrollY: 765 },
    };
    writeTaskNavigation(actor, second, "push");
    const detail = {
      ...second,
      view: { type: "detail", id: secondId, archived: false } as const,
    };
    writeTaskNavigation(actor, detail, "push");
    expect(readTaskNavigation(actor)).toEqual(detail);
    await travel("back");
    expect(readTaskNavigation(actor)).toEqual(second);
    await travel("back");
    expect(readTaskNavigation(actor)).toEqual(first);
    await travel("forward");
    expect(readTaskNavigation(actor)).toEqual(second);
    await travel("forward");
    expect(readTaskNavigation(actor)).toEqual(detail);
  });

  it("cannot import another account's older entry when navigating back", async () => {
    writeTaskNavigation(actor, snapshot(), "replace");
    const other = { ...actor, ID: 12, username: "synthetic-two" };
    writeTaskNavigation(other, defaults(), "push");
    await travel("back");
    expect(readTaskNavigation(other)).toEqual(defaults());
    expect(readTaskNavigation(actor)).toEqual(snapshot());
    await travel("forward");
    expect(readTaskNavigation(actor)).toEqual(defaults());
    expect(readTaskNavigation(other)).toEqual(defaults());
  });

  it("replaces the current entry without adding one and pushes a new entry explicitly", () => {
    const length = window.history.length;
    writeTaskNavigation(actor, snapshot({ type: "list" }), "replace");
    expect(window.history.length).toBe(length);
    writeTaskNavigation(actor, snapshot(), "push");
    expect(window.history.length).toBe(length + 1);
  });

  it.each<TaskNavigationView>([
    { type: "list" },
    { type: "detail", id: taskId },
    { type: "detail", id: taskId, archived: false },
    { type: "detail", id: taskId, archived: true },
    { type: "schedules" },
    { type: "schedule-detail", id: secondId },
  ])(
    "restores a strictly validated read view %j without widening visibility",
    (view) => {
      writeTaskNavigation(actor, snapshot(view), "replace");
      expect(readTaskNavigation(actor)).toEqual(snapshot(view));
    },
  );

  it.each([
    ["submit", "list"],
    ["archive", "list"],
    ["maintenance-confirm", "list"],
    ["schedule-create", "schedules"],
  ] as const)("opens %s normally but restores only %s", (type, safeType) => {
    const requested = snapshot({ type });
    expect(writeTaskNavigation(actor, requested, "replace")).toEqual(requested);
    expect(window.location.hash).toBe(`#tasks/${type}`);
    expect(readTaskNavigation(actor)).toEqual({
      ...requested,
      view: { type: safeType },
    });
  });

  it("defaults absent optional return metadata, including earlier entries", () => {
    const input = { view: { type: "list" } as const, query: filtered };
    expect(writeTaskNavigation(actor, input, "replace")).toEqual({
      ...input,
      listReturn: defaultTaskListReturn(),
    });
    const { listReturn: _unused, ...older } = stored();
    install(older);
    expect(readTaskNavigation(actor)).toEqual({
      ...snapshot(),
      listReturn: defaultTaskListReturn(),
    });
  });

  it("does not alias input, returned values, or the stored object", () => {
    const input = snapshot();
    const result = writeTaskNavigation(actor, input, "replace");
    input.query.domainId = "changed-input";
    input.view.type = "list";
    input.listReturn.scrollY = 2;
    result.query.domainId = "changed-result";
    result.view.type = "list";
    result.listReturn.scrollY = 3;
    expect(readTaskNavigation(actor)).toEqual(snapshot());
    const restored = readTaskNavigation(actor);
    restored.query.domainId = "changed-read";
    restored.view.type = "list";
    restored.listReturn.scrollY = 4;
    expect(readTaskNavigation(actor)).toEqual(snapshot());
  });

  it("never touches browser storage or makes requests", () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem");
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    const removeItem = vi.spyOn(Storage.prototype, "removeItem");
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    writeTaskNavigation(
      actor,
      snapshot({ type: "maintenance-confirm" }),
      "replace",
    );
    readTaskNavigation(actor);
    expect(getItem).not.toHaveBeenCalled();
    expect(setItem).not.toHaveBeenCalled();
    expect(removeItem).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });
});

describe("strict task navigation validation", () => {
  it.each([null, undefined, [], true, 1, "state", {}, { version: 1 }])(
    "rejects a malformed payload %j",
    (value) => {
      install(value);
      expect(readTaskNavigation(actor)).toEqual(defaults());
    },
  );

  it.each([
    null,
    [],
    "state",
    {},
    { unowned: stored() },
    { [namespace]: stored(), unknown: "value" },
  ])("rejects an unowned outer record %j", (value) => {
    window.history.replaceState(value, "", `#tasks/detail/${taskId}`);
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each(["version", "owner", "hash", "view", "query"])(
    "rejects a missing required %s",
    (key) => {
      const value: Record<string, unknown> = stored();
      delete value[key];
      install(value);
      expect(readTaskNavigation(actor)).toEqual(defaults());
    },
  );

  it.each([
    { version: 0 },
    { version: 2 },
    { version: "1" },
    { owner: null },
    { owner: [] },
    { owner: actor.ID },
    { owner: { ID: actor.ID } },
    { owner: { username: actor.username } },
    { owner: { ID: String(actor.ID), username: actor.username } },
    { owner: { ID: actor.ID, username: actor.username, csrfToken: "secret" } },
    { owner: { ID: actor.ID, username: actor.username.toUpperCase() } },
    { owner: { ID: actor.ID, username: "x".repeat(65) } },
    { csrfToken: "secret" },
    { proof: { actorPassword: "secret" } },
    { pendingAction: "cancel" },
    { tasks: [{ taskUUID: taskId }] },
    { hash: null },
    { hash: 1 },
    { hash: [] },
  ])("rejects payload/owner tampering %j", (changes) => {
    install({ ...stored(), ...changes });
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each([
    "",
    "#tasks",
    "#tasks/list",
    `#tasks/detail/${secondId}`,
    `#tasks/detail/${taskId}/`,
    `#tasks/detail/${taskId}?archived=true`,
    `#tasks/detail/${taskId}#fragment`,
    `#tasks/detail/%31${taskId.slice(1)}`,
    "#tasks/unknown",
    "#tasks/../../account",
    "#profile",
    `#tasks/${"x".repeat(1000)}`,
  ])("rejects an exact URL mismatch or unknown route %s", (hash) => {
    install(stored(), hash);
    expect(readTaskNavigation(actor)).toEqual(defaults());
    install({ ...stored(), hash }, hash);
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each([
    null,
    [],
    "list",
    {},
    { type: "unknown" },
    { type: "cancel" },
    { type: "list", id: taskId },
    { type: "list", archived: false },
    { type: "submit", actorPassword: "secret" },
    { type: "maintenance-confirm", action: "archive" },
    { type: "detail" },
    { type: "detail", id: "" },
    { type: "detail", id: taskId.toUpperCase().replace("1", "A") },
    { type: "detail", id: `../${taskId}` },
    { type: "detail", id: taskId + "/" },
    { type: "detail", id: "%31" + taskId.slice(1) },
    { type: "detail", id: 11 },
    { type: "detail", id: "a".repeat(129) },
    { type: "detail", id: taskId, archived: "true" },
    { type: "detail", id: taskId, archived: 1 },
    { type: "detail", id: taskId, archived: undefined },
    { type: "detail", id: taskId, result: { private: "response" } },
    { type: "schedule-detail", id: taskId, archived: false },
    { type: "schedule-create", id: taskId },
  ])("rejects a malformed or over-specified view %j", (view) => {
    install({ ...stored(), view });
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each([
    { pageIdx: 0 },
    { pageIdx: -1 },
    { pageIdx: 1000001 },
    { pageIdx: 1.5 },
    { pageIdx: "1" },
    { pageIdx: null },
    { pageIdx: NaN },
    { pageIdx: Infinity },
    { pageIdx: Number.MAX_SAFE_INTEGER + 1 },
    { pageSize: 0 },
    { pageSize: 1 },
    { pageSize: 25 },
    { pageSize: 101 },
    { pageSize: 20.5 },
    { pageSize: "20" },
    { pageSize: NaN },
    { domainId: null },
    { domainId: "a".repeat(129) },
    { domainId: "a/b" },
    { domainId: "a b" },
    { domainId: "a\n" },
    { domainId: "a\0" },
    { domainId: "域" },
    { taskName: null },
    { taskName: "a".repeat(65) },
    { taskName: "界".repeat(22) },
    { taskName: "😀".repeat(17) },
    { taskName: "\uD800" },
    { taskName: "\uDC00" },
    { state: "complete" },
    { state: "SUCCEEDED" },
    { state: [] },
    { state: null },
    { visibility: "active" },
    { visibility: "deleted" },
    { visibility: true },
    { visibility: null },
    { visibility: [] },
    { unknown: "value" },
    { actorPassword: "secret" },
    { selected: [taskId] },
  ])("rejects the entire snapshot for invalid query values %j", (changes) => {
    expect(validTaskListQuery({ ...filtered, ...changes })).toBe(false);
    install({ ...stored(), query: { ...filtered, ...changes } });
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each(Object.keys(filtered))("rejects a missing query key %s", (key) => {
    const query: Record<string, unknown> = { ...filtered };
    delete query[key];
    install({ ...stored(), query });
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each([null, [], "query", 1, true])(
    "rejects non-object queries %j",
    (query) => {
      install({ ...stored(), query });
      expect(readTaskNavigation(actor)).toEqual(defaults());
    },
  );

  it.each([
    {
      ...defaultTaskQuery(),
      pageIdx: 1000000,
      pageSize: 100,
      domainId: "a".repeat(128),
      taskName: "x".repeat(64),
    },
    { ...defaultTaskQuery(), pageSize: 10, taskName: "界".repeat(21) },
    { ...defaultTaskQuery(), pageSize: 50, taskName: "😀".repeat(16) },
    ...taskStates.map((state) => ({ ...defaultTaskQuery(), state })),
    { ...defaultTaskQuery(), visibility: "archived" as const },
    { ...defaultTaskQuery(), visibility: "all" as const },
  ])("accepts the current API/UI query bounds %j", (query) => {
    expect(validTaskListQuery(query)).toBe(true);
    writeTaskNavigation(actor, { ...snapshot(), query }, "replace");
    expect(readTaskNavigation(actor).query).toEqual(query);
  });

  it.each([
    null,
    undefined,
    [],
    "position",
    {},
    { focusTaskId: taskId },
    { scrollY: 1 },
    { focusTaskId: "invalid", scrollY: 0 },
    { focusTaskId: null, scrollY: 0 },
    { focusTaskId: taskId + "/", scrollY: 0 },
    { focusTaskId: "x".repeat(129), scrollY: 0 },
    { focusTaskId: taskId, scrollY: -1 },
    { focusTaskId: taskId, scrollY: 10000001 },
    { focusTaskId: taskId, scrollY: 0.5 },
    { focusTaskId: taskId, scrollY: NaN },
    { focusTaskId: taskId, scrollY: Infinity },
    { focusTaskId: taskId, scrollY: "0" },
    { ...listReturn, selector: "input[type=password]" },
    { ...listReturn, proof: "secret" },
  ])("rejects malformed or over-specified return metadata %j", (value) => {
    install({ ...stored(), listReturn: value });
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each([
    { focusTaskId: "", scrollY: 0 },
    { focusTaskId: taskId, scrollY: 10000000 },
  ])("accepts bounded return metadata %j", (value) => {
    writeTaskNavigation(actor, { ...snapshot(), listReturn: value }, "replace");
    expect(readTaskNavigation(actor).listReturn).toEqual(value);
  });

  it("rejects inherited fields, accessors, symbol keys and non-data objects", () => {
    const getter = vi.fn(() => filtered);
    const accessor = { ...stored() };
    Object.defineProperty(accessor, "query", { enumerable: true, get: getter });
    const hidden = { ...stored() };
    Object.defineProperty(hidden, "secret", {
      value: "private",
      enumerable: false,
    });
    for (const value of [
      new Date(),
      Object.create(stored()),
      Object.assign(Object.create({ foreign: true }), stored()),
      { ...stored(), [Symbol("secret")]: "private" },
      accessor,
      hidden,
      { ...stored(), query: Object.create(filtered) },
    ]) {
      // Mock the boundary because a real browser structured-clones these values;
      // this also covers hostile/incomplete DOM implementations without running getters.
      vi.spyOn(window.history, "state", "get").mockReturnValue({
        [namespace]: value,
      });
      expect(readTaskNavigation(actor)).toEqual(defaults());
    }
    expect(getter).not.toHaveBeenCalled();
  });

  it("validates queries without invoking accessors or throwing for malformed input", () => {
    const getter = vi.fn(() => 1);
    const accessor = { ...filtered };
    Object.defineProperty(accessor, "pageIdx", {
      enumerable: true,
      get: getter,
    });
    for (const value of [
      null,
      undefined,
      [],
      "query",
      1,
      true,
      {},
      accessor,
      new Proxy(
        {},
        {
          getPrototypeOf() {
            throw new Error("unavailable");
          },
        },
      ),
    ])
      expect(validTaskListQuery(value)).toBe(false);
    expect(getter).not.toHaveBeenCalled();
  });

  it("accepts plain null-prototype records without inheriting properties", () => {
    const value = stored();
    value.query = Object.assign(Object.create(null), filtered);
    install(Object.assign(Object.create(null), value));
    expect(readTaskNavigation(actor)).toEqual(snapshot());
  });
});

describe("safe writes and unavailable browser history", () => {
  it.each([
    null,
    undefined,
    [],
    {},
    { ...snapshot(), proof: { actorPassword: "secret" } },
    { ...snapshot(), query: { ...filtered, csrfToken: "secret" } },
    { ...snapshot(), view: { type: "detail", id: "invalid" } },
    { ...snapshot(), listReturn: { ...listReturn, scrollY: -1 } },
  ])("writes fresh safe defaults for an invalid snapshot %j", (value) => {
    expect(
      writeTaskNavigation(actor, value as TaskNavigationSnapshot, "replace"),
    ).toEqual(defaults());
    expect(readTaskNavigation(actor)).toEqual(defaults());
    expect(window.location.hash).toBe("#tasks/list");
    expect(JSON.stringify(window.history.state)).not.toContain("secret");
  });

  it.each([
    { ...actor, ID: 0 },
    { ...actor, ID: -1 },
    { ...actor, ID: 1.1 },
    { ...actor, ID: NaN },
    { ...actor, ID: Infinity },
    { ...actor, ID: Number.MAX_SAFE_INTEGER + 1 },
    { ...actor, ID: "11" },
    { ...actor, username: "" },
    { ...actor, username: "x".repeat(65) },
    { ...actor, username: "bad\n" },
    { ...actor, username: null },
    null,
  ])(
    "does not read or write for an invalid authenticated identity %j",
    (profile) => {
      install(stored());
      const replace = vi.spyOn(window.history, "replaceState");
      const push = vi.spyOn(window.history, "pushState");
      expect(readTaskNavigation(profile as Profile)).toEqual(defaults());
      expect(
        writeTaskNavigation(profile as Profile, snapshot(), "replace"),
      ).toEqual(defaults());
      expect(replace).not.toHaveBeenCalled();
      expect(push).not.toHaveBeenCalled();
    },
  );

  it("rejects unknown write modes without silently replacing an entry", () => {
    const replace = vi.spyOn(window.history, "replaceState");
    const push = vi.spyOn(window.history, "pushState");
    expect(
      writeTaskNavigation(actor, snapshot(), "unknown" as "replace"),
    ).toEqual(defaults());
    expect(replace).not.toHaveBeenCalled();
    expect(push).not.toHaveBeenCalled();
  });

  it("falls back if reading history is unavailable", () => {
    vi.spyOn(window.history, "state", "get").mockImplementation(() => {
      throw new Error("unavailable");
    });
    expect(readTaskNavigation(actor)).toEqual(defaults());
  });

  it.each(["push", "replace"] as const)(
    "preserves only clean in-memory navigation if %sState fails",
    (mode) => {
      vi.spyOn(window.history, `${mode}State`).mockImplementation(() => {
        throw new DOMException("unavailable", "SecurityError");
      });
      expect(writeTaskNavigation(actor, snapshot(), mode)).toEqual(snapshot());
      expect(window.location.hash).toBe("#tasks");
      expect(readTaskNavigation(actor)).toEqual(defaults());
    },
  );
});
