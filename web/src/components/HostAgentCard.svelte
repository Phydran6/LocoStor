<script>
  // Version of the agent on the Proxmox host, with an update button when it
  // is older than this LocoStor.
  import Icon from './Icon.svelte';
  import Badge from './Badge.svelte';
  import { api, session } from '../lib/api.svelte.js';
  import { toast } from '../lib/ui.svelte.js';
  import { host, loadHost } from '../lib/host.svelte.js';

  let busy = $state(false);
  let log = $state([]);

  let outdated = $derived(host.info?.available && host.info.version !== session.version && session.version !== 'dev');

  async function updateAgent() {
    busy = true;
    log = [];
    const before = host.info.version;
    try {
      await api.post('/api/host/update');
      const deadline = Date.now() + 120_000;
      while (Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 1500));
        try {
          const p = await api.get('/api/host/update/progress');
          log = p.log;
          if (p.phase === 'failed') throw new Error(p.error);
        } catch (e) {
          if (e.status !== 503 && e.status !== 502 && e.status !== 0) throw e;
        }
        await loadHost(true);
        if (host.info?.available && host.info.version !== before) {
          toast.success(`Host agent updated to ${host.info.version}`);
          return;
        }
      }
      throw new Error('The host agent did not come back within 2 minutes.');
    } catch (e) {
      toast.error(e.message);
    } finally {
      busy = false;
    }
  }
</script>

<section class="card mt-4">
  <div class="card-header">
    <h2 class="card-title">Proxmox host agent</h2>
    {#if host.info?.available}
      <Badge tone={outdated ? 'warn' : 'ok'} dot>{outdated ? 'older than LocoStor' : 'up to date'}</Badge>
    {:else}
      <Badge>not connected</Badge>
    {/if}
  </div>
  <div class="space-y-3 px-4 py-4 text-sm">
    {#if host.info?.available}
      <p class="text-zinc-600 dark:text-zinc-300">
        Runs on <span class="mono">{host.info.hostname}</span> and lets LocoStor show and edit the host's own shares. Version
        <span class="mono">{host.info.version}</span>.
      </p>
      {#if outdated}
        <button class="btn btn-primary" onclick={updateAgent} disabled={busy}>
          <Icon name="download" size={16} />{busy ? 'Updating…' : 'Update host agent'}
        </button>
      {/if}
      {#if log.length}
        <div class="max-h-40 overflow-auto rounded-md bg-zinc-50 p-3 font-mono text-xs dark:bg-ink-950">
          {#each log as line}<p class="text-zinc-600 dark:text-zinc-400">{line.msg}</p>{/each}
        </div>
      {/if}
    {:else}
      <p class="text-zinc-600 dark:text-zinc-300">
        To see and edit the shares of the Proxmox host itself, run the installer once on the <b>host</b> shell:
      </p>
      <code class="mono block overflow-x-auto rounded-md bg-zinc-100 px-3 py-2 text-xs whitespace-nowrap dark:bg-ink-950"
        >curl -fsSL https://raw.githubusercontent.com/Phydran6/LocoStor/main/scripts/install.sh | sh</code
      >
    {/if}
  </div>
</section>
