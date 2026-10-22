// Independent Chromium regression: every API/external request is intercepted.
import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
const here=dirname(fileURLToPath(import.meta.url));
const root=resolve(here,'..');
const require=createRequire(resolve(root,'package.json'));
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const origin='http://127.0.0.1:4268';let server,browser,output='';
before(async()=>{
 server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4268','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});
 server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);
 let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}
 assert.ok(ready,output);browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});
});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
for(const retryNew of [false,true])test(`Payment R4: original create result is correlated and a new failure stays visible (new=${retryNew})`,async()=>{
 const ctx=await browser.newContext({viewport:{width:375,height:812},serviceWorkers:'block'});
 const errors=[],unexpected=[];let state='none',creates=0,statusReads=0,release;
 const held=new Promise(r=>release=r),json=(r,b,status=200)=>r.fulfill({status,json:b});
 const item=()=>({kind:'logo_h',name:'Logo 横版组合',category:'logo',price_cents:390,purchased:state==='paid',unlocked:state==='paid',generated:state==='paid',file:state==='paid'?'assets/examples/m-brand/logo.svg':''});
 const checkout=()=>({id:'R4-CREATE-ORDER',job_id:'R4-CREATE-JOB',status:state,amount_cents:390,item_count:1});
 await ctx.route('**/*',async r=>{
  const req=r.request(),u=new URL(req.url()),path=u.pathname;
  if(u.origin!==origin){unexpected.push(u.origin);return r.abort()}
  if(!path.startsWith('/api/'))return r.continue();
  if(path==='/api/pay/create'){
   creates++;if(creates>1)return json(r,{error:'新订单暂时无法创建，请稍后重试'},503);state='pending';await held;
   try{await json(r,{error:'支付入口仍待核对，已保留原订单，请勿重复付款'},503)}catch{}
   return;
  }
  if(path==='/api/jobs/R4-CREATE-JOB')return json(r,{status:'done',stage:'done',images:[]});
  if(path==='/api/jobs/R4-CREATE-JOB/detail')return json(r,{items:[item(),{kind:'logo_v',name:'Logo 竖版组合',category:'logo',price_cents:390,purchased:false,unlocked:false,generated:false,file:''}],pending_order_id:state==='pending'?'R4-CREATE-ORDER':'',bundle_price:9900});
  if(path==='/api/pay/checkout/R4-CREATE-ORDER')return json(r,checkout());
  if(path==='/api/pay/status/R4-CREATE-ORDER'){statusReads++;return json(r,checkout())}
  unexpected.push(req.method()+' '+path);return json(r,{error:'Unknown fixture'},500);
 });
 const page=await ctx.newPage();page.setDefaultTimeout(6000);page.on('pageerror',e=>errors.push(e.message));
 try{
  await page.goto(origin+'/job/R4-CREATE-JOB');
  await page.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).check();
  await page.getByRole('button',{name:'支付 ¥3.90',exact:true}).click();
  for(let i=0;i<60&&!creates;i++)await new Promise(r=>setTimeout(r,20));assert.equal(creates,1);
  // The original mutation is still held; read reconciliation discovers its ID.
  await page.evaluate(()=>dispatchEvent(new Event('focus')));
  await page.getByRole('button',{name:'继续支付 / 查进度',exact:true}).waitFor();
  state='paid';await page.evaluate(()=>dispatchEvent(new Event('online')));
  await page.getByText('支付已确认，已购内容会自动生成。',{exact:true}).waitFor();
  release();await page.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true}).waitFor();
  await new Promise(r=>setTimeout(r,250));
  const alerts=await page.getByRole('alert').allTextContents();
  console.log(JSON.stringify({creates,statusReads,paidMessage:await page.getByText('支付已确认，已购内容会自动生成。',{exact:true}).count(),alerts,unexpected,errors}));
  await page.screenshot({path:resolve(root,'../../evidence/payment-r4-20261020/interactions/create-late-error-'+retryNew+'.png'),fullPage:true,animations:'disabled'});
  assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);assert.equal(creates,1);
  assert.equal(alerts.length,0,'late original create error must not override its independently confirmed paid result');
  if(retryNew){
   await page.getByRole('button',{name:'追加交付项',exact:true}).click();
   await page.getByRole('checkbox',{name:'Logo 竖版组合',exact:true}).check();
   await page.getByRole('button',{name:'支付 ¥3.90',exact:true}).click();
   await page.getByRole('alert').filter({hasText:'新订单暂时无法创建，请稍后重试'}).waitFor();
   assert.equal(creates,2,'only the two deliberate create attempts are sent');
  }
  assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
 }finally{release();await ctx.close()}
});
