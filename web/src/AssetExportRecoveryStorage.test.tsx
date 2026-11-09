// Synthetic storage failures and DOM/API spies only; not browser persistence or
// real server receipts. The recovery locator never supplies authority.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as user from "./user-asset-export-v3-recovery";
import * as group from "./group-asset-export-v3-recovery";
import * as computer from "./computer-asset-export-v3-recovery";
import {
  exportKind as userKind,
  userAssetExportAPI,
} from "./user-asset-export-v3-api";
import { exportKind as groupKind } from "./group-asset-export-v3-api";
import { exportKind as computerKind } from "./computer-asset-export-v3-api";
import {
  exportJSON,
  exportProfile,
  exportRequest,
} from "./user-asset-export-v3-fixtures";
import UserAssetExportV3 from "./UserAssetExportV3";
import { ApiError } from "./api";

type RecoveryInput = Omit<user.ExportRecovery, "kind"> & { kind: string };
const cases = [
  {
    name: "user",
    api: {
      ...user,
      writeExportRecovery: (value: RecoveryInput) =>
        user.writeExportRecovery({ ...value, kind: userKind }),
    },
    kind: userKind,
  },
  {
    name: "group",
    api: {
      ...group,
      writeExportRecovery: (value: RecoveryInput) =>
        group.writeExportRecovery({ ...value, kind: groupKind }),
    },
    kind: groupKind,
  },
  {
    name: "computer",
    api: {
      ...computer,
      writeExportRecovery: (value: RecoveryInput) =>
        computer.writeExportRecovery({ ...value, kind: computerKind }),
    },
    kind: computerKind,
  },
];
const locator = <Kind extends string>(
  kind: Kind,
  key = "original-A",
  actorID = 1,
) => ({
  version: 1 as const,
  kind,
  actorID,
  domainId: "synthetic-domain",
  idempotencyKey: key,
});
function outage(
  methods: ("getItem" | "setItem" | "removeItem")[] = [
    "getItem",
    "setItem",
    "removeItem",
  ],
) {
  const spies = methods.map((method) =>
    vi.spyOn(Storage.prototype, method).mockImplementation(() => {
      throw new DOMException("Synthetic storage unavailable", "SecurityError");
    }),
  );
  return () => spies.forEach((spy) => spy.mockRestore());
}
beforeEach(() => {
  for (const { api } of cases) api.clearExportRecovery();
  sessionStorage.clear();
  vi.stubGlobal("fetch", vi.fn());
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  for (const { api } of cases) api.clearExportRecovery();
  sessionStorage.clear();
});

describe.each(cases)(
  "$name export recovery during transient storage failure",
  ({ api, kind }) => {
    it("preserves healthy durable recovery and returns an independent copy", () => {
      const first = locator(kind);
      api.writeExportRecovery(first);
      first.idempotencyKey = "changed-input";
      const restored = api.readExportRecovery(1)!;
      expect(restored.idempotencyKey).toBe("original-A");
      restored.idempotencyKey = "changed-result";
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("original-A");
    });
    it.each([false, true])(
      "preserves an initially unread durable locator when removal also fails=%s",
      (removeUnavailable) => {
        sessionStorage.setItem(
          api.exportRecoveryStorage,
          JSON.stringify(locator(kind)),
        );
        const remove = vi.spyOn(Storage.prototype, "removeItem");
        if (removeUnavailable)
          remove.mockImplementation(() => {
            throw new DOMException(
              "Synthetic unavailable removal",
              "SecurityError",
            );
          });
        const restore = outage(["getItem"]);
        expect(api.readExportRecovery(1)).toBeNull();
        expect(remove).not.toHaveBeenCalled();
        restore();
        remove.mockRestore();
        expect(api.readExportRecovery(1)?.idempotencyKey).toBe("original-A");
      },
    );
    it("keeps new memory recovery when only setItem fails after a successful clear", () => {
      api.writeExportRecovery(locator(kind));
      api.clearExportRecovery();
      const restore = outage(["setItem"]);
      api.writeExportRecovery(locator(kind, "new-B"));
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
      restore();
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
      expect(sessionStorage.getItem(api.exportRecoveryStorage)).toBeNull();
    });
    it("does not resurrect explicitly cleared recovery when storage returns", () => {
      api.writeExportRecovery(locator(kind));
      const raw = sessionStorage.getItem(api.exportRecoveryStorage);
      const restore = outage();
      api.clearExportRecovery();
      expect(api.readExportRecovery(1)).toBeNull();
      restore();
      expect(sessionStorage.getItem(api.exportRecoveryStorage)).toBe(raw);
      expect(api.readExportRecovery(1)).toBeNull();
      expect(api.readExportRecovery(1)).toBeNull();
    });
    it("retains the newer locator after failed clear and write, even after storage recovers", () => {
      api.writeExportRecovery(locator(kind));
      const restore = outage();
      api.clearExportRecovery();
      api.writeExportRecovery(locator(kind, "new-B"));
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
      restore();
      expect(
        JSON.parse(sessionStorage.getItem(api.exportRecoveryStorage)!)
          .idempotencyKey,
      ).toBe("original-A");
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
    });
    it("does not let a readable older value override a failed replacement write", () => {
      api.writeExportRecovery(locator(kind));
      const restore = outage(["setItem"]);
      api.writeExportRecovery(locator(kind, "new-B"));
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
      restore();
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
    });
    it("retains the clear marker while remove keeps failing but reads work", () => {
      api.writeExportRecovery(locator(kind));
      const restore = outage(["removeItem"]);
      api.clearExportRecovery();
      expect(api.readExportRecovery(1)).toBeNull();
      expect(api.readExportRecovery(1)).toBeNull();
      restore();
      expect(api.readExportRecovery(1)).toBeNull();
    });
    it("keeps the latest actor boundary even when durable storage contains another actor", () => {
      api.writeExportRecovery(locator(kind, "old-owner-A", 1));
      const restore = outage();
      api.clearExportRecovery();
      api.writeExportRecovery(locator(kind, "new-owner-B", 2));
      restore();
      expect(api.readExportRecovery(2)?.idempotencyKey).toBe("new-owner-B");
      expect(api.readExportRecovery(1)).toBeNull();
      expect(api.readExportRecovery(2)).toBeNull();
    });
    it("rejects a wrong actor while writes and removal remain unavailable", () => {
      api.writeExportRecovery(locator(kind));
      const restore = outage();
      api.clearExportRecovery();
      api.writeExportRecovery(locator(kind, "new-B"));
      expect(api.readExportRecovery(2)).toBeNull();
      restore();
      expect(api.readExportRecovery(1)).toBeNull();
    });
    it("resumes durable validation after an explicit successful replacement write", () => {
      api.writeExportRecovery(locator(kind));
      const restore = outage();
      api.writeExportRecovery(locator(kind, "new-B"));
      restore();
      api.writeExportRecovery(locator(kind, "saved-C"));
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("saved-C");
      sessionStorage.setItem(api.exportRecoveryStorage, "{");
      expect(api.readExportRecovery(1)).toBeNull();
    });
    it("resumes ordinary restoration after a successful explicit clear", () => {
      api.writeExportRecovery(locator(kind));
      const restore = outage();
      api.clearExportRecovery();
      restore();
      api.clearExportRecovery();
      sessionStorage.setItem(
        api.exportRecoveryStorage,
        JSON.stringify(locator(kind, "external-C")),
      );
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("external-C");
    });
    it.each([
      "{",
      "x".repeat(1025),
      JSON.stringify({ ...locator(kind), extra: "not-permitted" }),
    ])("still rejects malformed or expanded healthy storage", (raw) => {
      sessionStorage.setItem(api.exportRecoveryStorage, raw);
      expect(api.readExportRecovery(1)).toBeNull();
      expect(sessionStorage.getItem(api.exportRecoveryStorage)).toBeNull();
    });
    it("stores and retains only the original non-secret locator whitelist", () => {
      const extra = {
        ...locator(kind),
        actorPassword: "synthetic-proof",
        csrfToken: "synthetic-token",
        request: { search: "private" },
      };
      const restore = outage();
      api.writeExportRecovery(extra);
      const memory = api.readExportRecovery(1)!;
      expect(Object.keys(memory).sort()).toEqual([
        "actorID",
        "domainId",
        "idempotencyKey",
        "kind",
        "version",
      ]);
      expect(JSON.stringify(memory)).not.toMatch(
        /synthetic-proof|synthetic-token|private/,
      );
      restore();
      expect(fetch).not.toHaveBeenCalled();
    });
    it("keeps its own storage failure isolated from other export kinds", () => {
      api.writeExportRecovery(locator(kind));
      const restore = outage();
      api.clearExportRecovery();
      api.writeExportRecovery(locator(kind, "new-B"));
      restore();
      for (const other of cases.filter((c) => c.kind !== kind)) {
        other.api.writeExportRecovery(locator(other.kind, "other-C"));
        other.api.clearExportRecovery();
      }
      expect(api.readExportRecovery(1)?.idempotencyKey).toBe("new-B");
      expect(fetch).not.toHaveBeenCalled();
    });
  },
);

const query = {
  ...exportRequest,
  creatTm: undefined,
  pageIdx: 1,
  pageSize: 10,
};
function view() {
  return render(
    <UserAssetExportV3
      profile={exportProfile}
      query={query}
      disabled={false}
      failure={vi.fn()}
    />,
  );
}
async function open() {
  fireEvent.click(screen.getByRole("button", { name: "导出匹配用户 XLSX" }));
  await waitFor(() =>
    expect(screen.queryByText("正在核对导出…")).not.toBeInTheDocument(),
  );
}
async function abandon() {
  fireEvent.click(screen.getByRole("button", { name: "放弃原回执恢复" }));
  fireEvent.click(screen.getByRole("button", { name: "确认放弃恢复记录" }));
  await screen.findByText("已按你的确认放弃恢复记录，未发送新建或取消请求。");
}
function gate() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/api/access/menu")
        return exportJSON({
          menu: [
            "domains",
            "directory_assets",
            "tasks",
            "user_asset_exports",
          ].map((mark) => ({
            mark,
            auth: { readable: true, writeable: true },
          })),
        });
      if (path === "/api/access/check")
        return exportJSON({ results: [true, true, true, true, true] });
      throw new Error(`Unexpected URL ${path}`);
    }),
  );
}
describe("actual user export component recovery after a transient storage outage", () => {
  it("does not rediscover an explicitly abandoned locator after component remount", async () => {
    gate();
    user.writeExportRecovery(locator(userKind));
    const receipt = vi
      .spyOn(userAssetExportAPI, "receipt")
      .mockRejectedValue(new ApiError("not_found", 404));
    const create = vi.spyOn(userAssetExportAPI, "create");
    const first = view();
    await open();
    expect(receipt.mock.calls[0][1]).toBe("original-A");
    const restore = outage();
    await abandon();
    first.unmount();
    restore();
    receipt.mockClear();
    view();
    await open();
    expect(receipt).not.toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "创建用户导出" })).toBeEnabled();
  });
  it("rehydrates the new ambiguous submission, not the old abandoned locator", async () => {
    gate();
    user.writeExportRecovery(locator(userKind));
    const receipt = vi
      .spyOn(userAssetExportAPI, "receipt")
      .mockRejectedValue(new ApiError("not_found", 404));
    const create = vi
      .spyOn(userAssetExportAPI, "create")
      .mockRejectedValue(new ApiError("network"));
    const first = view();
    await open();
    const restore = outage();
    await abandon();
    fireEvent.change(screen.getByLabelText("操作者当前密码"), {
      target: { value: "SyntheticPassword123!" },
    });
    fireEvent.change(screen.getByLabelText("未使用的认证器验证码"), {
      target: { value: "123456" },
    });
    fireEvent.click(screen.getByRole("button", { name: "创建用户导出" }));
    await screen.findByText(/网络中断/);
    const originalKey = create.mock.calls[0][1];
    expect(originalKey).not.toBe("original-A");
    first.unmount();
    restore();
    receipt.mockClear();
    view();
    await open();
    expect(receipt).toHaveBeenCalledTimes(1);
    expect(receipt.mock.calls[0][1]).toBe(originalKey);
    expect(create).toHaveBeenCalledTimes(1);
    expect(screen.getByText(originalKey)).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "使用原请求和幂等键重试" }),
    ).not.toBeInTheDocument();
  });
});
