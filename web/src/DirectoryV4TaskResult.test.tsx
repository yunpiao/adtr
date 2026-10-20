// Synthetic presentation contracts only. Authored, not executed.
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import DirectoryV4TaskResult from "./DirectoryV4TaskResult";
import DirectoryV4SourcePicker from "./DirectoryV4SourcePicker";
import type { DirectoryV4Controls } from "./directory-v4-controls-api";
import type { GroupMembersV4Page } from "./group-members-v4-read";
import type { GroupMembersV4 } from "./group-members-v4-api";
const source = { server_name: "dc.synthetic.invalid", dc_host_name: "observed.synthetic.invalid", domain: "synthetic.invalid", naming_context: "DC=synthetic,DC=invalid", started_at: "2026-10-20T00:00:00Z", completed_at: "2026-10-20T00:00:01Z", elapsed_milliseconds: 1000, pages: 1 };
const row = { objectGUID: "00000000-0000-0000-0000-000000000001", distinguishedName: "CN=synthetic", kind: "group" as const, objectClass: ["group"], samAccountName: "synthetic-group", userAccountControl: null, objectSid: null, mail: null, description: null, whenCreated: null, primaryGroupID: null, servicePrincipalName: null, rawMember: [" z ", "CN=<script>", "\\u0000", "\u0000"], returnedMemberCount: 4, memberReadState: "returned" as const };
const page = (pageIdx = 1, pageSize = 25): GroupMembersV4Page => ({ dictionaryVersion: 4, available: true, observationId: "original-task", source, list: [row], page: { pageIdx, pageSize, total: 26, totalPage: 2 } });
const detail = (): GroupMembersV4 => ({ dictionaryVersion: 4, selection: { domainId: "original-domain", revision: "11", credentialRevision: "13" }, observationId: "original-task", source, object: row });
function harness() {
  const pageRead = vi.fn(async (index: number, size: number, _signal: AbortSignal) => page(index, size));
  const detailRead = vi.fn(async (_guid: string, _signal: AbortSignal) => detail());
  // Narrow presentation test double: factory/identity enforcement is covered by
  // actual API and Workspace tests, never a replacement runtime evidence source.
  const api = { page: pageRead, detail: detailRead } as unknown as DirectoryV4Controls;
  return { api, pageRead, detailRead, failure: vi.fn(), close: vi.fn() };
}
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
describe("original-only v4 result presentation", () => {
  it("routes every refresh, size and page through the same opaque original API", async () => {
    const h = harness(); render(<DirectoryV4TaskResult api={h.api} taskUUID="original-task" close={h.close} failure={h.failure} />);
    await screen.findByRole("button", { name: "synthetic-group" });
    fireEvent.click(screen.getByRole("button", { name: "下一页组" })); await waitFor(() => expect(h.pageRead).toHaveBeenCalledWith(2, 25, expect.any(AbortSignal)));
    await screen.findByRole("button", { name: "synthetic-group" }); fireEvent.click(screen.getByRole("button", { name: "刷新原始结果" }));
    await waitFor(() => expect(h.pageRead).toHaveBeenCalledTimes(3)); await screen.findByRole("button", { name: "synthetic-group" });
    fireEvent.change(screen.getByLabelText("每页组数"), { target: { value: "50" } }); await waitFor(() => expect(h.pageRead).toHaveBeenLastCalledWith(1, 50, expect.any(AbortSignal)));
    expect(screen.getByText(/固定原始观测：original-task/)).toBeVisible(); expect(h.detailRead).not.toHaveBeenCalled();
  });
  it("renders original raw order reversibly, with no DN links or derived total", async () => {
    const h = harness(); render(<DirectoryV4TaskResult api={h.api} taskUUID="original-task" close={h.close} failure={h.failure} />);
    fireEvent.click(await screen.findByRole("button", { name: "synthetic-group" }));
    await screen.findByRole("region", { name: "组原始成员详情" }); expect(h.detailRead).toHaveBeenCalledWith(row.objectGUID, expect.any(AbortSignal));
    expect(screen.getByText("CN=<script>")).toBeVisible(); expect(screen.queryByRole("link")).toBeNull(); expect(screen.getByText(/不代表目录成员总数或完整性/)).toBeVisible();
    expect(document.querySelector("script")).toBeNull();
  });
  it.each([{ members: null }, { members: [] }] as { members: string[] | null }[])("distinguishes unreturned and empty original member values $members", async ({ members }) => {
    const h = harness(); h.detailRead.mockResolvedValue({ ...detail(), object: { ...row, rawMember: members, returnedMemberCount: members === null ? null : 0, memberReadState: members === null ? "not_returned" : "returned" } });
    render(<DirectoryV4TaskResult api={h.api} taskUUID="original-task" close={h.close} failure={h.failure} />); fireEvent.click(await screen.findByRole("button", { name: "synthetic-group" }));
    await screen.findByText(members === null ? /无法区分属性不存在与 ACL 隐藏/ : /返回零个值，不证明该组为空/);
  });
  it("drops an ignored-abort detail after page replacement", async () => {
    const h = harness(); let resolve!: (value: GroupMembersV4) => void; h.detailRead.mockImplementation(() => new Promise(r => { resolve = r; }));
    render(<DirectoryV4TaskResult api={h.api} taskUUID="original-task" close={h.close} failure={h.failure} />); fireEvent.click(await screen.findByRole("button", { name: "synthetic-group" }));
    fireEvent.click(screen.getByRole("button", { name: "下一页组" })); expect(h.detailRead.mock.calls[0][1].aborted).toBe(true);
    await act(async () => resolve(detail())); expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull();
  });
  it("unmounting a closed result rejects a late ignored-abort detail", async () => {
    const h = harness(); let resolve!: (value: GroupMembersV4) => void; h.detailRead.mockImplementation(() => new Promise(r => { resolve = r; }));
    const view = render(<DirectoryV4TaskResult api={h.api} taskUUID="original-task" close={h.close} failure={h.failure} />);
    fireEvent.click(await screen.findByRole("button", { name: "synthetic-group" })); view.unmount(); expect(h.detailRead.mock.calls[0][1].aborted).toBe(true);
    await act(async () => resolve(detail())); expect(screen.queryByRole("region", { name: "组原始成员详情" })).toBeNull(); expect(h.failure).not.toHaveBeenCalled();
  });
  it("clears page and detail on a failed original read without a latest fallback", async () => {
    const h = harness(); render(<DirectoryV4TaskResult api={h.api} taskUUID="original-task" close={h.close} failure={h.failure} />); await screen.findByRole("button", { name: "synthetic-group" });
    const denied = new Error("synthetic original unavailable"); h.pageRead.mockRejectedValueOnce(denied); fireEvent.click(screen.getByRole("button", { name: "刷新原始结果" }));
    await waitFor(() => expect(h.failure).toHaveBeenCalledWith(denied)); expect(screen.queryByText("synthetic-group")).toBeNull(); expect(h.pageRead).toHaveBeenCalledTimes(2);
  });
});
describe("bounded source picker query presentation", () => {
  it("uses v4 supplied transport and rejects overlong/control keyword before a new request", async () => {
    const listSources = vi.fn(async () => ({ List: [], exhausted: true, page: { pageIdx: 1, pageSize: 10, total: 0, totalPage: 0 } }));
    render(<DirectoryV4SourcePicker api={{ listSources } as unknown as DirectoryV4Controls} choose={vi.fn()} failure={vi.fn()} />);
    await waitFor(() => expect(listSources).toHaveBeenCalledTimes(1));
    expect(listSources).toHaveBeenCalledWith({ pageIdx: 1, pageSize: 10, keyword: "", observationState: "" }, expect.any(AbortSignal));
    fireEvent.change(screen.getByLabelText("搜索已保存域（最多 50 个字符）"), { target: { value: "字".repeat(51) } });
    expect(screen.getByRole("button", { name: "搜索域" })).toBeDisabled(); expect(listSources).toHaveBeenCalledTimes(1);
    fireEvent.change(screen.getByLabelText("搜索已保存域（最多 50 个字符）"), { target: { value: "\u0000" } }); expect(screen.getByRole("button", { name: "搜索域" })).toBeDisabled();
  });
});
