import { fail } from "../api/protocol.js";
// Observe framing/terminal metadata only. OMP remains the content parser.
export class SSEGuard {
  private readonly decoder = new TextDecoder("utf-8", { fatal: true });
  private pending = "";
  private data: string[] = [];
  private bytes = 0;
  terminal = false;
  readonly requiresTerminal: boolean;
  constructor(api: string, private readonly max: number) {
    this.requiresTerminal = ["openai-completions", "openai-responses", "openai-codex-responses", "anthropic-messages",
      "google-generative-ai", "google-gemini-cli"].includes(api);
  }
  feed(value?: Uint8Array) {
    this.bytes += value?.byteLength ?? 0;
    if (this.bytes > this.max) fail("PROVIDER_ENVELOPE_LIMIT", true);
    try { this.pending += this.decoder.decode(value, { stream: !!value }); }
    catch { fail("DELIVERY_UNKNOWN", true); }
    let at: number;
    while ((at = this.pending.indexOf("\n")) >= 0) {
      this.line(this.pending.slice(0, at).replace(/\r$/, "")); this.pending = this.pending.slice(at + 1);
    }
    if (!value) { if (this.pending) this.line(this.pending); this.pending = ""; this.frame(); }
  }
  private line(line: string) {
    if (!line) this.frame();
    else if (line.startsWith("data:")) this.data.push(line.slice(5).replace(/^ /, ""));
  }
  private frame() {
    if (!this.data.length) return;
    const raw = this.data.join("\n"); this.data = [];
    if (raw === "[DONE]") return;
    let e: Record<string, any>;
    try { e = JSON.parse(raw); } catch { fail("DELIVERY_UNKNOWN", true); }
    if (!e || typeof e !== "object") fail("DELIVERY_UNKNOWN", true);
    if (String(e.type ?? "").startsWith("response.refusal") || e.delta?.refusal || e.stop_details?.type === "refusal") fail("RESPONSE_REFUSED");
    if (e.type === "response.failed" || e.type === "response.incomplete") fail("INCOMPLETE_RESPONSE", true);
    if (e.type === "response.completed") {
      if (e.response?.status && e.response.status !== "completed") fail("INCOMPLETE_RESPONSE", true);
      for (const item of e.response?.output ?? []) for (const c of item.content ?? []) if (c.type === "refusal") fail("RESPONSE_REFUSED");
      this.terminal = true;
    }
    if (e.type === "message_delta" && e.delta?.stop_reason) {
      if (["refusal", "sensitive"].includes(e.delta.stop_reason) || e.delta.stop_details?.type === "refusal") fail("RESPONSE_REFUSED");
      this.terminal = true;
    }
    for (const c of e.choices ?? []) {
      if (c.delta?.refusal || c.message?.refusal) fail("RESPONSE_REFUSED");
      if (c.finish_reason) this.terminal = true;
    }
    const google = e.response ?? e;
    if (google.promptFeedback?.blockReason) fail("RESPONSE_REFUSED");
    for (const c of google.candidates ?? []) if (c.finishReason) {
      if (["SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII"].includes(c.finishReason)) fail("RESPONSE_REFUSED");
      this.terminal = true;
    }
  }
}
