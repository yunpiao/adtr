import { revealNavigation } from "./test-navigation";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import ProfileWorkspace from "./ProfileWorkspace";
import { ApiError, type Profile } from "./api";
import type { AccessUser } from "./access-api";
import {
  avatarPath,
  canonicalBase64,
  imageDimensions,
  maxAvatarBytes,
  maxUploadBytes,
  parsePersonalProfile,
  prepareAvatar,
  profileAPI,
  profileError,
} from "./profile-api";

const owner: Profile = {
  ID: 7,
  username: "viewer",
  role: "viewer",
  priv: 0,
  mobile: "",
  email: "",
  remark: "",
  passStrength: "high",
  hasMfa: false,
  needChangePwd: false,
  isExpired: false,
  passwordNotUpdatedDays: 2,
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  csrfToken: "synthetic-csrf",
};
const personal = (extra: Partial<AccessUser> = {}): AccessUser => ({
  ID: owner.ID,
  username: owner.username,
  role: "viewer",
  priv: 0,
  mobile: "",
  email: "viewer@synthetic.invalid",
  remark: "",
  passStrength: "high",
  hasMfa: false,
  createTm: "2026-10-01T00:00:00Z",
  pwdUpdateTm: "2026-10-01T00:00:00Z",
  address: "合成地址",
  realName: "合成用户",
  department: "合成部门",
  post: "合成职位",
  roleID: "viewer",
  roleName: "viewer",
  avatar: "",
  disabled: false,
  ...extra,
});
const png = () =>
  Uint8Array.from(
    atob(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jfusAAAAASUVORK5CYII=",
    ),
    (c) => c.charCodeAt(0),
  );
const json = (
  value: unknown,
  status = 200,
  headers: Record<string, string> = {},
) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
const binary = (bytes = png(), headers: Record<string, string> = {}) =>
  new Response(bytes as unknown as BodyInit, {
    headers: {
      "Content-Type": "image/png",
      "X-Profile-User-ID": String(owner.ID),
      ...headers,
    },
  });
const fixtureFile = (bytes = png(), type = "image/png") => {
  const file = new File([bytes as unknown as BlobPart], "synthetic.png", {
    type,
  });
  Object.defineProperty(file, "arrayBuffer", {
    value: async () => bytes.slice().buffer,
  });
  return file;
};
let fetcher: ReturnType<typeof vi.fn>,
  decoder: ReturnType<typeof vi.fn>,
  current: AccessUser,
  imagePresent: boolean;
let override: (url: string, init: RequestInit) => Promise<Response> | undefined;
const bitmaps: {
  width: number;
  height: number;
  close: ReturnType<typeof vi.fn>;
}[] = [];
beforeEach(() => {
  current = personal();
  imagePresent = false;
  override = () => undefined;
  bitmaps.length = 0;
  decoder = vi.fn(async () => {
    const image = { width: 1, height: 1, close: vi.fn() };
    bitmaps.push(image);
    return image;
  });
  vi.stubGlobal("createImageBitmap", decoder);
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
    drawImage: vi.fn(),
    clearRect: vi.fn(),
  } as unknown as CanvasRenderingContext2D);
  fetcher = vi.fn(async (url: string, init: RequestInit = {}) => {
    const result = override(url, init);
    if (result) return result;
    if (url === "/api/auth/me") return json(owner);
    if (url === "/api/auth/logout") return json({ result: "SUCCESS" });
    if (url === "/api/profile/me") return json(current);
    if (url === avatarPath && init.method === "POST") {
      imagePresent = true;
      current = { ...current, avatar: avatarPath };
      return json({ result: "success" });
    }
    if (url === avatarPath)
      return imagePresent
        ? binary()
        : json({ error: "avatar_not_found" }, 404, {
            "X-Profile-User-ID": String(owner.ID),
          });
    throw new Error("Unexpected test request");
  });
  vi.stubGlobal("fetch", fetcher);
  localStorage.clear();
  sessionStorage.clear();
  window.history.replaceState({}, "", "#account");
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
const click = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
async function start(profile = owner, changed = vi.fn()) {
  const view = render(
    <ProfileWorkspace profile={profile} sessionChanged={changed} />,
  );
  await screen.findByText("合成用户");
  return { ...view, changed };
}
async function select(file = fixtureFile()) {
  fireEvent.change(screen.getByLabelText("选择头像"), {
    target: { files: [file] },
  });
  await screen.findByText(
    "图片已通过本地检查，等待上传。最终校验由服务器执行。",
  );
}
const uploads = () =>
  fetcher.mock.calls.filter(
    ([url, init]) => url === avatarPath && init.method === "POST",
  );

describe("personal profile parser and transport", () => {
  it("returns safe strings for inherited or unknown error codes", () => {
    for (const code of [
      "__proto__",
      "constructor",
      "toString",
      "private-server-detail",
    ])
      expect(profileError(new ApiError(code, 400))).toBe(
        "服务暂时不可用，请稍后重试。",
      );
  });
  it("rejects avatar ownership mismatch before reading or decoding pixels", async () => {
    fetcher.mockResolvedValueOnce(binary(png(), { "X-Profile-User-ID": "8" }));
    await expect(
      profileAPI.avatar(owner.ID, new AbortController().signal),
    ).rejects.toThrow("profile_identity_changed");
    expect(decoder).not.toHaveBeenCalled();
  });
  it("accepts bounded JPEG dimensions swapped by browser EXIF orientation", async () => {
    const bytes = Uint8Array.from([255, 216, 255, 192, 0, 8, 8, 0, 2, 0, 3, 1]);
    const bitmap = { width: 2, height: 3, close: vi.fn() };
    decoder.mockResolvedValueOnce(bitmap);
    expect(
      await prepareAvatar(
        fixtureFile(bytes, "image/jpeg"),
        new AbortController().signal,
      ),
    ).toEqual(bytes);
    expect(bitmap.close).toHaveBeenCalledOnce();
  });
  it("accepts the direct current-user record and copies only public contract fields", () => {
    expect(
      parsePersonalProfile(
        { ...personal(), secret: "must-not-retain" },
        owner.ID,
      ),
    ).toEqual(personal());
    expect(() =>
      parsePersonalProfile({ profile: personal() }, owner.ID),
    ).toThrow();
  });
  it.each([
    { ID: 8 },
    { avatar: "https://synthetic.invalid/private.png" },
    { avatar: "data:image/png;base64,AA==" },
    { avatar: "/api/profile/avatar?userId=8" },
    { disabled: true },
    { hasMfa: "yes" },
    { department: null },
    { priv: 0.5 },
  ])("rejects untrusted profile fields %j", (extra) => {
    expect(() =>
      parsePersonalProfile({ ...personal(), ...extra }, owner.ID),
    ).toThrow();
  });
  it("rejects unsafe owner identifiers and treats HTML metadata as plain text", async () => {
    expect(() =>
      parsePersonalProfile(personal(), Number.MAX_SAFE_INTEGER + 1),
    ).toThrow();
    current.remark =
      '<img src="https://synthetic.invalid/leak" onerror="alert(1)">';
    await start();
    expect(screen.getByText(current.remark)).toBeVisible();
    expect(document.querySelector("img")).toBeNull();
  });
  it("uses fixed private paths, same-origin cookies, no-store, redirect rejection and auth-bound exact upload JSON", async () => {
    const controller = new AbortController();
    await profileAPI.me(owner.ID, controller.signal);
    await profileAPI.upload(owner, png(), controller.signal);
    expect(fetcher.mock.calls[0]).toEqual([
      "/api/profile/me",
      expect.objectContaining({
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        signal: controller.signal,
      }),
    ]);
    const [url, init] = uploads()[0];
    expect(url).toBe(avatarPath);
    expect(init.headers).toEqual({
      "Content-Type": "application/json",
      "X-CSRF-Token": owner.csrfToken,
    });
    expect(JSON.parse(init.body)).toEqual({
      userId: owner.ID,
      file: canonicalBase64(png()),
    });
  });
  it("recognizes PNG/JPEG headers and enforces dimensions before browser decode", () => {
    expect(imageDimensions(png())).toEqual({
      width: 1,
      height: 1,
      type: "image/png",
    });
    expect(
      imageDimensions(
        Uint8Array.from([255, 216, 255, 192, 0, 8, 8, 0, 2, 0, 3, 1]),
      ),
    ).toEqual({ width: 3, height: 2, type: "image/jpeg" });
    for (const dimension of [0, 1025, 0xffffffff]) {
      const bytes = png();
      new DataView(bytes.buffer).setUint32(16, dimension);
      expect(() => imageDimensions(bytes)).toThrow();
    }
    for (const bytes of [
      new Uint8Array(),
      new TextEncoder().encode("<svg></svg>"),
      Uint8Array.from([255, 216, 255, 192, 255, 255]),
      Uint8Array.from([255, 216, 255, 255, 255, 255]),
    ])
      expect(() => imageDimensions(bytes)).toThrow();
  });
  it("validates upload byte limits, file type, full decode and decoded dimensions", async () => {
    const signal = new AbortController().signal;
    for (const file of [
      fixtureFile(new Uint8Array()),
      fixtureFile(new Uint8Array(maxUploadBytes + 1)),
      fixtureFile(png(), "image/svg+xml"),
      fixtureFile(new TextEncoder().encode("not a PNG")),
    ])
      await expect(prepareAvatar(file, signal)).rejects.toThrow();
    expect(decoder).not.toHaveBeenCalled();
    decoder.mockRejectedValueOnce(new Error("bad raster"));
    await expect(prepareAvatar(fixtureFile(), signal)).rejects.toThrow();
    const image = { width: 2, height: 1, close: vi.fn() };
    decoder.mockResolvedValueOnce(image);
    await expect(prepareAvatar(fixtureFile(), signal)).rejects.toThrow();
    expect(image.close).toHaveBeenCalledOnce();
  });
  it("returns null only for exact avatar_not_found and rejects unsafe PNG responses", async () => {
    const signal = new AbortController().signal;
    expect(await profileAPI.avatar(owner.ID, signal)).toBeNull();
    for (const response of [
      json({ error: "not_found" }, 404),
      json({ error: "avatar_not_found" }, 404),
      json({ error: "avatar_not_found" }, 404, { "X-Profile-User-ID": "8" }),
      binary(png(), { "Content-Type": "image/svg+xml" }),
      binary(png(), { "Content-Length": String(maxAvatarBytes + 1) }),
      binary(new TextEncoder().encode("<svg/>")),
      binary(new Uint8Array(maxAvatarBytes + 1)),
    ]) {
      fetcher.mockResolvedValueOnce(response);
      await expect(profileAPI.avatar(owner.ID, signal)).rejects.toThrow();
    }
    expect(decoder).not.toHaveBeenCalled();
  });
  it("bounds actual streamed bytes even with a false Content-Length and cancels excess data", async () => {
    const cancel = vi.fn();
    let count = 0;
    const body = new ReadableStream({
      pull(controller) {
        controller.enqueue(new Uint8Array(1024 * 1024));
        count++;
      },
      cancel,
    });
    fetcher.mockResolvedValueOnce(
      new Response(body, {
        headers: {
          "Content-Type": "image/png",
          "Content-Length": "1",
          "X-Profile-User-ID": String(owner.ID),
        },
      }),
    );
    await expect(
      profileAPI.avatar(owner.ID, new AbortController().signal),
    ).rejects.toThrow();
    expect(cancel).toHaveBeenCalledOnce();
    expect(count).toBeLessThanOrEqual(7);
    expect(count).toBeGreaterThan(4);
    expect(decoder).not.toHaveBeenCalled();
  });
  it("rejects success-shaped failure or malformed upload responses", async () => {
    for (const response of [
      json({ result: "failed" }),
      json({ result: "SUCCESS" }),
      json({ result: "success", url: "https://synthetic.invalid" }),
      new Response("{", { headers: { "Content-Type": "application/json" } }),
    ]) {
      fetcher.mockResolvedValueOnce(response);
      await expect(
        profileAPI.upload(owner, png(), new AbortController().signal),
      ).rejects.toThrow();
    }
  });
});

describe("personal profile workspace", () => {
  it("has a separate authenticated tab, preserves Account heading, and permits viewers without management grants", async () => {
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await revealNavigation("个人资料");
    click("个人资料");
    await screen.findByText("合成用户");
    expect(screen.getByRole("heading", { name: "个人资料" })).toBeVisible();
    expect(screen.getByText("尚未设置头像")).toBeVisible();
    expect(
      fetcher.mock.calls.some(([url]) => url.startsWith("/api/access")),
    ).toBe(false);
    expect(screen.queryByLabelText("用户 ID")).toBeNull();
    expect(screen.queryByLabelText("当前密码")).toBeNull();
  });
  it.each(["needChangePwd", "isExpired"] as const)(
    "blocks forced state %s without profile calls",
    async (key) => {
      render(
        <ProfileWorkspace
          profile={{ ...owner, [key]: true }}
          sessionChanged={vi.fn()}
        />,
      );
      expect(screen.getByRole("status")).toHaveTextContent("请先修改密码");
      expect(fetcher).not.toHaveBeenCalled();
    },
  );
  it("uploads then replaces the actual avatar and keeps payloads out of browser storage and URLs", async () => {
    await start();
    await select();
    click("上传头像");
    await screen.findByText("头像已更新，已重新读取服务器保存的图片。");
    expect(screen.getByRole("img", { name: "当前头像" }).tagName).toBe(
      "CANVAS",
    );
    expect(screen.getByRole("button", { name: "替换头像" })).toBeDisabled();
    await select();
    click("替换头像");
    await waitFor(() => expect(uploads()).toHaveLength(2));
    await screen.findByText("头像已更新，已重新读取服务器保存的图片。");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(location.href).not.toContain("base64");
    expect(document.querySelector("[src]")).toBeNull();
    expect(document.body.textContent).not.toContain(canonicalBase64(png()));
  });
  it("rejects invalid input locally without a POST and clears an old selection", async () => {
    await start();
    await select();
    fireEvent.change(screen.getByLabelText("选择头像"), {
      target: { files: [fixtureFile(new Uint8Array(maxUploadBytes + 1))] },
    });
    await screen.findByRole("alert");
    expect(screen.getByRole("button", { name: "上传头像" })).toBeDisabled();
    expect(uploads()).toHaveLength(0);
  });
  it("does not replay an upload after response loss; re-reads and displays the persisted avatar", async () => {
    await start();
    await select();
    override = (url, init) => {
      if (url === avatarPath && init.method === "POST") {
        imagePresent = true;
        current.avatar = avatarPath;
        return Promise.reject(new TypeError("connection lost"));
      }
    };
    click("上传头像");
    await screen.findByRole("img", { name: "当前头像" });
    expect(screen.getByRole("alert")).toHaveTextContent("上传结果尚未确认");
    expect(
      screen.queryByText("头像已更新，已重新读取服务器保存的图片。"),
    ).toBeNull();
    expect(uploads()).toHaveLength(1);
    expect(screen.getByRole("button", { name: "替换头像" })).toBeDisabled();
    click("重新读取当前头像");
    await screen.findByRole("img", { name: "当前头像" });
    expect(uploads()).toHaveLength(1);
  });
  it("keeps unknown state when recovery fails and allows only an explicit fresh read", async () => {
    await start();
    await select();
    override = () => Promise.reject(new TypeError("offline"));
    click("上传头像");
    await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(2));
    expect(screen.queryByLabelText("选择头像")).toBeNull();
    expect(uploads()).toHaveLength(1);
    override = () => undefined;
    click("重新读取当前头像");
    await screen.findByText("合成用户");
    expect(uploads()).toHaveLength(1);
  });
  it("canceling an upload clears the file, re-reads, and ignores a late success without claiming rollback", async () => {
    let resolve!: (value: Response) => void;
    await start();
    await select();
    override = (url, init) =>
      url === avatarPath && init.method === "POST"
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    click("上传头像");
    click("取消等待");
    await screen.findByText("合成用户");
    expect(screen.getByRole("alert")).toHaveTextContent("取消等待不能撤销");
    await act(async () => resolve(json({ result: "success" })));
    expect(
      screen.queryByText("头像已更新，已重新读取服务器保存的图片。"),
    ).toBeNull();
    expect(uploads()).toHaveLength(1);
  });
  it("blocks repeated submission while a write is in flight", async () => {
    await start();
    await select();
    override = (url, init) =>
      url === avatarPath && init.method === "POST"
        ? new Promise(() => {})
        : undefined;
    const form = screen
      .getByRole("button", { name: "上传头像" })
      .closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(uploads()).toHaveLength(1);
  });
  it("clears private metadata and canvas when the session expires", async () => {
    imagePresent = true;
    const { changed } = await start();
    await screen.findByRole("img", { name: "当前头像" });
    override = (url) =>
      url === "/api/profile/me"
        ? Promise.resolve(json({ error: "unauthenticated" }, 401))
        : undefined;
    click("刷新个人资料");
    await waitFor(() => expect(changed).toHaveBeenCalledOnce());
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.queryByText("合成用户")).toBeNull();
    expect(bitmaps[0].close).toHaveBeenCalled();
  });
  it("refreshes identity and discards profile fields if an avatar belongs to a changed session", async () => {
    const changed = vi.fn();
    override = (url) =>
      url === avatarPath
        ? Promise.resolve(binary(png(), { "X-Profile-User-ID": "8" }))
        : undefined;
    render(<ProfileWorkspace profile={owner} sessionChanged={changed} />);
    await waitFor(() => expect(changed).toHaveBeenCalledOnce());
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.queryByText("合成用户")).toBeNull();
    expect(decoder).not.toHaveBeenCalled();
  });
  it("clears selected bytes, closes bitmap, and rejects stale reads when identity changes", async () => {
    imagePresent = true;
    const { rerender } = await start();
    await select();
    let resolve!: (response: Response) => void;
    override = (url) =>
      url === "/api/profile/me"
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    rerender(
      <ProfileWorkspace
        profile={{ ...owner, ID: 9, csrfToken: "new-session" }}
        sessionChanged={vi.fn()}
      />,
    );
    expect(screen.queryByText("合成用户")).toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
    expect(bitmaps.every((bitmap) => bitmap.close.mock.calls.length > 0)).toBe(
      true,
    );
    await act(async () => resolve(json(personal())));
    await screen.findByRole("alert");
    expect(screen.queryByText("合成用户")).toBeNull();
    expect(uploads()).toHaveLength(0);
  });
  it("cleans up an avatar decoded after unmount", async () => {
    imagePresent = true;
    let finish!: (bitmap: ImageBitmap) => void;
    decoder.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const view = render(
      <ProfileWorkspace profile={owner} sessionChanged={vi.fn()} />,
    );
    await waitFor(() => expect(decoder).toHaveBeenCalledOnce());
    view.unmount();
    const image = { width: 1, height: 1, close: vi.fn() };
    await act(async () => finish(image as unknown as ImageBitmap));
    expect(image.close).toHaveBeenCalledOnce();
  });
  it("leaving the tab and logout discard loaded avatar and file selection", async () => {
    imagePresent = true;
    render(<App />);
    await screen.findByRole("heading", { name: "账户概览" });
    await revealNavigation("个人资料");
    click("个人资料");
    await screen.findByText("合成用户");
    await select();
    await revealNavigation("账户概览");
    click("账户概览");
    await screen.findByRole("heading", { name: "账户概览" });
    expect(screen.queryByRole("img")).toBeNull();
    await revealNavigation("个人资料");
    click("个人资料");
    await screen.findByText("合成用户");
    expect(screen.getByRole("button", { name: "替换头像" })).toBeDisabled();
    await revealNavigation("退出登录");
    click("退出登录");
    await screen.findByRole("heading", { name: "登录账户" });
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.queryByText("合成用户")).toBeNull();
  });
});
