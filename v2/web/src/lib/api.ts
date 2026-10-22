const BASE = "";
// Bound both response headers and body reads; a stuck request must release the
// serial poll. Mutations are never automatically retried here.
export async function api(path: string, opts?: RequestInit) {
  const controller = new AbortController();
  const abort = () => controller.abort();
  let timedOut = false;
  if (opts?.signal?.aborted) abort();
  opts?.signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(() => { timedOut = true; controller.abort(); }, 20000);
  try {
    const r = await fetch(BASE + path, {
      credentials: "include", headers: { "Content-Type": "application/json" }, ...opts, signal: controller.signal,
    });
    const data = await r.json().catch(e => {
      if (controller.signal.aborted) throw e;
      // A cut-off 200 response is unknown, not an empty successful result.
      // Let callers retain their state and retry reads with the same identity.
      if (r.ok) throw new Error("响应未完整接收，结果暂未确认，请重试核对。");
      return {};
    });
    if (!r.ok) {
      const e: any = new Error(data.error || `HTTP ${r.status}`); e.status = r.status; e.data = data; throw e;
    }
    return data;
  } catch (e) {
    if (timedOut) throw new Error("连接超时，结果暂未确认，请重试核对。");
    throw e;
  } finally {
    clearTimeout(timer); opts?.signal?.removeEventListener("abort", abort);
  }
}
export const IMG = (p: string) => BASE + "/" + p;

export type Plan = {
  price_fen: number;
  price_label: string;
  title: string;
  trial_directions?: number;
  includes: string[];
  pay_enabled?: boolean;
  h5?: boolean;
  trial?: { enabled: boolean; directions: number; per_session: number; per_ip_day: number };
  sales_paused?: boolean;
  paused_reason?: string;
  fallback_img?: boolean;
};

export function getPlan(): Promise<Plan> {
  return api("/api/plan");
}
