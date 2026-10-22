// Isolated real React tests. API responses are fixtures; all external requests are blocked.
import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {mkdir} from 'node:fs/promises';
const require=createRequire(import.meta.url),{chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..'),origin='http://127.0.0.1:4256';
let server,browser,output='';
before(async()=>{server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4256','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}assert.ok(ready,output);browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
async function run(path,handler,check,setup){
 const ctx=await browser.newContext({viewport:{width:375,height:812},serviceWorkers:'block'}),unexpected=[],errors=[];let draft={};
 const json=(r,b,status=200)=>r.fulfill({status,json:b});
 ctx.on('page',p=>{p.setDefaultTimeout(4500);p.on('pageerror',e=>errors.push(e.message))});
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

const zip=Buffer.from('504b0506000000000000000000000000000000000000','hex');
const downloadLink=p=>p.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true});
async function delivery({r,path,json}){
 if(path==='/api/jobs/DELIVERY'){await json(r,{status:'done',stage:'done',images:[],progress:''});return true}
 if(path==='/api/jobs/DELIVERY/detail'){await json(r,{items:[{kind:'logo_h',name:'Logo 横版组合',category:'logo',price_cents:390,purchased:true,generated:true,unlocked:true,file:'assets/examples/m-brand/logo.svg'}],bundle_price:9900});return true}
}
const shot=async(p,name)=>{const dir='../../evidence/payment-r3-20261020/interactions';await mkdir(dir,{recursive:true});await p.screenshot({path:`${dir}/${name}.png`,fullPage:true,animations:'disabled',timeout:15000});assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)};
const item=(purchased=false)=>({kind:'logo_h',name:'Logo 横版组合',category:'logo',price_cents:390,purchased,generated:purchased,unlocked:purchased,file:purchased?'assets/examples/m-brand/logo.svg':''});
const checkout=(status='pending')=>({id:'ORDER-R3',job_id:'PAY-A',status,amount_cents:390,item_count:1,qr:status==='pending'?'data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><text x="0" y="50">MOCK ONLY</text></svg>':undefined});
function paymentModel(){
 const m={pending:false,paid:false,reads:0,posts:[],offline:false};
 m.handle=async({r,path,req,json})=>{
  if(path==='/api/jobs/PAY-A'){m.reads++;await json(r,{status:'done',stage:'done',images:[],progress:''});return true}
  if(path==='/api/jobs/PAY-A/detail'){await json(r,{items:[item(m.paid)],bundle_price:9900,pending_order_id:m.pending&&!m.paid?'ORDER-R3':''});return true}
  if(path==='/api/pay/create'){m.posts.push(req.postDataJSON());m.pending=true;await json(r,checkout(),201);return true}
  if(path==='/api/pay/checkout/ORDER-R3'){await json(r,checkout(m.paid?'paid':'pending'));return true}
  if(path==='/api/pay/status/ORDER-R3'){await json(r,{id:'ORDER-R3',status:m.paid?'paid':'pending'});return true}
 };
 return m;
}
const selectPay=async p=>{await p.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).check();await p.getByRole('button',{name:'支付 ¥3.90',exact:true}).click()};
test('Payment R3: accepted payment with lost response is read back without another POST',async()=>{
 const m=paymentModel();
 await run('/job/PAY-A',async args=>{
  if(args.path==='/api/pay/create'){m.pending=true;m.posts.push(args.req.postDataJSON());await args.r.abort('connectionreset');return true}
  return m.handle(args);
 },async p=>{
  await selectPay(p);await p.getByRole('alert').waitFor();assert.match(await p.getByRole('alert').innerText(),/正在核对原订单/);
  await shot(p,'payment-lost-response');
  await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).waitFor();
  assert.equal(m.posts.length,1);assert.ok(m.reads>1);assert.equal(await p.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).isDisabled(),true);
 });
});
for(const reopen of [false,true])test(`Payment R3: late payment persistence is discovered without resubmission (reopen=${reopen})`,async()=>{
 const m=paymentModel();let committed=false;
 await run('/job/PAY-A',async args=>{
  if(args.path==='/api/pay/create'){m.posts.push(args.req.postDataJSON());await args.r.abort('connectionreset');return true}
  if(committed)m.pending=true;return m.handle(args);
 },async(p,ctx)=>{
  await selectPay(p);await p.getByRole('alert').waitFor();await new Promise(r=>setTimeout(r,150));assert.equal(m.pending,false);
  let page=p;if(reopen){await p.close();page=await ctx.newPage();await page.goto(origin+'/job/PAY-A');await page.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).waitFor()}
  // Commit after the first refresh has already seen done + no pending order.
  committed=true;
  await page.getByRole('button',{name:'继续支付 / 查进度',exact:true}).waitFor({timeout:7500});
  assert.equal(m.posts.length,1);assert.ok(m.reads>=3);await shot(page,'late-payment-'+reopen);
 });
});
test('Payment R3: navigating directly between proposals during a request cannot leave the next one busy',async()=>{
 let release;const held=new Promise(r=>release=r),posts=[];
 await run('/job/PAY-A',async({r,path,req,json})=>{
  if(path==='/api/pay/create'){posts.push(req.postDataJSON());if(posts.length===1)await held;try{await json(r,{...checkout(),job_id:req.postDataJSON().job_id},201)}catch{}return true}
  if(/^\/api\/jobs\/PAY-[AB]$/.test(path)){await json(r,{status:'done',stage:'done',images:[],progress:''});return true}
  if(/^\/api\/jobs\/PAY-[AB]\/detail$/.test(path)){await json(r,{items:[item()],bundle_price:9900});return true}
  if(path.startsWith('/api/pay/status/')){await json(r,{status:'pending'});return true}
 },async p=>{try{
  await selectPay(p);await p.getByRole('button',{name:'处理中…',exact:true}).waitFor();
  // Browser history can navigate to a different id without remounting the route.
  await p.evaluate(()=>{history.pushState({},'', '/job/PAY-B');dispatchEvent(new PopStateEvent('popstate'))});
  await p.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).check();release();
  await shot(p,'payment-switch-brand');
  await p.getByRole('button',{name:'支付 ¥3.90',exact:true}).waitFor();
  assert.equal(await p.getByRole('button',{name:'支付 ¥3.90',exact:true}).isEnabled(),true);
  assert.equal(await p.getByRole('dialog').count(),0,'late order from A must not open over B');
  await p.getByRole('button',{name:'支付 ¥3.90',exact:true}).click();
  assert.deepEqual(posts.map(p=>p.job_id),['PAY-A','PAY-B']);
 }finally{release()}});
});
test('Payment R3: a late action from another brand cannot unlock the current payment',async()=>{
 let releaseA,releaseB;const heldA=new Promise(r=>releaseA=r),heldB=new Promise(r=>releaseB=r),posts=[];
 await run('/job/PAY-A',async({r,path,req,json})=>{
  if(path==='/api/pay/create'){const body=req.postDataJSON();posts.push(body);await(body.job_id==='PAY-A'?heldA:heldB);try{await json(r,{...checkout(),job_id:body.job_id},201)}catch{}return true}
  if(/^\/api\/jobs\/PAY-[AB]$/.test(path)){await json(r,{status:'done',stage:'done',images:[],progress:''});return true}
  if(/^\/api\/jobs\/PAY-[AB]\/detail$/.test(path)){await json(r,{items:[item()],bundle_price:9900});return true}
  if(path.startsWith('/api/pay/status/')){await json(r,{status:'pending'});return true}
 },async p=>{try{
  await selectPay(p);await p.evaluate(()=>{history.pushState({},'', '/job/PAY-B');dispatchEvent(new PopStateEvent('popstate'))});
  await selectPay(p);releaseA();await new Promise(r=>setTimeout(r,150));
  assert.equal(await p.getByRole('button',{name:'处理中…',exact:true}).isDisabled(),true);
  assert.equal(await p.getByRole('dialog').count(),0);assert.equal(posts.length,2);
  releaseB();await p.getByRole('dialog',{name:'微信支付',exact:true}).waitFor();assert.deepEqual(posts.map(p=>p.job_id),['PAY-A','PAY-B']);
 }finally{releaseA();releaseB()}});
});
test('Payment R3: two tabs recover after a day offline using reads and receive the same paid rights',async()=>{
 const m=paymentModel();m.pending=true;
 await run('/job/PAY-A',m.handle,async(p,ctx)=>{
  await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).waitFor();
  const b=await ctx.newPage();await b.clock.install();await b.goto(origin+'/job/PAY-A');await b.getByRole('button',{name:'继续支付 / 查进度',exact:true}).waitFor();
  await ctx.setOffline(true);await Promise.all([p.clock.fastForward(86400000),b.clock.fastForward(86400000)]);m.paid=true;await ctx.setOffline(false);
  for(const tab of [p,b]){await tab.getByRole('link',{name:/下载已完成的 \d+ 项/,exact:true}).waitFor();assert.equal(await tab.getByRole('button',{name:'继续支付 / 查进度',exact:true}).count(),0)}
  assert.equal(m.posts.length,0);await shot(p,'paid-after-day-offline');
 },p=>p.clock.install());
});
test('Payment R3: expired QR recovery waits for confirmed close without creating a replacement payment',async()=>{
 const m=paymentModel();m.pending=true;let closed=false,resumes=0;
 await run('/job/PAY-A',async args=>{
  const {r,path,json}=args;
  if(path==='/api/pay/checkout/ORDER-R3'){await json(r,{...checkout(closed?'closed':'pending'),qr:undefined,message:'原二维码已到期，请核对并恢复支付入口。'});return true}
  if(path==='/api/pay/status/ORDER-R3'){await json(r,{id:'ORDER-R3',status:closed?'closed':'pending'});return true}
  if(path==='/api/pay/orders/ORDER-R3/resume'){resumes++;closed=true;m.pending=false;await json(r,checkout('closed'));return true}
  return m.handle(args);
 },async p=>{
  await p.getByRole('button',{name:'继续支付 / 查进度',exact:true}).click();
  assert.equal(await p.getByAltText('微信支付二维码').count(),0);
  assert.equal(await p.getByText(/微信内可长按二维码/).count(),0,'expired payment must not instruct scanning a missing QR');
  await shot(p,'expired-payment-recovery');
  await p.getByRole('button',{name:'核对并恢复支付入口',exact:true}).click();
  await p.getByText('此订单已关闭或支付失败，可重新选择项目。',{exact:true}).waitFor({timeout:7000});
  assert.equal(await p.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).isEnabled(),true);
  assert.equal(resumes,1);assert.equal(m.posts.length,0);await shot(p,'expired-payment-confirmed-closed');
 });
});
test('Payment R3: three different brands concurrently retain all briefs and use distinct creation identities',async()=>{
 const posts=[],seen=new Map();let release;const held=new Promise(r=>release=r);
 await run('/',async({path,r,req,json})=>{
  if(path!=='/api/jobs')return false;
  const body=req.postDataJSON(),key=req.headers()['idempotency-key'];posts.push({key,body});
  if(!seen.has(key))seen.set(key,'THREE'+seen.size);if(posts.length===1)await held;
  try{await json(r,{id:seen.get(key)},201)}catch{}return true;
 },async(p,ctx)=>{try{
  const tabs=[p,await ctx.newPage(),await ctx.newPage()];
  for(let i=0;i<tabs.length;i++){if(i)await tabs[i].goto(origin);await fill(tabs[i]);await tabs[i].getByLabel('品牌名称',{exact:true}).fill('品牌'+i);await tabs[i].getByRole('textbox',{name:'简要说明',exact:false}).fill('资料'+i)}
  await Promise.all(tabs.map(tab=>submit(tab).click()));release();
  for(let i=0;i<80&&posts.length<1;i++)await new Promise(r=>setTimeout(r,25));
  await new Promise(r=>setTimeout(r,150));
  const first=posts[0].body.cn_name;assert.equal(seen.size,1);
  for(let i=0;i<tabs.length;i++){
   const tab=tabs[i];if('品牌'+i===first)continue;
   await tab.reload();assert.equal(await tab.getByLabel('品牌名称',{exact:true}).inputValue(),'品牌'+i);
   assert.equal(await tab.getByRole('textbox',{name:'简要说明',exact:false}).inputValue(),'资料'+i);
   await tab.getByRole('button',{name:'创建新的品牌提案',exact:true}).click();await submit(tab).click();await tab.waitForURL('**/job/THREE*');
  }
  assert.equal(seen.size,3);assert.equal(posts.length,3);assert.equal(new Set(posts.map(p=>p.body.cn_name)).size,3);
  for(const post of posts)assert.equal(post.body.products,'资料'+post.body.cn_name.slice(-1));
 }finally{release()}});
});
test('Payment R3: six consecutive download failures remain retryable then deliver exactly one ZIP',async()=>{
 let attempts=0,files=0;
 await run('/job/DELIVERY',async args=>{
  if(await delivery(args))return true;if(!args.path.endsWith('/download'))return false;
  attempts++;if(attempts<=6){if(attempts%2)await args.r.abort('connectionreset');else await args.json(args.r,{error:'temporarily unavailable'},503)}
  else await args.r.fulfill({status:200,contentType:'application/zip',body:zip});return true;
 },async p=>{
  p.on('download',()=>files++);
  for(let i=0;i<6;i++){await downloadLink(p).click();await p.getByRole('alert').waitFor();assert.equal(attempts,i+1);assert.equal(await downloadLink(p).getAttribute('aria-busy'),'false')}
  await shot(p,'download-six-failures');const event=p.waitForEvent('download');await downloadLink(p).click();assert.equal(await(await event).failure(),null);
  assert.equal(attempts,7);assert.equal(files,1);assert.equal(await p.getByRole('alert').count(),0);
 });
});
