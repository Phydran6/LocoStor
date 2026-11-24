<script>
  // A small segmented control: options = [{ value, label }].
  let { value = $bindable(), options, label = '', size = 'md' } = $props();
</script>

<div class="inline-flex flex-wrap rounded-lg border border-zinc-300 bg-zinc-50 p-0.5 dark:border-ink-700 dark:bg-ink-950" role="radiogroup" aria-label={label}>
  {#each options as o}
    <button
      type="button"
      role="radio"
      aria-checked={value === o.value}
      class="rounded-md font-medium transition-colors {size === 'sm' ? 'px-2 py-0.5 text-xs' : 'px-3 py-1 text-sm'}
        {value === o.value
        ? o.tone === 'bad'
          ? 'bg-red-600 text-white'
          : 'bg-white text-zinc-900 shadow-sm ring-1 ring-zinc-200 dark:bg-ink-800 dark:text-zinc-100 dark:ring-ink-700'
        : 'text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200'}"
      onclick={() => (value = o.value)}>{o.label}</button
    >
  {/each}
</div>
