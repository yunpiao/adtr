import { expect, type Page, type TestInfo } from "@playwright/test";
import { createHash } from "node:crypto";
import { groupMembersV4ResponseWitness } from "./group-members-v4-response-observer";
import { navigateTo } from "./navigation";

const path = "/api/directory-assets/v4/raw-dn-references";
const title = "引用此 DN 原文的组";
const display = (raw: string) =>
  raw.replaceAll("\\", "\\\\").replace(/[\p{Cc}\p{Cf}\u2028\u2029]/gu, (c) => {
    const point = c.codePointAt(0)!;
    return point <= 0xffff
      ? `\\u${point.toString(16).padStart(4, "0")}`
      : `\\u{${point.toString(16)}}`;
  });
async function capture(
  page: Page,
  actorId: number,
  action: () => Promise<unknown>,
) {
  const pending = page.waitForResponse(
    (r) => new URL(r.url()).pathname === path && r.request().method() === "GET",
  );
  await action();
  const response = await pending;
  expect(response.status()).toBe(200);
  expect(response.headers()["cache-control"]).toBe("no-store");
  expect(response.headers()["x-adtr-user-id"]).toBe(String(actorId));
  const { value, identity, ...witness } =
    await groupMembersV4ResponseWitness(response);
  expect(identity.method).toBe("GET");
  expect(identity.url).toBe(response.url());
  return {
    query: [...new URL(response.url()).searchParams.entries()],
    value,
    witness: { ...witness, method: "GET", path },
  };
}

// Only real UI GETs from this explicit probe scope enter this evidence. Expected
// links are decoded independently from the already SHA-verified database body.
export async function captureRawDNReferences(
  page: Page,
  actorId: number,
  domainId: string,
  connection: { revision: string; credentialRevision: string },
  observationId: string,
  source: unknown,
  objects: any[],
  stored: any,
  info: TestInfo,
  reader = false,
) {
  const groups = stored.objects.filter((o: any) => o.base.kind === "group");
  const coverage = {
    groupCount: groups.length,
    memberReturnedGroupCount: groups.filter(
      (o: any) => o.membership.memberBytes !== null,
    ).length,
    memberNotReturnedGroupCount: groups.filter(
      (o: any) => o.membership.memberBytes === null,
    ).length,
  };
  expect(coverage).toEqual({
    groupCount: 12,
    memberReturnedGroupCount: 11,
    memberNotReturnedGroupCount: 1,
  });
  const writes: string[] = [];
  const listener = (request: import("@playwright/test").Request) => {
    const pathname = new URL(request.url()).pathname;
    if (
      pathname.startsWith("/api/") &&
      !["GET", "HEAD"].includes(request.method()) &&
      !(pathname === "/api/access/check" && request.method() === "POST")
    )
      writes.push(`${request.method()} ${pathname}`);
  };
  page.on("request", listener);
  const cases = [];
  const region = page.getByRole("region", { name: title, exact: true });
  try {
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      for (const index of [0, 13, 24]) {
        const target = objects[index];
        const pending = page.waitForResponse(
          (r) =>
            new URL(r.url()).pathname === "/api/directory/v4/observation" &&
            r.request().method() === "GET",
        );
        await page
          .getByLabel("对象类别", { exact: true })
          .selectOption(target.kind);
        const discovery = await pending;
        const discoveryWitness = await groupMembersV4ResponseWitness(discovery);
        expect(new URL(discovery.url()).searchParams.get("observationId")).toBe(
          observationId,
        );
        expect((discoveryWitness.value as any).observationId).toBe(
          observationId,
        );
        expect((discoveryWitness.value as any).source).toEqual(source);
        await expect(region).toHaveCount(0);
        const expectedGUIDs = groups
          .filter(
            (g: any) =>
              g.membership.memberBytes !== null &&
              g.membership.memberBytes.some(
                (b: string) =>
                  Buffer.from(b, "base64").toString("utf8") ===
                  stored.objects[index].base.distinguishedName,
              ),
          )
          .map((g: any) => g.base.objectGUID)
          .sort();
        expect(expectedGUIDs).toEqual(
          index === 24 ? [] : [objects[14].objectGUID],
        );
        const expectedList = expectedGUIDs.map((guid: string) =>
          objects.find((o) => o.objectGUID === guid),
        );
        const button = page
          .getByRole("list", {
            name: target.kind === "group" ? "本次观测的组" : "本次观测的对象",
            exact: true,
          })
          .getByRole("listitem")
          .filter({ hasText: target.objectGUID })
          .getByRole("button", { name: title, exact: true });
        const reply = await capture(page, actorId, () => button.click());
        const query = {
          domainId,
          expectedRevision: connection.revision,
          expectedCredentialRevision: connection.credentialRevision,
          observationId,
          objectGUID: target.objectGUID,
          pageIdx: "1",
          pageSize: "25",
        };
        expect(reply.query).toHaveLength(7);
        expect(Object.fromEntries(reply.query)).toEqual(query);
        expect(reply.value).toEqual({
          dictionaryVersion: 4,
          selection: {
            domainId,
            revision: connection.revision,
            credentialRevision: connection.credentialRevision,
          },
          observationId,
          source,
          object: target,
          list: expectedList,
          page: {
            pageIdx: 1,
            pageSize: 25,
            total: expectedList.length,
            totalPage: expectedList.length ? 1 : 0,
          },
          coverage,
        });
        await expect(region).toBeVisible();
        await expect(region).toContainText(target.objectGUID);
        await expect(region).toContainText(observationId);
        await expect(region).toContainText("member 未返回的组：1");
        const rendered = await region
          .getByRole("list", { name: "DN 原文引用组列表", exact: true })
          .getByRole("listitem")
          .allTextContents();
        expect(rendered).toEqual(
          expectedList.map(
            (g: any) => `${display(g.distinguishedName)} · ${g.objectGUID}`,
          ),
        );
        if (!expectedList.length)
          await expect(region).toContainText(
            "本观测未发现完全相同的 DN 文本引用",
          );
        await expect(region.locator("a, script")).toHaveCount(0);
        const reread = await capture(page, actorId, () =>
          page
            .getByRole("button", { name: "重读 DN 原文引用", exact: true })
            .click(),
        );
        expect(reread.query).toEqual(reply.query);
        expect(reread.value).toEqual(reply.value);
        await expect(region).toBeVisible();
        const replacementTarget = objects[index + 1];
        expect(
          groups.filter(
            (g: any) =>
              g.membership.memberBytes !== null &&
              g.membership.memberBytes.some(
                (b: string) =>
                  Buffer.from(b, "base64").toString("utf8") ===
                  stored.objects[index + 1].base.distinguishedName,
              ),
          ),
        ).toEqual([]);
        const replacementButton = page
          .getByRole("list", {
            name: target.kind === "group" ? "本次观测的组" : "本次观测的对象",
            exact: true,
          })
          .getByRole("listitem")
          .filter({ hasText: replacementTarget.objectGUID })
          .getByRole("button", { name: title, exact: true });
        const replacement = await capture(page, actorId, () =>
          replacementButton.click(),
        );
        expect(replacement.query).toHaveLength(7);
        expect(Object.fromEntries(replacement.query)).toEqual({
          ...query,
          objectGUID: replacementTarget.objectGUID,
        });
        expect(replacement.value).toEqual({
          ...(reply.value as any),
          object: replacementTarget,
          list: [],
          page: { pageIdx: 1, pageSize: 25, total: 0, totalPage: 0 },
        });
        await expect(region).toContainText(replacementTarget.objectGUID);
        await expect(region.getByRole("listitem")).toHaveCount(0);
        const restored = await capture(page, actorId, () => button.click());
        expect(restored.query).toEqual(reply.query);
        expect(restored.value).toEqual(reply.value);
        await expect(region).toContainText(target.objectGUID);
        expect(
          await region
            .getByRole("list", { name: "DN 原文引用组列表", exact: true })
            .getByRole("listitem")
            .allTextContents(),
        ).toEqual(rendered);
        await region.scrollIntoViewIfNeeded();
        expect(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= window.innerWidth,
          ),
        ).toBe(true);
        const filename = `group-members-v4-raw-dn-${reader ? "reader-" : ""}${viewport.width}-${index}.png`;
        const bytes = await page.screenshot({
          path: info.outputPath(filename),
          fullPage: false,
        });
        expect(bytes.readUInt32BE(16)).toBe(viewport.width);
        expect(bytes.readUInt32BE(20)).toBe(viewport.height);
        await page
          .getByRole("button", { name: "关闭 DN 原文引用", exact: true })
          .click();
        await expect(region).toHaveCount(0);
        const reopened = await capture(page, actorId, () => button.click());
        expect(reopened.query).toEqual(reply.query);
        expect(reopened.value).toEqual(reply.value);
        await expect(region).toBeVisible();
        // Leave it open so the next kind change (or final navigation) must clear it.
        cases.push({
          viewport,
          reply,
          reread,
          replacement,
          restored,
          reopened,
          rendered,
          closed: true,
          screenshot: {
            filename,
            sha256: createHash("sha256").update(bytes).digest("hex"),
          },
        });
      }
    }
    await navigateTo(page, "账户概览");
    await expect(region).toHaveCount(0);
    expect(writes).toEqual([]);
    return {
      actorId,
      authorization: reader
        ? "custom_readonly_role"
        : "platform_admin_after_v4_use_revoke",
      observationId,
      cases,
      writes,
      navigationCleared: true,
      logoutCleared: false,
    };
  } finally {
    page.off("request", listener);
  }
}
