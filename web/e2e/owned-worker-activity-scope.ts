import { createHash } from "node:crypto";

const alphabet = "abcdefghijklmnopqrstuvwxyz234567";

/** Private CI identity; callers must never log the returned key. */
export function activityAlias(nonceHex: string, taskID: string): string {
  if (
    typeof nonceHex !== "string" ||
    nonceHex.length !== 32 ||
    /[^0-9a-f]/.test(nonceHex) ||
    typeof taskID !== "string" ||
    taskID.length < 1 ||
    taskID.length > 128 ||
    /[^A-Za-z0-9_.:-]/.test(taskID)
  ) {
    throw new Error("invalid owned activity identity");
  }
  const digest = createHash("sha256")
    .update("adtr-ci-activity-v1\0", "utf8")
    .update(Buffer.from(nonceHex, "hex"))
    .update("\0", "utf8")
    .update(taskID, "utf8")
    .digest();
  let bits = 0;
  let accumulator = 0;
  let encoded = "";
  for (const byte of digest) {
    accumulator = (accumulator << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      encoded += alphabet[(accumulator >>> bits) & 31];
    }
    accumulator &= (1 << bits) - 1;
  }
  if (bits > 0) encoded += alphabet[(accumulator << (5 - bits)) & 31];
  return "ci-activity-" + encoded;
}

/** Only the two exact expected keys may exist in the observed activity scope. */
export function assertActivityScope(value: unknown): void {
  try {
    if (
      value === null ||
      typeof value !== "object" ||
      Object.getPrototypeOf(value) !== Object.prototype
    ) {
      throw new Error();
    }
    const keys = Reflect.ownKeys(value);
    if (
      keys.length !== 3 ||
      !keys.every(
        (key) => key === "original" || key === "alias" || key === "other",
      )
    ) {
      throw new Error();
    }
    const counts = value as Record<string, unknown>;
    if (
      !keys.every((key) => {
        const descriptor = Object.getOwnPropertyDescriptor(value, key);
        return (
          descriptor &&
          "value" in descriptor &&
          Number.isSafeInteger(descriptor.value)
        );
      }) ||
      counts.original !== 1 ||
      counts.alias !== 1 ||
      counts.other !== 0
    ) {
      throw new Error();
    }
  } catch {
    throw new Error("invalid owned activity scope");
  }
}
