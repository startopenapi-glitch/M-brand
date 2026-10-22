import { useEffect, useRef, useState } from "react";
import { api } from "../../lib/api";
import { Button } from "../../components/ui/button";

type Me = {
  name: string; code: string; wechat: string; link: string;
  commission_pct: number;
  total_earned_cents: number; total_paid_cents: number; withdrawable_cents: number;
  pending_withdrawal_cents?: number; payout_method?: string; payout_account?: string; payout_name?: string;
};
type Stats = {
  order_count: number; pending_count: number; pending_cents: number;
  total_earned_cents: number; total_paid_cents: number; withdrawable_cents: number;
  pending_withdrawal_cents?: number; payout_method?: string; payout_account?: string; payout_name?: string;
};
type Commission = {
  order_id: string; order_amount_cents: number; commission_cents: number;
  status: string; created_at: string;
};

const fmt = (c: number) => `¥${(c / 100).toFixed(2)}`;

export default function AgentDashboard() {
  const [me, setMe] = useState<Me | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const [comms, setComms] = useState<Commission[]>([]);
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState("");
  const [copying, setCopying] = useState(false);
  const [payouts, setPayouts] = useState<any[]>([]);
  const [payoutAmount, setPayoutAmount] = useState(""), [payoutMsg, setPayoutMsg] = useState(""), [payoutBusy, setPayoutBusy] = useState(false);
  const [account, setAccount] = useState({ Method: "alipay", Account: "", Name: "" });
  const [analytics, setAnalytics] = useState<any>(null);
  const linkInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(timer);
  }, [copied]);

  useEffect(() => {
    api("/api/agent/me").then(setMe);
    api("/api/agent/me/stats").then(setStats);
    api("/api/agent/me/commissions").then(setComms);
    api("/api/agent/me/payouts").then(setPayouts).catch(()=>{});
    api("/api/agent/me/analytics").then(setAnalytics).catch(()=>{});
  }, []);
  useEffect(() => {
    if (me) setAccount({ Method: me.payout_method || "alipay", Account: "", Name: me.payout_name || "" });
  }, [me]);

  async function saveAccount() {
    setPayoutBusy(true); setPayoutMsg("");
    try { await api("/api/agent/me/payout-account", { method: "PUT", body: JSON.stringify(account) }); setPayoutMsg("收款账户已保存"); api("/api/agent/me").then(setMe); }
    catch (e: any) { setPayoutMsg(e.message); } finally { setPayoutBusy(false); }
  }
  async function requestPayout() {
    setPayoutBusy(true); setPayoutMsg("");
    try { await api("/api/agent/me/payouts", { method: "POST", body: JSON.stringify({ AmountCents: Math.round(Number(payoutAmount) * 100), RequestKey: crypto.randomUUID() }) }); setPayoutAmount(""); setPayoutMsg("提现申请已提交，等待管理员审核"); setPayouts(await api("/api/agent/me/payouts")); api("/api/agent/me/stats").then(setStats); api("/api/agent/me").then(setMe); }
    catch (e: any) { setPayoutMsg(e.message); } finally { setPayoutBusy(false); }
  }
  if (!me || !stats) return <p className="text-[var(--muted)] text-center">加载中…</p>;

  return (
    <div>
      <h1 className="font-sans text-2xl mb-6">你好，{me.name}</h1>

      {/* 推广码区 */}
      <div className="surface p-5 mb-6">
        <h2 className="font-sans text-lg mb-3">我的推广工具</h2>
        <div className="flex items-center gap-4 flex-wrap">
          <div className="text-center">
            <div className="bg-[#111] text-[#ffd97a] font-mono text-xl px-4 py-2 tracking-widest">{me.code}</div>
            <p className="text-xs text-[var(--muted)] mt-1">推广码</p>
          </div>
          <div className="min-w-0 w-full">
            <div className="flex gap-2">
              <input ref={linkInput} readOnly value={me.link}
                aria-label="推广链接" className="min-w-0 flex-1 border border-[#ccc] px-3 py-2 text-sm bg-[#fafafa]" />
              <button className="bg-[var(--ink)] text-white font-semibold px-4 py-2 text-sm" disabled={copying}
                onClick={async () => {
                  setCopying(true); setCopied(false); setCopyError("");
                  try { await navigator.clipboard.writeText(me.link); setCopied(true); }
                  catch { setCopyError("自动复制未完成，请长按或手动复制输入框里已选中的推广链接。"); linkInput.current?.focus(); linkInput.current?.select(); }
                  finally { setCopying(false); }
                }}>{copied ? "✓ 已复制" : copying ? "复制中…" : "复制"}</button>
            </div>
            {copyError && <p role="status" className="text-xs text-[var(--accent)] mt-2">{copyError}</p>}
            <p className="text-xs text-[var(--muted)] mt-1">分享此链接，客户通过它付费后你自动获得 {me.commission_pct}% 佣金</p>
          </div>
        </div>
      </div>

      {/* 业绩面板 */}
      <div className="grid gap-3 mb-6" style={{ gridTemplateColumns: "repeat(auto-fit,minmax(150px,1fr))" }}>
        <div className="surface p-4 text-center">
          <b className="block font-sans text-2xl">{stats.order_count}</b>
          <span className="text-xs text-[var(--muted)]">推广订单</span>
        </div>
        <div className="surface p-4 text-center">
          <b className="block font-sans text-2xl text-[#316b48]">{fmt(stats.total_earned_cents)}</b>
          <span className="text-xs text-[var(--muted)]">累计佣金</span>
        </div>
        <div className="surface p-4 text-center">
          <b className="block font-sans text-2xl">{fmt(stats.total_paid_cents)}</b>
          <span className="text-xs text-[var(--muted)]">已提现</span>
        </div>
        <div className="bg-white border border-[var(--line)] rounded-[18px] p-4 text-center">
          <b className="block font-sans text-2xl text-[var(--accent)]">{fmt(stats.withdrawable_cents)}</b>
          <span className="text-xs text-[var(--muted)]">可提现</span>
        </div>
      </div>

      <div className="surface p-5 mb-6">
        <h2 className="font-sans text-lg mb-2">提现</h2>
        <p className="text-xs text-[var(--muted)] mb-3">最低提现 ¥100，申请后 7 天内不能再次申请。可提现 {fmt(stats.withdrawable_cents)}，待审核 {fmt(me.pending_withdrawal_cents || 0)}。</p>
        <div className="grid gap-3" style={{gridTemplateColumns:"repeat(auto-fit,minmax(150px,1fr))"}}>
          <label className="text-xs">收款方式<select className="ui-input" value={account.Method} onChange={e=>setAccount({...account,Method:e.target.value})}><option value="alipay">支付宝</option><option value="bank">银行卡</option></select></label>
          <label className="text-xs">收款账户<input className="ui-input" value={account.Account} onChange={e=>setAccount({...account,Account:e.target.value})} placeholder={me.payout_account ? `已保存 ${me.payout_account}，输入新账户可替换` : "微信号或银行卡号"}/></label>
          <label className="text-xs">收款人<input className="ui-input" value={account.Name} onChange={e=>setAccount({...account,Name:e.target.value})} placeholder="姓名"/></label>
        </div>
        <Button className="mt-3" variant="outline" disabled={payoutBusy||!account.Account.trim()||!account.Name.trim()} onClick={saveAccount}>保存收款账户</Button>
        <div className="flex gap-2 items-end mt-4 max-w-[360px]"><label className="text-xs flex-1">提现金额（元）<input className="ui-input" inputMode="decimal" value={payoutAmount} onChange={e=>setPayoutAmount(e.target.value)} placeholder="最低 100"/></label><Button disabled={payoutBusy||Number(payoutAmount)<100} onClick={requestPayout}>申请提现</Button></div>
        {payoutMsg && <p role="status" className="text-xs mt-2">{payoutMsg}</p>}
        <div className="mt-4"><a className="text-link text-xs" href="/api/agent/me/statement.csv">下载佣金/提现明细 CSV</a></div>
        {payouts.length>0&&<div className="mt-4 text-xs">{payouts.slice(0,5).map((x:any)=><div key={x.id} className="flex justify-between border-b border-[var(--soft)] py-1"><span>{x.requested_at} · {x.status}</span><b>{fmt(x.amount_cents)}</b></div>)}</div>}
      </div>
      {analytics && <div className="surface p-4 mb-6">
        <h2 className="font-sans text-lg mb-2">近 30 天推广效果</h2>
        <div className="grid gap-3" style={{gridTemplateColumns:"repeat(auto-fit,minmax(110px,1fr))"}}>
          <div className="text-center"><b className="block text-xl">{analytics.visitors ?? 0}</b><span className="text-xs text-[var(--muted)]">访客</span></div>
          <div className="text-center"><b className="block text-xl">{analytics.events?.job_created ?? 0}</b><span className="text-xs text-[var(--muted)]">创建品牌</span></div>
          <div className="text-center"><b className="block text-xl">{analytics.events?.unlock_code ?? 0}</b><span className="text-xs text-[var(--muted)]">邀请码解锁</span></div>
        </div>
      </div>}
      {/* 佣金明细 */}
      <div className="surface mb-6">
        <div className="px-4 py-3 border-b border-[var(--line)]">
          <b>佣金明细</b>
          {stats.pending_cents > 0 && (
            <span className="ml-3 text-xs text-[var(--accent)]">待结算 {fmt(stats.pending_cents)}（{stats.pending_count} 笔）</span>
          )}
        </div>
        {comms.length === 0 ? (
          <p className="p-8 text-center text-[var(--muted)] text-sm">暂无佣金记录<br/>分享推广链接给潜在客户吧</p>
        ) : (
          comms.map(c => (
            <div key={c.order_id} className="flex flex-wrap gap-3 items-center justify-between px-4 py-2.5 border-b border-[var(--soft)] last:border-0">
              <div>
                <span className="font-mono text-xs text-[var(--muted)]">{c.order_id.slice(0,12)}…</span>
                <span className="ml-2 text-xs text-[var(--muted)]">{c.created_at}</span>
              </div>
              <div className="flex items-center gap-3">
                <span className="text-xs text-[var(--muted)]">订单 {fmt(c.order_amount_cents)}</span>
                <b className="text-sm text-[#316b48]">+{fmt(c.commission_cents)}</b>
                <span className={`text-[10px] px-1.5 py-0.5 rounded ${c.status === "paid" ? "bg-[#e8f5e9] text-[#2e7d32]" : "bg-[#fff3d9] text-[#8a6d1a]"}`}>
                  {c.status === "paid" ? "已结算" : "待结算"}
                </span>
              </div>
            </div>
          ))
        )}
      </div>

      {/* 提现说明 */}
      <div className="bg-[var(--canvas)] border border-[var(--line)] p-4 text-sm text-[var(--muted)]">
        <b>提现说明</b>
        <p className="mt-1 text-xs">可提现余额 = 累计佣金 - 已提现。请联系管理员（站点管理员）申请提现。</p>
      </div>
    </div>
  );
}
