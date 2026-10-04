// Connection to the agent on the Proxmox host, shared by all pages.
import { api } from './api.svelte.js';

export const host = $state({ info: null, loading: false });

let pending = null;

export function loadHost(force = false) {
  if (pending && !force) return pending;
  host.loading = true;
  pending = api
    .get('/api/host/info')
    .then((info) => (host.info = info))
    .catch((e) => (host.info = { available: false, error: e.message }))
    .finally(() => (host.loading = false));
  return pending;
}

// The selected scope ("host" or "container") is remembered per browser.
function stored() {
  try {
    return localStorage.getItem('locostor-scope');
  } catch {
    return null;
  }
}

export const scope = $state({ value: stored() || 'host' });

export function setScope(v) {
  scope.value = v;
  try {
    localStorage.setItem('locostor-scope', v);
  } catch {
    // not persisted
  }
}
