<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm, updates, loadUpdateStatus } from '../lib/ui.svelte.js';
  import { dateTime } from '../lib/format.js';

  let checking = $state(false);
  let working = $state(''); // '', 'update', 'rollback'
  let restarting = $state(false);

  let s = $derived(updates.status);

  onMount(loadUpdateStatus);

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

  // After an update or rollback the server restarts; wait until it answers
  // with a different version, then reload the page.
  async function waitForRestart(oldVersion) {
    restarting = true;
    const deadline = Date.now() + 90_000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 2000));
      try {
        const res = await fetch('/api/auth/me', { credentials: 'same-origin' });
        const me = await res.json();
        if (me.version && me.version !== oldVersion) {
          location.reload();
          return;
        }
      } catch {
        // still restarting
      }
    }
    restarting = false;
    toast.error('The server did not come back within 90 seconds. Check "journalctl -u locostor".');
  }

  async function run(kind) {
    const isUpdate = kind === 'update';
    const yes = await confirm({
      title: isUpdate ? 'Install update' : 'Roll back',
      message: isUpdate
        ? `Update LocoStor from ${s.current} to ${s.latest}? The web UI restarts; shares keep running.`
        : `Go back to version ${s.previous_version}? The web UI restarts; shares keep running.`,
      confirmLabel: isUpdate ? 'Update now' : 'Roll back',
      danger: !isUpdate,
    });
    if (!yes) return;
    working = kind;
    try {
      await api.post(isUpdate ? '/api/update/apply' : '/api/update/rollback');
      waitForRestart(s.current);
    } catch (e) {
      toast.error(e.message);
    } finally {
      working = '';
    }
  }
</script>

<PageHeader title="Update" description="LocoStor updates itself from GitHub Releases">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={check} disabled={checking}>
      <Icon name="refresh" size={16} class={checking ? 'animate-spin' : ''} />Check now
    </button>
  {/snippet}
</PageHeader>

{#if restarting}
  <div class="fixed inset-0 z-[70] flex flex-col items-center justify-center gap-4 bg-zinc-950/80 text-white backdrop-blur-sm">
    <span class="h-10 w-10 animate-spin rounded-full border-4 border-white/20 border-t-sky-400"></span>
    <p class="text-lg font-medium">Restarting LocoStor…</p>
    <p class="text-sm text-zinc-400">The page reloads automatically.</p>
  </div>
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
      <div class="flex flex-wrap gap-2 border-t border-zinc-200 px-4 py-3 dark:border-zinc-800">
        <button class="btn btn-primary" disabled={!s.can_update || working !== ''} onclick={() => run('update')}>
          <Icon name="download" size={16} />{working === 'update' ? 'Installing…' : 'Install update'}
        </button>
        <button class="btn btn-secondary" disabled={!s.previous_version || !!s.disabled_reason || working !== ''} onclick={() => run('rollback')}>
          <Icon name="rollback" size={16} />{working === 'rollback' ? 'Rolling back…' : 'Roll back'}
        </button>
      </div>
    </section>

    <section class="card lg:col-span-2">
      <div class="card-header">
        <h2 class="card-title">Release notes {s.latest ? `– ${s.latest}` : ''}</h2>
        {#if s.release_url}
          <a href={s.release_url} target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 text-xs font-medium text-sky-600 hover:underline dark:text-sky-400">
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
