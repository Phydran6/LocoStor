<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Progress from '../components/Progress.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { capacity, tone } from '../lib/format.js';

  let arrays = $state(null);
  let error = $state('');

  async function load() {
    try {
      arrays = await api.get('/api/raid');
      error = '';
    } catch (e) {
      error = e.message;
    }
  }

  onMount(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });
</script>

<PageHeader title="RAID" description="Software RAID (mdadm) arrays of the host – read only">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={load}><Icon name="refresh" size={16} />Refresh</button>
  {/snippet}
</PageHeader>

{#if arrays === null || arrays.length === 0}
  <div class="card">
    <State loading={arrays === null && !error} {error} empty="No md arrays found in /proc/mdstat" icon="layers" />
  </div>
{:else}
  <div class="grid gap-4 xl:grid-cols-2">
    {#each arrays as a}
      <section class="card">
        <div class="card-header">
          <div class="flex items-center gap-3">
            <h2 class="mono text-base font-semibold">/dev/{a.name}</h2>
            <Badge tone={tone(a.health)} dot>{a.health}</Badge>
          </div>
          <span class="text-sm text-zinc-500">{a.level || '–'} · {capacity(a.size_bytes)}</span>
        </div>

        <div class="space-y-4 px-4 py-4">
          <div class="flex flex-wrap items-center gap-2" aria-label="Disk slots">
            {#each (a.status || '').split('') as slot, i}
              <div
                class="flex h-9 w-9 items-center justify-center rounded-md text-xs font-semibold
                  {slot === 'U'
                  ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400'
                  : 'bg-red-500/15 text-red-700 dark:text-red-400'}"
                title="Slot {i}: {slot === 'U' ? 'up' : 'missing'}"
              >
                {slot === 'U' ? i : '✕'}
              </div>
            {/each}
            <span class="ml-2 text-sm text-zinc-500">{a.active_disks}/{a.raid_disks} disks active</span>
          </div>

          {#if a.progress >= 0}
            <div>
              <div class="mb-1 flex justify-between text-sm">
                <span class="font-medium capitalize">{a.sync_action}</span>
                <span class="text-zinc-500">{a.progress}%{a.finish ? ` · ${a.finish} left` : ''}{a.speed ? ` · ${a.speed}` : ''}</span>
              </div>
              <Progress value={a.progress} tone="warn" label="{a.sync_action} progress" />
            </div>
          {:else if a.sync_action}
            <p class="text-sm text-zinc-500">Sync: {a.sync_action}</p>
          {/if}

          <dl class="grid grid-cols-2 gap-x-4 gap-y-1 text-sm sm:grid-cols-4">
            <div><dt class="text-xs text-zinc-500">State</dt><dd>{a.state}{a.read_only ? ' (ro)' : ''}</dd></div>
            <div><dt class="text-xs text-zinc-500">Array state</dt><dd>{a.array_state || '–'}</dd></div>
            <div><dt class="text-xs text-zinc-500">Mismatches</dt><dd>{a.mismatch_cnt}</dd></div>
            <div><dt class="text-xs text-zinc-500">Members</dt><dd>{a.members.length}</dd></div>
          </dl>
        </div>

        <table class="table border-t border-zinc-200 dark:border-zinc-800">
          <thead><tr><th>Slot</th><th>Device</th><th>State</th></tr></thead>
          <tbody>
            {#each a.members as m}
              <tr>
                <td class="w-16">{m.index}</td>
                <td class="mono">/dev/{m.name}</td>
                <td><Badge tone={m.state === 'active' ? 'ok' : tone(m.state)}>{m.state}</Badge></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </section>
    {/each}
  </div>
{/if}
