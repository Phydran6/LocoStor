<script>
  import { onMount, onDestroy } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Progress from '../components/Progress.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import HostAgentCard from '../components/HostAgentCard.svelte';
  import Modal from '../components/Modal.svelte';
  import Toggle from '../components/Toggle.svelte';
  import Terminal from '../components/Terminal.svelte';
  import { host, loadHost } from '../lib/host.svelte.js';
  import { showTask, followTask } from '../lib/tasks.svelte.js';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm, updates, loadUpdateStatus } from '../lib/ui.svelte.js';
  import { bytes, dateTime } from '../lib/format.js';

  let checking = $state(false);
  let progress = $state(null); // server-side progress of the running operation
  let waiting = $state(''); // '' | 'restart' - waiting for the new process
  let done = $state('');
  let timer;

  let s = $derived(updates.status);
  let running = $derived(progress && !['idle', 'failed'].includes(progress.phase));
  let percent = $derived(progress?.total ? Math.round((progress.bytes / progress.total) * 100) : 0);

  const phases = ['preparing', 'downloading', 'verifying', 'installing', 'restarting'];
  const labels = { preparing: 'Prepare', downloading: 'Download', verifying: 'Verify', installing: 'Install', restarting: 'Restart' };

  // --- patch automation ---------------------------------------------------
  let maint = $state(null);
  let hostSys = $state(null);
  let taskList = $state([]);
  let hostTasks = $state([]);
  let runOpen = $state(false);
  let runForm = $state({ locostor: 'auto', system: 'all', host: 'notify' });
  let settingsBusy = $state(false);
  // Live output of the running system task, shown inline.
  let live = $state({ id: '', lines: [], task: null });
  let liveCtrl;

  let hostUpdates = $derived(host.info?.available && host.info.features?.includes('system-updates'));

  async function loadMaint() {
    try {
      maint = await api.get('/api/maintenance');
      if (maint.running && live.id !== maint.running) watch(maint.running);
    } catch {
      maint = null;
    }
    api.get('/api/tasks').then((t) => (taskList = t)).catch(() => {});
    if (hostUpdates) {
      api.get('/api/host/system/updates').then((r) => (hostSys = r)).catch(() => (hostSys = null));
      api.get('/api/host/tasks').then((t) => (hostTasks = t)).catch(() => {});
    }
  }

  function watch(id) {
    liveCtrl?.abort();
    const c = new AbortController();
    liveCtrl = c;
    live = { id, lines: [], task: { status: 'running' } };
    followTask('/api', id, (l) => live.lines.push(...l), c.signal)
      .then((t) => {
        if (!t || c.signal.aborted) return;
        live.task = t;
        loadMaint();
        loadUpdateStatus();
        if (live.lines.some((l) => l.includes('is running again'))) {
          done = 'new version';
          setTimeout(() => location.reload(), 2500);
        }
      })
      .catch(() => {});
  }
  onDestroy(() => liveCtrl?.abort());

  function openRun() {
    const st = maint?.settings;
    runForm = {
      locostor: s?.update_available ? 'auto' : 'notify',
      system: st?.system && st.system !== 'off' && st.system !== 'notify' ? st.system : 'all',
      host: hostUpdates ? (st?.host ?? 'notify') : 'off',
    };
    runOpen = true;
  }

  async function startRun() {
    runOpen = false;
    try {
      const r = await api.post('/api/maintenance/run', runForm);
      watch(r.task);
    } catch (e) {
      toast.error(e.message);
    }
  }

  async function saveSettings(e) {
    e.preventDefault();
    settingsBusy = true;
    try {
      maint = await api.put('/api/maintenance', maint.settings);
      toast.success('Automatic updates saved');
    } catch (err) {
      toast.error(err.message);
    } finally {
      settingsBusy = false;
    }
  }

  async function startSimple(path, body, base = '/api') {
    try {
      const r = await api.post(path, body);
      if (base === '/api') watch(r.task);
      else showTask(r.task, { base, title: 'Proxmox host', onDone: loadMaint });
    } catch (e) {
      toast.error(e.message);
    }
  }

  const sysModes = [
    { value: 'all', label: 'Install all updates' },
    { value: 'security', label: 'Install security updates only' },
    { value: 'notify', label: 'Only check and notify' },
    { value: 'off', label: 'Off' },
  ];
  const appModes = [
    { value: 'auto', label: 'Install new releases' },
    { value: 'notify', label: 'Only notify' },
    { value: 'off', label: 'Off' },
  ];
  const statusTone = { running: 'info', ok: 'ok', failed: 'bad', interrupted: 'warn', restarting: 'info' };

  onMount(async () => {
    await loadHost();
    loadMaint();
    await loadUpdateStatus();
    // Show a run started in another tab (or before a page reload).
    try {
      const p = await api.get('/api/update/progress');
      if (p.phase !== 'idle') {
        progress = p;
        if (!['failed'].includes(p.phase)) poll();
      }
    } catch {
      // ignore
    }
  });
  onDestroy(() => clearTimeout(timer));

  async function check() {
    checking = true;
    try {
      updates.status = await api.post('/api/update/check');
      if (updates.status.error) toast.error(updates.status.error);
      else if (!updates.status.update_available) toast.success('LocoStor is up to date');
    } catch (e) {
      toast.error(e.message);
    } finally {
      checking = false;
    }
  }

  function poll() {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      try {
        progress = await api.get('/api/update/progress');
        if (progress.phase === 'failed') {
          loadUpdateStatus();
          return;
        }
        if (progress.phase === 'restarting') {
          waitForRestart(progress.from);
          return;
        }
      } catch {
        // The server may already be restarting.
        if (progress?.phase === 'installing' || progress?.phase === 'restarting') {
          waitForRestart(progress.from);
          return;
        }
      }
      poll();
    }, 400);
  }

  // The process replaces itself; wait until it answers with another version.
  async function waitForRestart(oldVersion) {
    waiting = 'restart';
    const deadline = Date.now() + 120_000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 1500));
      try {
        const res = await fetch('/api/auth/me', { credentials: 'same-origin' });
        const me = await res.json();
        if (me.version && me.version !== oldVersion) {
          waiting = '';
          done = me.version;
          setTimeout(() => location.reload(), 2500);
          return;
        }
      } catch {
        // still restarting
      }
    }
    waiting = '';
    toast.error('LocoStor did not come back within 2 minutes. Check "journalctl -u locostor".');
  }

  async function run(kind) {
    const isUpdate = kind === 'update';
    const yes = await confirm({
      title: isUpdate ? 'Install update' : 'Roll back',
      message: isUpdate
        ? `Update LocoStor from ${s.current} to ${s.latest}? Only the web UI restarts – shares keep running.`
        : `Go back to version ${s.previous_version}? Only the web UI restarts – shares keep running.`,
      confirmLabel: isUpdate ? 'Update now' : 'Roll back',
      danger: !isUpdate,
    });
    if (!yes) return;
    done = '';
    try {
      progress = await api.post(isUpdate ? '/api/update/apply' : '/api/update/rollback');
      poll();
    } catch (e) {
      toast.error(e.message);
    }
  }

  function time(t) {
    return new Date(t).toLocaleTimeString();
  }
</script>

<PageHeader title="Update" description="LocoStor, the container and the Proxmox host – patched automatically, with live output">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={check} disabled={checking || running}>
      <Icon name="refresh" size={16} class={checking ? 'animate-spin' : ''} />Check now
    </button>
  {/snippet}
</PageHeader>

{#if progress && progress.phase !== 'idle'}
  {@const current = phases.indexOf(progress.phase)}
  <section class="card mb-4">
    <div class="card-header">
      <h2 class="card-title">
        {progress.action === 'rollback' ? 'Rollback' : 'Update'}
        <span class="mono font-normal text-zinc-500">{progress.from} → {progress.target || '…'}</span>
      </h2>
      {#if done}
        <Badge tone="ok" dot>done – now {done}</Badge>
      {:else if progress.phase === 'failed'}
        <Badge tone="bad" dot>failed</Badge>
      {:else}
        <Badge tone="info" dot>running</Badge>
      {/if}
    </div>

    <div class="space-y-4 p-4">
      <ol class="flex flex-wrap gap-x-2 gap-y-2 text-xs">
        {#each phases as ph, i}
          {@const state = done || (i < current) ? 'done' : i === current && progress.phase !== 'failed' ? 'active' : 'todo'}
          <li class="flex items-center gap-2">
            <span
              class="flex h-6 items-center gap-1.5 rounded-full px-2.5 font-medium
                {state === 'done'
                ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
                : state === 'active'
                  ? 'bg-brand-400/15 text-brand-700 dark:text-brand-300'
                  : 'bg-zinc-100 text-zinc-500 dark:bg-ink-800'}"
            >
              {#if state === 'done'}✓{:else if state === 'active'}<span class="h-1.5 w-1.5 animate-pulse rounded-full bg-current"></span>{/if}
              {labels[ph]}
            </span>
            {#if i < phases.length - 1}<span class="text-zinc-300 dark:text-ink-700">—</span>{/if}
          </li>
        {/each}
      </ol>

      {#if progress.phase === 'downloading' || (progress.total && current > 1)}
        <div>
          <div class="mb-1 flex justify-between text-xs text-zinc-500">
            <span>Download</span>
            <span class="tabular-nums">{bytes(progress.bytes)} / {bytes(progress.total)} · {percent}%</span>
          </div>
          <Progress value={percent} tone="brand" label="Download progress" />
        </div>
      {/if}

      {#if waiting}
        <p class="flex items-center gap-2 text-sm text-zinc-600 dark:text-zinc-300">
          <span class="h-4 w-4 animate-spin rounded-full border-2 border-zinc-300 border-t-brand-500 dark:border-ink-700"></span>
          Waiting for LocoStor to come back…
        </p>
      {/if}
      {#if done}
        <p class="text-sm text-emerald-700 dark:text-emerald-400">LocoStor {done} is running. The page reloads in a moment.</p>
      {/if}

      <div class="max-h-56 overflow-auto rounded-md bg-zinc-50 p-3 font-mono text-xs leading-relaxed dark:bg-ink-950">
        {#each progress.log as line}
          <p class={line.msg.startsWith('Failed') ? 'text-red-600 dark:text-red-400' : 'text-zinc-600 dark:text-zinc-400'}>
            <span class="text-zinc-400 dark:text-zinc-600">{time(line.time)}</span>
            {line.msg}
          </p>
        {/each}
      </div>
    </div>
  </section>
{/if}

{#if live.id}
  <section class="card mb-4">
    <div class="card-header">
      <h2 class="card-title flex items-center gap-2"><Icon name="terminal" size={16} />{live.task?.title || 'System update'}</h2>
      <div class="flex items-center gap-2">
        <Badge tone={statusTone[live.task?.status] ?? 'muted'} dot>{live.task?.status ?? '…'}</Badge>
        {#if live.task?.status !== 'running'}<button class="btn-icon" onclick={() => (live = { id: '', lines: [], task: null })} aria-label="Close output"><Icon name="x" size={16} /></button>{/if}
      </div>
    </div>
    <div class="p-3">
      <Terminal lines={live.lines} running={live.task?.status === 'running'} height="h-96" />
      <p class="hint">Shares keep running during the update. When LocoStor itself is updated, the page reloads once the new version answers.</p>
    </div>
  </section>
{/if}

{#if s}
  <div class="grid gap-4 lg:grid-cols-3">
    <section class="card">
      <div class="card-header"><h2 class="card-title">Version</h2></div>
      <dl class="space-y-3 px-4 py-4 text-sm">
        <div class="flex justify-between"><dt class="text-zinc-500">Installed</dt><dd class="mono font-medium">{s.current}</dd></div>
        <div class="flex justify-between">
          <dt class="text-zinc-500">Latest release</dt>
          <dd class="mono font-medium">
            {s.latest || '–'}
            {#if s.update_available}<Badge tone="info">new</Badge>{:else if s.latest}<Badge tone="ok">current</Badge>{/if}
          </dd>
        </div>
        <div class="flex justify-between"><dt class="text-zinc-500">Last check</dt><dd>{dateTime(s.checked_at)}</dd></div>
        {#if s.previous_version}
          <div class="flex justify-between"><dt class="text-zinc-500">Rollback target</dt><dd class="mono">{s.previous_version}</dd></div>
        {/if}
      </dl>
      {#if s.error}
        <p class="mx-4 mb-4 flex items-start gap-1.5 rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-700 dark:text-red-400"><Icon name="alert" size={14} class="mt-px" />{s.error}</p>
      {/if}
      {#if s.disabled_reason}
        <p class="mx-4 mb-4 flex items-start gap-1.5 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400"><Icon name="info" size={14} class="mt-px" />{s.disabled_reason}</p>
      {/if}
      <div class="flex flex-wrap gap-2 border-t border-zinc-200 px-4 py-3 dark:border-ink-800">
        <button class="btn btn-primary" disabled={running || !!waiting || live.task?.status === 'running'} onclick={openRun}>
          <Icon name="download" size={16} />{s.update_available ? 'Install update' : 'Update system'}
        </button>
        <button class="btn btn-secondary" disabled={!s.previous_version || !!s.disabled_reason || running || !!waiting} onclick={() => run('rollback')}>
          <Icon name="rollback" size={16} />Roll back
        </button>
      </div>
    </section>

    <section class="card lg:col-span-2">
      <div class="card-header">
        <h2 class="card-title">Release notes {s.latest ? `– ${s.latest}` : ''}</h2>
        {#if s.release_url}
          <a href={s.release_url} target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 text-xs font-medium text-brand-700 hover:underline dark:text-brand-300">
            GitHub <Icon name="external" size={13} />
          </a>
        {/if}
      </div>
      {#if s.release_notes}
        <pre class="max-h-[28rem] overflow-auto px-4 py-4 font-sans text-sm leading-relaxed whitespace-pre-wrap text-zinc-700 dark:text-zinc-300">{s.release_notes}</pre>
      {:else}
        <p class="px-4 py-10 text-center text-sm text-zinc-500">No release notes available.</p>
      {/if}
    </section>
  </div>
{/if}

{#if maint}
  <div class="mt-4 grid gap-4 lg:grid-cols-2">
    <section class="card">
      <div class="card-header">
        <h2 class="card-title flex items-center gap-2"><Icon name="calendar" size={16} />Automatic updates</h2>
        <Badge tone={maint.settings.enabled ? 'ok' : 'muted'} dot>{maint.settings.enabled ? 'daily' : 'off'}</Badge>
      </div>
      <form class="space-y-4 p-4" onsubmit={saveSettings}>
        <Toggle bind:checked={maint.settings.enabled} label="Run every day" hint="Checks for security fixes and installs updates without you having to look." />
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="label" for="m-time">Time</label>
            <input id="m-time" type="time" class="input" bind:value={maint.settings.time} required />
          </div>
          <div>
            <label class="label" for="m-app">LocoStor releases</label>
            <select id="m-app" class="input" bind:value={maint.settings.locostor}>
              {#each appModes as m}<option value={m.value}>{m.label}</option>{/each}
            </select>
          </div>
          <div>
            <label class="label" for="m-sys">Container packages (apt)</label>
            <select id="m-sys" class="input" bind:value={maint.settings.system}>
              {#each sysModes as m}<option value={m.value}>{m.label}</option>{/each}
            </select>
          </div>
          <div>
            <label class="label" for="m-host">Proxmox host packages</label>
            <select id="m-host" class="input" bind:value={maint.settings.host} disabled={!hostUpdates}>
              {#each sysModes as m}<option value={m.value}>{m.label}</option>{/each}
            </select>
            <p class="hint">{hostUpdates ? 'Uses apt dist-upgrade, as Proxmox requires. Kernel updates need a reboot.' : 'Needs the host agent 0.5.0 or newer.'}</p>
          </div>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-zinc-500">
          <span>{maint.next_run ? `Next run: ${dateTime(maint.next_run)}` : 'Not scheduled'}</span>
          <button class="btn btn-primary" disabled={settingsBusy}>Save</button>
        </div>
        {#if maint.last}
          <div class="rounded-md border border-zinc-200 px-3 py-2 text-xs dark:border-ink-800">
            <p class="flex items-center justify-between font-medium">
              <span>Last run {dateTime(maint.last.started)} ({maint.last.trigger})</span>
              <button type="button" class="text-brand-700 hover:underline dark:text-brand-300" onclick={() => showTask(maint.last.task, { title: 'System update' })}>Show log</button>
            </p>
            <ul class="mt-1 space-y-0.5 {maint.last.ok ? 'text-zinc-600 dark:text-zinc-400' : 'text-red-600 dark:text-red-400'}">
              {#each maint.last.summary as line}<li>• {line}</li>{/each}
            </ul>
          </div>
        {/if}
      </form>
    </section>

    <section class="card">
      <div class="card-header">
        <h2 class="card-title flex items-center gap-2"><Icon name="package" size={16} />System packages</h2>
      </div>
      <div class="divide-y divide-zinc-100 dark:divide-ink-800">
        {#snippet sysRow(label, st, base)}
          <div class="space-y-2 px-4 py-3 text-sm">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p class="font-medium">{label}</p>
                <p class="text-xs text-zinc-500">{st?.os || '–'} · checked {dateTime(st?.checked_at)}</p>
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                {#if !st?.checked_at}
                  <Badge>not checked yet</Badge>
                {:else if st.packages.length === 0}
                  <Badge tone="ok" dot>up to date</Badge>
                {:else}
                  <Badge tone={st.security ? 'bad' : 'warn'} dot>{st.packages.length} updates{st.security ? `, ${st.security} security` : ''}</Badge>
                {/if}
                {#if st?.reboot_required}<Badge tone="warn">reboot required</Badge>{/if}
                <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => startSimple(base === '/api' ? '/api/system/check' : '/api/host/system/check', {}, base)}>Check</button>
              </div>
            </div>
            {#if st?.packages?.length}
              <details class="text-xs">
                <summary class="cursor-pointer text-zinc-500 select-none">Show packages</summary>
                <ul class="mono mt-1 max-h-40 space-y-0.5 overflow-auto">
                  {#each st.packages as p}
                    <li class={p.security ? 'text-red-600 dark:text-red-400' : ''}>{p.name} {p.from} → {p.to}</li>
                  {/each}
                </ul>
              </details>
            {/if}
          </div>
        {/snippet}
        {@render sysRow('This container', maint.system, '/api')}
        {#if hostUpdates}{@render sysRow(`Proxmox host ${host.info.hostname}`, hostSys, '/api/host')}{/if}
        <div class="flex flex-wrap items-center gap-2 px-4 py-3 text-xs text-zinc-500">
          Optional:
          <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => startSimple('/api/system/install', { packages: ['wsdd'] })}>Install wsdd in the container</button>
          {#if hostUpdates}
            <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => startSimple('/api/host/system/install', { packages: ['wsdd'] }, '/api/host')}>… on the host</button>
          {/if}
          <span>(WS-Discovery: Windows 10/11 lists the server in the network)</span>
        </div>
      </div>
    </section>
  </div>

  <section class="card mt-4">
    <div class="card-header"><h2 class="card-title flex items-center gap-2"><Icon name="terminal" size={16} />Task log</h2></div>
    {#if taskList.length + hostTasks.length === 0}
      <p class="px-4 py-6 text-center text-sm text-zinc-500">No tasks yet.</p>
    {:else}
      <table class="table">
        <thead><tr><th>Started</th><th>Task</th><th>Where</th><th>Status</th><th></th></tr></thead>
        <tbody>
          {#each [...taskList.map((t) => ({ ...t, base: '/api' })), ...hostTasks.map((t) => ({ ...t, base: '/api/host' }))]
            .sort((a, b) => b.started.localeCompare(a.started))
            .slice(0, 15) as t}
            <tr>
              <td class="text-xs whitespace-nowrap">{dateTime(t.started)}</td>
              <td>{t.title}</td>
              <td class="text-xs text-zinc-500">{t.base === '/api' ? 'container' : 'host'}</td>
              <td><Badge tone={statusTone[t.status] ?? 'muted'} dot>{t.status}</Badge></td>
              <td class="text-right">
                <button class="btn-icon" onclick={() => showTask(t.id, { base: t.base, title: t.title })} title="Show output" aria-label="Show output of {t.title}"><Icon name="terminal" size={16} /></button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{/if}

<HostAgentCard />

<Modal title="Update now" bind:open={runOpen}>
  <div class="space-y-4 text-sm">
    <p class="text-zinc-600 dark:text-zinc-300">Runs now and shows the output live. Shares stay available; Samba restarts briefly if it gets an update.</p>
    <div>
      <label class="label" for="r-app">LocoStor {s?.update_available ? `${s.current} → ${s.latest}` : `(${s?.current}, latest)`}</label>
      <select id="r-app" class="input" bind:value={runForm.locostor}>
        <option value="auto">{s?.update_available ? 'Install the new release' : 'Check for a new release'}</option>
        <option value="notify">Only check</option>
        <option value="off">Skip</option>
      </select>
    </div>
    <div>
      <label class="label" for="r-sys">Container packages (apt-get update + upgrade)</label>
      <select id="r-sys" class="input" bind:value={runForm.system}>
        {#each sysModes as m}<option value={m.value}>{m.label}</option>{/each}
      </select>
    </div>
    <div>
      <label class="label" for="r-host">Proxmox host packages</label>
      <select id="r-host" class="input" bind:value={runForm.host} disabled={!hostUpdates}>
        {#each sysModes as m}<option value={m.value}>{m.label}</option>{/each}
      </select>
    </div>
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (runOpen = false)}>Cancel</button>
    <button class="btn btn-primary" onclick={startRun}><Icon name="play" size={15} />Start</button>
  {/snippet}
</Modal>
