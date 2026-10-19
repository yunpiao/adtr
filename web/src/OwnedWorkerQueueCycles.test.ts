import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";
import ts from "typescript";

// Execute the actual helper body with only in-memory doubles. No browser,
// database, process identity inspection, signals or timer sleeps are involved.
function fixture(kind: string, successes: number[], before = 10) {
  // npm test --prefix web runs from web; jsdom/Vite may rewrite import.meta.url.
  const source = readFileSync(resolve(process.cwd(), "e2e", `${kind}-asset-export-v3-terminal-helpers.ts`), "utf8");
  const marker = "export async function resumeOwnedExportWorker(";
  expect(source.split(marker)).toHaveLength(2);
  const body = source.slice(source.indexOf(marker)).replace("export async", "async");
  const javascript = ts.transpileModule(body, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
  }).outputText;
  const queueActivity = vi.fn(async () => {
    const success = successes.shift();
    if (success === undefined) throw new Error("unexpected queue observation");
    return { workerID: "worker", success };
  });
  const timeouts: number[] = [];
  const pollingExpect = Object.assign((value: unknown) => expect(value), {
    poll: (read: () => Promise<unknown>, options: { timeout: number }) => ({
      toBe: async (value: unknown) => {
        timeouts.push(options.timeout);
        expect(await read()).toBe(value);
      },
    }),
  });
  const restore = vi.fn(async () => {});
  const worker = { states: ["S"], pid: 1, starttime: "one", nonce: "nonce", readyURL: "unused" };
  const collect = new Function("restoreOwnedExportWorker", "checkedWorker", "queueActivity", "activityAlias", "hash", "expect",
    `${javascript}\nreturn resumeOwnedExportWorker;`)(restore, async () => worker, queueActivity, () => "alias", (value: unknown) => value, pollingExpect);
  const context = { request: { get: vi.fn(async () => ({ status: () => 200 })) } };
  const pause = { pid: 1, starttime: "one", nonceSHA256: "nonce", workerID: "worker", queueSuccessBefore: before };
  return { collect: (pending?: number) => collect(context, pause, pending === undefined ? undefined : "task", pending), queueActivity, restore, timeouts };
}

for (const kind of ["user", "group", "computer"]) {
  describe(`${kind} worker queue recovery boundary`, () => {
    it("requires the first cycle strictly after both pause and pending", async () => {
      for (const stale of [15, 20]) {
        const test = fixture(kind, [stale]);
        await expect(test.collect(20)).rejects.toThrow();
        expect(test.queueActivity).toHaveBeenCalledTimes(1);
      }
      const pauseIsLater = fixture(kind, [21], 30);
      await expect(pauseIsLater.collect(20)).rejects.toThrow();
    });

    it("returns the original two strictly newer observations without extending polling limits", async () => {
      const test = fixture(kind, [21, 21, 22, 22]);
      await expect(test.collect(20)).resolves.toMatchObject({ queueSuccessAfterFirst: 21, queueSuccessAfter: 22 });
      expect(test.queueActivity).toHaveBeenCalledTimes(4);
      expect(test.queueActivity).toHaveBeenCalledWith({ workerID: "worker", alias: "alias" });
      expect(test.restore).toHaveBeenCalledTimes(1);
      expect(test.timeouts).toEqual([5000, 10000, 15000, 20000]);
    });

    it("rechecks the actual returned samples after the polls", async () => {
      for (const observations of [[21, 20], [21, 21, 22, 21]]) {
        const test = fixture(kind, observations);
        await expect(test.collect(20)).rejects.toThrow();
      }
    });

    it("preserves the queued caller without a pending bound", async () => {
      const test = fixture(kind, [11, 11, 12, 12]);
      await expect(test.collect()).resolves.toMatchObject({ queueSuccessAfterFirst: 11, queueSuccessAfter: 12 });
      expect(test.queueActivity).toHaveBeenCalledWith(undefined);
      expect(test.timeouts).toEqual([5000, 10000, 15000, 20000]);
    });
  });
}
