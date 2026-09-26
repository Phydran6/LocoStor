<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Progress from '../components/Progress.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { bytes, capacity, duration, percent, tone } from '../lib/format.js';

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

  onMount(() => {
    load();
    api.get('/api/smart').then((r) => (smart = r.disks)).catch(() => (smart = []));
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
  <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
    <a href="#/smb/shares" class="card flex items-center gap-4 p-4 transition-colors hover:border-sky-500/50">
      <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-sky-500/10 text-sky-600 dark:text-sky-400"><Icon name="folder" /></div>
      <div>
        <p class="text-2xl font-semibold">{data.smb_shares}</p>
        <p class="text-xs text-zinc-500">SMB shares</p>
      </div>
    </a>
    <a href="#/nfs" class="card flex items-center gap-4 p-4 transition-colors hover:border-sky-500/50">
      <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-violet-500/10 text-violet-600 dark:text-violet-400"><Icon name="network" /></div>
      <div>
        <p class="text-2xl font-semibold">{data.nfs_exports}</p>
        <p class="text-xs text-zinc-500">NFS exports</p>
      </div>
    </a>
    <a href="#/raid" class="card flex items-center gap-4 p-4 transition-colors hover:border-sky-500/50">
      <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"><Icon name="layers" /></div>
      <div>
        <p class="text-2xl font-semibold">{data.raid.length}</p>
        <p class="text-xs text-zinc-500">
          RAID arrays{#if data.raid.some((a) => a.health !== 'ok')}<span class="ml-1 text-amber-600 dark:text-amber-400">· needs attention</span>{/if}
        </p>
      </div>
    </a>
    <a href="#/smart" class="card flex items-center gap-4 p-4 transition-colors hover:border-sky-500/50">
      <div class="flex h-10 w-10 items-center justify-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400"><Icon name="disk" /></div>
      <div>
        <p class="text-2xl font-semibold">{smart ? smart.length : '…'}</p>
        <p class="text-xs text-zinc-500">
          Disks
          {#if smartCounts.failed}<span class="ml-1 text-red-600 dark:text-red-400">· {smartCounts.failed} failed</span>{/if}
          {#if smartCounts.warning}<span class="ml-1 text-amber-600 dark:text-amber-400">· {smartCounts.warning} warning</span>{/if}
        </p>
      </div>
    </a>
  </div>

  <div class="mt-4 grid gap-4 lg:grid-cols-3">
    <section class="card lg:col-span-2">
      <div class="card-header"><h2 class="card-title">Storage</h2></div>
      {#if data.system.filesystems.length === 0}
        <State empty="No filesystems found" icon="disk" />
      {:else}
        <ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
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
          {#if data.update?.update_available}<a href="#/update" class="ml-1 text-sky-600 hover:underline dark:text-sky-400">→ {data.update.latest}</a>{/if}
        </dd>
      </dl>
      <div class="border-t border-zinc-200 px-4 py-3 dark:border-zinc-800">
        <p class="mb-2 text-xs font-semibold tracking-wide text-zinc-500 uppercase">Services</p>
        <ul class="space-y-1.5">
          {#each Object.entries(data.services) as [name, state]}
            <li class="flex items-center justify-between text-sm">
              <span class="mono">{name}</span>
              <Badge tone={tone(state)} dot>{state}</Badge>
            </li>
          {/each}
        </ul>
      </div>
    </section>
  </div>

  {#if data.raid.length}
    <section class="card mt-4">
      <div class="card-header">
        <h2 class="card-title">RAID arrays</h2>
        <a href="#/raid" class="text-xs font-medium text-sky-600 hover:underline dark:text-sky-400">Details</a>
      </div>
      <ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
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
