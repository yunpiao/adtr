import { test, expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { setupComputerFixture } from "./computer-assets-v3-fixture";
import {
  openComputerList,
  computerResponse,
  expectedComputers,
} from "./computer-assets-v3-helpers";
import {
  observeDirectoryV2Responses,
  directoryV2ResponseJSON,
} from "./directory-v2-response-observer";
import { holdAssetResponse } from "./user-assets-v2-held-response";
import { installUserAssetAbortIsolation } from "./user-assets-v2-abort-isolation";
import {
  useNativeTabLifecycle,
  requireSameNativeWindow,
} from "./native-tab-lifecycle";
import { navigateTo } from "./navigation";
test.use({ trace: "off", screenshot: "off", video: "off" });
test("computer pinned readers reject genuinely delivered stale private facts", async ({
  page,
  context,
}, info) => {
  test.setTimeout(930000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await useNativeTabLifecycle(page);
  await observeDirectoryV2Responses(context);
  await context.addInitScript(installUserAssetAbortIsolation);
  const f = await setupComputerFixture(page, context),
    expected = expectedComputers(),
    transport: any[] = [];
  await computerResponse(page, () => openComputerList(page));
  const table = page.getByRole("table", {
    name: "计算机资产列表",
    exact: true,
  });
  const search = async (value: string) => {
    await page.getByLabel("计算机资产关键词", { exact: true }).fill(value);
    await page
      .getByRole("button", { name: "应用计算机筛选", exact: true })
      .click();
  };
  const held = await holdAssetResponse(
    page,
    "/api/computer-assets/v3",
    f.actorId,
    null,
  );
  await search("computer-02");
  try {
    await held.fetched();
    const newer = await computerResponse(page, () => search("computer-22"));
    expect(newer.value.list).toEqual([expected[21]]);
    const outcome = await held.release();
    expect(outcome.application).toBe("complete-consumed");
    transport.push({ scenario: "superseded-computer-query", ...outcome });
    await expect(table).toContainText("computer-22$");
    await expect(table).not.toContainText("computer-02$");
  } finally {
    await held.release();
  }
  const navigation = await holdAssetResponse(
    page,
    "/api/computer-assets/v3",
    f.actorId,
    null,
  );
  await search("computer-02");
  try {
    await navigation.fetched();
    await navigateTo(page, "账户概览");
    const outcome = await navigation.release();
    expect(outcome.application).toBe("complete-consumed");
    transport.push({ scenario: "navigate-away", ...outcome });
    await expect(table).toHaveCount(0);
    await page.goBack();
    await expect(
      page.getByRole("heading", { name: "计算机资产", exact: true }),
    ).toBeVisible();
    await expect(table).toHaveCount(0);
    await page.goForward();
    await expect(table).toHaveCount(0);
  } finally {
    await navigation.release();
  }
  await computerResponse(page, () => openComputerList(page));
  await page
    .getByRole("button", { name: "展开事实", exact: true })
    .first()
    .click();
  await expect(
    page.getByText("主组 RID（primaryGroupID）", { exact: true }),
  ).toBeVisible();
  const peer = await context.newPage();
  await useNativeTabLifecycle(peer);
  await peer.goto("/");
  await computerResponse(peer, () => openComputerList(peer));
  const late = await holdAssetResponse(
    peer,
    "/api/computer-assets/v3",
    f.actorId,
    null,
  );
  await peer
    .getByLabel("计算机资产关键词", { exact: true })
    .fill("computer-03");
  await peer
    .getByRole("button", { name: "应用计算机筛选", exact: true })
    .click();
  await late.fetched();
  await page.bringToFront();
  const sourceHeld = await holdAssetResponse(
    page,
    "/api/computer-assets/v3",
    f.actorId,
    null,
  );
  await search("computer-02");
  await sourceHeld.fetched();
  const changed = await f.post("/api/domains/update", {
    domainId: f.domainId,
    expectedRevision: f.beforeConnection.revision,
    dcHostName: "changed.synthetic.invalid",
    ldapAddr: process.env.ADTR_E2E_LDAP_IP!,
    port: "636",
  });
  expect(changed.result).toBe("SUCCESS");
  expect(changed.revision).not.toBe(f.beforeConnection.revision);
  try {
    const denied = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/computer-assets/v3" &&
        r.status() === 409,
    );
    await page
      .getByRole("button", { name: "刷新计算机资产观测", exact: true })
      .click();
    expect(await directoryV2ResponseJSON(await denied)).toEqual({
      error: "selection_changed",
    });
    await expect(table).toHaveCount(0);
    await expect(
      page.getByText("主组 RID（primaryGroupID）", { exact: true }),
    ).toHaveCount(0);
    const outcome = await sourceHeld.release();
    expect(outcome.application).toBe("complete-consumed");
    transport.push({ scenario: "source-revision-conflict", ...outcome });
    await expect(table).toHaveCount(0);
  } finally {
    await sourceHeld.release();
  }
  const switching = await context.newPage();
  await useNativeTabLifecycle(switching);
  await requireSameNativeWindow([peer, switching]);
  try {
    await switching.goto("/");
    await switching.bringToFront();
    await expect
      .poll(() => peer.evaluate(() => document.visibilityState))
      .toBe("hidden");
    await navigateTo(switching, "退出登录");
    await expect(
      switching.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    await peer.bringToFront();
    await expect
      .poll(() =>
        peer.evaluate(() => ({
          visible: document.visibilityState,
          focused: document.hasFocus(),
        })),
      )
      .toEqual({ visible: "visible", focused: true });
    await expect(
      peer.getByRole("heading", { name: "登录账户", exact: true }),
    ).toBeVisible();
    await expect(
      peer.getByRole("table", { name: "计算机资产列表", exact: true }),
    ).toHaveCount(0);
    const outcome = await late.release();
    expect(outcome.application).toBe("complete-consumed");
    transport.push({ scenario: "cross-tab-native-logout", ...outcome });
    await expect(
      peer.getByRole("table", { name: "计算机资产列表", exact: true }),
    ).toHaveCount(0);
    await peer.screenshot({
      path: info.outputPath("computer-assets-v3-revoked.png"),
      fullPage: true,
    });
  } finally {
    await late.release();
    await peer.close();
    await switching.close();
  }
  expect(transport).toHaveLength(4);
  await writeFile(
    info.outputPath("computer-assets-v3-reader-evidence.json"),
    JSON.stringify(
      {
        browser: context.browser()?.version(),
        transport,
        sourceObservation: f.taskUUID,
        scope:
          "synthetic registered Worker / TLS LDAP / PG / browser; late original bytes fully consumed with independent native receipts",
      },
      null,
      2,
    ),
  );
});
