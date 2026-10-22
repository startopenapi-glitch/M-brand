import assert from 'node:assert/strict';
import { before, after, test } from 'node:test';
import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { fixtures } from '../verification/ui-fixtures.mjs';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const origin='http://127.0.0.1:4237';
let server,browser,output='';
before(async()=>{
 server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4237','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});
 server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);
 for(let i=0;i<80;i++){if(server.exitCode!==null)throw new Error(output);try{if((await fetch(origin)).ok)break}catch{}await new Promise(r=>setTimeout(r,100))}
 browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});
});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
async function run(path,handler,check){
 const context=await browser.newContext({serviceWorkers:'block'});
 const page=await context.newPage();page.setDefaultTimeout(1800);
 const unexpected=[],errors=[];page.on('pageerror',e=>errors.push(e.message));
 const json=(r,b,status=200)=>r.fulfill({status,contentType:'application/json',body:JSON.stringify(b)});
 await context.route('**/*',async r=>{
  const u=new URL(r.request().url());
  if(u.origin!==origin){unexpected.push(u.origin);return r.abort()}
  if(!u.pathname.startsWith('/api/'))return r.continue();
  if(await handler({r,path:u.pathname,request:r.request(),json}))return;
  if(u.pathname==='/api/draft')return json(r,r.request().method()==='GET'?{}:{ok:true});
  if(u.pathname==='/api/my/jobs'||u.pathname==='/api/my/orders'||u.pathname==='/api/cases')return json(r,[]);
  if(u.pathname==='/api/jobs/NEW')return json(r,{status:'running',stage:'directions',images:[],progress:'准备方向'});
  if(u.pathname==='/api/jobs/NEW/detail')return json(r,{items:[],bundle_price:9900});
  unexpected.push(r.request().method+' '+u.pathname);return json(r,{error:'Unexpected fixture'},500);
 });
 try{await page.goto(origin+path);await check(page,context);assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])}finally{await context.close()}
}
test('New brief: homepage submits minimum fields once without a second wizard and retains referral',async()=>{
 const posts=[];let release;const wait=new Promise(r=>release=r);
 await run('/?ref=PARTNER01',async({r,path,request,json})=>{
  if(path==='/api/jobs'){posts.push(JSON.parse(request.postData()));await wait;await json(r,{id:'NEW'},201);return true}
 },async(page,context)=>{
  await page.getByLabel('品牌名称',{exact:true}).fill('山物');
  await page.getByLabel('所属行业',{exact:true}).fill('茶饮');
  assert.equal(await page.getByLabel('英文品牌名',{exact:true}).isVisible(),false);
  const button=page.getByRole('button',{name:/9\.9 元解锁自己的品牌|免费生成 3 个方向/,exact:false});
  await button.click();await page.getByRole('button',{name:'提交中…'}).waitFor();
  await page.getByRole('form',{name:'品牌资料'}).evaluate(form=>form.requestSubmit());release();
  await page.waitForURL('**/job/NEW');
  assert.equal(posts.length,1);assert.equal(posts[0].cn_name,'山物');assert.equal(posts[0].industry,'茶饮');
  assert.equal((await context.cookies()).find(c=>c.name==='zb_ref')?.value,'PARTNER01');
 });
});
test('New brief: optional preferences are available without becoming extra required steps',async()=>{
 await run('/create?cn_name=山物&industry=茶饮',async()=>false,async page=>{
  assert.equal(await page.getByLabel('品牌名称',{exact:true}).inputValue(),'山物');
  await page.getByRole('button',{name:'更多设计偏好'}).click();
  await page.getByLabel('英文品牌名',{exact:true}).fill('SHANWU');
  await page.getByLabel('不希望出现的元素',{exact:true}).fill('写实树叶');
  assert.equal(await page.getByRole('button',{name:'下一步',exact:true}).count(),0);
  assert.ok(await page.getByRole('button',{name:/9\.9 元解锁自己的品牌|免费生成 3 个方向/}).isEnabled());
 });
});
test('New brief: validation prevents whitespace-only input without sending a generation',async()=>{
 const posts=[];
 await run('/',async({path,request})=>{if(path==='/api/jobs')posts.push(request.postData());return false},async page=>{
  await page.getByLabel('品牌名称',{exact:true}).fill('   ');
  await page.getByLabel('所属行业',{exact:true}).fill('茶饮');
  await page.getByRole('button',{name:/9\.9 元解锁自己的品牌|免费生成 3 个方向/}).click();
  await page.getByRole('alert').filter({hasText:'请填写品牌名称和所属行业'}).waitFor();
  assert.equal(posts.length,0);
 });
});
test('Orders: one selected tab and recover dialog keep secondary operations out of the initial view',async()=>{
 let recoveries=0;
 await run('/orders',async({r,path,json})=>{
  if(path==='/api/recover'){recoveries++;await json(r,{job_id:'NEW'});return true}
 },async page=>{
  await page.getByRole('tab',{name:'提案',exact:true}).waitFor();
  assert.equal(await page.getByRole('tab',{selected:true}).count(),1);
  assert.equal(await page.getByLabel('订单号或邀请码').count(),0);
  await page.getByRole('button',{name:'换设备找回'}).click();
  await page.getByRole('dialog',{name:'找回已购内容'}).waitFor();
  await page.getByLabel('订单号或邀请码').fill('ORDER-TEST');
  await page.getByRole('button',{name:'找回',exact:true}).click();
  await page.getByText('已找回品牌提案及已购交付项，已绑定到本设备',{exact:true}).waitFor();
  assert.equal(recoveries,1);await page.keyboard.press('Escape');
  await page.waitForFunction(()=>document.activeElement===document.querySelector('.recover-link'),null,{timeout:1200});
  await page.getByRole('tab',{name:'订单',exact:true}).click();
  await page.getByRole('tab',{name:'订单',selected:true}).waitFor();assert.equal(await page.getByRole('tab',{selected:true}).count(),1);
 });
});
test('Mobile navigation closes when selecting the current page',async()=>{
 await run('/',async()=>false,async page=>{
  await page.setViewportSize({width:375,height:812});
  await page.getByRole('button',{name:'打开导航'}).click();
  await page.getByRole('navigation',{name:'手机导航'}).getByRole('link',{name:'创建品牌'}).click();
  await page.getByRole('dialog',{name:'导航',exact:true}).waitFor({state:'hidden'});
 });
});
test('Draft writes stay ordered across leaving and returning to creation',async()=>{
 let serverDraft={},firstStarted=false,finished=0,release;
 const held=new Promise(resolve=>release=resolve);
 await run('/',async({r,path,request,json})=>{
  if(path!=='/api/draft')return false;
  if(request.method()==='PUT'){
   const body=request.postDataJSON();
   if(!firstStarted){firstStarted=true;await held}
   serverDraft=body;await json(r,{ok:true});finished++;
  }else await json(r,serverDraft);
  return true;
 },async page=>{
  try{
   await page.getByLabel('品牌名称',{exact:true}).fill('旧输入');
   await page.getByLabel('所属行业',{exact:true}).fill('茶饮');
   for(let i=0;i<30&&!firstStarted;i++)await page.waitForTimeout(100);
   assert.equal(firstStarted,true);
   await page.getByRole('navigation',{name:'主要导航'}).getByRole('link',{name:'作品案例'}).click();
   await page.getByRole('heading',{name:'作品案例',exact:true}).waitFor();
   await page.getByRole('navigation',{name:'主要导航'}).getByRole('link',{name:'创建品牌'}).click();
   await page.getByLabel('品牌名称',{exact:true}).fill('新输入');
   release();
   for(let i=0;i<30&&finished<1;i++)await page.waitForTimeout(100);
   assert.equal(finished,1);
   await page.getByText('本页品牌已保留，点击生成时再同步草稿',{exact:true}).waitFor({timeout:7000});
   await page.reload();
   await page.getByText('本页品牌已保留，点击生成时再同步草稿',{exact:true}).waitFor({timeout:7000});
   assert.equal(await page.getByLabel('品牌名称',{exact:true}).inputValue(),'新输入');
   assert.equal(finished,1,'another brand must not silently overwrite the server draft');
  }finally{release()}
 });
});
test('Direction preview does not generate; explicit selection submits the chosen index only once',async()=>{
 const posts=[];
 await run('/job/CHOOSE',async({r,path,request,json})=>{
  if(path==='/api/jobs/CHOOSE'){await json(r,{status:'ready_to_choose',stage:'choose',images:[0,1,2].map(idx=>({kind:'direction',idx,file:'assets/examples/m-brand/logo.svg'})),progress:''});return true}
  if(path==='/api/jobs/CHOOSE/detail'){await json(r,{items:[],bundle_price:9900});return true}
  if(path==='/api/jobs/CHOOSE/choose'){posts.push(JSON.parse(request.postData()));await json(r,{ok:true});return true}
 },async page=>{
  await page.getByRole('button',{name:'放大当前方向'}).click();
  await page.getByRole('dialog').waitFor();
  await page.keyboard.press('Escape');
  await page.waitForFunction(()=>document.activeElement===document.querySelector('.enlarge-button'),null,{timeout:1200});
  await page.getByRole('button',{name:'预览方向 B',exact:true}).click();assert.equal(posts.length,0);
  const chosen=page.waitForResponse(r=>r.url().endsWith('/choose'));
  await page.getByRole('button',{name:'使用这个方向',exact:true}).click();await chosen;
  assert.deepEqual(posts,[{direction:1}]);
 });
});
test('Gallery keyboard preview returns focus to the opened image',async()=>{
 const fixture=fixtures();
 await run('/gallery',async({r,path,request,json})=>{
  const result=fixture(request.method(),path,request.postDataJSON()||{});
  if(result!==undefined){await json(r,result);return true}
 },async page=>{
  const first=page.locator('.gallery-tile').first();
  await first.click();await page.getByRole('dialog').waitFor();
  await page.keyboard.press('ArrowRight');
  await page.getByRole('dialog').getByRole('heading',{name:/品牌系统/}).waitFor();
  await page.keyboard.press('Escape');
  await page.waitForFunction(()=>document.activeElement===document.querySelector('.gallery-tile'),null,{timeout:1200});
 });
});
test('Admin has one active route and mobile navigation closes after switching panels',async()=>{
 const fixture=fixtures();
 await run('/admin/agents',async({r,path,request,json})=>{
  if(path==='/api/admin/agent-payouts'){await json(r,[]);return true}
  const result=fixture(request.method(),path,request.postDataJSON()||{});
  if(result!==undefined){await json(r,result);return true}
 },async page=>{
  await page.setViewportSize({width:375,height:812});
  await page.getByRole('button',{name:'后台导航 · 代理'}).click();
  const nav=page.getByRole('navigation',{name:'管理导航'});
  assert.equal(await nav.locator('[aria-current=page]').count(),1);
  await nav.getByRole('link',{name:'订单',exact:true}).click();
  await page.waitForURL('**/admin/orders');
  await page.getByRole('button',{name:'后台导航 · 订单',expanded:false}).waitFor();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
 });
});
test('Mobile purchase summary leaves the last footer links reachable after scrolling to the end',async()=>{
 const fixture=fixtures();
 await run('/job/SELECT',async({r,path,request,json})=>{
  if(path==='/api/admin/agent-payouts'){await json(r,[]);return true}
  const result=fixture(request.method(),path,request.postDataJSON()||{});
  if(result!==undefined){await json(r,result);return true}
 },async page=>{
  await page.setViewportSize({width:375,height:812});
  await page.getByRole('button',{name:'全选',exact:true}).click();await page.getByRole('region',{name:'选购汇总'}).waitFor();
  await page.evaluate(()=>window.scrollTo(0,document.body.scrollHeight));
  const visible=await page.evaluate(()=>{
   const summary=document.querySelector('.purchase-summary').getBoundingClientRect();
   return [...document.querySelectorAll('.site-footer a')].every(a=>{
    const r=a.getBoundingClientRect();
    return r.top>=0&&r.bottom<=summary.top;
   });
  });
  assert.equal(visible,true,'footer navigation must not be covered by the purchase bar');
 });
});

test('Brand gallery keeps the self-owned example available when published cases fail',async()=>{
 await run('/gallery',async({r,path,json})=>{if(path==='/api/cases'){await json(r,{error:'Unavailable'},503);return true}},async page=>{
  await page.getByRole('alert').waitFor();
  assert.equal(await page.locator('.gallery-tile').count(),4);
  await page.locator('.gallery-tile').first().click();await page.getByRole('dialog').waitFor();
  assert.ok(await page.getByRole('dialog').getByRole('heading',{name:/M-brand/}).isVisible());
 });
});
test('Brand gallery replaces only previous marketing examples and preserves other published cases',async()=>{
 await run('/gallery',async({r,path,json})=>{
  if(path!=='/api/cases')return false;
  await json(r,[
   {id:1,title:'Previous sample',tagline:'',images:[{file:'assets/shanwu/board_1.webp',label:'Old'}]},
   {id:2,title:'Previous bakery sample',tagline:'',images:[{file:'gen/legacy-demo/board_1.webp',label:'Old'}]},
   {id:3,title:'SEEDLAB Customer',tagline:'Customer case must remain',images:[{file:'assets/examples/m-brand/logo.svg',label:'Customer Logo'}]}
  ]);return true;
 },async page=>{
  await page.getByRole('heading',{name:'SEEDLAB Customer',exact:true}).waitFor();
  assert.equal(await page.getByRole('heading',{name:'Previous sample',exact:true}).count(),0);
  assert.equal(await page.getByRole('heading',{name:'Previous bakery sample',exact:true}).count(),0);
  assert.equal(await page.locator('.gallery-tile').count(),5);
  await page.locator('.gallery-tile').last().click();
  await page.getByRole('dialog').getByRole('heading',{name:/SEEDLAB Customer/}).waitFor();
 });
});
