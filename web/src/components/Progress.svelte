<script>
  let { value = 0, tone = 'auto', label = '' } = $props();

  const colors = { ok: 'bg-emerald-500', warn: 'bg-amber-500', bad: 'bg-red-500', info: 'bg-sky-500' };
  let resolved = $derived(tone !== 'auto' ? tone : value >= 90 ? 'bad' : value >= 75 ? 'warn' : 'info');
</script>

<div
  class="h-2 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800"
  role="progressbar"
  aria-valuenow={value}
  aria-valuemin="0"
  aria-valuemax="100"
  aria-label={label}
>
  <div class="h-full rounded-full transition-all duration-500 {colors[resolved]}" style="width: {Math.min(100, Math.max(0, value))}%"></div>
</div>
