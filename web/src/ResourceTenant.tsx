import { useEffect, useState } from "react";
import { ApiError } from "./api";
import {
  ErrorNotice,
  Field,
  FormActions,
  ProofFields,
  readValues,
} from "./access-common";
import {
  parseTenant,
  resourceRequest,
  type TenantConfig,
} from "./resource-api";
import {
  useResourceMutation,
  useResourceTask,
  type ResourceContext,
} from "./resource-common";
export default function ResourceTenant({
  context,
}: {
  context: ResourceContext;
}) {
  const [tenant, setTenant] = useState<TenantConfig | null | undefined>(
      undefined,
    ),
    [revision, setRevision] = useState(0),
    [editing, setEditing] = useState(false),
    [notice, setNotice] = useState("");
  const task = useResourceTask(context.sessionChanged);
  const done = (
    message = "已取消编辑并重新读取租户配置。取消不能撤销已提交的修改。",
  ) => {
    mutation.cancel();
    setEditing(false);
    setNotice(message);
    setRevision((n) => n + 1);
  };
  const mutation = useResourceMutation(context, done);
  useEffect(() => {
    setTenant(undefined);
    void task.run(
      async (signal) => {
        try {
          return await resourceRequest<TenantConfig>("/tenant", signal);
        } catch (error) {
          if (
            error instanceof ApiError &&
            error.code === "tenant_not_configured"
          )
            return null;
          throw error;
        }
      },
      setTenant,
      true,
    );
  }, [revision]);
  return (
    <>
      <h3>租户配置</h3>
      <p className="warning">
        仅修改当前会话所属租户。保存后该租户的全部会话（包括当前会话）立即失效，需要重新登录。
      </p>
      <p className="muted">
        客户 UID
        是配置备注性质的元数据，不是租户选择器、外部身份绑定或许可验证。有效期是本地访问策略。
      </p>
      {notice && <p role="status">{notice}</p>}
      <ErrorNotice error={task.error} />
      {task.busy && <p role="status">正在读取租户配置…</p>}
      {tenant === null && (
        <p role="status">
          当前租户尚未配置。数据访问默认拒绝；没有预填的默认配置。
        </p>
      )}
      {tenant && !editing && (
        <dl>
          {[
            ["活动域数量上限", tenant.maxAdCount],
            ["失效时间（UTC Unix 秒）", tenant.expireTime],
            [
              "失效时间（UTC）",
              new Date(tenant.expireTime * 1000).toISOString(),
            ],
            ["客户 UID", tenant.uid],
            ["租户名称", tenant.name],
          ].map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {editing && tenant !== undefined ? (
        <form
          key={revision}
          onSubmit={(event) => {
            if (mutation.busy) {
              event.preventDefault();
              return;
            }
            const values = readValues(event),
              config = parseTenant(values);
            if (!config) {
              mutation.setError(
                "请检查域上限（0–100,000）、UTC 整数秒（0–253402300799）、UID（1–256 字符）及名称（1–32 字符）；文本不能有首尾空白或控制字符。",
              );
              return;
            }
            mutation.submit(
              "/tenant/save",
              { ...config },
              values,
              "租户配置已保存。",
            );
          }}
        >
          <ErrorNotice error={mutation.error} />
          <fieldset disabled={mutation.busy}>
            <Field
              label="活动域数量上限"
              help="0–100,000 的整数；0 表示不允许数据访问。不能低于已有活动域数"
            >
              <input
                name="maxAdCount"
                inputMode="numeric"
                defaultValue={tenant?.maxAdCount ?? ""}
                required
              />
            </Field>
            <Field
              label="失效时间（UTC Unix 秒）"
              help="整数秒，不是毫秒。0 或当前及过去时间表示已过期；没有永久有效选项"
            >
              <input
                name="expireTime"
                inputMode="numeric"
                defaultValue={tenant?.expireTime ?? ""}
                required
              />
            </Field>
            <Field
              label="客户 UID"
              help="1–256 个 Unicode 字符，不允许首尾空白或控制字符"
            >
              <input name="uid" defaultValue={tenant?.uid ?? ""} required />
            </Field>
            <Field
              label="租户名称"
              help="1–32 个 Unicode 字符，不允许首尾空白或控制字符"
            >
              <input name="name" defaultValue={tenant?.name ?? ""} required />
            </Field>
            <p className="warning">
              允许保存已过期配置，这会立即停用当前租户的数据访问。调整上限或有效期不会新增或删除域。
            </p>
            <ProofFields />
          </fieldset>
          <FormActions busy={mutation.busy} cancel={() => done()}>
            保存租户配置
          </FormActions>
        </form>
      ) : (
        <div className="actions">
          {tenant !== undefined &&
            context.can("POST /api/resources/tenant/save") && (
              <button
                onClick={() => {
                  setNotice("");
                  mutation.setError("");
                  setEditing(true);
                }}
              >
                编辑租户配置
              </button>
            )}
          <button
            className="secondary"
            onClick={() => setRevision((n) => n + 1)}
          >
            刷新租户配置
          </button>
        </div>
      )}
    </>
  );
}
