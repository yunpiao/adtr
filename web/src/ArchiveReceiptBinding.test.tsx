import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Profile } from "./api";
import { MaintenanceContent } from "./TaskMaintenance";
import {
  maintenanceAPI,
  type ArchiveInput,
  type ArchiveReceipt,
} from "./maintenance-api";
import {
  beginMaintenanceIntent,
  discardMaintenanceIntent,
  maintenanceIntent,
} from "./maintenance-intent";

type Action = "archive" | "restore";
type Result = { receipt: ArchiveReceipt; replayed: boolean };
const first = "00000000-0000-4000-8000-000000000001";
const second = "00000000-0000-4000-8000-000000000002";
const key = "00000000-0000-4000-8000-000000000003";
const cutoff = "2026-10-07T00:00:00.123400Z";
const proof = { actorPassword: "Synthetic Password 123", totpCode: "123456" };
const input = (action: Action, before = cutoff): ArchiveInput => ({
  targets: [
    { taskUUID: second, visibilityVersion: 7 },
    { taskUUID: first, visibilityVersion: 0 },
  ],
  reason: "Synthetic archive review",
  ...(action === "archive" ? { before } : {}),
});
const result = (
  action: Action,
  request: ArchiveInput,
  replayed = false,
): Result => ({
  receipt: {
    operationUUID: key,
    action,
    reason: request.reason,
    occurredAt: "2026-10-08T00:00:00Z",
    ...(action === "archive" ? { before: request.before } : {}),
    targets: request.targets
      .map((target) => ({
        taskUUID: target.taskUUID,
        visibilityVersion: target.visibilityVersion + 1,
        archived: action === "archive",
      }))
      .sort((a, b) => a.taskUUID.localeCompare(b.taskUUID)),
  },
  replayed,
});
const response = (value: unknown, status = 200) =>
  Promise.resolve({
    ok: status < 400,
    status,
    json: async () => value,
  } as Response);
let fetcher: ReturnType<typeof vi.fn>;
const profile: Profile = {
  ID: 1,
  username: "synthetic-receipt",
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
beforeEach(() => {
  discardMaintenanceIntent(profile);
  sessionStorage.clear();
  fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  discardMaintenanceIntent(profile);
  vi.unstubAllGlobals();
});
const submit = (action: Action, request: ArchiveInput) =>
  maintenanceAPI.archive(
    action,
    request,
    key,
    proof,
    profile.csrfToken,
    new AbortController().signal,
  );
const altered: [string, (value: Result) => unknown][] = [
  [
    "opposite target state",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t) => ({
          ...t,
          archived: !t.archived,
        })),
      },
    }),
  ],
  [
    "only one target has wrong state",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t, i) =>
          i ? t : { ...t, archived: !t.archived },
        ),
      },
    }),
  ],
  [
    "unchanged version",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t) => ({
          ...t,
          visibilityVersion: t.visibilityVersion - 1,
        })),
      },
    }),
  ],
  [
    "skipped version",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t) => ({
          ...t,
          visibilityVersion: t.visibilityVersion + 1,
        })),
      },
    }),
  ],
  [
    "versions assigned to wrong targets",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t, i) => ({
          ...t,
          visibilityVersion: v.receipt.targets[1 - i].visibilityVersion,
        })),
      },
    }),
  ],
  [
    "changed reason",
    (v) => ({ ...v, receipt: { ...v.receipt, reason: "Different operation" } }),
  ],
  [
    "trimmed reason",
    (v) => ({
      ...v,
      receipt: { ...v.receipt, reason: v.receipt.reason + " " },
    }),
  ],
  [
    "wrong action",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        action: v.receipt.action === "archive" ? "restore" : "archive",
      },
    }),
  ],
  [
    "missing target",
    (v) => ({
      ...v,
      receipt: { ...v.receipt, targets: v.receipt.targets.slice(1) },
    }),
  ],
  [
    "duplicate target",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: [v.receipt.targets[0], v.receipt.targets[0]],
      },
    }),
  ],
  [
    "foreign target",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t, i) =>
          i ? t : { ...t, taskUUID: "foreign" },
        ),
      },
    }),
  ],
  [
    "fractional version",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t) => ({
          ...t,
          visibilityVersion: t.visibilityVersion + 0.5,
        })),
      },
    }),
  ],
  [
    "unsafe version",
    (v) => ({
      ...v,
      receipt: {
        ...v.receipt,
        targets: v.receipt.targets.map((t) => ({
          ...t,
          visibilityVersion: Number.MAX_SAFE_INTEGER + 1,
        })),
      },
    }),
  ],
];
describe.each(["archive", "restore"] as const)(
  "%s receipt binding",
  (action) => {
    describe.each([false, true])("replayed=%s", (replayed) => {
      it("accepts a complete receipt in server order and preserves the exact outgoing intent", async () => {
        const request = input(action),
          original = structuredClone(request),
          value = result(action, request, replayed);
        fetcher.mockImplementation(() => response(value));
        await expect(submit(action, request)).resolves.toEqual(value);
        expect(request).toEqual(original);
        expect(fetcher).toHaveBeenCalledTimes(1);
        const [url, init] = fetcher.mock.calls[0];
        expect(url).toBe(`/api/tasks/${action}`);
        expect(init).toMatchObject({
          method: "POST",
          credentials: "same-origin",
          cache: "no-store",
        });
        expect(JSON.parse(init.body)).toEqual({
          ...original,
          idempotencyKey: key,
          ...proof,
        });
      });
      it.each(altered)("rejects %s", async (_name, alter) => {
        const request = input(action);
        fetcher.mockImplementation(() =>
          response(alter(result(action, request, replayed))),
        );
        await expect(submit(action, request)).rejects.toMatchObject({
          code: "invalid_response",
        });
      });
      it("accepts the largest exactly representable next version", async () => {
        const request = input(action);
        request.targets = [
          { taskUUID: first, visibilityVersion: Number.MAX_SAFE_INTEGER - 1 },
        ];
        const value = result(action, request, replayed);
        fetcher.mockImplementation(() => response(value));
        await expect(submit(action, request)).resolves.toEqual(value);
      });
      it("rejects success whose next version cannot be represented safely", async () => {
        const request = input(action);
        request.targets = [
          { taskUUID: first, visibilityVersion: Number.MAX_SAFE_INTEGER },
        ];
        fetcher.mockImplementation(() =>
          response(result(action, request, replayed)),
        );
        await expect(submit(action, request)).rejects.toMatchObject({
          code: "invalid_response",
        });
      });
    });
    it.each([false, true])(
      "binds sent intent when request changes while response is pending (altered receipt=%s)",
      async (alteredReceipt) => {
        const request = input(action),
          original = structuredClone(request);
        let deliver!: (value: Response) => void;
        fetcher.mockImplementation(
          () =>
            new Promise<Response>((resolve) => {
              deliver = resolve;
            }),
        );
        const pending = submit(action, request);
        expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({
          ...original,
          idempotencyKey: key,
          ...proof,
        });
        request.reason = "Changed after sending";
        request.targets[0].visibilityVersion += 2;
        if (action === "archive")
          request.before = "2026-10-07T00:00:00.123401Z";
        const value = result(action, alteredReceipt ? request : original, true);
        deliver(await response(value));
        if (alteredReceipt)
          await expect(pending).rejects.toMatchObject({
            code: "invalid_response",
          });
        else await expect(pending).resolves.toEqual(value);
        expect(fetcher).toHaveBeenCalledTimes(1);
      },
    );
    it.each([
      [401, "unauthenticated"],
      [403, "forbidden"],
      [409, "visibility_conflict"],
      [500, "internal"],
    ] as const)("preserves server error %i/%s", async (status, code) => {
      fetcher.mockImplementation(() => response({ error: code }, status));
      await expect(submit(action, input(action))).rejects.toMatchObject({
        code,
        status,
      });
    });
  },
);
describe.each([false, true])("cutoff precision replayed=%s", (replayed) => {
  it.each([
    [cutoff, "2026-10-07T00:00:00.1234Z"],
    ["2026-10-07T00:00:00.000000Z", "2026-10-07T00:00:00Z"],
    ["2026-10-07T00:00:00Z", "2026-10-07T00:00:00.000Z"],
    ["2024-02-29T00:00:00.000001Z", "2024-02-29T00:00:00.000001Z"],
  ])("accepts equal UTC instants %s / %s", async (before, returned) => {
    const request = input("archive", before),
      value = result("archive", request, replayed);
    value.receipt.before = returned;
    fetcher.mockImplementation(() => response(value));
    await expect(submit("archive", request)).resolves.toEqual(value);
  });
  it.each([
    undefined,
    null,
    "",
    "2026-10-07T00:00:00.123401Z",
    "2026-10-07T00:00:00.123Z",
    "2026-10-07T00:00:01.1234Z",
    "2026-02-30T00:00:00Z",
    "0000-10-07T00:00:00Z",
    "2026-10-07T00:00:00.1234000Z",
    "2026-10-07T00:00:00.1234+00:00",
  ])("rejects missing/mismatched/invalid archive cutoff %s", async (before) => {
    const request = input("archive"),
      value = result("archive", request, replayed);
    fetcher.mockImplementation(() =>
      response({ ...value, receipt: { ...value.receipt, before } }),
    );
    await expect(submit("archive", request)).rejects.toMatchObject({
      code: "invalid_response",
    });
  });
  it.each([undefined, null, "", cutoff])(
    "rejects any before property on restore (%s)",
    async (before) => {
      const request = input("restore"),
        value = result("restore", request, replayed);
      fetcher.mockImplementation(() =>
        response({ ...value, receipt: { ...value.receipt, before } }),
      );
      await expect(submit("restore", request)).rejects.toMatchObject({
        code: "invalid_response",
      });
    },
  );
});

describe.each(["archive", "restore"] as const)(
  "%s uncertain receipt UI",
  (action) => {
    it("retains the original persisted intent after contradiction and clears it only on explicit valid replay", async () => {
      const request = input(action),
        pending = beginMaintenanceIntent(profile, { action, input: request });
      const open = vi.fn(),
        sessionChanged = vi.fn();
      let attempts = 0;
      fetcher.mockImplementation((url: string) => {
        expect(url).toBe(`/api/tasks/${action}`);
        const value = result(action, request, attempts > 0);
        if (attempts++ === 0)
          value.receipt.targets[0].archived = action !== "archive";
        return response(value);
      });
      const context = {
        profile,
        canArchiveView: true,
        can: () => true,
        sessionChanged,
      };
      const view = () => (
        <MaintenanceContent
          view={{ type: "maintenance-confirm" }}
          context={context}
          open={open}
        />
      );
      const mounted = render(view());
      const fillProof = (code: string) => {
        fireEvent.change(
          screen.getByLabelText("操作者当前密码", { exact: true }),
          { target: { value: proof.actorPassword } },
        );
        fireEvent.change(
          screen.getByLabelText("未使用的认证器验证码", { exact: true }),
          { target: { value: code } },
        );
      };
      fillProof("123456");
      fireEvent.click(
        screen.getByRole("button", {
          name:
            action === "archive" ? "确认归档所选任务" : "确认恢复任务可见性",
        }),
      );
      await screen.findByRole("alert");
      await waitFor(() =>
        expect(
          screen.getByLabelText("操作者当前密码", { exact: true }),
        ).toHaveValue(""),
      );
      expect(open).not.toHaveBeenCalled();
      expect(sessionChanged).not.toHaveBeenCalled();
      expect(maintenanceIntent(profile)).toMatchObject({
        key: pending.key,
        attempted: true,
        action,
        input: request,
      });
      const stored = sessionStorage.getItem(
        `adtr.pending-task-maintenance:${profile.ID}:${encodeURIComponent(profile.username)}`,
      )!;
      expect(JSON.parse(stored)).toMatchObject({
        key: pending.key,
        action,
        input: request,
      });
      expect(stored).not.toContain(proof.actorPassword);
      expect(fetcher).toHaveBeenCalledTimes(1);
      mounted.unmount();
      render(view());
      expect(screen.getByLabelText("维护操作幂等键")).toHaveValue(pending.key);
      expect(fetcher).toHaveBeenCalledTimes(1);
      fillProof("654321");
      fireEvent.click(
        screen.getByRole("button", { name: "使用原幂等键核对维护操作" }),
      );
      await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
      expect(open).toHaveBeenCalledWith(
        { type: "detail", id: second, archived: true },
        expect.stringContaining("已确认原操作收据，当前可见性可能已经变化"),
      );
      expect(maintenanceIntent(profile)).toBeUndefined();
      expect(
        sessionStorage.getItem(
          `adtr.pending-task-maintenance:${profile.ID}:${encodeURIComponent(profile.username)}`,
        ),
      ).toBeNull();
      const bodies = fetcher.mock.calls.map(([, init]) =>
        JSON.parse(init.body),
      );
      expect(bodies).toEqual(
        ["123456", "654321"].map((totpCode) => ({
          ...request,
          idempotencyKey: pending.key,
          actorPassword: proof.actorPassword,
          totpCode,
        })),
      );
    });
  },
);
