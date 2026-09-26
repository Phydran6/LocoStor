<script>
  let { title, description = '', actions } = $props();
</script>

<div class="mb-5 flex flex-wrap items-end justify-between gap-3">
  <div>
    <h1 class="text-xl font-semibold tracking-tight">{title}</h1>
    {#if description}<p class="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{description}</p>{/if}
  </div>
  {#if actions}<div class="flex flex-wrap gap-2">{@render actions()}</div>{/if}
</div>
