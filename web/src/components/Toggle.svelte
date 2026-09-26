<script>
  let { checked = $bindable(false), label, hint = '' } = $props();
  const id = 'tg-' + Math.random().toString(36).slice(2, 9);
</script>

<div class="flex items-start justify-between gap-4">
  <div>
    <label for={id} class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{label}</label>
    {#if hint}<p class="hint">{hint}</p>{/if}
  </div>
  <button
    {id}
    type="button"
    role="switch"
    aria-checked={checked}
    aria-label={label}
    onclick={() => (checked = !checked)}
    class="relative mt-0.5 inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sky-500
      {checked ? 'bg-sky-600' : 'bg-zinc-300 dark:bg-zinc-700'}"
  >
    <span class="pointer-events-none mt-0.5 inline-block h-4 w-4 rounded-full bg-white shadow transition-transform {checked ? 'translate-x-4.5' : 'translate-x-0.5'}"></span>
  </button>
</div>
