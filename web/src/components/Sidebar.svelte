<script>
  import Icon from './Icon.svelte';
  import { route } from '../lib/router.svelte.js';
  import { session } from '../lib/api.svelte.js';
  import { updates } from '../lib/ui.svelte.js';

  let { open = $bindable(false), nav } = $props();

  $effect(() => {
    route.path; // close the mobile drawer on navigation
    open = false;
  });
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="fixed inset-0 z-30 bg-zinc-950/60 lg:hidden" onclick={() => (open = false)}></div>
{/if}

<aside
  class="fixed inset-y-0 left-0 z-40 flex w-64 flex-col border-r border-zinc-800 bg-zinc-900 text-zinc-300 transition-transform
    lg:translate-x-0 {open ? 'translate-x-0' : '-translate-x-full'}"
>
  <div class="flex h-14 items-center gap-2.5 border-b border-zinc-800 px-5">
    <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-sky-600 text-white">
      <Icon name="database" size={18} />
    </div>
    <span class="text-base font-semibold tracking-tight text-white">LocoStor</span>
    <button class="btn-icon ml-auto text-zinc-400 hover:bg-zinc-800 lg:hidden" onclick={() => (open = false)} aria-label="Close menu">
      <Icon name="x" />
    </button>
  </div>

  <nav class="flex-1 overflow-y-auto px-3 py-4">
    {#each nav as group}
      <p class="px-3 pt-4 pb-1.5 text-[11px] font-semibold tracking-wider text-zinc-500 uppercase first:pt-0">{group.label}</p>
      <ul class="space-y-0.5">
        {#each group.items as item}
          {@const active = route.path === item.path}
          <li>
            <a
              href="#{item.path}"
              class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors
                {active ? 'bg-sky-600/15 text-sky-300' : 'text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100'}"
              aria-current={active ? 'page' : undefined}
            >
              <Icon name={item.icon} size={17} class={active ? 'text-sky-400' : ''} />
              {item.title}
              {#if item.path === '/update' && updates.status?.update_available}
                <span class="ml-auto h-2 w-2 rounded-full bg-sky-400" title="Update available"></span>
              {/if}
            </a>
          </li>
        {/each}
      </ul>
    {/each}
  </nav>

  <div class="border-t border-zinc-800 px-5 py-3 text-xs text-zinc-500">
    Version <span class="mono text-zinc-400">{session.version}</span>
    {#if session.demo}<span class="ml-1 rounded bg-amber-500/15 px-1.5 py-0.5 text-amber-400">demo</span>{/if}
  </div>
</aside>
