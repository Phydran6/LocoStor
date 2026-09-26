<script>
  import { onMount, onDestroy } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Progress from '../components/Progress.svelte';
  import PageHeader from '../components/PageHeader.svelte';
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

  onMount(async () => {
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

<PageHeader title="Update" description="LocoStor updates itself from GitHub Releases">
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
        <button class="btn btn-primary" disabled={!s.can_update || running || !!waiting} onclick={() => run('update')}>
          <Icon name="download" size={16} />Install update
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
