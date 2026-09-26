<script>
  let { tone = 'muted', dot = false, children } = $props();

  const tones = {
    ok: 'bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-400 dark:ring-emerald-500/25',
    warn: 'bg-amber-50 text-amber-800 ring-amber-600/25 dark:bg-amber-500/10 dark:text-amber-400 dark:ring-amber-500/25',
    bad: 'bg-red-50 text-red-700 ring-red-600/20 dark:bg-red-500/10 dark:text-red-400 dark:ring-red-500/25',
    info: 'bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-400 dark:ring-sky-500/25',
    muted: 'bg-zinc-100 text-zinc-600 ring-zinc-500/20 dark:bg-ink-800 dark:text-zinc-400 dark:ring-ink-700',
  };
  const dots = { ok: 'bg-emerald-500', warn: 'bg-amber-500', bad: 'bg-red-500', info: 'bg-sky-500', muted: 'bg-zinc-400' };
</script>

<span class="inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium whitespace-nowrap ring-1 ring-inset {tones[tone] ?? tones.muted}">
  {#if dot}<span class="h-1.5 w-1.5 rounded-full {dots[tone] ?? dots.muted}"></span>{/if}
  {@render children()}
</span>
