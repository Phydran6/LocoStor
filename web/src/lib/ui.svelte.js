// Toasts, confirm dialogs, theme and shared update state.
import { api } from './api.svelte.js';

let nextId = 1;
export const toasts = $state([]);

function push(kind, message, timeout = 4000) {
  const id = nextId++;
  toasts.push({ id, kind, message });
  if (timeout) setTimeout(() => dismiss(id), timeout);
}

export function dismiss(id) {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
}

export const toast = {
  success: (msg) => push('success', msg),
  error: (msg) => push('error', msg, 8000),
};

// confirm() returns a promise resolved by the ConfirmHost component.
export const dialog = $state({ open: false, title: '', message: '', confirmLabel: 'OK', danger: false, resolve: null });

export function confirm({ title, message, confirmLabel = 'Confirm', danger = false }) {
  return new Promise((resolve) => {
    Object.assign(dialog, { open: true, title, message, confirmLabel, danger, resolve });
  });
}

// Theme: "light", "dark" or "system".
function storedTheme() {
  try {
    return localStorage.getItem('locostor-theme') || 'system';
  } catch {
    return 'system';
  }
}

export const theme = $state({ value: storedTheme() });
const media = matchMedia('(prefers-color-scheme: dark)');

function applyTheme() {
  const dark = theme.value === 'dark' || (theme.value === 'system' && media.matches);
  document.documentElement.classList.toggle('dark', dark);
}
media.addEventListener('change', applyTheme);

export function setTheme(value) {
  theme.value = value;
  try {
    localStorage.setItem('locostor-theme', value);
  } catch {
    // storage unavailable - theme just won't persist
  }
  applyTheme();
}

// Update status, shared by the top bar badge and the update page.
export const updates = $state({ status: null });

export async function loadUpdateStatus() {
  try {
    updates.status = await api.get('/api/update');
  } catch {
    // shown on the update page when it matters
  }
}
