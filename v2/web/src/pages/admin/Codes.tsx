import {useAction} from "../../features/admin/shared/useAction";
import { useEffect, useMemo, useState } from "react";
import { api } from "../../lib/api";

// 邀请码管理：一码一次 · 一码一人（2026-10-13）
// 发码只能发单次码；需要 N 份就一次发 N 张；支持客服解绑/重绑
// 渠道（小红书等）用法：按批次生成 → 复制/导出 → 粘贴到渠道的「自动发货卡密池」→ 导出未用码对账
export default function Codes() {
 const action=useAction();
  const [readError,setReadError]=useState("");
  const [rows, setRows] = useState<any[]>([]);
  const [note, setNote] = useState("");
  const [count, setCount] = useState(10);
  const [made, setMade] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const load = () => api("/api/admin/codes").then(r=>{setReadError("");setRows(r)}).catch(e=>setReadError(e.message));
  useEffect(() => { load(); }, []);

  const shown = useMemo(
    () => rows.filter((r) => !filter || (r.note || "").includes(filter)),
    [rows, filter]
  );
  const stat = useMemo(() => {
    const used = shown.filter((r) => !r.revoked && r.used >= r.max_uses).length;
    const revoked = shown.filter((r) => r.revoked).length;
    return { total: shown.length, unused: shown.length - used - revoked, used, revoked };
  }, [shown]);

  function download(lines: string[], filename: string) {
    const blob = new Blob([lines.join("\n") + "\n"], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 4000);
  }

  async function create() {return action.run(async()=>{
    const r = await api("/api/admin/codes", { method: "POST", body: JSON.stringify({ count, note }) });
    setMade(r.codes || []);
    load();
 });
  }
  async function bind(code: string, visitor: string) {return action.run(async()=>{
    await api(`/api/admin/codes/${code}/bind`, { method: "POST", body: JSON.stringify({ visitor }) });
    load();
 });
  }
  const stateText = (r: any) =>
    r.revoked ? <span className="text-[#b3261e]">已作废</span>
      : r.used >= r.max_uses ? <span className="text-[var(--muted)]">已使用</span>
        : r.bound_visitor ? <span className="text-[#8a6d1a]">已绑定 · 未使用</span>
          : <span className="text-green-700">有效 · 未绑定</span>;

  return (
    <div>
      {action.busy&&<p role="status" className="text-sm mb-3">正在处理，请勿重复提交…</p>}
      {action.error&&<p role="alert" className="notice notice-error mb-3">{action.error}</p>}
      {readError&&<p role="alert" className="notice notice-error mb-3">{readError}；联网后请刷新核对，已有结果已保留。</p>}
      <h1 className="font-sans text-2xl mb-2">邀请码管理</h1>
      <p className="text-xs text-[var(--muted)] mb-5">
        规则：<b>一码一次 · 一码一人</b>。渠道用法（小红书等）：按批次生成 → 「复制全部 / 下载 TXT」→
        粘贴到渠道的「虚拟商品自动发货卡密池」→ 客户拍下自动收到码 → 到网站「兑换邀请码」页兑换。
      </p>

      <div className="surface p-4 flex flex-wrap gap-3 items-end mb-4">
        <div>
          <label className="block text-xs text-[#666] mb-1">张数（1-50）</label>
          <input type="number" min={1} max={50} value={count}
                 onChange={(e) => setCount(+e.target.value)} className="border border-[#ccc] px-3 py-2 w-24" />
        </div>
        <div className="flex-1 min-w-[200px]">
          <label className="block text-xs text-[#666] mb-1">批次备注（建议写渠道+日期，如 小红书1013）</label>
          <input value={note} onChange={(e) => setNote(e.target.value)}
                 className="border border-[#ccc] px-3 py-2 w-full" placeholder="如 小红书1013 / 内测-张三 / 渠道-A" />
        </div>
        <button className="ctl-btn ctl-btn--primary" onClick={create}>
          生成单次码
        </button>
      </div>

      {made.length > 0 && (
        <div className="bg-[#f0f7f0] border border-[#bfe3c8] p-4 mb-5">
          <div className="flex items-center justify-between flex-wrap gap-2 mb-2">
            <p className="text-sm">已生成 {made.length} 张单次码（每行一个，可直接导入渠道卡密池）：</p>
            <div className="flex gap-2">
              <button className="ctl-btn"
                      onClick={() => navigator.clipboard.writeText(made.join("\n")).then(() => alert("已复制全部")).catch(() => prompt("手动复制：", made.join("\n")))}>
                复制全部
              </button>
              <button className="ctl-btn"
                      onClick={() => download(made, `M-brand-邀请码-${new Date().toISOString().slice(0, 10)}.txt`)}>
                下载 TXT
              </button>
            </div>
          </div>
          <div className="flex flex-wrap gap-2 max-h-[160px] overflow-auto">
            {made.map((c) => (
              <button key={c} className="font-mono text-xs bg-white border border-[#bfe3c8] px-2 py-1"
                      onClick={() => navigator.clipboard.writeText(c).catch(() => prompt("手动复制：", c))}>
                {c} ⧉
              </button>
            ))}
          </div>
        </div>
      )}

      <div className="flex flex-wrap items-end gap-3 mb-3">
        <div>
          <label className="block text-xs text-[#666] mb-1">按批次备注筛选</label>
          <input value={filter} onChange={(e) => setFilter(e.target.value)}
                 className="border border-[#ccc] px-3 py-2 w-64" placeholder="如 小红书1013" />
        </div>
        <p className="text-xs text-[var(--muted)] pb-2">
          共 {stat.total} 张：未用 <b className="text-green-700">{stat.unused}</b> · 已用 {stat.used} · 作废 {stat.revoked}
        </p>
        <button className="ctl-btn"
                onClick={() => {
                  const unused = shown.filter((r) => !r.revoked && r.used < r.max_uses).map((r) => r.code);
                  download(unused, `M-brand-未用码-${filter || "全部"}.txt`);
                }}>
          导出「未用码」TXT
        </button>
      </div>

      <div className="surface overflow-x-auto">
        <table className="w-full text-sm">
          <thead><tr className="text-left text-[#666] border-b border-[var(--line)]">
            <th className="p-3">邀请码</th><th className="p-3">状态</th><th className="p-3">绑定会话</th>
            <th className="p-3">使用时间</th><th className="p-3">来源订单</th><th className="p-3">备注</th>
            <th className="p-3">创建</th><th className="p-3"></th>
          </tr></thead>
          <tbody>
            {shown.map((r) => (
              <tr key={r.code} className="border-b border-[var(--soft)] align-top">
                <td className="p-3 font-mono font-bold">{r.code}</td>
                <td className="p-3">{stateText(r)}</td>
                <td className="p-3 font-mono text-[11px] text-[#666]">
                  {r.bound_visitor ? r.bound_visitor.slice(0, 10) + "…" : "—"}
                </td>
                <td className="p-3 text-[#666] text-xs">{r.used_at || "—"}</td>
                <td className="p-3 font-mono text-[11px] text-[#666]">{r.order_id || "—"}</td>
                <td className="p-3 text-[#666]">{r.note}</td>
                <td className="p-3 text-[#666] text-xs">{r.created_at.slice(5, 16)}</td>
                <td className="p-3 whitespace-nowrap">
                  {!r.revoked && (
                    <>
                      {r.bound_visitor ? (
                        <button className="ctl-btn-sm ctl-btn-sm--warn mr-2"
                                onClick={async () => {
                                  if (confirm("解绑 " + r.code + "？（客户可在新设备重新兑换）")) {
                                    await bind(r.code, "");
                                  }
                                }}>解绑</button>
                      ) : (
                        <button className="ctl-btn-sm ctl-btn-sm--warn mr-2"
                                onClick={async () => {
                                  const v = prompt("重绑到的会话 ID（一般直接「解绑」让客户自行兑换即可）：", "");
                                  if (v !== null) await bind(r.code, v.trim());
                                }}>重绑</button>
                      )}
                      <button className="ctl-btn-sm ctl-btn-sm--danger"
                              onClick={async () => {
                                if (confirm("作废 " + r.code + "？")) {
                                  await action.run(()=>api(`/api/admin/codes/${r.code}/revoke`, { method: "POST" }));
                                  load();
                                }
                              }}>作废</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
