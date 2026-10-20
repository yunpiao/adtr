import { ApiError } from "./api";

export const directoryV4ControlsBodyLimit = 8 * 1024 * 1024;

// Decode escaped keys before comparison. JSON.parse still checks full grammar.
// The depth budget also bounds the structural scanner's auxiliary memory.
export function parseDirectoryV4UniqueJSON(text: string): unknown {
  const stack: (Set<string> | null)[] = [];
  for (let index = 0; index < text.length; index++) {
    const char = text[index];
    if (char === "{" || char === "[") {
      if (stack.length >= 64) throw new ApiError("invalid_response");
      stack.push(char === "{" ? new Set() : null);
    } else if (char === "}" || char === "]") stack.pop();
    else if (char === '"') {
      const start = index++;
      while (index < text.length) {
        if (text[index] === "\\") index += 2;
        else if (text[index] === '"') break;
        else index++;
      }
      let after = index + 1;
      while (after < text.length && /\s/u.test(text[after])) after++;
      if (text[after] === ":") {
        const key: unknown = JSON.parse(text.slice(start, index + 1));
        const keys = stack[stack.length - 1];
        if (typeof key !== "string" || !keys || keys.has(key))
          throw new ApiError("invalid_response");
        keys.add(key);
      }
    }
  }
  try { return JSON.parse(text) as unknown; }
  catch { throw new ApiError("invalid_response"); }
}

// Native streaming is required: never replace with response.text/json, including
// no-store responses. Check abort after each read AND at completed EOF.
export async function readDirectoryV4ControlsJSON(
  response: Response, signal: AbortSignal,
): Promise<unknown> {
  signal.throwIfAborted();
  const length = response.headers.get("Content-Length");
  if (length !== null && (!/^\d+$/u.test(length) || Number(length) > directoryV4ControlsBodyLimit)) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError("directory_limit_exceeded", response.status);
  }
  if (!response.body) throw new ApiError("invalid_response", response.status);
  const reader = response.body.getReader();
  const abort = () => { void reader.cancel().catch(() => undefined); };
  signal.addEventListener("abort", abort, { once: true });
  let size = 0;
  let text = "";
  try {
    const decoder = new TextDecoder("utf-8", { fatal: true });
    while (true) {
      signal.throwIfAborted();
      const next = await reader.read();
      signal.throwIfAborted();
      if (next.done) break;
      if (next.value.byteLength > directoryV4ControlsBodyLimit - size)
        throw new ApiError("directory_limit_exceeded", response.status);
      size += next.value.byteLength;
      text += decoder.decode(next.value, { stream: true });
    }
    text += decoder.decode();
    signal.throwIfAborted();
    return parseDirectoryV4UniqueJSON(text);
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    signal.throwIfAborted();
    if (error instanceof ApiError) throw new ApiError(error.code, response.status);
    throw new ApiError("invalid_response", response.status);
  } finally {
    signal.removeEventListener("abort", abort);
    reader.releaseLock();
  }
}
