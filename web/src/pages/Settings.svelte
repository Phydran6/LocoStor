<script>
  import Icon from '../components/Icon.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, theme, setTheme } from '../lib/ui.svelte.js';

  let current = $state('');
  let next = $state('');
  let repeat = $state('');
  let saving = $state(false);
  let error = $state('');

  async function changePassword(e) {
    e.preventDefault();
    if (next !== repeat) {
      error = 'Passwords do not match';
      return;
    }
    saving = true;
    error = '';
    try {
      await api.post('/api/auth/password', { current, new: next });
      toast.success('Admin password changed. Other sessions were logged out.');
      current = next = repeat = '';
    } catch (err) {
      error = err.message;
    } finally {
      saving = false;
    }
  }

  const themes = [
    { value: 'system', label: 'System', icon: 'monitor' },
    { value: 'light', label: 'Light', icon: 'sun' },
    { value: 'dark', label: 'Dark', icon: 'moon' },
  ];
</script>

<PageHeader title="Settings" />

<div class="grid max-w-4xl gap-4 lg:grid-cols-2">
  <section class="card">
    <div class="card-header"><h2 class="card-title">Appearance</h2></div>
    <div class="grid grid-cols-3 gap-2 p-4">
      {#each themes as t}
        <button
          class="flex flex-col items-center gap-2 rounded-lg border px-3 py-4 text-sm font-medium transition-colors
            {theme.value === t.value
            ? 'border-brand-500 bg-brand-500/10 text-brand-700 dark:text-brand-300'
            : 'border-zinc-200 text-zinc-600 hover:border-zinc-300 dark:border-ink-800 dark:text-zinc-400 dark:hover:border-zinc-700'}"
          onclick={() => setTheme(t.value)}
          aria-pressed={theme.value === t.value}
        >
          <Icon name={t.icon} size={20} />{t.label}
        </button>
      {/each}
    </div>
  </section>

  <section class="card">
    <div class="card-header"><h2 class="card-title">Admin password</h2></div>
    <form class="space-y-3 p-4" onsubmit={changePassword}>
      <div>
        <label class="label" for="p-cur">Current password</label>
        <input id="p-cur" class="input" type="password" bind:value={current} required autocomplete="current-password" />
      </div>
      <div>
        <label class="label" for="p-new">New password</label>
        <input id="p-new" class="input" type="password" bind:value={next} required minlength="8" autocomplete="new-password" />
        <p class="hint">At least 8 characters.</p>
      </div>
      <div>
        <label class="label" for="p-rep">Repeat new password</label>
        <input id="p-rep" class="input" type="password" bind:value={repeat} required autocomplete="new-password" />
      </div>
      {#if error}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{error}</p>{/if}
      <button class="btn btn-primary" disabled={saving}>{saving ? 'Saving…' : 'Change password'}</button>
    </form>
  </section>
</div>
