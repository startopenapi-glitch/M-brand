// Isolated real React tests. API responses are fixtures; all external requests are blocked.
import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {mkdir} from 'node:fs/promises';
const require=createRequire(import.meta.url),{chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..'),origin='http://127.0.0.1:4253';
let server,browser,output='';
before(async()=>{server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4253','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}assert.ok(ready,output);browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
async function run(path,handler,check,setup){
 const ctx=await browser.newContext({viewport:{width:375,height:812},serviceWorkers:'block'}),unexpected=[],errors=[];let draft={};
 const json=(r,b,status=200)=>r.fulfill({status,json:b});
 ctx.on('page',p=>{p.setDefaultTimeout(2200);p.on('pageerror',e=>errors.push(e.message))});
 await ctx.route('**/*',async r=>{const req=r.request(),u=new URL(req.url());if(u.origin!==origin){unexpected.push(u.origin);return r.abort()}
  if(!u.pathname.startsWith('/api/'))return r.continue();
  if(await handler({r,path:u.pathname,req,json,ctx}))return;
  if(u.pathname==='/api/draft'){if(req.method()==='PUT')draft=req.postDataJSON();return json(r,req.method()==='GET'?draft:{ok:true})}
  if(u.pathname.startsWith('/api/jobs/')&&u.pathname.endsWith('/detail'))return json(r,{items:[],bundle_price:9900});
  if(u.pathname.startsWith('/api/jobs/')&&req.method()==='GET')return json(r,{status:'running',stage:'directions',images:[],progress:'准备方向'});
  if(['/api/my/jobs','/api/my/orders','/api/cases'].includes(u.pathname))return json(r,[]);
  unexpected.push(req.method()+' '+u.pathname);return json(r,{error:'Unexpected fixture'},500);
 });
 const page=await ctx.newPage();try{if(setup)await setup(page);await page.goto(origin+path);await check(page,ctx);assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])}finally{await ctx.close()}
}
async function fill(p){await p.getByLabel('品牌名称',{exact:true}).fill('回归品牌');await p.getByLabel('所属行业',{exact:true}).fill('电商');}
const submit=p=>p.getByRole('button',{name:/(9\.9 元解锁自己的品牌|免费生成 3 个方向|继续核对创建结果)/});
test('Create: lost response and reload reuse the original key and form',async()=>{
 const posts=[];
 await run('/',async({r,path,req,json})=>{if(path!=='/api/jobs')return false;posts.push({key:req.headers()['idempotency-key'],body:req.postDataJSON()});if(posts.length===1)await r.abort('failed');else await json(r,{id:'RECOVERED'},200);return true},async p=>{
  await fill(p);await submit(p).click();await p.getByRole('alert').waitFor();await p.reload();await submit(p).click();await p.waitForURL('**/job/RECOVERED');assert.ok(posts[0].key&&posts[0].key.length>=16,'creation needs a persisted request key');assert.equal(posts[1].key,posts[0].key);assert.deepEqual(posts[1].body,posts[0].body);
 });
});
test('Create: two tabs sharing one draft submit one accepted creation',async()=>{
 let release;const held=new Promise(r=>release=r),posts=[];
 await run('/',async({r,path,req,json})=>{if(path!=='/api/jobs')return false;posts.push(req.headers()['idempotency-key']);await held;await json(r,{id:'SHARED'},201);return true},async(p,ctx)=>{
  try{await fill(p);const second=await ctx.newPage();await second.goto(origin);await fill(second);await submit(p).click();await submit(second).click();await new Promise(r=>setTimeout(r,180));release();await p.waitForURL('**/job/SHARED');await second.waitForURL('**/job/SHARED');assert.equal(posts.length,1);}finally{release()}
 });
});
test('Create: completed intent remains recoverable; explicit new brand gets a new key',async()=>{
 const keys=[];
 await run('/',async({r,path,req,json})=>{if(path!=='/api/jobs')return false;keys.push(req.headers()['idempotency-key']);await json(r,{id:'NEW'+keys.length},201);return true},async p=>{
  await fill(p);await submit(p).click();await p.waitForURL('**/job/NEW1');await p.goto(origin);await p.getByRole('button',{name:'查看已创建提案',exact:true}).waitFor();await p.getByRole('button',{name:'创建新的品牌提案',exact:true}).click();await submit(p).click();await p.waitForURL('**/job/NEW2');assert.ok(keys.every(Boolean));assert.notEqual(keys[0],keys[1]);
 });
});
test('Proposal: hanging read times out and polling recovers without a generation',async()=>{
 let reads=0,release,recover=false;const held=new Promise(r=>release=r);
 await run('/job/WAIT',async({r,path,json})=>{if(path!=='/api/jobs/WAIT')return false;reads++;if(!recover)await held;try{await json(r,{status:'ready_to_choose',stage:'choose',images:[{kind:'direction',idx:0,file:'assets/examples/m-brand/logo.svg'}],progress:''})}catch{}return true},async p=>{
  try{for(let i=0;i<30&&reads===0;i++)await new Promise(r=>setTimeout(r,50));assert.ok(reads>0);await new Promise(r=>setTimeout(r,250));await p.clock.runFor(21000);recover=true;await p.clock.runFor(5000);await p.getByRole('heading',{name:'选择 Logo 方向',exact:true}).waitFor();assert.ok(reads>=2,'hanging read must not block all polls');}finally{release()}
 },p=>p.clock.install());
});
test('Download: failed ZIP keeps proposal visible and retry succeeds',async()=>{
 let downloads=0;
 await run('/job/DELIVERY',async({r,path,json})=>{
  if(path==='/api/jobs/DELIVERY'){await json(r,{status:'done',stage:'done',images:[],progress:''});return true}
  if(path==='/api/jobs/DELIVERY/detail'){await json(r,{items:[{kind:'logo_h',name:'Logo 横版组合',category:'logo',price_cents:390,purchased:true,generated:true,unlocked:true,file:'assets/examples/m-brand/logo.svg'}],bundle_price:9900});return true}
  if(path==='/api/jobs/DELIVERY/download'){downloads++;if(downloads===1)await json(r,{error:'稍后重试'},503);else await r.fulfill({status:200,contentType:'application/zip',headers:{'Content-Disposition':'attachment; filename="0brand-test.zip"'},body:Buffer.from('504b0506000000000000000000000000000000000000','hex')});return true}
 },async p=>{
  await p.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true}).click();await p.getByRole('alert').filter({hasText:/下载/}).waitFor();assert.equal(new URL(p.url()).pathname,'/job/DELIVERY');
  await mkdir('../../evidence/flow-review/errors',{recursive:true});await p.screenshot({path:'../../evidence/flow-review/errors/download-375.png',fullPage:true});
  const event=p.waitForEvent('download');await p.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true}).click();const dl=await event;assert.equal(await dl.failure(),null);assert.equal(downloads,2);assert.equal(new URL(p.url()).pathname,'/job/DELIVERY');
 });
});
test('Create: draft validation failure before POST keeps the brief editable',async()=>{
 let puts=0,posts=0;
 await run('/',async({r,path,req,json})=>{
  if(path==='/api/draft'&&req.method()==='PUT'){puts++;await json(r,puts===1?{error:'资料内容过长，请缩短后重试'}:{ok:true},puts===1?413:200);return true}
  if(path==='/api/jobs'){posts++;await json(r,{id:'EDITED'},201);return true}
 },async p=>{
  await fill(p);await submit(p).click();await p.getByRole('alert').waitFor();assert.equal(posts,0);
  assert.equal(await p.getByLabel('品牌名称',{exact:true}).isEnabled(),true,'pre-submit draft error must not trap the form');
  await p.getByLabel('品牌名称',{exact:true}).fill('改正后的品牌');await submit(p).click();await p.waitForURL('**/job/EDITED');assert.equal(posts,1);
 });
});
test('Create: confirmed transfer permits explicit new intention, never automatic resubmission',async()=>{
 let posts=0;
 await run('/',async({r,path,json})=>{
  if(path!=='/api/jobs')return false;posts++;
  if(posts===1)await json(r,{error:'此提案已在其他设备找回，请从我的提案核对',code:'creation_transferred'},409);
  else await json(r,{id:'AFTERTRANSFER'},201);
  return true;
 },async p=>{
  await fill(p);await submit(p).click();await p.getByRole('alert').waitFor();await p.reload();assert.equal(posts,1);
  await p.getByRole('button',{name:'创建新的品牌提案',exact:true}).click();await submit(p).click();await p.waitForURL('**/job/AFTERTRANSFER');assert.equal(posts,2);
 });
});
test('Create: late autosave from another anonymous tab cannot replace the new job owner',async()=>{
 let release,started=false,anonymous=0,owner='',reads=[];const held=new Promise(r=>release=r);
 await run('/',async({r,path,req,json})=>{
  if(path==='/api/draft'&&req.method()==='PUT'){
   if(!(req.headers()['cookie']||'').includes('zb_visitor=')){
    anonymous++;const n=anonymous;if(n===1){started=true;await held;}
    await r.fulfill({json:{ok:true},headers:{'Set-Cookie':`zb_visitor=owner-${n}; Path=/; HttpOnly; SameSite=Lax`}});
   }else await json(r,{ok:true});return true;
  }
  if(path==='/api/jobs'){owner=req.headers()['cookie']||'';await new Promise(r=>setTimeout(r,150));await json(r,{id:'OWNED'},201);return true}
  if(path.startsWith('/api/jobs/OWNED')){const accessible=(req.headers()['cookie']||'')===owner;reads.push(accessible);await json(r,path.endsWith('/detail')?{items:[],bundle_price:9900}:{status:'running',stage:'directions',images:[]},accessible?200:404);return true}
 },async(p,ctx)=>{
  try{const other=await ctx.newPage();await other.goto(origin);await fill(other);for(let i=0;i<40&&!started;i++)await new Promise(r=>setTimeout(r,50));assert.ok(started);
   await fill(p);await submit(p).click();await new Promise(r=>setTimeout(r,200));release();await p.waitForURL('**/job/OWNED');
   for(let i=0;i<30&&!reads.length;i++)await new Promise(r=>setTimeout(r,50));assert.ok(reads.length&&reads.every(Boolean));assert.equal(anonymous,1);
  }finally{release()}
 });
});
