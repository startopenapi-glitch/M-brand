import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {mkdir} from 'node:fs/promises';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const require=createRequire(import.meta.url),{chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const origin='http://127.0.0.1:4298';let server,browser,output='';
before(async()=>{server=spawn(process.execPath,[resolve(root,'node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port','4298','--strictPort'],{cwd:root,stdio:['ignore','pipe','pipe']});server.stdout.on('data',d=>output+=d);server.stderr.on('data',d=>output+=d);let ready=false;for(let i=0;i<100;i++){if(server.exitCode!==null)throw Error(output);try{ready=(await fetch(origin)).ok}catch{}if(ready)break;await new Promise(r=>setTimeout(r,100))}assert.ok(ready,output);browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_EXECUTABLE})});
after(async()=>{await browser?.close();server?.kill('SIGTERM')});
for(const width of [375,1440])test(`commission before invitation and after sending/activation ${width}px`,async()=>{
 const ctx=await browser.newContext({viewport:{width,height:900},serviceWorkers:'block'});const changes=[],unknown=[],errors=[];let invites=[],agentPct=40;
 await ctx.route('**/*',async r=>{const u=new URL(r.request().url()),p=u.pathname,method=r.request().method(),json=(body,status=200)=>r.fulfill({json:body,status});if(u.origin!==origin){unknown.push(u.origin);return r.abort()}if(!p.startsWith('/api/'))return r.continue();
 if(p==='/api/admin/session')return json({name:'fixture',capabilities:['overview','agents']});
 if(p==='/api/admin/agents'&&method==='GET')return json([{id:'AG-T',name:'Registered',email:'registered@example.test',code:'TEST',commission_pct:agentPct,active:true,total_earned_cents:270,total_paid_cents:0,withdrawable_cents:270,created_at:'10-22'}]);
 if(p==='/api/admin/agents/stats')return json({agent_count:1,total_commission_cents:270,total_paid_cents:0,total_pending_cents:270});
 if(p==='/api/admin/agent-payouts')return json([]);
 if(p==='/api/admin/agents/invitations'&&method==='GET')return json(invites);
 if(p==='/api/admin/agents/invite'&&method==='POST'){const body=r.request().postDataJSON();changes.push({kind:'invite',pct:body.CommissionPct});invites=[{id:'INV-T',email:body.Email,name:'Pending',commission_pct:body.CommissionPct,created_at:'2026-10-22',expires_at:'2026-10-29'}];return json({ok:true,id:'INV-T',email_sent:true},201)}
 if(p==='/api/admin/agents/invitations/INV-T'&&method==='PATCH'){const pct=r.request().postDataJSON().CommissionPct;changes.push({kind:'pending',pct});invites=invites.map(v=>({...v,commission_pct:pct}));return json({ok:true})}
 if(p==='/api/admin/agents/AG-T'&&method==='PATCH'){agentPct=r.request().postDataJSON().CommissionPct;changes.push({kind:'registered',pct:agentPct});return json({ok:true})}
 unknown.push(method+' '+p);return json({error:'unexpected'},500)});
 const page=await ctx.newPage();page.setDefaultTimeout(5000);page.on('pageerror',e=>errors.push(e.message));
 try{
 await page.goto(origin+'/admin/agents');await page.getByRole('heading',{name:'代理管理',exact:true}).waitFor();await page.getByRole('button',{name:'+ 新建代理',exact:true}).click();
 await page.getByLabel('代理邮箱',{exact:true}).fill('pending@example.test');const pct=page.getByLabel('邀请佣金比例（%）',{exact:true});assert.equal(await pct.inputValue(),'40');await pct.fill('91');await page.getByRole('button',{name:'发送邮件邀请',exact:true}).click();await page.getByText('佣金比例需为 1–90 之间的整数',{exact:true}).waitFor();assert.equal(changes.length,0);
 await pct.fill('27');await page.getByRole('button',{name:'发送邮件邀请',exact:true}).click();await page.getByText('pending@example.test',{exact:true}).waitFor();assert.deepEqual(changes,[{kind:'invite',pct:27}]);
 await page.getByRole('button',{name:'调整 pending@example.test 的佣金',exact:true}).click();await page.getByLabel('新佣金比例（%）',{exact:true}).fill('31');await page.getByRole('button',{name:'保存佣金',exact:true}).evaluate(b=>{b.click();b.click()});await page.getByText('31%',{exact:true}).waitFor();assert.deepEqual(changes[1],{kind:'pending',pct:31});assert.equal(changes.length,2);
 await page.getByRole('button',{name:'调整 registered@example.test 的佣金',exact:true}).click();await page.getByLabel('新佣金比例（%）',{exact:true}).fill('35');await page.getByRole('button',{name:'保存佣金',exact:true}).click();await page.getByText('35%',{exact:true}).waitFor();assert.deepEqual(changes[2],{kind:'registered',pct:35});assert.equal(changes.length,3);
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.deepEqual(unknown,[]);assert.deepEqual(errors,[]);await mkdir(resolve(root,'../../evidence'),{recursive:true});await page.screenshot({path:resolve(root,`../../evidence/commission-${width}.png`),fullPage:true});
 }finally{await ctx.close()}
});
