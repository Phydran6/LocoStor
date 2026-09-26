<script>
  import Icon from './Icon.svelte';
  import { api, session } from '../lib/api.svelte.js';
  import { theme, setTheme, updates } from '../lib/ui.svelte.js';

  let { title, onmenu } = $props();

  const order = ['system', 'light', 'dark'];
  const icons = { system: 'monitor', light: 'sun', dark: 'moon' };

  function cycleTheme() {
    setTheme(order[(order.indexOf(theme.value) + 1) % order.length]);
  }

  async function logout() {
    try {
      await api.post('/api/auth/logout');
    } finally {
      session.logged_in = false;
    }
  }
</script>

<header
  class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-zinc-200 bg-white/85 px-4 backdrop-blur sm:px-6 dark:border-zinc-800 dark:bg-zinc-900/85"
>
  <button class="btn-icon lg:hidden" onclick={onmenu} aria-label="Open menu"><Icon name="menu" /></button>
  <p class="truncate text-sm font-medium text-zinc-500 dark:text-zinc-400">{title}</p>

  <div class="ml-auto flex items-center gap-1">
    {#if updates.status?.update_available}
      <a href="#/update" class="mr-2 hidden items-center gap-1.5 rounded-full bg-sky-600/10 px-3 py-1 text-xs font-medium text-sky-700 hover:bg-sky-600/20 sm:inline-flex dark:text-sky-300">
        <Icon name="download" size={14} />
        Update {updates.status.latest} available
      </a>
    {/if}
    <button class="btn-icon" onclick={cycleTheme} title="Theme: {theme.value}" aria-label="Switch theme (current: {theme.value})">
      <Icon name={icons[theme.value]} />
    </button>
    <button class="btn-icon" onclick={logout} title="Log out" aria-label="Log out"><Icon name="logout" /></button>
  </div>
</header>
