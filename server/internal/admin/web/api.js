// The calls the Admin UI makes. Everything goes through the server's public API; the only
// thing this file knows beyond it is where it keeps the session.

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

const KEY = 'flare-admin-session';

export function loadSession() {
  try {
    const raw = sessionStorage.getItem(KEY);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

function saveSession(session) {
  try {
    if (session) sessionStorage.setItem(KEY, JSON.stringify(session));
    else sessionStorage.removeItem(KEY);
  } catch {
    // Private windows may refuse storage: the session then lasts until the page is closed.
  }
}

export class Api {
  constructor(session = null, onExpired = () => {}) {
    this.session = session;
    this.onExpired = onExpired;
  }

  async request(method, path, body, { auth = true } = {}) {
    const headers = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (auth && this.session) headers.Authorization = `Bearer ${this.session.token}`;
    let response;
    try {
      response = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    } catch {
      throw new ApiError(0, 0, 'The server cannot be reached.');
    }
    if (response.status === 204) return null;
    let data = null;
    try {
      data = await response.json();
    } catch {
      // not JSON
    }
    if (!response.ok) {
      if (response.status === 401 && auth && this.session) {
        this.session = null;
        saveSession(null);
        this.onExpired();
      }
      throw new ApiError(response.status, data?.code ?? 0, data?.error ?? `The server answered ${response.status}.`);
    }
    return data;
  }

  /** Logs in with a password, and keeps only the token it is given. */
  async login(user, password) {
    const basic = btoa(unescape(encodeURIComponent(`${user}:${password}`)));
    const response = await fetch('/v1/auth/login', {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', Authorization: `Basic ${basic}` },
      body: JSON.stringify({ label: 'Admin UI', expires_in: 12 * 3600 }),
    }).catch(() => {
      throw new ApiError(0, 0, 'The server cannot be reached.');
    });
    const data = await response.json().catch(() => null);
    if (!response.ok) {
      throw new ApiError(response.status, data?.code ?? 0, data?.error ?? `The server answered ${response.status}.`);
    }
    if (data.role !== 'admin') {
      // Do not keep a token that is no use here.
      await fetch(`/v1/account/tokens/${encodeURIComponent(data.id)}`, { method: 'DELETE', headers: { Authorization: `Bearer ${data.token}` } }).catch(() => {});
      throw new ApiError(403, 40301, 'This account is not an administrator.');
    }
    this.session = { token: data.token, tokenId: data.id, user: data.user };
    saveSession(this.session);
    return this.session;
  }

  /** Revokes the token on the server, and forgets it. */
  async logout() {
    const session = this.session;
    this.session = null;
    saveSession(null);
    if (session) {
      await fetch(`/v1/account/tokens/${encodeURIComponent(session.tokenId)}`, { method: 'DELETE', headers: { Authorization: `Bearer ${session.token}` } }).catch(() => {});
    }
  }

  config() { return this.request('GET', '/v1/config', undefined, { auth: false }); }
  contract() { return this.request('GET', '/v1/themes/schema', undefined, { auth: false }); }
  account() { return this.request('GET', '/v1/account'); }

  themes() { return this.request('GET', '/v1/themes', undefined, { auth: false }); }
  createTheme(theme) { return this.request('POST', '/v1/admin/themes', theme); }
  replaceTheme(theme) { return this.request('PUT', `/v1/admin/themes/${encodeURIComponent(theme.name)}`, theme); }
  deleteTheme(name) { return this.request('DELETE', `/v1/admin/themes/${encodeURIComponent(name)}`); }

  settings() { return this.request('GET', '/v1/admin/settings'); }
  setSetting(key, value) { return this.request('PUT', `/v1/admin/settings/${encodeURIComponent(key)}`, { value }); }
  resetSetting(key) { return this.request('DELETE', `/v1/admin/settings/${encodeURIComponent(key)}`); }

  users() { return this.request('GET', '/v1/admin/users'); }
  createUser(user) { return this.request('POST', '/v1/admin/users', user); }
  changeUser(name, change) { return this.request('PATCH', `/v1/admin/users/${encodeURIComponent(name)}`, change); }
  deleteUser(name) { return this.request('DELETE', `/v1/admin/users/${encodeURIComponent(name)}`); }
  userTokens(name) { return this.request('GET', `/v1/admin/users/${encodeURIComponent(name)}/tokens`); }
  revokeToken(name, id) { return this.request('DELETE', `/v1/admin/users/${encodeURIComponent(name)}/tokens/${encodeURIComponent(id)}`); }

  access() { return this.request('GET', '/v1/admin/access'); }
  grant(rule) { return this.request('PUT', '/v1/admin/access', rule); }
  revoke(who, pattern) { return this.request('DELETE', `/v1/admin/access?who=${encodeURIComponent(who)}&pattern=${encodeURIComponent(pattern)}`); }
}
