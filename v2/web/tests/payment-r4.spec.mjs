// Real React + Chromium. All API traffic is intercepted; no external calls.
import assert from 'node:assert/strict';
import {before, after, test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {mkdir} from 'node:fs/promises';
const require=createRequire(import.meta.url), {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..'), origin='http://127.0.0.1:4257';
let server,browser,output='';
before(async()=>{
 server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4257','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});
 server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);
 let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}
 assert.ok(ready,output);browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});
});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
const checkout=status=>({id:'R4-ORDER',job_id:'R4-JOB',status,amount_cents:390,item_count:1,message:'原二维码已到期，请核对并恢复支付入口。'});
const item=paid=>({kind:'logo_h',name:'Logo 横版组合',category:'logo',price_cents:390,purchased:paid,unlocked:paid,generated:paid,file:paid?'assets/examples/m-brand/logo.svg':''});
const resume=p=>p.getByRole('button',{name:'核对并恢复支付入口',exact:true});
async function shot(p,name){
 const dir='../../evidence/payment-r4-20261020/interactions';await mkdir(dir,{recursive:true});
 await p.screenshot({path:`${dir}/${name}.png`,fullPage:true,animations:'disabled'});
 assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
}
async function scenario(override,check){
 const ctx=await browser.newContext({viewport:{width:375,height:812},serviceWorkers:'block'}), errors=[],unexpected=[];
 const model={status:'pending',offline:false,resumes:0,creates:0};
 const json=(r,body,status=200)=>r.fulfill({status,json:body});
 ctx.on('page',p=>{p.setDefaultTimeout(5000);p.on('pageerror',e=>errors.push(e.message))});
 await ctx.route('**/*',async r=>{
  const req=r.request(),u=new URL(req.url()),path=u.pathname;
  if(u.origin!==origin){unexpected.push(u.origin);return r.abort()}
  if(!path.startsWith('/api/'))return r.continue();
  if(await override?.({r,path,req,json,model}))return;
  if(req.method()==='GET'&&model.offline)return r.abort('connectionreset');
  if(path==='/api/jobs/R4-JOB')return json(r,{status:'done',stage:'done',images:[]});
  if(path==='/api/jobs/R4-JOB/detail')return json(r,{items:[item(model.status==='paid')],pending_order_id:model.status==='pending'?'R4-ORDER':'',bundle_price:9900});
  if(path==='/api/pay/checkout/R4-ORDER')return json(r,checkout(model.status));
  if(path==='/api/pay/status/R4-ORDER')return json(r,{id:'R4-ORDER',status:model.status});
  if(path==='/api/pay/create'){model.creates++;return json(r,{error:'No new order expected'},500)}
  unexpected.push(req.method()+' '+path);return json(r,{error:'Unknown fixture'},500);
 });
 const p=await ctx.newPage();
 try{await p.goto(origin+'/job/R4-JOB');await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).click();await check(p,ctx,model);assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);assert.equal(model.creates,0)}
 finally{await ctx.close()}
}
for(const terminal of ['paid','closed'])test(`Payment R4: verified ${terminal} recovery response takes effect even if later reads fail`,async()=>{
 await scenario(async({r,path,model,json})=>{
  if(!path.endsWith('/resume'))return false;
  model.resumes++;model.status=terminal;model.offline=true;await json(r,checkout(terminal));return true;
 },async(p,ctx,m)=>{
  await resume(p).click();
  const text=terminal==='paid'?'支付已确认，已购内容会自动生成。':'此订单已关闭或支付失败，可重新选择项目。';
  await p.getByText(text,{exact:true}).waitFor();
  assert.equal(await p.getByRole('dialog',{name:'微信支付',exact:true}).count(),0);
  assert.equal(await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).count(),0);
  assert.equal(m.resumes,1);await shot(p,'terminal-'+terminal+'-offline');
  m.offline=false;await p.evaluate(()=>dispatchEvent(new Event('online')));
  if(terminal==='paid')await p.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true}).waitFor();
  else assert.equal(await p.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).isEnabled(),true);
 });
});
for(const late of ['pending','error'])test(`Payment R4: late ${late} recovery response cannot regress a verified paid order`,async()=>{
 let release;const held=new Promise(r=>release=r);
 await scenario(async({r,path,model,json})=>{
  if(!path.endsWith('/resume'))return false;
  model.resumes++;await held;try{await json(r,late==='pending'?checkout('pending'):{error:'核对结果暂未确认，请重试'},late==='pending'?202:503)}catch{}return true;
 },async(p,ctx,m)=>{try{
  await resume(p).click();
  for(let i=0;i<50&&!m.resumes;i++)await new Promise(r=>setTimeout(r,20));
  assert.equal(m.resumes,1);
  m.status='paid';await p.evaluate(()=>dispatchEvent(new Event('online')));
  await p.getByText('支付已确认，已购内容会自动生成。',{exact:true}).waitFor();
  m.offline=true;release();await new Promise(r=>setTimeout(r,200));
  assert.equal(await p.getByRole('dialog',{name:'微信支付',exact:true}).count(),0,'old pending must not reopen after paid');
  assert.equal(await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).count(),0);
  assert.equal(await p.getByRole('alert').count(),0,'an older failed recovery must not override verified success');
  await shot(p,'late-'+late+'-after-paid');
 }finally{release()}});
});
test('Payment R4: two tabs recover one order and converge on paid rights without creating a new checkout',async()=>{
 let release;const held=new Promise(r=>release=r);
 await scenario(async({r,path,model,json})=>{
  if(!path.endsWith('/resume'))return false;
  model.resumes++;if(model.resumes>1){await json(r,{error:'订单仍在核对中，请稍后重试'},409);return true}
  await held;try{await json(r,checkout('paid'))}catch{}return true;
 },async(p,ctx,m)=>{try{
  const b=await ctx.newPage();await b.goto(origin+'/job/R4-JOB');await b.getByRole('button',{name:'继续支付 / 查进度',exact:true}).click();
  await Promise.all([resume(p).click(),resume(b).click()]);
  for(let i=0;i<50&&m.resumes<2;i++)await new Promise(r=>setTimeout(r,20));
  assert.equal(m.resumes,2);m.status='paid';release();
  for(const tab of [p,b]){await tab.evaluate(()=>dispatchEvent(new Event('online')));await tab.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true}).waitFor();assert.equal(await tab.getByRole('dialog').count(),0)}
  await shot(b,'two-tabs-paid-rights');assert.equal(m.creates,0);
 }finally{release()}});
});
