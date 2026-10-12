// Test-only filesystem control; never imported by the application bundle.
import { afterEach, describe, expect, it, vi } from "vitest";
import { mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { publishEmptyFixtureMarker } from "../e2e/asset-empty-transition-marker";

const directories: string[] = [];
afterEach(async () => {
  vi.unstubAllEnvs();
  for (const path of directories.splice(0))
    await rm(path, { recursive: true, force: true });
});
async function directory(prefix = "adtr-ldap-empty-transition-") {
  const path = await mkdtemp(join(tmpdir(), prefix));
  directories.push(path);
  vi.stubEnv("ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR", path);
  return path;
}
describe("owned one-way empty fixture marker", () => {
  it("atomically publishes the exact marker and rejects a second transition without overwriting", async () => {
    const path = await directory();
    await publishEmptyFixtureMarker();
    expect(await readFile(join(path, "empty-v1"), "utf8")).toBe("empty-v1\n");
    await expect(readFile(join(path, ".empty-v1.pending"))).rejects.toThrow();
    await expect(publishEmptyFixtureMarker()).rejects.toThrow();
    expect(await readFile(join(path, "empty-v1"), "utf8")).toBe("empty-v1\n");
    await expect(readFile(join(path, ".empty-v1.pending"))).rejects.toThrow();
  });
  it("rejects an absent, relative or unrelated directory", async () => {
    for (const value of [
      "",
      "adtr-ldap-empty-transition-relative",
      await directory("unrelated-"),
    ]) {
      vi.stubEnv("ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR", value);
      await expect(publishEmptyFixtureMarker()).rejects.toThrow(
        "Owned empty-transition",
      );
    }
  });
  it("rejects symlink directory even with an allowed basename", async () => {
    const parent = await directory();
    const child = join(parent, "adtr-ldap-empty-transition-link");
    await symlink(parent, child);
    vi.stubEnv("ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR", child);
    await expect(publishEmptyFixtureMarker()).rejects.toThrow(
      "Unsafe empty-transition",
    );
  });
  it("rejects existing staging data without changing it", async () => {
    const path = await directory();
    await writeFile(join(path, ".empty-v1.pending"), "existing");
    await expect(publishEmptyFixtureMarker()).rejects.toThrow();
    expect(await readFile(join(path, ".empty-v1.pending"), "utf8")).toBe(
      "existing",
    );
    await expect(readFile(join(path, "empty-v1"))).rejects.toThrow();
  });
  it("does not follow an existing final-marker symlink", async () => {
    const path = await directory();
    const target = join(path, "target");
    await writeFile(target, "untouched");
    await symlink(target, join(path, "empty-v1"));
    await expect(publishEmptyFixtureMarker()).rejects.toThrow();
    expect(await readFile(target, "utf8")).toBe("untouched");
  });
});
