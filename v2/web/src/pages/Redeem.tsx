import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/field";
import { api, getPlan, type Plan } from "../lib/api";

/**
 * 兑换邀请码（公开页）：小红书/微信等渠道购买后，客户在这里输入单次码
 * 兑换 = 绑定本设备 → 去创建提案（选定方向后生成已解锁内容）
 */
export default function Redeem() {
  const [code, setCode] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [ok, setOk] = useState<{ code: string; remaining: number } | null>(null);
  const [plan, setPlan] = useState<Plan | null>(null);
  useEffect(() => { getPlan().then(setPlan).catch(() => {}); }, []);

  async function submit() {
    setBusy(true);
    setErr("");
    try {
      const r = await api("/api/code", { method: "POST", body: JSON.stringify({ code }) });
      setOk({ code: r.code, remaining: r.remaining ?? 0 });
      setCode("");
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-[520px] px-5 py-14 pb-24">
      <h1 className="font-sans text-3xl mb-2">兑换邀请码</h1>
      <p className="text-sm text-[var(--muted)] mb-6">
        在<b>小红书 / 微信</b>等渠道购买后，你会收到一张<b>单次邀请码</b>；兑换后可解锁一次 <b>自己的品牌</b>：3 个 Logo 方向 + Logo 组合卡（价值 ¥9.90）。
      </p>

      {!ok ? (
        <div className="surface p-6">
          <label htmlFor="redeem-code" className="block text-xs text-[#666] mb-1">邀请码（一次 9.9 元品牌解锁 · 一码一人）</label>
          <div className="flex gap-2">
            <Input id="redeem-code"
              placeholder="粘贴你收到的邀请码"
              value={code}
              onChange={(e) => setCode(e.target.value.toUpperCase())}
              onKeyDown={(e) => { if (e.key === "Enter" && code) submit(); }}
            />
            <Button disabled={busy || !code} onClick={submit}>{busy ? "验证中…" : "兑换"}</Button>
          </div>
          {err && <p className="text-sm text-[#b3261e] mt-3">{err}</p>}
          <ul className="text-xs text-[var(--muted)] mt-4 space-y-1">
            <li>· 兑换后自动绑定本设备；一张码只解锁一次 3 个方向 + Logo 组合卡（换设备请凭订单号找回）</li>
            <li>· 一张码只能占用一次：解锁自己的品牌：3 个方向 + Logo 组合卡，不附赠 17 个付费交付项</li>
            <li>· 兑换完成后：填品牌信息 → 9.9 元解锁自己的品牌 → 选定方向后生成 Logo 组合卡</li>
          </ul>
        </div>
      ) : (
        <div className="surface border-[#d3e7d9] p-6 text-center">
          <div className="text-[44px] mb-2">✓</div>
          <h2 className="font-sans text-xl mb-1">兑换成功</h2>
          <p className="text-sm text-[var(--muted)] mb-5">
            已绑定到本设备（可用 {ok.remaining} 次）。下一步：填品牌信息，9.9 元解锁自己的品牌：3 个 Logo 方向 + Logo 组合卡。
          </p>
          <div className="flex flex-col gap-3">
            <Link to="/create"><Button size="lg" className="w-full">去创建我的提案 →</Button></Link>
            <Link to="/orders"><Button variant="outline">我的提案与订单</Button></Link>
          </div>
        </div>
      )}

      <p className="text-xs text-[var(--muted)] mt-6">
        兑换遇到问题？联系微信 <b>站点管理员</b>
        {plan?.sales_paused ? "（当前生成通道维护中，兑换不受影响）" : ""}
      </p>
    </div>
  );
}
