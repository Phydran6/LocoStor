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

<!-- Always dark: the logo is drawn on the dark background. -->
<div class="dark flex min-h-screen flex-col items-center justify-center bg-[#05060c] px-4 py-10 text-zinc-100">
  <img src="/logo.png" alt="LocoStor" width="280" class="mb-8 w-56 sm:w-72" />

  <form class="w-full max-w-xs" onsubmit={submit}>
    <label class="mb-1.5 block text-sm text-zinc-400" for="password">Admin password</label>
    <!-- svelte-ignore a11y_autofocus -->
    <input
      id="password"
      class="block w-full rounded-md border border-ink-700 bg-ink-900 px-3 py-2 text-sm text-zinc-100 focus:border-brand-400 focus:ring-2 focus:ring-brand-400/20 focus:outline-none"
      type="password"
      autocomplete="current-password"
      bind:value={password}
      autofocus
      required
    />

    {#if error}
      <p class="mt-3 flex items-center gap-1.5 text-sm text-red-400"><Icon name="alert" size={16} />{error}</p>
    {/if}
    {#if session.demo}
      <p class="mt-3 text-xs text-zinc-500">Demo mode – the password is <span class="mono text-brand-300">demo</span>.</p>
    {/if}

    <button class="btn btn-primary mt-4 w-full py-2" disabled={busy || !password}>
      {busy ? 'Signing in…' : 'Sign in'}
    </button>
  </form>
</div>
