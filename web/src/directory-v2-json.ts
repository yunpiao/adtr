import { ApiError } from "./api";

export const directoryV2BodyLimit = 8 * 1024 * 1024;

// Bound received bytes before JSON parsing, including chunked/incorrect-length
// responses. Never fall back to an unbounded response.json()/text() read.
export async function readDirectoryV2JSON(
  response: Response,
  signal: AbortSignal,
): Promise<unknown> {
  signal.throwIfAborted();
  const length = response.headers.get("Content-Length");
  if (
    length !== null &&
    (!/^\d+$/u.test(length) || Number(length) > directoryV2BodyLimit)
  ) {
    await response.body?.cancel().catch(() => undefined);
    throw new ApiError("directory_limit_exceeded", response.status);
  }
  if (!response.body) throw new ApiError("invalid_response", response.status);
  const reader = response.body.getReader();
  const abort = () => {
    void reader.cancel().catch(() => undefined);
  };
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
      if (next.value.byteLength > directoryV2BodyLimit - size)
        throw new ApiError("directory_limit_exceeded", response.status);
      size += next.value.byteLength;
      text += decoder.decode(next.value, { stream: true });
    }
    text += decoder.decode();
    return JSON.parse(text) as unknown;
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    signal.throwIfAborted();
    if (error instanceof ApiError) throw error;
    throw new ApiError("invalid_response", response.status);
  } finally {
    signal.removeEventListener("abort", abort);
    reader.releaseLock();
  }
}

// Presentation only: retain raw values in the parsed object and show characters
// that can hide/reorder content visibly. JSX always receives a plain text node.
export function directoryV2DisplayText(value: string): string {
  return value.replace(/[\p{Cc}\p{Cf}\u2028\u2029]/gu, (character) => {
    const point = character.codePointAt(0)!;
    return point <= 0xffff
      ? `\\u${point.toString(16).padStart(4, "0")}`
      : `\\u{${point.toString(16)}}`;
  });
}
