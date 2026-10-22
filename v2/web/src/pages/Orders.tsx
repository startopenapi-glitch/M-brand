import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ArrowRight, FileText, FolderOpen, KeyRound, Plus, X } from "lucide-react";
import * as Dialog from "@radix-ui/react-dialog";
import { Button } from "../components/ui/button";
import { Input, Label, Textarea } from "../components/ui/field";
import { api, IMG } from "../lib/api";
type Order={id:string;amount_cents:number;channel:string;status:string;mock:boolean;created_at:string;paid_at:string;invite_code?:string;code_state?:string;job_id?:string};
type Job={id:string;status:string;stage:string;chosen:number;trial:boolean;images:number;thumb:string;created_at:string};
const statusText:Record<string,string>={paid:'已支付',pending:'待支付',closed:'已关闭',expired:'已过期',failed:'支付失败'};
const jobText:Record<string,string>={queued:'排队中',running:'生成方向中',ready_to_choose:'待选定方向',boards_running:'生成成图中',done:'已完成',error:'部分内容未完成'};
export default function Orders(){
 const recoverTrigger=useRef<HTMLButtonElement>(null);
 const [search,setSearch]=useSearchParams();const tab=search.get('tab')==='orders'?'orders':'jobs';
 const [orders,setOrders]=useState<Order[]|null>(null),[jobs,setJobs]=useState<Job[]|null>(null);
 const [ordersErr,setOrdersErr]=useState(''),[jobsErr,setJobsErr]=useState('');
 const [busy,setBusy]=useState(''),busyRef=useRef(false),[msg,setMsg]=useState('');
 const [recoverOpen,setRecoverOpen]=useState(false),[oid,setOid]=useState(''),[recoverMsg,setRecoverMsg]=useState('');
 const [recoverBusy,setRecoverBusy]=useState(false),recoverRef=useRef(false),[recoverOK,setRecoverOK]=useState(false);
 const [supportOpen,setSupportOpen]=useState(false),[supportOrder,setSupportOrder]=useState(''),[supportText,setSupportText]=useState(''),[supportMsg,setSupportMsg]=useState(''),[supportBusy,setSupportBusy]=useState(false);
 const loadOrders=()=>{setOrdersErr('');return api('/api/my/orders').then(setOrders).catch(e=>setOrdersErr(e.message))};
 const loadJobs=()=>{setJobsErr('');return api('/api/my/jobs').then(setJobs).catch(e=>setJobsErr(e.message))};
 const load=()=>{void loadOrders();void loadJobs()};
 useEffect(()=>{load()},[]);
 async function confirm(id:string){if(busyRef.current)return;busyRef.current=true;setBusy(id);setMsg('');try{const r=await api(`/api/pay/orders/${id}/confirm`,{method:'POST'});setMsg(r.status==='paid'?'支付已确认':`状态：${statusText[r.status]||'待核对'}（未检测到支付）`);if(r.status==='paid')load()}catch(e:any){setMsg(e.message)}finally{busyRef.current=false;setBusy('')}}
 async function createSupport(){
  if(supportBusy||!supportText.trim())return;
  setSupportBusy(true);setSupportMsg('');
  try{
   await api('/api/support',{method:'POST',body:JSON.stringify({request_key:crypto.randomUUID(),order_id:supportOrder,category:'order',description:supportText.trim()})});
   setSupportMsg('已提交售后申请，我们会按提交顺序处理。');setSupportText('');
  }catch(e:any){setSupportMsg(e.message)}
  finally{setSupportBusy(false)}
 }
 async function recover(){if(recoverRef.current||!oid.trim())return;recoverRef.current=true;setRecoverBusy(true);setRecoverMsg('');setRecoverOK(false);try{const r=await api('/api/recover',{method:'POST',body:JSON.stringify({order_id:oid.trim()})});setRecoverMsg(r.job_id?'已找回品牌提案及已购交付项，已绑定到本设备':`已找回：${r.code}（剩余 ${r.remaining} 次）· 已绑定到本设备`);setRecoverOK(true);setOid('');load()}catch(e:any){setRecoverMsg(e.message)}finally{recoverRef.current=false;setRecoverBusy(false)}}
 const pickTab=(next:string)=>{const params=new URLSearchParams(search);params.set('tab',next);setSearch(params,{replace:true})};
 return <main className="page-width orders-page">
  <div className="page-heading"><div><span className="eyebrow">你的品牌，都在这里</span><h1>我的提案</h1><p>继续设计，查看交付，或核对你的订单。</p></div><Button asChild><Link to="/create"><Plus size={16}/>创建品牌</Link></Button></div>
  <div className="orders-toolbar"><div role="tablist" aria-label="提案与订单" className="segmented"><button role="tab" id="tab-jobs" aria-selected={tab==='jobs'} aria-controls="panel-jobs" onClick={()=>pickTab('jobs')}>提案</button><button role="tab" id="tab-orders" aria-selected={tab==='orders'} aria-controls="panel-orders" onClick={()=>pickTab('orders')}>订单</button></div><button ref={recoverTrigger} className="recover-link" onClick={()=>setRecoverOpen(true)}><KeyRound size={15}/>换设备找回</button></div>
  {msg&&<p role="status" className="notice mb-5">{msg}</p>}
  <section role="tabpanel" id="panel-jobs" aria-labelledby="tab-jobs" hidden={tab!=='jobs'}>
   {jobsErr?<div className="surface empty-state"><p role="alert">{jobsErr}</p><Button variant="outline" onClick={()=>void loadJobs()}>重试加载提案</Button></div>:jobs===null?<p role="status" className="loading-line">正在读取提案…</p>:jobs.length===0?<div className="surface empty-state"><FolderOpen size={34}/><h2>你的第一个品牌，从这里开始。</h2><p>9.9 元解锁自己的品牌，选定后再按需购买。</p><Button asChild><Link to="/create">创建我的品牌<ArrowRight size={16}/></Link></Button></div>:<div className="proposal-list">{jobs.map(job=><Link to={`/job/${job.id}`} className="surface proposal-list-item" key={job.id}>{job.thumb?<img src={IMG(job.thumb)} alt="提案方向预览" width={80} height={100} loading="lazy"/>:<div className="list-placeholder"><FileText size={24}/></div>}<div className="proposal-list-info"><h2>品牌提案 <span>{job.id}</span></h2><p>{job.created_at} · 已生成 {job.images} 张</p><span className={`status-pill ${job.status==='done'?'success':job.status==='error'?'error':''}`}>{jobText[job.status]||'状态待核对'}</span></div><span className="continue-link">继续查看<ArrowRight size={17}/></span></Link>)}</div>}
  </section>
  <section role="tabpanel" id="panel-orders" aria-labelledby="tab-orders" hidden={tab!=='orders'}>
   {ordersErr?<div className="surface empty-state"><p role="alert">{ordersErr}</p><Button variant="outline" onClick={()=>void loadOrders()}>重试加载订单</Button></div>:orders===null?<p role="status" className="loading-line">正在读取订单…</p>:orders.length===0?<div className="surface empty-state"><FileText size={34}/><h2>暂无订单</h2><p>在品牌提案中选购交付项后，订单会显示在这里。</p><Button variant="outline" onClick={()=>pickTab('jobs')}>查看我的提案</Button></div>:<div className="order-list">{orders.map(order=><article className="surface order-card" key={order.id}><div className="order-card-main"><div><span className={`status-pill ${order.status==='paid'?'success':['failed'].includes(order.status)?'error':order.status==='pending'?'':'muted'}`}>{statusText[order.status]||'待核对'}</span>{order.mock&&<span className="status-pill muted ml-2">测试订单</span>}<h2>{order.job_id?'品牌交付项':'品牌提案订单'}</h2><p>{order.created_at}{order.paid_at&&` · 支付于 ${order.paid_at}`}</p></div><strong className="order-amount">¥{(order.amount_cents/100).toFixed(2)}</strong></div><details className="order-details"><summary>订单详情</summary><p>订单号：<span className="break-all">{order.id}</span></p><p>支付方式：{order.channel==='h5'?'微信 H5':'微信扫码'}</p>{order.invite_code&&<p>邀请码：{order.invite_code} · {order.code_state} · 一码一次，一码一人</p>}</details><div className="order-card-actions">{order.job_id&&<Button variant="outline" asChild><Link to={`/job/${order.job_id}`}>查看关联提案</Link></Button>}{order.status==='pending'&&<Button variant="outline" disabled={!!busy} onClick={()=>confirm(order.id)}>{busy===order.id?'查询中…':'查询支付结果'}</Button>}{!order.mock&&<Button variant="outline" onClick={()=>{setSupportOrder(order.id);setSupportMsg('');setSupportOpen(true)}}>申请售后</Button>}</div></article>)}</div>}
   {orders?.some(order=>order.status==='pending')&&<p className="helper-text mt-5">已扫码但仍显示待支付？请查询支付结果，核对前不要重复付款。</p>}
  </section>
  <p className="device-note">提案保存在当前浏览器会话中。换设备时，可凭付款订单号找回已购内容。</p>
  <Dialog.Root open={recoverOpen} onOpenChange={setRecoverOpen}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="dialog-card" aria-describedby="recover-description" onCloseAutoFocus={event=>{event.preventDefault();recoverTrigger.current?.focus()}}><Dialog.Title>找回已购内容</Dialog.Title><Dialog.Close className="icon-button dialog-close" aria-label="关闭找回"><X size={20}/></Dialog.Close><Dialog.Description id="recover-description" className="helper-text mt-3 mb-5">输入付款订单号，将原有权益绑定到本设备。未使用的邀请码也可兑换；已使用的邀请码请改用订单号找回。</Dialog.Description><form onSubmit={e=>{e.preventDefault();void recover()}}><Label htmlFor="recover-id">订单号或邀请码</Label><Input id="recover-id" autoComplete="off" value={oid} onChange={e=>setOid(e.target.value.toUpperCase())} disabled={recoverBusy}/><Button type="submit" className="w-full mt-5" disabled={recoverBusy||!oid.trim()}>{recoverBusy?'正在找回…':'找回'}</Button></form>{recoverMsg&&<p role={recoverOK?'status':'alert'} className={`notice mt-4 ${recoverOK?'notice-success':'notice-error'}`}>{recoverMsg}</p>}</Dialog.Content></Dialog.Portal></Dialog.Root>
  <Dialog.Root open={supportOpen} onOpenChange={setSupportOpen}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="dialog-card" aria-describedby="support-description"><Dialog.Title>申请售后</Dialog.Title><Dialog.Close className="icon-button dialog-close" aria-label="关闭售后"><X size={20}/></Dialog.Close><Dialog.Description id="support-description" className="helper-text mt-3 mb-4">请描述具体问题。生成失败、付款核对或交付异常都可以提交；本服务不承诺自动退款。</Dialog.Description><form onSubmit={e=>{e.preventDefault();void createSupport()}}><Label htmlFor="support-text">问题描述</Label><Textarea id="support-text" rows={5} value={supportText} onChange={e=>setSupportText(e.target.value)} placeholder="例如：9.9 解锁后方向没有生成；订单重复支付" disabled={supportBusy}/><Button type="submit" className="w-full mt-4" disabled={supportBusy||!supportText.trim()}>{supportBusy?'提交中…':'提交售后'}</Button></form>{supportMsg&&<p role="status" className="notice mt-4">{supportMsg}</p>}</Dialog.Content></Dialog.Portal></Dialog.Root>
 </main>;
}
