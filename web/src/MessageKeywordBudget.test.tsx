// Synthetic transport, actual input events/API/components, not product acceptance.
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import MessagesWorkspace from "./MessagesWorkspace";
import { messageAPI, type MessageSnapshot } from "./message-api";
import {
  defaultMessageQuery,
  messageQueryBody,
  type MessageQuery,
} from "./message-query";
import { sampleSnapshot } from "./message-test-fixtures";

const profile: Profile = {
  ID: 313,
  username: "synthetic-keyword-reader",
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
  pwdUpdateTm: "2026-11-09T00:00:00Z",
  csrfToken: "synthetic-session",
};
const bytes = (value: string) => new TextEncoder().encode(value).length;
// Independently spell out wire fields, rather than using the production builder
// to derive the expected size or the keyword at the HTTP boundary.
const wire = (keyword: string, timed = false) => ({
  types: ["task_result"],
  subTypes: timed ? [{ type: "task_result", subType: "failed" }] : [],
  keyword,
  ...(timed
    ? {
        start: "2026-11-08T00:00:00.000000001Z",
        end: "2026-11-09T00:00:00.999999999Z",
      }
    : {}),
  readFilter: "all",
  sort: "desc",
  offset: 0,
  limit: 20,
});
const atWireSize = (size: number, timed = false) => {
  const available = size - bytes(JSON.stringify(wire("", timed)));
  return "\u0000".repeat(Math.floor(available / 6)) + "x".repeat(available % 6);
};
const query = (keyword: string, timed = false): MessageQuery => ({
  ...defaultMessageQuery(),
  keyword,
  ...(timed
    ? {
        subType: "failed" as const,
        start: "2026-11-08T00:00:00.000000001Z",
        end: "2026-11-09T00:00:00.999999999Z",
      }
    : {}),
});
const legal = [
  ["ASCII beyond old native cap", "A".repeat(4001)],
  ["ASCII at raw byte cap", "A".repeat(16000)],
  ["astral beyond old native cap", "😀".repeat(2001)],
  ["astral at raw byte cap", "😀".repeat(4000)],
  ["CJK under raw byte cap", "中".repeat(5333)],
  ["decomposed accents preserved", "e\u0301".repeat(5000)],
  ["mixed byte boundary", "😀".repeat(3999) + "ABCD"],
  ["literal escapes", '\\".*%_'.repeat(2000)],
  ["exact HTTP JSON budget", atWireSize(32768)],
] as const;
const oversized = [
  ["ASCII 16001 bytes", "A".repeat(16001)],
  ["astral 16004 bytes", "😀".repeat(4001)],
  ["CJK 16002 bytes", "中".repeat(5334)],
  ["mixed 16001 bytes", "😀".repeat(4000) + "x"],
  ["escaped 32769 wire bytes", atWireSize(32769)],
  ["6000 controls expand beyond HTTP budget", "\u0000".repeat(6000)],
] as const;
const empty = (resultId = "synthetic-result"): MessageSnapshot => ({
  ...sampleSnapshot(),
  resultId,
  items: [],
  total: 0,
  counts: { all: 0, read: 0, unread: 0 },
});
const json = (data: unknown) => new Response(JSON.stringify(data));
const posts = () =>
  vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/messages/query");
const field = () =>
  screen.getByLabelText("关键词", { exact: true }) as HTMLInputElement;
const user = () => userEvent.setup({ delay: null });
async function paste(value: string) {
  const u = user();
  await u.click(field());
  await u.paste(value);
}
async function mount(strict = false) {
  const ui = <MessagesWorkspace profile={profile} sessionChanged={vi.fn()} />;
  const root = render(strict ? <StrictMode>{ui}</StrictMode> : ui);
  await screen.findByRole("button", { name: "确认查询" });
  return root;
}
async function settle() {
  await act(async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  });
}
let reply: (body: RequestInit) => Promise<Response>;
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  reply = async () => json(empty());
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (url === "/api/access/menu")
        return json({
          menu: [{ mark: "tasks", auth: { readable: true, writeable: false } }],
        });
      if (url === "/api/access/check")
        return json({
          results: [true, true, true, true, true, true, true, true],
        });
      if (url === "/api/messages/query") return reply(init);
      throw Error(`Unexpected synthetic request ${url}`);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

describe("message query budgets use raw UTF-8 and complete serialized JSON", () => {
  it.each(legal)("preserves %s", async (_label, keyword) => {
    expect(bytes(keyword)).toBeLessThanOrEqual(16000);
    expect(bytes(JSON.stringify(wire(keyword)))).toBeLessThanOrEqual(32768);
    expect(messageQueryBody(query(keyword))).toEqual(wire(keyword));
    await messageAPI.query(
      query(keyword),
      profile.csrfToken,
      new AbortController().signal,
    );
    expect(posts()).toHaveLength(1);
    expect(posts()[0][1]).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": profile.csrfToken,
      },
    });
    expect(posts()[0][1]!.body).toBe(JSON.stringify(wire(keyword)));
  });
  it.each(oversized)(
    "rejects %s before transport, not by truncation",
    async (_label, keyword) => {
      await expect(
        messageAPI.query(
          query(keyword),
          profile.csrfToken,
          new AbortController().signal,
        ),
      ).rejects.toMatchObject({ code: "invalid_input" });
      expect(posts()).toHaveLength(0);
      expect(query(keyword).keyword).toBe(keyword);
    },
  );
  for (const timed of [false, true]) {
    it.each([32767, 32768, 32769])(
      `all JSON fields included, timed=${timed}, wire=%s`,
      async (size) => {
        const keyword = atWireSize(size, timed);
        expect(bytes(keyword)).toBeLessThan(16000);
        expect(bytes(JSON.stringify(wire(keyword, timed)))).toBe(size);
        if (size <= 32768)
          expect(messageQueryBody(query(keyword, timed))).toEqual(
            wire(keyword, timed),
          );
        else expect(() => messageQueryBody(query(keyword, timed))).toThrow();
      },
    );
  }
  it("adding a valid time/subtype filter can exceed the unchanged wire budget", () => {
    const keyword = atWireSize(32768);
    expect(messageQueryBody(query(keyword))).toEqual(wire(keyword));
    expect(bytes(JSON.stringify(wire(keyword, true)))).toBeGreaterThan(32768);
    expect(() => messageQueryBody(query(keyword, true))).toThrow();
  });
  it.each(["", "[A].*", "%_", " A ", '"\\', "é", "e\u0301"])(
    "does not trim, normalize or interpret literal %j",
    (keyword) => {
      expect(messageQueryBody(query(keyword))).toEqual(wire(keyword));
    },
  );
  it("keeps default/time validation and does not mutate query state", () => {
    const input = query("literal", true),
      before = { ...input };
    expect(messageQueryBody(input)).toEqual(wire("literal", true));
    expect(input).toEqual(before);
    expect(() => messageQueryBody({ ...input, end: "" })).toThrow();
    expect(() => messageQueryBody({ ...input, limit: 1 })).toThrow();
  });
});

describe("actual keyword input does not silently change confirmed queries", () => {
  it.each(legal)(
    "pastes %s and submits the exact literal once",
    async (_label, keyword) => {
      await mount();
      await paste(keyword);
      expect(field()).toHaveValue(keyword);
      expect(posts()).toHaveLength(0);
      fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
      await settle();
      expect(posts()).toHaveLength(1);
      expect(JSON.parse(posts()[0][1]!.body as string).keyword).toBe(keyword);
      expect(screen.getByText("synthetic-result")).toBeInTheDocument();
    },
  );
  it.each(oversized)(
    "retains %s for correction and sends no truncated request",
    async (_label, keyword) => {
      await mount();
      await paste(keyword);
      expect(field()).toHaveValue(keyword);
      fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
      await settle();
      expect(posts()).toHaveLength(0);
      expect(screen.getByRole("alert")).toBeInTheDocument();
      expect(field()).toHaveValue(keyword);
      const u = user();
      await u.clear(field());
      await paste("corrected-literal");
      fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
      await settle();
      expect(posts()).toHaveLength(1);
      expect(JSON.parse(posts()[0][1]!.body as string).keyword).toBe(
        "corrected-literal",
      );
    },
  );
  it.each(["A".repeat(4000), "😀".repeat(2000)])(
    "typing across the old cap is preserved (%#)",
    async (prefix) => {
      await mount();
      await paste(prefix);
      await user().type(field(), "Z");
      expect(field()).toHaveValue(prefix + "Z");
      expect(posts()).toHaveLength(0);
      fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
      await settle();
      expect(JSON.parse(posts()[0][1]!.body as string).keyword).toBe(
        prefix + "Z",
      );
    },
  );
  it("selection replacement preserves the new Unicode literal without querying", async () => {
    await mount();
    await paste("old draft");
    const u = user();
    await u.click(field());
    field().setSelectionRange(0, field().value.length);
    const replacement = "😀".repeat(3000);
    await u.paste(replacement);
    expect(field()).toHaveValue(replacement);
    expect(posts()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
    await settle();
    expect(JSON.parse(posts()[0][1]!.body as string).keyword).toBe(replacement);
  });
  it("invalid draft keeps the previous confirmed result until a valid confirmation", async () => {
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
    await settle();
    expect(screen.getByText("synthetic-result")).toBeInTheDocument();
    await paste("A".repeat(16001));
    fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
    await settle();
    expect(posts()).toHaveLength(1);
    expect(screen.getByText("synthetic-result")).toBeInTheDocument();
    expect(screen.getByText(/筛选草稿已改变/)).toBeInTheDocument();
    await user().clear(field());
    await paste("A".repeat(5000));
    reply = async () => json(empty("new-result"));
    fireEvent.click(screen.getByRole("button", { name: "确认查询" }));
    await settle();
    expect(posts()).toHaveLength(2);
    expect(screen.getByText("new-result")).toBeInTheDocument();
    expect(screen.queryByText("synthetic-result")).not.toBeInTheDocument();
  });
  it("long keyword does not bypass duplicate-confirm lock or cancellation generation", async () => {
    let finish!: (r: Response) => void;
    reply = () =>
      new Promise((r) => {
        finish = r;
      });
    await mount();
    await paste("A".repeat(5000));
    const button = screen.getByRole("button", { name: "确认查询" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(posts()).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "停止等待" }));
    finish(json(empty()));
    await settle();
    expect(screen.queryByText("synthetic-result")).not.toBeInTheDocument();
    expect(posts()).toHaveLength(1);
    expect(field()).toHaveValue("A".repeat(5000));
  });
  it("StrictMode and identity remount never submit or retain another profile's draft", async () => {
    const root = await mount(true);
    await paste("A".repeat(5000));
    expect(posts()).toHaveLength(0);
    root.rerender(
      <MessagesWorkspace
        profile={{
          ...profile,
          ID: 314,
          username: "next-synthetic",
          csrfToken: "next-session",
        }}
        sessionChanged={vi.fn()}
      />,
    );
    await screen.findByRole("button", { name: "确认查询" });
    expect(field()).toHaveValue("");
    expect(posts()).toHaveLength(0);
  });
});
