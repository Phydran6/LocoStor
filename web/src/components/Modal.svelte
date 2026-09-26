<script>
  import Icon from './Icon.svelte';

  let { title, open = $bindable(false), wide = false, children, footer } = $props();

  function close() {
    open = false;
  }
  function onkeydown(e) {
    if (open && e.key === 'Escape') close();
  }
</script>

<svelte:window {onkeydown} />

{#if open}
  <div class="fixed inset-0 z-50 overflow-y-auto">
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="fixed inset-0 bg-black/60" onclick={close}></div>
    <div class="relative flex min-h-full items-start justify-center p-4 sm:items-center sm:p-6">
      <div
        class="card relative w-full shadow-xl {wide ? 'max-w-2xl' : 'max-w-lg'}"
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <div class="card-header">
          <h2 class="text-base font-semibold">{title}</h2>
          <button class="btn-icon" onclick={close} aria-label="Close"><Icon name="x" /></button>
        </div>
        <div class="px-5 py-4">
          {@render children()}
        </div>
        {#if footer}
          <div class="flex justify-end gap-2 border-t border-zinc-200 px-5 py-3 dark:border-ink-800">
            {@render footer()}
          </div>
        {/if}
      </div>
    </div>
  </div>
{/if}
