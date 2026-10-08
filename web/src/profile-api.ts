import { ApiError, messages, type Profile } from "./api";
import type { AccessUser } from "./access-api";

export const avatarPath = "/api/profile/avatar";
export const maxUploadBytes = 2 * 1024 * 1024;
export const maxAvatarBytes = 4 * 1024 * 1024;
const textFields = [
  "username",
  "passStrength",
  "role",
  "mobile",
  "email",
  "remark",
  "createTm",
  "pwdUpdateTm",
  "address",
  "realName",
  "department",
  "post",
  "roleID",
  "roleName",
] as const;

export function parsePersonalProfile(
  value: unknown,
  owner: number,
): AccessUser {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new ApiError("invalid_response");
  const record = value as Record<string, unknown>;
  if (
    Number.isSafeInteger(owner) &&
    owner > 0 &&
    Number.isSafeInteger(record.ID) &&
    record.ID !== owner
  )
    throw new ApiError("profile_identity_changed");
  if (
    !Number.isSafeInteger(owner) ||
    owner <= 0 ||
    record.ID !== owner ||
    !Number.isSafeInteger(record.priv) ||
    typeof record.hasMfa !== "boolean" ||
    record.disabled !== false ||
    (record.avatar !== "" && record.avatar !== avatarPath) ||
    textFields.some(
      (key) =>
        typeof record[key] !== "string" ||
        (record[key] as string).length > 4096,
    )
  )
    throw new ApiError("invalid_response");
  // Copy only contract fields. Unknown server properties never become view state.
  return Object.fromEntries([
    ...["ID", "priv", "hasMfa", "disabled", "avatar"].map((key) => [
      key,
      record[key],
    ]),
    ...textFields.map((key) => [key, record[key]]),
  ]) as unknown as AccessUser;
}

async function boundedBytes(
  response: Response,
  limit: number,
  signal: AbortSignal,
): Promise<Uint8Array> {
  const length = response.headers.get("Content-Length");
  if (length !== null && (!/^\d+$/u.test(length) || Number(length) > limit)) {
    await response.body?.cancel();
    throw new ApiError("invalid_response", response.status);
  }
  if (!response.body) throw new ApiError("invalid_response", response.status);
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      signal.throwIfAborted();
      const part = await reader.read();
      if (part.done) break;
      size += part.value.byteLength;
      if (size > limit) throw new ApiError("invalid_response", response.status);
      chunks.push(part.value);
    }
    signal.throwIfAborted();
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.byteLength;
    }
    return bytes;
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally {
    reader.releaseLock();
  }
}

async function json(response: Response, signal: AbortSignal): Promise<unknown> {
  try {
    if (
      response.headers
        .get("Content-Type")
        ?.split(";")[0]
        .trim()
        .toLowerCase() !== "application/json"
    )
      throw new Error();
    return JSON.parse(
      new TextDecoder("utf-8", { fatal: true }).decode(
        await boundedBytes(response, 128 * 1024, signal),
      ),
    );
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("invalid_response", response.status);
  }
}

async function fetchProfile(
  path: string,
  signal: AbortSignal,
  body?: { userId: number; file: string },
  csrfToken?: string,
  avatarOwner?: number,
): Promise<Response> {
  let response: Response;
  try {
    response = await fetch(path, {
      method: body ? "POST" : "GET",
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      signal,
      ...(body
        ? {
            headers: {
              "Content-Type": "application/json",
              "X-CSRF-Token": csrfToken ?? "",
            },
            body: JSON.stringify(body),
          }
        : {}),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new ApiError("network");
  }
  if (!response.ok) {
    const data = await json(response, signal);
    const code =
      data &&
      typeof data === "object" &&
      "error" in data &&
      typeof data.error === "string"
        ? data.error
        : "internal";
    if (
      avatarOwner !== undefined &&
      response.status === 404 &&
      code === "avatar_not_found" &&
      response.headers.get("X-Profile-User-ID") !== String(avatarOwner)
    )
      throw new ApiError("profile_identity_changed");
    throw new ApiError(code, response.status);
  }
  return response;
}

export function imageDimensions(bytes: Uint8Array): {
  width: number;
  height: number;
  type: "image/png" | "image/jpeg";
} {
  const invalid = () => {
    throw new ApiError("invalid_avatar");
  };
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  let width = 0,
    height = 0,
    type: "image/png" | "image/jpeg";
  if (
    bytes.length >= 33 &&
    [137, 80, 78, 71, 13, 10, 26, 10].every((n, i) => bytes[i] === n)
  ) {
    if (view.getUint32(8) !== 13 || view.getUint32(12) !== 0x49484452)
      return invalid();
    width = view.getUint32(16);
    height = view.getUint32(20);
    type = "image/png";
  } else if (bytes.length >= 4 && bytes[0] === 255 && bytes[1] === 216) {
    type = "image/jpeg";
    let offset = 2;
    while (offset + 3 < bytes.length) {
      if (bytes[offset++] !== 255) return invalid();
      while (bytes[offset] === 255) offset++;
      const marker = bytes[offset++];
      if (marker === 217 || marker === 218 || marker === undefined) break;
      if (marker === 1 || (marker >= 208 && marker <= 215)) continue;
      if (offset + 2 > bytes.length) return invalid();
      const size = view.getUint16(offset);
      if (size < 2 || offset + size > bytes.length) return invalid();
      if (
        [
          192, 193, 194, 195, 197, 198, 199, 201, 202, 203, 205, 206, 207,
        ].includes(marker)
      ) {
        if (size < 8) return invalid();
        height = view.getUint16(offset + 3);
        width = view.getUint16(offset + 5);
        break;
      }
      offset += size;
    }
  } else return invalid();
  if (!width || !height || width > 1024 || height > 1024) return invalid();
  return { width, height, type };
}

async function decodeImage(
  bytes: Uint8Array,
  signal: AbortSignal,
  pngOnly = false,
): Promise<ImageBitmap> {
  const config = imageDimensions(bytes);
  if (pngOnly && config.type !== "image/png")
    throw new ApiError("invalid_response");
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(
      new Blob([new Uint8Array(bytes)], { type: config.type }),
    );
  } catch {
    throw new ApiError(pngOnly ? "invalid_response" : "invalid_avatar");
  }
  // Browsers apply JPEG EXIF orientation during decode. Quarter turns swap
  // dimensions; both axes were already bounded before allocating the raster.
  const sameDimensions =
    bitmap.width === config.width && bitmap.height === config.height;
  const rotatedJpeg =
    config.type === "image/jpeg" &&
    bitmap.width === config.height &&
    bitmap.height === config.width;
  if (signal.aborted || (!sameDimensions && !rotatedJpeg)) {
    bitmap.close();
    signal.throwIfAborted();
    throw new ApiError(pngOnly ? "invalid_response" : "invalid_avatar");
  }
  return bitmap;
}

export async function prepareAvatar(
  file: File,
  signal: AbortSignal,
): Promise<Uint8Array> {
  if (
    file.size === 0 ||
    file.size > maxUploadBytes ||
    (file.type !== "" &&
      file.type !== "image/png" &&
      file.type !== "image/jpeg")
  )
    throw new ApiError("invalid_avatar");
  const bytes = new Uint8Array(await file.arrayBuffer());
  signal.throwIfAborted();
  if (!bytes.length || bytes.length > maxUploadBytes)
    throw new ApiError("invalid_avatar");
  const bitmap = await decodeImage(bytes, signal);
  bitmap.close();
  return bytes;
}

export function canonicalBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 32768)
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768));
  return btoa(binary);
}

export const profileAPI = {
  async me(owner: number, signal: AbortSignal): Promise<AccessUser> {
    return parsePersonalProfile(
      await json(await fetchProfile("/api/profile/me", signal), signal),
      owner,
    );
  },
  async avatar(
    owner: number,
    signal: AbortSignal,
  ): Promise<ImageBitmap | null> {
    let response: Response;
    try {
      response = await fetchProfile(
        avatarPath,
        signal,
        undefined,
        undefined,
        owner,
      );
    } catch (error) {
      if (
        error instanceof ApiError &&
        error.status === 404 &&
        error.code === "avatar_not_found"
      )
        return null;
      throw error;
    }
    if (response.headers.get("X-Profile-User-ID") !== String(owner)) {
      await response.body?.cancel();
      throw new ApiError("profile_identity_changed");
    }
    if (
      response.headers.get("Content-Type")?.trim().toLowerCase() !== "image/png"
    ) {
      await response.body?.cancel();
      throw new ApiError("invalid_response");
    }
    const bytes = await boundedBytes(response, maxAvatarBytes, signal);
    try {
      return await decodeImage(bytes, signal, true);
    } catch (error) {
      if (signal.aborted) throw error;
      throw new ApiError("invalid_response");
    }
  },
  async upload(
    profile: Profile,
    bytes: Uint8Array,
    signal: AbortSignal,
  ): Promise<void> {
    if (
      !Number.isSafeInteger(profile.ID) ||
      profile.ID <= 0 ||
      !profile.csrfToken ||
      profile.needChangePwd ||
      profile.isExpired ||
      !bytes.length ||
      bytes.length > maxUploadBytes
    )
      throw new ApiError("invalid_input");
    const data = await json(
      await fetchProfile(
        avatarPath,
        signal,
        { userId: profile.ID, file: canonicalBase64(bytes) },
        profile.csrfToken,
      ),
      signal,
    );
    if (
      !data ||
      typeof data !== "object" ||
      Object.keys(data).length !== 1 ||
      !("result" in data) ||
      data.result !== "success"
    )
      throw new ApiError("invalid_response");
  },
};

export function profileError(error: unknown): string {
  const code = error instanceof ApiError ? error.code : "internal";
  const specific: Record<string, string> = {
    profile_identity_changed: "当前登录账户已变化，请重新确认账户状态。",
    invalid_avatar:
      "请选择有效的 PNG 或 JPEG 图片，大小不超过 2 MiB，宽高均不超过 1024 像素。",
    profile_conflict: "头像记录发生冲突，请重新读取个人资料后再试。",
  };
  if (Object.hasOwn(specific, code)) return specific[code];
  return Object.hasOwn(messages, code) ? messages[code] : messages.internal;
}
