<script>
  import Icon from '../components/Icon.svelte';
  import logo from '../assets/logo.png';
  import { api, session } from '../lib/api.svelte.js';

  let username = $state('');
  let password = $state('');
  let code = $state('');
  let mfaToken = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(e) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      if (!mfaToken) {
        const res = await api.post('/api/auth/login', { username, password });
        password = '';
        if (res.mfa_required) {
          mfaToken = res.mfa_token;
          return;
        }
        Object.assign(session, res);
      } else {
        Object.assign(session, await api.post('/api/auth/mfa', { mfa_token: mfaToken, code }));
      }
    } catch (err) {
      error = err.message;
      code = '';
      if (mfaToken && err.status === 401 && /expired/.test(err.message)) mfaToken = '';
    } finally {
      busy = false;
    }
  }

  function back() {
    mfaToken = code = error = '';
  }

  const field =
    'block w-full rounded-md border border-ink-700 bg-ink-900 px-3 py-2 text-sm text-zinc-100 focus:border-brand-400 focus:ring-2 focus:ring-brand-400/20 focus:outline-none';
</script>

<!-- Always dark: the logo is drawn on the dark background. -->
<div class="dark flex min-h-screen flex-col items-center justify-center bg-[#05060c] px-4 py-10 text-zinc-100">
  <img src={logo} alt="LocoStor" width="280" class="mb-8 w-56 sm:w-72" />

  <form class="w-full max-w-xs space-y-3" onsubmit={submit}>
    {#if !mfaToken}
      <div>
        <label class="mb-1.5 block text-sm text-zinc-400" for="username">Username</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="username" class={field} autocomplete="username" autocapitalize="none" spellcheck="false" bind:value={username} autofocus required />
      </div>
      <div>
        <label class="mb-1.5 block text-sm text-zinc-400" for="password">Password</label>
        <input id="password" class={field} type="password" autocomplete="current-password" bind:value={password} required />
      </div>
    {:else}
      <div>
        <label class="mb-1.5 block text-sm text-zinc-400" for="code">Authentication code</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          id="code"
          class="{field} mono tracking-widest"
          autocomplete="one-time-code"
          inputmode="text"
          spellcheck="false"
          bind:value={code}
          autofocus
          required
          maxlength="16"
        />
        <p class="mt-1.5 text-xs text-zinc-500">6-digit code from your authenticator app, or a recovery code.</p>
      </div>
    {/if}

    {#if error}
      <p class="flex items-center gap-1.5 text-sm text-red-400"><Icon name="alert" size={16} />{error}</p>
    {/if}
    {#if session.demo && !mfaToken}
      <p class="text-xs text-zinc-500">Demo mode – log in as <span class="mono text-brand-300">admin</span> / <span class="mono text-brand-300">demo</span>.</p>
    {/if}

    <button class="btn btn-primary w-full py-2" disabled={busy}>
      {busy ? 'Checking…' : mfaToken ? 'Verify' : 'Sign in'}
    </button>
    {#if mfaToken}
      <button type="button" class="w-full text-center text-xs text-zinc-500 hover:text-zinc-300" onclick={back}>Back</button>
    {/if}
  </form>
</div>
