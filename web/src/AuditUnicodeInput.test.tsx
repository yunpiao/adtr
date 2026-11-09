// Synthetic DOM/HTTP fixtures, not real browser/PG or production acceptance.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { StrictMode } from "react";
import AuditWorkspace from "./AuditWorkspace";
import type { Profile } from "./api";
import { auditOperations, type AuditRow } from "./audit-api";
const profile: Profile = {
  ID: 307,
  username: "unicode-synthetic",
  role: "platform_admin",
  priv: 1,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: true,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 0,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "synthetic-csrf",
};
let deleted = false;
const row = (): AuditRow => ({
  ID: "auth.1",
  loginUser: "synthetic-user",
  sourceIp: "192.0.2.10",
  event: "login",
  eventArgs: '{"synthetic":true}',
  eventResult: "SUCCESS",
  CreateTm: "2026-10-07T00:00:00Z",
  logType: 9,
  logTypeName: "系统设置",
  userId: 307,
  source: "auth",
  domainId: null,
  availability: {
    loginUser: true,
    sourceIp: true,
    path: true,
    requestId: true,
    eventResult: true,
  },
  deleted,
  deletable: true,
  visibilityVersion: 0,
});
const calls = (path = "") =>
  vi
    .mocked(fetch)
    .mock.calls.filter(
      ([url]) => String(url).split("?")[0] === `/api/audit${path}`,
    );
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const input = () =>
  screen.getByLabelText("审计关键词", { exact: true }) as HTMLInputElement;
const user = () => userEvent.setup({ delay: null });
async function paste(label: string, text: string) {
  const field = screen.getByLabelText(label, { exact: true });
  await user().click(field);
  await user().paste(text);
  return field;
}
async function mount(strict = false) {
  await act(async () =>
    render(
      strict ? (
        <StrictMode>
          <AuditWorkspace profile={profile} sessionChanged={vi.fn()} />
        </StrictMode>
      ) : (
        <AuditWorkspace profile={profile} sessionChanged={vi.fn()} />
      ),
    ),
  );
  await screen.findByRole("table", { name: "操作审计记录" });
}
async function reasonForm(action: "delete" | "restore") {
  deleted = action === "restore";
  await mount();
  fireEvent.click(screen.getByLabelText("选择 auth.1"));
  click(action === "delete" ? "隐藏所选记录" : "恢复所选记录");
  fireEvent.change(screen.getByLabelText("操作者当前密码"), {
    target: { value: "Synthetic Password 123" },
  });
  fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), {
    target: { value: "123456" },
  });
}
beforeEach(() => {
  deleted = false;
  sessionStorage.clear();
  localStorage.clear();
  history.replaceState({}, "", "#audit");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const path = String(url).split("?")[0];
      let body: unknown;
      if (path === "/api/access/menu")
        body = {
          menu: [{ mark: "audit", auth: { readable: true, writeable: true } }],
        };
      else if (path === "/api/access/check")
        body = { results: auditOperations.map(() => true) };
      else if (path === "/api/audit")
        body = {
          page: { pageIdx: 1, pageSize: 20, total: 1, totalPage: 1 },
          List: [row()],
          exhausted: true,
        };
      else if (path === "/api/audit/types")
        body = {
          events: ["login"],
          List: [
            { logType: 9, logTypeName: "系统设置", eventNameList: ["login"] },
          ],
        };
      else if (path === "/api/audit/exports/history")
        body = {
          page: { pageIdx: 1, pageSize: 20, total: 0, totalPage: 0 },
          list: [],
          exhausted: true,
        };
      else if (path === "/api/audit/delete" || path === "/api/audit/restore") {
        deleted = path.endsWith("delete");
        body = { result: "success", changed: 1, visibilityRevision: 1 };
      } else throw Error(`unexpected fixture path ${path}`);
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "X-ADTR-User-ID": String(profile.ID),
        },
      });
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it.each([26, 50])(
  "types %i supplementary keyword characters intact, without a different search",
  async (count) => {
    await mount();
    const text = "😀".repeat(count);
    await user().type(input(), text);
    expect(input()).toHaveValue(text);
    expect(Array.from(input().value)).toHaveLength(count);
    await act(async () => click("筛选审计"));
    expect(
      new URL(String(calls().at(-1)![0]), "http://synthetic").searchParams.get(
        "keyword",
      ),
    ).toBe(text);
  },
);
it.each([
  ["ASCII", "a".repeat(50)],
  ["astral", "𠀀".repeat(50)],
  ["mixed", "a".repeat(25) + "😀".repeat(25)],
  ["combining", "e\u0301".repeat(25)],
  ["ZWJ", "👩‍💻".repeat(16)],
  ["literal", " %_\\" + "😀".repeat(44) + "  "],
])(
  "pastes a legal %s keyword without trimming, normalization or truncation",
  async (_name, text) => {
    await mount();
    expect(Array.from(text).length).toBeLessThanOrEqual(50);
    await paste("审计关键词", text);
    expect(input()).toHaveValue(text);
    await act(async () => click("筛选审计"));
    expect(
      new URL(String(calls().at(-1)![0]), "http://synthetic").searchParams.get(
        "keyword",
      ),
    ).toBe(text);
  },
);
it.each(["a".repeat(51), "😀".repeat(51), "e\u0301".repeat(26)])(
  "rejects oversized pasted keyword without silently converting it into a valid search (%#)",
  async (text) => {
    await mount();
    await paste("审计关键词", text);
    expect(input()).toHaveValue(text);
    const before = calls().length;
    click("筛选审计");
    expect(screen.getByRole("alert")).toHaveTextContent("关键词最多 50");
    expect(calls()).toHaveLength(before);
  },
);
it.each(["delete", "restore"] as const)(
  "preserves a 500-code-point supplementary %s reason in the actual request",
  async (action) => {
    await reasonForm(action);
    const text = "𠀀".repeat(500);
    expect(await paste("操作原因", text)).toHaveValue(text);
    await act(async () => click(action === "delete" ? "确认隐藏" : "确认恢复"));
    expect(calls(`/${action}`)).toHaveLength(1);
    expect(JSON.parse(String(calls(`/${action}`)[0][1]?.body))).toMatchObject({
      id: ["auth.1"],
      reason: text,
      actorPassword: "Synthetic Password 123",
      totpCode: "123456",
    });
  },
);
it.each(["a".repeat(501), "😀".repeat(501), "e\u0301".repeat(251)])(
  "rejects an oversized reason without a truncated mutation (%#)",
  async (text) => {
    await reasonForm("delete");
    expect(await paste("操作原因", text)).toHaveValue(text);
    click("确认隐藏");
    expect(screen.getByRole("alert")).toHaveTextContent("1 至 500");
    expect(calls("/delete")).toHaveLength(0);
  },
);
it("clears a supplementary draft locally, then applies the empty keyword without changing unrelated controls", async () => {
  await mount(true);
  await user().type(input(), "😀".repeat(26));
  fireEvent.change(screen.getByLabelText("开始时间（UTC）"), {
    target: { value: "2026-10-01T00:00:00" },
  });
  const before = calls().length;
  click("清空审计关键词");
  expect(input()).toHaveValue("");
  expect(input()).toHaveFocus();
  expect(calls()).toHaveLength(before);
  await act(async () => click("筛选审计"));
  const query = new URL(String(calls().at(-1)![0]), "http://synthetic")
    .searchParams;
  expect(query.get("keyword")).toBeNull();
  expect(query.get("startTm")).toBe("2026-10-01T00:00:00.000Z");
});
it("replaces a selected BMP draft with a full supplementary keyword without losing either boundary", async () => {
  await mount();
  const u = user();
  await u.type(input(), "a".repeat(49));
  input().select();
  await u.paste("😀".repeat(50));
  expect(input()).toHaveValue("😀".repeat(50));
  await act(async () => click("筛选审计"));
  expect(
    new URL(String(calls().at(-1)![0]), "http://synthetic").searchParams.get(
      "keyword",
    ),
  ).toBe("😀".repeat(50));
});
it("keeps the last applied query while an oversized draft is rejected, then accepts its corrected value", async () => {
  await mount();
  const u = user();
  await u.type(input(), "old-applied");
  await act(async () => click("筛选审计"));
  const before = calls().length;
  await u.clear(input());
  await u.paste("😀".repeat(51));
  click("筛选审计");
  expect(input()).toHaveValue("😀".repeat(51));
  expect(calls()).toHaveLength(before);
  expect(
    new URL(String(calls().at(-1)![0]), "http://synthetic").searchParams.get(
      "keyword",
    ),
  ).toBe("old-applied");
  await u.clear(input());
  await u.paste("😀".repeat(50));
  await act(async () => click("筛选审计"));
  expect(calls()).toHaveLength(before + 1);
  expect(
    new URL(String(calls().at(-1)![0]), "http://synthetic").searchParams.get(
      "keyword",
    ),
  ).toBe("😀".repeat(50));
});
it("simulated composition completion preserves the whole keyword and never queries before submit", async () => {
  await mount();
  const before = calls().length;
  const text = "𠀀".repeat(50);
  fireEvent.compositionStart(input());
  fireEvent.input(input(), {
    target: { value: text },
    inputType: "insertCompositionText",
    isComposing: true,
  });
  fireEvent.compositionEnd(input(), { data: text });
  expect(input()).toHaveValue(text);
  expect(calls()).toHaveLength(before);
  await act(async () => click("筛选审计"));
  expect(
    new URL(String(calls().at(-1)![0]), "http://synthetic").searchParams.get(
      "keyword",
    ),
  ).toBe(text);
});
it.each([
  "a".repeat(500),
  "a".repeat(250) + "😀".repeat(250),
  "e\u0301".repeat(250),
])(
  "accepts exactly 500 code points of a reason without normalization (%#)",
  async (text) => {
    await reasonForm("delete");
    expect(Array.from(text)).toHaveLength(500);
    expect(await paste("操作原因", text)).toHaveValue(text);
    await act(async () => click("确认隐藏"));
    expect(calls("/delete")).toHaveLength(1);
    expect(JSON.parse(String(calls("/delete")[0][1]?.body)).reason).toBe(text);
  },
);
it("rejects a reason draft, then submits its full corrected value once with the existing proof requirements", async () => {
  await reasonForm("delete");
  const field = await paste("操作原因", "😀".repeat(501));
  click("确认隐藏");
  expect(calls("/delete")).toHaveLength(0);
  const u = user();
  await u.clear(field);
  await u.paste("😀".repeat(500));
  await act(async () => click("确认隐藏"));
  expect(calls("/delete")).toHaveLength(1);
  expect(JSON.parse(String(calls("/delete")[0][1]?.body)).reason).toBe(
    "😀".repeat(500),
  );
  expect(localStorage.length + sessionStorage.length).toBe(0);
});
it.each(["   ", "bad\u0001reason"])(
  "retains whitespace/control validation for reasons (%#)",
  async (text) => {
    await reasonForm("delete");
    fireEvent.change(screen.getByLabelText("操作原因"), {
      target: { value: text },
    });
    click("确认隐藏");
    expect(screen.getByRole("alert")).toHaveTextContent("1 至 500");
    expect(calls("/delete")).toHaveLength(0);
  },
);
