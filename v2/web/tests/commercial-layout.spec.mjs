import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { spawn } from 'node:child_process';
import { setTimeout } from 'node:timers/promises';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const origin='http://127.0.0.1:4217';
const server=spawn(process.execPath,['node_modules/vite/bin/vite.js','--host','127.0.0.1','--port','4217','--strictPort'],{stdio:'ignore'});
let browser;const results=[];
try {
 for(let i=0;i<50;i++){try{if((await fetch(origin+'/')).ok)break}catch{}await setTimeout(150)}
 browser=await chromium.launch({executablePath:process.env.CHROMIUM_EXECUTABLE,headless:true});
 for(const width of [375,768,1440]) {
  const page=await browser.newPage({viewport:{width,height:1000},serviceWorkers:'block'});
  const external=[];
  await page.route('**/*',r=>{
   const url=new URL(r.request().url());
   if(url.origin!==origin){external.push(url.origin);return r.abort()}
   if(url.pathname.startsWith('/api/'))return r.fulfill({status:200,contentType:'application/json',body:'[]'});
   return r.continue();
  });
  for(const path of ['redeem','orders']) {
   await page.goto(origin+'/'+path);await page.getByRole('heading').first().waitFor();
   if(path==='orders')await page.getByRole('button',{name:'换设备找回'}).click();
   const button=page.getByRole('button',{name:path==='redeem'?'兑换':'找回',exact:true});
   const size=await button.evaluate(e=>{const r=document.createRange();r.selectNodeContents(e);return {textHeight:r.getBoundingClientRect().height,fontSize:parseFloat(getComputedStyle(e).fontSize),height:e.getBoundingClientRect().height}});
   const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth);
   results.push({width,path,...size,overflow});
   assert.ok(size.textHeight<size.fontSize*1.6,`${width}/${path}: 操作按钮文字不能被挤成两行`);
   assert.ok(size.height>=44,`${width}/${path}: 触控高度不得低于44`);
   assert.equal(overflow,false);
  }
  assert.deepEqual(external,[],'External requests are forbidden');
  await page.close();
 }
 console.log(JSON.stringify(results));
}finally{await browser?.close();server.kill('SIGTERM')}
