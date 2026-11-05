import { expect, type Page, type Request } from "@playwright/test";
import { createHash } from "node:crypto";
import { lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Independent browser consumer: no production parser, mocked response, fixture
// insert, new context, login, proof, or API mutation is used by this scenario.
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const output = join(root, "web/test-results/role-users-read");
const sourcePaths = [
  // ROLE-USERS-SOURCES-BEGIN
  ".github/workflows/ci.yml",
  "AGENTS.md",
  "README.md",
  "cmd/adtr/main.go",
  "docs/access-contract.md",
  "docs/architecture.md",
  "docs/delivery.md",
  "docs/task-contract.md",
  "internal/auth/access_http.go",
  "internal/auth/access_roles.go",
  "internal/auth/access_types.go",
  "internal/auth/access_users.go",
  "scripts/ci_workflow_contract.py",
  "scripts/e2e_runtime_diagnostics.py",
  "scripts/fixtures/role_users_read_ci_delta.json",
  "scripts/role_users_read_source_contract.py",
  "scripts/role_users_read_workflow.py",
  "scripts/test_access_role_bulk_contract.py",
  "scripts/test_auth_e2e.py",
  "scripts/test_role_users_read_contract.py",
  "scripts/test_role_users_read_workflow_contract.py",
  "scripts/test_user_asset_export_v3_uac_runner_contract.py",
  "scripts/validate_access_role_bulk_evidence.py",
  "scripts/validate_role_users_read_evidence.py",
  "web/e2e/access.spec.ts",
  "web/e2e/navigation.ts",
  "web/e2e/role-users-read.ts",
  "web/package.json",
  "web/playwright.config.ts",
  "web/src/Access.test.tsx",
  "web/src/AccessRoles.tsx",
  "web/src/AccessWorkspace.tsx",
  "web/src/App.tsx",
  "web/src/RoleUsersReadPanel.test.tsx",
  "web/src/RoleUsersReadPanel.tsx",
  "web/src/SocNavigation.tsx",
  "web/src/access-api.ts",
  "web/src/access-common.tsx",
  "web/src/api.ts",
  "web/src/role-users-read.css",
  "web/src/role-users-read.test.ts",
  "web/src/role-users-read.ts",
  "web/src/session-invalidation.ts",
  "web/src/soc-shell.css",
  "web/src/style.css",
  // ROLE-USERS-SOURCES-END
] as const;
// ROLE-USERS-LABEL-CONTENT-BEGIN
// CSSOM may preserve the two content tokens or serialize their exact join.
const roleUsersLabelContent = [
  "当前角色",
  "手机号",
  "邮箱",
  "账户状态",
  "MFA 状态",
].map((label) => ({
  label,
  content: [
    JSON.stringify(label + "："),
    JSON.stringify(label) + " " + JSON.stringify("："),
  ],
}));
// ROLE-USERS-LABEL-CONTENT-END
type ObjectValue = Record<string, unknown>;
type ExpectedUser = {
  username: "e2e.bulk-viewer" | "e2e.viewer";
  roleID: string;
  roleName: string;
  mobile: string;
  email: string;
  hasMfa: boolean;
  ID?: number;
};
type Identity = {
  repository: string;
  suite: string;
  runID: string;
  runAttempt: string;
  event: string;
  testedCommit: string;
};
const requireValue: (value: unknown) => asserts value = (value) => {
  if (!value) throw new Error("role_users_read_check_failed");
};
const object = (value: unknown): ObjectValue => {
  requireValue(
    value !== null && typeof value === "object" && !Array.isArray(value),
  );
  return value as ObjectValue;
};
const digest = (raw: Buffer) => createHash("sha256").update(raw).digest("hex");
const exactKeys = (value: ObjectValue, keys: string[]) => {
  requireValue(
    Object.keys(value).sort().join("\n") === [...keys].sort().join("\n"),
  );
};
const safeID = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value > 0;
const sourceHashes = () =>
  Object.fromEntries(
    sourcePaths.map((path) => {
      const target = join(root, path),
        info = lstatSync(target);
      requireValue(
        info.isFile() &&
          !info.isSymbolicLink() &&
          info.size > 0 &&
          info.size <= 2 * 1024 * 1024,
      );
      return [path, digest(readFileSync(target))];
    }),
  );

export async function verifyRoleUsersRead(
  page: Page,
  roleID: string,
  roleName: string,
  readerID: number,
  identity: Identity,
) {
  let phase = "guard",
    mutations = 0,
    checkPOSTs = 0,
    userRequests = 0;
  const requests: ObjectValue[] = [],
    screenshots: ObjectValue[] = [];
  const observer = (request: Request) => {
    const path = new URL(request.url()).pathname;
    if (path === "/api/access/users") userRequests++;
    if (!["GET", "HEAD"].includes(request.method())) {
      if (path === "/api/access/check" && request.method() === "POST")
        checkPOSTs++;
      else mutations++;
    }
  };
  page.on("request", observer);
  try {
    const base = new URL(process.env.ADTR_E2E_BASE_URL ?? "");
    requireValue(
      base.protocol === "http:" &&
        ["127.0.0.1", "[::1]"].includes(base.hostname) &&
        base.port !== "" &&
        base.username === "" &&
        base.password === "" &&
        base.pathname === "/" &&
        base.search === "" &&
        base.hash === "" &&
        process.env.ADTR_E2E_USERNAME === "e2e-admin",
    );
    requireValue(
      /^[A-Za-z0-9_-]{24}$/.test(roleID) &&
        roleName === "E2E Users Read Only" &&
        safeID(readerID),
    );
    for (const path of [root, join(root, "web"), dirname(output)]) {
      const info = lstatSync(path);
      requireValue(info.isDirectory() && !info.isSymbolicLink());
    }
    mkdirSync(output); // An old evidence directory is never reused.
    const panel = page.getByRole("region", {
      name: "角色关联用户工作区",
      exact: true,
    });
    const table = panel.getByRole("table", {
      name: "角色关联用户列表",
      exact: true,
    });
    const custom: ExpectedUser = {
      username: "e2e.bulk-viewer",
      roleID,
      roleName,
      mobile: "",
      email: "",
      hasMfa: false,
    };
    const viewer: ExpectedUser = {
      username: "e2e.viewer",
      roleID: "viewer",
      roleName: "viewer",
      mobile: "13800000000",
      email: "e2e.viewer@example.invalid",
      hasMfa: true,
      ID: readerID,
    };
    let customID = 0;
    async function read(
      name: string,
      target: ExpectedUser,
      search: string,
      size: number,
      count: 0 | 1,
      action: () => Promise<unknown>,
    ) {
      phase = name;
      const before = userRequests;
      // First actual matching request and its own response. Wait/action failures
      // are joined; there is one action, no route interception or retry.
      const pending = page.waitForRequest(
        (request) => new URL(request.url()).pathname === "/api/access/users",
        { timeout: 15_000 },
      );
      const settled = await Promise.allSettled([pending, action()]);
      requireValue(
        settled[0].status === "fulfilled" && settled[1].status === "fulfilled",
      );
      const request = settled[0].value as Request,
        url = new URL(request.url());
      requireValue(
        request.method() === "GET" &&
          url.origin === base.origin &&
          request.postData() === null,
      );
      const expected = {
        roleID: target.roleID,
        pageIdx: "1",
        pageSize: String(size),
        sort: "-1",
        ...(search ? { search } : {}),
      };
      const entries = [...url.searchParams.entries()].sort(([a], [b]) =>
        a.localeCompare(b),
      );
      requireValue(
        JSON.stringify(entries) ===
          JSON.stringify(
            Object.entries(expected).sort(([a], [b]) => a.localeCompare(b)),
          ),
      );
      const response = await request.response();
      requireValue(response !== null && response.status() === 200);
      const body = object(await response.json()),
        pageInfo = object(body.page);
      exactKeys(body, ["page", "List", "exhausted"]);
      exactKeys(pageInfo, ["pageIdx", "pageSize", "total", "totalPage"]);
      requireValue(
        pageInfo.pageIdx === 1 &&
          pageInfo.pageSize === size &&
          pageInfo.total === count &&
          pageInfo.totalPage === count &&
          body.exhausted === true,
      );
      requireValue(Array.isArray(body.List) && body.List.length === count);
      await expect(
        panel.getByRole("heading", {
          name: `角色关联用户：${target.roleName}`,
          exact: true,
        }),
      ).toBeVisible();
      await expect(table).toBeVisible();
      const rows = table.locator("tbody tr");
      await expect(rows).toHaveCount(count);
      let id: number | null = null;
      if (count) {
        const user = object(body.List[0]);
        requireValue(
          safeID(user.ID) &&
            user.username === target.username &&
            user.roleID === target.roleID &&
            user.roleName === target.roleName &&
            user.mobile === target.mobile &&
            user.email === target.email &&
            user.hasMfa === target.hasMfa &&
            user.disabled === false,
        );
        requireValue(
          user.role === "viewer" &&
            user.priv === (target.roleID === "viewer" ? 3 : 2),
        );
        if (target.ID) requireValue(user.ID === target.ID);
        else if (customID) requireValue(user.ID === customID);
        else {
          requireValue(user.ID !== readerID);
          customID = user.ID;
        }
        id = user.ID;
        const row = rows.first();
        await expect(row.getByRole("rowheader")).toHaveText(
          `${target.username}ID ${id}`,
        );
        const cells = row.getByRole("cell");
        await expect(cells).toHaveCount(5);
        await expect(cells.nth(0)).toHaveText(
          `${target.roleName}${target.roleID}`,
        );
        await expect(cells.nth(1)).toHaveText(target.mobile || "未提供");
        await expect(cells.nth(2)).toHaveText(target.email || "未提供");
        await expect(cells.nth(3)).toHaveText("账户已启用");
        await expect(cells.nth(4)).toHaveText(
          target.hasMfa ? "已开启" : "已关闭",
        );
      } else
        await expect(panel.getByRole("status")).toHaveText(
          "当前页没有符合条件的关联用户。空结果不证明角色仍存在。",
        );
      await expect(
        panel.getByRole("button", { name: "上一页", exact: true }),
      ).toBeDisabled();
      await expect(
        panel.getByRole("button", { name: "下一页", exact: true }),
      ).toBeDisabled();
      await expect(panel.getByLabel("分页", { exact: true })).toContainText(
        `共 ${count} 条 · 第 1 / 1 页`,
      );
      requireValue(userRequests === before + 1 && mutations === 0);
      requests.push({
        phase: name,
        role: target.roleID === "viewer" ? "viewer" : "users_read_only",
        method: "GET",
        status: 200,
        roleBound: true,
        exactQuery: true,
        pageIdx: 1,
        pageSize: size,
        searchKind:
          search === ""
            ? "empty"
            : search === "E2E.BULK-VIEWER"
              ? "case_insensitive"
              : search === "%"
                ? "literal_percent"
                : "unmatched",
        total: count,
        totalPage: count,
        exhausted: true,
        responseUsers: count,
        visibleUsers: count,
        userID: id,
        rowFieldsMatch: true,
        previousDisabled: true,
        nextDisabled: true,
      });
    }
    const button = (name: string) => () =>
      panel.getByRole("button", { name, exact: true }).click();
    phase = "served_document_policy";
    // Only this same-origin document GET uses the request context. User rows
    // still come exclusively from the actual UI request and its own response.
    const requestContext = page.context().request;
    const documentResponse = await requestContext.get(base.origin + "/", {
      maxRedirects: 0,
    });
    try {
      const headers = documentResponse.headers();
      requireValue(
        documentResponse.status() === 200 &&
          documentResponse.url() === base.origin + "/" &&
          headers["content-type"]?.split(";")[0] === "text/html" &&
          headers["content-security-policy"] ===
            "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
      );
    } finally {
      await documentResponse.dispose();
    }
    phase = "open_role_list";
    await page.getByRole("button", { name: "角色管理", exact: true }).click();
    await read("custom_initial", custom, "", 20, 1, () =>
      page
        .getByRole("button", { name: `查看关联用户 ${roleName}`, exact: true })
        .click(),
    );
    await panel
      .getByLabel("关联用户名搜索", { exact: true })
      .fill("E2E.BULK-VIEWER");
    await read(
      "custom_search",
      custom,
      "E2E.BULK-VIEWER",
      20,
      1,
      button("搜索关联用户"),
    );
    await panel.getByLabel("关联用户名搜索", { exact: true }).fill("%");
    await read("custom_literal", custom, "%", 20, 0, button("搜索关联用户"));
    await read("custom_clear", custom, "", 20, 1, button("清除关联用户搜索"));
    await expect(
      panel.getByLabel("关联用户名搜索", { exact: true }),
    ).toHaveValue("");
    await panel
      .getByLabel("关联用户名搜索", { exact: true })
      .fill("no-such-role-user");
    await read(
      "custom_empty",
      custom,
      "no-such-role-user",
      20,
      0,
      button("搜索关联用户"),
    );
    await read(
      "custom_refresh_empty",
      custom,
      "no-such-role-user",
      20,
      0,
      button("刷新关联用户"),
    );
    await read("custom_reset", custom, "", 20, 1, button("清除关联用户搜索"));
    const sizes = panel.getByLabel("关联用户每页条数", { exact: true });
    requireValue(
      JSON.stringify(await sizes.locator("option").allTextContents()) ===
        JSON.stringify(["10", "20", "30", "40", "50"]),
    );
    await sizes.selectOption("10");
    await read("custom_page_size", custom, "", 10, 1, button("搜索关联用户"));
    await read("custom_refresh", custom, "", 10, 1, button("刷新关联用户"));
    phase = "close_list";
    await panel
      .getByRole("button", { name: "关闭关联用户", exact: true })
      .click();
    await expect(panel).toHaveCount(0);
    await expect(
      page.getByRole("table", { name: "角色列表", exact: true }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: `详情 ${roleName}`, exact: true })
      .click();
    await read("custom_detail_reentry", custom, "", 20, 1, () =>
      page
        .getByRole("button", { name: `查看关联用户 ${roleName}`, exact: true })
        .click(),
    );
    phase = "external_stylesheet";
    const externalStyle = await page.evaluate(() => {
      const hasRoleRule = (rules: CSSRuleList): boolean =>
        Array.from(rules).some((rule) =>
          rule instanceof CSSStyleRule
            ? rule.selectorText.includes(".role-users-read")
            : "cssRules" in rule &&
              hasRoleRule((rule as CSSGroupingRule).cssRules),
        );
      return Array.from(document.styleSheets).some((sheet) => {
        if (
          !sheet.href ||
          new URL(sheet.href).origin !== location.origin ||
          !(sheet.ownerNode instanceof HTMLLinkElement) ||
          sheet.ownerNode.rel !== "stylesheet"
        )
          return false;
        try {
          return hasRoleRule(sheet.cssRules);
        } catch {
          return false;
        }
      });
    });
    requireValue(externalStyle);
    await expect(panel.locator("style")).toHaveCount(0);
    await expect(panel.locator("[style]")).toHaveCount(0);
    await expect(panel).not.toHaveAttribute("style");
    const styleDelivery = {
      documentHTTP: 200,
      documentSameOrigin: true,
      documentHTML: true,
      documentCSPExact: true,
      stylesheetSameOrigin: true,
      stylesheetRulePresent: true,
      inlineStyleElements: 0,
      inlineStyleAttributes: 0,
    };
    // Both images show this new panel, never the legacy bulk-assignment image.
    for (const [width, height] of [
      [1440, 1000],
      [390, 1200],
    ]) {
      phase = `safe_screenshot_${width}`;
      await page.setViewportSize({ width, height });
      await panel.scrollIntoViewIfNeeded();
      await expect(
        panel.getByRole("heading", {
          name: `角色关联用户：${roleName}`,
          exact: true,
        }),
      ).toBeInViewport({ ratio: 1 });
      await expect(table.getByRole("rowheader")).toBeInViewport({ ratio: 1 });
      await expect(
        table.locator("tbody tr").getByRole("cell").first(),
      ).toBeInViewport({ ratio: 1 });
      // Visibility of the first two columns is insufficient: all six fields
      // and their rendered content must fit the real screenshot viewport.
      const fields = table.locator("tbody tr").locator("th, td");
      await expect(fields).toHaveCount(6);
      for (let index = 0; index < 6; index++) {
        await expect(fields.nth(index)).toBeVisible();
        await expect(fields.nth(index)).toBeInViewport({ ratio: 1 });
      }
      const layout = await table.evaluate((element, labelContent) => {
        const body = element.querySelector("tbody"),
          row = body?.querySelector("tr"),
          head = element.querySelector("thead");
        if (!body || !row || !head)
          throw new Error("role_users_layout_missing");
        const fields = Array.from(row.querySelectorAll<HTMLElement>("th, td")),
          mobile = innerWidth === 390,
          headStyle = getComputedStyle(head),
          headBox = head.getBoundingClientRect();
        const headersVisuallyHidden =
          headStyle.position === "absolute" &&
          headBox.width <= 1 &&
          headBox.height <= 1 &&
          headStyle.overflow === "hidden" &&
          headStyle.clipPath === "inset(50%)";
        const display = mobile ? "block" : "table-cell";
        const cellsComputedDisplay =
          fields.length === 6 &&
          fields.every((field) => getComputedStyle(field).display === display);
        const cellsWithinViewport = fields.every((field) => {
          const box = field.getBoundingClientRect();
          return (
            box.width > 0 &&
            box.height > 0 &&
            box.left >= 0 &&
            box.top >= 0 &&
            box.right <= innerWidth &&
            box.bottom <= innerHeight
          );
        });
        const contentFits = fields.every(
          (field) =>
            field.scrollWidth <= field.clientWidth + 1 &&
            field.scrollHeight <= field.clientHeight + 1,
        );
        const scroll = element.parentElement;
        const horizontalOverflow =
          !scroll ||
          scroll.scrollWidth > scroll.clientWidth + 1 ||
          document.documentElement.scrollWidth > innerWidth + 1;
        const mobileFieldLabels = fields
          .slice(1)
          .every(
            (field, index) =>
              field.getAttribute("data-label") === labelContent[index].label &&
              labelContent[index].content.includes(
                getComputedStyle(field, "::before").content,
              ),
          );
        return {
          mode: mobile ? "stacked" : "table",
          tableDisplay: getComputedStyle(element).display,
          bodyDisplay: getComputedStyle(body).display,
          rowDisplay: getComputedStyle(row).display,
          cellDisplay: display,
          cellsComputedDisplay,
          cellsWithinViewport,
          contentFits,
          horizontalOverflow,
          headersVisuallyHidden,
          mobileFieldLabels,
          visibleFields: fields.length,
        };
      }, roleUsersLabelContent);
      requireValue(
        layout.mode === (width === 390 ? "stacked" : "table") &&
          layout.tableDisplay === (width === 390 ? "block" : "table") &&
          layout.bodyDisplay ===
            (width === 390 ? "block" : "table-row-group") &&
          layout.rowDisplay === (width === 390 ? "block" : "table-row") &&
          layout.cellsComputedDisplay &&
          layout.cellsWithinViewport &&
          layout.contentFits &&
          !layout.horizontalOverflow &&
          layout.headersVisuallyHidden === (width === 390) &&
          layout.mobileFieldLabels === (width === 390) &&
          layout.visibleFields === 6,
      );
      await expect(
        page.locator('input[type="password"], input[name="totpCode"], .proof'),
      ).toHaveCount(0);
      await expect(page.getByRole("dialog")).toHaveCount(0);
      await expect(panel.locator("form")).toHaveCount(0);
      const file = `role-users-read-${width}.png`,
        png = await page.screenshot({
          path: join(output, file),
          fullPage: false,
        });
      requireValue(digest(png) === digest(readFileSync(join(output, file))));
      screenshots.push({
        file,
        bytes: png.length,
        sha256: digest(png),
        width,
        height,
        viewport: { width, height },
        fullPage: false,
        proofControls: 0,
        visibleUsers: 1,
        headingVisible: true,
        usernameVisible: true,
        roleVisible: true,
        layout,
      });
    }
    phase = "return_detail";
    await page.setViewportSize({ width: 1440, height: 900 });
    await panel
      .getByRole("button", { name: "返回角色详情", exact: true })
      .click();
    await expect(panel).toHaveCount(0);
    await expect(
      page.getByRole("button", {
        name: `查看关联用户 ${roleName}`,
        exact: true,
      }),
    ).toBeVisible();
    await page.getByRole("button", { name: "取消", exact: true }).click();
    await read("viewer_initial", viewer, "", 20, 1, () =>
      page
        .getByRole("button", { name: "查看关联用户 viewer", exact: true })
        .click(),
    );
    phase = "return_list";
    await panel
      .getByRole("button", { name: "返回角色列表", exact: true })
      .click();
    await expect(panel).toHaveCount(0);
    await expect(
      page.getByRole("table", { name: "角色列表", exact: true }),
    ).toBeVisible();
    requireValue(
      mutations === 0 && userRequests === requests.length && checkPOSTs <= 2,
    );
    requireValue(
      await page.evaluate(
        () => localStorage.length === 0 && sessionStorage.length === 0,
      ),
    );
    phase = "write_evidence";
    const summary = {
      schemaVersion: 2,
      case: "role_users_readonly_workspace",
      status: "browser_passed_after_legacy_context_cleanup",
      identity,
      customRoleID: roleID,
      readerID,
      customUserID: customID,
      requests,
      screenshots,
      styleDelivery,
      checks: {
        mutatingRequests: mutations,
        readOnlyCheckPOSTs: checkPOSTs,
        userRequests,
        listClosed: true,
        detailReturned: true,
        viewerSelected: true,
        finalListVisible: true,
        localStorageEmpty: true,
        sessionStorageEmpty: true,
        extraSensitiveMutations: 0,
        newContexts: 0,
        pageNavigationBeyondSinglePage: "not_exercised_one_user_per_role",
      },
      sourceSHA256: sourceHashes(),
    };
    const encoded = Buffer.from(JSON.stringify(summary) + "\n");
    requireValue(encoded.length <= 65536);
    writeFileSync(join(output, "role-users-read-evidence.json"), encoded, {
      flag: "wx",
    });
  } catch {
    throw new Error(`role_users_read_failed:${phase}`);
  } finally {
    page.off("request", observer);
  }
}
