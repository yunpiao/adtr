import { validUsername, type Profile } from "./api";
import { taskStates, type TaskState } from "./task-api";

export type TaskListQuery = {
  pageIdx: number;
  pageSize: number;
  domainId: string;
  state: TaskState | "";
  taskName: string;
  visibility: "" | "archived" | "all";
};

export const defaultTaskQuery = (): TaskListQuery => ({
  pageIdx: 1,
  pageSize: 20,
  domainId: "",
  state: "",
  taskName: "",
  visibility: "",
});

export type TaskNavigationView =
  | { type: "list" }
  | { type: "submit" }
  | { type: "detail"; id: string; archived?: boolean }
  | { type: "schedules" }
  | { type: "schedule-detail"; id: string }
  | { type: "schedule-create" }
  | { type: "archive" }
  | { type: "maintenance-confirm" };

// Only a task row identifier and a bounded document offset may accompany the
// applied query. No selector, response data, draft, proof or action is retained.
export interface TaskListReturn {
  focusTaskId: string;
  scrollY: number;
}
export const defaultTaskListReturn = (): TaskListReturn => ({
  focusTaskId: "",
  scrollY: 0,
});
export interface TaskNavigationSnapshot {
  view: TaskNavigationView;
  query: TaskListQuery;
  listReturn: TaskListReturn;
}
type NavigationInput = Omit<TaskNavigationSnapshot, "listReturn"> & {
  listReturn?: TaskListReturn;
};
type Actor = Pick<Profile, "ID" | "username">;

const namespace = "adtr.task-navigation";
const uuid = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(value);
const integer = (value: unknown, min: number, max: number): value is number =>
  typeof value === "number" &&
  Number.isSafeInteger(value) &&
  value >= min &&
  value <= max;

// History is untrusted input. Reject unknown fields instead of quietly keeping
// part of a foreign or future payload. Accessors and non-data objects are not
// history records, even in environments that do not structured-clone state.
function record(
  value: unknown,
  required: string[],
  optional: string[] = [],
): value is Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  if (prototype !== Object.prototype && prototype !== null) return false;
  const keys = Reflect.ownKeys(value);
  return (
    required.every((key) => Object.hasOwn(value, key)) &&
    keys.every((key) => {
      if (
        typeof key !== "string" ||
        (!required.includes(key) && !optional.includes(key))
      )
        return false;
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      return !!descriptor?.enumerable && Object.hasOwn(descriptor, "value");
    })
  );
}

function validActor(profile: Actor): boolean {
  return (
    !!profile &&
    integer(profile.ID, 1, Number.MAX_SAFE_INTEGER) &&
    validUsername(profile.username)
  );
}

function projectQuery(value: unknown): TaskListQuery | undefined {
  if (
    !record(value, [
      "pageIdx",
      "pageSize",
      "domainId",
      "state",
      "taskName",
      "visibility",
    ]) ||
    !integer(value.pageIdx, 1, 1000000) ||
    typeof value.pageSize !== "number" ||
    ![10, 20, 50, 100].includes(value.pageSize) ||
    typeof value.domainId !== "string" ||
    !/^[A-Za-z0-9._-]{0,128}$/.test(value.domainId) ||
    (value.state !== "" && !taskStates.includes(value.state as TaskState)) ||
    typeof value.taskName !== "string" ||
    value.taskName.length > 64 ||
    // The API limit is UTF-8 bytes, not JavaScript UTF-16 code units. Reject
    // unpaired surrogates rather than allowing URL encoding to replace them.
    /[\uD800-\uDFFF]/u.test(value.taskName) ||
    new TextEncoder().encode(value.taskName).length > 64 ||
    !["", "archived", "all"].includes(value.visibility as string)
  )
    return;
  return {
    pageIdx: value.pageIdx,
    pageSize: value.pageSize,
    domainId: value.domainId,
    state: value.state as TaskListQuery["state"],
    taskName: value.taskName,
    visibility: value.visibility as TaskListQuery["visibility"],
  };
}

// The filter form uses the same contract before applying a new query so invalid
// input can be explained without replacing the user's last valid list context.
export function validTaskListQuery(value: unknown): value is TaskListQuery {
  try {
    return projectQuery(value) !== undefined;
  } catch {
    return false;
  }
}

function projectView(value: unknown): TaskNavigationView | undefined {
  if (!record(value, ["type"], ["id", "archived"])) return;
  switch (value.type) {
    case "detail":
      if (
        !record(value, ["type", "id"], ["archived"]) ||
        !uuid(value.id) ||
        (Object.hasOwn(value, "archived") &&
          typeof value.archived !== "boolean")
      )
        return;
      return {
        type: "detail",
        id: value.id,
        ...(Object.hasOwn(value, "archived")
          ? { archived: value.archived as boolean }
          : {}),
      };
    case "schedule-detail":
      if (!record(value, ["type", "id"]) || !uuid(value.id)) return;
      return { type: "schedule-detail", id: value.id };
    case "list":
    case "submit":
    case "schedules":
    case "schedule-create":
    case "archive":
    case "maintenance-confirm":
      if (!record(value, ["type"])) return;
      return { type: value.type };
    default:
      return;
  }
}

function projectListReturn(value: unknown): TaskListReturn | undefined {
  if (
    !record(value, ["focusTaskId", "scrollY"]) ||
    (value.focusTaskId !== "" && !uuid(value.focusTaskId)) ||
    !integer(value.scrollY, 0, 10000000)
  )
    return;
  return { focusTaskId: value.focusTaskId, scrollY: value.scrollY };
}

function projectSnapshot(value: unknown): TaskNavigationSnapshot | undefined {
  if (!record(value, ["view", "query"], ["listReturn"])) return;
  const view = projectView(value.view);
  const query = projectQuery(value.query);
  const listReturn = Object.hasOwn(value, "listReturn")
    ? projectListReturn(value.listReturn)
    : defaultTaskListReturn();
  if (!view || !query || !listReturn) return;
  return { view, query, listReturn };
}

const fallback = (): TaskNavigationSnapshot => ({
  view: { type: "list" },
  query: defaultTaskQuery(),
  listReturn: defaultTaskListReturn(),
});
const hashFor = (view: TaskNavigationView) =>
  `#tasks/${view.type}${view.type === "detail" || view.type === "schedule-detail" ? `/${view.id}` : ""}`;

function readView(view: TaskNavigationView): TaskNavigationView {
  switch (view.type) {
    case "submit":
    case "archive":
    case "maintenance-confirm":
      return { type: "list" };
    case "schedule-create":
      return { type: "schedules" };
    default:
      return view;
  }
}

// Restoration is per history entry and requires both account identifiers and
// the exact URL written for that entry. A raw deep link cannot import an old
// account's filters or selected task. Permission checks and fresh API reads
// remain the workspace's responsibility, including archived visibility.
export function readTaskNavigation(profile: Actor): TaskNavigationSnapshot {
  try {
    if (!validActor(profile)) return fallback();
    const outer: unknown = window.history.state;
    if (!record(outer, [namespace])) return fallback();
    const value = outer[namespace];
    if (
      !record(
        value,
        ["version", "owner", "hash", "view", "query"],
        ["listReturn"],
      ) ||
      value.version !== 1 ||
      !record(value.owner, ["ID", "username"]) ||
      value.owner.ID !== profile.ID ||
      value.owner.username !== profile.username
    )
      return fallback();
    const snapshot = projectSnapshot({
      view: value.view,
      query: value.query,
      ...(Object.hasOwn(value, "listReturn")
        ? { listReturn: value.listReturn }
        : {}),
    });
    if (
      !snapshot ||
      value.hash !== hashFor(snapshot.view) ||
      value.hash !== window.location.hash
    )
      return fallback();
    return { ...snapshot, view: readView(snapshot.view) };
  } catch {
    return fallback();
  }
}

// A user can open a mutation form normally, but only its route is retained and
// readTaskNavigation always returns a safe read view after refresh/back/forward.
// Pending-operation reconciliation has a separate existing storage contract.
// Replace is for the current entry (initial canonicalization/scroll capture);
// push creates a distinct applied-filter/page/view entry for browser navigation.
export function writeTaskNavigation(
  profile: Actor,
  input: NavigationInput,
  mode: "push" | "replace",
): TaskNavigationSnapshot {
  let snapshot = fallback();
  try {
    if (!validActor(profile) || (mode !== "push" && mode !== "replace"))
      return snapshot;
    snapshot = projectSnapshot(input) ?? fallback();
    const hash = hashFor(snapshot.view);
    const state = {
      [namespace]: {
        version: 1,
        owner: { ID: profile.ID, username: profile.username },
        hash,
        view: { ...snapshot.view },
        query: { ...snapshot.query },
        listReturn: { ...snapshot.listReturn },
      },
    };
    if (mode === "push") window.history.pushState(state, "", hash);
    else window.history.replaceState(state, "", hash);
  } catch {
    // Browser state can be unavailable or rate-limited. The current UI may
    // continue with this clean in-memory snapshot; no storage fallback shares
    // it with another entry, account or tab.
  }
  return snapshot;
}
