<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Modal from '../components/Modal.svelte';
  import Toggle from '../components/Toggle.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import ScopeTabs from '../components/ScopeTabs.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm } from '../lib/ui.svelte.js';
  import { tone } from '../lib/format.js';
  import { host, scope, loadHost } from '../lib/host.svelte.js';

  // Container scope
  let shares = $state(null);
  let external = $state([]);
  // Host scope
  let hostScan = $state(null);
  // Both
  let scan = $state(null); // sources + warnings of the active scope
  let users = $state([]);
  let error = $state('');

  let onHost = $derived(!!host.info?.available && scope.value === 'host');
  let base = $derived(onHost ? '/api/host/smb' : '/api/smb');

  let editOpen = $state(false);
  let editing = $state(null); // original name, null for new
  let form = $state({});
  let usersText = $state('');
  let optionsText = $state('');
  let saving = $state(false);
  let formError = $state('');

  async function load() {
    error = '';
    try {
      if (onHost) {
        hostScan = await api.get('/api/host/smb/shares');
        scan = hostScan;
      } else {
        [shares, scan] = await Promise.all([api.get('/api/smb/shares'), api.get('/api/smb/external')]);
        external = scan.shares;
      }
    } catch (e) {
      error = e.message;
    }
    api.get(`${base}/users`).then((u) => (users = u)).catch(() => (users = []));
  }

  onMount(() => loadHost());

  // Load once the host state is known, and again when the tab changes.
  let lastScope = '';
  $effect(() => {
    const key = `${onHost}`;
    if (host.info && key !== lastScope) {
      lastScope = key;
      shares = hostScan = scan = null;
      load();
    }
  });

  async function adopt(x) {
    const yes = await confirm({
      title: 'Take over share',
      message: `Manage "${x.name}" with LocoStor? It is removed from ${x.source} (a backup is kept) and keeps working unchanged.`,
      confirmLabel: 'Take over',
    });
    if (!yes) return;
    try {
      await api.post('/api/smb/external/adopt', { name: x.name });
      toast.success(`"${x.name}" is now managed by LocoStor`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }

  function parseOptions(text) {
    return text
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l && !l.startsWith('#') && !l.startsWith(';'))
      .map((l) => {
        const i = l.indexOf('=');
        return i < 0 ? { key: l, value: '' } : { key: l.slice(0, i).trim(), value: l.slice(i + 1).trim() };
      });
  }

  function openNew() {
    editing = null;
    form = { name: '', path: '', comment: '', read_only: false, browseable: true, guest_ok: false, enabled: true };
    usersText = optionsText = formError = '';
    editOpen = true;
  }

  function openEdit(s) {
    editing = s.name;
    const { file, editable, reason, ...share } = s;
    form = { ...share };
    usersText = s.valid_users.join(', ');
    optionsText = s.options.map((o) => `${o.key} = ${o.value}`).join('\n');
    formError = '';
    editOpen = true;
  }

  function addUser(name) {
    const list = usersText.split(/[\s,]+/).filter(Boolean);
    if (!list.includes(name)) list.push(name);
    usersText = list.join(', ');
  }

  async function save(e) {
    e.preventDefault();
    saving = true;
    formError = '';
    const body = { ...form, valid_users: usersText.split(/[\s,]+/).filter(Boolean), options: parseOptions(optionsText) };
    try {
      if (editing === null) await api.post(`${base}/shares`, body);
      else await api.put(`${base}/shares/${encodeURIComponent(editing)}`, body);
      toast.success(`Share "${body.name}" saved${onHost ? ' on the host' : ''}`);
      editOpen = false;
      load();
    } catch (err) {
      formError = err.message;
    } finally {
      saving = false;
    }
  }

  async function remove(s) {
    const yes = await confirm({
      title: 'Delete share',
      message: onHost
        ? `Remove the share "${s.name}" from ${s.file} on the host? A backup of the file is kept; the files in ${s.path} are not touched.`
        : `Delete the share "${s.name}"? The files in ${s.path} are not touched.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!yes) return;
    try {
      await api.del(`${base}/shares/${encodeURIComponent(s.name)}`);
      toast.success(`Share "${s.name}" deleted`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }
</script>

{#snippet accessBadges(s)}
  <div class="flex flex-wrap gap-1">
    <Badge tone={s.read_only ? 'muted' : 'info'}>{s.read_only ? 'read only' : 'read/write'}</Badge>
    {#if s.guest_ok}<Badge tone="warn">guest</Badge>{/if}
    {#if !s.browseable}<Badge>hidden</Badge>{/if}
    {#if !s.enabled}<Badge>disabled</Badge>{/if}
    {#if s.options.length}<Badge>+{s.options.length} {s.options.length === 1 ? 'option' : 'options'}</Badge>{/if}
  </div>
{/snippet}

<PageHeader title="SMB shares" description="Windows / macOS network shares served by Samba">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={load}><Icon name="refresh" size={16} />Refresh</button>
    {#if !onHost || host.info?.samba}
      <button class="btn btn-primary" onclick={openNew}><Icon name="plus" size={16} />Add share{onHost ? ' on host' : ''}</button>
    {/if}
  {/snippet}
</PageHeader>

<ScopeTabs />

{#if onHost}
  <section class="card overflow-hidden">
    <div class="card-header">
      <div>
        <h2 class="card-title">Samba on {host.info.hostname}</h2>
        <p class="mt-0.5 text-xs text-zinc-500">
          Edited directly in the host's config – manual changes on the host keep working. A backup is kept before every change.
        </p>
      </div>
      {#if host.info.services?.smbd}<Badge tone={tone(host.info.services.smbd)} dot>smbd {host.info.services.smbd}</Badge>{/if}
    </div>
    {#if !host.info.samba}
      <State empty="Samba is not installed on the Proxmox host." icon="folder" />
    {:else if hostScan === null || hostScan.shares.length === 0}
      <State loading={hostScan === null && !error} {error} empty="No shares on the host yet" icon="folder" />
    {:else}
      <div class="overflow-x-auto">
        <table class="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Path</th>
              <th>Access</th>
              <th>Users</th>
              <th>Defined in</th>
              <th class="w-24 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each hostScan.shares as s}
              <tr>
                <td>
                  <p class="font-medium text-zinc-900 dark:text-zinc-100">{s.name}</p>
                  {#if s.comment}<p class="text-xs text-zinc-500">{s.comment}</p>{/if}
                </td>
                <td class="mono">{s.path || '–'}</td>
                <td>{@render accessBadges(s)}</td>
                <td class="text-xs">{s.valid_users.length ? s.valid_users.join(', ') : 'all users'}</td>
                <td class="mono text-xs text-zinc-500">{s.file || 'registry'}</td>
                <td class="text-right whitespace-nowrap">
                  {#if s.editable}
                    <button class="btn-icon" onclick={() => openEdit(s)} title="Edit" aria-label="Edit {s.name}"><Icon name="pencil" size={16} /></button>
                    <button class="btn-icon hover:!text-red-600" onclick={() => remove(s)} title="Delete" aria-label="Delete {s.name}"><Icon name="trash" size={16} /></button>
                  {:else}
                    <span class="text-xs text-zinc-500" title={s.reason}>{s.reason}</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{:else}
  <div class="card overflow-hidden">
    {#if shares === null || shares.length === 0}
      <State loading={shares === null && !error} {error} empty="No shares in this container yet" icon="folder">
        <button class="btn btn-primary mt-2" onclick={openNew}><Icon name="plus" size={16} />Add share</button>
      </State>
    {:else}
      <div class="overflow-x-auto">
        <table class="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Path</th>
              <th>Access</th>
              <th>Users</th>
              <th class="w-24 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each shares as s}
              <tr>
                <td>
                  <p class="font-medium text-zinc-900 dark:text-zinc-100">{s.name}</p>
                  {#if s.comment}<p class="text-xs text-zinc-500">{s.comment}</p>{/if}
                </td>
                <td class="mono">{s.path}</td>
                <td>{@render accessBadges(s)}</td>
                <td class="text-xs">{s.valid_users.length ? s.valid_users.join(', ') : 'all users'}</td>
                <td class="text-right whitespace-nowrap">
                  <button class="btn-icon" onclick={() => openEdit(s)} title="Edit" aria-label="Edit {s.name}"><Icon name="pencil" size={16} /></button>
                  <button class="btn-icon hover:!text-red-600" onclick={() => remove(s)} title="Delete" aria-label="Delete {s.name}"><Icon name="trash" size={16} /></button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>

  {#if external.length}
    <section class="card mt-6 overflow-hidden">
      <div class="card-header">
        <div>
          <h2 class="card-title">Other shares in this container</h2>
          <p class="mt-0.5 text-xs text-zinc-500">Set up outside LocoStor. Take a share over to edit it here – its file is backed up first.</p>
        </div>
      </div>
      <div class="overflow-x-auto">
        <table class="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Path</th>
              <th>Access</th>
              <th>Defined in</th>
              <th class="text-right"></th>
            </tr>
          </thead>
          <tbody>
            {#each external as x}
              <tr>
                <td>
                  <p class="font-medium text-zinc-900 dark:text-zinc-100">{x.name}</p>
                  {#if x.comment}<p class="text-xs text-zinc-500">{x.comment}</p>{/if}
                </td>
                <td class="mono">{x.path || '–'}</td>
                <td>{@render accessBadges(x)}</td>
                <td class="mono text-xs text-zinc-500">{x.source}</td>
                <td class="text-right whitespace-nowrap">
                  {#if x.adoptable}
                    <button class="btn btn-secondary" onclick={() => adopt(x)}>Take over</button>
                  {:else}
                    <span class="text-xs text-zinc-500" title={x.reason}>{x.reason}</span>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
{/if}

{#if host.info && !host.info.available}
  <p class="mt-4 flex items-start gap-1.5 rounded-lg border border-zinc-200 bg-white/60 px-3 py-2 text-xs text-zinc-600 dark:border-ink-800 dark:bg-ink-900/60 dark:text-zinc-400">
    <Icon name="info" size={14} class="mt-px" />
    <span>
      Shares of the Proxmox host are not shown because the host is not connected. Run the installer once more on the <b>Proxmox host</b> shell
      – it sets up the connection.
    </span>
  </p>
{/if}

{#if scan}
  <details class="mt-3 text-xs text-zinc-500">
    <summary class="cursor-pointer select-none hover:text-zinc-700 dark:hover:text-zinc-300">
      Searched {scan.scanned.length} {scan.scanned.length === 1 ? 'source' : 'sources'}{#if scan.warnings.length}<span class="ml-1 text-amber-600 dark:text-amber-400">· {scan.warnings.length} {scan.warnings.length === 1 ? 'warning' : 'warnings'}</span>{/if}
    </summary>
    <ul class="mono mt-2 space-y-0.5 pl-4">
      {#each scan.scanned as src}<li>{src}</li>{/each}
      {#each scan.warnings as w}<li class="text-amber-600 dark:text-amber-400">{w}</li>{/each}
    </ul>
  </details>
{/if}

<Modal title={editing === null ? `Add SMB share${onHost ? ' on the host' : ''}` : `Edit share "${editing}"`} bind:open={editOpen}>
  <form id="share-form" class="space-y-4" onsubmit={save}>
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label class="label" for="s-name">Name</label>
        <input id="s-name" class="input" bind:value={form.name} required maxlength="80" placeholder="Data" />
      </div>
      <div>
        <label class="label" for="s-comment">Comment</label>
        <input id="s-comment" class="input" bind:value={form.comment} placeholder="optional" />
      </div>
    </div>
    <div>
      <label class="label" for="s-path">Path</label>
      <input id="s-path" class="input mono" bind:value={form.path} required placeholder="/srv/data" />
      <p class="hint">{onHost ? 'Absolute path on the Proxmox host.' : 'Absolute path inside the container, e.g. below the RAID bind mount.'}</p>
    </div>
    <div>
      <label class="label" for="s-users">Allowed users</label>
      <input id="s-users" class="input" bind:value={usersText} placeholder="alice, bob, @group – empty = all users" />
      {#if users.length}
        <div class="mt-1.5 flex flex-wrap gap-1">
          {#each users as u}
            <button
              type="button"
              class="rounded-md bg-zinc-100 px-2 py-0.5 text-xs text-zinc-600 hover:bg-brand-100 hover:text-brand-700 dark:bg-ink-800 dark:text-zinc-400 dark:hover:bg-brand-900/40 dark:hover:text-brand-300"
              onclick={() => addUser(u.name)}>+ {u.name}</button
            >
          {/each}
        </div>
      {/if}
    </div>
    <div class="space-y-3 rounded-lg border border-zinc-200 p-3 dark:border-ink-800">
      <Toggle bind:checked={form.enabled} label="Enabled" />
      <Toggle bind:checked={form.read_only} label="Read only" />
      <Toggle bind:checked={form.browseable} label="Visible in network browser" />
      <Toggle bind:checked={form.guest_ok} label="Allow guest access" hint="Anyone on the network can connect without a password." />
    </div>
    <div>
      <label class="label" for="s-options">More options</label>
      <textarea id="s-options" class="input mono min-h-20" rows="3" bind:value={optionsText} placeholder="force user = nobody&#10;create mask = 0664"></textarea>
      <p class="hint">Any smb.conf share parameter, one <span class="mono">key = value</span> per line.</p>
    </div>
    {#if formError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{formError}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (editOpen = false)}>Cancel</button>
    <button class="btn btn-primary" form="share-form" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Modal>
