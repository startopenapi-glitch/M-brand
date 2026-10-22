// Run with: node --test tests/commercial-job.spec.mjs
// Uses local Vite and the installed Playwright (or PLAYWRIGHT_MODULE).
// All API/image traffic is fulfilled here. Non-local requests are blocked.
import assert from "node:assert/strict";
import { before, after, test } from "node:test";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const origin = "http://127.0.0.1:4197";
const jobPath = "/api/jobs/commercial-job";
const plan = { price_fen: 9900, price_label: "¥99.00", title: "品牌提案", includes: ["品牌系统卡 + 四图"], pay_enabled: true, sales_paused: false };
const directions = [0, 1, 2].map((idx) => ({ kind: "direction", idx, file: `gen/test/direction_${idx}.webp` }));
const ready = { id: "commercial-job", status: "ready_to_choose", stage: "choose", chosen: null, error: "", images: directions, progress: "方向 3/3", trial: false, refunded: false, user_error: "", can_retry: false, need_unlock: false, plan };
const failed = { ...ready, status: "error", stage: "boards", chosen: 1, error: "provider failure", progress: "成图 0/5", refunded: true, user_error: "额度已返还，可以重试", can_retry: true };
const completed = { ...ready, status: "done", stage: "done", chosen: 1, images: [...directions, { kind: "system", idx: 0, file: "gen/test/system.webp" }, ...[1, 2, 3, 4].map((idx) => ({ kind: "board", idx, file: `gen/test/board_${idx}.webp` }))], progress: "成图 5/5" };
const pixel = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a/ZYAAAAASUVORK5CYII=", "base64");
let server;
let browser;
let serverOutput = "";

before(async () => {
  server = spawn(process.execPath, [resolve(webDir, "node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", "4197", "--strictPort"], { cwd: webDir, stdio: ["ignore", "pipe", "pipe"] });
  server.stdout.on("data", (chunk) => { serverOutput += chunk; });
  server.stderr.on("data", (chunk) => { serverOutput += chunk; });
  let available = false;
  for (let attempt = 0; attempt < 60; attempt++) {
    if (server.exitCode !== null) throw new Error(`Vite exited: ${serverOutput}`);
    try { available = (await fetch(origin + "/")).ok; } catch {}
    if (available) break;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  assert.ok(available, `Local Vite did not start: ${serverOutput}`);
  browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_EXECUTABLE });
});

after(async () => {
  await browser?.close();
  server?.kill("SIGTERM");
});

async function withJob(initial, handler, run) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  page.setDefaultTimeout(2500);
  const unexpected = [];
  const model = { state: structuredClone(initial), posts: [], readsAfterPost: 0 };
  const json = (route, body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  await context.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) {
      unexpected.push(`External request blocked: ${url.origin}`);
      return route.abort();
    }
    if (url.pathname.startsWith("/gen/")) return route.fulfill({ status: 200, contentType: "image/png", body: pixel });
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (await handler({ route, request, path: url.pathname, model, json })) return;
    if (url.pathname === jobPath && request.method() === "GET") return json(route, model.state);
    if(url.pathname === jobPath+"/detail")return json(route,{items:[{kind:'logo_h',name:'Logo 横版',category:'logo',price_cents:190,purchased:model.state.stage==='done'||model.state.stage==='boards',generated:model.state.stage==='done',file:model.state.stage==='done'?'gen/test/logo_h.png':'',unlocked:true}],bundle_price:9900});
    unexpected.push(`${request.method()} ${url.pathname}`);
    return json(route, { error: "Unexpected mock API request" }, 500);
  });
  try {
    await page.goto(origin + "/job/commercial-job");
    await page.getByRole("heading", { level:1, name: /提案|选择 Logo 方向/ }).waitFor();
    await run(page, model);
    assert.deepEqual(unexpected, [], "Test must never reach an unmocked API or external origin");
  } finally {
    await context.close();
  }
}

function actionButton(page, action) {
  return action === "choose"
    ? page.getByRole("button", { name: "使用这个方向", exact: true })
    : page.getByRole("button", { name: "重试未完成内容", exact: true });
}

for (const action of ["choose", "retry"]) {
  test(`${action}: an operation error remains visible on a loaded proposal`, async () => {
    await withJob(action === "choose" ? ready : failed, async ({ route, request, path, json }) => {
      if (path === `${jobPath}/${action}` && request.method() === "POST") {
        await json(route, { error: "请求暂时失败，请稍后重试" }, action === "choose" ? 500 : 429);
        return true;
      }
    }, async (page) => {
      if(action==="choose")await page.getByRole("button",{name:"预览方向 B",exact:true}).click();
      await actionButton(page, action).click();
      await page.getByRole("alert").filter({hasText:"请求暂时失败，请稍后重试"}).waitFor({ state: "visible" });
      assert.ok(await actionButton(page, action).isEnabled());
    });
  });

  test(`${action}: accepted operation recovers after the first status read fails`, async () => {
    await withJob(action === "choose" ? ready : failed, async ({ route, request, path, model, json }) => {
      if (path === `${jobPath}/${action}` && request.method() === "POST") {
        model.posts.push(request.postData());
        model.state = structuredClone(completed);
        await json(route, { ok: true });
        return true;
      }
      if (path === jobPath && request.method() === "GET" && model.posts.length) {
        model.readsAfterPost++;
        if (model.readsAfterPost === 1) {
          await json(route, { error: "状态暂时不可用" }, 503);
          return true;
        }
      }
    }, async (page, model) => {
      if(action==="choose")await page.getByRole("button",{name:"预览方向 B",exact:true}).click();
      await actionButton(page, action).click();
      await page.getByRole("heading",{name:"已生成的内容"}).waitFor({ state: "visible", timeout: 7000 });
      assert.equal(model.posts.length, 1, "Status recovery must not repeat a generation mutation");
      assert.equal(await page.getByText("状态暂时不可用", { exact: true }).count(), 0, "Recovered status clears stale errors");
    });
  });

  test(`${action}: a rejected operation never exposes the removed legacy purchase`,async()=>{
    await withJob(action==='choose'?ready:failed,async({route,request,path,json})=>{
      if(path===`${jobPath}/${action}`&&request.method()==='POST'){await json(route,{error:'操作资格待核对，请联系支持'},402);return true;}
    },async(page)=>{if(action==="choose")await page.getByRole("button",{name:"预览方向 B",exact:true}).click();await actionButton(page,action).click();await page.getByRole('alert').filter({hasText:'操作资格待核对，请联系支持'}).waitFor();assert.equal(await page.getByText(/¥69/).count(),0);assert.ok(await actionButton(page,action).isEnabled());});
  });
}

test('Completed proposal survives reload, shows downloads, and does not generate again',async()=>{
 await withJob(completed,async()=>false,async(page)=>{await page.getByRole('heading',{name:'已生成的内容'}).waitFor();await page.reload();await page.getByRole('link',{name:/下载已完成的 \d+ 项/}).waitFor();assert.equal(await page.getByText('等待生成',{exact:true}).count(),0);});
});
