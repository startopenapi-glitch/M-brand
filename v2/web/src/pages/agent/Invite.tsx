import { useEffect, useState } from "react";
import { BrandMark } from "../../components/SiteLayout";
import { api } from "../../lib/api";
import { Button } from "../../components/ui/button";
import { Input, Label } from "../../components/ui/field";

export default function AgentInvite({ token, onDone }: { token: string; onDone: () => void }) {
  const [info, setInfo] = useState<any>(null), [password, setPassword] = useState(""), [confirm, setConfirm] = useState("");
  const [err, setErr] = useState(""), [busy, setBusy] = useState(false);
  useEffect(() => { api(`/api/agent/invitations/${encodeURIComponent(token)}`).then(setInfo).catch(e=>setErr(e.message)); }, [token]);
  async function accept(e: React.FormEvent) {
    e.preventDefault(); if (busy) return;
    if (password.length < 8) { setErr("密码至少 8 位"); return; }
    if (password !== confirm) { setErr("两次输入的密码不一致"); return; }
    setBusy(true); setErr("");
    try { await api(`/api/agent/invitations/${encodeURIComponent(token)}/accept`, { method: "POST", body: JSON.stringify({ Password: password }) }); onDone(); }
    catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  }
  return <div className="zb-agent min-h-screen grid place-items-center px-5">
    <form className="surface p-8 w-full max-w-[420px]" onSubmit={accept} aria-label="代理邀请开通">
      <div className="auth-brand"><BrandMark /></div><h1 className="font-sans text-2xl mb-1">代理邀请</h1>
      {err && !info ? <p role="alert" className="text-sm text-[#b3261e] mt-4">{err}</p> : !info ? <p className="text-sm text-[var(--muted)] mt-4">正在读取邀请…</p> : <>
        <p className="text-sm text-[var(--muted)] mt-3">你好 {info.name}。设置密码后即可直接开通一级代理工作台。</p>
        <p className="text-xs text-[var(--muted)] mt-2">邮箱：{info.email} · 佣金 {info.commission_pct}%</p>
        <Label htmlFor="invite-password" className="mt-5">设置密码</Label>
        <Input id="invite-password" type="password" autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)} placeholder="至少 8 位" />
        <Label htmlFor="invite-confirm" className="mt-4">确认密码</Label>
        <Input id="invite-confirm" type="password" autoComplete="new-password" value={confirm} onChange={e=>setConfirm(e.target.value)} placeholder="再次输入" />
        <Button type="submit" className="w-full mt-5" disabled={busy}>{busy ? "开通中…" : "设置密码并开通"}</Button>
      </>}
      {err && info && <p role="alert" className="text-sm text-[#b3261e] mt-3">{err}</p>}
    </form>
  </div>;
}
