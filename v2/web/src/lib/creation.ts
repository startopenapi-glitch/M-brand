// A request identity survives a lost HTTP response and a page reload. It is not
// an entitlement: the server still checks session ownership on every replay.
export type Creation = { key: string; form: Record<string, string>; jobId?: string; transferred?: boolean };
export const CREATION_KEY = "zb_creation_intent_v1";
export function readCreation(): Creation | null {
  const v = JSON.parse(localStorage.getItem(CREATION_KEY) || "null");
  if (!v) return null;
  if (typeof v.key !== "string" || !/^[A-Za-z0-9_-]{16,128}$/.test(v.key) || !v.form || typeof v.form !== "object" || Array.isArray(v.form) || !Object.values(v.form).every(x => typeof x === "string") || (v.jobId !== undefined && typeof v.jobId !== "string")) throw new Error("创建记录无法读取，请先从我的提案核对结果。");
  return v;
}
// Native storage events cover other tabs only. Also notify a remounted form in
// this document when its departed predecessor finishes an accepted request.
export function saveCreation(v: Creation) {
  localStorage.setItem(CREATION_KEY, JSON.stringify(v)); window.dispatchEvent(new Event(CREATION_KEY));
}
export function clearCreation() {
  localStorage.removeItem(CREATION_KEY); window.dispatchEvent(new Event(CREATION_KEY));
}
export function sameCreationForm(a: Record<string, string>, b: Record<string, string>): boolean {
  const canonical = (form: Record<string, string>) => JSON.stringify(Object.keys(form).sort().map(key =>
    [key, ["cn_name", "industry"].includes(key) ? form[key].trim() : form[key]]));
  return canonical(a) === canonical(b);
}
// Bound only lock acquisition, never steal a lock protecting an in-flight POST.
// Remove timed-out requests from the queue so they cannot submit later.
async function waitForLock<T>(name: string, work: () => Promise<T>): Promise<T> {
  const controller = new AbortController();
  const deadline = Date.now() + 20000;
  const message = "另一个页面正在处理创建或保存，请稍候重试；本页尚未重复提交。";
  const timer = setTimeout(() => controller.abort(), 20000);
  try {
    return await navigator.locks.request(name, { signal: controller.signal }, () => {
      clearTimeout(timer);
      // A sleeping tab may resume with its timeout callback still queued.
      if (Date.now() >= deadline) throw new Error(message);
      return work();
    });
  } catch (error) {
    if (controller.signal.aborted) throw new Error(message);
    throw error;
  } finally { clearTimeout(timer); }
}
export function creationLock<T>(work: () => Promise<T>): Promise<T> {
  if (!navigator.locks) return Promise.reject(new Error("当前浏览器无法保护重复提交，请使用新版 Safari、Chrome 或 Edge 打开。"));
  return waitForLock("0brand-create", work);
}

// Autosave and submit share this lock across tabs. Otherwise two anonymous
// draft responses can install different cookies and orphan the first job.
export function draftLock<T>(work: () => Promise<T>): Promise<T> {
  if (!navigator.locks) return Promise.reject(new Error("请使用新版 Safari、Chrome 或 Edge 保存资料。"));
  return waitForLock("0brand-draft-write", work);
}
