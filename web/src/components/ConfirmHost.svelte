<script>
  import Modal from './Modal.svelte';
  import { dialog } from '../lib/ui.svelte.js';

  let open = $state(false);
  let result = false;

  $effect(() => {
    if (dialog.open) {
      result = false;
      open = true;
    }
  });

  // Resolve when the modal closes by any means (button, Escape, backdrop).
  $effect(() => {
    if (!open && dialog.open) {
      dialog.open = false;
      dialog.resolve?.(result);
    }
  });

  function answer(v) {
    result = v;
    open = false;
  }
</script>

<Modal title={dialog.title} bind:open>
  <p class="text-sm text-zinc-600 dark:text-zinc-300">{dialog.message}</p>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => answer(false)}>Cancel</button>
    <button class="btn {dialog.danger ? 'btn-danger' : 'btn-primary'}" onclick={() => answer(true)}>{dialog.confirmLabel}</button>
  {/snippet}
</Modal>
