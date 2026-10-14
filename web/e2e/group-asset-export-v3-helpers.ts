import { expect, type Page, type Response } from "@playwright/test";
import { createHash, createHmac } from "node:crypto";
import { execFile, execFileSync } from "node:child_process";
import { promisify } from "node:util";
import { navigateTo } from "./navigation";
import { login } from "./group-assets-v3-fixture";
import { directoryV2ResponseJSON } from "./directory-v2-response-observer";
const run = promisify(execFile);
export const exportPrefix = "/api/group-assets/v3/exports";
export const downloadHeaderNames = ["x-adtr-user-id", "cache-control", "x-content-type-options", "content-type", "content-length", "x-adtr-artifact-sha256", "x-adtr-row-count", "content-disposition"] as const;
export const hash = (bytes: Buffer | string) => createHash("sha256").update(bytes).digest("hex");

// Existing fixture enrollment owns the synthetic secret; never persist it.
export async function switchToExportOwner(page: Page, owner: { username: string; password: string; auth: { secret: string; lastCounter: number } }) {
  await navigateTo(page, "退出登录");
  await login(page, owner.username, owner.password);
  const { auth } = owner;
  const earliest = (auth.lastCounter + 1) * 30_000 + 1100;
  while (Date.now() < earliest) await new Promise((resolve) => setTimeout(resolve, earliest - Date.now()));
  const now = Date.now();
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const bits = [...auth.secret.replace(/=+$/u, "").toUpperCase()].map((c) => {
    const index = alphabet.indexOf(c);
    if (index < 0) throw new Error("Invalid synthetic authenticator");
    return index.toString(2).padStart(5, "0");
  }).join("");
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  const counter = Buffer.alloc(8);
  auth.lastCounter = Math.floor(now / 30_000);
  counter.writeBigUInt64BE(BigInt(auth.lastCounter));
  const digest = createHmac("sha1", Buffer.from(bytes)).update(counter).digest();
  const code = ((digest.readUInt32BE(digest[digest.length - 1] & 15) & 0x7fffffff) % 1_000_000).toString().padStart(6, "0");
  try { await page.getByLabel("二次认证验证码", { exact: true }).fill(code); }
  catch { throw new Error("Unable to enter synthetic login proof"); }
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "账户概览", exact: true })).toBeVisible();
}

export async function responseValue(response: Response, actor: number) {
  expect(response.status()).toBe(200);
  expect(response.headers()["x-adtr-user-id"]).toBe(String(actor));
  expect(response.headers()["cache-control"]).toBe("no-store");
  return directoryV2ResponseJSON(response);
}

export async function assetAction(page: Page, actor: number, action: () => Promise<unknown>) {
  const [response] = await Promise.all([
    page.waitForResponse((r) => new URL(r.url()).pathname === "/api/group-assets/v3" && r.request().method() === "GET"), action(),
  ]);
  return responseValue(response, actor);
}

export async function openAssets(page: Page, actor: number) {
  await navigateTo(page, "组资产");
  await expect(page).toHaveURL(/#group-assets-v3$/u);
  await page.getByRole("radio", { name: "选择数据源 synthetic.invalid", exact: true }).check();
  return assetAction(page, actor, () => page.getByRole("button", { name: "核对并打开连接详情", exact: true }).click());
}

async function databaseJSON(sql: string) {
  const container = process.env.ADTR_E2E_DB_CONTAINER ?? "";
  if (!/^adtr-auth-e2e-[0-9a-f]{12}$/u.test(container)) throw new Error("Owned isolated database required");
  let stdout: string;
  try {
    ({ stdout } = await run("docker", ["exec", "-e", "PGOPTIONS=-c statement_timeout=2000 -c default_transaction_read_only=on", container, "psql", "-U", "postgres", "-d", "adtr_e2e", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", sql], { encoding: "utf8", timeout: 5000, maxBuffer: 2 << 20 }));
  } catch { throw new Error("Read-only isolated export evidence query failed"); }
  if (!stdout.trim()) throw new Error("Missing persisted export evidence");
  return JSON.parse(stdout);
}

export async function storedExport(taskID: string) {
  if (!/^[A-Za-z0-9_.-]{1,128}$/u.test(taskID)) throw new Error("Invalid export task identity");
  return databaseJSON(`SELECT jsonb_build_object('taskID',t.task_id,'actorId',t.actor_id::text,'state',t.state,'kind',t.kind,'progress',t.progress,'attempt',t.attempt,'maxAttempts',t.max_attempts,'sourceSHA256',s.source_sha256,'rowCount',m.row_count,'byteCount',m.byte_count,'sha256',m.sha256,'chunkCount',m.chunk_count,'chunks',(SELECT jsonb_agg(jsonb_build_object('ordinal',c.chunk_no,'bodyBase64',replace(encode(c.data,'base64'),chr(10),''),'sha256',c.sha256) ORDER BY c.chunk_no) FROM adtr.group_asset_export_chunks c WHERE c.task_id=t.task_id AND c.fencing_token=m.fencing_token),'events',(SELECT jsonb_agg(jsonb_build_object('action',e.action,'state',e.state,'resultVersion',e.result_version) ORDER BY e.id) FROM adtr.task_events e WHERE e.task_id=t.task_id)) FROM adtr.tasks t JOIN adtr.group_asset_export_snapshots s ON s.task_id=t.task_id JOIN adtr.group_asset_export_manifests m ON m.task_id=t.task_id AND m.fencing_token=(t.result->>'artifactToken')::bigint WHERE t.tenant_id='default' AND t.task_id='${taskID}'`);
}

export async function sensitiveBudget(actor: number, producer: number) {
  if (![actor, producer].every((id) => Number.isSafeInteger(id) && id > 0)) throw new Error("Invalid fixture actors");
  const ownerBucket = hash(`sensitive:${actor}`), producerBucket = hash(`sensitive:${producer}`);
  return databaseJSON(`SELECT jsonb_build_object('owner',(SELECT count FROM adtr.auth_attempts WHERE bucket='${ownerBucket}'),'producer',(SELECT count FROM adtr.auth_attempts WHERE bucket='${producerBucket}'),'observations',(SELECT count(*) FROM adtr.domain_directory_observations WHERE tenant_id='default'),'directoryUses',(SELECT count(*) FROM adtr.domain_directory_task_uses WHERE tenant_id='default'),'exportTasks',(SELECT count(*) FROM adtr.tasks WHERE tenant_id='default' AND kind='group_assets.v3.export'))`);
}

export function evidenceProvenance() {
  const root = execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
  return JSON.parse(execFileSync("python3", ["scripts/validate_group_asset_export_v3_evidence.py", "--capture-provenance", "--repo", root], { cwd: root, encoding: "utf8", maxBuffer: 2 << 20 }));
}
