import {useAction} from "../../features/admin/shared/useAction";
import { useEffect, useRef, useState } from "react";
import { api } from "../../lib/api";

type Agent = {
  id: string; name: string; email: string; code: string; wechat: string;
  commission_pct: number; active: boolean;
  total_earned_cents: number; total_paid_cents: number; withdrawable_cents: number;
  created_at: string;
};
type Invitation = {id:string;email:string;name:string;commission_pct:number;expires_at:string;created_at:string};
const parseCommission=(text:string)=>{const n=Number(text);return text.trim()!==""&&Number.isInteger(n)&&n>=1&&n<=90?n:null};

type Stats = {
  agent_count: number; active_count: number;
  total_commission_cents: number; total_paid_cents: number; total_pending_cents: number;
};

const pendingPayout=(id:string)=>{try{return !!localStorage.getItem(`zb-payout-pending:${id}`)}catch{return false}};
const fmt = (c: number) => `¥${(c / 100).toFixed(2)}`;

export default function Agents() {
 const commands=useAction();
  const [rows, setRows] = useState<Agent[]>([]);
  const [stats, setStats] = useState<Stats | null>(null);
  const [payouts, setPayouts] = useState<any[]>([]);
  const paying=useRef(false);const [payoutBusy,setPayoutBusy]=useState(false);
  const [msg, setMsg] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ Name: "", Email: "", Password: "", Wechat: "", CommissionPct: 40 });

  const load = () => {
    api("/api/admin/agents").then(setRows).catch(e=>setMsg(e.message));
    api("/api/admin/agents/stats").then(setStats).catch(e=>setMsg(e.message));
    api("/api/admin/agent-payouts").then(setPayouts).catch(e=>setMsg(e.message));
  };
  useEffect(() => { load(); }, []);

  const [inviteLink, setInviteLink] = useState("");
  const [invitePct,setInvitePct]=useState("40");
  const [invitations,setInvitations]=useState<Invitation[]>([]),[showInvitations,setShowInvitations]=useState(false);
  const [editing,setEditing]=useState<{id:string;email:string;kind:"agent"|"invitation";pct:string}|null>(null);
  async function loadInvitations(){try{setInvitations(await api("/api/admin/agents/invitations"))}catch(e:any){setMsg(e.message)}}

  async function invite() {
    const pct=parseCommission(invitePct);
    if(pct===null){setMsg("佣金比例需为 1–90 之间的整数");return}
    if(!form.Email.trim()){setMsg("请填写代理邮箱");return}
    return commands.run(async()=>{
      try {
        const r=await api("/api/admin/agents/invite",{method:"POST",body:JSON.stringify({Email:form.Email,CommissionPct:pct})});
        setMsg(r.email_sent?"✓ 邀请邮件已发送":"邀请已创建，邮件未发送。请复制链接："+r.invite_url);
        setInviteLink(r.invite_url||"");setShowForm(false);setShowInvitations(true);await loadInvitations();
      }catch(e:any){setMsg(e.message)}
    });
  }
  async function saveCommission(event:React.FormEvent){
    event.preventDefault();if(!editing)return;
    const pct=parseCommission(editing.pct);if(pct===null){setMsg("佣金比例需为 1–90 之间的整数");return}
    const target=editing;
    return commands.run(async()=>{try{
      const path=target.kind==="invitation"?`/api/admin/agents/invitations/${target.id}`:`/api/admin/agents/${target.id}`;
      await api(path,{method:"PATCH",body:JSON.stringify({CommissionPct:pct})});
      setEditing(null);setMsg("✓ 佣金比例已更新，仅影响后续新订单");
      if(target.kind==="invitation")await loadInvitations();else setRows(await api("/api/admin/agents"));
    }catch(e:any){setMsg(e.message)}});
  }

  async function payout(agent: Agent) {
    if(paying.current)return;
    const storageKey=`zb-payout-pending:${agent.id}`;
    let pending:{AmountCents:number;RequestKey:string}|null=null;
    try {const raw=localStorage.getItem(storageKey);if(raw)pending=JSON.parse(raw);}catch{setMsg("无法读取待核对结算，请检查浏览器存储或联系支持，未提交新结算。");return;}
    if(!pending){
      const amount=prompt(`仅记录已人工打款的金额（元）\n代理：${agent.name}\n可结算：${fmt(agent.withdrawable_cents)}`,(agent.withdrawable_cents/100).toFixed(2));
      if(amount===null)return;
      const text=amount.trim();if(!/^\d+(?:\.\d{1,2})?$/.test(text)){setMsg("请输入正数金额，最多两位小数。");return;}
      const [yuan,fraction='']=text.split('.');const cents=Number(yuan)*100+Number(fraction.padEnd(2,'0'));
      if(!Number.isSafeInteger(cents)||cents<=0||cents>agent.withdrawable_cents){setMsg("金额必须大于零且不能超过可结算余额。");return;}
      pending={AmountCents:cents,RequestKey:crypto.randomUUID()};
      try{localStorage.setItem(storageKey,JSON.stringify(pending));}catch{setMsg("无法保存结算请求编号，未提交；请允许浏览器存储后重试。");return;}
    }
    paying.current=true;setPayoutBusy(true);
    try {
      await api(`/api/admin/agents/${agent.id}/payout`, {method:"POST",body:JSON.stringify(pending)});
      localStorage.removeItem(storageKey);setMsg("✓ 已记录人工结算");load();
    }catch(e:any){setMsg(`${e.message}。已保留本次结算请求，下次点击将核对同一笔，不会另记一笔。`);}
    finally{paying.current=false;setPayoutBusy(false);}
  }

  async function reviewPayout(row:any, decision:string) {return commands.run(async()=>{
    let note="", reference="";
    if(decision==="reject"){const v=prompt("拒绝原因");if(v===null)return;note=v;}
    if(decision==="paid"){const v=prompt("打款流水号（必填）");if(v===null)return;reference=v;}
    try{await api(`/api/admin/agent-payouts/${row.id}/review`,{method:"POST",body:JSON.stringify({Action:decision,Note:note,Reference:reference})});setMsg("✓ 提现状态已更新");load();}catch(e:any){setMsg(e.message)}
 });
  }
  async function toggle(agent: Agent) {return commands.run(async()=>{
    try{await api(`/api/admin/agents/${agent.id}`, { method: "PATCH", body: JSON.stringify({ Active: !agent.active }) });load();}catch(e:any){setMsg(e.message)}
 });
  }

  return (
    <div>
      {commands.busy&&<p role="status" className="text-sm mb-3">正在处理，请勿重复提交…</p>}
      {commands.error&&<p role="alert" className="notice notice-error mb-3">{commands.error}</p>}
      <h1 className="font-sans text-2xl mb-5">代理管理</h1>

      {stats && (
        <div className="grid gap-3 mb-5" style={{ gridTemplateColumns: "repeat(auto-fit,minmax(150px,1fr))" }}>
          <div className="surface p-4 text-center">
            <b className="block text-xl">{stats.agent_count}</b>
            <span className="text-xs text-[var(--muted)]">总代理</span>
          </div>
          <div className="surface p-4 text-center">
            <b className="block text-xl text-[#316b48]">{fmt(stats.total_commission_cents)}</b>
            <span className="text-xs text-[var(--muted)]">总佣金</span>
          </div>
          <div className="surface p-4 text-center">
            <b className="block text-xl">{fmt(stats.total_paid_cents)}</b>
            <span className="text-xs text-[var(--muted)]">已提现</span>
          </div>
          <div className="surface p-4 text-center">
            <b className="block text-xl text-[var(--accent)]">{fmt(stats.total_pending_cents)}</b>
            <span className="text-xs text-[var(--muted)]">待结算</span>
          </div>
        </div>
      )}

      {msg && <div className="bg-[#f0f7f0] border border-[#bfe3c8] px-4 py-2 mb-4 text-sm">{msg}</div>}

      <div className="flex justify-between mb-4">
        <button className="bg-[var(--ink)] text-white font-semibold px-5 py-2 text-sm" onClick={() => setShowForm(!showForm)}>
          {showForm ? "取消" : "+ 新建代理"}
        </button>
      </div>

      {showForm && (
        <div className="surface p-4 mb-5">
          <p className="text-sm font-semibold mb-2">通过邮箱邀请代理</p>
          <p className="text-xs text-[var(--muted)] mb-3">填写邮箱和佣金比例。代理会收到一次性开通链接，自行设置密码。</p>
          <div className="flex flex-wrap gap-3 items-end">
            <div className="flex-1 min-w-[220px]"><label htmlFor="agent-invite-email" className="block text-xs text-[#666] mb-1">代理邮箱</label>
              <input id="agent-invite-email" type="email" className="w-full border border-[#ccc] px-3 py-2" value={form.Email}
                onChange={e => setForm({...form, Email: e.target.value})} placeholder="agent@example.com" /></div>
            <div className="min-w-[130px]"><label htmlFor="agent-invite-pct" className="block text-xs text-[#666] mb-1">邀请佣金比例（%）</label><input id="agent-invite-pct" type="number" min="1" max="90" step="1" className="w-full border border-[#ccc] px-3 py-2" value={invitePct} onChange={e=>setInvitePct(e.target.value)}/></div>
            <button disabled={commands.busy} className="bg-[var(--ink)] text-white font-semibold px-5 py-2 text-sm" onClick={invite}>发送邮件邀请</button>
          </div>
          {inviteLink&&<p className="text-xs mt-2 break-all">邀请链接：{inviteLink}</p>}
        </div>
      )}

      <p className="text-xs text-[var(--muted)] mb-3">佣金比例为 1–90% 的整数。调整仅影响后续新订单，已创建订单和历史佣金保持原金额。</p>
      {editing&&<form className="surface p-4 mb-5" aria-label="调整代理佣金" onSubmit={saveCommission}>
        <h2 className="font-sans text-lg mb-2">调整佣金</h2><p className="text-sm mb-3 break-all">{editing.email} · {editing.kind==="invitation"?"待开通邀请":"已开通代理"}</p>
        <label htmlFor="agent-edit-pct" className="block text-xs mb-1">新佣金比例（%）</label>
        <div className="flex flex-wrap gap-3 items-center"><input id="agent-edit-pct" type="number" min="1" max="90" step="1" required className="border border-[#ccc] px-3 py-2 w-32" value={editing.pct} onChange={e=>setEditing({...editing,pct:e.target.value})}/>
        <button type="submit" disabled={commands.busy} className="border border-[var(--ink)] px-4 py-2 text-sm">保存佣金</button><button type="button" disabled={commands.busy} className="border border-[#ccc] px-4 py-2 text-sm" onClick={()=>setEditing(null)}>取消修改</button></div>
      </form>}
      <div className="mb-5"><button className="border border-[#ccc] px-4 py-2 text-sm" disabled={commands.busy} onClick={()=>{setShowInvitations(v=>!v);if(!showInvitations)void loadInvitations()}}>{showInvitations?"收起待开通邀请":"待开通邀请"}</button>
      {showInvitations&&<section className="surface p-4 mt-3" aria-label="待开通邀请"><h2 className="font-sans text-lg mb-2">待开通邀请</h2>{invitations.length===0?<p className="text-sm text-[var(--muted)]">暂无有效待开通邀请</p>:invitations.map(inv=><div key={inv.id} className="flex flex-wrap items-center gap-3 py-3 border-b border-[var(--soft)] last:border-0"><div className="flex-1 min-w-0"><p className="text-sm break-all">{inv.email}</p><p className="text-xs text-[var(--muted)]">有效期至 {new Date(inv.expires_at).toLocaleString("zh-CN",{hour12:false})}</p></div><b>{inv.commission_pct}%</b><button disabled={commands.busy} aria-label={`调整 ${inv.email} 的佣金`} className="text-xs border border-[#ccc] px-2 py-2" onClick={()=>setEditing({id:inv.id,email:inv.email,kind:"invitation",pct:String(inv.commission_pct)})}>调整佣金</button></div>)}</section>}
      </div>

      {payouts.length>0 && <div className="surface p-4 mb-5">
        <h2 className="font-sans text-lg mb-3">待处理提现</h2>
        {payouts.filter(p=>p.status==="pending_review"||p.status==="approved").map(p=><div key={p.id} className="flex flex-wrap items-center gap-3 border-b border-[var(--soft)] py-2 last:border-0">
          <div className="flex-1 min-w-[180px]"><b>{p.agent_name}</b><p className="text-xs text-[var(--muted)]">{p.agent_id} · {p.requested_at}</p></div>
          <b>{fmt(p.amount_cents)}</b><span className="text-xs">{p.status==="pending_review"?"待审核":"待打款"}</span>
          {p.status==="pending_review"&&<><button className="text-xs border border-[#09bb07] px-2 py-1" onClick={()=>reviewPayout(p,"approve")}>通过</button><button className="text-xs border border-[#ccc] px-2 py-1" onClick={()=>reviewPayout(p,"reject")}>拒绝</button></>}
          {p.status==="approved"&&<button className="text-xs border border-[#09bb07] px-2 py-1" onClick={()=>reviewPayout(p,"paid")}>记录打款</button>}
        </div>)}
      </div>}
      <div className="surface overflow-x-auto">
        <table className="w-full text-sm">
          <thead><tr className="text-left text-[#666] border-b border-[var(--line)]">
            <th className="p-3">代理</th><th className="p-3">推广码</th><th className="p-3">佣金%</th>
            <th className="p-3 text-right">累计</th><th className="p-3 text-right">已提现</th>
            <th className="p-3 text-right">可提现</th><th className="p-3">状态</th><th className="p-3"></th>
          </tr></thead>
          <tbody>
            {rows.map(a => (
              <tr key={a.id} className="border-b border-[var(--soft)]">
                <td className="p-3"><b>{a.name}</b><br/><span className="text-xs text-[var(--muted)]">{a.email}</span></td>
                <td className="p-3 font-mono">{a.code}</td>
                <td className="p-3">{a.commission_pct}%</td>
                <td className="p-3 text-right">{fmt(a.total_earned_cents)}</td>
                <td className="p-3 text-right">{fmt(a.total_paid_cents)}</td>
                <td className="p-3 text-right font-bold text-[var(--accent)]">{fmt(a.withdrawable_cents)}</td>
                <td className="p-3">{a.active ? <span className="text-[#316b48]">✓ 正常</span> : <span className="text-[var(--muted)]">停用</span>}</td>
                <td className="p-3 flex gap-2">
                  <button disabled={commands.busy} aria-label={`调整 ${a.email} 的佣金`} className="text-xs border border-[#ccc] px-2 py-1" onClick={()=>setEditing({id:a.id,email:a.email,kind:"agent",pct:String(a.commission_pct)})}>调整佣金</button>
                  {(a.withdrawable_cents > 0 || pendingPayout(a.id)) && (
                    <button className="text-xs border border-[#09bb07] text-[#316b48] px-2 py-1"
                      disabled={payoutBusy} onClick={() => payout(a)}>{pendingPayout(a.id)?"核对上次结算":"记录结算"}</button>
                  )}
                  <button className="text-xs border border-[#ccc] px-2 py-1"
                    onClick={() => toggle(a)}>{a.active ? "停用" : "启用"}</button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && <tr><td colSpan={8} className="p-8 text-center text-[var(--muted)]">暂无代理</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
