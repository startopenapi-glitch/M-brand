import { useState } from "react";
import { api } from "../../lib/api";
import { usePoll } from "../../lib/poll";
import { Button } from "../../components/ui/button";

// 运营首屏：今日生成 / 失败率 / 额度与通道健康 / 试用→付费转化
export default function Dashboard() {
  const [readError,setReadError]=useState("");
  const [s, setS] = useState<any>(null);
  const [probe, setProbe] = useState<{ busy: boolean; ok?: boolean; msg?: string }>({ busy: false });
  const [reconcile, setReconcile] = useState<any>(null);
  usePoll(async () => { try{const outcomes=await Promise.allSettled([api("/api/admin/stats").then(setS),api("/api/admin/agents/reconcile").then(setReconcile)]);const failed=outcomes.find(r=>r.status==="rejected");if(failed?.status==="rejected")throw failed.reason;setReadError("")}catch(e:any){setReadError(e.message);throw e} }, 5000);
  if (!s) return <p role={readError?"alert":"status"} className="text-[var(--muted)]">{readError||"加载中…"}</p>;
  const h = s.img_health || {};
  const conv = s.trial_visitors > 0 ? Math.round((s.trial_converted / s.trial_visitors) * 100) : 0;
  const yuan = (cents: any) => "¥" + (Number(cents || 0) / 100).toFixed(2);
  const cards: [any, string][] = [
    [yuan(s.revenue_today_cents), "今日实收"],
    [yuan(s.revenue_total_cents), "累计实收"],
    [s.orders_paid_today ?? 0, "今日成交"],
    [s.orders_today ?? 0, "今日订单"],
    [s.jobs_today ?? 0, "今日任务"],
    [s.today_images ?? 0, "今日成图"],
    [s.fail_rate_today != null ? Math.round(s.fail_rate_today) + "%" : "—", "今日失败率"],
    [`${s.trial_converted ?? 0}/${s.trial_visitors ?? 0} (${conv}%)`, "试用→付费"],
    [s.codes_active ?? 0, "有效邀请码"],
    [s.jobs_running ?? 0, "进行中"],
  ];
  async function runProbe() {
    setProbe({ busy: true });
    try {
      const r = await api("/api/admin/img-probe");
      setProbe({ busy: false, ok: r.ok, msg: r.message });
      api("/api/admin/stats").then(setS);
    } catch (e: any) {
      setProbe({ busy: false, ok: false, msg: e.message });
    }
  }
  const paused = s.sales_paused === true || h.tripped === true;
  return (
    <div>
      {readError&&<p role="alert" className="notice notice-error mb-3">{readError}；联网后请刷新核对，已有结果已保留。</p>}
      <h1 className="font-sans text-2xl mb-5">运营工作台</h1>

      <div className="surface p-4 mb-5"><h2 className="font-semibold mb-3">优先处理</h2><div className="flex flex-wrap gap-4 text-sm"><a className="text-link" href="/admin/jobs">失败任务 {s.jobs_failed??0} · 查看任务</a><a className="text-link" href="/admin/support">处理售后工单</a><a className="text-link" href="/admin/orders?status=pending">核对待支付订单</a></div></div>
      <div className={`border p-4 mb-5 ${paused ? "bg-[#fdf2f2] border-[#e5b4b0]" : "bg-[#f0f7f0] border-[#bfe3c8]"}`}>
        <div className="flex items-center justify-between flex-wrap gap-2">
          <div>
            <b>生图通道：{paused ? "不可用（已暂停收款与新建任务）" : "正常"}</b>
            <p className="text-xs text-[var(--muted)] mt-1">
              最近成功：{h.last_ok || "—"} · 连续失败：{h.fail_streak ?? 0} · 备用通道：{h.fallback_configured ? "已配置" : "未配置"}
            </p>
            {h.last_err && <p className="text-xs text-[#b3261e] mt-1">最近错误：{String(h.last_err).slice(0, 120)}</p>}
            {s.paused_reason && <p className="text-xs text-[var(--muted)] mt-1">{s.paused_reason}</p>}
          </div>
          <Button variant="outline" disabled={probe.busy} onClick={runProbe}>
            {probe.busy ? "检测中…" : "检测生图通道"}
          </Button>
        </div>
        {probe.msg && (
          <p className={`text-sm mt-2 ${probe.ok ? "text-[#0a7d32]" : "text-[#b3261e]"}`}>
            {probe.ok ? "✓ " : "✗ "}{probe.msg}
          </p>
        )}
      </div>

      {s.worker && <div className={`border p-3 mb-4 text-sm ${s.worker.ready ? "bg-[#f0f7f0] border-[#bfe3c8]" : "bg-[#fdf2f2] border-[#e5b4b0]"}`}>
        Worker：{s.worker.ready ? "正常" : "心跳异常"} · 队列 {s.worker.queued ?? 0} · 运行中 {s.worker.running ?? 0} · 最后心跳 {s.worker.age_seconds ?? "—"} 秒前
      </div>}
      {reconcile && reconcile.anomalies?.length > 0 && <div className="border border-[#e5b4b0] bg-[#fdf2f2] p-3 mb-4 text-sm">佣金对账发现 <b>{reconcile.anomalies.length}</b> 条异常，请到代理页核对。</div>}
      <div className="grid gap-3.5" style={{ gridTemplateColumns: "repeat(auto-fit,minmax(150px,1fr))" }}>
        {cards.map(([v, l]) => (
          <div key={l} className="surface p-4 text-center">
            <b className="block font-sans text-2xl">{v}</b>
            <span className="text-xs text-[var(--muted)]">{l}</span>
          </div>
        ))}
      </div>
      {s.funnel && <div className="surface p-4 mt-4">
        <h2 className="font-sans text-lg mb-3">近 30 天转化</h2>
        <div className="grid gap-3" style={{gridTemplateColumns:"repeat(auto-fit,minmax(110px,1fr))"}}>
          {[["job_created","创建"],["unlock_code","邀请码解锁"],["direction_choose_view","查看方向"],["support_created","售后"]].map(([k,l])=><div key={k} className="text-center"><b className="block text-xl">{s.funnel[k] ?? 0}</b><span className="text-xs text-[var(--muted)]">{l}</span></div>)}
        </div>
      </div>}
      {s.jobs_failed > 0 && (
        <p className="mt-4 text-sm text-[#b3261e]">
          ⚠ 有 {s.jobs_failed} 个失败任务，可到「任务」页一键重试（用户也能自助重试）。
        </p>
      )}
      <p className="mt-4 text-sm text-[var(--muted)]">
        订单：待支付 <b>{s.orders_pending ?? 0}</b> · 已过期 <b>{s.orders_expired ?? 0}</b>
        {s.orders_mock > 0 && <> · 已排除测试单 <b>{s.orders_mock}</b> 笔</>}
        （实收口径：已付款且非测试单；明细见「订单」页）
      </p>
      <p className="mt-4 text-xs text-[var(--muted)]">额度余量以服务商为准；此处按最近调用与告警实时反映通道可用性。</p>
    </div>
  );
}
