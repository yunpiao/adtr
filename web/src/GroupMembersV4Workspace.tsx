import { useLayoutEffect, useRef, useState } from "react";
import { ApiError, type Profile } from "./api";
import SavedSourcePicker from "./SavedSourcePicker";
import type { ResolvedSource } from "./domain-selection-api";
import type {
  GroupMembersV4,
  GroupMembersV4Query,
} from "./group-members-v4-api";
import { directoryV4DisplayMember as display } from "./directory-v4-facts";
import {
  groupMembersV4Read as api,
  groupMembersV4ReadError,
  type GroupMembersV4Page,
  type GroupMembersV4PageQuery,
} from "./group-members-v4-read";
import type {
  RawDNReferencesV4,
  RawDNReferencesV4Query,
} from "./raw-dn-references-v4-api";
import type { DirectoryV4Object } from "./directory-v4-facts";
import { listenForSessionInvalidation } from "./session-invalidation";

type Props = { profile: Profile; sessionChanged: () => void };
// Isolated consumer only. No route, producer, grant or mutation integration.
export default function GroupMembersV4Workspace(props: Props) {
  return (
    <Workspace
      key={`${props.profile.ID}:${props.profile.username}:${props.profile.csrfToken}:${props.profile.role}:${props.profile.priv}:${props.profile.needChangePwd}:${props.profile.isExpired}`}
      {...props}
    />
  );
}
function Workspace({ profile, sessionChanged }: Props) {
  const [allowed, setAllowed] = useState(false),
    [busy, setBusy] = useState(true),
    [notice, setNotice] = useState("");
  const [source, setSource] = useState<ResolvedSource | null>(null),
    [picker, setPicker] = useState(0);
  const [page, setPage] = useState<GroupMembersV4Page | null>(null),
    [detail, setDetail] = useState<GroupMembersV4 | null>(null);
  const [kind, setKind] = useState<"user" | "group" | "computer">("group");
  const [references, setReferences] = useState<RawDNReferencesV4 | null>(null);
  const [referencesBusy, setReferencesBusy] = useState(false);
  const referencesQuery = useRef<RawDNReferencesV4Query | null>(null);
  const [detailBusy, setDetailBusy] = useState(false);
  const sourceRef = useRef<ResolvedSource | null>(null),
    query = useRef<GroupMembersV4PageQuery | null>(null),
    detailQuery = useRef<GroupMembersV4Query | null>(null);
  // Independent generations make ignored AbortSignals harmless at all boundaries.
  const domainGeneration = useRef(0),
    sourceGeneration = useRef(0),
    detailGeneration = useRef(0);
  const domainAbort = useRef<AbortController | null>(null),
    sourceAbort = useRef<AbortController | null>(null),
    detailAbort = useRef<AbortController | null>(null);
  const stopDetail = () => {
    detailGeneration.current++;
    detailAbort.current?.abort();
    setDetail(null);
    setReferences(null);
    setReferencesBusy(false);
    referencesQuery.current = null;
    setDetailBusy(false);
  };
  const stopSource = () => {
    sourceGeneration.current++;
    sourceAbort.current?.abort();
    stopDetail();
    setPage(null);
  };
  const clearSource = () => {
    stopSource();
    sourceRef.current = null;
    query.current = null;
    detailQuery.current = null;
    setSource(null);
    setPicker((n) => n + 1);
  };
  const stopDomain = () => {
    domainGeneration.current++;
    domainAbort.current?.abort();
    stopSource();
    setAllowed(false);
  };
  const failure = (e: unknown) => {
    setNotice(groupMembersV4ReadError(e));
    setBusy(false);
    setDetailBusy(false);
    if (e instanceof ApiError && [401, 403].includes(e.status)) {
      stopDomain();
      clearSource();
      if (e.status === 401) sessionChanged();
    } else if (
      e instanceof ApiError &&
      (e.status === 409 || e.code === "selection_changed")
    )
      clearSource();
  };
  const readReferences = async (
    q: RawDNReferencesV4Query,
    target: DirectoryV4Object,
    expectedSource: RawDNReferencesV4["source"],
  ) => {
    stopDetail();
    detailQuery.current = null;
    referencesQuery.current = q;
    setReferencesBusy(true);
    setNotice("");
    const generation = detailGeneration.current,
      domain = domainGeneration.current,
      selected = sourceGeneration.current,
      abort = new AbortController();
    detailAbort.current = abort;
    try {
      const value = await api.references(
        q,
        sourceRef.current!.selection,
        profile,
        abort.signal,
      );
      if (
        abort.signal.aborted ||
        generation !== detailGeneration.current ||
        domain !== domainGeneration.current ||
        selected !== sourceGeneration.current
      )
        return;
      // A response must agree with the exact saved object and source that the
      // user selected, not merely reuse its GUID and observation identifier.
      if (
        Object.keys(expectedSource).some(
          (k) =>
            value.source[k as keyof typeof expectedSource] !==
            expectedSource[k as keyof typeof expectedSource],
        ) ||
        Object.keys(target).some(
          (k) =>
            JSON.stringify(value.object[k as keyof DirectoryV4Object]) !==
            JSON.stringify(target[k as keyof DirectoryV4Object]),
        )
      )
        throw new ApiError("invalid_response");
      setReferences(value);
      setReferencesBusy(false);
    } catch (e) {
      if (
        !abort.signal.aborted &&
        generation === detailGeneration.current &&
        domain === domainGeneration.current &&
        selected === sourceGeneration.current
      ) {
        setReferencesBusy(false);
        failure(e);
      }
    }
  };
  const readDetail = async (q: GroupMembersV4Query) => {
    stopDetail();
    detailQuery.current = q;
    setDetailBusy(true);
    setNotice("");
    const generation = detailGeneration.current,
      domain = domainGeneration.current,
      selected = sourceGeneration.current,
      abort = new AbortController();
    detailAbort.current = abort;
    try {
      const value = await api.detail(q, profile, abort.signal);
      if (
        abort.signal.aborted ||
        generation !== detailGeneration.current ||
        domain !== domainGeneration.current ||
        selected !== sourceGeneration.current
      )
        return;
      setDetail(value);
      setDetailBusy(false);
    } catch (e) {
      if (
        !abort.signal.aborted &&
        generation === detailGeneration.current &&
        domain === domainGeneration.current &&
        selected === sourceGeneration.current
      )
        failure(e);
    }
  };
  const readPage = async (
    q: GroupMembersV4PageQuery,
    selected: ResolvedSource,
    restoreDetail?: GroupMembersV4Query | null,
  ) => {
    stopSource();
    detailQuery.current = null;
    query.current = q;
    setBusy(true);
    setNotice("");
    const generation = sourceGeneration.current,
      domain = domainGeneration.current,
      abort = new AbortController();
    sourceAbort.current = abort;
    try {
      const value = await api.page(
        q,
        selected.selection,
        profile,
        abort.signal,
      );
      if (
        abort.signal.aborted ||
        generation !== sourceGeneration.current ||
        domain !== domainGeneration.current
      )
        return;
      query.current = value.available
        ? { ...q, observationId: value.observationId }
        : q;
      setPage(value);
      setBusy(false);
      if (
        restoreDetail &&
        value.available &&
        restoreDetail.observationId === value.observationId &&
        value.list.some((r) => r.objectGUID === restoreDetail.objectGUID)
      )
        void readDetail(restoreDetail);
    } catch (e) {
      if (
        !abort.signal.aborted &&
        generation === sourceGeneration.current &&
        domain === domainGeneration.current
      )
        failure(e);
    }
  };
  const verify = async () => {
    const previousSource = sourceRef.current,
      previousQuery = query.current,
      previousDetail = detailQuery.current;
    stopDomain();
    setBusy(true);
    setNotice("");
    const generation = domainGeneration.current,
      abort = new AbortController();
    domainAbort.current = abort;
    try {
      await api.authorize(profile, abort.signal);
      if (abort.signal.aborted || generation !== domainGeneration.current)
        return;
      setAllowed(true);
      setBusy(false);
      // Only a completed discovery can be resumed. Never silently rediscover latest.
      if (previousSource && previousQuery?.observationId)
        void readPage(previousQuery, previousSource, previousDetail);
      else if (previousSource) clearSource();
    } catch (e) {
      if (!abort.signal.aborted && generation === domainGeneration.current)
        failure(e);
    }
  };
  useLayoutEffect(() => {
    void verify();
    const focus = () => {
      if (document.visibilityState !== "hidden") void verify();
    };
    const hide = () => {
      stopDomain();
      setBusy(false);
    };
    const visibility = () =>
      document.visibilityState === "hidden" ? hide() : focus();
    const invalidation = listenForSessionInvalidation(() => {
      stopDomain();
      clearSource();
      void verify();
    });
    window.addEventListener("focus", focus);
    window.addEventListener("blur", hide);
    window.addEventListener("popstate", hide);
    document.addEventListener("visibilitychange", visibility);
    return () => {
      domainGeneration.current++;
      sourceGeneration.current++;
      detailGeneration.current++;
      domainAbort.current?.abort();
      sourceAbort.current?.abort();
      detailAbort.current?.abort();
      invalidation.close();
      window.removeEventListener("focus", focus);
      window.removeEventListener("blur", hide);
      window.removeEventListener("popstate", hide);
      document.removeEventListener("visibilitychange", visibility);
    };
  }, []);
  return (
    <section
      aria-labelledby="group-members-v4-title"
      style={{ overflowWrap: "anywhere" }}
    >
      <h2 id="group-members-v4-title">组成员原始观测 · 字典 4</h2>
      <p>
        仅读取已保存的字典 4 观测。多页观测不是 AD
        时间点快照；成员值按接收顺序展示。
      </p>
      <p>
        member
        不包含仅通过主组关联的成员；未做嵌套展开、目标查询或跨域完整性判断。
      </p>
      <p>
        “引用此 DN 原文的组”仅逐字匹配同一观测保存的 DN
        文本，不做大小写、空白或转义等价处理，不解析
        memberOf、嵌套、主组或跨域关系。同 DN 可能对应多个
        GUID；原始采集不是事务快照。
      </p>
      {notice && <p role="alert">{notice}</p>}
      {busy && <p role="status">正在核对权限或读取原观测…</p>}
      {!allowed && !busy && (
        <button onClick={() => void verify()}>重新核对读取权限</button>
      )}
      {allowed && !source && (
        <SavedSourcePicker
          key={picker}
          userID={profile.ID}
          sessionChanged={() => failure(new ApiError("unauthenticated", 401))}
          onResolved={(selected) => {
            clearSource();
            sourceRef.current = selected;
            setSource(selected);
            void readPage(
              {
                domainId: selected.selection.domainId,
                kind,
                pageIdx: 1,
                pageSize: 25,
              },
              selected,
            );
          }}
        />
      )}
      {allowed && source && (
        <>
          <p>
            已选域：{display(source.selection.domain)} · 配置版本{" "}
            {source.selection.revision} · 凭据版本{" "}
            {source.selection.credentialRevision}
          </p>
          <button
            onClick={() => {
              clearSource();
              setNotice("");
              setBusy(false);
            }}
          >
            重新选择数据源
          </button>
          {!busy && query.current?.observationId && (
            <button onClick={() => void readPage(query.current!, source)}>
              重读原观测
            </button>
          )}
          <label>
            对象类别{" "}
            <select
              aria-label="对象类别"
              value={kind}
              disabled={busy}
              onChange={(e) => {
                const next = e.target.value as typeof kind;
                setKind(next);
                void readPage(
                  {
                    ...query.current!,
                    domainId: source.selection.domainId,
                    kind: next,
                    pageIdx: 1,
                    pageSize: query.current?.pageSize ?? 25,
                  },
                  source,
                );
              }}
            >
              <option value="group">组</option>
              <option value="user">用户</option>
              <option value="computer">计算机</option>
            </select>
          </label>
          {page && !page.available && (
            <p role="status">
              暂无可读取的字典 4
              已完成观测，成员情况未知。请重新选择数据源后查询。
            </p>
          )}
          {page?.available && (
            <>
              <dl>
                <dt>观测编号</dt>
                <dd>{display(page.observationId!)}</dd>
                <dt>观测域控</dt>
                <dd>{display(page.source!.dc_host_name)}</dd>
                <dt>观测域</dt>
                <dd>{display(page.source!.domain)}</dd>
                <dt>命名上下文</dt>
                <dd>{display(page.source!.naming_context)}</dd>
                <dt>开始时间（UTC）</dt>
                <dd>{display(page.source!.started_at)}</dd>
                <dt>完成时间（UTC）</dt>
                <dd>{display(page.source!.completed_at)}</dd>
              </dl>
              {page.list.length === 0 && (
                <p role="status">
                  {kind === "group"
                    ? "本页观测未返回组对象；成员情况未知。"
                    : "本页观测未返回所选类别对象。"}
                </p>
              )}
              <ul
                aria-label={
                  kind === "group" ? "本次观测的组" : "本次观测的对象"
                }
              >
                {page.list.map((row) => (
                  <li key={row.objectGUID}>
                    <span>
                      {display(row.samAccountName ?? row.distinguishedName)}
                    </span>{" "}
                    <span>{row.objectGUID}</span>{" "}
                    {row.kind === "group" && (
                      <button
                        onClick={() =>
                          void readDetail({
                            domainId: source.selection.domainId,
                            expectedRevision: source.selection.revision,
                            expectedCredentialRevision:
                              source.selection.credentialRevision,
                            observationId: page.observationId!,
                            objectGUID: row.objectGUID,
                          })
                        }
                      >
                        查看原始成员
                      </button>
                    )}
                    <button
                      onClick={() =>
                        void readReferences(
                          {
                            domainId: source.selection.domainId,
                            expectedRevision: source.selection.revision,
                            expectedCredentialRevision:
                              source.selection.credentialRevision,
                            observationId: page.observationId!,
                            objectGUID: row.objectGUID,
                            pageIdx: 1,
                            pageSize: 25,
                          },
                          row,
                          page.source!,
                        )
                      }
                    >
                      引用此 DN 原文的组
                    </button>
                  </li>
                ))}
              </ul>
              <label>
                {kind === "group" ? "每页组数" : "每页对象数"}{" "}
                <select
                  aria-label={kind === "group" ? "每页组数" : "每页对象数"}
                  value={query.current?.pageSize}
                  onChange={(e) =>
                    void readPage(
                      {
                        ...query.current!,
                        pageIdx: 1,
                        pageSize: Number(e.target.value),
                      },
                      source,
                    )
                  }
                >
                  {[25, 50, 100].map((n) => (
                    <option key={n}>{n}</option>
                  ))}
                </select>
              </label>
              <button
                disabled={page.page.pageIdx <= 1}
                onClick={() =>
                  void readPage(
                    { ...query.current!, pageIdx: page.page.pageIdx - 1 },
                    source,
                  )
                }
              >
                上一页
              </button>
              <span>
                第 {page.page.pageIdx} 页 · 共 {page.page.totalPage}{" "}
                页（仅本次观测中的
                {kind === "group" ? "组" : kind === "user" ? "用户" : "计算机"}
                ）
              </span>
              <button
                disabled={page.page.pageIdx >= page.page.totalPage}
                onClick={() =>
                  void readPage(
                    { ...query.current!, pageIdx: page.page.pageIdx + 1 },
                    source,
                  )
                }
              >
                下一页
              </button>
            </>
          )}
          {(referencesBusy || references) && (
            <button
              onClick={() => {
                stopDetail();
                detailQuery.current = null;
              }}
            >
              关闭 DN 原文引用
            </button>
          )}
          {referencesBusy && <p role="status">正在读取 DN 原文引用…</p>}
          {references && (
            <section aria-label="引用此 DN 原文的组">
              <h3>引用此 DN 原文的组</h3>
              <p>目标 DN：{display(references.object.distinguishedName)}</p>
              <p>
                对象 GUID：{references.object.objectGUID} · 观测编号：
                {display(references.observationId)}
              </p>
              <p>
                本观测组数：{references.coverage.groupCount}；member
                已返回的组：{references.coverage.memberReturnedGroupCount}
                ；member 未返回的组：
                {references.coverage.memberNotReturnedGroupCount}
                （引用情况未知）。
              </p>
              {references.page.total === 0 && (
                <p>本观测未发现完全相同的 DN 文本引用</p>
              )}
              <ul aria-label="DN 原文引用组列表">
                {references.list.map((row) => (
                  <li key={row.objectGUID}>
                    {display(row.distinguishedName)} · {row.objectGUID}
                  </li>
                ))}
              </ul>
              <label>
                每页引用组数{" "}
                <select
                  aria-label="每页引用组数"
                  value={references.page.pageSize}
                  onChange={(e) =>
                    void readReferences(
                      {
                        ...referencesQuery.current!,
                        pageIdx: 1,
                        pageSize: Number(e.target.value),
                      },
                      references.object,
                      references.source,
                    )
                  }
                >
                  {[25, 50, 100].map((n) => (
                    <option key={n}>{n}</option>
                  ))}
                </select>
              </label>
              <button
                disabled={references.page.pageIdx <= 1}
                onClick={() =>
                  void readReferences(
                    {
                      ...referencesQuery.current!,
                      pageIdx: references.page.pageIdx - 1,
                    },
                    references.object,
                    references.source,
                  )
                }
              >
                引用组上一页
              </button>
              <span>
                引用组第 {references.page.pageIdx} 页 · 共{" "}
                {references.page.totalPage} 页
              </span>
              <button
                disabled={references.page.pageIdx >= references.page.totalPage}
                onClick={() =>
                  void readReferences(
                    {
                      ...referencesQuery.current!,
                      pageIdx: references.page.pageIdx + 1,
                    },
                    references.object,
                    references.source,
                  )
                }
              >
                引用组下一页
              </button>
              <button
                onClick={() =>
                  void readReferences(
                    referencesQuery.current!,
                    references.object,
                    references.source,
                  )
                }
              >
                重读 DN 原文引用
              </button>
            </section>
          )}
          {(detailBusy || detail) && (
            <button
              onClick={() => {
                stopDetail();
                detailQuery.current = null;
              }}
            >
              关闭成员详情
            </button>
          )}
          {detailBusy && <p role="status">正在读取所选组的原始成员…</p>}
          {detail && (
            <section aria-label="组原始成员详情">
              <h3>{display(detail.object.distinguishedName)}</h3>
              <p>
                对象 GUID：{detail.object.objectGUID} · 观测编号：
                {display(detail.observationId)}
              </p>
              {detail.object.rawMember === null ? (
                <p>member 未返回，成员情况未知；无法区分属性缺失与权限省略。</p>
              ) : (
                <>
                  <p>
                    本次返回 member 值数：{detail.object.returnedMemberCount}
                    ，不代表完整成员数。
                  </p>
                  {detail.object.rawMember.length === 0 ? (
                    <p>member 已返回空列表，不能据此断定组为空。</p>
                  ) : (
                    <>
                      <p>
                        控制字符、NUL 和双向文本控制符显示为 Unicode
                        编号；原反斜线显示为双反斜线。原值和顺序保持不变，文本不作为
                        DN 链接。
                      </p>
                      <ol
                        aria-label="原始 member 值（可逆展示转义）"
                        style={{
                          whiteSpace: "pre-wrap",
                          overflowWrap: "anywhere",
                        }}
                      >
                        {detail.object.rawMember.map((value, index) => (
                          <li key={index}>{display(value)}</li>
                        ))}
                      </ol>
                    </>
                  )}
                </>
              )}
            </section>
          )}
        </>
      )}
    </section>
  );
}
