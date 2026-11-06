import type { Page, Request, Response } from "@playwright/test";
import { createHash } from "node:crypto";
import { TextDecoder, types } from "node:util";
import type { Availability } from "../src/system-api";

export type CandidateKind = "overview-disk" | "storage-ordinals";
export interface CandidateObservation {
  kind: CandidateKind;
  responseSHA256: string;
  nativeFinished: true;
  current: {
    checkedAt: string;
    availability: Availability;
    stale: boolean;
  };
  disk?: {
    id: "runtime-root";
    source: "statfs";
    scope: "runtime_filesystem";
    percent: number | null;
    availability: Availability;
    observedAt: string;
  };
  page?: { page: number; pageSize: number; total: number };
  rows?: {
    id: "runtime-root";
    ordinal: number;
    revision: number;
    alarmPercent: number;
  }[];
}

type Projection = Omit<CandidateObservation, "nativeFinished">;
const failure = () => new Error("System candidate read observation failed");
const object = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);
const availability = (value: unknown): value is Availability =>
  value === "available" ||
  value === "unavailable" ||
  value === "warming_up" ||
  value === "unsupported";
const timestamp = (value: unknown): value is string =>
  typeof value === "string" &&
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u.test(
    value,
  ) &&
  Number.isFinite(Date.parse(value));
const integer = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0;

// These are evidence limits for the isolated native fixture, not product limits.
// This decoder stays private and cannot attest to native request completion.
function projectCandidate(kind: CandidateKind, bytes: Uint8Array): Projection {
  if (!types.isUint8Array(bytes) || bytes.byteLength > 64 * 1024)
    throw failure();
  const value: unknown = JSON.parse(
    new TextDecoder("utf-8", { fatal: true }).decode(bytes),
  );
  if (!object(value)) throw failure();
  const current = kind === "overview-disk" ? value.current : value;
  if (
    !object(current) ||
    current.instance !== "local-api" ||
    !timestamp(current.checkedAt) ||
    !availability(current.availability) ||
    typeof current.stale !== "boolean" ||
    (current.stale && current.availability === "available")
  )
    throw failure();
  const projected: Projection = {
    kind,
    responseSHA256: createHash("sha256").update(bytes).digest("hex"),
    current: {
      checkedAt: current.checkedAt,
      availability: current.availability,
      stale: current.stale,
    },
  };
  if (kind === "overview-disk") {
    if (current.snapshot === null) {
      if (current.availability === "available" || current.stale)
        throw failure();
      return projected;
    }
    const snapshot = current.snapshot;
    if (
      !object(snapshot) ||
      snapshot.instance !== "local-api" ||
      !timestamp(snapshot.observedAt) ||
      !Array.isArray(snapshot.storage) ||
      snapshot.storage.length !== 1
    )
      throw failure();
    const disk: unknown = snapshot.storage[0];
    if (
      !object(disk) ||
      disk.id !== "runtime-root" ||
      disk.source !== "statfs" ||
      disk.scope !== "runtime_filesystem" ||
      !timestamp(disk.observedAt) ||
      Date.parse(disk.observedAt) !== Date.parse(snapshot.observedAt) ||
      !availability(disk.availability) ||
      (disk.availability === "available"
        ? typeof disk.percent !== "number" ||
          !Number.isFinite(disk.percent) ||
          disk.percent < 0 ||
          disk.percent > 100
        : disk.percent !== null)
    )
      throw failure();
    const age = Date.parse(current.checkedAt) - Date.parse(snapshot.observedAt);
    if (current.availability === "available" && (age < -1000 || age > 45_000))
      throw failure();
    projected.disk = {
      id: "runtime-root",
      source: "statfs",
      scope: "runtime_filesystem",
      percent: disk.percent as number | null,
      availability: disk.availability,
      observedAt: disk.observedAt,
    };
    return projected;
  }
  if (
    value.page !== 1 ||
    value.pageSize !== 20 ||
    value.total !== 1 ||
    !Array.isArray(value.storage) ||
    value.storage.length !== 1
  )
    throw failure();
  const row: unknown = value.storage[0];
  if (
    !object(row) ||
    row.id !== "runtime-root" ||
    !integer(row.revision) ||
    !integer(row.alarmPercent) ||
    row.alarmPercent < 85 ||
    row.alarmPercent > 90
  )
    throw failure();
  projected.page = { page: 1, pageSize: 20, total: 1 };
  projected.rows = [
    {
      id: "runtime-root",
      ordinal: 1,
      revision: row.revision,
      alarmPercent: row.alarmPercent,
    },
  ];
  return projected;
}

// Observe only the first new matching browser GET during the supplied existing
// navigation/click. A coincident new poll can be witnessed too; causation remains
// the caller's independent DOM assertion. Never fetch, replay, route or mutate.
export async function observeSystemCandidateRead(
  page: Page,
  kind: CandidateKind,
  action: () => Promise<unknown>,
): Promise<CandidateObservation> {
  if (kind !== "overview-disk" && kind !== "storage-ordinals") throw failure();
  let origin: string;
  try {
    origin = new URL(page.url()).origin;
  } catch {
    throw failure();
  }
  const path =
    kind === "overview-disk" ? "/api/system/info" : "/api/system/storage";
  const query =
    kind === "overview-disk" ? "" : "?instance=local-api&page=1&pageSize=20";
  const deadline = Date.now() + 15_000;
  let started = false,
    settled = false,
    actionFinished = false,
    nativeFinished = false,
    bodyStarted = false;
  let request: Request | undefined, response: Response | undefined;
  let projection: Projection | undefined;
  type Outcome = { value: CandidateObservation } | null;
  let resolve!: (outcome: Outcome) => void;
  // Internal completion never rejects, including before the action is awaited.
  const completed = new Promise<Outcome>((done) => {
    resolve = done;
  });
  let timer: ReturnType<typeof setTimeout> | undefined;
  const cleanup = () => {
    clearTimeout(timer);
    page.off("request", onRequest);
    page.off("response", onResponse);
    page.off("requestfinished", onFinished);
    page.off("requestfailed", onFailed);
  };
  const finish = (outcome: Outcome) => {
    if (settled) return;
    settled = true;
    cleanup();
    resolve(outcome);
  };
  const live = () => {
    if (!settled && Date.now() >= deadline) finish(null);
    return !settled;
  };
  const succeed = () => {
    if (live() && actionFinished && nativeFinished && projection)
      finish({ value: { ...projection, nativeFinished: true } });
  };
  const onRequest = (candidate: Request) => {
    if (!started || !live() || request) return;
    try {
      const url = new URL(candidate.url());
      if (
        candidate.method() === "GET" &&
        !candidate.isNavigationRequest() &&
        candidate.frame() === page.mainFrame() &&
        url.origin === origin &&
        url.username === "" &&
        url.password === "" &&
        url.hash === "" &&
        url.pathname === path &&
        url.search === query
      )
        request = candidate;
    } catch {
      // Requests without a browser frame, or malformed URLs, are not witnesses.
    }
  };
  const onResponse = (candidate: Response) => {
    if (!request || !live() || response) return;
    try {
      if (candidate.request() === request) response = candidate;
    } catch {
      finish(null);
    }
  };
  const onFinished = (candidate: Request) => {
    if (!request || candidate !== request || !live() || bodyStarted) return;
    try {
      if (!response || response.status() !== 200) {
        finish(null);
        return;
      }
      nativeFinished = true;
      bodyStarted = true;
      const observed = response;
      // A later action failure or deadline suppresses both pending reads and
      // late bytes. Exactly one body read follows native requestfinished.
      void Promise.resolve()
        .then(() => (live() ? observed.body() : undefined))
        .then((bytes) => {
          if (!live() || bytes === undefined) return;
          projection = projectCandidate(kind, bytes);
          succeed();
        })
        .catch(() => finish(null));
    } catch {
      finish(null);
    }
  };
  const onFailed = (candidate: Request) => {
    if (request && candidate === request && live()) finish(null);
  };
  try {
    page.on("request", onRequest);
    page.on("response", onResponse);
    page.on("requestfinished", onFinished);
    page.on("requestfailed", onFailed);
    timer = setTimeout(() => finish(null), Math.max(0, deadline - Date.now()));
    void Promise.resolve()
      .then(() => {
        if (!live()) return;
        started = true;
        return action();
      })
      .then(() => {
        if (!live()) return;
        actionFinished = true;
        succeed();
      })
      .catch(() => finish(null));
    const outcome = await completed;
    if (!outcome) throw failure();
    return outcome.value;
  } catch {
    throw failure();
  } finally {
    finish(null);
    cleanup();
  }
}
