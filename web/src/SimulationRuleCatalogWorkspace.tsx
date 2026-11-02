import { useEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import { accessRequest, type Permission } from "./access-api";
import SavedSourcePicker from "./SavedSourcePicker";
import { listenForSessionInvalidation } from "./session-invalidation";
import {
  simulationRuleCatalogAPI as api,
  fields,
  operators,
  copyAcknowledgement,
  downloadCatalog,
  validDeclaration,
  verifyReceipt,
  intentFingerprint,
  type Action,
  type Write,
  type Declaration,
  type Fixture,
  type CatalogRecord,
  type Receipt,
  type Page,
} from "./simulation-rule-catalog-api";
type Props = { profile: Profile; sessionChanged: () => void };
type Permissions = {
  read: boolean;
  manage: boolean;
  publish: boolean;
  copy: boolean;
};
const readPaths = [
  "GET /api/domain-selection",
  "GET /api/domain-selection/resolve",
  ...["list", "detail", "receipt", "export"].map(
    (x) => `GET /api/simulation/rule-catalog/${x}`,
  ),
];
const managePaths = [
  "create",
  "edit",
  "test",
  "enable",
  "disable",
  "archive",
].map((x) => `POST /api/simulation/rule-catalog/${x}`);
export default function SimulationRuleCatalogWorkspace(props: Props) {
  return <Entry key={JSON.stringify(props.profile)} {...props} />;
}
function Entry({ profile, sessionChanged }: Props) {
  const [gate, setGate] = useState<Permissions | null>(null),
    [domain, setDomain] = useState(""),
    [error, setError] = useState(""),
    [retry, setRetry] = useState(0);
  const changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  useEffect(() => {
    let live = true;
    const c = new AbortController();
    setGate(null);
    setDomain("");
    setError("");
    const paths = [
      ...readPaths,
      ...managePaths,
      "POST /api/simulation/rule-catalog/publish",
      "POST /api/simulation/rule-catalog/create-template",
    ];
    Promise.all([
      accessRequest<{ menu: Permission[] }>("/menu", c.signal),
      accessRequest<{ results: boolean[] }>(
        "/check",
        c.signal,
        { paths },
        profile.csrfToken,
      ),
    ])
      .then(([menu, checks]) => {
        if (!live) return;
        if (
          !Array.isArray(checks.results) ||
          checks.results.length !== paths.length ||
          checks.results.some((x) => typeof x !== "boolean")
        )
          throw new ApiError("invalid_response");
        const read =
          menu.menu.some(
            (x) => x.mark === "simulation_rule_catalog" && x.auth?.readable,
          ) && checks.results.slice(0, readPaths.length).every(Boolean);
        setGate({
          read,
          manage:
            read && checks.results.slice(readPaths.length, -2).every(Boolean),
          publish: read && checks.results.at(-2) === true,
          copy: read && checks.results.at(-1) === true,
        });
      })
      .catch((e) => {
        if (!live) return;
        setGate({ read: false, manage: false, publish: false, copy: false });
        setError("权限核对失败，请重试。");
        if (e instanceof ApiError && e.status === 401) changed.current();
      });
    const notice = listenForSessionInvalidation(() => {
      live = false;
      c.abort();
      setGate(null);
      setDomain("");
      changed.current();
    });
    return () => {
      live = false;
      c.abort();
      notice.close();
    };
  }, [profile.csrfToken, retry]);
  return (
    <section aria-label="模拟规则库入口">
      <h2>SIMULATED · 规则生命周期与可视化编辑</h2>
      <p>仅合成规则和合成测试；不提供学习算法、生产告警或全局实时传播。</p>
      {error && <p role="alert">{error}</p>}
      {!gate ? (
        <p role="status">核对权限中…</p>
      ) : !gate.read ? (
        <>
          <p>没有模拟规则库读取权限。</p>
          <button onClick={() => setRetry((x) => x + 1)}>重新核对权限</button>
        </>
      ) : !domain ? (
        <SavedSourcePicker
          userID={profile.ID}
          sessionChanged={sessionChanged}
          onResolved={(v) => setDomain(v.selection.domainId)}
        />
      ) : (
        <>
          <button onClick={() => setDomain("")}>切换模拟域</button>
          <SimulationRuleCatalogDomainWorkspace
            profile={profile}
            sessionChanged={sessionChanged}
            domainId={domain}
            permissions={gate}
          />
        </>
      )}
    </section>
  );
}
export type WorkspaceProps = Props & {
  domainId: string;
  permissions?: Permissions;
};
export function SimulationRuleCatalogDomainWorkspace(props: WorkspaceProps) {
  return (
    <Workspace
      key={JSON.stringify([props.profile, props.domainId, props.permissions])}
      {...props}
    />
  );
}
const initial: Declaration = {
  combine: "all",
  conditions: [{ field: "event.code", operator: "eq", values: ["4625"] }],
};
const seedCases: Fixture[] = [
  { id: "positive", fields: { "event.code": "4625" }, wantMatch: true },
  { id: "negative", fields: { "event.code": "4624" }, wantMatch: false },
];
type Intent = { action: Action; input: Write; fingerprint: string };
function Workspace({
  profile,
  domainId,
  sessionChanged,
  permissions = { read: false, manage: false, publish: false, copy: false },
}: WorkspaceProps) {
  const [page, setPage] = useState<Page | null>(null),
    [offset, setOffset] = useState(0),
    [query, setQuery] = useState(""),
    [filter, setFilter] = useState(""),
    [refresh, setRefresh] = useState(0);
  const [selected, setSelected] = useState(""),
    [mode, setMode] = useState<"none" | "new" | "loading" | "loaded">("none"),
    [record, setRecord] = useState<CatalogRecord | null>(null),
    [id, setId] = useState(""),
    [declaration, setDeclaration] = useState<Declaration>(initial),
    [cases, setCases] = useState<Fixture[]>(seedCases);
  const [busy, setBusy] = useState(false),
    [denied, setDenied] = useState(false),
    [error, setError] = useState(""),
    [receipt, setReceipt] = useState<Receipt | null>(null),
    [pending, setPending] = useState<Intent | null>(null),
    [copyId, setCopyId] = useState(""),
    [copyName, setCopyName] = useState(""),
    [ack, setAck] = useState(false);
  const live = useRef(true),
    generation = useRef(0),
    operation = useRef(false),
    controllers = useRef(new Set<AbortController>());
  const changed = useRef(sessionChanged);
  changed.current = sessionChanged;
  function controller() {
    const c = new AbortController();
    controllers.current.add(c);
    return c;
  }
  function clear() {
    live.current = false;
    controllers.current.forEach((c) => c.abort());
    setDenied(true);
    setRecord(null);
    setPage(null);
    setReceipt(null);
    setPending(null);
    setDeclaration(initial);
    setCases([]);
    setCopyId("");
    setCopyName("");
  }
  function failure(e: unknown) {
    if (!live.current) return;
    if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
      clear();
      if (e.status === 401) changed.current();
      return;
    }
    setError(
      e instanceof ApiError
        ? `请求失败：${e.code}（${e.status}）。当前状态尚需重新读取。`
        : "请求失败，结果尚未确认。",
    );
  }
  useEffect(() => {
    live.current = true;
    const n = listenForSessionInvalidation(() => {
      clear();
      changed.current();
    });
    return () => {
      live.current = false;
      controllers.current.forEach((c) => c.abort());
      n.close();
    };
  }, []);
  useEffect(() => {
    if (!permissions.read || denied) return;
    const c = controller();
    setPage(null);
    api
      .list(domainId, filter, offset, profile, c.signal)
      .then((v) => {
        if (live.current && !c.signal.aborted) setPage(v);
      })
      .catch((e) => {
        if (!c.signal.aborted) failure(e);
      });
    return () => c.abort();
  }, [domainId, filter, offset, refresh, denied]);
  async function load(target: string) {
    const token = ++generation.current;
    setSelected(target);
    setMode("loading");
    setRecord(null);
    setAck(false);
    const c = controller();
    try {
      const v = await api.detail(domainId, target, profile, c.signal);
      if (!live.current || token !== generation.current) return;
      if (v.id !== target || v.snapshot.scope.domain !== domainId)
        throw new ApiError("invalid_response");
      setRecord(v);
      setId(v.id);
      const entry = v.snapshot.entries[0];
      const latest = entry.published.find(
        (p) => p.rule.version === v.latestPublishedVersion,
      );
      const source = entry.draft ?? latest?.rule;
      if (!source) throw new ApiError("invalid_response");
      setDeclaration(
        structuredClone({
          combine: source.combine,
          conditions: source.conditions,
        }),
      );
      const savedTest = entry.test ?? latest?.test;
      setCases(
        savedTest
          ? structuredClone(
              savedTest.cases.map((c) => ({
                id: c.event.id,
                fields: c.event.fields,
                wantMatch: c.want_match,
              })),
            )
          : structuredClone(seedCases),
      );
      setCopyId("");
      setCopyName("");
      setMode("loaded");
    } catch (e) {
      if (live.current && token === generation.current) {
        setMode("none");
        failure(e);
      }
    }
  }
  async function complete(r: Receipt, intent: Intent) {
    verifyReceipt(r, intent.action, intent.input, profile.ID);
    if (!live.current) return;
    setReceipt(r);
    setPending(null);
    setRefresh((x) => x + 1);
    await load(intent.input.id);
  }
  async function execute(intent: Intent, recover = false) {
    if (operation.current || !live.current) return;
    if (intent.fingerprint !== intentFingerprint(intent.action, intent.input)) {
      setError("原操作输入已改变，拒绝重试。");
      return;
    }
    operation.current = true;
    setBusy(true);
    setError("");
    const c = controller();
    try {
      if (recover) {
        try {
          const r = await api.receipt(
            domainId,
            intent.input.operationId,
            profile,
            c.signal,
          );
          await complete(r, intent);
          return;
        } catch (e) {
          if (!(e instanceof ApiError) || e.status !== 404) throw e;
        }
      }
      const r = await api.write(intent.action, intent.input, profile, c.signal);
      await complete(r, intent);
    } catch (e) {
      failure(e);
      if (
        live.current &&
        (!(e instanceof ApiError) ||
          e.status >= 500 ||
          ["network", "invalid_response"].includes(e.code))
      ) {
        setPending(intent);
        setError(
          "提交结果未确认。保留原操作 ID 与完整输入；先查回执，缺失才重试原操作。",
        );
      } else if (live.current) setPending(null);
    } finally {
      operation.current = false;
      if (live.current) setBusy(false);
    }
  }
  function write(action: Action) {
    if (busy || pending) return;
    const input: Write = {
      domainId,
      id,
      operationId: crypto.randomUUID(),
      expectedRevision: record?.revision ?? 0,
    };
    if (action === "create" || action === "edit")
      input.declaration = structuredClone(declaration);
    if (action === "test") input.cases = structuredClone(cases);
    if (action === "create-template") {
      const p = entry?.published.find(
        (p) => p.rule.version === record?.activeVersion,
      );
      if (!p || !ack || !record?.copySupported) return;
      Object.assign(input, {
        publishedVersion: p.rule.version,
        publishedDigest: p.digest,
        templateId: copyId,
        name: copyName,
        acknowledgement: copyAcknowledgement,
      });
    }
    void execute({
      action,
      input,
      fingerprint: intentFingerprint(action, input),
    });
  }
  const entry = record?.snapshot.entries[0];
  const locked = busy || !!pending || mode === "loading";
  const editable =
    permissions.manage &&
    !locked &&
    (mode === "new" || mode === "loaded") &&
    !record?.archived;
  const existing = !!record && mode === "loaded" && !locked;
  const active = entry?.published.find(
    (p) => p.rule.version === record?.activeVersion,
  );
  if (denied || !permissions.read)
    return (
      <p role="alert">当前权限不足，已清除规则与回执。请重新进入并核对权限。</p>
    );
  return (
    <section aria-label="模拟规则库工作区">
      <p>
        域：{domainId}；当前身份：{profile.ID}
        。发布审核人由服务器身份确定，必须不同于最后编辑者。
      </p>
      {error && <p role="alert">{error}</p>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setOffset(0);
          setFilter(query);
          setRefresh((x) => x + 1);
        }}
      >
        <label>
          规则 ID 筛选
          <input
            aria-label="规则 ID 筛选"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
        <button disabled={busy}>筛选规则</button>
      </form>
      {!page ? (
        <p role="status">加载规则元数据…</p>
      ) : (
        <>
          <p>
            元数据 {page.offset + 1}–{page.offset + page.items.length} /{" "}
            {page.count}（每页最多 {page.limit}）
          </p>
          <ul>
            {page.items.map((r) => (
              <li key={r.id}>
                <button disabled={locked} onClick={() => void load(r.id)}>
                  {r.id}
                </button>{" "}
                草稿 v{r.draftVersion} · 最新发布 v{r.latestPublishedVersion} ·{" "}
                {r.activeVersion ? `活动 v${r.activeVersion}` : "未激活"} ·{" "}
                {r.archived ? "已归档" : "未归档"}
              </li>
            ))}
          </ul>
          {page.items.length === 0 && <p>没有匹配规则。</p>}
          <button
            disabled={offset === 0 || locked}
            onClick={() => setOffset(Math.max(0, offset - page.limit))}
          >
            上一页规则
          </button>
          <button
            disabled={offset + page.items.length >= page.count || locked}
            onClick={() => setOffset(offset + page.items.length)}
          >
            下一页规则
          </button>
        </>
      )}
      <button
        disabled={!permissions.manage || locked}
        onClick={() => {
          generation.current++;
          setSelected("");
          setRecord(null);
          setId("");
          setDeclaration(structuredClone(initial));
          setCases(structuredClone(seedCases));
          setMode("new");
          setAck(false);
        }}
      >
        新建独立草稿
      </button>
      {mode === "loading" && (
        <p role="status">正在读取 {selected}，禁止创建或覆盖…</p>
      )}
      {(mode === "new" || mode === "loaded") && (
        <>
          <h3>{mode === "new" ? "新草稿" : "规则详情"}</h3>
          <label>
            规则 ID
            <input
              aria-label="规则 ID"
              disabled={mode !== "new" || !editable}
              value={id}
              onChange={(e) => setId(e.target.value)}
            />
          </label>
          {record && (
            <p>
              当前权威修订 {record.revision}；草稿 v{record.draftVersion}
              ；已发布 v{record.latestPublishedVersion}；活动版本{" "}
              {record.activeVersion || "无"}；最后编辑者 {record.lastEditorId}；
              {record.archived ? "已归档（保留历史证据）" : "未归档"}
              。编辑、测试或发布不会自动替换活动版本。
            </p>
          )}
          <fieldset disabled={!editable}>
            <legend>结构化规则编辑</legend>
            <label>
              组合
              <select
                aria-label="组合"
                value={declaration.combine}
                onChange={(e) =>
                  setDeclaration({
                    ...declaration,
                    combine: e.target.value as "all" | "any",
                  })
                }
              >
                <option value="all">全部条件</option>
                <option value="any">任一条件</option>
              </select>
            </label>
            {declaration.conditions.map((c, i) => (
              <div key={i}>
                <label>
                  字段 {i + 1}
                  <select
                    aria-label={`字段 ${i + 1}`}
                    value={c.field}
                    onChange={(e) =>
                      setDeclaration({
                        ...declaration,
                        conditions: declaration.conditions.map((x, j) =>
                          j === i ? { ...x, field: e.target.value } : x,
                        ),
                      })
                    }
                  >
                    {fields.map((x) => (
                      <option key={x}>{x}</option>
                    ))}
                  </select>
                </label>
                <label>
                  运算符 {i + 1}
                  <select
                    aria-label={`运算符 ${i + 1}`}
                    value={c.operator}
                    onChange={(e) =>
                      setDeclaration({
                        ...declaration,
                        conditions: declaration.conditions.map((x, j) =>
                          j === i
                            ? {
                                ...x,
                                operator: e.target.value,
                                values: e.target.value === "exists" ? [] : [""],
                              }
                            : x,
                        ),
                      })
                    }
                  >
                    {operators.map((x) => (
                      <option key={x}>{x}</option>
                    ))}
                  </select>
                </label>
                <label>
                  值 {i + 1}（in 每行一个值）
                  <textarea
                    aria-label={`值 ${i + 1}`}
                    disabled={c.operator === "exists"}
                    value={c.values.join("\n")}
                    onChange={(e) =>
                      setDeclaration({
                        ...declaration,
                        conditions: declaration.conditions.map((x, j) =>
                          j === i
                            ? {
                                ...x,
                                values:
                                  c.operator === "in"
                                    ? e.target.value.split("\n")
                                    : [e.target.value],
                              }
                            : x,
                        ),
                      })
                    }
                  />
                </label>
                <button
                  disabled={declaration.conditions.length === 1}
                  onClick={() =>
                    setDeclaration({
                      ...declaration,
                      conditions: declaration.conditions.filter(
                        (_, j) => j !== i,
                      ),
                    })
                  }
                >
                  移除条件 {i + 1}
                </button>
              </div>
            ))}
            <button
              disabled={declaration.conditions.length >= 32}
              onClick={() =>
                setDeclaration({
                  ...declaration,
                  conditions: [
                    ...declaration.conditions,
                    { field: "event.code", operator: "eq", values: [""] },
                  ],
                })
              }
            >
              添加条件
            </button>
          </fieldset>
          <label>
            生成声明 JSON（只读）
            <textarea
              aria-label="生成声明 JSON"
              readOnly
              value={JSON.stringify(declaration, null, 2)}
            />
          </label>
          <button
            disabled={!editable || !id || !validDeclaration(declaration)}
            onClick={() => write(mode === "new" ? "create" : "edit")}
          >
            {mode === "new" ? "保存新草稿" : "保存草稿修订"}
          </button>
          {record && (
            <>
              <fieldset
                disabled={!existing || !permissions.manage || record.archived}
              >
                <legend>合成正反用例（真实服务端评估）</legend>
                {cases.map((c, i) => (
                  <div key={i}>
                    <label>
                      用例 ID {i + 1}
                      <input
                        aria-label={`用例 ID ${i + 1}`}
                        value={c.id}
                        onChange={(e) =>
                          setCases(
                            cases.map((x, j) =>
                              j === i ? { ...x, id: e.target.value } : x,
                            ),
                          )
                        }
                      />
                    </label>
                    <label>
                      期望 {i + 1}
                      <select
                        aria-label={`期望 ${i + 1}`}
                        value={String(c.wantMatch)}
                        onChange={(e) =>
                          setCases(
                            cases.map((x, j) =>
                              j === i
                                ? { ...x, wantMatch: e.target.value === "true" }
                                : x,
                            ),
                          )
                        }
                      >
                        <option value="true">命中（正例）</option>
                        <option value="false">不命中（反例）</option>
                      </select>
                    </label>
                    {fields.map((f) => (
                      <label key={f}>
                        {c.id} {f}
                        <input
                          aria-label={`用例 ${i + 1} ${f}`}
                          value={c.fields[f] ?? ""}
                          onChange={(e) =>
                            setCases(
                              cases.map((x, j) => {
                                if (j !== i) return x;
                                const next = { ...x.fields };
                                if (e.target.value === "") delete next[f];
                                else next[f] = e.target.value;
                                return { ...x, fields: next };
                              }),
                            )
                          }
                        />
                      </label>
                    ))}
                  </div>
                ))}
                <button onClick={() => write("test")}>
                  执行已保存草稿的正反测试
                </button>
              </fieldset>
              <p>
                最新服务端测试：
                {record.testPassed === null
                  ? "尚未测试"
                  : record.testPassed
                    ? "通过"
                    : "失败"}
                。以下为服务器实际结果；编辑器尚未保存的修改不参与测试。
              </p>
              {record.caseResults && (
                <table aria-label="正反用例实际结果">
                  <thead>
                    <tr>
                      <th>用例</th>
                      <th>期望</th>
                      <th>实际</th>
                      <th>一致性</th>
                    </tr>
                  </thead>
                  <tbody>
                    {record.caseResults.map((c) => (
                      <tr key={c.id}>
                        <td>{c.id}</td>
                        <td>{c.wantMatch ? "命中" : "不命中"}</td>
                        <td>{c.actualMatch ? "命中" : "不命中"}</td>
                        <td>
                          {c.wantMatch === c.actualMatch ? "符合" : "失败"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <pre aria-label="实际测试结果">
                {JSON.stringify(
                  {
                    report: entry?.test,
                    caseResults: record.caseResults ?? null,
                  },
                  null,
                  2,
                )}
              </pre>
              <button
                disabled={
                  !existing ||
                  !permissions.publish ||
                  record.archived ||
                  record.lastEditorId === profile.ID ||
                  record.testPassed !== true
                }
                onClick={() => write("publish")}
              >
                以当前身份审核并发布模拟版本
              </button>
              <button
                disabled={
                  !existing ||
                  !permissions.manage ||
                  record.archived ||
                  !record.latestPublishedVersion
                }
                onClick={() => write("enable")}
              >
                激活最新已发布版本
              </button>
              <button
                disabled={
                  !existing ||
                  !permissions.manage ||
                  !record.activeVersion ||
                  record.archived
                }
                onClick={() => write("disable")}
              >
                停用活动版本
              </button>
              <button
                disabled={!existing || !permissions.manage || record.archived}
                onClick={() => write("archive")}
              >
                归档规则并保留证据
              </button>
              <button
                disabled={!existing || !entry?.published.length}
                onClick={async () => {
                  setBusy(true);
                  const c = controller();
                  try {
                    const value = await api.export(
                      domainId,
                      id,
                      profile,
                      c.signal,
                    );
                    if (live.current) {
                      if (value.id !== id)
                        throw new ApiError("invalid_response");
                      downloadCatalog(value);
                    }
                  } catch (e) {
                    failure(e);
                  } finally {
                    if (live.current) setBusy(false);
                  }
                }}
              >
                下载已发布规则 JSON
              </button>
              <pre aria-label="已发布版本">
                {JSON.stringify(entry?.published, null, 2)}
              </pre>
              <fieldset
                disabled={
                  !existing ||
                  !permissions.copy ||
                  !record.copySupported ||
                  !active ||
                  record.archived
                }
              >
                <legend>将活动版本复制为独立手动检测模板</legend>
                {!record.copySupported && (
                  <p>
                    当前活动版本不能复制到既有手动模板；不支持字段：
                    {record.copyUnsupportedFields.join("、") || "尚无活动版本"}
                    。原有映射不会被扩大。
                  </p>
                )}
                <p>
                  这是一次独立副本，不是实时绑定。以后停用或归档规则库不会改变已复制模板、任务或周期计划；需分别管理。复制不启动任务。
                </p>
                <label>
                  <input
                    type="checkbox"
                    aria-label="确认独立副本不级联"
                    checked={ack}
                    onChange={(e) => setAck(e.target.checked)}
                  />
                  我确认独立副本不级联，且原发布摘要与适配后模板摘要不同。
                </label>
                <label>
                  新模板 ID
                  <input
                    aria-label="新模板 ID"
                    value={copyId}
                    onChange={(e) => setCopyId(e.target.value)}
                  />
                </label>
                <label>
                  新模板名称
                  <input
                    aria-label="新模板名称"
                    value={copyName}
                    onChange={(e) => setCopyName(e.target.value)}
                  />
                </label>
                <p>
                  源发布 v{active?.rule.version}；源摘要 {active?.digest}
                </p>
                <button
                  disabled={!ack || !copyId || !copyName}
                  onClick={() => write("create-template")}
                >
                  复制为独立手动模板
                </button>
              </fieldset>
            </>
          )}
        </>
      )}
      {pending && (
        <aside>
          <p>
            未决操作 {pending.input.operationId}
            ；已冻结原始完整输入，不能创建新操作。
          </p>
          <button disabled={busy} onClick={() => void execute(pending, true)}>
            查询回执并安全重试原操作
          </button>
        </aside>
      )}
      {receipt && (
        <aside>
          <h3>历史操作回执（不是当前状态）</h3>
          <pre aria-label="操作回执">{JSON.stringify(receipt, null, 2)}</pre>
          <p>
            当前状态请以上方重新读取的规则修订为准。独立模板来源和两个摘要见
            origin。
          </p>
        </aside>
      )}
    </section>
  );
}
