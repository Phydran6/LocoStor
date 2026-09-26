<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { capacity, dateTime, hours, tone } from '../lib/format.js';

  let disks = $state(null);
  let updatedAt = $state('');
  let error = $state('');
  let busy = $state(false);
  let expanded = $state({});

  async function load(refresh = false) {
    busy = true;
    try {
      const r = await api.get('/api/smart' + (refresh ? '?refresh=1' : ''));
      disks = r.disks;
      updatedAt = r.updated_at;
      error = '';
    } catch (e) {
      error = e.message;
    } finally {
      busy = false;
    }
  }

  onMount(() => load());

  function tempClass(t) {
    if (t == null) return '';
    if (t >= 55) return 'text-red-600 dark:text-red-400 font-medium';
    if (t >= 45) return 'text-amber-600 dark:text-amber-400 font-medium';
    return '';
  }

  // Attributes that matter most for failure prediction.
  const critical = new Set([5, 10, 184, 187, 188, 196, 197, 198, 199]);
</script>

<PageHeader title="SMART" description="Disk health reported by smartctl. Sleeping disks are not woken up.">
  {#snippet actions()}
    {#if updatedAt}<span class="self-center text-xs text-zinc-500">Updated {dateTime(updatedAt)}</span>{/if}
    <button class="btn btn-secondary" onclick={() => load(true)} disabled={busy}>
      <Icon name="refresh" size={16} class={busy ? 'animate-spin' : ''} />Refresh
    </button>
  {/snippet}
</PageHeader>

<div class="card overflow-hidden">
  {#if disks === null || disks.length === 0}
    <State loading={disks === null && !error} {error} empty="No disks found. Pass the disks into the container (see README)." icon="disk" />
  {:else}
    <div class="overflow-x-auto">
      <table class="table">
        <thead>
          <tr>
            <th class="w-8"></th>
            <th>Device</th>
            <th>Model</th>
            <th>Capacity</th>
            <th>Temp</th>
            <th>Power on</th>
            <th>Health</th>
          </tr>
        </thead>
        <tbody>
          {#each disks as d}
            <tr class="cursor-pointer" onclick={() => (expanded[d.device] = !expanded[d.device])}>
              <td>
                <Icon name={expanded[d.device] ? 'chevronDown' : 'chevronRight'} size={16} class="text-zinc-400" />
              </td>
              <td class="mono whitespace-nowrap">
                {d.device}
                {#if d.type}<span class="ml-1 text-xs text-zinc-500">-d {d.type}</span>{/if}
              </td>
              <td class="max-w-72 truncate" title={d.model}>{d.model || '–'}</td>
              <td class="whitespace-nowrap">{capacity(d.capacity_bytes)}</td>
              <td class="whitespace-nowrap {tempClass(d.temperature)}">{d.temperature != null ? `${d.temperature} °C` : '–'}</td>
              <td class="whitespace-nowrap">{hours(d.power_on_hours)}</td>
              <td><Badge tone={tone(d.health)} dot>{d.health}</Badge></td>
            </tr>
            {#if expanded[d.device]}
              <tr class="hover:bg-transparent">
                <td colspan="7" class="bg-zinc-50/70 px-4 py-4 dark:bg-zinc-950/40">
                  <dl class="mb-4 grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-4">
                    <div><dt class="text-xs text-zinc-500">Serial</dt><dd class="mono">{d.serial || '–'}</dd></div>
                    <div><dt class="text-xs text-zinc-500">Firmware</dt><dd class="mono">{d.firmware || '–'}</dd></div>
                    <div><dt class="text-xs text-zinc-500">Protocol</dt><dd>{d.protocol || '–'}</dd></div>
                    <div><dt class="text-xs text-zinc-500">Type</dt><dd>{d.rotation_rate ? `HDD ${d.rotation_rate} rpm` : d.protocol ? 'SSD' : '–'}</dd></div>
                    <div><dt class="text-xs text-zinc-500">Self-assessment</dt><dd>{d.passed == null ? '–' : d.passed ? 'PASSED' : 'FAILED'}</dd></div>
                    <div><dt class="text-xs text-zinc-500">Power cycles</dt><dd>{d.power_cycles ?? '–'}</dd></div>
                    {#if d.reallocated != null}<div><dt class="text-xs text-zinc-500">Reallocated</dt><dd>{d.reallocated}</dd></div>{/if}
                    {#if d.pending != null}<div><dt class="text-xs text-zinc-500">Pending sectors</dt><dd>{d.pending}</dd></div>{/if}
                    {#if d.uncorrectable != null}<div><dt class="text-xs text-zinc-500">Uncorrectable</dt><dd>{d.uncorrectable}</dd></div>{/if}
                    {#if d.percent_used != null}<div><dt class="text-xs text-zinc-500">Wear</dt><dd>{d.percent_used}%</dd></div>{/if}
                    {#if d.media_errors != null}<div><dt class="text-xs text-zinc-500">Media errors</dt><dd>{d.media_errors}</dd></div>{/if}
                  </dl>

                  {#if d.messages.length}
                    <ul class="mb-4 space-y-1 text-xs text-zinc-500">
                      {#each d.messages as m}<li class="flex gap-1.5"><Icon name="info" size={14} />{m}</li>{/each}
                    </ul>
                  {/if}

                  {#if d.attributes.length}
                    <div class="overflow-x-auto rounded-lg border border-zinc-200 dark:border-zinc-800">
                      <table class="table">
                        <thead>
                          <tr><th>ID</th><th>Attribute</th><th>Value</th><th>Worst</th><th>Thresh</th><th>Raw</th></tr>
                        </thead>
                        <tbody>
                          {#each d.attributes as a}
                            {@const bad = a.when_failed || (critical.has(a.id) && a.raw !== '0')}
                            <tr class={bad ? 'text-amber-700 dark:text-amber-400' : ''}>
                              <td class="mono">{a.id}</td>
                              <td class="mono">{a.name}</td>
                              <td>{a.value}</td>
                              <td>{a.worst}</td>
                              <td>{a.thresh}</td>
                              <td class="mono {bad ? 'font-semibold text-amber-700 dark:text-amber-400' : ''}">{a.raw}</td>
                            </tr>
                          {/each}
                        </tbody>
                      </table>
                    </div>
                  {/if}
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
