import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronDown, Check, ArrowRight } from "lucide-react";
import { api } from "../lib/api";
import { CREATION_KEY, clearCreation, creationLock, draftLock, readCreation, saveCreation, sameCreationForm, type Creation } from "../lib/creation";
import { Input, Textarea, Label } from "./ui/field";
import { Button } from "./ui/button";

type Form = Record<string, string>;
const DRAFT_KEY = "zb_unsaved_draft";
const TAB_DRAFT_KEY = "zb_tab_draft_v1";
const TAB_DRAFT_FIELDS_KEY = "zb_tab_draft_pending_fields_v1";
const TAB_DRAFT_SYNCED_KEY = "zb_tab_draft_synced_v1";
// The same draft persists across public routes; writes must outlive form mounts
// in one queue so a slow request from a departed page cannot overwrite newer input.
let draftWrites: Promise<unknown> = Promise.resolve();
function enqueueDraft(body: string) {
  const pending = draftWrites.then(() => draftLock(() => api("/api/draft", { method: "PUT", body })));
  draftWrites = pending.catch(() => undefined);
  return pending;
}
const isForm = (v: unknown): v is Form => !!v && typeof v === "object" && !Array.isArray(v) && Object.values(v).every(x => typeof x === "string");
function tabDraft(): Form | null {
  try { const saved = JSON.parse(sessionStorage.getItem(TAB_DRAFT_KEY) || "null"); return isForm(saved) ? saved : null; } catch { return null; }
}
function pendingDraftFields(): Set<string> {
  try { const saved=JSON.parse(sessionStorage.getItem(TAB_DRAFT_FIELDS_KEY)||"[]");return new Set(Array.isArray(saved)?saved.filter((key):key is string=>typeof key==="string"):[]); } catch { return new Set(); }
}
function localDraft(): Form | null {
  try { const saved=JSON.parse(localStorage.getItem(DRAFT_KEY)||"null");return isForm(saved)?saved:null; } catch { return null; }
}
function initialDraft(): Form {
  let local: Form = {};
  // Preserve independently edited brands on reload, even when another tab has
  // created a different brand using the shared anonymous session.
  const tab = tabDraft(); if (tab) return tab;
  const saved=localDraft();if(saved)local=saved;
  try { const creation = readCreation(); if (creation) return creation.form; } catch { /* Submit reports corrupt/unavailable storage. */ }
  const q = new URLSearchParams(location.search);
  for (const key of ["cn_name", "industry"]) { const value = q.get(key)?.trim(); if (value) local[key] = value; }
  return { lang: "中英结合", ...local };
}
const optionalFields = [
  ["en_name", "英文品牌名", "留空可自动补全"], ["positioning", "品牌定位与目标人群", "希望服务什么样的人"],
  ["philosophy", "品牌核心理念", "你的品牌相信什么"], ["slogan", "品牌标语", "有现成标语可以填在这里"],
  ["keywords", "品牌性格关键词", "如：自然、温暖、简洁"], ["logo_inspiration", "Logo 灵感来源", "如：山间的轮廓"],
  ["logo_meaning", "希望 Logo 表达的含义", "希望顾客记住什么"], ["avoid", "不希望出现的元素", "告诉我们需要避开的元素"],
  ["style", "整体视觉风格", "留空按行业补全"], ["primary_color", "主色调", "如：墨绿"],
  ["accent_colors", "辅助色与点缀色", "有偏好时填写"], ["materials", "主要材质", "如：纸张、织物、金属"],
  ["photography", "摄影或空间氛围", "如：自然光、安静、柔和"],
] as const;
export default function BrandBriefForm() {
  const navigate = useNavigate();
  const [form, setForm] = useState<Form>(initialDraft);
  const [creation, setCreation] = useState<Creation | null>(() => { try { return readCreation(); } catch { return null; } });
  const [expanded, setExpanded] = useState(false);
  const [busy, setBusy] = useState(false), busyRef = useRef(false);
  const [error, setError] = useState("");
  const [logoUploading, setLogoUploading] = useState(false);
  const [logoError, setLogoError] = useState("");
  const [logoName, setLogoName] = useState("");
  const [saveStatus, setSaveStatus] = useState(() => tabDraft() ? "已恢复上次草稿" : "填写后自动保存");
  const [saveError, setSaveError] = useState("");
  const [draftReady, setDraftReady] = useState(false);
  const [draftFailed, setDraftFailed] = useState(false);
  const dirty = useRef(Object.keys(form).length > 1);
  const initialDirty = useRef(dirty.current);
  const initialForm = useRef(form);
  const initialTab = useRef(tabDraft());
  const initialStored = useRef(!!initialTab.current || !!localDraft());
  const initialSynced = useRef((()=>{try{return sessionStorage.getItem(TAB_DRAFT_SYNCED_KEY)}catch{return null}})());
  const pendingFields = useRef(pendingDraftFields());
  const editedKeys = useRef(new Set<string>());
  const autoSaveBlocked = useRef(false);
  const latest = useRef(form), mounted = useRef(true);
  const locked = busy || !!creation || draftFailed;
  const otherCreation = !!creation && !sameCreationForm(form, creation.form);
  useEffect(() => {
    const sync = (event: Event) => {
      if (event instanceof StorageEvent && event.key !== CREATION_KEY) return;
      try {
        const next = readCreation(); setCreation(next);
        if (next && (!dirty.current || sameCreationForm(latest.current, next.form))) {
          dirty.current = true; latest.current = next.form; setForm(next.form);
        }
      }
      catch { setError("创建记录暂不可用，请先从我的提案核对结果。"); }
    };
    window.addEventListener("storage", sync);
    window.addEventListener(CREATION_KEY, sync);
    return () => { window.removeEventListener("storage", sync); window.removeEventListener(CREATION_KEY, sync); };
  }, []);
  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    let inflight=false,restored=false,timer:ReturnType<typeof setTimeout>|undefined;
    const restore=async()=>{
      if(controller.signal.aborted||inflight||restored||document.hidden||!navigator.onLine)return;
      inflight=true;clearTimeout(timer);
      try{
        // A previous route's PUT may still be queued when this form mounts.
        // Read only after those writes finish, under the same cross-tab lock,
        // so an old GET snapshot cannot arrive after a newer successful PUT.
        await draftWrites;
        if(controller.signal.aborted)return;
        const d=await draftLock(()=>api("/api/draft",{signal:controller.signal}));
        if(controller.signal.aborted)return;
        if(!isForm(d))throw new Error("Invalid saved draft");
        if(Object.keys(d).length){
          // A queued save from the previous form mount may have completed while
          // this GET was in flight. Its callback reconciles sessionStorage, so
          // use that current marker/baseline instead of the mount-time snapshot.
          pendingFields.current=pendingDraftFields();
          try { initialSynced.current=sessionStorage.getItem(TAB_DRAFT_SYNCED_KEY); } catch { /* Keep the last known snapshot. */ }
          const serverDraft:Form={lang:"中英结合",...d};
          const localName=latest.current.cn_name?.trim(),savedName=d.cn_name?.trim();
          if((initialDirty.current || editedKeys.current.has("cn_name")) && localName && savedName && localName!==savedName){
            // The server draft belongs to another brand. Keep this tab's brief
            // until the user explicitly starts generation; never silently PUT it.
            autoSaveBlocked.current=true;
            try { sessionStorage.setItem(TAB_DRAFT_KEY,JSON.stringify(latest.current)); } catch { /* Current page still retains it. */ }
            setSaveStatus("本页品牌已保留，点击生成时再同步草稿");
          }else if(initialStored.current && pendingFields.current.size===0 && Object.keys(initialForm.current).some(key=>initialForm.current[key]!==serverDraft[key]) && !(initialTab.current && initialSynced.current===JSON.stringify(initialForm.current))){
            // Legacy tab drafts have no unsynced-field marker. They might be
            // stale after another device saved, so never auto-PUT them.
            autoSaveBlocked.current=true;
            setSaveStatus("本页草稿与已保存版本不同，请核对后主动生成");
          }else{
            const merged:Form={...serverDraft};
            if(initialStored.current)for(const key of pendingFields.current)merged[key]=initialForm.current[key]||"";
            else if(initialDirty.current)for(const key of ["cn_name","industry"]){
              const value=new URLSearchParams(location.search).get(key)?.trim();if(value)merged[key]=value;
            }
            for(const key of editedKeys.current) merged[key]=latest.current[key] || "";
            latest.current=merged;setForm(merged);
            const unchanged=Object.keys(merged).length===Object.keys(serverDraft).length && Object.keys(merged).every(key=>merged[key]===serverDraft[key]);
            if(unchanged){
              dirty.current=false;
              try { const body=JSON.stringify(merged);sessionStorage.setItem(TAB_DRAFT_KEY,body);sessionStorage.setItem(TAB_DRAFT_SYNCED_KEY,body);sessionStorage.removeItem(TAB_DRAFT_FIELDS_KEY);pendingFields.current.clear();if(JSON.stringify(localDraft())===body)localStorage.removeItem(DRAFT_KEY); } catch { /* Server copy is current. */ }
              setSaveStatus("已恢复上次草稿");
            }else if(dirty.current){
              // Keep the merged draft recoverable if the tab closes before PUT.
              try { sessionStorage.setItem(TAB_DRAFT_KEY,JSON.stringify(merged));localStorage.setItem(DRAFT_KEY,JSON.stringify(merged)); } catch { /* Server save follows. */ }
              setSaveStatus("正在保存…");
            }else setSaveStatus("已恢复上次草稿");
          }
        }
        restored=true;setDraftReady(true);setDraftFailed(false);setSaveError("");
      }catch{
        if(!controller.signal.aborted){setDraftFailed(true);setSaveStatus("正在恢复草稿…");setSaveError("暂时无法读取已保存草稿，正在自动重试；恢复前不会覆盖服务器资料。")}
      }finally{
        inflight=false;
        if(!controller.signal.aborted&&!restored)timer=setTimeout(restore,3500);
      }
    };
    const wake=()=>{if(!document.hidden){clearTimeout(timer);void restore()}};
    void restore();window.addEventListener("online",wake);window.addEventListener("focus",wake);document.addEventListener("visibilitychange",wake);
    return () => { mounted.current = false; controller.abort();clearTimeout(timer);window.removeEventListener("online",wake);window.removeEventListener("focus",wake);document.removeEventListener("visibilitychange",wake); };
  }, []);
  function change(key: string) {
    return (event: ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => {
      dirty.current = true;
      editedKeys.current.add(key);
      // Another mount's queued PUT can settle after this one mounted; never
      // put an already-saved marker back into storage on the next keystroke.
      pendingFields.current=pendingDraftFields();
      pendingFields.current.add(key);
      const next = { ...latest.current, [key]: event.target.value };
      latest.current = next; setForm(next); setSaveStatus(autoSaveBlocked.current?"本页草稿保存在本设备，点击生成时同步":"正在保存…");
      try { sessionStorage.setItem(TAB_DRAFT_KEY, JSON.stringify(next));sessionStorage.setItem(TAB_DRAFT_FIELDS_KEY,JSON.stringify([...pendingFields.current])); localStorage.setItem(DRAFT_KEY, JSON.stringify(next)); setSaveError(""); }
      catch { setSaveError("本设备暂时无法保存草稿，请等待联网保存后再离开。"); }
    };
  }
  useEffect(() => {
    if (!dirty.current || creation || !draftReady || autoSaveBlocked.current) return;
    const body = JSON.stringify(form);
    const timer = setTimeout(() => {
      void enqueueDraft(body).then(() => {
        try { if (localStorage.getItem(DRAFT_KEY) === body) localStorage.removeItem(DRAFT_KEY); } catch { /* Server saved. */ }
        // A queued PUT can succeed after this form unmounts or after another
        // edit. Reconcile storage first so already-saved fields cannot replay
        // over newer server data when this tab reopens.
        try {
          const submitted = JSON.parse(body) as Form;
          const current = tabDraft();
          if (current && current.cn_name?.trim() === submitted.cn_name?.trim()) {
            const remaining = pendingDraftFields();
            for (const key of remaining) if ((current[key] || "") === (submitted[key] || "")) remaining.delete(key);
            sessionStorage.setItem(TAB_DRAFT_SYNCED_KEY, body);
            if (remaining.size) sessionStorage.setItem(TAB_DRAFT_FIELDS_KEY, JSON.stringify([...remaining]));
            else sessionStorage.removeItem(TAB_DRAFT_FIELDS_KEY);
            pendingFields.current = remaining;
          }
        } catch { /* The next restore will guard an unresolved local draft. */ }
        if (!mounted.current || JSON.stringify(latest.current) !== body) return;
        setSaveStatus("草稿已保存"); setSaveError("");
      }).catch(() => {
        if (mounted.current && JSON.stringify(latest.current) === body) {
          setSaveStatus("草稿暂未同步"); setSaveError("草稿暂未同步到服务器，继续编辑可重试。请勿清除浏览器数据。");
        }
      });
    }, 800);
    return () => clearTimeout(timer);
  }, [form, creation, draftReady]);
  function removeLogo() {
    const next = { ...latest.current };
    delete next.logo_ref;
    dirty.current = true;
    editedKeys.current.add("logo_ref");
    pendingFields.current = pendingDraftFields(); pendingFields.current.add("logo_ref");
    latest.current = next; setForm(next); setLogoName(""); setLogoError(""); setSaveStatus("正在保存…");
    try { sessionStorage.setItem(TAB_DRAFT_KEY, JSON.stringify(next)); sessionStorage.setItem(TAB_DRAFT_FIELDS_KEY, JSON.stringify([...pendingFields.current])); localStorage.setItem(DRAFT_KEY, JSON.stringify(next)); } catch { /* Server draft follows. */ }
  }

  async function uploadLogo(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) return;
    setLogoError("");
    if (!/^image\/(png|jpeg)$/.test(file.type) || file.size > 8 * 1024 * 1024) {
      setLogoError("请上传 8MB 以内的 PNG/JPG 图片。");
      event.target.value = "";
      return;
    }
    setLogoUploading(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const response = await fetch("/api/logo-reference", { method: "POST", body: fd, credentials: "include" });
      const data = await response.json().catch(() => ({}));
      if (!response.ok || typeof data.token !== "string") throw new Error(data.error || "参考图上传失败");
      const next = { ...latest.current, logo_ref: data.token };
      dirty.current = true;
      editedKeys.current.add("logo_ref");
      pendingFields.current = pendingDraftFields(); pendingFields.current.add("logo_ref");
      latest.current = next; setForm(next); setLogoName(file.name); setSaveStatus("正在保存…");
      try { sessionStorage.setItem(TAB_DRAFT_KEY, JSON.stringify(next)); sessionStorage.setItem(TAB_DRAFT_FIELDS_KEY, JSON.stringify([...pendingFields.current])); localStorage.setItem(DRAFT_KEY, JSON.stringify(next)); } catch { /* Server draft follows. */ }
    } catch (e) {
      setLogoError(e instanceof Error ? e.message : "参考图上传失败，请重试。");
    } finally {
      setLogoUploading(false);
      event.target.value = "";
    }
  }

  async function submit(event: FormEvent, recoverOther = false) {
    event.preventDefault();
    if(!draftReady)return;
    if (busyRef.current) return;
    if (!recoverOther && (!form.cn_name?.trim() || !form.industry?.trim())) { setError("请填写品牌名称和所属行业"); return; }
    busyRef.current = true; setBusy(true); setError("");
    try {
      await creationLock(async () => {
        let intent = readCreation();
        if (recoverOther && !intent) throw new Error("原创建记录已更新，请刷新后核对。本次没有创建新提案。");
        if (intent && !recoverOther && !sameCreationForm(form, intent.form)) {
          if (mounted.current) setCreation(intent);
          throw new Error("另一个页面已提交不同品牌。本页资料已保留，请先核对原提案，再主动创建新的品牌提案。");
        }
        if (!intent) {
          intent = { key: crypto.randomUUID(), form: { ...form, cn_name: form.cn_name.trim(), industry: form.industry.trim() } };
        }
        if (intent.transferred) throw new Error("上次提案已在其他设备找回。可在这里开始新的品牌提案。");
        if (!intent.jobId) {
          // A successful draft round-trip establishes the anonymous session
          // before the first durable POST, even if its response gets lost.
          await enqueueDraft(JSON.stringify(intent.form));
          // Before this point no job POST has been sent, so a rejected draft
          // must remain editable. After persistence, retries keep this key.
          saveCreation(intent);
          if (mounted.current) {
            setCreation(intent);
            if (!recoverOther) { latest.current = intent.form; setForm(intent.form); }
          }
          let result;
          try { result = await api("/api/jobs", { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": intent.key }, body: JSON.stringify(intent.form) }); }
          catch (e: any) {
            if (e.status === 409 && e.data?.code === "creation_transferred") {
              intent = { ...intent, transferred: true }; saveCreation(intent); if (mounted.current) setCreation(intent);
            }
            throw e;
          }
          if (typeof result.id !== "string" || !result.id) throw new Error("创建结果暂未确认，请重试核对。");
          intent = { ...intent, jobId: result.id }; saveCreation(intent);
        }
        if (mounted.current) { setCreation(intent); if (!recoverOther) navigate(`/job/${encodeURIComponent(intent.jobId!)}`); }
      });
    } catch (e: unknown) { if (mounted.current) setError(e instanceof Error ? e.message : "暂时无法提交，请稍后重试。"); }
    finally { busyRef.current = false; if (mounted.current) setBusy(false); }
  }
  async function startNew() {
    if (busyRef.current) return;
    busyRef.current = true; setBusy(true);
    try { await creationLock(async () => {
      const current = readCreation();
      if (current && !current.jobId && !current.transferred) throw new Error("上次创建结果尚未确认，请先核对，避免重复生成。");
      clearCreation(); dirty.current = true; setCreation(null); setError("");
      localStorage.setItem(DRAFT_KEY, JSON.stringify(latest.current));
    }); } catch(e) { setError(e instanceof Error ? e.message : "暂时无法开始新提案"); }
    finally { busyRef.current = false; if (mounted.current) setBusy(false); }
  }
  return <form className="brief-form" onSubmit={submit} noValidate aria-label="品牌资料">
    <div className="form-field"><Label htmlFor="brand-name" required>品牌名称</Label><Input id="brand-name" aria-label="品牌名称" name="cn_name" placeholder="如：M-brand" value={form.cn_name || ""} onChange={change("cn_name")} maxLength={200} disabled={locked} required /></div>
    <div className="form-field"><Label htmlFor="brand-industry" required>所属行业</Label><Input id="brand-industry" aria-label="所属行业" name="industry" placeholder="如：AI 工具、品牌设计" value={form.industry || ""} onChange={change("industry")} maxLength={200} disabled={locked} required /></div>
    <div className="form-field"><Label htmlFor="brand-products">简要说明<span className="field-optional">选填</span></Label><Textarea id="brand-products" name="products" rows={2} placeholder="说说你提供的产品或服务" value={form.products || ""} onChange={change("products")} disabled={locked} /></div>
    <div className="form-field"><Label htmlFor="brand-logo-ref">Logo 参考图<span className="field-optional">选填</span></Label><input id="brand-logo-ref" aria-label="Logo 参考图" className="ui-input file-input" type="file" accept="image/png,image/jpeg" disabled={locked || logoUploading} onChange={uploadLogo} /><p className="helper-text">{logoUploading ? "正在上传参考图…" : logoError ? logoError : form.logo_ref ? `已上传${logoName ? `：${logoName}` : ""}，三个方向会参考它的气质与构图。` : "可上传现有 Logo 或手绘草图，仅作为风格参考，不会直接复制。"}</p>{form.logo_ref && !locked && <button type="button" className="helper-text" onClick={removeLogo} disabled={logoUploading}>移除参考图</button>}</div>
    <button type="button" className="preferences-toggle" aria-expanded={expanded} aria-controls="brand-preferences" onClick={() => setExpanded(v => !v)}><span>更多设计偏好</span><ChevronDown size={16} className={expanded ? "rotate-180" : ""} aria-hidden="true" /></button>
    <div id="brand-preferences" hidden={!expanded}>
      <p className="helper-text mb-4">有想法就告诉我们；留空的部分会结合品牌信息补全。</p>
      <div className="preference-grid">
        {optionalFields.map(([key, label, placeholder]) => <div className="form-field" key={key}><Label htmlFor={`brand-${key}`}>{label}</Label><Input id={`brand-${key}`} value={form[key] || ""} onChange={change(key)} placeholder={placeholder} disabled={locked} /></div>)}
        <div className="form-field"><Label htmlFor="brand-logo_type">Logo 类型</Label><select id="brand-logo_type" className="ui-input" value={form.logo_type || ""} onChange={change("logo_type")} disabled={locked}><option value="">自动选择</option>{["抽象符号","汉字融合","字母图形","几何符号","徽章"].map(v => <option key={v}>{v}</option>)}</select></div>
        <div className="form-field"><Label htmlFor="brand-lang">文字语言</Label><select id="brand-lang" className="ui-input" value={form.lang} onChange={change("lang")} disabled={locked}>{["中英结合","中文为主","英文为主"].map(v => <option key={v}>{v}</option>)}</select></div>
        {([["scenarios","产品 / 包装 / 应用场景"],["extra","补充要求"]] as const).map(([key,label]) => <div className="form-field" key={key}><Label htmlFor={`brand-${key}`}>{label}</Label><Textarea id={`brand-${key}`} rows={2} value={form[key] || ""} onChange={change(key)} disabled={locked} /></div>)}
      </div>
    </div>
    {error && <p role="alert" className="notice notice-error">{error}</p>}
    {saveError && <p role="status" className="helper-text">{saveError}</p>}
    {creation && <p role="status" className="helper-text">{otherCreation ? `另一个页面已提交「${creation.form.cn_name}」。本页资料已保留，核对后可继续创建。` : creation.transferred ? "上次提案已在其他设备找回。可主动开始新的品牌提案。" : creation.jobId ? "上次提案已保存，可继续查看，或主动开始新的品牌提案。" : "已保留这次创建请求。重新核对会找回原提案，不会重复生成。"}</p>}
    <div className="brief-action"><Button type={otherCreation ? "button" : "submit"} onClick={otherCreation ? event => creation?.jobId ? navigate(`/job/${encodeURIComponent(creation.jobId)}`) : void submit(event, true) : undefined} className="w-full" size="lg" disabled={busy || !draftReady || creation?.transferred}>{busy ? "提交中…" : creation?.transferred ? "原提案已在其他设备找回" : otherCreation ? creation?.jobId ? "查看其他页面的提案" : "核对其他页面的创建结果" : creation?.jobId ? "查看已创建提案" : creation ? "继续核对创建结果" : "9.9 元解锁自己的品牌"}{!busy && <ArrowRight size={17} aria-hidden="true" />}</Button>{(creation?.jobId || creation?.transferred) && <Button type="button" variant="outline" className="w-full mt-3" disabled={busy} onClick={startNew}>创建新的品牌提案</Button>}<p className="helper-text">9.9 元解锁自己的品牌和 Logo 组合卡；17 个应用项仍按需购买。</p></div>
    <div className="form-footnote"><span><Check size={14} aria-hidden="true" /> 其他交付项按需购买</span><span role="status">{saveStatus}</span></div>
  </form>;
}
