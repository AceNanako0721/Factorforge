import {
  test,
  expect,
  type Page,
} from "../../src/factorforge/operations/console/web/node_modules/@playwright/test/index.mjs";
async function login(page: Page) {
  // The flow page judges no module before its first read lands (S4-022).
  let hold!: () => void;
  const held = new Promise<void>((r) => (hold = r));
  await page.route("**/api/v2/console/overview?**", async (route) => {
    await held;
    await route.continue().catch(() => {});
  });
  await page.goto("/flow");
  await page.getByLabel("用户名", { exact: true }).fill("fixture-user");
  await page.getByLabel("密码", { exact: true }).fill("fixture-pass");
  await page.getByRole("button", { name: "进入管理台" }).click();
  // The only allowed instance opens by itself on the flow page.
  await expect(page.getByLabel("实时流程图")).toBeVisible();
  await expect(page.locator('.bd .blk[aria-label="模型研判"]')).toContainText("正在读取");
  await expect(page.getByText("未启用")).toHaveCount(0);
  hold();
  await expect(page.locator('.bd .blk[aria-label="模型研判"]')).toBeVisible();
}
async function fixture(page: Page, action: string) {
  await page.evaluate(
    (a) => fetch(`/fixture/state?action=${a}`, { method: "POST", headers: { Origin: location.origin } }),
    action,
  );
}

test("S4-022 flow page: modules, details on demand and stale data", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page);
  await fixture(page, "online");
  for (const title of ["新闻采集", "模型研判", "判断链", "信心池", "策略决策", "止损与阶梯", "执行器", "账户"])
    await expect(page.locator(`.bd .blk[aria-label="${title}"]`)).toBeVisible();
  // Details show only when a module is clicked, and go again.
  const panel = page.locator(".rp-insp");
  await expect(panel.getByText("synthetic thesis")).toHaveCount(0);
  await page.locator('.bd .blk[aria-label="模型研判"]').click();
  await expect(panel.getByText("synthetic thesis").first()).toBeVisible();
  await expect(panel.getByText("PRIVATE RAW")).toHaveCount(0);
  await page.locator('.bd .blk[aria-label="策略决策"]').click();
  // Parameters are read only (S4-022).
  await expect(panel.getByRole("button", { name: /修\s*改/ })).toBeDisabled();
  await page.getByLabel("实时流程图").click({ position: { x: 5, y: 5 } });
  await expect(panel.getByRole("button", { name: /修\s*改/ })).toHaveCount(0);
  // S4-014: the page speaks plain language, not status codes.
  expect(await page.locator("main").innerText()).not.toMatch(/\b[A-Z]{3,}_[A-Z_]{3,}\b/);
  await page.screenshot({ path: "../../../../../runtime/console-flow.png", fullPage: true });
  // The application listener stops answering: its modules say so.
  await fixture(page, "offline");
  await page.reload();
  await expect(page.getByText(/暂时读不到/).first()).toBeVisible({ timeout: 15_000 });
  await fixture(page, "online");
  expect(errors).toEqual([]);
});

// S4-024: synthetic replay records for one closed long batch, served over the
// fixture's own replay view so the selection and panel envelope stay real.
const replayData: Record<string, unknown> = {
  batches: [
    { batch_id: "batch-2d257f18aaaa", object_id: "OBJECT", direction: 1, unit_quantity: "4.85", budget: "25", opened_at: "2026-11-10T06:45:37Z", closed_at: "2026-11-10T07:20:00Z", exit_kind: "TARGET", net_pnl: "11.2", r: "-0.45" },
    { batch_id: "batch-5c0ffee0aaaa", object_id: "OBJECT", direction: 1, unit_quantity: "1", budget: "20", opened_at: "2026-11-10T08:00:00Z", closed_at: null, exit_kind: "", net_pnl: "0", r: "0" },
  ],
  "dynamic.plans": { items: [{ plan_id: "p1", at: "2026-11-10T06:40:05Z", kind: "INITIAL", direction: "LONG", confidence: 3, risk_fraction: "0.005", distance: "5", close_distance: "4.5", price: "50", equity: "5000", tier: 4, requested_tier: 24, session: "OVERNIGHT", r: "0.09", buffer: "0.01", trail: "0.04" }] },
  "dynamic.actions": { items: [{ action_id: "open-1", at: "2026-11-10T06:55:05Z", kind: "OPEN", direction: "LONG", entry: "50.2", close_stop: "45.68" }] },
  "dynamic.judgments": { items: [{ at: "2026-11-10T06:31:11Z", ok: true, direction: "LONG", confidence: 3, horizon_hours: 24 }] },
  fills: [{ happened_at: "2026-11-10T06:51:13Z", side: "BUY", quantity: "3.88", price: "50.2" }],
};
function replayDecisions(objectId: string) {
  const d = (at: string, lv: number, cb: string, tg: string, ac: string, rc: string[]) => ({
    decision: { decision_id: "d-" + at, object_id: objectId, available_cutoff: at, new_level: lv, pool_net: cb, pool_plus: cb, pool_minus: "0", components: { baseline: "0", core: "0", adjustment: cb, combined: cb }, target_quantity: tg, actual_quantity: ac, reason_codes: rc, stop_plan: null, input_snapshot: {} },
    inputs: { equity: "5000", price: "50", pool_quality: "1", contributions: 1 },
    delivery_state: "ACK", delivery_reason: null,
  });
  return {
    items: [d("2026-11-10T06:40:00Z", 3, "0.7", "0", "0", ["RISK_UNIT_DIRECTIVE_MISSING"]), d("2026-11-10T06:45:37Z", 3, "0.7", "3.88", "0", ["STOP_DIRECTIVE_SIZED"]), d("2026-11-10T06:55:00Z", 3, "0.7", "3.88", "3.88", ["NO_TRADE_BAND"]), d("2026-11-10T07:05:38Z", 4, "1", "4.85", "3.88", ["STOP_DIRECTIVE_SIZED"]), d("2026-11-10T07:15:00Z", 0, "0", "0", "4.85", [])],
    cursor: "",
    sizing: { max_risk_fraction: "0.03", object_loss_budget: "60", fee_rate: "0.0005", slippage_fraction: "0.0004", gap_fraction: "0.002", band_ratio_of_unit: "0.25", levels: [0.3, 0.6, 0.8, 1].map((x, i) => ({ exposure: String(x), enter: String(0.2 * (i + 1) + 0.05), exit: String(0.2 * (i + 1) - 0.05) })) },
  };
}

test("S4-022 flow page: drill down to the formula and replay an order", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const limits: number[] = [];
  await page.route("**/api/v2/console/replay?**", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    const objectId = body.selection.object_id as string;
    const url = new URL(route.request().url());
    // A decision page over the response budget is asked again with a smaller limit.
    if (url.searchParams.get("source") === "decision-history") {
      const limit = Number(url.searchParams.get("limit"));
      limits.push(limit);
      if (limit >= limits[0] && limits[0] > 10) {
        for (const p of body.panels) Object.assign(p, { state: "UNAVAILABLE", code: "LOWER_RESPONSE_BUDGET", data: {}, cursor: null });
        await route.fulfill({ response, json: body });
        return;
      }
    }
    for (const p of body.panels) {
      const data = p.source === "decision-history" ? replayDecisions(objectId) : p.source === "batches" ? (replayData.batches as { object_id: string }[]).map((b) => ({ ...b, object_id: objectId })) : replayData[p.source];
      Object.assign(p, { state: "AVAILABLE", code: null, data, cursor: null });
    }
    await route.fulfill({ response, json: body });
  });
  await login(page);
  // The open order shows the account's floating P&L.
  await expect(page.locator(".rp-row").filter({ hasText: "5c0ffee0" })).toContainText("浮动");
  // The order list holds the batch; picking it plays from the opening decision.
  const row = page.locator(".rp-row").filter({ hasText: "2d257f18" });
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.locator(".rp-mode")).toContainText("回放 · 已平仓订单");
  await expect(page.locator(".tl-time")).toBeVisible();
  // Playback ends on the last round.
  await expect(page.locator(".tl-note").filter({ hasText: "第 5 / 5 轮" })).toBeVisible({ timeout: 15_000 });
  expect(limits.some((l) => l < limits[0])).toBe(true);
  // Drill down: decision → unit quantity → the rounding step's formula.
  await page.locator('.bd .blk[aria-label="策略决策"]').dblclick();
  await expect(page.locator(".rp-crumbs")).toContainText("策略决策");
  await page.locator('.bd .blk[aria-label="算单位数量 Q"]').dblclick();
  await page.locator('.bd .blk[aria-label="向零取整到 0.01"]').dblclick();
  await expect(page.locator(".fp-card")).toContainText("Q = ⌊B ÷ U⌋");
  await expect(page.locator(".fp-port").filter({ hasText: "单位数量 Q" })).toContainText("4.85");
  // Back to the top through the path.
  await page.locator(".rp-crumbs button").first().click();
  await expect(page.getByLabel("实时流程图")).toBeVisible();
  expect(errors).toEqual([]);
});

test("S4-022 market page: totals, conviction, trades and folded records", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const seen: string[] = [];
  page.on("request", (r) => seen.push(r.url()));
  await login(page);
  await fixture(page, "online");
  await page.getByRole("link", { name: "行情与交易", exact: true }).click();
  await expect(page.getByText("权益（USDT）", { exact: true })).toBeVisible();
  await expect(page.getByText("当前信心", { exact: true })).toBeVisible();
  await expect(page.getByText("成交单", { exact: true })).toBeVisible();
  await expect(page.getByRole("img", { name: "已记录最终K线" })).toBeVisible();
  // The message lists stay folded until opened.
  await expect(page.getByText("synthetic thesis", { exact: true })).toBeHidden();
  await page.getByText(/^研判记录/).click();
  await expect(page.getByText("synthetic thesis", { exact: true })).toBeVisible();
  await expect(page.getByText("PRIVATE RAW")).toHaveCount(0);
  // S4-021: the range buttons read the longer history and marks.
  for (const label of ["3 天", "7 天", "全部", "24 小时"]) {
    await page.getByText(label, { exact: true }).click();
    await expect(page.locator("canvas").first()).toBeVisible();
    await page.waitForTimeout(600);
  }
  // S4-021: the chosen range is highlighted; S4-019: the price says where it comes from.
  await expect(page.locator(".ant-card .ant-segmented-item-selected")).toHaveText("24 小时");
  await expect(page.getByText(/^(实时参考价|最新收盘)$/)).toBeVisible();
  expect(seen.some((u) => u.includes("source=dynamic.judgments") && u.includes("from="))).toBe(true);
  expect(seen.some((u) => u.includes("source=candles") && u.includes("interval=5m"))).toBe(true);
  expect(seen.some((u) => u.includes("source=protections"))).toBe(true);
  // The wheel over the price axis zooms prices; the page does not scroll.
  const host = page.getByLabel("已记录最终K线");
  await host.scrollIntoViewIfNeeded();
  const box = (await host.boundingBox())!;
  const before = await page.evaluate(() => window.scrollY);
  await page.mouse.move(box.x + box.width - 20, box.y + box.height / 2);
  await page.mouse.wheel(0, -300);
  expect(await page.evaluate(() => window.scrollY)).toBe(before);
  await page.screenshot({ path: "../../../../../runtime/console-market-1440.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 900 });
  await expect(page.getByText("当前信心", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(1);
  await page.screenshot({ path: "../../../../../runtime/console-market-390.png", fullPage: true });
  expect(errors).toEqual([]);
});

test("S4-003 display zone and theme stay in the browser; nothing else is stored", async ({ page }) => {
  await login(page);
  await page.getByLabel("显示时区").click();
  await page.getByTitle("东京").click();
  await page.getByRole("button", { name: "切换浅色或深色" }).click();
  await page.reload();
  await expect(page.locator(".ant-select").filter({ hasText: "东京" })).toBeVisible();
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }));
  expect(stored).not.toContain("fixture");
  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("button", { name: "进入管理台" })).toBeVisible();
});

// Runs last: the fixture cannot restore a revoked selection.
test("revocation clears the instance and its data", async ({ page }) => {
  await login(page);
  await fixture(page, "revoke");
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await expect(page.getByText("没有可查看的实例")).toBeVisible({ timeout: 20_000 });
  await expect(page.locator(".bd .blk")).toHaveCount(0);
});
