<script>
  // Kernel NFS exports of the Proxmox host (/etc/exports), edited in place.
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import Badge from './Badge.svelte';
  import Modal from './Modal.svelte';
  import State from './State.svelte';
  import { api } from '../lib/api.svelte.js';
  import { toast, confirm } from '../lib/ui.svelte.js';
  import { tone } from '../lib/format.js';
  import { host } from '../lib/host.svelte.js';

  let scan = $state(null);
  let error = $state('');

  let editOpen = $state(false);
  let editingKey = $state(''); // '' = new
  let form = $state({});
  let clientsText = $state('');
  let optionsText = $state('');
  let saving = $state(false);
  let formError = $state('');

  const squashLabels = { root_squash: 'root squash', no_root_squash: 'no root squash', all_squash: 'all squash' };

  async function load() {
    try {
      scan = await api.get('/api/host/nfs/exports');
      error = '';
    } catch (e) {
      error = e.message;
    }
  }
  onMount(load);

  function openNew() {
    editingKey = '';
    form = { path: '', access: 'RW', squash: 'root_squash' };
    clientsText = '';
    optionsText = 'sync, no_subtree_check';
    formError = '';
    editOpen = true;
  }

  function openEdit(x) {
    editingKey = x.key;
    form = { path: x.path, access: x.access, squash: x.squash };
    clientsText = x.clients.join(', ');
    optionsText = x.options.join(', ');
    formError = '';
    editOpen = true;
  }

  const split = (t) => t.split(/[\s,]+/).filter(Boolean);

  async function save(e) {
    e.preventDefault();
    saving = true;
    formError = '';
    const exp = { ...form, clients: split(clientsText), options: split(optionsText) };
    try {
      await api.post('/api/host/nfs/exports', { key: editingKey, export: exp });
      toast.success(`Export ${exp.path} saved on the host`);
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
      message: `Remove ${x.path} from ${x.file} on the host? Clients using it lose access. A backup of the file is kept; files are not touched.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!yes) return;
    try {
      await api.post('/api/host/nfs/exports/delete', { key: x.key });
      toast.success(`Export ${x.path} deleted`);
      load();
    } catch (err) {
      toast.error(err.message);
    }
  }
</script>

<section class="card overflow-hidden">
  <div class="card-header">
    <div>
      <h2 class="card-title">NFS server on {host.info.hostname}</h2>
      <p class="mt-0.5 text-xs text-zinc-500">
        Kernel NFS exports, edited directly in /etc/exports – manual changes on the host keep working. A backup is kept before every change.
      </p>
    </div>
    <div class="flex items-center gap-2">
      {#if host.info.services?.['nfs-server']}<Badge tone={tone(host.info.services['nfs-server'])} dot>nfs-server {host.info.services['nfs-server']}</Badge>{/if}
      <button class="btn-icon" onclick={load} title="Refresh" aria-label="Refresh"><Icon name="refresh" size={16} /></button>
      {#if host.info.nfs}
        <button class="btn btn-primary" onclick={openNew}><Icon name="plus" size={16} />Add export on host</button>
      {/if}
    </div>
  </div>
  {#if !host.info.nfs}
    <State empty="The kernel NFS server (nfs-kernel-server) is not installed on the Proxmox host." icon="network" />
  {:else if scan === null || scan.exports.length === 0}
    <State loading={scan === null && !error} {error} empty="No NFS exports on the host yet" icon="network" />
  {:else}
    <div class="overflow-x-auto">
      <table class="table">
        <thead>
          <tr>
            <th>Path</th>
            <th>Clients</th>
            <th>Options</th>
            <th>Defined in</th>
            <th class="w-24 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each scan.exports as x}
            <tr>
              <td class="mono font-medium text-zinc-900 dark:text-zinc-100">{x.path}</td>
              <td class="mono text-xs">{x.clients.length ? x.clients.join(', ') : '* (everyone)'}</td>
              <td>
                <div class="flex flex-wrap gap-1">
                  <Badge tone={x.access === 'RW' ? 'info' : 'muted'}>{x.access === 'RW' ? 'read/write' : 'read only'}</Badge>
                  <Badge>{squashLabels[x.squash] ?? x.squash}</Badge>
                  {#each x.options as o}<Badge>{o}</Badge>{/each}
                </div>
              </td>
              <td class="mono text-xs text-zinc-500">{x.file}</td>
              <td class="text-right whitespace-nowrap">
                {#if x.editable}
                  <button class="btn-icon" onclick={() => openEdit(x)} title="Edit" aria-label="Edit {x.path}"><Icon name="pencil" size={16} /></button>
                  <button class="btn-icon hover:!text-red-600" onclick={() => remove(x)} title="Delete" aria-label="Delete {x.path}"><Icon name="trash" size={16} /></button>
                {:else}
                  <span class="text-xs text-zinc-500">{x.reason}</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

{#if scan}
  <details class="mt-3 text-xs text-zinc-500">
    <summary class="cursor-pointer select-none hover:text-zinc-700 dark:hover:text-zinc-300">
      Searched {scan.scanned.length} {scan.scanned.length === 1 ? 'file' : 'files'}{#if scan.warnings.length}<span class="ml-1 text-amber-600 dark:text-amber-400">· {scan.warnings.length} {scan.warnings.length === 1 ? 'warning' : 'warnings'}</span>{/if}
    </summary>
    <ul class="mono mt-2 space-y-0.5 pl-4">
      {#each scan.scanned as src}<li>{src}</li>{/each}
      {#each scan.warnings as w}<li class="text-amber-600 dark:text-amber-400">{w}</li>{/each}
    </ul>
  </details>
{/if}

<Modal title={editingKey ? `Edit export ${form.path}` : 'Add NFS export on the host'} bind:open={editOpen}>
  <form id="host-export-form" class="space-y-4" onsubmit={save}>
    <div>
      <label class="label" for="h-path">Path on the host</label>
      <input id="h-path" class="input mono" bind:value={form.path} required placeholder="/srv/data" />
    </div>
    <div>
      <label class="label" for="h-clients">Allowed clients</label>
      <input id="h-clients" class="input mono" bind:value={clientsText} placeholder="192.168.1.0/24, 10.0.0.5 – empty = everyone" />
    </div>
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label class="label" for="h-access">Access</label>
        <select id="h-access" class="input" bind:value={form.access}>
          <option value="RW">Read / write</option>
          <option value="RO">Read only</option>
        </select>
      </div>
      <div>
        <label class="label" for="h-squash">Squash</label>
        <select id="h-squash" class="input" bind:value={form.squash}>
          <option value="root_squash">Root squash (recommended)</option>
          <option value="no_root_squash">No root squash</option>
          <option value="all_squash">All squash</option>
        </select>
      </div>
    </div>
    <div>
      <label class="label" for="h-options">More options</label>
      <input id="h-options" class="input mono" bind:value={optionsText} placeholder="sync, no_subtree_check" />
      <p class="hint">Other exports(5) options, separated by commas – e.g. sync, no_subtree_check, insecure, fsid=1.</p>
    </div>
    {#if formError}<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400"><Icon name="alert" size={16} />{formError}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (editOpen = false)}>Cancel</button>
    <button class="btn btn-primary" form="host-export-form" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Modal>
