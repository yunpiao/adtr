import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import ts from "typescript";
import { describe, expect, it, vi } from "vitest";

// npm test --prefix web runs from web; jsdom/Vite may rewrite import.meta.url.
const source = readFileSync(resolve(process.cwd(), "e2e", "directory-v4-cancel.spec.ts"), "utf8");
const tree = ts.createSourceFile("cancel.ts", source, ts.ScriptTarget.Latest, true);
const nodes: ts.Node[] = [];
function visit(node: ts.Node) { nodes.push(node); ts.forEachChild(node, visit); }
visit(tree);
function unique(predicate: (node: ts.Node) => boolean) {
  const found = nodes.filter(predicate);
  if (found.length !== 1) throw new Error("Heartbeat contract source shape changed");
  return found[0].getText(tree);
}
const collector = unique(node => ts.isFunctionDeclaration(node) && node.name?.text === "cancelHeartbeatDiagnostics");
const state = unique(node => ts.isVariableStatement(node) && node.declarationList.declarations.some(declaration => ts.isIdentifier(declaration.name) && declaration.name.text === "heartbeat"));
const diagnostics = unique(node => ts.isVariableStatement(node) && node.declarationList.declarations.some(declaration => ts.isIdentifier(declaration.name) && declaration.name.text === "heartbeatDiagnostics"));
const poll = unique(node => ts.isTryStatement(node) && node.catchClause?.getText(tree).includes("heartbeatDiagnostics.report()") === true);
function compile(body: string, names: string[]) {
  const javascript = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText;
  return new Function(...names, javascript);
}
const secret = "private-uuid-token-proof-error";
const first = { resultVersion: 11, updatedAt: "2026-01-01T00:00:00Z", state: "running", progress: 41, taskUUID: secret, token: secret, proof: secret };
function task(overrides: Partial<typeof first> = {}) { return { ...first, ...overrides }; }
function setup() {
  const log = vi.fn();
  const collectorFactory = compile(`${collector}; return cancelHeartbeatDiagnostics;`, ["console"])({ log });
  return { log, collectorFactory };
}
function decoded(log: ReturnType<typeof vi.fn>) {
  return JSON.parse(log.mock.calls[0][0].slice("[v4-cancel-heartbeat] ".length));
}

describe("cancel heartbeat bounded private diagnostics", () => {
  it("emits only closed scalars, clamps invalid numbers, and keeps the last 16 samples", () => {
    const { log, collectorFactory } = setup(), buffer = collectorFactory(100);
    for (let index = 0; index < 20; index++) buffer.record(first, task({ updatedAt: secret, state: secret }), Infinity);
    expect(log).not.toHaveBeenCalled();
    buffer.report();
    const samples = decoded(log);
    expect(samples).toHaveLength(16);
    expect(samples[0].sample).toBe(5);
    expect(samples[15].sample).toBe(20);
    for (const sample of samples) {
      expect(sample).toEqual({ sample: expect.any(Number), elapsedMs: 0, sameVersion: true, updatedAt: "invalid", runningProgress: "outside-window" });
    }
    const bounded = collectorFactory(0);
    bounded.record(first, first, -10);
    bounded.record(first, first, 999_999_999);
    bounded.record(first, first, NaN);
    bounded.report();
    const limits = JSON.parse(log.mock.calls[1][0].slice("[v4-cancel-heartbeat] ".length));
    expect(limits.map((sample: { elapsedMs: number }) => sample.elapsedMs)).toEqual([0, 900_000, 0]);
    expect(JSON.stringify(log.mock.calls)).not.toContain(secret);
    expect(JSON.stringify(log.mock.calls)).not.toContain(first.updatedAt);
  });
  it("distinguishes equality, regression, advancement and real version changes without raw values", () => {
    const { log, collectorFactory } = setup(), buffer = collectorFactory(100);
    buffer.record(first, first, 101.9);
    buffer.record(first, task({ updatedAt: "2025-01-01T00:00:00Z" }), 102);
    buffer.record(first, { ...task({ updatedAt: "2027-01-01T00:00:00Z" }), resultVersion: secret }, 103);
    buffer.report();
    expect(decoded(log)).toEqual([
      { sample: 1, elapsedMs: 1, sameVersion: true, updatedAt: "unchanged", runningProgress: "running-41" },
      { sample: 2, elapsedMs: 2, sameVersion: true, updatedAt: "regressed", runningProgress: "running-41" },
      { sample: 3, elapsedMs: 3, sameVersion: false, updatedAt: "advanced", runningProgress: "running-41" },
    ]);
    expect(JSON.stringify(log.mock.calls)).not.toContain(secret);
  });
});

async function exercise(samples: Array<ReturnType<typeof task> | Error>, loggingFails = false) {
  const log = vi.fn(() => { if (loggingFails) throw new Error("diagnostic output unavailable"); });
  const failure = new Error("unchanged heartbeat deadline");
  let index = 0;
  const queryUI = vi.fn(async () => { const next = samples[index++]; if (next instanceof Error) throw next; return next; });
  const pollMock = vi.fn((callback: () => Promise<boolean>, options: unknown) => ({
    async toBe(value: boolean) {
      expect(options).toEqual({ timeout: 3_000, intervals: [100] });
      expect(value).toBe(true);
      for (let attempt = 0; attempt < samples.length; attempt++) if (await callback()) return;
      throw failure;
    },
  }));
  const execute = compile(`${collector}; return (async () => { ${state}\n${diagnostics}\n${poll}\nreturn { heartbeat, heartbeatFirst }; })();`, ["first", "queryUI", "page", "actor", "running", "invariant", "expect", "performance", "console"]);
  const invariant = (ok: boolean, message: string) => { if (!ok) throw new Error(message); };
  let result: unknown, error: unknown;
  try { result = await execute(first, queryUI, {}, {}, {}, invariant, { poll: pollMock }, { now: () => 100 }, { log }); }
  catch (caught) { error = caught; }
  return { result, error, failure, log, queryUI, pollMock };
}

describe("unchanged real heartbeat witness and failure propagation", () => {
  it("still rejects same-version unchanged time and version-changing newer time", async () => {
    for (const sample of [task(), task({ resultVersion: 12, updatedAt: "2027-01-01T00:00:00Z" })]) {
      const run = await exercise([sample]);
      expect(run.error).toBe(run.failure);
      expect(run.queryUI).toHaveBeenCalledTimes(1);
      expect(run.log).toHaveBeenCalledTimes(1);
      expect(JSON.stringify(run.log.mock.calls)).not.toContain(secret);
    }
  });
  it("accepts only a genuine same-version newer-time pair, with no success logging", async () => {
    const sample = task({ updatedAt: "2027-01-01T00:00:00Z" });
    const run = await exercise([sample]);
    expect(run.error).toBeUndefined();
    expect(run.result).toEqual({ heartbeat: sample, heartbeatFirst: first });
    expect(run.queryUI).toHaveBeenCalledTimes(1);
    expect(run.log).not.toHaveBeenCalled();
  });
  it("keeps the rolling previous sample and rejects progress or state outside the window", async () => {
    const changed = task({ resultVersion: 12, updatedAt: "2027-01-01T00:00:00Z" });
    const later = task({ resultVersion: 12, updatedAt: "2027-01-01T00:00:01Z" });
    const success = await exercise([changed, later]);
    expect(success.result).toEqual({ heartbeat: later, heartbeatFirst: changed });
    for (const sample of [task({ progress: 42 }), task({ state: secret })]) {
      const run = await exercise([sample]);
      expect(run.error).toBeInstanceOf(Error);
      expect((run.error as Error).message).toBe("Missed genuine stable-page heartbeat window");
      expect(decoded(run.log)[0].runningProgress).toBe("outside-window");
    }
  });
  it("propagates original query errors and deadline failures even if diagnostic logging fails", async () => {
    const original = new Error(secret);
    for (const loggingFails of [false, true]) {
      const run = await exercise([original], loggingFails);
      expect(run.error).toBe(original);
      expect(run.queryUI).toHaveBeenCalledTimes(1);
      expect(JSON.stringify(run.log.mock.calls)).not.toContain(secret);
      const timeout = await exercise([task()], loggingFails);
      expect(timeout.error).toBe(timeout.failure);
    }
  });
});
