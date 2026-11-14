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
  await expect(page.locator(".flow-node").filter({ hasText: "模型研判" })).toContainText("正在读取");
  await expect(page.getByText("未启用")).toHaveCount(0);
  hold();
  await expect(page.locator(".flow-node").filter({ hasText: "模型研判" })).toBeVisible();
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
    await expect(page.locator(".flow-node").filter({ hasText: title })).toBeVisible();
  // Details open only when a module is clicked, and close again.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.locator(".flow-node").filter({ hasText: "模型研判" }).click();
  const drawer = page.getByRole("dialog");
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText("synthetic thesis").first()).toBeVisible();
  await expect(drawer.getByText("PRIVATE RAW")).toHaveCount(0);
  // Parameters are read only (S4-022).
  await expect(drawer.getByRole("button", { name: /修\s*改/ })).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
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

test("S4-022 flow page: refreshes keep every module drawn", async ({ page }) => {
  // React Flow hides a node it has not measured; a refresh must not drop the
  // measured size, or a module vanishes until it is measured again.
  await page.route("**/api/v2/console/capabilities**", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...(await response.json()), refresh_seconds: 1 } });
  });
  await login(page);
  await fixture(page, "online");
  await expect(page.locator(".react-flow__node")).toHaveCount(9);
  await expect(page.locator(".flow-node").filter({ hasText: "正在读取" })).toHaveCount(0);
  const hidden = await page.evaluate(async () => {
    const nodes = [...document.querySelectorAll<HTMLElement>(".react-flow__node")];
    const seen = new Set<string>();
    const watch = new MutationObserver((records) => {
      for (const r of records) {
        const node = r.target as HTMLElement;
        if (node.style.visibility === "hidden") seen.add(node.dataset.id ?? "");
      }
    });
    for (const n of nodes) watch.observe(n, { attributes: true, attributeFilter: ["style"] });
    await new Promise((r) => setTimeout(r, 5000));
    watch.disconnect();
    return [...seen];
  });
  expect(hidden).toEqual([]);
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
  await expect(page.locator(".flow-node")).toHaveCount(0);
});
