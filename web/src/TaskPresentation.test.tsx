import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  TaskStateExplanation,
  TaskTimeline,
  taskKindLabel,
} from "./TaskPresentation";
import { taskStates, type Task, type TaskEvent } from "./task-api";

describe("task fact presentation", () => {
  it.each(taskStates)(
    "explains the actual %s state without an invented actor",
    (state) => {
      render(
        <TaskStateExplanation
          task={{ state, taskName: "infrastructure.health", error: "" } as Task}
        />,
      );
      expect(screen.getByText("当前执行状态")).toBeVisible();
      expect(
        document.querySelector(".task-state-explanation p")?.textContent
          ?.length,
      ).toBeGreaterThan(20);
      expect(document.body).not.toHaveTextContent("负责人");
    },
  );
  it("shows the exact provided error without inferring its cause", () => {
    render(
      <TaskStateExplanation
        task={
          {
            state: "failed",
            taskName: "unknown.kind",
            error: "<img src=x onerror=alert(1)>",
          } as Task
        }
      />,
    );
    expect(screen.getByText("<img src=x onerror=alert(1)>")).toBeVisible();
    expect(document.querySelector("img")).toBeNull();
    expect(document.body).not.toHaveTextContent("网络断开");
  });
  it.each(["constructor", "__proto__", "toString", "unknown.kind"])(
    "keeps unknown kind %s as literal text",
    (kind) => {
      expect(taskKindLabel(kind)).toBe(kind);
    },
  );
  it("renders real ordered event fields and unknown actions literally", () => {
    const events: TaskEvent[] = [
      {
        id: 8,
        action: "started",
        state: "running",
        attempt: 2,
        resultVersion: 4,
        createdAt: "2026-10-10T12:00:00Z",
      },
      {
        id: 12,
        action: "constructor",
        state: "failed",
        attempt: 2,
        resultVersion: 5,
        createdAt: "2026-10-10T12:00:01Z",
      },
      {
        id: 15,
        action: "<script>fake()</script>",
        state: "dead_letter",
        attempt: 5,
        resultVersion: 6,
        createdAt: "2026-10-10T12:00:02Z",
      },
    ];
    render(<TaskTimeline events={events} />);
    const items = within(
      screen.getByRole("list", { name: "持久化任务事件" }),
    ).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    for (const [index, event] of events.entries()) {
      expect(items[index]).toHaveTextContent(event.action);
      expect(items[index]).toHaveTextContent(event.state);
      expect(items[index]).toHaveTextContent(event.createdAt);
      expect(items[index]).toHaveTextContent(`事件 #${event.id}`);
      expect(items[index]).toHaveTextContent(
        `尝试 ${event.attempt} · 结果版本 ${event.resultVersion}`,
      );
    }
    expect(document.querySelector("script")).toBeNull();
    expect(document.body).not.toHaveTextContent("操作者");
  });
  it("labels cancellation and an ended attempt without claiming the retrying task ended", () => {
    render(
      <TaskTimeline
        events={[
          {
            id: 20,
            action: "cancel_requested",
            state: "cancel_requested",
            attempt: 1,
            resultVersion: 2,
            createdAt: "2026-10-10T12:00:00Z",
          },
          {
            id: 21,
            action: "finished",
            state: "retry_wait",
            attempt: 1,
            resultVersion: 3,
            createdAt: "2026-10-10T12:00:01Z",
          },
        ]}
      />,
    );
    expect(screen.getByText("取消请求已登记")).toBeVisible();
    expect(screen.getByText("本次尝试已结束")).toBeVisible();
    expect(screen.getByText("等待业务重试 · retry_wait")).toBeVisible();
    expect(screen.queryByText("执行已结束")).not.toBeInTheDocument();
  });
  it("does not fabricate timeline events for an empty response", () => {
    render(<TaskTimeline events={[]} />);
    expect(
      screen.getByRole("list", { name: "持久化任务事件" }),
    ).toBeEmptyDOMElement();
    expect(screen.getByText("服务器尚未返回事件。")).toBeVisible();
  });
});
