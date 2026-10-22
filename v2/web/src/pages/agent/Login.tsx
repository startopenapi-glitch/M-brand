import { BrandMark } from "../../components/SiteLayout";
import { useRef, useState } from "react";
import { api } from "../../lib/api";
import { Button } from "../../components/ui/button";
import { Input, Label } from "../../components/ui/field";

export default function AgentLogin({ onLogin }: { onLogin: () => void }) {
  const [email, setEmail] = useState("");
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [loggingIn, setLoggingIn] = useState(false);
  const loginPending = useRef(false);

  async function login() {
    if (loginPending.current) return;
    loginPending.current = true; setLoggingIn(true); setErr("");
    try {
      await api("/api/agent/login", { method: "POST", body: JSON.stringify({ Email: email, Password: pw }) });
      onLogin();
    } catch (e: any) { setErr(e.message); }
    finally { loginPending.current = false; setLoggingIn(false); }
  }

  return (
    <div className="zb-agent min-h-screen grid place-items-center px-5">
      <form className="surface p-8 w-full max-w-[380px]" aria-label="代理登录" onSubmit={event=>{event.preventDefault();void login()}}>
        <div className="auth-brand"><BrandMark /></div><h1 className="font-sans text-2xl mb-1">代理工作台</h1>
        <p className="text-sm text-[var(--muted)] mb-6">输入你的代理账号</p>
        <Label htmlFor="agent-email">代理邮箱</Label>
        <Input id="agent-email" type="email" autoComplete="username" placeholder="邮箱" required
          value={email} onChange={e => setEmail(e.target.value)} />
        <Label htmlFor="agent-pass" className="mt-4">密码</Label>
        <Input id="agent-pass" autoComplete="current-password" placeholder="密码" type="password" required
          value={pw} onChange={e => setPw(e.target.value)} />
        <Button type="submit" className="w-full mt-5" disabled={loggingIn}>{loggingIn ? "登录中…" : "登录"}</Button>
        {err && <p role="alert" className="text-sm text-[#b3261e] mt-3">{err}</p>}
      </form>
    </div>
  );
}
