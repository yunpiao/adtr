import { useLayoutEffect, useRef, useState } from "react";
import type { DirectoryV4Controls } from "./directory-v4-controls-api";
import { validSourceKeyword, type SourceChoice, type SourcePage } from "./domain-selection-api";
import { directoryV4DisplayMember as display } from "./directory-v4-facts";

type Props = { api: DirectoryV4Controls; choose: (source: SourceChoice) => void; failure: (error: unknown) => void };
export default function DirectoryV4SourcePicker({ api, choose, failure }: Props) {
  const [keyword, setKeyword] = useState(""), [page, setPage] = useState<SourcePage | null>(null), [busy, setBusy] = useState(false);
  const generation = useRef(0), request = useRef<AbortController | null>(null);
  const load = async (pageIdx: number) => {
    const current = ++generation.current;
    request.current?.abort();
    const abort = new AbortController(); request.current = abort;
    setPage(null); setBusy(true);
    try {
      const value = await api.listSources({ pageIdx, pageSize: 10, keyword, observationState: "" }, abort.signal);
      if (!abort.signal.aborted && current === generation.current) setPage(value);
    } catch (error) { if (!abort.signal.aborted && current === generation.current) failure(error); }
    finally { if (current === generation.current) setBusy(false); }
  };
  useLayoutEffect(() => { void load(1); return () => { generation.current++; request.current?.abort(); }; }, [api]);
  return <section aria-label="字典 4 已保存来源">
    <form onSubmit={event => { event.preventDefault(); if (validSourceKeyword(keyword)) void load(1); }}>
      <label>搜索已保存域（最多 50 个字符）<input value={keyword} onChange={event => { generation.current++; request.current?.abort(); setPage(null); setBusy(false); setKeyword(event.target.value); }} /></label>
      <button disabled={busy || !validSourceKeyword(keyword)}>搜索域</button>
    </form>
    {!validSourceKeyword(keyword) && <p role="alert">关键字超过 50 个字符或包含不允许的字符。</p>}
    {busy && <p role="status">正在核验来源…</p>}
    <ul>{page?.List.map(source => <li key={source.domainId}><button type="button" onClick={() => choose(source)}>{display(source.domain)} · 配置 {source.revision} · 凭据 {source.credentialRevision}</button></li>)}</ul>
    {page && <nav aria-label="来源分页"><button disabled={busy || page.page.pageIdx <= 1} onClick={() => void load(page.page.pageIdx - 1)}>上一页来源</button><span>第 {page.page.pageIdx} 页</span><button disabled={busy || page.exhausted} onClick={() => void load(page.page.pageIdx + 1)}>下一页来源</button></nav>}
  </section>;
}
