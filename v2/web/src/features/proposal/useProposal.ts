import {useEffect,useRef,useState} from "react";
import {api} from "../../lib/api";
import {readCheckoutProbe,saveCheckoutProbe,clearCheckoutProbe} from "./checkoutRecovery";
export type Item={kind:string;name:string;category:string;price_cents:number;purchased:boolean;generated:boolean;file:string;unlocked:boolean};
export type Delivery={started_at?:string;observed_at:string;needs_review:boolean;steps:{kind:string;index:number;state:string;started_at:string;finished_at?:string}[]};
export type State={delivery?:Delivery;status:string;stage:string;chosen?:number|null;logo_confirmed?:boolean;directions_unlocked?:boolean;unlock_price_cents?:number;images:{kind:string;idx:number;file:string}[];progress:string;user_error?:string;error?:string;can_retry?:boolean};
export type Detail={items:Item[];bundle_price:number;remaining_bundle_price?:number;pending_order_id?:string};
export type Checkout={id:string;qr?:string;amount_cents:number;item_count:number;status:string;message?:string;checkout_state?:string;checkout_error?:string;retryable?:boolean};
export const money=(n:number)=>`¥${(n/100).toFixed(2)}`;
const active=(s?:string)=>['queued','running','boards_running'].includes(s||'');
export function useProposal(id: string | undefined){
 const [state,setState]=useState<State|null>(null);const [detail,setDetail]=useState<Detail|null>(null);const detailRef=useRef<Detail|null>(null);
 const [checked,setChecked]=useState<Set<string>>(new Set());const [error,setError]=useState('');const [missing,setMissing]=useState(false);
 const [readError,setReadError]=useState('');
 const [busy,setBusy]=useState(false);const busyRef=useRef<symbol|null>(null);const [refresh,setRefresh]=useState(0);
 const [order,setOrder]=useState<Checkout|null>(null);const [open,setOpen]=useState(false);const [paymentMessage,setPaymentMessage]=useState('');
 const generation=useRef(0);
 const checkoutProbe=useRef<string[]|null>(null);
 const terminalOrders=useRef(new Map<string,string>());
 const currentOrder=useRef<string|null>(null);
 const creatingCheckout=useRef<{request:symbol;orderID?:string}|null>(null);
 // All verified responses share one terminal-state gate. An older pending
 // response (or error) must not reopen an order whose payment was confirmed.
 function finishCheckout(orderID:string,status:string){
  if(!['paid','closed','failed'].includes(status))return false;
  const previous=terminalOrders.current.get(orderID);
  if(previous===status||previous==='paid')return true;
  terminalOrders.current.set(orderID,status);
  if(currentOrder.current&&currentOrder.current!==orderID)return true;
  currentOrder.current=null;setOrder(null);setOpen(false);setError('');
  if(status==='paid'){setChecked(new Set());setPaymentMessage('支付已确认，已购内容会自动生成。')}
  else setPaymentMessage('此订单已关闭或支付失败，可重新选择项目。');
  setRefresh(n=>n+1);return true;
 }
 function receiveCheckout(checkout:Checkout,show=false){
  if(creatingCheckout.current&&!creatingCheckout.current.orderID&&!terminalOrders.current.has(checkout.id))creatingCheckout.current.orderID=checkout.id;
  if(finishCheckout(checkout.id,checkout.status)||terminalOrders.current.has(checkout.id))return;
  currentOrder.current=checkout.id;setOrder(checkout);if(show)setOpen(true);
 }
 useEffect(()=>{
  generation.current++;busyRef.current=null;setBusy(false);
  terminalOrders.current.clear();currentOrder.current=null;creatingCheckout.current=null;
  checkoutProbe.current=id?readCheckoutProbe(id):null;
  setState(null);setDetail(null);detailRef.current=null;setChecked(new Set());setOrder(null);setOpen(false);setError('');setMissing(false);setReadError('');setPaymentMessage('');
  return()=>{generation.current++};
 },[id]);
 // One serial job poll; focus/online cannot overlap an outstanding request.
 useEffect(()=>{
  let stopped=false,inflight=false;let timer:ReturnType<typeof setTimeout>|undefined;
  let failures=0,lastDetailAt=0,lastDetailKey='';
  const controller=new AbortController();
  const tick=async()=>{
   if(stopped||inflight||document.hidden||!navigator.onLine)return;inflight=true;clearTimeout(timer);let again=true,delay=4000;
   try{
    const s:State=await api(`/api/jobs/${id}`,{signal:controller.signal});
    const key=`${s.stage}|${s.status}|${s.progress}|${s.images.length}|${s.chosen??''}|${s.logo_confirmed??''}`;
    const now=Date.now();
    // The job state is cheap and drives progress. The detail payload is larger,
    // so refresh it on a stage/state change, during checkout, or at a bounded
    // interval. Payment recovery gets the faster path.
    const detailWindow=(checkoutProbe.current||currentOrder.current)?4000:8000;
    if(!detailRef.current||key!==lastDetailKey||now-lastDetailAt>detailWindow){
     const d:Detail=await api(`/api/jobs/${id}/detail`,{signal:controller.signal});
     detailRef.current=d;lastDetailAt=Date.now();lastDetailKey=key;setDetail(d);
    }
    const d=detailRef.current;
    if(!d)throw new Error('详情尚未就绪');
    if(stopped)return;setState(s);setMissing(false);setReadError('');
    setChecked(prev=>new Set([...prev].filter(k=>d.items.some(it=>it.kind===k&&!it.purchased))));
    if(id&&checkoutProbe.current&&(d.pending_order_id||(checkoutProbe.current.length>0&&checkoutProbe.current.every(k=>d.items.some(it=>it.kind===k&&it.purchased))))){
     checkoutProbe.current=null;clearCheckoutProbe(id);
    }
    again=active(s.status)||!!s.delivery?.needs_review||!!d.pending_order_id||!!checkoutProbe.current;
    if(d.pending_order_id){const o:Checkout=await api(`/api/pay/checkout/${d.pending_order_id}`,{signal:controller.signal});if(!stopped)receiveCheckout(o)}
    failures=0;
   }catch(e:any){if(stopped)return;failures++;delay=Math.min(20000,2000*2**Math.min(failures,3));if(e.status===404){setMissing(true);again=false;if(id)clearCheckoutProbe(id);checkoutProbe.current=null}else setReadError('暂时无法刷新进度，已保留当前内容。联网后将自动重试。')}
   finally{inflight=false;if(!stopped&&again)timer=setTimeout(tick,delay)}
  };
  const wake=()=>{if(!document.hidden){clearTimeout(timer);void tick()}};void tick();
  document.addEventListener('visibilitychange',wake);window.addEventListener('online',wake);window.addEventListener('focus',wake);
  return()=>{stopped=true;controller.abort();clearTimeout(timer);document.removeEventListener('visibilitychange',wake);window.removeEventListener('online',wake);window.removeEventListener('focus',wake)}
 },[id,refresh]);
 const orderID=order?.id;
 useEffect(()=>{
  if(!orderID)return;let stopped=false,inflight=false;let timer:ReturnType<typeof setTimeout>|undefined;const controller=new AbortController();let terminal=false;
  const tick=async()=>{
   if(stopped||inflight||terminal||document.hidden||!navigator.onLine)return;inflight=true;clearTimeout(timer);
   try{const s=await api(`/api/pay/status/${orderID}`,{signal:controller.signal});if(stopped)return;
    terminal=finishCheckout(orderID,s.status);
   }catch{if(!stopped&&!terminalOrders.current.has(orderID))setPaymentMessage('支付结果暂未确认，请勿重复付款。联网后会自动核对。')}
   finally{inflight=false;if(!stopped&&!terminal)timer=setTimeout(tick,4000)}
  };const wake=()=>{clearTimeout(timer);void tick()};void tick();document.addEventListener('visibilitychange',wake);window.addEventListener('online',wake);
  return()=>{stopped=true;controller.abort();clearTimeout(timer);document.removeEventListener('visibilitychange',wake);window.removeEventListener('online',wake)}
 },[orderID]);
 async function action(path:string,body?:unknown){
  if(busyRef.current)return;const request=Symbol();busyRef.current=request;setBusy(true);setError('');const epoch=generation.current;
  const creation:{request:symbol;orderID?:string}|null=path==='/api/pay/create'?{request}:null;
  if(creation)creatingCheckout.current=creation;
  if(path==='/api/pay/create'&&id){
   const selection=body as {all?:boolean;items?:string[]};
   const items=(detail?.items||[]).filter(it=>it.category!=='free'&&!it.purchased&&(selection?.all||selection?.items?.includes(it.kind))).map(it=>it.kind);
   checkoutProbe.current=items;saveCheckoutProbe(id,items);
  }
  try{const r=await api(path,{method:'POST',body:JSON.stringify(body||{})});if(epoch!==generation.current)return;
   if(r.id&&r.amount_cents!==undefined)receiveCheckout(r,true);setRefresh(n=>n+1);
  }catch(e:any){if(epoch===generation.current){
   if(path==='/api/pay/create'&&id&&e.status>=400&&e.status<500){checkoutProbe.current=null;clearCheckoutProbe(id)}
   const resumedOrder=path.match(/^\/api\/pay\/orders\/([^/]+)\/resume$/)?.[1];
   const affectedOrder=resumedOrder||creation?.orderID;
   if(!affectedOrder||!terminalOrders.current.has(affectedOrder))setError(path.startsWith('/api/pay/')&&!e.status?'支付结果暂未确认，正在核对原订单，请勿重复付款。':e.message||'操作失败，请重试');
   // The server may have accepted a timed-out POST. Read the durable job/order
   // immediately; never replay a payment or generation mutation automatically.
   setRefresh(n=>n+1);
  }}
  finally{if(creatingCheckout.current?.request===request)creatingCheckout.current=null;if(busyRef.current===request){busyRef.current=null;if(epoch===generation.current)setBusy(false)}}
 }

 return {state,detail,checked,setChecked,error,setError,missing,readError,busy,refresh:setRefresh,order,open,setOpen,paymentMessage,action};
}
