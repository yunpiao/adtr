import { navigateTo } from "./navigation";
import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import { createHmac } from "node:crypto";

// AD-F-176 bulk acceptance: additive host helpers, no production API changes.
import type {
  Request as BulkRequest,
  Response as BulkResponse,
} from "@playwright/test";
import { createHash } from "node:crypto";
import { lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

test.use({ trace: "off", screenshot: "off", video: "off" });

type BulkPhase =
  | "fixture_guard"
  | "create_viewer"
  | "viewer_initial_login"
  | "viewer_password_change"
  | "baseline_state"
  | "missing_role"
  | "filter_targets"
  | "audit_before"
  | "rejected_submit"
  | "rollback_users"
  | "rollback_sessions"
  | "rollback_audit"
  | "committed_submit"
  | "committed_users"
  | "old_sessions"
  | "committed_audit"
  | "reader_relogin"
  | "viewer_relogin"
  | "effective_access"
  | "safe_screenshot"
  | "new_context_cleanup"
  | "evidence_write";
type BulkObject = Record<string, unknown>;
const bulkRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const bulkOutput = join(bulkRoot, "web/test-results/access-role-bulk");
const bulkUsersPath =
  "/api/access/users?search=e2e.&sort=1&pageIdx=1&pageSize=20";
const bulkAuditPath =
  "/api/audit?filterEvent=user_role_assign&visibility=all&pageIdx=1&pageSize=100";
const bulkRoutes = [
  "GET /api/access/users",
  "POST /api/access/users/create",
  "GET /api/access/roles",
  "GET /api/access/permissions",
  "POST /api/access/assignments",
  "GET /api/access/not-registered",
  "POST /api/access/users",
];
const bulkSourcePaths = [
  // SOURCE-PATHS-BEGIN
  ".github/workflows/ci.yml",
  "AGENTS.md",
  "README.md",
  "docs/access-contract.md",
  "docs/architecture.md",
  "docs/delivery.md",
  "docs/task-contract.md",
  "internal/audit/directory_v4_schema.go",
  "internal/audit/query.go",
  "internal/audit/schema.go",
  "internal/audit/types.go",
  "internal/auth/access_http.go",
  "internal/auth/access_integration_test.go",
  "internal/auth/access_roles.go",
  "internal/auth/access_schema.go",
  "internal/auth/access_types.go",
  "internal/auth/access_users.go",
  "internal/auth/audit_http.go",
  "internal/auth/crypto.go",
  "internal/auth/request_metadata.go",
  "internal/auth/service.go",
  "requirements/catalog.json",
  "scripts/ci_inventory.json",
  "scripts/ci_selection.py",
  "scripts/ci_workflow_contract.py",
  "scripts/e2e_owned_container.py",
  "scripts/e2e_runtime_diagnostics.py",
  "scripts/fixtures/access_role_bulk_ci_delta.json",
  "scripts/fixtures/archive_target_observation_ci_delta.json",
  "scripts/test_access_role_bulk_contract.py",
  "scripts/test_archive_target_observation_ci_contract.py",
  "scripts/test_auth_e2e.py",
  "scripts/test_ci_selection_contract.py",
  "scripts/test_e2e_runtime_diagnostics_contract.py",
  "scripts/validate_access_role_bulk_evidence.py",
  "web/e2e/access.spec.ts",
  "web/e2e/navigation.ts",
  "web/package.json",
  "web/playwright.config.ts",
  "web/src/Access.test.tsx",
  "web/src/AccessRoles.tsx",
  "web/src/AccessUsers.tsx",
  "web/src/AccessWorkspace.tsx",
  "web/src/App.tsx",
  "web/src/access-common.tsx",
  "web/src/api.ts",
  // SOURCE-PATHS-END
] as const;

function bulkRequire(value: unknown): asserts value {
  if (!value) throw new Error("access_role_bulk_check_failed");
}

function bulkObject(value: unknown): BulkObject {
  bulkRequire(
    value !== null && typeof value === "object" && !Array.isArray(value),
  );
  return value as BulkObject;
}

function bulkExact(value: unknown, expected: unknown): boolean {
  if (Array.isArray(expected))
    return (
      Array.isArray(value) &&
      value.length === expected.length &&
      expected.every((item, index) => bulkExact(value[index], item))
    );
  if (expected !== null && typeof expected === "object") {
    if (value === null || typeof value !== "object" || Array.isArray(value))
      return false;
    const actual = value as BulkObject;
    const wanted = expected as BulkObject;
    return (
      Object.keys(actual).length === Object.keys(wanted).length &&
      Object.keys(wanted).every(
        (key) =>
          Object.hasOwn(actual, key) && bulkExact(actual[key], wanted[key]),
      )
    );
  }
  return value === expected;
}

function bulkID(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function bulkDigest(value: string | Buffer): string {
  return createHash("sha256").update(value).digest("hex");
}

async function bulkRead(
  context: BrowserContext,
  path: string,
  status = 200,
): Promise<BulkObject> {
  try {
    const response = await context.request.get(path);
    bulkRequire(response.status() === status);
    return bulkObject(await response.json());
  } catch {
    throw new Error("access_role_bulk_read_failed");
  }
}

async function bulkDenied(
  context: BrowserContext,
  path: string,
  status: number,
) {
  const body = await bulkRead(context, path, status);
  bulkRequire(
    bulkExact(body, {
      error: status === 401 ? "unauthenticated" : "forbidden",
    }),
  );
}

function bulkIdentity(
  value: BulkObject,
  id: number,
  username: string,
  mfa: boolean,
) {
  bulkRequire(
    value.ID === id &&
      value.username === username &&
      value.needChangePwd === false &&
      value.hasMfa === mfa,
  );
}

function bulkUserRows(value: BulkObject): BulkObject[] {
  bulkRequire(bulkObject(value.page).total === 2 && value.exhausted === true);
  bulkRequire(Array.isArray(value.List) && value.List.length === 2);
  return value.List.map(bulkObject);
}

function bulkUnchangedProfile(before: BulkObject, after: BulkObject) {
  for (const key of [
    "ID",
    "username",
    "role",
    "passStrength",
    "mobile",
    "email",
    "remark",
    "createTm",
    "hasMfa",
    "avatar",
    "pwdUpdateTm",
    "address",
    "realName",
    "department",
    "post",
    "disabled",
  ])
    bulkRequire(Object.hasOwn(before, key) && before[key] === after[key]);
}

async function bulkSubmit(
  page: Page,
  path: string,
  button: string,
  status: number,
  payload: (value: BulkObject) => boolean,
  accepted: (value: BulkObject) => boolean,
): Promise<BulkObject> {
  // Only controlled branches are joined. Playwright owns any underlying CDP
  // work after the logical deadline; immediate handlers discard late results.
  let closed = false;
  let selected: BulkRequest | undefined;
  let responseSeen = false;
  let count = 0;
  let result: BulkObject | undefined;
  let finishAction!: (ok: boolean) => void;
  let finishResponse!: (ok: boolean) => void;
  const action = new Promise<boolean>((resolve) => {
    finishAction = resolve;
  });
  const response = new Promise<boolean>((resolve) => {
    finishResponse = resolve;
  });
  const stop = () => {
    if (closed) return;
    closed = true;
    finishAction(false);
    finishResponse(false);
  };
  const origin = new URL(process.env.ADTR_E2E_BASE_URL!).origin;
  const onRequest = (request: BulkRequest) => {
    if (closed) return;
    try {
      const url = new URL(request.url());
      if (url.pathname !== path || request.method() !== "POST") return;
      count++;
      if (selected) {
        stop();
        return;
      }
      selected = request;
      bulkRequire(
        request.url() === origin + path &&
          request.frame() === page.mainFrame() &&
          request.resourceType() === "fetch" &&
          request.redirectedFrom() === null,
      );
      const raw = request.postData();
      bulkRequire(raw !== null && Buffer.byteLength(raw, "utf8") <= 8192);
      bulkRequire(payload(bulkObject(JSON.parse(raw))));
    } catch {
      stop();
    }
  };
  const onResponse = (candidate: BulkResponse) => {
    try {
      if (closed || candidate.request() !== selected) return;
      if (responseSeen) {
        stop();
        return;
      }
      responseSeen = true;
      // Start the exact selected object's JSON read immediately, only once.
      const body = candidate.json();
      void body.then(
        (value: unknown) => {
          if (closed) return;
          try {
            bulkRequire(
              candidate.status() === status &&
                candidate.url() === origin + path &&
                !candidate.fromServiceWorker(),
            );
            const object = bulkObject(value);
            bulkRequire(accepted(object));
            result = object;
            finishResponse(true);
          } catch {
            stop();
          }
        },
        () => {
          if (!closed) stop();
        },
      );
    } catch {
      stop();
    }
  };
  const onFailed = (request: BulkRequest) => {
    if (request === selected) stop();
  };
  const timer = setTimeout(stop, 15_000);
  try {
    page.on("request", onRequest);
    page.on("response", onResponse);
    page.on("requestfailed", onFailed);
    page.on("close", stop);
    page.on("crash", stop);
    try {
      const clicked = page
        .getByRole("button", { name: button, exact: true })
        .click();
      void clicked.then(() => {
        if (!closed) finishAction(true);
      }, stop);
    } catch {
      stop();
    }
    const outcomes = await Promise.all([action, response]);
    bulkRequire(!closed && outcomes.every(Boolean) && count === 1 && result);
    return result;
  } finally {
    closed = true;
    clearTimeout(timer);
    page.off("request", onRequest);
    page.off("response", onResponse);
    page.off("requestfailed", onFailed);
    page.off("close", stop);
    page.off("crash", stop);
    selected = undefined;
    result = undefined;
  }
}

async function bulkRouteChecks(
  context: BrowserContext,
  identity: BulkObject,
  allowed: boolean,
) {
  bulkRequire(
    typeof identity.csrfToken === "string" && identity.csrfToken.length > 0,
  );
  try {
    const response = await context.request.post("/api/access/check", {
      headers: {
        Origin: new URL(process.env.ADTR_E2E_BASE_URL!).origin,
        "X-CSRF-Token": identity.csrfToken,
      },
      data: { paths: bulkRoutes },
    });
    const results = [allowed, false, false, false, false, false, false];
    bulkRequire(response.status() === 200);
    bulkRequire(bulkExact(await response.json(), { results }));
    return results;
  } catch {
    throw new Error("access_role_bulk_check_failed");
  }
}

function bulkMenu(body: BulkObject): Permission[] {
  bulkRequire(Array.isArray(body.menu));
  return body.menu.map((item) => {
    const node = bulkObject(item);
    const auth = bulkObject(node.auth);
    bulkRequire(
      typeof node.mark === "string" &&
        typeof auth.readable === "boolean" &&
        typeof auth.writeable === "boolean",
    );
    return {
      mark: node.mark,
      auth: { readable: auth.readable, writeable: auth.writeable },
    };
  });
}

function bulkCIIdentity() {
  const runID = process.env.GITHUB_RUN_ID;
  const runAttempt = process.env.GITHUB_RUN_ATTEMPT;
  const event = process.env.GITHUB_EVENT_NAME;
  const testedCommit = process.env.GITHUB_SHA;
  bulkRequire(
    process.env.GITHUB_REPOSITORY === "yunpiao/adtr" &&
      typeof runID === "string" &&
      /^[1-9][0-9]{0,19}$/.test(runID) &&
      typeof runAttempt === "string" &&
      /^[1-9][0-9]{0,19}$/.test(runAttempt) &&
      (event === "push" || event === "pull_request") &&
      typeof testedCommit === "string" &&
      /^[0-9a-f]{40}$/.test(testedCommit),
  );
  return {
    repository: "yunpiao/adtr",
    suite: "access",
    runID,
    runAttempt,
    event,
    testedCommit,
  };
}

function bulkSources(): Record<string, string> {
  return Object.fromEntries(
    bulkSourcePaths.map((path) => {
      let current = bulkRoot;
      for (const part of path.split("/")) {
        current = join(current, part);
        bulkRequire(!lstatSync(current).isSymbolicLink());
      }
      bulkRequire(lstatSync(current).isFile());
      return [path, bulkDigest(readFileSync(current))];
    }),
  );
}
// End AD-F-176 additive host helpers.

type Permission = {
  mark: string;
  auth: { readable: boolean; writeable: boolean };
};
type Authenticator = { secret: string; lastCounter: number };

function totp(secret: string, now = Date.now()): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const char of secret.replace(/=+$/, "").toUpperCase()) {
    const index = alphabet.indexOf(char);
    if (index < 0) throw new Error("Invalid synthetic TOTP secret");
    bits += index.toString(2).padStart(5, "0");
  }
  const bytes = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8)
    bytes.push(parseInt(bits.slice(offset, offset + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30_000)));
  const digest = createHmac("sha1", Buffer.from(bytes))
    .update(counter)
    .digest();
  const offset = digest[digest.length - 1] & 15;
  return ((digest.readUInt32BE(offset) & 0x7fffffff) % 1_000_000)
    .toString()
    .padStart(6, "0");
}

async function freshCode(authenticator: Authenticator): Promise<string> {
  // Enrollment and every privileged write consume a counter. Use real time;
  // never freeze the browser clock or bypass the server's replay protection.
  const earliest = (authenticator.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest)
    await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  authenticator.lastCounter = Math.floor(now / 30_000);
  return totp(authenticator.secret, now);
}

async function login(page: Page, username: string, password: string) {
  await expect(
    page.getByRole("heading", { name: "登录账户", exact: true }),
  ).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function changeInitialPassword(
  page: Page,
  initial: string,
  changed: string,
) {
  await expect(
    page.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  await page.getByLabel("当前密码", { exact: true }).fill(initial);
  await page.getByLabel("新密码", { exact: true }).fill(changed);
  await page.getByLabel("确认新密码", { exact: true }).fill(changed);
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "账户概览", exact: true }),
  ).toBeVisible();
}

async function enrollMfa(page: Page, password: string): Promise<Authenticator> {
  await navigateTo(page, "多因素认证");
  await page.getByLabel("当前密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "开始设置", exact: true }).click();
  const secret = await page
    .getByLabel("设置密钥", { exact: true })
    .inputValue();
  expect(secret.length).toBeGreaterThan(10);
  const now = Date.now();
  await page
    .getByLabel("认证器验证码", { exact: true })
    .fill(totp(secret, now));
  await page.getByRole("button", { name: "验证并启用", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("多因素认证已启用");
  return { secret, lastCounter: Math.floor(now / 30_000) };
}

async function readJSON(context: BrowserContext, path: string) {
  const response = await context.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return response.json();
}

async function submitMutation(page: Page, path: string, button: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (candidate) =>
        new URL(candidate.url()).pathname === path &&
        candidate.request().method() === "POST",
    ),
    page.getByRole("button", { name: button, exact: true }).click(),
  ]);
  expect(response.status(), `POST ${path}`).toBe(200);
  const result = await response.json();
  expect(result).toMatchObject({ result: "SUCCESS", sessionRevoked: false });
  return result;
}

test("real browser → API → PostgreSQL users and function permissions", async ({
  page,
  context,
}) => {
  test.setTimeout(300_000);
  // AD-F-176 fixture and evidence guard; never target an external account.
  let bulkEvidence: BulkObject | null = null;
  let bulkRunIdentity: ReturnType<typeof bulkCIIdentity>;
  try {
    const base = new URL(process.env.ADTR_E2E_BASE_URL ?? "");
    bulkRequire(
      base.protocol === "http:" &&
        ["127.0.0.1", "[::1]"].includes(base.hostname) &&
        /^[1-9][0-9]{0,4}$/.test(base.port) &&
        Number(base.port) <= 65535 &&
        base.username === "" &&
        base.password === "" &&
        base.search === "" &&
        base.hash === "" &&
        base.pathname === "/" &&
        process.env.ADTR_E2E_USERNAME === "e2e-admin",
    );
    bulkRunIdentity = bulkCIIdentity();
    for (const path of [bulkRoot, join(bulkRoot, "web")]) {
      bulkRequire(
        lstatSync(path).isDirectory() && !lstatSync(path).isSymbolicLink(),
      );
    }
    try {
      mkdirSync(dirname(bulkOutput));
    } catch {
      // An existing parent is accepted only after the same no-symlink check.
    }
    bulkRequire(
      lstatSync(dirname(bulkOutput)).isDirectory() &&
        !lstatSync(dirname(bulkOutput)).isSymbolicLink(),
    );
    mkdirSync(bulkOutput);
  } catch {
    throw new Error("access_role_bulk_failed:fixture_guard");
  }
  // End AD-F-176 fixture and evidence guard.

  // Run only against the access runner's fresh disposable database. The auth
  // lifecycle spec changes its own bootstrap account and must run separately.
  const username = process.env.ADTR_E2E_USERNAME,
    password = process.env.ADTR_E2E_PASSWORD;
  if (!username || !password)
    throw new Error(
      "ADTR_E2E_USERNAME and ADTR_E2E_PASSWORD must identify a disposable synthetic bootstrap account",
    );
  const changed = "Changed E2E Password 123";
  const readerUsername = "e2e.viewer";
  const readerInitial = "Initial Reader Password 123";
  const readerChanged = "Changed Reader Password 456";
  const roleName = "E2E Users Read Only";
  const roleRemark = "Synthetic browser access test";
  const profile = {
    mobile: "13800000000",
    email: "e2e.viewer@example.invalid",
    remark: "Synthetic user created through the browser",
    address: "合成测试所在地",
    realName: "合成测试用户",
    department: "合成测试部门",
    post: "合成测试岗位",
  };

  await page.goto("/");
  await login(page, username, password);
  await changeInitialPassword(page, password, changed);
  const adminMfa = await enrollMfa(page, changed);
  await navigateTo(page, "访问管理");
  for (const name of ["用户管理", "角色管理", "功能权限"])
    await expect(page.getByRole("button", { name, exact: true })).toBeVisible();
  const adminMenu = await readJSON(context, "/api/access/menu");
  expect(adminMenu.menu.map((node: Permission) => node.mark).sort()).toEqual([
    "audit",
    "audit_exports",
    "computer_asset_exports",
    "directory_assets",
    "domains",
    "group_asset_exports",
    "operation_accounts",
    "permissions",
    "roles",
    "schedules",
    "system",
    "system_logs",
    "task_archive",
    "tasks",
    "user_asset_exports",
    "users",
  ]);

  await page.getByRole("button", { name: "角色管理", exact: true }).click();
  await page.getByRole("button", { name: "新增角色", exact: true }).click();
  await page.getByLabel("角色名称", { exact: true }).fill(roleName);
  await page.getByLabel("角色备注", { exact: true }).fill(roleRemark);
  await page
    .getByRole("checkbox", { name: "用户管理：读取", exact: true })
    .check();
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(adminMfa));
  const savedRole = await submitMutation(
    page,
    "/api/access/roles/save",
    "保存角色",
  );
  await expect(
    page.getByRole("status").filter({ hasText: "角色已保存" }),
  ).toBeVisible();
  expect(typeof savedRole.roleID).toBe("string");
  expect(savedRole.roleID.length).toBeGreaterThan(0);
  const roleID: string = savedRole.roleID;
  const persistedRole = await readJSON(
    context,
    `/api/access/roles/detail?roleID=${encodeURIComponent(roleID)}`,
  );
  expect(persistedRole.role).toMatchObject({
    id: roleID,
    name: roleName,
    remark: roleRemark,
    userNum: 0,
  });
  const permissions = await readJSON(
    context,
    `/api/access/permissions?roleID=${encodeURIComponent(roleID)}`,
  );
  expect(
    permissions.permissions.map(({ mark, auth }: Permission) => ({
      mark,
      auth,
    })),
  ).toEqual([
    { mark: "users", auth: { readable: true, writeable: false } },
    { mark: "roles", auth: { readable: false, writeable: false } },
    { mark: "permissions", auth: { readable: false, writeable: false } },
    { mark: "tasks", auth: { readable: false, writeable: false } },
    { mark: "audit", auth: { readable: false, writeable: false } },
    { mark: "audit_exports", auth: { readable: false, writeable: false } },
    { mark: "system", auth: { readable: false, writeable: false } },
    { mark: "schedules", auth: { readable: false, writeable: false } },
    { mark: "task_archive", auth: { readable: false, writeable: false } },
    { mark: "domains", auth: { readable: false, writeable: false } },
    { mark: "operation_accounts", auth: { readable: false, writeable: false } },
    { mark: "system_logs", auth: { readable: false, writeable: false } },
    { mark: "directory_assets", auth: { readable: false, writeable: false } },
    { mark: "user_asset_exports", auth: { readable: false, writeable: false } },
    { mark: "group_asset_exports", auth: { readable: false, writeable: false } },
    { mark: "computer_asset_exports", auth: { readable: false, writeable: false } },
  ]);
  expect(persistedRole.permissions).toEqual(permissions.permissions);

  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  await page.getByLabel("用户名", { exact: true }).fill(readerUsername);
  await page.getByLabel("初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("确认初始密码", { exact: true }).fill(readerInitial);
  await page.getByLabel("角色", { exact: true }).fill(roleID);
  for (const [label, value] of [
    ["手机号", profile.mobile],
    ["邮箱", profile.email],
    ["备注", profile.remark],
    ["所在地", profile.address],
    ["真实姓名", profile.realName],
    ["部门", profile.department],
    ["岗位", profile.post],
  ])
    await page.getByLabel(label, { exact: true }).fill(value);
  await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("未使用的认证器验证码", { exact: true })
    .fill(await freshCode(adminMfa));
  const savedUser = await submitMutation(
    page,
    "/api/access/users/create",
    "创建用户",
  );
  await expect(
    page.getByRole("status").filter({ hasText: "用户已创建" }),
  ).toBeVisible();
  await expect(
    page.getByRole("rowheader").filter({ hasText: readerUsername }),
  ).toBeVisible();
  expect(savedUser.ID).toBeGreaterThan(0);
  const users = await readJSON(
    context,
    `/api/access/users?${new URLSearchParams({
      search: readerUsername,
      filterRole: roleID,
      filterMfaStatus: "stop",
    })}`,
  );
  expect(users.page.total).toBe(1);
  expect(users.List).toHaveLength(1);
  expect(users.List[0]).toMatchObject({
    ...profile,
    ID: savedUser.ID,
    username: readerUsername,
    role: "viewer",
    priv: 2,
    roleID,
    roleName,
    hasMfa: false,
    disabled: false,
  });
  for (const secret of [
    "password",
    "passwordHash",
    "totpCode",
    "mfaSecret",
    "csrfToken",
  ])
    expect(users.List[0]).not.toHaveProperty(secret);
  expect(
    await readJSON(
      context,
      `/api/access/users/exists?username=${encodeURIComponent(readerUsername)}`,
    ),
  ).toEqual({ result: true });
  expect(
    (
      await readJSON(
        context,
        `/api/access/roles/detail?roleID=${encodeURIComponent(roleID)}`,
      )
    ).role.userNum,
  ).toBe(1);

  // A second browser context proves that an administrator reset revokes the
  // target's real cookie session, then requires a password change at next login.
  const readerContext = await context.browser()!.newContext({
    baseURL: process.env.ADTR_E2E_BASE_URL ?? "http://127.0.0.1:18080",
  });
  const readerPage = await readerContext.newPage();
  await readerPage.goto("/");
  await login(readerPage, readerUsername, readerInitial);
  await expect(
    readerPage.getByRole("heading", { name: "修改密码", exact: true }),
  ).toBeVisible();
  expect((await readJSON(readerContext, "/api/auth/me")).needChangePwd).toBe(
    true,
  );
  const readerReset = "Reset Reader Password 789";
  await navigateTo(page, "重置用户密码");
  await page.getByLabel("目标用户名", { exact: true }).fill(readerUsername);
  await page.getByLabel("用户新密码", { exact: true }).fill(readerReset);
  await page.getByLabel("确认用户新密码", { exact: true }).fill(readerReset);
  await page.getByLabel("管理员当前密码", { exact: true }).fill(changed);
  await page
    .getByLabel("管理员认证器验证码", { exact: true })
    .fill(await freshCode(adminMfa));
  const [resetResponse] = await Promise.all([
    page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/api/auth/reset-password" &&
        response.request().method() === "POST",
    ),
    page.getByRole("button", { name: "确认重置密码", exact: true }).click(),
  ]);
  expect(resetResponse.status()).toBe(200);
  expect(await resetResponse.json()).toEqual({ result: "SUCCESS" });
  await expect(page.getByRole("status")).toContainText("用户密码已重置");
  expect((await readerContext.request.get("/api/auth/me")).status()).toBe(401);
  await readerPage.reload();
  try {
    await login(readerPage, readerUsername, readerReset);
    await expect(
      readerPage.getByRole("heading", { name: "修改密码", exact: true }),
    ).toBeVisible();
    const firstReaderSession = await readJSON(readerContext, "/api/auth/me");
    expect(firstReaderSession).toMatchObject({
      username: readerUsername,
      needChangePwd: true,
    });
    const forcedAccess = await readerContext.request.get("/api/access/users");
    expect(forcedAccess.status()).toBe(403);
    expect(await forcedAccess.json()).toEqual({
      error: "password_change_required",
    });
    await changeInitialPassword(readerPage, readerReset, readerChanged);
    // Give the reader valid MFA proof too, so write denial demonstrates its
    // persisted role boundary rather than missing enrollment or invalid proof.
    const readerMfa = await enrollMfa(readerPage, readerChanged);
    await readerPage.reload();
    await expect(
      readerPage.getByRole("heading", { name: "账户概览", exact: true }),
    ).toBeVisible();
    await navigateTo(readerPage, "访问管理");
    await expect(
      readerPage.getByRole("button", { name: "用户管理", exact: true }),
    ).toBeVisible();
    await readerPage
      .getByRole("button", { name: "用户管理", exact: true })
      .click();
    await expect(
      readerPage.getByRole("rowheader").filter({ hasText: readerUsername }),
    ).toBeVisible();
    for (const name of ["角色管理", "功能权限", "新增用户"])
      await expect(
        readerPage.getByRole("button", { name, exact: true }),
      ).toHaveCount(0);
    const readerMenu = await readJSON(readerContext, "/api/access/menu");
    expect(readerMenu.menu.map((node: Permission) => node.mark)).toEqual([
      "users",
    ]);
    expect(readerMenu.menu[0].auth).toEqual({
      readable: true,
      writeable: false,
    });
    const readerSession = await readJSON(readerContext, "/api/auth/me");
    expect(readerSession.needChangePwd).toBe(false);
    expect(readerSession.hasMfa).toBe(true);
    const headers = {
      Origin: new URL(readerPage.url()).origin,
      "X-CSRF-Token": readerSession.csrfToken,
    };
    const checked = await readerContext.request.post("/api/access/check", {
      headers,
      data: {
        paths: [
          "GET /api/access/users",
          "POST /api/access/users/create",
          "GET /api/access/roles",
          "GET /api/access/permissions",
          "POST /api/access/assignments",
          "GET /api/access/not-registered",
          "POST /api/access/users",
        ],
      },
    });
    expect(checked.status()).toBe(200);
    expect(await checked.json()).toEqual({
      results: [true, false, false, false, false, false, false],
    });
    const allowedUsers = await readJSON(
      readerContext,
      `/api/access/users?search=${encodeURIComponent(readerUsername)}`,
    );
    expect(allowedUsers.List).toHaveLength(1);
    expect(allowedUsers.List[0]).toMatchObject({
      ID: savedUser.ID,
      username: readerUsername,
      roleID,
      roleName,
      ...profile,
    });
    for (const path of [
      "/api/access/roles",
      `/api/access/permissions?roleID=${encodeURIComponent(roleID)}`,
    ]) {
      const deniedRead = await readerContext.request.get(path);
      expect(deniedRead.status()).toBe(403);
      expect(await deniedRead.json()).toEqual({ error: "forbidden" });
    }
    const deniedWrite = await readerContext.request.post(
      "/api/access/users/create",
      {
        headers,
        data: {
          username: "e2e.forbidden",
          password: "Forbidden Synthetic Password 789",
          roleID: "viewer",
          actorPassword: readerChanged,
          totpCode: await freshCode(readerMfa),
        },
      },
    );
    expect(deniedWrite.status()).toBe(403);
    expect(await deniedWrite.json()).toEqual({ error: "forbidden" });
    expect(
      await readJSON(
        readerContext,
        "/api/access/users/exists?username=e2e.forbidden",
      ),
    ).toEqual({ result: false });
    expect(
      await readerPage.evaluate(
        () => localStorage.length + sessionStorage.length,
      ),
    ).toBe(0);
    // AD-F-176 real two-user atomic rollback, swap, revocation and re-login.
    let bulkPhase: BulkPhase = "create_viewer";
    let bulkFailed = false;
    let viewerContext: BrowserContext | undefined;
    let createRequests = 0;
    let assignmentRequests = 0;
    let requestCountValid = true;
    const viewerUsername = "e2e.bulk-viewer";
    const viewerInitial = "Initial Bulk Viewer Password 123";
    const viewerChanged = "Changed Bulk Viewer Password 456";
    const missingRole = "missing_bulk_role_000001";
    const countMutation = (request: BulkRequest) => {
      try {
        if (request.method() !== "POST") return;
        const url = new URL(request.url());
        if (url.origin !== new URL(process.env.ADTR_E2E_BASE_URL!).origin)
          return;
        if (url.pathname === "/api/access/users/create") createRequests++;
        if (url.pathname === "/api/access/assignments") assignmentRequests++;
      } catch {
        requestCountValid = false;
      }
    };
    try {
      page.on("request", countMutation);
      await navigateTo(page, "访问管理");
      await page.getByRole("button", { name: "用户管理", exact: true }).click();
      await page.getByRole("button", { name: "新增用户", exact: true }).click();
      await page.getByLabel("用户名", { exact: true }).fill(viewerUsername);
      await page.getByLabel("初始密码", { exact: true }).fill(viewerInitial);
      await page
        .getByLabel("确认初始密码", { exact: true })
        .fill(viewerInitial);
      await page.getByLabel("角色", { exact: true }).fill("viewer");
      await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
      await page
        .getByLabel("未使用的认证器验证码", { exact: true })
        .fill(await freshCode(adminMfa));
      const created = await bulkSubmit(
        page,
        "/api/access/users/create",
        "创建用户",
        200,
        (body) => body.username === viewerUsername && body.roleID === "viewer",
        (body) =>
          bulkID(body.ID) &&
          bulkExact(body, {
            ID: body.ID,
            result: "SUCCESS",
            sessionRevoked: false,
          }),
      );
      const viewerID = created.ID;
      bulkRequire(
        bulkID(viewerID) &&
          viewerID !== savedUser.ID &&
          Number(createRequests) === 1,
      );
      await expect(
        page.getByRole("status").filter({ hasText: "用户已创建" }),
      ).toBeVisible();

      bulkPhase = "viewer_initial_login";
      viewerContext = await context.browser()!.newContext({
        baseURL: process.env.ADTR_E2E_BASE_URL,
      });
      const viewerPage = await viewerContext.newPage();
      await viewerPage.goto("/");
      await login(viewerPage, viewerUsername, viewerInitial);
      bulkPhase = "viewer_password_change";
      await changeInitialPassword(viewerPage, viewerInitial, viewerChanged);

      bulkPhase = "baseline_state";
      const adminBefore = await bulkRead(context, "/api/auth/me");
      bulkRequire(
        bulkID(savedUser.ID) &&
          bulkID(adminBefore.ID) &&
          adminBefore.username === username &&
          adminBefore.role === "platform_admin",
      );
      const actorID = adminBefore.ID;
      bulkRequire(actorID !== savedUser.ID && actorID !== viewerID);
      bulkIdentity(
        await bulkRead(readerContext, "/api/auth/me"),
        savedUser.ID,
        readerUsername,
        true,
      );
      bulkIdentity(
        await bulkRead(viewerContext, "/api/auth/me"),
        viewerID,
        viewerUsername,
        false,
      );
      bulkRequire(
        bulkExact(
          bulkMenu(await bulkRead(viewerContext, "/api/access/menu")),
          [],
        ),
      );
      await bulkDenied(viewerContext, "/api/access/users", 403);
      const before = bulkUserRows(await bulkRead(context, bulkUsersPath));
      bulkRequire(
        before[0].ID === savedUser.ID &&
          before[0].username === readerUsername &&
          before[0].roleID === roleID &&
          before[0].priv === 2,
      );
      bulkRequire(
        before[1].ID === viewerID &&
          before[1].username === viewerUsername &&
          before[1].roleID === "viewer" &&
          before[1].priv === 3,
      );
      bulkRequire(
        before.every((user) => user.role === "viewer" &&
          user.disabled === false),
      );

      bulkPhase = "missing_role";
      bulkRequire(/^[A-Za-z0-9_-]{24}$/.test(roleID) && roleID !== missingRole);
      bulkRequire(
        bulkExact(
          await bulkRead(
            context,
            `/api/access/roles/detail?roleID=${missingRole}`,
            404,
          ),
          { error: "not_found" },
        ),
      );

      bulkPhase = "filter_targets";
      await page.getByLabel("搜索用户名", { exact: true }).fill("e2e.");
      await page.getByLabel("排序", { exact: true }).selectOption("1");
      await page.getByRole("button", { name: "应用筛选", exact: true }).click();
      const table = page.getByRole("table", {
        name: "平台用户列表",
        exact: true,
      });
      const rows = table.locator("tbody tr");
      await expect(rows).toHaveCount(2);
      await expect(
        rows.nth(0).getByRole("checkbox", {
          name: `选择 ${readerUsername}`,
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        rows.nth(1).getByRole("checkbox", {
          name: `选择 ${viewerUsername}`,
          exact: true,
        }),
      ).toBeVisible();
      await page
        .getByRole("checkbox", { name: `选择 ${readerUsername}`, exact: true })
        .check();
      await page
        .getByRole("checkbox", { name: `选择 ${viewerUsername}`, exact: true })
        .check();
      await page
        .getByRole("button", { name: "批量分配（2）", exact: true })
        .click();
      await page
        .getByLabel(`角色：${readerUsername}`, { exact: true })
        .fill("viewer");
      await page
        .getByLabel(`角色：${viewerUsername}`, { exact: true })
        .fill(missingRole);

      const auditRows = async (total: number) => {
        const data = await bulkRead(context, bulkAuditPath);
        bulkRequire(
          bulkObject(data.page).total === total && data.exhausted === true,
        );
        bulkRequire(Array.isArray(data.List) && data.List.length === total);
        return data.List.map(bulkObject);
      };
      bulkPhase = "audit_before";
      await auditRows(0);
      bulkPhase = "rejected_submit";
      await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
      await page
        .getByLabel("未使用的认证器验证码", { exact: true })
        .fill(await freshCode(adminMfa));
      await bulkSubmit(
        page,
        "/api/access/assignments",
        "保存角色分配",
        404,
        (body) =>
          bulkExact(body.userRoles, [
            { username: readerUsername, roleID: "viewer" },
            { username: viewerUsername, roleID: missingRole },
          ]),
        (body) => bulkExact(body, { error: "not_found" }),
      );
      bulkRequire(Number(assignmentRequests) === 1);
      await expect(
        page.getByRole("button", { name: "保存角色分配", exact: true }),
      ).toBeEnabled();
      bulkPhase = "rollback_users";
      const rejected = bulkUserRows(await bulkRead(context, bulkUsersPath));
      for (let i = 0; i < 2; i++) {
        bulkUnchangedProfile(before[i], rejected[i]);
        bulkRequire(
          rejected[i].roleID === before[i].roleID &&
            rejected[i].roleName === before[i].roleName &&
            rejected[i].priv === before[i].priv,
        );
      }
      bulkPhase = "rollback_sessions";
      bulkIdentity(
        await bulkRead(readerContext, "/api/auth/me"),
        savedUser.ID,
        readerUsername,
        true,
      );
      bulkIdentity(
        await bulkRead(viewerContext, "/api/auth/me"),
        viewerID,
        viewerUsername,
        false,
      );
      bulkPhase = "rollback_audit";
      await auditRows(0);

      bulkPhase = "committed_submit";
      await page
        .getByLabel(`角色：${viewerUsername}`, { exact: true })
        .fill(roleID);
      await page.getByLabel("操作者当前密码", { exact: true }).fill(changed);
      await page
        .getByLabel("未使用的认证器验证码", { exact: true })
        .fill(await freshCode(adminMfa));
      await bulkSubmit(
        page,
        "/api/access/assignments",
        "保存角色分配",
        200,
        (body) =>
          bulkExact(body.userRoles, [
            { username: readerUsername, roleID: "viewer" },
            { username: viewerUsername, roleID },
          ]),
        (body) => bulkExact(body, { result: "SUCCESS", sessionRevoked: false }),
      );
      bulkRequire(
        Number(assignmentRequests) === 2 &&
          Number(createRequests) === 1 &&
          requestCountValid,
      );
      await expect(
        page.getByRole("status").filter({ hasText: "角色分配已保存" }),
      ).toBeVisible();
      bulkPhase = "committed_users";
      const committed = bulkUserRows(await bulkRead(context, bulkUsersPath));
      for (let i = 0; i < 2; i++) bulkUnchangedProfile(before[i], committed[i]);
      bulkRequire(committed[0].roleID === "viewer" && committed[0].priv === 3);
      bulkRequire(
        committed[1].roleID === roleID &&
          committed[1].priv === 2 &&
          committed[1].roleName === roleName,
      );
      bulkRequire(
        bulkObject(
          (
            await bulkRead(
              context,
              `/api/access/roles/detail?roleID=${encodeURIComponent(roleID)}`,
            )
          ).role,
        ).userNum === 1,
      );
      const adminAfter = await bulkRead(context, "/api/auth/me");
      bulkRequire(
        adminAfter.ID === actorID &&
          adminAfter.username === username &&
          adminAfter.role === "platform_admin",
      );

      bulkPhase = "old_sessions";
      // Same two contexts; neither login is attempted before both 401s.
      await bulkDenied(readerContext, "/api/auth/me", 401);
      await bulkDenied(viewerContext, "/api/auth/me", 401);
      bulkPhase = "committed_audit";
      const audit = await auditRows(2);
      const events: { eventID: string; targetID: number }[] = [];
      let auditRequest = "";
      for (const targetID of [savedUser.ID, viewerID]) {
        const matching = audit.filter((row) => {
          bulkRequire(
            typeof row.eventArgs === "string" &&
              Buffer.byteLength(row.eventArgs, "utf8") <= 8192,
          );
          return bulkObject(JSON.parse(row.eventArgs)).targetId === targetID;
        });
        bulkRequire(matching.length === 1);
        const row = matching[0];
        bulkRequire(
          row.source === "auth" &&
            row.event === "user_role_assign" &&
            row.eventResult === "SUCCESS" &&
            row.deleted === false &&
            row.userId === actorID &&
            row.loginUser === username,
        );
        bulkRequire(
          typeof row.ID === "string" &&
            /^auth\.[1-9][0-9]{0,18}$/.test(row.ID) &&
            BigInt(row.ID.slice(5)) <= 9223372036854775807n,
        );
        const args = bulkObject(JSON.parse(row.eventArgs as string));
        bulkRequire(
          args.path === "/api/access/assignments" &&
            typeof args.requestId === "string" &&
            /^[A-Za-z0-9_-]{22}$/.test(args.requestId),
        );
        if (auditRequest) bulkRequire(auditRequest === args.requestId);
        auditRequest = args.requestId;
        events.push({ eventID: row.ID, targetID });
      }
      bulkRequire(events[0].eventID !== events[1].eventID);

      bulkPhase = "reader_relogin";
      await readerPage.reload();
      await login(readerPage, readerUsername, readerChanged);
      await expect(
        readerPage.getByLabel("二次认证验证码", { exact: true }),
      ).toBeVisible();
      await readerPage
        .getByLabel("二次认证验证码", { exact: true })
        .fill(await freshCode(readerMfa));
      await readerPage
        .getByRole("button", { name: "登录", exact: true })
        .click();
      await expect(
        readerPage.getByRole("heading", { name: "账户概览", exact: true }),
      ).toBeVisible();
      bulkPhase = "viewer_relogin";
      await viewerPage.reload();
      await login(viewerPage, viewerUsername, viewerChanged);
      await expect(
        viewerPage.getByRole("heading", { name: "账户概览", exact: true }),
      ).toBeVisible();

      bulkPhase = "effective_access";
      const readerNow = await bulkRead(readerContext, "/api/auth/me");
      const viewerNow = await bulkRead(viewerContext, "/api/auth/me");
      bulkIdentity(readerNow, savedUser.ID, readerUsername, true);
      bulkIdentity(viewerNow, viewerID, viewerUsername, false);
      const demotedMenu = bulkMenu(
        await bulkRead(readerContext, "/api/access/menu"),
      );
      const promotedMenu = bulkMenu(
        await bulkRead(viewerContext, "/api/access/menu"),
      );
      bulkRequire(bulkExact(demotedMenu, []));
      bulkRequire(
        bulkExact(promotedMenu, [
          { mark: "users", auth: { readable: true, writeable: false } },
        ]),
      );
      await bulkDenied(readerContext, "/api/access/users", 403);
      bulkRequire(
        Array.isArray(
          (await bulkRead(viewerContext, "/api/access/users")).List,
        ),
      );
      const readerChecks = await bulkRouteChecks(
        readerContext,
        readerNow,
        false,
      );
      const viewerChecks = await bulkRouteChecks(
        viewerContext,
        viewerNow,
        true,
      );
      await navigateTo(viewerPage, "访问管理");
      await expect(
        viewerPage.getByRole("button", { name: "用户管理", exact: true }),
      ).toBeVisible();
      for (const name of ["角色管理", "功能权限", "新增用户"])
        await expect(
          viewerPage.getByRole("button", { name, exact: true }),
        ).toHaveCount(0);
      for (const targetPage of [readerPage, viewerPage]) {
        bulkRequire(
          await targetPage.evaluate(
            () => localStorage.length === 0 && sessionStorage.length === 0,
          ),
        );
      }

      bulkPhase = "safe_screenshot";
      await page.getByLabel("搜索用户名", { exact: true }).fill("e2e.");
      await page.getByLabel("排序", { exact: true }).selectOption("1");
      await page.getByRole("button", { name: "应用筛选", exact: true }).click();
      await expect(rows).toHaveCount(2);
      await page.setViewportSize({ width: 1440, height: 900 });
      await table.scrollIntoViewIfNeeded();
      for (const [index, name, expectedRole] of [
        [0, readerUsername, "viewer"],
        [1, viewerUsername, roleName],
      ] as const) {
        const row = rows.nth(index);
        await expect(row.getByRole("rowheader")).toContainText(name);
        await expect(row.getByRole("cell").nth(1)).toContainText(expectedRole);
        await expect(row.getByRole("rowheader")).toBeInViewport({ ratio: 1 });
        await expect(row.getByRole("cell").nth(1)).toBeInViewport({ ratio: 1 });
      }
      await expect(
        page.locator('input[type="password"], input[name="totpCode"], .proof'),
      ).toHaveCount(0);
      await expect(page.getByRole("dialog")).toHaveCount(0);
      await expect(
        page.getByRole("heading", { name: "批量角色分配", exact: true }),
      ).toHaveCount(0);
      const screenshot = await page.screenshot({
        path: join(bulkOutput, "access-role-bulk-users-1440.png"),
        fullPage: false,
      });
      bulkRequire(
        bulkDigest(screenshot) ===
          bulkDigest(
            readFileSync(join(bulkOutput, "access-role-bulk-users-1440.png")),
          ),
      );
      bulkRequire(
        Number(createRequests) === 1 &&
          Number(assignmentRequests) === 2 &&
          requestCountValid,
      );
      bulkEvidence = {
        schemaVersion: 1,
        case: "access_role_bulk_atomic_rollback_and_swap",
        status: "browser_passed_owned_contexts_closed",
        identity: bulkRunIdentity,
        actorID,
        customRoleID: roleID,
        targets: [
          {
            target: "existing_reader",
            userID: savedUser.ID,
            beforeRole: "users_read_only",
            afterRejectedRole: "users_read_only",
            afterCommittedRole: "viewer",
            beforePriv: 2,
            afterRejectedPriv: 2,
            afterCommittedPriv: 3,
            profilePreserved: true,
            enabledPreserved: true,
          },
          {
            target: "new_viewer",
            userID: viewerID,
            beforeRole: "viewer",
            afterRejectedRole: "viewer",
            afterCommittedRole: "users_read_only",
            beforePriv: 3,
            afterRejectedPriv: 3,
            afterCommittedPriv: 2,
            profilePreserved: true,
            enabledPreserved: true,
          },
        ],
        rejectedBatch: {
          requestCount: 1,
          itemCount: 2,
          orderedTargets: ["existing_reader", "new_viewer"],
          orderedRoles: ["viewer", "missing_role"],
          httpStatus: 404,
          error: "not_found",
          auditDelta: 0,
        },
        committedBatch: {
          requestCount: 1,
          itemCount: 2,
          orderedTargets: ["existing_reader", "new_viewer"],
          orderedRoles: ["viewer", "users_read_only"],
          httpStatus: 200,
          result: "SUCCESS",
          actorSessionRevoked: false,
          auditDelta: 2,
        },
        targetSessions: [
          {
            target: "existing_reader",
            beforeHTTP: 200,
            afterRejectedHTTP: 200,
            afterCommittedHTTP: 401,
            reloginHTTP: 200,
            sameContextThroughRevocation: true,
            reloginAfterRevocation: true,
            sameIdentity: true,
            needChangePwd: false,
            hasMfa: true,
            reloginMenu: demotedMenu,
            usersHTTP: 403,
            routeChecks: readerChecks,
          },
          {
            target: "new_viewer",
            beforeHTTP: 200,
            afterRejectedHTTP: 200,
            afterCommittedHTTP: 401,
            reloginHTTP: 200,
            sameContextThroughRevocation: true,
            reloginAfterRevocation: true,
            sameIdentity: true,
            needChangePwd: false,
            hasMfa: false,
            reloginMenu: promotedMenu,
            usersHTTP: 200,
            routeChecks: viewerChecks,
          },
        ],
        audit: {
          beforeTotal: 0,
          afterRejectedTotal: 0,
          afterCommittedTotal: 2,
          exhausted: [true, true, true],
          actorID,
          events,
          sameRequest: true,
          requestIDSHA256: bulkDigest(auditRequest),
        },
        checks: {
          missingRoleHTTP: 404,
          missingRoleError: "not_found",
          filteredUserCount: 2,
          creationOrderAscending: true,
          adminSessionHTTP: 200,
          adminIdentityUnchanged: true,
          customRoleUserCount: 1,
          localStorageEmpty: true,
          sessionStorageEmpty: true,
          oldReaderAfterLogoutHTTP: 401,
        },
        proofBudget: {
          administratorBefore: 6,
          administratorAfter: 9,
          administratorLimit: 10,
          readerSensitive: 3,
          newViewerSensitive: 1,
          newAdministratorMutations: 3,
          assignmentRequests: 2,
        },
        screenshot: {
          file: "access-role-bulk-users-1440.png",
          bytes: screenshot.length,
          sha256: bulkDigest(screenshot),
          width: 1440,
          height: 900,
          viewport: { width: 1440, height: 900 },
          fullPage: false,
          proofControls: 0,
          visibleTargets: 2,
          finalRolesVerified: true,
        },
      };
    } catch {
      bulkFailed = true;
      throw new Error(`access_role_bulk_failed:${bulkPhase}`);
    } finally {
      page.off("request", countMutation);
      if (viewerContext) {
        try {
          await viewerContext.close();
        } catch {
          if (!bulkFailed)
            throw new Error("access_role_bulk_failed:new_context_cleanup");
        }
      }
    }
    // End AD-F-176 real two-user scenario; original logout and finally follow.

    await navigateTo(readerPage, "退出登录");
    await expect(
      readerPage.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    expect(
      (await readerContext.request.get("/api/access/users")).status(),
    ).toBe(401);
  } finally {
    await readerContext.close();
  }
  // AD-F-176 success is emitted only after both owned target contexts close.
  try {
    bulkRequire(bulkEvidence !== null);
    bulkEvidence.ownedContexts = { expected: 2, closed: 2 };
    bulkEvidence.sourceSHA256 = bulkSources();
    const output = Buffer.from(JSON.stringify(bulkEvidence) + "\n", "utf8");
    bulkRequire(output.length <= 65536);
    writeFileSync(join(bulkOutput, "access-role-bulk-evidence.json"), output, {
      flag: "wx",
    });
  } catch {
    throw new Error("access_role_bulk_failed:evidence_write");
  }
  // End AD-F-176 success writer.
});
