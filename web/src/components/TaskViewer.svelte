<script>
  // Global window that follows a background task live (see lib/tasks).
  import Modal from './Modal.svelte';
  import Badge from './Badge.svelte';
  import Terminal from './Terminal.svelte';
  import { viewer, followTask } from '../lib/tasks.svelte.js';

  let lines = $state([]);
  let task = $state(null);
  let ctrl;

  $effect(() => {
    if (!viewer.open || !viewer.id) return;
    const { id, base, onDone } = viewer;
    lines = [];
    task = { status: 'running', title: viewer.title };
    ctrl?.abort();
    const c = new AbortController();
    ctrl = c;
    followTask(base, id, (l) => lines.push(...l), c.signal)
      .then((t) => {
        if (c.signal.aborted || !t) return;
        task = t;
        onDone?.(t);
      })
      .catch((e) => (task = { status: 'failed', error: e.message }));
    return () => c.abort();
  });

  const tones = { running: 'info', ok: 'ok', failed: 'bad', interrupted: 'warn', restarting: 'info' };
</script>

<Modal title={task?.title || viewer.title || 'Task'} bind:open={viewer.open} wide>
  <div class="mb-2 flex items-center gap-2 text-xs text-zinc-500">
    <Badge tone={tones[task?.status] ?? 'muted'} dot>{task?.status ?? '…'}</Badge>
    {#if viewer.base === '/api/host'}<span>on the Proxmox host</span>{/if}
    {#if task?.error}<span class="text-red-600 dark:text-red-400">{task.error}</span>{/if}
  </div>
  <Terminal {lines} running={task?.status === 'running'} height="h-[26rem]" />
  <p class="hint">The task keeps running when you close this window. All tasks: Update → Task log.</p>
</Modal>
