import { useLayoutEffect, useRef, useState } from "react";
import type { DirectoryV4Controls } from "./directory-v4-controls-api";
import type { GroupMembersV4Page } from "./group-members-v4-read";
import type { GroupMembersV4 } from "./group-members-v4-api";
import { directoryV4DisplayMember as display } from "./directory-v4-facts";

type Props = { api: DirectoryV4Controls; taskUUID: string; close: () => void; failure: (error: unknown) => void };
// Mounted only by the explicit original-result button. The API, not this UUID
// presentation prop, owns and verifies the immutable original observation pins.
export default function DirectoryV4TaskResult({ api, taskUUID, close, failure }: Props) {
  const [page, setPage] = useState<GroupMembersV4Page | null>(null), [detail, setDetail] = useState<GroupMembersV4 | null>(null);
  const [busy, setBusy] = useState(false), [size, setSize] = useState(25);
  const generation = useRef(0), detailGeneration = useRef(0), request = useRef<AbortController | null>(null), detailRequest = useRef<AbortController | null>(null);
  const clearDetail = () => { detailGeneration.current++; detailRequest.current?.abort(); setDetail(null); };
  const load = async (index: number, pageSize = size) => {
    const current = ++generation.current; request.current?.abort(); clearDetail(); setPage(null); setBusy(true);
    const abort = new AbortController(); request.current = abort;
    try { const value = await api.page(index, pageSize, abort.signal); if (!abort.signal.aborted && current === generation.current) setPage(value); }
    catch (error) { if (!abort.signal.aborted && current === generation.current) failure(error); }
    finally { if (current === generation.current) setBusy(false); }
  };
  const inspect = async (guid: string) => {
    clearDetail(); const current = detailGeneration.current, parent = generation.current, abort = new AbortController(); detailRequest.current = abort;
    try { const value = await api.detail(guid, abort.signal); if (!abort.signal.aborted && current === detailGeneration.current && parent === generation.current) setDetail(value); }
    catch (error) { if (!abort.signal.aborted && current === detailGeneration.current && parent === generation.current) failure(error); }
  };
  useLayoutEffect(() => { void load(1); return () => { generation.current++; detailGeneration.current++; request.current?.abort(); detailRequest.current?.abort(); }; }, [api, taskUUID]);
  return <section aria-label="本任务的原始组成员结果">
    <h3>本任务的原始组成员结果</h3><p>固定原始观测：{display(taskUUID)}。不会读取最新观测；不是 AD 全域原子快照。</p>
    <button onClick={close}>关闭原始结果</button><button disabled={busy} onClick={() => void load(page?.page.pageIdx ?? 1)}>刷新原始结果</button>
    <label>每页组数<select aria-label="每页组数" value={size} onChange={event => { const next = Number(event.target.value); setSize(next); void load(1, next); }}>{[25,50,100].map(value => <option key={value}>{value}</option>)}</select></label>
    {busy && <p role="status">正在核验原始观测…</p>}
    {page && <><p>本观测共有 {page.page.total} 个组行；零组行不等于未采集。</p><ul>{page.list.map(row => <li key={row.objectGUID}><button onClick={() => void inspect(row.objectGUID)}>{display(row.samAccountName ?? row.distinguishedName)}</button></li>)}</ul><nav aria-label="原始结果分页"><button disabled={page.page.pageIdx <= 1} onClick={() => void load(page.page.pageIdx - 1)}>上一页组</button><span>第 {page.page.pageIdx} 页</span><button disabled={page.page.pageIdx >= page.page.totalPage} onClick={() => void load(page.page.pageIdx + 1)}>下一页组</button></nav></>}
    {detail && <section aria-label="组原始成员详情"><h4>{display(detail.object.distinguishedName)}</h4><button onClick={clearDetail}>关闭成员详情</button>
      {detail.object.rawMember === null ? <p>已请求但未返回 member 属性；无法区分属性不存在与 ACL 隐藏。</p> : <><p>本次返回 {detail.object.returnedMemberCount} 个原始值，不代表目录成员总数或完整性。</p>{detail.object.rawMember.length === 0 ? <p>返回零个值，不证明该组为空。</p> : <ol>{detail.object.rawMember.map((member, index) => <li key={index}>{display(member)}</li>)}</ol>}</>}
      <p>保留返回顺序并安全转义；不解析 DN、不跟随范围、不推断嵌套或跨域成员。</p></section>}
  </section>;
}
