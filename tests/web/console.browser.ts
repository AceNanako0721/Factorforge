import {
  test,
  expect,
  type Page,
} from "../../src/factorforge/applications/console/web/node_modules/@playwright/test/index.mjs";
async function login(page: Page) {
  await page.goto("/overview");
  await page.getByLabel("用户名", { exact: true }).fill("fixture-user");
  await page.getByLabel("密码", { exact: true }).fill("fixture-pass");
  await page.getByRole("button", { name: "进入管理台" }).click();
  await page.getByLabel("查看范围").selectOption("fixture-selection");
  await expect(
    page.getByRole("heading", { name: "总览", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "账户事实", exact: true }),
  ).toBeVisible();
}
test("native SIM facts, detail, final candles, export, failures and logout", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page);
  await expect(page.locator(".environment")).toHaveText("SIM");
  await page.screenshot({
    path: "../../../../../runtime/console-desktop.png",
    fullPage: true,
  });
  await page.getByRole("link", { name: "执行与账务", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "实际成交", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("FILLED · 已成交", { exact: true }).first(),
  ).toBeVisible();
  await page.getByRole("link", { name: "事件与评分", exact: true }).click();
  await page.getByRole("button", { name: "详情", exact: true }).first().click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page
    .getByRole("button", { name: "evidence-fixture", exact: true })
    .click();
  await expect(
    page.getByText("Synthetic original <img src=x onerror=alert(1)>", {
      exact: true,
    }),
  ).toBeVisible();
  expect(await page.locator('img[src="x"]').count()).toBe(0);
  await page.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("link", { name: "行情", exact: true }).click();
  await page.getByLabel("周期", { exact: true }).selectOption("1m");
  await page
    .getByLabel("起始 UTC", { exact: true })
    .fill("2026-01-05T00:00:00Z");
  await page
    .getByLabel("结束 UTC", { exact: true })
    .fill("2026-01-05T00:10:00Z");
  await page.getByRole("button", { name: "应用范围" }).click();
  await expect(page.getByRole("img", { name: "已记录最终K线" })).toBeVisible();
  await page.getByRole("link", { name: "总览", exact: true }).click();
	await page.getByRole("link", { name: "周期报告", exact: true }).click();
	await expect(page.getByText("fixture-periodic-report", {exact:true}).first()).toBeVisible();
	await page.getByText("7 个字段", {exact:true}).first().click();
	await expect(page.getByText("未知或未成熟比例", {exact:true})).toBeVisible();
	await expect(page.getByText("0.5", {exact:true})).toBeVisible();
	await page.screenshot({path:"../../../../../runtime/console-report-detail.png",fullPage:true});
	await page.getByRole("link", { name: "总览", exact: true }).click();
  await page.locator("summary").filter({ hasText: "导出" }).click();
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "JSON", exact: true }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("factorforge-overview.json");
  await page.evaluate(() =>
    fetch("/fixture/state?action=offline", {
      method: "POST",
      headers: { Origin: location.origin },
    }),
  );
  await page.getByRole("button", { name: "刷新", exact: false }).click();
  await expect(page.getByText("STALE · 陈旧结果")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "账户事实", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "退出", exact: true }).click();
  await expect(page.getByRole("button", { name: "进入管理台" })).toBeVisible();
  const stored = await page.evaluate(() => ({ ...localStorage }));
  expect(JSON.stringify(stored)).not.toContain("fixture");
  expect(errors).toEqual([]);
});
test("a late response cannot restore data after deselection", async ({
  page,
}) => {
  await login(page);
  let release!: () => void;
  let received!: () => void;
  const waiting = new Promise<void>((resolve) => {
    release = resolve;
  });
  const arrived = new Promise<void>((resolve) => {
    received = resolve;
  });
  await page.route("**/api/v2/console/overview?**", async (route) => {
    const response = await route.fetch();
    received();
    await waiting;
    await route.fulfill({ response }).catch(() => {});
  });
  await page.getByRole("button", { name: "刷新", exact: false }).click();
  await arrived;
  await page.getByLabel("查看范围").selectOption("");
  await expect(
    page.getByRole("heading", { name: "先选择允许查看的运行" }),
  ).toBeVisible();
  release();
  await page.unroute("**/api/v2/console/overview?**");
  await expect(
    page.getByRole("heading", { name: "账户事实", exact: true }),
  ).toHaveCount(0);
  expect(
    await page.evaluate(() => JSON.stringify({ ...localStorage })),
  ).not.toContain("fixture");
});

test("S4-017 judgment and stop page on desktop and phone width", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page);
  await page.evaluate(() =>
    fetch("/fixture/state?action=online", {
      method: "POST",
      headers: { Origin: location.origin },
    }),
  );
  await page.getByRole("link", { name: "研判与止损", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "三层止损与价格", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("收盘止损", { exact: true })).toBeVisible();
  // v2.19.0: the ladder's next rung and what set the close stop are listed.
  await expect(page.getByText("阶梯第 2 级触发价", { exact: true })).toBeVisible();
  await expect(page.getByText("来自开仓时的初始止损 · 阶梯第 1 级", { exact: true })).toBeVisible();
  await expect(page.getByText(/阶梯步长 4\.0% · 当前第 1 级/)).toBeVisible();
  await expect(page.getByText("synthetic thesis", { exact: true })).toBeVisible();
  await expect(page.getByText("注入研判 · 做空", { exact: true })).toBeVisible();
  // v2.16.0: the timeline says which judgment changed the position inputs.
  await expect(page.getByText("注入（首次方向）", { exact: true })).toBeVisible();
  await expect(page.getByText("仅记录，未改变仓位", { exact: true })).toBeVisible();
  await page.getByLabel("显示未生效的研判（灰点）").check();
  await page.getByLabel("显示未生效的研判（灰点）").uncheck();
  await expect(page.getByText("未实现", { exact: true })).toBeVisible();
  await expect(page.getByText("PRIVATE RAW")).toHaveCount(0);
  // S4-020: the fixture run is internal matching, so the PnL block shows the
  // real-funding total with system-computed sources only.
  const pnl = page.getByLabel("持仓盈亏");
  await expect(pnl.getByRole("heading", { name: "持仓盈亏", exact: true })).toBeVisible();
  await expect(pnl.getByText("按真实资金费合计", { exact: true })).toBeVisible();
  await expect(pnl.getByText("系统计算").first()).toBeVisible();
  await expect(pnl.getByText("交易所", { exact: true })).toHaveCount(0);
  await expect(pnl.getByRole("cell", { name: /^累计/ })).toBeVisible();
  // No settlement record in the fixture: the reference is "not obtained", not 0.
  await expect(pnl.getByText("真实资金费尚未取得", { exact: true })).toBeVisible();
  // S4-019 reference layer: pushed values arrive and keep changing.
  const mark = page.getByLabel("参考标记价");
  await expect(mark.locator("strong")).toHaveText(/^100\.\d+$/);
  await expect(mark.locator("small")).toContainText("实时");
  const first = await mark.locator("strong").innerText();
  await expect(mark.locator("strong")).not.toHaveText(first);
  await expect(page.getByText("参考标记价", { exact: true })).toBeVisible();
  await expect(page.getByLabel("参考浮动盈亏").locator("strong")).not.toHaveText("—");
  // v2.14.8: the wheel over the price axis zooms prices and the page does not
  // scroll; the toggle and reset only change the view.
  const host = page.getByLabel("已记录最终K线");
  await host.scrollIntoViewIfNeeded();
  const box = (await host.boundingBox())!;
  const before = await page.evaluate(() => window.scrollY);
  await page.mouse.move(box.x + box.width - 20, box.y + box.height / 2);
  await page.mouse.wheel(0, -300);
  expect(await page.evaluate(() => window.scrollY)).toBe(before);
  await page.mouse.move(box.x + box.width / 3, box.y + box.height / 3);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 8 });
  await page.mouse.up();
  await page.getByLabel("纵轴包含全部止损线").check();
  await page.getByRole("button", { name: "纵轴自动" }).click();
  await page.getByLabel("纵轴包含全部止损线").uncheck();
  await page.screenshot({
    path: "../../../../../runtime/console-dynamic-1440.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 900 });
  await expect(
    page.getByRole("heading", { name: "三层止损与价格", exact: true }),
  ).toBeVisible();
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
  await page.screenshot({
    path: "../../../../../runtime/console-dynamic-390.png",
    fullPage: true,
  });
  expect(errors).toEqual([]);
});
test("narrow keyboard navigation and revocation clears selected data", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await page.screenshot({
    path: "../../../../../runtime/console-narrow.png",
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.getByRole("button", { name: "展开导航" }).click();
  await page.getByRole("link", { name: "采集与运维", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("heading", { name: "采集与运维", exact: true }),
  ).toBeVisible();
  await page.evaluate(() =>
    fetch("/fixture/state?action=revoke", {
      method: "POST",
      headers: { Origin: location.origin },
    }),
  );
  await page.getByRole("button", { name: "刷新", exact: false }).click();
  await expect(
    page.getByRole("heading", { name: "先选择允许查看的运行" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "账户事实", exact: true }),
  ).toHaveCount(0);
});
