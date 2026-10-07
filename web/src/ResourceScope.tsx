import { useEffect, useState } from "react";
import { ApiError } from "./api";
import { ErrorNotice, Field, readValues } from "./access-common";
import {
  parseChecks,
  resourceRequest,
  type ResourceChecks,
  type ResourceGrants,
} from "./resource-api";
import { useResourceTask, type ResourceContext } from "./resource-common";
export default function ResourceScope({
  context,
}: {
  context: ResourceContext;
}) {
  const [grants, setGrants] = useState<ResourceGrants | null>(null),
    [revision, setRevision] = useState(0),
    [results, setResults] = useState<
      { ids: string[]; allowed: boolean }[] | null
    >(null);
  const read = useResourceTask(context.sessionChanged),
    check = useResourceTask(context.sessionChanged);
  useEffect(() => {
    setGrants(null);
    if (!context.can("GET /api/resources/grants")) return;
    void read.run(
      async (signal) => {
        const data = await resourceRequest<ResourceGrants>("/grants", signal);
        if (
          data.authorizationScopeOnly !== true ||
          !Array.isArray(data.list) ||
          data.list.some(
            (entry) =>
              entry.application !== "ad" || !Array.isArray(entry.resources),
          )
        )
          throw new ApiError("invalid_response");
        return data;
      },
      setGrants,
      true,
    );
  }, [revision, context.profile.csrfToken]);
  return (
    <>
      <h3>当前数据权限</h3>
      <p className="muted">
        只展示当前会话在当前时刻的授权范围。结果不是访问令牌，不授予操作级写权限；实际
        AD 读取、搜索、导出和后台任务仍须在执行时重新鉴权。
      </p>
      {context.can("GET /api/resources/grants") && (
        <>
          <button
            className="secondary"
            onClick={() => setRevision((n) => n + 1)}
          >
            刷新当前数据权限
          </button>
          <ErrorNotice error={read.error} />
          {read.busy && <p role="status">正在读取当前数据权限…</p>}
          {grants &&
            (grants.list.length === 0 ||
            grants.list.every((entry) => entry.resources.length === 0) ? (
              <p className="resource-empty" role="status">
                当前没有获授权的活动域。平台管理员也没有自动数据授权。
              </p>
            ) : (
              <ul aria-label="当前获授权的域">
                {grants.list.flatMap((entry) =>
                  entry.resources.map((id) => (
                    <li key={`${entry.application}-${id}`}>AD · {id}</li>
                  )),
                )}
              </ul>
            ))}
        </>
      )}
      {context.can("POST /api/resources/check") && (
        <form
          className="resource-checker"
          onSubmit={(event) => {
            if (check.busy) {
              event.preventDefault();
              return;
            }
            const values = readValues(event),
              body = parseChecks(values.checks);
            setResults(null);
            if (!body) {
              check.setError(
                "请填写 1–100 行检查，每行包含 1–1,000 个不重复的有效域 ID；空请求不代表授权。",
              );
              return;
            }
            void check.run(
              async (signal) => {
                const data = await resourceRequest<ResourceChecks>(
                  "/check",
                  signal,
                  body,
                  context.profile.csrfToken,
                );
                if (
                  data.authorizationScopeOnly !== true ||
                  !Array.isArray(data.results) ||
                  data.results.length !== body.resources.length ||
                  data.results.some((value) => typeof value !== "boolean")
                )
                  throw new ApiError("invalid_response");
                return data;
              },
              (data) =>
                setResults(
                  data.results.map((allowed, index) => ({
                    allowed,
                    ids: body.resources[index].dataResource,
                  })),
                ),
            );
          }}
        >
          <h4>检查当前数据授权</h4>
          <p>
            应用固定为本地字符串 ad，资源类型固定为
            2（数据资源）。旧版数字应用、类型 0 和 1
            不受支持，不推断应用级全部域权限。
          </p>
          <ErrorNotice error={check.error} />
          <fieldset disabled={check.busy}>
            <Field
              label="待检查域 ID"
              help="每行一次检查；一行内多个 ID 用空格或逗号分隔，只有全部获授权才通过。未知、跨租户、停用或未授权域均返回不允许，不披露其存在性"
            >
              <textarea
                name="checks"
                rows={5}
                required
                onChange={() => setResults(null)}
              />
            </Field>
          </fieldset>
          <button disabled={check.busy}>
            {check.busy ? "正在检查…" : "检查数据授权"}
          </button>
          {results && (
            <ol aria-label="数据授权检查结果">
              {results.map((result, index) => (
                <li key={index}>
                  <strong>
                    {result.allowed ? "允许（仅范围）" : "不允许"}
                  </strong>{" "}
                  · {result.ids.join("、")}
                </li>
              ))}
            </ol>
          )}
        </form>
      )}
    </>
  );
}
