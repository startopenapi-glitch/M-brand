import { useEffect, useState } from "react";
import { api } from "../../lib/api";
export default function Usage() {
  const [readError,setReadError]=useState("");
  const [rows, setRows] = useState<any[]>([]);
  useEffect(() => { api("/api/admin/usage?days=30").then(setRows).catch(e=>setReadError(e.message)); }, []);
  const maxImg = Math.max(1, ...rows.map((r) => r.images));
  return (
    <div>
      {readError&&<p role="alert" className="notice notice-error mb-3">{readError}；联网后请刷新核对，已有结果已保留。</p>}
      <h1 className="font-sans text-2xl mb-5">用量报表（近 30 天）</h1>
      <div className="surface p-5">
        {rows.length === 0 && <p className="text-[var(--muted)]">暂无数据。</p>}
        {rows.map((r) => (
          <div key={r.day} className="flex items-center gap-3 mb-2">
            <span className="w-12 text-xs text-[#666]">{r.day}</span>
            <div className="flex-1 h-3 bg-[var(--soft)]">
              <div className="h-full bg-[var(--ink)]" style={{ width: `${(r.images / maxImg) * 100}%` }} />
            </div>
            <span className="w-28 text-xs text-right">{r.images} 图 · {r.tokens} tok</span>
          </div>
        ))}
      </div>
      <p className="text-xs text-[#666] mt-3">口径：每张图一条 usage_events（含 token 消耗）。</p>
    </div>
  );
}
