// Isolated real React tests. API responses are fixtures; all external requests are blocked.
import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {mkdir} from 'node:fs/promises';
const require=createRequire(import.meta.url),{chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..'),origin='http://127.0.0.1:4255';
let server,browser,output='';
before(async()=>{server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4255','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}assert.ok(ready,output);browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
async function run(path,handler,check,setup){
 const ctx=await browser.newContext({viewport:{width:375,height:812},serviceWorkers:'block'}),unexpected=[],errors=[];let draft={};
 const json=(r,b,status=200)=>r.fulfill({status,json:b});
 ctx.on('page',p=>{p.setDefaultTimeout(2200);p.on('pageerror',e=>errors.push(e.message))});
 await ctx.route('**/*',async r=>{const req=r.request(),u=new URL(req.url());if(u.origin!==origin){unexpected.push(u.origin);return r.abort()}
  if(!u.pathname.startsWith('/api/'))return r.continue();
  if(await handler({r,path:u.pathname,req,json,ctx}))return;
  if(u.pathname==='/api/draft'){if(req.method()==='PUT')draft=req.postDataJSON();return json(r,req.method()==='GET'?draft:{ok:true})}
  if(u.pathname.startsWith('/api/jobs/')&&u.pathname.endsWith('/detail'))return json(r,{items:[],bundle_price:4990});
  if(u.pathname.startsWith('/api/jobs/')&&req.method()==='GET')return json(r,{status:'running',stage:'directions',images:[],progress:'准备方向'});
  if(['/api/my/jobs','/api/my/orders','/api/cases'].includes(u.pathname))return json(r,[]);
  unexpected.push(req.method()+' '+u.pathname);return json(r,{error:'Unexpected fixture'},500);
 });
 const page=await ctx.newPage();try{if(setup)await setup(page);await page.goto(origin+path);await check(page,ctx);assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])}finally{await ctx.close()}
}
async function fill(p){await p.getByLabel('品牌名称',{exact:true}).fill('回归品牌');await p.getByLabel('所属行业',{exact:true}).fill('电商');}
const submit=p=>p.getByRole('button',{name:/(9\.9 元解锁自己的品牌|免费生成 3 个方向|继续核对创建结果)/});

test('draft outage never enables an empty replacement and automatically restores the saved brand',async()=>{
 let reads=0,writes=0,recovering=false;
 await run('/create',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()!=='GET'){writes++;await json(r,{ok:true});return true}
  reads++;await json(r,!recovering?{error:'temporary outage'}:{cn_name:'已保存品牌',industry:'电商'},!recovering?503:200);return true;
 },async p=>{
  await p.getByText('暂时无法读取已保存草稿，正在自动重试；恢复前不会覆盖服务器资料。',{exact:true}).waitFor();
  assert.equal(await p.getByLabel('品牌名称',{exact:true}).isDisabled(),true);
  assert.equal(writes,0);
  recovering=true;
  for(let i=0;i<80&&await p.getByLabel('品牌名称',{exact:true}).inputValue()!=='已保存品牌';i++)await new Promise(r=>setTimeout(r,100));
  assert.equal(await p.getByLabel('品牌名称',{exact:true}).inputValue(),'已保存品牌');
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'电商');
  assert.equal(await p.getByLabel('品牌名称',{exact:true}).isEnabled(),true);
  assert.ok(reads>=2);assert.equal(writes,0);
 });
});

test('slow successful draft restore merges edits typed before response and preserves saved fields',async()=>{
 let release;const held=new Promise(r=>release=r),writes=[];
 await run('/create',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()==='GET'){
   await held;
   await json(r,{cn_name:'已保存品牌',industry:'原行业',products:'原产品'});
  }else{
   writes.push(req.postDataJSON());
   await json(r,{ok:true});
  }
  return true;
 },async p=>{try{
  await p.getByLabel('品牌名称',{exact:true}).fill('已保存品牌');
  release();
  await p.getByLabel('所属行业',{exact:true}).fill('新行业');
  await p.getByText('草稿已保存',{exact:true}).waitFor({timeout:15000});
  assert.equal(await p.getByLabel('品牌名称',{exact:true}).inputValue(),'已保存品牌');
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'新行业');
  assert.equal(await p.getByLabel('简要说明').inputValue(),'原产品');
  assert.equal(writes.length,1);
  assert.equal(writes[0].products,'原产品');
 }finally{release()}});
});

test('URL prefill for the saved brand retains the existing server fields',async()=>{
 const writes=[];
 await run('/create?cn_name=%E9%A2%84%E5%A1%AB%E5%93%81%E7%89%8C',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,{cn_name:'预填品牌',industry:'原行业',products:'原产品'});
  else{writes.push(req.postDataJSON());await json(r,{ok:true})}
  return true;
 },async p=>{
  await p.getByLabel('简要说明').waitFor();
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor({timeout:7000});
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'原行业');
  assert.equal(await p.getByLabel('简要说明').inputValue(),'原产品');
  assert.equal(writes.length,0);
 });
});

test('URL prefill for another brand keeps server draft until explicit submit',async()=>{
 const writes=[];
 await run('/create?cn_name=%E6%96%B0%E5%93%81%E7%89%8C',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,{cn_name:'原品牌',industry:'原行业',products:'原产品'});
  else{writes.push(req.postDataJSON());await json(r,{ok:true})}
  return true;
 },async p=>{
  await p.getByText('本页品牌已保留，点击生成时再同步草稿',{exact:true}).waitFor({timeout:7000});
  await p.waitForTimeout(1100);
  assert.equal(await p.getByLabel('品牌名称',{exact:true}).inputValue(),'新品牌');
  assert.equal(await p.getByLabel('简要说明').inputValue(),'');
  assert.equal(writes.length,0);
 });
});

test('stale same-name tab draft cannot overwrite newer server fields',async()=>{
 const writes=[];
 await run('/create',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,{cn_name:'同名品牌',industry:'最新行业',products:'最新产品'});
  else{writes.push(req.postDataJSON());await json(r,{ok:true})}
  return true;
 },async p=>{
  await p.getByText('本页草稿与已保存版本不同，请核对后主动生成',{exact:true}).waitFor({timeout:7000});
  await p.getByLabel('所属行业',{exact:true}).fill('本页修改');
  await p.getByText('本页草稿保存在本设备，点击生成时同步',{exact:true}).waitFor();
  await p.waitForTimeout(1100);
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'本页修改');
  assert.equal(writes.length,0);
 },async p=>p.addInitScript(()=>sessionStorage.setItem('zb_tab_draft_v1',JSON.stringify({lang:'中英结合',cn_name:'同名品牌',industry:'旧行业',products:'旧产品'}))));
});

test('synced tab cache yields to newer same-brand server edits without losing tab identity',async()=>{
 const writes=[];
 await run('/create',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,{cn_name:'同名品牌',industry:'最新行业',products:'最新产品'});
  else{writes.push(req.postDataJSON());await json(r,{ok:true})}
  return true;
 },async p=>{
  await p.waitForFunction(()=>document.querySelector('#brand-industry')?.value==='最新行业',null,{timeout:7000});
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'最新行业');
  assert.equal(await p.getByLabel('简要说明').inputValue(),'最新产品');
  assert.equal(writes.length,0);
  assert.equal(JSON.parse(await p.evaluate(()=>sessionStorage.getItem('zb_tab_draft_v1'))).industry,'最新行业');
 },async p=>p.addInitScript(()=>{
  const prior=JSON.stringify({lang:'中英结合',cn_name:'同名品牌',industry:'旧行业',products:'旧产品'});
  sessionStorage.setItem('zb_tab_draft_v1',prior);
  sessionStorage.setItem('zb_tab_draft_synced_v1',prior);
 }));
});

test('only marked unsynced tab fields override the server and successful save clears tab cache',async()=>{
 const writes=[];
 await run('/create',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,{cn_name:'同名品牌',industry:'服务器行业',products:'最新产品'});
  else{writes.push(req.postDataJSON());await json(r,{ok:true})}
  return true;
 },async p=>{
  await p.getByText('草稿已保存',{exact:true}).waitFor({timeout:7000});
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'本地未同步');
  assert.equal(await p.getByLabel('简要说明').inputValue(),'最新产品');
  assert.equal(writes.at(-1).products,'最新产品');
  assert.equal(await p.evaluate(()=>sessionStorage.getItem('zb_tab_draft_pending_fields_v1')),null);
  assert.equal(await p.evaluate(()=>sessionStorage.getItem('zb_tab_draft_v1')===sessionStorage.getItem('zb_tab_draft_synced_v1')),true);
 },async p=>p.addInitScript(()=>{
  sessionStorage.setItem('zb_tab_draft_v1',JSON.stringify({lang:'中英结合',cn_name:'同名品牌',industry:'本地未同步',products:'旧产品'}));
  sessionStorage.setItem('zb_tab_draft_pending_fields_v1',JSON.stringify(['industry']));
 }));
});

test('a draft PUT completed after leaving the form does not replay over a newer server edit',async()=>{
 let release;const held=new Promise(r=>release=r);let server={cn_name:'同名品牌',industry:'原行业',products:'原产品'},writes=0;
 await run('/create',async({r,path,req,json})=>{
  if(path==='/api/items'){await json(r,{items:[],bundle_price:4990});return true}
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,server);
  else{writes++;await held;server=req.postDataJSON();await json(r,{ok:true})}
  return true;
 },async p=>{try{
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor();
  await p.getByLabel('所属行业',{exact:true}).fill('本页行业');
  for(let i=0;i<60&&!writes;i++)await p.waitForTimeout(25);
  assert.equal(writes,1);
  await p.getByRole('link',{name:'费用说明'}).last().click();
  await p.waitForURL('**/pricing');
  release();
  await p.waitForFunction(()=>sessionStorage.getItem('zb_tab_draft_pending_fields_v1')===null);
  // The server PUT settling after unmount may still be clearing tab storage
  // on the next macrotask; let that reconciliation finish before reloading.
  await p.waitForTimeout(1200);
  server={...server,industry:'较新的行业'};
  await p.goBack();
  await p.reload();
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor({timeout:7000});
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'较新的行业');
  await p.waitForTimeout(1000);
  assert.equal(writes,1);
 }finally{release()}});
});

test('an older successful PUT clears only fields it saved while a newer edit is still pending',async()=>{
 let release;const held=new Promise(r=>release=r);let server={cn_name:'同名品牌',industry:'原行业',products:'原产品'},writes=0;
 await run('/create',async({r,path,req,json})=>{
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,server);
  else{writes++;if(writes===1){await held;server=req.postDataJSON();await json(r,{ok:true})}else await json(r,{error:'temporary outage'},503)}
  return true;
 },async p=>{try{
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor();
  await p.getByLabel('简要说明').fill('本页产品');
  for(let i=0;i<60&&!writes;i++)await p.waitForTimeout(25);
  assert.equal(writes,1);
  await p.getByLabel('所属行业',{exact:true}).fill('本页行业');
  release();
  await p.getByText('草稿暂未同步',{exact:true}).waitFor({timeout:10000});
  assert.deepEqual(JSON.parse(await p.evaluate(()=>sessionStorage.getItem('zb_tab_draft_pending_fields_v1'))),['industry']);
  server={...server,products:'较新的产品'};
  await p.getByRole('link',{name:'费用说明'}).last().click();
  await p.waitForURL('**/pricing');
  await p.goBack();
  await p.reload();
  await p.getByText('正在保存…',{exact:true}).waitFor({timeout:7000});
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'本页行业');
  assert.equal(await p.getByLabel('简要说明').inputValue(),'较新的产品');
 }finally{release()}});
});

test('an old form save settling during a new form GET cannot replay its stale field',async()=>{
 let releasePut,releaseRead;const putHeld=new Promise(r=>releasePut=r),readHeld=new Promise(r=>releaseRead=r);
 let server={cn_name:'同名品牌',industry:'原行业',products:'原产品'},writes=0,holdRead=false,heldReads=0;
 await run('/create',async({r,path,req,json})=>{
  if(path==='/api/items'){await json(r,{items:[],bundle_price:4990});return true}
  if(path!=='/api/draft')return false;
  if(req.method()==='GET'){const snapshot={...server};if(holdRead){heldReads++;await readHeld}await json(r,snapshot)}
  else{writes++;await putHeld;server=req.postDataJSON();await json(r,{ok:true})}
  return true;
 },async p=>{try{
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor();
  await p.getByLabel('所属行业',{exact:true}).fill('旧页行业');
  for(let i=0;i<60&&!writes;i++)await p.waitForTimeout(25);
  assert.equal(writes,1);
  await p.getByRole('link',{name:'费用说明'}).last().click();
  await p.waitForURL('**/pricing');
  await p.waitForTimeout(200);holdRead=true;
  await p.locator('a.brand-mark').waitFor({timeout:7000});
  await p.locator('a.brand-mark').evaluate(el=>el.click());
  await p.getByLabel('所属行业',{exact:true}).waitFor();
  await p.waitForTimeout(250);
  assert.equal(heldReads,0,'restore GET must wait for the prior PUT');
  holdRead=false;
  releasePut();
  await p.waitForFunction(()=>sessionStorage.getItem('zb_tab_draft_pending_fields_v1')===null,null,{timeout:7000});
  releaseRead();
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor({timeout:7000});
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'旧页行业');
  await p.getByLabel('简要说明').fill('新页产品');
  await p.getByText('草稿已保存',{exact:true}).waitFor({timeout:7000});
  assert.equal(server.industry,'旧页行业');
  assert.equal(server.products,'新页产品');
 }finally{releasePut();releaseRead()}});
});

test('new form edits read a prior mount save reconciliation before marking pending fields',async()=>{
 let releasePut;const putHeld=new Promise(r=>releasePut=r);
 let server={cn_name:'同名品牌',industry:'原行业',products:'原产品'},writes=0;
 await run('/create',async({r,path,req,json})=>{
  if(path==='/api/items'){await json(r,{items:[],bundle_price:4990});return true}
  if(path!=='/api/draft')return false;
  if(req.method()==='GET')await json(r,server);
  else{writes++;if(writes===1){await putHeld;server=req.postDataJSON();await json(r,{ok:true})}else await json(r,{error:'temporary outage'},503)}
  return true;
 },async p=>{try{
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor();
  await p.getByLabel('所属行业',{exact:true}).fill('旧页行业');
  for(let i=0;i<60&&!writes;i++)await p.waitForTimeout(25);
  assert.equal(writes,1);
  await p.getByRole('link',{name:'费用说明'}).last().click();
  await p.waitForURL('**/pricing');
  await p.locator('a.brand-mark').waitFor({timeout:7000});
  await p.locator('a.brand-mark').evaluate(el=>el.click());
  await p.getByLabel('所属行业',{exact:true}).waitFor();
  releasePut();
  await p.waitForFunction(()=>sessionStorage.getItem('zb_tab_draft_pending_fields_v1')===null,null,{timeout:7000});
  await p.getByText('已恢复上次草稿',{exact:true}).waitFor({timeout:7000});
  await p.getByLabel('简要说明').fill('新页产品');
  assert.deepEqual(JSON.parse(await p.evaluate(()=>sessionStorage.getItem('zb_tab_draft_pending_fields_v1'))),['products']);
  server={...server,industry:'新设备行业'};
  await p.getByRole('link',{name:'费用说明'}).last().click();
  await p.waitForURL('**/pricing');
  await p.goBack();await p.reload();
  await p.getByLabel('简要说明').waitFor();
  // Reload first paints the tab draft; wait for asynchronous server reconciliation.
  await p.waitForFunction(()=>document.querySelector('#brand-industry')?.value==='新设备行业',null,{timeout:7000});
  assert.equal(await p.getByLabel('所属行业',{exact:true}).inputValue(),'新设备行业');
  assert.equal(await p.getByLabel('简要说明').inputValue(),'新页产品');
 }finally{releasePut()}});
});

const zip=Buffer.from('504b0506000000000000000000000000000000000000','hex');
const downloadLink=p=>p.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true});
async function delivery({r,path,json}){
 if(path==='/api/jobs/DELIVERY'){await json(r,{status:'done',stage:'done',images:[],progress:''});return true}
 if(path==='/api/jobs/DELIVERY/detail'){await json(r,{items:[{kind:'logo_h',name:'Logo 横版组合',category:'logo',price_cents:390,purchased:true,generated:true,unlocked:true,file:'assets/examples/m-brand/logo.svg'}],bundle_price:4990});return true}
}
const shot=async(p,name)=>{const dir='../../evidence/recovery-r2-20261020/interactions';await mkdir(dir,{recursive:true});await p.screenshot({path:`${dir}/${name}.png`,fullPage:true,animations:'disabled',timeout:15000});assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)};
test('Recovery R2: closing accepted creation before its response reopens the same job and key',async()=>{
 let release;const held=new Promise(r=>release=r),posts=[];
 await run('/',async({r,path,req,json})=>{
  if(path!=='/api/jobs')return false;posts.push({key:req.headers()['idempotency-key'],body:req.postDataJSON()});if(posts.length===1)await held;
  try{await json(r,{id:'CLOSEDRECOVER'},posts.length===1?201:200)}catch{}return true;
 },async(p,ctx)=>{try{await fill(p);await submit(p).click();for(let i=0;i<40&&!posts.length;i++)await new Promise(r=>setTimeout(r,25));assert.equal(posts.length,1);await p.close();
  const next=await ctx.newPage();await next.goto(origin);await shot(next,'closed-reopen-375');await submit(next).click();await next.waitForURL('**/job/CLOSEDRECOVER');assert.equal(posts.length,2);assert.deepEqual(posts[0],posts[1]);
 }finally{release()}});
});
test('Recovery R2: offline proposal resumes on reconnect without mutation',async()=>{
 let offline=false,recovered=false,reads=0;
 await run('/job/ONLINE',async({r,path,req,json})=>{if(path!=='/api/jobs/ONLINE')return false;assert.equal(req.method(),'GET');reads++;if(offline)await r.abort('internetdisconnected');else await json(r,{status:recovered?'ready_to_choose':'running',stage:recovered?'choose':'directions',images:recovered?[{kind:'direction',idx:0,file:'assets/examples/m-brand/logo.svg'}]:[],progress:'正在准备方向'});return true},async(p,ctx)=>{
  await p.getByRole('region',{name:'制作进度'}).waitFor();offline=true;await ctx.setOffline(true);await p.clock.runFor(5000);const count=reads;offline=false;recovered=true;await ctx.setOffline(false);await p.getByRole('button',{name:'使用这个方向',exact:true}).waitFor();for(let i=0;i<30&&await p.getByRole('button',{name:'使用这个方向',exact:true}).isDisabled();i++)await new Promise(r=>setTimeout(r,50));assert.ok(reads>count);assert.equal(await p.getByRole('button',{name:'使用这个方向',exact:true}).isEnabled(),true);await shot(p,'online-recovered-375');
 },p=>p.clock.install());
});
test('Recovery R2: interrupted ZIP retries once; repeated clicks never overlap',async()=>{
 let release;const held=new Promise(r=>release=r);let downloads=0;
 await run('/job/DELIVERY',async args=>{if(await delivery(args))return true;const {r,path}=args;if(!path.endsWith('/download'))return false;downloads++;if(downloads===1){await held;try{await r.abort('connectionreset')}catch{}}else await r.fulfill({status:200,contentType:'application/zip',body:zip});return true},async p=>{try{
  await downloadLink(p).click();await p.getByRole('link',{name:'正在准备下载…',exact:true}).click({force:true});assert.equal(downloads,1);release();await p.getByRole('alert').waitFor();await shot(p,'download-interrupted-375');const result=p.waitForEvent('download');await downloadLink(p).click();const d=await result;assert.equal(await d.failure(),null);assert.equal(downloads,2);assert.equal(new URL(p.url()).pathname,'/job/DELIVERY');
 }finally{release()}});
});
test('Recovery R2: close downloading page then reopen allows clean retry',async()=>{
 let release;const held=new Promise(r=>release=r);let downloads=0;
 await run('/job/DELIVERY',async args=>{if(await delivery(args))return true;const {r,path}=args;if(!path.endsWith('/download'))return false;downloads++;if(downloads===1)await held;try{await r.fulfill({status:200,contentType:'application/zip',body:zip})}catch{}return true},async(p,ctx)=>{try{
  await downloadLink(p).click();for(let i=0;i<40&&!downloads;i++)await new Promise(r=>setTimeout(r,25));await p.close();const next=await ctx.newPage();await next.goto(origin+'/job/DELIVERY');const result=next.waitForEvent('download');await downloadLink(next).click();assert.equal(await(await result).failure(),null);assert.equal(downloads,2);
 }finally{release()}});
});
test('Recovery R2: blocked create lock has a bounded wait and no delayed surprise submission',async()=>{
 let posts=0;
 await run('/',async({path,r,json})=>{if(path!=='/api/jobs')return false;posts++;await json(r,{id:'AFTERLOCK'},201);return true},async(p,ctx)=>{
  const holder=await ctx.newPage();await holder.goto(origin);await holder.evaluate(()=>{window.hold=navigator.locks.request('0brand-create',()=>new Promise(r=>{window.releaseLock=r}));});
  await fill(p);await submit(p).click();await p.clock.runFor(21000);await p.getByRole('alert').waitFor();assert.equal(posts,0);await shot(p,'lock-timeout-375');await holder.evaluate(()=>window.releaseLock());await new Promise(r=>setTimeout(r,150));assert.equal(posts,0,'timed out lock wait must be cancelled');await submit(p).click();await p.waitForURL('**/job/AFTERLOCK');assert.equal(posts,1);
 },p=>p.clock.install());
});
test('Recovery R2: different brands in concurrent tabs keep their own brief across reload',async()=>{
 let release,started=false;const held=new Promise(r=>release=r),posts=[];
 await run('/',async({r,path,req,json})=>{
  if(path==='/api/draft'&&req.method()==='PUT'&&!started){started=true;await held;await json(r,{ok:true});return true}
  if(path==='/api/jobs'){posts.push({key:req.headers()['idempotency-key'],form:req.postDataJSON()});await json(r,{id:'BRAND'+posts.length},201);return true}
 },async(p,ctx)=>{try{
  await fill(p);await p.getByLabel('品牌名称',{exact:true}).fill('甲品牌');const b=await ctx.newPage();await b.goto(origin);await fill(b);await b.getByLabel('品牌名称',{exact:true}).fill('乙品牌');
  await submit(p).click();release();await p.waitForURL('**/job/BRAND1');await new Promise(r=>setTimeout(r,200));
  assert.equal(new URL(b.url()).pathname,'/','a different brand must not silently open another brand result');assert.equal(await b.getByLabel('品牌名称',{exact:true}).inputValue(),'乙品牌');assert.equal(posts.length,1);await b.reload();assert.equal(await b.getByLabel('品牌名称',{exact:true}).inputValue(),'乙品牌');await shot(b,'distinct-brand-retained-375');
  await b.getByRole('button',{name:'创建新的品牌提案',exact:true}).click();await submit(b).click();await b.waitForURL('**/job/BRAND2');assert.equal(posts[1].form.cn_name,'乙品牌');assert.notEqual(posts[0].key,posts[1].key);
 }finally{release()}});
});
test('Recovery R2: other-tab unknown request can be reconciled while keeping this tab brief',async()=>{
 const posts=[];
 await run('/',async({r,path,req,json})=>{if(path!=='/api/jobs')return false;posts.push({key:req.headers()['idempotency-key'],form:req.postDataJSON()});if(posts.length===1)await r.abort('connectionreset');else await json(r,{id:posts.length===2?'OTHER':'OWN'},200);return true},async(p,ctx)=>{
  await fill(p);await p.getByLabel('品牌名称',{exact:true}).fill('甲品牌');const b=await ctx.newPage();await b.goto(origin);await fill(b);await b.getByLabel('品牌名称',{exact:true}).fill('乙品牌');await submit(p).click();await p.getByRole('alert').waitFor();await p.close();await b.reload();assert.equal(await b.getByLabel('品牌名称',{exact:true}).inputValue(),'乙品牌');
  await b.getByRole('button',{name:'核对其他页面的创建结果',exact:true}).click();await b.getByRole('button',{name:'创建新的品牌提案',exact:true}).waitFor();assert.equal(await b.getByLabel('品牌名称',{exact:true}).inputValue(),'乙品牌');assert.deepEqual(posts[0],posts[1]);
  await b.getByRole('button',{name:'创建新的品牌提案',exact:true}).click();await submit(b).click();await b.waitForURL('**/job/OWN');assert.equal(posts[2].form.cn_name,'乙品牌');assert.notEqual(posts[2].key,posts[0].key);
 });
});
test('Recovery R2: hanging ZIP times out and a user retry succeeds without late download',async()=>{
 let release;const held=new Promise(r=>release=r);let downloads=0;
 await run('/job/DELIVERY',async args=>{if(await delivery(args))return true;const {r,path}=args;if(!path.endsWith('/download'))return false;downloads++;if(downloads===1)await held;try{await r.fulfill({status:200,contentType:'application/zip',body:zip})}catch{}return true},async p=>{try{
  let files=0;p.on('download',()=>files++);await downloadLink(p).click();for(let i=0;i<40&&!downloads;i++)await new Promise(r=>setTimeout(r,25));await p.clock.runFor(121000);await p.getByRole('alert').waitFor();assert.equal(files,0);const result=p.waitForEvent('download');await downloadLink(p).click();assert.equal(await(await result).failure(),null);release();await new Promise(r=>setTimeout(r,100));assert.equal(downloads,2);assert.equal(files,1);
 }finally{release()}},p=>p.clock.install());
});
test('Recovery R2: truncated successful response preserves the page and polling can recover',async()=>{
 let complete=false;
 await run('/job/TRUNCATED',async({r,path,json})=>{if(path!=='/api/jobs/TRUNCATED')return false;if(!complete)await r.fulfill({status:200,contentType:'application/json',body:'{"status":"running",'});else await json(r,{status:'ready_to_choose',stage:'choose',images:[{kind:'direction',idx:0,file:'assets/examples/m-brand/logo.svg'}],progress:''});return true},async p=>{
  await p.getByText('暂时无法刷新进度，已保留当前内容。联网后将自动重试。',{exact:true}).waitFor();complete=true;await p.clock.runFor(5000);await p.getByRole('button',{name:'使用这个方向',exact:true}).waitFor();assert.equal(await p.getByRole('button',{name:'使用这个方向',exact:true}).isEnabled(),true);await shot(p,'truncated-response-recovered-375');
 },p=>p.clock.install());
});
test('Recovery R2: truncated create response never auto-resends and manual retry keeps identity',async()=>{
 const posts=[];
 await run('/',async({r,path,req,json})=>{if(path!=='/api/jobs')return false;posts.push({key:req.headers()['idempotency-key'],form:req.postDataJSON()});if(posts.length===1)await r.fulfill({status:201,contentType:'application/json',body:'{"id":"CUT'});else await json(r,{id:'CUT'},200);return true},async p=>{
  await fill(p);await submit(p).click();await p.getByRole('alert').waitFor();assert.equal(posts.length,1);await p.reload();assert.equal(posts.length,1);await submit(p).click();await p.waitForURL('**/job/CUT');assert.deepEqual(posts[0],posts[1]);
 });
});
test('Recovery R2: same-tab route remount observes completion of its earlier request',async()=>{
 let release,started=false;const held=new Promise(r=>release=r);
 await run('/',async({r,path,json})=>{if(path!=='/api/jobs')return false;started=true;await held;await json(r,{id:'REMOUNT'},201);return true},async p=>{try{
  await fill(p);await submit(p).click();for(let i=0;i<40&&!started;i++)await new Promise(r=>setTimeout(r,25));assert.ok(started);
  // React navigation deliberately keeps the document alive while the old form unmounts.
  await p.getByRole('link',{name:'看看品牌案例',exact:true}).click();await p.getByRole('heading',{name:'作品案例',exact:true}).waitFor();await p.goBack();await submit(p).waitFor();release();await p.getByRole('button',{name:'查看已创建提案',exact:true}).waitFor();
 }finally{release()}});
});
