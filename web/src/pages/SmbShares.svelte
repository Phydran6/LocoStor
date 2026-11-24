<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Modal from '../components/Modal.svelte';
  import Toggle from '../components/Toggle.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import ScopeTabs from '../components/ScopeTabs.svelte';
  import ShareAccess from '../components/ShareAccess.svelte';
  import SmbNetwork from '../components/SmbNetwork.svelte';
  import { showTask } from '../lib/tasks.svelte.js';
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
  let groups = $state([]);
  let error = $state('');
  let network = $state();

  let onHost = $derived(!!host.info?.available && scope.value === 'host');
  let base = $derived(onHost ? '/api/host/smb' : '/api/smb');
  let apiBase = $derived(onHost ? '/api/host' : '/api');
  // Agents before 0.5.0 do not understand the access settings.
  let hostOutdated = $derived(onHost && !host.info?.features?.includes('share-access'));

  let editOpen = $state(false);
  let editing = $state(null); // original name, null for new
  let form = $state({});
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
    api.get(`${base}/groups`).then((g) => (groups = g)).catch(() => (groups = []));
    network?.reload();
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
    form = { name: '', path: '', comment: '', browseable: true, enabled: true, everyone: 'write', access: [], guest: 'none', fix_permissions: true };
    optionsText = formError = '';
    editOpen = true;
  }

  function openEdit(s) {
    editing = s.name;
    form = {
      name: s.name,
      path: s.path,
      comment: s.comment,
      browseable: s.browseable,
      enabled: s.enabled,
      everyone: s.everyone || (s.valid_users?.length ? 'none' : s.read_only ? 'read' : 'write'),
      access: (s.access ?? []).map((a) => ({ ...a })),
      guest: s.guest || (s.guest_ok ? (s.read_only ? 'read' : 'write') : 'none'),
      fix_permissions: !!s.fix_permissions,
    };
    optionsText = s.options.map((o) => `${o.key} = ${o.value}`).join('\n');
    formError = '';
    editOpen = true;
  }

  function followPermissions(task, name) {
    if (!task) return;
    showTask(task, { base: apiBase, title: `Folder permissions for ${name}`, onDone: load });
  }

  async function save(e) {
    e.preventDefault();
    saving = true;
    formError = '';
    const body = { ...form, options: parseOptions(optionsText) };
    try {
      const res =
        editing === null ? await api.post(`${base}/shares`, body) : await api.put(`${base}/shares/${encodeURIComponent(editing)}`, body);
      toast.success(`Share "${body.name}" saved${onHost ? ' on the host' : ''}`);
      editOpen = false;
      followPermissions(res?.task, body.name);
      load();
    } catch (err) {
      formError = err.message;
    } finally {
      saving = false;
    }
  }

  // Shares whose folder permissions would block writing.
  let blocked = $derived(
    ((onHost ? hostScan?.shares?.filter((x) => x.editable) : shares) ?? []).filter((x) => !x.fix_permissions && x.warnings?.length && x.path),
  );

  async function repairAll() {
    const yes = await confirm({
      title: 'Make shares writable',
      message: `Fix the folder permissions of ${blocked.map((x) => x.name).join(', ')}? The LocoStor group gets read/write rights on the folders; who may read or write is still decided by each share's access settings.`,
      confirmLabel: 'Fix all',
    });
    if (!yes) return;
    for (const x of [...blocked]) await repair(x, false);
  }

  async function repair(s, ask = true) {
    const yes =
      !ask ||
      (await confirm({
        title: 'Fix folder permissions',
        message: `Give the LocoStor group read/write rights on ${s.path} and everything below it? Who may read or write is still decided by the share settings. Owners and existing rights stay; on large folders this takes a while.`,
        confirmLabel: 'Fix permissions',
      }));
    if (!yes) return;
    try {
      if (!s.fix_permissions) {
        // Turn the option on, which also starts the permission task.
        openEdit(s);
        form.fix_permissions = true;
        const res = await api.put(`${base}/shares/${encodeURIComponent(s.name)}`, { ...form, options: parseOptions(optionsText) });
        editOpen = false;
        followPermissions(res?.task, s.name);
      } else {
        const res = await api.post(`${base}/shares/${encodeURIComponent(s.name)}/permissions`);
        followPermissions(res.task, s.name);
      }
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }

  const levelText = { write: 'read & write', read: 'read only', none: 'no access', deny: 'denied' };
  function summary(s) {
    const everyone = s.everyone || (s.valid_users?.length ? 'none' : s.read_only ? 'read' : 'write');
    const acl = s.access ?? s.valid_users?.map((n) => ({ name: n, level: s.read_only ? 'read' : 'write' })) ?? [];
    return { everyone, acl };
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
  {@const sum = summary(s)}
  <div class="flex flex-wrap gap-1">
    {#if sum.everyone !== 'none'}
      <Badge tone={sum.everyone === 'write' ? 'info' : 'muted'}>all users: {levelText[sum.everyone]}</Badge>
    {:else}
      <Badge>only listed users</Badge>
    {/if}
    {#if s.guest && s.guest !== 'none'}<Badge tone="warn">guests: {levelText[s.guest]}</Badge>{:else if !s.guest && s.guest_ok}<Badge tone="warn">guest</Badge>{/if}
    {#if !s.browseable}<Badge>hidden</Badge>{/if}
    {#if !s.enabled}<Badge>disabled</Badge>{/if}
    {#if s.options.length}<Badge>+{s.options.length} {s.options.length === 1 ? 'option' : 'options'}</Badge>{/if}
  </div>
{/snippet}

{#snippet usersCell(s)}
  {@const sum = summary(s)}
  {#if sum.acl.length === 0}
    <span class="text-xs text-zinc-500">{sum.everyone === 'none' ? 'nobody' : 'all users'}</span>
  {:else}
    <ul class="space-y-0.5 text-xs">
      {#each sum.acl as a}
        <li class="whitespace-nowrap">
          <span class="mono">{a.name}</span>
          <span class={a.level === 'deny' ? 'text-red-600 dark:text-red-400' : a.level === 'write' ? 'text-sky-700 dark:text-sky-400' : 'text-zinc-500'}>· {levelText[a.level]}</span>
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

{#snippet warningsRow(s, cols)}
  {#if s.warnings?.length}
    <tr class="!bg-transparent">
      <td colspan={cols} class="!pt-0">
        {#each s.warnings as w}
          <p class="flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-400">
            <Icon name="alert" size={14} class="mt-px" />{w}
            {#if !s.fix_permissions && (s.editable ?? true)}<button class="ml-1 font-medium underline" onclick={() => repair(s)}>Fix now</button>{/if}
          </p>
        {/each}
      </td>
    </tr>
  {/if}
{/snippet}

{#snippet rowActions(s)}
  <button class="btn-icon" onclick={() => openEdit(s)} title="Edit" aria-label="Edit {s.name}"><Icon name="pencil" size={16} /></button>
  <button class="btn-icon" onclick={() => repair(s)} title="Fix folder permissions" aria-label="Fix folder permissions of {s.name}"><Icon name="shield" size={16} /></button>
  <button class="btn-icon hover:!text-red-600" onclick={() => remove(s)} title="Delete" aria-label="Delete {s.name}"><Icon name="trash" size={16} /></button>
{/snippet}

<PageHeader title="SMB shares" description="Windows / macOS network shares served by Samba">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={load}><Icon name="refresh" size={16} />Refresh</button>
    {#if (!onHost || host.info?.samba) && !hostOutdated}
      <button class="btn btn-primary" onclick={openNew}><Icon name="plus" size={16} />Add share{onHost ? ' on host' : ''}</button>
    {/if}
  {/snippet}
</PageHeader>

<ScopeTabs />

{#if hostOutdated}
  <p class="mb-4 flex flex-wrap items-center gap-2 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300">
    <Icon name="alert" size={16} />The agent on the Proxmox host is older than LocoStor ({host.info.version}). Update it to edit access rights and the network name of the host.
    <a href="#/update" class="font-medium underline">Update the host agent</a>
  </p>
{/if}

{#if blocked.length && !hostOutdated}
  <div class="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
    <Icon name="alert" size={18} />
    <span class="flex-1">
      <b>{blocked.length === 1 ? '1 share is' : `${blocked.length} shares are`} probably read only</b> because Linux does not let SMB users write to the
      folder: {blocked.map((x) => x.name).join(', ')}.
    </span>
    <button class="btn btn-primary" onclick={repairAll}><Icon name="shield" size={16} />Make writable</button>
  </div>
{/if}

{#if !hostOutdated && (!onHost || host.info?.samba)}
  <SmbNetwork bind:this={network} base={base} where={onHost ? `the Proxmox host (${host.info.hostname})` : 'this container'} onchange={load} />
{/if}

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
              <th class="w-32 text-right">Actions</th>
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
                <td>{@render usersCell(s)}</td>
                <td class="mono text-xs text-zinc-500">{s.file || 'registry'}</td>
                <td class="text-right whitespace-nowrap">
                  {#if s.editable && !hostOutdated}
                    {@render rowActions(s)}
                  {:else}
                    <span class="text-xs text-zinc-500" title={s.reason}>{s.reason || 'update the host agent'}</span>
                  {/if}
                </td>
              </tr>
              {@render warningsRow(s, 6)}
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
              <th class="w-32 text-right">Actions</th>
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
                <td>{@render usersCell(s)}</td>
                <td class="text-right whitespace-nowrap">{@render rowActions(s)}</td>
              </tr>
              {@render warningsRow(s, 5)}
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

<Modal title={editing === null ? `Add SMB share${onHost ? ' on the host' : ''}` : `Edit share "${editing}"`} bind:open={editOpen} wide>
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
      <p class="label">Access</p>
      <ShareAccess bind:everyone={form.everyone} bind:access={form.access} bind:guest={form.guest} {users} {groups} />
      {#if users.length === 0}
        <p class="hint">No SMB users on {onHost ? 'the host' : 'this container'} yet – create them under <a class="underline" href="#/smb/users">SMB users</a>.</p>
      {/if}
    </div>
    <div class="space-y-3 rounded-lg border border-zinc-200 p-3 dark:border-ink-800">
      <Toggle
        bind:checked={form.fix_permissions}
        label="Fix folder permissions"
        hint="Recommended. The folder gets permissions for the LocoStor group, so Linux never blocks what is allowed above (otherwise a root-owned folder stays read only)."
      />
      <Toggle bind:checked={form.enabled} label="Enabled" />
      <Toggle bind:checked={form.browseable} label="Visible in network browser" />
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
