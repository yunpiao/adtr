// Synthetic CI evidence only. No request body, credential, raw ledger tuple,
// environment dump or arbitrary attachment is admitted to this closed envelope.
import type { TestInfo } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstat, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

type Suite = "controls" | "cancel" | "readers";
type Check = boolean | number | string;
type Contract = { schemaVersion: number; sourceRoots: string[]; sourceFiles: string[]; suites: Record<Suite, { checks: Record<string, Check | string[]>; screenshots: Record<string, [number, number]> }> };
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const contractPath = "scripts/fixtures/directory_v4_controls_evidence.json";
const sha256 = (bytes: Uint8Array | string) => createHash("sha256").update(bytes).digest("hex");
const fail = (): never => { throw new Error("Dictionary4 evidence rejected"); };
const git = (...args: string[]) => execFileSync("git", ["-C", root, ...args], { encoding: "utf8", timeout: 10_000, maxBuffer: 4 * 1024 * 1024, stdio: ["ignore", "pipe", "pipe"] }).trimEnd();
const safePath = (value: string) => /^[A-Za-z0-9_./-]+$/u.test(value) && !value.split("/").some(part => part === ".." || part === "." || part === "") && !value.startsWith("/");
const selected = (path: string, contract: Contract) => contract.sourceFiles.includes(path) || contract.sourceRoots.some(prefix => path.startsWith(prefix));
async function contract(): Promise<Contract> {
  return JSON.parse(await readFile(join(root, contractPath), "utf8")) as Contract;
}
export async function directoryV4EvidenceProvenance() {
  const rules = await contract();
  const checkoutCommit = git("rev-parse", "HEAD"), checkoutTree = git("rev-parse", "HEAD^{tree}");
  const env = process.env;
  if (env.GITHUB_ACTIONS !== "true") fail(); // CI artifacts are never local-runtime claims.
  const eventHeadCommit = env.ADTR_EVENT_HEAD_SHA, eventBaseCommit = env.ADTR_EVENT_BASE_SHA;
  const workflowCommit = env.GITHUB_SHA, runId = env.GITHUB_RUN_ID, runAttempt = env.GITHUB_RUN_ATTEMPT, eventName = env.GITHUB_EVENT_NAME;
  if (![checkoutCommit, checkoutTree, eventHeadCommit, eventBaseCommit, workflowCommit].every(v => typeof v === "string" && /^[0-9a-f]{40}$/u.test(v)) || workflowCommit !== checkoutCommit || !/^[1-9][0-9]*$/u.test(runId ?? "") || !/^[1-9][0-9]*$/u.test(runAttempt ?? "") || !["pull_request", "push", "merge_group", "workflow_dispatch"].includes(eventName ?? "")) fail();
  if (git("diff", "HEAD", "--", ...rules.sourceRoots, ...rules.sourceFiles)) fail();
  const all = git("ls-files", "-z").split("\0").filter(Boolean);
  const paths = all.filter(path => selected(path, rules)).sort();
  if (!paths.length || paths.length > 4096 || rules.sourceFiles.some(path => !paths.includes(path)) || paths.some(path => !safePath(path))) fail();
  const untracked = git("ls-files", "--others", "--exclude-standard", "-z").split("\0").filter(Boolean);
  if (untracked.some(path => selected(path, rules))) fail();
  const sourceDigests: Record<string, string> = {};
  for (const path of paths) {
    const file = join(root, path), stat = await lstat(file);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 16 * 1024 * 1024) fail();
    sourceDigests[path] = sha256(await readFile(file));
  }
  return { checkoutCommit, checkoutTree, eventHeadCommit, eventBaseCommit, workflowCommit, runId, runAttempt, eventName, sourceDigests };
}
export async function writeV4ScenarioEvidence(info: TestInfo, suite: Suite, checks: Record<string, Check>, witnesses: Record<string, unknown>) {
  const rules = (await contract()).suites[suite];
  if (!rules || Object.keys(checks).sort().join("\0") !== Object.keys(rules.checks).sort().join("\0")) fail();
  for (const [key, expected] of Object.entries(rules.checks)) {
    const value = checks[key];
    if (Array.isArray(expected) ? typeof value !== "string" || !expected.includes(value) : value !== expected) fail();
  }
  const screenshots: Record<string, { sha256: string; bytes: number; width: number; height: number }> = {};
  for (const [name, [width, height]] of Object.entries(rules.screenshots)) {
    const path = info.outputPath(name), stat = await lstat(path);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 10 * 1024 * 1024) fail();
    const raw = await readFile(path);
    if (raw.length < 33 || raw.subarray(0, 8).toString("hex") !== "89504e470d0a1a0a" || raw.toString("ascii", 12, 16) !== "IHDR" || raw.readUInt32BE(16) !== width || raw.readUInt32BE(20) !== height) fail();
    screenshots[name] = { sha256: sha256(raw), bytes: raw.length, width, height };
  }
  // The post-run Python validator independently checks every witness field,
  // relationship, digest and PNG before any scenario artifact can be uploaded.
  const output = { schemaVersion: 1, suite: `directory-v4-${suite}`, checks, witnesses, provenance: await directoryV4EvidenceProvenance(), screenshots };
  const encoded = JSON.stringify(output, null, 2);
  if (Buffer.byteLength(encoded, "utf8") > 1024 * 1024) fail();
  await writeFile(info.outputPath(`directory-v4-${suite}-scenario.json`), encoded, { flag: "wx" });
}
