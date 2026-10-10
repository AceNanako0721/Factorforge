// Pre-design experiment: synthetic credentials only; never load operator config.
// Copy into runtime/omp-reuse-lab after installing its isolated pinned dependencies.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { AuthStorage } from '@oh-my-pi/pi-ai/auth-storage';
import { getOAuthProviders, registerOAuthProvider } from '@oh-my-pi/pi-ai/oauth';
import { getBundledModels } from '@oh-my-pi/pi-catalog';
import { complete } from '@oh-my-pi/pi-ai';
import { logger } from '@oh-my-pi/pi-utils';
import { OAuthSelectorComponent } from '@oh-my-pi/pi-tui/overlays/oauth-selector';
import { LoginDialogComponent } from '@oh-my-pi/pi-tui/overlays/login-dialog';
import { initThemeSync } from '@oh-my-pi/pi-tui/theme';
logger.setTransports({ console: false, file: false });
initThemeSync();
const file = path.resolve('synthetic-auth.json');
class Store {
  rows = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : [];
  persist() { fs.writeFileSync(file, JSON.stringify(this.rows), { mode: 0o600 }); }
  close() {}
  listAuthCredentials(provider) { return structuredClone(this.rows.filter(r => !provider || r.provider === provider)); }
  updateAuthCredential(id, credential) { this.rows.find(r => r.id === id).credential = credential; this.persist(); }
  async deleteAuthCredential(id) { const row = this.rows.find(r => r.id === id); if (!row) return false; row.disabledCause = 'logout'; this.persist(); return true; }
  tryDisableAuthCredentialIfMatches(id, data, cause) { const row = this.rows.find(r => r.id === id); if (!row || JSON.stringify(row.credential) !== data) return false; row.disabledCause = cause; this.persist(); return true; }
  tryUpdateAuthCredentialIfMatches(id, data, credential) { const row = this.rows.find(r => r.id === id); if (!row) return false; const { type, ...rest } = row.credential; if (JSON.stringify(rest) !== data) return false; this.updateAuthCredential(id, credential); return true; }
  async replaceAuthCredentials(provider, credentials) { this.rows = this.rows.filter(r => r.provider !== provider); for (const credential of credentials) await this.upsertAuthCredential(provider, credential); return this.listAuthCredentials(provider); }
  async upsertAuthCredential(provider, credential) { this.rows.push({ id: Math.max(0, ...this.rows.map(r => r.id)) + 1, provider, credential, disabledCause: null }); this.persist(); return this.listAuthCredentials(provider); }
  async deleteAuthCredentials(provider) { for (const row of this.rows.filter(r => r.provider === provider)) row.disabledCause = 'logout'; this.persist(); }
  getCache() { return null; } setCache() {} cleanExpiredCache() {}
}
fs.rmSync(file, { force: true });
const providers = getOAuthProviders();
assert.equal(providers.length, 82);
for (const id of ['anthropic', 'openai-codex', 'google-antigravity']) assert(providers.some(p => p.id === id));
let selected, cancelled = false;
const picker = new OAuthSelectorComponent('login', { credentials: { has: () => false }, keys: { source: () => undefined } }, id => selected = id, () => cancelled = true);
picker.handleInput('Antigravity'); picker.handleInput('\r');
assert.equal(selected, 'google-antigravity');
picker.handleInput('\x1b'); assert(cancelled); picker.stopValidation();
assert(picker.render(100).length); assert(picker.render(32).length);
const dialog = new LoginDialogComponent({ requestRender() {} }, 'anthropic', () => {}, () => {});
const prompt = dialog.showPrompt({ message: 'Fixture key', secret: true });
dialog.pasteText('fixture-secret');
assert(!dialog.render(100).join('\n').includes('fixture-secret'));
dialog.handleInput('\r'); assert.equal(await prompt, 'fixture-secret');
let authShown = 0, prompts = 0, refreshes = 0;
registerOAuthProvider({ id: 'fixture-login', name: 'Fixture', async login(ctrl) {
  ctrl.onAuth({ url: 'https://fixture.invalid/authorize' });
  const key = await ctrl.onPrompt({ message: 'Fixture code', secret: true });
  assert.equal(key, 'fixture-code');
  return { access: 'fixture-access', refresh: 'fixture-refresh', expires: Date.now() + 3600000 };
} });
const options = { usageLogger: { debug() {}, warn() {} }, async refreshOAuthCredential(_p, _id, credential) {
  refreshes++; await new Promise(r => setTimeout(r, 5));
  return { ...credential, access: 'fixture-new-access', expires: Date.now() + 3600000 };
} };
let auth = new AuthStorage(new Store(), options); await auth.credentials.reload();
await auth.oauth.login('fixture-login', { onAuth() { authShown++; }, async onPrompt() { prompts++; return 'fixture-code'; } });
assert.equal(authShown, 1); assert.equal(prompts, 1); auth.close();
const store = new Store(); auth = new AuthStorage(store, options); await auth.credentials.reload();
const row = store.rows[0]; row.credential.expires = 1; store.persist(); await auth.credentials.reload();
const refreshed = await Promise.all([auth.oauth.accessById('fixture-login', row.id), auth.oauth.accessById('fixture-login', row.id)]);
assert.equal(refreshes, 1); assert.equal(refreshed[0].accessToken, 'fixture-new-access');
assert.equal(JSON.parse(fs.readFileSync(file, 'utf8'))[0].credential.access, 'fixture-new-access');
auth.close();
let requests = 0;
const model = { ...getBundledModels('openai').find(m => m.id === 'gpt-4o-mini'), api: 'openai-completions', baseUrl: 'https://fixture.invalid/v1' };
const output = await complete(model, { messages: [{ role: 'user', content: 'synthetic request', timestamp: Date.now() }] }, { apiKey: 'fixture-key', fetch: async () => {
  requests++;
  return new Response('data: {"id":"fixture","choices":[{"index":0,"delta":{"content":"fixture result"},"finish_reason":null}]}\n\ndata: {"id":"fixture","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}\n\ndata: [DONE]\n\n', { headers: { 'content-type': 'text/event-stream' } });
} });
assert.equal(output.stopReason, 'stop'); assert.equal(output.content[0].text, 'fixture result'); assert.equal(requests, 1);
console.log(JSON.stringify({ runtime: Bun.version, login_entries: providers.length, original_tui: true, masked_prompt: true, restart_persistence: true, concurrent_refresh_calls: refreshes, upstream_inference_requests: requests, private_mode: (fs.statSync(file).mode & 0o777).toString(8) }));
