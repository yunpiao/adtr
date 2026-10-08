import { execFile } from "node:child_process";
import { promisify } from "node:util";

const run = promisify(execFile);
const taskStates = new Set([
  "queued",
  "running",
  "retry_wait",
  "cancel_requested",
  "succeeded",
  "failed",
  "partial_failed",
  "dead_letter",
  "cancelled",
]);
export type DirectoryUseObservation = {
  state: "reserved" | "opened" | "quiesced";
  reason: null | "executor_returned" | "never_opened_terminal";
  dependencies: number;
  taskState: string;
};

// Test-only observer: identifiers never become shell syntax, and the fixed SQL
// cannot change a task, ledger, dependency, opener or cleanup witness.
export function directoryObserverQuery(taskUUID: string): string {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskUUID))
    throw new Error("Invalid isolated directory task identifier");
  return `SELECT row_to_json(observed) FROM (
SELECT u.state,u.quiescence_reason AS reason,
(SELECT count(*)::int FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.directory_read' AND d.object_id=u.task_id) AS dependencies,
t.state AS "taskState"
FROM adtr.domain_directory_task_uses u JOIN adtr.tasks t ON t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id
WHERE u.tenant_id='default' AND u.purpose='domain.directory_read' AND t.kind='domain.directory_read' AND u.task_id='${taskUUID}'
) observed`;
}

export function parseDirectoryUseObservation(
  text: string,
): DirectoryUseObservation | null {
  if (text.length > 4096)
    throw new Error("Invalid isolated directory observation");
  if (!text.trim()) return null;
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    throw new Error("Invalid isolated directory observation");
  }
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid isolated directory observation");
  const v = value as Record<string, unknown>;
  if (
    Object.keys(v).sort().join(" ") !== "dependencies reason state taskState" ||
    !["reserved", "opened", "quiesced"].includes(v.state as string) ||
    ![null, "executor_returned", "never_opened_terminal"].includes(
      v.reason as null | string,
    ) ||
    !Number.isInteger(v.dependencies) ||
    (v.dependencies as number) < 0 ||
    (v.dependencies as number) > 1 ||
    !taskStates.has(v.taskState as string) ||
    (v.state === "quiesced"
      ? v.reason === null || v.dependencies !== 0
      : v.reason !== null || v.dependencies !== 1)
  )
    throw new Error("Invalid isolated directory observation");
  return v as DirectoryUseObservation;
}

export async function readDirectoryUse(
  taskUUID: string,
): Promise<DirectoryUseObservation | null> {
  const sql = directoryObserverQuery(taskUUID);
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container))
    throw new Error("Owned isolated database container is required");
  let stdout: string;
  try {
    ({ stdout } = await run(
      "docker",
      [
        "exec",
        "-e",
        "PGOPTIONS=-c statement_timeout=2000",
        container,
        "psql",
        "-U",
        "postgres",
        "-d",
        "adtr_e2e",
        "-X",
        "-A",
        "-t",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
      ],
      { encoding: "utf8", timeout: 5000, maxBuffer: 16384 },
    ));
  } catch {
    throw new Error("Isolated directory observer failed");
  }
  return parseDirectoryUseObservation(stdout);
}
