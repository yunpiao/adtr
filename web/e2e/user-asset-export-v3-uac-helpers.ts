import type {
  BrowserContext,
  Page,
  Request,
  Response,
  TestInfo,
} from "@playwright/test";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { parseDirectoryV4UniqueJSON } from "../src/directory-v4-controls-json";
import { hash } from "./user-asset-export-v3-helpers";

export const exportPrefix = "/api/user-assets/v3/exports";
const recoveryStorage = "adtr.user-assets-v3.export-recovery";
const run = promisify(execFile);
export type UACMode = "set" | "clear" | "not_returned";
export type ExportRequest = {
  domainId: string;
  expectedRevision: string;
  expectedCredentialRevision: string;
  observationId: string;
  search: "user-";
  sAMAccountName: "";
  startTm: "";
  endTm: "";
  creatTm: "desc";
  screenColumns: readonly string[];
  uacDisableBit: UACMode;
};
export type SourceEvidence = {
  taskUUID: string;
  actorId: string;
  selection: { domainId: string; revision: string; credentialRevision: string };
  sourcePins: {
    accountId: string;
    accountCredentialRevision: string;
    policyRevision: string;
    grantRoleId: string;
    grantRevision: string;
  };
  bodyBase64: string;
  sha256: string;
  dictionaryVersion: number;
  count: number;
  taskState: string;
  taskName: string;
  payloadVersion: number;
  maxAttempts: number;
  attempt: number;
  purpose: string;
  useState: string;
  reason: string;
  dependencies: number;
  taskBinding: {
    payloadVersion: number;
    parentTaskUUID: string;
    payloadHash: string;
    payload: Record<string, unknown>;
  };
};
export type ExportReceipt = {
  taskID: string;
  task: Record<string, unknown>;
  request: ExportRequest;
};
export type ExportDetail = ExportReceipt & {
  downloadReady: boolean;
  downloadStatus: string;
  artifact: {
    filename: string;
    rowCount: number;
    byteCount: number;
    sha256: string;
  };
};
export type StoredExport = Record<string, unknown>;
export type Budget = {
  owner: number;
  producer: number;
  observations: number;
  directoryUses: number;
  exportTasks: number;
};
export type SafeCreateRequest = {
  bodyFieldNames: string[];
  safeBusinessRequest: Readonly<ExportRequest>;
  idempotencyKeySHA256: string;
  sanitizedRequestSHA256: string;
};
export type PostEvent = SafeCreateRequest & {
  ordinal: number;
  case: UACMode;
  method: "POST";
  pathname: string;
  status: 200;
  transport: "response";
  taskID: string;
};
export type PostLedger = {
  events: PostEvent[];
  createCount: number;
  cancelCount: number;
  unexpectedExportWriteCount: number;
  listenerClosed: true;
  finalSensitiveSnapshot: Budget;
};
export type UACPostObserver = {
  armCase(
    mode: UACMode,
    expected: Readonly<ExportRequest>,
    actor: number,
    page: Page,
  ): void;
  settleCase(taskID: string): Promise<void>;
  closeAndSnapshot(finalBudget: Budget): Promise<PostLedger>;
};
const businessKeys = [
  "domainId",
  "expectedRevision",
  "expectedCredentialRevision",
  "observationId",
  "search",
  "sAMAccountName",
  "startTm",
  "endTm",
  "creatTm",
  "screenColumns",
  "uacDisableBit",
] as const;
const bodyKeys = [
  ...businessKeys,
  "idempotencyKey",
  "actorPassword",
  "totpCode",
].sort();
// Only these literals may reach the failure reporter. Never reuse the caught
// error, its stack/cause, or even a getter-backed message in the public error.
export const uacFailureStages = [
  "configuration",
  "fixture",
  "source_read",
  "wire_read",
  "owner_switch",
  "budget_before",
  "initial_list",
  "list_filters",
  "list_read",
  "list_query",
  "list_rows",
  "list_source",
  "list_page",
  "export_prepare",
  "export_submit",
  "export_settle",
  "export_ready",
  "export_database",
  "original_receipt",
  "export_download",
  "original_task",
  "original_download",
  "screenshot",
  "case_evidence",
  "case_budget",
  "case_dismiss",
  "budget_final",
  "observer_close",
  "candidate_write",
  "unexpected",
] as const;
export type UACFailureStage = (typeof uacFailureStages)[number];
const uacFailureCodes = [
  "configuration",
  "provenance",
  "candidate_discovery",
  "candidate_schema",
  "source_binding",
  "task_binding",
  "raw_transcript",
  "terminal_seal",
  "post_parse",
  "post_binding",
  "post_count",
  "artifact_binding",
  "cleanup",
  "publication",
] as const;
export function uacFailureMessage(
  error: unknown,
  stage: UACFailureStage,
): string {
  let code: string = "unexpected";
  try {
    // Read only an own data property; do not invoke an unknown message getter.
    const message =
      error instanceof Error
        ? Object.getOwnPropertyDescriptor(error, "message")?.value
        : undefined;
    code =
      uacFailureCodes.find(
        (value) => message === `uac_evidence_invalid:${value}`,
      ) ?? "unexpected";
  } catch {
    /* Uninspectable errors are still closed unexpected failures. */
  }
  const safeStage =
    uacFailureStages.find((value) => value === stage) ?? "unexpected";
  return `uac_evidence_invalid:${code};stage=${safeStage}`;
}

export function uacRequire(
  condition: unknown,
  code = "post_binding",
): asserts condition {
  if (!condition) throw new Error(`uac_evidence_invalid:${code}`);
}
function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
export function sameValue(a: unknown, b: unknown): boolean {
  if (typeof a !== typeof b || a === null || b === null) return a === b;
  if (Array.isArray(a) || Array.isArray(b))
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((v, i) => sameValue(v, b[i]))
    );
  if (object(a) && object(b))
    return (
      Object.keys(a).length === Object.keys(b).length &&
      Object.keys(a).every((k) => Object.hasOwn(b, k) && sameValue(a[k], b[k]))
    );
  return a === b;
}
function exact(
  value: unknown,
  keys: readonly string[],
): value is Record<string, unknown> {
  return (
    object(value) &&
    Object.keys(value).sort().join("\0") === [...keys].sort().join("\0")
  );
}
function scalarTree(value: unknown): void {
  if (typeof value === "string") {
    for (let i = 0; i < value.length; i++) {
      const n = value.charCodeAt(i);
      if (n >= 0xd800 && n <= 0xdbff) {
        const next = value.charCodeAt(++i);
        uacRequire(next >= 0xdc00 && next <= 0xdfff, "post_parse");
      } else uacRequire(n < 0xdc00 || n > 0xdfff, "post_parse");
    }
  } else if (typeof value === "number")
    uacRequire(Number.isFinite(value), "post_parse");
  else if (Array.isArray(value)) value.forEach(scalarTree);
  else if (object(value))
    Object.entries(value).forEach(([k, v]) => {
      scalarTree(k);
      scalarTree(v);
    });
}
// Keyed by the caller's private run list, never serialized or returned. The
// request observer also records actual outbound proof values, independent of
// the C proof-generation hook. Weak ownership ends when the run list is gone.
const observedProofSecrets = new WeakMap<readonly string[], Set<string>>();
function rememberObservedProof(
  knownSecrets: readonly string[],
  password: string,
  code: string,
): void {
  const values = observedProofSecrets.get(knownSecrets) ?? new Set<string>();
  values.add(password);
  values.add(code);
  observedProofSecrets.set(knownSecrets, values);
}
function runSecrets(knownSecrets: readonly string[]): readonly string[] {
  return [...knownSecrets, ...(observedProofSecrets.get(knownSecrets) ?? [])];
}

// Only allowed non-secret commitments survive the private raw-key owner.
// Retain every observed case hash until the run list becomes unreachable.
const observedIdempotencyHashes = new WeakMap<readonly string[], Set<string>>();
function rememberIdempotencyHash(
  knownSecrets: readonly string[],
  digest: string,
): void {
  const hashes =
    observedIdempotencyHashes.get(knownSecrets) ?? new Set<string>();
  hashes.add(digest);
  observedIdempotencyHashes.set(knownSecrets, hashes);
}
function rejectObservedKeys(
  text: string,
  knownSecrets: readonly string[],
  code = "post_binding",
): void {
  const hashes = observedIdempotencyHashes.get(knownSecrets);
  if (!hashes) return;
  const candidates =
    text.match(
      /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gu,
    ) ?? [];
  uacRequire(
    !candidates.some((candidate) => hashes.has(hash(candidate))),
    code,
  );
}

export function safeValue(
  value: unknown,
  knownSecrets: readonly string[],
): void {
  scalarTree(value);
  const secrets = runSecrets(knownSecrets);
  const text = JSON.stringify(value);
  rejectObservedKeys(text, knownSecrets);
  uacRequire(
    !secrets.some((secret) => secret.length > 0 && text.includes(secret)),
    "post_binding",
  );
  const visit = (v: unknown): void => {
    if (typeof v === "string") {
      rejectObservedKeys(v, knownSecrets);
      uacRequire(
        !secrets.some((secret) => secret.length > 0 && v.includes(secret)),
        "post_binding",
      );
    } else if (Array.isArray(v)) v.forEach(visit);
    else if (object(v)) {
      uacRequire(
        !Object.keys(v).some((key) =>
          [
            "actorPassword",
            "totpCode",
            "idempotencyKey",
            "password",
            "secret",
            "ciphertext",
            "userInfo",
          ].includes(key),
        ),
        "post_binding",
      );
      Object.keys(v).forEach(visit);
      Object.values(v).forEach(visit);
    }
  };
  visit(value);
}
function project(value: Record<string, unknown>): ExportRequest {
  return Object.fromEntries(
    businessKeys.map((key) => [key, value[key]]),
  ) as ExportRequest;
}
export function expectedUACRequest(
  source: Readonly<SourceEvidence>,
  mode: UACMode,
): Readonly<ExportRequest> {
  uacRequire(["set", "clear", "not_returned"].includes(mode));
  return Object.freeze({
    domainId: source.selection.domainId,
    expectedRevision: source.selection.revision,
    expectedCredentialRevision: source.selection.credentialRevision,
    observationId: source.taskUUID,
    search: "user-",
    sAMAccountName: "",
    startTm: "",
    endTm: "",
    creatTm: "desc",
    screenColumns: Object.freeze([
      "mail",
      "guid",
      "sAMAccountName",
      "dn",
      "sid",
      "whenCreated",
    ]),
    uacDisableBit: mode,
  });
}
function locatorKey(
  raw: unknown,
  actor: number,
  expected: Readonly<ExportRequest>,
  secrets: readonly string[],
): string {
  uacRequire(typeof raw === "string" && Buffer.byteLength(raw, "utf8") <= 2048);
  const locator = parseDirectoryV4UniqueJSON(raw);
  scalarTree(locator);
  uacRequire(
    exact(locator, [
      "version",
      "kind",
      "actorID",
      "domainId",
      "idempotencyKey",
    ]),
  );
  uacRequire(
    locator.version === 1 &&
      locator.kind === "user_assets.v3.export" &&
      Number.isSafeInteger(locator.actorID) &&
      locator.actorID === actor &&
      locator.domainId === expected.domainId &&
      typeof locator.idempotencyKey === "string" &&
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(
        locator.idempotencyKey,
      ),
  );
  uacRequire(
    !runSecrets(secrets).some(
      (secret) =>
        secret.length > 0 &&
        locator.idempotencyKey!.toString().includes(secret),
    ),
  );
  return locator.idempotencyKey;
}
function ownedRequest(request: Request, page: Page, origin: string): boolean {
  return (
    request.frame() === page.mainFrame() &&
    new URL(request.url()).origin === origin
  );
}
export async function inspectUACCreateRequest(
  request: Request,
  expected: Readonly<ExportRequest>,
  actorId: number,
  expectedPage: Page,
  ownedAppOrigin: string,
  readLocator: () => Promise<unknown>,
  knownSecrets: readonly string[],
): Promise<SafeCreateRequest> {
  let raw: Buffer | null = null;
  let text = "",
    key = "";
  let parsed: unknown;
  try {
    uacRequire(
      ownedRequest(request, expectedPage, ownedAppOrigin) &&
        request.method() === "POST" &&
        new URL(request.url()).pathname === exportPrefix,
      "post_parse",
    );
    raw = request.postDataBuffer();
    uacRequire(raw && raw.length > 0 && raw.length <= 32768, "post_parse");
    uacRequire(
      !(raw[0] === 0xef && raw[1] === 0xbb && raw[2] === 0xbf),
      "post_parse",
    );
    text = new TextDecoder("utf-8", { fatal: true }).decode(raw);
    parsed = parseDirectoryV4UniqueJSON(text);
    scalarTree(parsed);
    uacRequire(exact(parsed, bodyKeys), "post_parse");
    const body = parsed;
    uacRequire(
      businessKeys
        .filter((k) => k !== "screenColumns")
        .every((k) => typeof body[k] === "string") &&
        Array.isArray(body.screenColumns) &&
        body.screenColumns.every((v) => typeof v === "string"),
      "post_parse",
    );
    uacRequire(
      typeof body.actorPassword === "string" &&
        body.actorPassword.length >= 1 &&
        body.actorPassword.length <= 256 &&
        typeof body.totpCode === "string" &&
        /^[0-9]{6}$/u.test(body.totpCode),
      "post_parse",
    );
    rememberObservedProof(knownSecrets, body.actorPassword, body.totpCode);
    const secrets = runSecrets(knownSecrets);
    uacRequire(sameValue(project(body), expected), "post_binding");
    safeValue(expected, knownSecrets);
    key = locatorKey(await readLocator(), actorId, expected, secrets);
    uacRequire(
      typeof body.idempotencyKey === "string" && body.idempotencyKey === key,
      "post_binding",
    );
    const idempotencyKeySHA256 = hash(key);
    rememberIdempotencyHash(knownSecrets, idempotencyKeySHA256);
    const safeBusinessRequest = Object.freeze({
      ...expected,
      screenColumns: Object.freeze([...expected.screenColumns]),
    });
    return {
      bodyFieldNames: [...bodyKeys],
      safeBusinessRequest,
      idempotencyKeySHA256,
      sanitizedRequestSHA256: hash(
        JSON.stringify(project(safeBusinessRequest)),
      ),
    };
  } catch {
    throw new Error("uac_evidence_invalid:post_parse");
  } finally {
    raw?.fill(0);
    raw = null;
    parsed = undefined;
    text = "";
    key = "";
    // JS strings and Playwright internal caches cannot be physically erased.
  }
}

export async function safeResponse(
  response: Response,
  actor: number,
  knownSecrets: readonly string[],
): Promise<Record<string, unknown>> {
  try {
    uacRequire(
      response.status() === 200 &&
        response.headers()["x-adtr-user-id"] === String(actor) &&
        response.headers()["cache-control"] === "no-store",
    );
    const bytes = await response.body();
    uacRequire(
      bytes.length > 0 &&
        bytes.length <= 1 << 20 &&
        !(bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf),
    );
    const value = parseDirectoryV4UniqueJSON(
      new TextDecoder("utf-8", { fatal: true }).decode(bytes),
    );
    uacRequire(object(value));
    safeValue(value, knownSecrets);
    return value;
  } catch {
    throw new Error("uac_evidence_invalid:post_binding");
  }
}

export function observeUACExportWrites(
  context: BrowserContext,
  ownedAppOrigin: string,
  knownSecrets: readonly string[],
): UACPostObserver {
  type Active = {
    mode: UACMode;
    expected: Readonly<ExportRequest>;
    actor: number;
    page: Page;
    tasks: Promise<void>[];
    event?: PostEvent;
    settled: boolean;
  };
  let active: Active | undefined;
  let createCount = 0,
    cancelCount = 0,
    unexpectedExportWriteCount = 0,
    poisoned = false,
    closed = false;
  const tasks: Promise<void>[] = [],
    events: PostEvent[] = [];
  const writes = new WeakSet<Request>();
  const onRequest = (request: Request): void => {
    let pathname: string;
    try {
      pathname = new URL(request.url()).pathname;
    } catch {
      poisoned = true;
      return;
    }
    if (
      !pathname.startsWith(exportPrefix) ||
      ["GET", "HEAD", "OPTIONS"].includes(request.method())
    )
      return;
    writes.add(request);
    // Counting and case capture happen synchronously, before any async work.
    const captured = active;
    if (pathname === exportPrefix && request.method() === "POST") createCount++;
    else if (pathname === `${exportPrefix}/cancel`) {
      cancelCount++;
      poisoned = true;
    } else {
      unexpectedExportWriteCount++;
      poisoned = true;
    }
    if (
      !captured ||
      captured.settled ||
      captured.tasks.length !== 0 ||
      pathname !== exportPrefix ||
      request.method() !== "POST"
    ) {
      poisoned = true;
      return;
    }
    const ordinal = createCount;
    const task = (async () => {
      const inspected = await inspectUACCreateRequest(
        request,
        captured.expected,
        captured.actor,
        captured.page,
        ownedAppOrigin,
        () =>
          captured.page.evaluate(
            (name) => sessionStorage.getItem(name),
            recoveryStorage,
          ),
        knownSecrets,
      );
      const response = await request.response();
      uacRequire(response !== null);
      const created = await safeResponse(
        response,
        captured.actor,
        knownSecrets,
      );
      uacRequire(
        exact(created, ["taskID", "task", "request", "replayed"]) &&
          typeof created.taskID === "string" &&
          /^[A-Za-z0-9_.-]{1,128}$/u.test(created.taskID) &&
          created.taskID !== "platform" &&
          created.replayed === false &&
          sameValue(created.request, captured.expected),
      );
      const event: PostEvent = {
        ...inspected,
        ordinal,
        case: captured.mode,
        method: "POST",
        pathname: exportPrefix,
        status: 200,
        transport: "response",
        taskID: created.taskID,
      };
      captured.event = event;
      events.push(event);
    })().catch(() => {
      poisoned = true;
    });
    captured.tasks.push(task);
    tasks.push(task);
  };
  const onFailed = (request: Request): void => {
    if (writes.has(request)) poisoned = true;
  };
  context.on("request", onRequest);
  context.on("requestfailed", onFailed);
  return {
    armCase(mode, expected, actor, page) {
      uacRequire(
        !closed &&
          !poisoned &&
          (!active || active.settled) &&
          context.pages().includes(page) &&
          Number.isSafeInteger(actor) &&
          actor > 0,
        "post_count",
      );
      uacRequire(
        expected.uacDisableBit === mode &&
          Object.isFrozen(expected) &&
          Object.isFrozen(expected.screenColumns),
      );
      safeValue(expected, knownSecrets);
      active = { mode, expected, actor, page, tasks: [], settled: false };
    },
    async settleCase(taskID) {
      const captured = active;
      uacRequire(
        captured && !closed && !captured.settled && captured.tasks.length === 1,
        "post_count",
      );
      await Promise.all(captured.tasks);
      uacRequire(
        !poisoned && captured.event && captured.event.taskID === taskID,
        "post_binding",
      );
      captured.settled = true;
    },
    async closeAndSnapshot(finalBudget) {
      uacRequire(!closed && context.pages().length === 0, "post_count");
      let settledCount = -1;
      while (settledCount !== tasks.length) {
        settledCount = tasks.length;
        await Promise.all([...tasks]);
      }
      context.off("request", onRequest);
      context.off("requestfailed", onFailed);
      closed = true;
      uacRequire(
        !poisoned &&
          active?.settled &&
          createCount === 3 &&
          cancelCount === 0 &&
          unexpectedExportWriteCount === 0 &&
          events.length === 3,
        "post_count",
      );
      return {
        events: [...events].sort((a, b) => a.ordinal - b.ordinal),
        createCount,
        cancelCount,
        unexpectedExportWriteCount,
        listenerClosed: true,
        finalSensitiveSnapshot: { ...finalBudget },
      };
    },
  };
}

export async function readUACOriginalReceipt(
  page: Page,
  ownedAppOrigin: string,
  actorId: number,
  expected: Readonly<ExportRequest>,
  expectedIdempotencyKeySHA256: string,
  knownSecrets: readonly string[],
): Promise<ExportReceipt> {
  let key = "";
  let raw: unknown;
  try {
    uacRequire(
      observedIdempotencyHashes
        .get(knownSecrets)
        ?.has(expectedIdempotencyKeySHA256),
    );
    raw = await page.evaluate(
      (name) => sessionStorage.getItem(name),
      recoveryStorage,
    );
    key = locatorKey(raw, actorId, expected, knownSecrets);
    uacRequire(hash(key) === expectedIdempotencyKeySHA256);
    const [response] = await Promise.all([
      page.waitForResponse((r) => {
        try {
          return (
            ownedRequest(r.request(), page, ownedAppOrigin) &&
            new URL(r.url()).pathname === `${exportPrefix}/receipt`
          );
        } catch {
          return false;
        }
      }),
      page
        .getByRole("region", { name: "用户资产导出", exact: true })
        .getByRole("button", { name: "核对原导出回执", exact: true })
        .click(),
    ]);
    const request = response.request(),
      pairs = [...new URL(request.url()).searchParams.entries()];
    uacRequire(
      request.method() === "GET" &&
        pairs.length === 2 &&
        pairs.filter(([k, v]) => k === "domainId" && v === expected.domainId)
          .length === 1 &&
        pairs.filter(([k, v]) => k === "idempotencyKey" && v === key).length ===
          1,
    );
    const value = await safeResponse(response, actorId, knownSecrets);
    uacRequire(
      exact(value, ["taskID", "task", "request"]) &&
        typeof value.taskID === "string" &&
        object(value.task) &&
        value.task.state === "succeeded" &&
        sameValue(value.request, expected),
    );
    return value as ExportReceipt;
  } catch {
    throw new Error("uac_evidence_invalid:post_binding");
  } finally {
    key = "";
    raw = undefined;
  }
}

export async function captureUACReadOnlyScreenshot(
  originalTab: Page,
  sourcePage: Page,
  ownedAppOrigin: string,
  actorId: number,
  expectedTaskID: string,
  expected: Readonly<ExportRequest>,
  expectedIdempotencyKeySHA256: string,
  knownSecrets: readonly string[],
  info: TestInfo,
  mode: UACMode,
): Promise<void> {
  let contents = "",
    text = "";
  let values: string[] = [],
    candidates: string[] = [];
  try {
    uacRequire(
      originalTab !== sourcePage &&
        !sourcePage.isClosed() &&
        (await originalTab.opener()) === null &&
        new URL(originalTab.url()).origin === ownedAppOrigin &&
        ["set", "clear", "not_returned"].includes(mode) &&
        expected.uacDisableBit === mode &&
        /^[0-9a-f]{64}$/u.test(expectedIdempotencyKeySHA256),
      "artifact_binding",
    );
    uacRequire(
      (await originalTab.evaluate(
        (name) => sessionStorage.getItem(name),
        recoveryStorage,
      )) === null,
      "artifact_binding",
    );
    const identity = await originalTab.request.get("/api/auth/me");
    uacRequire(
      identity.status() === 200 && (await identity.json()).ID === actorId,
      "artifact_binding",
    );
    uacRequire(
      sameValue(originalTab.viewportSize(), { width: 1440, height: 900 }),
      "artifact_binding",
    );
    const panel = originalTab.getByRole("region", {
      name: "原导出只读详情",
      exact: true,
    });
    uacRequire(
      (await panel.isVisible()) &&
        (await panel.getByText(expectedTaskID, { exact: true }).count()) ===
          1 &&
        (await panel
          .getByText("原导出完整文件已就绪。", { exact: true })
          .isVisible()),
      "artifact_binding",
    );
    const requestText = await panel
      .getByLabel("原导出规范请求", { exact: true })
      .innerText();
    uacRequire(
      sameValue(parseDirectoryV4UniqueJSON(requestText), expected),
      "artifact_binding",
    );
    uacRequire(
      (await panel.getByLabel("原文件元数据", { exact: true }).isVisible()) &&
        (await panel
          .getByRole("button", { name: "下载原用户 XLSX", exact: true })
          .isEnabled()),
      "artifact_binding",
    );
    await panel.scrollIntoViewIfNeeded();
    uacRequire(
      await originalTab.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      "artifact_binding",
    );
    uacRequire(
      (await originalTab.locator('input[type="password"]').count()) === 0 &&
        (await originalTab
          .getByRole("region", { name: "用户资产导出", exact: true })
          .count()) === 0,
      "artifact_binding",
    );
    contents = await originalTab.locator("body").innerText();
    values = await originalTab
      .locator("input, textarea")
      .evaluateAll((nodes) =>
        nodes.map(
          (node) => (node as HTMLInputElement | HTMLTextAreaElement).value,
        ),
      );
    text = [contents, ...values].join("\n");
    rejectObservedKeys(text, knownSecrets, "artifact_binding");
    uacRequire(
      !runSecrets(knownSecrets).some(
        (secret) => secret.length > 0 && text.includes(secret),
      ),
      "artifact_binding",
    );
    candidates =
      text.match(
        /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gu,
      ) ?? [];
    uacRequire(
      !candidates.some(
        (candidate) => hash(candidate) === expectedIdempotencyKeySHA256,
      ),
      "artifact_binding",
    );
    uacRequire(
      (await originalTab.evaluate(
        (name) => sessionStorage.getItem(name),
        recoveryStorage,
      )) === null,
      "artifact_binding",
    );
    await originalTab.screenshot({
      path: info.outputPath(`${mode}-ready.png`),
      fullPage: false,
    });
  } catch {
    throw new Error("uac_evidence_invalid:artifact_binding");
  } finally {
    contents = "";
    text = "";
    values = [];
    candidates = [];
  }
}

async function databaseJSON(
  sql: string,
  cap: number,
): Promise<Record<string, unknown>> {
  try {
    const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
    uacRequire(
      /^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container),
      "source_binding",
    );
    const { stdout } = await run(
      "docker",
      [
        "exec",
        "-e",
        "PGOPTIONS=-c statement_timeout=2000",
        container,
        "psql",
        "-U",
        "postgres",
        "-d",
        "adtr_e2e",
        "-X",
        "-A",
        "-t",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
      ],
      { encoding: "utf8", timeout: 5000, maxBuffer: cap },
    );
    uacRequire(
      Buffer.byteLength(stdout, "utf8") <= cap && stdout.trim().length > 0,
      "source_binding",
    );
    const value = parseDirectoryV4UniqueJSON(stdout);
    scalarTree(value);
    uacRequire(object(value), "source_binding");
    return value;
  } catch {
    throw new Error("uac_evidence_invalid:source_binding");
  }
}

export async function storedUACSource(taskID: string): Promise<SourceEvidence> {
  uacRequire(
    /^[A-Za-z0-9_.-]{1,128}$/u.test(taskID) && taskID !== "platform",
    "task_binding",
  );
  return (await databaseJSON(
    `SELECT jsonb_build_object('taskUUID',o.task_id,'actorId',o.actor_id::text,'selection',jsonb_build_object('domainId',o.domain_id,'revision',u.connection_revision::text,'credentialRevision',u.connection_credential_generation::text),'sourcePins',jsonb_build_object('accountId',u.account_id,'accountCredentialRevision',u.account_credential_revision::text,'policyRevision',u.policy_revision,'grantRoleId',u.grant_role_id,'grantRevision',u.grant_revision::text),'bodyBase64',replace(encode(o.body,'base64'),chr(10),''),'sha256',o.sha256,'dictionaryVersion',o.dictionary_version,'count',o.object_count,'taskState',t.state,'taskName',t.kind,'payloadVersion',t.payload_version,'maxAttempts',t.max_attempts,'attempt',t.attempt,'purpose',u.purpose,'useState',u.state,'reason',u.quiescence_reason,'dependencies',(SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.consumer_kind=u.purpose AND d.object_id=u.task_id),'taskBinding',jsonb_build_object('payloadVersion',t.payload_version,'parentTaskUUID',t.parent_task_id,'payloadHash',t.payload_hash,'payload',t.payload)) FROM adtr.domain_directory_observations o JOIN adtr.tasks t ON t.task_id=o.task_id JOIN adtr.domain_directory_task_uses u ON u.task_id=o.task_id WHERE o.tenant_id='default' AND o.task_id='${taskID}'`,
    1 << 20,
  )) as unknown as SourceEvidence;
}

export async function storedUACExport(taskID: string): Promise<StoredExport> {
  uacRequire(
    /^[A-Za-z0-9_.-]{1,128}$/u.test(taskID) && taskID !== "platform",
    "task_binding",
  );
  return (await databaseJSON(
    `SELECT jsonb_build_object('taskID',t.task_id,'actorId',t.actor_id::text,'state',t.state,'kind',t.kind,'progress',t.progress,'attempt',t.attempt,'maxAttempts',t.max_attempts,'sourceSHA256',s.source_sha256,'rowCount',m.row_count,'byteCount',m.byte_count,'sha256',m.sha256,'chunkCount',m.chunk_count,'chunks',(SELECT jsonb_agg(jsonb_build_object('ordinal',c.chunk_no,'bodyBase64',replace(encode(c.data,'base64'),chr(10),''),'sha256',c.sha256) ORDER BY c.chunk_no) FROM adtr.user_asset_export_chunks c WHERE c.task_id=t.task_id AND c.fencing_token=m.fencing_token),'events',(SELECT jsonb_agg(jsonb_build_object('action',e.action,'state',e.state,'resultVersion',e.result_version) ORDER BY e.id) FROM adtr.task_events e WHERE e.task_id=t.task_id),'taskBinding',jsonb_build_object('payloadVersion',t.payload_version,'parentTaskUUID',t.parent_task_id,'payloadHash',t.payload_hash,'payload',t.payload),'idempotencyKeySHA256',encode(sha256(convert_to(t.idempotency_key,'UTF8')),'hex')) FROM adtr.tasks t JOIN adtr.user_asset_export_snapshots s ON s.task_id=t.task_id JOIN adtr.user_asset_export_manifests m ON m.task_id=t.task_id AND m.fencing_token=(t.result->>'artifactToken')::bigint WHERE t.tenant_id='default' AND t.task_id='${taskID}'`,
    2 << 20,
  )) as unknown as StoredExport;
}
