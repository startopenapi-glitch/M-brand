import assert from 'node:assert/strict';
import {before, after, test} from 'node:test';
import {spawn} from 'node:child_process';
import {readFile} from 'node:fs/promises';
import {resolve, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {fixtures} from '../verification/ui-fixtures.mjs';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..'),origin='http://127.0.0.1:4241';
let server,browser,output='';
before(async()=>{
 server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4241','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});
 server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);
 for(let i=0;i<80;i++){if(server.exitCode!==null)throw Error(output);try{if((await fetch(origin)).ok)break}catch{}await new Promise(r=>setTimeout(r,100))}
 browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE});
});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
async function run(path,handler,check){
 const context=await browser.newContext({viewport:{width:375,height:667},serviceWorkers:'block'});
 const page=await context.newPage(),fixture=fixtures(),unexpected=[],errors=[];
 page.setDefaultTimeout(2500);page.on('pageerror',e=>errors.push(e.message));
 await context.route('**/*',async r=>{
  const u=new URL(r.request().url()),path=u.pathname,request=r.request();
  if(u.origin!==origin){unexpected.push(u.origin);return r.abort()}
  const json=(body,status=200)=>r.fulfill({status,json:body});
  if(await handler({path,request,json,r}))return;
  if(path==='/api/agent/me/payouts'||path==='/api/agent/me/analytics')return json([]);
  if(path==='/docs/user-agreement')return r.fulfill({contentType:'text/html',body:await readFile(resolve(root,'../../docs/user-agreement.html'),'utf8')});
  if(!path.startsWith('/api/'))return r.continue();
  const data=fixture(request.method(),path,request.postDataJSON()||{});
  if(data!==undefined)return json(data);
  unexpected.push(path);return json({error:'Unexpected mock request'},500);
 });
 try{await page.goto(origin+path);await check(page);assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])}finally{await context.close()}
}
for(const [role,identity] of [['admin','管理员账号'],['agent','代理邮箱']]){
 test(`${role} login: Enter and repeated submit send one request and recover after failure`,async()=>{
  let calls=0,release;const held=new Promise(r=>release=r),bodies=[];
  await run('/'+role,async({path,request,json})=>{
   if(path==='/api/admin/session'||path==='/api/admin/stats'||path==='/api/agent/me/stats'){await json({error:'请先登录'},401);return true}
   if(path===`/api/${role}/login`){calls++;bodies.push(request.postDataJSON());if(calls===1)await held;await json({error:'测试凭据错误'},401);return true}
  },async page=>{
   try{
    await page.getByLabel(identity,{exact:true}).fill(role==='admin'?'test-admin':'test@example.invalid');
    const password=page.getByLabel('密码',{exact:true});
    await password.fill('mock-only-password');
    await password.press('Enter');
    await page.getByRole('button',{name:'登录中…',exact:true}).waitFor();
    await page.locator('form').evaluate(form=>{form.requestSubmit();form.requestSubmit()});
    assert.equal(calls,1);
    release();
    await page.getByRole('alert').filter({hasText:'测试凭据错误'}).waitFor();
    assert.equal(await page.getByRole('button',{name:'登录',exact:true}).isEnabled(),true);
    assert.ok(await password.evaluate(el=>parseFloat(getComputedStyle(el).fontSize)>=16));
    const nextResponse=page.waitForResponse(r=>r.url().endsWith(`/${role}/login`));
    await password.press('Enter');
    await nextResponse;
    assert.equal(calls,2);
    assert.deepEqual(bodies[0],role==='admin'?{Username:'test-admin',Password:'mock-only-password'}:{Email:'test@example.invalid',Password:'mock-only-password'});
   }finally{release()}
  });
 });
}
test('Admin task detail has a keyboard-safe mobile dialog with uncropped images',async()=>{
 await run('/admin/jobs',async({path,json})=>{
  if(path==='/api/admin/jobs/CHOOSE'){await json({id:'CHOOSE',status:'ready_to_choose',form:{cn_name:'独立测试品牌',note:'x'.repeat(200)},images:[{kind:'direction',idx:0,file:'assets/examples/m-brand/logo.svg'}]});return true}
 },async page=>{
  const trigger=page.getByRole('button',{name:'详情',exact:true}).first();
  await trigger.click();
  const dialog=page.getByRole('dialog',{name:/任务 CHOOSE/});
  await dialog.waitFor();
  assert.equal(await dialog.evaluate(el=>el.contains(document.activeElement)),true);
  for(let i=0;i<5;i++)await page.keyboard.press('Tab');
  assert.equal(await dialog.evaluate(el=>el.contains(document.activeElement)),true);
  const close=dialog.getByRole('button',{name:'关闭任务详情',exact:true});
  const rect=await close.boundingBox();assert.ok(rect.width>=44&&rect.height>=44);
  assert.equal(await dialog.locator('img').first().evaluate(el=>getComputedStyle(el).objectFit),'contain');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});
  await page.waitForFunction(()=>document.activeElement?.textContent==='详情');
  assert.equal(await trigger.evaluate(el=>el===document.activeElement),true);
 });
});
test('Payment dialog returns keyboard focus to continuing the same pending order',async()=>{
 let creates=0;
 await run('/job/SELECT',async({path})=>{if(path==='/api/pay/create')creates++;return false},async page=>{
  await page.getByRole('button',{name:'全选',exact:true}).click();
  await page.getByRole('button',{name:/^支付 ¥/}).click();
  const dialog=page.getByRole('dialog',{name:'微信支付'}),resume=page.getByRole('button',{name:'继续支付 / 查进度',exact:true});
  await dialog.waitFor();await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});
  await page.waitForFunction(()=>document.activeElement?.textContent==='继续支付 / 查进度');
  assert.equal(await resume.evaluate(el=>document.activeElement===el),true);
  await page.keyboard.press('Enter');await dialog.waitFor();
  await page.getByRole('button',{name:'关闭，稍后继续',exact:true}).click();
  await page.waitForFunction(()=>document.activeElement?.textContent==='继续支付 / 查进度');
  assert.equal(creates,1);
 });
});
test('Legal document retains anchors and offers the branded route back home',async()=>{
 await run('/docs/user-agreement',async()=>false,async page=>{
  const home=page.getByRole('link',{name:'M-brand 首页',exact:true});
  await home.waitFor();
  assert.equal(await page.locator('link[rel=icon]').getAttribute('href'),'/favicon.svg');
  assert.equal(await page.evaluate(()=>getComputedStyle(document.body).backgroundColor),'rgb(246, 242, 234)');
  await page.getByRole('link',{name:'5. 退款政策',exact:true}).click();
  assert.equal(new URL(page.url()).hash,'#s5');
  await home.click();await page.getByRole('heading',{name:'创建品牌提案',exact:true}).waitFor();
 });
});
test('Agent copy failure never claims success and offers manual copy before retry',async()=>{
 await run('/agent',async({path,r})=>{
  if(path.startsWith('/api/agents/')&&path.endsWith('/qr')){
   await r.fulfill({contentType:'image/svg+xml',body:await readFile(resolve(root,'public/assets/examples/m-brand/logo.svg'))});return true;
  }
 },async page=>{
  await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async()=>{throw new DOMException('denied','NotAllowedError')}}}));
  await page.getByRole('button',{name:'复制',exact:true}).click();
  await page.getByRole('status').filter({hasText:'请长按或手动复制'}).waitFor();
  assert.equal(await page.getByText('✓ 已复制',{exact:true}).count(),0);
  assert.equal(await page.getByLabel('推广链接').evaluate(el=>document.activeElement===el&&el.selectionEnd===el.value.length),true);
  await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{window.__copiedLink=text}}}));
  await page.getByRole('button',{name:'复制',exact:true}).click();
  await page.getByRole('button',{name:'✓ 已复制',exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>window.__copiedLink),await page.getByLabel('推广链接').inputValue());
 });
});
test('Admin task detail ignores late responses after another selection and closing',async()=>{
 let release;const held=new Promise(r=>release=r);
 await run('/admin/jobs',async({path,json})=>{
  if(path==='/api/admin/jobs/CHOOSE'){await held;await json({id:'CHOOSE',status:'ready_to_choose',form:{},images:[]});return true}
  if(path==='/api/admin/jobs/DELIVERED'){await json({id:'DELIVERED',status:'done',form:{},images:[]});return true}
 },async page=>{
  try{
   const firstRequest=page.waitForRequest(r=>r.url().endsWith('/admin/jobs/CHOOSE'));
   await page.getByRole('button',{name:'详情',exact:true}).first().click();await firstRequest;
   await page.getByRole('button',{name:'详情',exact:true}).nth(1).click();
   await page.getByRole('dialog',{name:/任务 DELIVERED/}).waitFor();
   await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'hidden'});
   const late=page.waitForEvent('requestfinished',r=>r.url().endsWith('/admin/jobs/CHOOSE'));
   release();await late;
   await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
   assert.equal(await page.getByRole('dialog').count(),0,'A late response must not reopen a dismissed dialog');
  }finally{release()}
 });
});
