import * as readline from "node:readline";
import stringWidth from "string-width";
import { MenuSelection, getMenuWindow } from "../application/menu-selection.js";
import { fail } from "../api/protocol.js";

export type Item = { id: string; label: string; disabled?: boolean };
export class MenuExit extends Error {}
export function cleanText(value: string) {
  // Includes C1, bidi controls and line separators: provider-controlled labels
  // must not become terminal instructions, forged rows or invisible choices.
  return value.replace(/[\x00-\x1f\x7f-\x9f\u2028\u2029\u202a-\u202e\u2066-\u2069]/g, " ");
}
const segmenter = new Intl.Segmenter(undefined, { granularity: "grapheme" });
export function clipText(value: string, columns: number) {
  const text = cleanText(value), max = Math.max(0, columns);
  if (stringWidth(text) <= max) return text;
  let out = "", used = 0;
  for (const { segment } of segmenter.segment(text)) {
    const width = stringWidth(segment);
    if (used + width > max - 1) break;
    used += width; out += segment;
  }
  return max > 0 ? out + "…" : "";
}
export class TerminalMenu {
  private active = false;
  private wasRaw = false;
  private wasPaused = true;
  private keyHandler?: (text: string, key: readline.Key) => void;
  private draw = () => {};
  private stopWait?: () => void;
  readonly exit = new AbortController();
  private readonly stop = () => {
    this.exit.abort(); this.stopWait?.(); this.leave();
  };
  constructor(
    private readonly input: NodeJS.ReadStream = process.stdin,
    private readonly output: NodeJS.WriteStream = process.stdout,
    private readonly signals: NodeJS.Process = process,
  ) {}
  open() {
    if (!this.input.isTTY || !this.output.isTTY) fail("TTY_REQUIRED");
    this.signals.on("SIGINT", this.stop);
    this.signals.on("SIGTERM", this.stop);
    this.input.on("end", this.stop);
    this.input.on("close", this.stop);
    this.output.on("resize", this.redraw);
    this.wasRaw = this.input.isRaw ?? false;
    this.wasPaused = this.input.isPaused();
    readline.emitKeypressEvents(this.input);
    this.enter();
  }
  private readonly redraw = () => this.draw();
  private enter() {
    if (this.active || this.exit.signal.aborted) return;
    this.input.setRawMode(true); this.input.resume();
    this.output.write("\x1b[?1049h\x1b[?25l"); this.active = true;
  }
  private leave() {
    if (!this.active) return;
    this.input.setRawMode(this.wasRaw);
    if (this.wasPaused) this.input.pause();
    this.output.write("\x1b[?25h\x1b[?1049l"); this.active = false;
  }
  close() {
    if (this.keyHandler) this.input.off("keypress", this.keyHandler);
    this.signals.off("SIGINT", this.stop); this.signals.off("SIGTERM", this.stop);
    this.input.off("end", this.stop); this.input.off("close", this.stop);
    this.output.off("resize", this.redraw); this.draw = () => {};
    this.leave();
    // emitKeypressEvents/resume starts a TTY read handle even when the original
    // stream reported isPaused=false before ever being read. Close that handle
    // explicitly so an otherwise finished CLI exits instead of waiting forever.
    this.input.pause();
  }
  private frame(lines: string[], full = false) {
    if (!this.active) return;
    const columns = Math.max(1, (this.output.columns || 80) - 1);
    this.output.write("\x1b[H\x1b[2J" + lines.map(l => full ? l : clipText(l, columns)).join("\r\n"));
  }
  async select(title: string, items: readonly Item[], detail: string[] = []): Promise<string | undefined> {
    if (this.exit.signal.aborted) throw new MenuExit();
    const selection = new MenuSelection(items, {
      getKey: i => i.id, getSearchText: i => cleanText(i.label), isDisabled: i => !!i.disabled,
    });
    return new Promise((resolve, reject) => {
      const finish = (id?: string) => {
        if (this.keyHandler) this.input.off("keypress", this.keyHandler);
        this.keyHandler = undefined; this.stopWait = undefined; this.draw = () => {};
        if (this.exit.signal.aborted) reject(new MenuExit()); else resolve(id);
      };
      this.stopWait = () => finish();
      this.draw = () => {
        const rows = Math.max(1, this.output.rows || 24);
        const visible = selection.visibleItems;
        // On very small terminals keep the selected row rather than the header.
        const head = [title, ...detail, `搜索: ${selection.query}`];
        const footer = "↑↓ 选择 · 输入搜索 · Enter 确认 · Esc 返回 · Ctrl+C 退出";
        const reserved = rows >= 5 ? Math.min(head.length, rows - 3) : 0;
        const budget = Math.max(1, rows - reserved - (rows >= 3 ? 1 : 0) - 1);
        const win = getMenuWindow(visible.map(() => 1), selection.selectedIndex, budget);
        const list = visible.slice(win.startIndex, win.endIndex).map((i, offset) =>
          `${win.startIndex + offset === selection.selectedIndex ? "›" : " "} ${i.label}${i.disabled ? " [不可选]" : ""}`);
        this.frame([...head.slice(0, reserved), ...(list.length ? list : ["没有匹配项"]), ...(rows >= 3 ? [footer] : [])]);
      };
      this.keyHandler = (text, key) => {
        if (key.ctrl && key.name === "c") { this.stop(); return; }
        if (key.name === "escape") { finish(); return; }
        if (key.name === "return") {
          const activation = selection.requestActivation();
          if (activation.kind === "confirmed") finish(activation.item.id);
          return;
        }
        if (key.name === "up") selection.move(-1, true);
        else if (key.name === "down") selection.move(1, true);
        else if (key.name === "home") selection.moveToBoundary("first");
        else if (key.name === "end") selection.moveToBoundary("last");
        else if (key.name === "backspace") selection.setQuery([...selection.query].slice(0, -1).join(""));
        else if (!key.ctrl && !key.meta && text && !/[\x00-\x1f\x7f-\x9f]/.test(text))
          selection.setQuery((selection.query + text).slice(0, 256));
        this.draw();
      };
      this.input.on("keypress", this.keyHandler); this.draw();
    });
  }
  async notice(title: string, detail: string[]) {
    await this.select(title, [{ id: "back", label: "返回" }], detail);
  }
  async confirm(title: string) {
    return await this.select(title, [
      { id: "no", label: "取消" }, { id: "yes", label: "确认退出此连接" },
    ]) === "yes";
  }
  async task<T>(title: string, operation: (signal: AbortSignal, show: (lines: string[]) => void) => Promise<T>): Promise<T> {
    const cancel = new AbortController();
    const signal = AbortSignal.any([this.exit.signal, cancel.signal]);
    let lines = [title, "请稍候；Esc 取消，Ctrl+C 退出"];
    this.draw = () => this.frame(lines, true);
    this.keyHandler = (_text, key) => {
      if (key.ctrl && key.name === "c") this.stop();
      else if (key.name === "escape") cancel.abort();
    };
    this.input.on("keypress", this.keyHandler); this.draw();
    try {
      return await operation(signal, next => {
        // Only operator-generated instructions and an official auth URL are
        // shown here. No provider response body or exception text is rendered.
        lines = [title, ...next, "Esc 取消 · Ctrl+C 退出"]; this.draw();
      });
    } finally {
      this.input.off("keypress", this.keyHandler); this.keyHandler = undefined; this.draw = () => {};
    }
  }
  async handoff<T>(operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
    this.draw = () => {}; this.leave();
    // Paused Node input avoids consuming keystrokes intended for the native CLI.
    this.input.pause();
    try { return await operation(this.exit.signal); }
    finally { this.enter(); }
  }
}
