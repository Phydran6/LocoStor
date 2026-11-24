<script>
  // Read-only terminal: shows command output like a shell, auto-scrolls
  // while the user is at the bottom.
  let { lines = [], height = 'h-80', running = false } = $props();
  let el = $state();
  let stick = $state(true);

  function onscroll() {
    stick = el.scrollTop + el.clientHeight >= el.scrollHeight - 24;
  }

  $effect(() => {
    lines.length;
    if (el && stick) queueMicrotask(() => (el.scrollTop = el.scrollHeight));
  });

  function cls(l) {
    if (l.startsWith('$ ')) return 'text-sky-300';
    if (l.startsWith('▶ ')) return 'font-semibold text-brand-300';
    if (l.startsWith('✔')) return 'text-emerald-400';
    if (l.startsWith('✖') || l.startsWith('!!') || /^(E:|error|Err:|fatal)/i.test(l)) return 'text-red-400';
    if (/^(W:|warning)/i.test(l)) return 'text-amber-300';
    return 'text-zinc-300';
  }
</script>

<div
  bind:this={el}
  {onscroll}
  class="{height} overflow-auto rounded-md border border-ink-800 bg-[#05060a] p-3 font-mono text-[12px] leading-[1.45] shadow-inner"
  role="log"
  aria-live="polite"
>
  {#each lines as l}
    <div class="break-all whitespace-pre-wrap {cls(l)}">{l || ' '}</div>
  {/each}
  {#if running}<span class="inline-block h-3.5 w-2 animate-pulse bg-zinc-400 align-middle"></span>{/if}
</div>
