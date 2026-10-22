// Actual React UI in Chromium; every API call and external origin is mocked.
import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {mkdir} from 'node:fs/promises';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const require=createRequire(import.meta.url),{chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const origin='http://127.0.0.1:4273';let server,browser,output='';
before(async()=>{
 server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4273','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});
 server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);
 let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}assert.ok(ready,output);
 browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});
});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
for(const width of [375,1440])for(const mode of ['restart-reopen','repeated-timeout'])test(`R5 ${mode} ${width}px: two tabs retain one order and converge without auto POST`,async()=>{
 const ctx=await browser.newContext({viewport:{width,height:900},serviceWorkers:'block'}),errors=[],unknown=[];
 let status='pending',outage=false,resumes=0,creates=0,reads=0;
 const checkout=()=>({id:'R5-ORDER',job_id:'R5-JOB',status,amount_cents:390,item_count:1,message:'原二维码已到期，请核对并恢复支付入口。'});
 ctx.on('page',p=>{p.setDefaultTimeout(7000);p.on('pageerror',e=>errors.push(e.message))});
 await ctx.route('**/*',async r=>{
  const req=r.request(),u=new URL(req.url()),p=u.pathname,json=(b,s=200)=>r.fulfill({status:s,json:b});
  if(u.origin!==origin){unknown.push(u.origin);return r.abort()}
  if(!p.startsWith('/api/'))return r.continue();
  if(req.method()==='GET'){reads++;if(outage)return json({error:'服务暂时不可用'},503)}
  if(p==='/api/jobs/R5-JOB')return json({status:'done',stage:'done',images:[]});
  if(p==='/api/jobs/R5-JOB/detail')return json({items:[{kind:'logo_h',name:'Logo 横版组合',category:'logo',price_cents:390,purchased:status==='paid',unlocked:status==='paid',generated:status==='paid',file:status==='paid'?'assets/examples/m-brand/logo.svg':''}],pending_order_id:status==='pending'?'R5-ORDER':'',bundle_price:9900});
  if(p==='/api/pay/checkout/R5-ORDER'||p==='/api/pay/status/R5-ORDER')return json(checkout());
  if(p==='/api/pay/orders/R5-ORDER/resume'){resumes++;return json({error:'渠道尚未确认订单，请勿重复支付，稍后重试或联系支持'},resumes%2===0?409:503)}
  if(p==='/api/pay/create'){creates++;return json({error:'Unexpected new order'},500)}
  unknown.push(req.method()+' '+p);return json({error:'Unknown fixture'},500);
 });
 const tabs=[await ctx.newPage(),await ctx.newPage()];
 try{
  for(const p of tabs){await p.goto(origin+'/job/R5-JOB');await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).waitFor()}
  if(mode==='restart-reopen'){
   outage=true;await tabs[0].reload();await tabs[0].getByText('暂时无法刷新进度，已保留当前内容。联网后将自动重试。',{exact:true}).waitFor();
   await tabs[1].close();tabs[1]=await ctx.newPage();await tabs[1].goto(origin+'/job/R5-JOB');
   outage=false;
   for(const p of tabs){await p.evaluate(()=>dispatchEvent(new Event('online')));await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).waitFor()}
   assert.equal(resumes,0,'restart/reopen must only read persisted order');
  }else{
   for(const p of tabs)await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).click();
   for(let round=0;round<3;round++){
    await Promise.all(tabs.map(p=>p.getByRole('button',{name:'核对并恢复支付入口',exact:true}).click()));
    for(const p of tabs){try{await p.getByRole('alert').filter({hasText:'渠道尚未确认订单'}).waitFor()}catch(e){
     console.log(JSON.stringify({resumes,alertDOM:await p.locator('[role=alert]').evaluateAll(es=>es.map(e=>({text:e.textContent,hiddenByModal:!!e.closest('[aria-hidden=true]')})))}));
     const dir=resolve(root,'../../evidence/payment-r5-20261020/interactions');await mkdir(dir,{recursive:true});await p.screenshot({path:resolve(dir,`retry-error-hidden-${width}.png`),fullPage:true});throw e
    }}
    assert.equal(resumes,(round+1)*2);
   }
   for(const p of tabs)await p.evaluate(()=>{for(let i=0;i<6;i++){dispatchEvent(new Event('online'));dispatchEvent(new Event('focus'))}});
   await new Promise(r=>setTimeout(r,250));assert.equal(resumes,6);
  }
  status='paid';
  for(const p of tabs){await p.evaluate(()=>dispatchEvent(new Event('online')));await p.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true}).waitFor();assert.equal(await p.getByRole('dialog').count(),0);assert.equal(await p.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).count(),0);assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
  const folder=resolve(root,'../../evidence/payment-r5-20261020/interactions');await mkdir(folder,{recursive:true});await tabs[1].screenshot({path:resolve(folder,`${mode}-${width}.png`),fullPage:true,animations:'disabled'});
  assert.equal(creates,0);assert.equal(resumes,mode==='repeated-timeout'?6:0);assert.deepEqual(errors,[]);assert.deepEqual(unknown,[]);
  console.log(JSON.stringify({mode,width,reads,resumes,creates,errors,unknown}));
 }finally{await ctx.close()}
});
