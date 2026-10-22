import { useRef,useState } from "react";
import { BrandMark } from "../../components/SiteLayout";
import { Button } from "../../components/ui/button";
import { Input,Label } from "../../components/ui/field";
import { api } from "../../lib/api";
export default function AdminLogin({onAuthenticated}:{onAuthenticated:()=>void}){
  const [user, setUser] = useState(""); const [pass, setUserPass] = useState("");
  const [err, setErr] = useState("");
  const [loggingIn, setLoggingIn] = useState(false);
  const [loginStage, setLoginStage] = useState<"password"|"totp"|"setup">("password");
  const [challenge, setChallenge] = useState(""); const [setupToken, setSetupToken] = useState("");
  const [setupSecret, setSetupSecret] = useState(""); const [setupURL, setSetupURL] = useState("");
  const [otp, setOtp] = useState("");
  const loginPending = useRef(false);
  return (
    <div className="zb-admin min-h-screen grid place-items-center px-5">
      <form className="surface p-8 w-full max-w-[380px]" aria-label="管理员登录" onSubmit={async event => {
        event.preventDefault();
        if (loginPending.current) return;
        loginPending.current = true; setLoggingIn(true); setErr("");
        try {
          if (loginStage === "password") {
            const r = await api("/api/admin/login", { method: "POST", body: JSON.stringify({ Username: user, Password: pass }) });
            if (r.totp_setup_required) { setLoginStage("setup"); setSetupToken(r.setup_token); setSetupSecret(r.secret); setSetupURL(r.otpauth_url); setOtp(""); }
            else if (r.totp_required) { setLoginStage("totp"); setChallenge(r.challenge); setOtp(""); }
            else onAuthenticated();
          } else if (loginStage === "totp") {
            await api("/api/admin/totp/verify", { method: "POST", body: JSON.stringify({ Challenge: challenge, Code: otp }) });
            onAuthenticated();
          } else {
            await api("/api/admin/totp/setup", { method: "POST", body: JSON.stringify({ Token: setupToken, Code: otp }) });
            onAuthenticated();
          }
        } catch (e: any) { setErr(e.message); }
        finally { loginPending.current = false; setLoggingIn(false); }
      }}>
        <div className="auth-brand"><BrandMark /></div><h1 className="font-sans text-2xl mb-1">管理后台</h1>
        {loginStage === "password" ? <>
          <Label htmlFor="admin-user" className="mt-5">管理员账号</Label>
          <Input id="admin-user" autoComplete="username" placeholder="管理员账号" required value={user} onChange={(e) => setUser(e.target.value)} />
          <Label htmlFor="admin-pass" className="mt-4">密码</Label>
          <Input id="admin-pass" autoComplete="current-password" placeholder="密码" type="password" required value={pass} onChange={(e) => setUserPass(e.target.value)} />
          <Button type="submit" className="w-full mt-5" disabled={loggingIn}>{loggingIn ? "登录中…" : "登录"}</Button>
        </> : <>
          {loginStage === "setup" ? <>
            <p className="text-sm text-[var(--muted)] mt-4">首次登录需要绑定 TOTP。请把下面密钥加入验证器，然后输入当前 6 位验证码。</p>
            <code className="block break-all bg-[var(--soft)] p-3 rounded mt-3 text-xs">{setupSecret}</code>
            <a className="text-link text-xs break-all block mt-2" href={setupURL} target="_blank" rel="noreferrer">在验证器中打开</a>
          </> : <p className="text-sm text-[var(--muted)] mt-4">请输入验证器中的 6 位动态验证码。</p>}
          <Label htmlFor="admin-otp" className="mt-4">动态验证码</Label>
          <Input id="admin-otp" inputMode="numeric" autoComplete="one-time-code" placeholder="6 位数字" maxLength={6} required value={otp} onChange={(e) => setOtp(e.target.value.replace(/\D/g, ""))} />
          <Button type="submit" className="w-full mt-5" disabled={loggingIn || otp.length !== 6}>{loggingIn ? "验证中…" : loginStage === "setup" ? "绑定并登录" : "验证"}</Button>
          <Button type="button" variant="outline" className="w-full mt-3" onClick={() => { setLoginStage("password"); setErr(""); }}>返回重新输入密码</Button>
        </>}
        {err && <p role="alert" className="text-sm text-[#b3261e] mt-3">{err}</p>}
      </form>
    </div>
  );
}
