<script>
  import { onMount } from 'svelte';
  import { renderSVG } from 'uqr';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Modal from '../components/Modal.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api, session } from '../lib/api.svelte.js';
  import { toast, theme, setTheme } from '../lib/ui.svelte.js';

  let account = $state(null);

  onMount(loadAccount);
  async function loadAccount() {
    try {
      account = await api.get('/api/auth/account');
    } catch (e) {
      toast.error(e.message);
    }
  }

  // --- username ---
  let newName = $state('');
  let namePw = $state('');
  let nameBusy = $state(false);
  async function changeUsername(e) {
    e.preventDefault();
    nameBusy = true;
    try {
      await api.post('/api/auth/username', { username: newName, password: namePw });
      toast.success(`Username changed to "${newName}"`);
      newName = namePw = '';
      loadAccount();
    } catch (err) {
      toast.error(err.message);
    } finally {
      nameBusy = false;
    }
  }

  // --- password ---
  let current = $state('');
  let next = $state('');
  let repeat = $state('');
  let pwBusy = $state(false);
  let pwError = $state('');
  async function changePassword(e) {
    e.preventDefault();
    if (next !== repeat) {
      pwError = 'Passwords do not match';
      return;
    }
    pwBusy = true;
    pwError = '';
    try {
      await api.post('/api/auth/password', { current, new: next });
      toast.success('Password changed. Other sessions were logged out.');
      current = next = repeat = '';
    } catch (err) {
      pwError = err.message;
    } finally {
      pwBusy = false;
    }
  }

  // --- two-factor ---
  let setup = $state(null); // { secret, uri, qr }
  let setupCode = $state('');
  let recovery = $state(null); // codes to show once
  let confirmMode = $state(''); // 'disable' | 'regenerate'
  let confirmPw = $state('');
  let confirmCode = $state('');
  let mfaBusy = $state(false);

  async function startSetup() {
    try {
      const r = await api.post('/api/auth/totp/setup');
      const svg = renderSVG(r.uri, { border: 2, whiteColor: '#ffffff', blackColor: '#000000' });
      setup = { ...r, qr: 'data:image/svg+xml;base64,' + btoa(svg) };
      setupCode = '';
    } catch (e) {
      toast.error(e.message);
    }
  }

  async function enable(e) {
    e.preventDefault();
    mfaBusy = true;
    try {
      const r = await api.post('/api/auth/totp/enable', { code: setupCode });
      recovery = r.recovery_codes;
      setup = null;
      toast.success('Two-factor login is on');
      loadAccount();
    } catch (err) {
      toast.error(err.message);
    } finally {
      mfaBusy = false;
    }
  }

  async function confirmAction(e) {
    e.preventDefault();
    mfaBusy = true;
    try {
      if (confirmMode === 'disable') {
        await api.post('/api/auth/totp/disable', { password: confirmPw, code: confirmCode });
        toast.success('Two-factor login is off');
      } else {
        const r = await api.post('/api/auth/recovery', { password: confirmPw, code: confirmCode });
        recovery = r.recovery_codes;
      }
      confirmMode = '';
      loadAccount();
    } catch (err) {
      toast.error(err.message);
    } finally {
      mfaBusy = false;
    }
  }

  function openConfirm(mode) {
    confirmMode = mode;
    confirmPw = confirmCode = '';
  }

  function downloadCodes() {
    const text = `LocoStor recovery codes (${account?.username})\nEach code works once.\n\n${recovery.join('\n')}\n`;
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
    a.download = 'locostor-recovery-codes.txt';
    a.click();
    URL.revokeObjectURL(a.href);
  }

  const themes = [
    { value: 'system', label: 'System', icon: 'monitor' },
    { value: 'light', label: 'Light', icon: 'sun' },
    { value: 'dark', label: 'Dark', icon: 'moon' },
  ];
</script>

<PageHeader title="Settings" />

<div class="grid max-w-5xl gap-4 lg:grid-cols-2">
  <section class="card lg:col-span-2">
    <div class="card-header">
      <h2 class="card-title">Two-factor login</h2>
      {#if account}<Badge tone={account.mfa_enabled ? 'ok' : 'muted'} dot>{account.mfa_enabled ? 'on' : 'off'}</Badge>{/if}
    </div>
    <div class="p-4 text-sm">
      {#if !account}
        <p class="text-zinc-500">Loading…</p>
      {:else if setup}
        <div class="flex flex-col gap-5 sm:flex-row">
          <img src={setup.qr} alt="QR code for the authenticator app" class="h-44 w-44 rounded-md bg-white p-1" />
          <form class="flex-1 space-y-3" onsubmit={enable}>
            <p class="text-zinc-600 dark:text-zinc-300">
              Scan the code with an authenticator app (e.g. Aegis, 2FAS, Google Authenticator, 1Password), then enter the 6-digit code it shows.
            </p>
            <div>
              <p class="text-xs text-zinc-500">Or enter the key manually:</p>
              <p class="mono break-all select-all">{setup.secret}</p>
            </div>
            <div class="flex gap-2">
              <input class="input mono max-w-40 tracking-widest" bind:value={setupCode} inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="123456" required />
              <button class="btn btn-primary" disabled={mfaBusy}>Turn on</button>
              <button type="button" class="btn btn-secondary" onclick={() => (setup = null)}>Cancel</button>
            </div>
          </form>
        </div>
      {:else if account.mfa_enabled}
        <p class="text-zinc-600 dark:text-zinc-300">
          Logging in needs a code from your authenticator app. Recovery codes left: <b>{account.recovery_codes_left}</b>.
        </p>
        <div class="mt-3 flex flex-wrap gap-2">
          <button class="btn btn-secondary" onclick={() => openConfirm('regenerate')}><Icon name="refresh" size={16} />New recovery codes</button>
          <button class="btn btn-secondary" onclick={() => openConfirm('disable')}>Turn off</button>
        </div>
      {:else}
        <p class="text-zinc-600 dark:text-zinc-300">
          Protect the login with a one-time code (TOTP) from an authenticator app. You also get recovery codes for when the phone is lost.
        </p>
        <button class="btn btn-primary mt-3" onclick={startSetup}><Icon name="key" size={16} />Set up</button>
      {/if}
    </div>
  </section>

  <section class="card">
    <div class="card-header"><h2 class="card-title">Password</h2></div>
    <form class="space-y-3 p-4" onsubmit={changePassword}>
      <input type="text" autocomplete="username" value={account?.username ?? ''} class="hidden" readonly aria-hidden="true" tabindex="-1" />
      <div>
        <label class="label" for="p-cur">Current password</label>
        <input id="p-cur" class="input" type="password" bind:value={current} required autocomplete="current-password" />
      </div>
      <div>
        <label class="label" for="p-new">New password</label>
        <input id="p-new" class="input" type="password" bind:value={next} required minlength="8" maxlength="72" autocomplete="new-password" />
        <p class="hint">8 to 72 characters.</p>
      </div>
      <div>
        <label class="label" for="p-rep">Repeat new password</label>
        <input id="p-rep" class="input" type="password" bind:value={repeat} required autocomplete="new-password" />
      </div>
      {#if pwError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{pwError}</p>{/if}
      <button class="btn btn-primary" disabled={pwBusy || session.demo}>{pwBusy ? 'Saving…' : 'Change password'}</button>
    </form>
  </section>

  <div class="space-y-4">
    <section class="card">
      <div class="card-header"><h2 class="card-title">Username</h2></div>
      <form class="space-y-3 p-4" onsubmit={changeUsername}>
        <p class="text-sm text-zinc-500">Current: <span class="mono text-zinc-800 dark:text-zinc-200">{account?.username ?? '…'}</span></p>
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="label" for="u-new">New username</label>
            <input id="u-new" class="input" bind:value={newName} required maxlength="64" autocomplete="off" autocapitalize="none" />
          </div>
          <div>
            <label class="label" for="u-pw">Password</label>
            <input id="u-pw" class="input" type="password" bind:value={namePw} required autocomplete="current-password" />
          </div>
        </div>
        <button class="btn btn-secondary" disabled={nameBusy || session.demo}>Change username</button>
      </form>
    </section>

    <section class="card">
      <div class="card-header"><h2 class="card-title">Appearance</h2></div>
      <div class="grid grid-cols-3 gap-2 p-4">
        {#each themes as t}
          <button
            class="flex flex-col items-center gap-2 rounded-lg border px-3 py-3 text-sm font-medium transition-colors
              {theme.value === t.value
              ? 'border-brand-500 bg-brand-500/10 text-brand-700 dark:text-brand-300'
              : 'border-zinc-200 text-zinc-600 hover:border-zinc-300 dark:border-ink-800 dark:text-zinc-400 dark:hover:border-ink-700'}"
            onclick={() => setTheme(t.value)}
            aria-pressed={theme.value === t.value}
          >
            <Icon name={t.icon} size={20} />{t.label}
          </button>
        {/each}
      </div>
    </section>
  </div>
</div>

<Modal title="Recovery codes" open={!!recovery} onclose={() => (recovery = null)}>
  {#if recovery}
    <p class="text-sm text-zinc-600 dark:text-zinc-300">
      Store these codes somewhere safe. Each one logs you in once if your authenticator is not available. They are shown only now.
    </p>
    <ul class="mono mt-3 grid grid-cols-2 gap-x-6 gap-y-1 rounded-md bg-zinc-100 p-3 text-sm select-all dark:bg-ink-950">
      {#each recovery as c}<li>{c}</li>{/each}
    </ul>
  {/if}
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={downloadCodes}><Icon name="download" size={16} />Download</button>
    <button class="btn btn-primary" onclick={() => (recovery = null)}>I saved them</button>
  {/snippet}
</Modal>

<Modal title={confirmMode === 'disable' ? 'Turn off two-factor login' : 'New recovery codes'} open={!!confirmMode} onclose={() => (confirmMode = '')}>
  <form id="mfa-confirm" class="space-y-3" onsubmit={confirmAction}>
    <p class="text-sm text-zinc-600 dark:text-zinc-300">Confirm with your password and a current code.</p>
    <div>
      <label class="label" for="c-pw">Password</label>
      <input id="c-pw" class="input" type="password" bind:value={confirmPw} required autocomplete="current-password" />
    </div>
    <div>
      <label class="label" for="c-code">Authentication or recovery code</label>
      <input id="c-code" class="input mono" bind:value={confirmCode} required autocomplete="one-time-code" maxlength="16" />
    </div>
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (confirmMode = '')}>Cancel</button>
    <button class="btn {confirmMode === 'disable' ? 'btn-danger' : 'btn-primary'}" form="mfa-confirm" disabled={mfaBusy}>Confirm</button>
  {/snippet}
</Modal>
