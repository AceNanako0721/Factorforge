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
