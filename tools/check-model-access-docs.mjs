// Offline visual verification. Uses the console's existing Playwright dependency;
// artifacts go to ignored runtime, and no account or model service is contacted.
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { createRequire } from "node:module";

const root = path.resolve(import.meta.dirname, "..");
const manifest = JSON.parse(fs.readFileSync(path.join(root, "doc/releases.json"), "utf8"));
const base = path.resolve(root, manifest.releases.at(-1).directory);
if (!base.startsWith(path.join(root, "doc") + path.sep)) throw new Error("Invalid baseline");
const require = createRequire(path.join(root, "src/factorforge/applications/console/web/package.json"));
const { chromium } = require("@playwright/test");
const out = path.join(root, "runtime/model-access-review");
fs.mkdirSync(out, { recursive: true });
const browser = await chromium.launch({ headless: true, args: ["--disable-gpu"] });
const report = [];
try {
  for (const width of [1440, 390]) {
    for (const name of fs.readdirSync(base).filter((n) => n.endsWith(".html"))) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } });
      await page.route(/^https?:/, (route) => route.abort());
      await page.goto(pathToFileURL(path.join(base, name)).href);
      const state = await page.evaluate(() => ({
        h1: document.querySelector("h1")?.textContent,
        overflow: document.documentElement.scrollWidth > innerWidth,
        svg: document.querySelectorAll("svg").length,
        article: !!document.querySelector("article"),
      }));
      if (state.overflow || !state.article) throw new Error(name + ": invalid layout");
      report.push({ name, width, ...state });
      if (name.startsWith("05_") || name === "index.html") {
        await page.screenshot({ path: path.join(out, `${width}-${name}.png`) });
      }
      await page.close();
    }
  }
} finally {
  await browser.close();
}
fs.writeFileSync(path.join(out, "report.json"), JSON.stringify(report, null, 2));
console.log(JSON.stringify({ checks: report.length, overflow: false, external_requests: false }));
