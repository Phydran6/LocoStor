<script>
  // Switches a page between the Proxmox host's own shares and the shares
  // of the LocoStor container. Hidden when no host is connected.
  import Icon from './Icon.svelte';
  import { host, scope, setScope } from '../lib/host.svelte.js';

  let { hostLabel = 'Proxmox host' } = $props();

  const tabs = $derived([
    { value: 'host', label: hostLabel, sub: host.info?.hostname, icon: 'server' },
    { value: 'container', label: 'This container', sub: 'LocoStor', icon: 'layers' },
  ]);
</script>

{#if host.info?.available}
  <div class="mb-4 flex gap-1 border-b border-zinc-200 dark:border-ink-800" role="tablist">
    {#each tabs as t}
      <button
        role="tab"
        aria-selected={scope.value === t.value}
        class="-mb-px flex items-center gap-2 border-b-2 px-3 py-2 text-sm transition-colors
          {scope.value === t.value
          ? 'border-brand-500 font-medium text-zinc-900 dark:text-zinc-100'
          : 'border-transparent text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200'}"
        onclick={() => setScope(t.value)}
      >
        <Icon name={t.icon} size={16} />
        {t.label}
        {#if t.sub}<span class="mono text-xs text-zinc-400">{t.sub}</span>{/if}
      </button>
    {/each}
  </div>
{/if}
