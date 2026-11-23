import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuditExportDetail } from "./AuditExports";
import type { AuditContext } from "./audit-common";

const taskUUID = "00000000-0000-4000-8000-000000000011";
const downloadPath = `/api/audit/exports/download?taskUUID=${taskUUID}`;
const detailPath = `/api/audit/exports/detail?taskUUID=${taskUUID}`;
const xlsxType =
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
const context: AuditContext = {
  profile: {
    ID: 1,
    username: "audit-reader",
    role: "custom",
    priv: 0,
    mobile: "",
    email: "",
    remark: "",
    passStrength: "high",
    hasMfa: false,
    needChangePwd: false,
    isExpired: false,
    passwordNotUpdatedDays: 0,
    pwdUpdateTm: "2026-10-01T00:00:00Z",
    csrfToken: "fixture",
  },
  can: (operation) => operation === "GET /api/audit/exports/download",
  sessionChanged: vi.fn(),
};

const detail = {
  task: {
    taskUUID,
    taskName: "audit.export",
    domainId: "platform",
    payloadVersion: 1,
    state: "succeeded",
    sourceState: "SUCCESS",
    error: "",
    createdAt: "2026-10-07T00:00:00Z",
    updatedAt: "2026-10-07T00:00:01Z",
    attempt: 0,
    maxAttempts: 5,
    progress: 100,
    resultVersion: 0,
    result: null,
    cursor: null,
    parentTaskUUID: "",
    terminalAt: null,
    archived: false,
    visibilityVersion: 0,
  },
  downloadReady: true,
  rowCount: 1,
};
const identity = { "X-ADTR-User-ID": "1" };

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("audit export download feedback", () => {
  it.each([true, false])(
    "announces the pending file separately from task progress (success: %s)",
    async (succeeds) => {
      let settle!: (value: Response) => void;
      const held = new Promise<Response>((resolve) => {
        settle = resolve;
      });
      const fetcher = vi.fn<typeof fetch>((url) => {
        if (url === downloadPath) return held;
        if (url === detailPath)
          return Promise.resolve(Response.json(detail, { headers: identity }));
        throw new Error("Unexpected test request");
      });
      const createURL = vi.fn(() => "blob:synthetic");
      vi.stubGlobal(
        "URL",
        class extends URL {
          static createObjectURL = createURL;
          static revokeObjectURL = vi.fn();
        },
      );
      vi.stubGlobal("fetch", fetcher);
      const anchor = vi
        .spyOn(HTMLAnchorElement.prototype, "click")
        .mockImplementation(() => {});
      render(
        <AuditExportDetail
          context={context}
          id={taskUUID}
          done={vi.fn()}
          history={vi.fn()}
        />,
      );
      const button = await screen.findByRole("button", { name: "下载 XLSX" });
      const status = screen.queryByRole("status", { name: "XLSX 下载进度" });
      button.focus();
      fireEvent.click(button);
      fireEvent.click(button);
      expect(
        fetcher.mock.calls.filter(([url]) => url === downloadPath),
      ).toHaveLength(1);
      expect(
        screen.getByRole("progressbar", { name: "导出持久化进度" }),
      ).toHaveValue(100);
      expect(
        screen.getByRole("status", { name: "XLSX 下载进度" }),
      ).toHaveTextContent("正在下载");
      expect(status).toBe(
        screen.getByRole("status", { name: "XLSX 下载进度" }),
      );
      expect(button).toHaveFocus();
      expect(button).toHaveAttribute("aria-disabled", "true");
      expect(anchor).not.toHaveBeenCalled();

      await act(async () =>
        settle(
          succeeds
            ? new Response("synthetic XLSX", {
                headers: {
                  ...identity,
                  "Content-Type": xlsxType,
                  "Content-Disposition": `attachment; filename="audit-${taskUUID}.xlsx"`,
                },
              })
            : Response.json({ error: "export_not_ready" }, { status: 503 }),
        ),
      );
      expect(button).toHaveAttribute("aria-disabled", "false");
      expect(button).toHaveFocus();
      expect(status).toBe(
        screen.getByRole("status", { name: "XLSX 下载进度" }),
      );
      expect(status).not.toHaveTextContent("正在下载");
      if (succeeds) {
        expect(status).toHaveTextContent("并交由浏览器下载");
        expect(anchor).toHaveBeenCalledTimes(1);
      } else {
        expect(status).toBeEmptyDOMElement();
        expect(screen.getByRole("alert")).toHaveTextContent("导出文件尚未就绪");
        expect(anchor).not.toHaveBeenCalled();
      }
    },
  );
});
