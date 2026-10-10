import { createContext } from "react";

// Keep shell background controls inert for the full lifetime of a workspace
// modal, including temporary hiding during same-session revalidation.
const ignoreModalChange = (_open: boolean) => {};
export const WorkspaceModalContext =
  createContext<(open: boolean) => void>(ignoreModalChange);
