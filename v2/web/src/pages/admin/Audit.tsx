import { useState } from "react";
import { api } from "../../lib/api";
import { usePoll } from "../../lib/poll";
export default function Audit() {
  const [readError,setReadError]=useState("");
  const [rows, setRows] = useState<any[]>([]);
  usePoll(() => api("/api/admin/audit").then(r=>{setReadError("");setRows(r)}).catch(e=>{setReadError(e.message);throw e}), 6000);
  return (
    <div>
      {readError&&<p role="alert" className="notice notice-error mb-3">{readError}；联网后请刷新核对，已有结果已保留。</p>}
      <h1 className="font-sans text-2xl mb-5">审计日志（近 100 条）</h1>
      <div className="surface text-sm">
        {rows.map((r) => (
          <div key={r.id} className="p-2.5 border-b border-[var(--soft)] flex gap-3 flex-wrap">
            <span className="text-[#666] w-32">{r.created_at.slice(5, 19)}</span>
            <b>{r.actor || "系统"}</b>
            <span className="text-[var(--accent)]">{r.action}</span>
            <span className="text-[#666]">{r.object_type} {r.object_id}</span>
          </div>
        ))}
        {rows.length === 0 && <p className="p-4 text-[var(--muted)]">暂无记录。</p>}
      </div>
    </div>
  );
}
