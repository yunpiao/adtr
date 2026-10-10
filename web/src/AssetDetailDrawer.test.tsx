import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { StrictMode, useRef, useState } from "react";
import AssetDetailDrawer from "./AssetDetailDrawer";

afterEach(cleanup);
function Harness({ stop = vi.fn() }: { stop?: () => void }) {
  const [open, setOpen] = useState(false);
  const [checking, setChecking] = useState(false);
  const origin = useRef<HTMLButtonElement>(null);
  return (
    <div>
      <header data-testid="header">Platform</header>
      <button onClick={() => setChecking(true)}>Begin session check</button>
      <main hidden={checking} inert={checking}>
        <aside inert data-testid="previous-inert">
          Already protected
        </aside>
        <button ref={origin} onClick={() => setOpen(true)}>
          Open user
        </button>
        {open && (
          <AssetDetailDrawer
            close={() => {
              stop();
              setOpen(false);
            }}
            returnFocus={() => {
              if (
                origin.current &&
                !origin.current.closest("[hidden], [inert]")
              )
                origin.current.focus();
            }}
          >
            <section aria-label="User facts" tabIndex={-1}>
              <button
                onClick={() => {
                  stop();
                  setOpen(false);
                }}
              >
                Close user
              </button>
              <details>
                <summary>Source</summary>
                <button>Hidden source control</button>
              </details>
              <button>Last control</button>
            </section>
          </AssetDetailDrawer>
        )}
      </main>
    </div>
  );
}
function open() {
  fireEvent.click(screen.getByText("Open user"));
}
describe("SOC asset drawer", () => {
  it("inerts background and restores its prior state and scroll after close", () => {
    document.body.style.overflow = "auto";
    render(<Harness />);
    open();
    expect(
      screen.getByRole("dialog", { name: "用户详情抽屉" }),
    ).toHaveAttribute("aria-modal", "true");
    expect(screen.getByTestId("header")).toHaveAttribute("inert");
    expect(screen.getByText("Open user")).toHaveAttribute("inert");
    expect(document.body.style.overflow).toBe("hidden");
    fireEvent.click(screen.getByText("Close user"));
    expect(screen.getByTestId("header")).not.toHaveAttribute("inert");
    expect(screen.getByTestId("previous-inert")).toHaveAttribute("inert");
    expect(document.body.style.overflow).toBe("auto");
    expect(screen.getByText("Open user")).toHaveFocus();
  });
  it("wraps Tab and Shift+Tab and excludes controls in closed provenance", () => {
    render(<Harness />);
    open();
    const first = screen.getByText("Close user"),
      last = screen.getByText("Last control");
    last.focus();
    fireEvent.keyDown(last, { key: "Tab" });
    expect(first).toHaveFocus();
    first.focus();
    fireEvent.keyDown(first, { key: "Tab", shiftKey: true });
    expect(last).toHaveFocus();
  });
  it("Escape cancels the owned detail before restoring its row focus", () => {
    const stop = vi.fn(() =>
      expect(screen.getByText("Open user")).toHaveAttribute("inert"),
    );
    render(<Harness stop={stop} />);
    open();
    screen.getByText("Close user").focus();
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    expect(stop).toHaveBeenCalledOnce();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByText("Open user")).toHaveFocus();
  });
  it("remains concealed by its parent session gate and does not intercept its keys", () => {
    const stop = vi.fn();
    render(<Harness stop={stop} />);
    open();
    fireEvent.click(screen.getByText("Begin session check"));
    expect(
      screen.getByRole("dialog", { name: "用户详情抽屉", hidden: true }),
    ).not.toBeVisible();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(stop).not.toHaveBeenCalled();
  });
  it("StrictMode setup replay retains focus inside the open modal", () => {
    render(
      <StrictMode>
        <Harness />
      </StrictMode>,
    );
    open();
    expect(screen.getByRole("region", { name: "User facts" })).toHaveFocus();
    expect(screen.getByText("Open user")).toHaveAttribute("inert");
  });
  it("unmount cleanup restores background without focusing a removed workspace", () => {
    const view = render(<Harness />);
    open();
    view.unmount();
    expect(document.body.style.overflow).toBe("auto");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
