import {useAction} from "../../features/admin/shared/useAction";
import { useEffect, useState } from "react";
import { api } from "../../lib/api";
export default function Cases() {
 const action=useAction();
  const [cases, setCases] = useState<any[]>([]);
  const [jobs, setJobs] = useState<any[]>([]);
  const [jid, setJid] = useState(""); const [title, setTitle] = useState(""); const [tagline, setTagline] = useState("");
  const load = () => action.run(async()=>{await Promise.all([api("/api/cases").then(setCases),api("/api/admin/jobs").then(setJobs)])});
  useEffect(() => { load(); }, []);
  const doneJobs = jobs.filter((j) => j.status === "done" && j.images >= 5);
  return (
    <div>
      {action.busy&&<p role="status" className="text-sm mb-3">正在处理，请勿重复提交…</p>}
      {action.error&&<p role="alert" className="notice notice-error mb-3">{action.error}</p>}
      <h1 className="font-sans text-2xl mb-5">案例管理（画廊）</h1>
      <div className="surface p-4 mb-5">
        <p className="text-sm font-semibold mb-2">上架新案例（选已完成任务）</p>
        <div className="flex flex-wrap gap-3 items-end">
          <div><label className="block text-xs text-[#666] mb-1">任务</label>
            <select value={jid} onChange={(e) => setJid(e.target.value)} className="border border-[#ccc] px-3 py-2">
              <option value="">选择…</option>
              {doneJobs.map((j) => <option key={j.id} value={j.id}>{j.id}（{j.code}）</option>)}
            </select></div>
          <div><label className="block text-xs text-[#666] mb-1">标题</label>
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="如：M-brand · 品牌设计" className="border border-[#ccc] px-3 py-2" /></div>
          <div className="flex-1 min-w-[180px]"><label className="block text-xs text-[#666] mb-1">副标题</label>
            <input value={tagline} onChange={(e) => setTagline(e.target.value)} placeholder="符号 / 配色" className="border border-[#ccc] px-3 py-2 w-full" /></div>
          <button className="ctl-btn ctl-btn--primary" disabled={!jid}
            onClick={() => action.run(async () => {
              try { await api("/api/admin/cases", { method: "POST", body: JSON.stringify({ job_id: jid, title, tagline }) }); setTitle(""); setTagline(""); setJid(""); load(); alert("已上架"); }
              catch (e: any) { alert(e.message); }
            })}>上架</button>
        </div>
      </div>
      {cases.map((c) => (
        <div key={c.id} className="surface p-4 mb-3 flex items-center gap-4 flex-wrap">
          <img src={"/" + c.images?.[0]?.file} className="w-16 h-20 object-cover border border-[var(--line)]" />
          <div className="flex-1 min-w-[160px]">
            <b className="font-sans">{c.title}</b>
            <p className="text-xs text-[#666]">{c.tagline}</p>
          </div>
          <button className="ctl-btn-sm"
            onClick={() => action.run(async () => { const t = prompt("新标题", c.title); if (t) { await api(`/api/admin/cases/${c.id}`, { method: "PATCH", body: JSON.stringify({ title: t }) }); load(); } })}>改名</button>
          <button className="ctl-btn-sm ctl-btn-sm--danger"
            onClick={() => action.run(async () => { if (confirm("下架并删除该案例？")) { await api(`/api/admin/cases/${c.id}`, { method: "DELETE" }); load(); } })}>下架</button>
        </div>
      ))}
    </div>
  );
}
