import { useState } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AuditHistory from "./AuditHistory";
import type { Profile } from "./api";
import { emptyHistoryQuery, type ExportHistoryQuery } from "./audit-history";

const profile: Profile = {
  ID: 1,
  username: "synthetic-history",
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
  csrfToken: "synthetic-history-session",
};
const startLabel = "导出创建开始时间（UTC）";
const endLabel = "导出创建结束时间（UTC，不包含）";
const statusLabel = "导出任务状态";
const sortLabel = "导出创建时间排序";
const sizeLabel = "每页导出数";
const clearLabel = "清空导出筛选";
let fetcher: ReturnType<typeof vi.fn>;
const emptyResponse = (url: string): Response => {
  const params = new URL(url, "http://localhost").searchParams;
  return {
    ok: true,
    status: 200,
    headers: new Headers({ "X-ADTR-User-ID": "1" }),
    json: async () => ({
      page: {
        pageIdx: Number(params.get("pageIdx")),
        pageSize: Number(params.get("pageSize")),
        total: 0,
        totalPage: 0,
      },
      list: [],
      exhausted: true,
    }),
  } as Response;
};
beforeEach(() => {
  fetcher = vi.fn(async (url: string) => emptyResponse(url));
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => vi.unstubAllGlobals());
function Harness({
  initial = emptyHistoryQuery(),
}: {
  initial?: ExportHistoryQuery;
}) {
  const [query, change] = useState(initial);
  return (
    <>
      <input aria-label="独立页面草稿" defaultValue="retain unrelated draft" />
      <output aria-label="已应用历史查询">{JSON.stringify(query)}</output>
      <AuditHistory
        context={{ profile, can: () => true, sessionChanged: vi.fn() }}
        query={query}
        change={change}
        open={vi.fn()}
        done={vi.fn()}
      />
    </>
  );
}
const fill = (label: string, value: string) =>
  fireEvent.change(screen.getByLabelText(label, { exact: true }), {
    target: { value },
  });
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
function selectStatuses(values: string[]) {
  const select = screen.getByLabelText(statusLabel, {
    exact: true,
  }) as HTMLSelectElement;
  for (const option of select.options)
    option.selected = values.includes(option.value);
  fireEvent.change(select);
}
function fillDraft() {
  fill(startLabel, "2026-10-06T01:02:03");
  fill(endLabel, "2026-10-08T04:05:06");
  selectStatuses(["failed", "cancelled"]);
  fill(sortLabel, "1");
  fill(sizeLabel, "50");
}
function expectDefaults() {
  expect(screen.getByLabelText(startLabel, { exact: true })).toHaveValue("");
  expect(screen.getByLabelText(endLabel, { exact: true })).toHaveValue("");
  expect(screen.getByLabelText(statusLabel, { exact: true })).toHaveValue([]);
  expect(screen.getByLabelText(sortLabel, { exact: true })).toHaveValue("-1");
  expect(screen.getByLabelText(sizeLabel, { exact: true })).toHaveValue("20");
  expect(screen.getByLabelText("已应用历史查询")).toHaveTextContent(
    JSON.stringify(emptyHistoryQuery()),
  );
  expect(screen.getByLabelText("独立页面草稿")).toHaveValue(
    "retain unrelated draft",
  );
}
function expectReadOnlyRequests() {
  for (const [url, init] of fetcher.mock.calls) {
    expect(url.split("?")[0]).toBe("/api/audit/exports/history");
    expect(init).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
    });
    expect(init.body).toBeUndefined();
  }
}
async function start(initial?: ExportHistoryQuery) {
  render(<Harness initial={initial} />);
  await screen.findByText("当前页没有符合条件的可见导出。");
}

describe("export-history clear resets the actual draft", () => {
  it.each([
    [startLabel, "2026-10-06T01:02:03"],
    [endLabel, "2026-10-08T04:05:06"],
    [sortLabel, "1"],
    [sizeLabel, "50"],
  ])(
    "clears an unapplied %s while the applied query is already default",
    async (label, value) => {
      await start();
      fill(label, value);
      click(clearLabel);
      expectDefaults();
      expect(fetcher).toHaveBeenCalledTimes(1);
      expectReadOnlyRequests();
    },
  );
  it("clears all selected statuses without changing the applied default request", async () => {
    await start();
    selectStatuses(["retry_wait", "cancel_requested", "cancelled"]);
    click(clearLabel);
    expectDefaults();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("does not resubmit supposedly cleared draft values on the next filter action", async () => {
    await start();
    fillDraft();
    click(clearLabel);
    click("筛选导出历史");
    expectDefaults();
    expect(fetcher).toHaveBeenCalledTimes(1);
    const params = new URL(fetcher.mock.calls[0][0], "http://localhost")
      .searchParams;
    expect(Object.fromEntries(params)).toEqual({
      pageIdx: "1",
      pageSize: "20",
      sortTm: "-1",
    });
    expectReadOnlyRequests();
  });
  it("clears invalid unsent time drafts and their form error", async () => {
    await start();
    fill(startLabel, "2026-10-08T00:00:00");
    fill(endLabel, "2026-10-07T00:00:00");
    click("筛选导出历史");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "开始时间必须早于结束时间",
    );
    expect(fetcher).toHaveBeenCalledTimes(1);
    click(clearLabel);
    expectDefaults();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    click("筛选导出历史");
    expect(fetcher).toHaveBeenCalledTimes(1);
    expectReadOnlyRequests();
  });
  it("keeps repeated edit/clear cycles idempotent without unnecessary history reads", async () => {
    await start();
    for (let index = 0; index < 3; index++) {
      fillDraft();
      click(clearLabel);
      expectDefaults();
      click(clearLabel);
      click("筛选导出历史");
      expectDefaults();
      expect(fetcher).toHaveBeenCalledTimes(1);
    }
    expectReadOnlyRequests();
  });
  it("preserves the clear button and focus when the applied query does not change", async () => {
    await start();
    const clear = screen.getByRole("button", { name: clearLabel });
    clear.focus();
    click(clearLabel);
    expectDefaults();
    expect(screen.getByRole("button", { name: clearLabel })).toBe(clear);
    expect(clear).toHaveFocus();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("still clears an applied nondefault query and then clears a new unsent draft", async () => {
    await start();
    fillDraft();
    click("筛选导出历史");
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    await screen.findByText("当前页没有符合条件的可见导出。");
    fill(sizeLabel, "10");
    click(clearLabel);
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(3));
    await screen.findByText("当前页没有符合条件的可见导出。");
    expectDefaults();
    fillDraft();
    click(clearLabel);
    expectDefaults();
    expect(fetcher).toHaveBeenCalledTimes(3);
    expectReadOnlyRequests();
  });
  it("resets an applied later page to page one with exactly one new read", async () => {
    await start({
      ...emptyHistoryQuery(),
      pageIdx: 3,
      pageSize: 10,
      sortTm: 1,
      status: ["failed"],
    });
    fillDraft();
    click(clearLabel);
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    expectDefaults();
    expect(
      new URL(fetcher.mock.calls[1][0], "http://localhost").searchParams.get(
        "pageIdx",
      ),
    ).toBe("1");
    expectReadOnlyRequests();
  });
  it("clears drafts during an outstanding default read without replacing that request", async () => {
    let deliver!: (value: Response) => void;
    fetcher.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          deliver = resolve;
        }),
    );
    render(<Harness />);
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
    const signal = fetcher.mock.calls[0][1].signal as AbortSignal;
    fillDraft();
    click(clearLabel);
    expectDefaults();
    expect(signal.aborted).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(1);
    await act(async () => {
      deliver(emptyResponse(fetcher.mock.calls[0][0]));
    });
    await screen.findByText("当前页没有符合条件的可见导出。");
    expectDefaults();
    expectReadOnlyRequests();
  });
});
