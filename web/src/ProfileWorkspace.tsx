import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import type { AccessUser } from "./access-api";
import { prepareAvatar, profileAPI, profileError } from "./profile-api";

type Props = { profile: Profile; sessionChanged: () => void };
export default function ProfileWorkspace(props: Props) {
  if (props.profile.needChangePwd || props.profile.isExpired)
    return <p role="status">请先修改密码，再查看个人资料。</p>;
  return (
    <Workspace
      key={`${props.profile.ID}:${props.profile.csrfToken}`}
      {...props}
    />
  );
}

function Avatar({ bitmap }: { bitmap: ImageBitmap }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  useLayoutEffect(() => {
    const element = canvas.current;
    const context = element?.getContext("2d");
    if (context) context.drawImage(bitmap, 0, 0);
    return () => context?.clearRect(0, 0, bitmap.width, bitmap.height);
  }, [bitmap]);
  return (
    <canvas
      ref={canvas}
      role="img"
      aria-label="当前头像"
      width={bitmap.width}
      height={bitmap.height}
      className="profile-avatar"
    />
  );
}

function Workspace({ profile, sessionChanged }: Props) {
  const [data, setData] = useState<AccessUser | null>(null),
    [avatar, setAvatar] = useState<ImageBitmap | null>(null),
    [selected, setSelected] = useState<Uint8Array | null>(null),
    [busy, setBusy] = useState<"read" | "prepare" | "upload" | null>(null),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [uncertain, setUncertain] = useState(false),
    [loaded, setLoaded] = useState(false);
  const sequence = useRef(0),
    controller = useRef<AbortController | null>(null),
    image = useRef<ImageBitmap | null>(null),
    file = useRef<Uint8Array | null>(null),
    lock = useRef(false),
    changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  const discardSelection = () => {
    file.current?.fill(0);
    file.current = null;
    setSelected(null);
  };
  const discardPrivate = () => {
    image.current?.close();
    image.current = null;
    setAvatar(null);
    setData(null);
    setLoaded(false);
    discardSelection();
  };
  const begin = (kind: "read" | "prepare" | "upload") => {
    controller.current?.abort();
    const current = new AbortController();
    controller.current = current;
    const id = ++sequence.current;
    lock.current = true;
    setBusy(kind);
    setError("");
    setNotice("");
    return { id, signal: current.signal };
  };
  const failure = (reason: unknown) => {
    if (
      reason instanceof ApiError &&
      (reason.status === 401 ||
        reason.code === "password_change_required" ||
        reason.code === "forbidden" ||
        reason.code === "profile_identity_changed")
    ) {
      discardPrivate();
      changed.current();
    }
    setError(profileError(reason));
  };
  const reload = async (message = "", recovery = uncertain) => {
    const { id, signal } = begin("read");
    discardPrivate();
    setUncertain(recovery);
    let next: ImageBitmap | null = null;
    try {
      const current = await profileAPI.me(profile.ID, signal);
      next = await profileAPI.avatar(profile.ID, signal);
      if (id !== sequence.current) {
        next?.close();
        return;
      }
      image.current = next;
      setAvatar(next);
      setData(current);
      setLoaded(true);
      setNotice(message);
    } catch (reason) {
      next?.close();
      if (id === sequence.current) failure(reason);
    } finally {
      if (id === sequence.current) {
        lock.current = false;
        setBusy(null);
      }
    }
  };
  useEffect(() => {
    void reload();
    return () => {
      sequence.current++;
      controller.current?.abort();
      image.current?.close();
      file.current?.fill(0);
    };
  }, []);
  const select = async (input: File | undefined) => {
    if (lock.current || !input) return;
    const { id, signal } = begin("prepare");
    discardSelection();
    try {
      const bytes = await prepareAvatar(input, signal);
      if (id !== sequence.current) {
        bytes.fill(0);
        return;
      }
      file.current = bytes;
      setSelected(bytes);
    } catch (reason) {
      if (id === sequence.current) failure(reason);
    } finally {
      if (id === sequence.current) {
        lock.current = false;
        setBusy(null);
      }
    }
  };
  const upload = async () => {
    if (lock.current || !selected || !loaded) return;
    const { id, signal } = begin("upload");
    // Upload takes a synchronous copy into the request body; discard the file
    // immediately, including on a lost response. Nothing is persisted/replayed.
    const pending = profileAPI.upload(profile, selected, signal);
    discardSelection();
    try {
      await pending;
      if (id !== sequence.current) return;
      await reload("头像已更新，已重新读取服务器保存的图片。", false);
    } catch (reason) {
      if (id !== sequence.current) return;
      if (
        !(reason instanceof ApiError) ||
        reason.status === 0 ||
        reason.status >= 500 ||
        reason.code === "invalid_response"
      ) {
        await reload(
          "已重新读取当前头像，请核对图片后再决定是否再次上传。",
          true,
        );
      } else {
        failure(reason);
        lock.current = false;
        setBusy(null);
      }
    }
  };
  const cancel = () => {
    const wasUpload = busy === "upload";
    sequence.current++;
    controller.current?.abort();
    lock.current = false;
    discardSelection();
    setBusy(null);
    setError("");
    if (wasUpload)
      void reload(
        "已停止等待；上传可能已提交，也可能仍在处理。请核对当前头像，必要时稍后再次读取。",
        true,
      );
    else setNotice("已取消等待。未提交的图片已清除。");
  };
  return (
    <div className="profile-workspace">
      <h2>个人资料</h2>
      <p className="muted">
        查看当前账户的个人资料。头像仅供当前登录账户读取。
      </p>
      {error && (
        <p role="alert" className="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {uncertain && (
        <p role="alert" className="warning">
          上传结果尚未确认，请重新读取当前头像后再决定是否上传。取消等待不能撤销已提交的上传。
        </p>
      )}
      <div className="actions">
        <button
          className="secondary"
          disabled={!!busy}
          onClick={() =>
            void reload(
              uncertain
                ? "已重新读取当前头像，请核对图片后再决定是否再次上传。"
                : "个人资料已刷新。",
            )
          }
        >
          {uncertain ? "重新读取当前头像" : "刷新个人资料"}
        </button>
        {busy && (
          <button className="secondary" onClick={cancel}>
            取消等待
          </button>
        )}
      </div>
      {busy === "read" && <p role="status">正在读取个人资料与头像…</p>}
      {data && (
        <>
          <section aria-label="当前头像与上传">
            <h3>头像</h3>
            {avatar ? <Avatar bitmap={avatar} /> : <p>尚未设置头像</p>}
            <p id="avatar-help" className="muted">
              PNG 或 JPEG，最大 2 MiB，宽高均不超过 1024 像素。上传后将保存为
              PNG。
            </p>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void upload();
              }}
            >
              <label className="field" htmlFor="profile-avatar-file">
                选择头像
                <input
                  id="profile-avatar-file"
                  type="file"
                  accept="image/png,image/jpeg"
                  aria-describedby="avatar-help"
                  disabled={!!busy || !loaded}
                  onChange={(event) => {
                    const input = event.target.files?.[0];
                    event.target.value = "";
                    void select(input);
                  }}
                />
              </label>
              {selected && (
                <p role="status">
                  图片已通过本地检查，等待上传。最终校验由服务器执行。
                </p>
              )}
              {busy === "prepare" && <p role="status">正在检查图片…</p>}
              {busy === "upload" && (
                <p role="status">
                  正在上传头像…取消只停止等待，不能撤销已提交的操作。
                </p>
              )}
              <div className="actions">
                <button disabled={!!busy || !selected || !loaded}>
                  {avatar ? "替换头像" : "上传头像"}
                </button>
                {selected && (
                  <button
                    type="button"
                    className="secondary"
                    onClick={discardSelection}
                  >
                    清除选择
                  </button>
                )}
              </div>
            </form>
          </section>
          <h3>资料详情</h3>
          <dl>
            {[
              ["用户 ID", data.ID],
              ["用户名", data.username],
              ["姓名", data.realName || "未设置"],
              ["部门", data.department || "未设置"],
              ["职位", data.post || "未设置"],
              ["地址", data.address || "未设置"],
              ["邮箱", data.email || "未设置"],
              ["手机号", data.mobile || "未设置"],
              ["备注", data.remark || "未设置"],
              ["角色", data.roleName || data.role],
              ["角色 ID", data.roleID],
              ["权限值", data.priv],
              ["密码强度", data.passStrength],
              ["多因素认证", data.hasMfa ? "已启用" : "未启用"],
              ["创建时间（UTC）", data.createTm || "未提供"],
              ["密码更新时间（UTC）", data.pwdUpdateTm || "未提供"],
            ].map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
    </div>
  );
}
