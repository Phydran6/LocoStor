<script>
  import Icon from '../components/Icon.svelte';
  import { api, session } from '../lib/api.svelte.js';

  let password = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(e) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      Object.assign(session, await api.post('/api/auth/login', { password }));
    } catch (err) {
      error = err.message;
      password = '';
    } finally {
      busy = false;
    }
  }
</script>

<div class="flex min-h-screen items-center justify-center bg-gradient-to-br from-zinc-100 to-zinc-200 p-4 dark:from-zinc-950 dark:to-zinc-900">
  <form class="card w-full max-w-sm p-8" onsubmit={submit}>
    <div class="mb-6 flex flex-col items-center gap-3">
      <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-sky-600 text-white shadow-lg shadow-sky-600/30">
        <Icon name="database" size={26} />
      </div>
      <div class="text-center">
        <h1 class="text-xl font-semibold tracking-tight">LocoStor</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">Storage management</p>
      </div>
    </div>

    <label class="label" for="password">Admin password</label>
    <!-- svelte-ignore a11y_autofocus -->
    <input id="password" class="input" type="password" autocomplete="current-password" bind:value={password} autofocus required />

    {#if error}
      <p class="mt-3 flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{error}</p>
    {/if}
    {#if session.demo}
      <p class="mt-3 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">Demo mode – the password is <b>demo</b>.</p>
    {/if}

    <button class="btn btn-primary mt-5 w-full py-2" disabled={busy || !password}>
      {busy ? 'Signing in…' : 'Sign in'}
    </button>
  </form>
</div>
