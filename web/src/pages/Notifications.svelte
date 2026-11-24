<script>
  import { onMount, onDestroy } from 'svelte';
  import { renderSVG } from 'uqr';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Modal from '../components/Modal.svelte';
  import Toggle from '../components/Toggle.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm } from '../lib/ui.svelte.js';
  import { dateTime } from '../lib/format.js';

  let cfg = $state(null);
  let providers = $state([]);
  let events = $state(null);
  let testing = $state(false);

  async function load() {
    try {
      const r = await api.get('/api/notify');
      cfg = r.config;
      providers = r.providers;
      events = { ...r.config.events };
    } catch (e) {
      toast.error(e.message);
    }
  }
  onMount(load);

  let timers = [];
  onDestroy(() => timers.forEach(clearInterval));
  function every(ms, fn) {
    const t = setInterval(fn, ms);
    timers.push(t);
    return () => {
      clearInterval(t);
      timers = timers.filter((x) => x !== t);
    };
  }

  async function test() {
    testing = true;
    try {
      const r = await api.post('/api/notify/test');
      if (r.error) toast.error(r.error);
      else toast.success(`Test message sent via ${r.message.sent.join(' and ')}`);
      load();
    } catch (e) {
      toast.error(e.message);
    } finally {
      testing = false;
    }
  }

  async function saveEvents(e) {
    e.preventDefault();
    try {
      await api.put('/api/notify/events', { ...events, temperature: +events.temperature || 0, space: +events.space || 0 });
      toast.success('Saved');
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }

  // --- e-mail assistant ------------------------------------------------------
  let mailOpen = $state(false);
  let step = $state(1);
  let mail = $state({});
  let provider = $state(null);
  let busy = $state(false);
  let err = $state('');
  let oauth = $state(null); // { user_code, verification_uri } or { auth_url }
  let ownApp = $state(false);
  let usePassword = $state(false);
  let googleUrl = $state('');
  let stopPoll = null;

  function openMail() {
    const e = cfg?.email;
    mail = e
      ? { address: e.username, host: e.host, port: e.port, security: e.security, username: e.username, password: '', from: e.from, to: e.to.join(', '), client_id: '', client_secret: '', tenant: '' }
      : { address: '', host: '', port: 587, security: 'starttls', username: '', password: '', from: '', to: '', client_id: '', client_secret: '', tenant: '' };
    provider = e ? providers.find((p) => p.id === e.provider) : null;
    step = e ? 2 : 1;
    oauth = null;
    usePassword = e ? !e.oauth && provider?.auth === 'oauth' : false;
    ownApp = false;
    err = googleUrl = '';
    mailOpen = true;
  }

  async function detect(e) {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      const p = await api.post('/api/notify/email/detect', { email: mail.address });
      pick(p);
      step = 2;
    } catch (x) {
      err = x.message;
    } finally {
      busy = false;
    }
  }

  function pick(p) {
    provider = { ...(providers.find((x) => x.id === p.id) ?? {}), ...p };
    mail.host = p.host;
    mail.port = p.port;
    mail.security = p.security;
    mail.username = mail.address;
    mail.from = mail.address;
    if (!mail.to) mail.to = mail.address;
    usePassword = false;
    oauth = null;
  }

  function changeProvider(id) {
    pick(providers.find((p) => p.id === id));
  }

  let builtin = $derived(provider && cfg?.oauth_builtin?.[provider.id]);
  let needsOwnApp = $derived(provider?.auth === 'oauth' && !builtin);

  async function signIn() {
    busy = true;
    err = '';
    stopPoll?.();
    try {
      oauth = await api.post('/api/notify/email/oauth/start', {
        provider: provider.id,
        email: mail.address,
        client_id: ownApp || needsOwnApp ? mail.client_id : '',
        client_secret: ownApp || needsOwnApp ? mail.client_secret : '',
        tenant: mail.tenant,
      });
      if (oauth.auth_url) {
        window.open(oauth.auth_url, '_blank', 'noopener');
      } else {
        stopPoll = every(2000, async () => {
          const st = await api.get('/api/notify/email/oauth/status').catch(() => null);
          if (st?.status === 'ok') {
            stopPoll();
            oauth = { done: true };
            toast.success('Signed in');
            await load();
            step = 3;
          } else if (st?.status === 'error') {
            stopPoll();
            err = st.error;
            oauth = null;
          }
        });
      }
    } catch (x) {
      err = x.message;
    } finally {
      busy = false;
    }
  }

  async function googleCode() {
    busy = true;
    err = '';
    try {
      await api.post('/api/notify/email/oauth/code', { url: googleUrl });
      oauth = { done: true };
      toast.success('Signed in with Google');
      await load();
      step = 3;
    } catch (x) {
      err = x.message;
    } finally {
      busy = false;
    }
  }

  async function saveMail(e) {
    e?.preventDefault();
    busy = true;
    err = '';
    try {
      await api.put('/api/notify/email', {
        enabled: true,
        provider: provider?.id ?? 'custom',
        host: mail.host,
        port: +mail.port,
        security: mail.security,
        username: mail.username,
        password: mail.password,
        from: mail.from,
        to: mail.to.split(/[\s,;]+/).filter(Boolean),
      });
      mailOpen = false;
      toast.success('E-mail is set up');
      await load();
      test();
    } catch (x) {
      err = x.message;
    } finally {
      busy = false;
    }
  }

  async function removeMail() {
    if (!(await confirm({ title: 'Remove e-mail', message: 'Remove the mail account and its stored sign-in?', confirmLabel: 'Remove', danger: true }))) return;
    await api.del('/api/notify/email').catch((e) => toast.error(e.message));
    load();
  }

  // --- Telegram assistant ------------------------------------------------------
  let tgOpen = $state(false);
  let tgStep = $state(1);
  let tgToken = $state('');
  let tgLink = $state(null);
  let tgChats = $state([]);
  let tgErr = $state('');
  let stopTg = null;

  function openTelegram() {
    tgStep = cfg?.telegram ? 2 : 1;
    tgToken = '';
    tgLink = null;
    tgChats = [];
    tgErr = '';
    tgOpen = true;
    if (cfg?.telegram) checkToken();
  }

  async function checkToken(e) {
    e?.preventDefault();
    busy = true;
    tgErr = '';
    try {
      tgLink = await api.post('/api/notify/telegram/token', { token: tgToken });
      const svg = renderSVG(tgLink.link, { border: 2, whiteColor: '#ffffff', blackColor: '#000000' });
      tgLink.qr = 'data:image/svg+xml;base64,' + btoa(svg);
      tgStep = 2;
      stopTg?.();
      stopTg = every(2000, async () => {
        if (!tgOpen) return stopTg();
        tgChats = await api.get('/api/notify/telegram/chats').catch(() => tgChats);
      });
    } catch (x) {
      tgErr = x.message;
    } finally {
      busy = false;
    }
  }

  async function saveTelegram() {
    busy = true;
    tgErr = '';
    try {
      await api.put('/api/notify/telegram', { enabled: true, keep: null });
      stopTg?.();
      tgOpen = false;
      toast.success('Telegram is set up');
      await load();
      test();
    } catch (x) {
      tgErr = x.message;
    } finally {
      busy = false;
    }
  }

  async function removeChat(id) {
    await api.put('/api/notify/telegram', { enabled: true, keep: cfg.telegram.chats.filter((c) => c.id !== id).map((c) => c.id) }).catch((e) => toast.error(e.message));
    load();
  }

  async function removeTelegram() {
    if (!(await confirm({ title: 'Remove Telegram', message: 'Remove the bot token and all chats?', confirmLabel: 'Remove', danger: true }))) return;
    await api.del('/api/notify/telegram').catch((e) => toast.error(e.message));
    load();
  }

  const sevTone = { info: 'info', warning: 'warn', error: 'bad' };
</script>

<PageHeader title="Notifications" description="Alerts for RAID, disk health, free space and updates – by e-mail or Telegram">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={test} disabled={testing || !(cfg?.email?.enabled || cfg?.telegram?.enabled)}>
      <Icon name="send" size={16} />{testing ? 'Sending…' : 'Send test'}
    </button>
  {/snippet}
</PageHeader>

{#if cfg}
  <div class="grid gap-4 lg:grid-cols-2">
    <section class="card">
      <div class="card-header">
        <h2 class="card-title flex items-center gap-2"><Icon name="mail" size={16} />E-mail</h2>
        {#if cfg.email}<Badge tone={cfg.email.enabled ? 'ok' : 'muted'} dot>{cfg.email.enabled ? 'active' : 'off'}</Badge>{:else}<Badge>not set up</Badge>{/if}
      </div>
      <div class="space-y-3 p-4 text-sm">
        {#if cfg.email}
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
            <dt class="text-zinc-500">Account</dt><dd class="truncate">{cfg.email.username} {#if cfg.email.oauth}<Badge tone="info">signed in</Badge>{/if}</dd>
            <dt class="text-zinc-500">Provider</dt><dd>{providers.find((p) => p.id === cfg.email.provider)?.name ?? cfg.email.provider}</dd>
            <dt class="text-zinc-500">Server</dt><dd class="mono">{cfg.email.host}:{cfg.email.port}</dd>
            <dt class="text-zinc-500">To</dt><dd class="truncate">{cfg.email.to.join(', ')}</dd>
          </dl>
          <div class="flex gap-2">
            <button class="btn btn-secondary" onclick={openMail}><Icon name="pencil" size={15} />Change</button>
            <button class="btn btn-secondary" onclick={removeMail}><Icon name="trash" size={15} />Remove</button>
          </div>
        {:else}
          <p class="text-zinc-600 dark:text-zinc-300">Microsoft 365, Outlook, Gmail, WEB.DE, GMX, Telekom, iCloud and more – the assistant finds the right server from your address.</p>
          <button class="btn btn-primary" onclick={openMail}><Icon name="plus" size={16} />Set up e-mail</button>
        {/if}
      </div>
    </section>

    <section class="card">
      <div class="card-header">
        <h2 class="card-title flex items-center gap-2"><Icon name="send" size={16} />Telegram</h2>
        {#if cfg.telegram}<Badge tone={cfg.telegram.enabled ? 'ok' : 'muted'} dot>{cfg.telegram.enabled ? 'active' : 'off'}</Badge>{:else}<Badge>not set up</Badge>{/if}
      </div>
      <div class="space-y-3 p-4 text-sm">
        {#if cfg.telegram}
          <p>Bot <a class="mono text-brand-700 hover:underline dark:text-brand-300" href="https://t.me/{cfg.telegram.bot}" target="_blank" rel="noopener noreferrer">@{cfg.telegram.bot}</a> sends to:</p>
          <ul class="space-y-1">
            {#each cfg.telegram.chats as c}
              <li class="flex items-center justify-between rounded-md bg-zinc-50 px-2.5 py-1 dark:bg-ink-850">
                <span>{c.title || c.id}</span>
                <button class="btn-icon h-7 w-7" onclick={() => removeChat(c.id)} aria-label="Remove {c.title}"><Icon name="x" size={14} /></button>
              </li>
            {/each}
          </ul>
          <div class="flex gap-2">
            <button class="btn btn-secondary" onclick={openTelegram}><Icon name="plus" size={15} />Add chat</button>
            <button class="btn btn-secondary" onclick={removeTelegram}><Icon name="trash" size={15} />Remove</button>
          </div>
        {:else}
          <p class="text-zinc-600 dark:text-zinc-300">Push messages to your phone. Create a bot with @BotFather, paste its token, tap one link – done.</p>
          <button class="btn btn-primary" onclick={openTelegram}><Icon name="plus" size={16} />Set up Telegram</button>
        {/if}
      </div>
    </section>

    <section class="card">
      <div class="card-header"><h2 class="card-title flex items-center gap-2"><Icon name="bell" size={16} />What to report</h2></div>
      <form class="space-y-3 p-4" onsubmit={saveEvents}>
        <Toggle bind:checked={events.raid} label="RAID" hint="A disk drops out, the array is degraded, rebuilding or healthy again." />
        <Toggle bind:checked={events.smart} label="Disk health (SMART)" hint="Warnings and failing disks; checked every 30 minutes, sleeping disks are not woken up." />
        <Toggle bind:checked={events.updates} label="Updates" hint="Results of the daily update, pending security updates, new LocoStor releases." />
        <div class="grid gap-3 sm:grid-cols-3">
          <div>
            <label class="label" for="e-temp">Disk temperature</label>
            <div class="flex items-center gap-1.5"><input id="e-temp" class="input" type="number" min="0" max="100" bind:value={events.temperature} /><span class="text-sm text-zinc-500">°C</span></div>
            <p class="hint">0 = off</p>
          </div>
          <div>
            <label class="label" for="e-space">Filesystem full</label>
            <div class="flex items-center gap-1.5"><input id="e-space" class="input" type="number" min="0" max="100" bind:value={events.space} /><span class="text-sm text-zinc-500">%</span></div>
            <p class="hint">0 = off</p>
          </div>
          <div>
            <label class="label" for="e-sev">Send from</label>
            <select id="e-sev" class="input" bind:value={events.min_severity}>
              <option value="info">Info (everything)</option>
              <option value="warning">Warnings</option>
              <option value="error">Errors only</option>
            </select>
          </div>
        </div>
        <button class="btn btn-primary">Save</button>
      </form>
    </section>

    <section class="card">
      <div class="card-header"><h2 class="card-title">Recent messages</h2></div>
      {#if cfg.history.length === 0}
        <p class="px-4 py-8 text-center text-sm text-zinc-500">Nothing sent yet.</p>
      {:else}
        <ul class="max-h-[26rem] divide-y divide-zinc-100 overflow-auto dark:divide-ink-800">
          {#each cfg.history as m}
            <li class="px-4 py-2.5 text-sm">
              <div class="flex flex-wrap items-center gap-2">
                <Badge tone={sevTone[m.severity]}>{m.severity}</Badge>
                <span class="font-medium">{m.title}</span>
                <span class="ml-auto text-xs text-zinc-500">{dateTime(m.time)}</span>
              </div>
              <p class="mt-0.5 text-xs whitespace-pre-line text-zinc-500">{m.body}</p>
              <p class="mt-0.5 text-xs">
                {#if m.sent.length}<span class="text-emerald-600 dark:text-emerald-400">sent via {m.sent.join(', ')}</span>{/if}
                {#each m.errors ?? [] as e}<span class="block text-red-600 dark:text-red-400">{e}</span>{/each}
                {#if !m.sent.length && !m.errors?.length}<span class="text-zinc-500">not sent – no channel set up</span>{/if}
              </p>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  </div>
{/if}

<Modal title="Set up e-mail" bind:open={mailOpen} wide onclose={() => stopPoll?.()}>
  <ol class="mb-4 flex gap-2 text-xs">
    {#each ['Address', 'Sign in', 'Recipients'] as label, i}
      <li class="rounded-full px-2.5 py-1 font-medium {step === i + 1 ? 'bg-brand-400/20 text-brand-700 dark:text-brand-300' : step > i + 1 ? 'text-emerald-600' : 'text-zinc-500'}">{i + 1}. {label}</li>
    {/each}
  </ol>

  {#if step === 1}
    <form class="space-y-3" onsubmit={detect}>
      <label class="label" for="m-addr">Which address should send the alerts?</label>
      <!-- svelte-ignore a11y_autofocus -->
      <input id="m-addr" class="input" type="email" bind:value={mail.address} required placeholder="name@outlook.de" autofocus />
      <p class="hint">The provider and server are detected automatically.</p>
      <button class="btn btn-primary" disabled={busy}>{busy ? 'Detecting…' : 'Continue'}</button>
    </form>
  {:else if step === 2 && provider}
    <div class="space-y-4 text-sm">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-zinc-500">Provider:</span>
        <select class="input w-auto" value={provider.id} onchange={(e) => changeProvider(e.target.value)}>
          {#each providers as p}<option value={p.id}>{p.name}</option>{/each}
        </select>
      </div>
      {#if provider.hint}<p class="rounded-md bg-sky-500/10 px-3 py-2 text-sky-800 dark:text-sky-300">{provider.hint} {#if provider.link}<a class="underline" href={provider.link} target="_blank" rel="noopener noreferrer">Open settings</a>{/if}</p>{/if}

      {#if provider.auth === 'oauth' && !usePassword}
        {#if oauth?.user_code}
          <div class="rounded-lg border border-zinc-200 p-4 text-center dark:border-ink-800">
            <p>Open <a class="font-medium text-brand-700 underline dark:text-brand-300" href={oauth.verification_uri} target="_blank" rel="noopener noreferrer">{oauth.verification_uri}</a> and enter this code:</p>
            <p class="mono my-3 text-3xl font-semibold tracking-widest select-all">{oauth.user_code}</p>
            <p class="flex items-center justify-center gap-2 text-xs text-zinc-500"><span class="h-3 w-3 animate-spin rounded-full border-2 border-zinc-300 border-t-brand-500"></span>Waiting for the sign-in…</p>
          </div>
        {:else if oauth?.auth_url}
          <div class="space-y-2 rounded-lg border border-zinc-200 p-4 dark:border-ink-800">
            <p>1. Sign in with Google in the new tab (<a class="underline" href={oauth.auth_url} target="_blank" rel="noopener noreferrer">open again</a>) and allow sending mail.</p>
            <p>2. Google then opens a page that does not load (<span class="mono">127.0.0.1</span>). Copy its full address from the address bar and paste it here:</p>
            <div class="flex gap-2">
              <input class="input mono" bind:value={googleUrl} placeholder="http://127.0.0.1:53682/?code=…" />
              <button class="btn btn-primary" onclick={googleCode} disabled={busy || !googleUrl}>Finish</button>
            </div>
          </div>
        {:else}
          {#if needsOwnApp || ownApp}
            <div class="space-y-3 rounded-lg border border-zinc-200 p-3 dark:border-ink-800">
              <p class="font-medium">App registration</p>
              {#if provider.id === 'microsoft'}
                <ol class="list-decimal space-y-0.5 pl-5 text-xs text-zinc-600 dark:text-zinc-400">
                  <li>Open <a class="underline" href="https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade" target="_blank" rel="noopener noreferrer">Entra → App registrations</a> → New registration, name "LocoStor".</li>
                  <li>Supported accounts: "Any Entra ID tenant + personal Microsoft accounts". No redirect URI.</li>
                  <li>Authentication → "Allow public client flows" = Yes.</li>
                  <li>API permissions → Add → APIs my organization uses → "Office 365 Exchange Online" → Delegated → SMTP.Send.</li>
                  <li>Copy the "Application (client) ID" here.</li>
                </ol>
                <input class="input mono" bind:value={mail.client_id} placeholder="Application (client) ID" />
                <input class="input mono" bind:value={mail.tenant} placeholder="Tenant (empty = common)" />
              {:else}
                <ol class="list-decimal space-y-0.5 pl-5 text-xs text-zinc-600 dark:text-zinc-400">
                  <li>Open <a class="underline" href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noopener noreferrer">Google Cloud → Credentials</a> (create a project if asked).</li>
                  <li>Configure the OAuth consent screen (External, add yourself as test user).</li>
                  <li>Create credentials → OAuth client ID → Application type "Desktop app".</li>
                  <li>Copy client ID and client secret here.</li>
                </ol>
                <input class="input mono" bind:value={mail.client_id} placeholder="Client ID" />
                <input class="input mono" bind:value={mail.client_secret} placeholder="Client secret" />
              {/if}
            </div>
          {/if}
          <div class="flex flex-wrap items-center gap-3">
            <button class="btn btn-primary" onclick={signIn} disabled={busy || ((needsOwnApp || ownApp) && !mail.client_id)}>
              <Icon name="key" size={16} />Sign in with {provider.id === 'microsoft' ? 'Microsoft' : 'Google'}
            </button>
            {#if builtin && !ownApp}<button class="text-xs text-zinc-500 underline" onclick={() => (ownApp = true)}>use my own app registration</button>{/if}
            {#if provider.id === 'google'}<button class="text-xs text-zinc-500 underline" onclick={() => (usePassword = true)}>use an app password instead</button>{/if}
          </div>
          {#if cfg.email?.oauth && cfg.email.provider === provider.id}
            <p class="text-xs text-emerald-600">Already signed in as {cfg.email.username}. <button class="underline" onclick={() => (step = 3)}>Keep it</button></p>
          {/if}
        {/if}
      {:else}
        <form id="mail-auth" class="space-y-3" onsubmit={(e) => { e.preventDefault(); step = 3; }}>
          {#if provider.id === 'custom' || provider.auth === 'oauth'}
            <div class="grid gap-3 sm:grid-cols-[1fr_6rem_9rem]">
              <div><label class="label" for="m-host">SMTP server</label><input id="m-host" class="input mono" bind:value={mail.host} required /></div>
              <div><label class="label" for="m-port">Port</label><input id="m-port" class="input" type="number" bind:value={mail.port} required /></div>
              <div>
                <label class="label" for="m-sec">Security</label>
                <select id="m-sec" class="input" bind:value={mail.security}><option value="starttls">STARTTLS</option><option value="tls">SSL/TLS</option><option value="none">none</option></select>
              </div>
            </div>
          {:else}
            <p class="text-xs text-zinc-500">Server: <span class="mono">{mail.host}:{mail.port}</span> ({mail.security})</p>
          {/if}
          <div class="grid gap-3 sm:grid-cols-2">
            <div><label class="label" for="m-user">User name</label><input id="m-user" class="input" bind:value={mail.username} required autocomplete="off" /></div>
            <div>
              <label class="label" for="m-pw">{provider.auth === 'password' ? 'Password' : 'App password'}</label>
              <input id="m-pw" class="input" type="password" bind:value={mail.password} autocomplete="new-password" placeholder={cfg.email?.has_password ? 'unchanged' : ''} required={!cfg.email?.has_password} />
            </div>
          </div>
          <button class="btn btn-primary">Continue</button>
        </form>
      {/if}
      {#if err}<p class="flex items-center gap-1.5 text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{err}</p>{/if}
    </div>
  {:else}
    <form class="space-y-3 text-sm" onsubmit={saveMail}>
      <div><label class="label" for="m-to">Send alerts to</label><input id="m-to" class="input" bind:value={mail.to} required placeholder="me@example.com, partner@example.com" /></div>
      <div><label class="label" for="m-from">Sender address</label><input id="m-from" class="input" type="email" bind:value={mail.from} required /></div>
      {#if err}<p class="flex items-center gap-1.5 text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{err}</p>{/if}
      <div class="flex gap-2">
        <button type="button" class="btn btn-secondary" onclick={() => (step = 2)}>Back</button>
        <button class="btn btn-primary" disabled={busy}>Save and send a test</button>
      </div>
    </form>
  {/if}
</Modal>

<Modal title="Set up Telegram" bind:open={tgOpen} wide onclose={() => stopTg?.()}>
  {#if tgStep === 1}
    <form class="space-y-3 text-sm" onsubmit={checkToken}>
      <ol class="list-decimal space-y-1 pl-5 text-zinc-600 dark:text-zinc-300">
        <li>Open <a class="font-medium text-brand-700 underline dark:text-brand-300" href="https://t.me/BotFather" target="_blank" rel="noopener noreferrer">@BotFather</a> in Telegram and send <span class="mono">/newbot</span>.</li>
        <li>Choose a name (e.g. "My NAS") and a user name ending in "bot" (e.g. <span class="mono">mynas_alert_bot</span>).</li>
        <li>BotFather answers with a token like <span class="mono">123456789:AAH…</span> – paste it here.</li>
      </ol>
      <input class="input mono" bind:value={tgToken} required placeholder="123456789:AAH…" autocomplete="off" />
      {#if tgErr}<p class="flex items-center gap-1.5 text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{tgErr}</p>{/if}
      <button class="btn btn-primary" disabled={busy}>{busy ? 'Checking…' : 'Continue'}</button>
    </form>
  {:else if tgLink}
    <div class="space-y-4 text-sm">
      <div class="flex flex-col items-center gap-4 sm:flex-row">
        <img src={tgLink.qr} alt="QR code to open the bot" class="h-40 w-40 rounded-md bg-white p-1" />
        <div class="space-y-2">
          <p>Bot <b>@{tgLink.bot}</b> is ready. Open it and press <b>Start</b> – on the phone scan the code, on this computer use the button:</p>
          <a class="btn btn-primary" href={tgLink.link} target="_blank" rel="noopener noreferrer"><Icon name="send" size={16} />Open Telegram</a>
          <p class="hint">For a group: add the bot to the group, then send <span class="mono select-all">/start {tgLink.code}</span> there.</p>
        </div>
      </div>
      <div class="rounded-lg border border-zinc-200 p-3 dark:border-ink-800">
        <p class="mb-1 font-medium">Connected chats</p>
        {#if tgChats.length === 0 && !(cfg.telegram?.chats?.length)}
          <p class="flex items-center gap-2 text-xs text-zinc-500"><span class="h-3 w-3 animate-spin rounded-full border-2 border-zinc-300 border-t-brand-500"></span>Waiting for "Start"…</p>
        {/if}
        <ul class="space-y-0.5">
          {#each [...(cfg.telegram?.chats ?? []), ...tgChats.filter((c) => !cfg.telegram?.chats?.some((x) => x.id === c.id))] as c}
            <li class="flex items-center gap-1.5 text-emerald-700 dark:text-emerald-400"><Icon name="check" size={14} />{c.title || c.id}</li>
          {/each}
        </ul>
      </div>
      {#if tgErr}<p class="flex items-center gap-1.5 text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{tgErr}</p>{/if}
    </div>
  {:else}
    <p class="text-sm text-zinc-500">Connecting to the bot…</p>
  {/if}
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (tgOpen = false)}>Cancel</button>
    {#if tgStep === 2}<button class="btn btn-primary" onclick={saveTelegram} disabled={busy || (tgChats.length === 0 && !cfg?.telegram?.chats?.length)}>Save and send a test</button>{/if}
  {/snippet}
</Modal>
