<script>
  import { onMount } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Badge from '../components/Badge.svelte';
  import Modal from '../components/Modal.svelte';
  import Toggle from '../components/Toggle.svelte';
  import State from '../components/State.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm } from '../lib/ui.svelte.js';

  let shares = $state(null);
  let users = $state([]);
  let error = $state('');

  let editOpen = $state(false);
  let editing = $state(null); // original name, null for new
  let form = $state({});
  let usersText = $state('');
  let saving = $state(false);
  let formError = $state('');

  async function load() {
    try {
      shares = await api.get('/api/smb/shares');
      error = '';
    } catch (e) {
      error = e.message;
    }
  }

  onMount(() => {
    load();
    api.get('/api/smb/users').then((u) => (users = u)).catch(() => {});
  });

  function openNew() {
    editing = null;
    form = { name: '', path: '', comment: '', read_only: false, browseable: true, guest_ok: false, enabled: true };
    usersText = '';
    formError = '';
    editOpen = true;
  }

  function openEdit(s) {
    editing = s.name;
    form = { ...s };
    usersText = s.valid_users.join(', ');
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
    const body = { ...form, valid_users: usersText.split(/[\s,]+/).filter(Boolean) };
    try {
      if (editing === null) await api.post('/api/smb/shares', body);
      else await api.put(`/api/smb/shares/${encodeURIComponent(editing)}`, body);
      toast.success(`Share "${body.name}" saved`);
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
      message: `Delete the share "${s.name}"? The files in ${s.path} are not touched.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!yes) return;
    try {
      await api.del(`/api/smb/shares/${encodeURIComponent(s.name)}`);
      toast.success(`Share "${s.name}" deleted`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }
</script>

<PageHeader title="SMB shares" description="Windows / macOS network shares served by Samba">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={load}><Icon name="refresh" size={16} />Refresh</button>
    <button class="btn btn-primary" onclick={openNew}><Icon name="plus" size={16} />Add share</button>
  {/snippet}
</PageHeader>

<div class="card overflow-hidden">
  {#if shares === null || shares.length === 0}
    <State loading={shares === null && !error} {error} empty="No shares yet" icon="folder">
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
            <th>Status</th>
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
              <td>
                <div class="flex flex-wrap gap-1">
                  <Badge tone={s.read_only ? 'muted' : 'info'}>{s.read_only ? 'read only' : 'read/write'}</Badge>
                  {#if s.guest_ok}<Badge tone="warn">guest</Badge>{/if}
                  {#if !s.browseable}<Badge>hidden</Badge>{/if}
                </div>
              </td>
              <td class="text-xs">{s.valid_users.length ? s.valid_users.join(', ') : 'all users'}</td>
              <td><Badge tone={s.enabled ? 'ok' : 'muted'} dot>{s.enabled ? 'enabled' : 'disabled'}</Badge></td>
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

<Modal title={editing === null ? 'Add SMB share' : `Edit share "${editing}"`} bind:open={editOpen}>
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
      <input id="s-path" class="input mono" bind:value={form.path} required placeholder="/mnt/raid/data" />
      <p class="hint">Absolute path inside the container, e.g. below the RAID bind mount.</p>
    </div>
    <div>
      <label class="label" for="s-users">Allowed users</label>
      <input id="s-users" class="input" bind:value={usersText} placeholder="alice, bob, @group – empty = all users" />
      {#if users.length}
        <div class="mt-1.5 flex flex-wrap gap-1">
          {#each users as u}
            <button type="button" class="rounded-md bg-zinc-100 px-2 py-0.5 text-xs text-zinc-600 hover:bg-sky-100 hover:text-sky-700 dark:bg-zinc-800 dark:text-zinc-400 dark:hover:bg-sky-900/40 dark:hover:text-sky-300" onclick={() => addUser(u.name)}>+ {u.name}</button>
          {/each}
        </div>
      {/if}
    </div>
    <div class="space-y-3 rounded-lg border border-zinc-200 p-3 dark:border-zinc-800">
      <Toggle bind:checked={form.enabled} label="Enabled" />
      <Toggle bind:checked={form.read_only} label="Read only" />
      <Toggle bind:checked={form.browseable} label="Visible in network browser" />
      <Toggle bind:checked={form.guest_ok} label="Allow guest access" hint="Anyone on the network can connect without a password." />
    </div>
    {#if formError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{formError}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (editOpen = false)}>Cancel</button>
    <button class="btn btn-primary" form="share-form" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Modal>
