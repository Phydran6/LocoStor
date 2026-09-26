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

  let exports = $state(null);
  let external = $state([]);
  let error = $state('');

  let editOpen = $state(false);
  let editing = $state(0);
  let form = $state({});
  let clientsText = $state('');
  let v3 = $state(false);
  let v4 = $state(true);
  let saving = $state(false);
  let formError = $state('');

  const squashLabels = { root_squash: 'root squash', no_root_squash: 'no root squash', all_squash: 'all squash' };
  const host = location.hostname;

  async function load() {
    try {
      [exports, external] = await Promise.all([api.get('/api/nfs/exports'), api.get('/api/nfs/external')]);
      error = '';
    } catch (e) {
      error = e.message;
    }
  }

  async function adopt(x) {
    const yes = await confirm({
      title: 'Take over export',
      message: `Manage ${x.pseudo} with LocoStor? It is removed from ${x.source} (a backup is kept) and served by NFS-Ganesha from now on.`,
      confirmLabel: 'Take over',
    });
    if (!yes) return;
    try {
      await api.post('/api/nfs/external/adopt', { key: x.key });
      toast.success(`${x.pseudo} is now managed by LocoStor`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }

  onMount(load);

  function openNew() {
    editing = 0;
    form = { path: '', pseudo: '', access: 'RW', squash: 'root_squash', comment: '', enabled: true };
    clientsText = '';
    v3 = false;
    v4 = true;
    formError = '';
    editOpen = true;
  }

  function openEdit(x) {
    editing = x.id;
    form = { ...x };
    clientsText = x.clients.join(', ');
    v3 = x.protocols.includes(3);
    v4 = x.protocols.includes(4);
    formError = '';
    editOpen = true;
  }

  async function save(e) {
    e.preventDefault();
    const protocols = [v3 && 3, v4 && 4].filter(Boolean);
    if (!protocols.length) {
      formError = 'Select at least one NFS version';
      return;
    }
    saving = true;
    formError = '';
    const body = { ...form, protocols, clients: clientsText.split(/[\s,]+/).filter(Boolean) };
    delete body.id;
    try {
      if (editing) await api.put(`/api/nfs/exports/${editing}`, body);
      else await api.post('/api/nfs/exports', body);
      toast.success(`Export ${body.pseudo || body.path} saved`);
      editOpen = false;
      load();
    } catch (err) {
      formError = err.message;
    } finally {
      saving = false;
    }
  }

  async function remove(x) {
    const yes = await confirm({
      title: 'Delete export',
      message: `Delete the NFS export ${x.pseudo}? Clients using it lose access. Files are not touched.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!yes) return;
    try {
      await api.del(`/api/nfs/exports/${x.id}`);
      toast.success(`Export ${x.pseudo} deleted`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }
</script>

<PageHeader title="NFS exports" description="Linux / Unix network shares served by NFS-Ganesha">
  {#snippet actions()}
    <button class="btn btn-secondary" onclick={load}><Icon name="refresh" size={16} />Refresh</button>
    <button class="btn btn-primary" onclick={openNew}><Icon name="plus" size={16} />Add export</button>
  {/snippet}
</PageHeader>

<div class="card overflow-hidden">
  {#if exports === null || exports.length === 0}
    <State loading={exports === null && !error} {error} empty="No exports yet" icon="network">
      <button class="btn btn-primary mt-2" onclick={openNew}><Icon name="plus" size={16} />Add export</button>
    </State>
  {:else}
    <div class="overflow-x-auto">
      <table class="table">
        <thead>
          <tr>
            <th>Export</th>
            <th>Path</th>
            <th>Clients</th>
            <th>Options</th>
            <th>Status</th>
            <th class="w-24 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each exports as x}
            <tr>
              <td>
                <p class="mono font-medium text-zinc-900 dark:text-zinc-100">{x.pseudo}</p>
                {#if x.comment}<p class="text-xs text-zinc-500">{x.comment}</p>{/if}
              </td>
              <td class="mono">{x.path}</td>
              <td class="mono text-xs">{x.clients.length ? x.clients.join(', ') : '* (everyone)'}</td>
              <td>
                <div class="flex flex-wrap gap-1">
                  <Badge tone={x.access === 'RW' ? 'info' : 'muted'}>{x.access === 'RW' ? 'read/write' : 'read only'}</Badge>
                  <Badge>{squashLabels[x.squash]}</Badge>
                  <Badge>v{x.protocols.join(', v')}</Badge>
                </div>
              </td>
              <td><Badge tone={x.enabled ? 'ok' : 'muted'} dot>{x.enabled ? 'enabled' : 'disabled'}</Badge></td>
              <td class="text-right whitespace-nowrap">
                <button class="btn-icon" onclick={() => openEdit(x)} title="Edit" aria-label="Edit {x.pseudo}"><Icon name="pencil" size={16} /></button>
                <button class="btn-icon hover:!text-red-600" onclick={() => remove(x)} title="Delete" aria-label="Delete {x.pseudo}"><Icon name="trash" size={16} /></button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

{#if exports?.length}
  <div class="mt-4 rounded-lg border border-zinc-200 bg-white/60 px-4 py-3 text-sm text-zinc-600 dark:border-ink-800 dark:bg-zinc-900/60 dark:text-zinc-400">
    <p class="mb-1 flex items-center gap-1.5 font-medium text-zinc-700 dark:text-zinc-300"><Icon name="info" size={16} />Mount on a client</p>
    <code class="mono block overflow-x-auto whitespace-nowrap">mount -t nfs4 {host}:{exports[0].pseudo} /mnt/{exports[0].pseudo.split('/').pop()}</code>
  </div>
{/if}

{#if external.length}
  <section class="card mt-6 overflow-hidden">
    <div class="card-header">
      <div>
        <h2 class="card-title">Other exports on this system</h2>
        <p class="mt-0.5 text-xs text-zinc-500">Set up outside LocoStor. Take an export over to edit it here – its file is backed up first.</p>
      </div>
    </div>
    <div class="overflow-x-auto">
      <table class="table">
        <thead>
          <tr>
            <th>Export</th>
            <th>Path</th>
            <th>Clients</th>
            <th>Defined in</th>
            <th class="text-right"></th>
          </tr>
        </thead>
        <tbody>
          {#each external as x}
            <tr>
              <td class="mono font-medium text-zinc-900 dark:text-zinc-100">{x.pseudo}</td>
              <td class="mono">{x.path}</td>
              <td class="mono text-xs">{x.clients.length ? x.clients.join(', ') : '*'}</td>
              <td class="mono text-xs text-zinc-500">{x.source}</td>
              <td class="text-right">
                {#if x.adoptable}
                  <button class="btn btn-secondary" onclick={() => adopt(x)}>Take over</button>
                {:else}
                  <span class="inline-block max-w-64 text-left text-xs text-zinc-500">{x.reason}</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </section>
{/if}

<Modal title={editing ? `Edit export ${form.pseudo}` : 'Add NFS export'} bind:open={editOpen}>
  <form id="export-form" class="space-y-4" onsubmit={save}>
    <div>
      <label class="label" for="n-path">Path</label>
      <input id="n-path" class="input mono" bind:value={form.path} required placeholder="/mnt/raid/data" />
    </div>
    <div>
      <label class="label" for="n-pseudo">Export path (NFSv4 pseudo path)</label>
      <input id="n-pseudo" class="input mono" bind:value={form.pseudo} placeholder="/data – defaults to the path" />
      <p class="hint">The path clients mount, e.g. <span class="mono">server:/data</span>.</p>
    </div>
    <div>
      <label class="label" for="n-clients">Allowed clients</label>
      <input id="n-clients" class="input mono" bind:value={clientsText} placeholder="192.168.1.0/24, 10.0.0.5 – empty = everyone" />
    </div>
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label class="label" for="n-access">Access</label>
        <select id="n-access" class="input" bind:value={form.access}>
          <option value="RW">Read / write</option>
          <option value="RO">Read only</option>
        </select>
      </div>
      <div>
        <label class="label" for="n-squash">Squash</label>
        <select id="n-squash" class="input" bind:value={form.squash}>
          <option value="root_squash">Root squash (recommended)</option>
          <option value="no_root_squash">No root squash</option>
          <option value="all_squash">All squash</option>
        </select>
      </div>
    </div>
    <fieldset>
      <legend class="label">NFS versions</legend>
      <div class="flex gap-5 text-sm">
        <label class="flex items-center gap-2"><input type="checkbox" class="accent-brand-600" bind:checked={v4} /> NFSv4</label>
        <label class="flex items-center gap-2"><input type="checkbox" class="accent-brand-600" bind:checked={v3} /> NFSv3</label>
      </div>
    </fieldset>
    <div>
      <label class="label" for="n-comment">Comment</label>
      <input id="n-comment" class="input" bind:value={form.comment} placeholder="optional" />
    </div>
    <div class="rounded-lg border border-zinc-200 p-3 dark:border-ink-800">
      <Toggle bind:checked={form.enabled} label="Enabled" />
    </div>
    {#if formError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{formError}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (editOpen = false)}>Cancel</button>
    <button class="btn btn-primary" form="export-form" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Modal>
