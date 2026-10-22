import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {mkdir} from 'node:fs/promises';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..'),require=createRequire(import.meta.url),{chromium}=require('playwright');
const origin='http://127.0.0.1:4287';let server,browser,log='';
before(async()=>{server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4287','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});server.stdout.on('data',d=>log+=d);server.stderr.on('data',d=>log+=d);let ready=false;for(let i=0;i<80;i++){if(server.exitCode!==null)throw Error(log);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}assert.ok(ready,log);browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE})});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
async function scenario(width,check,options={}){
 const ctx=await browser.newContext({viewport:{width,height:850},serviceWorkers:'block'}),errors=[],unknown=[];let failing=false,posts=0;
 let settled=false;
 const item=(kind,name,purchased,generated)=>({kind,name,category:kind==='system_card'?'free':'logo',price_cents:390,purchased,generated,unlocked:purchased,file:generated?'assets/examples/m-brand/logo.svg':''});
 await ctx.route('**/*',async r=>{const u=new URL(r.request().url());if(u.origin!==origin){unknown.push(u.origin);return r.abort()}if(!u.pathname.startsWith('/api/'))return r.continue();if(r.request().method()!=='GET'){posts++;return r.fulfill({status:503,json:{error:'Do not mutate'}})}
 if(failing)return r.fulfill({status:503,json:{error:'retry later'}});
 if(u.pathname==='/api/jobs/R6')return r.fulfill({json:{status:settled?'done':options.uncertain?'error':options.queued?'queued':'running',stage:'boards',images:[],chosen:0,progress:'已完成 2/4 项',delivery:{started_at:new Date(Date.now()-(options.queued?86400000:125000)).toISOString(),observed_at:new Date().toISOString(),steps:[{kind:'logo_v',index:3,state:settled?'done':options.uncertain?'unknown':'submitted',started_at:new Date(Date.now()-45000).toISOString()}],needs_review:!settled&&!!options.uncertain}}});
 if(u.pathname==='/api/jobs/R6/detail')return r.fulfill({json:{items:[item('system_card','品牌系统卡',true,true),item('logo_h','Logo 横版组合',true,true),item('logo_v','Logo 竖版组合',true,settled),item('logo_icon','独立图标',true,settled),item('logo_mono','单色 Logo',false,false)],bundle_price:9900}});
 unknown.push(u.pathname);return r.fulfill({status:500,json:{error:'unmapped'}})
 });
 ctx.on('page',p=>{p.setDefaultTimeout(6000);p.on('pageerror',e=>errors.push(e.message))});
 try{await check(ctx,()=>{failing=true},()=>{failing=false},()=>{settled=true});assert.equal(posts,0);assert.deepEqual(errors,[]);assert.deepEqual(unknown,[])}finally{await ctx.close()}
}
for(const width of [375,768,1440])test(`delivery ${width}: paid results first, truthful progress, optional shopping and partial download`,async()=>scenario(width,async ctx=>{
 const p=await ctx.newPage();await p.goto(origin+'/job/R6');
 await p.getByRole('region',{name:'制作进度'}).waitFor();
 await p.getByText('已购内容：已完成 1/3 项',{exact:true}).waitFor();
 await p.getByText('正在生成：Logo 竖版组合',{exact:true}).waitFor();
 assert.match(await p.getByTestId('delivery-elapsed').innerText(),/2 分/);
 assert.equal(await p.getByRole('button',{name:'支付 ¥0.00',exact:true}).isVisible().catch(()=>false),false);
 const download=p.getByRole('link',{name:'下载已完成的 1 项',exact:true});await download.waitFor();
 const more=p.getByRole('button',{name:'追加交付项',exact:true});await more.waitFor();
 assert.ok((await download.boundingBox()).y<(await more.boundingBox()).y,'finished work must precede shopping');
 assert.equal(await p.getByRole('checkbox',{name:'单色 Logo',exact:true}).isVisible().catch(()=>false),false);
 const dir=resolve(root,'../../evidence/delivery-r6-20261020/ui');await mkdir(dir,{recursive:true});await p.screenshot({path:resolve(dir,`delivery-first-${width}.png`),fullPage:true});
 await more.click();await p.getByRole('checkbox',{name:'单色 Logo',exact:true}).check();await p.getByRole('button',{name:'支付 ¥3.90',exact:true}).waitFor();
 assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
 await p.screenshot({path:resolve(dir,`delivery-${width}.png`),fullPage:true});
}));
test('uncertain delivery observes late recovery without browser events or mutation',async()=>scenario(375,async(ctx,_fail,_recover,finish)=>{
 const p=await ctx.newPage();await p.goto(origin+'/job/R6');
 await p.getByRole('heading',{name:'生成结果正在核对',exact:true}).waitFor();
 finish();
 await p.getByText('已购内容：已完成 3/3 项',{exact:true}).waitFor({timeout:10000});
 await p.getByRole('link',{name:'下载已完成的 3 项',exact:true}).waitFor();
 assert.equal(await p.getByRole('heading',{name:'生成结果正在核对',exact:true}).count(),0);
},{uncertain:true}));
test('three tabs retain delivery during DB outage and recover without online event or any POST',async()=>scenario(375,async(ctx,fail,recover)=>{
 const tabs=await Promise.all([ctx.newPage(),ctx.newPage(),ctx.newPage()]);for(const p of tabs){await p.goto(origin+'/job/R6');await p.getByRole('heading',{name:'我的品牌提案',exact:true}).waitFor()}
 fail();for(const p of tabs)await p.getByText('暂时无法刷新进度，已保留当前内容。联网后将自动重试。',{exact:true}).waitFor({timeout:10000});
 for(const p of tabs)assert.equal(await p.getByRole('heading',{name:'链接无效'}).count(),0);
 recover();for(const p of tabs){await p.getByText('暂时无法刷新进度，已保留当前内容。联网后将自动重试。',{exact:true}).waitFor({state:'hidden',timeout:10000});await p.getByRole('link',{name:'下载已完成的 1 项',exact:true}).waitFor()}
}));

test('queued new stage does not reuse elapsed time from the previous stage',async()=>scenario(375,async ctx=>{
 const p=await ctx.newPage();await p.goto(origin+'/job/R6');
 await p.getByRole('heading',{name:'提案已受理，等待开始',exact:true}).waitFor();
 assert.equal(await p.getByTestId('delivery-elapsed').count(),0);
 assert.equal(await p.getByText('本次制作耗时较长，已有成果已保留。页面会自动更新，请勿重复提交。',{exact:true}).count(),0);
},{queued:true}));
