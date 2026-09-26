<script>
  // Loading / error / empty placeholder for cards and tables.
  import Icon from './Icon.svelte';

  let { loading = false, error = '', empty = '', icon = 'info', children } = $props();
</script>

{#if loading}
  <div class="flex items-center justify-center gap-2 px-4 py-12 text-sm text-zinc-500">
    <span class="h-4 w-4 animate-spin rounded-full border-2 border-zinc-300 border-t-brand-500 dark:border-zinc-700"></span>
    Loading…
  </div>
{:else if error}
  <div class="flex items-center justify-center gap-2 px-4 py-12 text-sm text-red-600 dark:text-red-400">
    <Icon name="alert" />
    {error}
  </div>
{:else if empty}
  <div class="flex flex-col items-center justify-center gap-2 px-4 py-12 text-center text-sm text-zinc-500">
    <Icon name={icon} size={28} class="text-zinc-400 dark:text-zinc-600" />
    {empty}
    {#if children}{@render children()}{/if}
  </div>
{/if}
