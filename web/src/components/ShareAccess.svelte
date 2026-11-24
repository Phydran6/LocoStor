<script>
  // Who may read, write or not open a share.
  import Icon from './Icon.svelte';
  import Segmented from './Segmented.svelte';

  let { everyone = $bindable(), access = $bindable(), guest = $bindable(), users = [], groups = [] } = $props();

  const allOpts = [
    { value: 'write', label: 'Read & write' },
    { value: 'read', label: 'Read only' },
    { value: 'none', label: 'No access' },
  ];
  const entryOpts = [
    { value: 'write', label: 'Read & write' },
    { value: 'read', label: 'Read only' },
    { value: 'deny', label: 'Denied', tone: 'bad' },
  ];
  const guestOpts = [
    { value: 'none', label: 'No access' },
    { value: 'read', label: 'Read only' },
    { value: 'write', label: 'Read & write' },
  ];

  let newName = $state('');
  let known = $derived(new Set([...users.map((u) => u.name), ...groups.map((g) => '@' + g.name)].map((n) => n.toLowerCase())));
  let suggestions = $derived(
    [...users.map((u) => u.name), ...groups.map((g) => '@' + g.name)].filter((n) => !access.some((a) => a.name.toLowerCase() === n.toLowerCase())),
  );

  function add(name) {
    name = (name ?? newName).trim();
    if (!name || access.some((a) => a.name.toLowerCase() === name.toLowerCase())) return;
    access = [...access, { name, level: everyone === 'write' ? 'read' : 'write' }];
    newName = '';
  }
  function remove(i) {
    access = access.filter((_, j) => j !== i);
  }
  function onkey(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      add();
    }
  }
</script>

<div class="space-y-4 rounded-lg border border-zinc-200 p-3 dark:border-ink-800">
  <div class="flex flex-wrap items-center justify-between gap-2">
    <div>
      <p class="text-sm font-medium text-zinc-700 dark:text-zinc-300">All SMB users</p>
      <p class="hint">Everyone with a login on this server, unless listed below.</p>
    </div>
    <Segmented bind:value={everyone} options={allOpts} label="Access for all users" />
  </div>

  <div>
    <p class="text-sm font-medium text-zinc-700 dark:text-zinc-300">Users and groups</p>
    {#if access.length}
      <ul class="mt-2 divide-y divide-zinc-100 rounded-md border border-zinc-200 dark:divide-ink-800 dark:border-ink-800">
        {#each access as a, i}
          <li class="flex flex-wrap items-center gap-2 px-2.5 py-1.5">
            <Icon name={a.name.startsWith('@') ? 'users' : 'user'} size={15} class="text-zinc-400" />
            <span class="mono min-w-0 flex-1 truncate">{a.name}</span>
            {#if !known.has(a.name.toLowerCase()) && (users.length || groups.length)}
              <span class="text-xs text-amber-600 dark:text-amber-400" title="Not a Samba user or group on this server">unknown here</span>
            {/if}
            <Segmented bind:value={a.level} options={entryOpts} size="sm" label="Access of {a.name}" />
            <button type="button" class="btn-icon h-7 w-7" onclick={() => remove(i)} aria-label="Remove {a.name}"><Icon name="x" size={15} /></button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="hint">
        {everyone === 'none' ? 'Add the users or groups that may open this share.' : 'Add users or groups that need other rights than everybody else.'}
      </p>
    {/if}
    <div class="mt-2 flex gap-2">
      <input class="input" list="share-principals" bind:value={newName} onkeydown={onkey} placeholder="user or @group" autocomplete="off" />
      <button type="button" class="btn btn-secondary" onclick={() => add()}><Icon name="plus" size={16} />Add</button>
      <datalist id="share-principals">
        {#each suggestions as s}<option value={s}></option>{/each}
      </datalist>
    </div>
    {#if suggestions.length}
      <div class="mt-1.5 flex flex-wrap gap-1">
        {#each suggestions.slice(0, 16) as s}
          <button
            type="button"
            class="rounded-md bg-zinc-100 px-2 py-0.5 text-xs text-zinc-600 hover:bg-brand-100 hover:text-brand-700 dark:bg-ink-800 dark:text-zinc-400 dark:hover:bg-brand-900/40 dark:hover:text-brand-300"
            onclick={() => add(s)}>+ {s}</button
          >
        {/each}
      </div>
    {/if}
  </div>

  <div class="flex flex-wrap items-center justify-between gap-2 border-t border-zinc-200 pt-3 dark:border-ink-800">
    <div>
      <p class="text-sm font-medium text-zinc-700 dark:text-zinc-300">Guests (no password)</p>
      <p class="hint">Anyone in the network, without login.</p>
    </div>
    <Segmented bind:value={guest} options={guestOpts} label="Guest access" />
  </div>
</div>
