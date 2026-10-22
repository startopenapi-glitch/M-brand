// Isolated fixtures only. No network clients, production credentials or real orders.
export const asset='/assets/examples/m-brand/';
export const definitions=[['logo_h','Logo 横版组合',390,'logo'],['logo_v','Logo 竖版组合',390,'logo'],['palette','品牌色彩规范',190,'system'],['card_front','名片正面',290,'apps'],['brand_story','品牌故事',490,'boards']];
export const sampleJobs=[{id:'CHOOSE',status:'ready_to_choose',stage:'choose',images:3,thumb:asset.slice(1)+'logo.svg',created_at:'2026-10-20 10:00'},{id:'DELIVERED',status:'done',stage:'done',images:5,thumb:asset.slice(1)+'applications.svg',created_at:'2026-10-20 09:00'}];
export function fixtures(){
 let draft={},paid=false,pending=false;const selected=new Set();
 return function respond(method,path,body={}){
  if(path==='/api/draft'){if(method==='PUT'){draft=body;return {ok:true}}return draft}
  if(path==='/api/jobs'&&method==='POST')return {id:'CHOOSE'};
  if(path==='/api/my/jobs')return sampleJobs;
  if(path==='/api/my/orders'||path==='/api/admin/orders')return [{id:'PREVIEW-ONLY-ORDER',job_id:'DELIVERED',amount_cents:390,channel:'native',status:'paid',mock:true,created_at:'2026-10-20 10:00',paid_at:'2026-10-20 10:01'}];
  if(path==='/api/cases')return [];
  if(path==='/api/items')return {items:definitions.map(([kind,name,price_cents])=>({kind,name,price_cents})),bundle_price:4990};
  if(path==='/api/plan')return {price_fen:4990,price_label:'¥49.90',includes:[],pay_enabled:true,sales_paused:false};
  if(path==='/api/recover')return {job_id:'DELIVERED'};
  if(path==='/api/code')return {code:'PREVIEW',remaining:1};
  if(path==='/api/pay/create'){pending=true;return {id:'PREVIEW-ONLY-ORDER',amount_cents:390,item_count:1,status:'pending',message:'隔离预览，不会创建真实支付二维码。'}}
  if(path==='/api/pay/checkout/PREVIEW-ONLY-ORDER'||path==='/api/pay/status/PREVIEW-ONLY-ORDER')return {id:'PREVIEW-ONLY-ORDER',status:paid?'paid':'pending',amount_cents:390,item_count:1,message:'隔离预览，不会发起付款。'};
  if(path==='/api/pay/orders/PREVIEW-ONLY-ORDER/confirm'){paid=true;return {status:'paid'}}
  if(path==='/api/pay/orders/PREVIEW-ONLY-ORDER/resume')return {id:'PREVIEW-ONLY-ORDER',status:'pending',amount_cents:390,item_count:1,message:'隔离预览不提供真实付款。'};
  if(path.startsWith('/api/jobs/')){
   const parts=path.split('/'),id=parts[3],action=parts[4];
   const choosing=id==='CHOOSE'&&!selected.has(id),done=id==='DELIVERED'||paid;
   if(action==='choose'&&method==='POST'){selected.add(id);return {ok:true}}
   if(method==='POST')return {ok:true};
   if(action==='detail')return {items:[{kind:'system_card',name:'品牌系统卡',category:'free',purchased:true,generated:true,unlocked:true,file:asset.slice(1)+'system.svg',price_cents:0},...definitions.map(([kind,name,price_cents,category])=>({kind,name,price_cents,category,purchased:done,generated:done,unlocked:done,file:done?asset.slice(1)+'applications.svg':''}))],bundle_price:4990,remaining_bundle_price:4990,pending_order_id:pending?'PREVIEW-ONLY-ORDER':''};
   return {id,status:choosing?'ready_to_choose':id==='FAILED'?'error':done?'done':'boards_running',stage:choosing?'choose':'boards',chosen:1,images:[0,1,2].map(idx=>({kind:'direction',idx,file:asset.slice(1)+['logo.svg','hero.svg','applications.svg'][idx]})),progress:'正在准备交付内容',user_error:id==='FAILED'?'服务暂不可用，已完成的内容和权益都已保留。':'',can_retry:id==='FAILED'};
  }
  if(path==='/api/admin/session')return {name:'preview',capabilities:['overview','orders','jobs','support','codes','cases','agents','usage','audit']};
  if(path==='/api/admin/orders/page')return {items:[],total:0,page:1,page_size:50,summary:{revenue_cents:0,paid:0,pending:0}};
  if(['/api/admin/support','/api/admin/agent-payouts'].includes(path))return [];
  if(path==='/api/admin/agents/reconcile')return {anomalies:[]};
  if(path==='/api/admin/stats')return {jobs:2,codes:0,paid_cents:0,orders:0,visitors:0,images:3,health:{}};
  if(path==='/api/admin/jobs')return sampleJobs;
  if(path==='/api/admin/agents')return [{id:'DEMO',name:'示例代理',email:'demo@example.invalid',code:'PREVIEW',active:true,commission_pct:40,total_earned_cents:400,total_paid_cents:0,withdrawable_cents:400}];
  if(path==='/api/admin/agents/stats')return {agent_count:1,total_commission_cents:400,total_paid_cents:0,total_pending_cents:400};
  if(['/api/admin/codes','/api/admin/usage','/api/admin/audit'].includes(path))return [];
  if(path==='/api/agent/me')return {name:'示例代理',code:'PREVIEW',wechat:'',link:'http://localhost/?ref=PREVIEW',commission_pct:40,total_earned_cents:400,total_paid_cents:0,withdrawable_cents:400};
  if(path==='/api/agent/me/stats')return {order_count:1,pending_count:1,pending_cents:400,total_earned_cents:400,total_paid_cents:0,withdrawable_cents:400};
  if(path==='/api/agent/me/commissions')return [{order_id:'DISPLAY-ID',order_amount_cents:1000,commission_cents:400,status:'pending',created_at:'2026-10-20 10:00'}];
  return undefined;
 }
}
