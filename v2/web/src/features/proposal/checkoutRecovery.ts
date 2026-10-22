// This is only a read-reconciliation hint, never a price, order or entitlement.
// Persist before POST so a closed/reloaded tab can discover a late server commit.
const key = (job: string) => `zb_checkout_probe_v1:${job}`;
export function readCheckoutProbe(job: string): string[] | null {
 try {
  const value = JSON.parse(localStorage.getItem(key(job)) || 'null');
  return Array.isArray(value) && value.every(k => typeof k === 'string') ? value : null;
 } catch { return null; }
}
export function saveCheckoutProbe(job: string, items: string[]) {
 try { localStorage.setItem(key(job), JSON.stringify(items)); } catch { /* In-memory polling still protects this page. */ }
}
export function clearCheckoutProbe(job: string) {
 try { localStorage.removeItem(key(job)); } catch { /* Server data remains authoritative. */ }
}
