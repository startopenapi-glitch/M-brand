// Browser-level regression for the locked job's two unlock paths.
// Uses local Vite and mocked APIs only; never reaches real payment or generation.
import assert from "node:assert/strict";
import { before, after, test } from "node:test";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const origin = "http://127.0.0.1:4273";
let server, browser, output = "";

const locked = {
  id: "LOCK-E2E",
  status: "waiting_unlock",
  stage: "unlock",
  directions_unlocked: false,
  unlock_price_cents: 990,
  images: [],
  progress: "等待解锁",
  delivery: { observed_at: new Date().toISOString(), needs_review: false, steps: [] },
};
const directions = {
  ...locked,
  status: "queued",
  stage: "directions",
  directions_unlocked: true,
  progress: "准备方向",
};

before(async () => {
  server = spawn(process.execPath, [resolve(webDir, "node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", "4273", "--strictPort"], { cwd: webDir, stdio: ["ignore", "pipe", "pipe"] });
  server.stdout.on("data", (chunk) => { output += chunk; });
  server.stderr.on("data", (chunk) => { output += chunk; });
  let ready = false;
  for (let i = 0; i < 80; i++) {
    if (server.exitCode !== null) throw new Error(output);
    try { ready = (await fetch(origin)).ok; } catch {}
    if (ready) break;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  assert.ok(ready, output);
  browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_EXECUTABLE });
});

after(async () => {
  await browser?.close();
  server?.kill("SIGTERM");
});

async function withLocked(handler, check) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  page.setDefaultTimeout(4000);
  const model = { state: structuredClone(locked), posts: [], errors: [] };
  page.on("pageerror", (error) => model.errors.push(error.message));
  const json = (route, body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  await context.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) return route.abort();
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (await handler({ route, request, path: url.pathname, model, json })) return;
    if (url.pathname === "/api/jobs/LOCK-E2E" && request.method() === "GET") return json(route, model.state);
    if (url.pathname === "/api/jobs/LOCK-E2E/detail") return json(route, { items: [], bundle_price: 9900 });
    return json(route, { error: `Unexpected mock request: ${request.method()} ${url.pathname}` }, 500);
  });
  try {
    await page.goto(origin + "/job/LOCK-E2E");
    await page.getByRole("heading", { level: 1, name: "解锁自己的品牌" }).waitFor();
    await check(page, model);
    assert.deepEqual(model.errors, []);
  } finally {
    await context.close();
  }
}

test("locked job: invite code is uppercased and unlocks the task", async () => {
  await withLocked(async ({ route, request, path, model, json }) => {
    if (path === "/api/jobs/LOCK-E2E/unlock-code" && request.method() === "POST") {
      model.posts.push(request.postDataJSON());
      model.state = structuredClone(directions);
      await json(route, { ok: true, directions_unlocked: true, stage: "directions", status: "queued" });
      return true;
    }
  }, async (page, model) => {
    await page.getByRole("button", { name: "解锁自己的品牌", exact: true }).click();
    await page.getByRole("button", { name: "使用邀请码", exact: true }).click();
    await page.getByLabel("邀请码", { exact: true }).fill("test-code");
    await page.getByRole("button", { name: "立即解锁", exact: true }).click();
    await page.getByRole("heading", { level: 1, name: "正在制作 Logo 方向" }).waitFor();
    assert.deepEqual(model.posts, [{ code: "TEST-CODE" }]);
    assert.equal(await page.getByRole("dialog").count(), 0);
  });
});

test("locked job: payment option opens checkout without creating a code unlock", async () => {
  await withLocked(async ({ route, request, path, model, json }) => {
    if (path === "/api/jobs/LOCK-E2E/unlock" && request.method() === "POST") {
      model.posts.push(request.postDataJSON());
      await json(route, {
        id: "UNLOCK-E2E", job_id: "LOCK-E2E", amount_cents: 990, item_count: 1,
        status: "pending", checkout_state: "ready",
        qr: "data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='100' height='100'><text x='4' y='50'>MOCK</text></svg>",
      }, 201);
      return true;
    }
  }, async (page, model) => {
    await page.getByRole("button", { name: "解锁自己的品牌", exact: true }).click();
    await page.getByRole("button", { name: "生成支付二维码", exact: true }).click();
    await page.getByRole("dialog", { name: /微信支付/ }).waitFor();
    assert.equal(model.posts.length, 1);
    assert.equal(await page.getByText("订单号 UNLOCK-E2E").count(), 1);
  });
});

test("locked job: invalid invite code stays in the dialog with a visible error", async () => {
  await withLocked(async ({ route, request, path, json }) => {
    if (path === "/api/jobs/LOCK-E2E/unlock-code" && request.method() === "POST") {
      await json(route, { error: "邀请码无效、已使用或不属于本设备" }, 403);
      return true;
    }
  }, async (page) => {
    await page.getByRole("button", { name: "解锁自己的品牌", exact: true }).click();
    await page.getByRole("button", { name: "使用邀请码", exact: true }).click();
    await page.getByLabel("邀请码", { exact: true }).fill("BADCODE");
    await page.getByRole("button", { name: "立即解锁", exact: true }).click();
    await page.getByRole("alert").filter({ hasText: "邀请码无效" }).waitFor();
    assert.equal(await page.getByRole("dialog").count(), 1);
  });
});
