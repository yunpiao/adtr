import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import SystemDiskOverview from "./SystemDiskOverview";
import type { Availability, Current, StorageMetric } from "./system-api";

const observedAt = "2026-11-06T08:00:00Z";
const baseMetric = {
  observedAt,
  source: "linux_proc",
  scope: "kernel_visible",
  availability: "available" as const,
};
const baseDisk: StorageMetric = {
  ...baseMetric,
  source: "statfs",
  scope: "runtime_filesystem",
  id: "runtime-root",
  mount: "/",
  fs: "overlay",
  totalBytes: "10000",
  usedBytes: "2012",
  freeBytes: "7988",
  reservedBytes: "0",
  percent: 20.12,
};
const baseCurrent: Current = {
  instance: "local-api",
  availability: "available",
  stale: false,
  availableSince: observedAt,
  checkedAt: observedAt,
  snapshot: {
    instance: "local-api",
    observedAt,
    cpu: { ...baseMetric, percent: 4 },
    memory: {
      ...baseMetric,
      percent: 20,
      totalBytes: "100000",
      usedBytes: "20000",
      availableBytes: "80000",
    },
    uptime: { ...baseMetric, seconds: 123 },
    load: { ...baseMetric, values: { one: 1, five: 2, fifteen: 3 } },
    storage: [baseDisk],
  },
};
const current = () => structuredClone(baseCurrent);
const disk = () => structuredClone(baseDisk);
function panel() {
  return screen.getByRole("region", { name: "API 根文件系统观测" });
}
function expectUnknown() {
  expect(panel().querySelector("strong.system-value")).toHaveTextContent(
    /^当前使用率未知$/,
  );
  expect(panel().querySelector(".system-badge")).toHaveAttribute(
    "data-state",
    "unknown",
  );
}

describe("system overview runtime-root observation", () => {
  it("shows the existing root percentage with source, scope and UTC evidence", () => {
    render(<SystemDiskOverview current={current()} />);
    expect(
      within(panel()).getByRole("heading", { name: "API 根文件系统观测" }),
    ).toBeInTheDocument();
    expect(panel().querySelector("strong.system-value")).toHaveTextContent(
      /^20\.12%$/,
    );
    expect(panel().querySelector(".system-badge")).toHaveAttribute(
      "data-state",
      "observed",
    );
    expect(panel()).toHaveTextContent("runtime-root · statfs");
    expect(panel()).toHaveTextContent("runtime_filesystem");
    expect(panel()).toHaveTextContent("采样（UTC）：2026-11-06T08:00:00.000Z");
    expect(panel()).toHaveTextContent("API 进程看到的根文件系统");
    expect(panel()).toHaveTextContent("不是宿主机全部磁盘");
    expect(panel()).toHaveTextContent("PostgreSQL 数据卷或容器磁盘配额");
    expect(within(panel()).queryByRole("button")).not.toBeInTheDocument();
    expect(within(panel()).queryByRole("textbox")).not.toBeInTheDocument();
  });

  it.each([
    [0, "0.00%"],
    [100, "100.00%"],
    [12.3456, "12.35%"],
  ])(
    "formats the available percentage %s without treating zero as missing",
    (value, expected) => {
      const valueCurrent = current();
      valueCurrent.snapshot!.storage[0].percent = value;
      render(<SystemDiskOverview current={valueCurrent} />);
      expect(panel().querySelector("strong.system-value")).toHaveTextContent(
        expected,
      );
      expect(panel().querySelector(".system-badge")).toHaveAttribute(
        "data-state",
        "observed",
      );
    },
  );

  it.each<Availability>(["available", "unavailable"])(
    "never promotes a stale snapshot with %s availability to a current value",
    (availability) => {
      const value = current();
      value.stale = true;
      value.availability = availability;
      value.reason = "stale_sample";
      render(<SystemDiskOverview current={value} />);
      expectUnknown();
      expect(panel()).toHaveTextContent("已有观测：20.12%，不代表当前使用率");
      expect(panel()).toHaveTextContent("最近采样已超过有效期");
      expect(panel()).toHaveTextContent("等待新样本后刷新");
      expect(panel()).toHaveTextContent("数据过期");
    },
  );

  it.each<Availability>(["unavailable", "unsupported", "warming_up"])(
    "requires available whole-current metadata even when the disk is measured (%s)",
    (availability) => {
      const value = current();
      value.availability = availability;
      render(<SystemDiskOverview current={value} />);
      expectUnknown();
      expect(panel()).toHaveTextContent("已有观测：20.12%，不代表当前使用率");
      expect(panel()).toHaveTextContent("资源指标暂无可用观测");
    },
  );

  it("keeps a server-rejected future-clock sample separate from the current value", () => {
    const value = current();
    value.availability = "unavailable";
    value.reason = "clock_skew";
    value.snapshot!.observedAt = "2026-11-06T09:00:00Z";
    value.snapshot!.storage[0].observedAt = value.snapshot!.observedAt;
    render(<SystemDiskOverview current={value} />);
    expectUnknown();
    expect(panel()).toHaveTextContent("已有观测：20.12%，不代表当前使用率");
    expect(panel()).toHaveTextContent("观测时间晚于检查时间");
    expect(panel()).toHaveTextContent("核对服务与数据库时钟");
    expect(panel()).toHaveTextContent("2026-11-06T09:00:00.000Z");
  });

  it.each<Availability>(["unavailable", "unsupported", "warming_up"])(
    "does not show a current or historical percentage for an unavailable disk (%s)",
    (availability) => {
      const value = current();
      Object.assign(value.snapshot!.storage[0], {
        availability,
        percent: null,
        reason: "read_failed",
      });
      render(<SystemDiskOverview current={value} />);
      expectUnknown();
      expect(panel()).not.toHaveTextContent("已有观测");
      expect(panel()).not.toHaveTextContent("0.00%");
      expect(panel()).toHaveTextContent("采样来源读取失败");
      expect(panel()).toHaveTextContent("核对读取权限和采样日志");
    },
  );

  it("does not invent a percentage from a null disk value", () => {
    const value = current();
    value.snapshot!.storage[0].percent = null;
    render(<SystemDiskOverview current={value} />);
    expectUnknown();
    expect(panel()).not.toHaveTextContent("已有观测");
    expect(panel()).not.toHaveTextContent("0.00%");
  });

  it("keeps missing snapshots unknown with recovery advice", () => {
    const value = current();
    value.snapshot = null;
    value.availability = "unavailable";
    value.reason = "no_samples";
    render(<SystemDiskOverview current={value} />);
    expectUnknown();
    expect(panel()).toHaveTextContent("尚无持久化采样");
    expect(panel()).toHaveTextContent("检查采样与写入并等待首轮记录");
    expect(panel()).not.toHaveTextContent("已有观测");
    expect(panel()).not.toHaveTextContent("0.00%");
    expect(panel()).not.toHaveTextContent("statfs");
  });

  it("does not substitute another storage target for a missing runtime-root", () => {
    const value = current();
    value.snapshot!.storage[0].id = "another-root";
    render(<SystemDiskOverview current={value} />);
    expectUnknown();
    expect(panel()).toHaveTextContent("没有 runtime-root 观测");
    expect(panel()).not.toHaveTextContent("20.12%");
    expect(panel()).not.toHaveTextContent("已有观测");
  });

  it("finds the unique runtime-root by identity rather than array position", () => {
    const value = current();
    const other = disk();
    other.id = "another-root";
    other.percent = 81;
    value.snapshot!.storage.unshift(other);
    render(<SystemDiskOverview current={value} />);
    expect(panel().querySelector("strong.system-value")).toHaveTextContent(
      /^20\.12%$/,
    );
    expect(panel()).not.toHaveTextContent("81.00%");
  });

  it("does not choose either duplicate runtime-root observation", () => {
    const value = current();
    const duplicate = disk();
    duplicate.percent = 81;
    value.snapshot!.storage.push(duplicate);
    render(<SystemDiskOverview current={value} />);
    expectUnknown();
    expect(panel()).toHaveTextContent("重复的 runtime-root 观测");
    expect(panel()).not.toHaveTextContent("20.12%");
    expect(panel()).not.toHaveTextContent("81.00%");
    expect(panel()).not.toHaveTextContent("已有观测");
  });

  it("replaces stale evidence with the next confirmed observation", () => {
    const stale = current();
    stale.stale = true;
    stale.availability = "unavailable";
    stale.reason = "stale_sample";
    const { rerender } = render(<SystemDiskOverview current={stale} />);
    expectUnknown();
    const next = current();
    next.snapshot!.storage[0].percent = 21;
    rerender(<SystemDiskOverview current={next} />);
    expect(panel().querySelector("strong.system-value")).toHaveTextContent(
      /^21\.00%$/,
    );
    expect(panel()).not.toHaveTextContent("已有观测");
    expect(panel()).not.toHaveTextContent("20.12%");
    expect(panel().querySelector(".system-badge")).toHaveAttribute(
      "data-state",
      "observed",
    );
  });
});
