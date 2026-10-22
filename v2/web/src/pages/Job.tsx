import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, Check, FileText, LoaderCircle, Maximize2, X } from "lucide-react";
import * as Dialog from "@radix-ui/react-dialog";
import { Button } from "../components/ui/button";
import { Input, Label, Textarea } from "../components/ui/field";
import { DeliveryProgress } from "../features/proposal/DeliveryProgress";
import { DeliveryDownload } from "../features/proposal/DeliveryDownload";
import { api, IMG } from "../lib/api";
import { money, useProposal } from "../features/proposal/useProposal";
const categories=[['logo','Logo 资产'],['system','品牌系统'],['boards','品牌提案'],['apps','应用落地']];
const active=(s?:string)=>['queued','running','boards_running'].includes(s||'');
export default function JobPage(){
 const {id}=useParams();
 const enlargeTrigger=useRef<HTMLButtonElement>(null);
 const paymentTrigger=useRef<HTMLButtonElement>(null);
 const {state,detail,checked,setChecked,error,setError,missing,readError,busy,refresh,order,open,setOpen,paymentMessage,action}=useProposal(id);
 // Selecting a direction happens near the bottom on phones. Bring the next
 // review stage into view, without scrolling on routine polls or completion.
 const viewedStage=useRef<{id?:string;stage?:string}>({});
 useEffect(()=>{
  const previous=viewedStage.current;
  viewedStage.current={id,stage:state?.stage};
  if(previous.id===id&&previous.stage==="choose"&&state?.stage==="logo")window.scrollTo(0,0);
 },[id,state?.stage]);
 const [preview,setPreview]=useState(0),[enlarge,setEnlarge]=useState(false),[shopping,setShopping]=useState(false);
 const [logoInstruction,setLogoInstruction]=useState("");
 const [unlockOpen,setUnlockOpen]=useState(false);
 const [unlockMode,setUnlockMode]=useState<"pay"|"code">("pay");
 const [unlockCode,setUnlockCode]=useState("");
 const [unlockError,setUnlockError]=useState("");
 const [unlockBusy,setUnlockBusy]=useState(false);
 useEffect(()=>{setPreview(0);setEnlarge(false);setShopping(false);setUnlockOpen(false);setUnlockMode("pay");setUnlockCode("");setUnlockError("");setUnlockBusy(false)},[id]);
 const choices=detail?.items.filter(it=>it.category!=='free'&&!it.purchased)||[];
 const selected=choices.filter(it=>checked.has(it.kind));
 const all=choices.length>0&&choices.every(it=>checked.has(it.kind));
 const raw=selected.reduce((n,it)=>n+it.price_cents,0);
 const total=all?Math.min(raw,detail?.remaining_bundle_price??detail?.bundle_price??raw):raw;
 const errorNotice=error&&<div role="alert" className="notice notice-error">{error} <button className="underline ml-2" onClick={()=>{setError('');refresh(n=>n+1)}}>刷新</button></div>;
 const notices=<div className="proposal-notices">{readError&&<p role="status" className="notice">{readError}</p>}{!(order&&open)&&errorNotice}{paymentMessage&&<p role="status" className="notice">{paymentMessage}</p>}</div>;
 if(missing)return <main className="page-width"><section className="surface empty-state"><FileText size={32}/><h1>链接无效</h1><p>任务不存在或不属于当前设备。</p><Button asChild><Link to="/orders">查看我的提案与订单</Link></Button></section></main>;
 if(!state)return <main className="page-width">{notices}<p role="status" className="loading-line"><LoaderCircle size={18} className="animate-spin"/>正在读取提案…</p></main>;
 const choosing=state.stage==='directions'||state.stage==='choose';
 const directions=state.images.filter(it=>it.kind==='direction');
 const image=directions.find(it=>it.idx===preview);
 const system=detail?.items.find(it=>it.kind==='system_card');
 const logoPending=state.stage==='logo'||state.stage==='logo_revision';
 const logoConfirmed=state.logo_confirmed!==false && !logoPending;
 const systemReady=(!!system?.generated||state.stage==='done')&&logoConfirmed;
 const locked=state.directions_unlocked===false;
 const delivered=detail?.items.filter(it=>it.category!=='free'&&it.purchased&&it.generated&&it.file&&it.unlocked)||[];
 const paidItems=detail?.items.filter(it=>it.category!=='free'&&it.purchased)||[];
 const showShopping=!paidItems.length||shopping;
 const unpaidCount=detail?.items.filter(it=>it.purchased&&!it.generated).length||0;
 const cover=system?.file||directions.find(it=>it.idx===state.chosen)?.file;
 async function unlockWithCode(){
  if(unlockBusy||!unlockCode.trim())return;
  setUnlockBusy(true);setUnlockError("");
  try{
   await api(`/api/jobs/${id}/unlock-code`,{method:"POST",body:JSON.stringify({code:unlockCode.trim()})});
   setUnlockOpen(false);setUnlockCode("");refresh(n=>n+1);
  }catch(e:any){setUnlockError(e?.message||"邀请码暂不可用，请重试。")}
  finally{setUnlockBusy(false)}
 }
 function startPay(){
  setUnlockOpen(false);
  void action(`/api/jobs/${id}/unlock`);
 }
 return <main className={`page-width proposal-page ${!choosing&&showShopping&&selected.length>0?'has-purchase-summary':''}`}>
  <div className="page-heading"><div><Link to="/orders" className="back-link"><ArrowLeft size={14}/>我的提案与订单</Link><h1>{locked?'解锁自己的品牌':choosing?(state.status==='ready_to_choose'?'选择 Logo 方向':'正在制作 Logo 方向'):logoPending?'确认正式 Logo':'我的品牌提案'}</h1><p>{locked?'支付 ¥9.90，解锁自己的品牌：3 个 Logo 方向 + Logo 组合卡。':choosing?'找到最喜欢的方向，延展成一套品牌视觉。':logoPending?'这是独立的正式 Logo 候选稿，确认后才会生成付费应用项。':'沿用选定的方向，只购买你需要的内容。'}</p></div><div className="workflow-pills" aria-label="当前阶段"><span className={choosing?'current':''}>选择方向</span><span className={!choosing&&!paidItems.length?'current':''}>选购交付</span><span className={!choosing&&paidItems.length?'current':''}>查看成果</span></div></div>
  {notices}
  {state.status==='error'&&<section role="alert" className="notice notice-error failure-notice"><div><strong>部分内容暂未完成</strong><p>{state.user_error||'已完成的内容和已购权益都已保留。'}</p></div><Button variant="outline" disabled={busy||state.can_retry===false} onClick={()=>action(`/api/jobs/${id}/retry`)}>重试未完成内容</Button></section>}
  <DeliveryProgress state={state} detail={detail}/>
  {locked?<section className="surface direction-choice"><span className="eyebrow">一次解锁</span><h2>9.9 元，<br/>开始自己的品牌。</h2><p>支付 ¥9.90 解锁自己的品牌：3 个 Logo 方向 + Logo 组合卡。17 个应用项不包含在解锁内，之后按需购买。</p><div className="summary-total"><span>方向 + Logo 组合卡</span><strong>{money(state.unlock_price_cents??990)}</strong></div><Button className="w-full" size="lg" disabled={busy} onClick={()=>order?setOpen(true):setUnlockOpen(true)}>{busy?'处理中…':order?'继续核对支付':'解锁自己的品牌'}</Button><p className="helper-text">可选择微信支付 ¥9.90 或使用邀请码；确认后自动开始生成 3 个方向。</p></section>:choosing?<div className="workspace-grid direction-workspace">
   <section className="direction-gallery"><div className="surface direction-main">{image?.file?<><img src={IMG(image.file)} alt={`方向 ${'ABC'[preview]} 大图`} width={800} height={1000}/><button ref={enlargeTrigger} className="icon-button enlarge-button" aria-label="放大当前方向" onClick={()=>setEnlarge(true)}><Maximize2 size={18}/></button></>:<div className="art-pending"><LoaderCircle size={25} className={active(state.status)?'animate-spin':''}/><strong>{state.status==='queued'?'提案已受理':'正在设计这个方向'}</strong><span>完成后自动展示，不需要刷新</span></div>}</div>
    <div className="direction-thumbnails">{[0,1,2].map(index=>{const item=directions.find(it=>it.idx===index);return <button key={index} aria-label={`预览方向 ${'ABC'[index]}`} aria-pressed={preview===index} onClick={()=>setPreview(index)}>{item?.file?<img src={IMG(item.file)} alt="" width={100} height={125}/>:<div className="thumbnail-pending"><LoaderCircle size={18}/></div>}<span>方向 {'ABC'[index]}{preview===index&&<Check size={14}/>}</span></button>})}</div>
   </section>
   <aside className="surface direction-choice"><span className="eyebrow">三个方向，一个起点</span><h2>让喜欢的设计，<br/>成为你的品牌。</h2><p>点选缩略图比较方向，也可以放大查看细节。</p><div className="selected-direction"><span>当前选择</span><strong>方向 {'ABC'[preview]}</strong></div><Button className="w-full" size="lg" disabled={busy||state.status!=='ready_to_choose'||!image?.file} onClick={()=>action(`/api/jobs/${id}/choose`,{direction:preview})}>{busy?'确认中…':'使用这个方向'}</Button><p className="helper-text">选定后生成 Logo 组合卡，再按需购买交付项。</p></aside>
  </div>:<div className="workspace-grid delivery-workspace">
   <div className="deliverable-main">
    {delivered.length>0&&<section className="delivery-results"><div className="section-heading"><h2>已生成的内容</h2><p>已购内容无需重复购买。</p></div><div className="result-grid">{delivered.map(it=><a key={it.kind} href={IMG(it.file)} target="_blank" rel="noreferrer" className="surface result-card">{it.kind==='brand_story'?<div className="document-preview"><FileText size={40}/><span>品牌故事文档</span></div>:<img loading="lazy" alt={it.name} src={IMG(it.file)} width={400} height={400}/>}<span>{it.name}</span></a>)}</div><DeliveryDownload key={id} id={id!} count={delivered.length}/><p className="helper-text mt-3">压缩包包含当前已完成且有权下载的文件。{paidItems.length-delivered.length>0?`还有 ${paidItems.length-delivered.length} 项已购内容待完成，稍后可重新下载。`:'所有已购内容已完成。'}</p></section>}
    {cover&&<section className="surface system-preview"><a href={IMG(cover)} target="_blank" rel="noreferrer"><img src={IMG(cover)} alt={system?.file?'Logo 组合卡':'已选 Logo 方向'} width={120} height={150}/></a><div><span className="status-pill success">已解锁内容</span><h2>{system?.file?'Logo 组合卡':'已选 Logo 方向'}</h2><p>{system?.file?'Symbol、中文标准字与英文标准字（如有）；后续付费交付都复用这个 Logo 组合。':'Logo 组合卡准备完成后，会在这里展示；完成后才能购买付费项。'}</p><a href={IMG(cover)} target="_blank" rel="noreferrer" className="text-link">查看大图</a></div></section>}
    {logoPending&&state.stage==='logo'&&system?.generated&&<section className="surface system-preview"><div><span className="status-pill">正式 Logo 候选</span><h2>确认这个 Logo 母版</h2><p>方向卡中的 Logo 只是探索稿；确认后才会生成后续应用项。需要调整时写清楚保留什么、改变什么。</p><div className="form-field"><label htmlFor="logo-revision">修改要求</label><Textarea id="logo-revision" rows={3} placeholder="例如：叶子再抽象一点，保留符号和字标，只调整轮廓" value={logoInstruction} onChange={event=>setLogoInstruction(event.target.value)} disabled={busy} /></div><div className="summary-actions"><Button disabled={busy} onClick={()=>action(`/api/jobs/${id}/logo/confirm`)}>{busy?'处理中…':'确认 Logo，继续'}</Button><Button variant="outline" disabled={busy||!logoInstruction.trim()} onClick={()=>action(`/api/jobs/${id}/logo/revise`,{instruction:logoInstruction.trim()})}>提交修改</Button></div></div></section>}
    {logoPending&&state.stage==='logo_revision'&&<section className="surface system-preview"><div><span className="status-pill">正在修改</span><h2>正在生成新的 Logo 版本</h2><p>原图会保留；完成后回到确认状态，页面会自动刷新。</p></div></section>}
    {paidItems.length>0&&choices.length>0&&<Button variant="outline" className="additional-delivery" aria-expanded={shopping} onClick={()=>setShopping(v=>!v)}>{shopping?'收起追加交付项':'追加交付项'}</Button>}
    <div hidden={!showShopping}>
    <div className="section-heading"><div><h2>选择需要的交付项</h2><p>勾选项目 → 微信支付 → 自动生成</p></div><Button variant="outline" disabled={!!order} onClick={()=>setChecked(all?new Set():new Set(choices.map(it=>it.kind)))}>{all?'取消全选':'全选'}</Button></div>
    {categories.filter(([cat])=>detail?.items.some(it=>it.category===cat)).map(([cat,name])=><section key={cat} className="surface deliverable-group"><h3>{name}</h3>{detail?.items.filter(it=>it.category===cat).map(it=><div key={it.kind} className={`deliverable-row ${it.purchased?'is-purchased':''}`}>
     {it.purchased?<span className="purchased-check" aria-label="已购买"><Check size={18}/></span>:<input id={`item-${it.kind}`} type="checkbox" aria-label={it.name} checked={checked.has(it.kind)} disabled={!!order} onChange={()=>setChecked(prev=>{const next=new Set(prev);next.has(it.kind)?next.delete(it.kind):next.add(it.kind);return next})}/>}
     <label htmlFor={it.purchased?undefined:`item-${it.kind}`} className="deliverable-label"><strong>{it.name}</strong><span>{it.purchased?(it.generated?'已购 · 已完成':'已购 · 待完成'):'付款后生成'}</span></label>
     {it.file&&it.unlocked&&<a className="text-link" href={IMG(it.file)} target="_blank" rel="noreferrer">查看</a>}<span className="item-price">{it.purchased?'已购':money(it.price_cents)}</span>
    </div>)}</section>)}

    </div>
   </div>
   <aside className="purchase-aside">
    {order&&<section className="surface pending-order"><span className="status-pill">待核对订单</span><h2>{money(order.amount_cents)}</h2><p>先处理这笔订单，再追加其他项目，避免重复付款。</p><Button ref={paymentTrigger} className="w-full" onClick={()=>setOpen(true)}>继续支付 / 查进度</Button></section>}
    {choices.length>0&&showShopping?<section className={`surface purchase-summary ${selected.length===0?'is-empty-selection':''}`} aria-label="选购汇总"><div className="summary-title"><h2>按需选择，清楚付费。</h2><p>方向与 Logo 组合卡已解锁，其他内容由你决定。</p></div><div className="summary-total"><span>已选 {selected.length} 项</span><strong>{money(total)}</strong></div><div className="summary-actions"><Button disabled={busy||!!order||!systemReady||selected.length===0||total<1} onClick={()=>action('/api/pay/create',all?{job_id:id,all:true}:{job_id:id,items:selected.map(it=>it.kind)})}>{busy?'处理中…':!systemReady?'等待 Logo 组合卡':`支付 ${money(total)}`}</Button></div><p className="helper-text summary-help">{systemReady?'微信安全支付 · 支付后自动生成':'Logo 组合卡完成后才能付款；当前页面自动更新。'}<br/>本服务不支持退款，请确认所选项目后付款。</p></section>:paidItems.length>0?<section className="surface completion-card"><Check size={28}/><h2>{unpaidCount?'已购内容正在准备':'你的品牌，已经就绪。'}</h2><p>{unpaidCount?`还有 ${unpaidCount} 项尚未完成，已有成果可以先查看。`:'查看你的品牌成果，下载后开始使用。'}</p></section>:null}
   </aside>
  </div>}
  <Dialog.Root open={enlarge} onOpenChange={setEnlarge}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="dialog-card image-dialog" aria-describedby={undefined} onCloseAutoFocus={event=>{event.preventDefault();enlargeTrigger.current?.focus()}}><Dialog.Title>方向 {'ABC'[preview]}</Dialog.Title><Dialog.Close className="icon-button dialog-close" aria-label="关闭预览"><X size={20}/></Dialog.Close>{image?.file&&<img src={IMG(image.file)} alt={`方向 ${'ABC'[preview]} 完整预览`}/>}</Dialog.Content></Dialog.Portal></Dialog.Root>
  <Dialog.Root open={unlockOpen} onOpenChange={setUnlockOpen}>
   <Dialog.Portal>
    <Dialog.Overlay className="dialog-overlay"/>
    <Dialog.Content className="dialog-card payment-dialog" aria-describedby="unlock-description">
     <Dialog.Title>解锁自己的品牌</Dialog.Title>
     <Dialog.Description id="unlock-description" className="helper-text">3 个 Logo 方向 + Logo 组合卡 · {money(state.unlock_price_cents??990)}</Dialog.Description>
     <Dialog.Close className="icon-button dialog-close" aria-label="关闭解锁"><X size={20}/></Dialog.Close>
     <div className="summary-actions" style={{marginTop:18}}>
      <Button variant={unlockMode==="pay"?"default":"outline"} onClick={()=>setUnlockMode("pay")}>微信支付 ¥9.90</Button>
      <Button variant={unlockMode==="code"?"default":"outline"} onClick={()=>setUnlockMode("code")}>使用邀请码</Button>
     </div>
     {unlockMode==="pay"?<div><p className="helper-text" style={{marginTop:16}}>支付确认后自动解锁，不需要刷新。</p><Button className="w-full mt-3" size="lg" disabled={busy} onClick={startPay}>{busy?"创建支付中…":"生成支付二维码"}</Button></div>:<div><div className="form-field" style={{marginTop:16}}><Label htmlFor="unlock-code">邀请码</Label><Input id="unlock-code" autoComplete="off" value={unlockCode} onChange={event=>setUnlockCode(event.target.value.toUpperCase())} placeholder="输入邀请码" disabled={unlockBusy}/></div>{unlockError&&<p role="alert" className="notice notice-error">{unlockError}</p>}<Button className="w-full" size="lg" disabled={unlockBusy||!unlockCode.trim()} onClick={unlockWithCode}>{unlockBusy?"解锁中…":"立即解锁"}</Button><p className="helper-text">一码一人、一码一次；如果已有未完成的 9.9 支付，请先核对原订单。</p></div>}
     <Button className="mt-4 w-full" variant="outline" onClick={()=>setUnlockOpen(false)}>稍后再说</Button>
    </Dialog.Content>
   </Dialog.Portal>
  </Dialog.Root>
  {order&&<Dialog.Root open={open} onOpenChange={setOpen}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content aria-describedby="payment-description" className="dialog-card payment-dialog" onCloseAutoFocus={event=>{if(paymentTrigger.current){event.preventDefault();paymentTrigger.current.focus()}}}><Dialog.Title>微信支付</Dialog.Title><Dialog.Description id="payment-description">{order.item_count} 项交付内容 · 支付后自动生成</Dialog.Description><div className="payment-amount">{money(order.amount_cents)}</div>{order.qr?<img alt="微信支付二维码" src={order.qr} className="payment-qr"/>:<p className="notice">{order.message||'支付入口正在核对，请勿重复下单。'}</p>}{errorNotice}<p className="payment-order-id">订单号 {order.id}</p><p className="helper-text">{order.qr?"微信内可长按二维码尝试识别，或用另一台设备扫码。关闭窗口后仍会继续核对支付进度。":order.checkout_state==='failed'?"这笔订单的支付入口创建失败，不会扣款。关闭后重新选择项目即可。":"先核对原订单，不会自动新建付款。若已付款，请等待到账核验；关闭窗口后仍会继续核对。"}</p><p className="helper-text mt-3">本服务不支持退款。请确认所选项目后付款。</p>{!order.qr&&order.checkout_state!=='failed'&&<Button className="w-full mt-4" variant="outline" disabled={busy} onClick={()=>action(`/api/pay/orders/${order.id}/resume`)}>核对并恢复支付入口</Button>}{order.checkout_state==='failed'&&<Button className="w-full mt-4" variant="outline" onClick={()=>setOpen(false)}>关闭并重新选择</Button>}<Button className="mt-5 w-full" variant="outline" onClick={()=>setOpen(false)}>关闭，稍后继续</Button></Dialog.Content></Dialog.Portal></Dialog.Root>}
 </main>;
}
