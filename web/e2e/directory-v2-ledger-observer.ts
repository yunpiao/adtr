import { execFile } from "node:child_process";
import { promisify } from "node:util";
import {
  parseDirectoryUseObservation,
  type DirectoryUseObservation,
} from "./directory-ledger-observer";

const run = promisify(execFile);

// Fixed v2-purpose SELECT-only observer. Reuse v1's strict response validation;
// neither task state nor cancellation can synthesize a cleanup witness.
export function directoryV2ObserverQuery(taskUUID: string): string {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskUUID))
    throw new Error("Invalid isolated directory task identifier");
  return `SELECT row_to_json(observed) FROM (
SELECT u.state,u.quiescence_reason AS reason,
(SELECT count(*)::int FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.directory_read.v2' AND d.object_id=u.task_id) AS dependencies,
t.state AS "taskState"
FROM adtr.domain_directory_task_uses u JOIN adtr.tasks t ON t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id
WHERE u.tenant_id='default' AND u.purpose='domain.directory_read.v2' AND t.kind='domain.directory_read.v2' AND u.task_id='${taskUUID}'
) observed`;
}

export async function readDirectoryV2Use(
  taskUUID: string,
): Promise<DirectoryUseObservation | null> {
  const sql = directoryV2ObserverQuery(taskUUID);
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
