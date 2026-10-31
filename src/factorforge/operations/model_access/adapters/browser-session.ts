// Adapted from oh-my-pi b07a1c146d0d12cfc855a2c65d52f892ef319040
// packages/coding-agent/src/utils/browser-session.ts (MIT).
// Copyright (c) 2025 Mario Zechner; Copyright (c) 2026 Stencil Labs, Inc.
// Copyright (c) 2025-2026 Can Bölük. Full MIT notice: THIRD_PARTY_NOTICES.md.
// Host differences: explicit browser, canonical private runtime, configured
// lifetime, no logging/downloads/dynamic imports, cleanup only owned resources.
import fs from "node:fs/promises";
import path from "node:path";
import puppeteer, { type Browser } from "puppeteer-core";
import type { OAuthBrowserSessionRequest } from "@oh-my-pi/pi-ai/oauth";
import { ConfigStore } from "../config/store.js";
import { privateRuntime } from "../config/omp-runtime.js";
import { fail } from "../api/protocol.js";
import { onAbort } from "./omp-access.js";

export async function captureBrowserSession(config: ConfigStore, request: OAuthBrowserSessionRequest, signal: AbortSignal) {
  signal.throwIfAborted();
  if (new URL(request.url).protocol !== "https:" || !request.cookieNames.length) fail("LOGIN_FAILED");
  const executablePath = config.settings.omp_browser_path;
  if (!executablePath || !path.isAbsolute(executablePath)) fail("BROWSER_CONFIGURATION_REQUIRED");
  await fs.access(executablePath);
  if (process.platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY) fail("BROWSER_DISPLAY_REQUIRED");
  const root = await privateRuntime(config.file);
  const profile = await fs.mkdtemp(path.join(root, "login-profile-"));
  const closed = new AbortController(), lifetime = AbortSignal.any([signal, closed.signal]);
  let browser: Browser | undefined;
  try {
    // Keep launch ownership even if ESC arrives before Chromium returns.
    browser = await puppeteer.launch({ executablePath, headless: false, defaultViewport: null, pipe: true,
      userDataDir: profile, signal,
      ignoreDefaultArgs: ["--no-sandbox", "--disable-setuid-sandbox", "--ignore-certificate-errors"],
      timeout: config.settings.timeout_seconds * 1000 });
    lifetime.throwIfAborted();
    browser.once("disconnected", () => closed.abort());
    const context = await onAbort(browser.createBrowserContext(), lifetime);
    const page = await onAbort(context.newPage(), lifetime);
    page.once("close", () => closed.abort());
    await onAbort(page.goto(request.url, { waitUntil: "domcontentloaded", timeout: config.settings.timeout_seconds * 1000 }), lifetime);
    await onAbort(page.bringToFront(), lifetime);
    const cdp = await onAbort(page.createCDPSession(), lifetime);
    while (true) {
      const { cookies } = await onAbort(cdp.send("Network.getCookies", { urls: [request.url] }), lifetime);
      lifetime.throwIfAborted();
      for (const name of request.cookieNames) {
        const cookie = cookies.find(c => c.name === name && c.value);
        if (cookie) return cookie.value;
      }
      await onAbort(new Promise(resolve => setTimeout(resolve, 250)), lifetime);
    }
  } catch {
    if (signal.aborted) fail("LOGIN_CANCELLED");
    fail(closed.signal.aborted ? "LOGIN_WINDOW_CLOSED" : "BROWSER_LOGIN_FAILED");
  } finally {
    if (browser) {
      try { await onAbort(browser.close(), AbortSignal.timeout(5000)); }
      catch { browser.process()?.kill("SIGKILL"); }
    }
    await fs.rm(profile, { recursive: true, force: true });
  }
}
