import { useContext, useLayoutEffect, useRef, type ReactNode } from "react";
import { WorkspaceModalContext } from "./workspace-modal";

// Keep the drawer in its owning workspace. A parent session check hides the
// complete subtree, including this fixed layer; no portal can outlive that gate.
export default function AssetDetailDrawer({
  children,
  close,
  returnFocus,
}: {
  children: ReactNode;
  close: () => void;
  returnFocus: () => void;
}) {
  const setWorkspaceModalOpen = useContext(WorkspaceModalContext);
  const layer = useRef<HTMLDivElement>(null);
  const callbacks = useRef({ close, returnFocus });
  callbacks.current = { close, returnFocus };
  useLayoutEffect(() => {
    const root = layer.current!;
    setWorkspaceModalOpen(true);
    const changed: { node: HTMLElement; inert: boolean }[] = [];
    let child: HTMLElement = root;
    for (
      let parent = child.parentElement;
      parent && parent !== document.body;
      parent = child.parentElement
    ) {
      for (const sibling of parent.children) {
        if (
          sibling !== child &&
          sibling instanceof HTMLElement &&
          !sibling.hasAttribute("data-workspace-modal-owned")
        ) {
          changed.push({ node: sibling, inert: sibling.hasAttribute("inert") });
          sibling.setAttribute("inert", "");
        }
      }
      child = parent;
    }
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    // Setup owns initial focus so StrictMode cleanup/setup replay cannot leave
    // focus on the inert originating row.
    root
      .querySelector<HTMLElement>('[tabindex="-1"], button:not([disabled])')
      ?.focus();
    const keydown = (event: KeyboardEvent) => {
      // Hidden/inert ancestors belong to the session gate. Do not retain focus
      // or consume keys while that gate has removed this private workspace.
      if (root.closest("[hidden], [inert]")) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        callbacks.current.close();
      } else if (event.key === "Tab") {
        const controls = [
          ...root.querySelectorAll<HTMLElement>(
            'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex="0"]',
          ),
        ].filter((node) => {
          if (node.closest("[hidden], [inert]")) return false;
          const closed = node.closest("details:not([open])");
          return !closed || node === closed.querySelector("summary");
        });
        const first = controls[0],
          last = controls[controls.length - 1];
        if (!first || !last) {
          event.preventDefault();
          return;
        }
        if (
          event.shiftKey &&
          (!root.contains(document.activeElement) ||
            document.activeElement === first ||
            document.activeElement?.getAttribute("tabindex") === "-1")
        ) {
          event.preventDefault();
          last.focus();
        } else if (
          !event.shiftKey &&
          (!root.contains(document.activeElement) ||
            document.activeElement === last)
        ) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", keydown, true);
    return () => {
      document.removeEventListener("keydown", keydown, true);
      for (const { node, inert } of changed)
        if (!inert) node.removeAttribute("inert");
      document.body.style.overflow = previousOverflow;
      setWorkspaceModalOpen(false);
      callbacks.current.returnFocus();
    };
  }, [setWorkspaceModalOpen]);
  return (
    <div className="asset-drawer-layer" ref={layer}>
      <div className="asset-drawer-scrim" onClick={close} aria-hidden="true" />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="用户详情抽屉"
        className="asset-drawer"
      >
        {children}
      </div>
    </div>
  );
}
