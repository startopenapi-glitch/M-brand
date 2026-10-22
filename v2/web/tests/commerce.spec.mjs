// Isolated real React UI tests: no production API or payment/generation traffic.
import {mkdir} from "node:fs/promises";
import assert from "node:assert/strict";
import { before, after, test } from "node:test";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const origin = "http://127.0.0.1:4227";
const plan = { price_fen: 9900, price_label: "¥99.00", title: "品牌提案", includes: ["品牌系统卡 + 四图"], pay_enabled: true, h5: false, sales_paused: false };
const meta = { enabled: true, h5Enabled: false, mock: false };
const qr = 'data:image/svg+xml;base64,'+Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="220" height="220"><rect width="220" height="220" fill="white"/><g fill="black">${Array.from({length:441},(_,i)=>{const x=i%21,y=Math.floor(i/21);return ((x*7+y*11)%5<2)?`<rect x="${16+x*9}" y="${16+y*9}" width="9" height="9"/>`:''}).join('')}</g><rect x="30" y="88" width="160" height="45" fill="white"/><text x="110" y="116" text-anchor="middle" font-size="20">MOCK TEST</text></svg>`).toString('base64');
const artwork = `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512"><rect width="512" height="512" fill="#f4f1e9"/><circle cx="256" cy="210" r="90" fill="#172f2b"/><path d="M220 240V170L256 210L292 170V240" fill="none" stroke="#f4f1e9" stroke-width="12"/><text x="256" y="350" text-anchor="middle" fill="#172f2b" font-size="28">BRAND · TEST</text></svg>`;
const oldOrder = { id: "MOCKOLDORDER0001", amount_cents: 6900, channel: "native", qr, ts: Date.now() };
let server, browser, serverOutput = "";
before(async () => {
  server = spawn(process.execPath, [resolve(webDir, "node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", "4227", "--strictPort"], { cwd: webDir, stdio: ["ignore", "pipe", "pipe"] });
  server.stdout.on("data", (data) => { serverOutput += data; });
  server.stderr.on("data", (data) => { serverOutput += data; });
  let available = false;
  for (let attempt = 0; attempt < 60; attempt++) {
    if (server.exitCode !== null) throw new Error(serverOutput);
    try { available = (await fetch(origin + "/")).ok; } catch {}
    if (available) break;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  assert.ok(available, serverOutput);
  browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_EXECUTABLE });
});
after(async () => { await browser?.close(); server?.kill("SIGTERM"); });

async function withPage(path, handler, run, stored = {}) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  await context.addInitScript((values) => {
    if (!sessionStorage.getItem("test_initialized")) {
      for (const [key, value] of Object.entries(values)) localStorage.setItem(key, JSON.stringify(value));
      sessionStorage.setItem("test_initialized", "yes");
    }
  }, stored);
  const page = await context.newPage();
  page.setDefaultTimeout(2500);
  const unexpected = [];
  const json = (route, body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  await context.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) { unexpected.push(url.origin); return route.abort(); }
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (await handler({ route, request, path: url.pathname, json })) return;
    if (url.pathname === "/api/pay/status") return json(route, meta);
    if (url.pathname === "/api/plan") return json(route, plan);
    unexpected.push(`${request.method()} ${url.pathname}`);
    return json(route, { error: "Unexpected test API" }, 500);
  });
  try {
    await page.goto(origin + "/" + path);
    await run(page,context);
    assert.deepEqual(unexpected, [], "All API requests must be mocked; external requests are forbidden");
  } finally { await context.close(); }
}


const jid='commerce-fixture';
const definitions=[['logo_h','Logo 横版组合',390,'logo'],['logo_v','Logo 竖版组合',390,'logo'],['card_front','名片正面',290,'apps']];
function commerceMock(){
 const m={posts:[],paid:false,pending:false,unknown:false,failedRead:false};
 m.handle=async({route,request,path,json})=>{
  if(path===`/api/jobs/${jid}`){await json(route,{status:'done',stage:'done',images:[],progress:''});return true;}
  if(path===`/api/jobs/${jid}/detail`){await json(route,{items:definitions.map(([kind,name,price_cents,category])=>({kind,name,price_cents,category,purchased:m.paid&&kind==='logo_h',generated:m.paid&&kind==='logo_h',file:m.paid&&kind==='logo_h'?'gen/commerce/logo_h.png':'',unlocked:m.paid&&kind==='logo_h'})),bundle_price:9900,remaining_bundle_price:m.paid?4600:9900,pending_order_id:m.pending&&!m.paid?'ORDER-ONE':''});return true;}
  if(path==='/api/pay/create'&&request.method()==='POST'){m.posts.push(JSON.parse(request.postData()));m.pending=true;await json(route,{id:'ORDER-ONE',amount_cents:390,item_count:1,status:'pending',qr:m.unknown?undefined:qr},m.unknown?202:201);return true;}
  if(path==='/api/pay/checkout/ORDER-ONE'){await json(route,{id:'ORDER-ONE',amount_cents:390,item_count:1,status:m.paid?'paid':'pending',qr:m.unknown?undefined:qr});return true;}
  if(path==='/api/pay/status/ORDER-ONE'){await json(route,m.failedRead?{error:'状态暂不可用'}:{id:'ORDER-ONE',status:m.paid?'paid':'pending'},m.failedRead?503:200);return true;}
  if(path==='/api/pay/orders/ORDER-ONE/resume'){m.unknown=false;await json(route,{id:'ORDER-ONE',amount_cents:390,item_count:1,status:'pending',qr});return true;}
 };
 return m;
}
for(const width of [375,768,1440]){
 test(`Commerce ${width}px: select once, restore pending order, then show paid delivery`,async()=>{
  const m=commerceMock();await withPage(`job/${jid}`,m.handle,async(page,context)=>{
   await page.setViewportSize({width,height:900});await page.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).check();
   await page.getByRole('button',{name:'支付 ¥3.90',exact:true}).click();await page.getByRole('dialog',{name:'微信支付'}).waitFor();
   assert.deepEqual(m.posts,[{job_id:jid,items:['logo_h']}]);
   await page.keyboard.press('Tab');assert.ok(await page.getByRole('dialog').evaluate(el=>el.contains(document.activeElement)),'focus stays inside payment dialog');
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   await mkdir('../../evidence/screenshots',{recursive:true});await page.screenshot({path:`../../evidence/screenshots/payment-${width}.png`,fullPage:true});
   await page.getByRole('button',{name:'关闭，稍后继续'}).click();await page.reload();await page.getByRole('button',{name:'继续支付 / 查进度'}).waitFor();assert.equal(m.posts.length,1);
   await context.route('**/gen/commerce/logo_h.png',route=>route.fulfill({contentType:'image/svg+xml',body:artwork}));
   m.paid=true;await page.evaluate(()=>window.dispatchEvent(new Event('online')));await page.getByRole('link',{name:/下载已完成的 \d+ 项/}).waitFor({timeout:7000});assert.equal(m.posts.length,1);
   await page.screenshot({path:`../../evidence/screenshots/delivery-${width}.png`,fullPage:true});
  });
 });
}
test('Checkout unknown outcome recovers the same order; offline poll does not recreate payment',async()=>{
 const m=commerceMock();m.unknown=true;m.failedRead=true;
 await withPage(`job/${jid}`,m.handle,async(page)=>{
  await page.getByRole('checkbox',{name:'Logo 横版组合',exact:true}).check();await page.getByRole('button',{name:'支付 ¥3.90',exact:true}).click();
  await page.getByRole('button',{name:'核对并恢复支付入口'}).click();await page.getByAltText('微信支付二维码').waitFor();assert.equal(m.posts.length,1);await page.reload();await page.getByRole('button',{name:'继续支付 / 查进度'}).waitFor();assert.equal(m.posts.length,1);
 });
});
test('Admin payout persists its request key across lost response and reload; decimal amount stays exact',async()=>{
 const payloads=[];const agent={id:'AG-TEST',name:'测试代理',email:'fixture@example.invalid',code:'TEST',active:true,commission_pct:40,total_earned_cents:1000,total_paid_cents:0,withdrawable_cents:1000};
 await withPage('admin/agents',async({route,request,path,json})=>{
  if(path==='/api/admin/session'){await json(route,{name:'fixture',capabilities:['overview','orders','jobs','support','codes','cases','agents','usage','audit']});return true;}
  if(path==='/api/admin/stats'){await json(route,{});return true;}
  if(path==='/api/admin/agents'){await json(route,[agent]);return true;}
  if(path==='/api/admin/agent-payouts'){await json(route,[]);return true;}
  if(path==='/api/admin/agents/stats'){await json(route,{agent_count:1,total_commission_cents:1000,total_paid_cents:0,total_pending_cents:1000});return true;}
  if(path==='/api/admin/agents/AG-TEST/payout'){payloads.push(JSON.parse(request.postData()));await json(route,payloads.length===1?{error:'响应丢失'}:{ok:true},payloads.length===1?503:200);return true;}
 },async(page)=>{
  await page.setViewportSize({width:375,height:900});page.once('dialog',dialog=>dialog.accept('1.23'));
  await page.getByRole('button',{name:'记录结算'}).click();await page.getByText(/已保留本次结算请求/).waitFor();await page.reload();await page.getByRole('button',{name:'核对上次结算'}).click();await page.getByText('✓ 已记录人工结算',{exact:true}).waitFor();
  assert.equal(payloads.length,2);assert.deepEqual(payloads[0],payloads[1]);assert.equal(payloads[0].AmountCents,123);assert.ok(payloads[0].RequestKey.length>=16);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await page.screenshot({path:'../../evidence/screenshots/admin-375.png',fullPage:true});
 });
});
