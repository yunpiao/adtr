// The checkout is the independent oracle; never infer this expectation from API/DB data.
import { closeSync, openSync, readSync } from "node:fs";
import { resolve } from "node:path";

const maxSourceBytes = 4096;
export function parseCurrentSchema(source: Buffer): number {
  if (source.length > maxSourceBytes || source.some((byte) => byte > 127)) {
    throw new Error("invalid application schema source size or encoding");
  }
  const text = source.toString("ascii").replace(/^[ \t]*\/\/[^\r\n]*/gm, "");
  const match = /^\s*package schemaversion\s+const Current = ([1-9][0-9]{0,5})\s*$/.exec(text);
  if (!match) throw new Error("invalid application schema declaration");
  return Number(match[1]);
}

function currentSchema(): number {
  // This is checkout data, not a web module. Changes under internal/ already
  // require full CI coverage; keep the module graph free of unresolved URLs.
  const descriptor = openSync(resolve(import.meta.dirname, "../../internal/schemaversion/version.go"), "r");
  try {
    const source = Buffer.alloc(maxSourceBytes + 1);
    const length = readSync(descriptor, source, 0, source.length, 0);
    return parseCurrentSchema(source.subarray(0, length));
  } finally {
    closeSync(descriptor);
  }
}

export const currentApplicationSchema = currentSchema();
