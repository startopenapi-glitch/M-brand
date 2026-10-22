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
const origin='http://127.0.0.1:4291';let server,browser,output='';
before(async()=>{
 server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4291','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});
 server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);
 let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}assert.ok(ready,output);
 browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});
});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});

async function run(check){const ctx=await browser.newContext({viewport:{width:375,height:812},serviceWorkers:'block'});let failSession=false,reads=0,posts=0;const unknown=[],errors=[];
 await ctx.route('**/*',async r=>{const u=new URL(r.request().url()),p=u.pathname,json=(b,status=200)=>r.fulfill({json:b,status});if(u.origin!==origin){unknown.push(u.origin);return r.abort()}if(!p.startsWith('/api/'))return r.continue();if(r.request().method()!=='GET')posts++;
 if(p==='/api/admin/session'){reads++;return json(failSession?{error:'unavailable'}:{name:'test',capabilities:['overview','orders','jobs','support','codes','cases','agents','usage','audit']},failSession?503:200)}
 if(p==='/api/admin/orders/page')return json({items:[{id:'OLDER-ORDER',amount_cents:390,status:'paid',mock:false,credited:true,channel:'native',created_at:'2026-01-01 12:00:00',paid_at:'2026-01-01 12:00:00',job_id:'TEST-JOB'}],total:310,page:Number(u.searchParams.get('page')||1),page_size:50,summary:{revenue_cents:120900,paid:310,pending:0}});
 if(['/api/admin/support','/api/admin/jobs','/api/admin/codes','/api/admin/usage','/api/admin/audit','/api/admin/agents','/api/admin/agent-payouts','/api/cases'].includes(p))return json([]);
 if(p==='/api/admin/agents/stats')return json({agent_count:0,total_commission_cents:0,total_paid_cents:0,total_pending_cents:0});
 if(p==='/api/admin/stats')return json({jobs_failed:2,worker:{ready:true,queued:1,running:0}});
 if(p==='/api/admin/agents/reconcile')return json({anomalies:[]});
 unknown.push(p);return json({error:'unknown'},500)});
 const p=await ctx.newPage();p.setDefaultTimeout(5000);p.on('pageerror',e=>errors.push(e.message));
 try{await check(p,()=>failSession=true,()=>failSession=false);assert.equal(posts,0);assert.deepEqual(unknown,[]);assert.deepEqual(errors,[])}finally{await ctx.close()}}
test('admin 503 session stays recoverable instead of login',async()=>run(async(p,fail,recover)=>{fail();await p.goto(origin+'/admin');await p.getByRole('button',{name:'重试连接',exact:true}).waitFor();assert.equal(await p.getByLabel('管理员账号',{exact:true}).count(),0);recover();await p.getByRole('button',{name:'重试连接',exact:true}).click();await p.getByRole('heading',{name:'运营工作台',exact:true}).waitFor()}));
test('admin orders server pagination, URL refresh, safe phone layout',async()=>run(async p=>{await p.goto(origin+'/admin/orders?q=older&page=2');await p.getByText('OLDER-ORDER',{exact:true}).waitFor();await p.getByText('筛选结果共 310 笔',{exact:false}).waitFor();assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await p.reload();await p.getByText('OLDER-ORDER',{exact:true}).waitFor();assert.match(p.url(),/page=2/);await p.getByRole('button',{name:'下一页',exact:true}).click();assert.match(p.url(),/page=3/)}));
test('admin support phone has no page overflow',async()=>run(async p=>{await p.goto(origin+'/admin/support');await p.getByRole('heading',{name:'售后工单',exact:true}).waitFor();assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}));

test('admin authentication distinguishes 401 and denied 403',async()=>run(async p=>{
 let status=401;await p.context().route('**/api/admin/session',r=>r.fulfill({status,json:{error:status===401?'请先登录':'无权限'}}));
 await p.goto(origin+'/admin');await p.getByLabel('管理员账号',{exact:true}).waitFor();
 status=403;await p.reload();await p.getByText('当前账号无后台访问权限。',{exact:true}).waitFor();assert.equal(await p.getByLabel('管理员账号',{exact:true}).count(),0);
}));
test('admin dashboard wake storms never overlap held stats reads',async()=>run(async p=>{
 let calls=0,release;const held=new Promise(r=>release=r);
 await p.context().route('**/api/admin/stats',async r=>{calls++;await held;await r.fulfill({json:{jobs_failed:0}}).catch(()=>{})});
 try{await p.clock.install();await p.goto(origin+'/admin');for(let i=0;i<100&&!calls;i++)await new Promise(r=>setTimeout(r,20));const baseline=calls;assert.ok(baseline>=1&&baseline<=2);await p.clock.runFor(8000);await p.evaluate(()=>{for(let i=0;i<20;i++){document.dispatchEvent(new Event('visibilitychange'));dispatchEvent(new Event('online'))}});assert.equal(calls,baseline);release();await p.getByRole('heading',{name:'运营工作台',exact:true}).waitFor()}finally{release()}
}));
test('admin support double submit emits one versioned command',async()=>run(async p=>{
 let posts=0,release;const held=new Promise(r=>release=r);
 await p.context().route('**/api/admin/support',r=>r.fulfill({json:[{id:'S1',job_id:'J1',order_id:'O1',category:'payment',description:'test',status:'open',resolution:'',created_at:'2026-10-22',version:0}]}));
 await p.context().route('**/api/admin/support/S1',async r=>{posts++;assert.equal(r.request().postDataJSON().version,0);await held;await r.fulfill({json:{ok:true}}).catch(()=>{})});
 try{await p.goto(origin+'/admin/support');await p.getByText('S1',{exact:true}).click();await p.getByRole('button',{name:'标记已解决',exact:true}).evaluate(b=>{b.click();b.click()});for(let i=0;i<100&&!posts;i++)await new Promise(r=>setTimeout(r,20));assert.equal(posts,1);assert.equal(await p.getByRole('button',{name:'标记已解决',exact:true}).isDisabled(),true)}finally{release()}
}));

for(const width of [375,768,1440])test(`all eight admin resources ${width}px have no errors or page overflow`,async()=>run(async p=>{
 await p.setViewportSize({width,height:900});
 for(const [path,title] of [['','运营工作台'],['/orders','订单'],['/jobs','任务监控'],['/support','售后工单'],['/codes','邀请码管理'],['/agents','代理管理'],['/usage','用量报表（近 30 天）'],['/audit','审计日志（近 100 条）']]){
  await p.goto(origin+'/admin'+path);await p.getByRole('heading',{name:title,exact:true}).waitFor();
  assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${path} overflow`);
 }
}));
test('orders late old filter response never overwrites new result',async()=>run(async p=>{
 let release,seen=false;const held=new Promise(r=>release=r);
 await p.context().route('**/api/admin/orders/page?**',async r=>{const old=new URL(r.request().url()).searchParams.get('q')==='old';if(old){seen=true;await held}await r.fulfill({json:{items:[{id:old?'OLD':'NEW',amount_cents:390,status:'paid',mock:false,created_at:'2026-10-22',paid_at:'',visitor:'',code:'',job_id:''}],total:1,page:1,page_size:50,summary:{revenue_cents:390,paid:1,pending:0}}}).catch(()=>{})});
 try{await p.goto(origin+'/admin/orders?q=old');await p.getByRole('heading',{name:'订单',exact:true}).waitFor();for(let i=0;i<100&&!seen;i++)await new Promise(r=>setTimeout(r,20));assert.ok(seen);await p.getByLabel('搜索订单',{exact:true}).fill('new');await p.getByText('NEW',{exact:true}).waitFor();release();await new Promise(r=>setTimeout(r,200));assert.equal(await p.getByText('OLD',{exact:true}).count(),0)}finally{release()}
}));

test('restored admin retains operations and hides cases including old deep link',async()=>run(async p=>{
 await p.goto(origin+'/admin');await p.getByRole('heading',{name:'运营工作台',exact:true}).waitFor();
 await p.getByRole('button',{name:/后台导航/}).click();
 const nav=p.getByRole('navigation',{name:'管理导航'});
 for(const name of ['订单','任务','售后','邀请码','代理','用量','审计'])assert.equal(await nav.getByRole('link',{name,exact:true}).count(),1);
 assert.equal(await nav.getByRole('link',{name:'案例',exact:true}).count(),0);
 await p.goto(origin+'/admin/cases');await p.getByRole('heading',{name:'运营工作台',exact:true}).waitFor();assert.equal(new URL(p.url()).pathname,'/admin');
}));
