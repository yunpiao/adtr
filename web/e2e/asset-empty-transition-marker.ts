import { lstat, link, realpath, unlink, writeFile } from "node:fs/promises";
import { basename, isAbsolute, join } from "node:path";

// This one-way marker is owned by the disposable test harness. It never changes
// application source configuration or exposes a network mutation endpoint.
export async function publishEmptyFixtureMarker() {
  const directory = process.env.ADTR_E2E_LDAP_EMPTY_TRANSITION_DIR ?? "";
  if (
    !isAbsolute(directory) ||
    !/^adtr-ldap-empty-transition-[A-Za-z0-9_-]+$/u.test(basename(directory))
  )
    throw new Error("Owned empty-transition fixture directory required");
  const info = await lstat(directory);
  if (
    !info.isDirectory() ||
    info.isSymbolicLink() ||
    (await realpath(directory)) !== directory
  )
    throw new Error("Unsafe empty-transition fixture directory");
  const pending = join(directory, ".empty-v1.pending");
  await writeFile(pending, "empty-v1\n", { flag: "wx", mode: 0o600 });
  try {
    // Hard-link publication is atomic and fails if the final marker exists.
    // The LDAP process sees either no file or the complete immutable content.
    await link(pending, join(directory, "empty-v1"));
  } finally {
    await unlink(pending);
  }
}
