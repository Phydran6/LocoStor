<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Progress from '../components/Progress.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { bytes, capacity, duration, percent, tone } from '../lib/format.js';
  import { host, loadHost } from '../lib/host.svelte.js';

  let data = $state(null);
  let error = $state('');
  let smart = $state(null);

  async function load() {
    try {
      data = await api.get('/api/dashboard');
      error = '';
    } catch (e) {
      error = e.message;
    }
  }

  // Shares of the Proxmox host, if it is connected.
  let hostSmb = $state(null);
  let hostNfs = $state(null);

  onMount(() => {
    load();
    api.get('/api/smart').then((r) => (smart = r.disks)).catch(() => (smart = []));
    loadHost().then(() => {
      if (!host.info?.available) return;
      if (host.info.samba) api.get('/api/host/smb/shares').then((r) => (hostSmb = r.shares.filter((s) => s.path).length)).catch(() => {});
      if (host.info.nfs) api.get('/api/host/nfs/exports').then((r) => (hostNfs = r.exports.length)).catch(() => {});
    });
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  });

  let smartCounts = $derived.by(() => {
    const c = { ok: 0, warning: 0, failed: 0, other: 0 };
    for (const d of smart ?? []) {
      if (d.health in c) c[d.health]++;
      else c.other++;
    }
    return c;
  });

  let memUsed = $derived(data ? data.system.mem_total - data.system.mem_available : 0);
</script>

<PageHeader title="Dashboard" description="Overview of this storage container" />

{#if !data}
  <div class="card"><State loading={!error} {error} /></div>
{:else}
  <div class="card grid grid-cols-2 divide-zinc-200 xl:grid-cols-4 xl:divide-x dark:divide-ink-800">
    {#snippet stat(href, icon, value, label, note, noteTone)}
      <a {href} class="group flex items-start gap-3 px-4 py-3.5 hover:bg-zinc-50 dark:hover:bg-ink-850">
        <Icon name={icon} size={18} class="mt-0.5 text-zinc-400 group-hover:text-brand-500 dark:text-zinc-500" />
        <div>
          <p class="text-xl leading-tight font-semibold tabular-nums">{value}</p>
          <p class="text-xs text-zinc-500">
            {label}{#if note}<span class="ml-1 {noteTone}">· {note}</span>{/if}
          </p>
        </div>
      </a>
    {/snippet}
    {@render stat('#/smb/shares', 'folder', data.smb_shares + (hostSmb ?? 0), 'SMB shares', hostSmb !== null ? `${hostSmb} on the host` : '', 'text-zinc-500')}
    {@render stat('#/nfs', 'network', data.nfs_exports + (hostNfs ?? 0), 'NFS exports', hostNfs !== null ? `${hostNfs} on the host` : '', 'text-zinc-500')}
    {@render stat(
      '#/raid',
      'layers',
      data.raid.length,
      'RAID arrays',
      data.raid.some((a) => a.health !== 'ok') ? 'needs attention' : '',
      'text-amber-600 dark:text-amber-400',
    )}
    {@render stat(
      '#/smart',
      'disk',
      smart ? smart.length : '…',
      'Disks',
      smartCounts.failed ? `${smartCounts.failed} failed` : smartCounts.warning ? `${smartCounts.warning} warning` : '',
      smartCounts.failed ? 'text-red-600 dark:text-red-400' : 'text-amber-600 dark:text-amber-400',
    )}
  </div>

  <div class="mt-4 grid gap-4 lg:grid-cols-3">
    <section class="card lg:col-span-2">
      <div class="card-header"><h2 class="card-title">Storage</h2></div>
      {#if data.system.filesystems.length === 0}
        <State empty="No filesystems found" icon="disk" />
      {:else}
        <ul class="divide-y divide-zinc-100 dark:divide-ink-800">
          {#each data.system.filesystems as fs}
            {@const p = percent(fs.used, fs.size)}
            <li class="px-4 py-3">
              <div class="mb-1.5 flex flex-wrap items-baseline justify-between gap-x-4 text-sm">
                <span class="mono font-medium text-zinc-900 dark:text-zinc-100">{fs.mount}</span>
                <span class="text-zinc-500">{bytes(fs.used)} of {bytes(fs.size)} · <b class="font-medium text-zinc-700 dark:text-zinc-300">{p}%</b></span>
              </div>
              <Progress value={p} label="{fs.mount} usage" />
              <p class="mt-1 text-xs text-zinc-500"><span class="mono">{fs.source}</span> · {fs.type} · {bytes(fs.avail)} free</p>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section class="card">
      <div class="card-header"><h2 class="card-title">System</h2></div>
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 px-4 py-3 text-sm">
        <dt class="text-zinc-500">Host</dt>
        <dd class="truncate font-medium">{data.system.hostname}</dd>
        <dt class="text-zinc-500">OS</dt>
        <dd class="truncate">{data.system.os}</dd>
        <dt class="text-zinc-500">Kernel</dt>
        <dd class="mono truncate">{data.system.kernel || '–'}</dd>
        <dt class="text-zinc-500">Uptime</dt>
        <dd>{duration(data.system.uptime_seconds)}</dd>
        <dt class="text-zinc-500">Load</dt>
        <dd class="mono">{data.system.load.map((l) => l.toFixed(2)).join('  ')}</dd>
        <dt class="text-zinc-500">Memory</dt>
        <dd>
          <span class="text-xs text-zinc-500">{bytes(memUsed)} / {bytes(data.system.mem_total)}</span>
          <Progress value={percent(memUsed, data.system.mem_total)} label="Memory usage" />
        </dd>
        <dt class="text-zinc-500">LocoStor</dt>
        <dd class="mono">
          {data.version}
          {#if data.update?.update_available}<a href="#/update" class="ml-1 text-brand-600 hover:underline dark:text-brand-400">→ {data.update.latest}</a>{/if}
        </dd>
      </dl>
      <div class="border-t border-zinc-200 px-4 py-3 dark:border-ink-800">
        <p class="mb-2 text-xs font-semibold tracking-wide text-zinc-500 uppercase">Services</p>
        <ul class="space-y-1.5">
          {#each Object.entries(data.services) as [name, state]}
            <li class="flex items-center justify-between text-sm">
              <span class="mono">{name}</span>
              <Badge tone={tone(state)} dot>{state}</Badge>
            </li>
          {/each}
          {#if host.info?.available}
            {#each Object.entries(host.info.services ?? {}) as [name, state]}
              <li class="flex items-center justify-between text-sm">
                <span class="mono">{name} <span class="text-xs text-zinc-500">(host)</span></span>
                <Badge tone={tone(state)} dot>{state}</Badge>
              </li>
            {/each}
          {/if}
        </ul>
      </div>
    </section>
  </div>

  {#if data.raid.length}
    <section class="card mt-4">
      <div class="card-header">
        <h2 class="card-title">RAID arrays</h2>
        <a href="#/raid" class="text-xs font-medium text-brand-600 hover:underline dark:text-brand-400">Details</a>
      </div>
      <ul class="divide-y divide-zinc-100 dark:divide-ink-800">
        {#each data.raid as a}
          <li class="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-sm">
            <span class="mono w-14 font-medium">{a.name}</span>
            <Badge tone={tone(a.health)} dot>{a.health}</Badge>
            <span class="text-zinc-500">{a.level} · {capacity(a.size_bytes)} · [{a.status}]</span>
            {#if a.progress >= 0}
              <div class="flex min-w-48 flex-1 items-center gap-2">
                <Progress value={a.progress} tone="warn" label="{a.sync_action} progress" />
                <span class="text-xs whitespace-nowrap text-zinc-500">{a.sync_action} {a.progress}%</span>
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    </section>
  {/if}
{/if}
