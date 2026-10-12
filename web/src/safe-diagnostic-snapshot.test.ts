import { afterEach, describe, expect, it } from "vitest";
import type { Page } from "@playwright/test";
import { safeSnapshot } from "../e2e/safe-request-diagnostics";
const canary = "CANARY-secret-source-name-password";
afterEach(() => {
  document.body.innerHTML = "";
  location.hash = "";
});
const page = {
  isClosed: () => false,
  evaluate: async (fn: () => unknown) => fn(),
} as unknown as Page;
describe("semantic snapshots never return DOM values", () => {
  it.each([
    ["正在读取已授权数据源…", "loading"],
    ["当前筛选下没有已授权数据源。", "empty"],
    [canary, "absent"],
  ])("recognizes only fixed source states", async (text, source) => {
    location.hash = "#user-assets-v2";
    document.body.innerHTML = `<section id="asset-source-picker"><p role="status">${text}</p><input value="${canary}" /></section><div role="alert">${canary}</div>`;
    const value = await safeSnapshot(page);
    expect(value).toMatchObject({
      view: "v2",
      picker: "hidden",
      source,
      alert: true,
    });
    expect(JSON.stringify(value)).not.toContain(canary);
  });
  it("recognizes visible picker, radio presence, permission denial and login without source labels", async () => {
    location.hash = "#user-assets-v3";
    document.body.innerHTML = `<section id="asset-source-picker"><input type="radio" value="${canary}" aria-label="${canary}" /></section><p role="status">当前账户无权选择已配置域。</p>`;
    document.getElementById("asset-source-picker")!.getClientRects = () =>
      ({ length: 1 }) as DOMRectList;
    expect(await safeSnapshot(page)).toMatchObject({
      view: "v3",
      picker: "visible",
      source: "radio-present",
      permission_denied: true,
    });
    document.body.innerHTML = `<h1>登录账户</h1><input value="${canary}" />`;
    expect(await safeSnapshot(page)).toMatchObject({
      view: "login",
      picker: "absent",
      source: "absent",
    });
    document.body.innerHTML = canary;
    location.hash = `#${canary}`;
    expect(await safeSnapshot(page)).toMatchObject({ view: "other" });
  });
});
