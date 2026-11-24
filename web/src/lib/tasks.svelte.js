// Live view of background tasks (system updates, folder permissions, ...).
import { api } from './api.svelte.js';

export const viewer = $state({ open: false, id: '', base: '/api', title: '', onDone: null });

// showTask opens the terminal window for a task. base is '/api' for the
// container or '/api/host' for the Proxmox host.
export function showTask(id, { base = '/api', title = '', onDone = null } = {}) {
  if (!id) return;
  Object.assign(viewer, { open: true, id, base, title, onDone });
}

// followTask polls a task until it ends and returns its final info.
// onLines receives new log lines as they arrive.
export async function followTask(base, id, onLines, signal) {
  let next = 0;
  let misses = 0;
  for (;;) {
    if (signal?.aborted) return null;
    try {
      const r = await api.get(`${base}/tasks/${encodeURIComponent(id)}?from=${next}`);
      misses = 0;
      if (r.lines.length) onLines?.(r.lines, r.from);
      next = r.next;
      if (r.task.status !== 'running') return r.task;
    } catch (e) {
      // LocoStor may be restarting during an update.
      if (++misses > 120 && e.status !== 0 && e.status !== 502 && e.status !== 503) throw e;
    }
    await new Promise((res) => setTimeout(res, 600));
  }
}
