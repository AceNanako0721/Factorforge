import { isDeepStrictEqual } from "node:util";
import { resolveCredentialIdentityKey, type AuthCredential, type AuthCredentialStore } from "@oh-my-pi/pi-ai/auth-storage";
import { getOAuthCredentialProvider } from "@oh-my-pi/pi-ai/oauth";
import { ConfigStore } from "./store.js";
import { emptyOMPState, validateOMPState } from "./omp-state.js";
import { fail } from "../api/protocol.js";

// Implements OMP's public storage port; never opens its default SQLite vault.
export class TomlCredentialStore implements AuthCredentialStore {
  constructor(readonly config: ConfigStore, private readonly loginSignal?: () => AbortSignal | undefined) {
    if (!config.state.omp) { config.state.omp = emptyOMPState(); config.saveSync(); }
  }
  private get state() {
    this.loginSignal?.()?.throwIfAborted();
    this.config.assertUsable(); return this.config.state.omp!;
  }
  private commit() {
    this.loginSignal?.()?.throwIfAborted();
    validateOMPState(this.state);
    this.config.saveSync();
  }
  close() {}
  listAuthCredentials(provider?: string) {
    if (provider) provider = getOAuthCredentialProvider(provider);
    return structuredClone(this.state.credentials.filter(r => r.disabledCause === null && (!provider || r.provider === provider)));
  }
  updateAuthCredential(id: number, credential: AuthCredential) {
    const row = this.state.credentials.find(r => r.id === id && r.disabledCause === null);
    if (!row) fail("AUTH_REQUIRED");
    row.credential = structuredClone(credential); this.commit();
  }
  async deleteAuthCredential(id: number, _cause: string) {
    const before = this.state.credentials.length;
    this.state.credentials = this.state.credentials.filter(r => r.id !== id);
    if (before === this.state.credentials.length) return false;
    // Do not persist vendor error strings or tombstone token bytes.
    this.state.cache = {}; this.commit(); return true;
  }
  private matches(id: number, data: string) {
    const row = this.state.credentials.find(r => r.id === id && r.disabledCause === null);
    if (!row) return false;
    const c = row.credential;
    const value = c.type === "api_key" ? { key: c.key, ...(c.source === "login" ? { source: "login" } : {}) } :
      (({ type: _type, ...rest }) => rest)(c);
    try { return isDeepStrictEqual(value, JSON.parse(data)); } catch { return false; }
  }
  tryUpdateAuthCredentialIfMatches(id: number, expected: string, credential: AuthCredential) {
    if (!this.matches(id, expected)) return false;
    this.updateAuthCredential(id, credential); return true;
  }
  tryDisableAuthCredentialIfMatches(id: number, expected: string, _cause: string) {
    if (!this.matches(id, expected)) return false;
    this.state.credentials = this.state.credentials.filter(r => r.id !== id);
    this.state.cache = {}; this.commit(); return true;
  }
  async replaceAuthCredentials(provider: string, credentials: AuthCredential[]) {
    provider = getOAuthCredentialProvider(provider);
    this.state.credentials = this.state.credentials.filter(r => r.provider !== provider);
    for (const credential of credentials) this.add(provider, credential);
    this.commit(); return this.listAuthCredentials(provider);
  }
  private add(provider: string, credential: AuthCredential) {
    if (!Number.isSafeInteger(this.state.next_id + 1)) fail("MODEL_STATE_INVALID");
    const identity = resolveCredentialIdentityKey(provider, credential);
    const found = this.state.credentials.find(r => r.provider === provider &&
      (identity ? resolveCredentialIdentityKey(provider, r.credential) === identity : isDeepStrictEqual(r.credential, credential)));
    if (found) { found.credential = structuredClone(credential); found.disabledCause = null; }
    else this.state.credentials.push({ id: this.state.next_id++, provider, credential: structuredClone(credential), disabledCause: null });
  }
  async upsertAuthCredential(provider: string, credential: AuthCredential) {
    provider = getOAuthCredentialProvider(provider);
    this.add(provider, credential); this.commit(); return this.listAuthCredentials(provider);
  }
  async deleteAuthCredentials(provider: string, _cause: string) {
    provider = getOAuthCredentialProvider(provider);
    this.state.credentials = this.state.credentials.filter(r => r.provider !== provider);
    this.state.cache = {}; this.commit();
  }
  getCache(key: string, options?: { includeExpired?: boolean }) {
    const row = this.state.cache[key];
    return row && (options?.includeExpired || row.expires > Date.now() / 1000) ? row.value : null;
  }
  setCache(key: string, value: string, expiresAtSec: number) {
    this.state.cache[key] = { value, expires: expiresAtSec }; this.commit();
  }
  deleteCachePrefix(prefix: string) {
    for (const key of Object.keys(this.state.cache)) if (key.startsWith(prefix)) delete this.state.cache[key];
    this.commit();
  }
  cleanExpiredCache() {
    const keys = Object.keys(this.state.cache).filter(k => this.state.cache[k]!.expires <= Date.now() / 1000);
    if (keys.length) { for (const key of keys) delete this.state.cache[key]; this.commit(); }
  }
}
