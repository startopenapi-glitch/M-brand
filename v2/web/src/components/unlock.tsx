import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "./ui/button";
import { Input } from "./ui/field";
import { api, type Plan } from "../lib/api";

export function UnlockCard({
  jobId,
  plan,
  note,
  onUnlocked,
}: {
  jobId?: string;
  plan?: Plan | null;
  note?: string;
  onUnlocked?: () => void;
}) {
  const [code, setCode] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [ok, setOk] = useState("");
  async function redeem() {
    setBusy(true);
    setErr("");
    setOk("");
    try {
      const r = await api("/api/code", { method: "POST", body: JSON.stringify({ code }) });
      setOk(`已兑换 ✓ 剩余额度 ${r.remaining ?? 1} 次`);
      setCode("");
      onUnlocked?.();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="bg-white border-2 border-[var(--brand)] p-6 sm:p-8">
      <h2 className="font-serif text-2xl mb-1.5">继续使用已有权益</h2>
      <p className="text-sm text-[var(--muted)] mb-5">
        {note || `已有邀请码可在这里兑换，之后进入统一的项目交付页面。`}
      </p>
      <Link to={jobId ? `/job/${jobId}` : "/orders"}><Button variant="outline">查看已有提案</Button></Link>
      {plan?.sales_paused && <p className="my-3 text-sm">生成服务维护中，已有权益会保留。</p>}
      <div className="border-t border-[var(--line)] mt-6 pt-4">
        <p className="text-[13px] text-[var(--muted)] mb-2">
          已有邀请码？输入兑换 —— <b>一码一次 · 一码一人</b>（兑换即绑定本设备，他人拿到也用不了）：
        </p>
        <div className="flex gap-2">
          <Input
            placeholder="邀请码"
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            onKeyDown={(e) => { if (e.key === "Enter" && code) redeem(); }}
          />
          <Button variant="outline" disabled={busy || !code} onClick={redeem}>
            {busy ? "验证中…" : "兑换"}
          </Button>
        </div>
        {err && <p className="text-sm text-[#b3261e] mt-2">{err}</p>}
        {ok && <p className="text-sm text-[#0a7d32] mt-2">{ok}</p>}
      </div>
    </div>
  );
}
