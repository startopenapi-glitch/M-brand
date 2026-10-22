import {useSearchParams} from "react-router-dom";
import { useEffect, useRef, useState } from "react";
import {listSupport,reviewSupport,type SupportCase as Case} from "../../features/admin/resources/support/api";
import { Button } from "../../components/ui/button";



const labels: Record<string, string> = { open: "待处理", reviewing: "处理中", resolved: "已解决", rejected: "已拒绝" };

export default function Support() {
  const [params]=useSearchParams();
  const saving=useRef(false); const [busy,setBusy]=useState(false);
  const [rows, setRows] = useState<Case[]>([]);
  const [selected, setSelected] = useState<Case | null>(null);
  const [resolution, setResolution] = useState("");
  const [err, setErr] = useState("");
  const load=()=>listSupport(params.get("order")||"").then(rows=>{setRows(rows);setErr("")}).catch(e=>setErr(e.message));
  useEffect(() => { load(); }, [params.toString()]);
  async function save(status: string) {
    if (!selected||saving.current) return;
    saving.current=true;setBusy(true);setErr("");
    try {
      await reviewSupport(selected,status,resolution);
      setSelected(null); setResolution(""); await load();
    } catch (e: any) { setErr(e.message); } finally{saving.current=false;setBusy(false)}
  }
  return <div>
    <h1 className="font-sans text-2xl mb-2">售后工单</h1>
    <p className="text-xs text-[var(--muted)] mb-5">客户提交的生成失败、付款核对和售后请求。处理结果会记录在事件日志中。</p>
    {err && <p role="alert" className="notice notice-error mb-3">{err}</p>}
    <button className="ctl-btn mb-3" disabled={busy} onClick={()=>load()}>刷新工单</button>
    <div className="grid gap-4 min-w-0 md:grid-cols-2">
      <div className="surface overflow-x-auto">
        <table className="w-full text-sm"><thead><tr className="text-left text-[#666] border-b border-[var(--line)]"><th className="p-3">工单</th><th>类型</th><th>状态</th></tr></thead>
          <tbody>{rows.filter(r=>!params.get("order")||r.order_id===params.get("order")).map(r => <tr key={r.id} className={`border-b border-[var(--soft)] cursor-pointer ${selected?.id === r.id ? "bg-[var(--soft)]" : ""}`} onClick={() => { setSelected(r); setResolution(r.resolution); }}>
            <td className="p-3"><b>{r.id}</b><br/><span className="text-xs text-[var(--muted)]">{r.created_at}</span></td><td>{r.category}</td><td>{labels[r.status] || r.status}</td></tr>)}
            {rows.length === 0 && <tr><td colSpan={3} className="p-6 text-center text-[var(--muted)]">暂无工单</td></tr>}</tbody></table>
      </div>
      <div className="surface p-5">
        {!selected ? <p className="text-[var(--muted)]">选择左侧工单查看详情。</p> : <>
          <div className="flex items-start justify-between gap-3"><div><b>{selected.id}</b><p className="text-xs text-[var(--muted)] mt-1">{selected.job_id && `提案 ${selected.job_id}`} {selected.order_id && `订单 ${selected.order_id}`}</p></div><span className="status-pill">{labels[selected.status] || selected.status}</span></div>
          <div className="flex flex-wrap gap-3 mt-3 text-sm">{selected.order_id&&<a className="text-link" href={`/admin/orders?q=${encodeURIComponent(selected.order_id)}`}>查看关联订单</a>}{selected.job_id&&<a className="text-link" href={`/admin/jobs?id=${encodeURIComponent(selected.job_id)}`}>查看关联任务</a>}</div><p className="mt-4 whitespace-pre-wrap break-words">{selected.description}</p>
          <label className="block text-xs text-[#666] mt-5 mb-1" htmlFor="support-resolution">处理说明</label>
          <textarea id="support-resolution" className="ui-input" rows={4} value={resolution} onChange={e => setResolution(e.target.value)} placeholder="给客户的处理结果、退款/补偿说明或拒绝原因" />
          <div className="flex flex-wrap gap-2 mt-4"><Button disabled={busy} onClick={() => save("resolved")}>标记已解决</Button><Button disabled={busy} variant="outline" onClick={() => save("reviewing")}>处理中</Button><Button disabled={busy} variant="outline" onClick={() => save("rejected")}>拒绝</Button></div>
        </>}
      </div>
    </div>
  </div>;
}
