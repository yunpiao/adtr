// Only this isolated fixture's loopback controller receives the private token.
// Native fetch avoids Playwright request diagnostics recording its header.
export async function runningGuard(path: "prepare" | "checkpoint" | "resume", body: Record<string, string> = {}) {
  const base = process.env.ADTR_E2E_RUNNING_CANCEL_GUARD_URL ?? "";
  const token = process.env.ADTR_E2E_RUNNING_CANCEL_GUARD_TOKEN ?? "";
  if (!/^http:\/\/127\.0\.0\.1:[0-9]+$/u.test(base) || !/^[0-9a-f]{64}$/u.test(token)) throw new Error("Owned running-cancel guard required");
  try {
    const response = await fetch(`${base}/${path}`, {
      method: "POST", redirect: "error", signal: AbortSignal.timeout(path === "checkpoint" ? 12_000 : 3000),
      headers: { "Content-Type": "application/json", "X-ADTR-Guard-Token": token }, body: JSON.stringify(body),
    });
    if (!response.ok) throw new Error("guard rejected");
    return await response.json();
  } catch { throw new Error(`Owned running-cancel guard ${path} failed`); }
}
