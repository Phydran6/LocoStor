<script>
  import Icon from './Icon.svelte';
  import { toasts, dismiss } from '../lib/ui.svelte.js';
</script>

<div class="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-2" aria-live="polite">
  {#each toasts as t (t.id)}
    <div
      class="pointer-events-auto flex items-start gap-3 rounded-lg border px-4 py-3 text-sm shadow-lg
        {t.kind === 'error'
        ? 'border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200'
        : 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-200'}"
    >
      <Icon name={t.kind === 'error' ? 'alert' : 'check'} class="mt-0.5" />
      <p class="flex-1 break-words">{t.message}</p>
      <button class="opacity-60 hover:opacity-100" onclick={() => dismiss(t.id)} aria-label="Dismiss"><Icon name="x" size={16} /></button>
    </div>
  {/each}
</div>
