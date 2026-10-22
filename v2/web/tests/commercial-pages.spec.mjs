// Isolated real React UI tests: no production API or payment/generation traffic.
import assert from "node:assert/strict";
import { before, after, test } from "node:test";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const origin = "http://127.0.0.1:4207";
const plan = { price_fen: 9900, price_label: "¥99.00", title: "品牌提案", includes: ["品牌系统卡 + 四图"], pay_enabled: true, h5: false, sales_paused: false };
const meta = { enabled: true, h5Enabled: false, mock: false };
const qr = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a/ZYAAAAASUVORK5CYII=";
const oldOrder = { id: "MOCKOLDORDER0001", amount_cents: 6900, channel: "native", qr, ts: Date.now() };
let server, browser, serverOutput = "";
before(async () => {
  server = spawn(process.execPath, [resolve(webDir, "node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", "4207", "--strictPort"], { cwd: webDir, stdio: ["ignore", "pipe", "pipe"] });
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
    await run(page);
    assert.deepEqual(unexpected, [], "All API requests must be mocked; external requests are forbidden");
  } finally { await context.close(); }
}

for(const status of ['paid','pending','expired','closed','failed']) {
 test(`Pay: recorded ${status} orders are read-only and never create a legacy purchase`,async()=>{
  await withPage('pay',async({route,path,json})=>{if(path===`/api/pay/order/${oldOrder.id}`){await json(route,{id:oldOrder.id,amount_cents:6900,status});return true;}},async(page)=>{
   await page.getByRole('heading',{name:'在提案内选择并付款'}).waitFor();await page.getByText(`订单 ${oldOrder.id}`,{exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:/支付|购买/}).count(),0);await page.getByRole('link',{name:'我的提案与订单'}).waitFor();
  },{zb_last_order:oldOrder});
 });
}
test('Pay: failed lookup keeps the original order reference for recovery',async()=>{
 await withPage('pay',async({route,path,json})=>{if(path===`/api/pay/order/${oldOrder.id}`){await json(route,{error:'读取失败'},503);return true;}},async(page)=>{await page.getByRole('alert').waitFor();assert.ok(await page.evaluate(()=>localStorage.getItem('zb_last_order')));assert.equal(await page.getByRole('button',{name:/支付|购买/}).count(),0);},{zb_last_order:oldOrder});
});

test("Wizard: a late server draft cannot overwrite the user's current input", async () => {
  let release;
  const draftReady = new Promise((resolve) => { release = resolve; });
  await withPage("create", async ({ route, request, path, json }) => {
    if (path === "/api/draft") {
      if (request.method() === "GET") { await draftReady; await json(route, { cn_name: "旧草稿", industry: "茶饮" }); }
      else await json(route, { ok: true });
      return true;
    }
  }, async (page) => {
    await page.locator("input").first().fill("刚填写的新品牌");
    const response = page.waitForResponse((r) => r.url().endsWith("/api/draft") && r.request().method() === "GET");
    release();
    await (await response).finished();
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal(await page.locator("input").first().inputValue(), "刚填写的新品牌");
  });
});

test("Wizard: reloading before debounce completes preserves the final typed text", async () => {
  await withPage("create", async ({ route, request, path, json }) => {
    if (path === "/api/draft") {
      await json(route, request.method() === "GET" ? {} : { ok: true });
      return true;
    }
  }, async (page) => {
    await page.locator("input").first().fill("最后输入的品牌尾字");
    await page.reload();
    await page.locator("input").first().waitFor();
    assert.equal(await page.locator("input").first().inputValue(), "最后输入的品牌尾字");
  });
});

for (const resource of ["jobs", "orders"]) {
  test(`Orders: failed ${resource} load is retryable and cannot masquerade as an empty account`, async () => {
    let unavailable = true;
    await withPage("orders", async ({ route, path, json }) => {
      if (path === `/api/my/${resource}`) {
        await json(route, unavailable ? { error: "暂时无法读取记录" } : [], unavailable ? 503 : 200);
        return true;
      }
      if (path.startsWith("/api/my/")) { await json(route, []); return true; }
    }, async (page) => {
      if(resource === "orders") await page.getByRole("tab", {name:"订单",exact:true}).click();
      await page.getByText("暂时无法读取记录", { exact: true }).waitFor();
      const empty = resource === "jobs" ? "你的第一个品牌，从这里开始。" : "暂无订单";
      assert.equal(await page.getByText(empty, { exact: true }).count(), 0);
      unavailable = false;
      await page.getByRole("button", { name: resource === "jobs" ? "重试加载提案" : "重试加载订单" }).click();
      await page.getByText(empty, { exact: true }).waitFor();
    });
  });
}
