// Authored under local execution HOLD. Native browser completion still requires CI.
import { describe, expect, it } from "vitest";
import { directoryV4ControlsBodyLimit, parseDirectoryV4UniqueJSON, readDirectoryV4ControlsJSON } from "./directory-v4-controls-json";
const signal = () => new AbortController().signal;
describe("v4 bounded native reader", () => {
  it.each(['{"x":1,"x":2}', '{"x":1,"\\u0078":2}', '{"a":[{"x":1,"x":2}]}'])('rejects duplicate keys: %s', text => {
    expect(() => parseDirectoryV4UniqueJSON(text)).toThrow();
  });
  it("permits equal keys in separate objects and braces inside strings", () => {
    expect(parseDirectoryV4UniqueJSON('[{"x":"{}[]\\\""},{"x":2}]')).toEqual([{ x: '{}[]"' }, { x: 2 }]);
  });
  it("rejects excessive depth and malformed JSON", () => {
    expect(() => parseDirectoryV4UniqueJSON('['.repeat(65) + '0' + ']'.repeat(65))).toThrow();
    expect(() => parseDirectoryV4UniqueJSON('{"x":')).toThrow();
  });
  it("decodes UTF8 across native chunks without text/json fallback", async () => {
    const bytes = new TextEncoder().encode('{"x":"中文"}');
    const response = new Response(new ReadableStream({ start(c) {
      for (const byte of bytes) c.enqueue(new Uint8Array([byte])); c.close();
    } }));
    response.json = () => { throw new Error("unbounded json"); };
    response.text = () => { throw new Error("unbounded text"); };
    await expect(readDirectoryV4ControlsJSON(response, signal())).resolves.toEqual({ x: "中文" });
  });
  it("rejects truncated UTF8", async () => {
    await expect(readDirectoryV4ControlsJSON(new Response(new Uint8Array([0xe4, 0xb8])), signal())).rejects.toMatchObject({ code: "invalid_response" });
  });
  it.each(["-1", "oops", String(directoryV4ControlsBodyLimit + 1)])("rejects invalid length %s", async length => {
    await expect(readDirectoryV4ControlsJSON(new Response('{}', { headers: { "Content-Length": length } }), signal())).rejects.toMatchObject({ code: "directory_limit_exceeded" });
  });
  it("bounds actual chunks regardless of declared length", async () => {
    const response = new Response(new ReadableStream({ start(c) { c.enqueue(new Uint8Array(directoryV4ControlsBodyLimit + 1)); c.close(); } }), { headers: { "Content-Length": "2" } });
    await expect(readDirectoryV4ControlsJSON(response, signal())).rejects.toMatchObject({ code: "directory_limit_exceeded" });
  });
  it("rejects abort during completion even if transport ignores it", async () => {
    const controller = new AbortController();
    const response = new Response(new ReadableStream({ pull(c) { c.enqueue(new TextEncoder().encode('{}')); c.close(); controller.abort(); } }));
    await expect(readDirectoryV4ControlsJSON(response, controller.signal)).rejects.toThrow();
  });
});
